import { infoHref } from "../lib/flyway-info";
import { useState } from "react";
import {
  Link,
  Navigate,
  NavLink,
  useParams,
  useSearchParams,
} from "react-router-dom";
import {
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { Plus, Database, Play } from "lucide-react";
import {
  api,
  apiList,
  type DatabaseTarget,
  type TargetSchema,
  type ExecutionAccount,
} from "../lib/contracts";
import type { Run, RuntimeStatus } from "../lib/execution";
import { useIdentity } from "../lib/identity-context";
import {
  Badge,
  Button,
  DataTable,
  KeyValues,
  Modal,
  Notice,
  PageHeading,
  Panel,
  SearchBox,
  Status,
} from "../components/ui";
import ConnectionWizard from "./ConnectionWizard";
import History from "./History";
import { SchemaConfiguration } from "./Databases";
import { schemaHref } from "../lib/navigation";
import { canRun } from "../lib/permissions";
import { CredentialEditor } from "./Credentials";

export function InstanceTabs({ id }: { id: string }) {
  return (
    <nav className="instance-tabs" aria-label="Instance sections">
      {[
        ["", "Overview"],
        ["/schemas", "Databases / Schemas"],
        ["/connections", "Connections"],
        ["/history", "Migration History"],
      ].map(([suffix, label]) => (
        <NavLink
          key={suffix}
          end
          to={`/instances/${encodeURIComponent(id)}${suffix}`}
        >
          {label}
        </NavLink>
      ))}
    </nav>
  );
}
export function RunLink({
  target,
  schema,
}: {
  target: DatabaseTarget;
  schema?: TargetSchema;
}) {
  const identity = useIdentity();
  const allowed =
    target.engine !== "postgresql" && canRun(identity, target, schema);
  return allowed ? (
    <Link
      className="button secondary compact"
      to={`/runs/new?target_id=${encodeURIComponent(target.id)}${schema ? `&schema_id=${encodeURIComponent(schema.id)}` : ""}`}
    >
      <Play size={14} />
      Run migration
    </Link>
  ) : (
    <span className="small muted">
      Run unavailable ·{" "}
      {target.engine === "postgresql"
        ? "Gate E/F acceptance required"
        : target.active
          ? "permission required"
          : "inactive"}
    </span>
  );
}
export default function Instances() {
  const admin = useIdentity().user.role === "Admin";
  const [search, setSearch] = useState("");
  const [env, setEnv] = useState("");
  const [engine, setEngine] = useState("");
  const [create, setCreate] = useState(false);
  const targets = useQuery({
    queryKey: ["database-targets"],
    queryFn: () => apiList<DatabaseTarget>("/database-targets"),
  });
  const columns: ColumnDef<DatabaseTarget>[] = [
    {
      accessorKey: "name",
      header: "Instance",
      cell: ({ row }) => (
        <Link
          className="instance-name"
          to={`/instances/${encodeURIComponent(row.original.id)}`}
        >
          <Database size={16} />
          {row.original.name}
        </Link>
      ),
    },
    {
      id: "engine",
      header: "Engine",
      cell: ({ row }) => (
        <Badge>
          {row.original.engine === "postgresql" ? "PostgreSQL" : "Oracle"}
        </Badge>
      ),
    },
    {
      accessorKey: "environment",
      header: "Environment",
      cell: ({ row }) => (
        <Badge tone={row.original.environment === "PROD" ? "error" : "accent"}>
          {row.original.environment}
        </Badge>
      ),
    },
    {
      id: "destination",
      header: "Destination",
      cell: ({ row }) => (
        <span className="mono">
          {row.original.host}:{row.original.port}
          <small className="block muted">
            {row.original.engine === "postgresql"
              ? "Database"
              : "Service / PDB"}{" "}
            · {row.original.database_name || row.original.service}
          </small>
        </span>
      ),
    },
    { accessorKey: "transport", header: "Transport" },
    {
      accessorKey: "active",
      header: "Registry status",
      cell: ({ row }) => (
        <Badge tone={row.original.active ? "success" : "neutral"}>
          {row.original.active ? "Active" : "Inactive"}
        </Badge>
      ),
    },
    {
      id: "connection",
      header: "Connection",
      cell: () => <span className="small muted">Not tested</span>,
    },
    {
      id: "actions",
      header: "Actions",
      cell: ({ row }) => <RunLink target={row.original} />,
    },
  ];
  return (
    <>
      <PageHeading
        title="Instances"
        description="Oracle · Operational; PostgreSQL · Connection/Info; migration gated"
        actions={
          <Button
            variant="primary"
            disabled={!admin}
            onClick={() => setCreate(true)}
          >
            <Plus size={16} />
            Add instance
          </Button>
        }
      />
      {!admin && (
        <Notice>
          Read-only view. Target and schema administration requires Admin.
        </Notice>
      )}
      <div className="filter-bar">
        <SearchBox
          value={search}
          onChange={setSearch}
          placeholder="Search names, host or service…"
        />
        <select
          aria-label="Environment"
          value={env}
          onChange={(e) => setEnv(e.target.value)}
        >
          <option value="">Environment: All</option>
          {["DEV", "SIT", "UAT", "PROD"].map((v) => (
            <option key={v}>{v}</option>
          ))}
        </select>
        <select
          aria-label="Engine"
          value={engine}
          onChange={(e) => setEngine(e.target.value)}
        >
          <option value="">Engine: All engines</option>
          <option value="Oracle">Oracle</option>
          <option value="PostgreSQL">PostgreSQL · Connection/Info</option>
          {["MySQL", "SQL Server"].map((v) => (
            <option key={v} disabled>
              {v} · Future
            </option>
          ))}
        </select>
      </div>
      {targets.isPending ? (
        <div
          className="skeleton"
          role="status"
          aria-label="Loading instances"
        />
      ) : targets.isError ? (
        <Notice tone="error">
          {targets.error.message}
          <Button onClick={() => void targets.refetch()}>Retry</Button>
        </Notice>
      ) : (
        <DataTable
          key={`${env}:${search}`}
          columns={columns}
          data={targets.data.filter(
            (t) =>
              (!env || t.environment === env) &&
              (!engine ||
                (t.engine === "postgresql" ? "PostgreSQL" : "Oracle") ===
                  engine) &&
              [t.name, t.host, t.service, t.database_name]
                .join(" ")
                .toLowerCase()
                .includes(search.toLowerCase()),
          )}
          pageSize={10}
          empty="No instances in this view. Change filters or ask an administrator to add an instance."
        />
      )}
      {create && <ConnectionWizard onClose={() => setCreate(false)} />}
    </>
  );
}
export function LegacyInstance() {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  return (
    <Navigate
      replace
      to={`/instances/${encodeURIComponent(id)}${params.has("schema") || params.has("newSchema") ? "/schemas" : ""}?${params.toString()}`}
    />
  );
}
export function InstanceDetails() {
  const { id = "", section = "" } = useParams();
  const [params] = useSearchParams();
  const admin = useIdentity().user.role === "Admin";
  const client = useQueryClient();
  const [edit, setEdit] = useState(false);
  const target = useQuery({
    queryKey: ["database-target", id],
    queryFn: () =>
      api<DatabaseTarget>(`/database-targets/${encodeURIComponent(id)}`),
  });
  const schemas = useQuery({
    queryKey: ["schemas", id],
    queryFn: () =>
      apiList<TargetSchema>(
        `/database-targets/${encodeURIComponent(id)}/schemas`,
      ),
  });
  const save = useMutation({
    mutationFn: () =>
      api<DatabaseTarget>(`/database-targets/${encodeURIComponent(id)}`, {
        method: "PUT",
        body: JSON.stringify({
          name: target.data!.name,
          environment: target.data!.environment,
          host: target.data!.host,
          port: target.data!.port,
          service: target.data!.service,
          engine: target.data!.engine,
          database_name: target.data!.database_name,
          transport: target.data!.transport,
          active: !target.data!.active,
        }),
      }),
    onSuccess: () =>
      void client
        .invalidateQueries({ queryKey: ["database-target"] })
        .then(() =>
          client.invalidateQueries({ queryKey: ["database-targets"] }),
        ),
  });
  if (target.isPending)
    return (
      <>
        <PageHeading title="Instance" />
        <div className="skeleton" role="status" />
      </>
    );
  if (target.isError)
    return (
      <>
        <PageHeading title="Instance unavailable" />
        <Notice tone="error">
          {target.error.message}
          <Button onClick={() => void target.refetch()}>Retry</Button>
        </Notice>
        <Link to="/instances">Back to instances</Link>
      </>
    );
  const t = target.data;
  return (
    <>
      <PageHeading
        title={t.name}
        breadcrumb={
          <>
            <Link to="/instances">Instances</Link>
            <span>/</span>
            {t.name}
            {section && (
              <>
                <span>/</span>
                {section === "schemas"
                  ? "Databases / Schemas"
                  : section === "history"
                    ? "Migration History"
                    : section === "connections"
                      ? "Connections"
                      : "Unknown section"}
              </>
            )}
          </>
        }
        description={
          target.data?.engine === "postgresql"
            ? "PostgreSQL · Connection/Info; migration disabled pending Gate E/F"
            : "Oracle · Operational"
        }
        actions={
          <>
            <Badge tone={t.environment === "PROD" ? "error" : "accent"}>
              {t.environment}
            </Badge>
            <Badge tone={t.active ? "success" : "neutral"}>
              {t.active ? "Active" : "Inactive"}
            </Badge>
            <RunLink target={t} />
          </>
        }
      />
      <InstanceTabs id={id} />
      {section === "connections" ? (
        <Connections key={id} target={t} schemas={schemas.data ?? []} />
      ) : section === "history" ? (
        <History key={id} targetID={id} />
      ) : section === "schemas" ? (
        <SchemaList
          target={t}
          schemas={schemas.data ?? []}
          pending={schemas.isPending}
          error={schemas.error}
          selectedID={params.get("schema") ?? ""}
        />
      ) : section ? (
        <Notice tone="error">
          Unknown instance section.{" "}
          <Link to={`/instances/${encodeURIComponent(id)}`}>Open overview</Link>
        </Notice>
      ) : (
        <>
          <Notice>
            Active is a registry setting. Test connections from the{" "}
            <Link to={`/instances/${encodeURIComponent(id)}/connections`}>
              Connections tab
            </Link>
            .
          </Notice>
          <div className="grid-two">
            <Panel title="Endpoint definition">
              <KeyValues
                rows={[
                  [
                    "Engine",
                    t.engine === "postgresql" ? "PostgreSQL" : "Oracle",
                  ],
                  ["Host", <code>{t.host}</code>],
                  ["Port", t.port],
                  [
                    t.engine === "postgresql"
                      ? "Database"
                      : "Oracle Service / PDB",
                    <code>{t.database_name || t.service}</code>,
                  ],
                  ["Profile revision", t.profile_revision || "Legacy Oracle"],
                  [
                    "Migration capability",
                    t.engine === "postgresql"
                      ? "Disabled pending Gate E/F acceptance"
                      : "Operational",
                  ],
                  ["Transport", t.transport],
                  [
                    "Registry state",
                    t.active ? "Configured & active" : "Inactive",
                  ],
                ]}
              />
              <details>
                <summary>Technical IDs & metadata</summary>
                <p className="mono">{t.id}</p>
                <p>Registered {new Date(t.created_at).toLocaleString()}</p>
              </details>
              {admin && (
                <div className="actions wrap">
                  <Button onClick={() => setEdit(true)}>
                    Edit connection / Test
                  </Button>
                  <Button
                    variant={t.active ? "danger" : "secondary"}
                    disabled={save.isPending}
                    onClick={() => save.mutate()}
                  >
                    {t.active ? "Deactivate instance" : "Reactivate instance"}
                  </Button>
                </div>
              )}
              {save.isError && (
                <Notice tone="error">{save.error.message}</Notice>
              )}
            </Panel>
            <Panel title="Registered schemas">
              {schemas.isError ? (
                <Notice tone="error">{schemas.error.message}</Notice>
              ) : schemas.isPending ? (
                <div className="skeleton" />
              ) : schemas.data?.length ? (
                schemas.data.map((s) => (
                  <div className="spread inset wrap" key={s.id}>
                    <div>
                      <Link className="mono" to={schemaHref(id, s.id)}>
                        {s.name}
                      </Link>
                      <small className="block mono muted">
                        {s.history_schema}.{s.history_table}
                      </small>
                    </div>
                    <Link to={schemaHref(id, s.id)}>View schema</Link>
                  </div>
                ))
              ) : (
                <p className="empty">No registered schemas.</p>
              )}
              <Link to={`/instances/${encodeURIComponent(id)}/connections`}>
                Manage allowed schemas →
              </Link>
            </Panel>
          </div>
          <RecentRuns targetID={id} />
        </>
      )}
      {edit && <ConnectionWizard target={t} onClose={() => setEdit(false)} />}
    </>
  );
}
function SchemaUnavailable() {
  return (
    <Notice tone="error" title="Schema not found or unavailable">
      This schema may have been removed, may belong to another instance, or you
      may not have access. <Link to="/schemas">Browse available schemas</Link>
    </Notice>
  );
}

type SchemaRow = { target: DatabaseTarget; schema: TargetSchema };
function SchemaTable({
  rows,
  global = false,
  empty,
}: {
  rows: SchemaRow[];
  global?: boolean;
  empty: string;
}) {
  const [params, setParams] = useSearchParams();
  const requestedSize = Number(params.get("page_size") || 10);
  const pageSize = [4, 6, 10, 20].includes(requestedSize) ? requestedSize : 10;
  const requestedPage = Number(params.get("page") || 1);
  const pageIndex = Math.min(
    Math.max(0, Number.isSafeInteger(requestedPage) ? requestedPage - 1 : 0),
    Math.max(0, Math.ceil(rows.length / pageSize) - 1),
  );
  const pagination = { pageIndex, pageSize };
  const columns: ColumnDef<SchemaRow>[] = [
    {
      id: "name",
      accessorFn: (row) => row.schema.name,
      header: "Schema",
      cell: ({ row: { original: r } }) => (
        <Link
          className="mono"
          to={schemaHref(r.target.id, r.schema.id, params)}
        >
          {r.schema.name}
        </Link>
      ),
    },
    ...(global
      ? [
          {
            id: "instance",
            accessorFn: (r: SchemaRow) => r.target.name,
            header: "Instance",
            cell: ({
              row: { original: r },
            }: {
              row: { original: SchemaRow };
            }) => (
              <Link to={`/instances/${encodeURIComponent(r.target.id)}`}>
                {r.target.name}
              </Link>
            ),
          },
          {
            id: "environment",
            accessorFn: (r: SchemaRow) => r.target.environment,
            header: "Environment",
            cell: ({
              row: { original: r },
            }: {
              row: { original: SchemaRow };
            }) => (
              <Badge
                tone={r.target.environment === "PROD" ? "error" : "accent"}
              >
                {r.target.environment}
              </Badge>
            ),
          },
        ]
      : []),
    {
      id: "active",
      accessorFn: (r) => r.schema.active,
      header: "Registry status",
      cell: ({ row: { original: r } }) => (
        <Badge tone={r.schema.active ? "success" : "neutral"}>
          {r.schema.active ? "Active" : "Inactive"}
        </Badge>
      ),
    },
    {
      id: "history",
      accessorFn: (r) => `${r.schema.history_schema}.${r.schema.history_table}`,
      header: "History owner / table",
      cell: ({ row: { original: r } }) => (
        <code>
          {r.schema.history_schema}.{r.schema.history_table}
        </code>
      ),
    },
    {
      id: "actions",
      header: "Actions",
      cell: ({ row: { original: r } }) => (
        <div className="actions wrap">
          <RunLink target={r.target} schema={r.schema} />
          <Link
            to={`/runs?target_id=${encodeURIComponent(r.target.id)}&schema_id=${encodeURIComponent(r.schema.id)}`}
          >
            History
          </Link>
        </div>
      ),
    },
  ];
  return (
    <DataTable
      columns={columns}
      data={rows}
      pagination={pagination}
      onPaginationChange={(updater) => {
        const value =
          typeof updater === "function" ? updater(pagination) : updater;
        const next = new URLSearchParams(params);
        if (value.pageIndex) next.set("page", String(value.pageIndex + 1));
        else next.delete("page");
        if (value.pageSize !== 10)
          next.set("page_size", String(value.pageSize));
        else next.delete("page_size");
        setParams(next);
      }}
      empty={empty}
    />
  );
}

function SchemaList({
  target,
  schemas,
  pending,
  error,
  selectedID,
}: {
  target: DatabaseTarget;
  schemas: TargetSchema[];
  pending: boolean;
  error: Error | null;
  selectedID: string;
}) {
  const identity = useIdentity();
  const admin = identity.user.role === "Admin";
  const [params, setParams] = useSearchParams();
  const q = params.get("q") ?? "";
  const status = params.get("status") ?? "";
  const selected = schemas.find((s) => s.id === selectedID);
  const update = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    next.delete("page");
    setParams(next, { replace: true });
  };
  if (pending)
    return (
      <div className="skeleton" role="status" aria-label="Loading schemas" />
    );
  if (error) return <Notice tone="error">{error.message}</Notice>;
  if (params.has("schema") && !selected) return <SchemaUnavailable />;
  return (
    <>
      {selected && (
        <section aria-label="Schema details">
          <div className="spread wrap section-heading">
            <div>
              <h2>{selected.name}</h2>
              <p className="muted">
                Registered schema ·{" "}
                {target.engine === "postgresql" ? "PostgreSQL" : "Oracle"}
              </p>
            </div>
            <div className="actions wrap">
              <Link
                className="button secondary"
                to={infoHref(target.id, selected.id)}
              >
                Flyway Info
              </Link>
              <RunLink target={target} schema={selected} />
              <Link
                to={`/runs?target_id=${encodeURIComponent(target.id)}&schema_id=${encodeURIComponent(selected.id)}`}
              >
                Schema migration history
              </Link>
            </div>
          </div>
          <SchemaConfiguration
            key={selected.id}
            targetID={target.id}
            schema={selected}
            canManage={admin}
            canOperate={canRun(identity, target, selected)}
          />
        </section>
      )}
      <div className="spread wrap section-heading">
        <h2>Registered schemas</h2>
        {admin && (
          <Link to={`/instances/${encodeURIComponent(target.id)}/connections`}>
            Manage allowed schemas
          </Link>
        )}
      </div>
      <div className="filter-bar">
        <SearchBox
          value={q}
          onChange={(v) => update("q", v)}
          placeholder="Search schemas or history…"
        />
        <select
          aria-label="Registry status"
          value={status}
          onChange={(e) => update("status", e.target.value)}
        >
          <option value="">Status: All</option>
          <option value="active">Active</option>
          <option value="inactive">Inactive</option>
        </select>
      </div>
      <SchemaTable
        rows={schemas
          .filter(
            (s) =>
              (!status || s.active === (status === "active")) &&
              [s.name, s.history_schema, s.history_table]
                .join(" ")
                .toLowerCase()
                .includes(q.toLowerCase()),
          )
          .map((schema) => ({ target, schema }))}
        empty={
          schemas.length
            ? "No schemas match these filters. Change search or status."
            : "No registered schemas are available to you on this instance. Ask an administrator to review registration and access."
        }
      />
      <p className="small muted">
        Registry status describes application configuration. Live Oracle
        privileges are checked separately during migration preflight.
      </p>
    </>
  );
}
function Connections({
  target,
  schemas,
}: {
  target: DatabaseTarget;
  schemas: TargetSchema[];
}) {
  const admin = useIdentity().user.role === "Admin";
  const [wizard, setWizard] = useState(false);
  const [accountID, setAccountID] = useState("");
  const [editor, setEditor] = useState<ExecutionAccount>();
  const [result, setResult] = useState<{
    account: string;
    value: Record<string, unknown>;
  }>();
  const client = useQueryClient();
  const accounts = useQuery({
    queryKey: ["execution-accounts", target.id],
    queryFn: () =>
      apiList<ExecutionAccount>(
        `/execution-accounts?target_id=${encodeURIComponent(target.id)}`,
      ),
    enabled: admin,
  });
  const probe = useMutation({
    mutationFn: (a: ExecutionAccount) =>
      api<Record<string, unknown>>(
        `/connections/${encodeURIComponent(target.id)}/test`,
        {
          method: "POST",
          body: JSON.stringify({
            target: {
              name: target.name,
              environment: target.environment,
              host: target.host,
              port: target.port,
              service: target.service,
              engine: target.engine,
              database_name: target.database_name,
              transport: target.transport,
            },
            account_id: a.id,
            username: a.username,
            password: "",
            timeout_seconds: 20,
          }),
        },
      ),
    onSuccess: (value, a) => setResult({ account: a.username, value }),
  });
  const columns: ColumnDef<ExecutionAccount>[] = [
    {
      accessorKey: "username",
      header: "Username",
      cell: ({ row }) => (
        <strong className="mono">{row.original.username}</strong>
      ),
    },
    {
      id: "credential",
      header: "Credential metadata",
      cell: ({ row }) => (
        <span>
          {row.original.credential.revoked
            ? "Revoked"
            : row.original.credential.configured
              ? `Saved · version ${row.original.credential.version} (encrypted at rest)`
              : "Not configured"}
        </span>
      ),
    },
    {
      accessorKey: "active",
      header: "Account status",
      cell: ({ row }) => (
        <Badge tone={row.original.active ? "success" : "neutral"}>
          {row.original.active ? "Active" : "Inactive"}
        </Badge>
      ),
    },
    {
      id: "scope",
      header: "Allowed schemas",
      cell: ({ row }) => (
        <span>
          {row.original.schema_ids
            .map(
              (id) =>
                schemas.find((s) => s.id === id)?.name ?? "Restricted schema",
            )
            .join(", ") || "No allowed schemas"}
        </span>
      ),
    },
    {
      id: "actions",
      header: "Actions",
      cell: ({ row }) => (
        <div className="actions wrap">
          <Button
            disabled={!target.active}
            onClick={() => {
              setAccountID(row.original.id);
              setWizard(true);
            }}
          >
            Edit account / Allowed schemas
          </Button>
          <Button
            disabled={
              probe.isPending ||
              !row.original.active ||
              !row.original.credential.configured ||
              row.original.credential.revoked ||
              !target.active
            }
            onClick={() => probe.mutate(row.original)}
          >
            Test connection
          </Button>
          <Button onClick={() => setEditor(row.original)}>
            Credential settings
          </Button>
        </div>
      ),
    },
  ];
  return (
    <>
      <div className="spread wrap section-heading">
        <div>
          <h2>Execution accounts</h2>
          <p className="muted">
            Saved credentials are encrypted at rest and never returned by the
            API.
          </p>
        </div>
        <Button
          variant="primary"
          disabled={!admin || !target.active}
          onClick={() => {
            setAccountID("");
            setWizard(true);
          }}
        >
          <Plus size={16} />
          Add execution account
        </Button>
      </div>
      {!admin ? (
        <Notice>
          Connection credentials and allowlists require Admin. Use Run Migration
          to see your authorized execution accounts.
        </Notice>
      ) : accounts.isPending ? (
        <div className="skeleton" role="status" />
      ) : accounts.isError ? (
        <Notice tone="error">
          {accounts.error.message}
          <Button onClick={() => void accounts.refetch()}>Retry</Button>
        </Notice>
      ) : (
        <DataTable
          data={accounts.data}
          columns={columns}
          empty="No execution accounts. Add an account to test connectivity, discover schemas and configure its allowlist."
        />
      )}
      {probe.isPending && (
        <Notice>Testing connection and discovering database metadata…</Notice>
      )}
      {probe.isError && <Notice tone="error">{probe.error.message}</Notice>}
      {result && (
        <Panel title={`Connection test · ${result.account}`}>
          <KeyValues
            rows={[
              ["Network", String(result.value.network_state ?? "Unknown")],
              [
                "Authentication",
                String(result.value.authentication_state ?? "Unknown"),
              ],
              [
                "Current schema",
                String(result.value.current_schema ?? "Unknown"),
              ],
              ["Discovered schemas", String(result.value.schema_total ?? 0)],
            ]}
          />
          {!!result.value.error_message && (
            <Notice tone="error">{String(result.value.error_message)}</Notice>
          )}
          <Notice>
            Metadata visibility does not verify arbitrary DDL privileges. Open
            Edit account / Allowed schemas to select schemas and review before
            atomic save.
          </Notice>
        </Panel>
      )}
      <Notice>
        Per-account allowlists restrict Flyway choices. They grant no database
        privileges and do not constrain cross-schema SQL. Changes affect future
        runs; historical evidence remains available.
      </Notice>
      {wizard && (
        <ConnectionWizard
          target={target}
          initialAccountID={accountID}
          onClose={() => setWizard(false)}
        />
      )}
      <Modal
        open={!!editor}
        onOpenChange={(open) => {
          if (!open) setEditor(undefined);
        }}
        title="Execution account settings"
        description="Credential and account lifecycle for this instance."
      >
        {editor && (
          <CredentialEditor
            key={editor.id}
            account={editor}
            lifecycleOnly
            targetID={target.id}
            canManage={admin}
            onSaved={() => {
              setEditor(undefined);
              void client.invalidateQueries();
            }}
          />
        )}
      </Modal>
    </>
  );
}
export function SchemaCatalog() {
  const [params, setParams] = useSearchParams();
  const id = params.get("instance") ?? "";
  const q = params.get("q") ?? "";
  const environment = params.get("environment") ?? "";
  const targets = useQuery({
    queryKey: ["database-targets"],
    queryFn: () => apiList<DatabaseTarget>("/database-targets"),
  });
  // Enumerate only authorized targets. Each schema list is independently authorized
  // by the server and apiList consumes every page before catalog filtering.
  const schemaQueries = useQueries({
    queries: (targets.data ?? []).map((target) => ({
      queryKey: ["schemas", target.id],
      queryFn: () =>
        apiList<TargetSchema>(
          `/database-targets/${encodeURIComponent(target.id)}/schemas`,
        ),
    })),
  });
  const pending = targets.isPending || schemaQueries.some((r) => r.isPending);
  const error = targets.error ?? schemaQueries.find((r) => r.isError)?.error;
  const rows = (targets.data ?? []).flatMap((target, index) =>
    (schemaQueries[index].data ?? []).map((schema) => ({ target, schema })),
  );
  const update = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    next.delete("page");
    setParams(next, { replace: true });
  };
  // Legacy schema-only URLs resolve against the full authorized catalog, regardless
  // of stale filter context. Never substitute an unrelated schema or instance.
  const selected = rows.find((r) => r.schema.id === params.get("schema"));
  if (!pending && !error && selected && params.has("schema"))
    return (
      <Navigate
        replace
        to={schemaHref(selected.target.id, selected.schema.id, params)}
      />
    );
  return (
    <>
      <PageHeading
        title="Databases / Schemas"
        description="Registered schemas across your authorized database instances"
      />
      {pending ? (
        <div
          className="skeleton"
          role="status"
          aria-label="Loading schema catalog"
        />
      ) : error ? (
        <Notice tone="error" title="Unable to load schema catalog">
          {error.message}{" "}
          <Button
            onClick={() => {
              void targets.refetch();
              schemaQueries.forEach((r) => void r.refetch());
            }}
          >
            Retry
          </Button>
        </Notice>
      ) : params.has("schema") ? (
        <SchemaUnavailable />
      ) : (
        <>
          <div className="filter-bar">
            <SearchBox
              value={q}
              onChange={(v) => update("q", v)}
              placeholder="Search schemas, instances or history…"
            />
            <select
              aria-label="Instance"
              value={id}
              onChange={(e) => update("instance", e.target.value)}
            >
              <option value="">All instances</option>
              {id && !targets.data?.some((t) => t.id === id) && (
                <option value={id}>Unavailable instance</option>
              )}
              {targets.data?.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </select>
            <select
              aria-label="Environment"
              value={environment}
              onChange={(e) => update("environment", e.target.value)}
            >
              <option value="">Environment: All</option>
              {["DEV", "SIT", "UAT", "PROD"].map((v) => (
                <option key={v}>{v}</option>
              ))}
            </select>
            {(q || id || environment) && (
              <Button
                onClick={() => {
                  const next = new URLSearchParams(params);
                  ["q", "instance", "environment", "page"].forEach((key) =>
                    next.delete(key),
                  );
                  setParams(next);
                }}
              >
                Clear filters
              </Button>
            )}
          </div>
          <SchemaTable
            global
            rows={rows.filter(
              ({ target, schema }) =>
                (!id || target.id === id) &&
                (!environment || target.environment === environment) &&
                [
                  schema.name,
                  target.name,
                  target.host,
                  target.database_name || target.service,
                  schema.history_schema,
                  schema.history_table,
                ]
                  .join(" ")
                  .toLowerCase()
                  .includes(q.toLowerCase()),
            )}
            empty={
              rows.length
                ? "No schemas match these filters. Change search, instance or environment."
                : "No registered schemas are available to you. Ask an administrator to review instance registration and schema access."
            }
          />
          <p className="small muted">
            Registry status describes application configuration. Live Oracle
            privileges are checked separately during migration preflight.
          </p>
        </>
      )}
    </>
  );
}
export function RecentRuns({ targetID }: { targetID?: string }) {
  const runs = useQuery({
    queryKey: ["instance-runs", targetID],
    queryFn: () =>
      api<Run[]>(
        `/migration-runs?limit=10${targetID ? `&target_id=${encodeURIComponent(targetID)}` : ""}`,
      ),
    refetchInterval: 10000,
  });
  const columns: ColumnDef<Run>[] = [
    {
      accessorKey: "run_id",
      header: "Run",
      cell: ({ row }) => (
        <Link to={`/runs/${encodeURIComponent(row.original.id)}`}>
          {row.original.run_id}
        </Link>
      ),
    },
    {
      accessorKey: "status",
      header: "Status",
      cell: ({ row }) => <Status value={row.original.status} />,
    },
    {
      id: "destination",
      header: "Destination",
      cell: ({ row }) => (
        <span>
          {row.original.target?.name ?? "Unknown instance"}
          <small className="block mono muted">
            <Link
              to={schemaHref(row.original.target_id, row.original.schema_id)}
            >
              {row.original.schema?.name ?? "Unknown schema"}
            </Link>
          </small>
        </span>
      ),
    },
    {
      accessorKey: "actor_name",
      header: "Initiator",
      cell: ({ row }) => (
        <span>
          {row.original.source} · {row.original.actor_name}
        </span>
      ),
    },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => new Date(row.original.created_at).toLocaleString(),
    },
    {
      id: "actions",
      header: "Actions",
      cell: ({ row }) => (
        <Link to={`/runs/${encodeURIComponent(row.original.id)}`}>
          View logs →
        </Link>
      ),
    },
  ];
  return (
    <Panel
      title={
        targetID
          ? "Recent migration runs for this instance"
          : "Recent migration runs"
      }
      aside={<Button onClick={() => void runs.refetch()}>Refresh</Button>}
    >
      {runs.isPending ? (
        <div className="skeleton" role="status" />
      ) : runs.isError ? (
        <Notice tone="error">
          {runs.error.message}
          <Button onClick={() => void runs.refetch()}>Retry</Button>
        </Notice>
      ) : (
        <DataTable
          columns={columns}
          data={runs.data}
          pageSize={10}
          empty="No migration runs in this scope."
        />
      )}
      <Link
        to={`/runs${targetID ? `?target_id=${encodeURIComponent(targetID)}` : ""}`}
      >
        Open migration history →
      </Link>
    </Panel>
  );
}
export function Overview() {
  const runtime = useQuery({
    queryKey: ["runtime"],
    queryFn: () => api<RuntimeStatus>("/runtime"),
    refetchInterval: 30000,
  });
  return (
    <>
      <PageHeading
        title="Operations overview"
        description="Instance-centric database migration management"
        actions={
          <Link to="/runs/new" className="button primary">
            <Play size={15} />
            Run migration
          </Link>
        }
      />
      <div className="grid-two">
        <Panel title="Migration runtime">
          {runtime.isPending ? (
            <div className="skeleton" />
          ) : runtime.isError ? (
            <Notice tone="error">{runtime.error.message}</Notice>
          ) : (
            <>
              <Badge tone={runtime.data.ready ? "success" : "warning"}>
                {runtime.data.ready ? "Ready" : "Unavailable"}
              </Badge>
              <KeyValues
                rows={[
                  ["Flyway version", runtime.data.version || "Unknown"],
                  ["Integrity", runtime.data.integrity],
                  ["Dependency", runtime.data.dependency],
                ]}
              />
              {runtime.data.blockers.map((x) => (
                <Notice key={x} tone="warning">
                  {x}
                </Notice>
              ))}
            </>
          )}
        </Panel>
        <Panel title="Operational workflow">
          <ol className="workflow-list">
            <li>
              <Link to="/instances">Choose or register an instance</Link>
            </li>
            <li>Test connections and discover allowed schemas</li>
            <li>
              <Link to="/runs/new">
                Upload, preflight and review the exact destination
              </Link>
            </li>
            <li>Confirm execution and monitor logs</li>
            <li>
              <Link to="/runs">Review migration and repair evidence</Link>
            </li>
          </ol>
        </Panel>
      </div>
      <RecentRuns />
    </>
  );
}
