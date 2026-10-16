import { test, expect, type Page } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const admin = {
  username: "execution-ui-admin",
  password: "Execution-ui-admin-password-2026",
};
const serverTimeout = 15_000;

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

async function startIsolatedServer(configPath = "") {
  const port = await unusedPort();
  const dataDir = await mkdtemp(
    join(tmpdir(), "flyway-manager-execution-e2e-"),
  );
  const baseURL = `http://127.0.0.1:${port}`;
  const processHandle: ChildProcess = spawn(
    resolve(process.cwd(), "../bin/flyway-manager"),
    [],
    {
      env: {
        ...process.env,
        FLYWAY_MANAGER_CONFIG: configPath,
        FLYWAY_MANAGER_LISTEN_ADDRESS: `127.0.0.1:${port}`,
        FLYWAY_MANAGER_DATA_DIR: dataDir,
        FLYWAY_MANAGER_PUBLIC_URL: "",
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  let output = "";
  processHandle.stdout?.on("data", (chunk) => {
    output += String(chunk);
  });
  processHandle.stderr?.on("data", (chunk) => {
    output += String(chunk);
  });
  const deadline = Date.now() + serverTimeout;
  while (Date.now() < deadline) {
    if (processHandle.exitCode !== null)
      throw new Error(`Isolated application exited before starting: ${output}`);
    try {
      const response = await fetch(`${baseURL}/api/v1/health`);
      if (response.ok) return { baseURL, dataDir, processHandle };
    } catch {
      /* wait for the local listener */
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 100));
  }
  processHandle.kill("SIGTERM");
  throw new Error(`Timed out starting isolated application: ${output}`);
}

async function createFakeRuntime(scenario: "success" | "repair") {
  const runtimeDir = await mkdtemp(
    join(tmpdir(), "flyway-manager-fake-runtime-"),
  );
  const root = join(runtimeDir, "distribution");
  const marker = join(runtimeDir, "commands.txt");
  await mkdir(join(root, "drivers"), { recursive: true });
  await mkdir(join(root, "lib"), { recursive: true });
  await mkdir(join(root, "jre", "bin"), { recursive: true });
  const version = "10.20.0";
  const infoPending = `printf '%s\\n' '{"operation":"info","schemaVersion":null,"migrations":[{"category":"Versioned","version":"1","description":"browser","type":"SQL","filepath":"/private/flyway-run-fixture/sql/V1__browser.sql","state":"Pending","installedOn":"","installedBy":"","installedRank":1}],"flywayVersion":"${version}"}'`;
  const infoSuccess = `printf '%s\\n' '{"operation":"info","schemaVersion":null,"migrations":[{"category":"Versioned","version":"1","description":"browser","type":"SQL","script":"V1__browser.sql","state":"Success","installedOn":"2026-10-16T00:00:00Z","installedBy":"BROWSER_DEPLOY","installedRank":1,"checksum":123}],"flywayVersion":"${version}"}'`;
  const infoFailed = `printf '%s\\n' '{"operation":"info","schemaVersion":null,"migrations":[{"category":"Versioned","version":"1","description":"browser","type":"SQL","script":"V1__browser.sql","state":"Failed","installedOn":"","installedBy":"BROWSER_DEPLOY","installedRank":1}],"flywayVersion":"${version}"}'`;
  const validateFailed = `printf '%s\\n' '{"operation":"validate","validationSuccessful":false,"invalidMigrations":[{"version":"1","filepath":"V1__browser.sql","errorDetails":{"errorCode":"FAILED_VERSIONED_MIGRATION"}}],"validateCount":1,"flywayVersion":"${version}"}'`;
  const validateSuccess = `printf '%s\\n' '{"operation":"validate","validationSuccessful":true,"invalidMigrations":[],"validateCount":1,"flywayVersion":"${version}"}'`;
  const migrateFailure = `printf '%s\\n' '{"operation":"migrate","migrationsExecuted":0,"migrations":[],"errorDetails":{"message":"deterministic fake migration failure"},"flywayVersion":"${version}"}'; exit 1`;
  const migrateSuccess = `printf '%s\\n' '{"operation":"migrate","migrationsExecuted":1,"migrations":[{"version":"1","script":"V1__browser.sql"}],"flywayVersion":"${version}"}'`;
  const repairSuccess = `printf '%s\\n' '{"operation":"repair","repairActions":["Removed failed migration"],"migrationsRemoved":[{"version":"1"}],"migrationsDeleted":[],"migrationsAligned":[],"flywayVersion":"${version}"}'`;
  const script =
    [
      "#!/bin/sh",
      `if [ "$1" = "-v" ]; then printf '%s\\n' 'Flyway Community Edition ${version} by Redgate'; exit 0; fi`,
      `MARKER='${marker}'`,
      'command="$1"',
      'printf \'%s\\n\' "$command" >> "$MARKER"',
      'case "$command" in',
      `info) ${scenario === "repair" ? `if grep -q '^migrate$' "$MARKER" && ! grep -q '^repair$' "$MARKER"; then ${infoFailed}; else ${infoPending}; fi` : `if grep -q '^migrate$' "$MARKER"; then ${infoSuccess}; else ${infoPending}; fi`} ;;`,
      `validate) ${scenario === "repair" ? `if grep -q '^migrate$' "$MARKER" && ! grep -q '^repair$' "$MARKER"; then ${validateFailed}; else ${validateSuccess}; fi` : validateSuccess} ;;`,
      `migrate) ${scenario === "repair" ? migrateFailure : migrateSuccess} ;;`,
      `repair) ${repairSuccess} ;;`,
      "*) exit 2 ;;",
      "esac",
    ].join("\n") + "\n";
  const files: Record<string, Buffer> = {
    flyway: Buffer.from(script),
    "drivers/ojdbc11-test.jar": Buffer.from(
      "test-only oracle driver placeholder",
    ),
    "lib/flyway-database-oracle-test.jar": Buffer.from(
      "test-only flyway oracle module placeholder",
    ),
    "jre/bin/java": Buffer.from("#!/bin/sh\nexit 0\n"),
  };
  for (const [relative, content] of Object.entries(files)) {
    const destination = join(root, relative);
    await mkdir(join(destination, ".."), { recursive: true });
    await writeFile(destination, content, {
      mode: relative === "flyway" || relative.endsWith("/java") ? 0o700 : 0o600,
    });
  }
  const manifest = Object.entries(files)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(
      ([name, content]) =>
        `${createHash("sha256").update(content).digest("hex")}  ${name}\n`,
    )
    .join("");
  await writeFile(join(root, "flyway-manifest.sha256"), manifest, {
    mode: 0o600,
  });
  const manifestHash = createHash("sha256").update(manifest).digest("hex");
  const configPath = join(runtimeDir, "config.json");
  await writeFile(
    configPath,
    JSON.stringify({
      flyway: {
        executable: join(root, "flyway"),
        version,
        sha256: manifestHash,
        timeout_seconds: 30,
      },
    }),
    { mode: 0o600 },
  );
  return { runtimeDir, configPath, marker };
}

async function stopIsolatedServer(
  server: Awaited<ReturnType<typeof startIsolatedServer>>,
) {
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
  const statusResponse = await page.request.get(
    `${baseURL}/api/v1/auth/status`,
  );
  expect(statusResponse.ok()).toBeTruthy();
  const status = (await statusResponse.json()).data as {
    bootstrap_token_file: string;
  };
  const setupToken = (
    await readFile(status.bootstrap_token_file, "utf8")
  ).trim();
  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Setup token").fill(setupToken);
  await page.getByLabel("Display name").fill("Execution UI Administrator");
  await page.getByLabel("Username").fill(admin.username);
  await page
    .getByRole("textbox", { name: "New password" })
    .fill(admin.password);
  await page.getByRole("button", { name: "Create administrator" }).click();
  await expect(page).toHaveURL(/\/runs$/);
}

async function postAsSession(
  page: Page,
  baseURL: string,
  csrf: string,
  path: string,
  body: unknown,
  expectedStatus: number,
  method = "POST",
) {
  const options = {
    data: body,
    headers: { Origin: baseURL, "X-CSRF-Token": csrf },
  };
  const response =
    method === "PUT"
      ? await page.request.put(`${baseURL}/api/v1${path}`, options)
      : await page.request.post(`${baseURL}/api/v1${path}`, options);
  expect(response.status()).toBe(expectedStatus);
  return (await response.json()).data;
}

async function createAuthorizedStream(page: Page, baseURL: string) {
  const identity = (
    await (await page.request.get(`${baseURL}/api/v1/auth/me`)).json()
  ).data as { user: { id: string }; csrf_token: string };
  const target = (await postAsSession(
    page,
    baseURL,
    identity.csrf_token,
    "/database-targets",
    {
      id: "browser-fake-flyway",
      name: "Fake Flyway UAT",
      environment: "UAT",
      host: "oracle.invalid",
      port: 1521,
      service: "E2E",
      transport: "TCP",
    },
    201,
  )) as { id: string };
  const schema = (await postAsSession(
    page,
    baseURL,
    identity.csrf_token,
    `/database-targets/${target.id}/schemas`,
    { name: "FAKE_SCHEMA" },
    201,
  )) as { id: string };
  await postAsSession(
    page,
    baseURL,
    identity.csrf_token,
    "/execution-accounts",
    {
      target_id: target.id,
      name: "Fake deployer",
      username: "FAKE_DEPLOY",
      schema_ids: [schema.id],
      password: "oracle-fake-secret",
    },
    201,
  );
  await postAsSession(
    page,
    baseURL,
    identity.csrf_token,
    `/access/users/${identity.user.id}/grants`,
    {
      grants: [
        {
          target_id: target.id,
          schema_id: schema.id,
          read: true,
          operate: true,
          prod: false,
        },
      ],
    },
    200,
    "PUT",
  );
  return { target, schema };
}

async function uploadAndPreflight(page: Page, fileName = "V1__browser.sql") {
  await page.goto(new URL("/runs/new", page.url()).toString());
  await expect(page.getByText("Flyway 10.20.0")).toBeVisible();
  await page
    .locator('input[type="file"]')
    .first()
    .setInputFiles({
      name: fileName,
      mimeType: "text/plain",
      buffer: Buffer.from(
        "-- controlled fake Flyway test\r\nSELECT 1 FROM dual;\r\n",
      ),
    });
  await page.getByRole("button", { name: "Upload and normalize" }).click();
  await expect(page.getByText("Immutable artifact ready")).toBeVisible();
  await page.getByRole("button", { name: "Run preflight" }).click();
  await expect(page.getByText("VERIFIED", { exact: true })).toBeVisible({
    timeout: 15_000,
  });
  await expect(
    page.getByRole("heading", { name: "Flyway migration preview" }),
  ).toBeVisible();
  const preview = page.getByRole("table").filter({
    has: page.getByRole("columnheader", { name: "State", exact: true }),
  });
  await expect(
    preview.getByRole("cell", { name: "V1__browser.sql", exact: true }),
  ).toBeVisible();
  await expect(preview).not.toContainText("/private/flyway-run-fixture");
  await page
    .getByRole("button", { name: "Review and submit migration" })
    .click();
  await expect(page.getByRole("dialog")).toContainText("Fake Flyway UAT");
}

async function submitReviewedRun(page: Page) {
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Submit migration" })
    .click();
  await expect(page).toHaveURL(
    (url) =>
      /^\/runs\/[^/]+$/.test(url.pathname) && url.pathname !== "/runs/new",
  );
  return decodeURIComponent(
    new URL(page.url()).pathname.split("/").at(-1) ?? "",
  );
}

test("migration upload shows a real manifest and stays blocked without Flyway runtime", async ({
  browser,
}) => {
  const server = await startIsolatedServer();
  const context = await browser.newContext();
  const page = await context.newPage();
  try {
    await bootstrap(page, server.baseURL);
    const identity = (
      await (await page.request.get(`${server.baseURL}/api/v1/auth/me`)).json()
    ).data as { user: { id: string }; csrf_token: string };
    const target = (await postAsSession(
      page,
      server.baseURL,
      identity.csrf_token,
      "/database-targets",
      {
        id: "browser-oracle",
        name: "Browser Oracle UAT",
        environment: "UAT",
        host: "oracle.invalid",
        port: 1521,
        service: "E2E",
        transport: "TCP",
      },
      201,
    )) as { id: string };
    const schema = (await postAsSession(
      page,
      server.baseURL,
      identity.csrf_token,
      `/database-targets/${target.id}/schemas`,
      { name: "BROWSER_SCHEMA" },
      201,
    )) as { id: string };
    await postAsSession(
      page,
      server.baseURL,
      identity.csrf_token,
      "/execution-accounts",
      {
        target_id: target.id,
        name: "Browser deployer",
        username: "BROWSER_DEPLOY",
        schema_ids: [schema.id],
        password: "oracle-browser-secret",
      },
      201,
    );
    await postAsSession(
      page,
      server.baseURL,
      identity.csrf_token,
      `/access/users/${identity.user.id}/grants`,
      {
        grants: [
          {
            target_id: target.id,
            schema_id: schema.id,
            read: true,
            operate: true,
            prod: false,
          },
        ],
      },
      200,
      "PUT",
    );

    await page.goto(`${server.baseURL}/runs/new`);
    await expect(page.getByText("Flyway runtime unavailable")).toBeVisible();
    const selectedFile = page.locator('input[type="file"]').first();
    await selectedFile.setInputFiles({
      name: "V1__browser_upload.sql",
      mimeType: "text/plain",
      buffer: Buffer.from("-- browser upload\r\nSELECT 1 FROM dual;\r\n"),
    });
    await page.getByRole("button", { name: "Upload and normalize" }).click();
    await expect(page.getByText("Immutable artifact ready")).toBeVisible();
    await expect(
      page.getByText("V1__browser_upload.sql").first(),
    ).toBeVisible();
    await expect(page.getByText("versioned · 1")).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Run preflight" }),
    ).toBeDisabled();
    expect(
      (
        await page.request.get(
          `${server.baseURL}/api/v1/migration-runs?limit=20&offset=0`,
        )
      ).status(),
    ).toBe(200);
  } finally {
    await context.close();
    await stopIsolatedServer(server);
  }
});

test("controlled fake Flyway completes the real UI workflow through download and filtered history", async ({
  browser,
}) => {
  const runtime = await createFakeRuntime("success");
  const server = await startIsolatedServer(runtime.configPath);
  const context = await browser.newContext({ acceptDownloads: true });
  const page = await context.newPage();
  try {
    await bootstrap(page, server.baseURL);
    const { target } = await createAuthorizedStream(page, server.baseURL);
    await uploadAndPreflight(page);
    const runID = await submitReviewedRun(page);
    await expect(
      page.getByText("SUCCEEDED", { exact: true }).first(),
    ).toBeVisible({ timeout: 15_000 });
    await expect(page.locator(".run-progress-step.skipped")).toContainText(
      "Repair skipped",
    );
    await expect(
      page.getByText("No repair needed", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("Repair does not change migration outcome"),
    ).toHaveCount(0);
    await expect(
      page.getByRole("heading", { name: "Searchable logs" }),
    ).toBeVisible();
    const pause = page.getByRole("button", { name: "Pause follow" });
    await expect(pause).toHaveAttribute("aria-pressed", "true");
    await pause.click();
    const follow = page.getByRole("button", { name: "Follow logs" });
    await expect(follow).toHaveAttribute("aria-pressed", "false");
    await follow.click();
    await expect(pause).toHaveAttribute("aria-pressed", "true");
    await page.getByPlaceholder("Search logs…").fill("migrationsExecuted");
    await expect(
      page.locator(".log-viewer").getByText(/1 migration executed/),
    ).toBeVisible();
    const originalJSON = page
      .locator(".log-viewer")
      .getByText("Original Flyway JSON");
    await originalJSON.click();
    await expect(
      page.locator(".log-viewer").getByText(/"migrationsExecuted": 1/),
    ).toBeVisible();
    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page.getByRole("link", { name: "SQL", exact: true }).click(),
    ]);
    expect(download.suggestedFilename()).toBe("V1__browser.sql");
    expect((await readFile(await download.path()!)).toString()).toContain(
      "SELECT 1 FROM dual;",
    );
    const commands = (await readFile(runtime.marker, "utf8"))
      .trim()
      .split("\n");
    expect(commands.filter((command) => command === "migrate")).toHaveLength(1);
    await page.getByRole("link", { name: "Flyway Info for this run" }).click();
    await expect(
      page.getByRole("heading", { name: "Native migrations", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("cell", { name: "Success", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("Historical Info from run", { exact: false }),
    ).toBeVisible();
    await page.reload();
    await expect(
      page.getByRole("heading", { name: "Native migrations", exact: true }),
    ).toBeVisible();
    await page
      .locator("summary")
      .filter({ hasText: "Detailed diagnostics" })
      .click();
    await page.getByRole("link", { name: "Associated execution logs" }).click();
    await expect(
      page.getByRole("heading", { name: "Searchable logs" }),
    ).toBeVisible();
    await page.goto(`${server.baseURL}/runs`);
    await page.getByLabel("Run status").selectOption("SUCCEEDED");
    await page.getByLabel("Database target").selectOption(target.id);
    const history = (
      await (
        await page.request.get(
          `${server.baseURL}/api/v1/migration-runs?limit=20&offset=0&status=SUCCEEDED&target_id=${encodeURIComponent(target.id)}`,
        )
      ).json()
    ).data as { id: string; run_id: string }[];
    const recorded = history.find((run) => run.id === runID);
    expect(recorded).toBeDefined();
    await expect(
      page.getByRole("link", { name: recorded!.run_id || recorded!.id }),
    ).toBeVisible();
    await expect(page.getByText("Fake Flyway UAT").last()).toBeVisible();
  } finally {
    await context.close();
    await stopIsolatedServer(server);
    await rm(runtime.runtimeDir, { recursive: true, force: true });
  }
});

test("controlled fake Flyway keeps a failed migration failed after one safe repair without SQL retry", async ({
  browser,
}) => {
  const runtime = await createFakeRuntime("repair");
  const server = await startIsolatedServer(runtime.configPath);
  const context = await browser.newContext();
  const page = await context.newPage();
  try {
    await bootstrap(page, server.baseURL);
    await createAuthorizedStream(page, server.baseURL);
    await uploadAndPreflight(page);
    await submitReviewedRun(page);
    await expect(page.getByText("FAILED", { exact: true }).first()).toBeVisible(
      { timeout: 20_000 },
    );
    await expect(
      page.getByText("Repair completed; migration remains failed"),
    ).toBeVisible({ timeout: 15_000 });
    await expect(
      page.getByText("The original migration SQL was not rerun."),
    ).toBeVisible();
    const commands = (await readFile(runtime.marker, "utf8"))
      .trim()
      .split("\n");
    expect(commands.filter((command) => command === "migrate")).toHaveLength(1);
    expect(commands.filter((command) => command === "repair")).toHaveLength(1);
    await expect(
      page
        .getByText("Oracle DDL may have partially committed", { exact: false })
        .first(),
    ).toBeVisible();
  } finally {
    await context.close();
    await stopIsolatedServer(server);
    await rm(runtime.runtimeDir, { recursive: true, force: true });
  }
});
