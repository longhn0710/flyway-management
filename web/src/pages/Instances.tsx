import { useState } from "react";
import {
  Link,
  Navigate,
  NavLink,
  useParams,
  useSearchParams,
} from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
  Field,
  KeyValues,
  Modal,
  Notice,
  PageHeading,
  Panel,
  SearchBox,
  Status,
} from "../components/ui";
import ConnectionWizard from "./ConnectionWizard";
import { SchemaConfiguration } from "./Databases";
import { CredentialEditor } from "./Credentials";

export function EngineNotice() {
  return (
    <Notice title="Engine availability:">
      Oracle is currently implemented. PostgreSQL, MySQL and SQL Server are
      future options; connection testing, registration, discovery and execution
      are unavailable for these engines. Registry activity does not indicate
      live connection health.
    </Notice>
  );
}
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
    target.active &&
    (!schema || schema.active) &&
    (["Admin", "Operator"].includes(identity.user.role) &&
      identity.grants.some(
        (g) =>
          g.target_id === target.id &&
          (!schema || !g.schema_id || g.schema_id === schema.id) &&
          g.operate &&
          (target.environment !== "PROD" || g.prod),
      ));
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
      Run unavailable · {target.active ? "permission required" : "inactive"}
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
    { id: "engine", header: "Engine", cell: () => <Badge>Oracle</Badge> },
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
            Service / PDB · {row.original.service}
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
        description="Database instances · Oracle currently implemented"
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
      <EngineNotice />
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
          {["DEV", "UAT", "PROD"].map((v) => (
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
          {["PostgreSQL", "MySQL", "SQL Server"].map((v) => (
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
              [t.name, t.host, t.service]
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
          </>
        }
        description="Engine: Oracle · Currently implemented"
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
        <RecentRuns targetID={id} />
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
                  ["Engine", "Oracle"],
                  ["Host", <code>{t.host}</code>],
                  ["Port", t.port],
                  ["Oracle Service / PDB", <code>{t.service}</code>],
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
                      <strong className="mono">{s.name}</strong>
                      <small className="block mono muted">
                        {s.history_schema}.{s.history_table}
                      </small>
                    </div>
                    <Link
                      to={`/instances/${encodeURIComponent(id)}/schemas?schema=${encodeURIComponent(s.id)}`}
                    >
                      View schema
                    </Link>
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
function SchemaList({
  target,
  schemas,
  pending,
  error,
  selectedID,
  catalog = false,
}: {
  target: DatabaseTarget;
  schemas: TargetSchema[];
  pending: boolean;
  error: Error | null;
  selectedID: string;
  catalog?: boolean;
}) {
  const admin = useIdentity().user.role === "Admin";
  const selected = schemas.find((s) => s.id === selectedID);
  const columns: ColumnDef<TargetSchema>[] = [
    ...(catalog
      ? [
          {
            id: "instance",
            header: "Instance",
            cell: () => (
              <Link to={`/instances/${encodeURIComponent(target.id)}`}>
                {target.name}
              </Link>
            ),
          },
          { id: "engine", header: "Engine", cell: () => <Badge>Oracle</Badge> },
          {
            id: "environment",
            header: "Environment",
            cell: () => (
              <Badge tone={target.environment === "PROD" ? "error" : "accent"}>
                {target.environment}
              </Badge>
            ),
          },
        ]
      : []),
    {
      accessorKey: "name",
      header: "Schema",
      cell: ({ row }) => (
        <Link
          className="mono"
          to={`?schema=${encodeURIComponent(row.original.id)}`}
        >
          {row.original.name}
        </Link>
      ),
    },
    {
      id: "history",
      header: "History owner / table",
      cell: ({ row }) => (
        <code>
          {row.original.history_schema}.{row.original.history_table}
        </code>
      ),
    },
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
      id: "actions",
      header: "Actions",
      cell: ({ row }) => <RunLink target={target} schema={row.original} />,
    },
  ];
  return (
    <>
      <Notice>
        Oracle privileges are managed in Oracle. Application grants are managed
        in Users & Permissions.{" "}
        <Link to={`/instances/${encodeURIComponent(target.id)}/connections`}>
          Discover and manage allowed schemas
        </Link>
        .
      </Notice>
      {pending ? (
        <div className="skeleton" role="status" />
      ) : error ? (
        <Notice tone="error">{error.message}</Notice>
      ) : (
        <DataTable
          columns={columns}
          data={schemas}
          empty="No schemas registered. Test and discover from Connections to configure an account allowlist."
        />
      )}
      {selected && (
        <>
          <PageHeading
            title={selected.name}
            description="Schema details & stream settings"
          />
          <SchemaConfiguration
            key={selected.id}
            targetID={target.id}
            schema={selected}
            canManage={admin}
          />
        </>
      )}
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
        <Notice>Testing connection and discovering Oracle metadata…</Notice>
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
        Per-account allowlists restrict Flyway choices. They grant no Oracle
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
  const targets = useQuery({
    queryKey: ["database-targets"],
    queryFn: () => apiList<DatabaseTarget>("/database-targets"),
  });
  const schemas = useQuery({
    queryKey: ["schemas", id],
    queryFn: () =>
      apiList<TargetSchema>(
        `/database-targets/${encodeURIComponent(id)}/schemas`,
      ),
    enabled: !!id,
  });
  const target = targets.data?.find((t) => t.id === id);
  return (
    <>
      <PageHeading
        title="Databases / Schemas"
        description="Browse registered schemas by authorized instance"
      />
      <EngineNotice />
      <div className="filter-bar">
        <Field label="Instance">
          <select
            value={id}
            onChange={(e) =>
              setParams(e.target.value ? { instance: e.target.value } : {})
            }
          >
            <option value="">Choose an instance</option>
            {targets.data?.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name} · {t.environment}
              </option>
            ))}
          </select>
        </Field>
      </div>
      {targets.isError ? (
        <Notice tone="error">{targets.error.message}</Notice>
      ) : targets.isPending ? (
        <div className="skeleton" />
      ) : target ? (
        <SchemaList
          target={target}
          schemas={schemas.data ?? []}
          pending={schemas.isPending}
          error={schemas.error}
          selectedID=""
        />
      ) : (
        <p className="empty">
          Choose an instance to browse its authorized registered schemas.
        </p>
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
            {row.original.schema?.name ?? "Unknown schema"}
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
      <EngineNotice />
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
