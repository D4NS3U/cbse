# Copyright 2025-2026 Daniel Seufferth
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for the credential-free ``erlang_a_v1`` host CLI.

Covers deterministic byte-identical replay, the exact compact key-sorted
JSON payload with finite/bounded values, invalid-input stderr/non-zero
behavior with empty stdout, and the absence of credential/database/network
options on the CLI surface. The CLI imports the model (and thus simpy), so
the suite skips when simpy is not installed.
"""

import ast
import json
import math
import os
import subprocess
import sys
import unittest

try:
    import simpy  # noqa: F401
    HAS_SIMPY = True
except ImportError:
    HAS_SIMPY = False

SRC_DIR = os.path.dirname(os.path.abspath(__file__))
CLI_PATH = os.path.join(SRC_DIR, "cli.py")

EXPECTED_KEYS = {
    "arrival_rate",
    "service_rate",
    "run_duration",
    "effective_seed",
    "model_id",
    "completed_customers",
    "served_customers",
    "abandoned_customers",
    "total_customers",
    "mean_wait_time",
    "abandonment_probability",
    "throughput",
    "server_utilization",
}


def run_cli(*argv):
    return subprocess.run(
        [sys.executable, CLI_PATH, *argv],
        capture_output=True,
        text=True,
        timeout=120,
    )


def parse_payload(payload):
    def reject_constant(name):
        raise AssertionError(f"non-finite JSON constant in CLI output: {name}")

    return json.loads(payload, parse_constant=reject_constant)


@unittest.skipUnless(HAS_SIMPY, "simpy not available")
class TestCliSuccess(unittest.TestCase):
    ARGS = ("--arrival-rate", "4", "--service-rate", "5", "--run-duration", "50",
            "--seed", "2027")

    def test_single_line_compact_sorted_json_with_exact_fields(self):
        proc = run_cli(*self.ARGS)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertTrue(proc.stdout.endswith("\n"))
        self.assertEqual(proc.stdout.count("\n"), 1)
        payload = proc.stdout[:-1]
        record = parse_payload(payload)
        self.assertEqual(set(record), EXPECTED_KEYS)
        self.assertEqual(record["arrival_rate"], 4)
        self.assertEqual(record["service_rate"], 5)
        self.assertEqual(record["run_duration"], 50)
        self.assertEqual(record["effective_seed"], 2027)
        self.assertEqual(record["model_id"], "erlang_a_v1")
        self.assertEqual(
            record["served_customers"] + record["abandoned_customers"],
            record["total_customers"],
        )
        self.assertEqual(record["completed_customers"], record["served_customers"])
        for key in ("mean_wait_time", "abandonment_probability", "throughput",
                    "server_utilization"):
            self.assertTrue(math.isfinite(record[key]), msg=key)
        self.assertGreaterEqual(record["abandonment_probability"], 0.0)
        self.assertLessEqual(record["abandonment_probability"], 1.0)
        self.assertGreaterEqual(record["server_utilization"], 0.0)
        self.assertLessEqual(record["server_utilization"], 1.0)
        # Compact, key-sorted serialization: no whitespace, stable order.
        self.assertNotIn(" ", payload)
        self.assertEqual(
            payload,
            json.dumps(record, sort_keys=True, separators=(",", ":")),
        )

    def test_byte_identical_replay_for_identical_arguments(self):
        a = run_cli(*self.ARGS)
        b = run_cli(*self.ARGS)
        self.assertEqual(a.returncode, 0, a.stderr)
        self.assertEqual(b.returncode, 0, b.stderr)
        self.assertEqual(a.stdout.encode("utf-8"), b.stdout.encode("utf-8"))


@unittest.skipUnless(HAS_SIMPY, "simpy not available")
class TestCliInvalidInput(unittest.TestCase):
    def _assert_invalid(self, args):
        proc = run_cli(*args)
        self.assertNotEqual(proc.returncode, 0, "expected non-zero exit")
        self.assertEqual(proc.stdout, "", "stdout must be empty on invalid input")
        self.assertTrue(proc.stderr.strip(), "expected stderr diagnostics")

    def test_missing_required_flag(self):
        self._assert_invalid(
            ("--arrival-rate", "4", "--service-rate", "5", "--run-duration", "50")
        )

    def test_zero_rate(self):
        self._assert_invalid(
            ("--arrival-rate", "0", "--service-rate", "5", "--run-duration", "50",
             "--seed", "1")
        )

    def test_negative_rate(self):
        self._assert_invalid(
            ("--arrival-rate", "4", "--service-rate", "5", "--run-duration", "-1",
             "--seed", "1")
        )

    def test_non_integer_rate(self):
        self._assert_invalid(
            ("--arrival-rate", "4.5", "--service-rate", "5", "--run-duration", "50",
             "--seed", "1")
        )

    def test_non_integer_run_duration(self):
        self._assert_invalid(
            ("--arrival-rate", "4", "--service-rate", "5", "--run-duration", "abc",
             "--seed", "1")
        )

    def test_negative_seed(self):
        self._assert_invalid(
            ("--arrival-rate", "4", "--service-rate", "5", "--run-duration", "50",
             "--seed", "-1")
        )

    def test_non_integer_seed(self):
        self._assert_invalid(
            ("--arrival-rate", "4", "--service-rate", "5", "--run-duration", "50",
             "--seed", "2.5")
        )


@unittest.skipUnless(HAS_SIMPY, "simpy not available")
class TestCliSurface(unittest.TestCase):
    def test_help_lists_only_scenario_options(self):
        proc = run_cli("--help")
        self.assertEqual(proc.returncode, 0)
        help_text = proc.stdout
        for flag in ("--arrival-rate", "--service-rate", "--run-duration",
                     "--seed"):
            self.assertIn(flag, help_text)
        for forbidden in ("password", "credential", "nats", "postgres",
                          "database", "hostname", "registry", "sslmode"):
            self.assertNotIn(forbidden, help_text.lower())

    def test_source_declares_no_credential_or_database_options(self):
        with open(CLI_PATH, "r", encoding="utf-8") as fh:
            source = fh.read()
        tree = ast.parse(source)
        declared = set()
        for node in ast.walk(tree):
            if (
                isinstance(node, ast.Call)
                and isinstance(node.func, ast.Attribute)
                and node.func.attr == "add_argument"
                and node.args
                and isinstance(node.args[0], ast.Constant)
            ):
                declared.update(
                    s for s in str(node.args[0].value).split() if s.startswith("--")
                )
        self.assertEqual(
            declared,
            {"--arrival-rate", "--service-rate", "--run-duration", "--seed"},
        )


if __name__ == "__main__":
    unittest.main()
