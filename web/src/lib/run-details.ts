export type RepairStepState =
  "not-started" | "running" | "complete" | "failed" | "skipped";

export function repairStepState(
  runStatus: string,
  repairOutcome: string,
): RepairStepState {
  if (repairOutcome === "SKIPPED") return "skipped";
  if (repairOutcome === "SUCCEEDED") return "complete";
  if (repairOutcome === "FAILED" || repairOutcome === "UNSAFE") return "failed";
  if (
    runStatus === "REPAIRING" ||
    (runStatus === "FAILED" && (!repairOutcome || repairOutcome === "PENDING"))
  ) {
    return "running";
  }
  return "not-started";
}

export function repairStepLabel(
  state: RepairStepState,
  outcome: string,
): string {
  if (state === "skipped") return "Repair skipped";
  if (state === "running") return "Repair";
  if (state === "complete") return "Repair complete";
  if (outcome === "UNSAFE") return "Repair unsafe";
  if (state === "failed") return "Repair failed";
  return "Repair";
}

export interface RepairNotice {
  tone: "info" | "warning" | "error" | "success";
  title: string;
  message: string;
}

export function repairNotice(
  runStatus: string,
  outcome: string,
  reason: string,
): RepairNotice {
  if (outcome === "SKIPPED" && runStatus === "SUCCEEDED") {
    return {
      tone: "success",
      title: "No repair needed",
      message: "The migration succeeded; repair was skipped.",
    };
  }
  if (outcome === "SKIPPED") {
    return {
      tone: "info",
      title: "Repair skipped",
      message: reason || "No repair operation was run.",
    };
  }
  if (outcome === "UNSAFE") {
    return {
      tone: "error",
      title: "Repair was unsafe",
      message: reason || "Repair was not attempted because it was unsafe.",
    };
  }
  if (outcome === "FAILED") {
    return {
      tone: "error",
      title: "Repair could not be verified",
      message: reason || "The repair outcome could not be verified.",
    };
  }
  if (outcome === "SUCCEEDED") {
    return {
      tone: "warning",
      title: "Repair completed; migration remains failed",
      message: reason || "Repair evidence is recorded separately.",
    };
  }
  return {
    tone: "warning",
    title: "Repair status",
    message: reason || "Repair evidence is recorded separately.",
  };
}

type JsonObject = Record<string, unknown>;

export interface FlywaySummary {
  title: string;
  message: string;
  prettyJson: string;
}

function objectValue(value: unknown): JsonObject | null {
  if (typeof value === "string") {
    try {
      value = JSON.parse(value) as unknown;
    } catch {
      return null;
    }
  }
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as JsonObject)
    : null;
}

function migrations(value: unknown): JsonObject[] {
  return Array.isArray(value)
    ? value.filter(
        (entry): entry is JsonObject =>
          entry !== null && typeof entry === "object" && !Array.isArray(entry),
      )
    : [];
}

function numeric(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) && value >= 0
    ? value
    : null;
}

function plural(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? "" : "s"}`;
}

export function summarizeFlywayResult(
  command: string,
  value: unknown,
  successful?: boolean,
): FlywaySummary | null {
  if (!["info", "validate", "migrate"].includes(command)) return null;
  const result = objectValue(value);
  if (!result || result.operation !== command) return null;
  const prettyJson = JSON.stringify(result, null, 2);

  if (command === "info") {
    const entries = migrations(result.migrations);
    let applied = 0;
    let pending = 0;
    let failed = 0;
    for (const entry of entries) {
      const state = String(entry.state ?? "")
        .trim()
        .toLowerCase();
      if (
        ["success", "baseline", "out of order", "out_of_order"].includes(state)
      ) {
        applied += 1;
      } else if (state.includes("pending")) {
        pending += 1;
      } else if (state.includes("fail")) {
        failed += 1;
      }
    }
    return {
      title: "Flyway history",
      message: `${applied} applied · ${pending} pending · ${failed} failed`,
      prettyJson,
    };
  }

  if (command === "validate") {
    const checked = numeric(result.validateCount);
    const invalid = migrations(result.invalidMigrations).length;
    const resultText =
      result.validationSuccessful === true
        ? "Validation passed"
        : result.validationSuccessful === false
          ? "Validation failed"
          : "Validation completed";
    const checkedText =
      checked === null ? "" : ` · ${plural(checked, "migration")} checked`;
    const invalidText =
      result.validationSuccessful === false
        ? ` · ${plural(invalid, "invalid migration")}`
        : "";
    return {
      title: "Flyway validation",
      message: `${resultText}${checkedText}${invalidText}`,
      prettyJson,
    };
  }

  const count =
    numeric(result.migrationsExecuted) ?? migrations(result.migrations).length;
  const outcome =
    successful === true
      ? "Migration succeeded"
      : successful === false ||
          (result.errorDetails !== undefined && result.errorDetails !== null)
        ? "Migration failed"
        : "Migration result recorded";
  return {
    title: "Flyway migration",
    message: `${outcome} · ${plural(count, "migration")} executed`,
    prettyJson,
  };
}
