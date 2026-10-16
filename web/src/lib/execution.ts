import type {
  DatabaseTarget,
  ExecutionAccount,
  TargetSchema,
} from "./contracts";

export type RunStatus =
  | "QUEUED"
  | "PREFLIGHT"
  | "RUNNING"
  | "REPAIRING"
  | "SUCCEEDED"
  | "FAILED"
  | "BLOCKED"
  | "NEEDS_REVIEW"
  | "CANCELLED";
export interface RuntimeStatus {
  ready: boolean;
  version: string;
  blockers: string[];
  integrity: string;
  dependency: string;
}
export interface RunInput {
  target_id: string;
  schema_id: string;
  execution_account_id: string;
  artifact_id: string;
  context?: {
    external_run_id?: string;
    commit_sha?: string;
    initiated_by?: string;
  };
}
export interface MigrationFile {
  path: string;
  sha256: string;
  kind: "versioned" | "repeatable";
  version?: string;
  size: number;
}
export interface Artifact {
  id: string;
  target_id: string;
  schema_id: string;
  uploader_id: string;
  uploader_kind: string;
  uploader_name: string;
  created_at: string;
  sha256: string;
  total_bytes: number;
  files: MigrationFile[];
  inputs: { name: string; sha256: string; size: number }[];
}
export interface FlywayMigrationPreview {
  category?: string;
  version?: string;
  description?: string;
  filepath?: string;
  script?: string;
  state?: string;
}
export interface Preflight {
  ready: boolean;
  local_ready: boolean;
  live_status: "VERIFIED" | "NOT_VERIFIED" | "FAILED";
  blockers: { code: string; message: string }[];
  warnings: string[];
  runtime_version: string;
  files: MigrationFile[];
  diagnostics: { name: string; status: string; detail: string }[];
  info?: { migrations?: FlywayMigrationPreview[] };
}
export interface Run {
  id: string;
  run_id: string;
  status: RunStatus;
  target_id: string;
  schema_id: string;
  artifact_id: string;
  artifact_name?: string;
  execution_account_id: string;
  actor_id: string;
  actor_kind: string;
  actor_name: string;
  source: "WEB" | "API";
  created_at: string;
  started_at: string;
  finished_at: string;
  flyway_version: string;
  exit_code: number | null;
  migration_outcome: string;
  repair_outcome: string;
  reason: string;
  target: DatabaseTarget;
  schema: TargetSchema;
  account: Pick<ExecutionAccount, "id" | "name" | "username">;
  context: Record<string, string>;
  preflight?: Preflight;
}
export interface RunLog {
  sequence: number;
  command: string;
  channel: string;
  message: string;
  created_at: string;
}
export interface RunExecution {
  id: string;
  command: string;
  success: boolean;
  exit_code: number | null;
  stdout: string;
  stderr: string;
  json?: unknown;
  started_at: string;
  finished_at: string;
}
export interface RepairEvidence {
  outcome: string;
  reason: string;
  before: unknown;
  after: unknown;
  execution_id: string;
}
