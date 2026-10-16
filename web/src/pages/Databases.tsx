import ConnectionWizard from "./ConnectionWizard";
import { useMemo, useState, type FormEvent } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import {
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import {
  Plus,
  ChevronDown,
  ChevronRight,
  ShieldCheck,
  Database,
  ArrowLeft,
} from "lucide-react";
import {
  api,
  apiList,
  ApiError,
  type DatabaseTarget,
  type ExecutionAccount,
  type TargetSchema,
} from "../lib/contracts";
import { useIdentity } from "../lib/identity-context";
import {
  Panel,
  PageHeading,
  Button,
  Badge,
  Notice,
  SearchBox,
  Modal,
  Field,
} from "../components/ui";

export default function Databases() {
  const identity = useIdentity();
  const admin = identity.user.role === "Admin";
  const client = useQueryClient();
  const [q, setQ] = useState("");
  const [env, setEnv] = useState("");
  const [expanded, setExpanded] = useState("");
  const [create, setCreate] = useState(false);
  const [schemaTarget, setSchemaTarget] = useState<DatabaseTarget | null>(null);
  const targets = useQuery({
    queryKey: ["database-targets"],
    queryFn: () => apiList<DatabaseTarget>("/database-targets"),
  });
  const rows = useMemo(() => targets.data ?? [], [targets.data]);
  const schemas = useQueries({
    queries: rows.map((target) => ({
      queryKey: ["schemas", target.id],
      queryFn: () =>
        apiList<TargetSchema>(
          `/database-targets/${encodeURIComponent(target.id)}/schemas`,
        ),
    })),
  });
  const filtered = useMemo(
    () =>
      rows.filter(
        (target) =>
          (!env || target.environment === env) &&
          [target.id, target.name, target.host, target.service]
            .join(" ")
            .toLowerCase()
            .includes(q.toLowerCase()),
      ),
    [rows, env, q],
  );
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ["database-targets"] });
  };
  return (
    <>
      <PageHeading
        title="Database & Schema"
        description="Manage Oracle database targets and independent target-schema migration streams."
        breadcrumb="Target administration / Database & Schema"
        actions={
          <>
            <Button
              variant="primary"
              disabled={!admin}
              onClick={() => setCreate(true)}
            >
              <Plus size={16} />
              Add connection
            </Button>
          </>
        }
      />
      {!admin && (
        <Notice tone="warning">
          Read-only view. Target and schema administration requires Admin.
        </Notice>
      )}
      <div className="filter-bar">
        <SearchBox
          value={q}
          onChange={setQ}
          placeholder="Search database, host, service or ID…"
        />
        <div className="inline">
          <span className="small muted">ENVIRONMENT:</span>
          <div className="segments">
            {["", "UAT", "PROD", "DEV"].map((value) => (
              <button
                key={value}
                aria-pressed={env === value}
                className={env === value ? "selected" : ""}
                onClick={() => setEnv(value)}
              >
                {value || "All"}
              </button>
            ))}
          </div>
        </div>
        <span className="small mono push-right">
          {filtered.length} targets ·{" "}
          {filtered.reduce(
            (n, target) =>
              n + (schemas[rows.indexOf(target)]?.data?.length ?? 0),
            0,
          )}{" "}
          schemas
        </span>
      </div>
      {targets.isPending ? (
        <div
          className="skeleton"
          role="status"
          aria-label="Loading databases"
        />
      ) : targets.isError ? (
        <Notice tone="error">
          {targets.error instanceof ApiError && targets.error.status === 403
            ? "Access denied to database targets."
            : targets.error.message}
          <Button onClick={() => void targets.refetch()}>Retry</Button>
        </Notice>
      ) : (
        <div className="panel registry">
          <div className="registry-head">
            <span>Database & stable ID</span>
            <span>Environment</span>
            <span>Host:Port / Service</span>
            <span>Transport</span>
            <span>Status</span>
            <span>Schemas / Actions</span>
          </div>
          {filtered.map((target) => {
            const index = rows.findIndex((item) => item.id === target.id);
            const targetSchemas = schemas[index];
            return (
              <div key={target.id}>
                <div className="registry-row">
                  <button
                    className="target-toggle"
                    aria-label={`Toggle schemas ${target.name}`}
                    aria-expanded={expanded === target.id}
                    onClick={() =>
                      setExpanded(expanded === target.id ? "" : target.id)
                    }
                  >
                    {expanded === target.id ? (
                      <ChevronDown size={16} />
                    ) : (
                      <ChevronRight size={16} />
                    )}
                    <div>
                      <strong>{target.name}</strong>
                      <small className="block muted mono">{target.id}</small>
                    </div>
                  </button>
                  <Badge
                    tone={target.environment === "PROD" ? "error" : "neutral"}
                  >
                    {target.environment}
                  </Badge>
                  <span className="mono small">
                    {target.host}:{target.port}
                    <br />
                    Service: {target.service}
                  </span>
                  <span className="small">{target.transport}</span>
                  <span>
                    <Badge tone={target.active ? "success" : "error"}>
                      {target.active ? "Active" : "Disabled"}
                    </Badge>
                    <small className="block muted">
                      Test via connection wizard
                    </small>
                  </span>
                  <div className="inline wrap">
                    <Badge>{targetSchemas?.data?.length ?? "…"} schemas</Badge>
                    <Link
                      className="button secondary compact"
                      to={`/databases/${encodeURIComponent(target.id)}`}
                    >
                      Details
                    </Link>
                  </div>
                </div>
                {expanded === target.id && (
                  <div className="schema-expansion">
                    <div className="spread">
                      <h3>Schemas on {target.name}</h3>
                      <Button
                        disabled={!admin || !target.active}
                        onClick={() => setSchemaTarget(target)}
                      >
                        <Plus size={14} />
                        Register schema
                      </Button>
                    </div>
                    <p className="small muted">
                      Each target schema is an independent migration stream.
                    </p>
                    {targetSchemas?.isPending ? (
                      <div className="skeleton" role="status" />
                    ) : targetSchemas?.isError ? (
                      <Notice tone="error">
                        {targetSchemas.error.message}
                      </Notice>
                    ) : targetSchemas?.data?.length ? (
                      <div className="table-scroll">
                        <table>
                          <thead>
                            <tr>
                              <th>Schema</th>
                              <th>Flyway history</th>
                              <th>Default execution account</th>
                              <th>Status</th>
                              <th>Action</th>
                            </tr>
                          </thead>
                          <tbody>
                            {targetSchemas.data.map((schema) => (
                              <tr key={schema.id}>
                                <td>
                                  <code>{schema.name}</code>
                                </td>
                                <td>
                                  <code>
                                    {schema.history_schema}.
                                    {schema.history_table}
                                  </code>
                                </td>
                                <td>
                                  {schema.default_account_id ? (
                                    <code>{schema.default_account_id}</code>
                                  ) : (
                                    "Not configured"
                                  )}
                                </td>
                                <td>
                                  <Badge
                                    tone={schema.active ? "success" : "error"}
                                  >
                                    {schema.active ? "Active" : "Disabled"}
                                  </Badge>
                                </td>
                                <td>
                                  <Link
                                    to={`/databases/${encodeURIComponent(target.id)}?schema=${encodeURIComponent(schema.id)}`}
                                  >
                                    Configure stream →
                                  </Link>
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    ) : (
                      <p className="empty">No schemas have been registered.</p>
                    )}
                  </div>
                )}
              </div>
            );
          })}
          {filtered.length === 0 && (
            <div className="empty">
              <strong>
                {rows.length
                  ? "No database matches."
                  : "No database targets have been registered."}
              </strong>
            </div>
          )}
        </div>
      )}
      <Notice title="Target and schema boundaries">
        <ShieldCheck size={15} />
        Application grants and the connection schema allowlist are enforced
        separately from Oracle privileges.
      </Notice>
      {create && (
        <ConnectionWizard
          onClose={() => {
            setCreate(false);
            refresh();
          }}
        />
      )}
      <SchemaCreate
        open={!!schemaTarget}
        target={schemaTarget ?? undefined}
        onCreated={() => {
          if (schemaTarget) {
            void client.invalidateQueries({
              queryKey: ["schemas", schemaTarget.id],
            });
          }
          setSchemaTarget(null);
          refresh();
        }}
        onClose={() => setSchemaTarget(null)}
      />
    </>
  );
}
function SchemaCreate({
  open,
  target,
  onCreated,
  onClose,
}: {
  open: boolean;
  target?: DatabaseTarget;
  onCreated: () => void;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [historySchema, setHistorySchema] = useState("");
  const [historyTable, setHistoryTable] = useState("flyway_schema_history");
  const create = useMutation({
    mutationFn: () =>
      api<TargetSchema>(
        `/database-targets/${encodeURIComponent(target!.id)}/schemas`,
        {
          method: "POST",
          body: JSON.stringify({
            name,
            history_schema: historySchema || name,
            history_table: historyTable,
          }),
        },
      ),
    onSuccess: onCreated,
  });
  return (
    <Modal
      open={open}
      onOpenChange={(visible) => {
        if (!visible) onClose();
      }}
      title="Register target schema"
      description={`Creates an application stream under ${target?.name ?? ""}. Does not create an Oracle schema.`}
    >
      <form
        onSubmit={(event: FormEvent) => {
          event.preventDefault();
          create.mutate();
        }}
      >
        <Field label="Target schema name" required>
          <input
            required
            pattern="[A-Za-z][A-Za-z0-9_$#]*"
            maxLength={128}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field label="History schema" hint="Defaults to the target schema.">
          <input
            pattern="[A-Za-z][A-Za-z0-9_$#]*"
            value={historySchema}
            onChange={(event) => setHistorySchema(event.target.value)}
          />
        </Field>
        <Field label="History table">
          <input
            required
            pattern="[A-Za-z][A-Za-z0-9_$#]*"
            value={historyTable}
            onChange={(event) => setHistoryTable(event.target.value)}
          />
        </Field>
        {create.isError && <Notice tone="error">{create.error.message}</Notice>}
        <div className="dialog-actions">
          <Button type="button" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="submit"
            variant="primary"
            disabled={!target || create.isPending}
          >
            {create.isPending ? "Registering…" : "Register schema"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}

export function DatabaseDetails() {
  const { id = "" } = useParams();
  const [searchParams] = useSearchParams();
  const identity = useIdentity();
  const admin = identity.user.role === "Admin";
  const queryClient = useQueryClient();
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
    enabled: !!id,
  });
  const [editConnection, setEditConnection] = useState(false);
  const [schemaID, setSchemaID] = useState(searchParams.get("schema") ?? "");
  const [showCreate, setShowCreate] = useState(
    searchParams.get("newSchema") === "1",
  );
  const selectedSchema =
    schemas.data?.find((schema) => schema.id === schemaID) ?? schemas.data?.[0];
  const saveTarget = useMutation({
    mutationFn: (active: boolean) =>
      api<DatabaseTarget>(`/database-targets/${encodeURIComponent(id)}`, {
        method: "PUT",
        body: JSON.stringify({
          name: target.data!.name,
          environment: target.data!.environment,
          host: target.data!.host,
          port: target.data!.port,
          service: target.data!.service,
          transport: target.data!.transport,
          active,
        }),
      }),
    onSuccess: (data) => {
      queryClient.setQueryData(["database-target", id], data);
      void queryClient.invalidateQueries({ queryKey: ["database-targets"] });
    },
  });
  if (target.isPending || schemas.isPending)
    return (
      <>
        <PageHeading title="Database details" />
        <div className="skeleton" role="status" />
      </>
    );
  if (target.isError || schemas.isError)
    return (
      <>
        <PageHeading title="Database details" />
        <Notice tone="error">
          {target.error?.message ?? schemas.error?.message}
        </Notice>
        <Link to="/databases">Back to databases</Link>
      </>
    );
  return (
    <>
      <PageHeading
        title={target.data.name}
        breadcrumb={
          <>
            Database & Schema / <code>{target.data.id}</code>
          </>
        }
        description={`${target.data.host}:${target.data.port} · Service ${target.data.service} · ${target.data.environment}`}
        actions={
          <>
            <Link className="button secondary" to="/databases">
              <ArrowLeft size={15} />
              Back to databases
            </Link>
            <Button disabled={!admin} onClick={() => setEditConnection(true)}>
              Edit connection / Test
            </Button>
          </>
        }
      />
      {!admin && (
        <Notice tone="warning">
          Read-only view. Registry changes require Admin.
        </Notice>
      )}
      <Panel
        title="Database target registry"
        aside={
          <Badge tone={target.data.active ? "success" : "error"}>
            {target.data.active ? "Active" : "Disabled"}
          </Badge>
        }
      >
        <div className="probe-grid">
          {[
            ["Target", target.data.name],
            ["Address", `${target.data.host}:${target.data.port}`],
            ["Service / PDB", target.data.service],
            ["Transport", target.data.transport],
          ].map(([key, value]) => (
            <div className="inset" key={key}>
              <strong>{key}</strong>
              <p className="mono">{value}</p>
            </div>
          ))}
        </div>
        <Notice tone="warning">
          Use Edit connection / Test to authenticate, discover schemas, update
          the allowlist or rotate the encrypted credential.
        </Notice>
        <Button
          variant={target.data.active ? "danger" : "secondary"}
          disabled={!admin || saveTarget.isPending}
          onClick={() => saveTarget.mutate(!target.data!.active)}
        >
          {target.data.active ? "Deactivate target" : "Reactivate target"}
        </Button>
        {saveTarget.isError && (
          <Notice tone="error">{saveTarget.error.message}</Notice>
        )}
      </Panel>
      <div className="spread database-schema-toolbar">
        <div className="tabs" role="tablist" aria-label="Target schema">
          {(schemas.data ?? []).map((schema) => (
            <button
              key={schema.id}
              role="tab"
              aria-selected={selectedSchema?.id === schema.id}
              onClick={() => setSchemaID(schema.id)}
            >
              <Database size={15} />
              {schema.name}
              <Badge>{schema.history_table}</Badge>
            </button>
          ))}
        </div>
        <Button
          disabled={!admin || !target.data.active}
          onClick={() => setShowCreate(true)}
        >
          <Plus size={14} />
          Register schema
        </Button>
      </div>
      {!selectedSchema ? (
        <p className="empty">No target schemas registered.</p>
      ) : (
        <SchemaConfiguration
          key={selectedSchema.id}
          targetID={id}
          schema={selectedSchema}
          canManage={admin}
        />
      )}
      {editConnection && (
        <ConnectionWizard
          target={target.data}
          onClose={() => {
            setEditConnection(false);
            void queryClient.invalidateQueries();
          }}
        />
      )}
      <SchemaCreate
        open={showCreate}
        target={target.data}
        onCreated={() => {
          setShowCreate(false);
          void queryClient.invalidateQueries({ queryKey: ["schemas", id] });
        }}
        onClose={() => setShowCreate(false)}
      />
    </>
  );
}

function SchemaConfiguration({
  targetID,
  schema,
  canManage,
}: {
  targetID: string;
  schema: TargetSchema;
  canManage: boolean;
}) {
  const queryClient = useQueryClient();
  const [historySchema, setHistorySchema] = useState(schema.history_schema);
  const [historyTable, setHistoryTable] = useState(schema.history_table);
  const [defaultAccountID, setDefaultAccountID] = useState(
    schema.default_account_id,
  );
  const [active, setActive] = useState(schema.active);
  const accounts = useQuery({
    queryKey: ["execution-accounts", targetID],
    queryFn: () =>
      apiList<ExecutionAccount>(
        `/execution-accounts?target_id=${encodeURIComponent(targetID)}`,
      ),
  });
  const save = useMutation({
    mutationFn: () =>
      api<TargetSchema>(
        `/database-targets/${encodeURIComponent(targetID)}/schemas/${encodeURIComponent(schema.id)}`,
        {
          method: "PUT",
          body: JSON.stringify({
            name: schema.name,
            history_schema: historySchema,
            history_table: historyTable,
            default_account_id: defaultAccountID,
            active,
          }),
        },
      ),
    onSuccess: () =>
      void queryClient.invalidateQueries({ queryKey: ["schemas", targetID] }),
  });
  return (
    <div className="grid-two">
      <Panel title="Schema stream configuration">
        <Field label="Target schema">
          <input value={schema.name} readOnly />
        </Field>
        <Field label="History schema" required>
          <input
            required
            pattern="[A-Za-z][A-Za-z0-9_$#]*"
            value={historySchema}
            disabled={!canManage}
            onChange={(event) => setHistorySchema(event.target.value)}
          />
        </Field>
        <Field label="History table" required>
          <input
            required
            pattern="[A-Za-z][A-Za-z0-9_$#]*"
            value={historyTable}
            disabled={!canManage}
            onChange={(event) => setHistoryTable(event.target.value)}
          />
        </Field>
        <Field label="Default execution account">
          <select
            value={defaultAccountID}
            disabled={!canManage || accounts.isPending}
            onChange={(event) => setDefaultAccountID(event.target.value)}
          >
            <option value="">No default account</option>
            {(accounts.data ?? [])
              .filter(
                (account) =>
                  account.active && account.schema_ids.includes(schema.id),
              )
              .map((account) => (
                <option key={account.id} value={account.id}>
                  {account.name} ({account.username})
                </option>
              ))}
          </select>
        </Field>
        <label className="check">
          <input
            type="checkbox"
            checked={active}
            disabled={!canManage}
            onChange={(event) => setActive(event.target.checked)}
          />
          Stream active
        </label>
        {save.isError && <Notice tone="error">{save.error.message}</Notice>}
        <Button
          variant="primary"
          disabled={!canManage || save.isPending}
          onClick={() => save.mutate()}
        >
          {save.isPending ? "Saving…" : "Save stream"}
        </Button>
      </Panel>
      <Panel title="Execution account association">
        <p className="small muted">
          An execution account can be shared between schemas. Flyway schema
          configuration is not an Oracle privilege boundary.
        </p>
        {accounts.isPending ? (
          <div className="skeleton" role="status" />
        ) : accounts.isError ? (
          <Notice tone="error">{accounts.error.message}</Notice>
        ) : (
          (accounts.data ?? [])
            .filter((account) => account.schema_ids.includes(schema.id))
            .map((account) => (
              <div className="inset spread" key={account.id}>
                <span>
                  <strong>{account.name}</strong>
                  <small className="block mono muted">
                    Oracle username: {account.username}
                  </small>
                </span>
                <Badge
                  tone={
                    account.credential.configured && !account.credential.revoked
                      ? "success"
                      : "warning"
                  }
                >
                  {account.credential.configured && !account.credential.revoked
                    ? "Credential configured"
                    : "No active credential"}
                </Badge>
              </div>
            ))
        )}
        <Link to="/execution-accounts">Manage execution accounts →</Link>
      </Panel>
    </div>
  );
}
