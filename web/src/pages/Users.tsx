import { useState } from "react";
import { Link } from "react-router-dom";
import { Plus, Users as UsersIcon, Fingerprint } from "lucide-react";
import { previewFixtures as fixtures } from "../lib/fixtures";
const { users } = fixtures;
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
  Unavailable,
} from "../components/ui";
export default function Users() {
  const [q, setQ] = useState(""),
    [provider, setProvider] = useState(""),
    [role, setRole] = useState(""),
    [status, setStatus] = useState(""),
    [selected, setSelected] = useState("usr_op_02"),
    [create, setCreate] = useState(false);
  const data = users.filter(
      (u) =>
        [u.name, u.email, u.id]
          .join(" ")
          .toLowerCase()
          .includes(q.toLowerCase()) &&
        (!provider || u.provider === provider) &&
        (!role || u.role === role) &&
        (!status || String(u.active) === status),
    ),
    user = users.find((u) => u.id === selected)!;
  return (
    <>
      <Notice title="Quy tắc định danh & phân quyền:">
        Người dùng ứng dụng đăng nhập console; tài khoản Oracle thực thi DDL.
        API tokens được quản lý riêng tại{" "}
        <Link to="/access/service-accounts">Service Accounts</Link>.
      </Notice>
      <PageHeading
        title="Người dùng & Phân quyền truy cập (Scoped RBAC)"
        description="Kiểm soát người dùng nội bộ / ngoại vi, vai trò và quyền theo từng Database Target và Schema."
        breadcrumb="Quản trị hệ thống / Người dùng & Quyền"
        actions={
          <>
            <Link className="button secondary" to="/settings/authentication">
              Nhà cung cấp xác thực
            </Link>
            <Button variant="primary" onClick={() => setCreate(true)}>
              <Plus size={16} />
              Thêm người dùng mới
            </Button>
          </>
        }
      />
      <div className="filter-bar">
        <SearchBox
          value={q}
          onChange={setQ}
          placeholder="Tìm theo họ tên, username, email…"
        />
        <select
          value={provider}
          onChange={(e) => setProvider(e.target.value)}
          aria-label="Nguồn người dùng"
        >
          <option value="">Nguồn: Tất cả nhà cung cấp</option>
          {["Local", "LDAP", "OIDC"].map((p) => (
            <option key={p}>{p}</option>
          ))}
        </select>
        <select
          value={role}
          onChange={(e) => setRole(e.target.value)}
          aria-label="Vai trò"
        >
          <option value="">Tất cả vai trò</option>
          {["Admin", "Operator", "Viewer"].map((r) => (
            <option key={r}>{r}</option>
          ))}
        </select>
        <select
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          aria-label="Trạng thái người dùng"
        >
          <option value="">Tất cả trạng thái</option>
          <option value="true">Hoạt động</option>
          <option value="false">Vô hiệu hóa</option>
        </select>
        <Button
          variant="ghost"
          onClick={() => {
            setQ("");
            setProvider("");
            setRole("");
            setStatus("");
          }}
        >
          Đặt lại bộ lọc
        </Button>
      </div>
      <div className="layout-editor">
        <Panel
          title={
            <>
              <UsersIcon size={17} />
              Danh sách tài khoản quản trị & vận hành
            </>
          }
          aside={<small className="muted">Chọn một dòng để chỉnh sửa</small>}
        >
          <div className="table-scroll">
            <table className="user-table">
              <thead>
                <tr>
                  <th>Tên & Định danh</th>
                  <th>Phương thức & Binding</th>
                  <th>Vai trò</th>
                  <th>Trạng thái</th>
                  <th>Thao tác</th>
                </tr>
              </thead>
              <tbody>
                {data.map((u) => (
                  <tr
                    key={u.id}
                    className={selected === u.id ? "selected" : ""}
                  >
                    <td>
                      <strong>{u.name}</strong>
                      <small className="mono block muted">{u.id}</small>
                    </td>
                    <td>
                      <Badge
                        tone={u.provider === "LDAP" ? "accent" : "neutral"}
                      >
                        {u.provider}
                      </Badge>
                      <small className="block mono muted">
                        {u.provider === "LDAP"
                          ? "corp-ad"
                          : u.provider === "OIDC"
                            ? "issuer + sub"
                            : u.subject}
                      </small>
                    </td>
                    <td>
                      <Badge
                        tone={
                          u.role === "Admin"
                            ? "success"
                            : u.role === "Operator"
                              ? "accent"
                              : "neutral"
                        }
                      >
                        {u.role}
                      </Badge>
                    </td>
                    <td>
                      <Badge tone={u.active ? "success" : "error"}>
                        {u.active ? "Hoạt động" : "Vô hiệu hóa"}
                      </Badge>
                    </td>
                    <td>
                      <Button variant="ghost" onClick={() => setSelected(u.id)}>
                        {selected === u.id ? "Đang chỉnh sửa" : "Sửa quyền"}
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {!data.length && (
            <p className="empty">Không có người dùng phù hợp.</p>
          )}
          <div className="panel-footer">
            {data.length} người dùng minh họa · Không có tài khoản thực
          </div>
        </Panel>
        <UserEditor key={user.id} user={user} />
      </div>
      <Modal
        open={create}
        onOpenChange={setCreate}
        title="Thêm người dùng cục bộ"
        description="Bản nháp thông tin người dùng. Chưa có API quản lý danh tính."
      >
        <form onSubmit={(e) => e.preventDefault()}>
          <Field label="Họ và tên" required>
            <input placeholder="Họ và tên" />
          </Field>
          <Field label="Username" required>
            <input autoComplete="off" placeholder="operator_user" />
          </Field>
          <Field label="Email">
            <input type="email" placeholder="operator@organization.vn" />
          </Field>
          <Field label="Vai trò cơ sở">
            <select defaultValue="Viewer">
              <option>Viewer</option>
              <option>Operator</option>
              <option>Admin</option>
            </select>
          </Field>
          <Notice>
            Danh tính mới không tự động nhận quyền chạy migration. Quyền
            target/schema và PROD cần được cấp rõ ràng.
          </Notice>
          <Unavailable label="Tạo người dùng" />
        </form>
      </Modal>
    </>
  );
}
function UserEditor({ user }: { user: (typeof users)[number] }) {
  const [role, setRole] = useState(user.role),
    [disable, setDisable] = useState(false);
  return (
    <Panel
      title={
        <>
          <div className="user-avatar">
            {user.name
              .split(" ")
              .slice(-2)
              .map((s) => s[0])
              .join("")}
          </div>
          {user.name}
          <Badge>{user.id}</Badge>
        </>
      }
    >
      <div className="inset">
        <h3>
          <Fingerprint size={16} />
          Ràng buộc định danh bất biến
        </h3>
        <p className="small">
          Provider:{" "}
          <code>
            {user.provider === "LDAP"
              ? "corp-ad"
              : user.provider === "OIDC"
                ? "https://iam.bank.internal"
                : "local"}
          </code>
        </p>
        <p className="small break-word">
          {user.provider === "LDAP"
            ? "objectGUID"
            : user.provider === "OIDC"
              ? "issuer + sub"
              : "Subject"}
          : <code>{user.subject}</code>
        </p>
        <p className="small muted">
          DN và email là thuộc tính có thể thay đổi. Khóa định danh không dựa
          trên email.
        </p>
      </div>
      <Field label="Vai trò cơ sở (Base Role)">
        <select value={role} onChange={(e) => setRole(e.target.value)}>
          <option>Admin</option>
          <option>Operator</option>
          <option>Viewer</option>
        </select>
      </Field>
      <p className="small muted">
        Admin quản trị ứng dụng nhưng không tự động được chạy PROD.
      </p>
      <ScopeEditor key={role} viewer={role === "Viewer"} />
      <div className="actions">
        <Button variant="danger" onClick={() => setDisable(true)}>
          {user.active ? "Vô hiệu hóa người dùng" : "Kích hoạt người dùng"}
        </Button>
        <Button onClick={() => setRole(user.role)}>Hủy bản nháp vai trò</Button>
      </div>
      <Unavailable label="Lưu quyền truy cập" />
      <Modal
        open={disable}
        onOpenChange={setDisable}
        title={`${user.active ? "Vô hiệu hóa" : "Kích hoạt"} ${user.name}?`}
        description="Thay đổi sẽ ảnh hưởng quyền đăng nhập và truy cập console của người dùng này."
      >
        <Notice tone="warning">
          Chưa có API quản lý người dùng trong Phase 1. Không thể áp dụng thay
          đổi này.
        </Notice>
        <div className="dialog-actions">
          <Button onClick={() => setDisable(false)}>Hủy</Button>
          <Button variant="danger" disabled>
            Xác nhận thay đổi
          </Button>
        </div>
      </Modal>
    </Panel>
  );
}
