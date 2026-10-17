import { test, expect, type Page } from "@playwright/test";
import { createHash } from "node:crypto";
import type { RunFile } from "../src/lib/sql-viewer";

const stamp = "2026-10-16T08:02:39Z";
const target = {
  id: "oracle",
  name: "Fixture Oracle",
  service: "FREEPDB1",
  environment: "UAT",
  host: "unreachable.invalid",
  port: 1521,
  transport: "TCP",
  active: true,
};
const schema = {
  id: "app",
  target_id: "oracle",
  name: "APP",
  history_schema: "APP",
  history_table: "flyway_schema_history",
  active: true,
};
const artifact = "historical-artifact";
const sql =
  "-- Untrusted <img src=x onerror=alert(1)>\r\n" +
  Array.from(
    { length: 450 },
    (_, i) => `SELECT '${i === 449 ? "last needle" : i}' FROM dual;`,
  ).join("\r\n");
const bytes = Buffer.from(sql);
const files: RunFile[] = [
  {
    artifact_id: artifact,
    path: "nested/V1__long.sql",
    kind: "versioned",
    version: "1",
    sha256: createHash("sha256").update(bytes).digest("hex"),
    size: bytes.length,
  },
  {
    artifact_id: artifact,
    path: "R__refresh.sql",
    kind: "repeatable",
    sha256: createHash("sha256").update("SELECT 2;").digest("hex"),
    size: 9,
  },
];
async function fixture(
  page: Page,
  options: {
    info?: "none" | "empty" | "unknown";
    content?: "missing" | "corrupt" | "forbidden";
    manifest?: "missing";
    many?: boolean;
    repetitive?: boolean;
  } = {},
) {
  const repetitiveBytes = Buffer.alloc(200_000, "x");
  const effectiveFiles = options.repetitive
    ? [
        {
          ...files[0],
          size: repetitiveBytes.length,
          sha256: createHash("sha256").update(repetitiveBytes).digest("hex"),
        },
      ]
    : files;
  const requests: string[] = [],
    errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/api/v1/**", async (route) => {
    const req = route.request(),
      url = new URL(req.url()),
      path = url.pathname.replace("/api/v1", "");
    requests.push(`${req.method()} ${path}${url.search}`);
    const send = (data: unknown) => route.fulfill({ json: { data } });
    const reject = (status: number, message: string) =>
      route.fulfill({ status, json: { error: { code: "FIXTURE", message } } });
    if (req.method() !== "GET")
      throw new Error(`Unexpected mutation ${req.method()} ${path}`);
    if (path === "/auth/me")
      return send({
        user: {
          id: "reader",
          username: "reader",
          display_name: "Read-only reviewer",
          role: "Viewer",
          active: true,
        },
        grants: [{ target_id: target.id, schema_id: schema.id, read: true }],
        csrf_token: "fixture",
      });
    if (path === "/health")
      return send({
        status: "ok",
        database: "ok",
        installation_id: "fixture",
        capabilities: {},
      });
    if (path === "/projects") return send([]);
    if (path === "/migration-runs/run")
      return send({
        id: "run",
        run_id: "run",
        status: "SUCCEEDED",
        target_id: target.id,
        schema_id: schema.id,
        artifact_id: artifact,
        actor_name: "operator",
        actor_kind: "user",
        source: "WEB",
        created_at: stamp,
        started_at: stamp,
        finished_at: stamp,
        flyway_version: "13.10.3",
        migration_outcome: "SUCCEEDED",
        repair_outcome: "SKIPPED",
        target,
        schema,
        account: {},
        context: {},
      });
    if (path === "/migration-runs/run/artifacts")
      return send([
        {
          id: artifact,
          files,
          inputs: [
            {
              name: "historical.zip",
              sha256: "package-hash",
              size: bytes.length,
            },
          ],
          sha256: "artifact-hash",
          total_bytes: bytes.length,
        },
      ]);
    if (path === "/migration-runs/run/files")
      return options.manifest
        ? reject(409, "Retained artifact file is missing")
        : send(effectiveFiles);
    if (path === "/migration-runs/run/files/content") {
      if (options.content === "missing") return reject(404, "Missing artifact");
      if (options.content === "forbidden") return reject(403, "Not authorized");
      const body = options.repetitive
        ? repetitiveBytes
        : options.content === "corrupt"
          ? Buffer.from("changed bytes")
          : url.searchParams.get("path")?.startsWith("R__")
            ? Buffer.from("SELECT 2;")
            : bytes;
      return route.fulfill({ body, contentType: "application/octet-stream" });
    }
    if (
      path === "/migration-runs/run/executions" ||
      path === "/migration-runs/run/logs"
    )
      return send([]);
    if (path === "/migration-runs/run/repair")
      return send({
        outcome: "SKIPPED",
        reason: "No repair needed",
        before: null,
        after: null,
      });
    if (path.endsWith("/flyway-info")) {
      if (url.searchParams.get("run_id") !== "run")
        throw new Error("Historical screen requested a newer capture");
      if (options.info === "none") return send(null);
      const migrations =
        options.info === "empty"
          ? []
          : Array.from({ length: options.many ? 25 : 2 }, (_, i) => ({
              category: i === 1 ? "Repeatable" : "Versioned",
              version: i === 1 ? null : String(i + 1),
              description: i === 1 ? "refresh" : `long ${i}`,
              type: "SQL",
              installedOnUTC: stamp,
              state:
                options.info === "unknown" ? "Native New State" : "Success",
              undoable: "No",
              installedBy: "APP_DEPLOY",
              executionTime: 9,
              filepath:
                i === 1
                  ? "R__refresh.sql"
                  : `/private/retained/sql/${files[0].path}`,
            }));
      return send({
        id: "capture",
        source_run_id: "run",
        captured_at: stamp,
        artifact_id: artifact,
        artifact_sha256: "artifact-hash",
        snapshot: {
          target,
          schema,
          account: { id: "secret-account", name: "", username: "" },
        },
        files,
        runtime_version: "13.10.3",
        actor_name: "operator",
        success: true,
        exit_code: 0,
        json: { migrations, schemaVersion: "1", database: "FREEPDB1" },
        stdout: "retained native stdout",
        stderr: "",
        warnings: ["Recorded snapshot, not live database state."],
      });
    }
    throw new Error(`Unhandled fixture ${path}`);
  });
  return { requests, errors };
}

test("read-only long SQL: native row journey, nested path, repeatable, search, pagination, mobile focus and safe text", async ({
  page,
}) => {
  const state = await fixture(page, { info: "unknown" });
  await page.goto("/runs/run?tab=info");
  await expect(
    page.getByRole("cell", { name: "Native New State", exact: true }),
  ).toHaveCount(2);
  for (const name of [
    "Category",
    "Version",
    "Description",
    "Type",
    "Installed On",
    "State",
    "Undoable",
  ])
    await expect(
      page.getByRole("columnheader", { name: new RegExp(name) }),
    ).toBeVisible();
  await page.getByLabel("Native state").selectOption("Native New State");
  await page.getByRole("button", { name: "long 0", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("APP_DEPLOY");
  await page.getByRole("link", { name: "Open SQL script" }).click();
  await expect(page).toHaveURL(
    /artifact_id=historical-artifact&path=nested%2FV1__long.sql/,
  );
  await expect(page.getByRole("region", { name: "SQL source" })).toContainText(
    "<img src=x onerror=alert(1)>",
  );
  await expect(page.locator(".sql-source img")).toHaveCount(0);
  await expect(page.locator(".sql-keyword").first()).toHaveText("SELECT");
  await expect(page.locator(".sql-line-number")).toHaveCount(200);
  await page.getByRole("textbox", { name: "Find in SQL" }).fill("last needle");
  await expect(page.getByRole("region", { name: "SQL source" })).toContainText(
    "last needle",
  );
  await expect(
    page.getByRole("status").filter({ hasText: "1 of 1 matches" }),
  ).toBeVisible();
  await expect(page.getByLabel("Next SQL lines")).toBeDisabled();
  await page.getByLabel("Previous SQL lines").click();
  await expect(page.getByLabel("Line 201", { exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await page
    .getByRole("button", { name: "Full-screen reader", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(
    page.getByRole("dialog").getByRole("region", { name: "SQL source" }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Full-screen reader", exact: true }),
  ).toBeFocused();
  await page.getByRole("link", { name: /R__refresh.sql/ }).click();
  await expect(page.getByRole("region", { name: "SQL source" })).toContainText(
    "SELECT 2;",
  );
  await expect(page.locator(".sql-file-meta")).toContainText("Repeatable");
  await page.reload();
  await expect(page.getByRole("region", { name: "SQL source" })).toContainText(
    "SELECT 2;",
  );
  await page.getByRole("link", { name: "Return to Migration Info" }).click();
  await expect(
    page
      .locator(".data-table td .badge")
      .filter({ hasText: /^Native New State$/ }),
  ).toHaveCount(2);
  expect(state.errors).toEqual([]);
  expect(state.requests.every((r) => r.startsWith("GET "))).toBe(true);
});

for (const info of ["none", "empty"] as const)
  test(`historical ${info} Info never falls back to a newer capture`, async ({
    page,
  }) => {
    const state = await fixture(page, { info });
    await page.goto("/runs/run?tab=info");
    await expect(
      page.getByText(
        info === "none"
          ? "No native Info capture is available for this run."
          : "No records found",
        { exact: false },
      ),
    ).toBeVisible();
    await expect(
      page.getByRole("cell", { name: "Success", exact: true }),
    ).toHaveCount(0);
    expect(
      state.requests
        .filter((r) => r.includes("flyway-info"))
        .every((r) => r.includes("run_id=run")),
    ).toBe(true);
  });

for (const content of ["missing", "corrupt", "forbidden"] as const)
  test(`SQL ${content} artifact shows explicit error and no stale source`, async ({
    page,
  }) => {
    await fixture(page, { content });
    await page.goto("/runs/run?tab=sql");
    await expect(
      page.getByText(
        content === "corrupt"
          ? /SQL integrity check failed/
          : /SQL unavailable/,
      ),
    ).toBeVisible();
    await expect(page.getByRole("region", { name: "SQL source" })).toHaveCount(
      0,
    );
    await expect(page.getByRole("button", { name: "Copy SQL" })).toBeDisabled();
  });

test("missing historical manifest and unknown deep link do not substitute current uploads", async ({
  page,
}) => {
  await fixture(page, { manifest: "missing" });
  await page.goto("/runs/run?tab=sql&path=unrelated.sql");
  await expect(
    page.getByText(/Historical artifact manifest unavailable/),
  ).toBeVisible();
  await expect(page.getByRole("region", { name: "SQL source" })).toHaveCount(0);
});

test("native Info sorting and pagination preserve unknown states", async ({
  page,
}) => {
  await fixture(page, { info: "unknown", many: true });
  await page.goto("/runs/run?tab=info");
  await expect(
    page.getByRole("cell", { name: "Native New State", exact: true }),
  ).toHaveCount(20);
  await page
    .getByRole("button", { name: "Sort Installed On", exact: true })
    .click();
  await page.getByLabel("Next page", { exact: true }).click();
  await expect(
    page.getByRole("cell", { name: "Native New State", exact: true }),
  ).toHaveCount(5);
  await page
    .getByPlaceholder("Search version, description or filepath…")
    .fill("long 24");
  await expect(
    page.getByRole("cell", { name: "Native New State", exact: true }),
  ).toHaveCount(1);
  await expect(page.getByText("1 results · Page 1 / 1")).toBeVisible();
});

test("SQL search bounds repetitive input and keeps later lines accessible", async ({
  page,
}) => {
  await fixture(page, { repetitive: true });
  await page.goto("/runs/run?tab=sql");
  await expect(page.getByRole("region", { name: "SQL source" })).toBeVisible();
  await page.getByRole("textbox", { name: "Find in SQL" }).fill("x");
  await expect(
    page
      .getByRole("status")
      .filter({ hasText: "first 10,000 shown; narrow search" }),
  ).toBeVisible();
  await expect(page.locator(".sql-active-match")).toHaveText("x");
  await page.getByLabel("Next SQL match").click();
  await expect(
    page.getByRole("status").filter({ hasText: "2 of 10000+ matches" }),
  ).toBeVisible();
});
