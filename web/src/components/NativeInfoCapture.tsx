import { useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Link, useSearchParams } from "react-router-dom";
import {
  type InfoCapture,
  type NativeMigration,
  nativeValue,
} from "../lib/flyway-info";
import { Badge, Button, DataTable, KeyValues, Notice, SearchBox } from "./ui";

// Render the recorded native payload directly; no state or migration interpretation.
export function NativeInfoCapture({
  recorded,
  sqlLink,
}: {
  recorded: InfoCapture;
  sqlLink?: (row: NativeMigration) => string | null;
}) {
  const [params, setParams] = useSearchParams();
  const [selected, setSelected] = useState<NativeMigration | null>(null);
  const returnFocus = useRef<string | null>(null);
  const set = (name: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(name, value);
    else next.delete(name);
    next.delete("page");
    setParams(next);
  };
  const rows = recorded?.json?.migrations;
  const migrations: NativeMigration[] = Array.isArray(rows)
    ? rows.filter(
        (row): row is NativeMigration =>
          !!row && typeof row === "object" && !Array.isArray(row),
      )
    : [];
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
      <section
        className="info-provenance"
        aria-label="Historical capture identity"
      >
        <div className="spread wrap">
          <p className="small">
            <strong>
              Captured {new Date(recorded.captured_at).toLocaleString()}
            </strong>{" "}
            · Flyway {recorded.runtime_version}
          </p>
          <Badge tone={recorded.success ? "neutral" : "warning"}>
            {recorded.success
              ? "Recorded snapshot"
              : "Partial / failed inspection"}
          </Badge>
        </div>
        <p className="small">
          Source artifact <span className="mono">{recorded.artifact_id}</span>
        </p>
        {recorded.warnings.map((w, i) =>
          i === 0 && recorded.success ? (
            <p className="small muted" key={i}>
              {w}
            </p>
          ) : (
            <Notice key={i} tone="warning">
              {w}
            </Notice>
          ),
        )}
        <details>
          <summary>Capture details · account, history and runtime</summary>
          <KeyValues
            rows={[
              [
                "Destination",
                `${recorded.snapshot.target.name} / ${recorded.snapshot.target.service} / ${recorded.snapshot.schema.name}`,
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
                "Artifact SHA-256",
                <span className="mono">{recorded.artifact_sha256}</span>,
              ],
              ["Captured by", recorded.actor_name],
              [
                "Execution account",
                recorded.snapshot.account.username
                  ? `${recorded.snapshot.account.name} · ${recorded.snapshot.account.username}`
                  : "Account identity unavailable",
              ],
              [
                "History",
                `${recorded.snapshot.schema.history_schema}.${recorded.snapshot.schema.history_table}`,
              ],
              ["Schema version", nativeValue(recorded.json?.schemaVersion)],
              ["Runtime", recorded.runtime_version],
              [
                "Database identity",
                nativeValue(
                  recorded.json?.database ?? recorded.json?.databaseType,
                ) + " · CLI-reported; DBID not independently verified",
              ],
              [
                "Captured endpoint",
                `${recorded.snapshot.target.host}:${recorded.snapshot.target.port}/${recorded.snapshot.target.service}`,
              ],
            ]}
          />
        </details>
      </section>
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
              ...new Set(migrations.map((r) => r.category).filter(Boolean)),
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
            key={`${params.get("q")}/${params.get("state")}/${params.get("category")}`}
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
                accessorFn: (row) => row.installedOnUTC ?? row.installedOn,
                cell: ({ row }) =>
                  nativeValue(
                    row.original.installedOnUTC ?? row.original.installedOn,
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
      <details className="advanced-diagnostics">
        <summary>
          Effective file manifest · {recorded.files.length} captured files
        </summary>
        <div className="diagnostic-list">
          <p className="small muted">
            Files captured for this inspection; native states are relative to
            this set.
          </p>
          {recorded.files.map((file) => (
            <p key={file.path} className="small mono">
              {file.path} · {file.size} bytes · SHA-256 {file.sha256}
            </p>
          ))}
        </div>
      </details>
      <details className="panel">
        <summary>
          Advanced Diagnostics · Detailed diagnostics · original redacted stdout
          / stderr / JSON
        </summary>
        <div className="panel-content">
          <p>
            Exit code {recorded.exit_code} · {recorded.files.length} captured
            files
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
              to={`/runs/${encodeURIComponent(recorded.source_run_id)}?tab=logs`}
            >
              Associated execution logs
            </Link>
          )}
        </div>
      </details>
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
            {selected && sqlLink?.(selected) && (
              <Link className="button secondary" to={sqlLink(selected)!}>
                Open SQL script
              </Link>
            )}
            {selected && sqlLink && !sqlLink(selected) && (
              <Notice>
                No matching SQL file is available in this run’s effective
                manifest. Filename matches must be unambiguous.
              </Notice>
            )}
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
