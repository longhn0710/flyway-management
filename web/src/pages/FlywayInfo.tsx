import { NativeInfoCapture } from "../components/NativeInfoCapture";
import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api,
  apiList,
  type DatabaseTarget,
  type TargetSchema,
  type ExecutionAccount,
} from "../lib/contracts";
import { type Artifact } from "../lib/execution";
import { type InfoCapture } from "../lib/flyway-info";
import { useIdentity } from "../lib/identity-context";
import { canRun } from "../lib/permissions";
import { schemaHref } from "../lib/navigation";
import { Button, Field, Notice, PageHeading, Panel } from "../components/ui";

export default function FlywayInfo() {
  const [params] = useSearchParams();
  const target = params.get("target") || "",
    schema = params.get("schema") || "",
    run = params.get("run_id") || "";
  return (
    <InfoView
      key={`${target}/${schema}/${run}`}
      target={target}
      schema={schema}
      run={run}
    />
  );
}
function InfoView({
  target,
  schema,
  run,
}: {
  target: string;
  schema: string;
  run: string;
}) {
  const identity = useIdentity(),
    client = useQueryClient();
  const [params, setParams] = useSearchParams();
  const [account, setAccount] = useState(""),
    [artifact, setArtifact] = useState<Artifact | null>(null),
    [files, setFiles] = useState<File[]>([]);
  const base = `/database-targets/${encodeURIComponent(target)}/schemas/${encodeURIComponent(schema)}/flyway-info`;
  const key = ["flyway-info", target, schema, run];
  const registry = useQuery({
    queryKey: ["info-target", target],
    queryFn: () =>
      api<DatabaseTarget>(`/database-targets/${encodeURIComponent(target)}`),
    enabled: !!target,
  });
  const schemas = useQuery({
    queryKey: ["schemas", target],
    queryFn: () =>
      apiList<TargetSchema>(
        `/database-targets/${encodeURIComponent(target)}/schemas`,
      ),
    enabled: !!target,
  });
  const stream = schemas.data?.find((s) => s.id === schema);
  const operate = !!(
    registry.data &&
    stream &&
    canRun(identity, registry.data, stream)
  );
  const info = useQuery({
    queryKey: key,
    queryFn: () =>
      api<InfoCapture | null>(
        `${base}${run ? `?run_id=${encodeURIComponent(run)}` : ""}`,
      ),
    enabled: !!target && !!schema,
  });
  const accounts = useQuery({
    queryKey: ["info-accounts", target, schema],
    queryFn: () =>
      api<ExecutionAccount[]>(
        `/database-targets/${encodeURIComponent(target)}/schemas/${encodeURIComponent(schema)}/execution-accounts`,
      ),
    enabled: operate,
  });
  const recorded = info.data;
  const accountID = account || recorded?.snapshot.account.id || "";
  const artifactID = artifact?.id || recorded?.artifact_id || "";
  const upload = useMutation({
    mutationFn: async () => {
      const body = new FormData();
      for (const f of files)
        body.append(
          "files",
          f,
          (f as File & { webkitRelativePath?: string }).webkitRelativePath ||
            f.name,
        );
      return api<Artifact>(
        `/artifacts?target_id=${encodeURIComponent(target)}&schema_id=${encodeURIComponent(schema)}`,
        { method: "POST", body },
      );
    },
    onSuccess: setArtifact,
  });
  const refresh = useMutation({
    mutationFn: () =>
      api<InfoCapture>(`${base}/refresh`, {
        method: "POST",
        body: JSON.stringify({
          execution_account_id: accountID,
          artifact_id: artifactID,
        }),
      }),
    onSuccess: async (value) => {
      client.setQueryData(["flyway-info", target, schema, ""], value);
      const next = new URLSearchParams(params);
      next.delete("run_id");
      setParams(next);
      await client.invalidateQueries({
        queryKey: ["flyway-info", target, schema],
      });
    },
  });
  return (
    <>
      <PageHeading
        title="Flyway Info"
        breadcrumb={
          <>
            <Link to={schemaHref(target, schema)}>Schema</Link> / Flyway Info
          </>
        }
        description="Native migration states from the pinned Flyway CLI."
        actions={
          <Link
            to={`/runs?target_id=${encodeURIComponent(target)}&schema_id=${encodeURIComponent(schema)}`}
          >
            Migration history
          </Link>
        }
      />
      {!target || !schema ? (
        <Notice tone="warning">
          Open Flyway Info from a registered schema or migration run.
        </Notice>
      ) : (
        <>
          {run && (
            <Notice>
              Historical Info from run{" "}
              <Link to={`/runs/${encodeURIComponent(run)}`}>{run}</Link>.{" "}
              <Link
                to={`/flyway-info?${new URLSearchParams({ target, schema })}`}
              >
                Latest recorded Info
              </Link>
            </Notice>
          )}
          {info.isPending ? (
            <div
              className="skeleton"
              role="status"
              aria-label="Loading Flyway Info"
            />
          ) : info.isError ? (
            <Notice tone="error">
              {info.error.message}{" "}
              <Button onClick={() => void info.refetch()}>Retry</Button>
            </Notice>
          ) : !recorded ? (
            <Notice>
              No recorded Flyway Info. Select an execution account and immutable
              migration artifact to capture read-only status.
            </Notice>
          ) : (
            <>
              <NativeInfoCapture recorded={recorded} />
            </>
          )}
          {operate ? (
            <Panel title="Refresh Flyway Info (read-only)">
              <p>
                Uses the current registered destination and selected account,
                with the immutable artifact plus reserved stream files. Only
                native info runs; migration and repair safeguards remain
                enforced.
              </p>
              <div className="grid-two">
                <Field label="Execution account">
                  <select
                    value={accountID}
                    onChange={(e) => setAccount(e.target.value)}
                  >
                    <option value="">Select account</option>
                    {accounts.data?.map((a) => (
                      <option key={a.id} value={a.id}>
                        {a.name} · {a.username}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field
                  label="Migration artifact"
                  hint="Use a complete migration package for meaningful Pending / Missing states."
                >
                  <input
                    type="file"
                    multiple
                    accept=".sql,.zip"
                    onChange={(e) => {
                      setFiles(Array.from(e.target.files || []));
                      upload.reset();
                    }}
                  />
                  <Button
                    disabled={
                      !files.length || upload.isPending || refresh.isPending
                    }
                    onClick={() => upload.mutate()}
                  >
                    {upload.isPending
                      ? "Uploading…"
                      : "Upload immutable artifact"}
                  </Button>
                </Field>
              </div>
              {accounts.isError && (
                <Notice tone="error">{accounts.error.message}</Notice>
              )}
              {upload.isError && (
                <Notice tone="error">{upload.error.message}</Notice>
              )}
              <p className="mono">
                Artifact: {artifactID || "Required"}
                <br />
                SHA-256: {artifact?.sha256 || recorded?.artifact_sha256 || "—"}
              </p>
              {refresh.isError && (
                <Notice tone="error">{refresh.error.message}</Notice>
              )}
              <Button
                variant="primary"
                disabled={
                  !accountID ||
                  !artifactID ||
                  refresh.isPending ||
                  upload.isPending ||
                  !accounts.data?.some(
                    (a) => a.id === accountID && a.credential.configured,
                  )
                }
                onClick={() => refresh.mutate()}
              >
                {refresh.isPending ? "Inspecting…" : "Refresh Flyway Info"}
              </Button>
            </Panel>
          ) : (
            <Notice>
              Read-only viewing. Live refresh requires existing operate
              permission, including explicit PROD authorization and a mapped
              execution account.
            </Notice>
          )}
        </>
      )}
    </>
  );
}
