import { useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api,
  apiV2List,
  apiList,
  type DatabaseTarget,
  type TargetSchema,
} from "../lib/contracts";
import {
  type Project,
  type ProjectInput,
  type ProjectMember,
} from "../lib/projects";
import { infoHref } from "../lib/flyway-info";
import { schemaHref } from "../lib/navigation";
import { canRun } from "../lib/permissions";
import type { Run } from "../lib/execution";
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
  SearchBox,
} from "../components/ui";
import History from "./History";
import DeploymentTargets from "./DeploymentTargets";

type Resource = { target: DatabaseTarget; schema: TargetSchema };
function useResources() {
  return useQuery({
    queryKey: ["project-resources"],
    queryFn: async () => {
      const targets = await apiList<DatabaseTarget>("/database-targets");
      const resources: Resource[] = [];
      for (const target of targets) {
        const schemas = await apiList<TargetSchema>(
          `/database-targets/${encodeURIComponent(target.id)}/schemas`,
        );
        for (const schema of schemas) resources.push({ target, schema });
      }
      return { targets, resources };
    },
  });
}
export default function Projects() {
  const { id } = useParams();
  return id ? <ProjectDetails key={id} id={id} /> : <ProjectList />;
}
function ProjectList() {
  const [params, setParams] = useSearchParams(),
    [editing, setEditing] = useState(false);
  const admin = useIdentity().user.role === "Admin";
  const projects = useQuery({
    queryKey: ["projects"],
    queryFn: () => api<Project[]>("/projects"),
  });
  const q = params.get("q") || "",
    status = params.get("status") || "";
  const rows = (projects.data || []).filter(
    (p) =>
      (!status || p.status === status) &&
      [p.name, p.id, p.description]
        .join(" ")
        .toLowerCase()
        .includes(q.toLowerCase()),
  );
  const set = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next);
  };
  return (
    <>
      <PageHeading
        title="Projects"
        description="Organize existing database and schema resources across environments."
        actions={
          admin && (
            <Button variant="primary" onClick={() => setEditing(true)}>
              Create project
            </Button>
          )
        }
      />
      <div className="filter-bar">
        <SearchBox
          value={q}
          onChange={(v) => set("q", v)}
          placeholder="Search project, identifier or description…"
        />
        <select
          aria-label="Project status"
          value={status}
          onChange={(e) => set("status", e.target.value)}
        >
          <option value="">All statuses</option>
          <option value="active">Active</option>
          <option value="archived">Archived</option>
        </select>
      </div>
      {projects.isPending ? (
        <div className="skeleton" role="status" />
      ) : projects.isError ? (
        <Notice tone="error">
          {projects.error.message}{" "}
          <Button onClick={() => void projects.refetch()}>Retry</Button>
        </Notice>
      ) : (
        <DataTable
          data={rows}
          empty="No projects are available in this view."
          columns={[
            {
              header: "Project",
              accessorKey: "name",
              cell: ({ row }) => (
                <Link to={`/projects/${encodeURIComponent(row.original.id)}`}>
                  {row.original.name}
                </Link>
              ),
            },
            { header: "Description", accessorKey: "description" },
            {
              header: "Status",
              accessorKey: "status",
              cell: ({ row }) => <Badge>{row.original.status}</Badge>,
            },
            {
              header: "Visible scopes",
              id: "scopes",
              cell: ({ row }) => row.original.members.length,
            },
            {
              header: "Updated",
              accessorKey: "updated_at",
              cell: ({ row }) =>
                new Date(row.original.updated_at).toLocaleString(),
            },
          ]}
        />
      )}
      <p className="small muted">
        Project membership organizes resources. Existing permissions and account
        schema allowlists control access and execution.
      </p>
      {editing && <ProjectEditor close={() => setEditing(false)} />}
    </>
  );
}
function ProjectDetails({ id }: { id: string }) {
  const identity = useIdentity(),
    admin = identity.user.role === "Admin";
  const [editing, setEditing] = useState(false),
    [params, setParams] = useSearchParams();
  const project = useQuery({
    queryKey: ["project", id],
    queryFn: () => api<Project>(`/projects/${encodeURIComponent(id)}`),
  });
  const registry = useResources();
  const members = project.data?.members || [];
  const resources = (registry.data?.resources || []).filter((r) =>
    members.some(
      (m) =>
        m.target_id === r.target.id &&
        (!m.schema_id || m.schema_id === r.schema.id),
    ),
  );
  const currentTab = params.get("tab") || "resources";
  return (
    <>
      <PageHeading
        title={project.data?.name || "Project details"}
        breadcrumb={
          <>
            <Link to="/projects">Projects</Link> / Details
          </>
        }
        description={project.data?.description}
        actions={
          admin &&
          project.data && (
            <Button onClick={() => setEditing(true)}>Edit project</Button>
          )
        }
      />
      {project.isPending ? (
        <div className="skeleton" role="status" />
      ) : project.isError ? (
        <Notice tone="error">
          {project.error.message}{" "}
          <Button onClick={() => void project.refetch()}>Retry</Button>
        </Notice>
      ) : (
        <>
          <KeyValues
            rows={[
              ["Stable identifier", <code>{project.data.id}</code>],
              ["Status", <Badge>{project.data.status}</Badge>],
              ["Created", new Date(project.data.created_at).toLocaleString()],
              ["Updated", new Date(project.data.updated_at).toLocaleString()],
            ]}
          />
          {project.data.status === "archived" && (
            <Notice>
              Archived project. Restore it to change membership. Resource
              permissions and existing migration workflows are independent of
              project status.
            </Notice>
          )}
          <div className="tabs" role="tablist" aria-label="Project sections">
            {(
              ["resources", "targets", "history", "legacy-history"] as const
            ).map((tab) => (
              <button
                key={tab}
                role="tab"
                aria-selected={currentTab === tab}
                className={currentTab === tab ? "active" : ""}
                onClick={() => {
                  const next = new URLSearchParams();
                  if (tab !== "resources") next.set("tab", tab);
                  setParams(next);
                }}
              >
                {
                  {
                    resources: "Databases / Schemas",
                    targets: "Deployment Targets",
                    history: "Project history",
                    "legacy-history": "Legacy history",
                  }[tab]
                }
              </button>
            ))}
          </div>
          {currentTab === "targets" ? (
            <DeploymentTargets
              projectID={id}
              projectActive={project.data.status === "active"}
            />
          ) : currentTab === "history" ? (
            <ProjectAttributedHistory projectID={id} />
          ) : currentTab === "legacy-history" ? (
            <>
              <Notice>
                Legacy history is grouped using current project membership.
                Adding a scope includes prior runs; removing it removes them
                from this view. These runs do not carry immutable project
                attribution.
              </Notice>
              <History projectID={id} />
            </>
          ) : (
            <>
              {registry.isPending ? (
                <div className="skeleton" role="status" />
              ) : registry.isError ? (
                <Notice tone="error">
                  {registry.error.message}{" "}
                  <Button onClick={() => void registry.refetch()}>Retry</Button>
                </Notice>
              ) : (
                <>
                  <h2>Databases / Schemas</h2>
                  <p>
                    {members.length} visible membership scopes ·{" "}
                    {resources.length} authorized registered schemas
                  </p>
                  <DataTable
                    data={resources}
                    empty="No authorized registered schemas in this project. Resources may remain unassigned."
                    columns={[
                      {
                        header: "Schema",
                        id: "schema",
                        cell: ({ row }) => (
                          <Link
                            to={schemaHref(
                              row.original.target.id,
                              row.original.schema.id,
                            )}
                          >
                            {row.original.schema.name}
                          </Link>
                        ),
                      },
                      {
                        header: "Instance",
                        id: "target",
                        cell: ({ row }) => (
                          <Link
                            to={`/instances/${encodeURIComponent(row.original.target.id)}`}
                          >
                            {row.original.target.name}
                          </Link>
                        ),
                      },
                      {
                        header: "Environment",
                        id: "environment",
                        cell: ({ row }) => (
                          <Badge>{row.original.target.environment}</Badge>
                        ),
                      },
                      {
                        header: "Registration",
                        id: "active",
                        cell: ({ row }) => (
                          <Badge>
                            {row.original.target.active &&
                            row.original.schema.active
                              ? "Active"
                              : "Inactive"}
                          </Badge>
                        ),
                      },
                      {
                        header: "Actions",
                        id: "actions",
                        cell: ({ row }) => (
                          <div className="actions wrap">
                            <Link
                              to={infoHref(
                                row.original.target.id,
                                row.original.schema.id,
                              )}
                            >
                              Flyway Info
                            </Link>
                            {canRun(
                              identity,
                              row.original.target,
                              row.original.schema,
                            ) && (
                              <Link
                                to={`/runs/new?${new URLSearchParams({ target_id: row.original.target.id, schema_id: row.original.schema.id })}`}
                              >
                                Run migration
                              </Link>
                            )}
                            <Link
                              to={`/runs?${new URLSearchParams({ target_id: row.original.target.id, schema_id: row.original.schema.id })}`}
                            >
                              History
                            </Link>
                          </div>
                        ),
                      },
                    ]}
                  />
                  <h3>Membership scopes</h3>
                  {members.map((m) => (
                    <p key={`${m.target_id}/${m.schema_id || ""}`}>
                      <Link
                        to={
                          m.schema_id
                            ? schemaHref(m.target_id, m.schema_id)
                            : `/instances/${encodeURIComponent(m.target_id)}/schemas`
                        }
                      >
                        {registry.data?.targets.find(
                          (t) => t.id === m.target_id,
                        )?.name || m.target_id}
                        {m.schema_id
                          ? " / " +
                            (registry.data?.resources.find(
                              (r) => r.schema.id === m.schema_id,
                            )?.schema.name || m.schema_id)
                          : " / Instance scope (authorized schemas)"}
                      </Link>
                    </p>
                  ))}
                </>
              )}
            </>
          )}
          {editing && (
            <ProjectEditor
              project={project.data}
              close={() => setEditing(false)}
            />
          )}
        </>
      )}
    </>
  );
}
function ProjectEditor({
  project,
  close,
}: {
  project?: Project;
  close: () => void;
}) {
  const client = useQueryClient(),
    registry = useResources();
  const [name, setName] = useState(project?.name || ""),
    [description, setDescription] = useState(project?.description || ""),
    [status, setStatus] = useState<ProjectInput["status"]>(
      project?.status || "active",
    ),
    [members, setMembers] = useState<ProjectMember[]>(project?.members || []),
    [scopeQuery, setScopeQuery] = useState("");
  const save = useMutation({
    mutationFn: () =>
      api<Project>(
        project ? `/projects/${encodeURIComponent(project.id)}` : "/projects",
        {
          method: project ? "PUT" : "POST",
          body: JSON.stringify({ name, description, status, members }),
        },
      ),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["projects"] });
      await client.invalidateQueries({ queryKey: ["project"] });
      await client.invalidateQueries({ queryKey: ["runs"] });
      close();
    },
  });
  const toggle = (m: ProjectMember) => {
    setMembers((old) =>
      old.some(
        (v) =>
          v.target_id === m.target_id &&
          (v.schema_id || "") === (m.schema_id || ""),
      )
        ? old.filter(
            (v) =>
              !(
                v.target_id === m.target_id &&
                (v.schema_id || "") === (m.schema_id || "")
              ),
          )
        : [...old, m],
    );
  };
  const locked = project?.status === "archived";
  const scopes = [
    ...(registry.data?.targets || []).map((t) => ({
      member: { target_id: t.id } as ProjectMember,
      label: `${t.name} · ${t.environment} · Instance scope`,
    })),
    ...(registry.data?.resources || []).map((r) => ({
      member: { target_id: r.target.id, schema_id: r.schema.id },
      label: `${r.target.name} / ${r.schema.name} · ${r.target.environment}`,
    })),
  ];
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title={project ? "Edit project" : "Create project"}
      description="Group existing resources; membership grants no migration permission."
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
        <Field label="Description">
          <textarea
            maxLength={2000}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </Field>
        <Field label="Status">
          <select
            value={status}
            onChange={(e) =>
              setStatus(e.target.value as ProjectInput["status"])
            }
          >
            <option value="active">Active</option>
            <option value="archived">Archived</option>
          </select>
        </Field>
        <h3>Resource membership</h3>
        {locked && (
          <Notice>
            Save with Active status first to restore membership editing.
          </Notice>
        )}
        <SearchBox
          value={scopeQuery}
          onChange={setScopeQuery}
          placeholder="Search instances, schemas or environments…"
        />
        {registry.isPending ? (
          <div className="skeleton" role="status" />
        ) : registry.isError ? (
          <Notice tone="error">
            {registry.error.message}{" "}
            <Button type="button" onClick={() => void registry.refetch()}>
              Retry
            </Button>
          </Notice>
        ) : (
          <div className="project-scope-list">
            {scopes
              .filter((s) =>
                s.label.toLowerCase().includes(scopeQuery.toLowerCase()),
              )
              .map(({ member, label }) => (
                <label
                  className="checkbox"
                  key={`${member.target_id}/${member.schema_id || ""}`}
                >
                  <input
                    type="checkbox"
                    disabled={locked || save.isPending}
                    checked={members.some(
                      (v) =>
                        v.target_id === member.target_id &&
                        (v.schema_id || "") === (member.schema_id || ""),
                    )}
                    onChange={() => toggle(member)}
                  />
                  {label}
                </label>
              ))}
          </div>
        )}
        <p className="small muted">
          {members.length} selected scopes. Instance scopes include current and
          future authorized schemas. Multiple projects can reference the same
          instance.
        </p>
        {save.isError && <Notice tone="error">{save.error.message}</Notice>}
        <div className="actions">
          <Button type="button" onClick={close}>
            Cancel
          </Button>
          <Button
            variant="primary"
            disabled={
              !name.trim() ||
              save.isPending ||
              registry.isPending ||
              registry.isError
            }
          >
            {save.isPending
              ? "Saving…"
              : project
                ? "Save project"
                : "Create project"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}

function ProjectAttributedHistory({ projectID }: { projectID: string }) {
  const query = useQuery({
    queryKey: ["project-attributed-runs", projectID],
    queryFn: () =>
      apiV2List<Run>(
        `/projects/${encodeURIComponent(projectID)}/migration-runs`,
      ),
    refetchInterval: (q) =>
      q.state.data?.some(
        (r) =>
          ["QUEUED", "PREFLIGHT", "RUNNING", "REPAIRING"].includes(r.status) ||
          (r.status === "FAILED" &&
            (!r.repair_outcome || r.repair_outcome === "PENDING")),
      )
        ? 2000
        : false,
  });
  const rows = query.data ?? [];
  return (
    <section>
      <h2>Project attributed history</h2>
      <p className="small muted">
        Runs in this view carry immutable project and deployment target
        attribution.
      </p>
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
          empty="No project attributed runs yet."
          columns={[
            {
              header: "Run",
              id: "run",
              cell: ({ row }) => (
                <Link
                  to={`/runs/${encodeURIComponent(row.original.run_id || row.original.id)}`}
                >
                  {row.original.run_id || row.original.id}
                </Link>
              ),
            },
            {
              header: "Status",
              accessorKey: "status",
              cell: ({ row }) => <Badge>{row.original.status}</Badge>,
            },
            {
              header: "Deployment target",
              id: "dt",
              cell: ({ row }) => (
                <code>
                  {(row.original as Run & { deployment_target_id?: string })
                    .deployment_target_id || "—"}
                </code>
              ),
            },
            {
              header: "Destination",
              id: "dest",
              cell: ({ row }) => (
                <>
                  {row.original.target?.name || row.original.target_id} /{" "}
                  {row.original.schema?.name || row.original.schema_id}
                </>
              ),
            },
            {
              header: "External source",
              id: "source",
              cell: ({ row }) => {
                const source = (
                  row.original as Run & { external_source?: unknown }
                ).external_source;
                return source === undefined || source === null ? (
                  "—"
                ) : (
                  <pre className="small code-card">
                    {JSON.stringify(source, null, 2)}
                  </pre>
                );
              },
            },
            {
              header: "Requested",
              accessorKey: "created_at",
              cell: ({ row }) =>
                row.original.created_at
                  ? new Date(row.original.created_at).toLocaleString()
                  : "—",
            },
            {
              header: "Completion",
              id: "completion",
              cell: ({ row }) =>
                (row.original as Run & { completed?: boolean }).completed
                  ? "Completed"
                  : row.original.status,
            },
          ]}
        />
      )}
    </section>
  );
}
