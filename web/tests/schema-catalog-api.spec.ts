import { test, expect } from "@playwright/test";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { mkdtemp, readFile, rm, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

test("real APIs enforce schema catalog visibility for Admin, Operator and Viewer", async ({
  page,
}) => {
  const listener = createServer();
  await new Promise<void>((r) => listener.listen(0, "127.0.0.1", r));
  const port = (listener.address() as { port: number }).port;
  await new Promise<void>((r) => listener.close(() => r()));
  const directory = await mkdtemp(join(tmpdir(), "fwm-catalog-rbac-"));
  const base = `http://127.0.0.1:${port}`;
  const server = spawn(resolve("../bin/flyway-manager"), [], {
    env: {
      ...process.env,
      FLYWAY_MANAGER_CONFIG: "",
      FLYWAY_MANAGER_PUBLIC_URL: "",
      FLYWAY_MANAGER_DATA_DIR: directory,
      FLYWAY_MANAGER_LISTEN_ADDRESS: `127.0.0.1:${port}`,
    },
    stdio: "ignore",
  });
  const evidence: {
    role: string;
    targets: string[];
    schemas: string[];
    deniedStatus?: number;
  }[] = [];
  const output = process.env.FLYWAY_MANAGER_EVIDENCE_DIR
    ? resolve(process.env.FLYWAY_MANAGER_EVIDENCE_DIR, "catalog-rbac")
    : resolve("../docs/evidence/schema-catalog-20261016");
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
    const bootstrap = await page.request.post(base + "/api/v1/auth/bootstrap", {
      headers: { Origin: base },
      data: {
        username: "catalog-admin",
        password: "Catalog-admin-password-2026",
        setup_token: (
          await readFile(status.bootstrap_token_file, "utf8")
        ).trim(),
      },
    });
    expect(bootstrap.status()).toBe(201);
    const admin = (await bootstrap.json()).data;
    const headers = { Origin: base, "X-CSRF-Token": admin.csrf_token };
    const post = async (path: string, data: unknown) => {
      const r = await page.request.post(base + "/api/v1" + path, {
        headers,
        data,
      });
      expect(r.status()).toBe(201);
      return (await r.json()).data;
    };
    const put = async (path: string, data: unknown) => {
      const r = await page.request.put(base + "/api/v1" + path, {
        headers,
        data,
      });
      expect(r.status()).toBe(200);
      return (await r.json()).data;
    };
    for (const [id, name, environment] of [
      ["finance", "Finance UAT", "UAT"],
      ["billing", "Billing Production", "PROD"],
    ])
      await post("/database-targets", {
        id,
        name,
        environment,
        host: `${id}.invalid`,
        port: 1521,
        service: "ACCEPTANCE",
        transport: "TCP",
      });
    const visible = await post("/database-targets/finance/schemas", {
      name: "CORE_BANKING",
    });
    const hidden = await post("/database-targets/finance/schemas", {
      name: "PRIVATE_FINANCE",
    });
    await post("/database-targets/billing/schemas", { name: "BILLING" });
    await post("/execution-accounts", {
      target_id: "finance",
      name: "Finance deploy",
      username: "FINANCE_DEPLOY",
      schema_ids: [visible.id],
      password: "disposable-local-fixture",
    });
    for (const role of ["Operator", "Viewer"]) {
      const user = await post("/access/users", {
        username: `catalog-${role.toLowerCase()}`,
        display_name: `Catalog ${role}`,
        role,
        password: "Catalog-role-password-2026",
      });
      await put(`/access/users/${user.id}/grants`, {
        grants: [
          {
            target_id: "finance",
            schema_id: visible.id,
            read: true,
            operate: role === "Operator",
            prod: false,
          },
        ],
      });
    }
    await page.goto(base + "/schemas");
    await expect(page.getByText("3 results · Page 1 / 1")).toBeVisible();
    evidence.push({
      role: "Admin",
      targets: ["finance", "billing"],
      schemas: ["CORE_BANKING", "PRIVATE_FINANCE", "BILLING"],
    });
    await mkdir(output, { recursive: true });
    await page.screenshot({
      path: join(output, "after-representative-catalog-1440.png"),
      fullPage: true,
    });
    for (const role of ["Operator", "Viewer"]) {
      const login = await page.request.post(base + "/api/v1/auth/login", {
        headers: { Origin: base },
        data: {
          provider: "local",
          username: `catalog-${role.toLowerCase()}`,
          password: "Catalog-role-password-2026",
        },
      });
      expect(login.status()).toBe(200);
      const ts = (
        await (
          await page.request.get(base + "/api/v1/database-targets?limit=200")
        ).json()
      ).data;
      const ss = (
        await (
          await page.request.get(
            base + "/api/v1/database-targets/finance/schemas?limit=200",
          )
        ).json()
      ).data;
      expect(ts.map((t: { id: string }) => t.id)).toEqual(["finance"]);
      expect(ss.map((s: { id: string }) => s.id)).toEqual([visible.id]);
      const denied = await page.request.get(
        base + `/api/v1/database-targets/finance/schemas/${hidden.id}`,
      );
      expect(denied.status()).toBe(403);
      evidence.push({
        role,
        targets: ts.map((t: { id: string }) => t.id),
        schemas: ss.map((s: { name: string }) => s.name),
        deniedStatus: denied.status(),
      });
      await page.goto(base + "/schemas");
      await expect(page.locator("tbody tr")).toHaveCount(1);
      await expect(
        page.getByRole("link", { name: "CORE_BANKING", exact: true }),
      ).toBeVisible();
      await expect(
        page.getByText("PRIVATE_FINANCE", { exact: true }),
      ).toHaveCount(0);
      await page.goto(base + `/schemas?schema=${hidden.id}`);
      await expect(
        page.getByText("Schema not found or unavailable", { exact: true }),
      ).toBeVisible();
      await page.goto(base + `/schemas?schema=${visible.id}`);
      await expect(page).toHaveURL(
        new RegExp(`/instances/finance/schemas\\?schema=${visible.id}$`),
      );
      await expect(
        page.getByRole("heading", { name: "CORE_BANKING", exact: true }),
      ).toBeVisible();
      if (role === "Operator")
        await expect(
          page.getByText("Oracle username: FINANCE_DEPLOY", { exact: true }),
        ).toBeVisible();
      else
        await expect(
          page.getByText(
            "Execution account metadata requires operation permission for this schema.",
          ),
        ).toBeVisible();
      await page.screenshot({
        path: join(output, `after-real-api-${role.toLowerCase()}-1440.png`),
        fullPage: true,
      });
    }
    await writeFile(
      join(output, "real-api-rbac.json"),
      JSON.stringify(
        {
          source:
            "Disposable local SQLite registry; actual Go APIs; no Oracle/runtime configured",
          checks: evidence,
          oracleCommands: 0,
        },
        null,
        2,
      ),
    );
  } finally {
    server.kill("SIGTERM");
    if (server.exitCode === null)
      await new Promise<void>((r) => server.once("exit", () => r()));
    await rm(directory, { recursive: true, force: true });
  }
});
