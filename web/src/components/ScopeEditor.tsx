import { useState } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { Database, ChevronDown } from "lucide-react";
import {
  apiList,
  type DatabaseTarget,
  type Grant,
  type TargetSchema,
} from "../lib/contracts";
import { Badge, Check, Notice, SearchBox } from "./ui";

type Permission = "read" | "operate" | "prod";
export default function ScopeEditor({
  value = [],
  onChange,
  disabled = false,
}: {
  value?: Grant[];
  onChange?: (grants: Grant[]) => void;
  disabled?: boolean;
}) {
  const [q, setQ] = useState("");
  const [pending, setPending] = useState<{
    grants: Grant[];
    label: string;
  } | null>(null);
  const targets = useQuery({
    queryKey: ["database-targets"],
    queryFn: () => apiList<DatabaseTarget>("/database-targets"),
  });
  const activeTargets = targets.data?.filter((target) => target.active) ?? [];
  const schemaQueries = useQueries({
    queries: activeTargets.map((target) => ({
      queryKey: ["schemas", target.id],
      queryFn: () =>
        apiList<TargetSchema>(
          `/database-targets/${encodeURIComponent(target.id)}/schemas`,
        ),
    })),
  });
  if (targets.isPending || schemaQueries.some((item) => item.isPending))
    return (
      <div
        className="skeleton"
        role="status"
        aria-label="Loading target scopes"
      />
    );
  if (targets.isError)
    return (
      <Notice tone="error">
        Unable to load database scopes: {targets.error.message}
      </Notice>
    );
  const schemaError = schemaQueries.find((item) => item.isError);
  if (schemaError?.error)
    return (
      <Notice tone="error">
        Unable to load schemas: {schemaError.error.message}
      </Notice>
    );
  if (activeTargets.length === 0)
    return (
      <p className="empty">
        Register a database target before granting scoped access.
      </p>
    );

  const withGrant = (
    source: Grant[],
    targetID: string,
    schemaID: string,
    property: Permission,
    checked: boolean,
  ) => {
    const matching = source.find(
      (grant) => grant.target_id === targetID && grant.schema_id === schemaID,
    );
    const next: Grant = {
      target_id: targetID,
      schema_id: schemaID,
      read: matching?.read ?? false,
      operate: matching?.operate ?? false,
      prod: matching?.prod ?? false,
    };
    if (property === "read") {
      next.read = checked;
      if (!checked) {
        next.operate = false;
        next.prod = false;
      }
    } else if (property === "operate") {
      next.operate = checked;
      if (checked) next.read = true;
      else next.prod = false;
    } else {
      next.prod = checked;
      if (checked) {
        next.read = true;
        next.operate = true;
      }
    }
    const rest = source.filter(
      (grant) =>
        !(grant.target_id === targetID && grant.schema_id === schemaID),
    );
    return next.read || next.operate || next.prod ? [...rest, next] : rest;
  };
  const change = (
    targetID: string,
    schemaID: string,
    property: Permission,
    checked: boolean,
  ) => {
    const next = withGrant(value, targetID, schemaID, property, checked);
    const reduces = value.some((grant) => {
      if (
        grant.target_id !== targetID ||
        (grant.schema_id !== schemaID && schemaID !== "")
      )
        return false;
      const remaining = next.find(
        (candidate) =>
          candidate.target_id === targetID &&
          (candidate.schema_id === grant.schema_id ||
            candidate.schema_id === ""),
      );
      return (
        (grant.read && !remaining?.read) ||
        (grant.operate && !remaining?.operate) ||
        (grant.prod && !remaining?.prod)
      );
    });
    if (reduces)
      setPending({
        grants: next,
        label: `Remove ${property} access for ${schemaID ? "this schema" : "the whole target"}?`,
      });
    else onChange?.(next);
  };
  const query = q.trim().toLowerCase();
  return (
    <div className="scope-editor">
      <h3>Scoped access by database and schema</h3>
      <SearchBox
        label="Filter targets"
        value={q}
        onChange={setQ}
        placeholder="Search target or schema…"
      />
      {activeTargets.map((target, index) => {
        const allSchemas = (schemaQueries[index]?.data ?? []).filter(
          (schema) => schema.active,
        );
        const targetMatches =
          !query ||
          [target.name, target.id, target.host]
            .join(" ")
            .toLowerCase()
            .includes(query);
        const matchingSchemas = allSchemas.filter(
          (schema) =>
            !query ||
            [schema.name, schema.id].join(" ").toLowerCase().includes(query),
        );
        if (!targetMatches && matchingSchemas.length === 0) return null;
        const schemas = targetMatches ? allSchemas : matchingSchemas;
        const broad = value.find(
          (grant) => grant.target_id === target.id && grant.schema_id === "",
        );
        const broadActive = !!(broad?.read || broad?.operate || broad?.prod);
        return (
          <details open key={target.id} className="scope-target">
            <summary>
              <Database size={15} />
              <code>{target.name}</code>
              <Badge tone={target.environment === "PROD" ? "error" : "accent"}>
                {target.environment} · {target.service}
              </Badge>
              <ChevronDown size={14} />
            </summary>
            <div className="scope-schema">
              <div className="spread wrap">
                <strong>Entire target</strong>
                {target.environment === "PROD" && (
                  <Badge tone="error">Explicit PROD grant required</Badge>
                )}
              </div>
              <div className="grid-two">
                <Check
                  label="Read authorized data"
                  checked={!!broad?.read}
                  disabled={disabled}
                  onChange={(checked) => change(target.id, "", "read", checked)}
                />
                <Check
                  label="Operate authorized migrations"
                  checked={!!broad?.operate}
                  disabled={disabled}
                  onChange={(checked) =>
                    change(target.id, "", "operate", checked)
                  }
                />
              </div>
              {target.environment === "PROD" && (
                <Check
                  label="Allow operations in PROD"
                  checked={!!broad?.prod}
                  disabled={disabled}
                  onChange={(checked) => change(target.id, "", "prod", checked)}
                />
              )}
            </div>
            {schemas.length === 0 ? (
              <p className="small muted">
                No active schemas match this filter.
              </p>
            ) : (
              <div className="scope-schemas">
                {schemas.map((schema) => {
                  const grant = value.find(
                    (item) =>
                      item.target_id === target.id &&
                      item.schema_id === schema.id,
                  );
                  const inherited = broadActive;
                  return (
                    <div className="scope-schema" key={schema.id}>
                      <div className="spread wrap">
                        <code>Schema: {schema.name}</code>
                        {inherited ? (
                          <Badge>Includes target-wide access</Badge>
                        ) : (
                          target.environment === "PROD" && (
                            <Badge tone="error">
                              Explicit PROD grant required
                            </Badge>
                          )
                        )}
                      </div>
                      {inherited && (
                        <p className="small muted">
                          Effective access includes the whole-target grant.
                          Adjust it above to change inherited access.
                        </p>
                      )}
                      <div className="grid-two">
                        <Check
                          label="Read authorized data"
                          checked={inherited || !!grant?.read}
                          disabled={disabled || inherited}
                          onChange={(checked) =>
                            change(target.id, schema.id, "read", checked)
                          }
                        />
                        <Check
                          label="Operate authorized migrations"
                          checked={inherited || !!grant?.operate}
                          disabled={disabled || inherited}
                          onChange={(checked) =>
                            change(target.id, schema.id, "operate", checked)
                          }
                        />
                      </div>
                      {target.environment === "PROD" && (
                        <Check
                          label="Allow operations in PROD"
                          checked={!!broad?.prod || !!grant?.prod}
                          disabled={disabled || !!broad?.prod}
                          onChange={(checked) =>
                            change(target.id, schema.id, "prod", checked)
                          }
                        />
                      )}
                    </div>
                  );
                })}
              </div>
            )}
          </details>
        );
      })}
      <Notice>
        Administrator role does not grant PROD operation access. The server
        enforces every grant.
      </Notice>
      {pending && (
        <Notice tone="warning" title="Confirm access reduction">
          <>
            <span>{pending.label}</span>
            <div className="dialog-actions">
              <button
                className="button secondary"
                type="button"
                onClick={() => setPending(null)}
              >
                Keep access
              </button>
              <button
                className="button danger"
                type="button"
                onClick={() => {
                  onChange?.(pending.grants);
                  setPending(null);
                }}
              >
                Remove access
              </button>
            </div>
          </>
        </Notice>
      )}
    </div>
  );
}
