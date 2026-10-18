import { test, expect } from "@playwright/test";
import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { randomBytes, randomUUID } from "node:crypto";
import {
  chmod,
  mkdtemp,
  mkdir,
  readFile,
  rm,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createServer } from "node:net";

const enabled = process.env.FWM_KEYCLOAK_LIVE === "1";
const keycloakImage =
  "quay.io/keycloak/keycloak@sha256:0f198be292568439d700cdbfb893e69a6009bb43a94a06a945b1d3d506c76b13";
const realmName = "flyway-acceptance";
const clientID = "flyway-manager";

async function port(): Promise<number> {
  const server = createServer();
  await new Promise<void>((resolveListen, reject) =>
    server.once("error", reject).listen(0, "127.0.0.1", resolveListen),
  );
  const value = (server.address() as { port: number }).port;
  await new Promise<void>((resolveClose, reject) =>
    server.close((error) => (error ? reject(error) : resolveClose())),
  );
  return value;
}

function run(
  command: string,
  args: string[],
  options: { cwd?: string; env?: NodeJS.ProcessEnv } = {},
): Promise<void> {
  return new Promise((resolveRun, reject) => {
    const child = spawn(command, args, {
      cwd: options.cwd,
      env: options.env,
      stdio: "ignore",
    });
    child.once("error", reject);
    child.once("exit", (code) =>
      code === 0
        ? resolveRun()
        : reject(new Error(`${command} failed with exit code ${code}`)),
    );
  });
}

async function waitFor(url: string, child: ChildProcess, caFile?: string, containerName?: string, privateValues: string[] = []) {
  const until = Date.now() + 180_000;
  while (Date.now() < until) {
    if (child.exitCode !== null) {
      const state = containerName
        ? spawnSync("docker", ["inspect", "--format", "{{.State.Status}} exit={{.State.ExitCode}} oom={{.State.OOMKilled}} error={{.State.Error}}", containerName], { encoding: "utf8" }).stdout.trim()
        : "";
      const rawLogs = containerName
        ? spawnSync("docker", ["logs", containerName], { encoding: "utf8" }).stdout
        : "";
      const safeLogs = privateValues.reduce(
        (value, secret) => value.split(secret).join("[redacted]"),
        rawLogs,
      );
      throw new Error(`isolated Keycloak exited during startup (${state})${safeLogs ? `: ${safeLogs.split("\n").slice(-24).join("\n")}` : ""}`);
    }
    try {
      if (caFile) {
        await run("curl", ["--fail", "--silent", "--show-error", "--cacert", caFile, "--output", "/dev/null", url]);
        return;
      }
      if ((await fetch(url, { signal: AbortSignal.timeout(1_000) })).ok) return;
    } catch {
      // Wait for the isolated provider's HTTPS listener.
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 500));
  }
  throw new Error("timed out waiting for isolated Keycloak");
}

async function stop(child?: ChildProcess) {
  if (!child || child.exitCode !== null) return;
  child.kill("SIGTERM");
  await Promise.race([
    new Promise<void>((resolveExit) => child.once("exit", () => resolveExit())),
    new Promise<void>((resolveTimeout) => setTimeout(resolveTimeout, 8_000)),
  ]);
}

test("real Keycloak OIDC browser acceptance and explicit local RBAC", async ({
  page,
}) => {
  test.skip(!enabled, "set FWM_KEYCLOAK_LIVE=1 to run the isolated Keycloak acceptance");
  test.setTimeout(360_000);
  page.setDefaultTimeout(15_000);
  page.setDefaultNavigationTimeout(15_000);

  const work = await mkdtemp(join(tmpdir(), "fwm-keycloak-acceptance-"));
  await chmod(work, 0o700);
  const dockerName = `fwm-keycloak-acceptance-${randomBytes(6).toString("hex")}`;
  const managerPort = await port();
  const keycloakPort = await port();
  const managerURL = `http://127.0.0.1:${managerPort}`;
  const issuer = `https://127.0.0.1:${keycloakPort}/realms/${realmName}`;
  const clientSecret = randomBytes(32).toString("base64url");
  const adminPassword = randomBytes(28).toString("base64url");
  const operatorPassword = randomBytes(28).toString("base64url");
  const viewerPassword = randomBytes(28).toString("base64url");
  const inactivePassword = randomBytes(28).toString("base64url");
  const bootstrapPassword = randomBytes(32).toString("base64url");
  const adminSubject = randomUUID();
  const operatorSubject = randomUUID();
  const viewerSubject = randomUUID();
  const inactiveSubject = randomUUID();
  let keycloak: ChildProcess | undefined;
  let manager: ChildProcess | undefined;
  const evidence = {
    provider: "Keycloak 26.7.0 isolated local fixture",
    protocol: "Authorization Code with PKCE S256",
    issuer,
    roles: {} as Record<string, string>,
    untrustedAdminClaimDidNotEscalateViewer: false,
    savedUserAndServiceScopesUnchanged: false,
    prodGrantsRemainExplicitlyFalse: false,
    inactiveUserRejected: false,
    localLogoutRevokedManagerSession: false,
    localAdminFallbackDuringProviderOutage: false,
    upstreamLogout: "not implemented; Manager logout ends the local Manager session only",
  };

  try {
    const certDir = join(work, "certs");
    const importDir = join(work, "import");
    const dataDir = join(work, "manager-data");
    await Promise.all([
      mkdir(certDir, { mode: 0o700 }),
      mkdir(importDir, { mode: 0o700 }),
      mkdir(dataDir, { mode: 0o700 }),
    ]);
    const cert = join(certDir, "tls.crt");
    const key = join(certDir, "tls.key");
    const config = join(work, "manager.json");
    const secretFile = join(work, "secrets.json");
    const keycloakEnvFile = join(work, "keycloak.env");
    const realmFile = join(importDir, `${realmName}-realm.json`);
    await run("openssl", [
      "req",
      "-x509",
      "-newkey",
      "rsa:3072",
      "-nodes",
      "-keyout",
      key,
      "-out",
      cert,
      "-days",
      "2",
      "-subj",
      "/CN=127.0.0.1",
      "-addext",
      "subjectAltName=IP:127.0.0.1",
      "-addext",
      "basicConstraints=critical,CA:TRUE",
    ]);
    await chmod(key, 0o600);
    await chmod(cert, 0o600);

    const realm = {
      realm: realmName,
      enabled: true,
      sslRequired: "none",
      registrationAllowed: false,
      loginWithEmailAllowed: false,
      clients: [
        {
          clientId: clientID,
          enabled: true,
          protocol: "openid-connect",
          publicClient: false,
          secret: clientSecret,
          standardFlowEnabled: true,
          directAccessGrantsEnabled: false,
          serviceAccountsEnabled: false,
          redirectUris: [
            `${managerURL}/api/v1/auth/oidc/callback`,
          ],
          webOrigins: [managerURL],
          attributes: { "pkce.code.challenge.method": "S256" },
        },
      ],
      users: [
        {
          id: adminSubject,
          username: "kc-admin",
          enabled: true,
          firstName: "Keycloak",
          lastName: "Admin",
          email: "kc-admin@example.test",
          emailVerified: true,
          credentials: [{ type: "password", value: adminPassword, temporary: false }],
          realmRoles: ["admin"],
        },
        {
          id: operatorSubject,
          username: "kc-operator",
          enabled: true,
          firstName: "Keycloak",
          lastName: "Operator",
          email: "kc-operator@example.test",
          emailVerified: true,
          credentials: [{ type: "password", value: operatorPassword, temporary: false }],
        },
        {
          id: viewerSubject,
          username: "kc-viewer",
          enabled: true,
          firstName: "Keycloak",
          lastName: "Viewer",
          email: "kc-viewer@example.test",
          emailVerified: true,
          credentials: [{ type: "password", value: viewerPassword, temporary: false }],
          realmRoles: ["admin"],
        },
        {
          id: inactiveSubject,
          username: "kc-inactive",
          enabled: true,
          firstName: "Keycloak",
          lastName: "Inactive",
          email: "kc-inactive@example.test",
          emailVerified: true,
          credentials: [{ type: "password", value: inactivePassword, temporary: false }],
        },
      ],
      roles: { realm: [{ name: "admin" }] },
    };
    await writeFile(realmFile, JSON.stringify(realm), { mode: 0o600 });
    await writeFile(
      secretFile,
      JSON.stringify({ clientSecret, bootstrapPassword }),
      { mode: 0o600 },
    );
    await writeFile(keycloakEnvFile, `KC_BOOTSTRAP_ADMIN_USERNAME=admin\nKC_BOOTSTRAP_ADMIN_PASSWORD=${bootstrapPassword}\n`, { mode: 0o600 });
    await writeFile(
      config,
      JSON.stringify({
        listen_address: `127.0.0.1:${managerPort}`,
        data_dir: dataDir,
        public_url: "",
      }),
      { mode: 0o600 },
    );

    await run("docker", ["run", "--detach", "--name", dockerName,
      // GitHub-hosted runners commonly use UID 1001 while the Keycloak image
      // defaults to 1000. Match the runner UID so its private 0700/0600 bind
      // mounts stay private and readable. The pinned image's /opt/keycloak
      // data and config directories are group-0 writable, so retain that GID.
      "--user", `${process.getuid()}:0`,
      "--label", "flyway-manager.acceptance=keycloak",
      "--tmpfs", "/tmp:rw,nosuid,nodev,size=128m", "--tmpfs", "/opt/keycloak/data:rw,nosuid,nodev,size=512m",
      "--publish", `127.0.0.1:${keycloakPort}:8443`,
      "--volume", `${certDir}:/opt/keycloak/conf/tls:ro`,
      "--volume", `${importDir}:/opt/keycloak/data/import:ro`,
      "--env-file", keycloakEnvFile,
      "--env", `KC_HOSTNAME=https://127.0.0.1:${keycloakPort}`,
      "--env", "KC_HOSTNAME_STRICT=true",
      "--env", "KC_HTTP_ENABLED=false",
      keycloakImage,
      "start-dev", "--https-port=8443",
      "--https-certificate-file=/opt/keycloak/conf/tls/tls.crt",
      "--https-certificate-key-file=/opt/keycloak/conf/tls/tls.key",
      "--import-realm",
    ]);
    keycloak = spawn("docker", ["logs", "--follow", dockerName], { stdio: "ignore" });
    await waitFor(`${issuer}/.well-known/openid-configuration`, keycloak, cert, dockerName, [clientSecret, adminPassword, operatorPassword, viewerPassword, inactivePassword, bootstrapPassword]);

    manager = spawn(resolve(process.cwd(), "../bin/flyway-manager"), [], {
      env: {
        ...process.env,
        FLYWAY_MANAGER_CONFIG: config,
        FLYWAY_MANAGER_LISTEN_ADDRESS: `127.0.0.1:${managerPort}`,
        FLYWAY_MANAGER_DATA_DIR: dataDir,
      },
      stdio: "ignore",
    });
    await waitFor(`${managerURL}/api/v1/health`, manager);

    const status = await (await page.request.get(`${managerURL}/api/v1/auth/status`)).json();
    const setupToken = (await readFile(status.data.bootstrap_token_file, "utf8")).trim();
    const boot = await page.request.post(`${managerURL}/api/v1/auth/bootstrap`, {
      headers: { Origin: managerURL },
      data: {
        username: "local-admin",
        display_name: "Local Admin",
        password: bootstrapPassword,
        setup_token: setupToken,
      },
    });
    expect(boot.status()).toBe(201);
    const adminSession = (await boot.json()).data;
    const headers = { Origin: managerURL, "X-CSRF-Token": adminSession.csrf_token };
    const post = async (path: string, data: unknown, expected = 201) => {
      const response = await page.request.post(`${managerURL}/api/v1${path}`, { headers, data });
      expect(response.status(), `POST ${path} status`).toBe(expected);
      return response.status() === 204 ? null : (await response.json()).data;
    };
    const addExternal = async (username: string, subject: string, role: string, active = true) => {
      const user = await post("/access/users", {
        username,
        display_name: username,
        provider: "oidc",
        issuer,
        subject,
        role,
      });
      if (!active) {
        const updated = await page.request.put(`${managerURL}/api/v1/access/users/${user.id}`, {
          headers,
          data: { display_name: username, role, active: false },
        });
        expect(updated.status()).toBe(200);
      }
    };
    const primaryTarget = await post("/database-targets", {
      id: "oidc-prod-fixture",
      name: "OIDC PROD authorization fixture",
      engine: "oracle",
      environment: "PROD",
      host: "127.0.0.1",
      port: 1,
      service: "UNUSED",
      transport: "TCP",
      active: true,
    });
    await post("/database-targets", {
      id: "oidc-ungranted-fixture",
      name: "OIDC ungranted authorization fixture",
      engine: "oracle",
      environment: "DEV",
      host: "127.0.0.1",
      port: 2,
      service: "UNUSED",
      transport: "TCP",
      active: true,
    });
    await addExternal("oidc-admin", adminSubject, "Admin");
    await addExternal("oidc-operator", operatorSubject, "Operator");
    await addExternal("oidc-viewer", viewerSubject, "Viewer");
    await addExternal("oidc-inactive", inactiveSubject, "Viewer", false);
    const userList = (await (await page.request.get(`${managerURL}/api/v1/access/users`)).json()).data;
    const viewer = userList.find((user: { username: string }) => user.username === "oidc-viewer");
    const viewerGrant = [{ target_id: primaryTarget.id, schema_id: "", read: true, operate: false, prod: false }];
    const service = await post("/access/service-accounts", {
      name: "oidc-scope-fixture",
      owner_id: adminSession.user.id,
      purpose: "Phase 9 Keycloak acceptance scope preservation",
      active: true,
    });
    const serviceGrant = [{ target_id: primaryTarget.id, schema_id: "", read: true, operate: true, prod: false }];
    for (const [path, grants] of [
      [`/access/users/${viewer.id}/grants`, viewerGrant],
      [`/access/service-accounts/${service.id}/grants`, serviceGrant],
    ] as const) {
      const response = await page.request.put(`${managerURL}/api/v1${path}`, { headers, data: { grants } });
      expect(response.status()).toBe(200);
    }
    const serviceAccount = (await (await page.request.get(`${managerURL}/api/v1/access/service-accounts`)).json()).data.find(
      (account: { id: string }) => account.id === service.id,
    );
    const scopeSnapshot = {
      viewer: (await (await page.request.get(`${managerURL}/api/v1/access/users/${viewer.id}/grants`)).json()).data,
      service: (await (await page.request.get(`${managerURL}/api/v1/access/service-accounts/${service.id}/grants`)).json()).data,
      serviceAccount,
    };
    const clientSecretFromFile = JSON.parse(await readFile(secretFile, "utf8")).clientSecret as string;
    const settingsResponse = await page.request.put(`${managerURL}/api/v1/settings/authentication`, {
      headers,
      data: {
        local_enabled: true,
        ldap: { enabled: false, subject_attribute: "entryUUID", username_attribute: "uid", display_name_attribute: "displayName" },
        oidc: {
          enabled: true,
          issuer,
          client_id: clientID,
          client_secret: clientSecretFromFile,
          redirect_url: `${managerURL}/api/v1/auth/oidc/callback`,
          ca_file: cert,
        },
      },
    });
    expect(settingsResponse.status()).toBe(200);
    expect(JSON.stringify((await settingsResponse.json()).data)).not.toContain(clientSecretFromFile);
    expect((await settingsResponse.json()).data.local_enabled).toBe(true);

    // The authorization request itself proves the real provider received S256 and
    // state/nonce. URL values are checked in memory and never emitted into evidence.
    await page.goto(`${managerURL}/login`);
    const authNavigation = page.waitForURL((url) => url.origin === `https://127.0.0.1:${keycloakPort}`);
    await page.getByRole("button", { name: /Sign in with SSO/ }).click();
    await authNavigation;
    const authorization = new URL(page.url());
    expect(authorization.searchParams.get("response_type")).toBe("code");
    expect(authorization.searchParams.get("code_challenge_method")).toBe("S256");
    expect(authorization.searchParams.get("code_challenge")).toBeTruthy();
    expect(authorization.searchParams.get("state")).toBeTruthy();
    expect(authorization.searchParams.get("nonce")).toBeTruthy();
    await page.getByLabel("Username").fill("kc-admin");
    await page.getByRole("textbox", { name: "Password" }).fill(adminPassword);
    await page.getByRole("button", { name: "Sign In" }).click();
    await expect.poll(() => new URL(page.url()).pathname).toBe("/runs");
    let me = await (await page.request.get(`${managerURL}/api/v1/auth/me`)).json();
    expect(me.data.user.role).toBe("Admin");
    evidence.roles.admin = me.data.user.role;
    const shotDir = process.env.FWM_KEYCLOAK_EVIDENCE || join(tmpdir(), `fwm-keycloak-evidence-${process.pid}`);
    await mkdir(shotDir, { recursive: true, mode: 0o700 });
    await chmod(shotDir, 0o700);
    await expect(page.getByText("No migration runs match these filters.")).toBeVisible();
    await page.screenshot({ path: join(shotDir, "keycloak-admin-postlogin.png"), fullPage: true });

    // Local Manager logout revokes its cookie-backed session. Keycloak's own SSO
    // session remains active; re-login is explicit and still validates a fresh flow.
    const logout = await page.request.post(`${managerURL}/api/v1/auth/logout`, {
      headers: { Origin: managerURL, "X-CSRF-Token": me.data.csrf_token },
      data: {},
    });
    expect(logout.status()).toBe(200);
    expect((await page.request.get(`${managerURL}/api/v1/auth/me`)).status()).toBe(401);
    evidence.localLogoutRevokedManagerSession = true;

    const tryLogin = async (username: string, password: string, expectedRole?: string) => {
      // Each fixture user must authenticate independently; Keycloak can make the
      // previous account's username read-only during an SSO re-authentication.
      await page.context().clearCookies();
      const start = await page.request.get(`${managerURL}/api/v1/auth/oidc/start`, { maxRedirects: 0 });
      expect(start.status()).toBe(302);
      const authURL = new URL(start.headers().location!);
      authURL.searchParams.set("prompt", "login");
      await page.goto(authURL.toString());
      await expect.poll(() => new URL(page.url()).pathname).toContain("/protocol/openid-connect/auth");
      await page.getByLabel("Username").fill(username);
      await page.getByRole("textbox", { name: "Password" }).fill(password);
      await page.getByRole("button", { name: "Sign In" }).click();
      if (!expectedRole) return;
      await expect.poll(() => new URL(page.url()).pathname).toBe("/runs");
      me = await (await page.request.get(`${managerURL}/api/v1/auth/me`)).json();
      expect(me.data.user.role).toBe(expectedRole);
      if (username === "kc-viewer") expect(me.data.grants).toEqual(viewerGrant);
      evidence.roles[username] = me.data.user.role;
    };
    await tryLogin("kc-viewer", viewerPassword, "Viewer");
    const viewerAdminSettings = await page.request.get(`${managerURL}/api/v1/settings/authentication`);
    expect(viewerAdminSettings.status()).toBe(403);
    evidence.untrustedAdminClaimDidNotEscalateViewer = true;
    await page.request.post(`${managerURL}/api/v1/auth/logout`, {
      headers: { Origin: managerURL, "X-CSRF-Token": me.data.csrf_token }, data: {},
    });
    await tryLogin("kc-operator", operatorPassword, "Operator");
    await page.request.post(`${managerURL}/api/v1/auth/logout`, {
      headers: { Origin: managerURL, "X-CSRF-Token": me.data.csrf_token }, data: {},
    });
    await tryLogin("kc-inactive", inactivePassword);
    await expect(page.locator("body")).toContainText("INVALID_CREDENTIALS");
    evidence.inactiveUserRejected = (await page.request.get(`${managerURL}/api/v1/auth/me`)).status() === 401;
    expect(evidence.inactiveUserRejected).toBe(true);

    // Stop only this uniquely named disposable provider. Local Admin remains usable.
    await run("docker", ["stop", "--time", "5", dockerName]);
    await page.goto(`${managerURL}/login`);
    await page.getByLabel("Username").fill("local-admin");
    await page.getByRole("textbox", { name: "Password" }).fill(bootstrapPassword);
    await page.getByRole("button", { name: /Sign in$/i }).click();
    await expect.poll(() => new URL(page.url()).pathname).toBe("/runs");
    const finalScopes = {
      viewer: (await (await page.request.get(`${managerURL}/api/v1/access/users/${viewer.id}/grants`)).json()).data,
      service: (await (await page.request.get(`${managerURL}/api/v1/access/service-accounts/${service.id}/grants`)).json()).data,
      serviceAccount: (await (await page.request.get(`${managerURL}/api/v1/access/service-accounts`)).json()).data.find(
        (account: { id: string }) => account.id === service.id,
      ),
    };
    expect(finalScopes).toEqual(scopeSnapshot);
    expect(finalScopes.viewer[0].prod).toBe(false);
    expect(finalScopes.service[0].prod).toBe(false);
    evidence.savedUserAndServiceScopesUnchanged = true;
    evidence.prodGrantsRemainExplicitlyFalse = true;
    evidence.localAdminFallbackDuringProviderOutage = true;
    await writeFile(join(shotDir, "keycloak-acceptance.json"), JSON.stringify(evidence, null, 2), { mode: 0o600 });
  } finally {
    await stop(manager);
    await stop(keycloak);
    await run("docker", ["rm", "--force", dockerName]).catch(() => undefined);
    await rm(work, { recursive: true, force: true });
  }
});
