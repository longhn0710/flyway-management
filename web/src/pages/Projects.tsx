import { useState } from "react";
import {
  Link,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api,
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
    [deleting, setDeleting] = useState(false),
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
  const history = params.get("tab") === "history";
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
          project.data &&
          !project.isError && (
            <>
              <Button onClick={() => setEditing(true)}>Edit project</Button>
              <Button variant="danger" onClick={() => setDeleting(true)}>
                Delete project
              </Button>
            </>
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
            {["resources", "history"].map((tab) => (
              <button
                key={tab}
                role="tab"
                aria-selected={
                  history ? tab === "history" : tab === "resources"
                }
                className={
                  (history ? tab === "history" : tab === "resources")
                    ? "active"
                    : ""
                }
                onClick={() => {
                  const next = new URLSearchParams();
                  if (tab === "history") next.set("tab", tab);
                  setParams(next);
                }}
              >
                {tab === "resources"
                  ? "Databases / Schemas"
                  : "Migration history"}
              </button>
            ))}
          </div>
          {history ? (
            <>
              <Notice>
                History uses current project membership and your current read
                permissions. Adding a scope includes its prior runs; removing it
                removes those runs from this view. Original execution snapshots
                and actor attribution stay unchanged.
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
          {deleting && (
            <ProjectDeleteDialog
              project={project.data}
              close={() => setDeleting(false)}
            />
          )}
        </>
      )}
    </>
  );
}
function ProjectDeleteDialog({
  project,
  close,
}: {
  project: Project;
  close: () => void;
}) {
  const client = useQueryClient(),
    navigate = useNavigate();
  const [confirmation, setConfirmation] = useState("");
  const blocked = project.members.length > 0;
  const deletion = useMutation({
    mutationFn: () =>
      api<{ deleted: boolean }>(`/projects/${encodeURIComponent(project.id)}`, {
        method: "DELETE",
        body: JSON.stringify({ confirmation_name: confirmation }),
      }),
    onSuccess: () => {
      navigate("/projects", { replace: true });
      client.removeQueries({ queryKey: ["project", project.id], exact: true });
      void client.invalidateQueries({ queryKey: ["projects"] });
    },
  });
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open && !deletion.isPending) close();
      }}
      title="Delete project"
      description={`Delete ${project.name} from Projects.`}
    >
      <p>
        Instances, schemas, accounts, SQL, migration history and audit records
        are preserved.
      </p>
      {blocked && (
        <Notice tone="warning">
          Remove all {project.members.length} membership scopes before deleting
          this project.
          {project.status === "archived" &&
            " Unarchive it first to edit membership."}
        </Notice>
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!blocked && confirmation === project.name && !deletion.isPending)
            deletion.mutate();
        }}
      >
        <Field
          label="Confirm project name"
          required
          hint={
            <>
              Type <strong>{project.name}</strong> exactly.
            </>
          }
        >
          <input
            required
            autoComplete="off"
            value={confirmation}
            maxLength={120}
            disabled={blocked || deletion.isPending}
            onChange={(e) => setConfirmation(e.target.value)}
          />
        </Field>
        {deletion.isError && (
          <Notice tone="error">{deletion.error.message}</Notice>
        )}
        <div className="actions">
          <Button type="button" disabled={deletion.isPending} onClick={close}>
            Cancel
          </Button>
          <Button
            type="submit"
            variant="danger"
            disabled={
              blocked || confirmation !== project.name || deletion.isPending
            }
          >
            {deletion.isPending ? "Deleting…" : "Delete project"}
          </Button>
        </div>
      </form>
    </Modal>
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
