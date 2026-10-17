import { defineConfig } from "@playwright/test";
import { chmodSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const outputDir = mkdtempSync(join(tmpdir(), "fwm-postgres-browser-results-"));
chmodSync(outputDir, 0o700);

// The opt-in live PostgreSQL spec starts its own isolated Manager. Do not start
// the shared 18081 webServer configured by playwright.config.ts.
export default defineConfig({
  testDir: "./tests",
  testMatch: "postgresql-live.spec.ts",
  outputDir,
  fullyParallel: false,
  workers: 1,
  timeout: 300_000,
  expect: { timeout: 50_000 },
  retries: 0,
  reporter: "list",
  use: {
    headless: true,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH }
      : {},
  },
});
