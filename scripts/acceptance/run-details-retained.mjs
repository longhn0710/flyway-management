import { chromium, expect } from "../../web/node_modules/@playwright/test/index.mjs";
import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { relative, resolve } from "node:path";
import { spawn } from "node:child_process";

const root = resolve(import.meta.dirname, "../..");
const dataDir = process.env.FWM_RETAINED_DATA_DIR;
const masterKey = process.env.FWM_RETAINED_MASTER_KEY;
const sourceDB = process.env.FWM_SOURCE_DB;
const sourceMasterKey = process.env.FWM_SOURCE_MASTER_KEY;
const credentialsFile = process.env.FWM_LOCAL_ADMIN_CREDENTIALS;
const baseURL = process.env.FWM_ACCEPTANCE_BASE_URL ?? "http://127.0.0.1:18119";
const evidenceDir = resolve(
  process.env.FWM_EVIDENCE_DIR ?? `${root}/docs/evidence/run-details-ux-20261017/after`,
);
const runID = "DG5lFUs-8nPKq7_Ra8hqNB";
if (!dataDir) throw new Error("Set FWM_RETAINED_DATA_DIR to the private copied v7 data directory.");
if (!masterKey) throw new Error("Set FWM_RETAINED_MASTER_KEY to the private copy of the retained external key.");
if (!sourceDB || !sourceMasterKey) throw new Error("Set FWM_SOURCE_DB and FWM_SOURCE_MASTER_KEY so this run can verify the retained originals remain unchanged.");
if (!credentialsFile) throw new Error("Set FWM_LOCAL_ADMIN_CREDENTIALS to the private local admin credentials JSON.");
const credentials = JSON.parse(await readFile(credentialsFile, "utf8"));
if (!credentials.username || !credentials.password) throw new Error("Local admin credentials JSON must contain a username and password.");
const hash = (bytes) => createHash("sha256").update(bytes).digest("hex");
const sourceHashesBefore = {
  database: hash(await readFile(sourceDB)),
  master_key: hash(await readFile(sourceMasterKey)),
};
const copiedKeyHash = hash(await readFile(masterKey));
if (copiedKeyHash !== sourceHashesBefore.master_key) throw new Error("The private master key copy differs from the retained source key.");

const db = await import("node:sqlite");
const sqlite = new db.DatabaseSync(resolve(dataDir, "manager.db"), { readOnly: true });
const row = sqlite.prepare("SELECT artifact_id,status,repair_outcome,flyway_version FROM migration_runs WHERE id=?").get(runID);
if (!row || row.status !== "SUCCEEDED" || row.repair_outcome !== "SKIPPED" || row.flyway_version !== "13.10.3") {
  throw new Error("Retained fixture does not match the expected Flyway 13.10.3 success with Repair skipped.");
}
const scripts = sqlite.prepare("SELECT b.name,b.sha256,b.content FROM artifact_blobs b JOIN migration_run_items i ON i.artifact_id=b.artifact_id AND i.path=b.name WHERE i.run_id=? ORDER BY i.path").all(runID);
if (scripts.length !== 2) throw new Error(`Expected two retained scripts, got ${scripts.length}.`);
for (const script of scripts) {
  if (hash(script.content) !== script.sha256) throw new Error(`Retained bytes fail SHA-256: ${script.name}`);
  script.text = Buffer.from(script.content).toString("utf8");
}
const info = sqlite.prepare("SELECT result FROM run_executions WHERE run_id=? AND command='info' ORDER BY started_at DESC LIMIT 1").get(runID);
const nativeInfo = JSON.parse(info?.result ?? "null");
if (!nativeInfo || nativeInfo.flywayVersion !== "13.10.3" || !Array.isArray(nativeInfo.migrations)) throw new Error("Run-bound native Info JSON is absent or invalid.");
sqlite.close();

await mkdir(evidenceDir, { recursive: true });
const configPath = resolve(dataDir, "acceptance-config.json");
await writeFile(configPath, JSON.stringify({
  listen_address: "127.0.0.1:18119",
  data_dir: dataDir,
  master_key_file: masterKey,
  log_level: "info",
  flyway: { version: "13.10.3", timeout_seconds: 300 },
}));
const proc = spawn(resolve(root, "bin/flyway-manager"), [], { cwd: root,
  env: { ...process.env, FLYWAY_MANAGER_CONFIG: configPath, FLYWAY_MANAGER_DATA_DIR: dataDir, FLYWAY_MANAGER_LISTEN_ADDRESS: "127.0.0.1:18119", FLYWAY_MANAGER_PUBLIC_URL: "" },
  stdio: ["ignore", "ignore", "inherit"],
});
const cleanup = async () => { if (!proc.killed) proc.kill("SIGTERM"); };
process.on("SIGINT", () => void cleanup());
process.on("SIGTERM", () => void cleanup());
try {
  let ready = false;
  for (let i = 0; i < 90; i++) {
    try { const r = await fetch(`${baseURL}/api/v1/health`); if (r.ok) { ready = true; break; } } catch {}
    await new Promise((r) => setTimeout(r, 1000));
  }
  if (!ready) throw new Error("Local retained Manager did not become ready.");

  const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: baseURL });
  const denied = [];
  await context.route("**/*", async (route) => {
    const req = route.request();
    if (req.method() === "POST" && new URL(req.url()).pathname === "/api/v1/auth/login") {
      return route.continue();
    }
    if (! ["GET", "HEAD"].includes(req.method())) {
      denied.push({ method: req.method(), url: new URL(req.url()).pathname });
      return route.abort();
    }
    return route.continue();
  });
  const page = await context.newPage();
  const pageErrors = [];
  const consoleErrors = [];
  let fullscreenMobileRect = null;
  let loginStatus = null;
  let authenticated = false;
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => {
    if (authenticated && message.type() === "error") consoleErrors.push(message.text());
  });
  page.on("response", (response) => {
    if (new URL(response.url()).pathname === "/api/v1/auth/login") loginStatus = response.status();
  });
  await page.goto(baseURL);
  await page.getByRole("textbox", { name: "Username" }).fill(credentials.username);
  await page.getByRole("textbox", { name: "Password" }).fill(credentials.password);
  await page.getByRole("button", { name: /sign in|log in/i }).click();
  try {
    await page.waitForURL(/\/(runs|home)(\/|\?|$)/, { timeout: 8000 });
  } catch {
    throw new Error(`Local login did not navigate. status=${loginStatus}; url=${page.url()}; page=${(await page.locator("body").innerText()).slice(0, 500)}`);
  }
  authenticated = true;
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(`${baseURL}/runs/${runID}`);
  await expect(page.locator(".run-overview")).toContainText("SUCCEEDED");
  await expect(page.getByText("SKIPPED", { exact: true })).toBeVisible();
  await expect(page.getByText("No repair needed", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Execution Logs" })).toBeVisible();
  await page.screenshot({ path: resolve(evidenceDir, "run-details-1440.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: resolve(evidenceDir, "run-overview-390.png"), fullPage: true });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("link", { name: "Migration Info", exact: true }).click();

  const infoTable = page.getByRole("table");
  await expect(infoTable.getByRole("row").filter({ hasText: "wizard acceptance" })).toHaveCount(1);
  await expect(infoTable.getByRole("row").filter({ hasText: "wizard deployed acceptance" })).toHaveCount(1);
  await expect(infoTable.getByText("Success", { exact: true })).toHaveCount(2);
  await page.screenshot({ path: resolve(evidenceDir, "migration-info-1440.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: resolve(evidenceDir, "migration-info-390.png"), fullPage: true });
  await page.setViewportSize({ width: 1440, height: 1000 });
  for (const script of scripts) {
    const description = script.name.includes("deployed") ? "wizard deployed acceptance" : "wizard acceptance";
    const infoRow = infoTable.getByRole("row").filter({ hasText: description });
    await infoRow.getByRole("button", { name: script.name.startsWith("V1") ? "1" : "2" }).click();
    await page.getByRole("dialog").getByRole("link", { name: "Open SQL script" }).click();
    const url = new URL(page.url());
    if (url.searchParams.get("path") !== script.name || url.searchParams.get("artifact_id") !== row.artifact_id) {
      throw new Error(`Migration Info opened the wrong source SQL for ${script.name}.`);
    }
    const code = page.locator(".sql-source tbody tr td:nth-child(2) code");
    await expect(code.first()).toBeVisible();
    const displayed = await code.evaluateAll((nodes) => nodes.map((node) => node.textContent ?? "").join("\n"));
    if (displayed.replaceAll("\u200b", "") !== script.text) throw new Error(`SQL preview differs from immutable artifact bytes for ${script.name}: displayed=${JSON.stringify(displayed)}, expected=${JSON.stringify(script.text)}.`);
    await expect(page.locator(".sql-line-number").first()).toContainText("1");
    await expect(page.locator(".sql-keyword").first()).toBeVisible();
    if (script.name.startsWith("V1")) await page.screenshot({ path: resolve(evidenceDir, "sql-reader-1440.png"), fullPage: true });
    await page.getByRole("textbox", { name: "Find in SQL" }).fill("SYS_CONTEXT");
    await expect(page.getByRole("status").filter({ hasText: /of [1-9]\d* matches/ })).toBeVisible();
    await page.getByRole("button", { name: "Copy SQL" }).click();
    await expect(page.getByRole("status").filter({ hasText: "SQL copied" })).toBeVisible();
    const copied = await page.evaluate(() => navigator.clipboard.readText());
    if (copied !== script.text) throw new Error(`Clipboard differs from immutable bytes for ${script.name}.`);
    if (script.name.startsWith("V1")) await page.setViewportSize({ width: 390, height: 844 });
    await page.getByRole("button", { name: "Full-screen reader" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByRole("region", { name: "SQL source" })).toBeVisible();
    if (script.name.startsWith("V1")) {
      const rect = await dialog.boundingBox();
      if (!rect || Math.abs(rect.x) > 1 || Math.abs(rect.y) > 1 || Math.abs(rect.width - 390) > 1 || Math.abs(rect.height - 844) > 1) {
        throw new Error(`Mobile full-screen reader does not fill the 390x844 viewport: ${JSON.stringify(rect)}.`);
      }
      fullscreenMobileRect = rect;
      await page.screenshot({ path: resolve(evidenceDir, "sql-fullscreen-390.png"), fullPage: false });
    }
    await page.getByRole("button", { name: "Close full-screen reader" }).click();
    if (script.name.startsWith("V1")) {
      await page.screenshot({ path: resolve(evidenceDir, "sql-reader-390.png"), fullPage: true });
      await page.setViewportSize({ width: 1440, height: 1000 });
    }
    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page.getByRole("link", { name: "Download original file" }).click(),
    ]);
    if (download.suggestedFilename() !== script.name) throw new Error(`Unexpected download filename for ${script.name}.`);
    const downloaded = await readFile(await download.path());
    if (!downloaded.equals(Buffer.from(script.content))) throw new Error(`Download bytes differ from immutable artifact for ${script.name}.`);
    await page.getByRole("link", { name: "Return to Migration Info" }).click();
    await expect(page.getByRole("heading", { name: "Native migrations" })).toBeVisible();
    await page.reload();
    await expect(page.getByRole("heading", { name: "Native migrations" })).toBeVisible();
  }
  await page.getByRole("link", { name: "Execution Logs" }).click();
  await expect(page.getByRole("heading", { name: "Searchable logs" })).toBeVisible();
  const pause = page.getByRole("button", { name: "Pause follow" });
  await expect(pause).toHaveAttribute("aria-pressed", "true");
  await pause.click();
  await expect(page.getByRole("button", { name: "Follow logs" })).toHaveAttribute("aria-pressed", "false");
  await page.getByRole("textbox", { name: "Search" }).fill("Picked up JAVA_TOOL_OPTIONS");
  await expect(page.locator(".log-viewer")).toContainText("JAVA_TOOL_OPTIONS");
  await page.getByLabel("Log channel").selectOption("stderr");
  await expect(page.locator(".log-viewer")).toContainText("stderr");
  await page.screenshot({ path: resolve(evidenceDir, "execution-logs-1440.png"), fullPage: true });
  await page.setViewportSize({ width: 768, height: 900 });
  await page.screenshot({ path: resolve(evidenceDir, "execution-logs-768.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: resolve(evidenceDir, "execution-logs-390.png"), fullPage: true });
  await page.goto(`${baseURL}/runs/${runID}?tab=info`);
  await page.reload();
  await expect(page.getByRole("table").getByRole("row").filter({ hasText: "wizard acceptance" })).toHaveCount(1);
  await expect(page.getByText("Native JSON")).toBeHidden();
  const sourceHashesAfter = {
    database: hash(await readFile(sourceDB)),
    master_key: hash(await readFile(sourceMasterKey)),
  };
  if (JSON.stringify(sourceHashesBefore) !== JSON.stringify(sourceHashesAfter)) throw new Error("The retained source database or key changed during local acceptance.");
  if (pageErrors.length || consoleErrors.length) throw new Error(`Browser errors occurred: page=${pageErrors.length}, console=${JSON.stringify(consoleErrors)}.`);
  const result = {
    run_id: runID,
    status: row.status,
    repair_outcome: row.repair_outcome,
    runtime: row.flyway_version,
    info_migrations: nativeInfo.migrations.length,
    scripts: scripts.map(({ name, sha256, content }) => ({ name, sha256, bytes: content.length })),
    journey: ["Overview", "Migration Info rows", "Open V1/V2 SQL", "Preview byte equality", "Clipboard byte equality", "Original download byte equality", "SQL search/highlighting/line numbers/full-screen", "Execution Logs search/channel/follow", "direct link and refresh"],
    blocked_mutations: denied,
    browser_page_errors: pageErrors.length,
    browser_console_errors: consoleErrors.length,
    retained_source_sha256: { database: sourceHashesBefore.database },
    retained_source_unchanged: true,
    copied_master_key_sha256_matches_source: copiedKeyHash === sourceHashesBefore.master_key,
    screenshot_dir: relative(root, evidenceDir),
    screenshots: ["run-details-1440.png", "run-overview-390.png", "migration-info-1440.png", "migration-info-390.png", "sql-reader-1440.png", "sql-reader-390.png", "sql-fullscreen-390.png", "execution-logs-1440.png", "execution-logs-390.png", "execution-logs-768.png"],
    mobile_fullscreen_reader_rect: fullscreenMobileRect,
  };
  await writeFile(resolve(evidenceDir, "retained-fixture.json"), JSON.stringify(result, null, 2));
  await context.close();
  await browser.close();
  if (denied.length) throw new Error(`Unexpected blocked non-GET browser requests: ${JSON.stringify(denied)}`);
  console.log(JSON.stringify(result, null, 2));
} finally {
  await cleanup();
}
