import { defineConfig } from "@playwright/test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
export default defineConfig({
  testDir: "./tests",
  fullyParallel: true,
  workers: 2,
  timeout: 30000,
  retries: 0,
  reporter: "list",
  use: {
    baseURL: "http://127.0.0.1:18081",
    headless: true,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH }
      : {},
  },
  webServer: {
    command: "../bin/flyway-manager",
    url: "http://127.0.0.1:18081/api/v1/health",
    reuseExistingServer: false,
    env: {
      FLYWAY_MANAGER_CONFIG: "",
      FLYWAY_MANAGER_PUBLIC_URL: "",
      FLYWAY_MANAGER_LISTEN_ADDRESS: "127.0.0.1:18081",
      FLYWAY_MANAGER_DATA_DIR: mkdtempSync(
        join(tmpdir(), "flyway-manager-e2e-"),
      ),
    },
  },
});
