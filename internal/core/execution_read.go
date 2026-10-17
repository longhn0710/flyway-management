package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"flyway-manager/internal/flyway"
	"fmt"
	"strings"
	"time"
)

func (s *Service) Runtime(ctx context.Context) flyway.Readiness {
	if s.execution == nil {
		return flyway.Readiness{Ready: false, Blockers: []string{"Flyway runtime is not configured."}}
	}
	return s.execution.runner.Check(ctx)
}
func (s *Service) ExecutionEnabled() bool { return s.execution != nil }
func (s *Service) StreamAccounts(ctx context.Context, a Actor, target, schema string) ([]ExecutionAccount, error) {
	if e := s.authorizeExecution(ctx, s.db, a, target, schema); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT a.id,a.target_id,a.name,a.username,a.active,a.created_at,c.version,c.updated_at,c.revoked FROM execution_accounts a JOIN account_schema_grants g ON g.account_id=a.id LEFT JOIN credential_secrets c ON c.account_id=a.id WHERE a.target_id=? AND g.schema_id=? AND a.active=1 AND `+allowedAccountSQL+` ORDER BY a.name,a.id`, target, schema)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ExecutionAccount{}
	for rows.Next() {
		var x ExecutionAccount
		var ts int64
		var version, updated, revoked sql.NullInt64
		if e = rows.Scan(&x.ID, &x.TargetID, &x.Name, &x.Username, &x.Active, &ts, &version, &updated, &revoked); e != nil {
			return nil, e
		}
		x.CreatedAt = stamp(ts)
		x.SchemaIDs = []string{schema}
		x.Credential = CredentialMetadata{Configured: version.Valid && revoked.Int64 == 0, Version: int(version.Int64), Revoked: revoked.Int64 != 0}
		if updated.Valid {
			x.Credential.UpdatedAt = stamp(updated.Int64)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

const runColumns = `id,status,request,snapshot,actor_id,actor_kind,actor_name,source,created_at,started_at,finished_at,flyway_version,exit_code,migration_outcome,repair_outcome,reason,preflight,(SELECT GROUP_CONCAT(name, ', ') FROM (SELECT name FROM artifact_blobs b WHERE b.artifact_id=migration_runs.artifact_id ORDER BY input_index))`

func runScan(row interface{ Scan(...any) error }) (Run, error) {
	var x Run
	var request, snapshot string
	var created int64
	var started, finished, exit sql.NullInt64
	var pre sql.NullString
	e := row.Scan(&x.ID, &x.Status, &request, &snapshot, &x.ActorID, &x.ActorKind, &x.ActorName, &x.Source, &created, &started, &finished, &x.FlywayVersion, &exit, &x.MigrationOutcome, &x.RepairOutcome, &x.Reason, &pre, &x.ArtifactName)
	if e != nil {
		return x, translate(e)
	}
	if e = json.Unmarshal([]byte(request), &x.RunInput); e != nil {
		return x, e
	}
	if e = json.Unmarshal([]byte(snapshot), &x.RunSnapshot); e != nil {
		return x, e
	}
	x.RunID = x.ID
	x.StatusURL = "/api/v1/migration-runs/" + x.ID
	x.LogsURL = x.StatusURL + "/logs"
	x.CreatedAt = stamp(created)
	if started.Valid {
		x.StartedAt = stamp(started.Int64)
	}
	if finished.Valid {
		x.FinishedAt = stamp(finished.Int64)
	}
	if exit.Valid {
		v := int(exit.Int64)
		x.ExitCode = &v
	}
	if pre.Valid {
		e = json.Unmarshal([]byte(pre.String), &x.Preflight)
	}
	return x, e
}
func (s *Service) Run(ctx context.Context, a Actor, rid string) (Run, error) {
	x, e := runScan(s.db.QueryRowContext(ctx, "SELECT "+runColumns+" FROM migration_runs WHERE id=?", rid))
	if e != nil {
		return x, e
	}
	if e = s.Authorize(ctx, a, "read", x.TargetID, x.SchemaID); e != nil {
		return Run{}, e
	}
	return x, nil
}
func (s *Service) Runs(ctx context.Context, a Actor, f RunFilter) ([]Run, error) {
	for _, v := range []string{f.From, f.To} {
		if v != "" {
			if _, e := time.Parse(time.RFC3339, v); e != nil {
				return nil, invalid("date", "Dates must be RFC3339.")
			}
		}
	}
	from, to := int64(0), int64(1<<62)
	if f.From != "" {
		v, _ := time.Parse(time.RFC3339, f.From)
		from = v.Unix()
	}
	if f.To != "" {
		v, _ := time.Parse(time.RFC3339, f.To)
		to = v.Unix()
	}
	gs, e := s.Grants(ctx, a.Kind, a.ID)
	if e != nil {
		return nil, e
	}
	type scope struct {
		environment string
		active      bool
	}
	scopes := map[string]scope{}
	registry, e := s.db.QueryContext(ctx, `SELECT t.id,s.id,t.environment,t.active AND s.active FROM database_targets t JOIN target_schemas s ON s.target_id=t.id`)
	if e != nil {
		return nil, e
	}
	for registry.Next() {
		var tid, sid, environment string
		var active bool
		if e = registry.Scan(&tid, &sid, &environment, &active); e != nil {
			registry.Close()
			return nil, e
		}
		scopes[tid+"/"+sid] = scope{environment, active}
	}
	e = registry.Err()
	registry.Close()
	if e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT `+runColumns+` FROM migration_runs WHERE (?='' OR status=?) AND (?='' OR target_id=?) AND (?='' OR schema_id=?) AND (?='' OR actor_name LIKE ? OR actor_id=?) AND (?='' OR source=?) AND created_at>=? AND created_at<=? AND (id LIKE ? OR actor_name LIKE ? OR snapshot LIKE ? OR EXISTS(SELECT 1 FROM artifact_blobs b WHERE b.artifact_id=migration_runs.artifact_id AND b.name LIKE ?)) ORDER BY created_at DESC,sequence DESC`, f.Status, f.Status, f.TargetID, f.TargetID, f.SchemaID, f.SchemaID, f.Actor, "%"+f.Actor+"%", f.Actor, f.Source, f.Source, from, to, "%"+f.Query+"%", "%"+f.Query+"%", "%"+f.Query+"%", "%"+f.Query+"%")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Run{}
	l, o := f.bounds()
	seen := 0
	for rows.Next() {
		x, e := runScan(rows)
		if e != nil {
			return nil, e
		}
		scope, ok := scopes[x.TargetID+"/"+x.SchemaID]
		if !ok || (!scope.active && !a.Admin()) || !Allowed(a, "read", x.TargetID, x.SchemaID, scope.environment, gs) {
			continue
		}
		if seen >= o && len(out) < l {
			out = append(out, x)
		}
		seen++
	}
	return out, rows.Err()
}
func (s *Service) RunLogs(ctx context.Context, a Actor, rid string, p Page, channel string) ([]RunLog, error) {
	if _, e := s.Run(ctx, a, rid); e != nil {
		return nil, e
	}
	l, o := p.bounds()
	rows, e := s.db.QueryContext(ctx, `SELECT sequence,command,channel,message,created_at FROM migration_logs WHERE run_id=? AND (?='' OR message LIKE ?) AND (?='' OR channel=?) ORDER BY sequence LIMIT ? OFFSET ?`, rid, p.Query, "%"+p.Query+"%", channel, channel, l, o)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []RunLog{}
	for rows.Next() {
		var x RunLog
		if e = rows.Scan(&x.Sequence, &x.Command, &x.Channel, &x.Message, &x.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) RunArtifacts(ctx context.Context, a Actor, rid string) ([]Artifact, error) {
	if _, e := s.Run(ctx, a, rid); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT DISTINCT a.manifest FROM artifacts a JOIN migration_run_items i ON i.artifact_id=a.id WHERE i.run_id=? ORDER BY a.id`, rid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Artifact{}
	for rows.Next() {
		var payload string
		if e = rows.Scan(&payload); e != nil {
			return nil, e
		}
		var x Artifact
		if e = json.Unmarshal([]byte(payload), &x); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// RunFiles returns the frozen effective file set for a run. Artifact metadata
// comes from immutable manifests while membership comes from migration_run_items.
func (s *Service) RunFiles(ctx context.Context, a Actor, rid string) ([]RunFile, error) {
	if _, e := s.Run(ctx, a, rid); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT i.artifact_id,i.path,COALESCE(f.sha256,''),COALESCE(f.kind,''),COALESCE(f.version,''),COALESCE(length(f.content),-1)
		FROM migration_run_items i LEFT JOIN migration_files f ON f.artifact_id=i.artifact_id AND f.path=i.path
		WHERE i.run_id=? ORDER BY i.path,i.artifact_id`, rid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []RunFile{}
	for rows.Next() {
		var x RunFile
		if e = rows.Scan(&x.ArtifactID, &x.Path, &x.SHA256, &x.Kind, &x.Version, &x.Size); e != nil {
			return nil, e
		}
		if x.Size < 0 {
			return nil, conflict("A file reserved by this run is unavailable.")
		}
		out = append(out, x)
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return nil, e
	}
	if e = rows.Close(); e != nil {
		return nil, e
	}
	manifests := map[string]Artifact{}
	for _, x := range out {
		manifest, ok := manifests[x.ArtifactID]
		if !ok {
			manifest, e = s.Artifact(ctx, a, x.ArtifactID)
			if e != nil {
				return nil, e
			}
			manifests[x.ArtifactID] = manifest
		}
		found := false
		for _, mf := range manifest.Files {
			if mf.Path == x.Path && mf.SHA256 == x.SHA256 && mf.Kind == x.Kind && mf.Version == x.Version && mf.Size == x.Size {
				found = true
				break
			}
		}
		if !found {
			return nil, conflict("A file reserved by this run does not match its immutable manifest.")
		}
	}
	return out, nil
}

// RunFileBytes binds content reads to both the authorized run and its frozen
// artifact/path membership, then checks stored bytes against the frozen digest.
func (s *Service) RunFileBytes(ctx context.Context, a Actor, rid, aid, path string) (string, []byte, error) {
	if _, e := s.Run(ctx, a, rid); e != nil {
		return "", nil, e
	}
	var stored, payload, frozen string
	var size int
	e := s.db.QueryRowContext(ctx, `SELECT COALESCE(f.sha256,''),COALESCE(length(f.content),-1),a.manifest FROM migration_run_items i LEFT JOIN migration_files f ON f.artifact_id=i.artifact_id AND f.path=i.path JOIN artifacts a ON a.id=i.artifact_id WHERE i.run_id=? AND i.artifact_id=? AND i.path=?`, rid, aid, path).Scan(&stored, &size, &payload)
	if e != nil {
		return "", nil, translate(e)
	}
	if size < 0 {
		return "", nil, conflict("A file reserved by this run is unavailable.")
	}
	if int64(size) > s.artifactLimits().MaxFileBytes {
		return "", nil, conflict("The reserved file exceeds the configured read limit.")
	}
	var manifest Artifact
	if e = json.Unmarshal([]byte(payload), &manifest); e != nil {
		return "", nil, e
	}
	for _, f := range manifest.Files {
		if f.Path == path {
			frozen = f.SHA256
			if f.Size != size {
				return "", nil, conflict("A file reserved by this run does not match its immutable manifest.")
			}
			break
		}
	}
	if frozen == "" || stored != frozen {
		return "", nil, conflict("A file reserved by this run does not match its immutable manifest.")
	}
	name, content, e := s.ArtifactBytes(ctx, a, aid, path, 0, false)
	if e != nil {
		return "", nil, e
	}
	if digest(string(content)) != frozen {
		return "", nil, fmt.Errorf("artifact integrity failure")
	}
	return name, content, nil
}
func (s *Service) RunExecutions(ctx context.Context, a Actor, rid string) ([]RunExecution, error) {
	if _, e := s.Run(ctx, a, rid); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT id,command,success,exit_code,stdout,stderr,result,started_at,finished_at,ambiguous FROM run_executions WHERE run_id=? ORDER BY started_at,id`, rid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []RunExecution{}
	for rows.Next() {
		var x RunExecution
		var payload string
		if e = rows.Scan(&x.ID, &x.Command, &x.Success, &x.ExitCode, &x.Stdout, &x.Stderr, &payload, &x.StartedAt, &x.FinishedAt, &x.Ambiguous); e != nil {
			return nil, e
		}
		if json.Valid([]byte(payload)) {
			x.JSON = json.RawMessage(payload)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) RunRepair(ctx context.Context, a Actor, rid string) (Repair, error) {
	if _, e := s.Run(ctx, a, rid); e != nil {
		return Repair{}, e
	}
	var x Repair
	var before, after string
	e := s.db.QueryRowContext(ctx, `SELECT outcome,reason,before_history,after_history,execution_id FROM repair_operations WHERE run_id=?`, rid).Scan(&x.Outcome, &x.Reason, &before, &after, &x.ExecutionID)
	if e == sql.ErrNoRows {
		return Repair{Outcome: "SKIPPED", Reason: "No repair operation has completed.", Before: json.RawMessage("null"), After: json.RawMessage("null")}, nil
	}
	if e != nil {
		return x, e
	}
	x.Before = json.RawMessage(before)
	x.After = json.RawMessage(after)
	return x, nil
}
func terminal(status string) bool {
	return strings.Contains(" SUCCEEDED FAILED BLOCKED NEEDS_REVIEW CANCELLED ", " "+status+" ")
}
