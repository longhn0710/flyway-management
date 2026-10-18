package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flyway-manager/internal/artifact"
	"strings"
)

type ExternalRunInput struct {
	ArtifactID               string         `json:"artifact_id"`
	PreflightID              string         `json:"preflight_id,omitempty"`
	DeploymentTargetRevision int            `json:"deployment_target_revision"`
	ExternalSource           ExternalSource `json:"external_source,omitempty"`
}
type externalAdmission struct {
	Project, Deployment    string
	Revision               int
	Preflight, RequestHash string
	Source                 ExternalSource
}

func (x externalAdmission) runInput(d DeploymentTarget, aid string) RunInput {
	return RunInput{TargetID: d.TargetID, SchemaID: d.SchemaID, ExecutionAccountID: d.ExecutionAccountID, ArtifactID: aid, external: &x}
}
func validKey(key string) error {
	if !validText(key, 200) {
		return invalid("Idempotency-Key", "A nonempty key of at most 200 bytes is required.")
	}
	return nil
}
func (s *Service) checkAdmission(ctx context.Context, q queryer, a Actor, in RunInput, action string) error {
	if in.external == nil {
		return legacyStreamAllowed(ctx, q, in.TargetID, in.SchemaID)
	}
	x := in.external
	d, e := s.deploymentAccess(ctx, q, a, x.Project, x.Deployment, action)
	if e != nil {
		return e
	}
	if action != "read" && d.Revision != x.Revision {
		return conflict("Deployment Target revision is stale.")
	}
	if d.TargetID != in.TargetID || d.SchemaID != in.SchemaID || d.ExecutionAccountID != in.ExecutionAccountID {
		return denied()
	}
	return nil
}
func (s *Service) externalArtifactAccess(ctx context.Context, q queryer, a Actor, aid string) error {
	var p, d string
	e := q.QueryRowContext(ctx, `SELECT project_id,deployment_target_id FROM project_artifacts WHERE artifact_id=?`, aid).Scan(&p, &d)
	if e == sql.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	_, e = s.deploymentAccess(ctx, q, a, p, d, "read")
	return e
}
func (s *Service) ExternalArtifact(ctx context.Context, a Actor, p, aid string) (map[string]any, error) {
	var project, d, hash, source string
	var rev int
	e := s.db.QueryRowContext(ctx, `SELECT project_id,deployment_target_id,revision,input_sha256,external_source FROM project_artifacts WHERE artifact_id=? AND project_id=?`, aid, p).Scan(&project, &d, &rev, &hash, &source)
	if e != nil {
		return nil, translate(e)
	}
	if _, e = s.DeploymentTarget(ctx, a, p, d, "read"); e != nil {
		return nil, e
	}
	x, e := s.Artifact(ctx, a, aid)
	if e != nil {
		return nil, e
	}
	return map[string]any{"id": x.ID, "project_id": project, "deployment_target_id": d, "deployment_target_revision": rev, "input_sha256": hash, "sha256": x.SHA256, "files": x.Files, "inputs": x.Inputs, "total_bytes": x.TotalBytes, "created_at": x.CreatedAt, "external_source": json.RawMessage(source), "uploader_id": x.UploaderID, "uploader_kind": x.UploaderKind, "uploader_name": x.UploaderName}, nil
}
func (s *Service) UploadExternalArtifact(ctx context.Context, t Trace, p, d string, revision int, checksum, key string, source ExternalSource, input artifact.Input) (map[string]any, error) {
	if e := validKey(key); e != nil {
		return nil, e
	}
	if e := source.validate(); e != nil {
		return nil, e
	}
	dest, e := s.DeploymentTarget(ctx, t.Actor, p, d, "read")
	if e != nil {
		return nil, e
	}
	if !strings.HasSuffix(strings.ToLower(input.Name), ".zip") || len(input.Bytes) < 4 || !bytes.Equal(input.Bytes[:4], []byte{'P', 'K', 3, 4}) {
		return nil, &Error{415, "ZIP_REQUIRED", "Upload exactly one SQL ZIP package.", "files"}
	}
	if len(checksum) != 64 || checksum != digest(string(input.Bytes)) {
		return nil, invalid("input_sha256", "Original ZIP SHA-256 does not match.")
	}
	req, _ := json.Marshal([]any{revision, checksum, input.Name, source})
	requestHash := digest(string(req))
	var aid, oldhash string
	e = s.db.QueryRowContext(ctx, `SELECT artifact_id,request_hash FROM project_artifacts WHERE actor_kind=? AND actor_id=? AND project_id=? AND deployment_target_id=? AND idempotency_key=?`, t.Actor.Kind, t.Actor.ID, p, d, key).Scan(&aid, &oldhash)
	if e == nil {
		if oldhash != requestHash {
			return nil, conflict("Idempotency key was already used with a different upload.")
		}
		return s.ExternalArtifact(ctx, t.Actor, p, aid)
	}
	if e != sql.ErrNoRows {
		return nil, e
	}
	if _, e = s.DeploymentTarget(ctx, t.Actor, p, d, "upload"); e != nil {
		return nil, e
	}
	if dest.Revision != revision {
		return nil, conflict("Deployment Target revision is stale.")
	}
	adm := &artifactAdmission{externalAdmission: externalAdmission{Project: p, Deployment: d, Revision: revision, Source: source}, Key: key, Checksum: checksum, RequestHash: requestHash}
	x, e := s.uploadArtifact(ctx, t, dest.TargetID, dest.SchemaID, []artifact.Input{input}, adm)
	if e != nil {
		if ce, ok := e.(*Error); ok && ce.Status == 400 && ce.Field == "files" {
			return nil, &Error{422, "INVALID_SQL_PACKAGE", ce.Message, ce.Field}
		}
		return nil, e
	}
	return s.ExternalArtifact(ctx, t.Actor, p, x.ID)
}

type artifactAdmission struct {
	externalAdmission
	Key, Checksum, RequestHash string
}

func (s *Service) persistArtifactAdmission(ctx context.Context, tx *sql.Tx, t Trace, aid string, in *artifactAdmission) error {
	source, _ := json.Marshal(in.Source)
	_, e := tx.ExecContext(ctx, `INSERT INTO project_artifacts VALUES(?,?,?,?,?,?,?,?,?,?)`, aid, in.Project, in.Deployment, in.Revision, in.Checksum, string(source), t.Actor.Kind, t.Actor.ID, in.Key, in.RequestHash)
	return e
}
func (s *Service) externalFingerprint(ctx context.Context, q queryer, d DeploymentTarget, aid string) (string, error) {
	in := RunInput{TargetID: d.TargetID, SchemaID: d.SchemaID, ExecutionAccountID: d.ExecutionAccountID, ArtifactID: aid}
	snap, password, e := s.executionSnapshot(ctx, q, in)
	clear(password)
	if e != nil {
		return "", e
	}
	files, e := s.applicableFiles(ctx, q, in)
	if e != nil {
		return "", e
	}
	var held bool
	var config, pin string
	e = q.QueryRowContext(ctx, `SELECT held,configuration,runtime_pin FROM migration_streams WHERE target_id=? AND schema_id=?`, d.TargetID, d.SchemaID).Scan(&held, &config, &pin)
	if e != nil && e != sql.ErrNoRows {
		return "", e
	}
	fs := []any{}
	for _, f := range files {
		fs = append(fs, []any{f.Path, f.SHA256})
	}
	runtimePin := ""
	if s.execution != nil {
		runtimePin = s.execution.pin
	}
	b, _ := json.Marshal([]any{snap, fs, held, config, pin, runtimePin})
	return digest(string(b)), nil
}
func (s *Service) ExternalPreflight(ctx context.Context, t Trace, p, d string, in ExternalRunInput) (map[string]any, error) {
	if e := in.ExternalSource.validate(); e != nil {
		return nil, e
	}
	dest, e := s.DeploymentTarget(ctx, t.Actor, p, d, "preflight")
	if e != nil {
		return nil, e
	}
	if dest.Revision != in.DeploymentTargetRevision {
		return nil, conflict("Deployment Target revision is stale.")
	}
	if _, e = s.artifactForDeployment(ctx, s.db, t.Actor, p, d, in.ArtifactID); e != nil {
		return nil, e
	}
	fingerprint, e := s.externalFingerprint(ctx, s.db, dest, in.ArtifactID)
	if e != nil {
		return nil, e
	}
	adm := externalAdmission{Project: p, Deployment: d, Revision: dest.Revision}
	assessment, e := s.Preflight(ctx, t, adm.runInput(dest, in.ArtifactID))
	if e != nil {
		return nil, e
	}
	fid := id()
	created := now()
	out := map[string]any{"id": fid, "project_id": p, "deployment_target_id": d, "deployment_target_revision": dest.Revision, "artifact_id": in.ArtifactID, "destination": dest, "created_at": stamp(created), "expires_at": stamp(created + 600), "ready": assessment.Ready, "live_status": assessment.LiveStatus, "blockers": assessment.Blockers, "warnings": assessment.Warnings, "assessment": assessment, "external_source": in.ExternalSource, "actor": map[string]string{"id": t.Actor.ID, "kind": t.Actor.Kind, "name": t.Actor.Name}}
	payload, _ := json.Marshal(out)
	e = s.mutation(ctx, t, "project.preflight", fid, func(tx *sql.Tx) error {
		current, e := s.deploymentAccess(ctx, tx, t.Actor, p, d, "preflight")
		if e != nil {
			return e
		}
		if current.Revision != dest.Revision {
			return conflict("Deployment Target changed during preflight.")
		}
		f, e := s.externalFingerprint(ctx, tx, current, in.ArtifactID)
		if e != nil {
			return e
		}
		if f != fingerprint {
			return conflict("Destination or stream changed during preflight; assess again.")
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO migration_preflights VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, fid, p, d, in.ArtifactID, dest.Revision, t.Actor.Kind, t.Actor.ID, fingerprint, assessment.Ready, created, created+600, string(payload))
		return e
	})
	return out, e
}
func (s *Service) artifactForDeployment(ctx context.Context, q queryer, a Actor, p, d, aid string) (string, error) {
	var found string
	e := q.QueryRowContext(ctx, `SELECT artifact_id FROM project_artifacts WHERE project_id=? AND deployment_target_id=? AND artifact_id=?`, p, d, aid).Scan(&found)
	return found, translate(e)
}
func (s *Service) GetExternalPreflight(ctx context.Context, a Actor, p, f string) (map[string]any, error) {
	var d, payload string
	e := s.db.QueryRowContext(ctx, `SELECT deployment_target_id,assessment FROM migration_preflights WHERE id=? AND project_id=?`, f, p).Scan(&d, &payload)
	if e != nil {
		return nil, translate(e)
	}
	if _, e = s.DeploymentTarget(ctx, a, p, d, "read"); e != nil {
		return nil, e
	}
	var out map[string]any
	e = json.Unmarshal([]byte(payload), &out)
	return out, e
}
func (s *Service) validateExternalPreflight(ctx context.Context, q queryer, a Actor, in RunInput) error {
	x := in.external
	if x == nil {
		return nil
	}
	if e := s.checkAdmission(ctx, q, a, in, "execute"); e != nil {
		return e
	}
	if _, e := s.artifactForDeployment(ctx, q, a, x.Project, x.Deployment, in.ArtifactID); e != nil {
		return e
	}
	var fingerprint string
	var ready bool
	var expiry int64
	e := q.QueryRowContext(ctx, `SELECT fingerprint,ready,expires_at FROM migration_preflights WHERE id=? AND project_id=? AND deployment_target_id=? AND artifact_id=? AND revision=? AND actor_kind=? AND actor_id=?`, x.Preflight, x.Project, x.Deployment, in.ArtifactID, x.Revision, a.Kind, a.ID).Scan(&fingerprint, &ready, &expiry)
	if e != nil {
		return translate(e)
	}
	if !ready || expiry <= now() {
		return conflict("Preflight is blocked or expired; assess again.")
	}
	dest, e := deploymentFrom(ctx, q, x.Project, x.Deployment)
	if e != nil {
		return e
	}
	current, e := s.externalFingerprint(ctx, q, dest, in.ArtifactID)
	if e != nil {
		return e
	}
	if current != fingerprint {
		return conflict("Destination or migration stream changed after preflight.")
	}
	return nil
}
func (s *Service) SubmitExternalRun(ctx context.Context, t Trace, p, d string, in ExternalRunInput, key string) (map[string]any, error) {
	if e := validKey(key); e != nil {
		return nil, e
	}
	if e := in.ExternalSource.validate(); e != nil {
		return nil, e
	}
	if in.PreflightID == "" {
		return nil, invalid("preflight_id", "A durable ready Preflight ID is required.")
	}
	dest, e := s.DeploymentTarget(ctx, t.Actor, p, d, "read")
	if e != nil {
		return nil, e
	}
	req, _ := json.Marshal(in)
	adm := externalAdmission{Project: p, Deployment: d, Revision: in.DeploymentTargetRevision, Preflight: in.PreflightID, RequestHash: digest(string(req)), Source: in.ExternalSource}
	// Namespace the existing durable key; original request hash is also frozen in attribution.
	scopedKey := "v2:" + digest(p+"/"+d+"/"+key)
	var rid, hash string
	e = s.db.QueryRowContext(ctx, `SELECT i.run_id,p.request_hash FROM idempotency_records i JOIN project_migration_runs p ON p.run_id=i.run_id WHERE i.actor_kind=? AND i.actor_id=? AND i.key=? AND p.project_id=? AND p.deployment_target_id=?`, t.Actor.Kind, t.Actor.ID, scopedKey, p, d).Scan(&rid, &hash)
	if e == nil {
		if hash != adm.RequestHash {
			return nil, conflict("Idempotency key was already used with a different request.")
		}
		return s.ExternalRun(ctx, t.Actor, p, rid)
	}
	if e != sql.ErrNoRows {
		return nil, e
	}
	run, e := s.SubmitRun(ctx, t, adm.runInput(dest, in.ArtifactID), scopedKey)
	if e != nil {
		return nil, e
	}
	return s.ExternalRun(ctx, t.Actor, p, run.ID)
}
func (s *Service) persistRunAdmission(ctx context.Context, tx *sql.Tx, rid string, in RunInput) error {
	if in.external == nil {
		return nil
	}
	x := in.external
	source, _ := json.Marshal(x.Source)
	_, e := tx.ExecContext(ctx, `INSERT INTO project_migration_runs VALUES(?,?,?,?,?,?,?)`, rid, x.Project, x.Deployment, x.Revision, x.Preflight, string(source), x.RequestHash)
	return e
}
func (s *Service) runProjectAccess(ctx context.Context, q queryer, a Actor, rid string, run *Run) error {
	var p, d, source string
	var rev int
	e := q.QueryRowContext(ctx, `SELECT project_id,deployment_target_id,revision,external_source FROM project_migration_runs WHERE run_id=?`, rid).Scan(&p, &d, &rev, &source)
	if e == sql.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	if _, e = s.deploymentAccess(ctx, q, a, p, d, "read"); e != nil {
		return e
	}
	run.ProjectID = p
	run.DeploymentTargetID = d
	run.DeploymentTargetRevision = rev
	_ = json.Unmarshal([]byte(source), &run.ExternalSource)
	var claimed bool
	if e = q.QueryRowContext(ctx, `SELECT claimed FROM jobs WHERE run_id=?`, rid).Scan(&claimed); e != nil {
		return e
	}
	run.Completed = terminal(run.Status) && !claimed && run.RepairOutcome != "PENDING"
	var n int
	if e = q.QueryRowContext(ctx, `SELECT count(*) FROM execution_records WHERE run_id=?`, rid).Scan(&n); e != nil {
		return e
	}
	run.RecordAvailable = n == 1
	return nil
}
func (s *Service) ExternalRun(ctx context.Context, a Actor, p, r string) (map[string]any, error) {
	var n int
	if e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM project_migration_runs WHERE project_id=? AND run_id=?`, p, r).Scan(&n); e != nil {
		return nil, e
	}
	if n != 1 {
		return nil, missing()
	}
	x, e := s.Run(ctx, a, r)
	if e != nil {
		return nil, e
	}
	b, _ := json.Marshal(x)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	url := "/api/v2/projects/" + p + "/migration-runs/" + r
	out["status_url"] = url
	out["logs_url"] = url + "/logs"
	out["record_url"] = url + "/record"
	return out, nil
}
func (s *Service) ExternalLogs(ctx context.Context, a Actor, p, r string, page Page) ([]RunLog, error) {
	if _, e := s.ExternalRun(ctx, a, p, r); e != nil {
		return nil, e
	}
	return s.RunLogs(ctx, a, r, page, "")
}
func (s *Service) ExternalRecord(ctx context.Context, a Actor, p, r string) (json.RawMessage, error) {
	if _, e := s.ExternalRun(ctx, a, p, r); e != nil {
		return nil, e
	}
	var payload, hash string
	e := s.db.QueryRowContext(ctx, `SELECT payload,sha256 FROM execution_records WHERE run_id=?`, r).Scan(&payload, &hash)
	if e == sql.ErrNoRows {
		return nil, &Error{409, "RECORD_NOT_READY", "Execution record is not finalized.", ""}
	}
	if e != nil {
		return nil, e
	}
	if digest(payload) != hash {
		return nil, conflict("Execution record integrity failure.")
	}
	return json.RawMessage(payload), nil
}
