import { useState } from "react";
import { NavLink, Outlet, Link, useLocation } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
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
import { getHealth } from "../lib/contracts";
import { Button, Modal, KeyValues, Notice } from "./ui";
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
  const [menu, setMenu] = useState(false),
    [help, setHelp] = useState(false),
    [system, setSystem] = useState(false);
  const health = useQuery({
    queryKey: ["health"],
    queryFn: ({ signal }) => getHealth(signal),
    refetchInterval: 30000,
    retry: 1,
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
          <div className="user-avatar">NA</div>
          <div className="desktop-only profile">
            <strong>Nguyễn Minh Anh · Admin</strong>
            <small>Danh tính minh họa</small>
          </div>
          <Link
            className="icon-link"
            to="/login"
            aria-label="Thoát xem trước"
            onClick={() => sessionStorage.removeItem("fm-preview")}
          >
            <LogOut size={17} />
          </Link>
        </div>
      </header>
      <aside className="sidebar">
        <nav aria-label="Điều hướng chính">
          {nav.map(({ to, text, icon: Icon }) => (
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
          <span className="phase-label">PHASE 1</span>
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
          {nav.map(({ to, text, icon: Icon }) => (
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
            <strong>Dữ liệu minh họa · Development fixtures</strong> — Các thay
            đổi là bản nháp trong bộ nhớ. Chưa kết nối Oracle hoặc xác thực.
          </span>
          <Button variant="ghost" onClick={() => setSystem(true)}>
            {health.isPending
              ? "Đang kết nối API…"
              : health.isError
                ? "API mất kết nối · Thử lại"
                : "Go API sẵn sàng"}
            <HeartPulse size={14} />
          </Button>
        </div>
        <Outlet />
      </main>
      <Modal
        open={help}
        onOpenChange={setHelp}
        title="Flyway Manager · Phase 1"
        description="Hướng dẫn cho phiên bản nền tảng ứng dụng."
      >
        <p>
          Điều hướng giữa các màn hình, tìm kiếm dữ liệu minh họa, soạn cấu hình
          và xem nhật ký mẫu. Tất cả dữ liệu nghiệp vụ hiện tại là fixture trong
          trình duyệt.
        </p>
        <Notice title="Giới hạn hiện tại">
          Chưa hỗ trợ đăng nhập, lưu thông tin xác thực, tải bundle lên máy chủ
          hoặc chạy Flyway. Các nút thực thi được vô hiệu hóa.
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
              ["Authentication", "Chưa triển khai"],
            ]}
          />
        )}
        <Button onClick={() => setSystem(false)}>
          <X size={15} />
          Đóng
        </Button>
      </Modal>
    </>
  );
}
