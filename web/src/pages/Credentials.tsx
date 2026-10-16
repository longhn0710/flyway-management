import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus, ShieldCheck } from "lucide-react";
import {
  api,
  apiList,
  ApiError,
  type DatabaseTarget,
  type ExecutionAccount,
  type TargetSchema,
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
  Check,
} from "../components/ui";

export default function Credentials() {
  const identity = useIdentity();
  const admin = identity.user.role === "Admin";
  const queryClient = useQueryClient();
  const [targetID, setTargetID] = useState("");
  const [q, setQ] = useState("");
  const [selectedID, setSelectedID] = useState("");
  const [create, setCreate] = useState(false);
  const targets = useQuery({
    queryKey: ["database-targets"],
    queryFn: () => apiList<DatabaseTarget>("/database-targets"),
  });
  const activeTargets = (targets.data ?? []).filter((target) => target.active);
  const selectedTargetID = targetID || activeTargets[0]?.id || "";
  const accounts = useQuery({
    queryKey: ["execution-accounts", selectedTargetID],
    queryFn: () =>
      apiList<ExecutionAccount>(
        `/execution-accounts?target_id=${encodeURIComponent(selectedTargetID)}`,
      ),
    enabled: !!selectedTargetID,
  });
  const data = (accounts.data ?? []).filter((account) =>
    [account.name, account.username, account.id]
      .join(" ")
      .toLowerCase()
      .includes(q.toLowerCase()),
  );
  const selected = data.find((account) => account.id === selectedID) ?? data[0];
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["execution-accounts"] });
    void queryClient.invalidateQueries({ queryKey: ["schemas"] });
  };
  return (
    <>
      <PageHeading
        title="Oracle execution accounts"
        description="Register Oracle identities, map them to target schemas, and replace or revoke encrypted credentials."
        breadcrumb="Execution administration / Oracle accounts"
        actions={
          <>
            <Button disabled>Test connections · Unavailable</Button>
            <Button
              variant="primary"
              disabled={!admin || !selectedTargetID}
              onClick={() => setCreate(true)}
            >
              <Plus size={16} />
              Register Oracle account
            </Button>
          </>
        }
      />
      {!admin && (
        <Notice tone="warning">
          Execution account metadata and credentials are visible to Admin only.
        </Notice>
      )}
      <div className="filter-bar">
        <select
          aria-label="Database target"
          value={selectedTargetID}
          onChange={(event) => {
            setTargetID(event.target.value);
            setSelectedID("");
          }}
        >
          <option value="">Choose target</option>
          {activeTargets.map((target) => (
            <option value={target.id} key={target.id}>
              {target.name} · {target.environment}
            </option>
          ))}
        </select>
        <SearchBox
          value={q}
          onChange={setQ}
          placeholder="Search execution account…"
        />
      </div>
      {!selectedTargetID ? (
        targets.isPending ? (
          <div className="skeleton" role="status" />
        ) : targets.isError ? (
          <Notice tone="error">{targets.error.message}</Notice>
        ) : (
          <p className="empty">Register an active database target first.</p>
        )
      ) : (
        <div className="layout-editor">
          <Panel
            title="Execution accounts"
            aside={
              <Badge>
                {accounts.isSuccess ? `${accounts.data.length} accounts` : ""}
              </Badge>
            }
          >
            {accounts.isPending ? (
              <div
                className="skeleton"
                role="status"
                aria-label="Loading execution accounts"
              />
            ) : accounts.isError ? (
              <Notice tone="error">
                {accounts.error instanceof ApiError &&
                accounts.error.status === 403
                  ? "Execution account management requires Admin."
                  : accounts.error.message}
                <Button onClick={() => void accounts.refetch()}>Retry</Button>
              </Notice>
            ) : data.length === 0 ? (
              <p className="empty">
                {accounts.data.length
                  ? "No accounts match."
                  : "No execution accounts registered for this target."}
              </p>
            ) : (
              <div className="credential-list">
                {data.map((account) => (
                  <button
                    key={account.id}
                    className={`credential-row ${selected?.id === account.id ? "selected" : ""}`}
                    onClick={() => setSelectedID(account.id)}
                  >
                    <div>
                      <strong className="mono">
                        <KeyRound size={14} />
                        {account.name}
                      </strong>
                      <small className="mono muted block">
                        {account.username}
                      </small>
                    </div>
                    <div>
                      <Badge
                        tone={
                          account.credential.configured &&
                          !account.credential.revoked
                            ? "success"
                            : "warning"
                        }
                      >
                        {account.credential.revoked
                          ? "Revoked"
                          : account.credential.configured
                            ? "Encrypted credential"
                            : "No credential"}
                      </Badge>
                      <small className="block muted">
                        {account.credential.updated_at
                          ? `Updated ${new Date(account.credential.updated_at).toLocaleString()}`
                          : "No credential stored"}
                      </small>
                    </div>
                    <div className="stack tight">
                      {account.schema_ids.map((schemaID) => (
                        <Badge key={schemaID}>{schemaID}</Badge>
                      ))}
                    </div>
                    <Badge tone={account.active ? "success" : "error"}>
                      {account.active ? "Active" : "Disabled"}
                    </Badge>
                  </button>
                ))}
              </div>
            )}
          </Panel>
          {selected && (
            <CredentialEditor
              key={selected.id}
              account={selected}
              targetID={selectedTargetID}
              canManage={admin}
              onSaved={invalidate}
            />
          )}
        </div>
      )}
      <Notice title="Credential boundary">
        <ShieldCheck size={16} />
        Credential values are write-only, encrypted at rest, and never returned
        by the API. The application does not provision Oracle users.
      </Notice>
      <CreateExecutionAccount
        open={create}
        onOpenChange={setCreate}
        targetID={selectedTargetID}
        onCreated={(account) => {
          setCreate(false);
          setSelectedID(account.id);
          invalidate();
        }}
      />
    </>
  );
}
export function CredentialEditor({
  account,
  targetID,
  canManage,
  onSaved,
  lifecycleOnly = false,
}: {
  account: ExecutionAccount;
  targetID: string;
  canManage: boolean;
  onSaved: () => void;
  lifecycleOnly?: boolean;
}) {
  const [name, setName] = useState(account.name);
  const [username, setUsername] = useState(account.username);
  const [active, setActive] = useState(account.active);
  const [schemaIDs, setSchemaIDs] = useState(account.schema_ids);
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [confirmRevoke, setConfirmRevoke] = useState(false);
  const schemas = useQuery({
    queryKey: ["schemas", targetID],
    queryFn: () =>
      apiList<TargetSchema>(
        `/database-targets/${encodeURIComponent(targetID)}/schemas`,
      ),
  });
  const update = useMutation({
    mutationFn: () =>
      api<ExecutionAccount>(
        `/execution-accounts/${encodeURIComponent(account.id)}`,
        {
          method: "PUT",
          body: JSON.stringify({
            name,
            username,
            active,
            schema_ids: schemaIDs,
          }),
        },
      ),
    onSuccess: onSaved,
  });
  const replaceCredential = useMutation({
    mutationFn: () =>
      api<ExecutionAccount>(
        `/execution-accounts/${encodeURIComponent(account.id)}/credential`,
        { method: "PUT", body: JSON.stringify({ password }) },
      ),
    onSuccess: () => {
      setPassword("");
      setConfirmPassword("");
      onSaved();
    },
  });
  const revokeCredential = useMutation({
    mutationFn: () =>
      api<void>(
        `/execution-accounts/${encodeURIComponent(account.id)}/credential`,
        { method: "DELETE" },
      ),
    onSuccess: () => {
      setConfirmRevoke(false);
      onSaved();
    },
  });
  const toggleSchema = (id: string, checked: boolean) =>
    setSchemaIDs(
      checked ? [...schemaIDs, id] : schemaIDs.filter((item) => item !== id),
    );
  return (
    <Panel
      title={
        <>
          Update account: <code>{account.name}</code>
        </>
      }
    >
      <Field label="Oracle account name" required>
        <input
          required
          maxLength={120}
          value={name}
          disabled={!canManage}
          onChange={(event) => setName(event.target.value)}
        />
      </Field>
      <Field label="Oracle username" required>
        <input
          required
          maxLength={120}
          value={username}
          disabled={!canManage || lifecycleOnly}
          onChange={(event) => setUsername(event.target.value)}
        />
      </Field>
      <label className="check">
        <input
          type="checkbox"
          checked={active}
          disabled={!canManage}
          onChange={(event) => setActive(event.target.checked)}
        />
        Account active
      </label>
      <h3>Target schema mappings</h3>
      {schemas.isPending ? (
        <div className="skeleton" role="status" />
      ) : schemas.isError ? (
        <Notice tone="error">{schemas.error.message}</Notice>
      ) : schemas.data.filter((schema) => schema.active).length ? (
        schemas.data
          .filter((schema) => schema.active)
          .map((schema) => (
            <Check
              key={schema.id}
              label={
                <>
                  <code>{schema.name}</code>{" "}
                  <small className="muted">({schema.id})</small>
                </>
              }
              checked={schemaIDs.includes(schema.id)}
              disabled={!canManage || lifecycleOnly}
              onChange={(checked) => toggleSchema(schema.id, checked)}
            />
          ))
      ) : (
        <p className="empty">No active schemas for this target.</p>
      )}
      {update.isError && <Notice tone="error">{update.error.message}</Notice>}
      <Button
        variant="primary"
        disabled={
          !canManage ||
          update.isPending ||
          !name.trim() ||
          !username.trim() ||
          (!lifecycleOnly && schemaIDs.length === 0)
        }
        onClick={() => update.mutate()}
      >
        {lifecycleOnly ? "Save account" : "Save mappings"}
      </Button>
      {!lifecycleOnly && (
        <div className="inset">
          <h3>
            {account.credential.configured
              ? "Replace credential"
              : "Set credential"}{" "}
            <Badge>Write only</Badge>
          </h3>
          <Notice>
            No saved password can be retrieved. Enter a new Oracle credential to
            encrypt and replace it.
          </Notice>
          <Field label="New Oracle password" required>
            <input
              type="password"
              autoComplete="new-password"
              value={password}
              disabled={!canManage}
              onChange={(event) => setPassword(event.target.value)}
            />
          </Field>
          <Field label="Confirm new password" required>
            <input
              type="password"
              autoComplete="new-password"
              value={confirmPassword}
              disabled={!canManage}
              onChange={(event) => setConfirmPassword(event.target.value)}
            />
          </Field>
          {replaceCredential.isError && (
            <Notice tone="error">{replaceCredential.error.message}</Notice>
          )}
          <div className="actions">
            <Button
              variant="primary"
              disabled={
                !canManage ||
                !password ||
                password !== confirmPassword ||
                replaceCredential.isPending
              }
              onClick={() => replaceCredential.mutate()}
            >
              {replaceCredential.isPending ? "Encrypting…" : "Save credential"}
            </Button>
            {account.credential.configured && !account.credential.revoked && (
              <Button
                variant="danger"
                disabled={!canManage}
                onClick={() => setConfirmRevoke(true)}
              >
                Revoke credential
              </Button>
            )}
          </div>
        </div>
      )}
      {lifecycleOnly && (
        <>
          <Notice>
            Rotate credentials and change allowed schemas through Edit account /
            Allowed schemas. Discovery is rechecked before atomic save.
          </Notice>
          {account.credential.configured && !account.credential.revoked && (
            <Button
              variant="danger"
              disabled={!canManage}
              onClick={() => setConfirmRevoke(true)}
            >
              Revoke credential
            </Button>
          )}
        </>
      )}
      <Modal
        open={confirmRevoke}
        onOpenChange={setConfirmRevoke}
        title="Revoke this Oracle credential?"
        description="The application will stop using this credential. Change the Oracle password separately if needed."
      >
        {revokeCredential.isError && (
          <Notice tone="error">{revokeCredential.error.message}</Notice>
        )}
        <div className="dialog-actions">
          <Button onClick={() => setConfirmRevoke(false)}>Cancel</Button>
          <Button
            variant="danger"
            disabled={revokeCredential.isPending}
            onClick={() => revokeCredential.mutate()}
          >
            Revoke credential
          </Button>
        </div>
      </Modal>
    </Panel>
  );
}
function CreateExecutionAccount({
  open,
  onOpenChange,
  targetID,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  targetID: string;
  onCreated: (account: ExecutionAccount) => void;
}) {
  const [name, setName] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [schemaIDs, setSchemaIDs] = useState<string[]>([]);
  const schemas = useQuery({
    queryKey: ["schemas", targetID],
    queryFn: () =>
      apiList<TargetSchema>(
        `/database-targets/${encodeURIComponent(targetID)}/schemas`,
      ),
    enabled: open && !!targetID,
  });
  const create = useMutation({
    mutationFn: () =>
      api<ExecutionAccount>("/execution-accounts", {
        method: "POST",
        body: JSON.stringify({
          target_id: targetID,
          name,
          username,
          schema_ids: schemaIDs,
          password,
        }),
      }),
    onSuccess: (account) => {
      setPassword("");
      setConfirmPassword("");
      onCreated(account);
    },
  });
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Register Oracle execution account"
      description="Creates application metadata and an encrypted credential. This does not create an Oracle account."
    >
      <form
        onSubmit={(event: FormEvent) => {
          event.preventDefault();
          create.mutate();
        }}
      >
        <Field label="Account label" required>
          <input
            required
            maxLength={120}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field label="Oracle username" required>
          <input
            required
            maxLength={120}
            value={username}
            onChange={(event) => setUsername(event.target.value)}
          />
        </Field>
        <Field label="Oracle password" required>
          <input
            required
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
        </Field>
        <Field label="Confirm password" required>
          <input
            required
            type="password"
            autoComplete="new-password"
            value={confirmPassword}
            onChange={(event) => setConfirmPassword(event.target.value)}
          />
        </Field>
        <h3>Map to schemas</h3>
        {schemas.isPending ? (
          <div className="skeleton" role="status" />
        ) : schemas.isError ? (
          <Notice tone="error">{schemas.error.message}</Notice>
        ) : schemas.data.filter((schema) => schema.active).length ? (
          schemas.data
            .filter((schema) => schema.active)
            .map((schema) => (
              <Check
                key={schema.id}
                label={<code>{schema.name}</code>}
                checked={schemaIDs.includes(schema.id)}
                onChange={(checked) =>
                  setSchemaIDs(
                    checked
                      ? [...schemaIDs, schema.id]
                      : schemaIDs.filter((id) => id !== schema.id),
                  )
                }
              />
            ))
        ) : (
          <p className="empty">Register a schema first.</p>
        )}
        <Notice>
          Secrets are encrypted locally and never returned again. Confirm
          default account mappings on the database detail page.
        </Notice>
        {create.isError && <Notice tone="error">{create.error.message}</Notice>}
        <div className="dialog-actions">
          <Button type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            variant="primary"
            disabled={
              create.isPending ||
              !name.trim() ||
              !username.trim() ||
              !password ||
              password !== confirmPassword ||
              schemaIDs.length === 0
            }
          >
            {create.isPending ? "Encrypting…" : "Register account"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
