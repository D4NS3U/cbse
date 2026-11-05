#!/usr/bin/env python3
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

"""Offline self-tests for the P5 acceptance Python tools (P5-R5, P5-R6).

Covers, for ``assert-results.py``:

- the exact 15-field result contract, cross-checked against the canonical
  ``result.RESULT_FIELDS`` (drift guard);
- valid rows passing with the informational verdict summary;
- every P5-R5 violation class with synthetic rows (missing/extra fields,
  wrong types, boolean rejection, negative counts, wrong model id,
  non-finite and negative floats, out-of-bounds probability/utilization,
  alias and accounting mismatches, derived-metric mismatches, design-value
  mismatches, duplicate effective seeds, and the computed-replication count
  check, including the zero-safe empty-measured-set case);
- the pure-stdlib Student-t quantile against known values and the PPS-style
  half-width against hand-computed vectors;
- the CLI exit codes (0 pass, 1 violation, 2 usage/context error) via
  subprocess.

Covers, for ``replay-seeds.py``:

- synthetic rows generated from the canonical model itself, proving a
  successful replay (exact counts and bit-exact floats);
- deterministic re-replay of the same recorded seed;
- deliberately perturbed counts and float metrics, detected as mismatches;
- malformed rows, detected before any model call;
- distinct-seed rows replaying independently;
- the CLI exit codes (0 all rows match, 1 mismatch) and the report shape,
  via subprocess.

The replay tests import the canonical model and require SimPy 4.1.2; they
skip cleanly when simpy is not installed, mirroring the repository
conformance convention (``pads-paper/simulation-model/src``). Run with
``python3 -m unittest`` from this directory; the system interpreter is used
for the subprocess tests so no third-party dependency is needed to collect.
"""

import importlib.util
import json
import math
import os
import random
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
PADS_PAPER_ROOT = os.path.normpath(os.path.join(HERE, os.pardir))
CANONICAL_SRC = os.path.normpath(os.path.join(PADS_PAPER_ROOT, "simulation-model", "src"))
PYTHON = sys.executable

ASSERT_RESULTS_PY = os.path.join(HERE, "assert-results.py")
REPLAY_SEEDS_PY = os.path.join(HERE, "replay-seeds.py")


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


assert_results = load_module("pads_acceptance_assert_results", ASSERT_RESULTS_PY)
replay_seeds = load_module("pads_acceptance_replay_seeds", REPLAY_SEEDS_PY)

try:
    import simpy  # noqa: F401

    HAS_SIMPY = True
except ImportError:
    HAS_SIMPY = False

#: The P5 ruled design values (used to build synthetic rows and contexts).
DESIGN = {
    "parameterset_id": 5,
    "arrival_rate": 4,
    "service_rate": 4,
    "run_duration": 500,
    "seed_policy": 2027,
}


def make_row(seed, served, abandoned, mean_wait, utilization, **overrides):
    """Build a synthetic well-formed row (no SimPy required)."""
    total = served + abandoned
    row = {
        "parameterset_id": DESIGN["parameterset_id"],
        "arrival_rate": DESIGN["arrival_rate"],
        "service_rate": DESIGN["service_rate"],
        "run_duration": DESIGN["run_duration"],
        "seed_policy": DESIGN["seed_policy"],
        "effective_seed": seed,
        "model_id": "erlang_a_v1",
        "completed_customers": served,
        "served_customers": served,
        "abandoned_customers": abandoned,
        "total_customers": total,
        "mean_wait_time": mean_wait,
        "abandonment_probability": abandoned / total if total else 0.0,
        "throughput": served / DESIGN["run_duration"],
        "server_utilization": utilization,
    }
    row.update(overrides)
    return row


def make_context(computed_reps, confidence_metric=0.05):
    context = dict(DESIGN)
    context["number_of_reps"] = 5
    context["confidence_metric"] = confidence_metric
    context["number_of_computed_reps"] = computed_reps
    return context


def two_valid_rows():
    return [make_row(11, 40, 6, 0.91, 0.62), make_row(12, 41, 5, 0.88, 0.64)]


class TestResultContract(unittest.TestCase):
    def test_exactly_fifteen_fields_in_canonical_order(self):
        self.assertEqual(len(assert_results.RESULT_FIELDS), 15)
        canonical = load_module(
            "pads_paper_canonical_result_contract", os.path.join(CANONICAL_SRC, "result.py")
        )
        self.assertEqual(
            tuple(assert_results.RESULT_FIELDS), tuple(canonical.RESULT_FIELDS)
        )

    def test_replay_locates_the_canonical_model_relative_to_the_script(self):
        self.assertTrue(
            os.path.isabs(replay_seeds.CANONICAL_MODEL_PY),
            "the canonical model path must be resolved relative to the script",
        )
        self.assertTrue(replay_seeds.CANONICAL_MODEL_PY.endswith(
            os.path.join("simulation-model", "src", "model.py")
        ))
        self.assertTrue(os.path.isfile(replay_seeds.CANONICAL_MODEL_PY))

    def test_replay_module_does_not_import_simpy_at_module_level(self):
        # The module-level import of replay-seeds.py must stay simpy-free so
        # its self-tests can skip cleanly on hosts without simpy (P5-R1/R6).
        import ast

        with open(REPLAY_SEEDS_PY, "r", encoding="utf-8") as fh:
            tree = ast.parse(fh.read())
        top_level_simpy = [
            node
            for node in tree.body
            if (
                isinstance(node, ast.Import)
                and any(alias.name == "simpy" for alias in node.names)
                or isinstance(node, ast.ImportFrom)
                and node.module == "simpy"
            )
        ]
        self.assertEqual(top_level_simpy, [], "replay-seeds.py must not import simpy at module level")


class TestAssertValidRows(unittest.TestCase):
    def test_valid_rows_pass_with_summary(self):
        rows = two_valid_rows()
        violations, summary = assert_results.validate_rows(rows, make_context(2))
        self.assertEqual(violations, [])
        self.assertEqual(summary["rows"], 2)
        self.assertTrue(summary["seeds_distinct"])
        self.assertEqual(summary["number_of_computed_reps"], 2)
        self.assertEqual(summary["assertions_passed"], True)

    def test_zero_safe_empty_measured_set_is_valid(self):
        row = make_row(7, 0, 0, 0.0, 0.0)
        self.assertEqual(row["abandonment_probability"], 0.0)
        violations, _ = assert_results.validate_rows([row, two_valid_rows()[0]], make_context(2))
        self.assertEqual(violations, [])

    def test_half_width_and_verdict_are_pps_style(self):
        # Observations [0.91, 0.88]: mean 0.895, two-pass s = 0.015*sqrt(2),
        # h = t(0.975, df=1) * s / sqrt(2) = 12.706204736174703 * 0.015.
        rows = two_valid_rows()
        _, summary = assert_results.validate_rows(rows, make_context(2))
        self.assertAlmostEqual(summary["sample_mean"], 0.895, places=12)
        self.assertAlmostEqual(
            summary["half_width"], 12.706204736174703 * 0.015, delta=2e-9
        )
        self.assertEqual(summary["recomputed_verdict"], "unmet")

    def test_zero_variance_is_met_with_zero_half_width(self):
        rows = [make_row(21, 40, 6, 0.9, 0.6), make_row(22, 40, 6, 0.9, 0.6)]
        _, summary = assert_results.validate_rows(rows, make_context(2))
        self.assertEqual(summary["half_width"], 0.0)
        self.assertEqual(summary["recomputed_verdict"], "met")


class TestAssertViolationClasses(unittest.TestCase):
    def assert_violated(self, rows, needle, context=None):
        violations, summary = assert_results.validate_rows(
            rows, context if context is not None else make_context(2)
        )
        self.assertIsNone(summary, "summary must not be produced with violations")
        self.assertTrue(
            any(needle in v for v in violations),
            "expected a violation containing {!r}; got {}".format(needle, violations),
        )

    def test_missing_field(self):
        rows = two_valid_rows()
        del rows[0]["effective_seed"]
        self.assert_violated(rows, "missing fields")

    def test_extra_field(self):
        rows = two_valid_rows()
        rows[1]["extra_field"] = 1
        self.assert_violated(rows, "unexpected fields")

    def test_count_as_string(self):
        rows = two_valid_rows()
        rows[0]["served_customers"] = "40"
        self.assert_violated(rows, "served_customers must be a non-negative integer")

    def test_count_as_boolean(self):
        rows = two_valid_rows()
        rows[0]["completed_customers"] = True
        self.assert_violated(rows, "completed_customers must be a non-negative integer")

    def test_negative_count(self):
        rows = two_valid_rows()
        rows[0]["abandoned_customers"] = -1
        self.assert_violated(rows, "abandoned_customers must be a non-negative integer")

    def test_wrong_model_id(self):
        rows = two_valid_rows()
        rows[0]["model_id"] = "erlang_a_v0"
        self.assert_violated(rows, "model_id must be 'erlang_a_v1'")

    def test_non_finite_float(self):
        rows = two_valid_rows()
        rows[0]["mean_wait_time"] = float("inf")
        self.assert_violated(rows, "mean_wait_time must be a finite number")
        rows[1]["throughput"] = float("nan")
        self.assert_violated(rows, "throughput must be a finite number")

    def test_negative_mean_wait_time(self):
        rows = two_valid_rows()
        rows[0]["mean_wait_time"] = -0.5
        self.assert_violated(rows, "mean_wait_time must be non-negative")

    def test_abandonment_probability_out_of_bounds(self):
        rows = two_valid_rows()
        rows[0]["abandonment_probability"] = 1.5
        self.assert_violated(rows, "abandonment_probability must be in [0, 1]")

    def test_utilization_out_of_bounds(self):
        rows = two_valid_rows()
        rows[0]["server_utilization"] = 1.2
        self.assert_violated(rows, "server_utilization must be in [0, 1]")

    def test_alias_mismatch(self):
        rows = two_valid_rows()
        rows[0]["completed_customers"] = rows[0]["served_customers"] - 1
        self.assert_violated(rows, "alias mismatch")

    def test_accounting_mismatch(self):
        rows = two_valid_rows()
        rows[0]["total_customers"] = rows[0]["served_customers"] + rows[0]["abandoned_customers"] + 1
        self.assert_violated(rows, "accounting mismatch")

    def test_throughput_mismatch(self):
        rows = two_valid_rows()
        rows[0]["throughput"] = rows[0]["served_customers"] / 500 + 1e-3
        self.assert_violated(rows, "throughput")

    def test_abandonment_probability_mismatch(self):
        rows = two_valid_rows()
        row = rows[0]
        row["abandonment_probability"] = (row["abandoned_customers"] + 1) / row["total_customers"]
        self.assert_violated(rows, "abandonment_probability")

    def test_design_value_mismatch(self):
        rows = two_valid_rows()
        rows[0]["arrival_rate"] = 5
        self.assert_violated(rows, "baked arrival_rate is 5, expected design value 4")

    def test_duplicate_effective_seeds(self):
        rows = two_valid_rows()
        rows[1]["effective_seed"] = rows[0]["effective_seed"]
        self.assert_violated(rows, "duplicate effective seeds")

    def test_count_does_not_match_computed_reps(self):
        rows = two_valid_rows()
        self.assert_violated(rows, "computed-replication count", context=make_context(3))

    def test_empty_row_set(self):
        violations, summary = assert_results.validate_rows([], make_context(2))
        self.assertEqual(violations, ["no exported result rows"])
        self.assertIsNone(summary)

    def test_rows_not_a_list(self):
        violations, summary = assert_results.validate_rows({"rows": []}, make_context(2))
        self.assertEqual(violations, ["rows file is not a JSON array"])
        self.assertIsNone(summary)

    def test_context_contract(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "context.json")
            with open(path, "w", encoding="utf-8") as fh:
                json.dump(make_context(2), fh)
            context = assert_results.load_context(path)
            self.assertEqual(context["number_of_computed_reps"], 2)
            bad = make_context(2)
            del bad["confidence_metric"]
            with open(path, "w", encoding="utf-8") as fh:
                json.dump(bad, fh)
            with self.assertRaises(ValueError):
                assert_results.load_context(path)


class TestStudentTQuantile(unittest.TestCase):
    # Known t(0.975, df) values. df=1 and df=2 are exact closed forms
    # (t = tan(pi * 0.475) for the Cauchy case; the quadratic identity for
    # df=2); the rest are verified by adaptive numerical integration of the
    # Student-t density (relative agreement with the implementation < 1e-9).
    KNOWN = {
        1: 12.706204736174703,
        2: 4.302652729749464,
        3: 3.182446305345366,
        4: 2.776445105246239,
        9: 2.262157162749645,
        30: 2.042272456219507,
        60: 2.000297821996355,
        120: 1.979930405013874,
        1000: 1.962339080910169,
    }

    def test_known_quantile_values(self):
        for df, expected in self.KNOWN.items():
            with self.subTest(df=df):
                got = assert_results.student_t_quantile(0.975, df)
                self.assertAlmostEqual(got, expected, delta=expected * 1e-7)

    def test_hand_computed_half_width_vector(self):
        # obs [2.0, 2.2, 1.8]: mean 2.0, s = 0.2, df = 2,
        # h = t(0.975, 2) * 0.2 / sqrt(3).
        mean, std = assert_results.sample_mean_std([2.0, 2.2, 1.8])
        self.assertAlmostEqual(mean, 2.0, places=12)
        self.assertAlmostEqual(std, 0.2, places=12)
        expected = 4.302652729749464 * 0.2 / math.sqrt(3.0)
        got = assert_results.student_t_quantile(0.975, 2) * std / math.sqrt(3.0)
        self.assertAlmostEqual(got, expected, delta=expected * 1e-7)

    def test_degenerate_sample_means(self):
        self.assertEqual(assert_results.sample_mean_std([]), (0.0, 0.0))
        self.assertEqual(assert_results.sample_mean_std([3.5]), (3.5, 0.0))


def run_cli(args):
    return subprocess.run(
        [PYTHON] + args, capture_output=True, text=True, timeout=120
    )


class TestAssertCLI(unittest.TestCase):
    def _write(self, tmp, name, payload):
        path = os.path.join(tmp, name)
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(payload, fh)
        return path

    def test_passing_run_exits_zero_and_writes_summary(self):
        with tempfile.TemporaryDirectory() as tmp:
            rows = self._write(tmp, "rows.json", two_valid_rows())
            context = self._write(tmp, "context.json", make_context(2))
            summary = os.path.join(tmp, "summary.json")
            proc = run_cli(
                [ASSERT_RESULTS_PY, "--rows", rows, "--context", context, "--summary-out", summary]
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertIn("PASS", proc.stdout)
            with open(summary, "r", encoding="utf-8") as fh:
                data = json.load(fh)
            self.assertTrue(data["assertions_passed"])

    def test_violation_exits_nonzero(self):
        with tempfile.TemporaryDirectory() as tmp:
            rows_payload = two_valid_rows()
            rows_payload[0]["total_customers"] += 1
            rows = self._write(tmp, "rows.json", rows_payload)
            context = self._write(tmp, "context.json", make_context(2))
            proc = run_cli([ASSERT_RESULTS_PY, "--rows", rows, "--context", context])
            self.assertEqual(proc.returncode, 1)
            self.assertIn("VIOLATION", proc.stderr)

    def test_bad_context_exits_two(self):
        with tempfile.TemporaryDirectory() as tmp:
            rows = self._write(tmp, "rows.json", two_valid_rows())
            context = self._write(tmp, "context.json", make_context(2))
            os.unlink(context)
            proc = run_cli([ASSERT_RESULTS_PY, "--rows", rows, "--context", context])
            self.assertEqual(proc.returncode, 2)


@unittest.skipUnless(
    HAS_SIMPY,
    "SimPy is not installed; the replay self-tests skip cleanly, mirroring the "
    "repository conformance convention (P5-R1)",
)
class TestReplay(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.model = load_module(
            "pads_paper_canonical_model_replay_test", os.path.join(CANONICAL_SRC, "model.py")
        )
        cls.result = load_module(
            "pads_paper_canonical_result_replay_test", os.path.join(CANONICAL_SRC, "result.py")
        )

    def build_row(self, seed, run_duration=None):
        scenario = dict(DESIGN)
        if run_duration is not None:
            scenario["run_duration"] = run_duration
        rng = random.Random(seed)
        metrics = self.model.run(
            scenario["arrival_rate"], scenario["service_rate"], scenario["run_duration"], rng
        )
        return self.result.build_result(
            {
                "parameterset_id": scenario["parameterset_id"],
                "arrival_rate": scenario["arrival_rate"],
                "service_rate": scenario["service_rate"],
                "run_duration": scenario["run_duration"],
                "seed_policy": scenario["seed_policy"],
            },
            metrics,
            seed,
        )

    def test_simpy_contract_reports_412(self):
        self.assertEqual(replay_seeds.simpy_version(), "4.1.2")
        self.assertEqual(replay_seeds.require_simpy(), "4.1.2")

    def test_successful_replay_of_canonical_rows(self):
        rows = [self.build_row(99), self.build_row(100)]
        report = replay_seeds.replay_rows(rows)
        self.assertEqual(len(report), 2)
        self.assertTrue(all(entry["passed"] for entry in report), report)
        self.assertTrue(all(entry["mismatches"] == [] for entry in report), report)
        self.assertEqual([entry["effective_seed"] for entry in report], [99, 100])

    def test_replay_is_deterministic_for_a_recorded_seed(self):
        row = self.build_row(1234)
        first = replay_seeds.replay_rows([row])
        second = replay_seeds.replay_rows([row])
        self.assertEqual(first, second)
        self.assertTrue(first[0]["passed"])

    def test_perturbed_counts_are_detected(self):
        row = self.build_row(2027)
        served = row["served_customers"]
        abandoned = row["abandoned_customers"]
        total = served + abandoned + 1
        tampered = dict(row)
        tampered.update(
            {
                "completed_customers": served + 1,
                "served_customers": served + 1,
                "total_customers": total,
                "throughput": (served + 1) / row["run_duration"],
                "abandonment_probability": abandoned / total if total else 0.0,
            }
        )
        # The tampered row still passes the shape contract (consistent
        # accounting), so the failure must come from the replay comparison.
        report = replay_seeds.replay_rows([tampered])
        self.assertFalse(report[0]["passed"])
        self.assertTrue(
            any("served_customers" in mismatch for mismatch in report[0]["mismatches"]),
            report[0]["mismatches"],
        )

    def test_perturbed_float_metric_is_detected(self):
        row = self.build_row(31337)
        tampered = dict(row)
        tampered["mean_wait_time"] = row["mean_wait_time"] + 1e-9
        report = replay_seeds.replay_rows([tampered])
        self.assertFalse(report[0]["passed"])
        self.assertTrue(
            any(mismatch.startswith("mean_wait_time") for mismatch in report[0]["mismatches"]),
            report[0]["mismatches"],
        )

    def test_malformed_row_is_reported_before_replay(self):
        row = self.build_row(4242)
        del row["effective_seed"]
        report = replay_seeds.replay_rows([row])
        self.assertFalse(report[0]["passed"])
        self.assertTrue(
            all(mismatch.startswith("malformed row") for mismatch in report[0]["mismatches"]),
            report[0]["mismatches"],
        )

    def test_distinct_seeds_replay_independently(self):
        rows = [self.build_row(501), self.build_row(502)]
        self.assertNotEqual(rows[0]["effective_seed"], rows[1]["effective_seed"])
        report = replay_seeds.replay_rows(rows)
        self.assertTrue(all(entry["passed"] for entry in report), report)


class TestReplayCLI(unittest.TestCase):
    @unittest.skipUnless(
        HAS_SIMPY,
        "SimPy is not installed; the replay self-tests skip cleanly, mirroring the "
        "repository conformance convention (P5-R1)",
    )
    def test_all_rows_match_exits_zero_and_writes_report(self):
        model = load_module(
            "pads_paper_canonical_model_cli", os.path.join(CANONICAL_SRC, "model.py")
        )
        result = load_module(
            "pads_paper_canonical_result_cli", os.path.join(CANONICAL_SRC, "result.py")
        )
        rng = random.Random(7)
        metrics = model.run(DESIGN["arrival_rate"], DESIGN["service_rate"], 100, rng)
        row = result.build_result(
            {
                "parameterset_id": DESIGN["parameterset_id"],
                "arrival_rate": DESIGN["arrival_rate"],
                "service_rate": DESIGN["service_rate"],
                "run_duration": 100,
                "seed_policy": DESIGN["seed_policy"],
            },
            metrics,
            7,
        )
        with tempfile.TemporaryDirectory() as tmp:
            rows_path = os.path.join(tmp, "rows.json")
            with open(rows_path, "w", encoding="utf-8") as fh:
                json.dump([row], fh)
            report_path = os.path.join(tmp, "report.json")
            proc = run_cli(
                [REPLAY_SEEDS_PY, "--rows", rows_path, "--report", report_path]
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertIn("PASS", proc.stdout)
            with open(report_path, "r", encoding="utf-8") as fh:
                report = json.load(fh)
            self.assertTrue(report["all_passed"])
            self.assertEqual(report["simpy_version"], "4.1.2")
            self.assertEqual(report["rows_total"], 1)
            self.assertEqual(report["rows"][0]["effective_seed"], 7)

    @unittest.skipUnless(
        HAS_SIMPY,
        "SimPy is not installed; the replay self-tests skip cleanly, mirroring the "
        "repository conformance convention (P5-R1)",
    )
    def test_mismatched_row_exits_nonzero(self):
        model = load_module(
            "pads_paper_canonical_model_cli2", os.path.join(CANONICAL_SRC, "model.py")
        )
        result = load_module(
            "pads_paper_canonical_result_cli2", os.path.join(CANONICAL_SRC, "result.py")
        )
        rng = random.Random(8)
        metrics = model.run(DESIGN["arrival_rate"], DESIGN["service_rate"], 100, rng)
        row = result.build_result(
            {
                "parameterset_id": DESIGN["parameterset_id"],
                "arrival_rate": DESIGN["arrival_rate"],
                "service_rate": DESIGN["service_rate"],
                "run_duration": 100,
                "seed_policy": DESIGN["seed_policy"],
            },
            metrics,
            8,
        )
        row["mean_wait_time"] += 1e-6
        with tempfile.TemporaryDirectory() as tmp:
            rows_path = os.path.join(tmp, "rows.json")
            with open(rows_path, "w", encoding="utf-8") as fh:
                json.dump([row], fh)
            proc = run_cli([REPLAY_SEEDS_PY, "--rows", rows_path])
            self.assertEqual(proc.returncode, 1)
            self.assertIn("FAILED", proc.stderr)

    @unittest.skipUnless(
        HAS_SIMPY,
        "SimPy is not installed; the replay self-tests skip cleanly, mirroring the "
        "repository conformance convention (P5-R1)",
    )
    def test_empty_row_set_exits_nonzero(self):
        with tempfile.TemporaryDirectory() as tmp:
            rows_path = os.path.join(tmp, "rows.json")
            with open(rows_path, "w", encoding="utf-8") as fh:
                json.dump([], fh)
            proc = run_cli([REPLAY_SEEDS_PY, "--rows", rows_path])
            self.assertEqual(proc.returncode, 2)


if __name__ == "__main__":
    unittest.main()
