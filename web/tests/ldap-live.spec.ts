import { test, expect, request as requests } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { randomBytes, createHash, createPublicKey, verify } from "node:crypto";
import { chmod, mkdtemp, mkdir, readFile, rm, stat, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { createServer as httpServer } from "node:http";
import { request as httpsRequest } from "node:https";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const image = "quay.io/keycloak/keycloak@sha256:0f198be292568439d700cdbfb893e69a6009bb43a94a06a945b1d3d506c76b13";
const enabled = process.env.FWM_LDAP_LIVE === "1";
async function port() {
  const server = createServer();
  await new Promise<void>((done, fail) => server.once("error", fail).listen(0, "127.0.0.1", done));
  const value = (server.address() as { port: number }).port;
  await new Promise<void>((done) => server.close(() => done()));
  return value;
}
async function run(command: string, args: string[], includeStderr = false) {
  return new Promise<string>((done, fail) => {
    const child = spawn(command, args, { stdio: ["ignore", "pipe", includeStderr ? "pipe" : "ignore"] });
    let output = "";
    child.stdout?.on("data", (part) => { if (output.length < 1 << 20) output += part; });
    if (includeStderr) child.stderr?.on("data", (part) => { if (output.length < 1 << 20) output += part; });
    child.once("error", () => fail(new Error(`${command} acceptance operation could not start`)));
    child.once("exit", (code) => code === 0 ? done(output) : fail(new Error(`${command} acceptance operation failed`)));
  });
}
function parse(body: string) {
  try { return JSON.parse(body); } catch { throw new Error("isolated identity response was not JSON"); }
}
async function privateText(path: string) {
  if ((await stat(path)).mode & 0o077) throw new Error("operator input must be mode 0600");
  return (await readFile(path, "utf8")).replace(/[\r\n]+$/, "");
}

test("real OpenLDAP direct bind, Keycloak federation, signed groups and deprovisioning", async ({ page }) => {
  test.skip(!enabled, "requires the explicitly authorized LDAP lab and protected operator files");
  const uid = process.getuid?.();
  if (uid === undefined) throw new Error("identity acceptance requires POSIX");
  const operator = process.env.FWM_LDAP_OPERATOR_DIRECTORY;
  if (!operator) throw new Error("FWM_LDAP_OPERATOR_DIRECTORY is required; never supply passwords in environment values");
  const endpoint = "ldaps://127.0.0.1:1636";
  const work = await mkdtemp(join(tmpdir(), "fwm-ldap-acceptance-"));
  await chmod(work, 0o700);
  const name = `fwm-ldap-keycloak-${randomBytes(6).toString("hex")}`;
  const retireUID = `phase9-retire-${randomBytes(6).toString("hex")}`;
  const retireFile = join(work, "retire.password");
  const retirePassword = randomBytes(32).toString("base64url");
  await writeFile(retireFile, retirePassword, { mode: 0o600 });
  const managerPort = await port(), kcPort = await port(), diagnosticPort = await port();
  const managerURL = `http://127.0.0.1:${managerPort}`;
  const kcURL = `https://127.0.0.1:${kcPort}`;
  const realm = "flyway-ldap-acceptance";
  const issuer = `${kcURL}/realms/${realm}`;
  const clientSecret = randomBytes(32).toString("base64url");
  const diagnosticSecret = randomBytes(32).toString("base64url");
  const localPassword = randomBytes(32).toString("base64url");
  const kcPassword = randomBytes(32).toString("base64url");
  const passwords = {
    "phase9-admin": await privateText(join(operator, "test-admin-password.trimmed")),
    "phase9-operator": await privateText(join(operator, "test-operator-password.trimmed")),
    "phase9-viewer": await privateText(join(operator, "test-viewer-password.trimmed")),
    [retireUID]: retirePassword,
  };
  let manager: ChildProcess | undefined;
  let tempCreated = false, tempDeleted = false;
  const evidence: Record<string, unknown> = { directory: "approved real OpenLDAP lab", keycloakImage: image,
    sharedManagerOrKeycloakChanged: false, sqlExecuted: false, kubernetesSecretValuesReadOrDecoded: false,
    upstreamLogout: "Manager session only; upstream SSO session is separate",
    federatedRelogin: "prompt=login; cached upstream SSO revocation is not verified" };
  const ldap = async (operation: string, username = retireUID, passwordFile = retireFile, extra: string[] = []) => {
    const result = parse(await run("go", ["run", "../scripts/identity/ldap-lab", "--operator-directory", operator,
      "--operation", operation, "--uid", username, "--password-file", passwordFile, ...extra]));
    expect(["PASS", "OBSERVED"]).toContain(result.status);
    return result;
  };
  try {
    // Mutations are restricted by the helper to a newly generated disposable UID.
    await ldap("create"); tempCreated = true;
    await ldap("readonly-denial");
    const inventory = await ldap("inspect");
    evidence.initialDirectory = inventory;
    for (const role of ["admin", "operator", "viewer"]) {
      const answer = await ldap("native", `phase9-${role}`, join(operator, `test-${role}-password.trimmed`));
      expect(answer.authenticated).toBe(true);
    }
    expect((await ldap("native", "phase9-viewer", join(operator, "test-viewer-password.trimmed"), ["--wrong-password"])).authenticated).toBe(false);
    expect((await ldap("native", "*)(uid=*)", retireFile)).authenticated).toBe(false);
    expect((await ldap("native", "phase9-viewer", join(operator, "test-viewer-password.trimmed"), ["--system-trust-only"])).authenticated).toBe(false);
    expect((await ldap("native", "phase9-viewer", join(operator, "test-viewer-password.trimmed"), ["--endpoint", "ldaps://localhost:1636"])).authenticated).toBe(false);
    await ldap("add-alias");
    expect((await ldap("native", "phase9-viewer", join(operator, "test-viewer-password.trimmed"))).authenticated).toBe(false);
    await ldap("remove-alias");
    expect((await ldap("native", "phase9-viewer", join(operator, "test-viewer-password.trimmed"))).authenticated).toBe(true);
    evidence.nativeLDAP = "PASS: three users, immutable subjects, wrong password, escaped input, CA/hostname rejection, ambiguous search and read-only write denial";

    const certs = join(work, "certs"), imports = join(work, "imports"), data = join(work, "manager-data");
    await Promise.all([mkdir(certs, { mode: 0o700 }), mkdir(imports, { mode: 0o700 }), mkdir(data, { mode: 0o700 })]);
    const cert = join(certs, "tls.crt"), key = join(certs, "tls.key");
    await run("openssl", ["req", "-x509", "-newkey", "rsa:3072", "-nodes", "-keyout", key, "-out", cert,
      "-days", "2", "-subj", "/CN=127.0.0.1", "-addext", "subjectAltName=IP:127.0.0.1", "-addext", "basicConstraints=critical,CA:TRUE"]);
    await Promise.all([chmod(key, 0o600), chmod(cert, 0o600)]);
    await writeFile(join(certs, "ldap-ca.crt"), await readFile(join(operator, "ca.crt")), { mode: 0o600 });
    const groupClaim = { name: "approved-fixture-groups", protocol: "openid-connect", protocolMapper: "oidc-group-membership-mapper", config: {
      "claim.name": "groups", "full.path": "true", "id.token.claim": "true", "access.token.claim": "true", "userinfo.token.claim": "true" } };
    const client = (id: string, secret: string, redirect: string) => ({ clientId: id, enabled: true, protocol: "openid-connect", publicClient: false, secret,
      standardFlowEnabled: true, implicitFlowEnabled: false, directAccessGrantsEnabled: false, serviceAccountsEnabled: false,
      redirectUris: [redirect], webOrigins: [managerURL], attributes: { "pkce.code.challenge.method": "S256" }, protocolMappers: [groupClaim] });
    await writeFile(join(imports, `${realm}-realm.json`), JSON.stringify({ realm, enabled: true, sslRequired: "none", registrationAllowed: false,
      loginWithEmailAllowed: false, clients: [client("flyway-manager", clientSecret, `${managerURL}/api/v1/auth/oidc/callback`),
        client("group-claim-proof", diagnosticSecret, `http://127.0.0.1:${diagnosticPort}/oidc-proof`)] }), { mode: 0o600 });
    const envFile = join(work, "keycloak.env");
    await writeFile(envFile, `KC_BOOTSTRAP_ADMIN_USERNAME=acceptance-admin\nKC_BOOTSTRAP_ADMIN_PASSWORD=${kcPassword}\n`, { mode: 0o600 });
    await run("docker", ["run", "--detach", "--name", name, "--user", `${uid}:0`, "--network", "host",
      "--tmpfs", "/tmp:rw,nosuid,nodev,size=128m", "--tmpfs", "/opt/keycloak/data:rw,nosuid,nodev,size=512m",
      "--volume", `${certs}:/opt/keycloak/conf/fixture:ro`, "--volume", `${imports}:/opt/keycloak/data/import:ro`,
      "--env-file", envFile, "--env", `KC_HOSTNAME=${kcURL}`, "--env", "KC_HOSTNAME_STRICT=true", "--env", "KC_HTTP_ENABLED=false",
      "--env", "KC_TRUSTSTORE_PATHS=/opt/keycloak/conf/fixture/ldap-ca.crt", image,
      "start-dev", `--https-port=${kcPort}`, "--http-host=127.0.0.1", "--https-certificate-file=/opt/keycloak/conf/fixture/tls.crt",
      "--https-certificate-key-file=/opt/keycloak/conf/fixture/tls.key", "--import-realm"]);
    const ca = await readFile(cert);
    async function https(path: string, method = "GET", body?: string, token?: string, form = false) {
      return new Promise<{ status: number; body: string; location?: string }>((done, fail) => {
        const call = httpsRequest(kcURL + path, { method, ca, rejectUnauthorized: true, headers: {
          ...(body ? { "Content-Type": form ? "application/x-www-form-urlencoded" : "application/json" } : {}),
          ...(token ? { Authorization: `Bearer ${token}` } : {}) } }, (response) => {
          let result = ""; response.on("data", (chunk) => { if (result.length < 1 << 20) result += chunk; });
          response.on("end", () => done({ status: response.statusCode ?? 0, body: result, location: response.headers.location }));
        });
        call.setTimeout(30_000, () => call.destroy());
        call.on("error", () => fail(new Error("isolated Keycloak HTTPS request failed")));
        if (body) call.write(body); call.end();
      });
    }
    const until = Date.now() + 180_000;
    while (true) {
      try { if ((await https(`/realms/${realm}/.well-known/openid-configuration`)).status === 200) break; } catch { /* bounded startup wait */ }
      if (Date.now() > until) throw new Error("isolated Keycloak startup timed out; inspect only sanitized diagnostics");
      await new Promise((done) => setTimeout(done, 500));
    }
    const tokenResponse = await https("/realms/master/protocol/openid-connect/token", "POST", new URLSearchParams({
      grant_type: "password", client_id: "admin-cli", username: "acceptance-admin", password: kcPassword }).toString(), undefined, true);
    expect(tokenResponse.status).toBe(200);
    let adminCredentials = parse(tokenResponse.body);
    let adminTokenExpires = Date.now() + Number(adminCredentials.expires_in) * 1000;
    const kc = async (path: string, method = "GET", body?: object, expected = 200) => {
      // Refresh before administrative calls; never retry an ambiguous mutation.
      if (Date.now() >= adminTokenExpires - 5_000) {
        const refreshed = await https("/realms/master/protocol/openid-connect/token", "POST", new URLSearchParams({
          grant_type: "refresh_token", client_id: "admin-cli", refresh_token: adminCredentials.refresh_token }).toString(), undefined, true);
        expect(refreshed.status).toBe(200);
        adminCredentials = parse(refreshed.body);
        adminTokenExpires = Date.now() + Number(adminCredentials.expires_in) * 1000;
      }
      const response = await https(`/admin/realms/${realm}` + path, method, body ? JSON.stringify(body) : undefined, adminCredentials.access_token);
      expect(response.status, `isolated Keycloak ${method} administrative contract`).toBe(expected);
      return response.body ? parse(response.body) : response.location?.split("/").pop();
    };
    console.log("Local Keycloak ready; configuring isolated read-only federation");
    const realmID = (await kc("")).id;
    const provider = await kc("/components", "POST", { name: "approved-isolated-openldap", providerId: "ldap",
      providerType: "org.keycloak.storage.UserStorageProvider", parentId: realmID, config: {
        enabled: ["true"], priority: ["0"], vendor: ["other"], editMode: ["READ_ONLY"], importEnabled: ["true"], syncRegistrations: ["false"],
        connectionUrl: [endpoint], usersDn: ["ou=people,dc=flyway,dc=lab"], bindDn: ["cn=readonly,dc=flyway,dc=lab"],
        bindCredential: [await privateText(join(operator, "readonly-bind-password.trimmed"))], authType: ["simple"],
        usernameLDAPAttribute: ["uid"], rdnLDAPAttribute: ["uid"], uuidLDAPAttribute: ["entryUUID"], userObjectClasses: ["inetOrgPerson"],
        searchScope: ["2"], cachePolicy: ["NO_CACHE"], useTruststoreSpi: ["always"], connectionPooling: ["false"], pagination: ["true"] } }, 201);
    const mapper = await kc("/components", "POST", { name: "approved-groups", providerId: "group-ldap-mapper",
      providerType: "org.keycloak.storage.ldap.mappers.LDAPStorageMapper", parentId: provider, config: {
        "groups.dn": ["ou=groups,dc=flyway,dc=lab"], "group.name.ldap.attribute": ["cn"], "group.object.classes": ["groupOfNames"],
        "preserve.group.inheritance": ["false"], "membership.ldap.attribute": ["member"], "membership.attribute.type": ["DN"],
        "membership.user.ldap.attribute": ["uid"], "mode": ["READ_ONLY"], "user.roles.retrieve.strategy": ["LOAD_GROUPS_BY_MEMBER_ATTRIBUTE"],
        "drop.non.existing.groups.during.sync": ["true"], "groups.path": ["/"] } }, 201);
    evidence.groupSync = await kc(`/user-storage/${provider}/mappers/${mapper}/sync?direction=fedToKeycloak`, "POST");
    evidence.userSync = await kc(`/user-storage/${provider}/sync?action=triggerFullSync`, "POST");
    console.log("Directory groups and users synchronized");
    const subjects: Record<string, string> = {};
    for (const username of Object.keys(passwords)) {
      const users = await kc(`/users?username=${encodeURIComponent(username)}&exact=true`);
      expect(users.length, `federated identity lookup for ${username}`).toBe(1); subjects[username] = users[0].id;
      const groups = await kc(`/users/${users[0].id}/groups`);
      expect(groups.map((group: { path: string }) => group.path)).toContain(username === retireUID ? "/flyway-viewer" : `/flyway-${username.replace("phase9-", "")}`);
    }
    evidence.federation = { mode: "READ_ONLY", cache: "NO_CACHE", immutableLDAPAttribute: "entryUUID", actualUserGroupMapping: true, subjects };

    console.log("Real LDAP user/group mappings verified");
    const managerConfig = join(work, "manager.json");
    await writeFile(managerConfig, JSON.stringify({ listen_address: `127.0.0.1:${managerPort}`, data_dir: data, public_url: "" }), { mode: 0o600 });
    const environment = { ...process.env, FLYWAY_MANAGER_CONFIG: managerConfig, FLYWAY_MANAGER_LISTEN_ADDRESS: `127.0.0.1:${managerPort}`,
      FLYWAY_MANAGER_DATA_DIR: data, FLYWAY_MANAGER_PUBLIC_URL: "", FLYWAY_MANAGER_MASTER_KEY_FILE: "" };
    manager = spawn(resolve("../bin/flyway-manager"), [], { env: environment, stdio: "ignore" });
    await expect.poll(async () => { try { return (await page.request.get(`${managerURL}/api/v1/health`)).status(); } catch { return 0; } }, { timeout: 20_000 }).toBe(200);
    const setup = (await (await page.request.get(`${managerURL}/api/v1/auth/status`)).json()).data;
    const boot = await page.request.post(`${managerURL}/api/v1/auth/bootstrap`, { headers: { Origin: managerURL }, data: {
      username: "local-admin", display_name: "Local Admin", password: localPassword, setup_token: (await readFile(setup.bootstrap_token_file, "utf8")).trim() } });
    expect(boot.status()).toBe(201);
    const session = (await boot.json()).data;
    const admin = await requests.newContext({ storageState: await page.context().storageState() });
    const headers = { Origin: managerURL, "X-CSRF-Token": session.csrf_token };
    const app = async (path: string, method = "GET", body?: object, expected = 200) => {
      const response = await admin.fetch(`${managerURL}/api/v1` + path, { method, headers, data: body });
      expect(response.status(), `isolated Manager ${method} contract`).toBe(expected);
      return (await response.json()).data;
    };
    const newUser = async (username: string, subject: string, role: string, providerType = "oidc") => app("/access/users", "POST", {
      username, display_name: username, role, provider: providerType, issuer: providerType === "oidc" ? issuer : endpoint, subject }, 201);
    await newUser("oidc-admin", subjects["phase9-admin"], "Admin");
    await newUser("oidc-operator", subjects["phase9-operator"], "Operator");
    const viewer = await newUser("oidc-viewer", subjects["phase9-viewer"], "Viewer");
    const retired = await newUser("oidc-retire", subjects[retireUID], "Viewer");
    await app(`/access/users/${retired.id}`, "PUT", { display_name: "Disposable", role: "Viewer", active: false });
    const nativeSubject = inventory.entries.find((entry: { uid: string }) => entry.uid === "phase9-viewer").uuid;
    const native = await newUser("ldap-viewer", nativeSubject, "Viewer", "ldap");
    expect(native.id === viewer.id).toBe(false);
    const target = await app("/database-targets", "POST", { id: "ldap-prod-fixture", name: "LDAP inert PROD", engine: "oracle", environment: "PROD",
      host: "127.0.0.1", port: 1, service: "UNUSED", transport: "TCP", active: true }, 201);
    const grants = [{ target_id: target.id, schema_id: "", read: true, operate: false, prod: false }];
    await app(`/access/users/${viewer.id}/grants`, "PUT", { grants });
    const service = await app("/access/service-accounts", "POST", { name: "ldap-retained-scope", owner_id: session.user.id, purpose: "Acceptance only", active: true }, 201);
    await app(`/access/service-accounts/${service.id}/grants`, "PUT", { grants });
    const initialScopes = { viewer: await app(`/access/users/${viewer.id}/grants`), service: await app(`/access/service-accounts/${service.id}/grants`) };
    await app("/settings/authentication", "PUT", { local_enabled: true, ldap: { enabled: true, url: endpoint, start_tls: false,
      base_dn: "ou=people,dc=flyway,dc=lab", bind_dn_template: "uid={username},ou=people,dc=flyway,dc=lab", username_attribute: "uid", subject_attribute: "entryUUID", display_name_attribute: "cn", ca_file: join(operator, "ca.crt") },
      oidc: { enabled: true, issuer, client_id: "flyway-manager", client_secret: clientSecret, redirect_url: `${managerURL}/api/v1/auth/oidc/callback`, ca_file: cert } });
    const nativeAPI = await requests.newContext();
    const nativeLogin = await nativeAPI.post(`${managerURL}/api/v1/auth/login`, { headers: { Origin: managerURL }, data: {
      provider: "ldap", username: "phase9-viewer", password: passwords["phase9-viewer"] } });
    expect(nativeLogin.status()).toBe(200); expect((await nativeLogin.json()).data.user.id).toBe(native.id);
    await nativeAPI.dispose();
    // Respect the production 10-per-minute source limit; no limiter override.
    await new Promise((done) => setTimeout(done, 61_000));
    const login = async (username: string, expectedRole?: string) => {
      await page.context().clearCookies();
      const start = await page.request.get(`${managerURL}/api/v1/auth/oidc/start`, { maxRedirects: 0 });
      expect(start.status()).toBe(302);
      const authorization = new URL(start.headers().location); authorization.searchParams.set("prompt", "login");
      expect(authorization.searchParams.get("code_challenge_method")).toBe("S256");
      try { await page.goto(authorization.toString()); } catch { throw new Error("isolated authorization navigation failed"); }
      await page.getByLabel("Username").fill(username);
      await page.getByRole("textbox", { name: "Password" }).fill(passwords[username]);
      await page.getByRole("button", { name: "Sign In" }).click();
      if (!expectedRole) return;
      await expect.poll(() => new URL(page.url()).pathname).toBe("/runs");
      const me = (await (await page.request.get(`${managerURL}/api/v1/auth/me`)).json()).data;
      expect(me.user.role).toBe(expectedRole); return me;
    };
    await login("phase9-admin", "Admin"); await login("phase9-operator", "Operator"); await login("phase9-viewer", "Viewer");
    expect((await page.request.get(`${managerURL}/api/v1/settings/authentication`)).status()).toBe(403);
    await login(retireUID);
    await expect(page.locator("body")).toContainText("INVALID_CREDENTIALS");
    evidence.explicitRolesAndInactiveDenial = true;
    await app(`/access/users/${retired.id}`, "PUT", { display_name: "Disposable", role: "Viewer", active: true });
    await new Promise((done) => setTimeout(done, 61_000));
    const active = await login(retireUID, "Viewer");
    expect(active.user.id).toBe(retired.id);

    // Separate loopback PKCE client: verify signed group claims without retaining JWTs.
    const verifier = randomBytes(32).toString("base64url"), nonce = randomBytes(24).toString("base64url"), state = randomBytes(24).toString("base64url");
    let callbackResolve: (value: URL) => void = () => undefined;
    const callback = new Promise<URL>((done) => { callbackResolve = done; });
    const receiver = httpServer((req, res) => { callbackResolve(new URL(req.url ?? "/", `http://127.0.0.1:${diagnosticPort}`)); res.end("Identity claim check completed."); });
    await new Promise<void>((done) => receiver.listen(diagnosticPort, "127.0.0.1", done));
    try {
      const authorization = new URL(`${issuer}/protocol/openid-connect/auth`);
      for (const [key, value] of Object.entries({ response_type: "code", client_id: "group-claim-proof", redirect_uri: `http://127.0.0.1:${diagnosticPort}/oidc-proof`,
        scope: "openid", state, nonce, code_challenge: createHash("sha256").update(verifier).digest("base64url"), code_challenge_method: "S256" })) authorization.searchParams.set(key, value);
      try { await page.goto(authorization.toString()); } catch { throw new Error("isolated claim-check navigation failed"); }
      const result = await Promise.race([callback, new Promise<never>((_, fail) => setTimeout(() => fail(new Error("claim-check callback timed out")), 15_000))]);
      expect(result.searchParams.get("state") === state).toBe(true);
      const tokens = await https(`/realms/${realm}/protocol/openid-connect/token`, "POST", new URLSearchParams({ grant_type: "authorization_code", client_id: "group-claim-proof", client_secret: diagnosticSecret,
        code: result.searchParams.get("code") ?? "", code_verifier: verifier, redirect_uri: `http://127.0.0.1:${diagnosticPort}/oidc-proof` }).toString(), undefined, true);
      expect(tokens.status).toBe(200);
      const jwt = parse(tokens.body).id_token as string; const parts = jwt.split(".");
      const header = parse(Buffer.from(parts[0], "base64url").toString());
      expect(header.alg).toBe("RS256");
      const jwks = parse((await https(`/realms/${realm}/protocol/openid-connect/certs`)).body);
      const publicKey = createPublicKey({ key: jwks.keys.find((candidate: { kid: string }) => candidate.kid === header.kid), format: "jwk" });
      expect(verify("RSA-SHA256", Buffer.from(parts[0] + "." + parts[1]), publicKey, Buffer.from(parts[2], "base64url"))).toBe(true);
      const claims = parse(Buffer.from(parts[1], "base64url").toString());
      expect(claims.iss).toBe(issuer); expect(claims.aud).toBe("group-claim-proof"); expect(claims.nonce === nonce).toBe(true);
      expect(claims.exp > Date.now() / 1000).toBe(true); expect(claims.sub).toBe(subjects[retireUID]); expect(claims.groups).toContain("/flyway-viewer");
      evidence.signedOIDCGroupClaimVerified = true;
    } finally { await new Promise<void>((done) => receiver.close(() => done())); }

    await ldap("remove-group");
    await kc(`/user-storage/${provider}/mappers/${mapper}/sync?direction=fedToKeycloak`, "POST");
    const groupsAfter = await kc(`/users/${subjects[retireUID]}/groups`);
    expect(groupsAfter.map((group: { path: string }) => group.path)).not.toContain("/flyway-viewer");
    // Explicit Manager mapping remains Viewer; group removal is not a grant revocation.
    await ldap("delete"); tempDeleted = true;
    expect((await ldap("native", retireUID, retireFile)).authenticated).toBe(false);
    expect((await page.request.get(`${managerURL}/api/v1/auth/me`)).status()).toBe(200);
    await app(`/access/users/${retired.id}`, "PUT", { display_name: "Disposable retired", role: "Viewer", active: false });
    expect((await page.request.get(`${managerURL}/api/v1/auth/me`)).status()).toBe(401);
    await login(retireUID);
    await expect(page.getByText("Invalid username or password.")).toBeVisible();
    evidence.deprovisioning = { groupRemovedAfterSync: true, directoryEntryDeleted: true, newDirectAndFederatedLoginDenied: true,
      existingManagerSessionRequiredExplicitDisable: true, explicitDisableRevokedSession: true };
    const finalScopes = { viewer: await app(`/access/users/${viewer.id}/grants`), service: await app(`/access/service-accounts/${service.id}/grants`) };
    expect(finalScopes).toEqual(initialScopes); expect(finalScopes.viewer[0].prod).toBe(false);
    await run("docker", ["stop", "--time", "5", name]);
    await page.context().clearCookies(); await page.goto(`${managerURL}/login`);
    await page.getByLabel("Username").fill("local-admin"); await page.getByRole("textbox", { name: "Password" }).fill(localPassword);
    await page.getByRole("button", { name: /^Sign in$/ }).click(); await expect.poll(() => new URL(page.url()).pathname).toBe("/runs");
    evidence.localAdminFallback = true; evidence.grantsAndProdPreserved = true; evidence.localRegistrySchema = 8;
    const evidenceDir = process.env.FWM_LDAP_EVIDENCE || join(tmpdir(), `fwm-ldap-evidence-${process.pid}`);
    await mkdir(evidenceDir, { recursive: true, mode: 0o700 }); await chmod(evidenceDir, 0o700);
    await page.screenshot({ path: join(evidenceDir, "local-admin-after-federation.png"), fullPage: true });
    await writeFile(join(evidenceDir, "ldap-federation.json"), JSON.stringify(evidence, null, 2), { mode: 0o600 });
    await admin.dispose();
  } catch (error) {
    const logs = await run("docker", ["logs", name], true).catch(() => "");
    const classes = [...new Set(logs.match(/(?:javax\.naming|javax\.net\.ssl|java\.security|java\.net|java\.io|sun\.security|org\.keycloak)[A-Za-z0-9_.]*Exception/g) || [])];
    console.log("Sanitized local provider exception classes:", classes);
    console.log("Sanitized transport flags:", Object.fromEntries(["PKIX", "Connection refused", "Connection reset", "Broken pipe", "No subject alternative", "No name matching", "timed out", "valid certification path", "Invalid credentials"].map((label) => [label, logs.includes(label)])));
    throw error;
  } finally {
    if (tempCreated && !tempDeleted) {
      // Probe read-only after a broken forwarding handle; never retry a mutation blindly.
      try {
        const until = Date.now() + 8_000;
        let inventory;
        while (true) {
          try { inventory = await ldap("inspect"); break; }
          catch { if (Date.now() > until) break; await new Promise((done) => setTimeout(done, 500)); }
        }
        if (!inventory) console.log("Disposable cleanup requires review for UID", retireUID);
        const dn = `uid=${retireUID},ou=people,dc=flyway,dc=lab`;
        if (inventory?.entries.some((entry: { members: string[] }) => entry.members.includes(dn))) await ldap("remove-group");
        if (inventory?.entries.some((entry: { dn: string }) => entry.dn === dn)) await ldap("delete");
      } catch { console.log("Disposable cleanup requires review for UID", retireUID); }
    }
    if (manager && manager.exitCode === null) {
      manager.kill("SIGTERM");
      await Promise.race([new Promise((done) => manager!.once("exit", done)), new Promise((done) => setTimeout(done, 5_000))]);
      if (manager.exitCode === null) manager.kill("SIGKILL");
    }
    await run("docker", ["rm", "--force", name]).catch(() => undefined);
    await rm(work, { recursive: true, force: true });
  }
});
