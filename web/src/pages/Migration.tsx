import { useRef, useState, type ChangeEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  ArrowRight,
  Upload,
  FolderUp,
  FileArchive,
  Trash2,
  ShieldCheck,
  Database,
  CheckCircle2,
} from "lucide-react";
import { useDraft } from "../lib/draft";
import { targets, accounts, manifest } from "../lib/fixtures";
import type { BundleFile } from "../lib/contracts";
import {
  Panel,
  PageHeading,
  Button,
  Field,
  Notice,
  Badge,
  KeyValues,
  Check,
} from "../components/ui";
export function Guardrails() {
  return (
    <>
      <div className="guardrails">
        {Object.entries({
          validateOnMigrate: true,
          outOfOrder: false,
          cleanDisabled: true,
          baselineOnMigrate: false,
          createSchemas: false,
        }).map(([key, value]) => (
          <div key={key}>
            <code>{key}</code>
            <Badge tone={value ? "success" : "neutral"}>{String(value)}</Badge>
          </div>
        ))}
      </div>
      <Notice>
        Cấu hình cố định theo từng stream. Quyền Oracle quyết định truy cập thực
        tế; tham số Flyway không phải sandbox.
      </Notice>
    </>
  );
}
function Summary() {
  const { draft } = useDraft(),
    target = targets.find((t) => t.id === draft.targetId)!;
  return (
    <Panel title="Tóm tắt cấu hình dự thảo">
      <KeyValues
        rows={[
          [
            "Môi trường",
            <Badge tone={target.environment === "PROD" ? "error" : "neutral"}>
              {target.environment}
            </Badge>,
          ],
          ["Database Target", <code>{target.name}</code>],
          ["Oracle Service", <code>{target.service}</code>],
          ["Target Schema", <Badge tone="accent">{draft.schema}</Badge>],
          ["Execution Account", <code>{draft.account}</code>],
          ["Tệp đã chọn", `${draft.files.length} tệp`],
          [
            "History location",
            <code>{draft.schema}.flyway_schema_history</code>,
          ],
        ]}
      />
      <Notice tone="warning">
        Quyền Oracle (Grants) quyết định quyền DDL thực tế. Chưa có lệnh DDL nào
        được gửi.
      </Notice>
    </Panel>
  );
}
export default function Migration() {
  const { draft, setDraft } = useDraft(),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [mode, setMode] = useState("archive");
  const input = useRef<HTMLInputElement>(null),
    navigate = useNavigate(),
    target = targets.find((t) => t.id === draft.targetId)!;
  async function selectFiles(files: FileList | null) {
    if (!files?.length) return;
    const selected = Array.from(files),
      total = selected.reduce((s, f) => s + f.size, 0);
    setError("");
    if (selected.length > 100 || total > 25 * 1024 * 1024) {
      setError("Giới hạn xem trước: tối đa 100 tệp, tổng 25 MB.");
      return;
    }
    if (selected.some((f) => !/\.(sql|zip|tar|tar\.gz|tgz)$/i.test(f.name))) {
      setError(
        "Chỉ chọn SQL, ZIP, TAR, TAR.GZ hoặc TGZ. Nội dung chưa được xác minh bởi máy chủ.",
      );
      return;
    }
    setBusy(true);
    try {
      const metadata: BundleFile[] = await Promise.all(
        selected.map(async (f) => {
          const bytes = await f.arrayBuffer();
          const digest = await crypto.subtle.digest("SHA-256", bytes);
          return {
            name: f.webkitRelativePath || f.name,
            size: f.size,
            checksum: Array.from(new Uint8Array(digest), (b) =>
              b.toString(16).padStart(2, "0"),
            ).join(""),
          };
        }),
      );
      setDraft({ ...draft, files: metadata, fixture: false });
    } catch {
      setError("Không thể đọc tệp cục bộ. Hãy chọn lại.");
    } finally {
      setBusy(false);
    }
  }
  function sample() {
    setDraft({
      ...draft,
      fixture: true,
      files: manifest.map((name, i) => ({ name, size: 1024 * (i + 2) })),
    });
    setError("");
  }
  const next = () => {
    if (!draft.files.length) {
      setError("Vui lòng chọn tệp hoặc dùng bundle minh họa.");
      return;
    }
    navigate("/runs/new/review");
  };
  return (
    <>
      <PageHeading
        title="Chạy migration schema Oracle"
        description="Chọn một stream độc lập và chuẩn bị migration bundle."
        breadcrumb="Chạy migration / Bước 1: Chọn mục tiêu & tải bundle"
        actions={
          <div className="stepper">
            <Badge tone="accent">1</Badge>
            <strong>Mục tiêu & bundle</strong>
            <ArrowRight size={14} />
            <Badge>2</Badge>
            <span>Kiểm tra & xác nhận</span>
          </div>
        }
      />
      <div className="layout-aside">
        <div className="stack">
          <Panel
            title={
              <>
                <Database size={17} />
                1. Mục tiêu cơ sở dữ liệu & Tài khoản thực thi
              </>
            }
          >
            <Field label="Cơ sở dữ liệu đích (Database / PDB)" required>
              <select
                value={draft.targetId}
                onChange={(e) => {
                  const t = targets.find((t) => t.id === e.target.value)!;
                  setDraft({
                    ...draft,
                    targetId: t.id,
                    schema: t.schemas[0],
                    account: "DEPLOY_ADMIN",
                  });
                }}
              >
                {targets.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name} (Service: {t.service} · Env: {t.environment})
                  </option>
                ))}
              </select>
            </Field>
            <Field
              label="Target Schema"
              required
              hint="Stream độc lập. Mỗi schema có lịch sử và phiên bản riêng."
            >
              <select
                value={draft.schema}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    schema: e.target.value,
                    account: "DEPLOY_ADMIN",
                  })
                }
              >
                {target.schemas.map((s) => (
                  <option key={s}>{s}</option>
                ))}
              </select>
            </Field>
            <Field
              label="Tài khoản thực thi Oracle (Execution Account)"
              required
              hint="Chỉ hiển thị tên tài khoản. Không có mật khẩu trong trình duyệt."
            >
              <select
                value={draft.account}
                onChange={(e) =>
                  setDraft({ ...draft, account: e.target.value })
                }
              >
                {(draft.schema === "CRM"
                  ? [accounts[0]]
                  : accounts.filter((a) => a.schemas.includes(draft.schema))
                ).map((a) => (
                  <option key={a.id}>{a.name}</option>
                ))}
              </select>
            </Field>
            <Notice>
              Quyền hạn Oracle phân cấp trực tiếp trên database; việc dùng chung
              tài khoản giữa các schema không gộp chung lịch sử migration.
            </Notice>
          </Panel>
          <Panel
            title={
              <>
                <Upload size={17} />
                2. Tải lên migration bundle (SQL / Archive)
              </>
            }
          >
            <div className="upload-tabs">
              {[
                ["sql", "Tải tệp SQL", Upload],
                ["folder", "Tải thư mục", FolderUp],
                ["archive", "Tải gói nén (Archive)", FileArchive],
              ].map(([key, label, Icon]) => {
                const I = Icon as typeof Upload;
                return (
                  <Button
                    key={String(key)}
                    className={mode === key ? "selected" : ""}
                    onClick={() => setMode(String(key))}
                  >
                    <I size={15} />
                    {String(label)}
                  </Button>
                );
              })}
            </div>
            <p className="muted small">
              SQL, ZIP, TAR, TAR.GZ, TGZ. Phase 1 chỉ đọc metadata/checksum cục
              bộ; không upload, giải nén hay kiểm tra tương thích Flyway.
            </p>
            <input
              key={mode}
              ref={input}
              className="sr-only"
              tabIndex={-1}
              type="file"
              multiple={mode !== "archive"}
              accept={
                mode === "sql"
                  ? ".sql"
                  : mode === "archive"
                    ? ".zip,.tar,.tar.gz,.tgz"
                    : undefined
              }
              {...(mode === "folder"
                ? { webkitdirectory: "", directory: "" }
                : {})}
              onChange={(e: ChangeEvent<HTMLInputElement>) =>
                void selectFiles(e.target.files)
              }
            />
            <div
              className="dropzone"
              onDragOver={(e) => e.preventDefault()}
              onDrop={(e) => {
                e.preventDefault();
                void selectFiles(e.dataTransfer.files);
              }}
            >
              <Upload size={27} />
              <strong>Kéo thả tệp hoặc chọn từ máy tính</strong>
              <span>Giới hạn xem trước: 25 MB · 100 tệp</span>
              <Button onClick={() => input.current?.click()} disabled={busy}>
                {busy ? "Đang đọc checksum…" : "Chọn tệp"}
              </Button>
              <Button variant="ghost" onClick={sample}>
                Dùng bundle minh họa
              </Button>
            </div>
            {error && (
              <p className="form-error" role="alert">
                {error}
              </p>
            )}
            {draft.files.length > 0 && (
              <div className="bundle-summary">
                <div className="spread">
                  <strong>
                    <FileArchive size={17} />
                    {draft.fixture
                      ? "finance_v3.zip · 4 tệp đại diện"
                      : `${draft.files.length} tệp cục bộ`}
                  </strong>
                  <Button
                    variant="ghost"
                    onClick={() =>
                      setDraft({ ...draft, files: [], fixture: false })
                    }
                  >
                    <Trash2 size={14} />
                    Xóa
                  </Button>
                </div>
                <Badge tone="warning">
                  {draft.fixture
                    ? "FIXTURE · Minh họa"
                    : "CHƯA XÁC MINH BỞI MÁY CHỦ"}
                </Badge>
                <p className="small muted">
                  Không có Artifact ID thực. Chưa được chuẩn hóa hoặc kiểm tra
                  continuity.
                </p>
              </div>
            )}
          </Panel>
          {draft.files.length > 0 && (
            <Panel title="3. Danh sách tệp đã chọn">
              <div className="file-list">
                {draft.files.map((file) => (
                  <div key={file.name}>
                    <code>{file.name}</code>
                    <span className="small muted">
                      {file.size.toLocaleString()} bytes
                    </span>
                    {file.checksum && (
                      <code className="checksum">SHA-256: {file.checksum}</code>
                    )}
                  </div>
                ))}
              </div>
            </Panel>
          )}
        </div>
        <aside className="stack">
          <Summary />
          <Panel>
            <Button
              variant="primary"
              className="full"
              disabled={busy}
              onClick={next}
            >
              Tiếp tục: Kiểm tra & xác nhận <ArrowRight size={15} />
            </Button>
            <p className="small muted">
              Xem lại lựa chọn. Thực thi migration chưa được triển khai.
            </p>
          </Panel>
          <Notice title="Stream Serialization">
            Ứng dụng tuần tự hóa job theo database + schema, từ migrate đến hết
            conditional repair. Không phải table lock vĩnh viễn trên Oracle.
          </Notice>
        </aside>
      </div>
    </>
  );
}
export function Review() {
  const { draft } = useDraft(),
    [confirmed, setConfirmed] = useState(false),
    [prod, setProd] = useState(false);
  const target = targets.find((t) => t.id === draft.targetId)!;
  return (
    <>
      <PageHeading
        title="Kiểm tra & xác nhận migration"
        description="Xem lại mục tiêu và nội dung bundle trước khi thực thi."
        breadcrumb="Chạy migration / Bước 2: Kiểm tra & xác nhận"
        actions={
          <Link className="button secondary" to="/runs/new">
            ← Quay lại chọn bundle
          </Link>
        }
      />
      {!draft.files.length ? (
        <Panel>
          <Notice>
            Chưa có bundle trong bản nháp. Hãy chọn tệp trước khi xem lại.
          </Notice>
          <Link className="button primary" to="/runs/new">
            Chọn bundle
          </Link>
        </Panel>
      ) : (
        <div className="layout-aside">
          <div className="stack">
            <Notice tone="warning" title="Chưa sẵn sàng để thực thi.">
              Chưa có API chuẩn hóa bundle, kiểm tra continuity hoặc xác thực
              quyền. Trang này chỉ xem lại bản nháp cục bộ.
            </Notice>
            <Panel
              title={
                <>
                  <ShieldCheck size={17} />
                  Kiểm tra bundle & lịch sử stream
                </>
              }
            >
              <div className="checklist">
                <p>
                  <CheckCircle2 size={16} />
                  Đã chọn đúng một database / target schema
                </p>
                <p>
                  <CheckCircle2 size={16} />
                  Đã chọn tên tài khoản thực thi
                </p>
                <p>
                  <Badge>CHƯA KIỂM TRA</Badge> Cấu trúc archive, tên migration
                  và khả năng Flyway
                </p>
                <p>
                  <Badge>CHƯA KIỂM TRA</Badge> Continuity, checksum drift và
                  lịch sử migration
                </p>
                <p>
                  <Badge>CHƯA KIỂM TRA</Badge> Quyền ứng dụng và snapshot bất
                  biến
                </p>
              </div>
            </Panel>
            <Panel title="Manifest bản nháp">
              <div className="file-list">
                {draft.files.map((f) => (
                  <div key={f.name}>
                    <code>{f.name}</code>
                    <Badge>
                      {draft.fixture ? "Fixture" : "Metadata cục bộ"}
                    </Badge>
                    {f.checksum && (
                      <code className="checksum">{f.checksum}</code>
                    )}
                  </div>
                ))}
              </div>
            </Panel>
            <Panel title="Guardrails dự kiến">
              <Guardrails />
            </Panel>
          </div>
          <aside className="stack">
            <Summary />
            <Panel title="Xác nhận mục tiêu">
              <Check
                checked={confirmed}
                onChange={setConfirmed}
                label={`Tôi đã xem lại ${target.name} / ${draft.schema}.`}
              />
              {target.environment === "PROD" && (
                <Check
                  checked={prod}
                  onChange={setProd}
                  label="Tôi xác nhận môi trường PROD cần quyền chạy được cấp rõ ràng."
                />
              )}
              <Button className="full" variant="primary" disabled>
                Gửi migration
              </Button>
              <p className="small muted">
                Chưa triển khai thực thi Flyway trong Phase 1. Không có job hoặc
                bản ghi nghiệp vụ nào được tạo.
              </p>
            </Panel>
          </aside>
        </div>
      )}
    </>
  );
}
