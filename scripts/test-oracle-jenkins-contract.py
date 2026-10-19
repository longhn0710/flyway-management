#!/usr/bin/env python3
"""Offline poll contract tests against the current Git-to-Oracle demo helper.

Usage: PYTHONDONTWRITEBYTECODE=1 python3 scripts/test-oracle-jenkins-contract.py /path/to/demo-flyway-manager

The real demo helper is imported from its workspace checkout. Manager GETs,
native log streaming, and evidence collection are mocked; all state is temporary.
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
            self.state = Path(self.temp.name) / "state"
            self.state.mkdir()
            self.cfg = {
                "manager_url": "https://manager.example",
                "project_id": "project1",
                "deployment_target_id": "deployment1",
            }
            self.request = {
                "artifact_id": "artifact1",
                "deployment_target_revision": 7,
                "external_source": {"execution_url": "https://jenkins.example/job/7"},
            }
            self.submitted = dict(self.request, id="run1", project_id="project1",
                                  deployment_target_id="deployment1")
            api.save(self.state / "request.json", self.request)
            api.save(self.state / "submission.json", {"data": self.submitted})
            self.client = mock.Mock()
            self.client.request.side_effect = lambda method, route, **kwargs: {
                "data": self.run,
            }
            self.args = SimpleNamespace(
                command="poll", environment="develop", release=self.state,
                state=self.state, timeout=1, interval=0.2,
            )

        def run_poll(self, run):
            self.run = dict(self.submitted, status="SUCCEEDED", completed=True,
                            migration_outcome="SUCCEEDED", repair_outcome="SKIPPED")
            self.run.update(run)
            env = mock.patch.dict(os.environ, {"FM_MANAGER_ACCESS_AUTHORIZED": "true"})
            fake_time = [100.0]

            def monotonic():
                fake_time[0] += 0.3
                return fake_time[0]

            with env, \
                    mock.patch.object(api, "config", return_value=self.cfg), \
                    mock.patch.object(api, "Client", return_value=self.client), \
                    mock.patch.object(api, "stream_logs") as logs, \
                    mock.patch.object(api, "collect") as collect, \
                    mock.patch.object(api.time, "sleep") as sleep, \
                    mock.patch.object(api.time, "monotonic", side_effect=monotonic):
                try:
                    result = api.run_command(self.args)
                except Exception as exc:  # caller asserts exact terminal/timeout result
                    result = exc
            expected_route = "/projects/project1/migration-runs/run1"
            self.assertTrue(self.client.request.called, f"poller stopped before GET: {result!r}")
            self.assertTrue(all(call.args[0] == "GET" and call.args[1] == expected_route
                                for call in self.client.request.call_args_list))
            self.assertEqual(api.load(self.state / "poll.json")["data"], self.run)
            if self.run.get("completed") is True:
                collect.assert_called_once()
                self.assertEqual(collect.call_args.args[0], self.client)
                logs.assert_called()
            else:
                collect.assert_not_called()
                if self.run["status"] in ("QUEUED", "PREFLIGHT", "RUNNING", "REPAIRING"):
                    self.assertTrue(sleep.called, "active run should be polled again until the bound")
            return result

        def test_failed_migration_remains_failure_after_successful_repair(self):
            result = self.run_poll({"status": "FAILED", "completed": True,
                                    "migration_outcome": "FAILED", "repair_outcome": "SUCCEEDED"})
            self.assertIsInstance(result, ValueError)
            self.assertIn("terminal migration failed", str(result))

        def test_failed_unsafe_and_unknown_repair_outcomes_fail(self):
            for status, repair in (("NEEDS_REVIEW", "FAILED"),
                                   ("NEEDS_REVIEW", "UNSAFE"),
                                   ("NEEDS_REVIEW", "UNKNOWN"),
                                   ("BLOCKED", "UNKNOWN")):
                with self.subTest(status=status, repair=repair):
                    result = self.run_poll({"status": status, "completed": True,
                                            "migration_outcome": "FAILED",
                                            "repair_outcome": repair})
                    self.assertIsInstance(result, ValueError)
                    self.assertIn("terminal migration failed", str(result))

        def test_repairing_waits_and_bounded_timeout_preserves_same_run(self):
            result = self.run_poll({"status": "REPAIRING", "completed": False,
                                    "migration_outcome": "FAILED", "repair_outcome": "PENDING"})
            self.assertIsInstance(result, ValueError)
            self.assertIn("bounded poll timeout", str(result))
            self.assertGreaterEqual(self.client.request.call_count, 1)

        def test_only_terminal_success_returns_success(self):
            result = self.run_poll({"status": "SUCCEEDED", "completed": True,
                                    "migration_outcome": "SUCCEEDED", "repair_outcome": "SKIPPED"})
            self.assertIsNone(result)

        def test_success_status_with_failed_migration_still_fails(self):
            result = self.run_poll({"status": "SUCCEEDED", "completed": True,
                                    "migration_outcome": "FAILED", "repair_outcome": "SUCCEEDED"})
            self.assertIsInstance(result, ValueError)

        def test_unknown_run_status_fails_closed(self):
            result = self.run_poll({"status": "UNRECOGNIZED", "completed": False,
                                    "migration_outcome": "UNKNOWN", "repair_outcome": "UNKNOWN"})
            self.assertIsInstance(result, ValueError)
            self.assertIn("unknown run status", str(result))

    return JenkinsPollContractTest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("demo_repo", type=Path,
                        help="checkout of the demo repository")
    args = parser.parse_args()
    api = load_demo_helper(args.demo_repo.resolve(strict=True))
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(make_contract_test(api))
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    return 0 if result.wasSuccessful() else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError) as exc:
        print(f"contract test setup failed: {exc}", file=sys.stderr)
        raise SystemExit(2)
