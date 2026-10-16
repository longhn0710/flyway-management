import { test, expect, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";

let initialized = false;
const admin = { username: "e2e-admin", password: "E2E-admin-password-2026" };
const targets = [
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
  await page.getByLabel("Tên đăng nhập").fill(admin.username);
  await page.getByRole("textbox", { name: "Mật khẩu" }).fill(admin.password);
  await page.getByRole("button", { name: "Đăng nhập vào console" }).click();
  await expect(
    page.getByText(`${admin.username}`, { exact: true }),
  ).toBeVisible();
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
  await page.getByLabel("Tên hiển thị").fill("E2E Administrator");
  await page.getByLabel("Tên đăng nhập").fill(admin.username);
  await page.getByLabel("Mật khẩu mới").fill(admin.password);
  await page.getByRole("button", { name: "Tạo quản trị viên" }).click();
  await expect(page).toHaveURL(/\/runs$/);
  await expect(
    page.getByRole("heading", { name: "Run History" }),
  ).toBeVisible();
  await expect(page.getByText("Unavailable until Phase 3")).toBeVisible();
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
  await page.getByRole("button", { name: "Đăng xuất" }).click();
  await expect(page).toHaveURL(/\/login$/);
  await page.goto("/access/users?view=private");
  await expect(page).toHaveURL(/\/login\?next=/);
  initialized = true;
});

test.beforeEach(async ({ page }) => {
  if (initialized) await login(page);
});

test("register database and schema, then create and scope a user", async ({
  page,
}) => {
  await page.goto("/databases");
  await page.getByRole("button", { name: "Add database" }).click();
  await page.getByLabel("Display name").fill("E2E Oracle UAT");
  await page.getByLabel("Stable ID").fill("e2e-db");
  await page.getByLabel("Hostname").fill("oracle.invalid");
  await page.getByRole("spinbutton", { name: "Port" }).fill("1521");
  await page.getByLabel("Oracle service / PDB").fill("E2EUAT");
  await page.getByRole("button", { name: "Register target" }).click();
  await expect(page.getByText("E2E Oracle UAT", { exact: true })).toBeVisible();
  await page
    .getByRole("button", { name: "Toggle schemas E2E Oracle UAT" })
    .click();
  await page.getByRole("button", { name: "Register schema" }).click();
  await page.getByLabel("Target schema name").fill("E2E_SCHEMA");
  await page
    .getByRole("button", { name: "Register schema", exact: true })
    .last()
    .click();
  await expect(page.getByText("E2E_SCHEMA", { exact: true })).toBeVisible();

  await page.goto("/access/users");
  await page.getByRole("button", { name: "Thêm người dùng" }).click();
  await page.getByLabel("Provider").selectOption("local");
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
  await page.getByLabel("Tên đăng nhập").fill("e2e-operator");
  await page
    .getByRole("textbox", { name: "Mật khẩu" })
    .fill("E2E-operator-password-2026");
  await page.getByRole("button", { name: "Đăng nhập vào console" }).click();
  await expect(page).toHaveURL(/\/access\/users$/);
  await expect(
    page
      .getByRole("navigation", { name: "Điều hướng chính" })
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
  await page
    .getByRole("row")
    .filter({ hasText: "Active" })
    .last()
    .getByRole("button", { name: "Revoke", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Revoke token", exact: true })
    .last()
    .click();
  await expect(page.getByText("Revoked", { exact: true })).toBeVisible();
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
  await page.goto("/execution-accounts");
  await page.getByLabel("Database target").selectOption("e2e-db");
  await page.getByRole("button", { name: "Register Oracle account" }).click();
  await page.getByLabel("Account label").fill("E2E_DEPLOY");
  await page.getByLabel("Oracle username").fill("E2E_DEPLOY");
  await page.getByLabel("Oracle password").fill("temporary-fixture-password");
  await page.getByLabel("Confirm password").fill("temporary-fixture-password");
  await page.getByRole("checkbox", { name: /E2E_SCHEMA/ }).check();
  await page.getByRole("button", { name: "Register account" }).click();
  await expect(
    page.getByText("E2E_DEPLOY", { exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByText("Encrypted credential", { exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("New Oracle password")).toHaveValue("");
});

test("authentication settings show server configuration and run routes stay unavailable", async ({
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
  await expect(
    page.getByText("No bundle upload or Flyway command is available."),
  ).toBeVisible();
  await page.goto("/runs/e2e-run");
  await expect(
    page.getByText(
      "This application has not executed this migration and has no run details to display.",
    ),
  ).toBeVisible();
});

for (const width of [1440, 390]) {
  test(`authenticated routes render without overflow at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 1000 });
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto("/runs");
    for (const route of targets) {
      await page.goto(route);
      await expect(page.locator("h1")).toBeVisible();
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
      ).toBeLessThanOrEqual(width);
    }
    if (width === 390) {
      await page.getByRole("button", { name: "Mở điều hướng" }).click();
      await page
        .getByRole("dialog")
        .getByRole("link", { name: "Database & Schema" })
        .click();
      await expect(page).toHaveURL("/databases");
    }
    expect(errors).toEqual([]);
  });
}
