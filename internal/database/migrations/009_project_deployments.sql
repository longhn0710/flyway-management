-- Additive ownership; existing projects, streams and evidence are not reassigned.
CREATE TABLE project_identity_memberships (
 project_id TEXT NOT NULL REFERENCES projects(id),
 user_id TEXT REFERENCES users(id), service_id TEXT REFERENCES service_accounts(id),
 CHECK((user_id IS NULL) <> (service_id IS NULL))
);
CREATE UNIQUE INDEX project_user ON project_identity_memberships(project_id,user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX project_service ON project_identity_memberships(project_id,service_id) WHERE service_id IS NOT NULL;
CREATE TABLE project_stream_owners (
 target_id TEXT NOT NULL, schema_id TEXT NOT NULL, project_id TEXT NOT NULL REFERENCES projects(id),
 PRIMARY KEY(target_id,schema_id), UNIQUE(target_id,schema_id,project_id),
 FOREIGN KEY(schema_id,target_id) REFERENCES target_schemas(id,target_id)
);
CREATE TABLE deployment_targets (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), name TEXT NOT NULL COLLATE NOCASE,
 environment TEXT NOT NULL CHECK(environment IN ('DEV','SIT','UAT','PROD')),
 target_id TEXT NOT NULL, schema_id TEXT NOT NULL, execution_account_id TEXT NOT NULL,
 active INTEGER NOT NULL CHECK(active IN (0,1)), revision INTEGER NOT NULL CHECK(revision>0),
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 UNIQUE(project_id,name), UNIQUE(id,project_id),
 FOREIGN KEY(target_id,schema_id,project_id) REFERENCES project_stream_owners(target_id,schema_id,project_id),
 FOREIGN KEY(execution_account_id,target_id) REFERENCES execution_accounts(id,target_id)
);
CREATE TRIGGER deployment_identity BEFORE UPDATE ON deployment_targets WHEN NEW.id<>OLD.id OR NEW.project_id<>OLD.project_id OR NEW.target_id<>OLD.target_id OR NEW.schema_id<>OLD.schema_id OR NEW.execution_account_id<>OLD.execution_account_id OR NEW.environment<>OLD.environment BEGIN SELECT RAISE(ABORT,'immutable deployment destination'); END;
CREATE TABLE deployment_grants (
 deployment_target_id TEXT NOT NULL REFERENCES deployment_targets(id),
 user_id TEXT REFERENCES users(id), service_id TEXT REFERENCES service_accounts(id),
 can_read INTEGER NOT NULL CHECK(can_read IN (0,1)), can_upload INTEGER NOT NULL CHECK(can_upload IN (0,1)),
 can_preflight INTEGER NOT NULL CHECK(can_preflight IN (0,1)), can_execute INTEGER NOT NULL CHECK(can_execute IN (0,1)),
 CHECK((user_id IS NULL) <> (service_id IS NULL)), CHECK(can_read=1 OR (can_upload=0 AND can_preflight=0 AND can_execute=0))
);
CREATE UNIQUE INDEX deployment_user ON deployment_grants(deployment_target_id,user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX deployment_service ON deployment_grants(deployment_target_id,service_id) WHERE service_id IS NOT NULL;
CREATE TABLE token_deployment_scopes (
 token_id TEXT NOT NULL REFERENCES api_tokens(id), deployment_target_id TEXT NOT NULL REFERENCES deployment_targets(id),
 can_read INTEGER NOT NULL CHECK(can_read IN (0,1)), can_upload INTEGER NOT NULL CHECK(can_upload IN (0,1)),
 can_preflight INTEGER NOT NULL CHECK(can_preflight IN (0,1)), can_execute INTEGER NOT NULL CHECK(can_execute IN (0,1)),
 PRIMARY KEY(token_id,deployment_target_id), CHECK(can_read=1 OR (can_upload=0 AND can_preflight=0 AND can_execute=0))
);
CREATE TABLE project_artifacts (
 artifact_id TEXT PRIMARY KEY REFERENCES artifacts(id), project_id TEXT NOT NULL,
 deployment_target_id TEXT NOT NULL, revision INTEGER NOT NULL, input_sha256 TEXT NOT NULL,
 external_source TEXT NOT NULL, actor_kind TEXT NOT NULL, actor_id TEXT NOT NULL,
 idempotency_key TEXT NOT NULL, request_hash TEXT NOT NULL,
 FOREIGN KEY(deployment_target_id,project_id) REFERENCES deployment_targets(id,project_id),
 UNIQUE(actor_kind,actor_id,project_id,deployment_target_id,idempotency_key),
 UNIQUE(artifact_id,project_id,deployment_target_id)
);
CREATE INDEX project_artifact_history ON project_artifacts(project_id,artifact_id);
CREATE TABLE migration_preflights (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, deployment_target_id TEXT NOT NULL,
 artifact_id TEXT NOT NULL REFERENCES project_artifacts(artifact_id), revision INTEGER NOT NULL,
 actor_kind TEXT NOT NULL, actor_id TEXT NOT NULL, fingerprint TEXT NOT NULL,
 ready INTEGER NOT NULL CHECK(ready IN (0,1)), created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, assessment TEXT NOT NULL,
 FOREIGN KEY(deployment_target_id,project_id) REFERENCES deployment_targets(id,project_id),
 FOREIGN KEY(artifact_id,project_id,deployment_target_id) REFERENCES project_artifacts(artifact_id,project_id,deployment_target_id),
 UNIQUE(id,project_id,deployment_target_id,revision)
);
CREATE TABLE project_migration_runs (
 run_id TEXT PRIMARY KEY REFERENCES migration_runs(id), project_id TEXT NOT NULL,
 deployment_target_id TEXT NOT NULL, revision INTEGER NOT NULL, preflight_id TEXT NOT NULL REFERENCES migration_preflights(id),
 external_source TEXT NOT NULL, request_hash TEXT NOT NULL,
 FOREIGN KEY(deployment_target_id,project_id) REFERENCES deployment_targets(id,project_id),
 FOREIGN KEY(preflight_id,project_id,deployment_target_id,revision) REFERENCES migration_preflights(id,project_id,deployment_target_id,revision)
);
CREATE INDEX project_run_history ON project_migration_runs(project_id,run_id);
CREATE TABLE execution_records (
 run_id TEXT PRIMARY KEY REFERENCES project_migration_runs(run_id), created_at INTEGER NOT NULL, sha256 TEXT NOT NULL, payload TEXT NOT NULL
);
CREATE TRIGGER project_artifacts_update BEFORE UPDATE ON project_artifacts BEGIN SELECT RAISE(ABORT,'immutable artifact attribution'); END;
CREATE TRIGGER project_artifacts_delete BEFORE DELETE ON project_artifacts BEGIN SELECT RAISE(ABORT,'immutable artifact attribution'); END;
CREATE TRIGGER project_runs_update BEFORE UPDATE ON project_migration_runs BEGIN SELECT RAISE(ABORT,'immutable run attribution'); END;
CREATE TRIGGER project_runs_delete BEFORE DELETE ON project_migration_runs BEGIN SELECT RAISE(ABORT,'immutable run attribution'); END;
CREATE TRIGGER preflights_update BEFORE UPDATE ON migration_preflights BEGIN SELECT RAISE(ABORT,'immutable preflight'); END;
CREATE TRIGGER preflights_delete BEFORE DELETE ON migration_preflights BEGIN SELECT RAISE(ABORT,'immutable preflight'); END;
CREATE TRIGGER execution_records_update BEFORE UPDATE ON execution_records BEGIN SELECT RAISE(ABORT,'immutable execution record'); END;
CREATE TRIGGER execution_records_delete BEFORE DELETE ON execution_records BEGIN SELECT RAISE(ABORT,'immutable execution record'); END;
CREATE TRIGGER project_owners_update BEFORE UPDATE ON project_stream_owners BEGIN SELECT RAISE(ABORT,'immutable stream ownership'); END;
CREATE TRIGGER project_owners_delete BEFORE DELETE ON project_stream_owners BEGIN SELECT RAISE(ABORT,'immutable stream ownership'); END;
