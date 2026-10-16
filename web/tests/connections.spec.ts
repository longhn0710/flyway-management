import { test, expect, type Page, type Route } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const admin = {
  username: "connection-wizard-admin",
  password: "Connection-wizard-admin-password-2026",
};
const operator = {
  username: "connection-wizard-operator",
  password: "Connection-wizard-operator-password-2026",
};

type IsolatedServer = {
  baseURL: string;
  dataDir: string;
  processHandle: ChildProcess;
};

async function unusedPort(): Promise<number> {
  const server = createServer();
  await new Promise<void>((resolveListen, reject) =>
    server.once("error", reject).listen(0, "127.0.0.1", resolveListen),
  );
  const port = (server.address() as { port: number }).port;
  await new Promise<void>((resolveClose, reject) =>
    server.close((error) => (error ? reject(error) : resolveClose())),
  );
  return port;
}

async function startIsolatedServer(): Promise<IsolatedServer> {
  const port = await unusedPort();
  const dataDir = await mkdtemp(
    join(tmpdir(), "flyway-manager-connections-e2e-"),
  );
  const baseURL = `http://127.0.0.1:${port}`;
  const processHandle = spawn(
    resolve(process.cwd(), "../bin/flyway-manager"),
    [],
    {
      env: {
        ...process.env,
        FLYWAY_MANAGER_CONFIG: "",
        FLYWAY_MANAGER_LISTEN_ADDRESS: `127.0.0.1:${port}`,
        FLYWAY_MANAGER_DATA_DIR: dataDir,
        FLYWAY_MANAGER_PUBLIC_URL: "",
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  let output = "";
  processHandle.stdout?.on("data", (chunk) => (output += String(chunk)));
  processHandle.stderr?.on("data", (chunk) => (output += String(chunk)));
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    if (processHandle.exitCode !== null)
      throw new Error(`Isolated application exited before starting: ${output}`);
    try {
      const response = await fetch(`${baseURL}/api/v1/health`);
      if (response.ok) return { baseURL, dataDir, processHandle };
    } catch {
      /* Wait for the local listener. */
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 100));
  }
  processHandle.kill("SIGTERM");
  throw new Error(`Timed out starting isolated application: ${output}`);
}

async function stopIsolatedServer(server: IsolatedServer) {
  if (server.processHandle.exitCode === null) {
    server.processHandle.kill("SIGTERM");
    await Promise.race([
      new Promise<void>((resolveExit) =>
        server.processHandle.once("exit", () => resolveExit()),
      ),
      new Promise<void>((resolveTimeout) => setTimeout(resolveTimeout, 3_000)),
    ]);
  }
  await rm(server.dataDir, { recursive: true, force: true });
}

async function bootstrap(page: Page, baseURL: string) {
  const response = await page.request.get(`${baseURL}/api/v1/auth/status`);
  const status = (await response.json()).data as {
    bootstrap_token_file: string;
  };
  const token = (await readFile(status.bootstrap_token_file, "utf8")).trim();
  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Setup token").fill(token);
  await page.getByLabel("Display name").fill("Connection Wizard Admin");
  await page.getByLabel("Username").fill(admin.username);
  await page.getByLabel("New password").fill(admin.password);
  await page.getByRole("button", { name: "Create administrator" }).click();
  await expect(page).toHaveURL(/\/runs$/);
}

async function fulfillJSON(route: Route, data: unknown, status = 200) {
  await route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify({ data }),
  });
}

function discoveredSchemas() {
  return [
    {
      name: "OWN_SCHEMA",
      own: true,
      object_count: 3,
      known_privileges: ["CREATE TABLE"],
      migration_capability: "potential",
      capability_reason: "Login schema with metadata evidence.",
    },
    ...Array.from({ length: 10 }, (_, index) => ({
      name: `APP_SCHEMA_${String(index + 1).padStart(2, "0")}`,
      own: false,
      object_count: index + 1,
      known_privileges: [],
      migration_capability: "potential",
      capability_reason: "Metadata discovery only.",
    })),
    {
      name: "UNCERTAIN_SCHEMA",
      own: false,
      object_count: 0,
      known_privileges: [],
      migration_capability: "unknown",
      capability_reason: "Oracle could not verify DDL capability.",
    },
    {
      name: "LOCKED_SCHEMA",
      own: false,
      object_count: 1,
      known_privileges: [],
      migration_capability: "unsupported",
      capability_reason: "Known read-only schema.",
    },
  ];
}

function discoveryResult(schemas = discoveredSchemas()) {
  return {
    network_state: "reachable",
    authentication_state: "verified",
    actual_user: "APP_LOGIN",
    current_schema: "OWN_SCHEMA",
    database_service: "E2E_SERVICE",
    pdb_name: "E2E_PDB",
    schema_total: schemas.length,
    schemas,
    warnings: [],
  };
}

function target() {
  return {
    id: "e2e-db",
    name: "E2E Oracle",
    environment: "UAT",
    host: "oracle.invalid",
    port: 1521,
    service: "E2EUAT",
    transport: "TCP",
    active: true,
    created_at: "2026-10-16T00:00:00Z",
  };
}

function savedConnection() {
  const database = target();
  const schema = {
    id: "e2e-schema",
    target_id: database.id,
    name: "OWN_SCHEMA",
    history_schema: "OWN_SCHEMA",
    history_table: "flyway_schema_history",
    default_account_id: "e2e-account",
    active: true,
    created_at: "2026-10-16T00:00:00Z",
  };
  const account = {
    id: "e2e-account",
    target_id: database.id,
    name: "E2E deployer",
    username: "APP_LOGIN",
    active: true,
    schema_ids: [schema.id],
    credential: {
      configured: true,
      version: 1,
      updated_at: "2026-10-16T00:00:00Z",
      revoked: false,
    },
    created_at: "2026-10-16T00:00:00Z",
  };
  return { target: database, account, schemas: [schema], timeout_seconds: 20 };
}

test("connection wizard discovers, filters, constrains, and atomically saves schemas", async ({
  browser,
}) => {
  const server = await startIsolatedServer();
  const context = await browser.newContext();
  const page = await context.newPage();
  let savePayload: Record<string, unknown> | undefined;
  try {
    await bootstrap(page, server.baseURL);
    await page.route("**/api/v1/database-targets", (route) =>
      fulfillJSON(route, []),
    );
    await page.route("**/api/v1/connections/test", async (route) => {
      await fulfillJSON(route, discoveryResult());
    });
    await page.route("**/api/v1/connections", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      savePayload = route.request().postDataJSON() as Record<string, unknown>;
      await fulfillJSON(route, savedConnection(), 201);
    });

    await page.goto(`${server.baseURL}/instances`);
    await page.getByRole("button", { name: "Add instance" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByLabel("Oracle username")).toBeVisible();
    await expect(dialog.getByLabel("Oracle password")).toBeVisible();
    await expect(
      dialog.getByText("Used transiently for testing; encrypted only on Save."),
    ).toBeVisible();
    await dialog.getByLabel("Connection display name").fill("E2E Oracle");
    await dialog.getByLabel("Environment").selectOption("PROD");
    await dialog.getByLabel("Host").fill("oracle.invalid");
    await dialog.getByRole("spinbutton", { name: "Port" }).fill("1521");
    await dialog.getByLabel("Oracle service / PDB").fill("E2EUAT");
    await dialog.getByLabel("Oracle username").fill("APP_LOGIN");
    await dialog.getByLabel("Oracle password").fill("transient-secret");
    await dialog.getByRole("button", { name: "Test Connection" }).click();

    await expect(dialog.getByText("Schemas discovered: 13")).toBeVisible();
    const discoveryTable = dialog.getByRole("table");
    await expect(discoveryTable.locator("tbody tr")).toHaveCount(13);
    const discoveryFilter = dialog.getByPlaceholder(
      "Filter discovered schemas…",
    );
    await discoveryFilter.fill("OWN_SCHEMA");
    await expect(discoveryTable.locator("tbody tr")).toHaveCount(1);
    await expect(discoveryTable).toContainText("OWN_SCHEMA");
    await expect(discoveryTable).not.toContainText("APP_SCHEMA_01");
    await discoveryFilter.fill("NO_MATCHING_SCHEMA");
    await expect(discoveryTable.locator("tbody tr")).toHaveCount(0);
    await discoveryFilter.clear();
    await dialog
      .getByRole("button", { name: "Choose allowed schemas" })
      .click();

    const allowed = dialog.getByPlaceholder("Filter allowed schema choices…");
    const checkboxes = dialog.getByRole("checkbox");
    await expect
      .poll(() =>
        checkboxes.evaluateAll(
          (elements) =>
            elements.filter((element) => (element as HTMLInputElement).checked)
              .length,
        ),
      )
      .toBe(1);
    await expect(
      dialog.getByRole("checkbox", { name: /OWN_SCHEMA/ }),
    ).toBeChecked();
    await expect(
      dialog.getByRole("checkbox", { name: /APP_SCHEMA_01/ }),
    ).not.toBeChecked();
    await allowed.fill("LOCKED_SCHEMA");
    await expect(
      dialog.getByRole("checkbox", { name: /LOCKED_SCHEMA/ }),
    ).toBeDisabled();
    await allowed.clear();

    await dialog.getByRole("checkbox", { name: /OWN_SCHEMA/ }).uncheck();
    await dialog.getByRole("checkbox", { name: /APP_SCHEMA_01/ }).check();
    await dialog.getByRole("checkbox", { name: /UNCERTAIN_SCHEMA/ }).check();
    const review = dialog.getByRole("button", { name: "Review selection" });
    await expect(review).toBeDisabled();
    const ackUnknown = dialog.getByRole("checkbox", {
      name: /I acknowledge that selected schemas/,
    });
    await ackUnknown.check();
    await expect(review).toBeEnabled();
    const grantCurrent = dialog.getByRole("checkbox", {
      name: /Authorize my Flyway Manager user to run migrations/,
    });
    await expect(grantCurrent).not.toBeChecked();
    await grantCurrent.check();
    const grantProd = dialog.getByRole("checkbox", {
      name: /Explicitly authorize my user for PROD execution/,
    });
    await expect(grantProd).toBeVisible();
    await expect(grantProd).not.toBeChecked();
    await grantProd.check();
    await review.click();
    await expect(dialog).toContainText("Review & register instance");
    const save = dialog.getByRole("button", { name: "Save instance" });
    await expect(save).toBeEnabled();
    await expect(dialog).toContainText("Current-user operate grants: Requested.");
    await expect(dialog).toContainText("Explicit PROD grant: Requested");
    await dialog.getByRole("button", { name: "Back", exact: true }).click();
    await expect(dialog.getByRole("button", { name: "Review selection" })).toBeEnabled();
    await dialog.getByRole("button", { name: "Review selection" }).click();
    await expect(dialog.getByRole("button", { name: "Save instance" })).toBeEnabled();
    await save.click();
    await expect(dialog).toHaveCount(0);

    expect(savePayload).toEqual({
      target: {
        name: "E2E Oracle",
        environment: "PROD",
        host: "oracle.invalid",
        port: 1521,
        service: "E2EUAT",
        transport: "TCP",
      },
      account_id: "",
      username: "APP_LOGIN",
      password: "transient-secret",
      timeout_seconds: 20,
      schemas: ["APP_SCHEMA_01", "UNCERTAIN_SCHEMA"],
      acknowledge_unknown: true,
      grant_current_user: true,
      grant_prod: true,
    });
  } finally {
    await context.close().catch(() => undefined);
    await stopIsolatedServer(server);
  }
});

test("empty discovery leaves schema selection disabled", async ({
  browser,
}) => {
  const server = await startIsolatedServer();
  const context = await browser.newContext();
  const page = await context.newPage();
  try {
    await bootstrap(page, server.baseURL);
    await page.route("**/api/v1/connections/test", (route) =>
      fulfillJSON(route, discoveryResult([])),
    );
    await page.goto(`${server.baseURL}/instances`);
    await page.getByRole("button", { name: "Add instance" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Connection display name").fill("Empty Oracle");
    await dialog.getByLabel("Host").fill("oracle.invalid");
    await dialog.getByLabel("Oracle service / PDB").fill("E2E");
    await dialog.getByLabel("Oracle username").fill("EMPTY_LOGIN");
    await dialog.getByLabel("Oracle password").fill("temporary-secret");
    await dialog.getByRole("button", { name: "Test Connection" }).click();
    await expect(dialog.getByText("Schemas discovered: 0")).toBeVisible();
    await expect(dialog.getByRole("table").locator("tbody tr")).toHaveCount(0);
    await dialog
      .getByRole("button", { name: "Choose allowed schemas" })
      .click();
    await expect(
      dialog.getByText(/Select at least one potentially usable schema/),
    ).toBeVisible();
    await expect(
      dialog.getByRole("checkbox", {
        name: /Authorize my Flyway Manager user to run migrations/,
      }),
    ).not.toBeChecked();
    await expect(
      dialog.getByRole("button", { name: "Review selection" }),
    ).toBeDisabled();
  } finally {
    await context.close().catch(() => undefined);
    await stopIsolatedServer(server);
  }
});

test("authentication failure stays on connection step and shows discovery error", async ({
  browser,
}) => {
  const server = await startIsolatedServer();
  const context = await browser.newContext();
  const page = await context.newPage();
  try {
    await bootstrap(page, server.baseURL);
    await page.route("**/api/v1/connections/test", (route) =>
      fulfillJSON(route, {
        ...discoveryResult([]),
        authentication_state: "failed",
        error_code: "ORACLE_AUTHENTICATION_FAILED",
        error_message: "Oracle rejected these credentials.",
      }),
    );
    await page.goto(`${server.baseURL}/instances`);
    await page.getByRole("button", { name: "Add instance" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Connection display name").fill("Rejected Oracle");
    await dialog.getByLabel("Host").fill("oracle.invalid");
    await dialog.getByLabel("Oracle service / PDB").fill("E2E");
    await dialog.getByLabel("Oracle username").fill("BAD_LOGIN");
    await dialog.getByLabel("Oracle password").fill("wrong-secret");
    await dialog.getByRole("button", { name: "Test Connection" }).click();
    await expect(dialog).toContainText("Oracle rejected these credentials.");
    await expect(dialog).toContainText("Step 1 of 4");
    await expect(
      dialog.getByRole("button", { name: "Test Connection" }),
    ).toBeVisible();
    await expect(
      dialog.getByRole("button", { name: "Choose allowed schemas" }),
    ).toHaveCount(0);
  } finally {
    await context.close().catch(() => undefined);
    await stopIsolatedServer(server);
  }
});

test("saved connection account can be edited and its credential rotated", async ({
  browser,
}) => {
  const server = await startIsolatedServer();
  const context = await browser.newContext();
  const page = await context.newPage();
  let savePayload: Record<string, unknown> | undefined;
  try {
    await bootstrap(page, server.baseURL);
    const saved = savedConnection();
    await page.route(
      /\/api\/v1\/database-targets\/e2e-db(?:\/schemas)?(?:\?.*)?$/,
      (route) =>
        fulfillJSON(
          route,
          route.request().url().includes("/schemas")
            ? saved.schemas
            : saved.target,
        ),
    );
    await page.route(
      /\/api\/v1\/execution-accounts\?target_id=e2e-db(?:&.*)?$/,
      (route) => fulfillJSON(route, [saved.account]),
    );
    await page.route(
      /\/api\/v1\/connections\/e2e-db(?:\?.*)?$/,
      async (route) => {
        if (route.request().method() === "PUT") {
          savePayload = route.request().postDataJSON() as Record<
            string,
            unknown
          >;
          await fulfillJSON(route, saved);
        } else {
          await fulfillJSON(route, saved);
        }
      },
    );
    await page.route("**/api/v1/connections/e2e-db/test", (route) =>
      fulfillJSON(
        route,
        discoveryResult([
          {
            name: "OWN_SCHEMA",
            own: true,
            object_count: 3,
            known_privileges: ["CREATE TABLE"],
            migration_capability: "potential",
            capability_reason: "Login schema with metadata evidence.",
          },
        ]),
      ),
    );

    await page.goto(`${server.baseURL}/instances/e2e-db/connections`);
    await page
      .getByRole("button", { name: "Edit account / Allowed schemas" })
      .click({ timeout: 5_000 });
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByLabel("Oracle username")).toHaveValue("APP_LOGIN");
    await expect(dialog.getByLabel("Oracle username")).toHaveAttribute(
      "readonly",
      "",
    );
    await expect(dialog.getByLabel("Oracle password")).toHaveValue("");
    await expect(
      dialog.getByText(/Enter a new password to rotate on Save/),
    ).toBeVisible();
    await dialog.getByLabel("Oracle password").fill("rotated-secret");
    await dialog.getByRole("button", { name: "Test Connection" }).click();
    await dialog
      .getByRole("button", { name: "Choose allowed schemas" })
      .click();
    await expect(
      dialog.getByRole("checkbox", { name: /OWN_SCHEMA/ }),
    ).toBeChecked();
    await dialog.getByRole("button", { name: "Review selection" }).click();
    await dialog.getByRole("button", { name: "Save instance" }).click();
    await expect(dialog).toHaveCount(0);

    expect(savePayload).toMatchObject({
      account_id: "e2e-account",
      username: "APP_LOGIN",
      password: "rotated-secret",
      schemas: ["OWN_SCHEMA"],
    });
  } finally {
    await context.close().catch(() => undefined);
    await stopIsolatedServer(server);
  }
});

test("non-admin users cannot start the connection wizard", async ({
  browser,
}) => {
  const server = await startIsolatedServer();
  const context = await browser.newContext();
  const page = await context.newPage();
  try {
    await bootstrap(page, server.baseURL);
    const identity = (
      await (await page.request.get(`${server.baseURL}/api/v1/auth/me`)).json()
    ).data as { csrf_token: string };
    const created = await page.request.post(
      `${server.baseURL}/api/v1/access/users`,
      {
        data: {
          provider: "local",
          username: operator.username,
          display_name: "Connection Wizard Operator",
          password: operator.password,
          role: "Operator",
        },
        headers: {
          Origin: server.baseURL,
          "X-CSRF-Token": identity.csrf_token,
        },
      },
    );
    expect(created.status()).toBe(201);

    await page.getByRole("button", { name: "Sign out" }).click();
    await expect(page).toHaveURL(/\/login$/);
    await page.goto(`${server.baseURL}/login`);
    await page.getByLabel("Username").fill(operator.username);
    await page.getByRole("textbox", { name: "Password" }).fill(operator.password);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page).toHaveURL(/\/runs$/);
    await page.goto(`${server.baseURL}/databases`);
    await expect(
      page.getByRole("button", { name: "Add instance" }),
    ).toBeDisabled();
    await expect(
      page.getByText(
        "Read-only view. Target and schema administration requires Admin.",
      ),
    ).toBeVisible();
  } finally {
    await context.close().catch(() => undefined);
    await stopIsolatedServer(server);
  }
});

test("future engine preview disables connection actions and sends no API request", async ({
  browser,
}) => {
  const server = await startIsolatedServer();
  const context = await browser.newContext();
  const page = await context.newPage();
  const requests: string[] = [];
  try {
    await bootstrap(page, server.baseURL);
    await page.route("**/api/v1/connections**", async (route) => {
      requests.push(route.request().url());
      await route.continue();
    });
    await page.goto(`${server.baseURL}/instances`);
    await page.getByRole("button", { name: "Add instance" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Database engine").selectOption("PostgreSQL");
    await expect(dialog).toContainText("Future engine");
    await expect(
      dialog.getByRole("textbox", { name: "Host", exact: true }),
    ).toBeDisabled();
    await expect(
      dialog.getByRole("button", { name: "Test Connection" }),
    ).toBeDisabled();
    await expect(
      dialog.getByRole("button", { name: "Save instance" }),
    ).toHaveCount(0);
    expect(requests).toEqual([]);
  } finally {
    await context.close().catch(() => undefined);
    await stopIsolatedServer(server);
  }
});
