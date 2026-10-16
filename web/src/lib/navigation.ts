// Canonical schema destination. Keep filter context when opening catalog rows.
export function schemaHref(
  targetID: string,
  schemaID: string,
  context?: URLSearchParams,
) {
  const params = new URLSearchParams(context);
  params.set("schema", schemaID);
  return `/instances/${encodeURIComponent(targetID)}/schemas?${params.toString()}`;
}
