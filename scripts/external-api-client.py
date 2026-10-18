#!/usr/bin/env python3
"""Small generic client for the Flyway Manager project API v2."""

import argparse
import hashlib
import json
import os
import sys
import tempfile
import time
from pathlib import Path
from urllib.parse import quote

import requests


def fail(response):
    try:
        detail = response.json()
    except ValueError:
        detail = response.text
    raise RuntimeError(f"HTTP {response.status_code}: {detail}")


def request(session, method, url, **kwargs):
    response = session.request(method, url, timeout=(10, 60), **kwargs)
    if not response.ok:
        fail(response)
    return response


def data(response):
    return response.json()["data"]


def collection(session, url):
    result = []
    params = {"limit": 200}
    while True:
        page = request(session, "GET", url, params=params).json()
        result.extend(page.get("data", []))
        cursor = page.get("page", {}).get("next_cursor")
        if not cursor:
            return result
        params = {"limit": 200, "cursor": cursor}


def unique_named(items, name, what):
    matches = [item for item in items if item.get("name") == name]
    if len(matches) != 1:
        raise RuntimeError(f"Expected exactly one {what} named {name!r}; found {len(matches)}")
    return matches[0]


def save_state(path, state):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(state, stream, sort_keys=True, separators=(",", ":"))
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def build_run_url(project_url, run_id):
    return project_url + "/migration-runs/" + quote(str(run_id), safe="")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", required=True, help="Manager origin, such as https://manager.example")
    parser.add_argument("--token", default=os.environ.get("FM_TOKEN"), help="Service token (defaults to FM_TOKEN)")
    parser.add_argument("--project", required=True, help="Unique project name")
    parser.add_argument("--deployment-target", required=True, help="Unique deployment target name")
    parser.add_argument("--zip", required=True, type=Path, help="ZIP artifact")
    parser.add_argument("--key", required=True, help="Stable release key, reused on retries")
    parser.add_argument("--external-source", default="{}", help="JSON object describing CI provenance")
    parser.add_argument("--state-file", type=Path, help="Private retry state file (defaults to a key-derived file in the current directory)")
    parser.add_argument("--poll-seconds", type=float, default=2)
    parser.add_argument("--max-polls", type=int, default=900)
    args = parser.parse_args()
    if not args.token:
        parser.error("--token or FM_TOKEN is required")
    if len(args.key) < 1 or len(args.key) > 200:
        parser.error("--key must contain 1 to 200 characters")
    source = json.loads(args.external_source)
    if not isinstance(source, dict):
        parser.error("--external-source must be a JSON object")

    base = args.url.rstrip("/") + "/api/v2"
    session = requests.Session()
    session.headers.update({"Authorization": "Bearer " + args.token, "Accept": "application/json"})
    archive = args.zip.read_bytes()
    if not archive.startswith(b"PK\x03\x04"):
        raise RuntimeError("--zip must be a ZIP archive")
    digest = hashlib.sha256(archive).hexdigest()
    upload_key = args.key + ":upload"
    if len(upload_key) > 200:
        raise RuntimeError("The release key is too long to derive the upload idempotency key")

    state_path = args.state_file or Path.cwd() / (".fm-external-" + hashlib.sha256(args.key.encode()).hexdigest()[:20] + ".json")
    identity = {
        "url": args.url.rstrip("/"),
        "project": args.project,
        "deployment_target": args.deployment_target,
        "key": args.key,
        "input_sha256": digest,
        "external_source": source,
    }
    try:
        state = json.loads(state_path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        state = dict(identity)
    except (OSError, ValueError) as exc:
        raise RuntimeError(f"Cannot read retry state {state_path}: {exc}") from exc
    for field, expected in identity.items():
        if state.get(field) != expected:
            raise RuntimeError(f"Retry state {state_path} belongs to a different {field}; use matching release inputs or a new --key")

    if state.get("run_id"):
        project_id = quote(str(state["project_id"]), safe="")
        project_url = base + "/projects/" + project_id
        run_id = str(state["run_id"])
        run = None
        response = None
    else:
        if not state.get("project_id"):
            project = unique_named(collection(session, base + "/projects"), args.project, "project")
            state["project_id"] = str(project["id"])
            save_state(state_path, state)
        project_id = quote(str(state["project_id"]), safe="")
        project_url = base + "/projects/" + project_id
        if not state.get("deployment_target_id"):
            target = unique_named(collection(session, project_url + "/deployment-targets"), args.deployment_target, "deployment target")
            if not target.get("active", False):
                raise RuntimeError("The selected deployment target is inactive")
            revision = target.get("revision")
            if not isinstance(revision, int) or revision < 1:
                raise RuntimeError("Deployment target response has no positive revision")
            state["deployment_target_id"] = str(target["id"])
            state["deployment_target_revision"] = revision
            save_state(state_path, state)
        target_id = quote(str(state["deployment_target_id"]), safe="")
        target_url = project_url + "/deployment-targets/" + target_id
        revision = state["deployment_target_revision"]

        if not state.get("artifact_id"):
            upload = request(
                session,
                "POST",
                target_url + "/artifacts",
                headers={"Idempotency-Key": upload_key},
                files={"files": (args.zip.name, archive, "application/zip")},
                data={
                    "deployment_target_revision": str(revision),
                    "input_sha256": digest,
                    "external_source": json.dumps(source, separators=(",", ":")),
                },
            )
            artifact = data(upload)
            artifact_id = artifact.get("id", artifact.get("artifact_id"))
            if not artifact_id:
                raise RuntimeError("Artifact response did not include an id")
            state["artifact_id"] = artifact_id
            save_state(state_path, state)

        if not state.get("run_input"):
            run_input = {
                "artifact_id": state["artifact_id"],
                "deployment_target_revision": revision,
                "external_source": source,
            }
            assessment = data(request(session, "POST", target_url + "/preflights", json=run_input))
            blockers = assessment.get("blockers") or []
            if assessment.get("ready") is False or blockers:
                raise RuntimeError("Preflight is blocked: " + json.dumps(blockers or assessment, sort_keys=True))
            preflight_id = assessment.get("id", assessment.get("preflight_id"))
            if not preflight_id:
                raise RuntimeError("Preflight response did not include an id")
            run_input["preflight_id"] = preflight_id
            state["preflight_id"] = preflight_id
            state["run_input"] = run_input
            save_state(state_path, state)

        try:
            response = request(
                session,
                "POST",
                target_url + "/migration-runs",
                headers={"Idempotency-Key": args.key},
                json=state["run_input"],
            )
        except RuntimeError as exc:
            if "HTTP 409" in str(exc):
                raise RuntimeError(f"The saved preflight was rejected; inspect {state_path} and start a new release with a new --key") from exc
            raise
        run = data(response)
        run_id = run.get("id", run.get("run_id"))
        if not run_id:
            raise RuntimeError("Accepted run response did not include an id")
        state["run_id"] = str(run_id)
        state["location"] = response.headers.get("Location")
        save_state(state_path, state)

    run_url = build_run_url(project_url, run_id)
    retry_after = response.headers.get("Retry-After") if response is not None else None
    delay = max(args.poll_seconds, float(retry_after or 0))
    for _ in range(args.max_polls):
        if not (run and run.get("completed") is True):
            time.sleep(delay)
        run = data(request(session, "GET", run_url))
        if run.get("completed") is True:
            break
    else:
        raise TimeoutError(f"Run {run_id} is still active; resume polling this run, do not resubmit under a new key")

    logs = collection(session, run_url + "/logs")
    record_response = request(session, "GET", run_url + "/record", headers={"Accept": "application/json"})
    print(json.dumps({
        "run": run,
        "logs": logs,
        "record_etag": record_response.headers.get("ETag"),
        "record": record_response.json(),
    }, indent=2, sort_keys=True))
    if run.get("status") != "SUCCEEDED":
        return 1
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:  # Keep CI failures concise and avoid dumping request headers.
        print(f"external-api-client: {exc}", file=sys.stderr)
        sys.exit(2)
