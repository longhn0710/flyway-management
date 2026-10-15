import { useState } from "react";
import { Link } from "react-router-dom";
import { LockKeyhole, Building2, ShieldCheck } from "lucide-react";
import {
  Panel,
  PageHeading,
  Button,
  Badge,
  Notice,
  Field,
  Check,
  Unavailable,
  Modal,
} from "../components/ui";
export default function Authentication() {
  const [tab, setTab] = useState("local"),
    [local, setLocal] = useState(true),
    [ldap, setLdap] = useState(false),
    [oidc, setOidc] = useState(false),
    [warning, setWarning] = useState(false),
    [transport, setTransport] = useState("ldaps"),
    [endpoint, setEndpoint] = useState("ldaps://ad.bank.internal:636");
  function toggleLocal(value: boolean) {
    if (!value && !ldap && !oidc) {
      setWarning(true);
      return;
    }
    setLocal(value);
  }
  return (
    <>
      <PageHeading
        title="Nhà cung cấp xác thực (Identity Providers)"
        description="Cấu hình phương thức đăng nhập console: cục bộ, LDAP / Active Directory và OpenID Connect."
        breadcrumb="Cài đặt hệ thống / Nhà cung cấp xác thực"
        actions={
          <>
            <Button disabled>Kiểm tra toàn bộ kết nối</Button>
            <Button variant="primary" disabled>
              Lưu thay đổi cấu hình
            </Button>
          </>
        }
      />
      <p className="small muted">
        Cần cấu hình API token cho CI/CD?{" "}
        <Link to="/access/service-accounts">Quản lý Service Accounts →</Link>
      </p>
      <Notice tone="warning" title="Emergency Admin Guardrail:">
        Giữ ít nhất một phương thức đăng nhập Admin khả dụng. Không tự động
        fallback giữa LDAP và tài khoản cục bộ trùng tên.
      </Notice>
      <div
        className="tabs auth-tabs"
        role="tablist"
        aria-label="Nhà cung cấp xác thực"
      >
        {[
          ["local", "Cục bộ", LockKeyhole],
          ["ldap", "LDAP / Active Directory", Building2],
          ["oidc", "OIDC / SSO", ShieldCheck],
        ].map(([key, label, Icon]) => {
          const I = Icon as typeof LockKeyhole;
          return (
            <button
              role="tab"
              aria-selected={tab === key}
              key={String(key)}
              onClick={() => setTab(String(key))}
            >
              <I size={16} />
              {String(label)}
            </button>
          );
        })}
      </div>
      {tab === "local" && (
        <Panel
          title={
            <>
              <LockKeyhole size={17} />
              1. Xác thực Cục bộ (Local Authentication)
            </>
          }
          aside={<Badge>BẢN NHÁP</Badge>}
        >
          <Check
            label="Kích hoạt đăng nhập cục bộ"
            checked={local}
            onChange={toggleLocal}
          />
          <div className="grid-two">
            <div className="inset">
              <h3>Chính sách mật khẩu</h3>
              <Field label="Độ dài tối thiểu">
                <input type="number" min="8" max="128" defaultValue="12" />
              </Field>
              <p className="small">
                Thiết kế: Argon2id. Tham số sẽ được xác minh ở giai đoạn
                Identity.
              </p>
              <p className="small muted">
                Không có chính sách bắt buộc đổi mật khẩu định kỳ trong Phase 1.
              </p>
            </div>
            <div className="inset">
              <h3>Tài khoản cứu hộ khẩn cấp</h3>
              <Field label="Tên đăng nhập Admin">
                <input placeholder="recovery_admin" autoComplete="off" />
              </Field>
              <Field label="Mật khẩu mới (Write-only replace)">
                <input
                  type="password"
                  placeholder="Không nhập mật khẩu thật trong bản xem trước"
                  autoComplete="new-password"
                />
              </Field>
            </div>
          </div>
          <Notice>
            Danh tính cục bộ thuộc SQLite của Flyway Manager, tách biệt với
            Oracle schema. Chưa có bootstrap hoặc password hashing thực trong
            Phase 1.
          </Notice>
          <Unavailable label="Lưu chính sách Local" />
        </Panel>
      )}
      {tab === "ldap" && (
        <Panel
          title={
            <>
              <Building2 size={17} />
              2. LDAP / Active Directory
            </>
          }
          aside={<Badge>CHƯA KẾT NỐI</Badge>}
        >
          <Check
            label="Kích hoạt LDAP / AD"
            checked={ldap}
            onChange={setLdap}
          />
          <div className="grid-two">
            <Field label="Kênh truyền bắt buộc">
              <select
                value={transport}
                onChange={(e) => {
                  setTransport(e.target.value);
                  setEndpoint(
                    e.target.value === "ldaps"
                      ? "ldaps://ad.bank.internal:636"
                      : "ldap://ad.bank.internal:389",
                  );
                }}
              >
                <option value="ldaps">LDAPS</option>
                <option value="starttls">LDAP + StartTLS bắt buộc</option>
              </select>
            </Field>
            <Field label="Thời gian chờ kết nối (ms)">
              <input type="number" min="1000" max="60000" defaultValue="5000" />
            </Field>
          </div>
          <Field
            label="LDAP Endpoint"
            required
            hint={
              transport === "ldaps"
                ? "Yêu cầu ldaps:// và xác minh chứng chỉ TLS."
                : "Yêu cầu nâng cấp StartTLS thành công trước user bind."
            }
          >
            <input
              type="url"
              value={endpoint}
              onChange={(e) => setEndpoint(e.target.value)}
              aria-invalid={
                transport === "ldaps" && !endpoint.startsWith("ldaps://")
              }
            />
          </Field>
          {transport === "ldaps" && !endpoint.startsWith("ldaps://") && (
            <p className="form-error" role="alert">
              LDAPS endpoint phải bắt đầu bằng ldaps://.
            </p>
          )}
          <div className="grid-two">
            <Field label="Base DN">
              <input defaultValue="ou=Users,dc=bank,dc=vn" />
            </Field>
            <Field label="User DN template (Direct bind)">
              <input defaultValue="uid={username},ou=Users,dc=bank,dc=vn" />
            </Field>
          </div>
          <Field label="Immutable identity attribute">
            <select>
              <option>objectGUID (Active Directory)</option>
              <option>entryUUID (LDAP)</option>
            </select>
          </Field>
          <Notice title="Direct LDAP bind · Không lưu mật khẩu LDAP">
            Không có service-bind password lưu trong provider settings. Test
            credentials chỉ dùng tạm trong phiên kiểm tra; không lưu vào SQLite.
          </Notice>
          <div className="inset">
            <h3>Thử nghiệm xác thực tạm thời</h3>
            <div className="grid-two">
              <Field label="Username kiểm tra">
                <input placeholder="test_user" autoComplete="off" />
              </Field>
              <Field label="Mật khẩu kiểm tra (không lưu)">
                <input
                  type="password"
                  autoComplete="off"
                  placeholder="Chỉ dùng giá trị minh họa"
                />
              </Field>
            </div>
            <Button disabled>Thử nghiệm LDAP</Button>
            <p className="small muted">Kết quả: Chưa kiểm tra.</p>
          </div>
          <Unavailable label="Lưu cấu hình LDAP" />
        </Panel>
      )}
      {tab === "oidc" && (
        <Panel
          title={
            <>
              <ShieldCheck size={17} />
              3. OpenID Connect (Enterprise OIDC / SSO)
            </>
          }
          aside={<Badge>CHƯA KẾT NỐI</Badge>}
        >
          <Check
            label="Kích hoạt OIDC / SSO"
            checked={oidc}
            onChange={setOidc}
          />
          <Field label="OpenID Provider Issuer URL" required>
            <input type="url" placeholder="https://iam.bank.internal" />
          </Field>
          <div className="grid-two">
            <Field label="Client ID" required>
              <input placeholder="flyway-manager" />
            </Field>
            <Field label="Client Secret (Write-only replace)">
              <input
                type="password"
                placeholder="Chỉ nhập để thay thế"
                autoComplete="new-password"
              />
            </Field>
          </div>
          <Field label="Redirect URI">
            <input
              type="url"
              placeholder="https://manager.example/auth/callback/oidc"
            />
          </Field>
          <div className="grid-two">
            <div className="inset">
              <h3>Authorization Code + PKCE</h3>
              <p className="small muted">
                Thiết kế bắt buộc PKCE (S256). Chưa triển khai OIDC discovery
                hoặc callback.
              </p>
            </div>
            <div className="inset">
              <h3>Immutable Identity: issuer + sub</h3>
              <p className="small muted">
                Không ánh xạ tài khoản chỉ bằng email; danh tính được gắn với
                provider và subject bất biến.
              </p>
            </div>
          </div>
          <Button disabled>Kiểm tra OIDC Discovery</Button>
          <Unavailable label="Lưu cấu hình OIDC" />
        </Panel>
      )}
      <Modal
        open={warning}
        onOpenChange={setWarning}
        title="Giữ đường đăng nhập Admin khả dụng"
        description="Bản nháp không thể tắt phương thức đăng nhập cuối cùng."
      >
        <Notice tone="warning">
          Xác minh một đường đăng nhập Admin thay thế trước khi tắt Local. Tất
          cả provider hiện đều chưa được triển khai.
        </Notice>
        <Button onClick={() => setWarning(false)}>Giữ cấu hình cục bộ</Button>
      </Modal>
    </>
  );
}
