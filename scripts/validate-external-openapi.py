#!/usr/bin/env python3
"""Validate OpenAPI and actual responses from an isolated Manager subprocess."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

from jsonschema import Draft202012Validator
from openapi_spec_validator import validate
import yaml


def verify(evidence_path):
    spec = yaml.safe_load(Path("docs/openapi.yaml").read_text())
    validate(spec)
    evidence = json.loads(Path(evidence_path).read_text())
    count = 0
    for request in evidence["requests"]:
        path = request["path"].split("?", 1)[0]
        matching = [item for template, item in spec["paths"].items()
                    if re.fullmatch(re.sub(r"\{[^}]+\}", "[^/]+", template), path)]
        if len(matching) != 1:
            raise ValueError(f"Expected one OpenAPI path for {path}")
        operation = matching[0][request["method"].lower()]
        response = operation["responses"][str(request["status"])]["content"]["application/json"]["schema"]
        Draft202012Validator({"components": spec["components"], **response}).validate(request["response"])
        if "request" in request:
            body = operation["requestBody"]["content"]["application/json"]["schema"]
            Draft202012Validator({"components": spec["components"], **body}).validate(request["request"])
        count += 1
    print(f"OpenAPI 3.1 valid; {count} actual HTTP responses and submitted JSON bodies match their schemas.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("evidence", nargs="?", help="Existing sanitized evidence, or omit to run fresh HTTP acceptance")
    args = parser.parse_args()
    if args.evidence:
        verify(args.evidence)
        return
    with tempfile.TemporaryDirectory(prefix="fwm-openapi-") as directory:
        path = Path(directory) / "http-evidence.json"
        subprocess.run(["go", "test", "./cmd/flyway-manager", "-run",
                        "^TestExternalProjectAPIUsesNativeWorkerAndPersistsImmutableEvidence$", "-count=1"],
                       env={**os.environ, "FWM_EXTERNAL_EVIDENCE_FILE": str(path)}, check=True)
        verify(path)


if __name__ == "__main__":
    main()
