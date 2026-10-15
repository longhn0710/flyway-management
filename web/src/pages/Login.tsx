import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  ArrowRight,
  LockKeyhole,
  Building2,
  ShieldCheck,
  Eye,
  EyeOff,
} from "lucide-react";
import { Brand } from "../components/Shell";
import { Button, Field, Notice } from "../components/ui";
export default function Login() {
  const [provider, setProvider] = useState("Local"),
    [show, setShow] = useState(false);
  const navigate = useNavigate(),
    [params] = useSearchParams();
  function preview() {
    sessionStorage.setItem("fm-preview", "true");
    const next = params.get("next") ?? "/runs";
    navigate(
      next.startsWith("/") &&
        !next.startsWith("//") &&
        !next.startsWith("/login")
        ? next
        : "/runs",
    );
  }
  return (
    <div className="login-page">
      <header className="topbar">
        <Brand />
      </header>
      <main className="login-main">
        <section className="login-card">
          <h1>Đăng nhập hệ thống</h1>
          <p>Quản trị và điều phối migration schema cơ sở dữ liệu Oracle.</p>
          <div
            className="tabs login-tabs"
            role="tablist"
            aria-label="Phương thức đăng nhập"
          >
            {["Local", "LDAP"].map((mode) => (
              <button
                key={mode}
                role="tab"
                aria-selected={provider === mode}
                onClick={() => setProvider(mode)}
              >
                {mode === "Local" ? (
                  <LockKeyhole size={15} />
                ) : (
                  <Building2 size={15} />
                )}{" "}
                {mode === "Local"
                  ? "Tài khoản cục bộ"
                  : "LDAP / Active Directory"}
              </button>
            ))}
          </div>
          <p className="small muted">
            Đăng nhập bằng{" "}
            {provider === "Local" ? "tài khoản cục bộ" : "tài khoản LDAP"}
          </p>
          <form onSubmit={(e) => e.preventDefault()}>
            <Field label="Tên đăng nhập">
              <input
                placeholder="Ví dụ: admin hoặc operator_user"
                autoComplete="username"
                disabled
              />
            </Field>
            <Field label="Mật khẩu">
              <div className="password-field">
                <input
                  type={show ? "text" : "password"}
                  placeholder="••••••••••••"
                  autoComplete="current-password"
                  disabled
                />
                <Button
                  variant="ghost"
                  onClick={() => setShow(!show)}
                  type="button"
                  aria-label={show ? "Ẩn mật khẩu" : "Hiện mật khẩu"}
                >
                  {show ? <EyeOff size={16} /> : <Eye size={16} />}
                </Button>
              </div>
            </Field>
            <Button variant="primary" className="full" disabled>
              Đăng nhập vào console <ArrowRight size={16} />
            </Button>
          </form>
          <div className="divider">HOẶC TIẾP TỤC VỚI</div>
          <Button className="full" disabled>
            <ShieldCheck size={16} />
            Đăng nhập bằng SSO (OIDC)
          </Button>
          <Notice title="Phiên bản nền tảng · Phase 1">
            Xác thực chưa được triển khai. Xem trước sử dụng dữ liệu minh họa,
            không cần tài khoản hoặc mật khẩu.
          </Notice>
          <Button variant="primary" className="full" onClick={preview}>
            Mở console minh họa <ArrowRight size={16} />
          </Button>
        </section>
      </main>
      <footer>Flyway Manager · Ứng dụng độc lập · Oracle-first</footer>
    </div>
  );
}
