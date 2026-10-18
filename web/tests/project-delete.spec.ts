import { test, expect } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

for (const viewport of [
  { width: 1440, height: 1000 },
  { width: 390, height: 844 },
]) {
  test(`Safe Delete Project: confirmation, membership, archive and RBAC at ${viewport.width}px`, async ({
    page,
  }, info) => {
    test.setTimeout(90000);
    await page.setViewportSize(viewport);
    const listener = createServer();
    await new Promise<void>((r) => listener.listen(0, "127.0.0.1", r));
    const port = (listener.address() as { port: number }).port;
    await new Promise<void>((r) => listener.close(() => r()));
    const dir = await mkdtemp(join(tmpdir(), "fwm-project-delete-"));
    const base = `http://127.0.0.1:${port}`;
    const server = spawn(resolve("../bin/flyway-manager"), [], {
      env: {
        ...process.env,
        FLYWAY_MANAGER_CONFIG: "",
        FLYWAY_MANAGER_PUBLIC_URL: "",
        FLYWAY_MANAGER_DATA_DIR: dir,
        FLYWAY_MANAGER_LISTEN_ADDRESS: `127.0.0.1:${port}`,
      },
      stdio: "ignore",
    });
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
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
      const bootstrap = await page.request.post(
        base + "/api/v1/auth/bootstrap",
        {
          headers: { Origin: base },
          data: {
            username: "delete-admin",
            password: "Delete-browser-admin-2026",
            setup_token: (
              await readFile(status.bootstrap_token_file, "utf8")
            ).trim(),
          },
        },
      );
      expect(bootstrap.status()).toBe(201);
      const admin = (await bootstrap.json()).data;
      const headers = { Origin: base, "X-CSRF-Token": admin.csrf_token };
      const post = async (path: string, data: unknown) => {
        const result = await page.request.post(base + "/api/v1" + path, {
          headers,
          data,
        });
        expect(result.status(), await result.text()).toBe(201);
        return (await result.json()).data;
      };
      const empty = await post("/projects", { name: "Disposable project" });
      const archived = await post("/projects", {
        name: "Archived empty",
        status: "archived",
      });
      const target = await post("/database-targets", {
        id: "retained-db",
        name: "Retained database",
        environment: "DEV",
        host: "db.example",
        port: 1521,
        service: "APP",
        transport: "TCP",
      });
      const populated = await post("/projects", {
        name: "Keep memberships",
        members: [{ target_id: target.id }],
      });
      const archivedMember = await post("/projects", {
        name: "Archived member",
        status: "archived",
        members: [{ target_id: target.id }],
      });
      await page.goto(base + `/projects/${empty.id}`);
      await page
        .getByRole("button", { name: "Delete project", exact: true })
        .click();
      let dialog = page.getByRole("dialog", {
        name: "Delete project",
        exact: true,
      });
      await expect(
        dialog.getByRole("button", { name: "Delete project", exact: true }),
      ).toBeDisabled();
      await dialog
        .getByLabel("Confirm project name")
        .fill("disposable project");
      await expect(
        dialog.getByRole("button", { name: "Delete project", exact: true }),
      ).toBeDisabled();
      await dialog.getByRole("button", { name: "Cancel" }).click();
      expect(
        (
          await page.request.get(base + `/api/v1/projects/${empty.id}`)
        ).status(),
      ).toBe(200);

      // The server rechecks membership even when the browser opened an empty project.
      await page
        .getByRole("button", { name: "Delete project", exact: true })
        .click();
      dialog = page.getByRole("dialog", {
        name: "Delete project",
        exact: true,
      });
      await dialog.getByLabel("Confirm project name").fill(empty.name);
      const changed = await page.request.put(
        base + `/api/v1/projects/${empty.id}`,
        {
          headers,
          data: { name: empty.name, members: [{ target_id: target.id }] },
        },
      );
      expect(changed.status()).toBe(200);
      await dialog
        .getByRole("button", { name: "Delete project", exact: true })
        .click();
      await expect(
        dialog.getByText(/Remove all project memberships/),
      ).toBeVisible();
      await dialog.getByRole("button", { name: "Cancel" }).click();
      const cleared = await page.request.put(
        base + `/api/v1/projects/${empty.id}`,
        { headers, data: { name: empty.name, members: [] } },
      );
      expect(cleared.status()).toBe(200);
      await page.reload();
      await page
        .getByRole("button", { name: "Delete project", exact: true })
        .click();
      dialog = page.getByRole("dialog", {
        name: "Delete project",
        exact: true,
      });
      await dialog.getByLabel("Confirm project name").fill(empty.name);
      await page.screenshot({
        path: info.outputPath("delete-confirmation.png"),
        fullPage: true,
      });
      await dialog
        .getByRole("button", { name: "Delete project", exact: true })
        .click();
      await expect(page).toHaveURL(base + "/projects");
      await expect(
        page.getByRole("link", { name: empty.name, exact: true }),
      ).toHaveCount(0);
      expect(
        (
          await page.request.get(base + `/api/v1/projects/${empty.id}`)
        ).status(),
      ).toBe(404);
      await page.goto(base + `/projects/${empty.id}`);
      await expect(
        page.getByText("Resource not found.", { exact: false }),
      ).toBeVisible();
      await expect(
        page.getByRole("button", { name: "Delete project", exact: true }),
      ).toHaveCount(0);

      for (const project of [populated, archivedMember]) {
        await page.goto(base + `/projects/${project.id}`);
        await page
          .getByRole("button", { name: "Delete project", exact: true })
          .click();
        dialog = page.getByRole("dialog", {
          name: "Delete project",
          exact: true,
        });
        await expect(dialog.getByLabel("Confirm project name")).toBeDisabled();
        await expect(
          dialog.getByRole("button", { name: "Delete project", exact: true }),
        ).toBeDisabled();
        if (project.status === "archived")
          await expect(dialog.getByText(/Unarchive it first/)).toBeVisible();
        await dialog.getByRole("button", { name: "Cancel" }).click();
      }
      await page.goto(base + `/projects/${archived.id}`);
      await page
        .getByRole("button", { name: "Delete project", exact: true })
        .click();
      dialog = page.getByRole("dialog", {
        name: "Delete project",
        exact: true,
      });
      await dialog.getByLabel("Confirm project name").fill(archived.name);
      await dialog
        .getByRole("button", { name: "Delete project", exact: true })
        .click();
      await expect(page).toHaveURL(base + "/projects");
      const audit = (
        await (
          await page.request.get(
            base + "/api/v1/access/audit?action=project.delete",
          )
        ).json()
      ).data;
      expect(
        audit.map((event: { target: string }) => event.target).sort(),
      ).toEqual([empty.id, archived.id].sort());
      expect(
        (
          await page.request.get(base + `/api/v1/database-targets/${target.id}`)
        ).status(),
      ).toBe(200);
      const viewer = await post("/access/users", {
        username: "delete-viewer",
        password: "Delete-browser-viewer-2026",
        role: "Viewer",
      });
      expect(
        (
          await page.request.put(
            base + `/api/v1/access/users/${viewer.id}/grants`,
            {
              headers,
              data: { grants: [{ target_id: target.id, read: true }] },
            },
          )
        ).status(),
      ).toBe(200);
      expect(
        (
          await page.request.post(base + "/api/v1/auth/logout", {
            headers,
            data: {},
          })
        ).status(),
      ).toBe(200);
      const login = await page.request.post(base + "/api/v1/auth/login", {
        headers: { Origin: base },
        data: {
          provider: "local",
          username: viewer.username,
          password: "Delete-browser-viewer-2026",
        },
      });
      expect(login.status()).toBe(200);
      await page.goto(base + `/projects/${populated.id}`);
      await expect(
        page.getByRole("heading", { name: populated.name, exact: true }),
      ).toBeVisible();
      await expect(
        page.getByRole("button", { name: "Delete project", exact: true }),
      ).toHaveCount(0);
      await expect(
        page.getByRole("button", { name: "Edit project", exact: true }),
      ).toHaveCount(0);
      expect(errors).toEqual([]);
    } finally {
      const exited = new Promise<void>((r) => server.once("exit", () => r()));
      server.kill("SIGTERM");
      await exited;
      await rm(dir, { recursive: true, force: true });
    }
  });
}
