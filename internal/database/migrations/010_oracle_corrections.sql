-- A single-use human approval references an immutable correction review preflight.
-- Original artifacts, run items, execution records and Repair evidence are untouched.
CREATE TABLE oracle_correction_approvals (
 id TEXT PRIMARY KEY,
 preflight_id TEXT NOT NULL UNIQUE REFERENCES migration_preflights(id),
 failed_run_id TEXT NOT NULL REFERENCES project_migration_runs(run_id),
 project_id TEXT NOT NULL,
 deployment_target_id TEXT NOT NULL,
 input_sha256 TEXT NOT NULL CHECK(length(input_sha256)=64),
 approved_by TEXT NOT NULL REFERENCES users(id),
 note TEXT NOT NULL,
 created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL,
 consumed_run_id TEXT UNIQUE REFERENCES project_migration_runs(run_id),
 FOREIGN KEY(deployment_target_id,project_id) REFERENCES deployment_targets(id,project_id)
);
CREATE INDEX oracle_correction_lookup ON oracle_correction_approvals(deployment_target_id,input_sha256,expires_at);
CREATE TRIGGER correction_approval_update BEFORE UPDATE ON oracle_correction_approvals
 WHEN NEW.id<>OLD.id OR NEW.preflight_id<>OLD.preflight_id OR NEW.failed_run_id<>OLD.failed_run_id
 OR NEW.project_id<>OLD.project_id OR NEW.deployment_target_id<>OLD.deployment_target_id
 OR NEW.input_sha256<>OLD.input_sha256 OR NEW.approved_by<>OLD.approved_by OR NEW.note<>OLD.note
 OR NEW.created_at<>OLD.created_at OR NEW.expires_at<>OLD.expires_at
 OR OLD.consumed_run_id IS NOT NULL OR NEW.consumed_run_id IS NULL
 BEGIN SELECT RAISE(ABORT,'immutable correction approval; consumption is once only'); END;
CREATE TRIGGER correction_approval_delete BEFORE DELETE ON oracle_correction_approvals
 BEGIN SELECT RAISE(ABORT,'immutable correction approval'); END;
