import { useState } from "react";
import { Bot, KeyRound, Plus, Copy, ShieldCheck } from "lucide-react";
import { Link } from "react-router-dom";
import ScopeEditor from "../components/ScopeEditor";
import {
  Panel,
  PageHeading,
  Button,
  Badge,
  Notice,
  SearchBox,
  Modal,
  Field,
  Check,
  Unavailable,
} from "../components/ui";
const services = [
  {
    id: "svc-ci-finance-uat",
    name: "ci-finance-uat",
    purpose: "CI/CD pipeline triển khai môi trường UAT",
    owner: "devops-fin@bank.internal",
    prefix: "fm_uat_89a2…",
    last: "RUN-1047",
    expiry: "2025-12-31",
  },
  {
    id: "svc-release-payment",
    name: "release-payment",
    purpose: "Batch script thanh toán định kỳ",
    owner: "payment-team@bank.internal",
    prefix: "fm_rel_c41e…",
    last: "RUN-1045",
    expiry: "2025-09-15",
  },
];
export default function ServiceAccounts() {
  const [selected, setSelected] = useState(services[0].id),
    [q, setQ] = useState(""),
    [create, setCreate] = useState(false),
    [action, setAction] = useState(""),
    [example, setExample] = useState(false),
    [copied, setCopied] = useState(false);
  const service = services.find((s) => s.id === selected)!;
  return (
    <>
      <PageHeading
        title="Service Accounts & API Tokens"
        description="Định danh tự động hóa cho CI/CD và API scripts. Tách biệt với người dùng ứng dụng và tài khoản thực thi Oracle."
        breadcrumb="Quản trị hệ thống / Service Accounts"
        actions={
          <Button variant="primary" onClick={() => setCreate(true)}>
            <Plus size={16} />
            Tạo Service Account mới
          </Button>
        }
      />
      <div className="layout-editor">
        <Panel
          title="Danh bạ Service Accounts"
          aside={<Badge>2 tài khoản mẫu</Badge>}
        >
          <SearchBox
            value={q}
            onChange={setQ}
            placeholder="Tìm kiếm định danh…"
          />
          <p className="small muted">
            Token prefix ONLY — Không thể đọc lại toàn bộ token sau khi cấp.
          </p>
          <div className="service-list">
            {services
              .filter((s) => [s.name, s.purpose].join(" ").includes(q))
              .map((s) => (
                <div
                  className={`service-row ${selected === s.id ? "selected" : ""}`}
                  key={s.id}
                >
                  <Button variant="ghost" onClick={() => setSelected(s.id)}>
                    <Bot size={18} />
                    <strong className="mono">{s.name}</strong>
                  </Button>
                  <p>{s.purpose}</p>
                  <small className="muted block">{s.owner}</small>
                  <div className="spread wrap">
                    <code>{s.prefix}</code>
                    <Badge>Fixture · snapshot 2025</Badge>
                  </div>
                  <p className="small">
                    Lần dùng cuối: <Link to={`/runs/${s.last}`}>{s.last}</Link>{" "}
                    · Hết hạn: <code>{s.expiry}</code>
                  </p>
                  <div className="actions">
                    <Button
                      onClick={() => {
                        setSelected(s.id);
                        setAction("Luân chuyển");
                      }}
                    >
                      Luân chuyển token
                    </Button>
                    <Button
                      variant="danger"
                      onClick={() => {
                        setSelected(s.id);
                        setAction("Thu hồi");
                      }}
                    >
                      Thu hồi token
                    </Button>
                  </div>
                </div>
              ))}
          </div>
          <Notice title="Thiết kế bảo mật">
            Token được hash khi lưu trữ, chỉ hiện đầy đủ một lần khi cấp. Chưa
            triển khai token API trong Phase 1.
          </Notice>
        </Panel>
        <Panel
          title={
            <>
              Cấp phát Token cho <code>{service.name}</code>
            </>
          }
        >
          <ServiceForm
            key={service.id}
            name={service.name}
            purpose={service.purpose}
          />
          <Button
            className="full"
            onClick={() => {
              setCopied(false);
              setExample(true);
            }}
          >
            Xem trạng thái một lần (fixture)
          </Button>
        </Panel>
      </div>
      <Panel
        title="Hướng dẫn tích hợp REST API cho CI/CD"
        aside={<Badge>HỢP ĐỒNG DỰ KIẾN</Badge>}
      >
        <p className="muted">
          Ví dụ hợp đồng cho giai đoạn thực thi. Các endpoint nghiệp vụ hiện trả
          HTTP 501; không gửi token hoặc bundle thật.
        </p>
        <div className="grid-two">
          <div className="code-card">
            <h3>1. Upload Migration Bundle</h3>
            <pre>{`POST /api/v1/artifacts\nAuthorization: Bearer <TOKEN>\nContent-Type: multipart/form-data\n\nbundle=@migrations.zip\n\nDự kiến: 201 Created\n{ "artifact_id": "art_82fd21" }`}</pre>
          </div>
          <div className="code-card">
            <h3>2. Khởi tạo Migration Run</h3>
            <pre>{`POST /api/v1/migration-runs\nAuthorization: Bearer <TOKEN>\nIdempotency-Key: deploy-core-uat-1042\n\n{\n  "target_id": "db-uat-finance",\n  "schema_id": "schema-core-banking",\n  "execution_account_id": "acc-deploy-admin",\n  "artifact_id": "art_82fd21"\n}\n\nDự kiến: 202 Accepted\n{ "run_id": "run_1049",\n  "status_url": "/api/v1/migration-runs/run_1049",\n  "logs_url": "/api/v1/migration-runs/run_1049/logs" }`}</pre>
          </div>
        </div>
        <div className="inset mono small break-word">
          GET /api/v1/migration-runs/&#123;run_id&#125;
          <br />
          SSE dự kiến: GET /api/v1/migration-runs/&#123;run_id&#125;/events
        </div>
        <Notice tone="warning" title="Quy tắc Pipeline:">
          HTTP 200 khi đọc trạng thái không có nghĩa migration thành công.
          Client chỉ pass khi SUCCEEDED; FAILED vẫn fail dù repair SUCCEEDED.
          Repair không rollback DDL.
        </Notice>
      </Panel>
      <Modal
        open={create}
        onOpenChange={setCreate}
        title="Tạo Service Account mới"
        description="Bản nháp định danh và quyền cho API client. Chưa có token thật."
      >
        <ServiceForm />
      </Modal>
      <Modal
        open={!!action}
        onOpenChange={(open) => {
          if (!open) setAction("");
        }}
        title={`${action} token · ${service.name}?`}
        description={
          action === "Thu hồi"
            ? "CI/CD đang dùng token này sẽ không thể truy cập API sau khi thu hồi."
            : "Luân chuyển tạo token mới; cần quy định thời hạn hoặc thu hồi token cũ."
        }
      >
        <Notice tone="warning">
          Chưa triển khai phát hành, luân chuyển hoặc thu hồi token. Không có
          thay đổi nào được thực hiện.
        </Notice>
        <div className="dialog-actions">
          <Button onClick={() => setAction("")}>Hủy</Button>
          <Button variant="danger" disabled>
            Xác nhận {action.toLowerCase()}
          </Button>
        </div>
      </Modal>
      <Modal
        open={example}
        onOpenChange={setExample}
        title="Token minh họa — chỉ hiển thị tại đây"
        description="Fixture cho trạng thái cấp một lần. Giá trị này không thể xác thực API."
      >
        <Notice title="DỮ LIỆU MINH HỌA">
          Đây là placeholder, không phải token đã được cấp.
        </Notice>
        <code className="token-example">EXAMPLE_ONLY_NOT_A_VALID_TOKEN</code>
        <div className="dialog-actions">
          <Button
            onClick={() =>
              void navigator.clipboard
                .writeText("EXAMPLE_ONLY_NOT_A_VALID_TOKEN")
                .then(() => setCopied(true))
                .catch(() => setCopied(false))
            }
          >
            <Copy size={15} />
            {copied ? "Đã sao chép placeholder" : "Sao chép placeholder"}
          </Button>
          <Button variant="primary" onClick={() => setExample(false)}>
            Đã xem ví dụ
          </Button>
        </div>
      </Modal>
    </>
  );
}
function ServiceForm({
  name = "",
  purpose = "",
}: {
  name?: string;
  purpose?: string;
}) {
  const [actions, setActions] = useState(["read-runs", "read-logs"]),
    [expiry, setExpiry] = useState("");
  return (
    <form onSubmit={(e) => e.preventDefault()}>
      <Field label="Tên định danh (Identifier)" required>
        <input
          defaultValue={name}
          readOnly={!!name}
          placeholder="ci-finance-uat"
        />
      </Field>
      <Field label="Mục đích / Ghi chú cấp phát" required>
        <input defaultValue={purpose} placeholder="Mục đích của API client" />
      </Field>
      <Field label="Ngày hết hạn token" required>
        <input
          type="date"
          value={expiry}
          onChange={(e) => setExpiry(e.target.value)}
          min={new Date().toISOString().slice(0, 10)}
        />
      </Field>
      <h3>Quyền thao tác được phép</h3>
      <div className="grid-two inset">
        {[
          ["read-runs", "Xem lịch sử chạy"],
          ["read-logs", "Xem nhật ký"],
          ["upload", "Tải bundle"],
          ["submit", "Khởi tạo migration"],
        ].map(([key, label]) => (
          <Check
            key={key}
            label={
              <>
                {label} <code>({key})</code>
              </>
            }
            checked={actions.includes(key)}
            onChange={(v) =>
              setActions(
                v ? [...actions, key] : actions.filter((a) => a !== key),
              )
            }
          />
        ))}
      </div>
      <ScopeEditor />
      <Notice>
        <ShieldCheck size={15} />
        Quyền PROD phải được cấp riêng. Không dùng mật khẩu Oracle làm service
        token.
      </Notice>
      <Unavailable label="Tạo mã Token Scoped mới" />
      <span className="sr-only">
        <KeyRound size={1} />
      </span>
    </form>
  );
}
