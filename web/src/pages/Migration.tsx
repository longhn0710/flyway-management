import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import {
  api,
  apiList,
  type DatabaseTarget,
  type ExecutionAccount,
  type TargetSchema,
} from "../lib/contracts";
import type {
  Artifact,
  Preflight,
  Run,
  RunInput,
  RuntimeStatus,
} from "../lib/execution";
import { useIdentity } from "../lib/identity-context";
import {
  Badge,
  Button,
  Field,
  Modal,
  Notice,
  PageHeading,
  Panel,
  Status,
} from "../components/ui";

const newKey = () => crypto.randomUUID();
export default function Migration() {
  return <MigrationPage />;
}
export function Review() {
  return <MigrationPage />;
}

function MigrationPage() {
  const identity = useIdentity();
  const navigate = useNavigate();
  const client = useQueryClient();
  const [targetID, setTargetID] = useState("");
  const [schemaID, setSchemaID] = useState("");
  const [accountID, setAccountID] = useState("");
  const [files, setFiles] = useState<File[]>([]);
  const [artifact, setArtifact] = useState<Artifact | null>(null);
  const [context, setContext] = useState({
    external_run_id: "",
    commit_sha: "",
    initiated_by: "",
  });
  const [preflight, setPreflight] = useState<Preflight | null>(null);
  const [preflightFor, setPreflightFor] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [submissionError, setSubmissionError] = useState("");
  const [failedSubmissionSignature, setFailedSubmissionSignature] =
    useState("");
  const keyRef = useRef({ signature: "", key: newKey() });
  const canSubmit =
    identity.user.role === "Admin" || identity.user.role === "Operator";
  const runtime = useQuery({
    queryKey: ["runtime"],
    queryFn: () => api<RuntimeStatus>("/runtime"),
  });
  const targets = useQuery({
    queryKey: ["database-targets"],
    queryFn: () => apiList<DatabaseTarget>("/database-targets"),
  });
  const selectedTargetID =
    targetID || targets.data?.find((t) => t.active)?.id || "";
  const schemas = useQuery({
    queryKey: ["schemas", selectedTargetID],
    queryFn: () =>
      apiList<TargetSchema>(
        `/database-targets/${encodeURIComponent(selectedTargetID)}/schemas`,
      ),
    enabled: !!selectedTargetID,
  });
  const selectedSchemaID =
    schemaID || schemas.data?.find((s) => s.active)?.id || "";
  const accounts = useQuery({
    queryKey: ["stream-accounts", selectedTargetID, selectedSchemaID],
    queryFn: () =>
      apiList<ExecutionAccount>(
        `/database-targets/${encodeURIComponent(selectedTargetID)}/schemas/${encodeURIComponent(selectedSchemaID)}/execution-accounts`,
      ),
    enabled: !!selectedTargetID && !!selectedSchemaID,
  });
  const target = targets.data?.find((t) => t.id === selectedTargetID);
  const schema = schemas.data?.find((s) => s.id === selectedSchemaID);
  const mappedAccounts = (accounts.data ?? []).filter(
    (a) =>
      a.active &&
      a.credential.configured &&
      !a.credential.revoked &&
      a.schema_ids.includes(selectedSchemaID),
  );
  const selectedAccount =
    mappedAccounts.find((a) => a.id === accountID) ??
    mappedAccounts.find((a) => a.id === schema?.default_account_id) ??
    mappedAccounts[0];
  const authorized =
    canSubmit &&
    identity.grants.some(
      (g) =>
        g.target_id === selectedTargetID &&
        (g.schema_id === selectedSchemaID || !g.schema_id) &&
        g.operate &&
        (target?.environment !== "PROD" || g.prod),
    );
  const input: RunInput | null =
    artifact && selectedTargetID && selectedSchemaID && selectedAccount
      ? {
          target_id: selectedTargetID,
          schema_id: selectedSchemaID,
          execution_account_id: selectedAccount.id,
          artifact_id: artifact.id,
          context: Object.fromEntries(
            Object.entries(context)
              .filter(([, v]) => v.trim())
              .map(([k, v]) => [k, v.trim()]),
          ),
        }
      : null;
  const upload = useMutation({
    mutationFn: async (selected: File[]) => {
      const stream = { targetID: selectedTargetID, schemaID: selectedSchemaID };
      const body = new FormData();
      for (const file of selected)
        body.append(
          "files",
          file,
          (file as File & { webkitRelativePath?: string }).webkitRelativePath ||
            file.name,
        );
      return {
        value: await api<Artifact>(
          `/artifacts?target_id=${encodeURIComponent(stream.targetID)}&schema_id=${encodeURIComponent(stream.schemaID)}`,
          { method: "POST", body },
        ),
        stream,
      };
    },
    onSuccess: ({ value, stream }) => {
      if (
        value.target_id !== selectedTargetID ||
        value.schema_id !== selectedSchemaID ||
        stream.targetID !== selectedTargetID ||
        stream.schemaID !== selectedSchemaID
      )
        return;
      setArtifact(value);
      setPreflight(null);
    },
  });
  const check = useMutation({
    mutationFn: (body: RunInput) =>
      api<Preflight>("/migration-preflight", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: (value, body) => {
      setPreflight(value);
      setPreflightFor(JSON.stringify(body));
    },
  });
  const submit = useMutation({
    mutationFn: (body: RunInput) => {
      const signature = JSON.stringify(body);
      if (keyRef.current.signature !== signature)
        keyRef.current = { signature, key: newKey() };
      return api<Run>("/migration-runs", {
        method: "POST",
        headers: { "Idempotency-Key": keyRef.current.key },
        body: JSON.stringify(body),
      });
    },
    onSuccess: (run) => {
      setConfirm(false);
      setSubmissionError("");
      setFailedSubmissionSignature("");
      void client.invalidateQueries({ queryKey: ["runs"] });
      navigate(`/runs/${encodeURIComponent(run.id || run.run_id)}`);
    },
    onError: (error, body) => {
      setFailedSubmissionSignature(JSON.stringify(body));
      setSubmissionError(
        `${error instanceof Error ? error.message : "Submission failed"}. Retry uses the same request key safely.`,
      );
    },
  });
  const chooseFiles = (event: ChangeEvent<HTMLInputElement>) => {
    setFiles(Array.from(event.target.files ?? []));
    setArtifact(null);
    setPreflight(null);
  };
  const selectedInputSignature = input ? JSON.stringify(input) : "";
  useEffect(() => {
    if (keyRef.current.signature !== selectedInputSignature)
      keyRef.current = { signature: selectedInputSignature, key: newKey() };
  }, [selectedInputSignature]);
  const preflightCurrent =
    !!preflight?.ready &&
    preflight.live_status === "VERIFIED" &&
    preflightFor === selectedInputSignature;
  return (
    <>
      <PageHeading
        title="Run migration"
        description="Upload an immutable SQL bundle, validate it against Flyway, then submit one target-schema stream."
        breadcrumb="Operations / Run migration"
      />
      {runtime.isError ? (
        <Notice tone="error" title="Flyway runtime unavailable">
          Runtime readiness could not be read.{" "}
          <Button onClick={() => void runtime.refetch()}>Retry</Button>
        </Notice>
      ) : runtime.data && !runtime.data.ready ? (
        <Notice tone="warning" title="Flyway runtime unavailable">
          Migration submission is disabled until the configured runtime is
          ready. {runtime.data.blockers.join(" ")}
        </Notice>
      ) : runtime.data?.ready ? (
        <p className="small muted">
          Flyway {runtime.data.version} · integrity {runtime.data.integrity} ·
          dependencies {runtime.data.dependency}
        </p>
      ) : null}
      {!canSubmit && (
        <Notice tone="warning">
          Your role can view migration information but cannot upload or submit
          runs.
        </Notice>
      )}
      {canSubmit && !authorized && target && schema && (
        <Notice tone="warning">
          This target and schema require an explicit operate grant
          {target.environment === "PROD" ? " and explicit PROD permission" : ""}
          .
        </Notice>
      )}
      <div className="layout-editor">
        <Panel title="1 · Select stream">
          {targets.isPending ? (
            <div
              className="skeleton"
              role="status"
              aria-label="Loading targets"
            />
          ) : targets.isError ? (
            <Notice tone="error">
              Could not load database targets. {targets.error.message}{" "}
              <Button onClick={() => void targets.refetch()}>Retry</Button>
            </Notice>
          ) : null}
          <div className="form-grid">
            <Field label="Database target" required>
              <select
                value={selectedTargetID}
                onChange={(e) => {
                  setTargetID(e.target.value);
                  setSchemaID("");
                  setAccountID("");
                  setArtifact(null);
                  setPreflight(null);
                }}
                disabled={upload.isPending}
              >
                <option value="">Choose target</option>
                {(targets.data ?? [])
                  .filter((t) => t.active)
                  .map((t) => (
                    <option key={t.id} value={t.id}>
                      {t.name} · {t.environment} · {t.service}
                    </option>
                  ))}
              </select>
            </Field>
            <Field label="Target schema" required>
              <select
                value={selectedSchemaID}
                onChange={(e) => {
                  setSchemaID(e.target.value);
                  setAccountID("");
                  setArtifact(null);
                  setPreflight(null);
                }}
                disabled={
                  !selectedTargetID || schemas.isPending || upload.isPending
                }
              >
                <option value="">Choose schema</option>
                {(schemas.data ?? [])
                  .filter((s) => s.active)
                  .map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
              </select>
            </Field>
            <Field
              label="Execution account"
              required
              hint="Only active accounts explicitly mapped to this schema with a configured credential are listed."
            >
              <select
                value={selectedAccount?.id ?? ""}
                onChange={(e) => {
                  setAccountID(e.target.value);
                  setPreflight(null);
                }}
                disabled={!selectedSchemaID || accounts.isPending}
              >
                <option value="">Choose account</option>
                {mappedAccounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name} · {a.username}
                    {a.id === schema?.default_account_id ? " · default" : ""}
                  </option>
                ))}
              </select>
            </Field>
          </div>
          {selectedTargetID && schemas.isPending && (
            <div
              className="skeleton"
              role="status"
              aria-label="Loading schemas"
            />
          )}
          {selectedTargetID && schemas.isError && (
            <Notice tone="error">
              Could not load schemas. {schemas.error.message}{" "}
              <Button onClick={() => void schemas.refetch()}>Retry</Button>
            </Notice>
          )}
          {selectedSchemaID && accounts.isPending && (
            <div
              className="skeleton"
              role="status"
              aria-label="Loading execution accounts"
            />
          )}
          {selectedSchemaID && accounts.isError && (
            <Notice tone="error">
              Could not load authorized execution accounts.{" "}
              {accounts.error.message}{" "}
              <Button onClick={() => void accounts.refetch()}>Retry</Button>
            </Notice>
          )}
          {accounts.isSuccess && mappedAccounts.length === 0 && (
            <p className="empty">
              No active, credentialed execution account is mapped to this
              schema.
            </p>
          )}
        </Panel>
        <Panel
          title="2 · Upload migration bundle"
          aside={artifact && <Badge>{artifact.id}</Badge>}
        >
          <p className="muted">
            Choose SQL files, ZIP, TAR, TAR.GZ or TGZ. Folder selection keeps
            each relative path.
          </p>
          <div className="inline">
            <label className="button secondary">
              Select files
              <input
                type="file"
                multiple
                accept=".sql,.zip,.tar,.gz,.tgz"
                hidden
                onChange={chooseFiles}
              />
            </label>
            <label className="button secondary">
              Select folder
              <input
                type="file"
                multiple
                hidden
                {...({ webkitdirectory: "", directory: "" } as Record<
                  string,
                  string
                >)}
                onChange={chooseFiles}
              />
            </label>
            <Button
              disabled={
                !files.length ||
                !selectedSchemaID ||
                !authorized ||
                upload.isPending ||
                targets.isPending ||
                schemas.isPending ||
                accounts.isPending
              }
              onClick={() => upload.mutate(files)}
            >
              {upload.isPending ? "Uploading…" : "Upload and normalize"}
            </Button>
          </div>
          {files.length > 0 && (
            <p className="small muted">
              {files.length} selected ·{" "}
              {(files.reduce((n, f) => n + f.size, 0) / 1024 / 1024).toFixed(2)}{" "}
              MB
            </p>
          )}
          {upload.isError && (
            <Notice tone="error">{upload.error.message}</Notice>
          )}
          {artifact && (
            <>
              <Notice tone="success" title="Immutable artifact ready">
                {artifact.id} · {artifact.sha256} ·{" "}
                {artifact.total_bytes.toLocaleString()} extracted SQL bytes
              </Notice>
              <p className="small">
                <strong>Original inputs</strong>
              </p>
              {artifact.inputs.map((item, index) => (
                <p key={`${item.name}-${index}`} className="small muted">
                  <span className="mono">{item.name}</span> ·{" "}
                  {item.size.toLocaleString()} bytes · SHA-256 {item.sha256}
                </p>
              ))}
              <div className="table-scroll">
                <table>
                  <thead>
                    <tr>
                      <th>Migration</th>
                      <th>Type</th>
                      <th>Size</th>
                      <th>SHA-256</th>
                    </tr>
                  </thead>
                  <tbody>
                    {artifact.files.map((file) => (
                      <tr key={file.path}>
                        <td className="mono">{file.path}</td>
                        <td>
                          {file.kind}
                          {file.version ? ` · ${file.version}` : ""}
                        </td>
                        <td>{file.size.toLocaleString()}</td>
                        <td className="mono small">{file.sha256}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )}
        </Panel>
        {artifact && (
          <Panel title="3 · Run context (optional)">
            <div className="form-grid">
              <Field label="External run ID">
                <input
                  value={context.external_run_id}
                  onChange={(e) => {
                    setContext({ ...context, external_run_id: e.target.value });
                    setPreflight(null);
                  }}
                />
              </Field>
              <Field label="Commit SHA">
                <input
                  value={context.commit_sha}
                  onChange={(e) => {
                    setContext({ ...context, commit_sha: e.target.value });
                    setPreflight(null);
                  }}
                />
              </Field>
              <Field label="Initiated by">
                <input
                  value={context.initiated_by}
                  onChange={(e) => {
                    setContext({ ...context, initiated_by: e.target.value });
                    setPreflight(null);
                  }}
                />
              </Field>
            </div>
          </Panel>
        )}
        {artifact && (
          <Panel title="4 · Preflight and review">
            <Button
              variant="primary"
              disabled={
                !input ||
                !authorized ||
                runtime.isPending ||
                !runtime.data?.ready ||
                check.isPending
              }
              onClick={() => input && check.mutate(input)}
            >
              {check.isPending
                ? "Checking Flyway and Oracle…"
                : "Run preflight"}
            </Button>
            {check.isError && (
              <Notice tone="error">{check.error.message}</Notice>
            )}
            {preflight && (
              <div className="stack">
                <div className="inline">
                  <Status
                    value={preflight.ready ? preflight.live_status : "BLOCKED"}
                  />
                  <span>Runtime {preflight.runtime_version || "unknown"}</span>
                </div>
                {preflight.blockers.map((b, i) => (
                  <Notice key={`${b.code}-${i}`} tone="error" title={b.code}>
                    {b.message}
                  </Notice>
                ))}
                {preflight.warnings.map((w, i) => (
                  <Notice key={i} tone="warning">
                    {w}
                  </Notice>
                ))}
                {preflight.diagnostics.map((d) => (
                  <p key={d.name} className="small">
                    <strong>{d.name}</strong> · {d.status} — {d.detail}
                  </p>
                ))}
                {preflight.info?.migrations && (
                  <div>
                    <h3>Flyway migration preview</h3>
                    <p className="small muted">
                      Existing history in Flyway order, before this upload.
                    </p>
                    {preflight.info.migrations.length === 0 ? (
                      <p className="empty">No migrations are recorded yet.</p>
                    ) : (
                      <div className="table-scroll">
                        <table>
                          <thead>
                            <tr>
                              <th>Version</th>
                              <th>Type</th>
                              <th>Script</th>
                              <th>State</th>
                            </tr>
                          </thead>
                          <tbody>
                            {preflight.info.migrations.map(
                              (migration, index) => (
                                <tr
                                  key={`${migration.filepath || migration.script || "migration"}-${index}`}
                                >
                                  <td>{migration.version || "—"}</td>
                                  <td>{migration.category || "—"}</td>
                                  <td className="mono">
                                    {migration.filepath ||
                                      migration.script ||
                                      migration.description ||
                                      "—"}
                                  </td>
                                  <td>{migration.state || "—"}</td>
                                </tr>
                              ),
                            )}
                          </tbody>
                        </table>
                      </div>
                    )}
                  </div>
                )}
                {preflight.ready && preflight.live_status !== "VERIFIED" && (
                  <Notice tone="warning">
                    Offline checks are not enough to submit. Oracle and Flyway
                    validation must be VERIFIED.
                  </Notice>
                )}
                {preflightCurrent && (
                  <>
                    <Notice
                      tone="warning"
                      title="Oracle DDL can partially commit"
                    >
                      A failed migration may leave database changes in place.
                      Automatic repair never reruns migration SQL or turns a
                      failed run into success.
                    </Notice>
                    <Button
                      variant="primary"
                      disabled={!preflightCurrent}
                      onClick={() => setConfirm(true)}
                    >
                      Review and submit migration
                    </Button>
                  </>
                )}
              </div>
            )}
          </Panel>
        )}
      </div>
      {submissionError && (
        <Notice tone="error">
          {submissionError}
          {input &&
            authorized &&
            preflightCurrent &&
            selectedInputSignature === failedSubmissionSignature && (
              <Button
                disabled={submit.isPending}
                onClick={() => submit.mutate(input)}
              >
                Retry same submission
              </Button>
            )}
        </Notice>
      )}
      <Modal
        open={confirm}
        onOpenChange={setConfirm}
        title="Confirm migration submission"
        description="Verify the destination and immutable artifact before queuing this run."
      >
        <div className="">
          <Notice tone="warning">
            Oracle DDL may partially commit. A failed migration remains failed
            even if conditional repair succeeds; SQL is never rerun
            automatically.
          </Notice>
          <dl className="key-values">
            <div>
              <dt>Environment / database</dt>
              <dd>
                {target?.environment} · {target?.name} · {target?.service}
              </dd>
            </div>
            <div>
              <dt>Target schema</dt>
              <dd>{schema?.name}</dd>
            </div>
            <div>
              <dt>Execution account</dt>
              <dd>
                {selectedAccount?.name} · {selectedAccount?.username}
              </dd>
            </div>
            <div>
              <dt>History location</dt>
              <dd>
                {schema?.history_schema}.{schema?.history_table}
              </dd>
            </div>
            <div>
              <dt>Artifact</dt>
              <dd>
                {artifact?.id} · {artifact?.sha256}
              </dd>
            </div>
            <div>
              <dt>Migration files</dt>
              <dd>{artifact?.files.length}</dd>
            </div>
          </dl>
          <div className="dialog-actions">
            <Button onClick={() => setConfirm(false)}>Back to review</Button>
            <Button
              variant="primary"
              disabled={submit.isPending || !preflightCurrent}
              onClick={() => input && submit.mutate(input)}
            >
              {submit.isPending ? "Submitting…" : "Submit migration"}
            </Button>
          </div>
        </div>
      </Modal>
      <p className="small muted">
        Flyway schema settings do not limit Oracle privileges; the Oracle
        account’s grants govern database access.{" "}
        <Link to="/runs">View Run History</Link>
      </p>
    </>
  );
}
