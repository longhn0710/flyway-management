import { test, expect } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdtemp, mkdir, readFile, writeFile, rm } from "node:fs/promises";
import { createHash } from "node:crypto";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const states = [
  "Success",
  "Pending",
  "Failed",
  "Missing",
  "Superseded",
  "Ignored",
  "Out of Order",
  "Baseline",
  "Below Baseline",
  "Above Target",
  "Future",
  "Outdated",
  "Deleted",
  "Available",
  "Undone",
  "New Native State",
];

test("Projects and native Info: real APIs, read-only refresh, RBAC, browser workflows and responsive evidence", async ({
  page,
}) => {
  test.setTimeout(120000);
  page.setDefaultTimeout(10000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  const listener = createServer();
  await new Promise<void>((r) => listener.listen(0, "127.0.0.1", r));
  const port = (listener.address() as { port: number }).port;
  await new Promise<void>((r) => listener.close(() => r()));
  const dir = await mkdtemp(join(tmpdir(), "fwm-project-info-")),
    root = join(dir, "runtime"),
    marker = join(dir, "commands");
  const base = `http://127.0.0.1:${port}`,
    output = process.env.FLYWAY_MANAGER_EVIDENCE_DIR
      ? resolve(process.env.FLYWAY_MANAGER_EVIDENCE_DIR, "projects-info")
      : resolve("../docs/evidence/projects-info-20261016");
  await mkdir(output, { recursive: true });
  const native = {
    operation: "info",
    flywayVersion: "13.10.3",
    schemaVersion: "4",
    database: "Oracle fixture",
    migrations: states.map((state, i) => ({
      category: i === 4 ? "Repeatable" : "Versioned",
      version: i === 4 ? null : String(i + 1),
      description: `migration ${state}`,
      type: "SQL",
      installedOnUTC: i === 1 ? "" : "2026-10-16T00:00:00Z",
      state,
      undoable: "No",
      installedBy: i === 1 ? "" : "APP_DEPLOY",
      executionTime: i,
      filepath: i === 0 ? "V1__browser.sql" : `V${i + 1}__example.sql`,
    })),
  };
  const runtimeFiles: Record<string, string> = {
    flyway: `#!/bin/sh\nif [ "$1" = '-v' ]; then echo 'Flyway Community Edition 13.10.3'; exit 0; fi\nprintf '%s\\n' "$1" >> '${marker}'\nif [ "$1" != 'info' ]; then echo 'forbidden test command' >&2; exit 9; fi\nprintf '%s\\n' '${JSON.stringify(native)}'\n`,
    "jre/bin/java": "#!/bin/sh\nexit 0\n",
    "drivers/ojdbc-test.jar": "fixture",
    "lib/flyway-database-oracle.jar": "fixture",
  };
  for (const [name, value] of Object.entries(runtimeFiles)) {
    await mkdir(join(root, name, ".."), { recursive: true });
    await writeFile(join(root, name), value, {
      mode: name === "flyway" || name.endsWith("/java") ? 0o700 : 0o600,
    });
  }
  const manifest = Object.entries(runtimeFiles)
    .map(
      ([name, value]) =>
        `${createHash("sha256").update(value).digest("hex")}  ${name}\n`,
    )
    .join("");
  await writeFile(join(root, "flyway-manifest.sha256"), manifest);
  const config = join(dir, "config.json");
  await writeFile(
    config,
    JSON.stringify({
      flyway: {
        executable: join(root, "flyway"),
        version: "13.10.3",
        sha256: createHash("sha256").update(manifest).digest("hex"),
        timeout_seconds: 15,
      },
    }),
  );
  const server = spawn(resolve("../bin/flyway-manager"), [], {
    env: {
      ...process.env,
      FLYWAY_MANAGER_CONFIG: config,
      FLYWAY_MANAGER_DATA_DIR: join(dir, "data"),
      FLYWAY_MANAGER_LISTEN_ADDRESS: `127.0.0.1:${port}`,
      FLYWAY_MANAGER_PUBLIC_URL: "",
    },
    stdio: "ignore",
  });
  const pageErrors: string[] = [];
  page.on("pageerror", (e) => pageErrors.push(e.message));
  try {
    await expect
      .poll(async () => {
        try {
          return (await page.request.get(base + "/api/v1/health")).status();
        } catch {
          return 0;
        }
      })
      .toBe(200);
    const status = (
      await (await page.request.get(base + "/api/v1/auth/status")).json()
    ).data;
    const boot = await page.request.post(base + "/api/v1/auth/bootstrap", {
      headers: { Origin: base },
      data: {
        username: "project-admin",
        password: "Project-admin-password-2026",
        setup_token: (
          await readFile(status.bootstrap_token_file, "utf8")
        ).trim(),
      },
    });
    expect(boot.status()).toBe(201);
    const admin = (await boot.json()).data;
    let headers = { Origin: base, "X-CSRF-Token": admin.csrf_token };
    const post = async (path: string, data: unknown, expected = 201) => {
      const res = await page.request.post(base + "/api/v1" + path, {
        headers,
        data,
      });
      expect(res.status(), await res.text()).toBe(expected);
      return (await res.json()).data;
    };
    const put = async (path: string, data: unknown) => {
      const res = await page.request.put(base + "/api/v1" + path, {
        headers,
        data,
      });
      expect(res.status(), await res.text()).toBe(200);
      return (await res.json()).data;
    };
    for (const [id, environment] of [
      ["shared", "DEV"],
      ["sit", "SIT"],
      ["uat", "UAT"],
      ["prod", "PROD"],
    ])
      await post("/database-targets", {
        id,
        name: `Oracle ${environment}`,
        environment,
        host: `${id}.invalid`,
        port: 1521,
        service: "APP",
        transport: "TCP",
      });
    const app = await post("/database-targets/shared/schemas", {
        name: "APPLICATION",
      }),
      other = await post("/database-targets/shared/schemas", {
        name: "OTHER_SERVICE",
      }),
      unassigned = await post("/database-targets/shared/schemas", {
        name: "UNASSIGNED",
      }),
      uat = await post("/database-targets/uat/schemas", {
        name: "APPLICATION_UAT",
      });
    await post("/database-targets/sit/schemas", { name: "APPLICATION_SIT" });
    await post("/database-targets/prod/schemas", { name: "APPLICATION_PROD" });
    const account = await post("/execution-accounts", {
      target_id: "shared",
      name: "Read-only inspection account",
      username: "APP_DEPLOY",
      schema_ids: [app.id],
      password: "fixture-credential-must-stay-private",
    });
    await put(`/access/users/${admin.user.id}/grants`, {
      grants: [{ target_id: "shared", read: true, operate: true }],
    });
    const second = await post("/projects", {
      name: "Second service",
      description: "Shared Oracle instance",
      status: "active",
      members: [{ target_id: "shared", schema_id: other.id }],
    });
    // Exercise project creation and membership through the actual browser.
    await page.goto(base + "/projects");
    await page
      .getByRole("button", { name: "Create project", exact: true })
      .click();
    await page
      .getByRole("textbox", { name: "Name", exact: true })
      .fill("Payments platform");
    await page
      .getByLabel("Description", { exact: true })
      .fill("DEV, SIT, UAT and PROD database resources");
    await page
      .getByLabel("Oracle DEV / APPLICATION · DEV", { exact: true })
      .check();
    await page
      .getByLabel("Oracle UAT / APPLICATION_UAT · UAT", { exact: true })
      .check();
    await page
      .getByLabel("Oracle SIT / APPLICATION_SIT · SIT", { exact: true })
      .check();
    await page
      .getByLabel("Oracle PROD / APPLICATION_PROD · PROD", { exact: true })
      .check();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Create project", exact: true })
      .click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await page
      .getByPlaceholder("Search project, identifier or description…")
      .fill("Payments");
    await expect(
      page.getByRole("link", { name: "Second service", exact: true }),
    ).toHaveCount(0);
    await page.reload();
    await expect(
      page.getByPlaceholder("Search project, identifier or description…"),
    ).toHaveValue("Payments");
    await page
      .getByRole("link", { name: "Payments platform", exact: true })
      .click();
    const pid = page.url().split("/projects/")[1];
    await expect(
      page.getByRole("link", { name: "APPLICATION", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "APPLICATION_SIT", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "APPLICATION_PROD", exact: true }),
    ).toBeVisible();
    await expect(
      page
        .getByRole("row")
        .filter({
          has: page.getByRole("link", {
            name: "APPLICATION_PROD",
            exact: true,
          }),
        })
        .getByRole("link", { name: "Run migration", exact: true }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("link", { name: "APPLICATION_UAT", exact: true }),
    ).toBeVisible();
    await expect(page.getByText("UNASSIGNED", { exact: true })).toHaveCount(0);
    await page.screenshot({
      path: join(output, "project-desktop.png"),
      fullPage: true,
    });
    await page.setViewportSize({ width: 390, height: 1000 });
    await expect
      .poll(() =>
        page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      )
      .toBe(true);
    await page.screenshot({
      path: join(output, "project-mobile.png"),
      fullPage: true,
    });
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.getByRole("link", { name: "APPLICATION", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "APPLICATION", exact: true }),
    ).toBeVisible();
    await page.getByRole("link", { name: "Flyway Info", exact: true }).click();
    await expect(
      page.getByText("No recorded Flyway Info.", { exact: false }),
    ).toBeVisible();
    await page
      .getByLabel("Execution account", { exact: true })
      .selectOption(account.id);
    await page.getByLabel("Migration artifact", { exact: true }).setInputFiles({
      name: "V1__browser.sql",
      mimeType: "application/sql",
      buffer: Buffer.from("CREATE TABLE must_never_execute(id NUMBER);"),
    });
    await page
      .getByRole("button", { name: "Upload immutable artifact" })
      .click();
    await expect(
      page.getByRole("button", { name: "Refresh Flyway Info", exact: true }),
    ).toBeEnabled();
    await page
      .getByRole("button", { name: "Refresh Flyway Info", exact: true })
      .click();
    await expect(
      page.getByRole("heading", { name: "Native migrations", exact: true }),
    ).toBeVisible();
    for (const state of states)
      await expect(
        page.getByRole("cell", { name: state, exact: true }),
      ).toBeVisible();
    await expect(
      page.getByText("HISTORY_UNSAFE:", { exact: false }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "migration Success", exact: true })
      .click();
    await expect(page.getByRole("dialog")).toContainText("APP_DEPLOY");
    await expect(page.getByRole("dialog")).toContainText("executionTime");
    await page.getByRole("button", { name: "Close migration details" }).click();
    await expect(
      page.getByRole("button", { name: "migration Success", exact: true }),
    ).toBeFocused();
    await page.getByLabel("Native state").selectOption("Missing");
    await expect(
      page.getByRole("cell", { name: "Missing", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("cell", { name: "Success", exact: true }),
    ).toHaveCount(0);
    await page.reload();
    await expect(page.getByLabel("Native state")).toHaveValue("Missing");
    await page.getByLabel("Native state").selectOption("");
    await expect(page.getByLabel("Native state")).toHaveValue("");
    await page.goBack();
    await expect(page.getByLabel("Native state")).toHaveValue("Missing");
    await page.goForward();
    await expect(page.getByLabel("Native state")).toHaveValue("");
    await page
      .getByPlaceholder("Search version, description or filepath…")
      .fill("New Native");
    await expect(
      page.getByRole("cell", { name: "New Native State", exact: true }),
    ).toBeVisible();
    await page
      .getByPlaceholder("Search version, description or filepath…")
      .fill("");
    await page.evaluate(() => {
      (document.activeElement as HTMLElement)?.blur();
      window.scrollTo(0, 0);
    });
    await page.screenshot({
      path: join(output, "info-desktop.png"),
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
          path: join(output, "info-mobile.png"),
          fullPage: true,
        });
    }
    const data = (
      await (
        await page.request.get(
          base +
            `/api/v1/database-targets/shared/schemas/${app.id}/flyway-info`,
        )
      ).json()
    ).data;
    expect(data.json.migrations).toHaveLength(states.length);
    expect(data.source).toBe("read-only-refresh");
    const bad = await page.request.post(
      base +
        `/api/v1/database-targets/uat/schemas/${uat.id}/flyway-info/refresh`,
      {
        headers,
        data: {
          execution_account_id: account.id,
          artifact_id: data.artifact_id,
        },
      },
    );
    expect(bad.status()).toBeGreaterThanOrEqual(400);
    const runs = (
      await (await page.request.get(base + "/api/v1/migration-runs")).json()
    ).data;
    expect(runs).toHaveLength(0);
    expect((await readFile(marker, "utf8")).trim()).toBe("info");
    // Deterministic response variants verify browser error/empty/partial recovery.
    const infoURL =
      base + `/api/v1/database-targets/shared/schemas/${app.id}/flyway-info`;
    const viewURL = page.url();
    let mode = "error";
    await page.route(infoURL, async (route) => {
      if (mode === "error")
        return route.fulfill({
          status: 503,
          json: {
            error: {
              code: "TEMPORARY",
              message: "Info temporarily unavailable",
            },
          },
        });
      const capture = {
        ...data,
        captured_at: "2025-01-01T00:00:00Z",
        success: mode !== "partial",
        json: mode === "partial" ? null : { ...data.json, migrations: [] },
        warnings: ["Recorded snapshot, not live database state."],
      };
      return route.fulfill({ json: { data: capture } });
    });
    await page.reload();
    await expect(
      page.getByText("Info temporarily unavailable", { exact: false }),
    ).toBeVisible();
    mode = "empty";
    await page.getByRole("button", { name: "Retry", exact: true }).click();
    await expect(
      page.getByText(
        "An empty table does not prove that schema history exists.",
        { exact: false },
      ),
    ).toBeVisible();
    await expect(
      page.getByText("Recorded snapshot, not live database state.", {
        exact: true,
      }),
    ).toBeVisible();
    mode = "partial";
    await page.reload();
    await expect(
      page.getByText("Partial / failed inspection", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("Native migration table unavailable.", { exact: false }),
    ).toBeVisible();
    await page.unroute(infoURL);
    await page.goto(viewURL);
    await expect(
      page.getByRole("heading", { name: "Native migrations", exact: true }),
    ).toBeVisible();
    // Archive blocks membership edits, restore then remove a scope.
    await page.goto(base + `/projects/${pid}`);
    await page.getByRole("button", { name: "Edit project" }).click();
    await page.getByLabel("Status", { exact: true }).selectOption("archived");
    await page.getByRole("button", { name: "Save project" }).click();
    await expect(
      page.getByText("Archived project.", { exact: false }),
    ).toBeVisible();
    await page.goto(base + "/projects?status=archived&q=Payments");
    await expect(
      page.getByRole("link", { name: "Second service", exact: true }),
    ).toHaveCount(0);
    await page
      .getByRole("link", { name: "Payments platform", exact: true })
      .click();
    await page.getByRole("button", { name: "Edit project" }).click();
    await expect(
      page.getByLabel("Oracle DEV / APPLICATION · DEV", { exact: true }),
    ).toBeDisabled();
    await page.getByLabel("Status", { exact: true }).selectOption("active");
    await page.getByRole("button", { name: "Save project" }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await page.getByRole("button", { name: "Edit project" }).click();
    await page
      .getByLabel("Oracle UAT / APPLICATION_UAT · UAT", { exact: true })
      .uncheck();
    await page.getByRole("button", { name: "Save project" }).click();
    await expect(
      page.getByRole("link", { name: "APPLICATION_UAT", exact: true }),
    ).toHaveCount(0);
    await page
      .getByRole("tab", { name: "Migration history", exact: true })
      .click();
    await expect(
      page.getByText("History uses current project membership", {
        exact: false,
      }),
    ).toBeVisible();
    await expect(
      page.getByText("No migration runs match these filters."),
    ).toBeVisible();
    const viewer = await post("/access/users", {
      username: "project-viewer",
      display_name: "Project viewer",
      role: "Viewer",
      password: "Project-viewer-password-2026",
    });
    await put(`/access/users/${viewer.id}/grants`, {
      grants: [{ target_id: "shared", schema_id: app.id, read: true }],
    });
    const scopeProject = await post("/projects", {
      name: "Instance scoped project",
      members: [{ target_id: "shared" }],
    });
    const login = await post(
      "/auth/login",
      {
        provider: "local",
        username: "project-viewer",
        password: "Project-viewer-password-2026",
      },
      200,
    );
    headers = { Origin: base, "X-CSRF-Token": login.csrf_token };
    const visible = (
      await (await page.request.get(base + "/api/v1/projects")).json()
    ).data;
    expect(visible.map((p: { id: string }) => p.id)).toEqual(
      expect.arrayContaining([pid, scopeProject.id]),
    );
    expect(visible.some((p: { id: string }) => p.id === second.id)).toBe(false);
    await page.goto(base + `/projects/${scopeProject.id}`);
    await expect(
      page.getByRole("link", { name: "APPLICATION", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "OTHER_SERVICE", exact: true }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("link", { name: "Run migration", exact: true }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Edit project" }),
    ).toHaveCount(0);
    let accountRequests = 0;
    page.on("request", (r) => {
      if (r.url().endsWith("/execution-accounts")) accountRequests++;
    });
    await page.getByRole("link", { name: "Flyway Info", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "Native migrations", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Refresh Flyway Info", exact: true }),
    ).toHaveCount(0);
    expect(accountRequests).toBe(0);
    expect(
      (
        await page.request.post(base + "/api/v1/projects", {
          headers,
          data: { name: "denied" },
        })
      ).status(),
    ).toBe(403);
    expect(
      (
        await page.request.post(
          base +
            `/api/v1/database-targets/shared/schemas/${app.id}/flyway-info/refresh`,
          {
            headers,
            data: {
              execution_account_id: account.id,
              artifact_id: data.artifact_id,
            },
          },
        )
      ).status(),
    ).toBe(403);
    expect(
      (
        await page.request.get(
          base +
            `/api/v1/database-targets/shared/schemas/${unassigned.id}/flyway-info`,
        )
      ).status(),
    ).toBe(403);
    expect(pageErrors).toEqual([]);
    await writeFile(
      join(output, "browser-acceptance.json"),
      JSON.stringify(
        {
          backend: "real disposable SQLite / API",
          runtime: "deterministic pinned fixture; no Oracle connection",
          native_states: states,
          commands: (await readFile(marker, "utf8")).trim().split("\n"),
          project_membership: true,
          environments: ["DEV", "SIT", "UAT", "PROD"],
          archive_restore: true,
          roles: ["Admin", "Viewer"],
          schema_only_grant_instance_membership: true,
          widths: [390, 768, 1024, 1440],
          page_errors: pageErrors,
        },
        null,
        2,
      ),
    );
  } finally {
    server.kill("SIGTERM");
    await new Promise<void>((r) => server.once("exit", () => r()));
    await rm(dir, { recursive: true, force: true });
  }
});
