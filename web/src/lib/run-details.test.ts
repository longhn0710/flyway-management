import { describe, expect, it } from "vitest";
import {
  repairNotice,
  repairStepLabel,
  repairStepState,
  summarizeFlywayResult,
} from "./run-details";

describe("Run Details summaries", () => {
  it("summarizes Flyway info states without losing the original payload", () => {
    const summary = summarizeFlywayResult("info", {
      operation: "info",
      migrations: [
        { state: "Success" },
        { state: "Out of Order" },
        { state: "Pending" },
        { state: "Failed" },
        { state: "Ignored" },
      ],
    });

    expect(summary?.message).toBe("2 applied · 1 pending · 1 failed");
    expect(JSON.parse(summary?.prettyJson ?? "{}").migrations).toHaveLength(5);
  });

  it("summarizes validate success and failure with counts", () => {
    expect(
      summarizeFlywayResult("validate", {
        operation: "validate",
        validationSuccessful: true,
        validateCount: 3,
        invalidMigrations: [],
      })?.message,
    ).toBe("Validation passed · 3 migrations checked");

    expect(
      summarizeFlywayResult("validate", {
        operation: "validate",
        validationSuccessful: false,
        validateCount: 3,
        invalidMigrations: [{ version: "2" }],
      })?.message,
    ).toBe("Validation failed · 3 migrations checked · 1 invalid migration");
  });

  it("uses execution evidence for migrate outcome and count", () => {
    const result = {
      operation: "migrate",
      migrationsExecuted: 2,
      migrations: [{ version: "1" }, { version: "2" }],
    };
    expect(summarizeFlywayResult("migrate", result, true)?.message).toBe(
      "Migration succeeded · 2 migrations executed",
    );
    expect(summarizeFlywayResult("migrate", result, false)?.message).toBe(
      "Migration failed · 2 migrations executed",
    );
    expect(
      summarizeFlywayResult("migrate", {
        ...result,
        errorDetails: null,
      })?.message,
    ).toBe("Migration result recorded · 2 migrations executed");
    expect(summarizeFlywayResult("repair", result)).toBeNull();
  });
});

describe("Run Details repair progress", () => {
  it("marks skipped repair as neutral and explains successful runs", () => {
    const state = repairStepState("SUCCEEDED", "SKIPPED");
    expect(state).toBe("skipped");
    expect(repairStepLabel(state, "SKIPPED")).toBe("Repair skipped");
    expect(repairNotice("SUCCEEDED", "SKIPPED", "")).toEqual({
      tone: "success",
      title: "No repair needed",
      message: "The migration succeeded; repair was skipped.",
    });
  });

  it("keeps active, unsafe, and failed repair states distinct", () => {
    expect(repairStepState("FAILED", "PENDING")).toBe("running");
    expect(repairStepState("NEEDS_REVIEW", "UNSAFE")).toBe("failed");
    expect(repairStepLabel("failed", "UNSAFE")).toBe("Repair unsafe");
    expect(
      repairNotice("NEEDS_REVIEW", "UNSAFE", "Not attempted"),
    ).toMatchObject({
      tone: "error",
      message: "Not attempted",
    });
    expect(repairNotice("FAILED", "FAILED", "Could not verify")).toMatchObject({
      tone: "error",
      message: "Could not verify",
    });
  });
});
