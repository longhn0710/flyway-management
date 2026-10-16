import type { DatabaseTarget, Identity, TargetSchema } from "./contracts";

// UI affordance only; the execution APIs enforce grants, allowlists and credentials.
export function canRun(
  identity: Identity,
  target: DatabaseTarget,
  schema?: TargetSchema,
) {
  return (
    target.active &&
    (!schema || schema.active) &&
    ["Admin", "Operator"].includes(identity.user.role) &&
    identity.grants.some(
      (g) =>
        g.target_id === target.id &&
        (!schema || !g.schema_id || g.schema_id === schema.id) &&
        g.operate &&
        (target.environment !== "PROD" || g.prod),
    )
  );
}
