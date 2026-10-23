# Safe Delete Project — local acceptance, 2026-10-18

The Phase 9 Delete Project slice is implemented on `feat/safe-delete-project`, based on accepted source `f507dcaa543f10cddf2805f06b8169d83fd980d7`. Local acceptance and implementation-head [CI 39083672453](https://github.com/longhn0710/flyway-management/actions/runs/39083672453) passed in [PR #6](https://github.com/longhn0710/flyway-management/pull/6). Current-head PR CI remains an independent release gate; this report does not authorize merge or deployment. The existing Manager and its data were not changed.

## Contract and preservation

An Admin opens a project's **Delete project** dialog and types its exact current name. The API is `DELETE /api/v1/projects/{id}` with JSON `{"confirmation_name":"Exact project name"}` and the existing session, Origin and CSRF requirements. Success is HTTP 200 with `{"data":{"deleted":true}}`. Missing or previously deleted IDs return 404; invalid confirmation returns 400; any remaining membership returns 409. Operator, Viewer and service actors are denied.

All membership rows block deletion, including instance-wide, schema, inactive-resource and archived-project membership. Delete never detaches a member. Archived projects retain the existing membership lock: unarchive first, remove membership explicitly, then delete. Empty active and archived projects can be deleted. A rename invalidates old name confirmation. A concurrent membership update either wins the write lock and blocks deletion, or sees a missing project afterward.

The existing immediate SQLite write transaction revalidates browser session/role, reads the name and membership count, removes the empty project row and appends `project.delete` audit evidence atomically. Audit failure rolls back deletion. Schema inspection found only the `project_members` project foreign key; the checked zero count makes its cascade empty. Runs and SQL/history are independently target/schema-scoped. No SQLite migration, execution worker, CLI, recovery policy or external database command changed.

Prior audit rows retain their original project-ID targets. The deleted project's name/description cannot subsequently be resolved through project metadata, and its project/history endpoints return 404. Existing direct run/history/SQL/log routes and other projects' authorized history remain available. There is no retained historical project-to-run association in the current model, and this change introduces none. A future model with historical project references must revisit physical deletion.

## Verified checks

| Check | Result |
| --- | --- |
| `make check` | PASS — production frontend/server build, Go vet/unit suite, ESLint and 26 frontend tests |
| `make test-race` | PASS — full existing Go package race suite, including simultaneous project delete/membership writes |
| `make test-e2e` | PASS — 44 browser tests; one existing opt-in live PostgreSQL test skipped |
| `make security` | PASS — npm audit found zero vulnerabilities; govulncheck found zero vulnerabilities affecting called code (one uncalled module advisory was reported) |
| OpenAPI | Parses; delete declares session-only authorization, strict confirmation body and expected 200/400/401/403/404/409 results |
| Documentation | Local links and Secret-instruction Bash syntax checked; diff whitespace check passed |
| Independent read-only review | No correctness, authorization, race, cascade or foreign-key defect found; name-resolution limitation documented above |

The new core/API tests cover exact-name matching, revoked Admin authority, audit rollback, active/inactive and archived membership, stale rename, repeated deletion, post-delete update/history lookup, CSRF, malformed/unknown fields, Viewer/Operator/service denial and audit attribution. Twenty simultaneous membership/delete races enforce the allowed outcomes and retain foreign-key integrity.

The preservation test first completes a simulated-runner migration using the existing execution fixture, then removes membership explicitly. It snapshots **every application table**, excluding only the deleted project row and the expected new deletion audit, and proves all other rows—including prior audits, credentials, artifacts, blobs, run evidence, captures, logs and shared-project membership—are identical afterward. Integrity and foreign-key checks pass. This proves local registry preservation; no live Oracle/PostgreSQL operation was needed or performed for deletion acceptance.

Real local browser/API journeys passed at 1440×1000 and 390×844, with no page JavaScript errors: confirmation/wrong case, cancellation, membership added after the dialog opened, successful redirect/list removal, deleted deep links, membership denial, archived empty success, archived membership denial, retained target, audit lookup and Viewer controls. Both screenshots were visually inspected:

- [Desktop confirmation](evidence/safe-delete-project-20261018/confirmation-desktop.png)
- [Mobile confirmation](evidence/safe-delete-project-20261018/confirmation-mobile.png)

Earlier attempts are not counted as clean passes: a test service-token fixture initially omitted required expiry/grants; it was corrected. A manual browser invocation from the repository root resolved the fixture binary from the wrong directory; the normal npm/web working directory and a temporary focused configuration passed. The final complete Makefile invocation above exited zero, using the normal committed Playwright configuration. No application admission control was weakened to fix a test.

## Delivery boundary

Review the focused PR and its current-head CI before any merge. Follow the existing immutable-image/GitOps process with fresh supported backup, separately protected key and isolated restore before a separately authorized rollout. No live project, membership, shared Keycloak realm, LDAP resource, collector, Secret or NetworkPolicy was modified. No merge/deployment acceptance is claimed. The [Phase 9 plan](phase9-enterprise-integrations.md) records the remaining identity and Elastic workstreams.
