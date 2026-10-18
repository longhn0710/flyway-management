import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  apiV2,
  apiV2List,
  apiList,
  type DatabaseTarget,
  type ExecutionAccount,
  type ServiceAccount,
  type TargetSchema,
  type User,
} from "../lib/contracts";
import { useIdentity } from "../lib/identity-context";
import {
  Badge,
  Button,
  DataTable,
  Field,
  Modal,
  Notice,
} from "../components/ui";

interface DeploymentTarget {
  id: string;
  project_id: string;
  name: string;
  environment: DatabaseTarget["environment"];
  target_id: string;
  schema_id: string;
  execution_account_id: string;
  active: boolean;
  revision: number;
  database_name?: string;
  schema_name?: string;
  account_name?: string;
  can_read: boolean;
  can_upload: boolean;
  can_preflight: boolean;
  can_execute: boolean;
}
type Action = "read" | "upload" | "preflight" | "execute";
interface TargetGrant {
  principal_kind: "user" | "service";
  principal_id: string;
  read: boolean;
  upload: boolean;
  preflight: boolean;
  execute: boolean;
}

export default function DeploymentTargets({
  projectID,
  projectActive,
}: {
  projectID: string;
  projectActive: boolean;
}) {
  const identity = useIdentity(),
    admin = identity.user.role === "Admin",
    client = useQueryClient();
  const [create, setCreate] = useState(false);
  const query = useQuery({
    queryKey: ["deployment-targets", projectID],
    queryFn: () =>
      apiV2List<DeploymentTarget>(
        `/projects/${encodeURIComponent(projectID)}/deployment-targets`,
      ),
  });
  const invalidate = () =>
    client.invalidateQueries({ queryKey: ["deployment-targets", projectID] });
  const rows = query.data ?? [];
  return (
    <section>
      <div className="spread wrap section-heading">
        <div>
          <h2>Deployment Targets</h2>
          <p>
            Project destinations bind one environment, registered schema and
            execution account.
          </p>
        </div>
        {admin && (
          <Button
            variant="primary"
            disabled={!projectActive}
            onClick={() => setCreate(true)}
          >
            Create deployment target
          </Button>
        )}
      </div>
      {query.isPending ? (
        <div className="skeleton" role="status" />
      ) : query.isError ? (
        <Notice tone="error">
          {query.error.message}{" "}
          <Button onClick={() => void query.refetch()}>Retry</Button>
        </Notice>
      ) : (
        <DataTable
          data={rows}
          empty="No deployment targets are configured for this project."
          columns={[
            { header: "Name", accessorKey: "name" },
            {
              header: "Environment",
              accessorKey: "environment",
              cell: ({ row }) => <Badge>{row.original.environment}</Badge>,
            },
            {
              header: "Destination",
              id: "destination",
              cell: ({ row }) => (
                <>
                  {row.original.database_name || row.original.target_id} /{" "}
                  {row.original.schema_name || row.original.schema_id}
                </>
              ),
            },
            {
              header: "Execution account",
              id: "account",
              cell: ({ row }) =>
                row.original.account_name || row.original.execution_account_id,
            },
            {
              header: "Status",
              accessorKey: "active",
              cell: ({ row }) => (
                <Badge>
                  {row.original.active ? "Active" : "Retired"} · rev{" "}
                  {row.original.revision}
                </Badge>
              ),
            },
            {
              header: "Your access",
              id: "access",
              cell: ({ row }) => (
                <span>
                  {(
                    [
                      "can_read",
                      "can_upload",
                      "can_preflight",
                      "can_execute",
                    ] as const
                  )
                    .filter((k) => row.original[k])
                    .map((k) => k.replace("can_", ""))
                    .join(" · ") || "None"}
                </span>
              ),
            },
            {
              header: "Admin",
              id: "admin",
              cell: ({ row }) =>
                admin ? (
                  <TargetAdmin
                    projectID={projectID}
                    target={row.original}
                    onSaved={invalidate}
                  />
                ) : (
                  "—"
                ),
            },
          ]}
        />
      )}
      {!projectActive && (
        <Notice>
          This project is archived. Deployment targets cannot be changed until
          it is restored.
        </Notice>
      )}
      {create && (
        <TargetEditor
          projectID={projectID}
          close={() => setCreate(false)}
          onSaved={invalidate}
        />
      )}
    </section>
  );
}

function TargetEditor({
  projectID,
  close,
  onSaved,
}: {
  projectID: string;
  close: () => void;
  onSaved: () => void;
}) {
  const client = useQueryClient();
  const registries = useQuery({
    queryKey: ["project-target-registry"],
    queryFn: async () => {
      const targets = await apiList<DatabaseTarget>("/database-targets");
      const resources: Array<{
        target: DatabaseTarget;
        schema: TargetSchema;
        accounts: ExecutionAccount[];
      }> = [];
      for (const target of targets) {
        const schemas = await apiList<TargetSchema>(
          `/database-targets/${encodeURIComponent(target.id)}/schemas`,
        );
        for (const schema of schemas) {
          const accounts = await apiList<ExecutionAccount>(
            `/database-targets/${encodeURIComponent(target.id)}/schemas/${encodeURIComponent(schema.id)}/execution-accounts`,
          );
          resources.push({ target, schema, accounts });
        }
      }
      return resources;
    },
  });
  const [name, setName] = useState(""),
    [resource, setResource] = useState(""),
    [accountID, setAccountID] = useState("");
  const selected = registries.data?.find(
      (r) => `${r.target.id}/${r.schema.id}` === resource,
    ),
    accounts = selected?.accounts ?? [];
  const save = useMutation({
    mutationFn: () =>
      apiV2<DeploymentTarget>(
        `/projects/${encodeURIComponent(projectID)}/deployment-targets`,
        {
          method: "POST",
          body: JSON.stringify({
            name: name.trim(),
            target_id: selected!.target.id,
            schema_id: selected!.schema.id,
            execution_account_id: accountID,
            environment: selected!.target.environment,
            active: true,
            revision: 0,
          }),
        },
      ),
    onSuccess: async () => {
      await client.invalidateQueries({
        queryKey: ["deployment-targets", projectID],
      });
      onSaved();
      close();
    },
  });
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title="Create deployment target"
      description="Select an existing registered destination and execution account."
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <Field label="Name" required>
          <input
            required
            maxLength={120}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field label="Registered schema" required>
          <select
            required
            value={resource}
            onChange={(e) => {
              setResource(e.target.value);
              setAccountID("");
            }}
          >
            <option value="">Choose database and schema</option>
            {(registries.data ?? [])
              .filter((r) => r.target.active && r.schema.active)
              .map((r) => (
                <option
                  key={`${r.target.id}/${r.schema.id}`}
                  value={`${r.target.id}/${r.schema.id}`}
                >
                  {r.target.name} · {r.schema.name} · {r.target.environment}
                </option>
              ))}
          </select>
        </Field>
        <Field label="Environment">
          <div>
            {selected ? (
              <Badge>{selected.target.environment}</Badge>
            ) : (
              <span className="muted">Inherited from selected database</span>
            )}
          </div>
        </Field>
        <Field label="Execution account" required>
          <select
            required
            value={accountID}
            onChange={(e) => setAccountID(e.target.value)}
            disabled={!selected}
          >
            <option value="">Choose account</option>
            {accounts
              .filter((a) => a.active)
              .map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name} · {a.username}
                </option>
              ))}
          </select>
        </Field>
        {registries.isPending && <div className="skeleton" role="status" />}
        {registries.isError && (
          <Notice tone="error">{registries.error.message}</Notice>
        )}
        {save.isError && <Notice tone="error">{save.error.message}</Notice>}
        <div className="actions">
          <Button type="button" onClick={close}>
            Cancel
          </Button>
          <Button
            variant="primary"
            disabled={!selected || !accountID || !name.trim() || save.isPending}
          >
            Create target
          </Button>
        </div>
      </form>
    </Modal>
  );
}

function TargetAdmin({
  projectID,
  target,
  onSaved,
}: {
  projectID: string;
  target: DeploymentTarget;
  onSaved: () => void;
}) {
  const client = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(target.name),
    [active, setActive] = useState(target.active);
  const [principal, setPrincipal] = useState("");
  const [kind, setKind] = useState<TargetGrant["principal_kind"]>("user");
  const [bits, setBits] = useState<Record<Action, boolean>>({
    read: false,
    upload: false,
    preflight: false,
    execute: false,
  });
  const users = useQuery({
    queryKey: ["users"],
    queryFn: () => apiList<User>("/access/users"),
  });
  const services = useQuery({
    queryKey: ["service-accounts"],
    queryFn: () => apiList<ServiceAccount>("/access/service-accounts"),
  });
  const principals =
    kind === "user"
      ? (users.data ?? []).map((u) => ({
          id: u.id,
          name: `${u.display_name} (${u.username})`,
        }))
      : (services.data ?? []).map((s) => ({ id: s.id, name: s.name }));
  const endpoint = `/projects/${encodeURIComponent(projectID)}/deployment-targets/${encodeURIComponent(target.id)}`;
  const grants = useQuery({
    queryKey: ["deployment-target-grants", projectID, target.id],
    queryFn: () => apiV2<TargetGrant[]>(`${endpoint}/grants`),
  });
  const selectedGrant = grants.data?.find(
    (g) => g.principal_kind === kind && g.principal_id === principal,
  );
  const save = useMutation({
    mutationFn: () =>
      apiV2<DeploymentTarget>(endpoint, {
        method: "PATCH",
        headers: { "If-Match": `"${target.revision}"` },
        body: JSON.stringify({
          name: name.trim(),
          target_id: target.target_id,
          schema_id: target.schema_id,
          execution_account_id: target.execution_account_id,
          environment: target.environment,
          active,
          revision: target.revision,
        }),
      }),
    onSuccess: () => {
      setEditing(false);
      onSaved();
    },
  });
  const grant = useMutation({
    mutationFn: () =>
      apiV2<TargetGrant>(`${endpoint}/grants`, {
        method: "PUT",
        body: JSON.stringify({
          principal_kind: kind,
          principal_id: principal,
          ...bits,
        }),
      }),
    onSuccess: async () => {
      await client.invalidateQueries({
        queryKey: ["deployment-targets", projectID],
      });
      await client.invalidateQueries({
        queryKey: ["deployment-target-grants", projectID, target.id],
      });
    },
  });
  return (
    <div className="actions wrap">
      <Button onClick={() => setEditing(!editing)}>
        {editing ? "Close" : "Manage"}
      </Button>
      {editing && (
        <div className="project-scope-list">
          <Field label="Display name">
            <input
              value={name}
              maxLength={120}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <label className="checkbox">
            <input
              type="checkbox"
              checked={active}
              onChange={(e) => setActive(e.target.checked)}
            />
            {active ? "Active" : "Retired"}
          </label>
          <Button
            disabled={
              !name.trim() || save.isPending || (!target.active && !active)
            }
            onClick={() => save.mutate()}
          >
            Save revision {target.revision + 1}
          </Button>
          {save.isError && <Notice tone="error">{save.error.message}</Notice>}
          <h4>Project target permissions</h4>
          <p className="small muted">
            These project permissions are combined with the user or service
            account’s existing database/schema grants. Service tokens also need
            matching token scopes.
          </p>
          <div className="inline wrap">
            <select
              aria-label="Principal type"
              value={kind}
              disabled={grants.isPending || grants.isError}
              onChange={(e) => {
                setKind(e.target.value as TargetGrant["principal_kind"]);
                setPrincipal("");
                setBits({
                  read: false,
                  upload: false,
                  preflight: false,
                  execute: false,
                });
              }}
            >
              <option value="user">User</option>
              <option value="service">Service account</option>
            </select>
            <select
              aria-label="Principal"
              value={principal}
              disabled={grants.isPending || grants.isError}
              onChange={(e) => {
                const value = e.target.value;
                setPrincipal(value);
                const existing = grants.data?.find(
                  (g) => g.principal_kind === kind && g.principal_id === value,
                );
                setBits({
                  read: existing?.read ?? false,
                  upload: existing?.upload ?? false,
                  preflight: existing?.preflight ?? false,
                  execute: existing?.execute ?? false,
                });
              }}
            >
              <option value="">Choose principal</option>
              {principals.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </div>
          {principal && grants.isPending ? (
            <div className="skeleton" role="status" />
          ) : principal && grants.isError ? (
            <Notice tone="error">
              Could not load current target permissions: {grants.error.message}
            </Notice>
          ) : (
            principal && (
              <>
                <p className="small">
                  {selectedGrant
                    ? "Current grant loaded. Saving replaces this principal’s four target permissions."
                    : "No target grant exists yet; all actions begin disabled."}
                </p>
                <div className="inline wrap">
                  {(["read", "upload", "preflight", "execute"] as Action[]).map(
                    (a) => (
                      <label className="checkbox" key={a}>
                        <input
                          type="checkbox"
                          checked={bits[a]}
                          disabled={
                            grants.isPending ||
                            grants.isError ||
                            grant.isPending
                          }
                          onChange={(e) =>
                            setBits({ ...bits, [a]: e.target.checked })
                          }
                        />
                        {a}
                      </label>
                    ),
                  )}
                  <Button
                    disabled={
                      !principal ||
                      grants.isPending ||
                      grants.isError ||
                      grant.isPending
                    }
                    onClick={() => grant.mutate()}
                  >
                    Save permissions
                  </Button>
                </div>
              </>
            )
          )}
          {grant.isError && <Notice tone="error">{grant.error.message}</Notice>}
        </div>
      )}
    </div>
  );
}
