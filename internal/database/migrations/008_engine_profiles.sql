-- Rebuild the referenced target table while retaining every legacy Oracle field.
-- The migration runner disables FK enforcement on its reserved connection before
-- opening the transaction, then checks all child references before commit.
CREATE TABLE database_targets_engine (
 id TEXT PRIMARY KEY, name TEXT NOT NULL COLLATE NOCASE UNIQUE,
 environment TEXT NOT NULL CHECK(environment IN ('DEV','SIT','UAT','PROD')),
 host TEXT NOT NULL COLLATE NOCASE, port INTEGER NOT NULL CHECK(port BETWEEN 1 AND 65535),
 service TEXT NOT NULL COLLATE NOCASE, transport TEXT NOT NULL CHECK(transport IN ('TCP','TCPS')),
 active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)), created_at INTEGER NOT NULL,
 engine TEXT NOT NULL DEFAULT 'oracle' CHECK(engine IN ('oracle','postgresql')),
 database_name TEXT NOT NULL DEFAULT '',
 profile_revision INTEGER NOT NULL DEFAULT 1 CHECK(profile_revision > 0),
 CHECK((engine='oracle' AND database_name='') OR (engine='postgresql' AND service='' AND database_name<>''))
);
INSERT INTO database_targets_engine(id,name,environment,host,port,service,transport,active,created_at,engine,database_name,profile_revision)
 SELECT id,name,environment,host,port,service,transport,active,created_at,'oracle','',1 FROM database_targets;
DROP TABLE database_targets;
ALTER TABLE database_targets_engine RENAME TO database_targets;
CREATE UNIQUE INDEX database_targets_oracle_endpoint_uq ON database_targets(host,port,service) WHERE engine='oracle';
CREATE UNIQUE INDEX database_targets_postgresql_endpoint_uq ON database_targets(host,port,database_name) WHERE engine='postgresql';
CREATE TABLE target_connection_profiles (
 target_id TEXT PRIMARY KEY REFERENCES database_targets(id) ON DELETE CASCADE,
 auth_kind TEXT NOT NULL CHECK(auth_kind='password'),
 tls_mode TEXT NOT NULL CHECK(tls_mode='verify-full'),
 ca_pem TEXT NOT NULL CHECK(length(trim(ca_pem)) > 0),
 ca_sha256 TEXT NOT NULL CHECK(length(ca_sha256)=64),
 revision INTEGER NOT NULL CHECK(revision > 0)
);
