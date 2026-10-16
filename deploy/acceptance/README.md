# External browser acceptance

browser.mjs runs the React console in a real Chromium browser against an existing Flyway Manager origin. It does not start a local server, seed data, create registry records, or submit a migration. With SQL inputs, its last mutating action is artifact upload; preflight is read-only. The harness explicitly watches for and fails if a migration submission request occurs.

It covers first-run bootstrap when a private token file is supplied, local login, cookie attributes, runtime readiness and manifest integrity, target/schema/account selection, optional artifact upload and preflight, optional inspection of an already existing run and its audit attribution, refresh, logout, and protected-route redirect. It never reads browser traces, records cookies, prints credentials, or captures screenshots on failure.

## Prerequisites

- Build the Manager and install the checked-in web dependencies with make build and npm --prefix web ci.
- Use a prepared Manager with an administrator account and, for preflight, a registered target, schema, mapped execution account, and the required target/schema grant.
- Ensure the target points to the actual in-cluster Oracle Service DNS. Preflight checks Oracle but does not execute SQL.
- Use an HTTPS origin. HTTP is rejected unless it is loopback and allowInsecureLoopback is explicitly true; loopback mode does not require a Secure cookie.
- Run the script where Chromium is installed. Set PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH only when using a system Chromium binary.

The private config, credential JSON, and bootstrap token files must be regular files with mode 0600 or stricter on Unix. Store them outside the repository. The config contains file paths and expected non-secret acceptance values; credentials and the one-time token are read from separate private files.

## Private input files

Create files with a private directory and restrictive umask, then edit them with a trusted local editor or secret manager. Do not place real values in shell command arguments, source control, CI logs, or screenshots.

credentials.json:

~~~json
{
  "username": "<administrator username>",
  "password": "<administrator password>"
}
~~~

Example browser-config.json for a read-only V3 preflight. Include every SQL file required to reconstruct the expected stream, using the exact original V1/V2 bytes plus the proposed V3 file. Expected migration entries are asserted after the browser performs preflight.

~~~json
{
  "baseURL": "https://flyway-manager.apps.drgdevlab.com",
  "credentialsFile": "/absolute/private/path/credentials.json",
  "bootstrapTokenFile": "/absolute/private/path/bootstrap.token",
  "displayName": "Flyway Manager Administrator",
  "expectedFlywayVersion": "13.10.3",
  "targetId": "<existing target id>",
  "schemaId": "<existing schema id>",
  "accountId": "<existing execution account id>",
  "expectedTarget": {
    "host": "oracle-free.flyway-manager-oracle-lab.svc.cluster.local",
    "port": 1521,
    "service": "FREEPDB1",
    "transport": "TCP"
  },
  "expectedSchema": "FWM_LAB",
  "expectedExecutionUsername": "FWM_LAB",
  "expectedMigrations": [
    { "version": "1", "script": "V1__lab_probe.sql", "state": "Success" },
    { "version": "2", "script": "V2__manager_lab_probe.sql", "state": "Success" },
    { "version": "3", "script": "V3__manager_deployed_acceptance.sql", "state": "Pending" }
  ],
  "sqlFiles": [
    "/absolute/private/path/sql/V1__lab_probe.sql",
    "/absolute/private/path/sql/V2__manager_lab_probe.sql",
    "/absolute/private/path/sql/V3__manager_deployed_acceptance.sql"
  ],
  "timeoutMs": 30000
}
~~~

bootstrapTokenFile is optional after initial administrator setup. When /auth/status reports that bootstrap is required, the script requires this file and completes the one-time setup through the UI. It does not print the token or value-bearing errors.

sqlFiles is optional. Without it, the harness verifies login, cookie policy, runtime, refresh/navigation, and logout. If set, all of targetId, schemaId, and accountId are required. expectedTarget fields, expectedSchema, expectedExecutionUsername, and expectedMigrations are optional assertions. A successful preflight only verifies the configured connection/runtime and migration resolver; the script never opens the confirmation dialog or clicks any submit control.

runId is optional and must name an already existing run. When supplied, the script opens its history/detail pages, checks searchable logs and optional expectedRunStatus, expectedActorName, and expectedArtifactName, refreshes the details page, then checks the Admin audit table for its migration.submit event. This is read-only.

An optional screenshotPath captures the final logged-in page only after the run checks. The output directory must be private (0700 or stricter); the file is created private and an existing destination is refused. Screenshots are never captured after a failure. Avoid putting any manually entered secret into other page fields before enabling screenshots.

## Invocation

~~~
node deploy/acceptance/browser.mjs --config /absolute/private/path/browser-config.json
~~~

Or set FLYWAY_BROWSER_CONFIG to the absolute private config path and omit --config. The script writes one compact JSON result to stdout. Failure output includes only a stage and stable error code; it excludes exception text, page content, passwords, setup tokens, cookies, SQL contents, and API response bodies.

For an explicitly permitted local development run, set baseURL to a loopback origin and allowInsecureLoopback to true; set requireSecureCookie to false only for that HTTP loopback case. Never use this option for a homelab or public hostname.

## Limits

This browser check does not register targets/accounts, change grants, inspect raw audit/API secrets, submit V3, restart a pod, or prove persistence across a restart. Perform registry preparation through the normal authenticated APIs/UI, inspect V1/V2/canary independently before preflight, and keep any migration submission as a separate explicitly controlled acceptance step. Do not use this harness for repair, clean, reset, partial-DDL failure, or migration retry tests.
