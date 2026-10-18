import { NativeInfoCapture } from "../components/NativeInfoCapture";
import { SQLScripts } from "../components/SQLScripts";
import { matchingFile, runFileHref, type RunFile } from "../lib/sql-viewer";
import type { InfoCapture } from "../lib/flyway-info";
import type { Project } from "../lib/projects";
import { schemaHref } from "../lib/navigation";
import { useEffect, useRef, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/contracts";
import type {
  Artifact,
  RepairEvidence,
  Run,
  RunExecution,
  RunLog,
} from "../lib/execution";
import { repairNotice, summarizeFlywayResult } from "../lib/run-details";
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
  const [params] = useSearchParams();
  const tab = ["info", "sql", "logs"].includes(params.get("tab") || "")
    ? params.get("tab")!
    : "overview";
  const [q, setQ] = useState("");
  const [channel, setChannel] = useState("");
  const [offset, setOffset] = useState(0);
  const [follow, setFollow] = useState(true);
  const logViewer = useRef<HTMLDivElement>(null);
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
  useEffect(() => {
    if (!follow || !logs.data || q || channel) return;
    if (logs.data.length === 100 && run.data && !settled(run.data)) {
      const timer = window.setInterval(
        () => setOffset((current) => current + 100),
        1000,
      );
      return () => window.clearInterval(timer);
    }
    if (logViewer.current)
      logViewer.current.scrollTop = logViewer.current.scrollHeight;
  }, [follow, logs.data, logs.dataUpdatedAt, q, channel, run.data, tab]);
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
  const files = useQuery({
    queryKey: ["run-files", id, row?.status],
    queryFn: () =>
      api<RunFile[]>(`/migration-runs/${encodeURIComponent(id)}/files`),
    enabled: !!row,
    refetchInterval: row && !settled(row) ? 2000 : false,
  });
  const info = useQuery({
    queryKey: ["flyway-info", row?.target_id, row?.schema_id, id],
    queryFn: () =>
      api<InfoCapture | null>(
        `/database-targets/${encodeURIComponent(row!.target_id)}/schemas/${encodeURIComponent(row!.schema_id)}/flyway-info?run_id=${encodeURIComponent(id)}`,
      ),
    enabled: !!row && tab === "info",
    refetchInterval: row && !settled(row) ? 2000 : false,
  });
  const projects = useQuery({
    queryKey: ["projects"],
    queryFn: () => api<Project[]>("/projects"),
    enabled: !!row,
  });
  const memberships = (projects.data || []).filter((p) =>
    p.members.some(
      (m) =>
        m.target_id === row?.target_id &&
        (!m.schema_id || m.schema_id === row?.schema_id),
    ),
  );
  const validation = executions.data
    ?.filter((e) => e.command === "validate")
    .at(-1);
  const duration =
    row?.started_at && row?.finished_at
      ? `${Math.max(0, (Date.parse(row.finished_at) - Date.parse(row.started_at)) / 1000)}s`
      : "—";
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
            <Link to="/runs">Migration history</Link> / <code>{id}</code>
          </>
        }
        description="Historical execution outcome, native migration information and immutable source SQL."
        actions={
          <Link className="button secondary" to="/runs">
            Back to history
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
                  <p className="eyebrow">
                    {row.target?.engine === "postgresql"
                      ? "PostgreSQL"
                      : "Oracle"}{" "}
                    migration run
                  </p>
                  <h2 className="mono">{row.run_id || row.id}</h2>
                </div>
                <Status value={row.status} />
              </div>
              <p className="run-destination">
                <Link to={`/instances/${encodeURIComponent(row.target_id)}`}>
                  {row.target?.name || row.target_id}
                </Link>{" "}
                /{" "}
                <span className="mono">
                  {row.target?.database_name || row.target?.service}
                </span>{" "}
                /{" "}
                <Link to={schemaHref(row.target_id, row.schema_id)}>
                  {row.schema?.name}
                </Link>{" "}
                · <Badge>{row.target?.environment}</Badge>
              </p>
              {!settled(row) && (
                <p className="small muted" aria-live="polite">
                  This run is still active. Status, execution evidence and logs
                  refresh automatically.
                </p>
              )}
              <div className="run-overview-meta">
                <Badge>{row.source}</Badge>
                <span>
                  Flyway {row.flyway_version || "Version not recorded"}
                </span>
                <span>
                  Requested by {row.actor_name} ({row.actor_kind})
                </span>
              </div>
            </section>
            {row.status === "FAILED" && (
              <Notice tone="error" title="Migration failed">
                {row.reason || "Flyway reported a failed migration."}{" "}
                {row.target?.engine === "postgresql"
                  ? "The run remains held for operator review. Earlier scripts may already be committed. No automatic PostgreSQL retry or Repair is performed."
                  : "Oracle DDL may have partially committed; failed or uncertain runs hold the stream for review. No automatic Repair or SQL retry is performed."}
              </Notice>
            )}
            {row.status === "NEEDS_REVIEW" ||
            row.status === "BLOCKED" ||
            (row.target?.engine === "postgresql" && row.status === "CANCELLED") ? (
              <Notice
                tone="warning"
                title={
                  row.target?.engine === "postgresql" && row.status === "CANCELLED"
                    ? "Migration cancelled"
                    : row.target?.engine === "postgresql" && row.status === "BLOCKED"
                      ? "Migration blocked"
                      : "Execution requires review"
                }
              >
                {row.target?.engine === "postgresql"
                  ? row.status === "NEEDS_REVIEW"
                    ? `${row.reason || "The PostgreSQL execution outcome could not be confirmed."} The run remains held for operator review. Earlier scripts may already be committed. No automatic retry or Repair is performed.`
                    : row.status === "CANCELLED"
                      ? "Cancelled before execution. No migration SQL ran, so no retry or Repair is needed."
                      : row.reason || "The migration was blocked before dispatch."
                  : row.reason ||
                    "The execution outcome is not safe to retry automatically."}
              </Notice>
            ) : null}
            <nav className="instance-tabs run-tabs" aria-label="Run sections">
              {[
                ["overview", "Overview"],
                ["info", "Migration Info"],
                ["sql", "SQL Scripts"],
                ["logs", "Execution Logs"],
              ].map(([key, label]) => (
                <Link
                  key={key}
                  className={tab === key ? "active" : undefined}
                  aria-current={tab === key ? "page" : undefined}
                  to={`?tab=${key}`}
                >
                  {label}
                </Link>
              ))}
            </nav>
            {tab === "overview" && (
              <>
                <div className="run-outcomes" aria-label="Execution outcomes">
                  <div>
                    <span>Preflight</span>
                    <strong>
                      {row.preflight
                        ? row.preflight.ready
                          ? "Passed"
                          : "Blocked"
                        : "Not recorded"}
                    </strong>
                  </div>
                  <div>
                    <span>Migration</span>
                    <strong>{row.migration_outcome || "Not recorded"}</strong>
                  </div>
                  <div>
                    <span>Validation · latest execution</span>
                    <strong>
                      {validation
                        ? validation.success
                          ? "Passed"
                          : "Failed"
                        : "Not recorded"}
                    </strong>
                  </div>
                  <div className="muted">
                    <span>Repair</span>
                    <strong>{row.repair_outcome || "Not recorded"}</strong>
                    {row.repair_outcome === "SKIPPED" &&
                      row.status === "SUCCEEDED" && (
                        <small>No repair needed</small>
                      )}
                  </div>
                </div>
                <div className="actions wrap section-heading">
                  <Link className="button primary" to="?tab=info">
                    View Migration Info
                  </Link>
                  <Link className="button secondary" to="?tab=sql">
                    Browse SQL scripts
                  </Link>
                </div>
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
                      <dt>Projects · current membership</dt>
                      <dd>
                        {projects.isPending
                          ? "Loading…"
                          : projects.isError
                            ? "Membership unavailable"
                            : memberships.length
                              ? memberships.map((p, i) => (
                                  <span key={p.id}>
                                    {i > 0 && ", "}
                                    <Link
                                      to={`/projects/${encodeURIComponent(p.id)}`}
                                    >
                                      {p.name}
                                    </Link>
                                  </span>
                                ))
                              : "No current project membership"}
                      </dd>
                    </div>
                    <div>
                      <dt>Engine / instance</dt>
                      <dd>
                        {row.target?.engine === "postgresql"
                          ? "PostgreSQL"
                          : "Oracle"}{" "}
                        · {row.target?.environment} ·{" "}
                        <Link
                          to={`/instances/${encodeURIComponent(row.target_id)}`}
                        >
                          {row.target?.name || "View instance"}
                        </Link>{" "}
                        · {row.target?.database_name || row.target?.service}
                      </dd>
                    </div>
                    <div>
                      <dt>Allowed schema / Flyway history</dt>
                      <dd>
                        <Link to={schemaHref(row.target_id, row.schema_id)}>
                          {row.schema?.name || "View schema"}
                        </Link>{" "}
                        · {row.schema?.history_schema}.
                        {row.schema?.history_table}
                      </dd>
                    </div>
                    <div>
                      <dt>Execution account</dt>
                      <dd>
                        {row.account?.username
                          ? `${row.account.name} · ${row.account.username}`
                          : "Account identity unavailable"}
                      </dd>
                    </div>
                    <div>
                      <dt>Artifact</dt>
                      <dd>
                        <code>{row.artifact_id}</code>
                        {row.artifact_name && (
                          <p className="small">{row.artifact_name}</p>
                        )}
                        <p className="small mono">
                          SHA-256:{" "}
                          {artifacts.data?.find((a) => a.id === row.artifact_id)
                            ?.sha256 || "Unavailable"}
                        </p>
                      </dd>
                    </div>
                    <div>
                      <dt>Flyway version</dt>
                      <dd>{row.flyway_version || "—"}</dd>
                    </div>
                    <div>
                      <dt>Migration outcome</dt>
                      <dd>
                        {row.migration_outcome || "—"} · exit{" "}
                        {row.exit_code ?? "—"}
                      </dd>
                    </div>
                    <div>
                      <dt>Duration</dt>
                      <dd>{duration}</dd>
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
                {repair.data && repair.data.outcome !== "SKIPPED" && (
                  <Notice
                    tone={repairSummary?.tone ?? "info"}
                    title={repairSummary?.title}
                  >
                    {row.target?.engine === "postgresql"
                      ? "Automatic PostgreSQL Repair is disabled; the original migration SQL was not rerun."
                      : `${repairSummary?.message} The original migration SQL was not rerun.`}
                  </Notice>
                )}
                <details className="advanced-diagnostics">
                  <summary>
                    Advanced Diagnostics · execution JSON and repair audit
                  </summary>
                  <div className="diagnostic-list">
                    {repair.data && (
                      <details>
                        <summary>
                          Repair {repair.data.outcome} · audit evidence
                        </summary>
                        <p>{repair.data.reason}</p>
                        <pre className="code-card">
                          {JSON.stringify(repair.data, null, 2)}
                        </pre>
                      </details>
                    )}
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
                                <Status
                                  value={item.success ? "SUCCEEDED" : "FAILED"}
                                />{" "}
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
                                  <pre className="code-card">
                                    {summary.prettyJson}
                                  </pre>
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
                  </div>
                </details>
              </>
            )}
            {tab === "info" && (
              <>
                <div className="spread wrap section-heading">
                  <div>
                    <h2>Migration Info</h2>
                    <p className="small muted">
                      Historical Info from run{" "}
                      <span className="mono">{id}</span>. This capture describes
                      the database at the recorded time.
                    </p>
                  </div>
                  <Link
                    to={`/flyway-info?${new URLSearchParams({ target: row.target_id, schema: row.schema_id })}`}
                  >
                    Latest recorded Info
                  </Link>
                </div>
                {info.isPending ? (
                  <div
                    className="skeleton"
                    role="status"
                    aria-label="Loading Flyway Info"
                  />
                ) : info.isError ? (
                  <Notice tone="error">
                    {info.error.message}{" "}
                    <Button onClick={() => void info.refetch()}>
                      Retry Info
                    </Button>
                  </Notice>
                ) : !info.data ? (
                  <Notice>
                    No native Info capture is available for this run. Newer
                    captures are not substituted for historical evidence.
                  </Notice>
                ) : (
                  <NativeInfoCapture
                    recorded={info.data}
                    sqlLink={(migration) => {
                      const file = matchingFile(migration, files.data || []);
                      return file ? runFileHref(id, file) : null;
                    }}
                  />
                )}
                {files.isError && (
                  <Notice tone="warning">
                    The effective SQL manifest is unavailable.{" "}
                    {files.error.message}
                  </Notice>
                )}
              </>
            )}
            {tab === "sql" &&
              (files.isPending ? (
                <div
                  className="skeleton"
                  role="status"
                  aria-label="Loading manifest"
                />
              ) : files.isError ? (
                <Notice tone="error">
                  Historical artifact manifest unavailable.{" "}
                  {files.error.message}{" "}
                  <Button onClick={() => void files.refetch()}>
                    Retry manifest
                  </Button>
                </Notice>
              ) : (
                <SQLScripts
                  run={id}
                  files={files.data || []}
                  artifacts={artifacts.data || []}
                />
              ))}
            {tab === "logs" && (
              <>
                <Panel
                  title="Searchable logs"
                  className="run-details-logs"
                  aside={
                    <div className="inline wrap">
                      <Button
                        aria-pressed={follow}
                        onClick={() => setFollow(!follow)}
                      >
                        {follow ? "Pause follow" : "Follow logs"}
                      </Button>
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
                  <p className="small muted" role="status">
                    {logs.dataUpdatedAt
                      ? `Last refreshed ${new Date(logs.dataUpdatedAt).toLocaleTimeString()}`
                      : "Loading logs…"}{" "}
                    · {follow ? "Following latest entries" : "Follow paused"}.
                    Connection loss does not stop server execution.
                  </p>
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
                      <div
                        className="log-viewer"
                        ref={logViewer}
                        tabIndex={0}
                        role="region"
                        aria-label="Migration log entries"
                      >
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
                                #{line.sequence} ·{" "}
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
                          onClick={() => {
                            setFollow(false);
                            setOffset(Math.max(0, offset - 100));
                          }}
                        >
                          Previous
                        </Button>
                        <Button
                          disabled={logs.data.length < 100}
                          onClick={() => {
                            setFollow(false);
                            setOffset(offset + 100);
                          }}
                        >
                          Next
                        </Button>
                      </div>
                    </>
                  )}
                </Panel>
              </>
            )}
          </>
        )
      )}
    </>
  );
}
