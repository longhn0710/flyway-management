import type { DatabaseTarget, TargetSchema } from "./contracts";
import type { MigrationFile } from "./execution";
export interface NativeMigration {
  category?: string;
  version?: string | null;
  description?: string;
  type?: string;
  installedOnUTC?: string;
  installedOn?: string;
  state?: string;
  undoable?: string | boolean;
  installedBy?: string;
  executionTime?: number;
  filepath?: string;
  script?: string;
  [key: string]: unknown;
}
export interface NativeInfo {
  migrations?: NativeMigration[];
  schemaVersion?: string | null;
  flywayVersion?: string;
  database?: string;
  databaseType?: string;
  schemaName?: string;
  [key: string]: unknown;
}
export interface InfoCapture {
  id: string;
  target_id: string;
  schema_id: string;
  source_run_id?: string;
  source: string;
  captured_at: string;
  started_at: string;
  artifact_id: string;
  artifact_sha256: string;
  snapshot: {
    snapshot_version?: number;
    profile?: {
      auth_kind: string;
      tls_mode: string;
      ca_sha256: string;
      revision: number;
    };
    target: DatabaseTarget;
    schema: TargetSchema;
    account: { id: string; name: string; username: string };
  };
  files: MigrationFile[];
  runtime_version: string;
  actor_name: string;
  success: boolean;
  exit_code: number;
  json: NativeInfo | null;
  stdout: string;
  stderr: string;
  warnings: string[];
}
export function infoHref(target: string, schema: string, run?: string) {
  const q = new URLSearchParams({ target, schema });
  if (run) q.set("run_id", run);
  return `/flyway-info?${q}`;
}
export function nativeValue(value: unknown): string {
  return value === undefined || value === null || value === ""
    ? "—"
    : typeof value === "object"
      ? JSON.stringify(value)
      : String(value);
}
