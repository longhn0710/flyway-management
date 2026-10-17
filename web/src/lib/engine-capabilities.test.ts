import { describe, expect, it } from "vitest";
import {
  capabilityFor,
  engineCapabilityLabel,
  migrationAvailable,
  type EngineCapability,
} from "./engine-capabilities";

const capabilities: EngineCapability[] = [
  {
    engine: "oracle",
    status: "Operational",
    connection: true,
    info: true,
    migration: true,
    profile: "Oracle policy",
  },
  {
    engine: "postgresql",
    status: "Connection/Info implemented",
    connection: true,
    info: true,
    migration: false,
    profile: "PostgreSQL 17",
  },
  {
    engine: "mysql",
    status: "Not implemented",
    connection: false,
    info: false,
    migration: false,
    profile: "",
  },
];

describe("engine capabilities", () => {
  it("keeps Oracle migration behavior stable if discovery is unavailable", () => {
    expect(migrationAvailable("oracle", undefined)).toBe(true);
    expect(migrationAvailable(undefined, undefined)).toBe(true);
  });

  it("requires an explicit true PostgreSQL migration capability", () => {
    expect(migrationAvailable("postgresql", undefined)).toBe(false);
    expect(migrationAvailable("postgresql", [])).toBe(false);
    expect(migrationAvailable("postgresql", capabilities)).toBe(false);
    const enabled = capabilities.map((capability) =>
      capability.engine === "postgresql"
        ? { ...capability, migration: true }
        : capability,
    );
    expect(migrationAvailable("postgresql", enabled)).toBe(true);
  });

  it("keeps engines without migration support unavailable", () => {
    expect(migrationAvailable("mysql", capabilities)).toBe(false);
  });

  it("looks up exact server engine IDs and formats labels from capabilities", () => {
    expect(capabilityFor(capabilities, "postgresql")?.status).toBe(
      "Connection/Info implemented",
    );
    expect(engineCapabilityLabel("PostgreSQL", capabilities)).toBe(
      "Connection / Info available · Migration disabled",
    );
    expect(engineCapabilityLabel("MySQL", capabilities)).toBe(
      "Not implemented",
    );
    expect(engineCapabilityLabel("PostgreSQL", undefined)).toBe(
      "Capability unavailable",
    );
  });
});
