import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api,
  apiList,
  type DatabaseTarget,
  type ExecutionAccount,
  type TargetSchema,
} from "../lib/contracts";
import {
  Button,
  Field,
  Modal,
  Notice,
  SearchBox,
  Badge,
} from "../components/ui";

type Candidate = {
  name: string;
  own: boolean;
  object_count: number;
  known_privileges: string[];
  migration_capability: string;
  capability_reason: string;
};
type Discovery = {
  network_state: string;
  authentication_state: string;
  actual_user: string;
  current_schema: string;
  database_service: string;
  pdb_name: string;
  schema_total: number;
  schemas: Candidate[];
  warnings: string[];
  error_code?: string;
  error_message?: string;
};
type Connection = {
  target: DatabaseTarget;
  account: ExecutionAccount;
  schemas: TargetSchema[];
  timeout_seconds: number;
};
export default function ConnectionWizard({
  target,
  onClose,
}: {
  target?: DatabaseTarget;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [name, setName] = useState(target?.name ?? "");
  const [environment, setEnvironment] = useState(target?.environment ?? "DEV");
  const [host, setHost] = useState(target?.host ?? "");
  const [port, setPort] = useState(target?.port ?? 1521);
  const [service, setService] = useState(target?.service ?? "");
  const [transport, setTransport] = useState(target?.transport ?? "TCP");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [accountID, setAccountID] = useState("");
  const [timeout, setTimeoutValue] = useState(20);
  const [discovery, setDiscovery] = useState<Discovery>();
  const [selected, setSelected] = useState<string[]>([]);
  const [ack, setAck] = useState(false);
  const [grantCurrentUser, setGrantCurrentUser] = useState(false);
  const [grantProd, setGrantProd] = useState(false);
  const [q, setQ] = useState("");
  const [step, setStep] = useState(1);
  const accounts = useQuery({
    queryKey: ["execution-accounts", target?.id],
    queryFn: () =>
      apiList<ExecutionAccount>(
        `/execution-accounts?target_id=${encodeURIComponent(target!.id)}`,
      ),
    enabled: !!target,
  });
  const saved = useQuery({
    queryKey: ["connection", target?.id, accountID],
    queryFn: () =>
      api<Connection>(
        `/connections/${encodeURIComponent(target!.id)}?account_id=${encodeURIComponent(accountID)}`,
      ),
    enabled: !!target,
  });
  const base = target
    ? `/connections/${encodeURIComponent(target.id)}`
    : "/connections";
  const input = () => ({
    target: { name, environment, host, port, service, transport },
    account_id: accountID,
    username,
    password,
    timeout_seconds: timeout,
    schemas: selected,
    acknowledge_unknown: ack,
    grant_current_user: grantCurrentUser,
    grant_prod: environment === "PROD" && grantCurrentUser && grantProd,
  });
  const test = useMutation({
    mutationFn: () =>
      api<Discovery>(`${base}/test`, {
        method: "POST",
        body: JSON.stringify(input()),
      }),
    onSuccess: (result) => {
      setDiscovery(result);
      setAck(false);
      if (result.authentication_state === "verified") {
        const current =
          accountID && saved.data?.account.id === accountID
            ? saved.data.schemas.map((x) => x.name)
            : undefined;
        setSelected(
          result.schemas
            .filter(
              (x) =>
                x.migration_capability !== "unsupported" &&
                (current !== undefined
                  ? current.includes(x.name)
                  : x.own && x.migration_capability === "potential"),
            )
            .map((x) => x.name),
        );
        setStep(2);
      }
    },
  });
  const save = useMutation({
    mutationFn: () =>
      api<Connection>(base, {
        method: target ? "PUT" : "POST",
        body: JSON.stringify(input()),
      }),
    onSuccess: () => {
      setPassword("");
      void client.invalidateQueries();
      onClose();
    },
  });
  const chooseAccount = (id: string) => {
    setAccountID(id);
    const a = accounts.data?.find((x) => x.id === id);
    setUsername(a?.username ?? "");
    setPassword("");
    setDiscovery(undefined);
    setStep(1);
  };
  const uncertain = discovery?.schemas.some(
    (x) => selected.includes(x.name) && x.migration_capability === "unknown",
  );
  const busy = test.isPending || save.isPending;
  const fields = () => (
    <>
      <div className="grid-two">
        <Field label="Connection display name" required>
          <input
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={200}
          />
        </Field>
        <Field label="Environment">
          <select
            value={environment}
            onChange={(e) =>
              setEnvironment(e.target.value as DatabaseTarget["environment"])
            }
          >
            <option>DEV</option>
            <option>UAT</option>
            <option>PROD</option>
          </select>
        </Field>
      </div>
      <div className="grid-two">
        <Field label="Host" required>
          <input
            required
            value={host}
            onChange={(e) => setHost(e.target.value)}
          />
        </Field>
        <Field label="Port" required>
          <input
            type="number"
            min={1}
            max={65535}
            required
            value={port}
            onChange={(e) => setPort(Number(e.target.value))}
          />
        </Field>
      </div>
      <div className="grid-two">
        <Field label="Transport">
          <select
            value={transport}
            onChange={(e) =>
              setTransport(e.target.value as DatabaseTarget["transport"])
            }
          >
            <option>TCP</option>
            <option>TCPS</option>
          </select>
        </Field>
        <Field label="Oracle service / PDB" required>
          <input
            required
            value={service}
            onChange={(e) => setService(e.target.value)}
          />
        </Field>
      </div>
      {target && (
        <Field label="Execution account">
          <select
            value={accountID}
            onChange={(e) => chooseAccount(e.target.value)}
          >
            <option value="">Add a new execution account</option>
            {accounts.data?.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} ({a.username})
              </option>
            ))}
          </select>
          <p className="small muted">
            Select the account to edit its mappings or rotate its credential.
            Other accounts are preserved.
          </p>
          {saved.data?.account?.id && !accountID && (
            <Button
              type="button"
              onClick={() => chooseAccount(saved.data!.account.id)}
            >
              Use saved connection account
            </Button>
          )}
        </Field>
      )}
      <div className="grid-two">
        <Field label="Oracle username" required>
          <input
            required
            autoComplete="off"
            value={username}
            readOnly={!!accountID}
            onChange={(e) => setUsername(e.target.value)}
          />
        </Field>
        <Field
          label="Oracle password"
          required={!accountID}
          hint={
            accountID
              ? "Leave empty to use the encrypted saved credential. Enter a new password to rotate on Save."
              : "Used transiently for testing; encrypted only on Save."
          }
        >
          <input
            type="password"
            required={!accountID}
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            maxLength={4096}
          />
        </Field>
      </div>
      <details>
        <summary>Advanced connection settings</summary>
        <Field
          label="Test timeout (seconds)"
          hint="Bounded read-only connection and metadata discovery budget."
        >
          <input
            type="number"
            min={5}
            max={30}
            value={timeout}
            onChange={(e) => setTimeoutValue(Number(e.target.value))}
          />
        </Field>
      </details>
    </>
  );
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open && !busy) {
          setPassword("");
          onClose();
        }
      }}
      title={target ? "Edit Oracle connection" : "Connect to Oracle"}
      description={`Step ${step} of 3 · ${step === 1 ? "Connection & authentication" : step === 2 ? "Schema discovery" : "Allowed schemas & review"}`}
    >
      <form
        onSubmit={(event: FormEvent) => {
          event.preventDefault();
          if (step === 1) test.mutate();
          else if (step === 3) save.mutate();
          else setStep(3);
        }}
      >
        {step === 1 ? (
          fields()
        ) : (
          <>
            <p>
              <strong>
                Oracle account: <code>{discovery?.actual_user}</code>
              </strong>
              <br />
              {host}:{port}/{service} · {environment} · {transport}
            </p>
            <p>
              Network: {discovery?.network_state} · Authentication:{" "}
              {discovery?.authentication_state}
              <br />
              Current schema: <code>{discovery?.current_schema}</code> ·
              Service: {discovery?.database_service} · PDB:{" "}
              {discovery?.pdb_name}
            </p>
            {discovery?.warnings?.map((warning, i) => (
              <Notice key={i} tone="warning">
                {warning}
              </Notice>
            ))}
            <div className="spread">
              <strong>Schemas discovered: {discovery?.schema_total}</strong>
              <Badge>
                Schemas enabled for Flyway Manager: {selected.length}
              </Badge>
            </div>
            <Notice>
              Visibility does not prove DDL authorization. Metadata
              classifications are potential, unknown or unsupported. Oracle
              checks every statement; the allowlist does not grant Oracle
              privileges.
            </Notice>
            {step === 2 ? (
              <>
                <SearchBox
                  value={q}
                  onChange={setQ}
                  placeholder="Filter discovered schemas…"
                />
                <div className="table-scroll" style={{ maxHeight: 350 }}>
                  <table>
                    <thead>
                      <tr>
                        <th>Schema</th>
                        <th>Migration capability</th>
                        <th>Known privileges / uncertainty</th>
                      </tr>
                    </thead>
                    <tbody>
                      {discovery?.schemas
                        .filter((x) =>
                          x.name.toLowerCase().includes(q.toLowerCase()),
                        )
                        .map((x) => (
                          <tr key={x.name}>
                            <td>
                              <code>{x.name}</code>
                              {x.own && <Badge>Login schema</Badge>}
                            </td>
                            <td>{x.migration_capability}</td>
                            <td>
                              {x.known_privileges?.join(", ") ||
                                "No relevant privilege evidence"}
                              <p className="small muted">
                                {x.capability_reason}
                              </p>
                            </td>
                          </tr>
                        ))}
                    </tbody>
                  </table>
                </div>
              </>
            ) : (
              <>
                <SearchBox
                  value={q}
                  onChange={setQ}
                  placeholder="Filter allowed schema choices…"
                />
                <div style={{ maxHeight: 300, overflow: "auto" }}>
                  {discovery?.schemas
                    .filter((x) =>
                      x.name.toLowerCase().includes(q.toLowerCase()),
                    )
                    .map((x) => (
                      <label className="check inset" key={x.name}>
                        <input
                          type="checkbox"
                          checked={selected.includes(x.name)}
                          disabled={
                            x.migration_capability === "unsupported" || busy
                          }
                          onChange={(e) => {
                            setSelected(
                              e.target.checked
                                ? [...selected, x.name]
                                : selected.filter((n) => n !== x.name),
                            );
                            setAck(false);
                          }}
                        />
                        <span>
                          <code>{x.name}</code>
                          {x.own ? " · Login schema" : ""} ·{" "}
                          {x.migration_capability}
                          <small className="block muted">
                            {x.capability_reason}
                          </small>
                        </span>
                      </label>
                    ))}
                </div>
                {uncertain && (
                  <label className="check">
                    <input
                      type="checkbox"
                      checked={ack}
                      onChange={(e) => setAck(e.target.checked)}
                    />
                    I acknowledge that selected schemas have unknown DDL
                    privileges. Oracle may reject migrations; no privilege is
                    fully verified.
                  </label>
                )}
                {!selected.length && (
                  <Notice tone="warning">
                    {target && accountID
                      ? "Saving with no schemas revokes this account’s future execution. Existing history remains available."
                      : "Select at least one potentially usable schema. No schemas are enabled automatically beyond the appropriate login schema."}
                  </Notice>
                )}
                <label className="check">
                  <input
                    type="checkbox"
                    checked={grantCurrentUser}
                    onChange={(e) => setGrantCurrentUser(e.target.checked)}
                  />
                  Authorize my Flyway Manager user to run migrations on the
                  selected schemas.
                </label>
                {environment === "PROD" && grantCurrentUser && (
                  <label className="check">
                    <input
                      type="checkbox"
                      checked={grantProd}
                      onChange={(e) => setGrantProd(e.target.checked)}
                    />
                    Explicitly authorize my user for PROD execution on these
                    schemas.
                  </label>
                )}
                <p className="small muted">
                  Save rechecks Oracle metadata and registers the target,
                  encrypted credential, schemas and allowlist atomically.
                  Application operators still require scoped grants
                  {environment === "PROD"
                    ? " and explicit PROD authorization"
                    : ""}
                  .
                </p>
              </>
            )}
          </>
        )}
        {discovery && step === 1 && (
          <Notice tone="error">
            {discovery.error_code}{" "}
            {discovery.error_message ||
              `Network: ${discovery.network_state}; authentication: ${discovery.authentication_state}. Check the address, listener, service and credentials.`}
          </Notice>
        )}
        {test.isError && <Notice tone="error">{test.error.message}</Notice>}
        {save.isError && <Notice tone="error">{save.error.message}</Notice>}
        <div className="dialog-actions">
          <Button
            type="button"
            disabled={busy}
            onClick={() => {
              setPassword("");
              onClose();
            }}
          >
            Cancel
          </Button>
          {step > 1 && (
            <Button
              type="button"
              disabled={busy}
              onClick={() => {
                setStep(step - 1);
                if (step === 2) setDiscovery(undefined);
              }}
            >
              Back
            </Button>
          )}
          <Button
            variant="primary"
            type="submit"
            disabled={
              busy ||
              (step === 3 &&
                ((!selected.length && !(target && accountID)) ||
                  (!!uncertain && !ack)))
            }
          >
            {test.isPending
              ? "Testing & discovering…"
              : save.isPending
                ? "Saving…"
                : step === 1
                  ? "Test Connection"
                  : step === 2
                    ? "Choose allowed schemas"
                    : "Save connection"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
