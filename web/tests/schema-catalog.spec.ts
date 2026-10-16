import { test, expect, type Page } from "@playwright/test";
import type { DatabaseTarget, TargetSchema, Role } from "../src/lib/contracts";

const stamp = "2026-10-16T01:00:00Z";
const targets: DatabaseTarget[] = [
  {
    id: "finance",
    name: "Finance UAT",
    environment: "UAT",
    host: "finance.invalid",
    port: 1521,
    service: "FIN",
    transport: "TCP",
    active: true,
    created_at: stamp,
  },
  {
    id: "billing",
    name: "Billing Production",
    environment: "PROD",
    host: "billing.invalid",
    port: 1521,
    service: "BILL",
    transport: "TCP",
    active: true,
    created_at: stamp,
  },
];
const schemas: TargetSchema[] = Array.from({ length: 13 }, (_, i) => ({
  id: `schema-${i}`,
  target_id: i < 11 ? "finance" : "billing",
  name: `APP_${String(i).padStart(2, "0")}`,
  history_schema: "HISTORY",
  history_table: `history_${i}`,
  default_account_id: "",
  active: i !== 10,
  created_at: stamp,
}));
async function fixture(
  page: Page,
  options: {
    role?: Role;
    empty?: boolean;
    fail?: boolean;
    many?: boolean;
  } = {},
) {
  const role = options.role ?? "Admin";
  const requests: string[] = [];
  let failed = options.fail;
  const data = options.many
    ? Array.from({ length: 205 }, (_, i) => ({
        ...schemas[0],
        id: `many-${i}`,
        name: `LARGE_${String(i).padStart(3, "0")}`,
      }))
    : schemas;
  await page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname.replace("/api/v1", "");
    requests.push(`${req.method()} ${path}${url.search}`);
    const send = (value: unknown) => route.fulfill({ json: { data: value } });
    if (req.method() !== "GET") throw new Error(`Unexpected mutation: ${path}`);
    if (path === "/auth/me")
      return send({
        user: {
          id: "fixture",
          username: "acceptance",
          display_name: "Catalog reviewer",
          role,
          active: true,
          provider: "local",
        },
        grants: [
          {
            target_id: "finance",
            schema_id: "",
            read: true,
            operate: role !== "Viewer",
            prod: false,
          },
        ],
        csrf_token: "fixture",
        expires_at: stamp,
      });
    if (path === "/health")
      return send({ status: "ok", database: "ok", capabilities: {} });
    if (path === "/database-targets") return send(options.empty ? [] : targets);
    const match = path.match(/^\/database-targets\/([^/]+)(?:\/schemas)?$/);
    if (match) {
      const t = targets.find((t) => t.id === match[1]);
      if (!path.endsWith("/schemas")) return send(t);
      if (failed && match[1] === "billing")
        return route.fulfill({
          status: 503,
          json: {
            error: {
              code: "UNAVAILABLE",
              message: "Catalog service unavailable",
            },
          },
        });
      const visible = data.filter(
        (s) =>
          s.target_id === match[1] && (role === "Admin" || s.id === "schema-0"),
      );
      const offset = Number(url.searchParams.get("offset") || 0),
        limit = Number(url.searchParams.get("limit") || 200);
      return send(visible.slice(offset, offset + limit));
    }
    if (path === "/execution-accounts" || path.endsWith("/execution-accounts"))
      return send([
        {
          id: "deploy",
          target_id: "finance",
          name: "Finance deploy",
          username: "DEPLOY",
          active: true,
          schema_ids: ["schema-0"],
          credential: {
            configured: true,
            version: 1,
            revoked: false,
            updated_at: stamp,
          },
        },
      ]);
    if (path === "/migration-runs")
      return send([
        {
          id: "run-1",
          run_id: "Run 1",
          status: "SUCCEEDED",
          target_id: "finance",
          schema_id: "schema-0",
          target: targets[0],
          schema: schemas[0],
          account: { name: "Finance deploy", username: "DEPLOY" },
          created_at: stamp,
          source: "WEB",
          migration_outcome: "SUCCEEDED",
          repair_outcome: "NOT_REQUIRED",
        },
      ]);
    if (path === "/migration-runs/run-1")
      return send({
        id: "run-1",
        run_id: "Run 1",
        status: "SUCCEEDED",
        target_id: "finance",
        schema_id: "schema-0",
        target: targets[0],
        schema: schemas[0],
        account: { name: "Finance deploy", username: "DEPLOY" },
        context: {},
        created_at: stamp,
        repair_outcome: "NOT_REQUIRED",
      });
    if (path.endsWith("/repair")) return send({ outcome: "NOT_REQUIRED" });
    if (path.startsWith("/migration-runs/")) return send([]);
    return route.fulfill({
      status: 404,
      json: { error: { code: "NOT_FOUND", message: "Not found" } },
    });
  });
  return {
    requests,
    recover: () => {
      failed = false;
    },
  };
}

test("global catalog aggregates, searches, filters and paginates multiple instances", async ({
  page,
}) => {
  const f = await fixture(page);
  await page.goto("/schemas");
  await expect(page.getByText("13 results · Page 1 / 2")).toBeVisible();
  await expect(page.locator("tbody tr")).toHaveCount(10);
  expect(f.requests).toContain(
    "GET /database-targets/finance/schemas?limit=200&offset=0",
  );
  expect(f.requests).toContain(
    "GET /database-targets/billing/schemas?limit=200&offset=0",
  );
  await page.getByRole("button", { name: "Next page", exact: true }).click();
  await expect(
    page.getByRole("link", { name: "APP_12", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("textbox", { name: "Search", exact: true })
    .fill("history_0");
  await expect(page.locator("tbody tr")).toHaveCount(1);
  await expect(page.getByText("1 results · Page 1 / 1")).toBeVisible();
  await page.getByRole("button", { name: "Clear filters" }).click();
  await page
    .getByRole("combobox", { name: "Instance", exact: true })
    .selectOption("billing");
  await expect(page.locator("tbody tr")).toHaveCount(2);
  await page
    .getByRole("combobox", { name: "Environment", exact: true })
    .selectOption("UAT");
  await expect(
    page.getByText(
      "No schemas match these filters. Change search, instance or environment.",
    ),
  ).toBeVisible();
  await page.getByRole("button", { name: "Clear filters" }).click();
  await page
    .getByRole("combobox", { name: "Environment", exact: true })
    .selectOption("PROD");
  await expect(page.locator("tbody tr")).toHaveCount(2);
  await page.getByRole("link", { name: "APP_11", exact: true }).click();
  await expect(page).toHaveURL(
    /\/instances\/billing\/schemas\?environment=PROD&schema=schema-11$/,
  );
  await expect(
    page.getByRole("heading", { name: "APP_11", exact: true }),
  ).toBeVisible();
});

test("legacy schema-only deep link resolves correct parent despite stale instance/filter, refresh and browser back", async ({
  page,
}) => {
  await fixture(page);
  await page.goto(
    "/schemas?schema=schema-11&instance=finance&q=missing&environment=UAT",
  );
  await expect(page).toHaveURL(
    /\/instances\/billing\/schemas\?schema=schema-11&instance=finance&q=missing&environment=UAT$/,
  );
  await expect(
    page.getByRole("heading", { name: "APP_11", exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "APP_11", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("navigation", { name: "Primary navigation" })
    .getByRole("link", { name: "Databases / Schemas", exact: true })
    .click();
  await expect(page.getByText("13 results · Page 1 / 2")).toBeVisible();
  await page.goBack();
  await expect(
    page.getByRole("heading", { name: "APP_11", exact: true }),
  ).toBeVisible();
});

for (const path of [
  "/schemas?schema=unknown",
  "/schemas?schema=",
  "/instances/finance/schemas?schema=schema-11",
  "/instances/finance/schemas?schema=unknown",
]) {
  test(`unknown or wrong-parent schema is explicit: ${path}`, async ({
    page,
  }) => {
    await fixture(page);
    await page.goto(path);
    await expect(
      page.getByText("Schema not found or unavailable", { exact: true }),
    ).toBeVisible();
    await expect(page.getByText("Choose an instance")).toHaveCount(0);
    await expect(
      page.getByRole("heading", { name: "APP_00", exact: true }),
    ).toHaveCount(0);
  });
}
for (const role of ["Admin", "Operator", "Viewer"] as Role[]) {
  test(`${role} sees only returned authorized schemas and permitted actions/accounts`, async ({
    page,
  }) => {
    const f = await fixture(page, { role });
    await page.goto("/schemas");
    await expect(
      page.getByRole("link", { name: "APP_00", exact: true }),
    ).toBeVisible();
    if (role !== "Admin") {
      await expect(page.locator("tbody tr")).toHaveCount(1);
      await expect(
        page.getByRole("link", { name: "APP_01", exact: true }),
      ).toHaveCount(0);
      await page.goto("/schemas?schema=schema-1");
      await expect(
        page.getByText("Schema not found or unavailable", { exact: true }),
      ).toBeVisible();
    }
    await page.goto("/instances/finance/schemas?schema=schema-0");
    await expect(
      page.getByRole("heading", { name: "APP_00", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Run migration", exact: true }).first(),
    ).toHaveCount(role === "Viewer" ? 0 : 1);
    if (role === "Viewer") {
      await expect(
        page.getByText(
          "Execution account metadata requires operation permission for this schema.",
        ),
      ).toBeVisible();
      expect(f.requests.some((p) => p.includes("execution-accounts"))).toBe(
        false,
      );
    } else
      await expect(
        page.getByText("Oracle username: DEPLOY", { exact: true }),
      ).toBeVisible();
    if (role === "Operator")
      expect(
        f.requests.some((p) => p.startsWith("GET /execution-accounts?")),
      ).toBe(false);
    await page.goto("/instances/billing/schemas?schema=schema-11");
    if (role === "Admin") {
      await expect(
        page.getByRole("heading", { name: "APP_11", exact: true }),
      ).toBeVisible();
      await expect(
        page.getByRole("link", { name: "Run migration", exact: true }),
      ).toHaveCount(0);
    } else
      await expect(
        page.getByText("Schema not found or unavailable", { exact: true }),
      ).toBeVisible();
  });
}

test("empty registry and temporary errors have distinct states and Retry recovers", async ({
  page,
}) => {
  const f = await fixture(page, { fail: true });
  await page.goto("/schemas");
  await expect(
    page.getByText("Unable to load schema catalog", { exact: true }),
  ).toBeVisible();
  await expect(page.locator("tbody tr")).toHaveCount(0);
  f.recover();
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(page.getByText("13 results · Page 1 / 2")).toBeVisible();
  await page.unrouteAll({ behavior: "wait" });
  await fixture(page, { empty: true });
  await page.reload();
  await expect(
    page.getByText(
      "No registered schemas are available to you. Ask an administrator to review instance registration and schema access.",
    ),
  ).toBeVisible();
});

test("schema API pages are fully consumed before local filtering", async ({
  page,
}) => {
  const f = await fixture(page, { many: true });
  await page.goto("/schemas");
  await expect(page.getByText("205 results · Page 1 / 21")).toBeVisible();
  expect(f.requests).toContain(
    "GET /database-targets/finance/schemas?limit=200&offset=200",
  );
  await page
    .getByRole("textbox", { name: "Search", exact: true })
    .fill("LARGE_204");
  await expect(
    page.getByRole("link", { name: "LARGE_204", exact: true }),
  ).toBeVisible();
});

test("instance search/status, overview, migration history and run snapshot use canonical schema links", async ({
  page,
}) => {
  await fixture(page);
  await page.goto("/instances/finance/schemas");
  await expect(page.getByText("11 results · Page 1 / 2")).toBeVisible();
  await page
    .getByRole("combobox", { name: "Registry status", exact: true })
    .selectOption("inactive");
  await expect(
    page.getByRole("link", { name: "APP_10", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("textbox", { name: "Search", exact: true })
    .fill("no-match");
  await expect(
    page.getByText("No schemas match these filters. Change search or status."),
  ).toBeVisible();
  for (const path of ["/instances/finance", "/runs", "/runs/run-1"]) {
    await page.goto(path);
    const link = page
      .getByRole("link", { name: "APP_00", exact: true })
      .first();
    await expect(link).toHaveAttribute(
      "href",
      "/instances/finance/schemas?schema=schema-0",
    );
    await link.click();
    await expect(
      page.getByRole("heading", { name: "APP_00", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Schema migration history", exact: true }),
    ).toHaveAttribute("href", "/runs?target_id=finance&schema_id=schema-0");
  }
});

test("catalog stays usable at desktop, tablet and mobile widths", async ({
  page,
}) => {
  await fixture(page);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  for (const width of [1440, 1024, 768, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    await page.goto("/schemas");
    await expect(page.getByText("13 results · Page 1 / 2")).toBeVisible();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(width);
    await page
      .getByRole("textbox", { name: "Search", exact: true })
      .fill("APP_00");
    await page.getByRole("link", { name: "APP_00", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "APP_00", exact: true }),
    ).toBeVisible();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(width);
  }
  expect(errors).toEqual([]);
});

test("instance history honors URL filters and keeps instance scope", async ({
  page,
}) => {
  const f = await fixture(page);
  await page.goto(
    "/instances/finance/history?status=FAILED&schema_id=schema-0",
  );
  await expect(
    page.getByRole("combobox", { name: "Run status", exact: true }),
  ).toHaveValue("FAILED");
  await expect(
    page.getByRole("combobox", { name: "Database target", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("combobox", { name: "Database target", exact: true }),
  ).toHaveValue("finance");
  expect(
    f.requests.some(
      (p) =>
        p.startsWith("GET /migration-runs?") &&
        p.includes("status=FAILED") &&
        p.includes("target_id=finance") &&
        p.includes("schema_id=schema-0"),
    ),
  ).toBe(true);
  await page
    .getByRole("textbox", { name: "Search", exact: true })
    .fill("bundle");
  await expect(page).toHaveURL(/q=bundle/);
  await page.goBack();
  await expect(
    page.getByRole("textbox", { name: "Search", exact: true }),
  ).not.toHaveValue("bundle");
});

test("catalog pagination survives opening a schema, browser back and refresh", async ({
  page,
}) => {
  await fixture(page);
  await page.goto("/schemas?environment=PROD&page_size=4");
  await expect(page.locator("tbody tr")).toHaveCount(2);
  await page.getByRole("button", { name: "Clear filters" }).click();
  await page.getByRole("button", { name: "Next page", exact: true }).click();
  await expect(page.getByText("13 results · Page 2 / 4")).toBeVisible();
  await page.getByRole("link", { name: "APP_04", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "APP_04", exact: true }),
  ).toBeVisible();
  await page.goBack();
  await expect(page.getByText("13 results · Page 2 / 4")).toBeVisible();
  await page.reload();
  await expect(page.getByText("13 results · Page 2 / 4")).toBeVisible();
  await page
    .getByRole("textbox", { name: "Search", exact: true })
    .fill("APP_00");
  await expect(page.getByText("1 results · Page 1 / 1")).toBeVisible();
  expect(new URL(page.url()).searchParams.has("page")).toBe(false);
});
