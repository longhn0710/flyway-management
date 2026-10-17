export type Role = "Admin" | "Operator" | "Viewer";
export type Provider = "local" | "ldap" | "oidc";
export interface Grant {
  target_id: string;
  schema_id: string;
  read: boolean;
  operate: boolean;
  prod: boolean;
}
export interface User {
  id: string;
  username: string;
  display_name: string;
  provider: Provider;
  issuer: string;
  subject: string;
  role: Role;
  active: boolean;
  created_at: string;
}
export interface Identity {
  user: User;
  grants: Grant[];
  csrf_token: string;
  expires_at: string;
}
export interface AuthStatus {
  bootstrap_required: boolean;
  providers: Provider[];
  bootstrap_token_file?: string;
}
export interface DatabaseTarget {
  engine?: "oracle" | "postgresql";
  database_name?: string;
  profile_revision?: number;
  id: string;
  name: string;
  environment: "DEV" | "SIT" | "UAT" | "PROD";
  host: string;
  port: number;
  service: string;
  transport: "TCP" | "TCPS";
  active: boolean;
  created_at: string;
}
export interface TargetSchema {
  id: string;
  target_id: string;
  name: string;
  history_schema: string;
  history_table: string;
  default_account_id: string;
  active: boolean;
  created_at: string;
}
export interface ExecutionAccount {
  id: string;
  target_id: string;
  name: string;
  username: string;
  active: boolean;
  schema_ids: string[];
  credential: {
    configured: boolean;
    version: number;
    updated_at: string;
    revoked: boolean;
  };
  created_at: string;
}
export interface ServiceAccount {
  id: string;
  name: string;
  owner_id: string;
  purpose: string;
  active: boolean;
  created_at: string;
}
export interface ApiToken {
  id: string;
  name: string;
  prefix: string;
  created_at: string;
  expires_at: string;
  last_used_at: string;
  revoked: boolean;
}
export interface TokenCreated {
  token: string;
  metadata: ApiToken;
}
export interface AuthConfiguration {
  local_enabled: boolean;
  ldap: {
    enabled: boolean;
    url: string;
    start_tls: boolean;
    base_dn: string;
    bind_dn_template: string;
    username_attribute: string;
    display_name_attribute: string;
    subject_attribute: string;
    ca_file: string;
  };
  oidc: {
    enabled: boolean;
    issuer: string;
    client_id: string;
    redirect_url: string;
    ca_file: string;
    client_secret_configured: boolean;
  };
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
  error?: { code?: string; message?: string; field?: string };
}
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public field?: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

let csrfToken = "";
export function setCsrfToken(token: string) {
  csrfToken = token;
}
export function clearCsrfToken() {
  csrfToken = "";
}
let onUnauthorized: (() => void) | undefined;
export function setUnauthorizedHandler(handler: (() => void) | undefined) {
  onUnauthorized = handler;
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body && !(init.body instanceof FormData))
    headers.set("Content-Type", "application/json");
  if (
    init.method &&
    !["GET", "HEAD", "OPTIONS"].includes(init.method.toUpperCase()) &&
    csrfToken
  ) {
    headers.set("X-CSRF-Token", csrfToken);
  }
  const response = await fetch(`/api/v1${path}`, {
    ...init,
    headers,
    credentials: "same-origin",
  });
  if (!response.ok) {
    const body = (await response
      .json()
      .catch(() => null)) as ApiErrorBody | null;
    if (
      response.status === 401 &&
      path !== "/auth/login" &&
      path !== "/auth/bootstrap"
    )
      onUnauthorized?.();
    throw new ApiError(
      response.status,
      body?.error?.code ?? "HTTP_ERROR",
      body?.error?.message ?? `Request failed (${response.status})`,
      body?.error?.field,
    );
  }
  if (response.status === 204) return undefined as T;
  const body = (await response.json()) as ApiResponse<T>;
  if (!body || !("data" in body))
    throw new Error("API response does not match the expected contract");
  return body.data;
}
export async function apiList<T>(path: string, pageSize = 200): Promise<T[]> {
  if (!Number.isInteger(pageSize) || pageSize < 1 || pageSize > 200)
    throw new RangeError("API page size must be between 1 and 200");
  const [resource, query = ""] = path.split("?", 2);
  const params = new URLSearchParams(query);
  const all: T[] = [];
  for (let offset = 0; ; offset += pageSize) {
    params.set("limit", String(pageSize));
    params.set("offset", String(offset));
    const page = await api<T[]>(`${resource}?${params.toString()}`);
    if (!Array.isArray(page))
      throw new Error("API list response does not match the expected contract");
    all.push(...page);
    if (page.length < pageSize) return all;
  }
}
export async function getHealth(signal?: AbortSignal): Promise<Health> {
  const result = await api<Health>("/health", { signal });
  if (!result || typeof result.installation_id !== "string")
    throw new Error("Health response does not match the expected contract");
  return result;
}
export function apiMessage(error: unknown): string {
  return error instanceof Error
    ? error.message
    : "An unexpected error occurred";
}

export interface AuditEvent {
  id: number;
  actor_id: string;
  actor_type: string;
  actor_name: string;
  action: string;
  target: string;
  timestamp: string;
  outcome: string;
  request_id: string;
}
