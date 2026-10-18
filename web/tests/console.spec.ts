import { test, expect, type Page } from "@playwright/test";
import { mkdir, readFile } from "node:fs/promises";
import { resolve } from "node:path";

let initialized = false;
const admin = { username: "e2e-admin", password: "E2E-admin-password-2026" };
const targets = [
  "/home",
  "/instances",
  "/instances/e2e-db",
  "/instances/e2e-db/schemas",
  "/instances/e2e-db/connections",
  "/instances/e2e-db/history?status=FAILED",
  "/schemas",
  "/runs",
  "/runs/new",
  "/runs/e2e-run",
  "/databases",
  "/databases/e2e-db",
  "/execution-accounts",
  "/access/users",
  "/access/service-accounts",
  "/settings/authentication",
];

test.describe.configure({ mode: "serial" });
async function login(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Username").fill(admin.username);
  await page.getByRole("textbox", { name: "Password" }).fill(admin.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/runs$/);
}

test("first-run setup is one-time; sessions are server-authoritative", async ({
  page,
}) => {
  await page.goto("/login");
  const statusResponse = await page.request.get("/api/v1/auth/status");
  expect(statusResponse.ok()).toBeTruthy();
  const status = (await statusResponse.json()).data as {
    bootstrap_required: boolean;
    bootstrap_token_file: string;
  };
  expect(status.bootstrap_required).toBe(true);
  const setupToken = (
    await readFile(status.bootstrap_token_file, "utf8")
  ).trim();
  await page.getByLabel("Setup token").fill(setupToken);
  await page.getByLabel("Display name").fill("E2E Administrator");
  await page.getByLabel("Username").fill(admin.username);
  await page.getByLabel("New password").fill(admin.password);
  await page.getByRole("button", { name: "Create administrator" }).click();
  await expect(page).toHaveURL(/\/runs$/);
  await expect(
    page.getByRole("heading", { name: "Migration history" }),
  ).toBeVisible();
  await expect(
    page.getByText("No migration runs match these filters."),
  ).toBeVisible();
  await page.reload();
  await expect(page.getByText("E2E Administrator · Admin")).toBeVisible();
  const identity = (await (await page.request.get("/api/v1/auth/me")).json())
    .data as { csrf_token: string };
  const sessions = (
    await (await page.request.get("/api/v1/auth/sessions")).json()
  ).data as { id: string; current: boolean }[];
  const current = sessions.find((session) => session.current)!;
  const revoked = await page.request.delete(
    `/api/v1/auth/sessions/${current.id}`,
    {
      headers: {
        Origin: new URL(page.url()).origin,
        "X-CSRF-Token": identity.csrf_token,
      },
    },
  );
  expect(revoked.ok()).toBe(true);
  await page.reload();
  await expect(page).toHaveURL(/\/login\?next=/);
  await login(page);
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login$/);
  await page.goto("/access/users?view=private");
  await expect(page).toHaveURL(/\/login\?next=/);
  initialized = true;
});

test.beforeEach(async ({ page }) => {
  if (initialized) await login(page);
});

test("connect a database, then create and scope a user", async ({ page }) => {
  await page.route("**/api/v1/connections/test", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          network_state: "verified",
          authentication_state: "verified",
          actual_user: "E2E_DEPLOY",
          current_schema: "E2E_SCHEMA",
          database_service: "E2EUAT",
          pdb_name: "E2EUAT",
          schema_total: 1,
          schemas: [
            {
              name: "E2E_SCHEMA",
              own: true,
              object_count: 1,
              known_privileges: ["CREATE TABLE"],
              migration_capability: "potential",
              capability_reason: "Login schema with metadata evidence.",
            },
          ],
          warnings: [],
        },
      }),
    }),
  );
  await page.route("**/api/v1/connections", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({ data: {} }),
    });
  });
  await page.goto("/instances");
  await page.getByRole("button", { name: "Add instance" }).click();
  const connection = page.getByRole("dialog");
  await connection.getByLabel("Connection display name").fill("E2E Oracle UAT");
  await connection.getByLabel("Environment").selectOption("UAT");
  await connection.getByLabel("Host").fill("oracle.invalid");
  await connection.getByRole("spinbutton", { name: "Port" }).fill("1521");
  await connection.getByLabel("Oracle service / PDB").fill("E2EUAT");
  await connection.getByLabel("Oracle username").fill("E2E_DEPLOY");
  await connection.getByLabel("Oracle password").fill("temporary-e2e-password");
  await connection.getByRole("button", { name: "Test Connection" }).click();
  await connection
    .getByRole("button", { name: "Choose allowed schemas" })
    .click();
  await expect(
    connection.getByRole("checkbox", { name: /E2E_SCHEMA/ }),
  ).toBeChecked();
  await connection.getByRole("button", { name: "Review selection" }).click();
  await connection.getByRole("button", { name: "Save instance" }).click();
  await expect(connection).toHaveCount(0);

  const identity = (await (await page.request.get("/api/v1/auth/me")).json())
    .data as { csrf_token: string };
  const headers = {
    Origin: new URL(page.url()).origin,
    "X-CSRF-Token": identity.csrf_token,
  };
  const registeredTarget = await page.request.post("/api/v1/database-targets", {
    headers,
    data: {
      id: "e2e-db",
      name: "E2E Oracle UAT",
      environment: "UAT",
      host: "oracle.invalid",
      port: 1521,
      service: "E2EUAT",
      transport: "TCP",
    },
  });
  expect(registeredTarget.status()).toBe(201);
  const registeredSchema = await page.request.post(
    "/api/v1/database-targets/e2e-db/schemas",
    { headers, data: { name: "E2E_SCHEMA" } },
  );
  expect(registeredSchema.status()).toBe(201);
  const registeredSchemaData = (await registeredSchema.json()).data as {
    id: string;
  };
  const executionAccount = await page.request.post(
    "/api/v1/execution-accounts",
    {
      headers,
      data: {
        target_id: "e2e-db",
        name: "E2E_DEPLOY",
        username: "E2E_DEPLOY",
        password: "temporary-fixture-password",
        schema_ids: [registeredSchemaData.id],
      },
    },
  );
  expect(executionAccount.status()).toBe(201);
  await page.reload();
  await expect(page.getByText("E2E Oracle UAT", { exact: true })).toBeVisible();
  await page.goto("/instances/e2e-db/schemas");
  await expect(page.getByText("E2E_SCHEMA", { exact: true })).toBeVisible();

  await page.goto("/access/users");
  await page.getByRole("button", { name: "Add user" }).click();
  await page.getByLabel("Provider", { exact: true }).selectOption("local");
  await page.getByLabel("Username").fill("e2e-operator");
  await page.getByLabel("Display name").last().fill("E2E Operator");
  await page.getByLabel("Initial password").fill("E2E-operator-password-2026");
  await page.getByLabel("Base role").last().selectOption("Operator");
  await page.getByRole("button", { name: "Create user" }).click();
  await expect(page.getByText("E2E Operator", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Manage" }).last().click();
  await page
    .getByRole("checkbox", { name: "Read authorized data" })
    .first()
    .click();
  await expect(
    page.getByRole("checkbox", { name: "Read authorized data" }).first(),
  ).toBeChecked();
  await expect(page.getByText("Could not save grants")).toHaveCount(0);
  await expect(page.getByText("E2E Operator", { exact: true })).toBeVisible();

  await page.goto("/login?next=%2Faccess%2Fusers");
  await page.getByLabel("Username").fill("e2e-operator");
  await page
    .getByRole("textbox", { name: "Password" })
    .fill("E2E-operator-password-2026");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/access\/users$/);
  await expect(
    page
      .getByRole("navigation", { name: "Primary navigation" })
      .getByRole("link", { name: "Người dùng & Quyền" }),
  ).toHaveCount(0);
  await expect(
    page.getByText(
      "Your role can view identity details only. User administration requires Admin.",
    ),
  ).toBeVisible();
});

test("service account grants, one-time token display, rotation and revocation", async ({
  page,
  request,
}) => {
  await page.goto("/access/service-accounts");
  await page.getByRole("button", { name: "Create service account" }).click();
  await page.getByLabel("Identifier").fill("e2e-ci");
  await page.getByLabel("Purpose").fill("E2E test client");
  await page
    .getByLabel("Owner")
    .selectOption({ label: "E2E Administrator (e2e-admin)" });
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await expect(page.getByText("e2e-ci", { exact: true }).first()).toBeVisible();
  await page
    .getByRole("checkbox", { name: "Read authorized data" })
    .first()
    .click();
  await page.getByLabel("Token name").fill("e2e-token");
  await page
    .getByLabel("Expiration")
    .fill(new Date(Date.now() + 7 * 86400000).toISOString().slice(0, 16));
  await page
    .getByLabel("Issue legacy token without project Deployment Target scopes")
    .check();
  await page.getByRole("button", { name: "Create token" }).click();
  const once = page.getByRole("dialog");
  await expect(once).toContainText("Copy your API token now");
  await expect(once.locator("code.token-example")).not.toBeEmpty();
  const originalToken = (
    await once.locator("code.token-example").innerText()
  ).trim();
  expect(
    (
      await request.get("/api/v1/database-targets", {
        headers: { Authorization: `Bearer ${originalToken}` },
      })
    ).status(),
  ).toBe(200);
  await once.getByRole("button", { name: "Done" }).click();
  await expect(page.getByText("e2e-token", { exact: true })).toBeVisible();
  await page
    .getByRole("button", { name: "Rotate", exact: true })
    .first()
    .click();
  await page
    .getByLabel("Replacement expiration")
    .fill(new Date(Date.now() + 30 * 86400000).toISOString().slice(0, 16));
  await page.getByRole("button", { name: "Rotate token", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText(
    "Copy your API token now",
  );
  const replacementToken = (
    await page.getByRole("dialog").locator("code.token-example").innerText()
  ).trim();
  const replacementPrefix = await page
    .getByRole("dialog")
    .locator("p.small code")
    .innerText();
  const replacementRow = page
    .getByRole("row")
    .filter({ has: page.getByText(replacementPrefix, { exact: true }) });
  expect(
    (
      await request.get("/api/v1/database-targets", {
        headers: { Authorization: `Bearer ${originalToken}` },
      })
    ).status(),
  ).toBe(401);
  expect(
    (
      await request.get("/api/v1/database-targets", {
        headers: { Authorization: `Bearer ${replacementToken}` },
      })
    ).status(),
  ).toBe(200);
  await page.getByRole("dialog").getByRole("button", { name: "Done" }).click();
  await expect(
    page.getByRole("button", { name: "Revoke", exact: true }),
  ).toHaveCount(2);
  await replacementRow
    .getByRole("button", { name: "Revoke", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Revoke token", exact: true })
    .last()
    .click();
  await expect(
    replacementRow.getByText("Revoked", { exact: true }),
  ).toBeVisible();
  expect(
    (
      await request.get("/api/v1/database-targets", {
        headers: { Authorization: `Bearer ${replacementToken}` },
      })
    ).status(),
  ).toBe(401);
});

test("execution account credentials are write-only and encrypted metadata is live", async ({
  page,
}) => {
  await page.goto("/instances/e2e-db/connections");
  const accountRow = page.getByRole("row").filter({ hasText: "E2E_DEPLOY" });
  await expect(accountRow).toContainText("Saved · version 1 (encrypted at rest)");
  await accountRow.getByRole("button", { name: "Credential settings" }).click();
  await expect(page.getByLabel("Oracle username")).toHaveValue("E2E_DEPLOY");
  await expect(page.getByLabel("Oracle username")).toBeDisabled();
  await expect(
    page.getByText(/rotate credentials and change allowed schemas/i),
  ).toBeVisible();
  await expect(page.getByLabel("New Oracle password")).toHaveCount(0);
});

test("authentication settings show server configuration and unavailable runtime is explicit", async ({
  page,
}) => {
  await page.goto("/settings/authentication");
  await expect(
    page.getByRole("heading", { name: "Authentication providers" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Save configuration" }),
  ).toBeDisabled();
  await expect(
    page.getByText(
      "Configure local login, verified LDAP/Active Directory and OIDC sign-in.",
    ),
  ).toBeVisible();
  await page.goto("/runs/new");
  await expect(page.getByText("Flyway runtime unavailable")).toBeVisible();
  await page.goto("/runs/e2e-run");
  await expect(page.getByText("Resource not found.")).toBeVisible();
});

test("authenticated routes render without overflow at reviewed viewport widths", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const evidenceDir = process.env.FLYWAY_MANAGER_EVIDENCE_DIR
    ? resolve(process.env.FLYWAY_MANAGER_EVIDENCE_DIR, "route-sweep")
    : resolve(process.cwd(), "../docs/evidence/stitch-implementation");
  await mkdir(evidenceDir, { recursive: true });
  for (const width of [1440, 1024, 768, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    await page.goto("/runs");
    for (const route of targets) {
      await page.goto(route);
      await expect(page.locator("h1")).toBeVisible();
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
      ).toBeLessThanOrEqual(width);
    }
    await page.goto("/instances");
    await expect(page.getByRole("heading", { name: "Instances" })).toBeVisible();
    await page.screenshot({
      path: resolve(evidenceDir, `instances-${width}.png`),
      fullPage: true,
    });
    await page.getByRole("link", { name: "E2E Oracle UAT" }).click();
    await expect(page).toHaveURL(/\/instances\/e2e-db$/);
    await page.screenshot({
      path: resolve(evidenceDir, `instance-overview-${width}.png`),
      fullPage: true,
    });
    await page
      .getByRole("navigation", { name: "Instance sections" })
      .getByRole("link", { name: "Migration History" })
      .click();
    await expect(page).toHaveURL(/\/instances\/e2e-db\/history$/);
    await page.screenshot({
      path: resolve(evidenceDir, `instance-history-${width}.png`),
      fullPage: true,
    });
    await page.goBack();
    await expect(page).toHaveURL(/\/instances\/e2e-db$/);
    await page.goForward();
    await expect(page).toHaveURL(/\/instances\/e2e-db\/history$/);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(width);
    if (width === 390) {
      await page.getByRole("button", { name: "Open navigation" }).click();
      await page
        .getByRole("dialog")
        .getByRole("link", { name: "Instances" })
        .click();
      await expect(page).toHaveURL("/instances");
    }
  }
  expect(errors).toEqual([]);
});
