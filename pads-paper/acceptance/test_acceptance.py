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

"""Offline self-tests for the P5 acceptance Python tools (P5-R5, P5-R6) and
the P6 campaign design tooling and driver contract (P6-R1, P6-R2).

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

Covers, for the P6 paper-result bundle contract:

- the campaign design validation (``pads-paper/results/analysis/campaign.py``)
  over the committed ``design.json`` and every ruled violation class
  (duplicate entry values, unknown/missing entry parameters, unresolvable
  recipe references, invalid scenario settings, unlabeled or duplicate
  scenario labels, wrong schema/model, non-strict JSON);
- the ``run-experiment.sh`` refusal paths (unknown argument, missing
  environment, missing design file, malformed design before any kubectl
  call, wrong kube context, non-digest image, bad campaign timeout);
- the single-scenario default-mode and the multi-scenario ``--design``
  campaign stub dry-runs: a stub ``kubectl`` (read-only preflight answers
  plus a canned Core DB/Result DB) and a stub EDS server prove the full
  driver flow offline, including the ruled per-scenario evidence structure
  and the P5 sanitization rules (SimPy-gated: the driver's live replay
  requires SimPy 4.1.2; skip cleanly without it).

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


# =============================================================================
# P6 — campaign design validation and driver contract
# =============================================================================

RESULTS_ROOT = os.path.normpath(os.path.join(PADS_PAPER_ROOT, "results"))
CAMPAIGN_PY = os.path.join(RESULTS_ROOT, "analysis", "campaign.py")
CAMPAIGN_DESIGN_JSON = os.path.join(RESULTS_ROOT, "campaign", "design.json")
RESULTS_EVIDENCE_DIR = os.path.join(RESULTS_ROOT, "evidence")
ARTIFACTS_PADS_DIR = os.path.normpath(
    os.path.join(PADS_PAPER_ROOT, os.pardir, "artifacts", "pads")
)
RUN_EXPERIMENT_SH = os.path.join(HERE, "run-experiment.sh")

#: The P5 single-scenario ruled values the default driver path bakes.
P5_DESIGN_VALUES = {
    "arrival_rate": 4,
    "service_rate": 4,
    "run_duration": 500,
    "seed_policy": 2027,
    "priority": 5,
    "number_of_reps": 5,
    "confidence_metric": 0.05,
}


def load_campaign_module():
    return load_module("pads_acceptance_results_campaign", CAMPAIGN_PY)


campaign = load_campaign_module()


def load_campaign_design():
    with open(CAMPAIGN_DESIGN_JSON, "r", encoding="utf-8") as fh:
        return json.load(fh)


# --- campaign design validation (P6-R1) ---------------------------------------


class TestCampaignDesignValidation(unittest.TestCase):
    def _violated(self, design, needle):
        violations = campaign.validate_design(design)
        self.assertTrue(
            any(needle in v for v in violations),
            "expected a violation containing {!r}; got {}".format(needle, violations),
        )

    def _valid_variant(self, mutate):
        design = load_campaign_design()
        mutate(design)
        return design

    def test_committed_design_is_valid_and_matches_p6_r1(self):
        design = load_campaign_design()
        self.assertEqual(campaign.validate_design(design), [])
        plan = campaign.plan(design)
        self.assertEqual(plan["entries"]["arrival_rate"], [4, 6, 8, 10])
        self.assertEqual(plan["entries"]["service_rate"], [4, 5])
        self.assertEqual(plan["entries"]["run_duration"], [500])
        self.assertEqual(plan["entries"]["seed_policy"], [2027])
        self.assertEqual([r["label"] for r in plan["recipes"]],
                         ["R1", "R2", "R3", "R4", "R5", "R6", "R7", "R8"])
        self.assertEqual(
            [(r["arrival_rate"], r["service_rate"]) for r in plan["recipes"]],
            [(4, 4), (6, 4), (8, 4), (10, 4), (4, 5), (6, 5), (8, 5), (10, 5)],
        )
        self.assertEqual([s["label"] for s in plan["scenarios"]],
                         ["A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8",
                          "B1", "B2", "B3"])
        for s in plan["scenarios"][:8]:
            self.assertEqual((s["priority"], s["number_of_reps"], s["confidence_metric"]), (1, 20, 0.05))
        for s in plan["scenarios"][8:]:
            self.assertEqual(s["recipe"], "R3")
            self.assertEqual((s["priority"], s["number_of_reps"]), (2, 10))
        self.assertEqual(
            [s["confidence_metric"] for s in plan["scenarios"][8:]], [0.20, 0.10, 0.05]
        )
        self.assertEqual([r["rho"] for r in plan["recipes"]],
                         [0.5, 0.75, 1.0, 1.25, 0.4, 0.6, 0.8, 1.0])

    def test_duplicate_entry_values_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["entries"]["arrival_rate"].extend([4])),
            "duplicate values",
        )

    def test_unknown_entry_parameter_rejected(self):
        design = self._valid_variant(lambda d: d["entries"].update({"patience_rate": [1]}))
        self._violated(design, "unknown parameters")

    def test_missing_entry_parameter_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["entries"].pop("seed_policy")),
            "missing parameters",
        )

    def test_non_integer_entry_value_rejected(self):
        design = self._valid_variant(
            lambda d: d["entries"]["arrival_rate"].__setitem__(0, "4")
        )
        self._violated(design, "must all be integers")

    def test_nonpositive_arrival_value_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["entries"]["arrival_rate"].__setitem__(0, 0)),
            "must be positive",
        )

    def test_negative_seed_policy_value_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["entries"]["seed_policy"].__setitem__(0, -1)),
            "seed_policy value -1",
        )

    def test_recipe_value_not_in_entries_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["recipes"][0].__setitem__("arrival_rate", 5)),
            "does not resolve to an entry value",
        )

    def test_recipe_missing_key_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["recipes"][0].pop("service_rate")),
            "missing keys",
        )

    def test_recipe_duplicate_label_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["recipes"][1].__setitem__("label", "R1")),
            "duplicate recipe label",
        )

    def test_scenario_recipe_not_resolved_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["scenarios"][0].__setitem__("recipe", "R9")),
            "does not reference a defined recipe label",
        )

    def test_scenario_priority_zero_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["scenarios"][0].__setitem__("priority", 0)),
            "priority must be a positive integer",
        )

    def test_scenario_reps_out_of_range_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["scenarios"][0].__setitem__("number_of_reps", 0)),
            "number_of_reps must be an integer",
        )
        self._violated(
            self._valid_variant(
                lambda d: d["scenarios"][0].__setitem__("number_of_reps", 100001)
            ),
            "number_of_reps must be an integer",
        )

    def test_scenario_confidence_metric_nonpositive_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["scenarios"][0].__setitem__("confidence_metric", 0.0)),
            "confidence_metric must be a finite number > 0",
        )
        self._violated(
            self._valid_variant(lambda d: d["scenarios"][0].__setitem__("confidence_metric", True)),
            "confidence_metric must be a finite number > 0",
        )

    def test_scenario_confidence_metric_nan_refused_by_strict_json(self):
        text = json.dumps(load_campaign_design()).replace('"confidence_metric": 0.05', '"confidence_metric": NaN')
        with self.assertRaises(campaign.DesignError):
            campaign.load_design(CAMPAIGN_DESIGN_JSON, raw_text=text)

    def test_scenario_label_lowercase_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["scenarios"][0].__setitem__("label", "a1")),
            "label must match",
        )

    def test_scenario_label_without_digits_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["scenarios"][0].__setitem__("label", "A")),
            "label must match",
        )

    def test_scenario_duplicate_label_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["scenarios"][1].__setitem__("label", "A1")),
            "duplicate scenario label",
        )

    def test_wrong_schema_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d.__setitem__("schema", "pads-campaign-design-v0")),
            "schema must be",
        )

    def test_wrong_model_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d.__setitem__("model", "erlang_a_v0")),
            "model must be",
        )

    def test_unknown_top_level_key_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d.__setitem__("notes", "x")),
            "unexpected keys",
        )

    def test_missing_top_level_key_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d.pop("campaign")),
            "missing keys",
        )

    def test_multiple_run_duration_entries_rejected(self):
        self._violated(
            self._valid_variant(lambda d: d["entries"]["run_duration"].append(600)),
            "exactly one entry value",
        )


# --- driver refusal paths (offline, stub kubectl) ------------------------------

STUB_KUBECTL = r"""#!/usr/bin/env python3
# Stub kubectl for the P6 offline dry-runs (test_acceptance.py). Answers only
# the read-only preflight calls with canned values, no-ops every mutation,
# serves the stub EDS over port-forward, and answers the psql exports from
# <stub-dir>/state.json. Every invocation is appended to kubectl-calls.log.
import json
import os
import re
import signal
import subprocess
import sys


def main():
    stub_dir = os.environ["PADS_STUB_DIR"]
    with open(os.path.join(stub_dir, "kubectl-calls.log"), "a", encoding="utf-8") as fh:
        fh.write(" ".join(sys.argv[1:]) + "\n")
    rest = []
    i = 0
    args = sys.argv[1:]
    while i < len(args):
        if args[i] == "--kubeconfig":
            i += 2
            continue
        rest.append(args[i])
        i += 1

    def out(s):
        sys.stdout.write(s if s.endswith("\n") else s + "\n")

    # Real kubectl reads stdin when "-f -" is given; drain it exactly the
    # same way so upstream writers (jq) never see a closed pipe.
    if "-f" in rest and rest[rest.index("-f") + 1] == "-":
        sys.stdin.read()

    if not rest:
        sys.exit(0)
    sub = rest[0]
    if sub == "config":
        out(os.environ.get("PADS_STUB_CONTEXT", "stub-context"))
    elif sub == "get":
        what = rest[1]
        if what == "secret":
            if "json" in rest and "jsonpath" not in rest:
                out(json.dumps({"apiVersion": "v1", "kind": "Secret",
                                "metadata": {"name": rest[2]},
                                "type": "kubernetes.io/dockerconfigjson",
                                "data": {}}))
            else:
                out("kubernetes.io/dockerconfigjson")
        elif what == "pod":
            out("resultdb-0" if any("resultdb" in a for a in rest) else "core-db-0")
        elif what == "simulationexperiment":
            if any(a.startswith("jsonpath") for a in rest):
                out("InProgress")
            else:
                out(json.dumps({"apiVersion": "cbse.terministic.de/v1",
                                "kind": "SimulationExperiment",
                                "metadata": {"name": rest[2]},
                                "status": {"phase": "Finished"}}))
    elif sub in ("create", "label", "patch"):
        if sub == "create" and rest[1] == "secret" and "-o" in rest:
            out("apiVersion: v1\nkind: Secret\nmetadata:\n  name: core-db-credentials\ntype: Opaque\n")
    elif sub in ("apply", "rollout", "logs", "delete"):
        if sub == "logs":
            out("Scenario Manager is ready")
    elif sub == "kustomize":
        out("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: stub-stack\n")
    elif sub == "port-forward":
        local_port = "18082"
        for a in rest:
            if re.match(r"^\d+:\d+$", a):
                local_port = a.split(":")[0]
        child = subprocess.Popen(
            [sys.executable, os.path.join(stub_dir, "eds-stub.py"),
             "--port", local_port, "--stub-dir", stub_dir]
        )

        def _term(signum, frame):
            child.terminate()
            sys.exit(143)

        signal.signal(signal.SIGTERM, _term)
        signal.signal(signal.SIGINT, _term)
        try:
            child.wait()
        except KeyboardInterrupt:
            child.terminate()
    elif sub == "exec":
        sql = rest[rest.index("-c") + 1]
        with open(os.path.join(stub_dir, "state.json"), "r", encoding="utf-8") as fh:
            state = json.load(fh)
        sequence = state.get("state_sequence")
        if sequence is not None:
            # Delayed terminal states (settlement regression guard): the
            # scenario-state query serves one per-row snapshot from
            # state_sequence per invocation (the state-query-count counter
            # file persists across invocations), and the Core DB export
            # returns the pre-translation rows (non_terminal_rows: Created,
            # translation_attempts 0, empty container_image — the shape the
            # live settlement campaigns exported) until the current snapshot
            # is fully terminal.
            count_path = os.path.join(stub_dir, "state-query-count")
            count = 0
            if os.path.isfile(count_path):
                with open(count_path, "r", encoding="utf-8") as fh:
                    count = int(fh.read().strip() or 0)
            snapshot = sequence[count] if count < len(sequence) else sequence[-1]
            if sql.startswith("SELECT s.state"):
                with open(count_path, "w", encoding="utf-8") as fh:
                    fh.write(str(count + 1))
                for row_state in snapshot:
                    out(row_state)
            elif sql.startswith("SELECT json_agg(row_to_json(s)"):
                if all(row_state == "Finished" for row_state in snapshot):
                    out(json.dumps(state["scenario_rows"]))
                else:
                    out(json.dumps(state["non_terminal_rows"]))
            elif "SELECT json_agg(result" in sql:
                match = re.search(r"scenario_(\d+)_results", sql)
                out(json.dumps(state["rows_by_scenario_id"][match.group(1)]))
            else:
                sys.stderr.write(
                    "stub kubectl: unknown SQL under state_sequence: " + sql + "\n"
                )
                sys.exit(1)
        elif sql.startswith("SELECT s.state"):
            for row in state["scenario_rows"]:
                out(row["state"])
        elif sql.startswith("SELECT json_agg(row_to_json(s)"):
            out(json.dumps(state["scenario_rows"]))
        elif "SELECT json_agg(result" in sql:
            match = re.search(r"scenario_(\d+)_results", sql)
            out(json.dumps(state["rows_by_scenario_id"][match.group(1)]))
        else:
            sys.stderr.write("stub kubectl: unknown SQL: " + sql + "\n")
            sys.exit(1)
    else:
        sys.stderr.write("stub kubectl: unhandled: " + " ".join(rest) + "\n")
        sys.exit(64)


if __name__ == "__main__":
    main()
"""

STUB_EDS = r"""#!/usr/bin/env python3
# Stub EDS server for the P6 offline dry-runs: mirrors the reference EDS JSON
# API (ids from floor 5 per parameter table, recipes, scenarios, save,
# start, state) over a localhost port. Stdlib only.
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PARAMS = ("arrival_rate", "service_rate", "run_duration", "seed_policy")


class State:
    def __init__(self):
        self.entries = []
        self.next_id = {p: 5 for p in PARAMS}
        self.recipes = []
        self.next_psid = 5
        self.scenarios = []

    def entry_value(self, parameter, entry_id):
        for entry_id_, param, value in self.entries:
            if param == parameter and entry_id_ == entry_id:
                return value
        return None


STATE = State()


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def _send(self, code, obj):
        body = json.dumps(obj).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path != "/api/state":
            self._send(404, {"error": "no such endpoint"})
            return
        st = STATE
        entries = [{"id": i, "parameter": p, "value": v} for i, p, v in st.entries]
        recipes = [
            {"parameterset_id": ps, "arrival_rate_id": a, "service_rate_id": s,
             "run_duration_id": r, "seed_policy_id": sp}
            for ps, a, s, r, sp in st.recipes
        ]
        scenarios = [
            {"index": index, "priority": p, "number_of_reps": nr,
             "confidence_metric": cm, "parameterset_id": ps, "published": True}
            for index, (p, nr, cm, ps) in enumerate(st.scenarios)
        ]
        db_rows = []
        for ps, a, s, r, sp in st.recipes:
            db_rows.append({"parameterset_id": ps,
                            "arrival_rate": st.entry_value("arrival_rate", a),
                            "service_rate": st.entry_value("service_rate", s),
                            "run_duration": st.entry_value("run_duration", r),
                            "seed_policy": st.entry_value("seed_policy", sp)})
        self._send(200, {"design": {"entries": entries, "recipes": recipes,
                                    "scenarios": scenarios},
                         "flow": {"saved": True, "started": True,
                                  "finalized": False,
                                  "last_handshake": {"status": "ready",
                                                    "batch_subject": "stub.batch.subject"},
                                  "last_publish": {"status": "inserted"}},
                         "db_rows": db_rows})

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length else b"{}"
        try:
            obj = json.loads(body)
        except Exception:
            self._send(400, {"error": "malformed JSON body"})
            return
        st = STATE
        if self.path == "/api/entries":
            parameter, value = obj.get("parameter"), obj.get("value")
            if parameter not in PARAMS or not isinstance(value, int):
                self._send(400, {"error": "bad entry"})
                return
            entry_id = st.next_id[parameter]
            st.next_id[parameter] += 1
            st.entries.append((entry_id, parameter, value))
            self._send(200, {"id": entry_id})
        elif self.path == "/api/recipes":
            ids = {f: obj.get(f) for f in
                   ("arrival_rate_id", "service_rate_id",
                    "run_duration_id", "seed_policy_id")}
            if not all(isinstance(v, int) for v in ids.values()):
                self._send(400, {"error": "bad recipe"})
                return
            psid = st.next_psid
            st.next_psid += 1
            st.recipes.append((psid, ids["arrival_rate_id"], ids["service_rate_id"],
                               ids["run_duration_id"], ids["seed_policy_id"]))
            self._send(200, {"parameterset_id": psid})
        elif self.path == "/api/scenarios":
            psid = obj.get("parameterset_id")
            known = {ps for ps, *_ in st.recipes}
            if psid not in known or obj.get("priority", 0) <= 0:
                self._send(400, {"error": "bad scenario"})
                return
            st.scenarios.append((obj["priority"], obj["number_of_reps"],
                                 obj["confidence_metric"], psid))
            self._send(200, {"index": len(st.scenarios) - 1})
        elif self.path == "/api/save":
            self._send(200, {"saved_entries": len(st.entries),
                             "saved_recipes": len(st.recipes)})
        elif self.path == "/api/start":
            self._send(200, {"batch_id": "stub-batch", "status": "inserted",
                             "published": len(st.scenarios),
                             "batch_subject": "stub.batch.subject"})
        else:
            self._send(404, {"error": "no such endpoint"})


if __name__ == "__main__":
    port = int(sys.argv[sys.argv.index("--port") + 1])
    server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    server.serve_forever()
"""


def write_stub(tmp, context="stub-context"):
    """Write the stub kubectl + EDS into ``tmp``; return (stub_dir, kubectl)."""
    stub_dir = os.path.join(tmp, "stub")
    os.makedirs(stub_dir, exist_ok=True)
    kubectl = os.path.join(stub_dir, "kubectl-stub.py")
    with open(kubectl, "w", encoding="utf-8") as fh:
        fh.write(STUB_KUBECTL)
    os.chmod(kubectl, 0o755)
    with open(os.path.join(stub_dir, "eds-stub.py"), "w", encoding="utf-8") as fh:
        fh.write(STUB_EDS)
    with open(os.path.join(stub_dir, "kubeconfig"), "w", encoding="utf-8") as fh:
        fh.write("stub kubeconfig (never read by the driver)\n")
    return stub_dir, kubectl


def free_port():
    import socket

    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def driver_env(tmp, stub_dir, kubectl, extra=None, missing=()):
    env = {
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
        "KUBECONFIG": os.path.join(stub_dir, "kubeconfig"),
        "CBSE_EXPECTED_CONTEXT": "stub-context",
        "KUBECTL": kubectl,
        "CBSE_REGISTRY": "stub-registry.example/cbse",
        "OPERATOR_IMAGE": "stub-registry.example/exop@sha256:" + "a" * 64,
        "SM_IMAGE": "stub-registry.example/sm@sha256:" + "b" * 64,
        "EDS_REFERENCE_IMAGE": "stub-registry.example/eds@sha256:" + "c" * 64,
        "PPS_IMAGE": "stub-registry.example/pps@sha256:" + "d" * 64,
        "DETAIL_DB_IMAGE": "stub-registry.example/scenario-detail-database@sha256:" + "e" * 64,
        "PADS_TRANS_IMAGE": "stub-registry.example/translator@sha256:" + "f" * 64,
        "RUNNER_BASE_IMAGE": "stub-registry.example/runner-base@sha256:" + "0" * 64,
        "PADS_SMOKE_RECEIPT": "stub receipt: 15/15 specs passed (offline dry-run)",
        "PADS_PYTHON": PYTHON,
        "PADS_STUB_DIR": stub_dir,
        "PADS_EDS_LOCAL_PORT": str(free_port()),
        "PADS_STUB_CONTEXT": "stub-context",
    }
    if extra:
        env.update(extra)
    for name in missing:
        env.pop(name, None)
    return env


def run_driver(args, env, timeout=900):
    return subprocess.run(
        ["bash", RUN_EXPERIMENT_SH] + args,
        capture_output=True,
        text=True,
        env=env,
        timeout=timeout,
    )


def kubectl_calls(stub_dir):
    path = os.path.join(stub_dir, "kubectl-calls.log")
    if not os.path.isfile(path):
        return []
    with open(path, "r", encoding="utf-8") as fh:
        return [line for line in fh.read().splitlines() if line]


MUTATING_VERBS = ("create", "label", "patch", "apply", "delete")


def mutating_calls(stub_dir):
    """The stub kubectl calls that would mutate the cluster."""
    mutating = []
    for call in kubectl_calls(stub_dir):
        tokens = call.split()
        i = 0
        while i < len(tokens) - 1 and tokens[i] == "--kubeconfig":
            i += 2
        if i < len(tokens) and tokens[i] in MUTATING_VERBS:
            mutating.append(call)
    return mutating


class TestDriverArgumentRefusals(unittest.TestCase):
    def test_unknown_argument_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            stub_dir, kubectl = write_stub(tmp)
            env = driver_env(tmp, stub_dir, kubectl)
            proc = run_driver(["--bogus"], env, timeout=120)
            self.assertEqual(proc.returncode, 2)
            self.assertIn("unknown argument", proc.stderr)
            self.assertEqual(kubectl_calls(stub_dir), [])

    def test_design_flag_without_value_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            stub_dir, kubectl = write_stub(tmp)
            env = driver_env(tmp, stub_dir, kubectl)
            proc = run_driver(["--design"], env, timeout=120)
            self.assertEqual(proc.returncode, 2)
            self.assertIn("--design requires a design.json path", proc.stderr)

    def test_missing_design_file_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            stub_dir, kubectl = write_stub(tmp)
            env = driver_env(tmp, stub_dir, kubectl)
            proc = run_driver(["--design", os.path.join(tmp, "nope.json")], env, timeout=120)
            self.assertEqual(proc.returncode, 2)
            self.assertIn("Design file is missing", proc.stderr)

    def test_missing_environment_refused_default_mode(self):
        with tempfile.TemporaryDirectory() as tmp:
            stub_dir, kubectl = write_stub(tmp)
            env = driver_env(tmp, stub_dir, kubectl, missing=("KUBECONFIG",))
            proc = run_driver([], env, timeout=120)
            self.assertEqual(proc.returncode, 2)
            self.assertIn("KUBECONFIG is required", proc.stderr)
            self.assertEqual(kubectl_calls(stub_dir), [])

    def test_missing_environment_refused_design_mode(self):
        with tempfile.TemporaryDirectory() as tmp:
            stub_dir, kubectl = write_stub(tmp)
            env = driver_env(tmp, stub_dir, kubectl, missing=("CBSE_REGISTRY",))
            proc = run_driver(["--design", CAMPAIGN_DESIGN_JSON], env, timeout=120)
            self.assertEqual(proc.returncode, 2)
            self.assertIn("CBSE_REGISTRY is required", proc.stderr)


@unittest.skipUnless(
    HAS_SIMPY,
    "SimPy is not installed; the driver preflight refusal proofs skip cleanly, "
    "mirroring the repository conformance convention (P5-R1)",
)
class TestDriverPreflightRefusals(unittest.TestCase):
    def test_malformed_design_refused_before_any_kubectl_call(self):
        with tempfile.TemporaryDirectory() as tmp:
            design = load_campaign_design()
            design["entries"]["arrival_rate"].append(4)  # duplicate value
            bad_design = os.path.join(tmp, "bad-design.json")
            with open(bad_design, "w", encoding="utf-8") as fh:
                json.dump(design, fh)
            stub_dir, kubectl = write_stub(tmp)
            env = driver_env(tmp, stub_dir, kubectl)
            proc = run_driver(["--design", bad_design], env, timeout=300)
            self.assertEqual(proc.returncode, 2, proc.stderr)
            self.assertIn("duplicate values", proc.stderr)
            self.assertIn("Campaign design validation failed", proc.stderr)
            self.assertEqual(kubectl_calls(stub_dir), [])

    def test_wrong_context_refused_before_mutation(self):
        with tempfile.TemporaryDirectory() as tmp:
            stub_dir, kubectl = write_stub(tmp)
            env = driver_env(tmp, stub_dir, kubectl, extra={"PADS_STUB_CONTEXT": "other-context"})
            proc = run_driver(["--design", CAMPAIGN_DESIGN_JSON], env, timeout=300)
            self.assertEqual(proc.returncode, 2, proc.stderr)
            self.assertIn("does not match", proc.stderr)
            self.assertEqual(mutating_calls(stub_dir), [])

    def test_non_digest_image_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            stub_dir, kubectl = write_stub(tmp)
            env = driver_env(tmp, stub_dir, kubectl, extra={"OPERATOR_IMAGE": "stub-registry.example/exop:26.11.5"})
            proc = run_driver(["--design", CAMPAIGN_DESIGN_JSON], env, timeout=300)
            self.assertEqual(proc.returncode, 2, proc.stderr)
            self.assertIn("immutable digest reference", proc.stderr)
            self.assertEqual(mutating_calls(stub_dir), [])

    def test_bad_campaign_timeout_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            stub_dir, kubectl = write_stub(tmp)
            env = driver_env(tmp, stub_dir, kubectl, extra={"PADS_CAMPAIGN_TIMEOUT": "4h"})
            proc = run_driver(["--design", CAMPAIGN_DESIGN_JSON], env, timeout=300)
            self.assertEqual(proc.returncode, 2, proc.stderr)
            self.assertIn("PADS_CAMPAIGN_TIMEOUT must be a positive integer", proc.stderr)
            self.assertEqual(mutating_calls(stub_dir), [])


@unittest.skipUnless(
    HAS_SIMPY,
    "SimPy is not installed; the stub dry-runs skip cleanly, mirroring the "
    "repository conformance convention (P5-R1)",
)
class StubDryRunBase(unittest.TestCase):
    """Shared stub-crate scaffolding for the driver dry-runs."""

    @classmethod
    def setUpClass(cls):
        cls.model = load_module(
            "pads_paper_canonical_model_stub", os.path.join(CANONICAL_SRC, "model.py")
        )
        cls.result = load_module(
            "pads_paper_canonical_result_stub", os.path.join(CANONICAL_SRC, "result.py")
        )

    def build_row(self, seed, psid, arrival, service, duration, seed_policy):
        rng = random.Random(seed)
        metrics = self.model.run(arrival, service, duration, rng)
        return self.result.build_result(
            {
                "parameterset_id": psid,
                "arrival_rate": arrival,
                "service_rate": service,
                "run_duration": duration,
                "seed_policy": seed_policy,
            },
            metrics,
            seed,
        )

    @staticmethod
    def derived_seed(salt, index):
        import hashlib

        return int.from_bytes(
            hashlib.sha256("{}:{}".format(salt, index).encode("utf-8")).digest()[:8], "big"
        ) & 0x7FFF_FFFF_FFFF_FFFF

    def assert_sanitized(self, path):
        """The P5 sanitization contract for one evidence file."""
        with open(path, "r", encoding="utf-8") as fh:
            text = fh.read()
        self.assertNotIn("stub-registry.example", text, path)
        self.assertNotIn("/private/var/folders", text, path)
        self.assertNotIn("/tmp/", text, path)
        self.assertNotIn("http://", text, path)
        self.assertNotIn("https://", text, path)

    def assert_teardown_ordered(self, stub_dir):
        calls = kubectl_calls(stub_dir)
        deletes = [i for i, c in enumerate(calls) if " delete " in " " + c + " "]
        simexp = [i for i in deletes if "simulationexperiment" in calls[i]]
        namespace = [i for i in deletes if " delete namespace " in " " + calls[i] + " "]
        self.assertEqual(len(simexp), 1)
        self.assertEqual(len(namespace), 1)
        self.assertLess(
            simexp[0],
            namespace[0],
            "the experiment CRs must be deleted before the namespace",
        )

    def cleanup_run_outputs(self, run_id):
        import shutil

        for pattern in (
            os.path.join(HERE, "evidence", run_id),
            os.path.join(RESULTS_EVIDENCE_DIR, run_id),
            os.path.join(ARTIFACTS_PADS_DIR, run_id),
        ):
            if os.path.isdir(pattern):
                shutil.rmtree(pattern, ignore_errors=True)


def default_stub_state():
    """Canned Core DB / Result DB for the P5 single-scenario default path."""
    import datetime

    base = datetime.datetime(2026, 11, 9, 10, 0, 0, tzinfo=datetime.timezone.utc)
    record = {
        "id": 1,
        "project_id": 1,
        "state": "Finished",
        "created_at": base.isoformat(),
        "updated_at": (base + datetime.timedelta(minutes=33)).isoformat(),
        "priority": P5_DESIGN_VALUES["priority"],
        "number_of_reps": P5_DESIGN_VALUES["number_of_reps"],
        "number_of_computed_reps": P5_DESIGN_VALUES["number_of_reps"],
        "translation_attempts": 1,
        "translation_publish_started_at": (base + datetime.timedelta(seconds=2)).isoformat(),
        "translation_request_published_at": (base + datetime.timedelta(seconds=2.5)).isoformat(),
        "recipe_info": {"parameterset_id": 5},
        "container_image": "stub-registry.example/cbse-test-runner@sha256:" + "1" * 64,
        "confidence_metric": P5_DESIGN_VALUES["confidence_metric"],
        "runner_round": 1,
        "round_reps": P5_DESIGN_VALUES["number_of_reps"],
        "round_computed_reps": P5_DESIGN_VALUES["number_of_reps"],
        "evaluation_attempts": 1,
        "evaluation_publish_started_at": (base + datetime.timedelta(minutes=32)).isoformat(),
        "evaluation_request_published_at": (base + datetime.timedelta(minutes=32.1)).isoformat(),
    }
    return {"scenario_rows": [record]}


class TestDefaultModeStubDryRun(StubDryRunBase):
    """The single-scenario default path (no --design) over the stub cluster."""

    def test_single_scenario_dry_run(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = default_stub_state()
            # The default path bakes P5_DESIGN_VALUES; the rows must be the
            # canonical model's output for those values so the live replay
            # inside the driver matches exactly.
            rows = []
            for k in range(P5_DESIGN_VALUES["number_of_reps"]):
                seed = self.derived_seed("default-stub", k)
                rows.append(
                    self.build_row(
                        seed,
                        5,
                        P5_DESIGN_VALUES["arrival_rate"],
                        P5_DESIGN_VALUES["service_rate"],
                        P5_DESIGN_VALUES["run_duration"],
                        P5_DESIGN_VALUES["seed_policy"],
                    )
                )
            state["rows_by_scenario_id"] = {"1": rows}
            stub_dir, kubectl = write_stub(tmp)
            with open(os.path.join(stub_dir, "state.json"), "w", encoding="utf-8") as fh:
                json.dump(state, fh)
            env = driver_env(tmp, stub_dir, kubectl)
            proc = run_driver([], env, timeout=900)
            self.assertEqual(proc.returncode, 0, proc.stderr + proc.stdout)
            self.assertIn("PADS acceptance run", proc.stdout)
            run_id = proc.stdout.strip().splitlines()[-1].split(",")[0].replace(
                "PADS acceptance run ", ""
            ).split(" ")[0]
            self.assertRegex(run_id, r"^pads-[0-9]{14}-[0-9a-f]{6}$")
            evidence_dir = os.path.join(HERE, "evidence", run_id)
            try:
                self.assertTrue(os.path.isdir(evidence_dir))
                for name in (
                    "run.md",
                    "result-rows.json",
                    "scenario-status.json",
                    "eds-state.json",
                    "replay-report.json",
                    "assertions.txt",
                ):
                    path = os.path.join(evidence_dir, name)
                    self.assertTrue(os.path.isfile(path), name)
                    self.assert_sanitized(path)
                with open(os.path.join(evidence_dir, "scenario-status.json"), "r", encoding="utf-8") as fh:
                    records = json.load(fh)
                self.assertEqual(len(records), 1)
                self.assertEqual(records[0]["state"], "Finished")
                with open(os.path.join(evidence_dir, "result-rows.json"), "r", encoding="utf-8") as fh:
                    exported = json.load(fh)
                self.assertEqual(len(exported), P5_DESIGN_VALUES["number_of_reps"])
                with open(os.path.join(evidence_dir, "replay-report.json"), "r", encoding="utf-8") as fh:
                    report = json.load(fh)
                self.assertTrue(report["all_passed"])
                self.assertEqual(report["rows_total"], len(rows))
                with open(os.path.join(evidence_dir, "run.md"), "r", encoding="utf-8") as fh:
                    run_md = fh.read()
                self.assertIn("stub-context", run_md)
                self.assertIn("smoke receipt", run_md.lower())
                self.assertIn("cbse-test-runner@sha256:", run_md)
                self.assertNotIn("stub-registry.example", run_md)
                self.assert_teardown_ordered(stub_dir)
            finally:
                self.cleanup_run_outputs(run_id)


class TestDesignModeStubDryRun(StubDryRunBase):
    """The multi-scenario --design campaign path over the stub cluster."""

    def _expected_rows_by_label(self, plan):
        # A3 and A4 simulate a second adaptive runner round (25 rows);
        # every other scenario computes exactly its initial replications.
        rows = {}
        for scenario in plan["scenarios"]:
            if scenario["label"] in ("A3", "A4"):
                rows[scenario["label"]] = 25
            else:
                rows[scenario["label"]] = scenario["number_of_reps"]
        return rows

    def test_campaign_dry_run(self):
        with tempfile.TemporaryDirectory() as tmp:
            import datetime

            design = load_campaign_design()
            plan = campaign.plan(design)
            psid_by_recipe = {
                recipe["label"]: 5 + index for index, recipe in enumerate(plan["recipes"])
            }
            rows_by_label = self._expected_rows_by_label(plan)
            base = datetime.datetime(2026, 11, 9, 10, 0, 0, tzinfo=datetime.timezone.utc)
            records = []
            rows_by_id = {}
            for index, scenario in enumerate(plan["scenarios"]):
                label = scenario["label"]
                n = rows_by_label[label]
                psid = psid_by_recipe[scenario["recipe"]]
                created = base + datetime.timedelta(seconds=7 * index)
                published = created + datetime.timedelta(seconds=2.5 + 0.1 * index)
                updated = published + datetime.timedelta(seconds=20 + 2 * index)
                record = {
                    "id": index + 1,
                    "project_id": 1,
                    "state": "Finished",
                    "created_at": created.isoformat(),
                    "updated_at": updated.isoformat(),
                    "priority": scenario["priority"],
                    "number_of_reps": scenario["number_of_reps"],
                    "number_of_computed_reps": n,
                    "translation_attempts": 1,
                    "translation_publish_started_at": (
                        published - datetime.timedelta(milliseconds=3)
                    ).isoformat(),
                    "translation_request_published_at": published.isoformat(),
                    "recipe_info": {"parameterset_id": psid},
                    "container_image": "stub-registry.example/cbse-test-runner@sha256:"
                    + format(index, "x").rjust(64, "a"),
                    "confidence_metric": scenario["confidence_metric"],
                    "runner_round": 2 if label in ("A3", "A4") else 1,
                    "round_reps": 5 if label in ("A3", "A4") else n,
                    "round_computed_reps": 5 if label in ("A3", "A4") else n,
                    "evaluation_attempts": 1,
                    "evaluation_publish_started_at": (
                        updated - datetime.timedelta(milliseconds=700)
                    ).isoformat(),
                    "evaluation_request_published_at": (
                        updated - datetime.timedelta(milliseconds=600)
                    ).isoformat(),
                }
                records.append(record)
                rows = []
                for k in range(n):
                    seed = self.derived_seed("campaign-stub:" + label, k)
                    rows.append(
                        self.build_row(
                            seed,
                            psid,
                            scenario["arrival_rate"],
                            scenario["service_rate"],
                            scenario["run_duration"],
                            scenario["seed_policy"],
                        )
                    )
                rows_by_id[str(index + 1)] = rows
            state = {"scenario_rows": records, "rows_by_scenario_id": rows_by_id}

            stub_dir, kubectl = write_stub(tmp)
            with open(os.path.join(stub_dir, "state.json"), "w", encoding="utf-8") as fh:
                json.dump(state, fh)
            env = driver_env(tmp, stub_dir, kubectl)
            proc = run_driver(["--design", CAMPAIGN_DESIGN_JSON], env, timeout=1800)
            self.assertEqual(proc.returncode, 0, proc.stderr + proc.stdout)
            self.assertIn("PADS campaign run", proc.stdout)
            last = proc.stdout.strip().splitlines()[-1]
            run_id = last.split(" ")[3]
            self.assertRegex(run_id, r"^pads-campaign-[0-9]{14}-[0-9a-f]{6}$")
            evidence_dir = os.path.join(RESULTS_EVIDENCE_DIR, run_id)
            try:
                self.assertTrue(os.path.isdir(evidence_dir))
                for name in ("run.md", "design.json", "eds-state.json", "scenario-status.json"):
                    path = os.path.join(evidence_dir, name)
                    self.assertTrue(os.path.isfile(path), name)
                    self.assert_sanitized(path)
                # design.json is the validated design, verbatim.
                with open(os.path.join(evidence_dir, "design.json"), "r", encoding="utf-8") as fh:
                    self.assertEqual(json.load(fh), design)
                # The full EDS state: 8 entries, 8 recipes, 11 published scenarios.
                with open(os.path.join(evidence_dir, "eds-state.json"), "r", encoding="utf-8") as fh:
                    eds = json.load(fh)
                self.assertEqual(len(eds["design"]["entries"]), 8)
                self.assertEqual(len(eds["design"]["recipes"]), 8)
                self.assertEqual(len(eds["design"]["scenarios"]), 11)
                self.assertTrue(all(s["published"] for s in eds["design"]["scenarios"]))
                # All eleven Core DB records, all Finished.
                with open(os.path.join(evidence_dir, "scenario-status.json"), "r", encoding="utf-8") as fh:
                    records_out = json.load(fh)
                self.assertEqual(len(records_out), 11)
                self.assertTrue(all(r["state"] == "Finished" for r in records_out))
                # The ruled per-scenario structure with the exact row counts.
                for scenario in plan["scenarios"]:
                    label = scenario["label"]
                    scenario_dir = os.path.join(evidence_dir, "scenarios", label)
                    self.assertTrue(os.path.isdir(scenario_dir), label)
                    for name in (
                        "result-rows.json",
                        "scenario-status.json",
                        "assertions.txt",
                        "replay-report.json",
                    ):
                        path = os.path.join(scenario_dir, name)
                        self.assertTrue(os.path.isfile(path), label + "/" + name)
                        self.assert_sanitized(path)
                    with open(os.path.join(scenario_dir, "result-rows.json"), "r", encoding="utf-8") as fh:
                        exported = json.load(fh)
                    self.assertEqual(len(exported), rows_by_label[label], label)
                    with open(os.path.join(scenario_dir, "scenario-status.json"), "r", encoding="utf-8") as fh:
                        record = json.load(fh)
                    self.assertEqual(record["recipe_info"]["parameterset_id"], psid_by_recipe[scenario["recipe"]], label)
                    with open(os.path.join(scenario_dir, "replay-report.json"), "r", encoding="utf-8") as fh:
                        report = json.load(fh)
                    self.assertTrue(report["all_passed"], label)
                    self.assertEqual(report["rows_total"], rows_by_label[label], label)
                    with open(os.path.join(scenario_dir, "assertions.txt"), "r", encoding="utf-8") as fh:
                        self.assertIn("PASS", fh.read())
                # The campaign run.md skeleton carries the analysis markers
                # and the scrubbed images (no registry host, no URL).
                with open(os.path.join(evidence_dir, "run.md"), "r", encoding="utf-8") as fh:
                    run_md = fh.read()
                for marker in (
                    "{{P6-ANALYSIS:EXPERIMENT-C}}",
                    "{{P6-ANALYSIS:ILLUSTRATION}}",
                    "{{P6-ANALYSIS:ANALYSIS-ENV}}",
                ):
                    self.assertIn(marker, run_md)
                self.assertIn("stub-context", run_md)
                self.assertIn("pads-campaign-", run_md)
                self.assertNotIn("stub-registry.example", run_md)
                self.assert_teardown_ordered(stub_dir)
            finally:
                self.cleanup_run_outputs(run_id)

    def test_campaign_dry_run_polls_until_terminal_states(self):
        # Regression lock for the settlement defect (live campaigns
        # pads-campaign-20261109091004-f0f630 and
        # pads-campaign-20261109091436-3a21cb): the campaign wait loop was
        # ``until :; do``, whose body never executes, so the driver polled
        # zero times, exported the pre-translation Core DB states (all
        # eleven scenarios Created, translation_attempts 0), and failed
        # the per-scenario generated-runner check within a minute. The stub
        # Core DB here serves the non-terminal Created snapshot to the
        # scenario-state queries and only the terminal Finished rows after
        # the driver has polled again; the export returns the
        # pre-translation rows (empty container_image) until the state
        # queries reach the terminal snapshot. A driver that stops polling
        # before the terminal states exports the Created rows and fails the
        # campaign; only a wait loop that actually executes its body until
        # every scenario is Finished completes it.
        with tempfile.TemporaryDirectory() as tmp:
            import datetime

            design = load_campaign_design()
            plan = campaign.plan(design)
            psid_by_recipe = {
                recipe["label"]: 5 + index for index, recipe in enumerate(plan["recipes"])
            }
            rows_by_label = self._expected_rows_by_label(plan)
            base = datetime.datetime(2026, 11, 9, 10, 0, 0, tzinfo=datetime.timezone.utc)
            records = []
            non_terminal_records = []
            rows_by_id = {}
            for index, scenario in enumerate(plan["scenarios"]):
                label = scenario["label"]
                n = rows_by_label[label]
                psid = psid_by_recipe[scenario["recipe"]]
                created = base + datetime.timedelta(seconds=7 * index)
                published = created + datetime.timedelta(seconds=2.5 + 0.1 * index)
                updated = published + datetime.timedelta(seconds=20 + 2 * index)
                record = {
                    "id": index + 1,
                    "project_id": 1,
                    "state": "Finished",
                    "created_at": created.isoformat(),
                    "updated_at": updated.isoformat(),
                    "priority": scenario["priority"],
                    "number_of_reps": scenario["number_of_reps"],
                    "number_of_computed_reps": n,
                    "translation_attempts": 1,
                    "translation_publish_started_at": (
                        published - datetime.timedelta(milliseconds=3)
                    ).isoformat(),
                    "translation_request_published_at": published.isoformat(),
                    "recipe_info": {"parameterset_id": psid},
                    "container_image": "stub-registry.example/cbse-test-runner@sha256:"
                    + format(index, "x").rjust(64, "a"),
                    "confidence_metric": scenario["confidence_metric"],
                    "runner_round": 2 if label in ("A3", "A4") else 1,
                    "round_reps": 5 if label in ("A3", "A4") else n,
                    "round_computed_reps": 5 if label in ("A3", "A4") else n,
                    "evaluation_attempts": 1,
                    "evaluation_publish_started_at": (
                        updated - datetime.timedelta(milliseconds=700)
                    ).isoformat(),
                    "evaluation_request_published_at": (
                        updated - datetime.timedelta(milliseconds=600)
                    ).isoformat(),
                }
                records.append(record)
                pre_translation = dict(record)
                pre_translation["state"] = "Created"
                pre_translation["translation_attempts"] = 0
                pre_translation["number_of_computed_reps"] = 0
                pre_translation["container_image"] = None
                pre_translation["updated_at"] = created.isoformat()
                non_terminal_records.append(pre_translation)
                rows = []
                for k in range(n):
                    seed = self.derived_seed("campaign-stub:" + label, k)
                    rows.append(
                        self.build_row(
                            seed,
                            psid,
                            scenario["arrival_rate"],
                            scenario["service_rate"],
                            scenario["run_duration"],
                            scenario["seed_policy"],
                        )
                    )
                rows_by_id[str(index + 1)] = rows
            state = {
                "scenario_rows": records,
                "non_terminal_rows": non_terminal_records,
                "state_sequence": [
                    ["Created"] * len(records),
                    ["Finished"] * len(records),
                ],
                "rows_by_scenario_id": rows_by_id,
            }
            stub_dir, kubectl = write_stub(tmp)
            with open(os.path.join(stub_dir, "state.json"), "w", encoding="utf-8") as fh:
                json.dump(state, fh)
            env = driver_env(tmp, stub_dir, kubectl)
            proc = run_driver(["--design", CAMPAIGN_DESIGN_JSON], env, timeout=1800)
            self.assertEqual(proc.returncode, 0, proc.stderr + proc.stdout)
            self.assertIn("PADS campaign run", proc.stdout)
            # The wait loop body must have executed: at least one
            # non-terminal (Created) state response before the terminal
            # Finished response. A loop that never runs its body
            # (``until :; do``) polls zero times and fails on the
            # pre-translation export instead.
            state_queries = [
                call for call in kubectl_calls(stub_dir) if "SELECT s.state" in call
            ]
            self.assertGreaterEqual(
                len(state_queries),
                2,
                "the campaign wait loop must re-poll the scenario states until "
                "every scenario is terminal; a loop that never runs its body "
                "polls zero times (settlement: until :; do)",
            )
            last = proc.stdout.strip().splitlines()[-1]
            run_id = last.split(" ")[3]
            self.assertRegex(run_id, r"^pads-campaign-[0-9]{14}-[0-9a-f]{6}$")
            evidence_dir = os.path.join(RESULTS_EVIDENCE_DIR, run_id)
            try:
                # The exported records are the terminal rows, not the
                # pre-translation Created rows.
                with open(
                    os.path.join(evidence_dir, "scenario-status.json"),
                    "r",
                    encoding="utf-8",
                ) as fh:
                    records_out = json.load(fh)
                self.assertEqual(len(records_out), len(records))
                self.assertTrue(all(r["state"] == "Finished" for r in records_out))
                self.assertTrue(all(r["container_image"] for r in records_out))
            finally:
                self.cleanup_run_outputs(run_id)


if __name__ == "__main__":
    unittest.main()
