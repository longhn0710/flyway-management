import { test, expect as baseExpect, type Page } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const enabled = process.env.FWM_POSTGRES_LAB === "1";
const labEnvPath =
  process.env.FWM_POSTGRES_LAB_ENV ||
  "/home/longhn0710/.local/state/flyway-manager-postgres-lab/lab.env";
const runtimeExe = process.env.FWM_LAB_FLYWAY;
const runtimeManifest = runtimeExe
  ? join(runtimeExe.replace(/\/flyway$/, ""), "flyway-manifest.sha256")
  : "";
const manifestHash =
  "a359f1f0e0a08bd7f5fe3272cc726c3866c0000028fee32a1df56a61f85ecd2c";
const expect = baseExpect.configure({ timeout: 50_000 });

function parsePrivateEnv(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split(/\r?\n/)) {
    const match = line.match(/^([A-Z0-9_]+)=(.*)$/);
    if (match) out[match[1]] = match[2];
  }
  return out;
}

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

async function startApp(port: number, dir: string): Promise<ChildProcess> {
  const config = join(dir, "config.json");
  await writeFile(
    config,
    JSON.stringify({
      listen_address: `127.0.0.1:${port}`,
      data_dir: join(dir, "data"),
      public_url: "",
      flyway: {
        executable: runtimeExe,
        version: "13.10.3",
        sha256: manifestHash,
        timeout_seconds: 45,
      },
    }),
    { mode: 0o600 },
  );
  const child = spawn(resolve(process.cwd(), "../bin/flyway-manager"), [], {
    env: {
      ...process.env,
      FLYWAY_MANAGER_CONFIG: config,
      FLYWAY_MANAGER_LISTEN_ADDRESS: `127.0.0.1:${port}`,
      FLYWAY_MANAGER_DATA_DIR: join(dir, "data"),
      FLYWAY_MANAGER_PUBLIC_URL: "",
    },
    stdio: "ignore",
  });
  const base = `http://127.0.0.1:${port}`;
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null)
      throw new Error("isolated Manager exited during startup");
    try {
      if ((await fetch(`${base}/api/v1/health`)).ok) return child;
    } catch {
      // The loopback listener is still starting.
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  child.kill("SIGTERM");
  throw new Error("timed out starting isolated Manager");
}

async function stopApp(child: ChildProcess) {
  if (child.exitCode !== null || child.killed) return;
  child.kill("SIGTERM");
  await Promise.race([
    new Promise<void>((resolveExit) => child.once("exit", () => resolveExit())),
    new Promise<void>((resolveTimeout) => setTimeout(resolveTimeout, 4_000)),
  ]);
}

async function login(
  page: Page,
  base: string,
  username: string,
  password: string,
) {
  await expect(page).toHaveURL(/\/login$/);
  await page.goto(`${base}/login`);
  await page.getByLabel("Username").fill(username);
  await page.getByRole("textbox", { name: "Password" }).fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/runs$/);
}

test("PostgreSQL 17 live browser acceptance: verify-full registration and native Info", async ({
  page,
}) => {
  test.skip(
    !enabled,
    "set FWM_POSTGRES_LAB=1 to use the disposable PostgreSQL lab",
  );
  test.setTimeout(300_000);
  page.setDefaultTimeout(50_000);
  await page.setViewportSize({ width: 1440, height: 1000 });

  if (!runtimeExe || !runtimeManifest)
    throw new Error(
      "FWM_LAB_FLYWAY must point to the pinned Flyway executable",
    );
  const env = parsePrivateEnv(await readFile(labEnvPath, "utf8"));
  const required = [
    "PGHOST",
    "PGPORT",
    "PGDATABASE",
    "PGUSER",
    "PGPASSWORD",
    "PGSSLROOTCERT",
  ];
  for (const name of required)
    if (!env[name])
      throw new Error(`private lab environment is missing ${name}`);
  const cert = await readFile(env.PGSSLROOTCERT, "utf8");
  const manifest = await readFile(runtimeManifest);
  if (createHash("sha256").update(manifest).digest("hex") !== manifestHash)
    throw new Error(
      "the configured pinned Flyway runtime manifest has changed",
    );

  const dir = await mkdtemp(join(tmpdir(), "fwm-postgresql-live-"));
  await mkdir(join(dir, "evidence"), { recursive: true, mode: 0o700 });
  const output = process.env.FWM_LAB_EVIDENCE
    ? resolve(process.env.FWM_LAB_EVIDENCE)
    : join(
        tmpdir(),
        `fwm-postgresql-info-evidence-${randomBytes(8).toString("hex")}`,
      );
  await mkdir(output, { recursive: true });
  const port = await unusedPort();
  const base = `http://127.0.0.1:${port}`;
  let child: ChildProcess | undefined;
  const pageErrors: string[] = [];
  page.on("pageerror", (e) => pageErrors.push(e.message));
  const adminUser = `pg-admin-${randomBytes(4).toString("hex")}`;
  const adminPassword = randomBytes(24).toString("base64url");
  const viewerUser = `pg-viewer-${randomBytes(4).toString("hex")}`;
  const viewerPassword = randomBytes(24).toString("base64url");
  try {
    child = await startApp(port, dir);
    const authStatus = (
      await (await page.request.get(`${base}/api/v1/auth/status`)).json()
    ).data;
    const token = (
      await readFile(authStatus.bootstrap_token_file, "utf8")
    ).trim();
    const boot = await page.request.post(`${base}/api/v1/auth/bootstrap`, {
      headers: { Origin: base },
      data: {
        username: adminUser,
        display_name: "PostgreSQL Lab Administrator",
        password: adminPassword,
        setup_token: token,
      },
    });
    expect(boot.status()).toBe(201);
    const admin = (await boot.json()).data;
    const capabilities = (
      await (
        await page.request.get(`${base}/api/v1/engine-capabilities`)
      ).json()
    ).data;
    const migrationEnabled =
      capabilities.find(
        (item: { engine: string }) => item.engine === "postgresql",
      )?.migration === true;
    const headers = { Origin: base, "X-CSRF-Token": admin.csrf_token };
    const post = async (path: string, data: unknown, expected = 201) => {
      const response = await page.request.post(`${base}/api/v1${path}`, {
        headers,
        data,
      });
      expect(response.status(), await response.text()).toBe(expected);
      return (await response.json()).data;
    };
    const put = async (path: string, data: unknown, expected = 200) => {
      const response = await page.request.put(`${base}/api/v1${path}`, {
        headers,
        data,
      });
      expect(response.status(), await response.text()).toBe(expected);
      return response.status() === 204 ? null : (await response.json()).data;
    };

    await page.goto(`${base}/instances`);
    await page
      .getByRole("button", { name: "Add instance", exact: true })
      .click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Database engine").selectOption("PostgreSQL");
    await dialog
      .getByLabel("Connection display name")
      .fill("PostgreSQL 17 disposable lab");
    await dialog.getByLabel("Environment").selectOption("DEV");
    await dialog.getByLabel("Host").fill(env.PGHOST);
    await dialog.getByLabel("Port").fill(env.PGPORT);
    await dialog.getByLabel("PostgreSQL database").fill(env.PGDATABASE);
    await dialog.getByLabel("Approved TLS CA bundle (PEM)").fill(cert);
    await dialog.getByLabel("PostgreSQL role").fill(env.PGUSER);
    await dialog.getByLabel("PostgreSQL password").fill(env.PGPASSWORD);
    await dialog.getByRole("button", { name: "Test Connection" }).click();
    await expect(dialog).toContainText("Authentication: verified", {
      timeout: 60_000,
    });
    await expect(dialog).toContainText(`Database: ${env.PGDATABASE}`);
    await expect(dialog).toContainText("TLS: verify-full");
    await expect(dialog.getByText("restricted", { exact: true })).toHaveCount(
      0,
    );
    await dialog
      .getByRole("button", { name: "Choose allowed schemas" })
      .click();
    const appSchema = dialog.getByRole("checkbox", { name: /app/ });
    await expect(appSchema).toBeVisible();
    await appSchema.check();
    await dialog
      .getByRole("checkbox", { name: /Authorize my Flyway Manager user/ })
      .check();
    await dialog.getByRole("button", { name: "Review selection" }).click();
    await expect(dialog).toContainText("PostgreSQL · Connection / Info");
    await expect(dialog).toContainText(
      migrationEnabled ? "Migration available" : "Migration disabled",
    );
    await dialog.getByRole("button", { name: "Save instance" }).click();
    await expect(
      page.getByRole("button", { name: "Add instance", exact: true }),
    ).toBeVisible();

    const targets = (
      await (await page.request.get(`${base}/api/v1/database-targets`)).json()
    ).data;
    const target = targets.find(
      (item: { engine: string }) => item.engine === "postgresql",
    );
    expect(target).toBeTruthy();
    const targetID = target.id as string;
    await put(`/access/users/${admin.user.id}/grants`, {
      grants: [{ target_id: targetID, read: true, operate: true }],
    });
    const schemas = (
      await (
        await page.request.get(
          `${base}/api/v1/database-targets/${targetID}/schemas`,
        )
      ).json()
    ).data;
    const schema = schemas.find(
      (item: { name: string }) => item.name === "app",
    );
    expect(schema).toBeTruthy();
    await page.goto(`${base}/instances/${encodeURIComponent(targetID)}`);
    await expect(
      page.getByRole("heading", {
        name: "PostgreSQL 17 disposable lab",
        exact: true,
      }),
    ).toBeVisible();
    await expect(page.getByText("PostgreSQL", { exact: true })).toBeVisible();
    await expect(page.getByText(env.PGDATABASE, { exact: true })).toBeVisible();
    await expect(
      page.getByText(
        migrationEnabled
          ? /migration capability enabled/
          : /migration unavailable or disabled/,
      ),
    ).toBeVisible();
    if (migrationEnabled) {
      await expect(
        page.getByRole("link", { name: "Run migration", exact: true }),
      ).toBeVisible();
    } else {
      await expect(page.getByText(/Run unavailable/)).toBeVisible();
    }

    await page.goto(
      `${base}/flyway-info?${new URLSearchParams({ target: targetID, schema: schema.id })}`,
    );
    await expect(
      page.getByText("No recorded Flyway Info.", { exact: false }),
    ).toBeVisible();
    const infoPanel = page
      .getByRole("heading", { name: "Refresh Flyway Info (read-only)" })
      .locator("xpath=../..");
    await expect(
      infoPanel.getByRole("combobox", { name: "Execution account" }),
    ).toBeVisible();
    await infoPanel.locator('input[type="file"]').setInputFiles([
      {
        name: "V1__foundation.sql",
        mimeType: "text/plain",
        buffer: Buffer.from(
          "CREATE TABLE foundation_probe(id integer PRIMARY KEY); INSERT INTO foundation_probe VALUES (1);\n",
        ),
      },
      {
        name: "V2__second.sql",
        mimeType: "text/plain",
        buffer: Buffer.from("INSERT INTO foundation_probe VALUES (2);\n"),
      },
      {
        name: "V3__pending.sql",
        mimeType: "text/plain",
        buffer: Buffer.from("SELECT 3;\n"),
      },
    ]);
    await infoPanel
      .getByRole("button", { name: "Upload immutable artifact" })
      .click();
    await expect(infoPanel.getByText(/Artifact: [A-Za-z0-9_-]+/)).toBeVisible();
    await infoPanel.getByLabel("Execution account").selectOption({ index: 1 });
    await infoPanel
      .getByRole("button", { name: "Refresh Flyway Info" })
      .click();
    await expect(
      page.getByRole("cell", { name: "Success", exact: true }),
    ).toHaveCount(2, { timeout: 60_000 });
    await expect(
      page.getByRole("cell", { name: "Pending", exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "foundation", exact: true }).click();
    const details = page.getByRole("dialog");
    await expect(details).toContainText("V1__foundation.sql");
    await details
      .getByRole("button", { name: "Close migration details" })
      .click();
    await page
      .getByRole("link", { name: "Migration history", exact: true })
      .click();
    await expect(page).toHaveURL(/\/runs\?/);
    await page.goBack();
    await expect(
      page.getByRole("cell", { name: "Success", exact: true }),
    ).toHaveCount(2);
    await expect(
      page.getByRole("cell", { name: "Pending", exact: true }),
    ).toBeVisible();
    await page.goForward();
    await expect(page).toHaveURL(/\/runs\?/);

    // A second same-endpoint database is a distinct target/schema identity.
    const second = await post("/connections", {
      target: {
        name: "PostgreSQL 17 second database",
        environment: "DEV",
        host: env.PGHOST,
        port: Number(env.PGPORT),
        service: "",
        transport: "TCPS",
        engine: "postgresql",
        database_name: env.PGSECOND_DATABASE,
        profile: {
          auth_kind: "password",
          tls_mode: "verify-full",
          ca_pem: cert,
        },
      },
      username: env.PGUSER,
      password: env.PGPASSWORD,
      schemas: ["app"],
      grant_current_user: true,
    });
    const secondSchema = second.schemas.find(
      (item: { name: string }) => item.name === "app",
    );
    expect(secondSchema).toBeTruthy();
    expect(secondSchema.id).not.toBe(schema.id);

    const viewer = await post("/access/users", {
      provider: "local",
      username: viewerUser,
      display_name: "PostgreSQL Read Only Viewer",
      password: viewerPassword,
      role: "Viewer",
    });
    await put(`/access/users/${viewer.id}/grants`, {
      grants: [{ target_id: targetID, schema_id: schema.id, read: true }],
    });
    const operatorUser = `pg-operator-${randomBytes(4).toString("hex")}`;
    const operatorPassword = randomBytes(24).toString("base64url");
    const operator = await post("/access/users", {
      provider: "local",
      username: operatorUser,
      display_name: "PG Operator",
      password: operatorPassword,
      role: "Operator",
    });
    await put(`/access/users/${operator.id}/grants`, {
      grants: [
        {
          target_id: targetID,
          schema_id: schema.id,
          read: true,
          operate: true,
        },
      ],
    });
    await page.getByRole("button", { name: "Sign out" }).click();
    await login(page, base, viewerUser, viewerPassword);
    let executionAccountRequests = 0;
    page.on("request", (request) => {
      if (request.url().includes("/execution-accounts"))
        executionAccountRequests++;
    });
    await page.goto(
      `${base}/flyway-info?${new URLSearchParams({ target: targetID, schema: schema.id })}`,
    );
    await expect(
      page.getByRole("heading", { name: "Flyway Info" }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Refresh Flyway Info", exact: true }),
    ).toHaveCount(0);
    expect(executionAccountRequests).toBe(0);
    const viewerRefresh = await page.request.post(
      `${base}/api/v1/database-targets/${targetID}/schemas/${schema.id}/flyway-info/refresh`,
      {
        headers: {
          Origin: base,
          "X-CSRF-Token": (
            await (await page.request.get(`${base}/api/v1/auth/me`)).json()
          ).data.csrf_token,
        },
        data: {
          execution_account_id: "not-authorized",
          artifact_id: "not-authorized",
        },
      },
    );
    expect(viewerRefresh.status()).toBe(403);

    // Operator cannot administer instances; PROD explicit authorization remains a separate gate.
    await page.getByRole("button", { name: "Sign out" }).click();
    await login(page, base, operatorUser, operatorPassword);
    await page.goto(`${base}/instances`);
    await expect(
      page.getByRole("button", { name: "Add instance" }),
    ).toBeDisabled();
    // DEV scope cannot exercise the separate explicit PROD grant gate.

    // Restore Admin for screenshot/evidence capture; the credential dialog is closed throughout.
    await page.getByRole("button", { name: "Sign out" }).click();
    await login(page, base, adminUser, adminPassword);
    await page.goto(
      `${base}/flyway-info?${new URLSearchParams({ target: targetID, schema: schema.id })}`,
    );
    await expect(page.getByLabel("Execution account")).toHaveValue("");
    await expect(
      page.getByRole("cell", { name: "Success", exact: true }),
    ).toHaveCount(2);
    await page.evaluate(() => {
      (document.activeElement as HTMLElement | null)?.blur();
      window.scrollTo(0, 0);
    });
    await page.screenshot({
      path: join(output, "postgresql-info-desktop.png"),
      fullPage: true,
    });
    for (const width of [390, 768, 1024, 1440]) {
      await page.setViewportSize({ width, height: 1000 });
      await expect
        .poll(() =>
          page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth,
          ),
        )
        .toBe(true);
      if (width === 390)
        await page.screenshot({
          path: join(output, "postgresql-info-mobile.png"),
          fullPage: true,
        });
    }
    expect(pageErrors).toEqual([]);
    await writeFile(
      join(output, "browser-live.json"),
      JSON.stringify(
        {
          backend: "real isolated Manager / disposable PostgreSQL 17 lab",
          tls: "verify-full; CA certificate; localhost DNS SAN",
          runtime: "pinned Flyway 13.10.3 manifest verified",
          schema_discovery: { included: "app", restricted_hidden: true },
          native_info: { V1: "Success", V2: "Success", V3: "Pending" },
          second_database_schema_identity_distinct: true,
          viewer_refresh_denied: viewerRefresh.status(),
          operator_instance_admin_disabled: true,
          widths: [390, 768, 1024, 1440],
          page_errors: pageErrors,
        },
        null,
        2,
      ),
      { mode: 0o600 },
    );
  } finally {
    if (child) await stopApp(child);
    await rm(dir, { recursive: true, force: true });
  }
});
