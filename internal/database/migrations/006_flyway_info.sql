-- Inspection evidence is separate from migration jobs and never changes run snapshots.
CREATE TABLE flyway_info_captures (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 target_id TEXT NOT NULL,
 schema_id TEXT NOT NULL,
 captured_at TEXT NOT NULL,
 record TEXT NOT NULL,
 FOREIGN KEY(schema_id,target_id) REFERENCES target_schemas(id,target_id)
);
CREATE INDEX info_stream_capture ON flyway_info_captures(target_id,schema_id,sequence);
CREATE TRIGGER info_capture_immutable_update BEFORE UPDATE ON flyway_info_captures BEGIN SELECT RAISE(ABORT,'immutable info evidence'); END;
CREATE TRIGGER info_capture_immutable_delete BEFORE DELETE ON flyway_info_captures BEGIN SELECT RAISE(ABORT,'immutable info evidence'); END;
