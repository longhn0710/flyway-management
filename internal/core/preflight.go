package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"flyway-manager/internal/artifact"
	"flyway-manager/internal/flyway"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

type storedFile struct {
	ArtifactID string
	artifact.File
}
type executionPlan struct {
	snapshot      RunSnapshot
	files         []storedFile
	password      []byte
	configuration string
}

func streamConfiguration(snap RunSnapshot) string {
	payload, _ := json.Marshal([]any{snap.Target.Host, snap.Target.Port, snap.Target.Service, snap.Target.Transport, snap.Target.Environment, snap.Schema.Name, snap.Schema.HistorySchema, snap.Schema.HistoryTable, snap.FlywayConfiguration})
	return string(payload)
}
func (s *Service) executionSnapshot(ctx context.Context, q queryer, in RunInput) (RunSnapshot, []byte, error) {
	var snap RunSnapshot
	var e error
	snap.Target, e = targetScan(q.QueryRowContext(ctx, "SELECT "+targetColumns+" FROM database_targets WHERE id=?", in.TargetID))
	if e != nil {
		return snap, nil, e
	}
	snap.Schema, e = schemaScan(q.QueryRowContext(ctx, "SELECT "+schemaColumns+" FROM target_schemas WHERE id=? AND target_id=?", in.SchemaID, in.TargetID))
	if e != nil {
		return snap, nil, e
	}
	var active, revoked bool
	var ciphertext []byte
	e = q.QueryRowContext(ctx, `SELECT a.id,a.name,a.username,a.active,c.version,c.payload,c.revoked FROM execution_accounts a JOIN credential_secrets c ON c.account_id=a.id JOIN account_schema_grants g ON g.account_id=a.id AND g.schema_id=? WHERE a.id=? AND a.target_id=?`, in.SchemaID, in.ExecutionAccountID, in.TargetID).Scan(&snap.Account.ID, &snap.Account.Name, &snap.Account.Username, &active, &snap.Account.CredentialVersion, &ciphertext, &revoked)
	if e == sql.ErrNoRows {
		return snap, nil, conflict("Execution account must be active, mapped and have a configured credential.")
	}
	if e != nil {
		return snap, nil, e
	}
	if !snap.Target.Active || !snap.Schema.Active || !active || revoked {
		return snap, nil, conflict("Target, schema, execution account and credential must be active.")
	}
	plain, e := s.vault.Open("credential:"+snap.Account.ID, ciphertext)
	if e != nil {
		return snap, nil, conflict("Oracle credential cannot be decrypted.")
	}
	if len(plain) == 0 {
		clear(plain)
		return snap, nil, conflict("Oracle credential is empty.")
	}
	snap.FlywayConfiguration = flyway.ConfigurationSnapshot(requestFor(executionPlan{snapshot: snap}, "info"))
	return snap, plain, nil
}
func migrationKey(f artifact.File) string {
	if f.Kind == "versioned" {
		return "V:" + f.Version
	}
	return "R:" + strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(path.Base(f.Path), "R__"), ".sql"))
}
func readStoredFiles(ctx context.Context, q queryer, query string, args ...any) ([]storedFile, error) {
	rows, e := q.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []storedFile{}
	for rows.Next() {
		var f storedFile
		if e = rows.Scan(&f.ArtifactID, &f.Path, &f.SHA256, &f.Kind, &f.Version, &f.Bytes); e != nil {
			return nil, e
		}
		if digest(string(f.Bytes)) != f.SHA256 {
			return nil, conflict("Stored migration checksum does not match immutable content.")
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (s *Service) applicableFiles(ctx context.Context, q queryer, in RunInput) ([]storedFile, error) {
	var target, schema string
	if e := q.QueryRowContext(ctx, "SELECT target_id,schema_id FROM artifacts WHERE id=?", in.ArtifactID).Scan(&target, &schema); e != nil {
		return nil, translate(e)
	}
	if target != in.TargetID || schema != in.SchemaID {
		return nil, denied()
	}
	prior, e := readStoredFiles(ctx, q, `SELECT f.artifact_id,f.path,f.sha256,f.kind,f.version,f.content FROM stream_files r JOIN migration_files f ON f.artifact_id=r.artifact_id AND f.path=r.path WHERE r.target_id=? AND r.schema_id=?`, in.TargetID, in.SchemaID)
	if e != nil {
		return nil, e
	}
	incoming, e := readStoredFiles(ctx, q, `SELECT artifact_id,path,sha256,kind,version,content FROM migration_files WHERE artifact_id=?`, in.ArtifactID)
	if e != nil {
		return nil, e
	}
	set := map[string]storedFile{}
	for _, f := range prior {
		set[migrationKey(f.File)] = f
	}
	for _, f := range incoming {
		k := migrationKey(f.File)
		if old, ok := set[k]; ok && f.Kind == "versioned" && (old.SHA256 != f.SHA256 || old.Path != f.Path) {
			return nil, conflict("A versioned migration already reserved by this stream differs in content or path.")
		}
		set[k] = f
	}
	out := make([]storedFile, 0, len(set))
	inputs := make([]artifact.Input, 0, len(set))
	for _, f := range set {
		out = append(out, f)
		inputs = append(inputs, artifact.Input{Name: f.Path, Bytes: f.Bytes})
	}
	limits := s.artifactLimits()
	limits.MaxUploadBytes = limits.MaxExpandedBytes
	if _, e = artifact.Normalize(inputs, limits); e != nil {
		return nil, conflict("Complete migration locations are invalid or exceed configured stream limits: " + e.Error())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
func (s *Service) localPlan(ctx context.Context, q queryer, a Actor, in RunInput, version string, checkBusy bool) (executionPlan, error) {
	var plan executionPlan
	if in.TargetID == "" || in.SchemaID == "" || in.ExecutionAccountID == "" || in.ArtifactID == "" {
		return plan, invalid("run", "Target, schema, account and immutable artifact are required.")
	}
	if len(in.Context.ExternalRunID) > 500 || len(in.Context.CommitSHA) > 200 || len(in.Context.InitiatedBy) > 500 {
		return plan, invalid("context", "External context is too long.")
	}
	if e := s.authorizeExecution(ctx, q, a, in.TargetID, in.SchemaID); e != nil {
		return plan, e
	}
	snap, password, e := s.executionSnapshot(ctx, q, in)
	if e != nil {
		return plan, e
	}
	plan.snapshot = snap
	plan.password = password
	plan.configuration = streamConfiguration(snap)
	fail := func(e error) (executionPlan, error) { clear(password); return executionPlan{}, e }
	// Until Oracle CURRENT_SCHEMA behavior is accepted, separate history schemas are intentionally blocked.
	if snap.Schema.HistorySchema != snap.Schema.Name {
		return fail(conflict("Dedicated history-schema execution is NOT_VERIFIED; use the target schema history location."))
	}
	var config, pin, reason string
	var held bool
	e = q.QueryRowContext(ctx, `SELECT configuration,runtime_pin,held,reason FROM migration_streams WHERE target_id=? AND schema_id=?`, in.TargetID, in.SchemaID).Scan(&config, &pin, &held, &reason)
	if e != nil && e != sql.ErrNoRows {
		return fail(e)
	}
	if e == nil {
		if held {
			return fail(conflict("Stream requires reconciliation: " + reason))
		}
		if config != plan.configuration || pin != s.execution.pin {
			return fail(conflict("Stream configuration or pinned Flyway runtime changed. Existing streams never switch silently."))
		}
	}
	if checkBusy {
		var n int
		if e = q.QueryRowContext(ctx, `SELECT count(*) FROM migration_runs r JOIN jobs j ON j.run_id=r.id WHERE r.target_id=? AND r.schema_id=? AND (r.status IN ('QUEUED','PREFLIGHT','RUNNING','REPAIRING') OR j.claimed=1)`, in.TargetID, in.SchemaID).Scan(&n); e != nil {
			return fail(e)
		}
		if n > 0 {
			return fail(conflict("Stream has a queued or active migration job."))
		}
	}
	plan.files, e = s.applicableFiles(ctx, q, in)
	if e != nil {
		return fail(e)
	}
	return plan, nil
}
func requestFor(plan executionPlan, command string) flyway.Request {
	snap := plan.snapshot
	files := map[string][]byte{}
	for _, f := range plan.files {
		files[f.Path] = f.Bytes
	}
	return flyway.Request{Command: command, Host: snap.Target.Host, Port: snap.Target.Port, Service: snap.Target.Service, Transport: snap.Target.Transport, Schema: snap.Schema.Name, HistorySchema: snap.Schema.HistorySchema, HistoryTable: snap.Schema.HistoryTable, Username: snap.Account.Username, Password: string(plan.password), Files: files}
}
func fillPlan(p *Preflight, plan executionPlan) {
	for _, f := range plan.files {
		p.Files = append(p.Files, ArtifactFile{f.Path, f.SHA256, f.Kind, f.Version, len(f.Bytes)})
	}
	p.LocalReady = true
}
func (s *Service) Preflight(ctx context.Context, t Trace, in RunInput) (Preflight, error) {
	p := preflightBase()
	if e := s.Authorize(ctx, t.Actor, "operate", in.TargetID, in.SchemaID); e != nil {
		return p, e
	}
	if s.execution == nil {
		p.block("RUNTIME_UNAVAILABLE", "Flyway runtime is not configured.")
		return p, nil
	}
	lock := s.execution.stream(in.TargetID, in.SchemaID)
	if !lock.TryLock() {
		p.block("STREAM_BUSY", "Stream is executing or validating another request.")
		return p, nil
	}
	defer lock.Unlock()
	ready := s.Runtime(ctx)
	p.RuntimeVersion = ready.Version
	if !ready.Ready {
		for _, b := range ready.Blockers {
			p.block("RUNTIME_UNAVAILABLE", b)
		}
		if len(p.Blockers) == 0 {
			p.block("RUNTIME_UNAVAILABLE", "Pinned Flyway runtime is not ready.")
		}
		return p, nil
	}
	plan, e := s.localPlan(ctx, s.db, t.Actor, in, ready.Version, true)
	if e != nil {
		var message string
		if ce, ok := e.(*Error); ok {
			message = ce.Message
		} else {
			return p, e
		}
		p.block("LOCAL_VALIDATION_FAILED", message)
		return p, nil
	}
	defer clear(plan.password)
	fillPlan(&p, plan)
	info := s.execution.runner.Execute(ctx, requestFor(plan, "info"))
	validate := flyway.Result{}
	if info.Success {
		validate = s.execution.runner.Execute(ctx, requestFor(plan, "validate"))
	}
	applyLivePreflight(&p, info, validate)
	if p.Ready {
		if e = resolvedSnapshot(plan, info.JSON); e != nil {
			p.block("INCOMPLETE_FLYWAY_LOCATIONS", safeExecutionError(e))
		}
	}
	if p.Ready {
		if e = s.noImplicitRetry(ctx, Run{RunInput: in}, plan, info.JSON); e != nil {
			p.block("IMPLICIT_SQL_RETRY", safeExecutionError(e))
		}
	}
	if ctx.Err() != nil {
		p.Ready = false
		p.LiveStatus = "NOT_VERIFIED"
		p.block("REQUEST_TIMEOUT", "Preflight timed out while inspecting Oracle. No migration was started.")
		auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if e = s.Event(auditCtx, t, "migration.preflight", in.SchemaID, p.LiveStatus); e != nil {
			return p, e
		}
		return p, nil
	}
	if e = s.Event(ctx, t, "migration.preflight", in.SchemaID, p.LiveStatus); e != nil {
		return p, e
	}
	return p, nil
}

type historyEntry struct {
	Category      string  `json:"category"`
	Version       *string `json:"version"`
	Description   string  `json:"description"`
	Type          string  `json:"type"`
	Script        string  `json:"script"`
	State         string  `json:"state"`
	InstalledOn   string  `json:"installedOn"`
	InstalledBy   string  `json:"installedBy"`
	InstalledRank int     `json:"installedRank"`
	Checksum      *int64  `json:"checksum,omitempty"`
}

// Flyway info uses filepath/installedOnUTC. Older adapter fixtures may expose
// script/installedOn; normalize both to a stable script name while retaining
// original JSON evidence. Ephemeral absolute workspace prefixes are not identity.
func (h *historyEntry) UnmarshalJSON(data []byte) error {
	type entry historyEntry
	var raw struct {
		entry
		Filepath       string `json:"filepath"`
		InstalledOnUTC string `json:"installedOnUTC"`
	}
	if e := json.Unmarshal(data, &raw); e != nil {
		return e
	}
	*h = historyEntry(raw.entry)
	if h.Script == "" {
		h.Script = raw.Filepath
	}
	if h.Script != "" {
		h.Script = path.Base(strings.ReplaceAll(h.Script, "\\", "/"))
	}
	if h.InstalledOn == "" {
		h.InstalledOn = raw.InstalledOnUTC
	}
	return nil
}

func history(payload json.RawMessage) ([]historyEntry, error) {
	var x struct {
		Migrations *[]historyEntry `json:"migrations"`
	}
	if e := json.Unmarshal(payload, &x); e != nil || x.Migrations == nil {
		return nil, fmt.Errorf("Flyway history evidence is missing")
	}
	return *x.Migrations, nil
}
func safeHistory(payload json.RawMessage) error {
	rows, e := history(payload)
	if e != nil {
		return e
	}
	for _, r := range rows {
		switch strings.ToLower(r.State) {
		case "success", "pending", "outdated", "superseded":
		default:
			return fmt.Errorf("Flyway history has unsupported or unsafe state %q for %s", r.State, r.Script)
		}
	}
	return nil
}
func applyLivePreflight(p *Preflight, info, validation flyway.Result) {
	p.Info = info.JSON
	p.Validation = validation.JSON
	for _, name := range []string{"network", "authentication", "service", "schema_access", "history"} {
		p.Diagnostics = append(p.Diagnostics, Diagnostic{name, "NOT_VERIFIED", "Flyway has not verified this condition."})
	}
	p.Diagnostics = append(p.Diagnostics, Diagnostic{"migration_privileges", "NOT_VERIFIED", "Read-only validation cannot prove all DDL privileges or cross-schema isolation."})
	if !info.Success {
		connectivityFailure(p, info)
		return
	}
	for i := 0; i < 3; i++ {
		p.Diagnostics[i].Status = "VERIFIED"
		p.Diagnostics[i].Detail = "Flyway info contacted the configured Oracle service using the selected account."
	}
	p.Diagnostics[3].Detail = "Flyway info/validate inspect the configured stream; read-only inspection cannot prove every schema DDL privilege."
	p.Diagnostics[4].Status = "INSPECTED"
	p.Diagnostics[4].Detail = "Flyway inspected schema history. An empty history does not prove the physical history table already exists."
	p.Diagnostics = append(p.Diagnostics, Diagnostic{"database_identity", "NOT_VERIFIED", "The configured service was contacted; database DBID and actual service identity require an approved Oracle diagnostic adapter/lab."})
	if e := safeHistory(info.JSON); e != nil {
		p.LiveStatus = "FAILED"
		p.block("HISTORY_UNSAFE", e.Error())
		return
	}
	if !validation.Success {
		p.LiveStatus = "FAILED"
		p.block("FLYWAY_VALIDATION_FAILED", "Flyway validation failed; migration will not start.")
		return
	}
	p.LiveStatus = "VERIFIED"
	p.Ready = p.LocalReady && len(p.Blockers) == 0
}

// Historical files reconstruct Flyway locations, but a historical pending SQL
// file must be explicitly present in this upload. Otherwise a cancelled/failed
// predecessor could accidentally be retried by uploading an unrelated Vn.
func (s *Service) noImplicitRetry(ctx context.Context, run Run, plan executionPlan, info json.RawMessage) error {
	rows, e := history(info)
	if e != nil {
		return e
	}
	incoming, e := readStoredFiles(ctx, s.db, `SELECT artifact_id,path,sha256,kind,version,content FROM migration_files WHERE artifact_id=?`, run.ArtifactID)
	if e != nil {
		return e
	}
	submitted := map[string]bool{}
	for _, f := range incoming {
		submitted[f.Path] = true
	}
	for _, r := range rows {
		if strings.EqualFold(r.State, "Pending") && r.Version != nil && *r.Version != "" {
			found := ""
			for _, f := range plan.files {
				if f.Kind == "versioned" && (r.Script == f.Path || r.Script == path.Base(f.Path)) {
					if found != "" {
						return conflict("Ambiguous Flyway script resolution.")
					}
					found = f.Path
				}
			}
			if found == "" || !submitted[found] {
				return conflict("A pending historical version was not explicitly included in this upload; automatic SQL retry is refused.")
			}
		}
	}
	return nil
}
func allVersionsApplied(info json.RawMessage) bool {
	rows, e := history(info)
	if e != nil {
		return false
	}
	for _, r := range rows {
		if r.Version != nil && *r.Version != "" && strings.EqualFold(r.State, "Pending") {
			return false
		}
	}
	return true
}

// A valid CLI response is insufficient if its configured locations resolve no
// uploaded files. Every frozen SQL file must appear in Flyway's resolver view.
func resolvedSnapshot(plan executionPlan, info json.RawMessage) error {
	rows, e := history(info)
	if e != nil {
		return e
	}
	for _, f := range plan.files {
		matches := 0
		for _, row := range rows {
			if row.Script == f.Path || row.Script == path.Base(f.Path) {
				matches++
			}
		}
		if matches == 0 {
			return conflict("Flyway did not resolve every immutable migration file; execution locations are not verified.")
		}
	}
	return nil
}

func connectivityFailure(p *Preflight, result flyway.Result) {
	text := strings.ToUpper(result.Stdout + "\n" + result.Stderr + "\n" + string(result.JSON))
	set := func(name, status, detail string) {
		for i := range p.Diagnostics {
			if p.Diagnostics[i].Name == name {
				p.Diagnostics[i].Status = status
				p.Diagnostics[i].Detail = detail
			}
		}
	}
	switch {
	case strings.Contains(text, "ORA-01017"), strings.Contains(text, "ORA-28000"), strings.Contains(text, "ORA-28001"):
		set("network", "VERIFIED", "Oracle returned an authentication response.")
		set("authentication", "FAILED", "Oracle rejected the credential, or the account is locked/expired.")
		p.LiveStatus = "FAILED"
		p.block("ORACLE_AUTHENTICATION_FAILED", "Oracle authentication failed; no migration was started.")
	case strings.Contains(text, "ORA-12514"), strings.Contains(text, "ORA-12505"):
		set("network", "VERIFIED", "Oracle listener responded.")
		set("service", "FAILED", "The listener did not recognize the configured service/PDB.")
		p.LiveStatus = "FAILED"
		p.block("ORACLE_SERVICE_UNAVAILABLE", "Oracle listener could not resolve the configured service/PDB.")
	case strings.Contains(text, "ORA-12541"), strings.Contains(text, "ORA-12154"), strings.Contains(text, "ORA-12170"), strings.Contains(text, "CONNECTION REFUSED"), strings.Contains(text, "UNKNOWNHOSTEXCEPTION"):
		set("network", "FAILED", "Network, name resolution, or Oracle listener connectivity failed.")
		p.LiveStatus = "FAILED"
		p.block("ORACLE_NETWORK_FAILED", "Oracle network/listener connectivity failed.")
	case strings.Contains(text, "ORA-01031"):
		set("migration_privileges", "FAILED", "Oracle reported insufficient privileges for the requested diagnostic.")
		p.LiveStatus = "FAILED"
		p.block("ORACLE_PRIVILEGES_INSUFFICIENT", "Oracle denied a required history/schema diagnostic privilege.")
	case strings.Contains(text, "ORA-00942"):
		set("history", "FAILED", "A required table is absent or inaccessible; Oracle does not distinguish these cases through this error.")
		p.LiveStatus = "FAILED"
		p.block("ORACLE_HISTORY_UNAVAILABLE", "Oracle history inspection reported an absent or inaccessible table.")
	default:
		p.LiveStatus = "NOT_VERIFIED"
		p.block("ORACLE_NOT_VERIFIED", "Flyway info did not verify Oracle connectivity/history. Inspect the authenticated diagnostics.")
	}
}
