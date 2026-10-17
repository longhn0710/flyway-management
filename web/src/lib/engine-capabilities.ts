import { useQuery } from "@tanstack/react-query";
import { apiList } from "./contracts";

export interface EngineCapability {
  engine: string;
  status: string;
  connection: boolean;
  info: boolean;
  migration: boolean;
  profile: string;
}

export const engineCapabilitiesQueryKey = ["engine-capabilities"] as const;

export function fetchEngineCapabilities(): Promise<EngineCapability[]> {
  return apiList<EngineCapability>("/engine-capabilities");
}

export function useEngineCapabilities() {
  const query = useQuery({
    queryKey: engineCapabilitiesQueryKey,
    queryFn: fetchEngineCapabilities,
    staleTime: 30_000,
  });
  return { ...query, data: query.isError ? undefined : query.data };
}

export function capabilityFor(
  capabilities: EngineCapability[] | undefined,
  engine: string,
): EngineCapability | undefined {
  return capabilities?.find((capability) => capability.engine === engine);
}

// Oracle keeps its established workflow if capability discovery is unavailable.
// PostgreSQL requires an explicit server response with migration === true.
export function migrationAvailable(
  engine: string | undefined,
  capabilities: EngineCapability[] | undefined,
): boolean {
  const normalized = engine || "oracle";
  if (normalized === "oracle") return true;
  if (normalized !== "postgresql") return false;
  return capabilityFor(capabilities, normalized)?.migration === true;
}

export function engineCapabilityLabel(
  engine: string,
  capabilities: EngineCapability[] | undefined,
): string {
  const normalized =
    engine.toLowerCase() === "sql server" ? "sqlserver" : engine.toLowerCase();
  const capability = capabilityFor(capabilities, normalized);
  if (!capability) {
    return normalized === "oracle"
      ? "Operational"
      : "Capability unavailable";
  }
  if (capability.connection !== true) return capability.status || "Not implemented";

  const available = ["Connection"];
  const unavailable: string[] = [];
  if (capability.info === true) available.push("Info");
  else unavailable.push("Info");
  if (capability.migration === true) available.push("Migration");
  else unavailable.push("Migration");
  return `${available.join(" / ")} available${unavailable.length ? ` · ${unavailable.join(" / ")} disabled` : ""}`;
}
