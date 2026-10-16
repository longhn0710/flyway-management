import { useState } from "react";
import { NavLink, Outlet, Link, useLocation } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Database,
  History,
  Play,
  KeyRound,
  Users,
  Bot,
  Settings,
  BookOpen,
  Menu,
  X,
  LogOut,
  HeartPulse,
} from "lucide-react";
import { api, clearCsrfToken, getHealth } from "../lib/contracts";
import { useIdentity } from "../lib/identity-context";
import { Button, Modal, KeyValues, Notice, Field, Badge } from "./ui";
const nav = [
  { to: "/runs", text: "Lịch sử chạy", icon: History },
  { to: "/runs/new", text: "Chạy migration", icon: Play },
  { to: "/databases", text: "Database & Schema", icon: Database },
  { to: "/execution-accounts", text: "Tài khoản thực thi", icon: KeyRound },
  { to: "/access/users", text: "Người dùng & Quyền", icon: Users },
  { to: "/access/service-accounts", text: "Service Accounts", icon: Bot },
  { to: "/settings/authentication", text: "Cài đặt", icon: Settings },
];
export function Brand() {
  return (
    <Link to="/" className="brand">
      <span className="brand-icon">
        <Database size={17} />
      </span>
      <strong>Flyway Manager</strong>
      <span className="brand-subtitle">Oracle Migration Console</span>
    </Link>
  );
}
export function Shell() {
  const { pathname } = useLocation();
  const identity = useIdentity();
  const visibleNav = nav.filter(
    ({ to }) =>
      identity.user.role === "Admin" ||
      ![
        "/execution-accounts",
        "/access/users",
        "/access/service-accounts",
        "/settings/authentication",
      ].includes(to),
  );
  const queryClient = useQueryClient();
  const [menu, setMenu] = useState(false),
    [help, setHelp] = useState(false),
    [system, setSystem] = useState(false),
    [accountOpen, setAccountOpen] = useState(false),
    [currentPassword, setCurrentPassword] = useState(""),
    [newPassword, setNewPassword] = useState(""),
    [confirmNewPassword, setConfirmNewPassword] = useState(""),
    [passwordNotice, setPasswordNotice] = useState("");
  const health = useQuery({
    queryKey: ["health"],
    queryFn: ({ signal }) => getHealth(signal),
    refetchInterval: 30000,
    retry: 1,
  });
  const logout = useMutation({
    mutationFn: () => api<void>("/auth/logout", { method: "POST", body: "{}" }),
    onSettled: () => {
      clearCsrfToken();
      queryClient.clear();
      window.location.assign("/login");
    },
  });
  const password = useMutation({
    mutationFn: () =>
      api<void>("/auth/password", {
        method: "POST",
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      }),
    onSuccess: () => {
      clearCsrfToken();
      queryClient.clear();
      window.location.assign(`/login?next=${encodeURIComponent(pathname)}`);
    },
    onError: (error) => setPasswordNotice(error.message),
  });
  return (
    <>
      <a className="skip-link" href="#main">
        Đến nội dung chính
      </a>
      <header className="topbar">
        <Button
          variant="ghost"
          className="mobile-menu"
          aria-label="Mở điều hướng"
          onClick={() => setMenu(true)}
        >
          <Menu size={20} />
        </Button>
        <Brand />
        <div className="topbar-actions">
          <Button variant="ghost" onClick={() => setHelp(true)}>
            <BookOpen size={15} />
            <span className="desktop-only">Tài liệu</span>
          </Button>
          <Button
            variant="ghost"
            onClick={() => setSystem(true)}
            aria-label="Trạng thái hệ thống"
          >
            <HeartPulse size={17} />
            <span
              className={`health-dot ${health.isSuccess ? "up" : "down"}`}
            />
          </Button>
          <button
            className="user-avatar"
            aria-label="Tài khoản và phiên"
            onClick={() => setAccountOpen(true)}
          >
            {identity.user.display_name
              .split(/\s+/)
              .map((part) => part[0])
              .slice(-2)
              .join("")
              .toUpperCase()}
          </button>
          <div className="desktop-only profile">
            <strong>
              {identity.user.display_name} · {identity.user.role}
            </strong>
            <small>{identity.user.username}</small>
          </div>
          <button
            className="icon-link"
            aria-label="Đăng xuất"
            onClick={() => logout.mutate()}
            disabled={logout.isPending}
          >
            <LogOut size={17} />
          </button>
        </div>
      </header>
      <aside className="sidebar">
        <nav aria-label="Điều hướng chính">
          {visibleNav.map(({ to, text, icon: Icon }) => (
            <NavLink
              end={false}
              key={to}
              to={to}
              className={({ isActive }) =>
                isActive &&
                !(to === "/runs" && pathname.startsWith("/runs/new"))
                  ? "active"
                  : ""
              }
            >
              <Icon size={17} />
              {text}
            </NavLink>
          ))}
        </nav>
        <div className="sidebar-footer">
          <span className="phase-label">PHASE 3</span>
          <p>
            Native Go · SQLite
            <br />
            React console
          </p>
        </div>
      </aside>
      <Modal
        open={menu}
        onOpenChange={setMenu}
        title="Điều hướng"
        description="Chọn màn hình trong console."
      >
        <nav className="mobile-nav">
          {visibleNav.map(({ to, text, icon: Icon }) => (
            <Link key={to} to={to} onClick={() => setMenu(false)}>
              <Icon size={18} />
              {text}
            </Link>
          ))}
        </nav>
      </Modal>
      <main id="main" className="main">
        <div className="preview-banner">
          <span>
            <strong>Console bảo mật</strong> — Tất cả dữ liệu quản trị được tải
            từ API và phân quyền phía máy chủ.
          </span>
          <Button variant="ghost" onClick={() => setSystem(true)}>
            {health.isPending
              ? "Đang kết nối API…"
              : health.isError
                ? "API mất kết nối · Thử lại"
                : "API trực tiếp"}
            <HeartPulse size={14} />
          </Button>
        </div>
        <Outlet />
      </main>
      <Modal
        open={help}
        onOpenChange={setHelp}
        title="Flyway Manager"
        description="Hướng dẫn cho phiên bản nền tảng ứng dụng."
      >
        <p>
          Quản lý người dùng, quyền truy cập theo target/schema, service
          account, cấu hình xác thực và registry Oracle.
        </p>
        <Notice title="Tình trạng thực thi">
          Migration, lịch sử và log dùng API thực. Kiểm tra Flyway runtime trước
          khi chạy. Kết nối Oracle chỉ được xác minh khi preflight liên hệ
          database thành công.
        </Notice>
        <p>
          Hướng dẫn cài đặt, cấu hình và xây dựng ứng dụng nằm trong README.md
          của repository.
        </p>
        <Button onClick={() => setHelp(false)}>Đã hiểu</Button>
      </Modal>
      <Modal
        open={system}
        onOpenChange={setSystem}
        title="Trạng thái hệ thống"
        description="Dữ liệu trực tiếp từ Go REST API; không phải fixture."
      >
        {health.isPending ? (
          <div className="skeleton" aria-label="Đang tải trạng thái" />
        ) : health.isError ? (
          <Notice tone="error">
            {health.error.message}
            <Button onClick={() => void health.refetch()}>Thử lại</Button>
          </Notice>
        ) : (
          <KeyValues
            rows={[
              ["HTTP server", health.data.status],
              ["SQLite", health.data.database],
              ["Schema version", health.data.schema_version],
              ["Installation ID", <code>{health.data.installation_id}</code>],
              ["Thực thi migration", "Chưa triển khai"],
              [
                "Authentication",
                health.data.capabilities.authentication
                  ? "Đang bật"
                  : "Chưa bật",
              ],
            ]}
          />
        )}
        <Button onClick={() => setSystem(false)}>
          <X size={15} />
          Đóng
        </Button>
      </Modal>
      <Modal
        open={accountOpen}
        onOpenChange={setAccountOpen}
        title="Tài khoản và phiên đăng nhập"
        description="Phiên do máy chủ quản lý; đăng xuất sẽ thu hồi phiên hiện tại."
      >
        <KeyValues
          rows={[
            ["Tên", identity.user.display_name],
            ["Username", identity.user.username],
            ["Vai trò", identity.user.role],
            ["Hết hạn", new Date(identity.expires_at).toLocaleString()],
          ]}
        />
        <h3>Đổi mật khẩu</h3>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            setPasswordNotice("");
            password.mutate();
          }}
        >
          <Field label="Mật khẩu hiện tại" required>
            <input
              required
              type="password"
              autoComplete="current-password"
              value={currentPassword}
              onChange={(event) => setCurrentPassword(event.target.value)}
            />
          </Field>
          <Field label="Mật khẩu mới" required>
            <input
              required
              minLength={12}
              type="password"
              autoComplete="new-password"
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
            />
          </Field>
          <Field label="Xác nhận mật khẩu mới" required>
            <input
              required
              minLength={12}
              type="password"
              autoComplete="new-password"
              value={confirmNewPassword}
              onChange={(event) => setConfirmNewPassword(event.target.value)}
            />
          </Field>
          {newPassword &&
            confirmNewPassword &&
            newPassword !== confirmNewPassword && (
              <p className="form-error" role="alert">
                Mật khẩu xác nhận chưa khớp.
              </p>
            )}
          {passwordNotice && (
            <Notice tone={password.isError ? "error" : "info"}>
              {passwordNotice}
            </Notice>
          )}
          <Button
            variant="primary"
            disabled={
              password.isPending ||
              !newPassword ||
              newPassword !== confirmNewPassword
            }
          >
            Cập nhật mật khẩu
          </Button>
        </form>
        <SessionList />
      </Modal>
    </>
  );
}

interface Session {
  id: string;
  created_at: string;
  expires_at: string;
  current: boolean;
}
function SessionList() {
  const sessions = useQuery({
    queryKey: ["sessions"],
    queryFn: () => api<Session[]>("/auth/sessions"),
  });
  const queryClient = useQueryClient();
  const revoke = useMutation({
    mutationFn: (id: string) =>
      api<void>(`/auth/sessions/${encodeURIComponent(id)}`, {
        method: "DELETE",
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["sessions"] }),
  });
  return (
    <section>
      <h3>Phiên hoạt động</h3>
      {sessions.isPending ? (
        <div className="skeleton" role="status" />
      ) : sessions.isError ? (
        <Notice tone="error">{sessions.error.message}</Notice>
      ) : sessions.data.length === 0 ? (
        <p className="empty">Không còn phiên hoạt động.</p>
      ) : (
        sessions.data.map((session) => (
          <div className="spread wrap inset" key={session.id}>
            <span>
              {new Date(session.created_at).toLocaleString()} · Hết hạn{" "}
              {new Date(session.expires_at).toLocaleString()}{" "}
              {session.current && <Badge tone="success">Phiên hiện tại</Badge>}
            </span>
            <Button
              variant="danger"
              disabled={session.current || revoke.isPending}
              onClick={() => revoke.mutate(session.id)}
            >
              Thu hồi
            </Button>
          </div>
        ))
      )}
    </section>
  );
}
