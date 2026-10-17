import { useEffect, useId, useMemo, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Link, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { type Artifact } from "../lib/execution";
import {
  contentHref,
  highlightSQL,
  linesPerPage,
  readSQL,
  runFileHref,
  type RunFile,
} from "../lib/sql-viewer";
import { Badge, Button, Notice, SearchBox } from "./ui";

export function SQLScripts({
  run,
  files,
  artifacts,
}: {
  run: string;
  files: RunFile[];
  artifacts: Artifact[];
}) {
  const [params] = useSearchParams();
  const path = params.get("path"),
    artifact = params.get("artifact_id");
  const candidates = files.filter(
    (f) => f.path === path && (!artifact || f.artifact_id === artifact),
  );
  const selected = path
    ? candidates.length === 1
      ? candidates[0]
      : undefined
    : files[0];
  const [q, setQ] = useState("");
  const visible = files.filter((f) =>
    `${f.path} ${f.version} ${f.kind}`.toLowerCase().includes(q.toLowerCase()),
  );
  return (
    <>
      <div className="spread wrap section-heading">
        <div>
          <h2>SQL scripts</h2>
          <p className="small muted">
            Immutable effective manifest · {files.length} files. Explorer order
            is by path; Flyway determines execution order.
          </p>
        </div>
        <Link to={`?tab=info`}>Return to Migration Info</Link>
      </div>
      {files.length === 0 ? (
        <Notice>
          No effective file manifest was retained for this run. A queued or
          blocked run may not have reserved any scripts.
        </Notice>
      ) : (
        <div className="script-workspace">
          <aside className="script-explorer" aria-label="Resolved SQL scripts">
            <div className="script-explorer-toolbar">
              <SearchBox
                label="Find script"
                value={q}
                onChange={setQ}
                placeholder="Find script…"
              />
            </div>
            <ul>
              {visible.map((f) => (
                <li key={`${f.artifact_id}/${f.path}`}>
                  <Link
                    to={runFileHref(run, f)}
                    aria-current={selected === f ? "true" : undefined}
                  >
                    <span className="script-name mono">
                      {f.path.split("/").at(-1)}
                    </span>
                    <span className="small muted">
                      {f.kind === "repeatable"
                        ? "Repeatable"
                        : `Versioned · V${f.version}`}{" "}
                      · {f.size.toLocaleString()} bytes
                    </span>
                    <span className="script-path small">{f.path}</span>
                  </Link>
                </li>
              ))}
            </ul>
            {visible.length === 0 && (
              <p className="small muted">No scripts match this search.</p>
            )}
          </aside>
          {selected ? (
            <SQLReader
              key={`${run}/${selected.artifact_id}/${selected.path}`}
              run={run}
              file={selected}
            />
          ) : (
            <Notice tone="warning">
              This script is unavailable in the selected run’s effective
              manifest. Select a retained file from the explorer.
            </Notice>
          )}
        </div>
      )}
      <details className="advanced-diagnostics">
        <summary>
          Original submitted packages · {artifacts.length} contributing
          artifacts
        </summary>
        <div className="diagnostic-list">
          <p className="small muted">
            Packages can contain additional files. The explorer above shows this
            run’s effective files. Only SQL and supported archives accepted by
            existing upload policy are executable sources; non-SQL entries,
            links and nested archives are rejected at upload.
          </p>
          {artifacts.map((a) => (
            <div key={a.id}>
              <p className="small mono">
                Artifact {a.id} · SHA-256 {a.sha256}
              </p>
              {a.inputs.map((input, index) => (
                <p className="small" key={index}>
                  <a
                    href={`/api/v1/artifacts/${encodeURIComponent(a.id)}/inputs?index=${index}`}
                  >
                    {input.name}
                  </a>{" "}
                  · {input.size.toLocaleString()} bytes ·{" "}
                  <span className="mono">{input.sha256}</span>
                </p>
              ))}
            </div>
          ))}
        </div>
      </details>
    </>
  );
}

function SQLReader({ run, file }: { run: string; file: RunFile }) {
  const [expanded, setExpanded] = useState(false);
  const [find, setFind] = useState("");
  const [match, setMatch] = useState(0);
  const [page, setPage] = useState(0);
  const [copied, setCopied] = useState("");
  const sourceID = useId();
  const expandID = useId();
  const url = contentHref(run, file);
  const sql = useQuery({
    queryKey: ["run-sql", run, file.artifact_id, file.path, file.sha256],
    queryFn: ({ signal }) => readSQL(url, file, signal),
    retry: false,
    gcTime: 0,
  });
  const rawLines = useMemo(() => (sql.data ?? "").split("\n"), [sql.data]);
  const pageLines = useMemo(() => {
    const visible = rawLines.slice(
      page * linesPerPage,
      (page + 1) * linesPerPage,
    );
    const text = visible.join("\n");
    // Bound lexical token allocation for an exceptionally long individual line.
    return text.length > 64 * 1024
      ? visible.map((text) => [{ text: text.replace(/\r$/, ""), kind: "" }])
      : highlightSQL(text);
  }, [rawLines, page]);
  const lineCount = rawLines.length;
  const search = useMemo(() => {
    const matches: { line: number; start: number; end: number }[] = [];
    const hitLines = new Set<number>();
    if (!find) return { matches, hitLines, limited: false };
    const needle = find.toLowerCase();
    for (let index = 0; index < rawLines.length; index++) {
      const lower = rawLines[index].toLowerCase();
      let start = 0;
      while ((start = lower.indexOf(needle, start)) !== -1) {
        if (matches.length === 10000)
          return { matches, hitLines, limited: true };
        matches.push({ line: index, start, end: start + needle.length });
        hitLines.add(index);
        start += Math.max(1, needle.length);
      }
    }
    return { matches, hitLines, limited: false };
  }, [find, rawLines]);
  const { matches, hitLines } = search;
  useEffect(() => {
    const active = matches[match];
    if (active)
      document
        .getElementById(sourceID)
        ?.querySelector(`.sql-active-match`)
        ?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [match, matches, page, expanded, sourceID]);
  const jump = (next: number) => {
    if (!matches.length) return;
    const value = (next + matches.length) % matches.length;
    setMatch(value);
    setPage(Math.floor(matches[value].line / linesPerPage));
  };
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(sql.data!);
      setCopied("SQL copied");
    } catch {
      setCopied("Copy unavailable. Use Download original file.");
    }
  };
  const content = (
    <div className="sql-reader">
      <div className="sql-reader-heading">
        <div>
          <h3 className="mono">{file.path.split("/").at(-1)}</h3>
          <p className="small mono">{file.path}</p>
        </div>
        <Badge>Read-only SQL</Badge>
      </div>
      <div className="sql-toolbar actions wrap">
        <Button disabled={!sql.isSuccess} onClick={() => void copy()}>
          Copy SQL
        </Button>
        <a className="button secondary" href={url}>
          Download original file
        </a>
        <button
          className="button secondary"
          id={expandID}
          onClick={() => setExpanded(!expanded)}
        >
          {expanded ? "Close full-screen reader" : "Full-screen reader"}
        </button>
        <span className="small muted" role="status">
          {copied}
        </span>
      </div>
      <details className="sql-file-meta">
        <summary>
          {file.kind === "repeatable"
            ? "Repeatable"
            : `Versioned · V${file.version}`}{" "}
          · {file.size.toLocaleString()} bytes · File identity
        </summary>
        <dl className="key-values">
          <div>
            <dt>Description from filename</dt>
            <dd>
              {file.path
                .split("/")
                .at(-1)
                ?.split("__")
                .slice(1)
                .join("__")
                .replace(/\.sql$/i, "")
                .replaceAll("_", " ") || "—"}
            </dd>
          </div>
          <div>
            <dt>Source artifact</dt>
            <dd className="mono">{file.artifact_id}</dd>
          </div>
          <div>
            <dt>SHA-256</dt>
            <dd className="mono">{file.sha256}</dd>
          </div>
        </dl>
      </details>
      {sql.isPending ? (
        <div className="skeleton" role="status" aria-label="Loading SQL" />
      ) : sql.isError ? (
        <Notice tone="error">
          {sql.error.message}
          <Button onClick={() => void sql.refetch()}>Retry SQL preview</Button>
        </Notice>
      ) : (
        <>
          <div className="sql-search actions wrap">
            <SearchBox
              label="Find in SQL"
              placeholder="Find in script…"
              value={find}
              onChange={(value) => {
                setFind(value);
                setMatch(0);
                const first = rawLines.findIndex((line) =>
                  line.toLowerCase().includes(value.toLowerCase()),
                );
                if (first >= 0) setPage(Math.floor(first / linesPerPage));
              }}
            />
            <span className="small" role="status">
              {find
                ? `${matches.length ? match + 1 : 0} of ${matches.length}${search.limited ? "+" : ""} matches${search.limited ? " · first 10,000 shown; narrow search" : ""}`
                : `${lineCount.toLocaleString()} lines`}
            </span>
            <Button
              aria-label="Previous SQL match"
              disabled={!matches.length}
              onClick={() => jump(match - 1)}
            >
              ↑
            </Button>
            <Button
              aria-label="Next SQL match"
              disabled={!matches.length}
              onClick={() => jump(match + 1)}
            >
              ↓
            </Button>
          </div>
          <div
            id={sourceID}
            className="sql-source"
            role="region"
            aria-label="SQL source"
            tabIndex={0}
          >
            <table>
              <tbody>
                {pageLines.map((tokens, index) => {
                  const line = page * linesPerPage + index;
                  const hit = find && hitLines.has(line);
                  let tokenOffset = 0;
                  return (
                    <tr
                      data-line={line}
                      key={line}
                      className={
                        hit
                          ? `sql-match${matches[match]?.line === line ? " sql-match-active" : ""}`
                          : undefined
                      }
                    >
                      <td
                        className="sql-line-number"
                        aria-label={`Line ${line + 1}`}
                      >
                        {line + 1}
                      </td>
                      <td>
                        <code>
                          {tokens.length
                            ? tokens.map((token, ti) => {
                                const active = matches[match];
                                const start =
                                  active?.line === line
                                    ? Math.max(0, active.start - tokenOffset)
                                    : token.text.length;
                                const end =
                                  active?.line === line
                                    ? Math.min(
                                        token.text.length,
                                        active.end - tokenOffset,
                                      )
                                    : 0;
                                tokenOffset += token.text.length;
                                return (
                                  <span
                                    className={
                                      token.kind
                                        ? `sql-${token.kind}`
                                        : undefined
                                    }
                                    key={ti}
                                  >
                                    {end > start ? (
                                      <>
                                        {token.text.slice(0, start)}
                                        <mark className="sql-active-match">
                                          {token.text.slice(start, end)}
                                        </mark>
                                        {token.text.slice(end)}
                                      </>
                                    ) : (
                                      token.text
                                    )}
                                  </span>
                                );
                              })
                            : "\u200b"}
                        </code>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <div className="pagination">
            <span>
              Lines {page * linesPerPage + 1}–
              {Math.min((page + 1) * linesPerPage, lineCount)} of{" "}
              {lineCount.toLocaleString()}
            </span>
            <div className="actions">
              <Button
                aria-label="Previous SQL lines"
                disabled={page === 0}
                onClick={() => setPage(page - 1)}
              >
                Previous lines
              </Button>
              <Button
                aria-label="Next SQL lines"
                disabled={(page + 1) * linesPerPage >= lineCount}
                onClick={() => setPage(page + 1)}
              >
                Next lines
              </Button>
            </div>
          </div>
        </>
      )}
    </div>
  );
  return (
    <>
      {!expanded && content}
      <Dialog.Root open={expanded} onOpenChange={setExpanded}>
        <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <Dialog.Content
            className="dialog-content sql-fullscreen"
            onCloseAutoFocus={(event) => {
              event.preventDefault();
              document.getElementById(expandID)?.focus();
            }}
          >
            <Dialog.Title className="sr-only">
              SQL reader · {file.path}
            </Dialog.Title>
            <Dialog.Description className="sr-only">
              Read-only immutable SQL. Close to return to the script explorer.
            </Dialog.Description>
            {content}
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  );
}
