import {
  chromium,
  expect,
} from "../../web/node_modules/@playwright/test/index.mjs";
import { createHash, randomBytes } from "node:crypto";
import { chmod, mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { spawn, execFileSync } from "node:child_process";
import { createServer } from "node:net";
import { homedir, tmpdir } from "node:os";
import { resolve, join } from "node:path";

const root = resolve(import.meta.dirname, "../..");
const evidence =
  process.env.FWM_LAB_EVIDENCE && resolve(process.env.FWM_LAB_EVIDENCE);
const labEnvPath =
  process.env.FWM_POSTGRES_LAB_ENV ||
  join(homedir(), ".local/state/flyway-manager-postgres-lab/lab.env");
const candidate =
  process.env.FWM_MANAGER_CANDIDATE &&
  resolve(process.env.FWM_MANAGER_CANDIDATE);
const runtime = process.env.FWM_LAB_FLYWAY;
const manifestHash =
  "a359f1f0e0a08bd7f5fe3272cc726c3866c0000028fee32a1df56a61f85ecd2c";
const configuredPrivateRoot =
  process.env.FWM_LAB_PRIVATE_ROOT && resolve(process.env.FWM_LAB_PRIVATE_ROOT);

if (!evidence)
  throw new Error("Set FWM_LAB_EVIDENCE to a new isolated evidence directory.");
if (!runtime)
  throw new Error(
    "Set FWM_LAB_FLYWAY to the pinned Flyway 13.10.3 executable.",
  );
if (!candidate)
  throw new Error(
    "Set FWM_MANAGER_CANDIDATE to the candidate Manager executable.",
  );
const managerRoot =
  configuredPrivateRoot ||
  (await mkdtemp(join(tmpdir(), "fwm-postgresql-recovery-")));
const envText = await readFile(labEnvPath, "utf8");
const lab = Object.fromEntries(
  envText
    .split(/\r?\n/)
    .map((line) => line.match(/^([A-Z0-9_]+)=(.*)$/))
    .filter(Boolean)
    .map((m) => [m[1], m[2]]),
);
for (const key of [
  "PGHOST",
  "PGPORT",
  "PGDATABASE",
  "PGUSER",
  "PGPASSWORD",
  "PGSSLROOTCERT",
])
  if (!lab[key]) throw new Error(`Private lab env lacks ${key}.`);
if (
  lab.PGHOST !== "localhost" ||
  lab.PGPORT !== "55440" ||
  lab.PGUSER !== "fwm_exec"
)
  throw new Error(
    "Refusing a PostgreSQL endpoint outside the declared disposable lab profile.",
  );
await readFile(candidate).then(async (b) => {
  if (!b.length) throw new Error("Candidate binary is empty.");
});
const runtimeManifest = join(
  runtime.replace(/\/flyway$/, ""),
  "flyway-manifest.sha256",
);
if (
  createHash("sha256")
    .update(await readFile(runtimeManifest))
    .digest("hex") !== manifestHash
)
  throw new Error("Pinned Flyway manifest hash mismatch.");
execFileSync("docker", ["inspect", "flyway-manager-postgres17-lab"], {
  stdio: "ignore",
});
const inspect = JSON.parse(
  execFileSync("docker", ["inspect", "flyway-manager-postgres17-lab"], {
    encoding: "utf8",
  }),
)[0];
if (
  inspect.State?.Running !== true ||
  !inspect.HostConfig?.PortBindings?.["5432/tcp"]?.some(
    (x) => x.HostIp === "127.0.0.1" && x.HostPort === "55440",
  )
)
  throw new Error(
    "Named PostgreSQL lab is not running with the expected loopback-only binding.",
  );
await mkdir(evidence, { recursive: true, mode: 0o700 });
await chmod(evidence, 0o700);
await mkdir(managerRoot, { recursive: true, mode: 0o700 });
const attempt = join(
  managerRoot,
  `manager-browser-${new Date().toISOString().replaceAll(":", "-")}-${randomBytes(4).toString("hex")}`,
);
await mkdir(attempt, { recursive: true, mode: 0o700 });
const dbName = `fwm_product_${randomBytes(4).toString("hex")}`;
if (!/^fwm_product_[a-f0-9]{8}$/.test(dbName))
  throw new Error("Unsafe generated database name.");
const psql = (db, sql) =>
  execFileSync(
    "docker",
    [
      "exec",
      "flyway-manager-postgres17-lab",
      "psql",
      "-XAtq",
      "-v",
      "ON_ERROR_STOP=1",
      "-U",
      "lab_admin",
      "-d",
      db,
      "-c",
      sql,
    ],
    { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] },
  ).trim();
const psqlJSON = (db, sql) => JSON.parse(psql(db, sql) || "null");
const executionMetadata = (rows) =>
  rows.map(
    ({
      id,
      command,
      success,
      exit_code,
      started_at,
      finished_at,
      ambiguous,
    }) => ({
      id,
      command,
      success,
      exit_code,
      started_at,
      finished_at,
      ambiguous,
    }),
  );
const logRecords = (rows) =>
  rows.map(({ sequence, command, channel, created_at, message }) => ({
    sequence,
    command,
    channel,
    created_at,
    message,
  }));
psql("postgres", `CREATE DATABASE ${dbName}`);
psql(
  dbName,
  "CREATE SCHEMA app AUTHORIZATION fwm_exec; CREATE SCHEMA other AUTHORIZATION fwm_exec; CREATE SCHEMA prod_app AUTHORIZATION fwm_exec; CREATE SCHEMA crash AUTHORIZATION fwm_exec",
);
const prodDbName = `${dbName}_prod`;
psql("postgres", `CREATE DATABASE ${prodDbName}`);
psql(prodDbName, "CREATE SCHEMA prod_app AUTHORIZATION fwm_exec");
const portServer = createServer();
await new Promise((r, j) =>
  portServer.once("error", j).listen(0, "127.0.0.1", r),
);
const port = portServer.address().port;
await new Promise((r, j) => portServer.close((e) => (e ? j(e) : r())));
const base = `http://127.0.0.1:${port}`;
const cfg = join(attempt, "config.json");
await writeFile(
  cfg,
  JSON.stringify({
    listen_address: `127.0.0.1:${port}`,
    data_dir: join(attempt, "data"),
    public_url: "",
    flyway: {
      executable: runtime,
      version: "13.10.3",
      sha256: manifestHash,
      timeout_seconds: 12,
    },
  }),
  { mode: 0o600 },
);
const spawnManager = () =>
  spawn(candidate, [], {
    cwd: root,
    env: {
      ...process.env,
      FLYWAY_MANAGER_CONFIG: cfg,
      FLYWAY_MANAGER_LISTEN_ADDRESS: `127.0.0.1:${port}`,
      FLYWAY_MANAGER_DATA_DIR: join(attempt, "data"),
      FLYWAY_MANAGER_PUBLIC_URL: "",
    },
    stdio: "ignore",
  });
let child = spawnManager();
let browser;
const stop = async () => {
  if (child.exitCode !== null) return;
  child.kill("SIGTERM");
  await Promise.race([
    new Promise((r) => child.once("exit", r)),
    new Promise((r) => setTimeout(r, 6000)),
  ]);
};
const awaitHealth = async (timeout = 30000) => {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (child.exitCode !== null)
      throw new Error("Candidate Manager exited during startup.");
    try {
      if ((await fetch(`${base}/api/v1/health`)).ok) return;
    } catch {}
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(
    "Candidate Manager did not become healthy within its startup bound.",
  );
};
process.once("SIGINT", () => {
  void stop();
});
process.once("SIGTERM", () => {
  void stop();
});
const redactedEvidence = {
  database: dbName,
  manager_attempt: attempt,
  run_id: null,
  target_id: null,
  schema_id: null,
  project_id: null,
  artifact_id: null,
  statuses: {},
  screenshots: [],
  role_checks: {},
};
try {
  await awaitHealth();
  const request = await (
    await import("../../web/node_modules/playwright-core/index.mjs")
  ).request.newContext({ baseURL: base, extraHTTPHeaders: { Origin: base } });
  const status = await (await request.get("/api/v1/auth/status")).json();
  const bootstrapToken = (
    await readFile(status.data.bootstrap_token_file, "utf8")
  ).trim();
  const adminUser = `browser-admin-${randomBytes(3).toString("hex")}`,
    adminPassword = randomBytes(24).toString("base64url");
  const boot = await request.post("/api/v1/auth/bootstrap", {
    data: {
      username: adminUser,
      display_name: "PostgreSQL Candidate Admin",
      password: adminPassword,
      setup_token: bootstrapToken,
    },
  });
  if (boot.status() !== 201)
    throw new Error(`Admin bootstrap returned ${boot.status()}.`);
  const admin = (await boot.json()).data;
  await writeFile(
    join(attempt, "admin-credentials.json"),
    JSON.stringify({
      username: adminUser,
      password: adminPassword,
      database: dbName,
    }),
    { mode: 0o600 },
  );
  const headers = { "X-CSRF-Token": admin.csrf_token, Origin: base };
  const api = async (method, path, body, expectStatus = 200, extra = {}) => {
    const r = await request.fetch(`/api/v1${path}`, {
      method,
      headers: { ...headers, ...extra },
      data: body,
      timeout: 180_000,
    });
    if (r.status() !== expectStatus)
      throw new Error(
        `${method} ${path} returned ${r.status()} (expected ${expectStatus}).`,
      );
    if (r.status() === 204) return null;
    const j = await r.json();
    return j.data;
  };
  const caps = await api("GET", "/engine-capabilities");
  const pgCap = caps.find((x) => x.engine === "postgresql");
  if (!pgCap?.migration)
    throw new Error(
      "Candidate does not advertise PostgreSQL migration capability.",
    );
  const projectName = `Gate F ${dbName}`;
  const project = await api(
    "POST",
    "/projects",
    {
      name: projectName,
      description: "Isolated PostgreSQL migration acceptance",
      status: "active",
      members: [],
    },
    201,
  );
  redactedEvidence.project_id = project.id;

  browser = await chromium.launch({
    headless: true,
    executablePath:
      process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined,
  });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
    acceptDownloads: true,
  });
  const page = await context.newPage();
  page.setDefaultTimeout(60_000);
  const pageErrors = [];
  page.on("pageerror", (e) => pageErrors.push(e.message));
  await page.goto(`${base}/login`);
  await page.getByLabel("Username").fill(adminUser);
  await page.getByRole("textbox", { name: "Password" }).fill(adminPassword);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/runs$/);
  await page.goto(`${base}/instances`);
  await page.getByRole("button", { name: "Add instance", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Database engine").selectOption("PostgreSQL");
  await dialog
    .getByLabel("Connection display name")
    .fill(`PostgreSQL ${dbName}`);
  await dialog.getByLabel("Environment").selectOption("DEV");
  await dialog.getByLabel("Host").fill(lab.PGHOST);
  await dialog.getByLabel("Port").fill(lab.PGPORT);
  await dialog.getByLabel("PostgreSQL database").fill(dbName);
  await dialog
    .getByLabel("Approved TLS CA bundle (PEM)")
    .fill(await readFile(lab.PGSSLROOTCERT, "utf8"));
  await dialog.getByLabel("PostgreSQL role").fill(lab.PGUSER);
  await dialog.getByLabel("PostgreSQL password").fill(lab.PGPASSWORD);
  await dialog.getByRole("button", { name: "Test Connection" }).click();
  await expect(dialog).toContainText("Authentication: verified", {
    timeout: 90_000,
  });
  await expect(dialog).toContainText(`Database: ${dbName}`);
  await expect(dialog).toContainText("TLS: verify-full");
  await dialog.getByRole("button", { name: "Choose allowed schemas" }).click();
  await dialog.getByRole("checkbox", { name: /^app ·/ }).check();
  await dialog.getByRole("checkbox", { name: /^crash ·/ }).check();
  await dialog
    .getByRole("checkbox", { name: /Authorize my Flyway Manager user/ })
    .check();
  await dialog.getByRole("button", { name: "Review selection" }).click();
  await expect(dialog).toContainText(/PostgreSQL.*Migration/);
  await dialog.getByRole("button", { name: "Save instance" }).click();
  await expect(dialog).toHaveCount(0, { timeout: 60000 });
  let targets = [],
    target;
  const targetDeadline = Date.now() + 20000;
  while (Date.now() < targetDeadline) {
    targets = await api("GET", "/database-targets");
    target = targets.find(
      (x) => x.engine === "postgresql" && x.database_name === dbName,
    );
    if (target) break;
    await new Promise((r) => setTimeout(r, 250));
  }
  if (!target)
    throw new Error(
      `Wizard did not register its PostgreSQL target (${targets.length} targets returned).`,
    );
  redactedEvidence.target_id = target.id;
  const schemas = await api(
    "GET",
    `/database-targets/${encodeURIComponent(target.id)}/schemas`,
  );
  const schema = schemas.find((x) => x.name === "app");
  if (!schema) throw new Error("Wizard did not discover selected app schema.");
  redactedEvidence.schema_id = schema.id;
  await api("PUT", `/projects/${encodeURIComponent(project.id)}`, {
    name: projectName,
    description: "Isolated PostgreSQL migration acceptance",
    status: "active",
    members: [{ target_id: target.id, schema_id: schema.id }],
  });
  const grants = [
    {
      target_id: target.id,
      schema_id: schema.id,
      read: true,
      operate: true,
      prod: false,
    },
  ];
  await api(
    "PUT",
    `/access/users/${encodeURIComponent(admin.user.id)}/grants`,
    { grants },
  );

  await page.goto(
    `${base}/runs/new?${new URLSearchParams({ target_id: target.id, schema_id: schema.id })}`,
  );
  await expect(page.locator(".layout-editor select").nth(0)).toHaveValue(
    target.id,
  );
  await expect(page.locator(".layout-editor select").nth(1)).toHaveValue(
    schema.id,
  );
  const migrations = [
    {
      name: "V1__browser_gate.sql",
      mimeType: "text/plain",
      buffer: Buffer.from(
        "CREATE TABLE manager_browser_rows (id integer PRIMARY KEY, note text NOT NULL);\nINSERT INTO manager_browser_rows VALUES (1, 'v1');\n",
      ),
    },
    {
      name: "V2__second_row.sql",
      mimeType: "text/plain",
      buffer: Buffer.from(
        "INSERT INTO manager_browser_rows VALUES (2, 'v2');\n",
      ),
    },
    {
      name: "R__row_count.sql",
      mimeType: "text/plain",
      buffer: Buffer.from(
        "CREATE OR REPLACE VIEW manager_browser_row_count AS SELECT count(*) AS row_count FROM manager_browser_rows;\n",
      ),
    },
  ];
  await page.locator('input[type="file"]').first().setInputFiles(migrations);
  await page.getByRole("button", { name: "Upload and normalize" }).click();
  await expect(page.getByText("Immutable artifact ready")).toBeVisible({
    timeout: 90_000,
  });
  await page.getByRole("button", { name: "Run preflight" }).click();
  await expect(
    page.getByRole("button", { name: "Review and submit migration" }),
  ).toBeEnabled({ timeout: 180_000 });
  await page
    .getByRole("button", { name: /Review and submit migration/ })
    .click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Submit migration" })
    .click();
  await expect(page).toHaveURL(/\/runs\/[A-Za-z0-9_-]+$/, { timeout: 240_000 });
  const runID = new URL(page.url()).pathname.split("/").pop();
  redactedEvidence.run_id = runID;
  await expect(
    page.locator(".run-overview").getByText("SUCCEEDED", { exact: true }),
  ).toBeVisible({ timeout: 240_000 });
  redactedEvidence.statuses.migration = "SUCCEEDED";
  const run = await api("GET", `/migration-runs/${runID}`);
  redactedEvidence.artifact_id = run.artifact_id;
  redactedEvidence.artifact_files = migrations.map((item) => ({
    name: item.name,
    size: item.buffer.length,
    sha256: createHash("sha256").update(item.buffer).digest("hex"),
  }));
  await page.screenshot({
    path: join(evidence, "manager-run-desktop.png"),
    fullPage: true,
  });
  redactedEvidence.screenshots.push("manager-run-desktop.png");
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: join(evidence, "manager-run-mobile.png"),
    fullPage: true,
  });
  redactedEvidence.screenshots.push("manager-run-mobile.png");
  const mobileOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth > innerWidth,
  );
  if (mobileOverflow)
    throw new Error("Run details overflows the 390px mobile viewport.");
  await page.goto(`${base}/runs/${runID}?tab=info`);
  await expect(
    page.getByRole("heading", { name: "Native migrations" }),
  ).toBeVisible();
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth))
    throw new Error("Migration Info overflows the 390px mobile viewport.");
  await expect(
    page.getByRole("table").getByRole("row").filter({ hasText: "browser gate" }),
  ).toHaveCount(1);
  await page.screenshot({
    path: join(evidence, "manager-info-mobile.png"),
    fullPage: true,
  });
  redactedEvidence.screenshots.push("manager-info-mobile.png");
  const firstMigrationRow = page
    .getByRole("table")
    .getByRole("row")
    .filter({ hasText: "browser gate" });
  await firstMigrationRow.getByRole("button").first().click();
  await page
    .getByRole("dialog")
    .getByRole("link", { name: "Open SQL script" })
    .click();
  await expect(
    page.locator(".sql-source tbody tr td:nth-child(2) code").first(),
  ).toBeVisible();
  const mobileSQLText = await page
    .locator(".sql-source tbody tr td:nth-child(2) code")
    .evaluateAll((nodes) => nodes.map((n) => n.textContent || "").join("\n"))
    .then((s) => s.replaceAll("\u200b", ""));
  if (!mobileSQLText.includes("manager_browser_rows"))
    throw new Error("Mobile SQL reader did not show the accepted SQL file.");
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth))
    throw new Error("SQL reader overflows the 390px mobile viewport.");
  await page.screenshot({
    path: join(evidence, "manager-sql-mobile.png"),
    fullPage: true,
  });
  redactedEvidence.screenshots.push("manager-sql-mobile.png");
  await page.goto(`${base}/runs/${runID}?tab=logs`);
  await expect(
    page.getByRole("heading", { name: "Searchable logs" }),
  ).toBeVisible();
  await expect(page.getByRole("region", { name: "Migration log entries" })).toBeVisible();
  await expect(page.locator(".log-row").first()).toBeVisible();
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth))
    throw new Error("Execution Logs overflows the 390px mobile viewport.");
  await page.screenshot({
    path: join(evidence, "manager-logs-mobile.png"),
    fullPage: true,
  });
  redactedEvidence.screenshots.push("manager-logs-mobile.png");
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("link", { name: "Migration Info", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Native migrations" }),
  ).toBeVisible();
  const infoTable = page.getByRole("table");
  const nativeCapture = await api("GET", `/database-targets/${target.id}/schemas/${schema.id}/flyway-info?run_id=${runID}`);
  if (nativeCapture.source_run_id !== runID || !nativeCapture.success || nativeCapture.json?.migrations?.length !== 3 || nativeCapture.json.migrations.some((entry) => entry.state !== "Success"))
    throw new Error("Recorded native Info lost its exact historical run or native Success states.");
  const nativeStdout = nativeCapture.stdout.slice(nativeCapture.stdout.indexOf("{"));
  if (JSON.stringify(JSON.parse(nativeStdout)) !== JSON.stringify(nativeCapture.json))
    throw new Error("Stored native JSON differs from original redacted Flyway stdout.");
  const nativeDiagnostics = page.locator("details").filter({ has: page.locator("summary", { hasText: "original redacted stdout" }) });
  await nativeDiagnostics.locator("summary").click();
  const displayedNativeJSON = await nativeDiagnostics.locator('h3:has-text("Native JSON") + pre').textContent();
  if (JSON.stringify(JSON.parse(displayedNativeJSON)) !== JSON.stringify(nativeCapture.json))
    throw new Error("Advanced Diagnostics changed original native Flyway JSON.");
  await nativeDiagnostics.locator("summary").click();
  redactedEvidence.native_info = nativeCapture;
  await expect(
    infoTable.getByRole("row").filter({ hasText: "browser gate" }),
  ).toHaveCount(1);
  await expect(
    infoTable.getByRole("row").filter({ hasText: "second row" }),
  ).toHaveCount(1);
  for (const item of migrations.slice(0, 2)) {
    const desc = item.name.replace(/^V\d+__|\.sql$/g, "").replaceAll("_", " ");
    const row = infoTable.getByRole("row").filter({ hasText: desc });
    await row.getByRole("button").first().click();
    await page
      .getByRole("dialog")
      .getByRole("link", { name: "Open SQL script" })
      .click();
    const code = page.locator(".sql-source tbody tr td:nth-child(2) code");
    await expect(code.first()).toBeVisible();
    const displayed = await code
      .evaluateAll((nodes) => nodes.map((n) => n.textContent || "").join("\n"))
      .then((s) => s.replaceAll("\u200b", ""));
    if (displayed !== item.buffer.toString())
      throw new Error(`SQL preview mismatch for ${item.name}.`);
    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page.getByRole("link", { name: "Download original file" }).click(),
    ]);
    if (!Buffer.from(await readFile(await download.path())).equals(item.buffer))
      throw new Error(`Immutable download mismatch for ${item.name}.`);
    await page.getByRole("link", { name: "Return to Migration Info" }).click();
    await expect(
      page.getByRole("heading", { name: "Native migrations" }),
    ).toBeVisible();
  }
  await page.getByRole("link", { name: "Execution Logs" }).click();
  await expect(
    page.getByRole("heading", { name: "Searchable logs" }),
  ).toBeVisible();
  const logURL = page.url();
  await page.goto(logURL);
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Searchable logs" }),
  ).toBeVisible();
  await page.goto(`${base}/runs/${runID}?tab=sql`);
  await expect(
    page.locator(".sql-source tbody tr td:nth-child(2) code").first(),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.locator(".sql-source tbody tr td:nth-child(2) code").first(),
  ).toBeVisible();
  await page.goto(`${base}/runs/${runID}?tab=info`);
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Native migrations" }),
  ).toBeVisible();
  await page.goto(`${base}/projects/${project.id}?tab=history`);
  await expect(
    page.getByRole("tab", { name: "Migration history" }),
  ).toHaveAttribute("aria-selected", "true");
  await expect(page.getByText(runID)).toBeVisible();
  const historyURL = page.url();
  await page.goto(historyURL);
  await page.reload();
  await expect(
    page.getByRole("tab", { name: "Migration history" }),
  ).toHaveAttribute("aria-selected", "true");
  await expect(page.getByText(runID)).toBeVisible();
  // Abrupt Manager death while a real Flyway statement is active, followed by same-registry restart.
  const crashSchema = schemas.find((x) => x.name === "crash");
  if (!crashSchema)
    throw new Error("Wizard did not select the dedicated crash schema.");
  await api(
    "PUT",
    `/access/users/${encodeURIComponent(admin.user.id)}/grants`,
    {
      grants: [
        ...grants,
        {
          target_id: target.id,
          schema_id: crashSchema.id,
          read: true,
          operate: true,
          prod: false,
        },
      ],
    },
  );
  const crashInputs = [
    {
      name: "V1__committed_marker.sql",
      mimeType: "text/plain",
      buffer: Buffer.from(
        "CREATE TABLE manager_crash_committed (id integer PRIMARY KEY);\nINSERT INTO manager_crash_committed VALUES (1);\n",
      ),
    },
    {
      name: "V2__crash_sleep.sql",
      mimeType: "text/plain",
      buffer: Buffer.from(
        "CREATE TABLE manager_crash_transient (id integer PRIMARY KEY);\nSELECT pg_sleep(90);\n",
      ),
    },
  ];
  const crashForm = new FormData();
  for (const f of crashInputs)
    crashForm.append(
      "files",
      new File([f.buffer], f.name, { type: f.mimeType }),
    );
  const crashUpload = await request.post(
    `/api/v1/artifacts?target_id=${target.id}&schema_id=${crashSchema.id}`,
    { headers, multipart: crashForm },
  );
  if (crashUpload.status() !== 201)
    throw new Error(`Crash artifact upload returned ${crashUpload.status()}.`);
  const crashArtifact = (await crashUpload.json()).data;
  const crashInput = {
    target_id: target.id,
    schema_id: crashSchema.id,
    execution_account_id: (
      await api(
        "GET",
        `/database-targets/${target.id}/schemas/${crashSchema.id}/execution-accounts`,
      )
    )[0].id,
    artifact_id: crashArtifact.id,
    context: {},
  };
  const crashPreflight = await api(
    "POST",
    "/migration-preflight",
    crashInput,
    200,
  );
  if (!crashPreflight.ready || crashPreflight.live_status !== "VERIFIED")
    throw new Error("Crash migration preflight did not verify.");
  const crashKey = randomBytes(18).toString("hex");
  const crashRun = await api("POST", "/migration-runs", crashInput, 202, {
    "Idempotency-Key": crashKey,
  });
  const observedSQL = () =>
    psql(
      dbName,
      `SELECT count(*) FROM pg_stat_activity WHERE datname='${dbName}' AND usename='fwm_exec' AND state='active' AND query LIKE '%pg_sleep(90)%'`,
    );
  const observedSessions = () =>
    psqlJSON(
      dbName,
      `SELECT COALESCE(json_agg(json_build_object('pid',pid,'backend_start',backend_start,'state',state,'xact_start',xact_start,'query_start',query_start,'observed_at',clock_timestamp(),'transaction_age_ms',extract(epoch from (clock_timestamp()-xact_start))*1000,'wait_event_type',wait_event_type,'wait_event',wait_event,'application_name',application_name) ORDER BY pid),'[]'::json) FROM pg_stat_activity WHERE datname='${dbName}' AND usename='fwm_exec' AND state='active' AND query LIKE '%pg_sleep(90)%'`,
    );
  let active = false;
  let activeSnapshots = [];
  const activeDeadline = Date.now() + 90_000;
  while (Date.now() < activeDeadline) {
    try {
      if (Number(observedSQL()) > 0) {
        activeSnapshots = observedSessions();
        if (activeSnapshots.length) {
          active = true;
          break;
        }
      }
    } catch {}
    await new Promise((r) => setTimeout(r, 150));
  }
  if (!active)
    throw new Error(
      "Did not observe the crash fixture SQL active in pg_stat_activity.",
    );
  const crashTags = [...new Set(activeSnapshots.map((s) => s.application_name))];
  if (crashTags.some((tag) => !/^fwm_[A-Za-z0-9_-]{16,58}$/.test(tag)))
    throw new Error("Crash fixture session lacks a Manager correlation tag.");
  const transactionAgeMs = Math.max(
    ...activeSnapshots.map((s) => Number(s.transaction_age_ms)),
  );
  if (!Number.isFinite(transactionAgeMs) || transactionAgeMs >= 12_000)
    throw new Error("Crash fixture was not observed inside its 12-second transaction budget.");
  const killAt = Date.now();
  const goneDeadline = killAt + 12_000 - transactionAgeMs + 1_000;
  const remainingCrashSessions = () => Number(psql(
    dbName,
    `SELECT count(*) FROM pg_stat_activity WHERE datname='${dbName}' AND usename='fwm_exec' AND application_name IN (${crashTags.map((tag) => `'${tag}'`).join(",")})`,
  ));
  child.kill("SIGKILL");
  await new Promise((resolveExit, reject) => {
    const timer = setTimeout(
      () => reject(new Error("SIGKILL candidate did not exit.")),
      5000,
    );
    child.once("exit", () => {
      clearTimeout(timer);
      resolveExit();
    });
  });
  let gone = false;
  let backendGoneAt;
  while (Date.now() < goneDeadline) {
    if (remainingCrashSessions() === 0) {
      gone = true;
      backendGoneAt = Date.now();
      break;
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  if (!gone || backendGoneAt > goneDeadline)
    throw new Error(
      "PostgreSQL session outlived the 12-second transaction bound plus one second observation tolerance after Manager SIGKILL.",
    );
  child = spawnManager();
  await awaitHealth(30_000);
  await new Promise((r) => setTimeout(r, 1000));
  const afterCrash = await api("GET", `/migration-runs/${crashRun.id}`);
  if (afterCrash.status !== "NEEDS_REVIEW")
    throw new Error(
      `Restarted crash run status was ${afterCrash.status}, expected NEEDS_REVIEW.`,
    );
  const sameKeyAfterRestart = await api("POST", "/migration-runs", crashInput, 202, {
    "Idempotency-Key": crashKey,
  });
  if (sameKeyAfterRestart.id !== crashRun.id || sameKeyAfterRestart.status !== "NEEDS_REVIEW")
    throw new Error("Restart lost durable idempotency or replayed the interrupted run.");
  const crashInfo = await api(
    "GET",
    `/migration-runs/${crashRun.id}/executions`,
  );
  if (
    !Array.isArray(crashInfo) ||
    crashInfo.some((x) => x.command === "migrate" && x.success)
  )
    throw new Error(
      "Restart created a successful migration execution or automatic retry.",
    );
  const committedRows = psql(
    dbName,
    "SELECT count(*) FROM crash.manager_crash_committed WHERE id=1",
  );
  const transientTables = psql(
    dbName,
    "SELECT count(*) FROM information_schema.tables WHERE table_schema='crash' AND table_name='manager_crash_transient'",
  );
  const historyV1 = psql(
    dbName,
    "SELECT count(*) FROM crash.flyway_schema_history WHERE version='1' AND success=true",
  );
  const historyV2 = psql(
    dbName,
    "SELECT count(*) FROM crash.flyway_schema_history WHERE version='2' AND success=true",
  );
  if (
    committedRows !== "1" ||
    transientTables !== "0" ||
    historyV1 !== "1" ||
    historyV2 !== "0"
  )
    throw new Error(
      "Process-death state did not preserve V1 exactly once while rolling back V2.",
    );
  const replay = await request.post("/api/v1/migration-runs", {
    headers: { ...headers, "Idempotency-Key": randomBytes(18).toString("hex") },
    data: crashInput,
  });
  if (replay.status() !== 409)
    throw new Error(
      `Held artifact was not blocked from a new-key replay (HTTP ${replay.status()}).`,
    );
  redactedEvidence.role_checks.crash_restart = {
    run_id: crashRun.id,
    artifact_id: crashArtifact.id,
    status: afterCrash.status,
    backend_gone: gone,
    v1_committed_once: true,
    v2_rolled_back: true,
    new_key_replay_denied: true,
    no_successful_execution: true,
    same_key_returns_original_after_restart: true,
    transaction_budget_ms: 12_000,
    observation_tolerance_ms: 1_000,
    transaction_age_at_signal_ms: transactionAgeMs,
    backend_disappearance_after_signal_ms: backendGoneAt - killAt,
  };
  await page.goto(`${base}/runs/${crashRun.id}`);
  await expect(
    page.locator(".run-overview").getByText("NEEDS_REVIEW", { exact: true }),
  ).toBeVisible();
  const crashLogs = await api(
    "GET",
    `/migration-runs/${crashRun.id}/logs?limit=100`,
  );
  const journalPrefix = "PostgreSQL recovered lifecycle journal: ";
  const journal = crashLogs.find((x) => x.message.startsWith(journalPrefix));
  if (!journal)
    throw new Error(
      "The exact recovered lifecycle journal was not retained in run logs.",
    );
  const journalData = JSON.parse(journal.message.slice(journalPrefix.length));
  const cliEvent = journalData.events?.find((event) => event.event === "cli_started");
  if (!cliEvent || cliEvent.cli_pid <= 1 || cliEvent.cli_process_group_id !== cliEvent.cli_pid || !crashTags.includes(cliEvent.application_name))
    throw new Error("Recovered journal lost its exact CLI PID/process-group correlation.");
  const finalJournalEvent = journalData.events?.at(-1);
  const observedActive = journalData.events?.some(
    (event) =>
      event.event === "observed" &&
      event.snapshots?.some((session) => session.state === "active"),
  );
  if (
    !finalJournalEvent ||
    !["observed", "finished"].includes(finalJournalEvent.event) ||
    !observedActive
  )
    throw new Error(
      "The recovered lifecycle journal lacks its final event or active-session observation.",
    );
  redactedEvidence.role_checks.crash_restart.journal_final_event =
    finalJournalEvent.event;
  redactedEvidence.role_checks.crash_restart.active_session_recorded = true;
  redactedEvidence.role_checks.crash_restart.active_sessions = activeSnapshots;
  redactedEvidence.role_checks.crash_restart.executions =
    executionMetadata(crashInfo);
  redactedEvidence.role_checks.crash_restart.journal = journalData;
  redactedEvidence.role_checks.crash_restart.logs = logRecords(crashLogs);
  redactedEvidence.role_checks.crash_restart.database = {
    committed_marker_rows: psqlJSON(
      dbName,
      "SELECT COALESCE(json_agg(to_jsonb(t) ORDER BY id),'[]'::json) FROM crash.manager_crash_committed t",
    ),
    history: psqlJSON(
      dbName,
      "SELECT COALESCE(json_agg(to_jsonb(t) ORDER BY installed_rank),'[]'::json) FROM crash.flyway_schema_history t",
    ),
    transient_table_count: Number(transientTables),
  };
  redactedEvidence.role_checks.crash_restart.artifact_files = crashInputs.map(
    (item) => ({
      name: item.name,
      size: item.buffer.length,
      sha256: createHash("sha256").update(item.buffer).digest("hex"),
    }),
  );

  const viewerName = `viewer-${randomBytes(3).toString("hex")}`;
  const viewerPassword = randomBytes(24).toString("base64url");
  const viewer = await api(
    "POST",
    "/access/users",
    {
      username: viewerName,
      display_name: "Scoped Viewer",
      role: "Viewer",
      password: viewerPassword,
    },
    201,
  );
  await api("PUT", `/access/users/${encodeURIComponent(viewer.id)}/grants`, {
    grants: [{ ...grants[0], operate: false }],
  });
  const viewerContext = await (
    await import("../../web/node_modules/playwright-core/index.mjs")
  ).request.newContext({ baseURL: base, extraHTTPHeaders: { Origin: base } });
  const viewerLogin = await viewerContext.post("/api/v1/auth/login", {
    data: { provider: "local", username: viewerName, password: viewerPassword },
  });
  if (viewerLogin.status() !== 200) throw new Error("Viewer login failed.");
  const viewerSession = (await viewerLogin.json()).data;
  const viewerPreflight = await viewerContext.post(
    "/api/v1/migration-preflight",
    {
      headers: { "X-CSRF-Token": viewerSession.csrf_token },
      data: {
        target_id: target.id,
        schema_id: schema.id,
        execution_account_id: run.execution_account_id,
        artifact_id: run.artifact_id,
        context: {},
      },
    },
  );
  if (viewerPreflight.status() !== 403)
    throw new Error(
      `Viewer preflight was not denied (HTTP ${viewerPreflight.status()}).`,
    );
  const viewerUpload = await viewerContext.post(
    `/api/v1/artifacts?target_id=${target.id}&schema_id=${schema.id}`,
    {
      headers: { "X-CSRF-Token": viewerSession.csrf_token },
      multipart: {
        files: {
          name: "V9__viewer_denial.sql",
          mimeType: "text/plain",
          buffer: Buffer.from("SELECT 1;\n"),
        },
      },
    },
  );
  if (viewerUpload.status() !== 403)
    throw new Error(
      `Viewer upload was not denied (HTTP ${viewerUpload.status()}).`,
    );
  redactedEvidence.role_checks.viewer_operate_denied = true;
  const operatorName = `operator-${randomBytes(3).toString("hex")}`;
  const operatorPassword = randomBytes(24).toString("base64url");
  const operator = await api(
    "POST",
    "/access/users",
    {
      username: operatorName,
      display_name: "Scoped Operator",
      role: "Operator",
      password: operatorPassword,
    },
    201,
  );
  await api("PUT", `/access/users/${encodeURIComponent(operator.id)}/grants`, {
    grants,
  });
  const operatorContext = await (
    await import("../../web/node_modules/playwright-core/index.mjs")
  ).request.newContext({ baseURL: base, extraHTTPHeaders: { Origin: base } });
  const operatorLogin = await operatorContext.post("/api/v1/auth/login", {
    data: {
      provider: "local",
      username: operatorName,
      password: operatorPassword,
    },
  });
  if (operatorLogin.status() !== 200) throw new Error("Operator login failed.");
  const operatorSession = (await operatorLogin.json()).data;
  const acceptedInput = {
    target_id: target.id,
    schema_id: schema.id,
    execution_account_id: run.execution_account_id,
    artifact_id: run.artifact_id,
    context: {},
  };
  const operatorPreflight = await operatorContext.post(
    "/api/v1/migration-preflight",
    {
      headers: { "X-CSRF-Token": operatorSession.csrf_token },
      data: acceptedInput,
      timeout: 180_000,
    },
  );
  if (
    operatorPreflight.status() !== 200 ||
    !(await operatorPreflight.json()).data.ready
  )
    throw new Error(
      `Operator DEV preflight was not allowed and ready (HTTP ${operatorPreflight.status()}).`,
    );
  const operatorUpload = await operatorContext.post(
    `/api/v1/artifacts?target_id=${target.id}&schema_id=${schema.id}`,
    {
      headers: { "X-CSRF-Token": operatorSession.csrf_token },
      multipart: {
        files: {
          name: "V9__operator_scope.sql",
          mimeType: "text/plain",
          buffer: Buffer.from("SELECT 1;\n"),
        },
      },
    },
  );
  if (operatorUpload.status() !== 201)
    throw new Error(
      `Operator DEV upload was not allowed (HTTP ${operatorUpload.status()}).`,
    );
  redactedEvidence.role_checks.operator_dev_upload_allowed = true;
  const viewerBrowser = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
  });
  const viewerPage = await viewerBrowser.newPage();
  viewerPage.setDefaultTimeout(30000);
  await viewerPage.goto(`${base}/login`);
  await viewerPage.getByLabel("Username").fill(viewerName);
  await viewerPage
    .getByRole("textbox", { name: "Password" })
    .fill(viewerPassword);
  await viewerPage.getByRole("button", { name: "Sign in" }).click();
  await expect(viewerPage).toHaveURL(/\/runs$/);
  await viewerPage.goto(`${base}/runs/${runID}?tab=info`);
  await expect(
    viewerPage.getByRole("link", { name: "Migration Info", exact: true }),
  ).toBeVisible();
  await viewerPage
    .getByRole("link", { name: "Migration Info", exact: true })
    .click();
  await expect(
    viewerPage.getByRole("heading", { name: "Native migrations" }),
  ).toBeVisible();
  const viewerInfoRow = viewerPage
    .getByRole("table")
    .getByRole("row")
    .filter({ hasText: "browser gate" });
  await viewerInfoRow.getByRole("button").first().click();
  await viewerPage
    .getByRole("dialog")
    .getByRole("link", { name: "Open SQL script" })
    .click();
  await expect(
    viewerPage.locator(".sql-source tbody tr td:nth-child(2) code").first(),
  ).toBeVisible();
  await viewerPage.goto(`${base}/projects/${project.id}`);
  await expect(
    viewerPage.getByRole("link", { name: "History", exact: true }),
  ).toBeVisible();
  await expect(
    viewerPage.getByRole("link", { name: "Run migration", exact: true }),
  ).toHaveCount(0);
  const operatorBrowser = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
  });
  const operatorPage = await operatorBrowser.newPage();
  operatorPage.setDefaultTimeout(30000);
  await operatorPage.goto(`${base}/login`);
  await operatorPage.getByLabel("Username").fill(operatorName);
  await operatorPage
    .getByRole("textbox", { name: "Password" })
    .fill(operatorPassword);
  await operatorPage.getByRole("button", { name: "Sign in" }).click();
  await expect(operatorPage).toHaveURL(/\/runs$/);
  await operatorPage.goto(`${base}/projects/${project.id}`);
  await expect(
    operatorPage.getByRole("link", { name: "Run migration", exact: true }),
  ).toBeVisible();
  redactedEvidence.role_checks.viewer_readonly_browser = true;
  redactedEvidence.role_checks.operator_operate_preflight = true;
  const service = await api(
    "POST",
    "/access/service-accounts",
    {
      name: "gate-f-scoped-service",
      purpose: "Verify project schema scope",
      owner_id: admin.user.id,
    },
    201,
  );
  await api("PUT", `/access/service-accounts/${service.id}/grants`, {
    grants: [{ ...grants[0], read: true, operate: true }],
  });
  const issued = await api(
    "POST",
    `/access/service-accounts/${service.id}/tokens`,
    {
      name: "Gate F browser",
      expires_at: new Date(Date.now() + 3600000).toISOString(),
    },
    201,
  );
  const serviceContext = await (
    await import("../../web/node_modules/playwright-core/index.mjs")
  ).request.newContext({
    baseURL: base,
    extraHTTPHeaders: { Authorization: `Bearer ${issued.token}` },
  });
  const scopedTargets = (
    await (await serviceContext.get("/api/v1/migration-connections")).json()
  ).data;
  if (!scopedTargets.some((x) => x.id === target.id))
    throw new Error("Service token cannot see its granted target.");
  const scopedSchemas = await serviceContext.get(
    `/api/v1/migration-connections/${target.id}/schemas`,
  );
  if (
    scopedSchemas.status() !== 200 ||
    (await scopedSchemas.json()).data.some((x) => x.name === "crash")
  )
    throw new Error("Service token scope leaked the ungranted crash schema.");
  redactedEvidence.role_checks.service_schema_scope = "passed";
  const serviceCrossScopeWrite = await serviceContext.post(
    `/api/v1/artifacts?target_id=${target.id}&schema_id=${crashSchema.id}`,
    {
      multipart: {
        files: {
          name: "V9__service_cross_scope.sql",
          mimeType: "text/plain",
          buffer: Buffer.from("SELECT 1;\n"),
        },
      },
    },
  );
  if (serviceCrossScopeWrite.status() !== 403)
    throw new Error(
      `Service cross-schema artifact write was not denied (HTTP ${serviceCrossScopeWrite.status()}).`,
    );
  redactedEvidence.role_checks.service_cross_schema_write_denied = true;
  const historyBeforeService = psql(
    dbName,
    "SELECT count(*) FROM app.flyway_schema_history WHERE success=true",
  );
  if (historyBeforeService !== "3")
    throw new Error(
      `Expected three successful app history rows before service V3, found ${historyBeforeService}.`,
    );
  const serviceMigration = {
    name: "V3__service_account.sql",
    mimeType: "text/plain",
    buffer: Buffer.from(
      "INSERT INTO manager_browser_rows VALUES (3, 'service-v3');\n",
    ),
  };
  const serviceUpload = await serviceContext.post(
    `/api/v1/artifacts?target_id=${target.id}&schema_id=${schema.id}`,
    {
      multipart: {
        files: serviceMigration,
      },
    },
  );
  if (serviceUpload.status() !== 201)
    throw new Error(
      `Scoped service artifact upload returned ${serviceUpload.status()}.`,
    );
  const serviceArtifact = (await serviceUpload.json()).data;
  const serviceInput = {
    target_id: target.id,
    schema_id: schema.id,
    execution_account_id: run.execution_account_id,
    artifact_id: serviceArtifact.id,
    context: {},
  };
  const servicePreflight = await serviceContext.post(
    "/api/v1/migration-preflight",
    { data: serviceInput, timeout: 180_000 },
  );
  if (
    servicePreflight.status() !== 200 ||
    !(await servicePreflight.json()).data.ready
  )
    throw new Error(
      `Scoped service preflight did not admit fresh V3 (HTTP ${servicePreflight.status()}).`,
    );
  const serviceKey = randomBytes(18).toString("hex");
  const serviceSubmit = await serviceContext.post("/api/v1/migration-runs", {
    headers: { "Idempotency-Key": serviceKey },
    data: serviceInput,
  });
  if (serviceSubmit.status() !== 202)
    throw new Error(
      `Scoped service run submission returned ${serviceSubmit.status()}.`,
    );
  const serviceRun = (await serviceSubmit.json()).data;
  const duplicateServiceSubmit = await serviceContext.post(
    "/api/v1/migration-runs",
    { headers: { "Idempotency-Key": serviceKey }, data: serviceInput },
  );
  if (
    duplicateServiceSubmit.status() !== 202 ||
    (await duplicateServiceSubmit.json()).data.id !== serviceRun.id
  )
    throw new Error(
      "Exact duplicate service submission did not return the original run.",
    );
  let serviceFinished;
  const serviceDeadline = Date.now() + 240_000;
  while (Date.now() < serviceDeadline) {
    const r = await serviceContext.get(
      `/api/v1/migration-runs/${serviceRun.id}`,
    );
    if (r.status() !== 200)
      throw new Error(`Service run polling returned ${r.status()}.`);
    serviceFinished = (await r.json()).data;
    if (
      ["SUCCEEDED", "FAILED", "BLOCKED", "NEEDS_REVIEW"].includes(
        serviceFinished.status,
      )
    )
      break;
    await new Promise((resolveWait) => setTimeout(resolveWait, 500));
  }
  if (
    serviceFinished?.status !== "SUCCEEDED" ||
    serviceFinished.actor_kind !== "service"
  )
    throw new Error(
      `Service V3 run did not succeed as service actor (${serviceFinished?.status}/${serviceFinished?.actor_kind}).`,
    );
  const historyAfterService = psql(
    dbName,
    "SELECT count(*) FROM app.flyway_schema_history WHERE success=true",
  );
  if (Number(historyAfterService) !== Number(historyBeforeService) + 1)
    throw new Error(
      "Service V3 did not add exactly one successful migration history row.",
    );
  const serviceRows = psqlJSON(
    dbName,
    "SELECT COALESCE(json_agg(to_jsonb(t) ORDER BY id),'[]'::json) FROM app.manager_browser_rows t",
  );
  if (
    !Array.isArray(serviceRows) ||
    serviceRows.length !== 3 ||
    serviceRows[2].note !== "service-v3"
  )
    throw new Error("Service V3 SQL did not produce its expected database row.");
  const mainRunAfterService = await api(
    "GET",
    `/migration-runs/${runID}`,
  );
  if (
    mainRunAfterService.artifact_id !== run.artifact_id ||
    mainRunAfterService.status !== "SUCCEEDED"
  )
    throw new Error("Service execution changed the original immutable run.");
  redactedEvidence.role_checks.service_queue = {
    run_id: serviceRun.id,
    status: serviceFinished.status,
    actor_kind: serviceFinished.actor_kind,
    artifact_id: serviceArtifact.id,
    fresh_v3_applied: true,
    database_rows_after_v3: serviceRows,
    exact_duplicate_idempotency: true,
    successful_history_rows_after_v3: Number(historyAfterService),
    original_run_artifact_unchanged: true,
  };
  const prodTarget = await api(
    "POST",
    "/connections",
    {
      target: {
        name: `PROD ${prodDbName}`,
        environment: "PROD",
        engine: "postgresql",
        host: lab.PGHOST,
        port: Number(lab.PGPORT),
        database_name: prodDbName,
        transport: "TCPS",
        profile: {
          auth_kind: "password",
          tls_mode: "verify-full",
          ca_pem: await readFile(lab.PGSSLROOTCERT, "utf8"),
        },
      },
      username: lab.PGUSER,
      password: lab.PGPASSWORD,
      schemas: ["prod_app"],
      grant_current_user: false,
    },
    201,
  );
  const prodSchema = prodTarget.schemas.find((x) => x.name === "prod_app");
  if (!prodSchema) throw new Error("PROD control target was not registered.");
  await api("PUT", `/access/users/${encodeURIComponent(operator.id)}/grants`, {
    grants: [
      ...grants,
      {
        target_id: prodTarget.target.id,
        schema_id: prodSchema.id,
        read: true,
        operate: true,
        prod: false,
      },
    ],
  });
  await api(
    "PUT",
    `/access/users/${encodeURIComponent(admin.user.id)}/grants`,
    {
      grants: [
        ...grants,
        {
          target_id: prodTarget.target.id,
          schema_id: prodSchema.id,
          read: true,
          operate: true,
          prod: true,
        },
      ],
    },
  );
  const prodUpload = await request.post(
    `/api/v1/artifacts?target_id=${prodTarget.target.id}&schema_id=${prodSchema.id}`,
    {
      headers,
      multipart: {
        files: {
          name: "V1__prod_authorized.sql",
          mimeType: "text/plain",
          buffer: Buffer.from(
            "CREATE TABLE prod_authorized_rows (id integer PRIMARY KEY, note text NOT NULL);\nINSERT INTO prod_authorized_rows VALUES (1, 'admin-prod');\n",
          ),
        },
      },
    },
  );
  if (prodUpload.status() !== 201)
    throw new Error(
      `PROD denial fixture artifact upload returned ${prodUpload.status()}.`,
    );
  const prodArtifact = (await prodUpload.json()).data;
  const operatorTargets = await operatorContext.get(
    "/api/v1/database-targets",
  );
  if (operatorTargets.status() !== 200)
    throw new Error("Operator could not read granted catalog targets.");
  const visibleProdTarget = (await operatorTargets.json()).data.find(
    (item) => item.id === prodTarget.target.id,
  );
  if (!visibleProdTarget)
    throw new Error(
      "Operator could not see the explicitly granted PROD target.",
    );
  const operatorProdSchemas = await operatorContext.get(
    `/api/v1/database-targets/${prodTarget.target.id}/schemas`,
  );
  if (
    operatorProdSchemas.status() !== 200 ||
    !(await operatorProdSchemas.json()).data.some(
      (item) => item.id === prodSchema.id,
    )
  )
    throw new Error(
      "Operator could not read the explicitly granted PROD schema.",
    );
  const operatorEligibleTargets = await operatorContext.get("/api/v1/migration-connections");
  if (operatorEligibleTargets.status() !== 200 || (await operatorEligibleTargets.json()).data.some((item) => item.id === prodTarget.target.id))
    throw new Error("PROD target appeared in migration destinations without explicit PROD authority.");
  const prodDeniedResponse = await operatorContext.post(
    "/api/v1/migration-runs",
    {
      headers: {
        "X-CSRF-Token": operatorSession.csrf_token,
        "Idempotency-Key": randomBytes(16).toString("hex"),
      },
      data: {
        target_id: prodTarget.target.id,
        schema_id: prodSchema.id,
        execution_account_id: prodTarget.account.id,
        artifact_id: prodArtifact.id,
        context: {},
      },
    },
  );
  if (prodDeniedResponse.status() !== 403)
    throw new Error(
      `Operator PROD submission was not denied (HTTP ${prodDeniedResponse.status()}).`,
    );
  const adminProdInput = {
    target_id: prodTarget.target.id,
    schema_id: prodSchema.id,
    execution_account_id: prodTarget.account.id,
    artifact_id: prodArtifact.id,
    context: {},
  };
  const adminProdPreflight = await api(
    "POST",
    "/migration-preflight",
    adminProdInput,
    200,
  );
  if (!adminProdPreflight.ready)
    throw new Error(
      `Admin PROD preflight was not ready: ${JSON.stringify(adminProdPreflight.blockers || [])}.`,
    );
  const adminProdSubmit = await api(
    "POST",
    "/migration-runs",
    adminProdInput,
    202,
    { "Idempotency-Key": randomBytes(16).toString("hex") },
  );
  let adminProdFinished;
  const adminProdDeadline = Date.now() + 240_000;
  while (Date.now() < adminProdDeadline) {
    adminProdFinished = await api(
      "GET",
      `/migration-runs/${adminProdSubmit.id}`,
    );
    if (
      ["SUCCEEDED", "FAILED", "BLOCKED", "NEEDS_REVIEW"].includes(
        adminProdFinished.status,
      )
    )
      break;
    await new Promise((resolveWait) => setTimeout(resolveWait, 500));
  }
  if (adminProdFinished?.status !== "SUCCEEDED")
    throw new Error(
      `Authorized Admin PROD migration ended ${adminProdFinished?.status || "without a terminal result"}.`,
    );
  const prodRows = psqlJSON(
    prodDbName,
    "SELECT COALESCE(json_agg(to_jsonb(t) ORDER BY id),'[]'::json) FROM prod_app.prod_authorized_rows t",
  );
  const prodHistory = psqlJSON(
    prodDbName,
    "SELECT COALESCE(json_agg(to_jsonb(t) ORDER BY installed_rank),'[]'::json) FROM prod_app.flyway_schema_history t",
  );
  if (
    !Array.isArray(prodRows) ||
    prodRows.length !== 1 ||
    prodRows[0].note !== "admin-prod" ||
    !Array.isArray(prodHistory) ||
    !prodHistory.some((row) => row.version === "1" && row.success === true)
  )
    throw new Error("Authorized Admin PROD database effects/history were not recorded.");
  redactedEvidence.role_checks.prod_denied = true;
  redactedEvidence.role_checks.admin_prod_allowed = {
    run_id: adminProdSubmit.id,
    status: adminProdFinished.status,
    rows: prodRows,
    history: prodHistory,
  };
  redactedEvidence.role_checks.prod_scope_visible_but_denied = true;
  redactedEvidence.role_checks.prod_migration_destination_hidden = true;
  redactedEvidence.role_checks.viewer_operate_denied = true;
  redactedEvidence.browser_page_errors = pageErrors.length;
  if (pageErrors.length)
    throw new Error("Browser page errors occurred during acceptance.");
  const mainExecutions = await api(
    "GET",
    `/migration-runs/${runID}/executions`,
  );
  const mainLogs = await api("GET", `/migration-runs/${runID}/logs?limit=100`);
  const serviceExecutions = await api(
    "GET",
    `/migration-runs/${serviceRun.id}/executions`,
  );
  const serviceLogs = await api(
    "GET",
    `/migration-runs/${serviceRun.id}/logs?limit=100`,
  );
  redactedEvidence.run_records = {
    main: {
      id: runID,
      status: run.status,
      actor_kind: run.actor_kind,
      target_id: target.id,
      schema_id: schema.id,
      artifact_id: run.artifact_id,
      executions: executionMetadata(mainExecutions),
      logs: logRecords(mainLogs),
      database: {
        rows: psqlJSON(
          dbName,
          "SELECT COALESCE(json_agg(to_jsonb(t) ORDER BY id),'[]'::json) FROM app.manager_browser_rows t",
        ),
        history: psqlJSON(
          dbName,
          "SELECT COALESCE(json_agg(to_jsonb(t) ORDER BY installed_rank),'[]'::json) FROM app.flyway_schema_history t",
        ),
      },
    },
    service: {
      id: serviceRun.id,
      status: serviceFinished.status,
      actor_kind: serviceFinished.actor_kind,
      artifact_id: serviceFinished.artifact_id,
      executions: executionMetadata(serviceExecutions),
      logs: logRecords(serviceLogs),
      successful_history_rows: Number(historyAfterService),
    },
    admin_prod: {
      id: adminProdSubmit.id,
      status: adminProdFinished.status,
      artifact_id: prodArtifact.id,
      database: { rows: prodRows, history: prodHistory },
    },
  };
  const cookieStates = await Promise.all(
    [
      request,
      serviceContext,
      viewerContext,
      operatorContext,
      context,
      viewerBrowser,
      operatorBrowser,
    ].map((sessionContext) => sessionContext.storageState()),
  );
  const cookieSecrets = cookieStates.flatMap((state) =>
    state.cookies.map((cookie) => cookie.value),
  );
  const finalEvidence = JSON.stringify(redactedEvidence, null, 2) + "\n";
  for (const secret of [
    lab.PGPASSWORD,
    adminPassword,
    bootstrapToken,
    admin.csrf_token,
    issued.token,
    viewerPassword,
    viewerSession.csrf_token,
    operatorPassword,
    operatorSession.csrf_token,
    ...cookieSecrets,
  ]) {
    if (secret && finalEvidence.includes(secret))
      throw new Error(
        "Acceptance evidence contains a private credential or session token.",
      );
  }
  if (
    /^(?:.*(?:cookie:|authorization:|x-csrf-token:).*)$/im.test(
      finalEvidence,
    ) ||
    /-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/.test(finalEvidence) ||
    /password\s*[:=]\s*[^" ,}]+/i.test(finalEvidence)
  )
    throw new Error(
      "Acceptance evidence contains a private header or key marker.",
    );
  await writeFile(join(evidence, "manager-browser.json"), finalEvidence, {
    mode: 0o600,
  });
  console.log(
    JSON.stringify({
      result: "PASS",
      evidence: join(evidence, "manager-browser.json"),
      database: dbName,
      run_id: runID,
      manager_attempt: attempt,
    }),
  );
  await viewerBrowser.close();
  await operatorBrowser.close();
  await viewerContext.dispose();
  await operatorContext.dispose();
  await serviceContext.dispose();
  await request.dispose();
  await context.close();
} catch (e) {
  const safeError =
    `${e?.name || "Error"}: ${String(e?.message || e).split(/\r?\n/, 1)[0]}`.slice(
      0,
      500,
    );
  await writeFile(
    join(evidence, "manager-browser-failure.json"),
    JSON.stringify(
      {
        database: dbName,
        manager_attempt: attempt,
        error: safeError,
        observed: redactedEvidence,
      },
      null,
      2,
    ) + "\n",
    { mode: 0o600 },
  ).catch(() => {});
  throw e;
} finally {
  if (browser) await browser.close().catch(() => {});
  await stop();
}
