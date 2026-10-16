import { useMemo, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Users as UsersIcon } from "lucide-react";
import ScopeEditor from "../components/ScopeEditor";
import {
  api,
  apiList,
  ApiError,
  type Grant,
  type Provider,
  type Role,
  type User,
  type AuditEvent,
} from "../lib/contracts";
import { useIdentity } from "../lib/identity-context";
import {
  Panel,
  PageHeading,
  Button,
  Badge,
  Notice,
  SearchBox,
  Modal,
  Field,
} from "../components/ui";

export default function Users() {
  const identity = useIdentity();
  const queryClient = useQueryClient();
  const [q, setQ] = useState("");
  const [provider, setProvider] = useState("");
  const [role, setRole] = useState("");
  const [status, setStatus] = useState("");
  const [selectedID, setSelectedID] = useState("");
  const [create, setCreate] = useState(false);
  const users = useQuery({
    queryKey: ["users"],
    queryFn: () => apiList<User>("/access/users"),
  });
  const records = useMemo(() => users.data ?? [], [users.data]);
  const filtered = useMemo(
    () =>
      records.filter(
        (user) =>
          [user.display_name, user.username, user.id]
            .join(" ")
            .toLowerCase()
            .includes(q.toLowerCase()) &&
          (!provider || user.provider === provider) &&
          (!role || user.role === role) &&
          (!status || String(user.active) === status),
      ),
    [records, q, provider, role, status],
  );
  const selected =
    filtered.find((user) => user.id === selectedID) ?? filtered[0];
  const canManage = identity.user.role === "Admin";
  return (
    <>
      <Notice title="Identity and authorization">
        Application identities authenticate to this console. Oracle execution
        accounts are managed separately. API tokens are managed in{" "}
        <Link to="/access/service-accounts">Service Accounts</Link>.
      </Notice>
      <PageHeading
        title="Người dùng & Quyền truy cập"
        description="Manage local and externally bound identities, base roles, and database/schema grants."
        breadcrumb="Quản trị hệ thống / Người dùng & Quyền"
        actions={
          <>
            <Link className="button secondary" to="/settings/authentication">
              Nhà cung cấp xác thực
            </Link>
            <Button
              variant="primary"
              disabled={!canManage}
              onClick={() => setCreate(true)}
            >
              <Plus size={16} />
              Thêm người dùng
            </Button>
          </>
        }
      />
      {!canManage && (
        <Notice tone="warning">
          Your role can view identity details only. User administration requires
          Admin.
        </Notice>
      )}
      <div className="filter-bar">
        <SearchBox
          value={q}
          onChange={setQ}
          placeholder="Tìm theo tên, username hoặc ID…"
        />
        <select
          value={provider}
          onChange={(e) => setProvider(e.target.value)}
          aria-label="Nguồn người dùng"
        >
          <option value="">All providers</option>
          <option value="local">Local</option>
          <option value="ldap">LDAP</option>
          <option value="oidc">OIDC</option>
        </select>
        <select
          value={role}
          onChange={(e) => setRole(e.target.value)}
          aria-label="Vai trò"
        >
          <option value="">All roles</option>
          {(["Admin", "Operator", "Viewer"] as Role[]).map((item) => (
            <option key={item}>{item}</option>
          ))}
        </select>
        <select
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          aria-label="Trạng thái"
        >
          <option value="">All statuses</option>
          <option value="true">Active</option>
          <option value="false">Disabled</option>
        </select>
      </div>
      <div className="layout-editor">
        <Panel
          title={
            <>
              <UsersIcon size={17} />
              Danh sách người dùng
            </>
          }
          aside={
            <small className="muted">
              {users.isSuccess ? `${records.length} accounts` : ""}
            </small>
          }
        >
          {users.isPending ? (
            <div
              className="skeleton"
              role="status"
              aria-label="Loading users"
            />
          ) : users.isError ? (
            <Notice tone="error">
              {users.error instanceof ApiError && users.error.status === 403
                ? "Access denied. User administration requires Admin."
                : users.error.message}
              <Button onClick={() => void users.refetch()}>Retry</Button>
            </Notice>
          ) : filtered.length === 0 ? (
            <p className="empty">
              {records.length
                ? "No users match these filters."
                : "No users have been added."}
            </p>
          ) : (
            <div className="table-scroll">
              <table className="user-table">
                <thead>
                  <tr>
                    <th>Name & identity</th>
                    <th>Provider</th>
                    <th>Role</th>
                    <th>Status</th>
                    <th>Action</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map((user) => (
                    <tr
                      key={user.id}
                      className={selected?.id === user.id ? "selected" : ""}
                    >
                      <td>
                        <strong>{user.display_name}</strong>
                        <small className="mono block muted">
                          {user.username} · {user.id}
                        </small>
                      </td>
                      <td>
                        <Badge
                          tone={
                            user.provider === "local" ? "neutral" : "accent"
                          }
                        >
                          {user.provider.toUpperCase()}
                        </Badge>
                        <small className="mono block muted break-word">
                          {user.issuer || "Local account"}
                        </small>
                      </td>
                      <td>
                        <Badge
                          tone={
                            user.role === "Admin"
                              ? "success"
                              : user.role === "Operator"
                                ? "accent"
                                : "neutral"
                          }
                        >
                          {user.role}
                        </Badge>
                      </td>
                      <td>
                        <Badge tone={user.active ? "success" : "error"}>
                          {user.active ? "Active" : "Disabled"}
                        </Badge>
                      </td>
                      <td>
                        <Button
                          variant="ghost"
                          onClick={() => setSelectedID(user.id)}
                        >
                          Manage
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Panel>
        {selected && (
          <UserEditor key={selected.id} user={selected} canManage={canManage} />
        )}
      </div>
      {canManage && <AuditHistory />}
      <CreateUser
        open={create}
        onOpenChange={setCreate}
        onCreated={(user) => {
          setSelectedID(user.id);
          setCreate(false);
          void queryClient.invalidateQueries({ queryKey: ["users"] });
        }}
      />
    </>
  );
}
function UserEditor({ user, canManage }: { user: User; canManage: boolean }) {
  const queryClient = useQueryClient();
  const [displayName, setDisplayName] = useState(user.display_name);
  const [role, setRole] = useState<Role>(user.role);
  const [active, setActive] = useState(user.active);
  const [confirm, setConfirm] = useState(false);
  const grants = useQuery({
    queryKey: ["user-grants", user.id],
    queryFn: () =>
      api<Grant[]>(`/access/users/${encodeURIComponent(user.id)}/grants`),
  });
  const saveUser = useMutation({
    mutationFn: (nextActive?: boolean) =>
      api<User>(`/access/users/${encodeURIComponent(user.id)}`, {
        method: "PUT",
        body: JSON.stringify({
          display_name: displayName,
          role,
          active: nextActive ?? active,
        }),
      }),
    onSuccess: (updated) => {
      setActive(updated.active);
      setConfirm(false);
      void queryClient.invalidateQueries({ queryKey: ["users"] });
    },
  });
  const saveGrants = useMutation({
    mutationFn: (items: Grant[]) =>
      api<Grant[]>(`/access/users/${encodeURIComponent(user.id)}/grants`, {
        method: "PUT",
        body: JSON.stringify({ grants: items }),
      }),
    onSuccess: () =>
      void queryClient.invalidateQueries({
        queryKey: ["user-grants", user.id],
      }),
  });
  return (
    <Panel
      title={
        <>
          {user.display_name}
          <Badge>{user.id}</Badge>
        </>
      }
    >
      <div className="inset">
        <h3>Stable identity binding</h3>
        <p className="small">
          Provider: <code>{user.provider}</code>
        </p>
        <p className="small break-word">
          Issuer / subject:{" "}
          <code>
            {user.issuer || "—"} / {user.subject || "—"}
          </code>
        </p>
      </div>
      <Field label="Display name" required>
        <input
          value={displayName}
          maxLength={120}
          onChange={(e) => setDisplayName(e.target.value)}
          disabled={!canManage}
        />
      </Field>
      <Field label="Base role">
        <select
          value={role}
          onChange={(e) => setRole(e.target.value as Role)}
          disabled={!canManage}
        >
          {["Admin", "Operator", "Viewer"].map((item) => (
            <option key={item}>{item}</option>
          ))}
        </select>
      </Field>
      <div className="spread wrap inset">
        <span>Account status</span>
        <Badge tone={active ? "success" : "error"}>
          {active ? "Active" : "Disabled"}
        </Badge>
        <Button
          variant={active ? "danger" : "secondary"}
          disabled={!canManage}
          onClick={() => setConfirm(true)}
        >
          {active ? "Disable user" : "Activate user"}
        </Button>
      </div>
      <ScopeEditor
        value={grants.data ?? []}
        disabled={!canManage || grants.isPending || saveGrants.isPending}
        onChange={(items) => saveGrants.mutate(items)}
      />
      {grants.isError && (
        <Notice tone="error">
          {grants.error instanceof ApiError && grants.error.status === 403
            ? "Access denied to these grants."
            : grants.error.message}
        </Notice>
      )}
      {saveGrants.isError && (
        <Notice tone="error">
          Could not save grants: {saveGrants.error.message}
        </Notice>
      )}
      {saveUser.isError && (
        <Notice tone="error">
          Could not save user: {saveUser.error.message}
        </Notice>
      )}
      <Button
        variant="primary"
        disabled={!canManage || saveUser.isPending || !displayName.trim()}
        onClick={() => saveUser.mutate()}
      >
        {saveUser.isPending ? "Saving…" : "Save user"}
      </Button>
      <Modal
        open={confirm}
        onOpenChange={setConfirm}
        title={`${active ? "Disable" : "Activate"} ${user.display_name}?`}
        description="Changing account status may revoke active sessions and affect access."
      >
        <Notice tone="warning">
          {active
            ? "This user will no longer be able to log in."
            : "The user will be able to log in with the existing provider binding."}
        </Notice>
        {saveUser.isError && (
          <Notice tone="error">{saveUser.error.message}</Notice>
        )}
        <div className="dialog-actions">
          <Button onClick={() => setConfirm(false)}>Cancel</Button>
          <Button
            variant="danger"
            disabled={saveUser.isPending}
            onClick={() => saveUser.mutate(!active)}
          >
            {saveUser.isPending
              ? "Saving…"
              : active
                ? "Disable user"
                : "Activate user"}
          </Button>
        </div>
      </Modal>
    </Panel>
  );
}
function CreateUser({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (user: User) => void;
}) {
  const [provider, setProvider] = useState<Provider>("local");
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [issuer, setIssuer] = useState("");
  const [subject, setSubject] = useState("");
  const [role, setRole] = useState<Role>("Viewer");
  const create = useMutation({
    mutationFn: () =>
      api<User>("/access/users", {
        method: "POST",
        body: JSON.stringify(
          provider === "local"
            ? { provider, username, display_name: displayName, password, role }
            : {
                provider,
                username,
                display_name: displayName,
                issuer,
                subject,
                role,
              },
        ),
      }),
    onSuccess: (user) => {
      setPassword("");
      onCreated(user);
    },
  });
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Add user"
      description="Local accounts have an application password. External identities must be explicitly bound by issuer and subject."
    >
      <form
        onSubmit={(event: FormEvent) => {
          event.preventDefault();
          create.mutate();
        }}
      >
        <Field label="Provider">
          <select
            value={provider}
            onChange={(event) => setProvider(event.target.value as Provider)}
          >
            <option value="local">Local</option>
            <option value="ldap">LDAP</option>
            <option value="oidc">OIDC</option>
          </select>
        </Field>
        <Field label="Username" required>
          <input
            required
            minLength={3}
            maxLength={128}
            value={username}
            onChange={(event) => setUsername(event.target.value)}
          />
        </Field>
        <Field label="Display name" required>
          <input
            required
            maxLength={120}
            value={displayName}
            onChange={(event) => setDisplayName(event.target.value)}
          />
        </Field>
        {provider === "local" ? (
          <Field label="Initial password" required>
            <input
              required
              minLength={12}
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </Field>
        ) : (
          <>
            <Field label="Issuer / provider identifier" required>
              <input
                required
                value={issuer}
                onChange={(event) => setIssuer(event.target.value)}
              />
            </Field>
            <Field label="Immutable subject" required>
              <input
                required
                value={subject}
                onChange={(event) => setSubject(event.target.value)}
              />
            </Field>
          </>
        )}
        <Field label="Base role">
          <select
            value={role}
            onChange={(event) => setRole(event.target.value as Role)}
          >
            <option>Viewer</option>
            <option>Operator</option>
            <option>Admin</option>
          </select>
        </Field>
        {create.isError && <Notice tone="error">{create.error.message}</Notice>}
        <div className="dialog-actions">
          <Button type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={create.isPending}>
            {create.isPending ? "Creating…" : "Create user"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}

function AuditHistory() {
  const [offset, setOffset] = useState(0);
  const [action, setAction] = useState("");
  const pageSize = 20;
  const events = useQuery({
    queryKey: ["audit", offset, action],
    queryFn: () =>
      api<AuditEvent[]>(
        `/access/audit?limit=${pageSize}&offset=${offset}&action=${encodeURIComponent(action)}`,
      ),
  });
  return (
    <Panel
      title="Security & administration audit"
      aside={
        <Button onClick={() => void events.refetch()}>Refresh audit</Button>
      }
    >
      <div className="filter-bar">
        <SearchBox
          label="Audit action filter"
          placeholder="Exact action, e.g. permissions.update"
          value={action}
          onChange={(value) => {
            setAction(value);
            setOffset(0);
          }}
        />
      </div>
      {events.isPending ? (
        <div
          className="skeleton"
          role="status"
          aria-label="Loading audit history"
        />
      ) : events.isError ? (
        <Notice tone="error">
          {events.error.message}
          <Button onClick={() => void events.refetch()}>Retry</Button>
        </Notice>
      ) : events.data.length === 0 ? (
        <p className="empty">No audit events match this filter.</p>
      ) : (
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th>Time</th>
                <th>Actor</th>
                <th>Action / target</th>
                <th>Outcome</th>
                <th>Request ID</th>
              </tr>
            </thead>
            <tbody>
              {events.data.map((event) => (
                <tr key={event.id}>
                  <td className="small">
                    {new Date(event.timestamp).toLocaleString()}
                  </td>
                  <td>
                    {event.actor_name || "Anonymous"}
                    <small className="muted block">{event.actor_type}</small>
                  </td>
                  <td>
                    <code>{event.action}</code>
                    <small className="muted block break-word">
                      {event.target}
                    </small>
                  </td>
                  <td>
                    <Badge
                      tone={event.outcome === "success" ? "success" : "error"}
                    >
                      {event.outcome}
                    </Badge>
                  </td>
                  <td className="small mono">{event.request_id}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="spread">
        <Button
          disabled={offset === 0 || events.isPending}
          onClick={() => setOffset(Math.max(0, offset - pageSize))}
        >
          Previous audit page
        </Button>
        <span className="small muted">
          Page {Math.floor(offset / pageSize) + 1}
        </span>
        <Button
          disabled={
            !events.data || events.data.length < pageSize || events.isPending
          }
          onClick={() => setOffset(offset + pageSize)}
        >
          Next audit page
        </Button>
      </div>
    </Panel>
  );
}
