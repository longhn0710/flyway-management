CREATE TABLE users (
 id TEXT PRIMARY KEY, username TEXT NOT NULL COLLATE NOCASE,
 display_name TEXT NOT NULL, provider TEXT NOT NULL CHECK(provider IN ('local','ldap','oidc')),
 issuer TEXT NOT NULL DEFAULT '', subject TEXT NOT NULL,
 password_hash TEXT, role TEXT NOT NULL CHECK(role IN ('Admin','Operator','Viewer')),
 active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)), created_at INTEGER NOT NULL,
 UNIQUE(provider, issuer, subject), UNIQUE(provider,username),
 CHECK((provider='local' AND password_hash IS NOT NULL AND issuer='') OR (provider<>'local' AND password_hash IS NULL))
);
CREATE TABLE sessions (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), secret_hash TEXT NOT NULL UNIQUE,
 csrf_token TEXT NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, last_used_at INTEGER NOT NULL,
 revoked INTEGER NOT NULL DEFAULT 0 CHECK(revoked IN (0,1))
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE database_targets (
 id TEXT PRIMARY KEY, name TEXT NOT NULL COLLATE NOCASE UNIQUE,
 environment TEXT NOT NULL CHECK(environment IN ('DEV','UAT','PROD')),
 host TEXT NOT NULL COLLATE NOCASE, port INTEGER NOT NULL CHECK(port BETWEEN 1 AND 65535),
 service TEXT NOT NULL COLLATE NOCASE, transport TEXT NOT NULL CHECK(transport IN ('TCP','TCPS')),
 active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)), created_at INTEGER NOT NULL,
 UNIQUE(host,port,service)
);
CREATE TABLE execution_accounts (
 id TEXT PRIMARY KEY, target_id TEXT NOT NULL REFERENCES database_targets(id), name TEXT NOT NULL,
 username TEXT NOT NULL COLLATE NOCASE, active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)), created_at INTEGER NOT NULL,
 UNIQUE(target_id,username), UNIQUE(target_id,name), UNIQUE(id,target_id)
);
CREATE TABLE target_schemas (
 id TEXT PRIMARY KEY, target_id TEXT NOT NULL REFERENCES database_targets(id), name TEXT NOT NULL COLLATE NOCASE,
 history_schema TEXT NOT NULL COLLATE NOCASE, history_table TEXT NOT NULL COLLATE NOCASE,
 default_account_id TEXT, active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)), created_at INTEGER NOT NULL,
 UNIQUE(target_id,name), UNIQUE(target_id,history_schema,history_table), UNIQUE(id,target_id),
 FOREIGN KEY(default_account_id,target_id) REFERENCES execution_accounts(id,target_id)
);
CREATE TABLE account_schema_grants (
 account_id TEXT NOT NULL, schema_id TEXT NOT NULL, target_id TEXT NOT NULL,
 PRIMARY KEY(account_id,schema_id),
 FOREIGN KEY(account_id,target_id) REFERENCES execution_accounts(id,target_id),
 FOREIGN KEY(schema_id,target_id) REFERENCES target_schemas(id,target_id)
);
CREATE TABLE credential_secrets (
 account_id TEXT PRIMARY KEY REFERENCES execution_accounts(id), payload BLOB NOT NULL,
 version INTEGER NOT NULL CHECK(version>0), updated_at INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0 CHECK(revoked IN (0,1))
);
CREATE TABLE service_accounts (
 id TEXT PRIMARY KEY, name TEXT NOT NULL COLLATE NOCASE UNIQUE, owner_id TEXT NOT NULL REFERENCES users(id),
 purpose TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)), created_at INTEGER NOT NULL
);
CREATE TABLE api_tokens (
 id TEXT PRIMARY KEY, service_id TEXT NOT NULL REFERENCES service_accounts(id), name TEXT NOT NULL,
 prefix TEXT NOT NULL, secret_hash TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL, last_used_at INTEGER, revoked INTEGER NOT NULL DEFAULT 0 CHECK(revoked IN (0,1))
);
CREATE UNIQUE INDEX token_active_name ON api_tokens(service_id,name) WHERE revoked=0;
CREATE TABLE permission_grants (
 id INTEGER PRIMARY KEY, user_id TEXT REFERENCES users(id), service_id TEXT REFERENCES service_accounts(id),
 target_id TEXT NOT NULL REFERENCES database_targets(id), schema_id TEXT,
 can_read INTEGER NOT NULL CHECK(can_read IN (0,1)), can_operate INTEGER NOT NULL CHECK(can_operate IN (0,1)),
 allow_prod INTEGER NOT NULL CHECK(allow_prod IN (0,1)),
 CHECK((user_id IS NOT NULL AND service_id IS NULL) OR (service_id IS NOT NULL AND user_id IS NULL)),
 CHECK(can_read=1 OR can_operate=0), CHECK(can_operate=1 OR allow_prod=0),
 FOREIGN KEY(schema_id,target_id) REFERENCES target_schemas(id,target_id)
);
CREATE UNIQUE INDEX user_scope ON permission_grants(user_id,target_id,IFNULL(schema_id,'')) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX service_scope ON permission_grants(service_id,target_id,IFNULL(schema_id,'')) WHERE service_id IS NOT NULL;
CREATE TABLE audit_events (
 id INTEGER PRIMARY KEY, actor_id TEXT NOT NULL, actor_type TEXT NOT NULL, actor_name TEXT NOT NULL,
 action TEXT NOT NULL, target TEXT NOT NULL, timestamp INTEGER NOT NULL, outcome TEXT NOT NULL, request_id TEXT NOT NULL
);
CREATE INDEX audit_time ON audit_events(timestamp,id);
CREATE TABLE auth_settings (id INTEGER PRIMARY KEY CHECK(id=1), payload BLOB NOT NULL);
CREATE TABLE oidc_flows (state_hash TEXT PRIMARY KEY, payload BLOB NOT NULL, expires_at INTEGER NOT NULL);
