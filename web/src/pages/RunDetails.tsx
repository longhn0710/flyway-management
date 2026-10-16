import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/contracts";
import type {
  Artifact,
  RepairEvidence,
  Run,
  RunExecution,
  RunLog,
} from "../lib/execution";
import {
  Badge,
  Button,
  Notice,
  PageHeading,
  Panel,
  SearchBox,
  Status,
} from "../components/ui";

const terminal = new Set([
  "SUCCEEDED",
  "FAILED",
  "BLOCKED",
  "NEEDS_REVIEW",
  "CANCELLED",
]);
const settled = (run: Run) =>
  terminal.has(run.status) &&
  !(
    run.status === "FAILED" &&
    (!run.repair_outcome || run.repair_outcome === "PENDING")
  );
export default function RunDetails() {
  const { id = "" } = useParams();
  const [q, setQ] = useState("");
  const [channel, setChannel] = useState("");
  const [offset, setOffset] = useState(0);
  const run = useQuery({
    queryKey: ["run", id],
    queryFn: () => api<Run>(`/migration-runs/${encodeURIComponent(id)}`),
    refetchInterval: (data) =>
      data.state.data && !settled(data.state.data) ? 2000 : false,
  });
  const logs = useQuery({
    queryKey: [
      "run-logs",
      id,
      q,
      channel,
      offset,
      run.data?.status,
      run.data?.repair_outcome,
    ],
    queryFn: () =>
      api<RunLog[]>(
        `/migration-runs/${encodeURIComponent(id)}/logs?limit=100&offset=${offset}${q ? `&q=${encodeURIComponent(q)}` : ""}${channel ? `&channel=${encodeURIComponent(channel)}` : ""}`,
      ),
    refetchInterval: run.data && !settled(run.data) ? 2000 : false,
  });
  const artifacts = useQuery({
    queryKey: ["run-artifacts", id],
    queryFn: () =>
      api<Artifact[]>(`/migration-runs/${encodeURIComponent(id)}/artifacts`),
  });
  const executions = useQuery({
    queryKey: [
      "run-executions",
      id,
      run.data?.status,
      run.data?.repair_outcome,
    ],
    queryFn: () =>
      api<RunExecution[]>(
        `/migration-runs/${encodeURIComponent(id)}/executions`,
      ),
  });
  const repair = useQuery({
    queryKey: ["run-repair", id, run.data?.repair_outcome],
    queryFn: () =>
      api<RepairEvidence>(`/migration-runs/${encodeURIComponent(id)}/repair`),
    enabled:
      !!run.data?.repair_outcome && run.data.repair_outcome !== "PENDING",
  });
  const row = run.data;
  return (
    <>
      <PageHeading
        title="Run details"
        breadcrumb={
          <>
            <Link to="/runs">Run History</Link> / <code>{id}</code>
          </>
        }
        description="Execution evidence, migration outcome and repair are shown as separate results."
        actions={
          <Link className="button secondary" to="/runs">
            Back to Run History
          </Link>
        }
      />
      {run.isPending ? (
        <div className="skeleton" role="status" aria-label="Loading run" />
      ) : run.isError ? (
        <Notice tone="error">
          {run.error.message}{" "}
          <Button onClick={() => void run.refetch()}>Retry</Button>
        </Notice>
      ) : (
        row && (
          <>
            <div className="inline">
              <Status value={row.status} />
              <span className="mono">{row.run_id || row.id}</span>
              <Badge>{row.source}</Badge>
              <span className="muted">
                Requested by {row.actor_name} ({row.actor_kind})
              </span>
            </div>
            {row.status === "FAILED" && (
              <Notice tone="error" title="Migration failed">
                {row.reason || "Flyway reported a failed migration."} Oracle DDL
                may have partially committed; this run remains failed regardless
                of repair outcome.
              </Notice>
            )}
            {row.status === "NEEDS_REVIEW" || row.status === "BLOCKED" ? (
              <Notice tone="warning" title="Execution requires review">
                {row.reason ||
                  "The execution outcome is not safe to retry automatically."}
              </Notice>
            ) : null}
            <Panel
              title="Run snapshot"
              aside={
                <a
                  className="button secondary"
                  href={`/api/v1/migration-runs/${encodeURIComponent(id)}/record`}
                >
                  Download run record
                </a>
              }
            >
              <dl className="key-values">
                <div>
                  <dt>Environment / target</dt>
                  <dd>
                    {row.target?.environment} · {row.target?.name} ·{" "}
                    {row.target?.service}
                  </dd>
                </div>
                <div>
                  <dt>Target schema / history</dt>
                  <dd>
                    {row.schema?.name} · {row.schema?.history_schema}.
                    {row.schema?.history_table}
                  </dd>
                </div>
                <div>
                  <dt>Execution account</dt>
                  <dd>
                    {row.account?.name} · {row.account?.username}
                  </dd>
                </div>
                <div>
                  <dt>Artifact</dt>
                  <dd>
                    <code>{row.artifact_id}</code>
                  </dd>
                </div>
                <div>
                  <dt>Flyway version</dt>
                  <dd>{row.flyway_version || "—"}</dd>
                </div>
                <div>
                  <dt>Migration outcome</dt>
                  <dd>
                    {row.migration_outcome || "—"} · exit {row.exit_code ?? "—"}
                  </dd>
                </div>
                <div>
                  <dt>Repair outcome</dt>
                  <dd>{row.repair_outcome || "—"}</dd>
                </div>
                <div>
                  <dt>Created / started / finished</dt>
                  <dd>
                    {[row.created_at, row.started_at, row.finished_at]
                      .map((v) => (v ? new Date(v).toLocaleString() : "—"))
                      .join(" / ")}
                  </dd>
                </div>
              </dl>
              {Object.keys(row.context ?? {}).length > 0 && (
                <p className="small">
                  Context:{" "}
                  {Object.entries(row.context)
                    .map(([k, v]) => `${k}: ${v}`)
                    .join(" · ")}
                </p>
              )}
            </Panel>
            {repair.data && (
              <Panel title={`Conditional repair · ${repair.data.outcome}`}>
                <Notice
                  tone={
                    repair.data.outcome === "SUCCEEDED" ? "warning" : "error"
                  }
                  title="Repair does not change migration outcome"
                >
                  {repair.data.reason ||
                    "Flyway repair evidence is recorded separately."}{" "}
                  The original migration SQL was not rerun.
                </Notice>
                <pre className="code-card">
                  Before: {JSON.stringify(repair.data.before, null, 2)}
                  {"\n"}After: {JSON.stringify(repair.data.after, null, 2)}
                </pre>
              </Panel>
            )}
            <Panel title="Artifact manifest">
              {artifacts.isPending ? (
                <div className="skeleton" role="status" />
              ) : artifacts.isError ? (
                <Notice tone="error">{artifacts.error.message}</Notice>
              ) : (
                (artifacts.data ?? []).map((bundle) => (
                  <div key={bundle.id}>
                    <p>
                      <strong>{bundle.id}</strong> · {bundle.sha256} ·{" "}
                      {bundle.total_bytes} bytes
                    </p>
                    <div className="table-scroll">
                      <table>
                        <thead>
                          <tr>
                            <th>Path</th>
                            <th>Kind / version</th>
                            <th>SHA-256</th>
                            <th>Download</th>
                          </tr>
                        </thead>
                        <tbody>
                          {bundle.files.map((file) => (
                            <tr key={file.path}>
                              <td className="mono">{file.path}</td>
                              <td>
                                {file.kind} {file.version}
                              </td>
                              <td className="mono small">{file.sha256}</td>
                              <td>
                                <a
                                  href={`/api/v1/artifacts/${encodeURIComponent(bundle.id)}/files?path=${encodeURIComponent(file.path)}`}
                                >
                                  SQL
                                </a>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                    {bundle.inputs.map((input, index) => (
                      <p key={`${input.name}-${index}`} className="small">
                        Original:{" "}
                        <a
                          href={`/api/v1/artifacts/${encodeURIComponent(bundle.id)}/inputs?index=${index}`}
                        >
                          {input.name}
                        </a>{" "}
                        · {input.sha256}
                      </p>
                    ))}
                  </div>
                ))
              )}
            </Panel>
            <Panel title="Flyway executions">
              {executions.isPending ? (
                <div className="skeleton" role="status" />
              ) : executions.isError ? (
                <Notice tone="error">{executions.error.message}</Notice>
              ) : (
                (executions.data ?? []).map((item) => (
                  <details key={item.id}>
                    <summary>
                      <Status value={item.success ? "SUCCEEDED" : "FAILED"} />{" "}
                      {item.command} · exit {item.exit_code ?? "—"}
                    </summary>
                    <pre className="code-card">
                      {item.stdout}
                      {item.stderr ? `\n${item.stderr}` : ""}
                      {item.json !== undefined
                        ? `\n${JSON.stringify(item.json, null, 2)}`
                        : ""}
                    </pre>
                  </details>
                ))
              )}
            </Panel>
            <Panel
              title="Searchable logs"
              aside={
                <div className="inline">
                  <select
                    aria-label="Log channel"
                    value={channel}
                    onChange={(e) => {
                      setChannel(e.target.value);
                      setOffset(0);
                    }}
                  >
                    <option value="">All channels</option>
                    <option>stdout</option>
                    <option>stderr</option>
                    <option>event</option>
                  </select>
                  <SearchBox
                    value={q}
                    onChange={(value) => {
                      setQ(value);
                      setOffset(0);
                    }}
                    placeholder="Search logs…"
                  />
                </div>
              }
            >
              {logs.isPending ? (
                <div className="skeleton" role="status" />
              ) : logs.isError ? (
                <Notice tone="error">
                  {logs.error.message}{" "}
                  <Button onClick={() => void logs.refetch()}>Retry</Button>
                </Notice>
              ) : (logs.data ?? []).length === 0 ? (
                <p className="empty">No log entries match.</p>
              ) : (
                <>
                  <div className="log-viewer">
                    {logs.data.map((line) => (
                      <div
                        className="log-row"
                        key={`${line.sequence}-${line.created_at}`}
                      >
                        <span className="muted">
                          {new Date(line.created_at).toLocaleTimeString()}
                        </span>
                        <Badge>{line.channel}</Badge>
                        <code>{line.command}</code>
                        <pre>{line.message}</pre>
                      </div>
                    ))}
                  </div>
                  <div className="inline">
                    <Button
                      disabled={offset === 0}
                      onClick={() => setOffset(Math.max(0, offset - 100))}
                    >
                      Previous
                    </Button>
                    <Button
                      disabled={logs.data.length < 100}
                      onClick={() => setOffset(offset + 100)}
                    >
                      Next
                    </Button>
                  </div>
                </>
              )}
            </Panel>
          </>
        )
      )}
    </>
  );
}
