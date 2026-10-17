# Run Details UX baseline — 2026-10-17

Read-only deployed inspection of `https://flyway-manager.apps.drgdevlab.com/runs/DG5lFUs-8nPKq7_Ra8hqNB` completed before the redesign.

The live health endpoint returned `ok`, schema version 7, with authentication, migration execution, and registry capabilities enabled. The loaded application bundle fingerprint was `index-BsOYbtNb.js` (the local repository uses source files rather than this deployed asset name). The run is `SUCCEEDED`, Flyway 13.10.3, Oracle Free isolated lab / FREEPDB1 / schema `FWM_WZ260_A`, with `Repair SKIPPED`. The page currently combines the outcome, repair diagnostics, artifact manifest, run executions, and searchable logs in one long page. It includes the link “Flyway Info for this run”; V1 and V2 are listed separately with their recorded SHA-256 values.

Before screenshots: `before-run-details-1440.png`, `before-run-details-768.png`, and `before-run-details-390.png`.

Only `POST /api/v1/auth/login` was permitted for authentication. Every other non-GET/HEAD request was blocked by the browser route guard; no other mutation request occurred. Session state and credentials were kept out of the repository.

The actual retained v7 database was copied with SQLite's online backup API to private `/tmp/fwm-run-ux-acceptance/data/manager.db`; the sibling external master key was copied to the same private data directory. The source backup and key were not modified. The copied fixture contains the exact run and artifact. Run items are `V1__wizard_acceptance.sql` (SHA-256 `8f3f2e99750ed719f2b0e0fcef87a3dd27838603e54f90ed7c9291c04659b2c9`) and `V2__wizard_deployed_acceptance.sql` (SHA-256 `9ad9600fbb70b23d02b454617f53ec711bd87a6f02f9c2bd2945fa51bb81dea`). Both hashes were recomputed from retained blob bytes. The latest run-bound `info` execution record is native Flyway 13.10.3 JSON. The separate recorded Info capture table belongs to a different schema and is not used as this run's Info source.

The reusable local runner is `scripts/acceptance/run-details-retained.mjs`. It expects the private copied data directory and external key paths, starts the local Manager on `127.0.0.1:18119`, and blocks all browser non-GET/HEAD traffic. Run only after the frontend build is ready. It does not use the deployed service.
