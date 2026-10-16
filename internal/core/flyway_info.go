package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// InfoCapture retains native JSON without restricting Flyway's open-ended state vocabulary.
// Recorded state is always historical; resolver completeness cannot prove the operator
// supplied every migration that belongs to an externally managed database.
type InfoCapture struct {
	ID             string          `json:"id"`
	TargetID       string          `json:"target_id"`
	SchemaID       string          `json:"schema_id"`
	SourceRunID    string          `json:"source_run_id,omitempty"`
	Source         string          `json:"source"`
	CapturedAt     string          `json:"captured_at"`
	StartedAt      string          `json:"started_at"`
	ArtifactID     string          `json:"artifact_id"`
	ArtifactSHA256 string          `json:"artifact_sha256"`
	Snapshot       RunSnapshot     `json:"snapshot"`
	Files          []ArtifactFile  `json:"files"`
	RuntimeVersion string          `json:"runtime_version"`
	ActorName      string          `json:"actor_name"`
	Success        bool            `json:"success"`
	ExitCode       int             `json:"exit_code"`
	JSON           json.RawMessage `json:"json"`
	Stdout         string          `json:"stdout"`
	Stderr         string          `json:"stderr"`
	Warnings       []string        `json:"warnings"`
}

func infoWarnings(x *InfoCapture) {
	x.Warnings = []string{"Recorded snapshot, not live database state. Pending and missing states depend on the captured migration files; completeness against external migration sources is not independently verified."}
	if !x.Success {
		x.Warnings = append(x.Warnings, "Inspection failed or returned partial evidence. Consult diagnostics; empty output does not mean empty history.")
	}
	if e := safeHistory(x.JSON); e != nil {
		x.Warnings = append(x.Warnings, "HISTORY_UNSAFE: "+e.Error()+". Existing migration safeguards remain enforced.")
	}
}

// LatestInfo returns the newest inspection (including failures), or the latest
// native info execution recorded by migration jobs. A run selector is historical.
func (s *Service) LatestInfo(ctx context.Context, a Actor, target, schema, runID string) (*InfoCapture, error) {
	if e := s.Authorize(ctx, a, "read", target, schema); e != nil {
		return nil, e
	}
	var latest *InfoCapture
	if runID == "" {
		var record string
		e := s.db.QueryRowContext(ctx, `SELECT record FROM flyway_info_captures WHERE target_id=? AND schema_id=? ORDER BY sequence DESC LIMIT 1`, target, schema).Scan(&record)
		if e != nil && e != sql.ErrNoRows {
			return nil, e
		}
		if e == nil {
			var x InfoCapture
			if e = json.Unmarshal([]byte(record), &x); e != nil {
				return nil, e
			}
			latest = &x
		}
	} else {
		r, e := s.Run(ctx, a, runID)
		if e != nil {
			return nil, e
		}
		if r.TargetID != target || r.SchemaID != schema {
			return nil, missing()
		}
	}
	var executionID, rid, finished string
	e := s.db.QueryRowContext(ctx, `SELECT e.id,r.id,e.finished_at FROM run_executions e JOIN migration_runs r ON r.id=e.run_id WHERE r.target_id=? AND r.schema_id=? AND e.command='info' AND (?='' OR r.id=?) ORDER BY e.rowid DESC LIMIT 1`, target, schema, runID, runID).Scan(&executionID, &rid, &finished)
	if e != nil && e != sql.ErrNoRows {
		return nil, e
	}
	if e == nil && (latest == nil || captureAfter(finished, latest.CapturedAt)) {
		r, e := s.Run(ctx, a, rid)
		if e != nil {
			return nil, e
		}
		executions, e := s.RunExecutions(ctx, a, rid)
		if e != nil {
			return nil, e
		}
		for _, ex := range executions {
			if ex.ID == executionID {
				x := InfoCapture{ID: ex.ID, TargetID: target, SchemaID: schema, SourceRunID: rid, Source: "migration-run", CapturedAt: ex.FinishedAt, StartedAt: ex.StartedAt, ArtifactID: r.ArtifactID, Snapshot: r.RunSnapshot, RuntimeVersion: r.FlywayVersion, ActorName: r.ActorName, Success: ex.Success, ExitCode: ex.ExitCode, JSON: ex.JSON, Stdout: ex.Stdout, Stderr: ex.Stderr, Files: []ArtifactFile{}}
				artifact, e := s.Artifact(ctx, a, r.ArtifactID)
				if e != nil {
					return nil, e
				}
				x.ArtifactSHA256 = artifact.SHA256
				files, e := readStoredFiles(ctx, s.db, `SELECT f.artifact_id,f.path,f.sha256,f.kind,f.version,f.content FROM migration_run_items i JOIN migration_files f ON f.artifact_id=i.artifact_id AND f.path=i.path WHERE i.run_id=? ORDER BY f.path`, rid)
				if e != nil {
					return nil, e
				}
				for _, f := range files {
					x.Files = append(x.Files, ArtifactFile{f.Path, f.SHA256, f.Kind, f.Version, len(f.Bytes)})
				}
				infoWarnings(&x)
				latest = &x
			}
		}
	}
	if latest != nil {
		// Compare current registry destination without changing historical evidence.
		t, e := s.Target(ctx, a, target)
		if e != nil {
			return nil, e
		}
		sc, e := schemaScan(s.db.QueryRowContext(ctx, "SELECT "+schemaColumns+" FROM target_schemas WHERE id=? AND target_id=?", schema, target))
		if e != nil {
			return nil, e
		}
		old := latest.Snapshot
		if t.Host != old.Target.Host || t.Port != old.Target.Port || t.Service != old.Target.Service || t.Transport != old.Target.Transport || sc.Name != old.Schema.Name || sc.HistorySchema != old.Schema.HistorySchema || sc.HistoryTable != old.Schema.HistoryTable {
			latest.Warnings = append(latest.Warnings, "Destination or history configuration has changed since capture. This evidence describes the original snapshot.")
		}
	}
	return latest, nil
}

func (s *Service) RefreshInfo(ctx context.Context, t Trace, in RunInput) (InfoCapture, error) {
	var out InfoCapture
	if e := s.Authorize(ctx, t.Actor, "operate", in.TargetID, in.SchemaID); e != nil {
		return out, e
	}
	if s.execution == nil {
		return out, conflict("Flyway runtime is not configured.")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	lock := s.execution.stream(in.TargetID, in.SchemaID)
	if !lock.TryLock() {
		return out, conflict("Stream is executing or inspecting another request.")
	}
	defer lock.Unlock()
	ready := s.Runtime(ctx)
	if !ready.Ready {
		return out, conflict("Pinned Flyway runtime is not ready.")
	}
	var plan executionPlan
	// Recheck current sessions, grants, account allowlists, stream holds and immutable
	// file checksums immediately before dispatch. No migration job is submitted.
	e := s.mutation(ctx, t, "flyway.info.requested", in.SchemaID, func(tx *sql.Tx) error {
		var err error
		plan, err = s.localPlan(ctx, tx, t.Actor, in, ready.Version, true)
		return err
	})
	defer clear(plan.password)
	if e != nil {
		return out, e
	}
	artifact, e := s.Artifact(ctx, t.Actor, in.ArtifactID)
	if e != nil {
		return out, e
	}
	out = InfoCapture{ID: id(), TargetID: in.TargetID, SchemaID: in.SchemaID, Source: "read-only-refresh", StartedAt: utcNow(), ArtifactID: in.ArtifactID, ArtifactSHA256: artifact.SHA256, Snapshot: plan.snapshot, RuntimeVersion: ready.Version, ActorName: t.Actor.Name, Files: []ArtifactFile{}}
	for _, f := range plan.files {
		out.Files = append(out.Files, ArtifactFile{f.Path, f.SHA256, f.Kind, f.Version, len(f.Bytes)})
	}
	// This literal command is the only runtime operation in the refresh path.
	result := s.execution.runner.Execute(ctx, requestFor(plan, "info"))
	out.CapturedAt = utcNow()
	out.Success = result.Success
	out.ExitCode = result.ExitCode
	out.JSON = result.JSON
	out.Stdout = result.Stdout
	out.Stderr = result.Stderr
	if len(out.JSON) == 0 {
		out.JSON = json.RawMessage("null")
	}
	infoWarnings(&out)
	if resolvedSnapshot(plan, out.JSON) != nil {
		out.Warnings = append(out.Warnings, "Artifact mismatch: Flyway did not resolve every captured file. Pending/missing states are not authoritative.")
	}
	// Persist bounded redacted diagnostics even when the browser times out/disconnects.
	durable, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer done()
	payload, e := json.Marshal(out)
	if e != nil {
		return out, e
	}
	tx, e := s.db.BeginTx(durable, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(durable, `INSERT INTO flyway_info_captures(id,target_id,schema_id,captured_at,record) VALUES(?,?,?,?,?)`, out.ID, in.TargetID, in.SchemaID, out.CapturedAt, string(payload)); e != nil {
		return out, e
	}
	outcome := "failed"
	if out.Success {
		outcome = "success"
	}
	if e = audit(durable, tx, t, "flyway.info.captured", out.ID, outcome); e != nil {
		return out, e
	}
	return out, tx.Commit()
}

// RFC3339Nano strings have variable precision and must be compared as instants.
func captureAfter(candidate, previous string) bool {
	a, ea := time.Parse(time.RFC3339Nano, candidate)
	b, eb := time.Parse(time.RFC3339Nano, previous)
	return ea == nil && (eb != nil || a.After(b))
}
