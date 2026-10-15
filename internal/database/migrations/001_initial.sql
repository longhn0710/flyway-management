CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS installation_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
