export type RunStatus =
  | "RUNNING"
  | "FAILED"
  | "SUCCEEDED"
  | "QUEUED"
  | "BLOCKED"
  | "NEEDS_REVIEW"
  | "CANCELLED"
  | "REPAIRING";
export type RepairStatus =
  "SUCCEEDED" | "UNSAFE" | "NOT_REQUIRED" | "NOT_APPLICABLE";
export interface MigrationRun {
  id: string;
  source: "WEB" | "API";
  status: RunStatus;
  repair: RepairStatus;
  targetId: string;
  target: string;
  environment: string;
  service: string;
  schema: string;
  artifactId: string;
  artifact: string;
  actor: string;
  actorType: "Human User" | "Service Account";
  account: string;
  startedAt: string;
  duration: string;
  version: string;
}
export interface DatabaseTarget {
  id: string;
  name: string;
  environment: string;
  host: string;
  service: string;
  transport: string;
  schemas: string[];
}
export interface Health {
  status: string;
  database: string;
  schema_version: number;
  installation_id: string;
  capabilities: {
    authentication: boolean;
    migration_execution: boolean;
    registry: boolean;
  };
}
export interface ApiResponse<T> {
  data: T;
}
export interface ApiErrorBody {
  error: { code: string; message: string };
}
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}
export async function getHealth(signal?: AbortSignal): Promise<Health> {
  const response = await fetch("/api/v1/health", {
    signal,
    headers: { Accept: "application/json" },
  });
  if (!response.ok) {
    const body = (await response
      .json()
      .catch(() => null)) as ApiErrorBody | null;
    throw new ApiError(
      response.status,
      body?.error?.code ?? "HTTP_ERROR",
      body?.error?.message ?? "Không kết nối được API",
    );
  }
  const body = (await response.json()) as ApiResponse<Health>;
  if (!body?.data || typeof body.data.installation_id !== "string")
    throw new Error("Phản hồi health không đúng hợp đồng");
  return body.data;
}
export interface BundleFile {
  name: string;
  size: number;
  /** Computed in this browser for display only; never a server validation result. */
  localChecksum?: string;
}
export interface MigrationDraft {
  targetId: string;
  schema: string;
  account: string;
  files: BundleFile[];
  fixture: boolean;
}
