import { useState, type FormEvent } from "react";
import { KeyRound, Plus, Eye, EyeOff, ShieldCheck } from "lucide-react";
import { previewFixtures as fixtures } from "../lib/fixtures";
const { accounts } = fixtures;
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
export default function Credentials() {
  const [q, setQ] = useState(""),
    [provider, setProvider] = useState(""),
    [schema, setSchema] = useState(""),
    [selected, setSelected] = useState("DEPLOY_ADMIN"),
    [create, setCreate] = useState(false);
  const data = accounts.filter(
      (a) =>
        a.name.toLowerCase().includes(q.toLowerCase()) &&
        (!provider || a.provider === provider) &&
        (!schema || a.schemas.includes(schema)),
    ),
    account = accounts.find((a) => a.name === selected)!;
  return (
    <>
      <PageHeading
        title="Tài khoản thực thi Oracle"
        description="Quản lý thông tin xác thực Oracle và ánh xạ schema. Tách biệt hoàn toàn với người dùng ứng dụng."
        breadcrumb="Quản trị thực thi / Tài khoản thực thi Oracle"
        actions={
          <>
            <Button disabled>Kiểm tra kết nối tất cả</Button>
            <Button variant="primary" onClick={() => setCreate(true)}>
              <Plus size={16} />
              Đăng ký tài khoản Oracle mới
            </Button>
          </>
        }
      />
      <div className="filter-bar">
        <Badge>Mục tiêu: ORA_UAT_FINANCE · FINUAT / UAT</Badge>
        <select
          aria-label="Nhà cung cấp khóa"
          value={provider}
          onChange={(e) => setProvider(e.target.value)}
        >
          <option value="">Tất cả nhà cung cấp khóa</option>
          <option>Local</option>
          <option>Vault</option>
        </select>
        <select
          aria-label="Schema ánh xạ"
          value={schema}
          onChange={(e) => setSchema(e.target.value)}
        >
          <option value="">Tất cả schema</option>
          <option>CORE_BANKING</option>
          <option>PAYMENT</option>
        </select>
        <SearchBox
          value={q}
          onChange={setQ}
          placeholder="Tìm kiếm tài khoản Oracle…"
        />
      </div>
      <div className="layout-editor">
        <Panel
          title="Danh sách tài khoản kết nối Oracle"
          aside={<Badge>{data.length} mục</Badge>}
        >
          <div className="credential-list">
            {data.map((a) => (
              <button
                key={a.name}
                className={`credential-row ${selected === a.name ? "selected" : ""}`}
                onClick={() => setSelected(a.name)}
              >
                <div>
                  <strong className="mono">
                    <KeyRound size={14} />
                    {a.name}
                  </strong>
                  <small className="mono muted block">
                    Mật khẩu: ••••••••••••
                  </small>
                </div>
                <div>
                  <Badge>
                    {a.provider === "Local"
                      ? "Mã hóa cục bộ"
                      : "HashiCorp Vault"}
                  </Badge>
                  <small className="block muted">
                    {a.provider === "Local"
                      ? "Thiết kế: AES-256-GCM"
                      : "secret/…/payment"}
                  </small>
                </div>
                <div className="stack tight">
                  {a.schemas.map((s) => (
                    <Badge key={s}>{s}</Badge>
                  ))}
                </div>
                <small className="mono muted">{a.rotated}</small>
              </button>
            ))}
          </div>
          {!data.length && <p className="empty">Không có tài khoản phù hợp.</p>}
          <Notice>
            Mật khẩu đã lưu chỉ hiển thị metadata / placeholder. Không có thao
            tác xem lại hoặc sao chép secret.
          </Notice>
        </Panel>
        <CredentialForm key={account.id} account={account} />
      </div>
      <Notice title="Nguyên tắc kiến trúc phân quyền">
        <ShieldCheck size={16} />
        Mỗi Target Database + Schema là một stream độc lập. Quyền Oracle quyết
        định quyền DDL; ánh xạ ứng dụng không thay thế GRANT.
      </Notice>
      <Modal
        open={create}
        onOpenChange={setCreate}
        title="Đăng ký tài khoản Oracle mới"
        description="Bản nháp giao diện · Không lưu hoặc kiểm tra thông tin xác thực."
      >
        <CredentialForm fresh />
      </Modal>
    </>
  );
}
function CredentialForm({
  account,
  fresh = false,
}: {
  account?: (typeof accounts)[number];
  fresh?: boolean;
}) {
  const [provider, setProvider] = useState(account?.provider ?? "Local"),
    [show, setShow] = useState(false),
    [password, setPassword] = useState(""),
    [confirm, setConfirm] = useState(""),
    [mapped, setMapped] = useState<string[]>(account?.schemas ?? []),
    [defaults, setDefaults] = useState<string[]>(
      account?.name === "DEPLOY_ADMIN" ? account.schemas : [],
    ),
    [error, setError] = useState(""),
    [reset, setReset] = useState(false);
  function validate(e: FormEvent) {
    e.preventDefault();
    setError(
      password !== confirm
        ? "Mật khẩu xác nhận chưa khớp."
        : !mapped.length
          ? "Chọn ít nhất một schema."
          : "Bản nháp chỉ được kiểm tra trên trình duyệt; không có thông tin nào được lưu.",
    );
    setPassword("");
    setConfirm("");
  }
  function changeMapping(schema: string, checked: boolean) {
    setMapped(
      checked ? [...mapped, schema] : mapped.filter((s) => s !== schema),
    );
    if (!checked) setDefaults(defaults.filter((s) => s !== schema));
  }
  return (
    <Panel
      title={
        fresh ? (
          "Tài khoản mới"
        ) : (
          <>
            Cập nhật / Luân chuyển: <code>{account?.name}</code>
          </>
        )
      }
    >
      <form onSubmit={validate} autoComplete="off">
        <Field
          label="Tên tài khoản Oracle (Username)"
          required
          hint="Đổi username bằng cách đăng ký tài khoản mới."
        >
          <input
            required
            defaultValue={account?.name ?? ""}
            readOnly={!fresh}
            placeholder="DEPLOY_ADMIN"
          />
        </Field>
        <Field label="Nguồn lưu trữ bí mật (Secret Provider)">
          <select
            value={provider}
            onChange={(e) => {
              setProvider(e.target.value);
              setPassword("");
              setConfirm("");
            }}
          >
            <option value="Local">Mã hóa cục bộ (mặc định)</option>
            <option value="Vault">HashiCorp Vault (tùy chọn)</option>
          </select>
        </Field>
        {provider === "Local" ? (
          <div className="inset">
            <h3>
              {fresh ? "Mật khẩu Oracle mới" : "Luân chuyển mật khẩu Oracle"}{" "}
              <Badge>Write-only</Badge>
            </h3>
            <Notice>
              Không hỗ trợ xem lại mật khẩu đã lưu. Chỉ nhập giá trị mới để thay
              thế. Không dùng mật khẩu thật trong bản xem trước.
            </Notice>
            <Field label="Mật khẩu mới Oracle">
              <div className="password-field">
                <input
                  autoComplete="new-password"
                  type={show ? "text" : "password"}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="Nhập giá trị minh họa"
                />
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => setShow(!show)}
                  aria-label={show ? "Ẩn mật khẩu mới" : "Hiện mật khẩu mới"}
                >
                  {show ? <EyeOff size={16} /> : <Eye size={16} />}
                </Button>
              </div>
            </Field>
            <Field label="Xác nhận mật khẩu mới">
              <input
                type="password"
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                placeholder="Nhập lại giá trị minh họa"
              />
            </Field>
          </div>
        ) : (
          <Field
            label="Vault secret reference"
            hint="Chỉ tham chiếu secret; chưa có Vault adapter."
          >
            <input placeholder="secret/data/oracle/payment" />
          </Field>
        )}
        <h3>Ánh xạ Target Schema & Quyền mặc định</h3>
        {["CORE_BANKING", "PAYMENT"].map((schema) => (
          <div className="inset" key={schema}>
            <Check
              label={<code>{schema}</code>}
              checked={mapped.includes(schema)}
              onChange={(checked) => changeMapping(schema, checked)}
            />
            <Check
              label={`Đặt làm tài khoản mặc định cho ${schema}`}
              checked={defaults.includes(schema)}
              disabled={!mapped.includes(schema)}
              onChange={(checked) =>
                setDefaults(
                  checked
                    ? [...defaults, schema]
                    : defaults.filter((s) => s !== schema),
                )
              }
            />
          </div>
        ))}
        <Notice tone="warning">
          Tài khoản có quyền DBA hoặc cross-schema grants vẫn có quyền rộng. Cấu
          hình Flyway không phải cơ chế cách ly quyền Oracle.
        </Notice>
        <div className="inset">
          <h3>Probed Diagnostics</h3>
          <p className="small muted">
            Reachability · Authentication · Privileges: Chưa kiểm tra.
          </p>
          <Button type="button" disabled>
            Kiểm tra kết nối
          </Button>
        </div>
        {error && (
          <p className="form-message" role="status">
            {error}
          </p>
        )}
        <div className="actions wrap">
          <Button type="button" onClick={() => setReset(true)}>
            Hủy thay đổi
          </Button>
          <Button type="submit">Kiểm tra bản nháp</Button>
        </div>
        <Unavailable
          label={fresh ? "Đăng ký tài khoản" : "Lưu & Cập nhật ánh xạ"}
        />
      </form>
      <Modal
        open={reset}
        onOpenChange={setReset}
        title="Hủy bản nháp thông tin xác thực?"
        description={`Xóa mật khẩu mới và các thay đổi chưa lưu của ${account?.name ?? "tài khoản mới"}.`}
      >
        <div className="dialog-actions">
          <Button onClick={() => setReset(false)}>Tiếp tục chỉnh sửa</Button>
          <Button
            variant="danger"
            onClick={() => {
              setPassword("");
              setConfirm("");
              setMapped(account?.schemas ?? []);
              setDefaults(
                account?.name === "DEPLOY_ADMIN" ? account.schemas : [],
              );
              setProvider(account?.provider ?? "Local");
              setError("");
              setReset(false);
            }}
          >
            Hủy bản nháp
          </Button>
        </div>
      </Modal>
    </Panel>
  );
}
