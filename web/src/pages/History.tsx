import { schemaHref } from "../lib/navigation";
import { Link, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  api,
  apiList,
  type DatabaseTarget,
  type TargetSchema,
} from "../lib/contracts";
import type { Run, RunStatus } from "../lib/execution";
import {
  Badge,
  Button,
  Notice,
  PageHeading,
  SearchBox,
  Status,
} from "../components/ui";

const pageSize = 20;
const states: RunStatus[] = [
  "QUEUED",
  "PREFLIGHT",
  "RUNNING",
  "REPAIRING",
  "SUCCEEDED",
  "FAILED",
  "BLOCKED",
  "NEEDS_REVIEW",
  "CANCELLED",
];
export default function History({
  targetID,
  projectID,
}: { targetID?: string; projectID?: string } = {}) {
  const [params, setParams] = useSearchParams();
  const q = params.get("q") ?? "";
  const filters = {
    status: params.get("status") ?? "",
    target_id: targetID ?? params.get("target_id") ?? "",
    schema_id: params.get("schema_id") ?? "",
    actor: params.get("actor") ?? "",
    source: params.get("source") ?? "",
    from: params.get("from") ?? "",
    to: params.get("to") ?? "",
    q: params.get("q") ?? "",
  };
  const parsedOffset = Number(params.get("offset") || 0);
  const offset = Number.isFinite(parsedOffset)
    ? Math.max(0, Math.floor(parsedOffset / pageSize) * pageSize)
    : 0;
  const targets = useQuery({
    queryKey: ["database-targets"],
    queryFn: () => apiList<DatabaseTarget>("/database-targets"),
  });
  const schemas = useQuery({
    queryKey: ["schemas", filters.target_id],
    queryFn: () =>
      apiList<TargetSchema>(
        `/database-targets/${encodeURIComponent(filters.target_id)}/schemas`,
      ),
    enabled: !!filters.target_id,
  });
  const query = new URLSearchParams({
    limit: String(pageSize),
    offset: String(offset),
  });
  for (const [key, value] of Object.entries(filters))
    if (value)
      query.set(
        key,
        key === "from"
          ? `${value}T00:00:00Z`
          : key === "to"
            ? `${value}T23:59:59Z`
            : value,
      );
  const runs = useQuery({
    queryKey: ["runs", projectID, filters, offset],
    queryFn: () =>
      api<Run[]>(
        `${projectID ? `/projects/${encodeURIComponent(projectID)}/migration-runs` : "/migration-runs"}?${query.toString()}`,
      ),
    refetchInterval: (query) =>
      query.state.data?.some(
        (run) =>
          ["QUEUED", "PREFLIGHT", "RUNNING", "REPAIRING"].includes(
            run.status,
          ) ||
          (run.status === "FAILED" &&
            (!run.repair_outcome || run.repair_outcome === "PENDING")),
      )
        ? 2000
        : false,
  });
  const set = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    next.delete("offset");
    setParams(next);
  };
  const targetRows = targets.data ?? [];
  return (
    <>
      {targetID || projectID ? (
        <h2>Migration history</h2>
      ) : (
        <PageHeading
          title="Migration history"
          breadcrumb="Instances / Migration history"
          description="Review migration and repair outcomes across the Oracle instances and allowed schemas you can access."
          actions={
            <Link className="button primary" to="/runs/new">
              Run migration
            </Link>
          }
        />
      )}
      <div className="filter-bar">
        <SearchBox
          value={q}
          onChange={(value) => {
            set("q", value);
          }}
          placeholder="Search run, artifact, requester…"
        />
        <select
          aria-label="Run status"
          value={filters.status}
          onChange={(e) => set("status", e.target.value)}
        >
          <option value="">All statuses</option>
          {states.map((s) => (
            <option key={s}>{s}</option>
          ))}
        </select>
        <select
          aria-label="Database target"
          disabled={!!targetID}
          value={filters.target_id}
          onChange={(e) => {
            const next = new URLSearchParams(params);
            if (e.target.value) next.set("target_id", e.target.value);
            else next.delete("target_id");
            next.delete("schema_id");
            next.delete("offset");
            setParams(next);
          }}
        >
          <option value="">All instances</option>
          {targetRows.map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
            </option>
          ))}
        </select>
        <select
          aria-label="Target schema"
          value={filters.schema_id}
          disabled={!filters.target_id}
          onChange={(e) => set("schema_id", e.target.value)}
        >
          <option value="">All allowed schemas</option>
          {(schemas.data ?? []).map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
        <select
          aria-label="Run source"
          value={filters.source}
          onChange={(e) => set("source", e.target.value)}
        >
          <option value="">All sources</option>
          <option>WEB</option>
          <option>API</option>
        </select>
        <input
          aria-label="Actor filter"
          placeholder="Actor"
          value={filters.actor}
          onChange={(e) => set("actor", e.target.value)}
        />
        <input
          aria-label="From date"
          type="date"
          value={filters.from}
          onChange={(e) => set("from", e.target.value)}
        />
        <input
          aria-label="To date"
          type="date"
          value={filters.to}
          onChange={(e) => set("to", e.target.value)}
        />
      </div>
      {runs.isPending ? (
        <div
          className="skeleton"
          role="status"
          aria-label="Loading run history"
        />
      ) : runs.isError ? (
        <Notice tone="error">
          {runs.error.message}{" "}
          <Button onClick={() => void runs.refetch()}>Retry</Button>
        </Notice>
      ) : runs.data.length === 0 ? (
        <p className="empty">No migration runs match these filters.</p>
      ) : (
        <>
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>Run ID</th>
                  <th>Status</th>
                  <th>Engine / instance</th>
                  <th>Allowed schema</th>
                  <th>Execution account</th>
                  <th>Artifact</th>
                  <th>Actor / source</th>
                  <th>Created / duration</th>
                  <th>Migration / repair</th>
                </tr>
              </thead>
              <tbody>
                {runs.data.map((run) => (
                  <tr key={run.id}>
                    <td>
                      <Link
                        className="mono"
                        to={`/runs/${encodeURIComponent(run.id)}`}
                      >
                        {run.run_id || run.id}
                      </Link>
                    </td>
                    <td>
                      <Status value={run.status} />
                    </td>
                    <td>
                      {run.target?.environment}
                      <br />
                      <span className="muted">
                        Oracle ·{" "}
                        <Link
                          to={`/instances/${encodeURIComponent(run.target_id)}`}
                        >
                          {run.target?.name || "View instance"}
                        </Link>
                      </span>
                    </td>
                    <td>
                      <Link to={schemaHref(run.target_id, run.schema_id)}>
                        {run.schema?.name || "View schema"}
                      </Link>
                    </td>
                    <td>
                      {run.account?.name || run.execution_account_id}
                      <br />
                      <span className="muted">{run.account?.username}</span>
                    </td>
                    <td className="mono">
                      {run.artifact_name || run.artifact_id}
                    </td>
                    <td>
                      {run.actor_name || run.actor_id}
                      <br />
                      <Badge>{run.source}</Badge>
                    </td>
                    <td>
                      {run.created_at
                        ? new Date(run.created_at).toLocaleString()
                        : "—"}
                      <br />
                      <span className="muted">
                        {run.started_at
                          ? run.finished_at
                            ? `${Math.max(0, Date.parse(run.finished_at) - Date.parse(run.started_at))} ms`
                            : "In progress"
                          : "Not started"}
                      </span>
                    </td>
                    <td>
                      {run.migration_outcome || "—"}
                      <br />
                      <span className="muted">
                        Repair: {run.repair_outcome || "—"}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
      {!runs.isPending && !runs.isError && (
        <div className="inline">
          <span className="small muted">
            {runs.data.length
              ? `Showing ${offset + 1}–${offset + runs.data.length}`
              : `No results on page ${Math.floor(offset / pageSize) + 1}`}
          </span>
          <span className="push-right" />
          <Button
            disabled={offset === 0}
            onClick={() => {
              const next = new URLSearchParams(params);
              if (offset <= pageSize) next.delete("offset");
              else next.set("offset", String(offset - pageSize));
              setParams(next);
            }}
          >
            Previous
          </Button>
          <Button
            disabled={runs.data.length < pageSize}
            onClick={() => {
              const next = new URLSearchParams(params);
              next.set("offset", String(offset + pageSize));
              setParams(next);
            }}
          >
            Next
          </Button>
        </div>
      )}
    </>
  );
}
