package core

import (
	"context"
	"database/sql"
	"encoding/json"
)

func (s *Service) DeploymentGrants(ctx context.Context, a Actor, p, d string) ([]DeploymentGrantInput, error) {
	if e := requireAdmin(a); e != nil {
		return nil, e
	}
	if _, e := s.DeploymentTarget(ctx, a, p, d, "read"); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT CASE WHEN user_id IS NOT NULL THEN 'user' ELSE 'service' END,COALESCE(user_id,service_id),can_read,can_upload,can_preflight,can_execute FROM deployment_grants WHERE deployment_target_id=? ORDER BY user_id,service_id`, d)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []DeploymentGrantInput{}
	for rows.Next() {
		var x DeploymentGrantInput
		if e = rows.Scan(&x.PrincipalKind, &x.PrincipalID, &x.Read, &x.Upload, &x.Preflight, &x.Execute); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) workerProjectAdmission(ctx context.Context, q queryer, a Actor, run Run) error {
	var p, d string
	var revision int
	e := q.QueryRowContext(ctx, `SELECT project_id,deployment_target_id,revision FROM project_migration_runs WHERE run_id=?`, run.ID).Scan(&p, &d, &revision)
	if e == sql.ErrNoRows {
		return legacyStreamAllowed(ctx, q, run.TargetID, run.SchemaID)
	}
	if e != nil {
		return e
	}
	dest, e := s.deploymentAccess(ctx, q, a, p, d, "execute")
	if e != nil {
		return e
	}
	if dest.Revision != revision {
		return conflict("Deployment Target changed after submission.")
	}
	return nil
}

// Freeze only settled jobs, inside the same transaction that releases them. A
// later reconciliation is new evidence and cannot rewrite the original record.
func (s *Service) freezeCompletedRecords(ctx context.Context, tx *sql.Tx) error {
	rows, e := tx.QueryContext(ctx, `SELECT r.id FROM migration_runs r JOIN project_migration_runs p ON p.run_id=r.id JOIN jobs j ON j.run_id=r.id WHERE r.status IN ('SUCCEEDED','FAILED','BLOCKED','NEEDS_REVIEW','CANCELLED') AND j.claimed=0 AND r.repair_outcome<>'PENDING' AND NOT EXISTS(SELECT 1 FROM execution_records e WHERE e.run_id=r.id)`)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var r string
		if e = rows.Scan(&r); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, rid := range ids {
		if e = s.freezeRecord(ctx, tx, rid); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) freezeRecord(ctx context.Context, tx *sql.Tx, rid string) error {
	run, e := runScan(tx.QueryRowContext(ctx, "SELECT "+runColumns+" FROM migration_runs WHERE id=?", rid))
	if e != nil {
		return e
	}
	var source string
	if e = tx.QueryRowContext(ctx, `SELECT project_id,deployment_target_id,revision,external_source FROM project_migration_runs WHERE run_id=?`, rid).Scan(&run.ProjectID, &run.DeploymentTargetID, &run.DeploymentTargetRevision, &source); e != nil {
		return e
	}
	if e = json.Unmarshal([]byte(source), &run.ExternalSource); e != nil {
		return e
	}
	if run.MigrationOutcome == "FAILED" {
		run.Failure, e = runMigrationFailureFrom(ctx, tx, rid, run.FlywayVersion)
		if e != nil {
			return e
		}
	}
	run.Completed = true
	run.RecordAvailable = true
	run.StatusURL = "/api/v2/projects/" + run.ProjectID + "/migration-runs/" + rid
	run.LogsURL = run.StatusURL + "/logs"
	logs := []RunLog{}
	rows, e := tx.QueryContext(ctx, `SELECT sequence,command,channel,message,created_at FROM migration_logs WHERE run_id=? ORDER BY sequence`, rid)
	if e != nil {
		return e
	}
	for rows.Next() {
		var x RunLog
		if e = rows.Scan(&x.Sequence, &x.Command, &x.Channel, &x.Message, &x.CreatedAt); e != nil {
			rows.Close()
			return e
		}
		logs = append(logs, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	executions := []RunExecution{}
	rows, e = tx.QueryContext(ctx, `SELECT id,command,success,exit_code,stdout,stderr,result,started_at,finished_at,ambiguous FROM run_executions WHERE run_id=? ORDER BY started_at,id`, rid)
	if e != nil {
		return e
	}
	for rows.Next() {
		var x RunExecution
		var result string
		if e = rows.Scan(&x.ID, &x.Command, &x.Success, &x.ExitCode, &x.Stdout, &x.Stderr, &result, &x.StartedAt, &x.FinishedAt, &x.Ambiguous); e != nil {
			rows.Close()
			return e
		}
		if json.Valid([]byte(result)) {
			x.JSON = json.RawMessage(result)
		}
		executions = append(executions, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	repair := Repair{Outcome: run.RepairOutcome, Reason: "No completed Repair operation.", Before: json.RawMessage("null"), After: json.RawMessage("null")}
	var before, after string
	e = tx.QueryRowContext(ctx, `SELECT outcome,reason,before_history,after_history,execution_id FROM repair_operations WHERE run_id=?`, rid).Scan(&repair.Outcome, &repair.Reason, &before, &after, &repair.ExecutionID)
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	if e == nil {
		repair.Before = json.RawMessage(before)
		repair.After = json.RawMessage(after)
	}
	artifacts := []json.RawMessage{}
	rows, e = tx.QueryContext(ctx, `SELECT manifest FROM artifacts WHERE id IN(SELECT artifact_id FROM migration_run_items WHERE run_id=?) ORDER BY id`, rid)
	if e != nil {
		return e
	}
	for rows.Next() {
		var payload string
		if e = rows.Scan(&payload); e != nil {
			rows.Close()
			return e
		}
		artifacts = append(artifacts, json.RawMessage(payload))
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	// Native command evidence remains verbatim after the existing runner's redaction.
	audits := []map[string]any{}
	rows, e = tx.QueryContext(ctx, `SELECT id,actor_id,actor_type,actor_name,action,timestamp,outcome,request_id FROM audit_events WHERE target=? ORDER BY id`, rid)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, time int64
		var actor, kind, name, action, outcome, request string
		if e = rows.Scan(&id, &actor, &kind, &name, &action, &time, &outcome, &request); e != nil {
			rows.Close()
			return e
		}
		audits = append(audits, map[string]any{"id": id, "actor_id": actor, "actor_kind": kind, "actor_name": name, "action": action, "timestamp": stamp(time), "outcome": outcome, "request_id": request})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	payload, e := json.Marshal(map[string]any{"record_version": 1, "finalized_at": stamp(now()), "run": run, "logs": logs, "executions": executions, "repair": repair, "artifacts": artifacts, "audit": audits})
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO execution_records VALUES(?,?,?,?)`, rid, now(), digest(string(payload)), string(payload))
	return e
}
func (s *Service) ScopedToken(ctx context.Context, a Actor) (bool, error) {
	if a.Kind != "service" {
		return false, nil
	}
	var n int
	e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM token_deployment_scopes WHERE token_id=?`, a.TokenID).Scan(&n)
	return n > 0, e
}
