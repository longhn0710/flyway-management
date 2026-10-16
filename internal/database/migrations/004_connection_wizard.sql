-- Wizard connections reuse the registry and existing account mappings. Rows are
-- never removed from immutable run/history tables when this allowlist changes.
CREATE TABLE connection_settings (
 target_id TEXT PRIMARY KEY REFERENCES database_targets(id),
 account_id TEXT NOT NULL,
 timeout_seconds INTEGER NOT NULL CHECK(timeout_seconds BETWEEN 5 AND 30),
 FOREIGN KEY(account_id,target_id) REFERENCES execution_accounts(id,target_id)
);
CREATE TABLE connection_managed_accounts (
 target_id TEXT NOT NULL REFERENCES database_targets(id),
 account_id TEXT NOT NULL,
 PRIMARY KEY(target_id,account_id),
 FOREIGN KEY(account_id,target_id) REFERENCES execution_accounts(id,target_id)
);
CREATE TABLE connection_allowed_schemas (
 target_id TEXT NOT NULL,
 account_id TEXT NOT NULL,
 schema_id TEXT NOT NULL,
 capability TEXT NOT NULL CHECK(capability IN ('potential','unknown')),
 acknowledged INTEGER NOT NULL CHECK(acknowledged IN (0,1)),
 PRIMARY KEY(target_id,account_id,schema_id),
 FOREIGN KEY(target_id,account_id) REFERENCES connection_managed_accounts(target_id,account_id),
 FOREIGN KEY(schema_id,target_id) REFERENCES target_schemas(id,target_id)
);
