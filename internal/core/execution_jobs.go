package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flyway-manager/internal/artifact"
	"path/filepath"
	"sync"
	"time"
)

// StartExecution must run before serving HTTP. One OS lock covers recovery and dispatch.
func (s *Service) StartExecution(parent context.Context, runner FlywayRunner, pin, dataDir string, limits artifact.Limits) error {
	if s.execution != nil {
		return errors.New("execution already started")
	}
	if runner == nil || pin == "" {
		return errors.New("runner and immutable runtime pin required")
	}
	release, e := executionLock(filepath.Join(dataDir, "execution.lock"))
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(parent)
	s.execution = &Execution{runner: runner, pin: pin, limits: limits, streams: map[string]*sync.Mutex{}, cancel: cancel, lockRelease: release}
	if e = s.recoverExecution(ctx); e != nil {
		cancel()
		release()
		s.execution = nil
		return e
	}
	s.execution.wg.Add(1)
	go func() { defer s.execution.wg.Done(); s.dispatch(ctx) }()
	s.execution.wg.Add(1)
	go func() { defer s.execution.wg.Done(); s.reconcileRecovered(ctx) }()
	return nil
}
func (s *Service) StopExecution() {
	if s.execution == nil {
		return
	}
	s.execution.cancel()
	s.execution.wg.Wait()
	_ = s.execution.lockRelease()
}

func (s *Service) SubmitRun(ctx context.Context, t Trace, in RunInput, key string) (Run, error) {
	if !validText(key, 200) {
		return Run{}, invalid("Idempotency-Key", "A nonempty Idempotency-Key of at most 200 bytes is required.")
	}
	if e := s.Authorize(ctx, t.Actor, "operate", in.TargetID, in.SchemaID); e != nil {
		return Run{}, e
	}
	request, _ := json.Marshal(in)
	requestHash := digest(string(request))
	var existing, hash string
	e := s.db.QueryRowContext(ctx, `SELECT run_id,request_hash FROM idempotency_records WHERE actor_kind=? AND actor_id=? AND key=?`, t.Actor.Kind, t.Actor.ID, key).Scan(&existing, &hash)
	if e == nil {
		if hash != requestHash {
			return Run{}, conflict("Idempotency key was already used with a different request.")
		}
		return s.Run(ctx, t.Actor, existing)
	}
	if e != sql.ErrNoRows {
		return Run{}, e
	}
	if e := connectionSchemaAllowed(ctx, s.db, in.TargetID, in.SchemaID); e != nil {
		return Run{}, e
	}
	if e := connectionAccountAllowed(ctx, s.db, in.TargetID, in.SchemaID, in.ExecutionAccountID); e != nil {
		return Run{}, e
	}
	if s.execution == nil {
		return Run{}, &Error{503, "RUNTIME_UNAVAILABLE", "Flyway runtime is not configured.", ""}
	}
	ready := s.Runtime(ctx)
	if !ready.Ready {
		return Run{}, &Error{503, "RUNTIME_UNAVAILABLE", "Pinned Flyway runtime failed readiness verification.", ""}
	}
	rid := id()
	source := "API"
	if t.Actor.Kind == "user" {
		source = "WEB"
	}
	e = s.mutation(ctx, t, "migration.submit", rid, func(tx *sql.Tx) error {
		// A concurrent equivalent submission may have won while readiness was checked.
		e := tx.QueryRowContext(ctx, `SELECT run_id,request_hash FROM idempotency_records WHERE actor_kind=? AND actor_id=? AND key=?`, t.Actor.Kind, t.Actor.ID, key).Scan(&existing, &hash)
		if e == nil {
			if hash != requestHash {
				return conflict("Idempotency key was already used with a different request.")
			}
			rid = existing
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		// Discard unused reservations of cancelled/blocked runs; accepted bytes remain immutable.
		if _, e = tx.ExecContext(ctx, `DELETE FROM stream_files WHERE target_id=? AND schema_id=? AND NOT EXISTS(SELECT 1 FROM migration_run_items i JOIN migration_runs r ON r.id=i.run_id WHERE i.artifact_id=stream_files.artifact_id AND i.path=stream_files.path AND r.status NOT IN ('CANCELLED','BLOCKED'))`, in.TargetID, in.SchemaID); e != nil {
			return e
		}
		plan, e := s.localPlan(ctx, tx, t.Actor, in, ready.Version, false)
		if e != nil {
			return e
		}
		defer clear(plan.password)
		snapshot, _ := json.Marshal(plan.snapshot)
		if _, e = tx.ExecContext(ctx, `INSERT INTO migration_streams(target_id,schema_id,configuration,runtime_pin) VALUES(?,?,?,?) ON CONFLICT(target_id,schema_id) DO NOTHING`, in.TargetID, in.SchemaID, plan.configuration, s.execution.pin); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO migration_runs(id,target_id,schema_id,artifact_id,execution_account_id,actor_id,actor_kind,actor_name,actor_role,actor_session,actor_token,source,request_id,request,snapshot,runtime_pin,created_at,status,flyway_version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'QUEUED',?)`, rid, in.TargetID, in.SchemaID, in.ArtifactID, in.ExecutionAccountID, t.Actor.ID, t.Actor.Kind, t.Actor.Name, t.Actor.Role, t.Actor.SessionID, t.Actor.TokenID, source, t.RequestID, string(request), string(snapshot), s.execution.pin, now(), ready.Version); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO run_dependencies(run_id,predecessor_id) SELECT ?,r.id FROM migration_runs r JOIN jobs j ON j.run_id=r.id WHERE r.target_id=? AND r.schema_id=? AND r.id<>? AND (r.status IN ('QUEUED','PREFLIGHT','RUNNING','REPAIRING') OR j.claimed=1)`, rid, in.TargetID, in.SchemaID, rid); e != nil {
			return e
		}
		for _, f := range plan.files {
			if _, e = tx.ExecContext(ctx, `INSERT INTO migration_run_items VALUES(?,?,?)`, rid, f.ArtifactID, f.Path); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO stream_files VALUES(?,?,?,?,?) ON CONFLICT(target_id,schema_id,migration_key) DO UPDATE SET artifact_id=excluded.artifact_id,path=excluded.path`, in.TargetID, in.SchemaID, migrationKey(f.File), f.ArtifactID, f.Path); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO jobs(run_id) VALUES(?)`, rid); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO idempotency_records VALUES(?,?,?,?,?)`, t.Actor.Kind, t.Actor.ID, key, requestHash, rid)
		return e
	})
	if e != nil {
		return Run{}, e
	}
	return s.Run(ctx, t.Actor, rid)
}
func (s *Service) recoverExecution(ctx context.Context) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// Even interrupted PREFLIGHT is held: a previous process might have crossed a
	// side-effect boundary before it could persist an outcome.
	reason := "Application stopped during execution or repair; Oracle outcome requires reconciliation. SQL was not replayed."
	if _, e = tx.ExecContext(ctx, `UPDATE migration_streams SET held=1,reason=? WHERE EXISTS(SELECT 1 FROM migration_runs r JOIN jobs j ON j.run_id=r.id WHERE r.target_id=migration_streams.target_id AND r.schema_id=migration_streams.schema_id AND (r.status IN ('PREFLIGHT','RUNNING','REPAIRING') OR j.claimed=1))`, reason); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,actor_type,actor_name,action,target,timestamp,outcome,request_id) SELECT actor_id,actor_kind,actor_name,'migration.recovery',id,?,'NEEDS_REVIEW',request_id FROM migration_runs WHERE status IN ('PREFLIGHT','RUNNING','REPAIRING') OR id IN(SELECT run_id FROM jobs WHERE claimed=1)`, now()); e != nil {
		return e
	}
	// FAILED with claimed=1 covers a crash between persisting the original failure and repair.
	if _, e = tx.ExecContext(ctx, `UPDATE migration_runs SET status='NEEDS_REVIEW',reason=?,finished_at=? WHERE status IN ('PREFLIGHT','RUNNING','REPAIRING') OR (status='FAILED' AND id IN(SELECT run_id FROM jobs WHERE claimed=1))`, reason, now()); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET claimed=0 WHERE run_id IN(SELECT id FROM migration_runs WHERE status='NEEDS_REVIEW')`); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE migration_runs SET status='BLOCKED',reason=?,finished_at=? WHERE status='QUEUED' AND EXISTS(SELECT 1 FROM migration_streams s WHERE s.target_id=migration_runs.target_id AND s.schema_id=migration_runs.schema_id AND s.held=1)`, reason, now()); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) claim(ctx context.Context) (string, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `UPDATE migration_runs SET status='BLOCKED',reason='Stream is held for Oracle reconciliation.',finished_at=? WHERE status='QUEUED' AND EXISTS(SELECT 1 FROM migration_streams s WHERE s.target_id=migration_runs.target_id AND s.schema_id=migration_runs.schema_id AND s.held=1)`, now()); e != nil {
		return "", e
	}
	var rid string
	e = tx.QueryRowContext(ctx, `SELECT r.id FROM migration_runs r JOIN jobs j ON j.run_id=r.id JOIN migration_streams s ON s.target_id=r.target_id AND s.schema_id=r.schema_id WHERE r.status='QUEUED' AND j.claimed=0 AND s.held=0 AND NOT EXISTS(SELECT 1 FROM migration_runs other JOIN jobs oj ON oj.run_id=other.id WHERE other.target_id=r.target_id AND other.schema_id=r.schema_id AND other.id<>r.id AND (oj.claimed=1 OR (other.status='QUEUED' AND other.sequence<r.sequence))) ORDER BY r.sequence LIMIT 1`).Scan(&rid)
	if e == sql.ErrNoRows {
		if e = tx.Commit(); e != nil {
			return "", e
		}
		return "", nil
	}
	if e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE migration_runs SET status='PREFLIGHT',started_at=? WHERE id=? AND status='QUEUED'`, now(), rid); e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET claimed=1 WHERE run_id=?`, rid); e != nil {
		return "", e
	}
	return rid, tx.Commit()
}
func (s *Service) dispatch(ctx context.Context) {
	slots := make(chan struct{}, 4)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		for {
			select {
			case slots <- struct{}{}:
			default:
				goto nextTick
			}
			rid, e := s.claim(ctx)
			if e != nil || rid == "" {
				<-slots
				break
			}
			s.execution.wg.Add(1)
			go func() { defer s.execution.wg.Done(); defer func() { <-slots }(); s.executeJob(ctx, rid) }()
		}
	nextTick:
	}
}
func (s *Service) jobTrace(ctx context.Context, rid string) (Trace, error) {
	var t Trace
	e := s.db.QueryRowContext(ctx, `SELECT actor_id,actor_kind,actor_name,actor_role,actor_session,actor_token,request_id FROM migration_runs WHERE id=?`, rid).Scan(&t.Actor.ID, &t.Actor.Kind, &t.Actor.Name, &t.Actor.Role, &t.Actor.SessionID, &t.Actor.TokenID, &t.RequestID)
	return t, e
}
func (s *Service) changeRun(ctx context.Context, rid, state, reason, migration, repair string, exit *int, hold, release bool) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var finished any
	if terminal(state) {
		finished = now()
	}
	if _, e = tx.ExecContext(ctx, `UPDATE migration_runs SET status=?,reason=?,migration_outcome=CASE WHEN ?='' THEN migration_outcome ELSE ? END,repair_outcome=CASE WHEN ?='' THEN repair_outcome ELSE ? END,exit_code=COALESCE(?,exit_code),finished_at=? WHERE id=?`, state, reason, migration, migration, repair, repair, exit, finished, rid); e != nil {
		return e
	}
	if hold {
		if _, e = tx.ExecContext(ctx, `UPDATE migration_streams SET held=1,reason=? WHERE (target_id,schema_id)=(SELECT target_id,schema_id FROM migration_runs WHERE id=?)`, reason, rid); e != nil {
			return e
		}
	}
	if release {
		if _, e = tx.ExecContext(ctx, `UPDATE jobs SET claimed=0 WHERE run_id=?`, rid); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,actor_type,actor_name,action,target,timestamp,outcome,request_id) SELECT actor_id,actor_kind,actor_name,'migration.state',id,?,?,request_id FROM migration_runs WHERE id=?`, now(), state, rid); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO migration_logs(run_id,command,channel,message,created_at) VALUES(?,'orchestration','event',?,?)`, rid, state+": "+reason, utcNow()); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) CancelRun(ctx context.Context, t Trace, rid string) error {
	x, e := s.Run(ctx, t.Actor, rid)
	if e != nil {
		return e
	}
	return s.mutation(ctx, t, "migration.cancel", rid, func(tx *sql.Tx) error {
		if e := s.authorizeExecution(ctx, tx, t.Actor, x.TargetID, x.SchemaID); e != nil {
			return e
		}
		res, e := tx.ExecContext(ctx, `UPDATE migration_runs SET status='CANCELLED',reason='Cancelled before execution.',finished_at=? WHERE id=? AND status='QUEUED'`, now(), rid)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return conflict("Only queued migrations can be cancelled.")
		}
		return nil
	})
}

// A worker's persistence failure leaves a claimed job and held stream on restart.
// Do not recover by rerunning SQL in the current process.
func (s *Service) workerFailure(rid string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.changeRun(ctx, rid, "NEEDS_REVIEW", "Execution evidence could not be persisted or operation was interrupted; reconcile Oracle before further migration.", "", "", nil, true, true)
}

// Startup reconciliation only inspects Oracle through info/validate. Without
// durable exit evidence, even matching history cannot prove absence of partial
// DDL, so the original run and stream remain NEEDS_REVIEW/held.
func (s *Service) reconcileRecovered(ctx context.Context) {
	rows, e := s.db.QueryContext(ctx, `SELECT r.id FROM migration_runs r JOIN migration_streams s ON s.target_id=r.target_id AND s.schema_id=r.schema_id WHERE r.status='NEEDS_REVIEW' AND s.held=1 ORDER BY r.sequence`)
	if e != nil {
		return
	}
	ids := []string{}
	for rows.Next() {
		var rid string
		if e = rows.Scan(&rid); e != nil {
			break
		}
		ids = append(ids, rid)
	}
	rows.Close()
	if e != nil {
		return
	}
	for _, rid := range ids {
		if ctx.Err() != nil {
			return
		}
		run, e := runScan(s.db.QueryRowContext(ctx, "SELECT "+runColumns+" FROM migration_runs WHERE id=?", rid))
		if e != nil {
			continue
		}
		lock := s.execution.stream(run.TargetID, run.SchemaID)
		lock.Lock()
		func() {
			defer lock.Unlock()
			t, e := s.jobTrace(ctx, rid)
			if e != nil {
				return
			}
			plan, e := s.planJob(ctx, s.db, t.Actor, run, true)
			if e != nil {
				_ = s.Event(ctx, t, "migration.reconciliation", rid, "NOT_VERIFIED")
				return
			}
			defer clear(plan.password)
			if !s.Runtime(ctx).Ready {
				_ = s.Event(ctx, t, "migration.reconciliation", rid, "NOT_VERIFIED")
				return
			}
			durable, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			inspected, _, e := s.runCommand(ctx, durable, rid, "info", plan)
			if e != nil {
				return
			}
			validated, _, e := s.runCommand(ctx, durable, rid, "validate", plan)
			if e != nil {
				return
			}
			outcome := "NOT_VERIFIED"
			if inspected.Success && validated.Success {
				outcome = "HISTORY_INSPECTED_REVIEW_REQUIRED"
			}
			_ = s.Event(durable, t, "migration.reconciliation", rid, outcome)
		}()
	}
}
