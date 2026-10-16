import { useState } from "react";
import { NavLink, Outlet, Link, useLocation } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Home,
  FolderTree,
  Database,
  History,
  Play,
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
  { to: "/home", text: "Home", icon: Home },
  { to: "/instances", text: "Instances", icon: Database },
  { to: "/schemas", text: "Databases / Schemas", icon: FolderTree },
  { to: "/runs/new", text: "Run Migration", icon: Play },
  { to: "/runs", text: "Migration History", icon: History },
  { to: "/access/users", text: "Users & Permissions", icon: Users },
  { to: "/access/service-accounts", text: "Service Accounts", icon: Bot },
  { to: "/settings/authentication", text: "Settings", icon: Settings },
];
export function Brand() {
  return (
    <Link to="/" className="brand">
      <span className="brand-icon">
        <Database size={17} />
      </span>
      <strong>Flyway Manager</strong>
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
        Skip to main content
      </a>
      <header className="topbar">
        <Button
          variant="ghost"
          className="mobile-menu"
          aria-label="Open navigation"
          onClick={() => setMenu(true)}
        >
          <Menu size={20} />
        </Button>
        <Brand />
        <div className="topbar-actions">
          <Button variant="ghost" onClick={() => setHelp(true)}>
            <BookOpen size={15} />
            <span className="desktop-only">Docs</span>
          </Button>
          <Button
            variant="ghost"
            onClick={() => setSystem(true)}
            aria-label="System status"
          >
            <HeartPulse size={17} />
            <span
              className={`health-dot ${health.isSuccess ? "up" : "down"}`}
            />
          </Button>
          <button
            className="user-avatar"
            aria-label="Account and sessions"
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
            aria-label="Sign out"
            onClick={() => logout.mutate()}
            disabled={logout.isPending}
          >
            <LogOut size={17} />
          </button>
        </div>
      </header>
      <aside className="sidebar">
        <nav aria-label="Primary navigation">
          <span className="nav-label">Navigation</span>
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
      </aside>
      <Modal
        open={menu}
        onOpenChange={setMenu}
        title="Navigation"
        description="Choose a page."
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
        <Outlet />
      </main>
      <Modal
        open={help}
        onOpenChange={setHelp}
        title="Flyway Manager"
        description="Database migration operations."
      >
        <p>
          Manage instances, connections, schema allowlists, migration runs and
          application access.
        </p>
        <p>
          Oracle is currently implemented. PostgreSQL, MySQL and SQL Server are
          future options with connection and execution actions unavailable.
        </p>
        <Notice title="Migration execution">
          Select an instance and allowed schema, upload an immutable package,
          then review live Oracle preflight before confirming execution.
        </Notice>
        <p>
          See the repository README for installation and operating instructions.
        </p>
        <Button onClick={() => setHelp(false)}>Close</Button>
      </Modal>
      <Modal
        open={system}
        onOpenChange={setSystem}
        title="System status"
        description="Current application status."
      >
        {health.isPending ? (
          <div className="skeleton" aria-label="Loading system status" />
        ) : health.isError ? (
          <Notice tone="error">
            {health.error.message}
            <Button onClick={() => void health.refetch()}>Retry</Button>
          </Notice>
        ) : (
          <KeyValues
            rows={[
              ["HTTP server", health.data.status],
              ["SQLite", health.data.database],
              ["Schema version", health.data.schema_version],
              ["Installation ID", <code>{health.data.installation_id}</code>],
              [
                "Migration execution",
                health.data.capabilities.migration_execution
                  ? "Enabled"
                  : "Unavailable",
              ],
              [
                "Authentication",
                health.data.capabilities.authentication
                  ? "Enabled"
                  : "Disabled",
              ],
            ]}
          />
        )}
        <Button onClick={() => setSystem(false)}>
          <X size={15} />
          Close
        </Button>
      </Modal>
      <Modal
        open={accountOpen}
        onOpenChange={setAccountOpen}
        title="Account and sessions"
        description="Manage your password and active sessions."
      >
        <KeyValues
          rows={[
            ["Name", identity.user.display_name],
            ["Username", identity.user.username],
            ["Role", identity.user.role],
            ["Expires", new Date(identity.expires_at).toLocaleString()],
          ]}
        />
        <h3>Change password</h3>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            setPasswordNotice("");
            password.mutate();
          }}
        >
          <Field label="Current password" required>
            <input
              required
              type="password"
              autoComplete="current-password"
              value={currentPassword}
              onChange={(event) => setCurrentPassword(event.target.value)}
            />
          </Field>
          <Field label="New password" required>
            <input
              required
              minLength={12}
              type="password"
              autoComplete="new-password"
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
            />
          </Field>
          <Field label="Confirm new password" required>
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
                Passwords do not match.
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
            Update password
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
      <h3>Active sessions</h3>
      {sessions.isPending ? (
        <div className="skeleton" role="status" />
      ) : sessions.isError ? (
        <Notice tone="error">{sessions.error.message}</Notice>
      ) : sessions.data.length === 0 ? (
        <p className="empty">No active sessions.</p>
      ) : (
        sessions.data.map((session) => (
          <div className="spread wrap inset" key={session.id}>
            <span>
              {new Date(session.created_at).toLocaleString()} · Expires{" "}
              {new Date(session.expires_at).toLocaleString()}{" "}
              {session.current && <Badge tone="success">Current session</Badge>}
            </span>
            <Button
              variant="danger"
              disabled={session.current || revoke.isPending}
              onClick={() => revoke.mutate(session.id)}
            >
              Revoke
            </Button>
          </div>
        ))
      )}
    </section>
  );
}
