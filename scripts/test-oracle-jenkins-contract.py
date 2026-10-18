#!/usr/bin/env python3
"""Offline contract check for the demo Jenkins poller against Manager outcomes.

Usage: PYTHONDONTWRITEBYTECODE=1 python3 scripts/test-oracle-jenkins-contract.py /path/to/demo-flyway-manager

This imports and invokes the demo repository's actual API helper. All Manager
responses are mocked and all report files are written under temporary dirs.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock


def load_demo_helper(repo):
    script = repo / "scripts" / "flyway_manager_api.py"
    if not script.is_file():
        raise ValueError(f"demo helper not found: {script}")
    sys.path.insert(0, str(script.parent))
    spec = importlib.util.spec_from_file_location("demo_flyway_manager_api", script)
    if spec is None or spec.loader is None:
        raise ValueError(f"could not load demo helper: {script}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def make_contract_test(api):
    class JenkinsPollContractTest(unittest.TestCase):
        def setUp(self):
            self.temp = tempfile.TemporaryDirectory()
            self.addCleanup(self.temp.cleanup)
            self.root = Path(self.temp.name)
            self.cfg_path = self.root / "manager-config.json"
            self.cfg_path.write_text(json.dumps({
                "environment": "develop",
                "manager_environment": "DEV",
                "manager_url": "https://manager.example",
                "target_id": "target1",
                "schema_id": "schema1",
                "execution_account_id": "exec1",
                "expected_schema_name": "APP",
                "approvers": ["dba@example.com"],
            }))
            self.env = mock.patch.dict(os.environ, {
                "FM_CONFIG_FILE": str(self.cfg_path),
                "FM_TOKEN": "fm_contract_test",
            })
            self.env.start()
            self.addCleanup(self.env.stop)
            self.request = {
                "target_id": "target1",
                "schema_id": "schema1",
                "execution_account_id": "exec1",
                "artifact_id": "artifact1",
                "context": {"external_run_id": "build-1", "commit_sha": "a" * 40},
            }

        def poll(self, status, migration, repair):
            state = self.root / f"reports-{status}-{migration}-{repair}"
            state.mkdir()
            run = dict(self.request, id="run1", status=status,
                       migration_outcome=migration, repair_outcome=repair)
            api.save(state / "request.json", self.request)
            api.save(state / "submission.json", {"data": run})
            client = mock.Mock()
            client.request.return_value = {"data": run}
            args = SimpleNamespace(command="poll", environment="develop",
                                   release=self.root, state=state,
                                   timeout=1, interval=1)
            with mock.patch.object(api, "Client", return_value=client), \
                    mock.patch.object(api.time, "sleep"):
                result = api.run_command(args)
            client.request.assert_called_once_with("GET", "/migration-runs/run1")
            self.assertEqual(api.load(state / "poll.json")["data"], run)
            return result

        def test_failed_migration_stays_pipeline_failure_even_after_repair(self):
            with self.assertRaisesRegex(ValueError, r"run FAILED; stop"):
                self.poll("FAILED", "FAILED", "SUCCEEDED")

        def test_review_and_unsafe_repair_fail_closed(self):
            for repair in ("FAILED", "UNSAFE"):
                with self.subTest(repair=repair):
                    with self.assertRaisesRegex(ValueError, r"run NEEDS_REVIEW; stop"):
                        self.poll("NEEDS_REVIEW", "FAILED", repair)

        def test_repairing_failed_migration_does_not_pass_early(self):
            with self.assertRaisesRegex(ValueError, r"run REPAIRING; stop"):
                self.poll("REPAIRING", "FAILED", "SUCCEEDED")

        def test_only_successful_migration_with_skipped_repair_passes(self):
            self.assertIsNone(self.poll("SUCCEEDED", "SUCCEEDED", "SKIPPED"))

        def test_success_status_with_other_outcomes_is_rejected(self):
            with self.assertRaisesRegex(ValueError, "unexpected success outcomes"):
                self.poll("SUCCEEDED", "FAILED", "SUCCEEDED")

    return JenkinsPollContractTest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("demo_repo", type=Path,
                        help="existing demo-flyway-manager repository checkout")
    args = parser.parse_args()
    repo = args.demo_repo.resolve(strict=True)
    api = load_demo_helper(repo)
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(make_contract_test(api))
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    return 0 if result.wasSuccessful() else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError) as exc:
        print(f"contract test setup failed: {exc}", file=sys.stderr)
        raise SystemExit(2)
