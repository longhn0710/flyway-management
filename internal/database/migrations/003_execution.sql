-- Additive only: Phase 1/2 records and migration checksums remain unchanged.
CREATE TABLE artifacts (
 id TEXT PRIMARY KEY, target_id TEXT NOT NULL, schema_id TEXT NOT NULL,
 uploader_id TEXT NOT NULL, uploader_kind TEXT NOT NULL, uploader_name TEXT NOT NULL,
 created_at INTEGER NOT NULL, sha256 TEXT NOT NULL, total_bytes INTEGER NOT NULL, manifest TEXT NOT NULL,
 FOREIGN KEY(schema_id,target_id) REFERENCES target_schemas(id,target_id)
);
CREATE TABLE artifact_blobs (
 artifact_id TEXT NOT NULL REFERENCES artifacts(id), input_index INTEGER NOT NULL,
 name TEXT NOT NULL, sha256 TEXT NOT NULL, content BLOB NOT NULL,
 PRIMARY KEY(artifact_id,input_index)
);
CREATE TABLE migration_files (
 artifact_id TEXT NOT NULL REFERENCES artifacts(id), path TEXT NOT NULL,
 sha256 TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('versioned','repeatable')),
 version TEXT NOT NULL, content BLOB NOT NULL, PRIMARY KEY(artifact_id,path)
);
CREATE TABLE migration_streams (
 target_id TEXT NOT NULL, schema_id TEXT NOT NULL, configuration TEXT NOT NULL,
 runtime_pin TEXT NOT NULL, held INTEGER NOT NULL DEFAULT 0 CHECK(held IN (0,1)), reason TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(target_id,schema_id), FOREIGN KEY(schema_id,target_id) REFERENCES target_schemas(id,target_id)
);
CREATE TABLE stream_files (
 target_id TEXT NOT NULL, schema_id TEXT NOT NULL, migration_key TEXT NOT NULL,
 artifact_id TEXT NOT NULL, path TEXT NOT NULL,
 PRIMARY KEY(target_id,schema_id,migration_key),
 FOREIGN KEY(target_id,schema_id) REFERENCES migration_streams(target_id,schema_id),
 FOREIGN KEY(artifact_id,path) REFERENCES migration_files(artifact_id,path)
);
CREATE TABLE migration_runs (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 target_id TEXT NOT NULL, schema_id TEXT NOT NULL, artifact_id TEXT NOT NULL REFERENCES artifacts(id),
 execution_account_id TEXT NOT NULL REFERENCES execution_accounts(id),
 actor_id TEXT NOT NULL, actor_kind TEXT NOT NULL, actor_name TEXT NOT NULL,
 actor_role TEXT NOT NULL, actor_session TEXT NOT NULL, actor_token TEXT NOT NULL,
 source TEXT NOT NULL CHECK(source IN ('WEB','API')), request_id TEXT NOT NULL,
 request TEXT NOT NULL, snapshot TEXT NOT NULL, runtime_pin TEXT NOT NULL,
 created_at INTEGER NOT NULL, started_at INTEGER, finished_at INTEGER,
 status TEXT NOT NULL CHECK(status IN ('QUEUED','PREFLIGHT','RUNNING','REPAIRING','SUCCEEDED','FAILED','BLOCKED','NEEDS_REVIEW','CANCELLED')),
 migration_outcome TEXT NOT NULL DEFAULT '', repair_outcome TEXT NOT NULL DEFAULT 'SKIPPED',
 reason TEXT NOT NULL DEFAULT '', flyway_version TEXT NOT NULL, exit_code INTEGER, preflight TEXT,
 FOREIGN KEY(target_id,schema_id) REFERENCES migration_streams(target_id,schema_id)
);
CREATE INDEX runs_history ON migration_runs(created_at DESC,sequence DESC);
CREATE UNIQUE INDEX one_active_stream ON migration_runs(target_id,schema_id) WHERE status IN ('PREFLIGHT','RUNNING','REPAIRING');
CREATE TABLE jobs (run_id TEXT PRIMARY KEY REFERENCES migration_runs(id), claimed INTEGER NOT NULL DEFAULT 0 CHECK(claimed IN (0,1)));
CREATE TABLE migration_run_items (
 run_id TEXT NOT NULL REFERENCES migration_runs(id), artifact_id TEXT NOT NULL, path TEXT NOT NULL,
 PRIMARY KEY(run_id,path), FOREIGN KEY(artifact_id,path) REFERENCES migration_files(artifact_id,path)
);
CREATE TABLE idempotency_records (
 actor_kind TEXT NOT NULL, actor_id TEXT NOT NULL, key TEXT NOT NULL,
 request_hash TEXT NOT NULL, run_id TEXT NOT NULL REFERENCES migration_runs(id),
 PRIMARY KEY(actor_kind,actor_id,key)
);
CREATE TABLE run_executions (
 id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES migration_runs(id), command TEXT NOT NULL,
 success INTEGER NOT NULL, exit_code INTEGER NOT NULL, stdout TEXT NOT NULL, stderr TEXT NOT NULL,
 result TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT NOT NULL, ambiguous INTEGER NOT NULL
);
CREATE TABLE migration_logs (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL REFERENCES migration_runs(id),
 command TEXT NOT NULL, channel TEXT NOT NULL, message TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE INDEX run_logs ON migration_logs(run_id,sequence);
CREATE TABLE repair_operations (
 run_id TEXT PRIMARY KEY REFERENCES migration_runs(id), outcome TEXT NOT NULL CHECK(outcome IN ('SUCCEEDED','FAILED','SKIPPED','UNSAFE')),
 reason TEXT NOT NULL, before_history TEXT NOT NULL, after_history TEXT NOT NULL, execution_id TEXT NOT NULL DEFAULT ''
);
CREATE TRIGGER run_state_machine BEFORE UPDATE OF status ON migration_runs
WHEN OLD.status <> NEW.status AND NOT (
 (OLD.status='QUEUED' AND NEW.status IN ('PREFLIGHT','BLOCKED','CANCELLED')) OR
 (OLD.status='PREFLIGHT' AND NEW.status IN ('RUNNING','BLOCKED','NEEDS_REVIEW')) OR
 (OLD.status='RUNNING' AND NEW.status IN ('SUCCEEDED','FAILED','NEEDS_REVIEW')) OR
 (OLD.status='FAILED' AND NEW.status IN ('REPAIRING','NEEDS_REVIEW','BLOCKED')) OR
 (OLD.status='REPAIRING' AND NEW.status IN ('FAILED','NEEDS_REVIEW','BLOCKED'))
) BEGIN SELECT RAISE(ABORT,'illegal migration run transition'); END;
CREATE TRIGGER run_snapshot_immutable BEFORE UPDATE OF id,target_id,schema_id,artifact_id,execution_account_id,actor_id,actor_kind,actor_name,actor_role,actor_session,actor_token,source,request_id,request,snapshot,runtime_pin,created_at,flyway_version ON migration_runs
BEGIN SELECT RAISE(ABORT,'immutable run snapshot'); END;
CREATE TRIGGER artifact_immutable_update BEFORE UPDATE ON artifacts BEGIN SELECT RAISE(ABORT,'immutable artifact'); END;
CREATE TRIGGER artifact_immutable_delete BEFORE DELETE ON artifacts BEGIN SELECT RAISE(ABORT,'immutable artifact'); END;
CREATE TRIGGER blob_immutable_update BEFORE UPDATE ON artifact_blobs BEGIN SELECT RAISE(ABORT,'immutable artifact input'); END;
CREATE TRIGGER blob_immutable_delete BEFORE DELETE ON artifact_blobs BEGIN SELECT RAISE(ABORT,'immutable artifact input'); END;
CREATE TRIGGER file_immutable_update BEFORE UPDATE ON migration_files BEGIN SELECT RAISE(ABORT,'immutable migration file'); END;
CREATE TRIGGER file_immutable_delete BEFORE DELETE ON migration_files BEGIN SELECT RAISE(ABORT,'immutable migration file'); END;
CREATE TRIGGER item_immutable_update BEFORE UPDATE ON migration_run_items BEGIN SELECT RAISE(ABORT,'immutable run files'); END;
CREATE TRIGGER item_immutable_delete BEFORE DELETE ON migration_run_items BEGIN SELECT RAISE(ABORT,'immutable run files'); END;
CREATE TRIGGER execution_immutable_update BEFORE UPDATE ON run_executions BEGIN SELECT RAISE(ABORT,'immutable execution evidence'); END;
CREATE TRIGGER execution_immutable_delete BEFORE DELETE ON run_executions BEGIN SELECT RAISE(ABORT,'immutable execution evidence'); END;
CREATE TRIGGER log_immutable_update BEFORE UPDATE ON migration_logs BEGIN SELECT RAISE(ABORT,'immutable execution log'); END;
CREATE TRIGGER log_immutable_delete BEFORE DELETE ON migration_logs BEGIN SELECT RAISE(ABORT,'immutable execution log'); END;
-- Frozen queued locations depend on earlier queued work; failed/cancelled SQL
-- must never become an implicit retry merely because a later upload adds Vn.
CREATE TABLE run_dependencies (
 run_id TEXT NOT NULL REFERENCES migration_runs(id), predecessor_id TEXT NOT NULL REFERENCES migration_runs(id),
 PRIMARY KEY(run_id,predecessor_id), CHECK(run_id<>predecessor_id)
);
CREATE TRIGGER repair_immutable_update BEFORE UPDATE ON repair_operations BEGIN SELECT RAISE(ABORT,'immutable repair evidence'); END;
CREATE TRIGGER repair_immutable_delete BEFORE DELETE ON repair_operations BEGIN SELECT RAISE(ABORT,'immutable repair evidence'); END;
