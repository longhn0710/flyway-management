CREATE TABLE projects (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL COLLATE NOCASE UNIQUE,
 description TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK(status IN ('active','archived')),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE TABLE project_members (
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 target_id TEXT NOT NULL REFERENCES database_targets(id) ON DELETE CASCADE,
 schema_id TEXT,
 PRIMARY KEY(project_id,target_id,schema_id),
 FOREIGN KEY(schema_id,target_id) REFERENCES target_schemas(id,target_id)
);
CREATE INDEX project_members_scope ON project_members(target_id,schema_id,project_id);
