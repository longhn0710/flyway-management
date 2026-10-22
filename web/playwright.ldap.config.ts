import { defineConfig } from "@playwright/test";
import { chmodSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
const outputDir = mkdtempSync(join(tmpdir(), "fwm-ldap-results-"));
chmodSync(outputDir, 0o700);
export default defineConfig({
  testDir: "./tests", testMatch: "ldap-live.spec.ts", outputDir,
  workers: 1, retries: 0, timeout: 600_000, reporter: "list",
  use: { headless: true, ignoreHTTPSErrors: true, trace: "off", screenshot: "off",
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } : {} },
});
