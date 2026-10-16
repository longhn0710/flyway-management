import { useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Link, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api,
  apiList,
  type DatabaseTarget,
  type TargetSchema,
  type ExecutionAccount,
} from "../lib/contracts";
import { type Artifact } from "../lib/execution";
import {
  type InfoCapture,
  type NativeMigration,
  nativeValue,
} from "../lib/flyway-info";
import { useIdentity } from "../lib/identity-context";
import { canRun } from "../lib/permissions";
import { schemaHref } from "../lib/navigation";
import {
  Badge,
  Button,
  DataTable,
  Field,
  KeyValues,
  Notice,
  PageHeading,
  Panel,
  SearchBox,
} from "../components/ui";

export default function FlywayInfo() {
  const [params] = useSearchParams();
  const target = params.get("target") || "",
    schema = params.get("schema") || "",
    run = params.get("run_id") || "";
  return (
    <InfoView
      key={`${target}/${schema}/${run}`}
      target={target}
      schema={schema}
      run={run}
    />
  );
}
function InfoView({
  target,
  schema,
  run,
}: {
  target: string;
  schema: string;
  run: string;
}) {
  const identity = useIdentity(),
    client = useQueryClient();
  const returnFocus = useRef<string | null>(null);
  const [params, setParams] = useSearchParams();
  const [account, setAccount] = useState(""),
    [artifact, setArtifact] = useState<Artifact | null>(null),
    [files, setFiles] = useState<File[]>([]),
    [selected, setSelected] = useState<NativeMigration | null>(null);
  const base = `/database-targets/${encodeURIComponent(target)}/schemas/${encodeURIComponent(schema)}/flyway-info`;
  const key = ["flyway-info", target, schema, run];
  const registry = useQuery({
    queryKey: ["info-target", target],
    queryFn: () =>
      api<DatabaseTarget>(`/database-targets/${encodeURIComponent(target)}`),
    enabled: !!target,
  });
  const schemas = useQuery({
    queryKey: ["schemas", target],
    queryFn: () =>
      apiList<TargetSchema>(
        `/database-targets/${encodeURIComponent(target)}/schemas`,
      ),
    enabled: !!target,
  });
  const stream = schemas.data?.find((s) => s.id === schema);
  const operate = !!(
    registry.data &&
    stream &&
    canRun(identity, registry.data, stream)
  );
  const info = useQuery({
    queryKey: key,
    queryFn: () =>
      api<InfoCapture | null>(
        `${base}${run ? `?run_id=${encodeURIComponent(run)}` : ""}`,
      ),
    enabled: !!target && !!schema,
  });
  const accounts = useQuery({
    queryKey: ["info-accounts", target, schema],
    queryFn: () =>
      api<ExecutionAccount[]>(
        `/database-targets/${encodeURIComponent(target)}/schemas/${encodeURIComponent(schema)}/execution-accounts`,
      ),
    enabled: operate,
  });
  const recorded = info.data;
  const accountID = account || recorded?.snapshot.account.id || "";
  const artifactID = artifact?.id || recorded?.artifact_id || "";
  const upload = useMutation({
    mutationFn: async () => {
      const body = new FormData();
      for (const f of files)
        body.append(
          "files",
          f,
          (f as File & { webkitRelativePath?: string }).webkitRelativePath ||
            f.name,
        );
      return api<Artifact>(
        `/artifacts?target_id=${encodeURIComponent(target)}&schema_id=${encodeURIComponent(schema)}`,
        { method: "POST", body },
      );
    },
    onSuccess: setArtifact,
  });
  const refresh = useMutation({
    mutationFn: () =>
      api<InfoCapture>(`${base}/refresh`, {
        method: "POST",
        body: JSON.stringify({
          execution_account_id: accountID,
          artifact_id: artifactID,
        }),
      }),
    onSuccess: async (value) => {
      client.setQueryData(["flyway-info", target, schema, ""], value);
      const next = new URLSearchParams(params);
      next.delete("run_id");
      setParams(next);
      await client.invalidateQueries({
        queryKey: ["flyway-info", target, schema],
      });
    },
  });
  const set = (name: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(name, value);
    else next.delete(name);
    next.delete("page");
    setParams(next);
  };
  const rows = recorded?.json?.migrations;
  const migrations = Array.isArray(rows) ? rows : [];
  const counts = new Map<string, number>();
  for (const row of migrations)
    counts.set(
      row.state || "Unknown",
      (counts.get(row.state || "Unknown") || 0) + 1,
    );
  const filtered = migrations.filter(
    (row) =>
      (!params.get("state") || row.state === params.get("state")) &&
      (!params.get("category") || row.category === params.get("category")) &&
      [row.version, row.description, row.filepath, row.script]
        .join(" ")
        .toLowerCase()
        .includes((params.get("q") || "").toLowerCase()),
  );
  return (
    <>
      <PageHeading
        title="Flyway Info"
        breadcrumb={
          <>
            <Link to={schemaHref(target, schema)}>Schema</Link> / Flyway Info
          </>
        }
        description="Native migration states from the pinned Flyway CLI."
        actions={
          <Link
            to={`/runs?target_id=${encodeURIComponent(target)}&schema_id=${encodeURIComponent(schema)}`}
          >
            Migration history
          </Link>
        }
      />
      {!target || !schema ? (
        <Notice tone="warning">
          Open Flyway Info from a registered schema or migration run.
        </Notice>
      ) : (
        <>
          {run && (
            <Notice>
              Historical Info from run{" "}
              <Link to={`/runs/${encodeURIComponent(run)}`}>{run}</Link>.{" "}
              <Link
                to={`/flyway-info?${new URLSearchParams({ target, schema })}`}
              >
                Latest recorded Info
              </Link>
            </Notice>
          )}
          {info.isPending ? (
            <div
              className="skeleton"
              role="status"
              aria-label="Loading Flyway Info"
            />
          ) : info.isError ? (
            <Notice tone="error">
              {info.error.message}{" "}
              <Button onClick={() => void info.refetch()}>Retry</Button>
            </Notice>
          ) : !recorded ? (
            <Notice>
              No recorded Flyway Info. Select an execution account and immutable
              migration artifact to capture read-only status.
            </Notice>
          ) : (
            <>
              <Panel
                title={`${recorded.snapshot.target.name} / ${recorded.snapshot.schema.name}`}
                aside={
                  <Badge tone={recorded.success ? "neutral" : "warning"}>
                    {recorded.success
                      ? "Recorded snapshot"
                      : "Partial / failed inspection"}
                  </Badge>
                }
              >
                <KeyValues
                  rows={[
                    [
                      "Captured",
                      new Date(recorded.captured_at).toLocaleString(),
                    ],
                    [
                      "Source",
                      recorded.source_run_id ? (
                        <Link
                          to={`/runs/${encodeURIComponent(recorded.source_run_id)}`}
                        >
                          Run {recorded.source_run_id} · execution {recorded.id}
                        </Link>
                      ) : (
                        `Read-only inspection ${recorded.id}`
                      ),
                    ],
                    [
                      "Artifact",
                      <span className="mono">
                        {recorded.artifact_id}
                        <br />
                        {recorded.artifact_sha256}
                      </span>,
                    ],
                  ]}
                />
                <details>
                  <summary>
                    Capture details · account, history and runtime
                  </summary>
                  <KeyValues
                    rows={[
                      ["Captured by", recorded.actor_name],
                      [
                        "Execution account",
                        `${recorded.snapshot.account.name} · ${recorded.snapshot.account.username}`,
                      ],
                      [
                        "History",
                        `${recorded.snapshot.schema.history_schema}.${recorded.snapshot.schema.history_table}`,
                      ],
                      [
                        "Schema version",
                        nativeValue(recorded.json?.schemaVersion),
                      ],
                      ["Runtime", recorded.runtime_version],
                      [
                        "Database identity",
                        nativeValue(
                          recorded.json?.database ??
                            recorded.json?.databaseType,
                        ) + " · CLI-reported; DBID not independently verified",
                      ],
                      [
                        "Captured endpoint",
                        `${recorded.snapshot.target.host}:${recorded.snapshot.target.port}/${recorded.snapshot.target.service}`,
                      ],
                    ]}
                  />
                </details>
                {recorded.warnings.map((w, i) => (
                  <Notice key={i} tone="warning">
                    {w}
                  </Notice>
                ))}
              </Panel>
              <section aria-label="Native migration information">
                <div className="spread wrap section-heading">
                  <h2>Native migrations</h2>
                  <span>
                    {migrations.length} total · {filtered.length} matching
                  </span>
                </div>
                <div className="actions wrap">
                  {[...counts].map(([state, count]) => (
                    <Badge key={state}>
                      {state}: {count}
                    </Badge>
                  ))}
                </div>
                <div className="filter-bar">
                  <SearchBox
                    value={params.get("q") || ""}
                    onChange={(v) => set("q", v)}
                    placeholder="Search version, description or filepath…"
                  />
                  <select
                    aria-label="Native state"
                    value={params.get("state") || ""}
                    onChange={(e) => set("state", e.target.value)}
                  >
                    <option value="">All states</option>
                    {[...counts.keys()].map((s) => (
                      <option key={s}>{s}</option>
                    ))}
                  </select>
                  <select
                    aria-label="Migration category"
                    value={params.get("category") || ""}
                    onChange={(e) => set("category", e.target.value)}
                  >
                    <option value="">All categories</option>
                    {[
                      ...new Set(
                        migrations.map((r) => r.category).filter(Boolean),
                      ),
                    ].map((c) => (
                      <option key={c}>{c}</option>
                    ))}
                  </select>
                </div>
                {!Array.isArray(rows) ? (
                  <Notice tone="warning">
                    Native migration table unavailable. Inspect diagnostics for
                    incomplete or invalid output.
                  </Notice>
                ) : (
                  <DataTable
                    data={filtered}
                    pageSize={20}
                    empty={
                      recorded.success
                        ? "No native migrations match this view. An empty table does not prove that schema history exists."
                        : "No migration rows were returned by this failed inspection."
                    }
                    columns={[
                      {
                        header: "Category",
                        accessorKey: "category",
                        cell: ({ row }) => nativeValue(row.original.category),
                      },
                      {
                        header: "Version",
                        accessorKey: "version",
                        cell: ({ row }) => (
                          <Button
                            variant="ghost"
                            id={`info-version-${row.id}`}
                            onClick={(event) => {
                              returnFocus.current = event.currentTarget.id;
                              setSelected(row.original);
                            }}
                          >
                            {nativeValue(row.original.version)}
                          </Button>
                        ),
                      },
                      {
                        header: "Description",
                        accessorKey: "description",
                        cell: ({ row }) => (
                          <button
                            className="text-link"
                            id={`info-description-${row.id}`}
                            onClick={(event) => {
                              returnFocus.current = event.currentTarget.id;
                              setSelected(row.original);
                            }}
                          >
                            {nativeValue(row.original.description)}
                          </button>
                        ),
                      },
                      { header: "Type", accessorKey: "type" },
                      {
                        header: "Installed On",
                        id: "installed",
                        cell: ({ row }) =>
                          nativeValue(
                            row.original.installedOnUTC ??
                              row.original.installedOn,
                          ),
                      },
                      {
                        header: "State",
                        accessorKey: "state",
                        cell: ({ row }) => (
                          <Badge
                            tone={
                              row.original.state === "Success"
                                ? "success"
                                : row.original.state?.includes("Failed")
                                  ? "error"
                                  : row.original.state === "Pending"
                                    ? "accent"
                                    : "neutral"
                            }
                          >
                            {nativeValue(row.original.state)}
                          </Badge>
                        ),
                      },
                      {
                        header: "Undoable",
                        accessorKey: "undoable",
                        cell: ({ row }) => nativeValue(row.original.undoable),
                      },
                    ]}
                  />
                )}
              </section>
              <details className="panel">
                <summary>
                  Detailed diagnostics · original redacted stdout / stderr /
                  JSON
                </summary>
                <div className="panel-content">
                  <p>
                    Exit code {recorded.exit_code} · {recorded.files.length}{" "}
                    captured files
                  </p>
                  <h3>File manifest</h3>
                  <pre>{JSON.stringify(recorded.files, null, 2)}</pre>
                  <h3>stdout</h3>
                  <pre>{recorded.stdout || "—"}</pre>
                  <h3>stderr</h3>
                  <pre>{recorded.stderr || "—"}</pre>
                  <h3>Native JSON</h3>
                  <pre>{JSON.stringify(recorded.json, null, 2)}</pre>
                  {recorded.source_run_id && (
                    <Link
                      to={`/runs/${encodeURIComponent(recorded.source_run_id)}`}
                    >
                      Associated execution logs
                    </Link>
                  )}
                </div>
              </details>
            </>
          )}
          {operate ? (
            <Panel title="Refresh Flyway Info (read-only)">
              <p>
                Uses the current registered destination and selected account,
                with the immutable artifact plus reserved stream files. Only
                native info runs; migration and repair safeguards remain
                enforced.
              </p>
              <div className="grid-two">
                <Field label="Execution account">
                  <select
                    value={accountID}
                    onChange={(e) => setAccount(e.target.value)}
                  >
                    <option value="">Select account</option>
                    {accounts.data?.map((a) => (
                      <option key={a.id} value={a.id}>
                        {a.name} · {a.username}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field
                  label="Migration artifact"
                  hint="Use a complete migration package for meaningful Pending / Missing states."
                >
                  <input
                    type="file"
                    multiple
                    accept=".sql,.zip"
                    onChange={(e) => {
                      setFiles(Array.from(e.target.files || []));
                      upload.reset();
                    }}
                  />
                  <Button
                    disabled={
                      !files.length || upload.isPending || refresh.isPending
                    }
                    onClick={() => upload.mutate()}
                  >
                    {upload.isPending
                      ? "Uploading…"
                      : "Upload immutable artifact"}
                  </Button>
                </Field>
              </div>
              {accounts.isError && (
                <Notice tone="error">{accounts.error.message}</Notice>
              )}
              {upload.isError && (
                <Notice tone="error">{upload.error.message}</Notice>
              )}
              <p className="mono">
                Artifact: {artifactID || "Required"}
                <br />
                SHA-256: {artifact?.sha256 || recorded?.artifact_sha256 || "—"}
              </p>
              {refresh.isError && (
                <Notice tone="error">{refresh.error.message}</Notice>
              )}
              <Button
                variant="primary"
                disabled={
                  !accountID ||
                  !artifactID ||
                  refresh.isPending ||
                  upload.isPending ||
                  !accounts.data?.some(
                    (a) => a.id === accountID && a.credential.configured,
                  )
                }
                onClick={() => refresh.mutate()}
              >
                {refresh.isPending ? "Inspecting…" : "Refresh Flyway Info"}
              </Button>
            </Panel>
          ) : (
            <Notice>
              Read-only viewing. Live refresh requires existing operate
              permission, including explicit PROD authorization and a mapped
              execution account.
            </Notice>
          )}
        </>
      )}
      <Dialog.Root
        open={!!selected}
        onOpenChange={(open) => {
          if (!open) setSelected(null);
        }}
      >
        <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <Dialog.Content
            className="dialog-content info-drawer"
            onCloseAutoFocus={(event) => {
              // Table cells can remount when drawer state changes. Resolve the
              // current row button rather than retaining a detached element.
              const trigger = returnFocus.current
                ? document.getElementById(returnFocus.current)
                : null;
              if (trigger) {
                event.preventDefault();
                trigger.focus();
              }
            }}
          >
            <Dialog.Title>Migration details</Dialog.Title>
            <Dialog.Description>
              Complete native Flyway migration metadata.
            </Dialog.Description>
            <Dialog.Close asChild>
              <Button
                className="dialog-close"
                aria-label="Close migration details"
              >
                Close
              </Button>
            </Dialog.Close>
            {selected && (
              <KeyValues
                rows={Object.entries(selected).map(([k, v]) => [
                  k,
                  nativeValue(v),
                ])}
              />
            )}
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  );
}
