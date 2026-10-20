package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flyway-manager/internal/flyway"
	"flyway-manager/internal/oracle"
	"hash/crc32"
	"path"
	"reflect"
	"regexp"
	"strings"
)

// This context is private: external callers cannot enable the review-only
// exception through ordinary preflight/submission JSON.
type correctionReview struct{ FailedRunID string }
type correctionApproval struct {
	ID, FailedRunID, ProjectID, DeploymentID, ApprovedBy, Fingerprint string
	ExpiresAt                                                         int64
	Evidence                                                          oracle.Evidence
}

type CorrectionReviewInput struct {
	FailedRunID              string `json:"failed_run_id"`
	ArtifactID               string `json:"artifact_id"`
	DeploymentTargetRevision int    `json:"deployment_target_revision"`
}
type CorrectionApprovalInput struct {
	PreflightID        string `json:"preflight_id"`
	InputSHA256        string `json:"input_sha256"`
	PartialDDLReviewed bool   `json:"partial_ddl_reviewed"`
	Note               string `json:"note"`
}

func (s *Service) oracleEvidence(ctx context.Context, plan executionPlan) (oracle.Evidence, error) {
	if err := ctx.Err(); err != nil {
		return oracle.Evidence{}, err
	}

	adapter, ok := s.execution.runner.(interface {
		OracleEvidence(context.Context, flyway.Request) (oracle.Evidence, error)
	})
	if !ok {
		return oracle.Evidence{}, &Error{409, "ORACLE_EVIDENCE_UNAVAILABLE", "Read-only Oracle identity and physical history evidence is unavailable.", ""}
	}
	e, err := adapter.OracleEvidence(ctx, requestFor(plan, "info"))
	if ctx.Err() != nil {
		return e, ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return e, err
	}
	if err != nil {
		code := regexp.MustCompile(`ORA-[0-9]{5}`).FindString(err.Error())
		return e, &Error{409, "ORACLE_EVIDENCE_UNAVAILABLE", "Oracle identity, object catalogue or physical history could not be verified. " + code, ""}
	}
	if e.DBID == "" || e.DatabaseUniqueName == "" || e.PDBName == "" || e.Service == "" || e.SessionUser != plan.snapshot.Account.Username || e.CurrentSchema != plan.snapshot.Schema.Name || e.ObjectsSHA256 == "" || e.History == nil {
		return e, &Error{409, "ORACLE_EVIDENCE_UNAVAILABLE", "Oracle evidence is incomplete or identifies another account/schema.", ""}
	}
	return e, nil
}
func withOracleEvidence(payload json.RawMessage, evidence oracle.Evidence) json.RawMessage {
	var obj map[string]any
	if json.Unmarshal(payload, &obj) != nil || obj == nil {
		return payload
	}
	obj["oracle_evidence"] = evidence
	raw, _ := json.Marshal(obj)
	return raw
}
func oracleEvidenceFrom(payload json.RawMessage) (oracle.Evidence, error) {
	var x struct {
		Evidence *oracle.Evidence `json:"oracle_evidence"`
	}
	if json.Unmarshal(payload, &x) != nil || x.Evidence == nil {
		return oracle.Evidence{}, conflict("Verified physical Oracle history evidence is required; legacy Repair evidence cannot authorize a replacement.")
	}
	return *x.Evidence, nil
}
func evidenceHash(e oracle.Evidence) string { raw, _ := json.Marshal(e); return digest(string(raw)) }
func sameOracleIdentity(a, b oracle.Evidence) bool {
	return a.DBID == b.DBID && a.DatabaseUniqueName == b.DatabaseUniqueName && a.PDBName == b.PDBName && a.Service == b.Service && a.SessionUser == b.SessionUser && a.CurrentSchema == b.CurrentSchema
}

func oracleSQLChecksum(raw []byte) int64 {
	text := strings.TrimPrefix(string(raw), "\ufeff")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	return int64(int32(crc32.ChecksumIEEE([]byte(strings.ReplaceAll(text, "\n", "")))))
}
func failedPhysicalFileMatches(e oracle.Evidence, failure *MigrationFailure, files []storedFile) bool {
	if failure == nil || failure.Version == "" || failure.Script == "" {
		return false
	}
	for _, row := range e.History {
		if !row.Success {
			if row.Version != failure.Version || row.Script != failure.Script || row.Checksum == nil {
				return false
			}
			for _, f := range files {
				if f.Path == row.Script && f.Version == row.Version && f.Kind == "versioned" {
					return *row.Checksum == oracleSQLChecksum(f.Bytes)
				}
			}
		}
	}
	return false
}
func physicalRepairCandidate(before, failed oracle.Evidence, failure *MigrationFailure, files []storedFile, migrated json.RawMessage) bool {
	if !sameOracleIdentity(before, failed) || !failedPhysicalFileMatches(failed, failure, files) {
		return false
	}
	// Earlier scripts may have committed in this migrate call. Accept only the
	// exact successes explicitly reported by native migrate, with physical CRCs
	// matching the frozen files; an unrelated appended history row is unsafe.
	var native struct {
		Count      int `json:"migrationsExecuted"`
		Migrations []struct {
			Filepath string `json:"filepath"`
			Script   string `json:"script"`
			Version  string `json:"version"`
		} `json:"migrations"`
	}
	if json.Unmarshal(migrated, &native) != nil || native.Count != len(native.Migrations) {
		return false
	}
	completed := map[string]string{}
	for _, m := range native.Migrations {
		name := m.Script
		if name == "" {
			name = path.Base(strings.ReplaceAll(m.Filepath, "\\", "/"))
		}
		if name == "" || m.Version == "" {
			return false
		}
		if _, ok := completed[name]; ok {
			return false
		}
		completed[name] = m.Version
	}
	if len(failed.History) != len(before.History)+1+len(completed) {
		return false
	}
	previous := map[int]oracle.HistoryRow{}
	maxRank := 0
	for _, row := range before.History {
		if !row.Success {
			return false
		}
		previous[row.Rank] = row
		maxRank = max(maxRank, row.Rank)
	}
	next := map[int]bool{}
	failures := 0
	for _, row := range failed.History {
		if next[row.Rank] {
			return false
		}
		next[row.Rank] = true
		if old, ok := previous[row.Rank]; ok {
			if !reflect.DeepEqual(old, row) {
				return false
			}
			continue
		}
		if row.Rank <= maxRank {
			return false
		}
		if !row.Success {
			failures++
			continue
		}
		if completed[row.Script] != row.Version || row.Checksum == nil {
			return false
		}
		matched := false
		for _, f := range files {
			if f.Path == row.Script && f.Version == row.Version && *row.Checksum == oracleSQLChecksum(f.Bytes) {
				matched = true
			}
		}
		if !matched {
			return false
		}
		delete(completed, row.Script)
	}
	return failures == 1 && len(completed) == 0
}
func physicalRepairRestored(failed, after oracle.Evidence) bool {
	if !sameOracleIdentity(failed, after) {
		return false
	}
	wanted := []oracle.HistoryRow{}
	for _, r := range failed.History {
		if r.Success {
			wanted = append(wanted, r)
		}
	}
	return reflect.DeepEqual(wanted, after.History)
}

// Human Admin/Operator, with current project and database execute authority.
// Service tokens can consume an approval; they cannot manufacture one.
func (s *Service) correctionDBA(ctx context.Context, q queryer, a Actor, p, d string) error {
	if a.Kind != "user" {
		return denied()
	}
	var current Actor
	current.ID = a.ID
	current.Kind = "user"
	if err := q.QueryRowContext(ctx, `SELECT role FROM users WHERE id=? AND active=1`, a.ID).Scan(&current.Role); err != nil {
		return denied()
	}
	if current.Role != "Admin" && current.Role != "Operator" {
		return denied()
	}
	if _, err := s.deploymentAccess(ctx, q, current, p, d, "execute"); err != nil {
		return err
	}
	dest, err := deploymentFrom(ctx, q, p, d)
	if err != nil {
		return err
	}
	return s.authorizeExecution(ctx, q, current, dest.TargetID, dest.SchemaID)
}

func (s *Service) correctionApproval(ctx context.Context, q queryer, in RunInput, runID string) (*correctionApproval, error) {
	var held bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM migration_streams WHERE target_id=? AND schema_id=? AND held=1)`, in.TargetID, in.SchemaID).Scan(&held); err != nil {
		return nil, err
	}
	if !held {
		return nil, nil
	}
	var x correctionApproval
	var preflight string
	err := q.QueryRowContext(ctx, `SELECT c.id,c.failed_run_id,c.project_id,c.deployment_target_id,c.approved_by,c.expires_at,p.fingerprint,p.assessment
 FROM oracle_correction_approvals c JOIN migration_preflights p ON p.id=c.preflight_id
 JOIN project_artifacts a ON a.project_id=c.project_id AND a.deployment_target_id=c.deployment_target_id AND a.input_sha256=c.input_sha256
 JOIN deployment_targets d ON d.id=c.deployment_target_id
 WHERE a.artifact_id=? AND d.target_id=? AND d.schema_id=? AND c.expires_at>? AND ((c.consumed_run_id IS NULL AND ?='') OR c.consumed_run_id=?)
 ORDER BY c.created_at DESC,c.id DESC LIMIT 1`, in.ArtifactID, in.TargetID, in.SchemaID, now(), runID, runID).Scan(&x.ID, &x.FailedRunID, &x.ProjectID, &x.DeploymentID, &x.ApprovedBy, &x.ExpiresAt, &x.Fingerprint, &preflight)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var review struct {
		Evidence oracle.Evidence `json:"oracle_evidence"`
	}
	if err = json.Unmarshal([]byte(preflight), &review); err != nil {
		return nil, err
	}
	x.Evidence = review.Evidence
	if err = s.correctionDBA(ctx, q, Actor{ID: x.ApprovedBy, Kind: "user"}, x.ProjectID, x.DeploymentID); err != nil {
		return nil, err
	}
	if err := s.latestCorrectionFailure(ctx, q, x.FailedRunID, runID); err != nil {
		return nil, err
	}
	return &x, nil
}

func (s *Service) correctionFailure(ctx context.Context, q queryer, in RunInput, failedID string) (*MigrationFailure, error) {
	var repair, engine string
	var claimed bool
	err := q.QueryRowContext(ctx, `SELECT r.repair_outcome,t.engine,j.claimed FROM migration_runs r JOIN database_targets t ON t.id=r.target_id JOIN jobs j ON j.run_id=r.id
 WHERE r.id=? AND r.target_id=? AND r.schema_id=? AND r.execution_account_id=? AND r.migration_outcome='FAILED' AND r.status='FAILED'`, failedID, in.TargetID, in.SchemaID, in.ExecutionAccountID).Scan(&repair, &engine, &claimed)
	if err != nil {
		return nil, conflict("Correction requires a settled Oracle failure with verified successful automatic Repair.")
	}
	if engine != "oracle" || repair != "SUCCEEDED" || claimed {
		return nil, conflict("Only verified successful automatic Repair permits the primary correction flow.")
	}
	var busy int
	if err = q.QueryRowContext(ctx, `SELECT count(*) FROM migration_runs r JOIN jobs j ON j.run_id=r.id WHERE r.target_id=? AND r.schema_id=? AND (r.status IN ('QUEUED','PREFLIGHT','RUNNING','REPAIRING') OR j.claimed=1)`, in.TargetID, in.SchemaID).Scan(&busy); err != nil {
		return nil, err
	}
	if busy > 0 {
		return nil, conflict("Stream has a queued or active migration job.")
	}
	var f *MigrationFailure
	// Use the failed run's pinned Flyway version for native failure parsing.
	var version string
	if err = q.QueryRowContext(ctx, `SELECT flyway_version FROM migration_runs WHERE id=?`, failedID).Scan(&version); err != nil {
		return nil, err
	}
	if err = s.latestCorrectionFailure(ctx, q, failedID, ""); err != nil {
		return nil, err
	}
	f, err = runMigrationFailureFrom(ctx, q, failedID, version)
	if err != nil {
		return nil, err
	}
	if f == nil || f.Script == "" || f.Version == "" || f.IdentityReason != "" {
		return nil, conflict("The original failure does not identify exactly one failed versioned SQL file.")
	}
	return f, nil
}

// A correction changes exactly the failed file's bytes, retains its version/path,
// and includes the entire original frozen package (including pending successors).
func (s *Service) correctionFiles(ctx context.Context, q queryer, in RunInput, failedID string) ([]storedFile, error) {
	f, err := s.correctionFailure(ctx, q, in, failedID)
	if err != nil {
		return nil, err
	}
	old, err := readStoredFiles(ctx, q, `SELECT f.artifact_id,f.path,f.sha256,f.kind,f.version,f.content FROM migration_run_items i JOIN migration_files f ON f.artifact_id=i.artifact_id AND f.path=i.path WHERE i.run_id=? ORDER BY f.path`, failedID)
	if err != nil {
		return nil, err
	}
	incoming, err := readStoredFiles(ctx, q, `SELECT artifact_id,path,sha256,kind,version,content FROM migration_files WHERE artifact_id=? ORDER BY path`, in.ArtifactID)
	if err != nil {
		return nil, err
	}
	if len(old) != len(incoming) {
		return nil, conflict("Corrected package must retain every original file and pending successor.")
	}
	changed := 0
	for i, x := range incoming {
		o := old[i]
		if x.Path != o.Path || x.Kind != o.Kind || x.Version != o.Version {
			return nil, conflict("Correction cannot rename, add, omit or renumber migrations.")
		}
		if x.SHA256 != o.SHA256 {
			if x.Kind != "versioned" || x.Version != f.Version || x.Path != f.Script {
				return nil, conflict("Only the confirmed failed version may change.")
			}
			changed++
		}
	}
	if changed != 1 {
		return nil, conflict("Correction must replace exactly the failed migration's bytes.")
	}
	return incoming, nil
}

func correctionFingerprint(plan executionPlan, e oracle.Evidence, failed string, revision int, pin string) string {
	files := []any{}
	for _, f := range plan.files {
		files = append(files, []string{f.Path, f.SHA256})
	}
	raw, _ := json.Marshal([]any{plan.snapshot, files, e, failed, revision, pin})
	return digest(string(raw))
}
func (s *Service) verifyCorrectionEvidence(ctx context.Context, plan executionPlan, c *correctionApproval) error {
	if c == nil {
		return nil
	}
	e, err := s.oracleEvidence(ctx, plan)
	if err != nil {
		return err
	}
	d, err := deploymentFrom(ctx, s.db, c.ProjectID, c.DeploymentID)
	if err != nil {
		return err
	}
	if correctionFingerprint(plan, e, c.FailedRunID, d.Revision, s.execution.pin) != c.Fingerprint {
		return conflict("DBA correction approval is stale: Oracle identity/history/catalogue, artifact or destination evidence changed. Review again.")
	}
	return nil
}

func (s *Service) ReviewOracleCorrection(ctx context.Context, t Trace, p, d string, in CorrectionReviewInput) (map[string]any, error) {
	if s.execution == nil {
		return nil, conflict("Flyway runtime is unavailable.")
	}
	if err := s.correctionDBA(ctx, s.db, t.Actor, p, d); err != nil {
		return nil, err
	}
	dest, err := s.DeploymentTarget(ctx, t.Actor, p, d, "preflight")
	if err != nil {
		return nil, err
	}
	if dest.Revision != in.DeploymentTargetRevision {
		return nil, conflict("Deployment Target revision is stale.")
	}
	if _, err = s.artifactForDeployment(ctx, s.db, t.Actor, p, d, in.ArtifactID); err != nil {
		return nil, err
	}
	var sourceProject, sourceDest string
	if err = s.db.QueryRowContext(ctx, `SELECT project_id,deployment_target_id FROM project_migration_runs WHERE run_id=?`, in.FailedRunID).Scan(&sourceProject, &sourceDest); err != nil {
		return nil, translate(err)
	}
	if sourceProject != p || sourceDest != d {
		return nil, missing()
	}
	input := externalAdmission{Project: p, Deployment: d, Revision: dest.Revision}
	ri := input.runInput(dest, in.ArtifactID)
	ri.correction = &correctionReview{FailedRunID: in.FailedRunID}
	lock := s.execution.stream(dest.TargetID, dest.SchemaID)
	lock.Lock()
	defer lock.Unlock()
	var held bool
	if err = s.db.QueryRowContext(ctx, `SELECT held FROM migration_streams WHERE target_id=? AND schema_id=?`, dest.TargetID, dest.SchemaID).Scan(&held); err != nil {
		return nil, translate(err)
	}
	if !held {
		return nil, conflict("Correction review requires the repaired stream to remain held.")
	}
	plan, err := s.localPlan(ctx, s.db, t.Actor, ri, s.Runtime(ctx).Version, true)
	if err != nil {
		return nil, err
	}
	defer clear(plan.password)
	var post string
	if err = s.db.QueryRowContext(ctx, `SELECT after_history FROM repair_operations WHERE run_id=? AND outcome='SUCCEEDED'`, in.FailedRunID).Scan(&post); err != nil {
		return nil, translate(err)
	}
	previous, err := oracleEvidenceFrom(json.RawMessage(post))
	if err != nil {
		return nil, err
	}
	current, err := s.oracleEvidence(ctx, plan)
	if err != nil {
		return nil, err
	}
	if !sameOracleIdentity(previous, current) || !reflect.DeepEqual(previous.History, current.History) {
		return nil, conflict("Oracle identity or physical history differs from verified post-Repair evidence.")
	}
	assessment, err := s.preflight(ctx, t, ri, true)
	if err != nil {
		return nil, err
	}
	// preflight records NOT_VERIFIED on timeout; that is not permission to
	// probe again or persist a review with the expired operation context.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Read again after native validation to bind a stable, current review snapshot.
	repeated, err := s.oracleEvidence(ctx, plan)
	if err != nil {
		return nil, err
	}
	if evidenceHash(repeated) != evidenceHash(current) {
		return nil, conflict("Oracle evidence changed during correction review.")
	}
	fingerprint := correctionFingerprint(plan, current, in.FailedRunID, dest.Revision, s.execution.pin)
	fid := id()
	out := map[string]any{"id": fid, "project_id": p, "deployment_target_id": d, "deployment_target_revision": dest.Revision, "artifact_id": in.ArtifactID, "correction_of_run_id": in.FailedRunID, "review_only": true, "ready": assessment.Ready, "live_status": assessment.LiveStatus, "blockers": assessment.Blockers, "assessment": assessment, "oracle_evidence": current, "created_at": stamp(now()), "expires_at": stamp(now() + 600)}
	out["destination"] = dest
	out["warnings"] = assessment.Warnings
	out["external_source"] = ExternalSource{}
	out["actor"] = map[string]string{"id": t.Actor.ID, "kind": t.Actor.Kind, "name": t.Actor.Name}
	payload, _ := json.Marshal(out)
	err = s.mutation(ctx, t, "oracle.correction.review", fid, func(tx *sql.Tx) error {
		if err := s.correctionDBA(ctx, tx, t.Actor, p, d); err != nil {
			return err
		}
		fresh, err := s.localPlan(ctx, tx, t.Actor, ri, assessment.RuntimeVersion, true)
		if err != nil {
			return err
		}
		defer clear(fresh.password)
		liveDest, err := deploymentFrom(ctx, tx, p, d)
		if err != nil {
			return err
		}
		if correctionFingerprint(fresh, current, in.FailedRunID, liveDest.Revision, s.execution.pin) != fingerprint {
			return conflict("Destination changed during correction review.")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO migration_preflights VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, fid, p, d, in.ArtifactID, dest.Revision, t.Actor.Kind, t.Actor.ID, fingerprint, assessment.Ready, now(), now()+600, string(payload))
		return err
	})
	return out, err
}

func (s *Service) ApproveOracleCorrection(ctx context.Context, t Trace, p, d string, in CorrectionApprovalInput) (map[string]any, error) {
	if s.execution == nil {
		return nil, conflict("Flyway runtime is unavailable.")
	}
	if err := s.correctionDBA(ctx, s.db, t.Actor, p, d); err != nil {
		return nil, err
	}
	if !in.PartialDDLReviewed || !validText(in.Note, 2000) {
		return nil, invalid("partial_ddl_reviewed", "A human DBA must inspect partial DDL and provide a review note.")
	}
	var aid, payload, fingerprint string
	var rev int
	var ready bool
	var expiry int64
	err := s.db.QueryRowContext(ctx, `SELECT artifact_id,assessment,fingerprint,revision,ready,expires_at FROM migration_preflights WHERE id=? AND project_id=? AND deployment_target_id=?`, in.PreflightID, p, d).Scan(&aid, &payload, &fingerprint, &rev, &ready, &expiry)
	if err != nil {
		return nil, translate(err)
	}
	var review struct {
		Failed     string          `json:"correction_of_run_id"`
		Evidence   oracle.Evidence `json:"oracle_evidence"`
		ReviewOnly bool            `json:"review_only"`
	}
	if json.Unmarshal([]byte(payload), &review) != nil || !review.ReviewOnly || review.Failed == "" || !ready || expiry <= now() {
		return nil, conflict("A fresh VERIFIED correction review is required.")
	}
	var checksum string
	if err = s.db.QueryRowContext(ctx, `SELECT input_sha256 FROM project_artifacts WHERE artifact_id=? AND project_id=? AND deployment_target_id=?`, aid, p, d).Scan(&checksum); err != nil {
		return nil, translate(err)
	}
	if in.InputSHA256 != checksum {
		return nil, conflict("DBA approval checksum does not match the reviewed ZIP.")
	}
	dest, err := s.DeploymentTarget(ctx, t.Actor, p, d, "execute")
	if err != nil {
		return nil, err
	}
	if dest.Revision != rev {
		return nil, conflict("Destination changed after correction review.")
	}
	adm := externalAdmission{Project: p, Deployment: d, Revision: rev}
	ri := adm.runInput(dest, aid)
	ri.correction = &correctionReview{FailedRunID: review.Failed}
	lock := s.execution.stream(dest.TargetID, dest.SchemaID)
	lock.Lock()
	defer lock.Unlock()
	plan, err := s.localPlan(ctx, s.db, t.Actor, ri, s.Runtime(ctx).Version, true)
	if err != nil {
		return nil, err
	}
	defer clear(plan.password)
	evidence, err := s.oracleEvidence(ctx, plan)
	if err != nil {
		return nil, err
	}
	if correctionFingerprint(plan, evidence, review.Failed, rev, s.execution.pin) != fingerprint {
		return nil, conflict("Oracle or destination evidence changed after review; review again.")
	}
	cid := id()
	err = s.mutation(ctx, t, "oracle.correction.approve", cid, func(tx *sql.Tx) error {
		if err := s.correctionDBA(ctx, tx, t.Actor, p, d); err != nil {
			return err
		}
		fresh, err := s.localPlan(ctx, tx, t.Actor, ri, s.Runtime(ctx).Version, true)
		if err != nil {
			return err
		}
		defer clear(fresh.password)
		liveDest, err := deploymentFrom(ctx, tx, p, d)
		if err != nil {
			return err
		}
		if expiry <= now() || correctionFingerprint(fresh, evidence, review.Failed, liveDest.Revision, s.execution.pin) != fingerprint {
			return conflict("Correction review changed or expired.")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO oracle_correction_approvals(id,preflight_id,failed_run_id,project_id,deployment_target_id,input_sha256,approved_by,note,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, cid, in.PreflightID, review.Failed, p, d, checksum, t.Actor.ID, in.Note, now(), expiry)
		return err
	})
	return map[string]any{"id": cid, "failed_run_id": review.Failed, "input_sha256": checksum, "approved_by": t.Actor.ID, "expires_at": stamp(expiry), "stream_held": true, "single_use": true}, err
}

func (s *Service) latestCorrectionFailure(ctx context.Context, q queryer, failed, ignoreRun string) error {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM migration_runs later JOIN migration_runs failed ON failed.id=? WHERE later.target_id=failed.target_id AND later.schema_id=failed.schema_id AND later.sequence>failed.sequence AND later.id<>? AND EXISTS(SELECT 1 FROM run_executions e WHERE e.run_id=later.id AND e.command='migrate')`, failed, ignoreRun).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return conflict("A later migration attempt superseded this failure; review the latest failure instead.")
	}
	return nil
}

func physicalCorrectionSucceeded(before, after oracle.Evidence, files []storedFile, migrated json.RawMessage) bool {
	if !sameOracleIdentity(before, after) {
		return false
	}
	old := map[int]oracle.HistoryRow{}
	for _, r := range before.History {
		if !r.Success {
			return false
		}
		old[r.Rank] = r
	}
	var native struct {
		Count      int `json:"migrationsExecuted"`
		Migrations []struct {
			Version  string `json:"version"`
			Filepath string `json:"filepath"`
			Script   string `json:"script"`
		} `json:"migrations"`
	}
	if json.Unmarshal(migrated, &native) != nil || native.Count != len(native.Migrations) || len(after.History) != len(before.History)+native.Count {
		return false
	}
	completed := map[string]string{}
	for _, m := range native.Migrations {
		name := m.Script
		if name == "" {
			name = path.Base(strings.ReplaceAll(m.Filepath, "\\", "/"))
		}
		if _, exists := completed[name]; exists {
			return false
		}
		completed[name] = m.Version
	}
	seen := map[int]bool{}
	for _, r := range after.History {
		if seen[r.Rank] || !r.Success {
			return false
		}
		seen[r.Rank] = true
		if previous, ok := old[r.Rank]; ok {
			if !reflect.DeepEqual(previous, r) {
				return false
			}
			continue
		}
		version, reported := completed[r.Script]
		if !reported || version != r.Version || r.Checksum == nil {
			return false
		}
		matched := false
		for _, f := range files {
			if f.Path == r.Script && f.Version == r.Version && oracleSQLChecksum(f.Bytes) == *r.Checksum {
				matched = true
			}
		}
		if !matched {
			return false
		}
		delete(completed, r.Script)
	}
	return len(completed) == 0
}
