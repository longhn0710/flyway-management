-- Rebuild the referenced target table to widen its CHECK constraint. The migration
-- runner executes this file on a reserved connection with FK checks disabled before
-- BEGIN, then validates foreign_key_check before commit and restores the PRAGMA.
CREATE TABLE database_targets_sit (
 id TEXT PRIMARY KEY, name TEXT NOT NULL COLLATE NOCASE UNIQUE,
 environment TEXT NOT NULL CHECK(environment IN ('DEV','SIT','UAT','PROD')),
 host TEXT NOT NULL COLLATE NOCASE, port INTEGER NOT NULL CHECK(port BETWEEN 1 AND 65535),
 service TEXT NOT NULL COLLATE NOCASE, transport TEXT NOT NULL CHECK(transport IN ('TCP','TCPS')),
 active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)), created_at INTEGER NOT NULL,
 UNIQUE(host,port,service)
);
INSERT INTO database_targets_sit(id,name,environment,host,port,service,transport,active,created_at)
 SELECT id,name,environment,host,port,service,transport,active,created_at FROM database_targets;
DROP TABLE database_targets;
ALTER TABLE database_targets_sit RENAME TO database_targets;
