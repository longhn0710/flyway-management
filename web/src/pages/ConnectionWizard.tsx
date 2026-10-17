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
import {
  engineCapabilityLabel,
  useEngineCapabilities,
} from "../lib/engine-capabilities";

type Candidate = {
  name: string;
  own: boolean;
  object_count: number;
  known_privileges: string[];
  migration_capability: string;
  capability_reason: string;
};
type Discovery = {
  database_name?: string;
  server_version?: string;
  tls_mode?: string;
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
  profile?: {
    auth_kind: string;
    tls_mode: string;
    ca_pem: string;
    ca_sha256: string;
    revision: number;
  };
  target: DatabaseTarget;
  account: ExecutionAccount;
  schemas: TargetSchema[];
  timeout_seconds: number;
};
export default function ConnectionWizard({
  target,
  onClose,
  initialAccountID = "",
}: {
  target?: DatabaseTarget;
  initialAccountID?: string;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const engineCapabilities = useEngineCapabilities();
  const [name, setName] = useState(target?.name ?? "");
  const [environment, setEnvironment] = useState(target?.environment ?? "DEV");
  const [host, setHost] = useState(target?.host ?? "");
  const [port, setPort] = useState(target?.port ?? 1521);
  const [databaseName, setDatabaseName] = useState(target?.database_name ?? "");
  const [caPEM, setCAPEM] = useState("");
  const [service, setService] = useState(target?.service ?? "");
  const [transport, setTransport] = useState(target?.transport ?? "TCP");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [accountID, setAccountID] = useState(initialAccountID);
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
  const effectiveUsername = accountID
    ? (accounts.data?.find((a) => a.id === accountID)?.username ??
      (saved.data?.account?.id === accountID
        ? saved.data.account.username
        : username))
    : username;
  const input = () => ({
    target: {
      name,
      environment,
      host,
      port,
      service: engine === "PostgreSQL" ? "" : service,
      transport: engine === "PostgreSQL" ? "TCPS" : transport,
      ...(engine === "PostgreSQL"
        ? {
            engine: "postgresql",
            database_name: databaseName,
            ...(caPEM
              ? {
                  profile: {
                    auth_kind: "password",
                    tls_mode: "verify-full",
                    ca_pem: caPEM,
                  },
                }
              : {}),
          }
        : {}),
    },
    account_id: accountID,
    username: effectiveUsername,
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
      if (result.authentication_state === "verified" && !result.error_code) {
        const current =
          accountID && saved.data?.account?.id === accountID
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
  const accountLoading =
    !!target && (accounts.isPending || (!!accountID && !saved.isSuccess));
  const invalidSelection =
    (!selected.length && !(target && accountID)) || (!!uncertain && !ack);
  const [engine, setEngine] = useState(
    target?.engine === "postgresql" ? "PostgreSQL" : "Oracle",
  );
  const [discard, setDiscard] = useState(false);
  const requestClose = () => {
    if (!busy) setDiscard(true);
  };
  const fields = () => (
    <>
      <Field label="Database engine">
        <select
          value={engine}
          disabled={!!target}
          onChange={(e) => {
            setEngine(e.target.value);
            setPort(e.target.value === "PostgreSQL" ? 5432 : 1521);
            setDiscovery(undefined);
            setPassword("");
          }}
        >
          <option value="Oracle">
            Oracle · {engineCapabilityLabel("Oracle", engineCapabilities.data)}
          </option>
          <option value="PostgreSQL">
            PostgreSQL · {engineCapabilityLabel("PostgreSQL", engineCapabilities.data)}
          </option>
          <option value="MySQL">
            MySQL · {engineCapabilityLabel("MySQL", engineCapabilities.data)}
          </option>
          <option value="SQL Server">
            SQL Server · {engineCapabilityLabel("SQL Server", engineCapabilities.data)}
          </option>
        </select>
      </Field>
      {engine !== "Oracle" && engine !== "PostgreSQL" && (
        <Notice tone="warning">
          {engine} · Future engine. Registration, testing, discovery and
          migrations are unavailable. Oracle fields below are disabled.
        </Notice>
      )}
      {engine !== "Oracle" && engine !== "PostgreSQL" && (
        <fieldset disabled className="future-engine-preview">
          <h3>{engine} connection preview · Future</h3>
          <div className="grid-two">
            <Field label="Instance display name">
              <input placeholder="Future engine registration unavailable" />
            </Field>
            <Field label="Environment">
              <select>
                <option>Unavailable</option>
              </select>
            </Field>
            <Field label="Host">
              <input />
            </Field>
            <Field label="Port">
              <input
                value={
                  engine === "PostgreSQL"
                    ? "5432"
                    : engine === "MySQL"
                      ? "3306"
                      : "1433"
                }
                readOnly
              />
            </Field>
            <Field
              label={
                engine === "SQL Server"
                  ? "Database / catalog"
                  : engine === "MySQL"
                    ? "Database (= schema)"
                    : "Database"
              }
            >
              <input />
            </Field>
            <Field label="Execution username">
              <input />
            </Field>
            <Field label="Execution password">
              <input type="password" value="" readOnly />
            </Field>
            <Field label="TLS configuration">
              <select>
                <option>Future · Unavailable</option>
              </select>
            </Field>
          </div>
          <p className="small muted">
            {engine === "PostgreSQL"
              ? "Future scope: database → schemas."
              : engine === "MySQL"
                ? "Future scope: database and schema are synonyms."
                : "Future scope: database/catalog → schemas."}{" "}
            Engine-specific discovery and defaults are unavailable.
          </p>
        </fieldset>
      )}
      <fieldset
        disabled={(engine !== "Oracle" && engine !== "PostgreSQL") || busy}
        hidden={engine !== "Oracle" && engine !== "PostgreSQL"}
      >
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
              <option>SIT</option>
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
          <Field label={engine === "PostgreSQL" ? "TLS policy" : "Transport"}>
            <select
              value={engine === "PostgreSQL" ? "TCPS" : transport}
              disabled={engine === "PostgreSQL"}
              onChange={(e) =>
                setTransport(e.target.value as DatabaseTarget["transport"])
              }
            >
              <option>TCP</option>
              <option value="TCPS">
                {engine === "PostgreSQL" ? "verify-full (required)" : "TCPS"}
              </option>
            </select>
          </Field>
          <Field
            label={
              engine === "PostgreSQL"
                ? "PostgreSQL database"
                : "Oracle service / PDB"
            }
            required
          >
            <input
              required
              value={engine === "PostgreSQL" ? databaseName : service}
              onChange={(e) =>
                engine === "PostgreSQL"
                  ? setDatabaseName(e.target.value)
                  : setService(e.target.value)
              }
            />
          </Field>
        </div>
        {engine === "PostgreSQL" && (
          <>
            <Notice>
              PostgreSQL 17 · {engineCapabilityLabel("PostgreSQL", engineCapabilities.data)}.
              Use existing lowercase database, schema and role names.
            </Notice>
            <Field label="Approved TLS CA bundle (PEM)" required={!target}>
              <textarea
                required={!target}
                value={caPEM}
                onChange={(e) => setCAPEM(e.target.value)}
                maxLength={65536}
                placeholder={
                  target
                    ? "Leave empty to preserve saved trust"
                    : "Paste approved CA certificates"
                }
              />
            </Field>
            {saved.data?.profile && (
              <p className="small muted">
                Trust SHA-256: <code>{saved.data.profile.ca_sha256}</code> ·
                revision {saved.data.profile.revision}
              </p>
            )}
          </>
        )}
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
          <Field
            label={
              engine === "PostgreSQL" ? "PostgreSQL role" : "Oracle username"
            }
            required
          >
            <input
              required
              autoComplete="off"
              value={effectiveUsername}
              readOnly={!!accountID}
              onChange={(e) => setUsername(e.target.value)}
            />
          </Field>
          <Field
            label={
              engine === "PostgreSQL"
                ? "PostgreSQL password"
                : "Oracle password"
            }
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
      </fieldset>
    </>
  );
  return (
    <>
      <Modal
        open
        onOpenChange={(open) => {
          if (!open) requestClose();
        }}
        title={target ? "Manage instance connection" : "Add instance"}
        description={`Step ${step} of 4 · ${["Details & connection", "Test & discover", "Allowed scope", "Review & save"][step - 1]}`}
      >
        <ol className="wizard-steps" aria-label="Registration steps">
          {[
            "Details & connection",
            "Test & discover",
            "Allowed scope",
            "Review & save",
          ].map((label, index) => (
            <li
              key={label}
              aria-current={step === index + 1 ? "step" : undefined}
            >
              <span>{index + 1}</span>
              {label}
            </li>
          ))}
        </ol>
        <form
          onSubmit={(event: FormEvent) => {
            event.preventDefault();
            if (busy || (step === 1 && accountLoading)) return;
            if (step === 1 && (engine === "Oracle" || engine === "PostgreSQL"))
              test.mutate();
            else if (step === 4 && !invalidSelection) save.mutate();
            else if (step === 3 && !invalidSelection) setStep(4);
            else if (step === 2) setStep(3);
          }}
        >
          {step === 1 ? (
            fields()
          ) : step === 4 ? (
            <div className="registration-review">
              <h2>Review & register instance</h2>
              <p>
                Verify the destination, execution account and exact schema
                allowlist before saving.
              </p>
              <div className="destination-strip">
                <Badge>
                  {engine === "PostgreSQL"
                    ? `PostgreSQL · ${engineCapabilityLabel("PostgreSQL", engineCapabilities.data)}`
                    : "Oracle · Operational"}
                </Badge>
                <Badge tone={environment === "PROD" ? "error" : "accent"}>
                  {environment}
                </Badge>
                <strong>{name}</strong>
                <code>
                  {host}:{port}/
                  {engine === "PostgreSQL" ? databaseName : service}
                </code>
                <strong className="mono">{effectiveUsername}</strong>
              </div>
              <h3>Allowed schemas · {selected.length}</h3>
              {selected.map((n) => (
                <div key={n} className="inset spread">
                  <code>{n}</code>
                  <Badge>
                    {
                      discovery?.schemas.find((x) => x.name === n)
                        ?.migration_capability
                    }
                  </Badge>
                </div>
              ))}
              <p>
                Credential:{" "}
                {password
                  ? "Provided for this save (write-only)"
                  : "Reuse encrypted saved credential"}
                .
              </p>
              <p>
                Current-user operate grants:{" "}
                {grantCurrentUser ? "Requested" : "Not requested"}. Explicit
                PROD grant:{" "}
                {environment === "PROD" && grantCurrentUser && grantProd
                  ? "Requested"
                  : "Not requested"}
                .
              </p>
              <details>
                <summary>Excluded discovered schemas</summary>
                {discovery?.schemas
                  .filter((x) => !selected.includes(x.name))
                  .map((x) => (
                    <p key={x.name}>
                      <code>{x.name}</code> · {x.migration_capability}
                    </p>
                  ))}
              </details>
              <Notice>
                Save performs fresh database discovery and atomic persistence.
                Per-account allowlists grant no database privileges and do not
                constrain cross-schema SQL. Existing migrations and audit
                history are preserved.
              </Notice>
            </div>
          ) : (
            <>
              <p>
                <strong>
                  {engine} account: <code>{discovery?.actual_user}</code>
                </strong>
                <br />
                {host}:{port}/{engine === "PostgreSQL" ? databaseName : service}{" "}
                · {environment} · {transport}
              </p>
              <p>
                Network: {discovery?.network_state} · Authentication:{" "}
                {discovery?.authentication_state}
                <br />
                {engine === "PostgreSQL" ? (
                  <>
                    Database: <code>{discovery?.database_name}</code> · Server
                    version: {discovery?.server_version} · TLS:{" "}
                    {discovery?.tls_mode}
                  </>
                ) : (
                  <>
                    Current schema: <code>{discovery?.current_schema}</code> ·
                    Service: {discovery?.database_service} · PDB:{" "}
                    {discovery?.pdb_name}
                  </>
                )}
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
                classifications are potential, unknown or unsupported. The
                database checks every statement; the allowlist does not grant
                database privileges.
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
                      privileges. The database may reject operations; no
                      privilege is fully verified.
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
                    Authorize my Flyway Manager user to{" "}
                    {engine === "PostgreSQL"
                      ? "operate on"
                      : "run migrations on"}{" "}
                    the selected schemas.
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
                    Save rechecks database metadata and registers the target,
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
          {target && accounts.isError && (
            <Notice tone="error">
              {accounts.error.message}
              <Button type="button" onClick={() => void accounts.refetch()}>
                Retry accounts
              </Button>
            </Notice>
          )}
          {target && accountID && saved.isError && (
            <Notice tone="error">
              {saved.error.message}
              <Button type="button" onClick={() => void saved.refetch()}>
                Retry saved account
              </Button>
            </Notice>
          )}
          {test.isError && <Notice tone="error">{test.error.message}</Notice>}
          {save.isError && <Notice tone="error">{save.error.message}</Notice>}
          <div className="dialog-actions">
            <Button
              type="button"
              disabled={busy}
              onClick={() => {
                requestClose();
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
                (engine !== "Oracle" && engine !== "PostgreSQL") ||
                (step === 1 && accountLoading) ||
                ((step === 3 || step === 4) && invalidSelection)
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
                      : step === 3
                        ? "Review selection"
                        : "Save instance"}
            </Button>
          </div>
        </form>
      </Modal>
      <Modal
        open={discard}
        onOpenChange={setDiscard}
        title="Discard unsaved connection?"
        description="No connection changes have been saved. The transient password will be cleared."
      >
        <div className="dialog-actions">
          <Button onClick={() => setDiscard(false)}>Keep editing</Button>
          <Button
            variant="danger"
            onClick={() => {
              setPassword("");
              onClose();
            }}
          >
            Discard changes
          </Button>
        </div>
      </Modal>
    </>
  );
}
