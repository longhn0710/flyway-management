import { test, expect } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

async function unusedPort() {
  const server = createServer();
  await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
  const port = (server.address() as { port: number }).port;
  await new Promise<void>((done) => server.close(() => done()));
  return port;
}

test("project Deployment Targets, grants, scoped tokens, and attributed history", async ({
  page,
}) => {
  test.setTimeout(120_000);
  page.setDefaultTimeout(15_000);
  const port = await unusedPort();
  const dir = await mkdtemp(join(tmpdir(), "fwm-external-api-"));
  const base = `http://127.0.0.1:${port}`;
  const runtime = join(dir, "runtime");
  await mkdir(join(runtime, "jre/bin"), { recursive: true });
  await mkdir(join(runtime, "drivers"), { recursive: true });
  await mkdir(join(runtime, "lib"), { recursive: true });
  const runtimeFiles = {
    flyway:
      "#!/bin/sh\nif [ \"$1\" = '-v' ]; then echo 'Flyway Community Edition 13.10.3'; exit 0; fi\necho 'unexpected runtime call' >&2; exit 8\n",
    "jre/bin/java": "#!/bin/sh\nexit 0\n",
    "drivers/ojdbc-test.jar": "fixture",
    "lib/flyway-database-oracle.jar": "fixture",
  };
  for (const [name, content] of Object.entries(runtimeFiles))
    await writeFile(join(runtime, name), content, {
      mode: name.endsWith(".jar") ? 0o600 : 0o700,
    });
  const manifest = Object.entries(runtimeFiles)
    .map(
      ([name, content]) =>
        `${createHash("sha256").update(content).digest("hex")}  ${name}\n`,
    )
    .join("");
  await writeFile(join(runtime, "flyway-manifest.sha256"), manifest);
  const config = join(dir, "config.json");
  await writeFile(
    config,
    JSON.stringify({
      listen_address: `127.0.0.1:${port}`,
      data_dir: join(dir, "data"),
      public_url: "",
      flyway: {
        executable: join(runtime, "flyway"),
        version: "13.10.3",
        sha256: createHash("sha256").update(manifest).digest("hex"),
        timeout_seconds: 15,
      },
    }),
  );
  const child: ChildProcess = spawn(resolve("../bin/flyway-manager"), [], {
    env: {
      ...process.env,
      FLYWAY_MANAGER_CONFIG: config,
      FLYWAY_MANAGER_DATA_DIR: join(dir, "data"),
      FLYWAY_MANAGER_LISTEN_ADDRESS: `127.0.0.1:${port}`,
      FLYWAY_MANAGER_PUBLIC_URL: "",
    },
    stdio: "ignore",
  });
  try {
    await expect
      .poll(async () => {
        try {
          return (await page.request.get(`${base}/api/v1/health`)).status();
        } catch {
          return 0;
        }
      })
      .toBe(200);
    const auth = (
      await (await page.request.get(`${base}/api/v1/auth/status`)).json()
    ).data;
    const boot = await page.request.post(`${base}/api/v1/auth/bootstrap`, {
      headers: { Origin: base },
      data: {
        username: "external-admin",
        display_name: "External API Admin",
        password: "External-admin-password-2026",
        setup_token: (await readFile(auth.bootstrap_token_file, "utf8")).trim(),
      },
    });
    expect(boot.status()).toBe(201);
    const admin = (await boot.json()).data;
    const headers = { Origin: base, "X-CSRF-Token": admin.csrf_token };
    const post = async (path: string, body: unknown, expected = 201) => {
      const response = await page.request.post(`${base}/api/v1${path}`, {
        headers,
        data: body,
      });
      expect(response.status(), await response.text()).toBe(expected);
      return (await response.json()).data;
    };
    const put = async (path: string, body: unknown) => {
      const response = await page.request.put(`${base}/api/v1${path}`, {
        headers,
        data: body,
      });
      expect(response.status(), await response.text()).toBe(200);
      return (await response.json()).data;
    };

    await post("/database-targets", {
      id: "external-db",
      name: "External DEV",
      environment: "DEV",
      host: "external.invalid",
      port: 1521,
      service: "APP",
      transport: "TCP",
    });
    const schema = await post("/database-targets/external-db/schemas", {
      name: "APP",
    });
    await put(`/access/users/${admin.user.id}/grants`, {
      grants: [
        {
          target_id: "external-db",
          schema_id: schema.id,
          read: true,
          operate: true,
          prod: false,
        },
      ],
    });
    await post("/execution-accounts", {
      target_id: "external-db",
      name: "Deploy account",
      username: "APP_DEPLOY",
      schema_ids: [schema.id],
      password: "fixture-only-password",
    });
    const project = await post("/projects", {
      name: "External Project",
      description: "v2 UI acceptance",
      status: "active",
      members: [{ target_id: "external-db", schema_id: schema.id }],
    });
    const service = await post("/access/service-accounts", {
      name: "external-ci",
      purpose: "Project deployment acceptance",
      owner_id: admin.user.id,
    });
    await put(`/access/service-accounts/${service.id}/grants`, {
      grants: [
        {
          target_id: "external-db",
          schema_id: schema.id,
          read: true,
          operate: true,
          prod: false,
        },
      ],
    });

    await page.goto(`${base}/projects/${project.id}?tab=targets`);
    await page
      .getByRole("button", { name: "Create deployment target" })
      .click();
    const targetDialog = page.getByRole("dialog");
    await targetDialog
      .getByRole("textbox", { name: "Name" })
      .fill("Release destination");
    await targetDialog
      .getByLabel("Registered schema")
      .selectOption({ label: "External DEV · APP · DEV" });
    await expect(targetDialog.getByText("DEV", { exact: true })).toBeVisible();
    await targetDialog
      .getByLabel("Execution account")
      .selectOption({ label: "Deploy account · APP_DEPLOY" });
    await targetDialog.getByRole("button", { name: "Create target" }).click();
    const targetRow = page
      .getByRole("row")
      .filter({ hasText: "Release destination" });
    await expect(targetRow).toContainText("Active · rev 1");
    await targetRow.getByRole("button", { name: "Manage" }).click();
    await targetRow
      .getByLabel("Display name")
      .fill("Release destination renamed");
    const renameRequest = page.waitForRequest(
      (request) =>
        request
          .url()
          .includes(`/api/v2/projects/${project.id}/deployment-targets/`) &&
        request.method() === "PATCH",
    );
    await targetRow.getByRole("button", { name: "Save revision 2" }).click();
    expect((await renameRequest).headers()["if-match"]).toBe('"1"');
    await expect(targetRow).toContainText("Release destination renamed");
    await targetRow.getByRole("button", { name: "Manage" }).click();
    await targetRow.getByLabel("Principal type").selectOption("user");
    await targetRow
      .getByLabel("Principal", { exact: true })
      .selectOption({ label: "External API Admin (external-admin)" });
    await targetRow.getByLabel("read", { exact: true }).check();
    await targetRow.getByLabel("execute", { exact: true }).check();
    await targetRow.getByRole("button", { name: "Save permissions" }).click();
    await expect(targetRow).toContainText("Current grant loaded");
    await expect(targetRow.getByLabel("read", { exact: true })).toBeChecked();
    await expect(
      targetRow.getByLabel("execute", { exact: true }),
    ).toBeChecked();

    await page.goto(`${base}/access/service-accounts`);
    await page.getByRole("button", { name: /external-ci/ }).click();
    await page.getByLabel("Token name").fill("scoped-release-token");
    await page
      .getByLabel("Expiration")
      .fill(new Date(Date.now() + 86400000).toISOString().slice(0, 16));
    await expect(
      page.getByText(
        "Database/schema grants remain required for the service account.",
        { exact: false },
      ),
    ).toBeVisible();
    await expect(
      page.getByLabel(
        "Issue legacy token without project Deployment Target scopes",
      ),
    ).not.toBeChecked();
    await page
      .getByLabel("Issue legacy token without project Deployment Target scopes")
      .check();
    await expect(
      page.getByText("Deployment Target token scopes", { exact: true }),
    ).toHaveCount(0);
    await page
      .getByLabel("Issue legacy token without project Deployment Target scopes")
      .uncheck();
    const targetScope = page
      .getByText("External Project · Release destination renamed")
      .locator("..");
    await expect(targetScope).toBeVisible();
    await targetScope.getByLabel("execute", { exact: true }).check();
    const tokenResponse = page.waitForResponse(
      (response) =>
        response
          .url()
          .includes(`/access/service-accounts/${service.id}/tokens`) &&
        response.request().method() === "POST",
    );
    await page
      .getByRole("button", { name: "Create token", exact: true })
      .click();
    const tokenRequest = await (await tokenResponse).request().postDataJSON();
    expect(tokenRequest.deployment_scopes).toEqual([
      {
        deployment_target_id: expect.any(String),
        read: true,
        upload: false,
        preflight: false,
        execute: true,
      },
    ]);
    await expect(
      page.getByRole("dialog", { name: "Copy your API token now" }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Done" }).click();

    await page.goto(`${base}/projects/${project.id}?tab=targets`);
    const rowAfterToken = page
      .getByRole("row")
      .filter({ hasText: "Release destination renamed" });
    await rowAfterToken.getByRole("button", { name: "Manage" }).click();
    await rowAfterToken.getByLabel("Active", { exact: true }).uncheck();
    const retireRequest = page.waitForRequest(
      (request) =>
        request
          .url()
          .includes(`/api/v2/projects/${project.id}/deployment-targets/`) &&
        request.method() === "PATCH",
    );
    await rowAfterToken
      .getByRole("button", { name: "Save revision 3" })
      .click();
    expect((await retireRequest).headers()["if-match"]).toBe('"2"');
    await expect(rowAfterToken).toContainText("Retired · rev 3");
    await page.getByRole("tab", { name: "Project history" }).click();
    await expect(
      page.getByRole("heading", { name: "Project attributed history" }),
    ).toBeVisible();
    await expect(
      page.getByText("No project attributed runs yet."),
    ).toBeVisible();
    await page.getByRole("tab", { name: "Legacy history" }).click();
    await expect(
      page.getByText(
        "Legacy history is grouped using current project membership.",
      ),
    ).toBeVisible();
  } finally {
    if (child.exitCode === null && !child.killed) {
      child.kill("SIGTERM");
      await Promise.race([
        new Promise<void>((done) => child.once("exit", () => done())),
        new Promise<void>((done) => setTimeout(done, 4000)),
      ]);
    }
    await rm(dir, { recursive: true, force: true });
  }
});
