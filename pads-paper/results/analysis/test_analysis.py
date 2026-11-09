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

"""Offline self-tests for the P6 paper-result bundle analysis tooling
(P6-R3).

Covers, for ``campaign.py`` (imported; its design-validation contract is
tested in ``pads-paper/acceptance/test_acceptance.py``):

- the plan derivation over the committed campaign design (recipe/scenario
  order, baked parameters, offered-load rho, experiment groups);
- the wall-clock stage durations from synthetic Core DB timestamps
  (exact values, null translation timestamp, out-of-order refusal).

Covers, for ``analyze.py``:

- the PPS-style half-width (Student-t usage) and the recomputed verdict
  (``met`` iff half-width <= epsilon, else ``stop_unmet``);
- the deterministic label -> Core DB mapping, including the shared-recipe
  B scenarios disambiguated by confidence_metric/priority/number_of_reps;
- the per-scenario records and the per-group aggregates over a synthetic
  campaign evidence directory;
- the CSV/JSON emission (column set, row count, values);
- the Experiment-B offline precision illustration (smallest seed-sorted
  prefix per epsilon, the nondecreasing relation, the unmet case);
- the Experiment-C replay over the A rows and the full CLI over synthetic
  evidence built from the canonical model (SimPy-gated, skip cleanly
  without SimPy, mirroring the repository conformance convention).

Covers, for ``plot-figures.py`` (matplotlib-gated, skip cleanly without
matplotlib; proven through a temporary untracked venv in the receipts):

- exactly four SVG figures from the aggregates;
- byte-stability: two renders from identical aggregates are
  byte-identical (fixed svg.hashsalt, no timestamps).

Run with ``python3 -m unittest`` from this directory.
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
RESULTS_ROOT = os.path.normpath(os.path.join(HERE, os.pardir))
PADS_PAPER_ROOT = os.path.normpath(os.path.join(HERE, os.pardir, os.pardir))
SIMULATION_SRC = os.path.normpath(
    os.path.join(PADS_PAPER_ROOT, "simulation-model", "src")
)
PYTHON = sys.executable

ANALYSIS_PY = os.path.join(HERE, "analyze.py")
CAMPAIGN_PY = os.path.join(HERE, "campaign.py")
PLOT_FIGURES_PY = os.path.join(HERE, "plot-figures.py")
DESIGN_JSON = os.path.join(RESULTS_ROOT, "campaign", "design.json")


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


analyze = load_module("pads_results_analyze", ANALYSIS_PY)
campaign = load_module("pads_results_campaign", CAMPAIGN_PY)
plot_figures = load_module("pads_results_plot_figures", PLOT_FIGURES_PY)
assert_results = analyze.load_assert_results()

try:
    import simpy  # noqa: F401

    HAS_SIMPY = True
except ImportError:
    HAS_SIMPY = False

try:
    import matplotlib  # noqa: F401

    HAS_MPL = True
except ImportError:
    HAS_MPL = False


# ---- synthetic campaign (mini design with a shared-recipe B pair) -----------


def mini_design():
    """A four-scenario campaign design: two A scenarios, two B scenarios on
    the shared recipe R2 (the B disambiguation case)."""
    return {
        "schema": "pads-campaign-design-v1",
        "campaign": "pads-test-campaign",
        "model": "erlang_a_v1",
        "entries": {
            "arrival_rate": [4, 8],
            "service_rate": [4],
            "run_duration": [100],
            "seed_policy": [7],
        },
        "recipes": [
            {"label": "R1", "arrival_rate": 4, "service_rate": 4},
            {"label": "R2", "arrival_rate": 8, "service_rate": 4},
        ],
        "scenarios": [
            {"label": "A1", "recipe": "R1", "priority": 1, "number_of_reps": 2, "confidence_metric": 0.1},
            {"label": "A2", "recipe": "R2", "priority": 1, "number_of_reps": 2, "confidence_metric": 0.1},
            {"label": "B1", "recipe": "R2", "priority": 2, "number_of_reps": 2, "confidence_metric": 0.2},
            {"label": "B2", "recipe": "R2", "priority": 2, "number_of_reps": 2, "confidence_metric": 0.05},
        ],
    }


def make_row(seed, psid, arrival, service, duration, seed_policy, served, abandoned, mean_wait, utilization):
    total = served + abandoned
    return {
        "parameterset_id": psid,
        "arrival_rate": arrival,
        "service_rate": service,
        "run_duration": duration,
        "seed_policy": seed_policy,
        "effective_seed": seed,
        "model_id": "erlang_a_v1",
        "completed_customers": served,
        "served_customers": served,
        "abandoned_customers": abandoned,
        "total_customers": total,
        "mean_wait_time": mean_wait,
        "abandonment_probability": abandoned / total if total else 0.0,
        "throughput": served / duration,
        "server_utilization": utilization,
    }


DIGEST = "a" * 64
IMAGE_REF = "stub-registry.example/cbse-test-runner@sha256:{}".format(DIGEST)


def make_record(scenario_id, psid, priority, number_of_reps, confidence_metric,
                computed=None, runner_round=1, round_reps=None,
                round_computed_reps=None):
    """A synthetic Core DB scenario_status record with deterministic
    timestamps: created 10:00:00, translation published 10:00:04, terminal
    10:00:34 (stages 4.0s, 30.0s, total 34.0s)."""
    computed = computed if computed is not None else number_of_reps
    return {
        "id": scenario_id,
        "project_id": 1,
        "state": "Finished",
        "created_at": "2026-11-09T10:00:00+00:00",
        "updated_at": "2026-11-09T10:00:34+00:00",
        "priority": priority,
        "number_of_reps": number_of_reps,
        "number_of_computed_reps": computed,
        "translation_attempts": 1,
        "translation_publish_started_at": "2026-11-09T10:00:03.997000+00:00",
        "translation_request_published_at": "2026-11-09T10:00:04+00:00",
        "recipe_info": {"parameterset_id": psid},
        "container_image": IMAGE_REF,
        "confidence_metric": confidence_metric,
        "runner_round": runner_round,
        "round_reps": round_reps if round_reps is not None else computed,
        "round_computed_reps": round_computed_reps if round_computed_reps is not None else computed,
        "evaluation_attempts": 1,
        "evaluation_publish_started_at": "2026-11-09T10:00:33.300000+00:00",
        "evaluation_request_published_at": "2026-11-09T10:00:33.400000+00:00",
    }


def make_eds_state(entries, recipes, scenarios):
    """entries: [(id, parameter, value)], recipes: [(psid, ar_id, sr_id,
    rd_id, sp_id)], scenarios: [(priority, nr, cm, psid)]."""
    return {
        "design": {
            "entries": [
                {"id": i, "parameter": p, "value": v} for i, p, v in entries
            ],
            "recipes": [
                {
                    "parameterset_id": psid,
                    "arrival_rate_id": ar,
                    "service_rate_id": sr,
                    "run_duration_id": rd,
                    "seed_policy_id": sp,
                }
                for psid, ar, sr, rd, sp in recipes
            ],
            "scenarios": [
                {
                    "index": index,
                    "priority": priority,
                    "number_of_reps": nr,
                    "confidence_metric": cm,
                    "parameterset_id": psid,
                    "published": True,
                }
                for index, (priority, nr, cm, psid) in enumerate(scenarios)
            ],
        },
        "flow": {"saved": True, "started": True, "finalized": False},
        "db_rows": [],
    }


def make_replay_report(rows):
    return {
        "model_id": "erlang_a_v1",
        "simpy_version": "4.1.2",
        "python_version": "{}.{}.{}".format(
            sys.version_info.major, sys.version_info.minor, sys.version_info.micro
        ),
        "rows_total": len(rows),
        "rows_passed": len(rows),
        "all_passed": True,
        "rows": [
            {"index": i, "effective_seed": row["effective_seed"], "passed": True, "mismatches": []}
            for i, row in enumerate(rows)
        ],
    }


RUN_MD_SKELETON = (
    "# PADS initial-results campaign run pads-campaign-stub\n\n"
    "## Experiment C replay (all Experiment-A rows)\n\n"
    + analyze.MARKER_EXPERIMENT_C + "\n\n"
    "## Offline precision illustration (Experiment B)\n\n"
    + analyze.MARKER_ILLUSTRATION + "\n\n"
    "## Analysis and plotting environment\n\n"
    + analyze.MARKER_ANALYSIS_ENV + "\n"
)


def build_evidence(tmp, design, records, rows_by_label, psid_by_recipe, seed_policy=7):
    """Assemble a synthetic campaign evidence directory under ``tmp`` and
    return (evidence_dir, plan)."""
    plan = campaign.plan(design)
    evidence_dir = os.path.join(tmp, "evidence")
    for label in [s["label"] for s in plan["scenarios"]]:
        os.makedirs(os.path.join(evidence_dir, "scenarios", label), exist_ok=True)

    with open(os.path.join(evidence_dir, "design.json"), "w", encoding="utf-8") as fh:
        json.dump(design, fh, indent=2)
        fh.write("\n")
    with open(os.path.join(evidence_dir, "run.md"), "w", encoding="utf-8") as fh:
        fh.write(RUN_MD_SKELETON)
    with open(os.path.join(evidence_dir, "scenario-status.json"), "w", encoding="utf-8") as fh:
        json.dump(records, fh, indent=2)
        fh.write("\n")

    # EDS state: one entry id per (parameter, value), psid 5+i per recipe.
    entries = []
    entry_ids = {}
    next_id = 5
    for parameter in ("arrival_rate", "service_rate", "run_duration", "seed_policy"):
        for value in plan["entries"][parameter]:
            entry_ids[(parameter, value)] = next_id
            entries.append((next_id, parameter, value))
            next_id += 1
    recipes = []
    next_psid = 5
    for recipe in plan["recipes"]:
        recipes.append(
            (
                next_psid,
                entry_ids[("arrival_rate", recipe["arrival_rate"])],
                entry_ids[("service_rate", recipe["service_rate"])],
                entry_ids[("run_duration", plan["entries"]["run_duration"][0])],
                entry_ids[("seed_policy", plan["entries"]["seed_policy"][0])],
            )
        )
        next_psid += 1
    scenarios = []
    for scenario in plan["scenarios"]:
        scenarios.append(
            (
                scenario["priority"],
                scenario["number_of_reps"],
                scenario["confidence_metric"],
                psid_by_recipe[scenario["recipe"]],
            )
        )
    state = make_eds_state(entries, recipes, scenarios)
    # db_rows: the recipe view projection.
    for recipe, (psid, _ar, _sr, _rd, _sp) in zip(plan["recipes"], recipes):
        state["db_rows"].append(
            {
                "parameterset_id": psid,
                "arrival_rate": recipe["arrival_rate"],
                "service_rate": recipe["service_rate"],
                "run_duration": plan["entries"]["run_duration"][0],
                "seed_policy": plan["entries"]["seed_policy"][0],
            }
        )
    with open(os.path.join(evidence_dir, "eds-state.json"), "w", encoding="utf-8") as fh:
        json.dump(state, fh, indent=2)
        fh.write("\n")

    for scenario in plan["scenarios"]:
        label = scenario["label"]
        rows = rows_by_label[label]
        scenario_dir = os.path.join(evidence_dir, "scenarios", label)
        with open(os.path.join(scenario_dir, "result-rows.json"), "w", encoding="utf-8") as fh:
            json.dump(rows, fh, indent=2)
            fh.write("\n")
        with open(os.path.join(scenario_dir, "replay-report.json"), "w", encoding="utf-8") as fh:
            json.dump(make_replay_report(rows), fh, indent=2)
            fh.write("\n")
        with open(os.path.join(scenario_dir, "assertions.txt"), "w", encoding="utf-8") as fh:
            fh.write("assert-results: PASS - {} rows\n".format(len(rows)))
    return evidence_dir, plan


# ---- plan derivation ---------------------------------------------------------


class TestPlanDerivation(unittest.TestCase):
    def test_committed_campaign_plan_matches_p6_r1(self):
        with open(DESIGN_JSON, "r", encoding="utf-8") as fh:
            design = json.load(fh)
        self.assertEqual(campaign.validate_design(design), [])
        plan = campaign.plan(design)
        self.assertEqual(plan["entries"], {
            "arrival_rate": [4, 6, 8, 10],
            "service_rate": [4, 5],
            "run_duration": [500],
            "seed_policy": [2027],
        })
        self.assertEqual(len(plan["recipes"]), 8)
        self.assertEqual([r["label"] for r in plan["recipes"]],
                         ["R1", "R2", "R3", "R4", "R5", "R6", "R7", "R8"])
        expected_rho = [0.50, 0.75, 1.00, 1.25, 0.40, 0.60, 0.80, 1.00]
        self.assertEqual(
            [round(r["rho"], 10) for r in plan["recipes"]],
            [round(x, 10) for x in expected_rho],
        )
        scenarios = plan["scenarios"]
        self.assertEqual(len(scenarios), 11)
        self.assertEqual([s["label"] for s in scenarios],
                         ["A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8",
                          "B1", "B2", "B3"])
        for scenario in scenarios[:8]:
            self.assertEqual(scenario["priority"], 1)
            self.assertEqual(scenario["number_of_reps"], 20)
            self.assertEqual(scenario["confidence_metric"], 0.05)
            self.assertEqual(scenario["group"], "A")
            self.assertEqual(scenario["run_duration"], 500)
            self.assertEqual(scenario["seed_policy"], 2027)
        for scenario in scenarios[8:]:
            self.assertEqual(scenario["priority"], 2)
            self.assertEqual(scenario["number_of_reps"], 10)
            self.assertEqual(scenario["recipe"], "R3")
            self.assertEqual(scenario["arrival_rate"], 8)
            self.assertEqual(scenario["service_rate"], 4)
            self.assertEqual(scenario["rho"], 1.0)
            self.assertEqual(scenario["group"], "B")
        self.assertEqual([s["confidence_metric"] for s in scenarios[8:]], [0.2, 0.1, 0.05])

    def test_offered_load_formula(self):
        self.assertEqual(campaign.offered_load(4, 4), 0.5)
        self.assertEqual(campaign.offered_load(10, 5), 1.0)
        self.assertAlmostEqual(campaign.offered_load(6, 5), 0.6, places=12)


# ---- stage durations ---------------------------------------------------------


class TestStageDurations(unittest.TestCase):
    def test_exact_stage_values(self):
        record = make_record(1, 5, 1, 5, 0.05)
        stages = campaign.stage_durations(record)
        self.assertAlmostEqual(stages["created_to_translation_published_s"], 4.0, places=9)
        self.assertAlmostEqual(stages["translation_published_to_terminal_s"], 30.0, places=9)
        self.assertAlmostEqual(stages["total_s"], 34.0, places=9)

    def test_null_translation_timestamp(self):
        record = make_record(1, 5, 1, 5, 0.05)
        record["translation_request_published_at"] = None
        stages = campaign.stage_durations(record)
        self.assertIsNone(stages["created_to_translation_published_s"])
        self.assertIsNone(stages["translation_published_to_terminal_s"])
        self.assertAlmostEqual(stages["total_s"], 34.0, places=9)

    def test_out_of_order_timestamps_are_refused(self):
        record = make_record(1, 5, 1, 5, 0.05)
        record["updated_at"] = "2026-11-09T09:59:59+00:00"
        with self.assertRaises(campaign.DesignError):
            campaign.stage_durations(record)

    def test_malformed_timestamp_is_refused(self):
        record = make_record(1, 5, 1, 5, 0.05)
        record["created_at"] = "not-a-timestamp"
        with self.assertRaises(campaign.DesignError):
            campaign.stage_durations(record)


# ---- statistics and verdict ---------------------------------------------------


class TestStatistics(unittest.TestCase):
    def test_half_width_matches_pps_formula(self):
        # obs [2.0, 2.2, 1.8]: mean 2.0, s = 0.2, df = 2,
        # h = t(0.975, 2) * 0.2 / sqrt(3).
        got = analyze.half_width([2.0, 2.2, 1.8], assert_results)
        expected = 4.302652729749464 * 0.2 / math.sqrt(3.0)
        self.assertAlmostEqual(got, expected, delta=expected * 1e-7)

    def test_half_width_degenerate_sample(self):
        self.assertEqual(analyze.half_width([1.5], assert_results), 0.0)

    def test_t_quantile_is_the_shared_pps_implementation(self):
        self.assertAlmostEqual(
            assert_results.student_t_quantile(0.975, 1),
            12.706204736174703,
            delta=1e-7,
        )


# ---- synthetic campaign fixture -----------------------------------------------


class CampaignFixture(unittest.TestCase):
    """Builds the synthetic mini-campaign evidence once per test."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.tmp = self._tmp.name
        self.design = mini_design()
        self.plan = campaign.plan(self.design)
        # psid 5 -> R1, 6 -> R2 (EDS floor-5 assignment over a fresh DB).
        self.psid_by_recipe = {"R1": 5, "R2": 6}
        # Deterministic synthetic rows (no SimPy required): two rows each.
        self.rows = {
            "A1": [
                make_row(101, 5, 4, 4, 100, 7, 30, 4, 0.10, 0.30),
                make_row(102, 5, 4, 4, 100, 7, 31, 5, 0.12, 0.31),
            ],
            "A2": [
                make_row(201, 6, 8, 4, 100, 7, 30, 10, 0.40, 0.60),
                make_row(202, 6, 8, 4, 100, 7, 29, 11, 0.42, 0.61),
            ],
            "B1": [
                make_row(301, 6, 8, 4, 100, 7, 30, 10, 0.41, 0.60),
                make_row(302, 6, 8, 4, 100, 7, 30, 10, 0.43, 0.61),
            ],
            "B2": [
                make_row(401, 6, 8, 4, 100, 7, 29, 12, 0.44, 0.62),
                make_row(402, 6, 8, 4, 100, 7, 31, 8, 0.46, 0.60),
            ],
        }
        self.records = [
            make_record(1, 5, 1, 2, 0.1),
            make_record(2, 6, 1, 2, 0.1),
            make_record(3, 6, 2, 2, 0.2),
            make_record(4, 6, 2, 2, 0.05),
        ]
        self.evidence_dir, self.plan = build_evidence(
            self.tmp, self.design, self.records, self.rows, self.psid_by_recipe
        )

    def tearDown(self):
        self._tmp.cleanup()

    def analysis(self):
        return analyze.analyze_evidence(self.design, self.evidence_dir)


# ---- label mapping -------------------------------------------------------------


class TestLabelMapping(CampaignFixture):
    def test_mapping_includes_the_shared_recipe_b_pair(self):
        evidence = analyze.load_evidence(self.evidence_dir, self.design)
        mapping = analyze.map_scenarios(evidence)
        self.assertEqual(
            [(p["label"], record["id"], record["recipe_info"]["parameterset_id"])
             for p, record, _eds in mapping],
            [("A1", 1, 5), ("A2", 2, 6), ("B1", 3, 6), ("B2", 4, 6)],
        )
        # The shared-records B1/B2 pair is disambiguated by confidence metric.
        b1 = next(record for p, record, _ in mapping if p["label"] == "B1")
        b2 = next(record for p, record, _ in mapping if p["label"] == "B2")
        self.assertEqual(b1["confidence_metric"], 0.2)
        self.assertEqual(b2["confidence_metric"], 0.05)

    def test_record_count_mismatch_is_refused(self):
        evidence = analyze.load_evidence(self.evidence_dir, self.design)
        evidence["records"] = evidence["records"][:3]
        with self.assertRaises(analyze.AnalysisError):
            analyze.map_scenarios(evidence)

    def test_eds_settings_order_mismatch_is_refused(self):
        # B1 (epsilon 0.2) and B2 (epsilon 0.05) share the recipe R2
        # parameterset; swapping their EDS state settings breaks the
        # creation-order contract and must be refused.
        path = os.path.join(self.evidence_dir, "eds-state.json")
        with open(path, "r", encoding="utf-8") as fh:
            state = json.load(fh)
        scenarios = state["design"]["scenarios"]
        scenarios[2], scenarios[3] = scenarios[3], scenarios[2]
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(state, fh)
        with self.assertRaises(analyze.AnalysisError):
            analyze.analyze_evidence(self.design, self.evidence_dir)

    def test_silent_parameterset_swap_is_caught_by_the_design_check(self):
        # A1 and A2 carry identical scenario settings, so swapping their EDS
        # parameterset ids is invisible to the settings cross-check; the
        # baked parameterset_id of the exported rows must expose it through
        # the recomputed assertions.
        path = os.path.join(self.evidence_dir, "eds-state.json")
        with open(path, "r", encoding="utf-8") as fh:
            state = json.load(fh)
        scenarios = state["design"]["scenarios"]
        scenarios[0], scenarios[1] = scenarios[1], scenarios[0]
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(state, fh)
        with self.assertRaises(analyze.AnalysisError):
            analyze.analyze_evidence(self.design, self.evidence_dir)

    def test_unpublished_eds_scenario_is_refused(self):
        path = os.path.join(self.evidence_dir, "eds-state.json")
        with open(path, "r", encoding="utf-8") as fh:
            state = json.load(fh)
        state["design"]["scenarios"][0]["published"] = False
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(state, fh)
        with self.assertRaises(analyze.AnalysisError):
            analyze.analyze_evidence(self.design, self.evidence_dir)


# ---- per-scenario analysis ------------------------------------------------------


class TestAnalyzeEvidence(CampaignFixture):
    def test_per_scenario_records(self):
        analysis = self.analysis()
        by_label = {r["label"]: r for r in analysis["scenarios"]}
        a1 = by_label["A1"]
        self.assertEqual(a1["scenario_id"], 1)
        self.assertEqual(a1["parameterset_id"], 5)
        self.assertEqual(a1["rho"], 0.5)
        self.assertEqual(a1["n"], 2)
        self.assertAlmostEqual(a1["mean_wait_time"], 0.11, places=12)
        # Two obs [0.10, 0.12]: two-pass s = 0.014142..., h = t(0.975, 1) * s / sqrt(2)
        # = 12.706204736174703 * 0.01 = 0.12706...
        self.assertAlmostEqual(
            a1["mean_wait_time_half_width_95"], 12.706204736174703 * 0.01, delta=2e-9
        )
        # epsilon 0.1 < half-width 0.12706 -> terminal scenario recorded stop_unmet.
        self.assertEqual(a1["verdict"], "stop_unmet")
        self.assertAlmostEqual(
            a1["abandonment_probability_mean"], 0.12826797385620915, places=12
        )
        self.assertAlmostEqual(a1["throughput_mean"], 0.305, places=12)
        self.assertAlmostEqual(a1["server_utilization_mean"], 0.305, places=12)
        self.assertEqual(a1["runner_round"], 1)
        self.assertAlmostEqual(a1["stage_created_to_translation_published_s"], 4.0, places=9)
        self.assertAlmostEqual(a1["stage_translation_published_to_terminal_s"], 30.0, places=9)
        self.assertAlmostEqual(a1["stage_total_s"], 34.0, places=9)
        self.assertTrue(a1["assertions_passed"])
        self.assertTrue(a1["replay_all_passed"])

    def test_met_verdict_when_half_width_within_epsilon(self):
        # Widen the tolerance so the half-width fits: verdict flips to met.
        self.design["scenarios"][0]["confidence_metric"] = 5.0
        self.records[0]["confidence_metric"] = 5.0
        self.evidence_dir, self.plan = build_evidence(
            self.tmp, self.design, self.records, self.rows, self.psid_by_recipe
        )
        analysis = self.analysis()
        self.assertEqual(analysis["scenarios"][0]["verdict"], "met")

    def test_aggregates_per_group(self):
        analysis = self.analysis()
        self.assertEqual(analysis["aggregates"]["A"]["scenarios"], 2)
        self.assertEqual(analysis["aggregates"]["A"]["total_replications"], 4)
        self.assertEqual(analysis["aggregates"]["A"]["verdicts"], {"stop_unmet": 2})
        self.assertEqual(analysis["aggregates"]["B"]["scenarios"], 2)
        self.assertEqual(analysis["aggregates"]["B"]["total_result_rows"], 4)
        self.assertAlmostEqual(
            analysis["aggregates"]["A"]["sum_stage_total_s"], 68.0, places=9
        )
        self.assertTrue(analysis["aggregates"]["A"]["all_replays_passed"])


# ---- CSV / JSON emission --------------------------------------------------------


class TestOutputEmission(CampaignFixture):
    def test_csv_and_json_outputs(self):
        analysis = self.analysis()
        with tempfile.TemporaryDirectory() as tmp:
            json_path = analyze.write_json(tmp, analysis)
            csv_path = analyze.write_csv(tmp, analysis)
            with open(json_path, "r", encoding="utf-8") as fh:
                on_disk = json.load(fh)
            self.assertEqual(on_disk["scenarios"], analysis["scenarios"])
            self.assertEqual(
                on_disk["design_summary"]["entries"],
                campaign.plan(self.design)["entries"],
            )
            with open(csv_path, "r", encoding="utf-8") as fh:
                lines = fh.read().splitlines()
            self.assertEqual(len(lines), 1 + 4)
            header = lines[0].split(",")
            self.assertEqual(header, list(analyze.CSV_COLUMNS))
            self.assertEqual(len(header), 29)
            a1 = lines[1].split(",")
            self.assertEqual(a1[0], "A1")
            self.assertEqual(a1[2], "1")  # scenario_id
            self.assertEqual(a1[13], "2")  # n
            self.assertEqual(a1[16], "stop_unmet")  # verdict
            self.assertEqual(a1[24], "4.0")  # stage created->published
            self.assertEqual(a1[25], "30.0")  # stage published->terminal
            self.assertEqual(a1[26], "34.0")  # stage total

    def test_missing_output_is_reported(self):
        analysis = self.analysis()
        with tempfile.TemporaryDirectory() as tmp:
            analyze.write_json(tmp, analysis)
            with self.assertRaises(analyze.AnalysisError):
                analyze.check_outputs(tmp, analysis)


# ---- Experiment-B illustration ---------------------------------------------------


class TestIllustration(CampaignFixture):
    def test_prefixes_and_relation(self):
        analysis = self.analysis()
        illustration = analysis["illustration"]
        # B1 and B2 both carry two rows; the tie keeps the design order (B1).
        self.assertEqual(illustration["scenario_label"], "B1")
        self.assertEqual(illustration["rows"], 2)
        self.assertEqual(
            set(illustration["epsilon_prefix"]), {"0.20", "0.10", "0.05"}
        )
        # Two obs [0.41, 0.43]: s/2 -> h = 12.706204736174703 * 0.01
        # = 0.12706... <= 0.2 but > 0.05.
        self.assertEqual(illustration["epsilon_prefix"]["0.20"], 2)
        self.assertEqual(illustration["epsilon_prefix"]["0.10"], None)
        self.assertEqual(illustration["epsilon_prefix"]["0.05"], None)
        self.assertTrue(illustration["nondecreasing_relation_holds"])
        self.assertIn("effective_seed ascending", illustration["order"])
        self.assertIn("not the live", illustration["caveat"])

    def test_unmet_prefix_is_reported_as_unmet(self):
        original = analyze.ILLUSTRATION_EPSILONS
        # A tolerance no prefix can satisfy: the smallest prefix is unmet
        # (reported as None, rendered as "unmet"), and the nondecreasing
        # relation still holds (unmet sorts after every satisfied k).
        analyze.ILLUSTRATION_EPSILONS = (0.20, 0.10, 1e-9)
        try:
            analysis = self.analysis()
            illustration = analysis["illustration"]
            self.assertIsNone(illustration["epsilon_prefix"]["0.00"])
            self.assertTrue(illustration["nondecreasing_relation_holds"])
        finally:
            analyze.ILLUSTRATION_EPSILONS = original

    def test_rendered_illustration_block(self):
        analysis = self.analysis()
        block = analyze.render_illustration_block(analysis["illustration"])
        self.assertIn("B1", block)
        self.assertIn("k=2", block)
        self.assertIn("unmet", block)
        self.assertIn("holds", block)


# ---- Experiment C (SimPy-gated) ---------------------------------------------------


@unittest.skipUnless(
    HAS_SIMPY,
    "SimPy is not installed; the Experiment-C self-tests skip cleanly, "
    "mirroring the repository conformance convention (P5-R1)",
)
class TestExperimentC(CampaignFixture):
    def setUp(self):
        super().setUp()
        model = load_module("pads_paper_canonical_model_c", os.path.join(SIMULATION_SRC, "model.py"))
        result = load_module("pads_paper_canonical_result_c", os.path.join(SIMULATION_SRC, "result.py"))
        # Rebuild the A rows from the canonical model so the replay matches.
        rebuilt = {}
        for label in ("A1", "A2"):
            rows = self.rows[label]
            rebuilt[label] = []
            for row in rows:
                rng = random.Random(row["effective_seed"])
                metrics = model.run(
                    row["arrival_rate"], row["service_rate"], row["run_duration"], rng
                )
                rebuilt[label].append(
                    result.build_result(
                        {
                            "parameterset_id": row["parameterset_id"],
                            "arrival_rate": row["arrival_rate"],
                            "service_rate": row["service_rate"],
                            "run_duration": row["run_duration"],
                            "seed_policy": row["seed_policy"],
                        },
                        metrics,
                        row["effective_seed"],
                    )
                )
        self.rows.update(rebuilt)
        self.evidence_dir, self.plan = build_evidence(
            self.tmp, self.design, self.records, self.rows, self.psid_by_recipe
        )

    def test_replay_passes_and_writes_report(self):
        analysis = self.analysis()
        report = analyze.run_experiment_c(
            self.evidence_dir, analyze.experiment_c_rows(analysis, self.evidence_dir)
        )
        self.assertTrue(report["all_passed"])
        self.assertEqual(report["rows_total"], 4)
        self.assertEqual(report["simpy_version"], "4.1.2")
        self.assertTrue(
            os.path.isfile(os.path.join(self.evidence_dir, "experiment-c-replay-report.json"))
        )

    def test_tampered_row_fails_the_replay(self):
        analysis = self.analysis()
        rows = analyze.experiment_c_rows(analysis, self.evidence_dir)
        rows[0]["mean_wait_time"] += 1e-6
        with self.assertRaises(analyze.AnalysisError):
            analyze.run_experiment_c(self.evidence_dir, rows)


# ---- full CLI (SimPy-gated) -------------------------------------------------------


@unittest.skipUnless(
    HAS_SIMPY,
    "SimPy is not installed; the full-CLI self-tests skip cleanly, "
    "mirroring the repository conformance convention (P5-R1)",
)
class TestAnalyzeCLI(CampaignFixture):
    def setUp(self):
        super().setUp()
        model = load_module("pads_paper_canonical_model_cli", os.path.join(SIMULATION_SRC, "model.py"))
        result = load_module("pads_paper_canonical_result_cli", os.path.join(SIMULATION_SRC, "result.py"))
        rebuilt = {}
        for label in ("A1", "A2"):
            rebuilt[label] = []
            for row in self.rows[label]:
                rng = random.Random(row["effective_seed"])
                metrics = model.run(
                    row["arrival_rate"], row["service_rate"], row["run_duration"], rng
                )
                rebuilt[label].append(
                    result.build_result(
                        {
                            "parameterset_id": row["parameterset_id"],
                            "arrival_rate": row["arrival_rate"],
                            "service_rate": row["service_rate"],
                            "run_duration": row["run_duration"],
                            "seed_policy": row["seed_policy"],
                        },
                        metrics,
                        row["effective_seed"],
                    )
                )
        self.rows.update(rebuilt)
        self.evidence_dir, self.plan = build_evidence(
            self.tmp, self.design, self.records, self.rows, self.psid_by_recipe
        )

    def test_full_run_completes_the_run_md_and_writes_outputs(self):
        proc = subprocess.run(
            [PYTHON, ANALYSIS_PY, "--evidence", self.evidence_dir],
            capture_output=True,
            text=True,
            timeout=600,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("analyze: OK", proc.stdout)
        with open(os.path.join(self.evidence_dir, "analysis.json"), "r", encoding="utf-8") as fh:
            analysis = json.load(fh)
        self.assertEqual(analysis["experiment_c"]["rows_total"], 4)
        self.assertTrue(analysis["experiment_c"]["all_passed"])
        self.assertEqual(len(analysis["scenarios"]), 4)
        with open(os.path.join(self.evidence_dir, "analysis.csv"), "r", encoding="utf-8") as fh:
            self.assertEqual(len(fh.read().splitlines()), 5)
        with open(os.path.join(self.evidence_dir, "run.md"), "r", encoding="utf-8") as fh:
            run_md = fh.read()
        for marker in (
            analyze.MARKER_EXPERIMENT_C,
            analyze.MARKER_ILLUSTRATION,
            analyze.MARKER_ANALYSIS_ENV,
        ):
            self.assertNotIn(marker, run_md)
        self.assertIn("4 of 4 rows replayed exactly", run_md)

    def test_inconsistent_evidence_exits_nonzero(self):
        # Corrupt one B record's settings so the mapping fails.
        path = os.path.join(self.evidence_dir, "scenario-status.json")
        with open(path, "r", encoding="utf-8") as fh:
            records = json.load(fh)
        records[3]["confidence_metric"] = 0.99
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(records, fh)
        proc = subprocess.run(
            [PYTHON, ANALYSIS_PY, "--evidence", self.evidence_dir],
            capture_output=True,
            text=True,
            timeout=300,
        )
        self.assertEqual(proc.returncode, 1)
        self.assertIn("analyze:", proc.stderr)

    def test_missing_design_exits_two(self):
        proc = subprocess.run(
            [PYTHON, ANALYSIS_PY, "--evidence", self.tmp, "--design", os.path.join(self.tmp, "nope.json")],
            capture_output=True,
            text=True,
            timeout=120,
        )
        self.assertEqual(proc.returncode, 2)


# ---- plot-figures (matplotlib-gated) ----------------------------------------------


def synthetic_analysis_for_plot():
    """A minimal analysis.json-compatible payload with four scenarios."""
    base = {
        "label": "A1", "group": "A", "recipe_label": "R1", "scenario_id": 1,
        "parameterset_id": 5, "arrival_rate": 4, "service_rate": 4,
        "run_duration": 100, "seed_policy": 7, "rho": 0.5, "priority": 1,
        "number_of_reps_initial": 2, "confidence_metric": 0.5, "n": 10,
        "mean_wait_time": 0.1, "mean_wait_time_half_width_95": 0.01, "verdict": "met",
        "abandonment_probability_mean": 0.12,
        "abandonment_probability_half_width_95": 0.004,
        "throughput_mean": 3.9, "server_utilization_mean": 0.45,
        "runner_round": 2, "round_reps": 3, "round_computed_reps": 3,
        "stage_created_to_translation_published_s": 4.0,
        "stage_translation_published_to_terminal_s": 30.0,
        "stage_total_s": 34.0, "assertions_passed": True,
        "replay_all_passed": True,
    }
    records = [
        dict(base),
        dict(base, label="A2", scenario_id=2, parameterset_id=6, rho=1.0,
             arrival_rate=8, mean_wait_time=0.4, mean_wait_time_half_width_95=0.02, n=12,
             runner_round=1, throughput_mean=3.7, server_utilization_mean=0.6),
        dict(base, label="B1", group="B", scenario_id=3, parameterset_id=6,
             rho=1.0, arrival_rate=8, confidence_metric=0.2,
             mean_wait_time=0.41, mean_wait_time_half_width_95=0.02, n=10,
             abandonment_probability_mean=0.13,
             abandonment_probability_mean_wait_time_half_width_95=0.005),
        dict(base, label="B2", group="B", scenario_id=4, parameterset_id=6,
             rho=1.0, arrival_rate=8, confidence_metric=0.05,
             mean_wait_time=0.42, mean_wait_time_half_width_95=0.015, n=14,
             abandonment_probability_mean=0.14,
             abandonment_probability_mean_wait_time_half_width_95=0.004),
    ]
    return {
        "campaign": "pads-test-campaign",
        "model": "erlang_a_v1",
        "design_summary": {"entries": {}, "recipes": 2, "scenarios": 4},
        "scenarios": records,
        "aggregates": {},
        "illustration": {},
    }


@unittest.skipUnless(
    HAS_MPL,
    "matplotlib is not installed; the plot-figures self-tests skip cleanly "
    "(proven through a temporary untracked venv per P6-R3)",
)
class TestPlotFigures(unittest.TestCase):
    def test_exactly_four_figures_and_byte_stability(self):
        with tempfile.TemporaryDirectory() as tmp:
            analysis_path = os.path.join(tmp, "analysis.json")
            with open(analysis_path, "w", encoding="utf-8") as fh:
                json.dump(synthetic_analysis_for_plot(), fh, indent=2)
            out_a = os.path.join(tmp, "figures-a")
            out_b = os.path.join(tmp, "figures-b")
            for out in (out_a, out_b):
                proc = subprocess.run(
                    [PYTHON, PLOT_FIGURES_PY, "--analysis", analysis_path, "--out", out],
                    capture_output=True,
                    text=True,
                    timeout=600,
                )
                self.assertEqual(proc.returncode, 0, proc.stderr)
            files = sorted(os.listdir(out_a))
            self.assertEqual(files, sorted(plot_figures.FIGURE_FILES))
            expected = (
                "f1-mean-wait-vs-rho.svg",
                "f2-abandonment-vs-rho.svg",
                "f3-throughput-utilization-vs-rho.svg",
                "f4-reps-rounds-per-scenario.svg",
            )
            self.assertEqual(files, list(expected))
            for name in expected:
                with open(os.path.join(out_a, name), "rb") as fh:
                    bytes_a = fh.read()
                with open(os.path.join(out_b, name), "rb") as fh:
                    bytes_b = fh.read()
                self.assertTrue(bytes_a.startswith(b"<?xml"), name)
                self.assertGreater(len(bytes_a), 500, name)
                self.assertEqual(bytes_a, bytes_b, "SVG must be byte-stable: " + name)

    def test_malformed_analysis_exits_two(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "analysis.json")
            with open(path, "w", encoding="utf-8") as fh:
                json.dump({"scenarios": []}, fh)
            proc = subprocess.run(
                [PYTHON, PLOT_FIGURES_PY, "--analysis", path],
                capture_output=True,
                text=True,
                timeout=300,
            )
            self.assertEqual(proc.returncode, 2)


if __name__ == "__main__":
    unittest.main()
