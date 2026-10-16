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
  repairNotice,
  repairStepLabel,
  repairStepState,
  summarizeFlywayResult,
} from "../lib/run-details";
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
    refetchInterval: run.data && !settled(run.data) ? 2000 : false,
  });
  const repair = useQuery({
    queryKey: ["run-repair", id, run.data?.repair_outcome],
    queryFn: () =>
      api<RepairEvidence>(`/migration-runs/${encodeURIComponent(id)}/repair`),
    enabled:
      !!run.data?.repair_outcome && run.data.repair_outcome !== "PENDING",
  });
  const row = run.data;
  const repairState = row
    ? repairStepState(row.status, row.repair_outcome)
    : "not-started";
  const progressStatus =
    row?.status === "FAILED" &&
    (!row.repair_outcome || row.repair_outcome === "PENDING")
      ? "REPAIRING"
      : row?.status;
  const stageIndex = progressStatus
    ? ["QUEUED", "PREFLIGHT", "RUNNING", "REPAIRING"].indexOf(progressStatus)
    : -1;
  const progressIndex = progressStatus ? (stageIndex < 0 ? 4 : stageIndex) : -1;
  const repairSummary =
    row && repair.data
      ? repairNotice(row.status, repair.data.outcome, repair.data.reason)
      : null;
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
            <section className="run-overview" aria-label="Run status">
              <div className="run-overview-heading">
                <div>
                  <p className="eyebrow">Migration run</p>
                  <h2 className="mono">{row.run_id || row.id}</h2>
                </div>
                <Status value={row.status} />
              </div>
              <div className="run-progress" aria-live="polite">
                <div
                  className="run-progress-track"
                  aria-label={`Current stage: ${progressStatus}`}
                >
                  {[
                    ["QUEUED", "Queued"],
                    ["PREFLIGHT", "Preflight"],
                    ["RUNNING", "Migration"],
                    ["REPAIRING", "Repair"],
                    [
                      "SUCCEEDED",
                      row.status === "SUCCEEDED" ? "Succeeded" : "Outcome",
                    ],
                  ].map(([stage, label], index) => {
                    const isRepair = stage === "REPAIRING";
                    const complete = isRepair
                      ? repairState === "complete"
                      : index < progressIndex ||
                        (row.status === "SUCCEEDED" && index < 3);
                    const current =
                      (isRepair
                        ? repairState === "running"
                        : index === progressIndex) ||
                      (index === 4 &&
                        ["BLOCKED", "NEEDS_REVIEW", "CANCELLED"].includes(
                          row.status,
                        ));
                    const failed = isRepair
                      ? repairState === "failed"
                      : index === 4 &&
                        row.status !== "SUCCEEDED" &&
                        settled(row);
                    const skipped = isRepair && repairState === "skipped";
                    return (
                      <div
                        className={`run-progress-step${complete ? " complete" : ""}${current ? " current" : ""}${failed ? " failed" : ""}${skipped ? " skipped" : ""}`}
                        key={stage}
                        aria-current={current ? "step" : undefined}
                      >
                        <span className="run-progress-dot" />
                        <span>
                          {isRepair
                            ? repairStepLabel(repairState, row.repair_outcome)
                            : label}
                        </span>
                      </div>
                    );
                  })}
                </div>
                {!settled(row) && (
                  <p className="small muted">
                    This run is still active. Status, execution evidence and
                    logs refresh automatically.
                  </p>
                )}
              </div>
              <div className="run-overview-meta">
                <Badge>{row.source}</Badge>
                <span>
                  Requested by {row.actor_name} ({row.actor_kind})
                </span>
              </div>
            </section>
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
              <Panel title={`Repair · ${repair.data.outcome}`}>
                <Notice
                  tone={repairSummary?.tone ?? "info"}
                  title={repairSummary?.title ?? "Repair status"}
                >
                  {repairSummary?.message}
                  {repair.data.outcome !== "SKIPPED" && (
                    <> The original migration SQL was not rerun.</>
                  )}
                </Notice>
                <details className="advanced-diagnostics">
                  <summary>Repair diagnostics</summary>
                  <pre className="code-card">
                    Before: {JSON.stringify(repair.data.before, null, 2)}
                    {"\n"}After: {JSON.stringify(repair.data.after, null, 2)}
                  </pre>
                </details>
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
            <Panel title="Flyway execution diagnostics">
              {executions.isPending ? (
                <div className="skeleton" role="status" />
              ) : executions.isError ? (
                <Notice tone="error">{executions.error.message}</Notice>
              ) : (
                (executions.data ?? []).map((item) => {
                  const summary = summarizeFlywayResult(
                    item.command,
                    item.json,
                    item.success,
                  );
                  const plainStdout = item.stdout
                    .split(/\r?\n/)
                    .filter(
                      (line) =>
                        !summarizeFlywayResult(
                          item.command,
                          line,
                          item.success,
                        ),
                    )
                    .join("\n");
                  return (
                    <details key={item.id}>
                      <summary>
                        <Status value={item.success ? "SUCCEEDED" : "FAILED"} />{" "}
                        {item.command} · exit {item.exit_code ?? "—"}
                        {summary ? ` · ${summary.message}` : ""}
                      </summary>
                      {summary && (
                        <div className="flyway-log-summary">
                          <strong>{summary.title}</strong>
                          <p>{summary.message}</p>
                        </div>
                      )}
                      {(plainStdout || item.stderr) && (
                        <pre className="code-card">
                          {plainStdout}
                          {item.stderr ? `\n${item.stderr}` : ""}
                        </pre>
                      )}
                      {summary ? (
                        <details className="flyway-log-raw">
                          <summary>Original Flyway JSON</summary>
                          <pre className="code-card">{summary.prettyJson}</pre>
                        </details>
                      ) : item.json !== undefined ? (
                        <pre className="code-card">
                          {JSON.stringify(item.json, null, 2)}
                        </pre>
                      ) : null}
                    </details>
                  );
                })
              )}
            </Panel>
            <Panel
              title="Searchable logs"
              className="run-details-logs"
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
                    {logs.data.map((line) => {
                      const execution = (executions.data ?? []).find(
                        (item) =>
                          item.command === line.command &&
                          item.stdout.split(/\r?\n/).includes(line.message),
                      );
                      const summary =
                        line.channel === "stdout"
                          ? summarizeFlywayResult(
                              line.command,
                              line.message,
                              execution?.success,
                            )
                          : null;
                      return (
                        <div
                          className="log-row"
                          key={`${line.sequence}-${line.created_at}`}
                        >
                          <span className="muted">
                            {new Date(line.created_at).toLocaleTimeString()}
                          </span>
                          <Badge>{line.channel}</Badge>
                          <code>{line.command}</code>
                          {summary ? (
                            <div className="flyway-log-entry">
                              <div className="flyway-log-summary">
                                <strong>{summary.title}</strong>
                                <p>{summary.message}</p>
                              </div>
                              <details className="flyway-log-raw">
                                <summary>Original Flyway JSON</summary>
                                <pre>{summary.prettyJson}</pre>
                              </details>
                            </div>
                          ) : (
                            <pre>{line.message}</pre>
                          )}
                        </div>
                      );
                    })}
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
