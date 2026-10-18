import { defineConfig } from "@playwright/test";
import { chmodSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const outputDir = mkdtempSync(join(tmpdir(), "fwm-keycloak-results-"));
chmodSync(outputDir, 0o700);

export default defineConfig({
  testDir: "./tests",
  testMatch: "keycloak-live.spec.ts",
  outputDir,
  fullyParallel: false,
  workers: 1,
  timeout: 360_000,
  expect: { timeout: 30_000 },
  retries: 0,
  reporter: "list",
  use: {
    headless: true,
    ignoreHTTPSErrors: true,
    trace: "off",
    screenshot: "off",
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH }
      : {},
  },
});
