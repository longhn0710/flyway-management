import { downloadText } from "../lib/download";
import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import {
  Download,
  Play,
  Pause,
  FileText,
  ShieldCheck,
  Terminal,
  CheckCircle2,
} from "lucide-react";
import { previewFixtures as fixtures } from "../lib/fixtures";
const { logsFor, runs } = fixtures;
import {
  Panel,
  PageHeading,
  Button,
  Status,
  Badge,
  Notice,
  KeyValues,
  SearchBox,
} from "../components/ui";
export default function RunDetails() {
  const { id } = useParams(),
    run = runs.find((r) => r.id === id),
    [query, setQuery] = useState(""),
    [level, setLevel] = useState(""),
    [source, setSource] = useState(""),
    [follow, setFollow] = useState(false),
    [tab, setTab] = useState("logs");
  if (!run)
    return (
      <>
        <PageHeading title="Không tìm thấy lần chạy" />
        <Notice tone="error">
          Run ID {id} không có trong dữ liệu minh họa.
        </Notice>
        <Link to="/runs" className="button secondary">
          Quay lại lịch sử
        </Link>
      </>
    );
  const failed = run.status === "FAILED",
    repaired = run.repair === "SUCCEEDED",
    logs = logsFor(run),
    visible = logs.filter(
      (l) =>
        (!level || l.level === level) &&
        (!source || l.source === source) &&
        l.message.toLowerCase().includes(query.toLowerCase()),
    );
  return (
    <>
      <PageHeading
        title={run.id}
        breadcrumb={
          <>
            Lịch sử chạy / <code>{run.id}</code>
          </>
        }
        description="Ảnh chụp trạng thái minh họa · Không có phiên Flyway trực tiếp"
        actions={
          <Link to="/runs" className="button secondary">
            ← Quay lại Lịch sử chạy
          </Link>
        }
      />
      <Panel>
        <div className="spread run-summary">
          <div>
            <h2>
              {run.id}{" "}
              <Badge>
                Target: {run.target} / {run.service}
              </Badge>
            </h2>
            <Badge tone="accent">Schema: {run.schema}</Badge>
            <p className="muted small">Bundle: {run.artifact}</p>
          </div>
          <div className="stack tight">
            <Status value={run.status} />
            {repaired && (
              <div className="inline">
                <span>Auto-repair REP-204:</span>
                <Status value="SUCCEEDED" />
              </div>
            )}
            {failed && (
              <Badge tone="warning">Schema: NEEDS_REVIEW · Exit code: 1</Badge>
            )}
          </div>
        </div>
      </Panel>
      {failed ? (
        <Notice tone="warning" title="Ghi chú an toàn nghiệp vụ:">
          Repair {repaired ? "thành công" : "không đổi kết quả"} không làm thay
          đổi kết quả FAILED. CI/CD vẫn thất bại. Partial Oracle DDL có thể còn
          tồn tại. Không tự động chạy lại SQL hoặc rollback. Stream cần DBA rà
          soát.
        </Notice>
      ) : (
        <div className="lifecycle">
          {[
            "QUEUED",
            "PREFLIGHT",
            run.status === "SUCCEEDED" ? "RUNNING" : run.status,
            run.status === "SUCCEEDED" ? "SUCCEEDED" : "Kết quả chưa có",
          ].map((s, i) => (
            <span key={`${s}-${i}`}>
              <Badge tone={i < 3 ? "accent" : "neutral"}>{i + 1}</Badge>
              {s}
            </span>
          ))}
          <span className="muted">Snapshot minh họa · {run.duration}</span>
        </div>
      )}
      <div className="layout-log">
        <div className="stack">
          <Panel
            title="Thông tin phiên thực thi gốc"
            aside={<Badge>IMMUTABLE · FIXTURE</Badge>}
          >
            <KeyValues
              rows={[
                ["Môi trường", <Badge>{run.environment}</Badge>],
                [
                  "Database & Service",
                  <code>
                    {run.target} / {run.service}
                  </code>,
                ],
                ["Target Schema", <code>{run.schema}</code>],
                ["Tài khoản thực thi", <code>{run.account}</code>],
                [
                  "Tác nhân xác thực (Actor)",
                  <>
                    {run.actor}
                    <small className="block muted">
                      {run.actorType} · {run.source}
                    </small>
                  </>,
                ],
                [
                  "Caller metadata (untrusted)",
                  <code>
                    {run.source === "API" ? "initiated_by=pipeline-1047" : "—"}
                  </code>,
                ],
                ["Artifact ID", <code>{run.artifactId}</code>],
                [
                  "Tên bundle",
                  <code className="break-word">{run.artifact}</code>,
                ],
                ["Bắt đầu", <code>{run.startedAt}</code>],
                ["Thời lượng", run.duration],
                ["Flyway version", "Fixture · chưa xác minh phiên bản hỗ trợ"],
              ]}
            />
            <Notice>
              Metadata và logs mẫu không phải bằng chứng Oracle hoặc kiểm chứng
              repair.
            </Notice>
          </Panel>
          {failed && (
            <Panel
              title="Kiểm toán Auto-repair · REP-204"
              aside={<Status value={repaired ? "SUCCEEDED" : "UNSAFE"} />}
            >
              <h3>Danh mục kiểm tra an toàn</h3>
              <div className="checklist">
                {[
                  "Immutable error snapshot persisted BEFORE repair.",
                  "Failed-history row thuộc đúng stream / history / cấu hình.",
                  "Đủ bộ migration lịch sử + mới.",
                  "Không realign checksum hoặc xóa migration không liên quan.",
                  "Đối chiếu before / after đã hoàn tất trong kịch bản minh họa.",
                ].map((text) => (
                  <p key={text}>
                    <CheckCircle2 size={15} />
                    {text}
                  </p>
                ))}
              </div>
              <Notice tone="warning">
                Guardrail chưa chứng minh được thì bỏ qua repair; giữ FAILED và
                BLOCKED / NEEDS_REVIEW.
              </Notice>
              <div className="inset">
                <h3>Bằng chứng đối chiếu mẫu</h3>
                <p>
                  <strong>Trước:</strong> V3.2.0 · success=false
                </p>
                <p>
                  <strong>Sau:</strong> bỏ failed row V3.2.0; V2 giữ nguyên.
                </p>
                <p>
                  Migration outcome:{" "}
                  <strong className="error-text">FAILED</strong>
                </p>
              </div>
              <Button
                className="full"
                onClick={() =>
                  downloadText(
                    `${run.id}-fixture-audit.json`,
                    JSON.stringify(
                      {
                        fixture: true,
                        run_id: run.id,
                        migration_outcome: "FAILED",
                        repair_outcome: run.repair,
                        stream_state: "NEEDS_REVIEW",
                        note: "Illustrative evidence only. No Oracle execution.",
                      },
                      null,
                      2,
                    ),
                    "application/json",
                  )
                }
              >
                <Download size={15} />
                Tải audit minh họa
              </Button>
            </Panel>
          )}
        </div>
        <Panel className="logs-panel">
          <div className="tabs" role="tablist" aria-label="Bằng chứng lần chạy">
            <button
              role="tab"
              aria-selected={tab === "logs"}
              onClick={() => setTab("logs")}
            >
              <Terminal size={16} />
              Nhật ký thực thi
            </button>
            <button
              role="tab"
              aria-selected={tab === "audit"}
              onClick={() => setTab("audit")}
            >
              <FileText size={16} />
              Schema History / Audit
            </button>
          </div>
          {tab === "logs" ? (
            <>
              <div className="log-toolbar">
                <SearchBox
                  value={query}
                  onChange={setQuery}
                  placeholder="Tìm trong log (văn bản)…"
                  label="Tìm trong log"
                />
                <select
                  aria-label="Log level"
                  value={level}
                  onChange={(e) => setLevel(e.target.value)}
                >
                  <option value="">Level: Tất cả</option>
                  {["INFO", "WARN", "ERROR"].map((s) => (
                    <option key={s}>{s}</option>
                  ))}
                </select>
                <select
                  aria-label="Nguồn log"
                  value={source}
                  onChange={(e) => setSource(e.target.value)}
                >
                  <option value="">Nguồn: Tất cả</option>
                  {["stdout", "stderr", "event"].map((s) => (
                    <option key={s}>{s}</option>
                  ))}
                </select>
              </div>
              <div className="spread log-actions">
                <Button onClick={() => setFollow(!follow)}>
                  {follow ? <Pause size={15} /> : <Play size={15} />}{" "}
                  {follow ? "Tạm dừng follow" : "Follow / Resume"}
                </Button>
                <span className="small muted">
                  {follow
                    ? "Theo dõi snapshot (không có live stream)"
                    : "Đã tạm dừng"}{" "}
                  · {visible.length} dòng
                </span>
                <Button
                  onClick={() =>
                    downloadText(
                      `${run.id}-fixture-logs.txt`,
                      "DEVELOPMENT FIXTURE — NOT LIVE EXECUTION\n" +
                        visible
                          .map(
                            (l) =>
                              `${l.sequence} ${l.time} ${l.level} ${l.source} ${l.message}`,
                          )
                          .join("\n"),
                    )
                  }
                >
                  <Download size={15} />
                  Tải log minh họa (.txt)
                </Button>
              </div>
              {failed && (
                <Notice tone="error" title="ORACLE DATABASE ERROR · ORA-00955">
                  name is already used by an existing object · SQL State: 42000
                </Notice>
              )}
              <div
                className="log-viewer"
                tabIndex={0}
                role="region"
                aria-label="Nhật ký minh họa"
                onScroll={() => setFollow(false)}
              >
                {visible.length ? (
                  visible.map((l) => (
                    <div
                      key={l.sequence}
                      className={`log-row ${l.level.toLowerCase()}`}
                    >
                      <span className="log-time">
                        [{String(l.sequence).padStart(3, "0")}] {l.time}
                      </span>
                      <Badge
                        tone={
                          l.source === "stderr"
                            ? "error"
                            : l.source === "stdout"
                              ? "accent"
                              : "neutral"
                        }
                      >
                        {l.source}
                      </Badge>
                      <span>
                        <b>{l.level}:</b> {l.message}
                      </span>
                    </div>
                  ))
                ) : (
                  <p className="empty" role="status">
                    Không có dòng log khớp bộ lọc.
                  </p>
                )}
              </div>
              <Notice>
                <ShieldCheck size={14} />
                Nếu mất kết nối thì trạng thái thực thi chưa biết. Kết quả
                migration và repair luôn tách biệt.
              </Notice>
            </>
          ) : (
            <div className="stack">
              <Notice title="Lịch sử mẫu">
                Mọi bằng chứng dưới đây chỉ minh họa hợp đồng kiểm toán.
              </Notice>
              <KeyValues
                rows={[
                  [
                    "History location",
                    <code>{run.schema}.flyway_schema_history</code>,
                  ],
                  ["Migration outcome", <Status value={run.status} />],
                  ["Repair outcome", <Status value={run.repair} />],
                  ["SQL retry / Rollback", "Không thực hiện"],
                ]}
              />
              <Button disabled>
                Tải SQL nguồn (chưa triển khai phân quyền)
              </Button>
            </div>
          )}
        </Panel>
      </div>
    </>
  );
}
