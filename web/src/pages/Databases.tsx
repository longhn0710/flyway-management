import { useState, type FormEvent } from "react";
import { Link, useParams } from "react-router-dom";
import {
  Plus,
  ChevronDown,
  ChevronRight,
  ShieldCheck,
  Database,
  ArrowLeft,
} from "lucide-react";
import { previewFixtures as fixtures } from "../lib/fixtures";
const { targets, runs } = fixtures;
import {
  Panel,
  PageHeading,
  Button,
  Badge,
  Notice,
  SearchBox,
  Modal,
  Field,
  KeyValues,
  Unavailable,
} from "../components/ui";
import { Guardrails } from "./Migration";
function Registration({
  open,
  onOpenChange,
  schemaOnly = false,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  schemaOnly?: boolean;
}) {
  const [message, setMessage] = useState("");
  function validate(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setMessage(
      "Bản nháp hợp lệ về trường bắt buộc. Chưa lưu vào máy chủ hoặc đăng ký Oracle.",
    );
  }
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={schemaOnly ? "Đăng ký Target Schema" : "Đăng ký Database Target"}
      description="Biểu mẫu bản nháp · Không tạo database hoặc schema trên Oracle."
    >
      <form onSubmit={validate}>
        {schemaOnly ? (
          <Field label="Schema name" required>
            <input
              required
              pattern="[A-Za-z][A-Za-z0-9_$#]*"
              placeholder="CORE_BANKING"
            />
          </Field>
        ) : (
          <>
            <Field label="Tên hiển thị" required>
              <input required placeholder="ORA_UAT_FINANCE" />
            </Field>
            <Field label="Stable ID" required>
              <input
                required
                pattern="[a-z0-9-]+"
                placeholder="db-uat-finance"
              />
            </Field>
            <div className="grid-two">
              <Field label="Môi trường">
                <select>
                  <option>UAT</option>
                  <option>PROD</option>
                  <option>DEV</option>
                </select>
              </Field>
              <Field label="Giao thức">
                <select>
                  <option>TCPS / TLS</option>
                  <option>TCP</option>
                </select>
              </Field>
            </div>
            <div className="grid-two">
              <Field label="Hostname" required>
                <input required placeholder="oracle.internal" />
              </Field>
              <Field label="Port" required>
                <input
                  required
                  type="number"
                  min="1"
                  max="65535"
                  defaultValue="1521"
                />
              </Field>
            </div>
            <Field
              label="Oracle Service / PDB"
              required
              hint="Hostname hoặc Oracle instance đơn lẻ không đủ nhận diện mục tiêu."
            >
              <input required placeholder="FINUAT" />
            </Field>
          </>
        )}
        <Notice>
          Chưa triển khai registry API. Không kiểm tra kết nối, lưu target hoặc
          đăng ký stream trong Phase 1.
        </Notice>
        {message && (
          <p className="form-message" role="status">
            {message}
          </p>
        )}
        <div className="dialog-actions">
          <Button type="button" onClick={() => onOpenChange(false)}>
            Hủy
          </Button>
          <Button type="submit">Kiểm tra bản nháp</Button>
          <Button type="button" variant="primary" disabled>
            Đăng ký
          </Button>
        </div>
      </form>
    </Modal>
  );
}
export default function Databases() {
  const [q, setQ] = useState(""),
    [env, setEnv] = useState(""),
    [expanded, setExpanded] = useState("db-uat-finance"),
    [register, setRegister] = useState(false),
    [schema, setSchema] = useState(false);
  const data = targets.filter(
    (t) =>
      (!env || t.environment === env) &&
      [t.id, t.name, t.host, t.service]
        .join(" ")
        .toLowerCase()
        .includes(q.toLowerCase()),
  );
  return (
    <>
      <PageHeading
        title="Database & Schema"
        description="Danh mục các mục tiêu cơ sở dữ liệu Oracle và các schema stream độc lập phục vụ điều phối migration."
        breadcrumb="Quản trị mục tiêu / Database & Schema"
        actions={
          <>
            <Button disabled>Kiểm tra kết nối</Button>
            <Button variant="primary" onClick={() => setRegister(true)}>
              <Plus size={16} />
              Thêm Database
            </Button>
          </>
        }
      />
      <div className="filter-bar">
        <SearchBox
          value={q}
          onChange={setQ}
          placeholder="Tìm database, host, service/PDB, stable ID…"
        />
        <div className="inline">
          <span className="small muted">MÔI TRƯỜNG:</span>
          <div className="segments">
            {["", "UAT", "PROD", "DEV"].map((e) => (
              <button
                key={e}
                aria-pressed={env === e}
                className={env === e ? "selected" : ""}
                onClick={() => setEnv(e)}
              >
                {e || "Tất cả"}
              </button>
            ))}
          </div>
        </div>
        <span className="small mono push-right">
          {data.length} targets ·{" "}
          {data.reduce((n, t) => n + t.schemas.length, 0)} streams
        </span>
      </div>
      <div className="panel registry">
        <div className="registry-head">
          <span>Tên Database & Stable ID</span>
          <span>Môi trường</span>
          <span>Host:Port / Service (PDB)</span>
          <span>Giao thức</span>
          <span>Kết nối gần nhất</span>
          <span>Schema / Thao tác</span>
        </div>
        {data.map((t) => (
          <div key={t.id}>
            <div className="registry-row">
              <button
                className="target-toggle"
                aria-label={`Mở schemas ${t.name}`}
                aria-expanded={expanded === t.id}
                onClick={() => setExpanded(expanded === t.id ? "" : t.id)}
              >
                {expanded === t.id ? (
                  <ChevronDown size={16} />
                ) : (
                  <ChevronRight size={16} />
                )}
                <div>
                  <strong>{t.name}</strong>
                  <small className="block muted mono">{t.id}</small>
                </div>
              </button>
              <Badge tone={t.environment === "PROD" ? "error" : "neutral"}>
                {t.environment}
              </Badge>
              <span className="mono small">
                {t.host}
                <br />
                Service: {t.service}
              </span>
              <span className="small">{t.transport}</span>
              <span>
                <Badge>Chưa kiểm tra</Badge>
                <small className="block muted">Fixture target</small>
              </span>
              <div className="inline wrap">
                <Badge>{t.schemas.length} schemas</Badge>
                <Link
                  className="button secondary compact"
                  to={`/databases/${t.id}`}
                >
                  Chi tiết
                </Link>
              </div>
            </div>
            {expanded === t.id && (
              <div className="schema-expansion">
                <div className="spread">
                  <h3>Các schema stream trên {t.name}</h3>
                  <Button onClick={() => setSchema(true)}>
                    <Plus size={14} />
                    Đăng ký Schema
                  </Button>
                </div>
                <p className="small muted">
                  Mỗi target schema là một stream riêng. Ứng dụng tuần tự hóa
                  các job theo database + schema.
                </p>
                <div className="table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>Schema name</th>
                        <th>Phiên bản hiện tại</th>
                        <th>Bảng lịch sử Flyway</th>
                        <th>Tài khoản mặc định</th>
                        <th>Thao tác</th>
                      </tr>
                    </thead>
                    <tbody>
                      {t.schemas.map((s, i) => (
                        <tr key={s}>
                          <td>
                            <code>{s}</code>
                          </td>
                          <td>
                            <Badge>V{i === 0 ? "2" : "1"}</Badge>
                          </td>
                          <td>
                            <code>{s}.flyway_schema_history</code>
                          </td>
                          <td>
                            <code>DEPLOY_ADMIN</code>
                            <small className="muted"> (chia sẻ)</small>
                          </td>
                          <td>
                            <Link to={`/databases/${t.id}?schema=${s}`}>
                              Cấu hình Stream →
                            </Link>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            )}
          </div>
        ))}
        {!data.length && (
          <div className="empty">
            <strong>Không tìm thấy database</strong>
            <Button
              onClick={() => {
                setQ("");
                setEnv("");
              }}
            >
              Đặt lại bộ lọc
            </Button>
          </div>
        )}
      </div>
      <Notice title="Nguyên tắc quản trị tài khoản & Schema Streams">
        <ShieldCheck size={15} />
        History, log và bằng chứng được ghi nhận độc lập theo database + schema.
        Dữ liệu trên là minh họa; chưa có connection probe thực.
      </Notice>
      <Registration open={register} onOpenChange={setRegister} />
      <Registration open={schema} onOpenChange={setSchema} schemaOnly />
    </>
  );
}
export function DatabaseDetails() {
  const { id } = useParams(),
    target = targets.find((t) => t.id === id);
  if (!target)
    return (
      <>
        <PageHeading title="Không tìm thấy database" />
        <Link to="/databases">Quay lại danh sách</Link>
      </>
    );
  return <DatabaseEditor key={target.id} target={target} />;
}
function DatabaseEditor({ target }: { target: (typeof targets)[number] }) {
  const initialSchema = new URLSearchParams(location.search).get("schema"),
    [selected, setSelected] = useState(
      initialSchema && target.schemas.includes(initialSchema)
        ? initialSchema
        : target.schemas[0],
    ),
    [dedicated, setDedicated] = useState(false),
    [owner, setOwner] = useState(""),
    [table, setTable] = useState("flyway_schema_history"),
    [account, setAccount] = useState("DEPLOY_ADMIN"),
    [policy, setPolicy] = useState("conditional"),
    [schemaModal, setSchemaModal] = useState(false);
  return (
    <>
      <PageHeading
        title={target.name}
        breadcrumb={
          <>
            Database & Schema / <code>{target.id}</code>
          </>
        }
        description={`Stable ID: ${target.id} · Host: ${target.host} · Service/PDB: ${target.service}`}
        actions={
          <>
            <Link className="button secondary" to="/databases">
              <ArrowLeft size={15} />
              Quay lại Danh sách
            </Link>
            <Button disabled>Kiểm tra kết nối mục tiêu</Button>
          </>
        }
      />
      <Panel title="Tình trạng kiểm tra kết nối & quyền truy cập Oracle">
        <div className="probe-grid">
          {["Reachability", "Authentication", "Probed Privileges"].map(
            (s, i) => (
              <div className="inset" key={s}>
                <div className="spread">
                  <strong>
                    {i + 1}. {s}
                  </strong>
                  <Badge>Chưa kiểm tra</Badge>
                </div>
                <p className="muted small">
                  Connection probe chưa được triển khai.
                </p>
              </div>
            ),
          )}
        </div>
        <Notice tone="warning">
          Quyền Oracle quyết định truy cập thực tế. Cấu hình schema Flyway không
          giới hạn quyền tài khoản Oracle.
        </Notice>
      </Panel>
      <div className="spread">
        <div className="tabs" role="tablist" aria-label="Target schema">
          {target.schemas.map((s) => (
            <button
              key={s}
              role="tab"
              aria-selected={selected === s}
              onClick={() => {
                setSelected(s);
                setDedicated(false);
                setOwner("");
                setAccount("DEPLOY_ADMIN");
              }}
            >
              <Database size={15} />
              {s}
              <Badge>V{s === "PAYMENT" ? "1" : "2"}</Badge>
            </button>
          ))}
        </div>
        <Button onClick={() => setSchemaModal(true)}>
          <Plus size={14} />
          Khai báo Schema mới
        </Button>
      </div>
      <p className="small muted">
        Đăng ký schema chỉ cấu hình stream trong ứng dụng; không thực hiện
        CREATE USER / SCHEMA.
      </p>
      <div className="grid-two">
        <Panel title="Định danh & vị trí Schema History">
          <Field label="Target schema (đích migration)">
            <input value={selected} readOnly />
          </Field>
          <div className="inset stack tight">
            <label className="check">
              <input
                type="radio"
                name="history"
                checked={!dedicated}
                onChange={() => setDedicated(false)}
              />
              Dùng chung Target Schema (mặc định)
            </label>
            <label className="check">
              <input
                type="radio"
                name="history"
                checked={dedicated}
                onChange={() => setDedicated(true)}
              />
              Dedicated History Schema
            </label>
            <Field label="History schema">
              <input
                value={dedicated ? owner : selected}
                readOnly={!dedicated}
                onChange={(e) => setOwner(e.target.value)}
                placeholder="FLYWAY_HISTORY"
              />
            </Field>
            <Field label="History table">
              <input value={table} onChange={(e) => setTable(e.target.value)} />
            </Field>
          </div>
          <Notice tone="warning">
            Kiểm chứng CURRENT_SCHEMA, SQL không định danh và quyền history bằng
            POC. Cấu hình schema không phải sandbox.
          </Notice>
          <Field label="Tài khoản thực thi Oracle">
            <select
              value={account}
              onChange={(e) => setAccount(e.target.value)}
            >
              <option>DEPLOY_ADMIN</option>
              {selected !== "CRM" && (
                <option>
                  {selected === "PAYMENT" ? "PAYMENT_MIGRATOR" : "APP_MIGRATOR"}
                </option>
              )}
            </select>
          </Field>
          <Link to="/execution-accounts">Quản lý tài khoản thực thi →</Link>
          <Field label="Chính sách Auto-repair history (Admin)">
            <select value={policy} onChange={(e) => setPolicy(e.target.value)}>
              <option value="conditional">Có điều kiện (guard bắt buộc)</option>
              <option value="disabled">Vô hiệu hóa</option>
            </select>
          </Field>
          <p className="small muted">
            Nếu chưa chứng minh được an toàn, bỏ qua repair và giữ FAILED /
            BLOCKED. Không có repair thủ công.
          </p>
          <Unavailable label="Cập nhật cấu hình định danh" />
        </Panel>
        <Panel
          title="Guardrails & tham số Flyway cố định"
          aside={<Badge>FROZEN CONFIG</Badge>}
        >
          <Field label="Bộ tệp migration (Locations / Workspace)">
            <div className="inset small">
              Bộ migration bất biến do máy chủ quản lý: lịch sử + bundle mới;
              filesystem tạm thời.
            </div>
          </Field>
          <Guardrails />
          <KeyValues
            rows={[
              [
                "Stream key",
                <code>
                  {target.id} / {selected}
                </code>,
              ],
              [
                "Physical history",
                <code className="break-word">
                  {dedicated ? owner || "(chưa chọn)" : selected}.{table}
                </code>,
              ],
              ["Trạng thái cấu hình", "Bản nháp chưa lưu"],
            ]}
          />
          <Notice>
            Không chia sẻ physical history table giữa các stream độc lập.
          </Notice>
        </Panel>
      </div>
      <Panel title={`Phiên chạy minh họa gần nhất · ${selected}`}>
        <div className="recent-runs">
          {runs
            .filter((r) => r.targetId === target.id && r.schema === selected)
            .slice(0, 3)
            .map((r) => (
              <div key={r.id}>
                <Link to={`/runs/${r.id}`} className="mono">
                  {r.id}
                </Link>
                <Badge>{r.status}</Badge>
                <code>{r.version}</code>
                <span>{r.actor}</span>
                <code>{r.startedAt}</code>
              </div>
            ))}
        </div>
      </Panel>
      <Registration
        open={schemaModal}
        onOpenChange={setSchemaModal}
        schemaOnly
      />
    </>
  );
}
