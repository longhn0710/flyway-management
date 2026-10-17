package core

import (
	"context"
	"encoding/json"
	"flyway-manager/internal/flyway"
	"fmt"
	"strings"
	"time"
)

func (s *Service) executeJob(parent context.Context, rid string) {
	// Evidence must still be persisted after a shutdown cancels the CLI context.
	durable, cancel := context.WithTimeout(context.WithoutCancel(parent), time.Hour)
	defer cancel()
	completed := false
	defer func() {
		if !completed {
			s.workerFailure(rid)
		}
	}()
	run, e := runScan(s.db.QueryRowContext(durable, "SELECT "+runColumns+" FROM migration_runs WHERE id=?", rid))
	if e != nil {
		return
	}
	lock := s.execution.stream(run.TargetID, run.SchemaID)
	lock.Lock()
	defer lock.Unlock()
	t, e := s.jobTrace(durable, rid)
	if e != nil {
		return
	}
	block := func(reason string) {
		if s.changeRun(durable, rid, "BLOCKED", reason, "", "SKIPPED", nil, false, true) == nil {
			completed = true
		}
	}
	if parent.Err() != nil {
		block("Application stopped before migration began.")
		return
	}
	ready := s.Runtime(parent)
	if !ready.Ready || ready.Version != run.FlywayVersion {
		block("Pinned Flyway runtime is unavailable or changed.")
		return
	}
	tx, e := s.db.BeginTx(durable, nil)
	if e != nil {
		return
	}
	plan, e := s.planJob(durable, tx, t.Actor, run)
	_ = tx.Rollback()
	if e != nil {
		block(safeExecutionError(e))
		return
	}
	defer clear(plan.password)
	p := preflightBase()
	p.RuntimeVersion = run.FlywayVersion
	fillPlan(&p, plan)
	info, _, e := s.runCommand(parent, durable, rid, "info", plan)
	if e != nil {
		return
	}
	validation := flyway.Result{}
	if info.Success {
		validation, _, e = s.runCommand(parent, durable, rid, "validate", plan)
		if e != nil {
			return
		}
	}
	applyLivePreflight(&p, info, validation, plan.snapshot.Target.Engine)
	if p.Ready {
		if e = resolvedSnapshot(plan, info.JSON); e != nil {
			p.block("INCOMPLETE_FLYWAY_LOCATIONS", safeExecutionError(e))
		}
	}
	if p.Ready {
		if e = s.noImplicitRetry(durable, run, plan, info.JSON); e != nil {
			p.block("IMPLICIT_SQL_RETRY", safeExecutionError(e))
		}
	}

	preJSON, _ := json.Marshal(p)
	if _, e = s.db.ExecContext(durable, `UPDATE migration_runs SET preflight=? WHERE id=?`, string(preJSON), rid); e != nil {
		return
	}
	if !p.Ready {
		reason := "Flyway preflight failed."
		if len(p.Blockers) > 0 {
			reason = p.Blockers[0].Message
		}
		block(reason)
		return
	}
	if parent.Err() != nil {
		block("Application stopped before migration began.")
		return
	}
	// Recheck current authorization, mapping, credential version and snapshot in the
	// transaction that commits RUNNING, immediately before launching the subprocess.
	tx, e = s.db.BeginTx(durable, nil)
	if e != nil {
		return
	}
	refreshed, e := s.planJob(durable, tx, t.Actor, run)
	if e == nil {
		_, e = tx.ExecContext(durable, `UPDATE migration_runs SET status='RUNNING' WHERE id=? AND status='PREFLIGHT'`, rid)
	}
	if e == nil {
		e = audit(durable, tx, t, "migration.running", rid, "RUNNING")
	}
	if e == nil {
		e = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if e != nil {
		clear(refreshed.password)
		block(safeExecutionError(e))
		return
	}
	clear(plan.password)
	plan.password = refreshed.password
	migrated, _, e := s.runCommand(parent, durable, rid, "migrate", plan)
	if e != nil {
		return
	}
	if migrated.Success {
		// A second authoritative info+validate proves post-migrate history is coherent.
		post, _, e := s.runCommand(parent, durable, rid, "info", plan)
		if e != nil {
			return
		}
		validated, _, e := s.runCommand(parent, durable, rid, "validate", plan)
		if e != nil {
			return
		}
		if post.Success && validated.Success && safeHistory(post.JSON) == nil && allVersionsApplied(post.JSON) && resolvedSnapshot(plan, post.JSON) == nil {
			if e = s.changeRun(durable, rid, "SUCCEEDED", "Flyway migrate and post-migration history validation succeeded.", "SUCCEEDED", "SKIPPED", &migrated.ExitCode, false, true); e == nil {
				completed = true
			}
			return
		}
		if e = s.changeRun(durable, rid, "NEEDS_REVIEW", "Migrate returned success but post-migration history could not be verified.", "SUCCEEDED", "UNSAFE", &migrated.ExitCode, true, true); e == nil {
			completed = true
		}
		return
	}
	if plan.snapshot.Target.Engine == "postgresql" {
		// PostgreSQL never enters Oracle's failed-history repair branch. Even a
		// proven rollback of the last script cannot undo earlier committed scripts.
		if parent.Err() == nil {
			if _, _, e = s.runCommand(parent, durable, rid, "info", plan); e != nil {
				return
			}
			if _, _, e = s.runCommand(parent, durable, rid, "validate", plan); e != nil {
				return
			}
		}
		reason := "Flyway reported a PostgreSQL migration failure. Manager does not establish whole-run rollback; earlier scripts may have committed. Stream held; no Repair or automatic retry."
		if migrated.Ambiguous || migrated.TimedOut || len(migrated.JSON) == 0 {
			reason = "PostgreSQL CLI did not produce a confirmed migration result. Database outcome is indeterminate; backend disappearance alone does not establish rollback. Earlier scripts may have committed. Stream held; no Repair or automatic retry."
		}
		if e = s.changeRun(durable, rid, "NEEDS_REVIEW", reason, "FAILED", "SKIPPED", &migrated.ExitCode, true, true); e == nil {
			completed = true
		}
		return
	}
	// The original failure and diagnostics commit before any automatic repair.
	if e = s.changeRun(durable, rid, "FAILED", "Flyway migrate failed. Oracle DDL may have partially committed.", "FAILED", "PENDING", &migrated.ExitCode, false, false); e != nil {
		return
	}
	if migrated.TimedOut || parent.Err() != nil || len(migrated.JSON) == 0 || migrated.ExitCode == 0 {
		if e = s.finishRepair(durable, rid, "UNSAFE", "Migration outcome is ambiguous; repair was not attempted.", info.JSON, nil, "", "NEEDS_REVIEW", true); e == nil {
			completed = true
		}
		return
	}
	after, _, e := s.runCommand(parent, durable, rid, "info", plan)
	if e != nil {
		return
	}
	failedValidation, _, e := s.runCommand(parent, durable, rid, "validate", plan)
	if e != nil {
		return
	}
	reason := repairEligibility(info, after, failedValidation)
	if reason != "" {
		if e = s.finishRepair(durable, rid, "UNSAFE", reason, after.JSON, nil, "", "NEEDS_REVIEW", true); e == nil {
			completed = true
		}
		return
	}

	// Repair also mutates Oracle history, so revoked account mappings and
	// allowlists must stop it even if the migration already failed.
	repairPlan, authErr := s.planJob(durable, s.db, t.Actor, run)
	clear(repairPlan.password)
	if authErr != nil {
		if e = s.finishRepair(durable, rid, "UNSAFE", "Current execution authorization or mapping changed; repair was not attempted.", after.JSON, nil, "", "NEEDS_REVIEW", true); e == nil {
			completed = true
		}
		return
	}
	if e = s.changeRun(durable, rid, "REPAIRING", "One eligible failed versioned history row; repairing history once without replaying SQL.", "FAILED", "PENDING", nil, false, false); e != nil {
		return
	}
	repaired, executionID, e := s.runCommand(parent, durable, rid, "repair", plan)
	if e != nil {
		return
	}
	post, _, e := s.runCommand(parent, durable, rid, "info", plan)
	if e != nil {
		return
	}
	validated, _, e := s.runCommand(parent, durable, rid, "validate", plan)
	if e != nil {
		return
	}
	safe := repaired.Success && repairResultSafe(repaired.JSON) && removedFailedVersion(repaired.JSON, after.JSON) && post.Success && validated.Success && repairHistoryRestored(info.JSON, post.JSON)
	if !safe {
		if e = s.finishRepair(durable, rid, "FAILED", "Repair result or unchanged unrelated history could not be verified. Oracle DDL remains unreverted.", after.JSON, post.JSON, executionID, "NEEDS_REVIEW", true); e == nil {
			completed = true
		}
		return
	}
	if e = s.finishRepair(durable, rid, "SUCCEEDED", "Failed history entry removed; migration remains FAILED. No SQL retry or rollback occurred; partial Oracle DDL may remain.", after.JSON, post.JSON, executionID, "FAILED", false); e == nil {
		completed = true
	}
}
func (s *Service) planJob(ctx context.Context, q queryer, a Actor, run Run, allowHeld ...bool) (executionPlan, error) {
	plan := executionPlan{}
	var engine string
	if e := q.QueryRowContext(ctx, "SELECT engine FROM database_targets WHERE id=?", run.TargetID).Scan(&engine); e != nil {
		return plan, e
	}
	if engine == "postgresql" && !postgresqlMigrationAccepted {
		return plan, conflict("PostgreSQL migration is disabled pending Gate E/F acceptance.")
	}
	var unfinished int
	if e := q.QueryRowContext(ctx, `SELECT count(*) FROM run_dependencies d JOIN migration_runs r ON r.id=d.predecessor_id WHERE d.run_id=? AND r.status<>'SUCCEEDED'`, run.ID).Scan(&unfinished); e != nil {
		return plan, e
	}
	if unfinished > 0 {
		return plan, conflict("An earlier migration required by this frozen snapshot did not succeed. Its SQL will not be retried automatically.")
	}
	if e := s.authorizeExecution(ctx, q, a, run.TargetID, run.SchemaID); e != nil {
		return plan, e
	}
	snap, password, e := s.executionSnapshot(ctx, q, run.RunInput)
	if e != nil {
		return plan, e
	}
	plan.password = password
	plan.snapshot = snap
	if snap.Target.Engine == "postgresql" {
		profile, err := loadProfile(ctx, q, run.TargetID)
		if err != nil {
			clear(password)
			return executionPlan{}, err
		}
		plan.caPEM = profile.CAPEM
	}
	current, _ := json.Marshal(snap)
	frozen, _ := json.Marshal(run.RunSnapshot)
	if string(current) != string(frozen) {
		clear(password)
		return executionPlan{}, conflict("Registry, account or credential changed after submission; frozen run will not execute.")
	}
	var pin, configuration string
	var held bool
	if e = q.QueryRowContext(ctx, `SELECT runtime_pin,configuration,held FROM migration_streams WHERE target_id=? AND schema_id=?`, run.TargetID, run.SchemaID).Scan(&pin, &configuration, &held); e != nil {
		clear(password)
		return executionPlan{}, e
	}
	if (held && (len(allowHeld) == 0 || !allowHeld[0])) || pin != s.execution.pin || configuration != streamConfiguration(snap) {
		clear(password)
		return executionPlan{}, conflict("Stream is held or its pinned execution configuration changed.")
	}
	plan.files, e = readStoredFiles(ctx, q, `SELECT f.artifact_id,f.path,f.sha256,f.kind,f.version,f.content FROM migration_run_items i JOIN migration_files f ON f.artifact_id=i.artifact_id AND f.path=i.path WHERE i.run_id=? ORDER BY f.path`, run.ID)
	if e != nil {
		clear(password)
		return executionPlan{}, e
	}
	if len(plan.files) == 0 {
		clear(password)
		return executionPlan{}, conflict("Run has no frozen migration files.")
	}
	if e = validateMigrationPlan(plan); e != nil {
		clear(password)
		return executionPlan{}, e
	}
	return plan, nil
}
func safeExecutionError(e error) string {
	if x, ok := e.(*Error); ok {
		return x.Message
	}
	return "Execution prerequisites could not be verified."
}
func (s *Service) runCommand(processCtx, persistCtx context.Context, rid, command string, plan executionPlan) (flyway.Result, string, error) {
	started := utcNow()
	eid := id()
	req := requestFor(plan, command)
	if req.Engine == "postgresql" {
		req.ExecutionTag = "fwm_" + eid
		// Commit correlation before launch so abrupt restart retains the lookup.
		if _, e := s.db.ExecContext(persistCtx, `INSERT INTO migration_logs(run_id,command,channel,message,created_at) VALUES(?,?,?,?,?)`, rid, command, "stderr", "PostgreSQL execution correlation: "+req.ExecutionTag, started); e != nil {
			return flyway.Result{}, "", e
		}
	}
	result := s.execution.runner.Execute(processCtx, req)
	finished := utcNow()
	if len(result.Lifecycle) > 0 && json.Valid(result.Lifecycle) {
		result.Stderr += "\nPostgreSQL execution lifecycle: " + string(result.Lifecycle)
	}
	payload := "null"
	if json.Valid(result.JSON) {
		payload = string(result.JSON)
	}
	tx, e := s.db.BeginTx(persistCtx, nil)
	if e != nil {
		return result, "", e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(persistCtx, `INSERT INTO run_executions VALUES(?,?,?,?,?,?,?,?,?,?,?)`, eid, rid, command, result.Success, result.ExitCode, result.Stdout, result.Stderr, payload, started, finished, result.Ambiguous); e != nil {
		return result, "", e
	}
	var logBytes int64
	var logCount int
	if e = tx.QueryRowContext(persistCtx, `SELECT COALESCE(SUM(length(CAST(message AS BLOB))),0),count(*) FROM migration_logs WHERE run_id=?`, rid).Scan(&logBytes, &logCount); e != nil {
		return result, "", e
	}
	for _, stream := range []struct{ channel, text string }{{"stdout", result.Stdout}, {"stderr", result.Stderr}} {
		for _, line := range strings.Split(stream.text, "\n") {
			if line == "" {
				continue
			}
			if logCount >= 10000 || logBytes+int64(len(line)) > 50<<20 {
				break
			}
			if _, e = tx.ExecContext(persistCtx, `INSERT INTO migration_logs(run_id,command,channel,message,created_at) VALUES(?,?,?,?,?)`, rid, command, stream.channel, line, finished); e != nil {
				return result, "", e
			}
			logBytes += int64(len(line))
			logCount++
		}
	}
	if _, e = tx.ExecContext(persistCtx, `INSERT INTO audit_events(actor_id,actor_type,actor_name,action,target,timestamp,outcome,request_id) SELECT actor_id,actor_kind,actor_name,?,id,?,?,request_id FROM migration_runs WHERE id=?`, "flyway."+command, now(), fmt.Sprintf("exit=%d success=%t", result.ExitCode, result.Success), rid); e != nil {
		return result, "", e
	}
	return result, eid, tx.Commit()
}
func rawOrNull(p json.RawMessage) string {
	if !json.Valid(p) {
		return "null"
	}
	return string(p)
}
func (s *Service) finishRepair(ctx context.Context, rid, outcome, reason string, before, after json.RawMessage, executionID, state string, hold bool) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `INSERT INTO repair_operations VALUES(?,?,?,?,?,?)`, rid, outcome, reason, rawOrNull(before), rawOrNull(after), executionID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE migration_runs SET status=?,repair_outcome=?,reason=?,finished_at=? WHERE id=?`, state, outcome, reason, now(), rid); e != nil {
		return e
	}
	if hold {
		if _, e = tx.ExecContext(ctx, `UPDATE migration_streams SET held=1,reason=? WHERE (target_id,schema_id)=(SELECT target_id,schema_id FROM migration_runs WHERE id=?)`, reason, rid); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET claimed=0 WHERE run_id=?`, rid); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,actor_type,actor_name,action,target,timestamp,outcome,request_id) SELECT actor_id,actor_kind,actor_name,'migration.repair',id,?,?,request_id FROM migration_runs WHERE id=?`, now(), outcome, rid); e != nil {
		return e
	}
	return tx.Commit()
}
