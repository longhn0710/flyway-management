#!/usr/bin/env node

import { readFile, mkdir, chmod, stat, lstat } from "node:fs/promises";
import { dirname, isAbsolute, resolve } from "node:path";
import { chromium } from "../../web/node_modules/playwright/index.mjs";

const stageResults = [];
let currentStage = "configuration";
let browser;
let context;

function record(name, details = {}) {
  stageResults.push({ name, ok: true, ...details });
}

function fail(message, code = "ACCEPTANCE_FAILED") {
  const error = new Error(message);
  error.code = code;
  throw error;
}

async function privateTextFile(filePath, label) {
  if (typeof filePath !== "string" || filePath.length === 0) {
    fail(`${label} path is required`, "INVALID_CONFIG");
  }
  const resolved = resolve(filePath);
  let metadata;
  try {
    metadata = await stat(resolved);
  } catch {
    fail(`${label} file is unavailable`, "PRIVATE_FILE_UNAVAILABLE");
  }
  if (!metadata.isFile() || (process.platform !== "win32" && (metadata.mode & 0o077) !== 0)) {
    fail(`${label} must be a regular file with private permissions (0600 or stricter)`, "PRIVATE_FILE_PERMISSIONS");
  }
  try {
    return await readFile(resolved, "utf8");
  } catch {
    fail(`${label} file cannot be read`, "PRIVATE_FILE_UNAVAILABLE");
  }
}

async function readConfig() {
  const args = process.argv.slice(2);
  let configPath = process.env.FLYWAY_BROWSER_CONFIG;
  if (args.length === 2 && args[0] === "--config") configPath = args[1];
  else if (args.length !== 0) fail("Usage: browser.mjs --config PRIVATE_CONFIG.json", "USAGE");
  if (!configPath || !isAbsolute(configPath)) {
    fail("Provide an absolute config path with --config or FLYWAY_BROWSER_CONFIG", "INVALID_CONFIG");
  }
  const raw = await privateTextFile(configPath, "Browser config");
  let config;
  try {
    config = JSON.parse(raw);
  } catch {
    fail("Browser config is not valid JSON", "INVALID_CONFIG");
  }
  if (!config || typeof config !== "object" || Array.isArray(config)) {
    fail("Browser config must be a JSON object", "INVALID_CONFIG");
  }
  let base;
  try {
    base = new URL(config.baseURL ?? "");
  } catch {
    fail("baseURL must be an absolute URL origin", "INVALID_CONFIG");
  }
  const loopback = ["localhost", "127.0.0.1", "::1"].includes(base.hostname);
  if (base.username || base.password || base.search || base.hash || base.pathname !== "/") {
    fail("baseURL must be an origin without credentials, path, query, or fragment", "INVALID_CONFIG");
  }
  if (base.protocol !== "https:" && !(base.protocol === "http:" && loopback && config.allowInsecureLoopback === true)) {
    fail("baseURL must use HTTPS (HTTP is allowed only for explicitly enabled loopback development)", "INSECURE_BASE_URL");
  }
  if (!config.credentialsFile || !isAbsolute(config.credentialsFile)) {
    fail("credentialsFile must be an absolute path to a private JSON file", "INVALID_CONFIG");
  }
  const credentialRaw = await privateTextFile(config.credentialsFile, "Credential");
  let credentials;
  try {
    credentials = JSON.parse(credentialRaw);
  } catch {
    fail("Credential file is not valid JSON", "INVALID_CREDENTIAL_FILE");
  }
  if (typeof credentials?.username !== "string" || !credentials.username || typeof credentials?.password !== "string" || !credentials.password) {
    fail("Credential file must contain nonempty username and password fields", "INVALID_CREDENTIAL_FILE");
  }
  let bootstrapToken = "";
  if (config.bootstrapTokenFile) {
    if (!isAbsolute(config.bootstrapTokenFile)) {
      fail("bootstrapTokenFile must be an absolute path", "INVALID_CONFIG");
    }
    bootstrapToken = (await privateTextFile(config.bootstrapTokenFile, "Bootstrap token")).trim();
    if (!bootstrapToken) fail("Bootstrap token file is empty", "INVALID_BOOTSTRAP_TOKEN");
  }
  const sqlFiles = config.sqlFiles ?? [];
  if (!Array.isArray(sqlFiles) || sqlFiles.some((p) => typeof p !== "string" || !isAbsolute(p))) {
    fail("sqlFiles must be an array of absolute file paths", "INVALID_CONFIG");
  }
  if (sqlFiles.length && !(config.targetId && config.schemaId && config.accountId)) {
    fail("targetId, schemaId, and accountId are all required when sqlFiles are supplied", "INVALID_CONFIG");
  }
  if (config.requireSecureCookie === false && base.protocol === "https:") {
    fail("requireSecureCookie cannot be disabled for HTTPS", "INVALID_CONFIG");
  }
  return { config, base, credentials, bootstrapToken, sqlFiles };
}

async function apiJson(page, pathname, options = {}) {
  let response;
  try {
    response = await page.request.fetch(new URL(`/api/v1${pathname}`, page.url()).toString(), options);
  } catch {
    fail("Application API request failed", "API_REQUEST_FAILED");
  }
  let body;
  try {
    body = await response.json();
  } catch {
    fail("Application API returned an invalid response", "API_RESPONSE_INVALID");
  }
  if (!response.ok()) fail("Application API returned an unexpected status", "API_STATUS_UNEXPECTED");
  return body?.data;
}

async function visibleLogin(page, config, credentials, bootstrapToken) {
  currentStage = "authentication-status";
  await page.goto(new URL("/login", config.baseURL).toString(), { waitUntil: "domcontentloaded" });
  const auth = await apiJson(page, "/auth/status");
  if (auth.bootstrap_required) {
    if (!bootstrapToken) fail("First-run bootstrap is required; supply bootstrapTokenFile", "BOOTSTRAP_REQUIRED");
    await page.getByLabel("Setup token").fill(bootstrapToken);
    await page.getByLabel("Tên hiển thị").fill(config.displayName ?? "Flyway Manager Administrator");
    await page.getByLabel("Tên đăng nhập").fill(credentials.username);
    await page.getByLabel("Mật khẩu mới").fill(credentials.password);
    await page.getByRole("button", { name: "Tạo quản trị viên" }).click();
    await page.waitForURL((url) => url.pathname === "/runs", { timeout: 30000 });
    record("bootstrap", { required: true, completed: true });
  } else {
    await page.getByLabel("Tên đăng nhập").fill(credentials.username);
    await page.getByRole("textbox", { name: "Mật khẩu" }).fill(credentials.password);
    await page.getByRole("button", { name: "Đăng nhập vào console" }).click();
    await page.waitForURL((url) => url.pathname === "/runs", { timeout: 30000 });
    record("login", { completed: true });
  }
  await page.getByText(credentials.username, { exact: true }).waitFor({ state: "visible", timeout: 15000 });
  const cookies = await context.cookies(config.baseURL);
  const session = cookies.find((cookie) => cookie.name === "fm_session");
  if (!session || !session.httpOnly || session.sameSite !== "Lax") {
    fail("Session cookie is missing required HttpOnly/SameSite attributes", "COOKIE_POLICY_FAILED");
  }
  if (config.requireSecureCookie !== false && !session.secure) {
    fail("HTTPS session cookie is missing Secure", "COOKIE_POLICY_FAILED");
  }
  record("session-cookie", { httpOnly: session.httpOnly, sameSite: session.sameSite, secure: session.secure });
}

async function verifyRuntime(page, config) {
  currentStage = "runtime-readiness";
  const runtime = await apiJson(page, "/runtime");
  if (!runtime?.ready || runtime.integrity !== "verified") {
    fail("Flyway runtime is not ready or its integrity is not verified", "RUNTIME_NOT_READY");
  }
  if (config.expectedFlywayVersion && runtime.version !== config.expectedFlywayVersion) {
    fail("Flyway version does not match expectedFlywayVersion", "RUNTIME_VERSION_MISMATCH");
  }
  record("runtime", { ready: true, integrity: runtime.integrity, version: runtime.version, dependency: runtime.dependency });
}

async function verifyRegistry(page, config) {
  currentStage = "registry-selection";
  if (!(config.targetId && config.schemaId && config.accountId)) return;
  const target = await apiJson(page, `/database-targets/${encodeURIComponent(config.targetId)}`);
  const schema = await apiJson(page, `/database-targets/${encodeURIComponent(config.targetId)}/schemas/${encodeURIComponent(config.schemaId)}`);
  const accounts = await apiJson(page, `/database-targets/${encodeURIComponent(config.targetId)}/schemas/${encodeURIComponent(config.schemaId)}/execution-accounts`);
  const account = Array.isArray(accounts) ? accounts.find((item) => item.id === config.accountId) : null;
  if (!target?.active || schema?.target_id !== target.id || !schema.active || !account?.active || !account.credential?.configured || account.credential.revoked) {
    fail("Configured target, schema, or mapped account is inactive or unavailable", "REGISTRY_SELECTION_INVALID");
  }
  const expected = config.expectedTarget ?? {};
  for (const key of ["host", "port", "service", "transport"]) {
    if (expected[key] !== undefined && target[key] !== expected[key]) {
      fail(`Registered target ${key} does not match expectedTarget`, "TARGET_MISMATCH");
    }
  }
  if (config.expectedSchema && schema.name !== config.expectedSchema) fail("Registered schema does not match expectedSchema", "SCHEMA_MISMATCH");
  if (config.expectedExecutionUsername && account.username !== config.expectedExecutionUsername) fail("Execution account username does not match expectedExecutionUsername", "ACCOUNT_MISMATCH");

  await page.goto(new URL("/runs/new", config.baseURL).toString(), { waitUntil: "domcontentloaded" });
  const targetSelect = page.getByLabel("Database target");
  await targetSelect.selectOption(config.targetId);
  await page.getByLabel("Target schema").selectOption(config.schemaId);
  await page.getByLabel("Execution account").selectOption(config.accountId);
  if (await targetSelect.inputValue() !== config.targetId || await page.getByLabel("Target schema").inputValue() !== config.schemaId || await page.getByLabel("Execution account").inputValue() !== config.accountId) {
    fail("Migration form did not retain the selected target, schema, and account", "SCOPE_SELECTION_FAILED");
  }
  record("registry-selection", { selected: true, targetActive: true, schemaActive: true, accountMapped: true, credentialConfigured: true });
}

async function uploadAndPreflight(page, config, sqlFiles) {
  if (!sqlFiles.length) return;
  currentStage = "artifact-upload";
  const filePayloads = [];
  for (const filePath of sqlFiles) {
    let content;
    try {
      content = await readFile(filePath);
    } catch {
      fail("A configured SQL artifact file cannot be read", "SQL_FILE_UNAVAILABLE");
    }
    filePayloads.push({ name: filePath.split(/[\\/]/).at(-1), mimeType: "text/plain", buffer: content });
  }
  const submissionObserved = { value: false };
  page.on("request", (request) => {
    if (request.method() === "POST" && new URL(request.url()).pathname === "/api/v1/migration-runs") {
      submissionObserved.value = true;
    }
  });
  await page.locator('input[type="file"]').first().setInputFiles(filePayloads);
  await page.getByRole("button", { name: "Upload and normalize" }).click();
  await page.getByText("Immutable artifact ready").waitFor({ state: "visible", timeout: 30000 });
  record("artifact-upload", { fileCount: filePayloads.length, completed: true });

  currentStage = "migration-preflight";
  const responsePromise = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/v1/migration-preflight", { timeout: 65000 });
  await page.getByRole("button", { name: "Run preflight" }).click();
  let response;
  try {
    response = await responsePromise;
  } catch {
    fail("Migration preflight did not return a response", "PREFLIGHT_TIMEOUT");
  }
  if (!response.ok()) fail("Migration preflight returned an unexpected status", "PREFLIGHT_HTTP_FAILED");
  let result;
  try {
    result = (await response.json())?.data;
  } catch {
    fail("Migration preflight returned invalid JSON", "PREFLIGHT_RESPONSE_INVALID");
  }
  if (!result?.ready || result.live_status !== "VERIFIED") {
    fail("Migration preflight did not verify the configured Oracle stream", "PREFLIGHT_NOT_VERIFIED");
  }
  if (config.expectedFlywayVersion && result.runtime_version !== config.expectedFlywayVersion) {
    fail("Preflight Flyway version does not match expectedFlywayVersion", "PREFLIGHT_VERSION_MISMATCH");
  }
  const migrations = result.info?.migrations ?? [];
  for (const expected of config.expectedMigrations ?? []) {
    const found = migrations.find((item) => item.version === expected.version && item.script === expected.script);
    if (!found || found.state !== expected.state) fail("Preflight migration history does not match expectedMigrations", "MIGRATION_HISTORY_MISMATCH");
  }
  if (submissionObserved.value) fail("Browser unexpectedly submitted a migration", "UNEXPECTED_MIGRATION_SUBMISSION");
  await page.getByRole("heading", { name: "Flyway migration preview" }).waitFor({ state: "visible", timeout: 15000 });
  await page.getByRole("button", { name: "Review and submit migration" }).waitFor({ state: "visible", timeout: 15000 });
  record("migration-preflight", {
    verified: true,
    artifactId: response.request().postDataJSON().artifact_id,
    targetId: response.request().postDataJSON().target_id,
    schemaId: response.request().postDataJSON().schema_id,
    accountId: response.request().postDataJSON().execution_account_id,
    runtimeVersion: result.runtime_version,
    migrationCount: migrations.length,
    expectedHistoryChecks: (config.expectedMigrations ?? []).length,
    submitted: false,
  });
}

async function verifyExistingRun(page, config) {
  if (!config.runId) return;
  currentStage = "run-history-details";
  await page.goto(new URL("/runs", config.baseURL).toString(), { waitUntil: "domcontentloaded" });
  const runLink = page.getByRole("link", { name: config.runId, exact: true });
  await runLink.waitFor({ state: "visible", timeout: 15000 });
  await runLink.click();
  await page.getByRole("heading", { name: "Run details" }).waitFor({ state: "visible" });
  const run = await apiJson(page, `/migration-runs/${encodeURIComponent(config.runId)}`);
  if (config.expectedRunStatus && run.status !== config.expectedRunStatus) fail("Run status does not match expectedRunStatus", "RUN_STATUS_MISMATCH");
  if (config.expectedActorName && run.actor_name !== config.expectedActorName) fail("Run actor does not match expectedActorName", "RUN_ACTOR_MISMATCH");
  await page.getByRole("heading", { name: "Searchable logs" }).waitFor({ state: "visible" });
  if (config.expectedArtifactName) await page.getByText(config.expectedArtifactName, { exact: true }).first().waitFor({ state: "visible" });
  await page.reload({ waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Run details" }).waitFor({ state: "visible" });
  record("run-history-details", { found: true, logsVisible: true, refreshed: true, status: run.status });

  if (run.status === "SUCCEEDED") {
    currentStage = "audit-attribution";
    await page.goto(new URL("/access/users", config.baseURL).toString(), { waitUntil: "domcontentloaded" });
    const filter = page.getByLabel("Audit action filter");
    await filter.fill("migration.submit");
    const row = page.getByRole("row").filter({ hasText: config.runId });
    await row.waitFor({ state: "visible", timeout: 15000 });
    if (config.expectedActorName) await row.getByText(config.expectedActorName, { exact: true }).waitFor({ state: "visible" });
    record("audit-attribution", { runSubmissionEventVisible: true, actorMatched: Boolean(config.expectedActorName) });
  }
}

async function logout(page, config, credentials) {
  currentStage = "logout-refresh";
  if (config.screenshotPath) await savePrivateScreenshot(page, config.screenshotPath);
  await page.goto(new URL("/runs", config.baseURL).toString(), { waitUntil: "domcontentloaded" });
  await page.reload({ waitUntil: "domcontentloaded" });
  await page.getByText(credentials.username, { exact: true }).waitFor({ state: "visible", timeout: 15000 });
  await page.getByRole("button", { name: "Đăng xuất" }).click();
  await page.waitForURL((url) => url.pathname === "/login", { timeout: 15000 });
  const remaining = (await context.cookies(config.baseURL)).some((cookie) => cookie.name === "fm_session");
  if (remaining) fail("Session cookie remains after logout", "LOGOUT_FAILED");
  await page.goto(new URL("/access/users", config.baseURL).toString(), { waitUntil: "domcontentloaded" });
  await page.waitForURL((url) => url.pathname === "/login" && url.searchParams.has("next"), { timeout: 15000 });
  record("refresh-navigation-logout", { refreshed: true, logoutClearedSession: true, protectedRouteRedirected: true });
}

async function savePrivateScreenshot(page, screenshotPath) {
  if (!isAbsolute(screenshotPath)) fail("screenshotPath must be absolute", "INVALID_CONFIG");
  const directory = dirname(screenshotPath);
  try {
    await mkdir(directory, { recursive: true, mode: 0o700 });
    if (process.platform !== "win32") {
      const metadata = await stat(directory);
      if ((metadata.mode & 0o077) !== 0) fail("Screenshot directory must have private permissions", "SCREENSHOT_PERMISSIONS");
    }
    try {
      await lstat(screenshotPath);
      fail("Screenshot path already exists; choose a new private path", "SCREENSHOT_PATH_EXISTS");
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
    }
    await page.screenshot({ path: screenshotPath, fullPage: true, animations: "disabled" });
    if (process.platform !== "win32") await chmod(screenshotPath, 0o600);
  } catch (error) {
    if (error?.code) throw error;
    fail("Private screenshot could not be written", "SCREENSHOT_FAILED");
  }
  record("private-screenshot", { saved: true });
}

async function main() {
  const { config, base, credentials, bootstrapToken, sqlFiles } = await readConfig();
  currentStage = "browser-start";
  browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } : {}) });
  context = await browser.newContext({ acceptDownloads: false });
  const page = await context.newPage();
  page.setDefaultTimeout(config.timeoutMs ?? 20000);
  await visibleLogin(page, config, credentials, bootstrapToken);
  await verifyRuntime(page, config);
  await verifyRegistry(page, config);
  await uploadAndPreflight(page, config, sqlFiles);
  await verifyExistingRun(page, config);
  await logout(page, config, credentials);
}

try {
  await main();
  process.stdout.write(`${JSON.stringify({ ok: true, results: stageResults })}\n`);
} catch (error) {
  const code = error?.code ?? "BROWSER_ACCEPTANCE_FAILED";
  stageResults.push({ name: currentStage, ok: false, code });
  process.stdout.write(`${JSON.stringify({ ok: false, results: stageResults })}\n`);
  process.exitCode = 1;
} finally {
  if (context) await context.close().catch(() => {});
  if (browser) await browser.close().catch(() => {});
}
