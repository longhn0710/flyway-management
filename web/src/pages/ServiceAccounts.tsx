import { useEffect, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, Plus, Copy, ShieldCheck } from "lucide-react";
import ScopeEditor from "../components/ScopeEditor";
import {
  api,
  apiList,
  apiV2List,
  ApiError,
  type ApiToken,
  type Grant,
  type ServiceAccount,
  type TokenCreated,
  type TokenDeploymentScope,
  type TokenInput,
  type User,
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

export default function ServiceAccounts() {
  const identity = useIdentity();
  const admin = identity.user.role === "Admin";
  const queryClient = useQueryClient();
  const [q, setQ] = useState("");
  const [selectedID, setSelectedID] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [action, setAction] = useState<{
    kind: "rotate" | "revoke";
    token: ApiToken;
  } | null>(null);
  const [oneTime, setOneTime] = useState<TokenCreated | null>(null);
  const [rotationExpiry, setRotationExpiry] = useState(() =>
    new Date(Date.now() + 30 * 86400000).toISOString().slice(0, 16),
  );
  const accounts = useQuery({
    queryKey: ["service-accounts"],
    queryFn: () => apiList<ServiceAccount>("/access/service-accounts"),
  });
  const filtered = (accounts.data ?? []).filter((item) =>
    [item.name, item.purpose, item.id]
      .join(" ")
      .toLowerCase()
      .includes(q.toLowerCase()),
  );
  const selected =
    filtered.find((item) => item.id === selectedID) ?? filtered[0];
  const accountTokens = useQuery({
    queryKey: ["service-tokens", selected?.id],
    queryFn: () =>
      api<ApiToken[]>(
        `/access/service-accounts/${encodeURIComponent(selected!.id)}/tokens`,
      ),
    enabled: !!selected,
  });
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["service-accounts"] });
    if (selected)
      void queryClient.invalidateQueries({
        queryKey: ["service-tokens", selected.id],
      });
  };
  const revoke = useMutation({
    mutationFn: (token: ApiToken) =>
      api<void>(
        `/access/service-accounts/${encodeURIComponent(selected!.id)}/tokens/${encodeURIComponent(token.id)}`,
        { method: "DELETE" },
      ),
    onSuccess: () => {
      setAction(null);
      invalidate();
    },
  });
  const rotate = useMutation({
    gcTime: 0,
    mutationFn: (token: ApiToken) =>
      api<TokenCreated>(
        `/access/service-accounts/${encodeURIComponent(selected!.id)}/tokens/${encodeURIComponent(token.id)}/rotate`,
        {
          method: "POST",
          body: JSON.stringify({
            expires_at: new Date(rotationExpiry).toISOString(),
          }),
        },
      ),
    onSuccess: (value) => {
      setAction(null);
      setOneTime(value);
      rotate.reset();
      invalidate();
    },
  });
  return (
    <>
      <PageHeading
        title="Service Accounts & API Tokens"
        description="Manage automation identities, explicit scopes, and revocable API tokens."
        breadcrumb="System administration / Service Accounts"
        actions={
          <Button
            variant="primary"
            disabled={!admin}
            onClick={() => setCreateOpen(true)}
          >
            <Plus size={16} />
            Create service account
          </Button>
        }
      />
      {!admin && (
        <Notice tone="warning">
          Read-only view. Service account and token administration requires
          Admin.
        </Notice>
      )}
      <div className="layout-editor">
        <Panel
          title="Service account directory"
          aside={
            <Badge>
              {accounts.isSuccess ? `${accounts.data.length} accounts` : ""}
            </Badge>
          }
        >
          <SearchBox
            value={q}
            onChange={setQ}
            placeholder="Search name, purpose or ID…"
          />
          {accounts.isPending ? (
            <div
              className="skeleton"
              role="status"
              aria-label="Loading service accounts"
            />
          ) : accounts.isError ? (
            <Notice tone="error">
              {accounts.error instanceof ApiError &&
              accounts.error.status === 403
                ? "Access denied to service accounts."
                : accounts.error.message}
              <Button onClick={() => void accounts.refetch()}>Retry</Button>
            </Notice>
          ) : filtered.length === 0 ? (
            <p className="empty">
              {accounts.data.length
                ? "No service accounts match."
                : "No service accounts have been registered."}
            </p>
          ) : (
            <div className="service-list">
              {filtered.map((account) => (
                <button
                  className={`service-row ${selected?.id === account.id ? "selected" : ""}`}
                  key={account.id}
                  onClick={() => setSelectedID(account.id)}
                >
                  <div className="spread">
                    <strong className="mono">
                      <Bot size={17} />
                      {account.name}
                    </strong>
                    <Badge tone={account.active ? "success" : "error"}>
                      {account.active ? "Active" : "Disabled"}
                    </Badge>
                  </div>
                  <p>{account.purpose}</p>
                  <small className="muted block">
                    Owner ID: {account.owner_id}
                  </small>
                  <small className="mono muted block">{account.id}</small>
                </button>
              ))}
            </div>
          )}
        </Panel>
        {selected && (
          <ServiceDetail
            key={selected.id}
            account={selected}
            canManage={admin}
            tokens={accountTokens.data ?? []}
            tokensLoading={accountTokens.isPending}
            tokensError={accountTokens.error}
            onToken={setOneTime}
            onAction={(kind, token) => setAction({ kind, token })}
          />
        )}
      </div>
      <CreateAccount
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(account) => {
          setCreateOpen(false);
          setSelectedID(account.id);
          invalidate();
        }}
      />
      {selected && (
        <Modal
          open={!!action}
          onOpenChange={(open) => {
            if (!open) setAction(null);
          }}
          title={`${action?.kind === "rotate" ? "Rotate" : "Revoke"} ${action?.token.name}?`}
          description={
            action?.kind === "rotate"
              ? "The old token will be revoked when its replacement is created."
              : "Clients using this token will lose API access immediately."
          }
        >
          <Notice tone="warning">
            {action?.kind === "rotate"
              ? "Copy the replacement token now. The plaintext is shown once."
              : "This action cannot be undone."}
          </Notice>
          {action?.kind === "rotate" && (
            <Field label="Replacement expiration" required>
              <input
                type="datetime-local"
                min={new Date().toISOString().slice(0, 16)}
                value={rotationExpiry}
                onChange={(event) => setRotationExpiry(event.target.value)}
              />
            </Field>
          )}
          {(revoke.isError || rotate.isError) && (
            <Notice tone="error">
              {(revoke.error ?? rotate.error)?.message}
            </Notice>
          )}
          <div className="dialog-actions">
            <Button onClick={() => setAction(null)}>Cancel</Button>
            <Button
              variant="danger"
              disabled={
                revoke.isPending ||
                rotate.isPending ||
                (action?.kind === "rotate" && !rotationExpiry)
              }
              onClick={() =>
                action?.kind === "rotate"
                  ? rotate.mutate(action.token)
                  : action && revoke.mutate(action.token)
              }
            >
              {action?.kind === "rotate" ? "Rotate token" : "Revoke token"}
            </Button>
          </div>
        </Modal>
      )}
      <TokenOnce value={oneTime} onClose={() => setOneTime(null)} />
    </>
  );
}
function ServiceDetail({
  account,
  canManage,
  tokens,
  tokensLoading,
  tokensError,
  onToken,
  onAction,
}: {
  account: ServiceAccount;
  canManage: boolean;
  tokens: ApiToken[];
  tokensLoading: boolean;
  tokensError: Error | null;
  onToken: (token: TokenCreated) => void;
  onAction: (kind: "rotate" | "revoke", token: ApiToken) => void;
}) {
  const queryClient = useQueryClient();
  const [clock, setClock] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setClock(Date.now()), 30000);
    return () => window.clearInterval(timer);
  }, []);
  const [name, setName] = useState(account.name);
  const [purpose, setPurpose] = useState(account.purpose);
  const [ownerID, setOwnerID] = useState(account.owner_id);
  const [active, setActive] = useState(account.active);
  const [confirmDisable, setConfirmDisable] = useState(false);
  const [tokenName, setTokenName] = useState("");
  const [expiry, setExpiry] = useState("");
  const [legacyToken, setLegacyToken] = useState(false);
  const [deploymentScopes, setDeploymentScopes] = useState<
    Record<string, Omit<TokenDeploymentScope, "deployment_target_id">>
  >({});
  const [minExpiry] = useState(() =>
    new Date(Date.now() + 60000).toISOString().slice(0, 16),
  );
  const users = useQuery({
    queryKey: ["users"],
    queryFn: () => apiList<User>("/access/users"),
  });
  const grants = useQuery({
    queryKey: ["service-grants", account.id],
    queryFn: () =>
      api<Grant[]>(
        `/access/service-accounts/${encodeURIComponent(account.id)}/grants`,
      ),
  });
  const projectTargets = useQuery({
    queryKey: ["token-deployment-targets"],
    enabled: canManage,
    queryFn: async () => {
      const projects = await apiV2List<{ id: string; name: string }>(
        "/projects",
      );
      const groups = await Promise.all(
        projects.map(async (project) => ({
          project,
          targets: await apiV2List<{
            id: string;
            name: string;
            environment: string;
            target_id: string;
            schema_id: string;
            database_name?: string;
            schema_name?: string;
            can_read: boolean;
            can_upload: boolean;
            can_preflight: boolean;
            can_execute: boolean;
          }>(`/projects/${encodeURIComponent(project.id)}/deployment-targets`),
        })),
      );
      return groups.flatMap(({ project, targets }) =>
        targets.map((target) => ({ project, target })),
      );
    },
  });
  const update = useMutation({
    mutationFn: () =>
      api<ServiceAccount>(
        `/access/service-accounts/${encodeURIComponent(account.id)}`,
        {
          method: "PUT",
          body: JSON.stringify({ name, purpose, owner_id: ownerID, active }),
        },
      ),
    onSuccess: () =>
      void queryClient.invalidateQueries({ queryKey: ["service-accounts"] }),
  });
  const saveGrants = useMutation({
    mutationFn: (items: Grant[]) =>
      api<Grant[]>(
        `/access/service-accounts/${encodeURIComponent(account.id)}/grants`,
        { method: "PUT", body: JSON.stringify({ grants: items }) },
      ),
    onSuccess: () =>
      void queryClient.invalidateQueries({
        queryKey: ["service-grants", account.id],
      }),
  });
  const createToken = useMutation({
    gcTime: 0,
    mutationFn: () => {
      const input: TokenInput = {
        name: tokenName,
        expires_at: new Date(expiry).toISOString(),
        deployment_scopes: legacyToken
          ? undefined
          : Object.entries(deploymentScopes)
              .filter(
                ([, scope]) =>
                  scope.read ||
                  scope.upload ||
                  scope.preflight ||
                  scope.execute,
              )
              .map(([deployment_target_id, scope]) => ({
                deployment_target_id,
                ...scope,
              })),
      };
      return api<TokenCreated>(
        `/access/service-accounts/${encodeURIComponent(account.id)}/tokens`,
        { method: "POST", body: JSON.stringify(input) },
      );
    },
    onSuccess: (value) => {
      setTokenName("");
      setExpiry("");
      onToken(value);
      createToken.reset();
      void queryClient.invalidateQueries({
        queryKey: ["service-tokens", account.id],
      });
    },
  });
  const hasDeploymentScope = Object.values(deploymentScopes).some(
    (scope) => scope.read || scope.upload || scope.preflight || scope.execute,
  );
  return (
    <Panel
      title={
        <>
          <Bot size={17} />
          <code>{account.name}</code>
        </>
      }
    >
      <Field label="Service account name" required>
        <input
          required
          maxLength={120}
          value={name}
          disabled={!canManage}
          onChange={(event) => setName(event.target.value)}
        />
      </Field>
      <Field label="Purpose" required>
        <input
          required
          maxLength={500}
          value={purpose}
          disabled={!canManage}
          onChange={(event) => setPurpose(event.target.value)}
        />
      </Field>
      <Field label="Owner" required>
        <select
          required
          value={ownerID}
          disabled={!canManage || users.isPending}
          onChange={(event) => setOwnerID(event.target.value)}
        >
          <option value="">Choose owner</option>
          {(users.data ?? []).map((user) => (
            <option key={user.id} value={user.id}>
              {user.display_name} ({user.username})
            </option>
          ))}
        </select>
      </Field>
      <label className="check">
        <input
          type="checkbox"
          checked={active}
          disabled={!canManage}
          onChange={(event) => setActive(event.target.checked)}
        />
        Account enabled
      </label>
      <Button
        variant="primary"
        disabled={
          !canManage ||
          update.isPending ||
          !name.trim() ||
          !purpose.trim() ||
          !ownerID
        }
        onClick={() =>
          account.active && !active ? setConfirmDisable(true) : update.mutate()
        }
      >
        Save account
      </Button>
      {update.isError && <Notice tone="error">{update.error.message}</Notice>}
      <ScopeEditor
        value={grants.data ?? []}
        disabled={!canManage || grants.isPending || saveGrants.isPending}
        onChange={(items) => saveGrants.mutate(items)}
      />
      {grants.isError && <Notice tone="error">{grants.error.message}</Notice>}
      {saveGrants.isError && (
        <Notice tone="error">{saveGrants.error.message}</Notice>
      )}
      <Notice>
        <ShieldCheck size={15} />
        Service-account grants support read and operate. PROD access requires an
        explicit grant.
      </Notice>
      <Notice title="Combined authorization">
        Database/schema grants remain required for the service account. Each
        token’s selected Deployment Target actions further restrict that access,
        and PROD operation still requires its explicit database/schema grant.
      </Notice>
      <h3>API tokens</h3>
      <p className="small muted">
        Only token prefixes are visible after creation. Full token values are
        shown once.
      </p>
      {tokensLoading ? (
        <div className="skeleton" role="status" />
      ) : tokensError ? (
        <Notice tone="error">{tokensError.message}</Notice>
      ) : tokens.length === 0 ? (
        <p className="empty">No tokens registered.</p>
      ) : (
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Prefix</th>
                <th>Expires</th>
                <th>Last used</th>
                <th>Status</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {tokens.map((token) => (
                <tr key={token.id}>
                  <td>{token.name}</td>
                  <td>
                    <code>{token.prefix}</code>
                  </td>
                  <td>{new Date(token.expires_at).toLocaleString()}</td>
                  <td>
                    {token.last_used_at
                      ? new Date(token.last_used_at).toLocaleString()
                      : "Never"}
                  </td>
                  <td>
                    <Badge
                      tone={
                        token.revoked
                          ? "error"
                          : new Date(token.expires_at).getTime() <= clock
                            ? "warning"
                            : "success"
                      }
                    >
                      {token.revoked
                        ? "Revoked"
                        : new Date(token.expires_at).getTime() <= clock
                          ? "Expired"
                          : "Active"}
                    </Badge>
                  </td>
                  <td>
                    <div className="inline">
                      <Button
                        variant="secondary"
                        disabled={!canManage || token.revoked}
                        onClick={() => onAction("rotate", token)}
                      >
                        Rotate
                      </Button>
                      <Button
                        variant="danger"
                        disabled={!canManage || token.revoked}
                        onClick={() => onAction("revoke", token)}
                      >
                        Revoke
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <form
        onSubmit={(event: FormEvent) => {
          event.preventDefault();
          createToken.mutate();
        }}
      >
        <div className="grid-two">
          <Field label="Token name" required>
            <input
              required
              maxLength={120}
              value={tokenName}
              onChange={(event) => setTokenName(event.target.value)}
              disabled={!canManage}
            />
          </Field>
          <Field label="Expiration" required>
            <input
              type="datetime-local"
              required
              min={minExpiry}
              value={expiry}
              onChange={(event) => setExpiry(event.target.value)}
              disabled={!canManage}
            />
          </Field>
        </div>
        <label className="checkbox">
          <input
            type="checkbox"
            checked={legacyToken}
            onChange={(event) => setLegacyToken(event.target.checked)}
            disabled={!canManage}
          />
          Issue legacy token without project Deployment Target scopes
        </label>
        {!legacyToken && (
          <fieldset
            className="project-scope-list"
            disabled={
              !canManage || projectTargets.isPending || projectTargets.isError
            }
          >
            <legend>Deployment Target token scopes</legend>
            <p className="small muted">
              Select the actions this token may request. Unselected targets are
              inaccessible to this token.
            </p>
            {projectTargets.isPending ? (
              <div className="skeleton" role="status" />
            ) : projectTargets.isError ? (
              <Notice tone="error">
                Could not load project Deployment Targets:{" "}
                {projectTargets.error.message}
              </Notice>
            ) : projectTargets.data.length === 0 ? (
              <p className="small">
                No Deployment Targets are available. Choose the explicit legacy
                option if this token is intended for the existing v1 API.
              </p>
            ) : (
              projectTargets.data.map(({ project, target }) => {
                const selected = deploymentScopes[target.id] ?? {
                  read: false,
                  upload: false,
                  preflight: false,
                  execute: false,
                };
                return (
                  <div className="scope-row" key={target.id}>
                    <strong>
                      {project.name} · {target.name}
                    </strong>
                    <span className="small muted">
                      {target.environment} ·{" "}
                      {target.database_name || target.target_id} /{" "}
                      {target.schema_name || target.schema_id}
                    </span>
                    <div className="inline wrap">
                      {(
                        ["read", "upload", "preflight", "execute"] as const
                      ).map((action) => (
                        <label className="checkbox" key={action}>
                          <input
                            type="checkbox"
                            checked={selected[action]}
                            onChange={(event) =>
                              setDeploymentScopes((old) => {
                                const next = {
                                  ...selected,
                                  [action]: event.target.checked,
                                };
                                if (
                                  action !== "read" &&
                                  !next.read &&
                                  event.target.checked
                                )
                                  next.read = true;
                                if (
                                  action === "read" &&
                                  !event.target.checked
                                ) {
                                  next.upload = false;
                                  next.preflight = false;
                                  next.execute = false;
                                }
                                return { ...old, [target.id]: next };
                              })
                            }
                          />
                          {action}
                        </label>
                      ))}
                    </div>
                  </div>
                );
              })
            )}
          </fieldset>
        )}
        {createToken.isError && (
          <Notice tone="error">{createToken.error.message}</Notice>
        )}
        <Button
          type="submit"
          variant="primary"
          disabled={
            !canManage ||
            createToken.isPending ||
            !tokenName.trim() ||
            !expiry ||
            (!legacyToken &&
              (!hasDeploymentScope ||
                projectTargets.isPending ||
                projectTargets.isError))
          }
        >
          {createToken.isPending ? "Creating…" : "Create token"}
        </Button>
      </form>
      <Modal
        open={confirmDisable}
        onOpenChange={setConfirmDisable}
        title={`Disable ${account.name}?`}
        description="Disabling this service account prevents its tokens from being used."
      >
        <Notice tone="warning">
          Existing tokens are permanently revoked. Reactivating this account
          requires issuing new tokens.
        </Notice>
        {update.isError && <Notice tone="error">{update.error.message}</Notice>}
        <div className="dialog-actions">
          <Button onClick={() => setConfirmDisable(false)}>Keep enabled</Button>
          <Button
            variant="danger"
            disabled={update.isPending}
            onClick={() => {
              setConfirmDisable(false);
              update.mutate();
            }}
          >
            {update.isPending ? "Saving…" : "Disable service account"}
          </Button>
        </div>
      </Modal>
    </Panel>
  );
}
function CreateAccount({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (account: ServiceAccount) => void;
}) {
  const [name, setName] = useState("");
  const [purpose, setPurpose] = useState("");
  const [ownerID, setOwnerID] = useState("");
  const users = useQuery({
    queryKey: ["users"],
    queryFn: () => apiList<User>("/access/users"),
    enabled: open,
  });
  const create = useMutation({
    mutationFn: () =>
      api<ServiceAccount>("/access/service-accounts", {
        method: "POST",
        body: JSON.stringify({ name, purpose, owner_id: ownerID }),
      }),
    onSuccess: onCreated,
  });
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Create service account"
      description="A service account has scoped API permissions and does not create a web session."
    >
      <form
        onSubmit={(event: FormEvent) => {
          event.preventDefault();
          create.mutate();
        }}
      >
        <Field label="Identifier" required>
          <input
            required
            maxLength={120}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field label="Purpose" required>
          <input
            required
            maxLength={500}
            value={purpose}
            onChange={(event) => setPurpose(event.target.value)}
          />
        </Field>
        <Field label="Owner" required>
          <select
            required
            value={ownerID}
            onChange={(event) => setOwnerID(event.target.value)}
          >
            <option value="">Choose owner</option>
            {(users.data ?? []).map((user) => (
              <option key={user.id} value={user.id}>
                {user.display_name} ({user.username})
              </option>
            ))}
          </select>
        </Field>
        {users.isError && (
          <Notice tone="error">
            Could not load users: {users.error.message}
          </Notice>
        )}
        {create.isError && <Notice tone="error">{create.error.message}</Notice>}
        <div className="dialog-actions">
          <Button type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            variant="primary"
            disabled={
              create.isPending || !name.trim() || !purpose.trim() || !ownerID
            }
          >
            {create.isPending ? "Creating…" : "Create"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
function TokenOnce({
  value,
  onClose,
}: {
  value: TokenCreated | null;
  onClose: () => void;
}) {
  const [copied, setCopied] = useState(false);
  return (
    <Modal
      open={!!value}
      onOpenChange={(open) => {
        if (!open) {
          setCopied(false);
          onClose();
        }
      }}
      title="Copy your API token now"
      description="This plaintext token is shown only once. Closing this dialog discards it."
    >
      {value && (
        <>
          <Notice tone="warning">
            Store this token in your secret manager. It will not be available
            again.
          </Notice>
          <code className="token-example">{value.token}</code>
          <p className="small">
            Prefix: <code>{value.metadata.prefix}</code> · Expires{" "}
            {new Date(value.metadata.expires_at).toLocaleString()}
          </p>
          <div className="dialog-actions">
            <Button
              onClick={() => {
                void navigator.clipboard
                  .writeText(value.token)
                  .then(() => setCopied(true))
                  .catch(() => setCopied(false));
              }}
            >
              {copied ? (
                "Copied"
              ) : (
                <>
                  <Copy size={15} />
                  Copy token
                </>
              )}
            </Button>
            <Button
              variant="primary"
              onClick={() => {
                setCopied(false);
                onClose();
              }}
            >
              Done
            </Button>
          </div>
        </>
      )}
    </Modal>
  );
}
