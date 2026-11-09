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

"""Offline analysis of the PADS initial-results campaign evidence (P6-R3).

Consumes the sanitized campaign evidence directory produced by
``run-experiment.sh --design`` (``pads-paper/results/evidence/pads-campaign-
<run-id>/``) together with the campaign design (``design.json``), and emits:

- per-scenario records: label, lambda, mu, rho, final ``n`` (= the computed
  replications, i.e. the exported row count), the ``mean_wait_time`` sample
  mean, the Student-t 95% half-width, the recomputed verdict (``met`` iff
  half-width <= epsilon; ``stop_unmet`` recorded as-is for a terminal
  scenario that stopped without meeting its target), the
  ``abandonment_probability`` mean with a Student-t 95% interval (the
  documented interval choice: the same two-pass sample standard deviation
  and ``h = t(0.975, n-1) * s / sqrt(n)`` half-width the PPS uses for
  ``mean_wait_time``, applied across the independent replication
  probabilities), the mean throughput, the mean server utilization, the
  runner rounds (``runner_round``, ``round_reps``,
  ``round_computed_reps``), and the wall-clock stage durations derived from
  the Core DB timestamps (created -> translation request published ->
  terminal updated);
- campaign aggregates: totals per experiment group (A, B);
- the Experiment-C offline replay over every Experiment-A row (delegated to
  ``replay-seeds.py`` with the analysis interpreter; SimPy 4.1.2 required);
- the Experiment-B offline common-sequence precision illustration: from the
  longest recorded B row set, in the deterministic seed-sorted order
  (recorded ``effective_seed`` ascending), the smallest prefix whose sample
  half-width satisfies each epsilon in {0.20, 0.10, 0.05}, with the
  nondecreasing prefix relation reported and the order caveat documented;
- CSV and JSON outputs into the evidence directory (``analysis.csv``,
  ``analysis.json``), the replay report
  (``experiment-c-replay-report.json``), and the completion of the campaign
  ``run.md`` skeleton (the ``{{P6-ANALYSIS:...}}`` markers are replaced).

The analysis runs offline over committed sanitized evidence only: no
cluster, registry, or network access. The statistical helpers (Student-t
quantile, two-pass sample mean/std) are the shared P5 contract in
``pads-paper/acceptance/assert-results.py`` (loaded through importlib), so
the paper-exact half-width has one implementation; the design plan and
stage durations come from ``campaign.py`` in this directory.

Deterministic label mapping (P6-R1): scenario creation order plus the
recipe/parameterset mapping from the exported EDS state. The final EDS
``/api/state`` lists the scenarios in creation order with their
``parameterset_id``; the Core DB ``scenario_status`` records carry
``recipe_info.parameterset_id`` plus the scenario settings, so each design
scenario maps to the (settings-unique) Core DB record; within a shared
parameterset (the B scenarios on recipe R3) the match is disambiguated by
``confidence_metric``/``priority``/``number_of_reps``, consuming records in
``id`` order.

CLI:

    python3 analyze.py --evidence EVIDENCE_DIR [--design DESIGN_JSON]

``--design`` defaults to ``<EVIDENCE_DIR>/design.json``. The per-scenario
assertion outcome is recomputed by this tool over the exported rows (the
live per-scenario ``assertions.txt`` in the evidence stays as the driver's
receipt); the per-scenario replay outcome is read from the driver's
per-scenario ``replay-report.json``.

Exit codes:

    0  analysis, Experiment-C replay, illustration, and run.md completion
       succeeded; all scenario assertions and live replays passed.
    1  the evidence is inconsistent with the design, or a recomputed
       scenario assertion failed, or a recorded replay failed.
    2  usage or input-file error.
    3  the SimPy 4.1.2 replay contract is not met by the analysis
       interpreter.
"""

import argparse
import csv
import importlib.util
import json
import math
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
PADS_PAPER_ROOT = os.path.normpath(os.path.join(HERE, os.pardir, os.pardir))
ASSERT_RESULTS_PY = os.path.join(PADS_PAPER_ROOT, "acceptance", "assert-results.py")
REPLAY_SEEDS_PY = os.path.join(PADS_PAPER_ROOT, "acceptance", "replay-seeds.py")
CAMPAIGN_PY = os.path.join(HERE, "campaign.py")

#: The run.md skeleton markers completed by this tool (P6-R4).
MARKER_EXPERIMENT_C = "{{P6-ANALYSIS:EXPERIMENT-C}}"
MARKER_ILLUSTRATION = "{{P6-ANALYSIS:ILLUSTRATION}}"
MARKER_ANALYSIS_ENV = "{{P6-ANALYSIS:ANALYSIS-ENV}}"

#: The Experiment-B illustration epsilons (the B scenario precision targets).
ILLUSTRATION_EPSILONS = (0.20, 0.10, 0.05)


class AnalysisError(Exception):
    """The evidence is inconsistent with the design or a check failed."""


class ReplayContractError(Exception):
    """The SimPy 4.1.2 replay contract is not met (replay-seeds exit 3)."""


def _load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise ImportError("cannot load module from {}".format(path))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def load_assert_results():
    """Load the shared P5 row-contract/statistics module."""
    return _load_module("pads_results_assert_results", ASSERT_RESULTS_PY)


def load_campaign():
    """Load the campaign design module (sibling, same directory)."""
    return _load_module("pads_results_campaign", CAMPAIGN_PY)


def _read_json(path, what):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        raise AnalysisError("cannot read {} {}: {}".format(what, path, exc))


# ---- evidence structure and label mapping -----------------------------------


def load_evidence(evidence_dir, design):
    """Validate the evidence structure and return the raw inputs.

    Returns a dict with the design, the EDS state, the Core DB records, and
    the per-label result rows and replay reports. Raises ``AnalysisError``
    on any structural inconsistency.
    """
    campaign = load_campaign()
    planned = campaign.plan(design)

    eds_state = _read_json(
        os.path.join(evidence_dir, "eds-state.json"), "EDS state"
    )
    records = _read_json(
        os.path.join(evidence_dir, "scenario-status.json"), "Core DB records"
    )
    if not isinstance(records, list):
        raise AnalysisError("scenario-status.json must be a JSON array")

    planned_scenarios = planned["scenarios"]
    if len(records) != len(planned_scenarios):
        raise AnalysisError(
            "expected {} Core DB scenario records, found {}".format(
                len(planned_scenarios), len(records)
            )
        )
    for record in records:
        if not isinstance(record, dict) or record.get("state") != "Finished":
            raise AnalysisError(
                "every Core DB scenario record must be state Finished; got {!r}".format(
                    record.get("state") if isinstance(record, dict) else record
                )
            )

    eds_scenarios = (
        eds_state.get("design", {}).get("scenarios")
        if isinstance(eds_state, dict)
        else None
    )
    if not isinstance(eds_scenarios, list) or len(eds_scenarios) != len(
        planned_scenarios
    ):
        raise AnalysisError(
            "EDS state must list {} scenarios in creation order".format(
                len(planned_scenarios)
            )
        )
    for index, (eds_scenario, planned_scenario) in enumerate(
        zip(eds_scenarios, planned_scenarios)
    ):
        for field in ("priority", "number_of_reps", "confidence_metric"):
            if eds_scenario.get(field) != planned_scenario[field]:
                raise AnalysisError(
                    "EDS scenario {} field {} is {!r}, design expects {!r} "
                    "(scenario order must match the design creation order)".format(
                        index, field, eds_scenario.get(field), planned_scenario[field]
                    )
                )
        if eds_scenario.get("published") is not True:
            raise AnalysisError(
                "EDS scenario {} is not marked published".format(index)
            )

    scenarios = {}
    for planned_scenario in planned_scenarios:
        label = planned_scenario["label"]
        scenario_dir = os.path.join(evidence_dir, "scenarios", label)
        rows = _read_json(
            os.path.join(scenario_dir, "result-rows.json"),
            "result rows for {}".format(label),
        )
        if not isinstance(rows, list) or not rows:
            raise AnalysisError(
                "scenario {} has no exported result rows".format(label)
            )
        replay_report = _read_json(
            os.path.join(scenario_dir, "replay-report.json"),
            "replay report for {}".format(label),
        )
        scenarios[label] = {"rows": rows, "replay_report": replay_report}
    return {
        "planned": planned,
        "eds_state": eds_state,
        "records": records,
        "scenarios": scenarios,
    }


def map_scenarios(evidence):
    """Map each planned scenario (creation order) to its Core DB record.

    Deterministic: the EDS state gives the parameterset per creation-order
    index; records are consumed in ``id`` order, matching on
    ``recipe_info.parameterset_id`` plus the scenario settings
    (``confidence_metric``, ``priority``, ``number_of_reps``), which makes
    the shared-recipe B scenarios unambiguous. Returns a list of
    ``(planned_scenario, record, eds_scenario)`` tuples; raises
    ``AnalysisError`` when the mapping is not exact.
    """
    planned_scenarios = evidence["planned"]["scenarios"]
    eds_scenarios = evidence["eds_state"]["design"]["scenarios"]
    records = sorted(
        evidence["records"], key=lambda record: record.get("id", 0)
    )
    consumed = [False] * len(records)
    mapping = []
    for index, planned_scenario in enumerate(planned_scenarios):
        eds_scenario = eds_scenarios[index]
        psid = eds_scenario.get("parameterset_id")
        wanted = (
            planned_scenario["confidence_metric"],
            planned_scenario["priority"],
            planned_scenario["number_of_reps"],
        )
        matched = None
        for record_index, record in enumerate(records):
            if consumed[record_index]:
                continue
            recipe_info = record.get("recipe_info") or {}
            if (
                recipe_info.get("parameterset_id") == psid
                and record.get("confidence_metric") == wanted[0]
                and record.get("priority") == wanted[1]
                and record.get("number_of_reps") == wanted[2]
            ):
                matched = record_index
                break
        if matched is None:
            raise AnalysisError(
                "no Core DB scenario record for design scenario {} "
                "(parameterset_id {}, settings {!r})".format(
                    planned_scenario["label"], psid, wanted
                )
            )
        consumed[matched] = True
        mapping.append((planned_scenario, records[matched], eds_scenario))
    if not all(consumed):
        raise AnalysisError(
            "{} Core DB scenario record(s) do not map to any design "
            "scenario".format(len(records) - sum(consumed))
        )
    return mapping


# ---- statistics and per-scenario records ------------------------------------


def half_width(observations, assert_results):
    """Return the PPS-style 95% Student-t half-width for the observations."""
    mean, std = assert_results.sample_mean_std(observations)
    n = len(observations)
    if n >= 2:
        return assert_results.student_t_quantile(0.975, n - 1) * std / math.sqrt(n)
    return 0.0


def analyze_scenario(evidence, mapping, assert_results, campaign):
    """Return the per-scenario analysis records (in creation order)."""
    records_out = []
    for planned_scenario, record, eds_scenario in mapping:
        label = planned_scenario["label"]
        rows = evidence["scenarios"][label]["rows"]
        replay_report = evidence["scenarios"][label]["replay_report"]

        context = {
            "parameterset_id": (record.get("recipe_info") or {}).get(
                "parameterset_id"
            ),
            "arrival_rate": planned_scenario["arrival_rate"],
            "service_rate": planned_scenario["service_rate"],
            "run_duration": planned_scenario["run_duration"],
            "seed_policy": planned_scenario["seed_policy"],
            "number_of_reps": planned_scenario["number_of_reps"],
            "confidence_metric": planned_scenario["confidence_metric"],
            "number_of_computed_reps": record.get("number_of_computed_reps"),
        }
        violations, summary = assert_results.validate_rows(rows, context)
        if violations:
            raise AnalysisError(
                "scenario {} failed the recomputed assertions: {}".format(
                    label, "; ".join(violations[:5])
                )
            )

        observations = [float(row["mean_wait_time"]) for row in rows]
        abandonment = [float(row["abandonment_probability"]) for row in rows]
        half = half_width(observations, assert_results)
        epsilon = planned_scenario["confidence_metric"]
        verdict = "met" if half <= epsilon else "stop_unmet"
        abandonment_half = half_width(abandonment, assert_results)

        stages = campaign.stage_durations(record)
        records_out.append(
            {
                "label": label,
                "group": planned_scenario["group"],
                "recipe_label": planned_scenario["recipe"],
                "scenario_id": record.get("id"),
                "parameterset_id": context["parameterset_id"],
                "arrival_rate": planned_scenario["arrival_rate"],
                "service_rate": planned_scenario["service_rate"],
                "run_duration": planned_scenario["run_duration"],
                "seed_policy": planned_scenario["seed_policy"],
                "rho": planned_scenario["rho"],
                "priority": planned_scenario["priority"],
                "number_of_reps_initial": planned_scenario["number_of_reps"],
                "confidence_metric": epsilon,
                "n": len(rows),
                "mean_wait_time": summary["sample_mean"],
                "mean_wait_time_half_width_95": half,
                "verdict": verdict,
                "abandonment_probability_mean": sum(abandonment) / len(abandonment),
                "abandonment_probability_half_width_95": abandonment_half,
                "throughput_mean": sum(
                    float(row["throughput"]) for row in rows
                ) / len(rows),
                "server_utilization_mean": sum(
                    float(row["server_utilization"]) for row in rows
                ) / len(rows),
                "runner_round": record.get("runner_round"),
                "round_reps": record.get("round_reps"),
                "round_computed_reps": record.get("round_computed_reps"),
                "stage_created_to_translation_published_s": stages[
                    "created_to_translation_published_s"
                ],
                "stage_translation_published_to_terminal_s": stages[
                    "translation_published_to_terminal_s"
                ],
                "stage_total_s": stages["total_s"],
                "assertions_passed": True,
                "replay_all_passed": bool(replay_report.get("all_passed")),
            }
        )
    return records_out


# ---- aggregates, illustration, Experiment C ---------------------------------


def aggregate_groups(scenario_records):
    """Return the per-experiment-group aggregate totals."""
    groups = {}
    for record in scenario_records:
        group = groups.setdefault(
            record["group"],
            {
                "scenarios": 0,
                "total_replications": 0,
                "total_result_rows": 0,
                "verdicts": {},
                "all_assertions_passed": True,
                "all_replays_passed": True,
                "sum_stage_total_s": 0.0,
            },
        )
        group["scenarios"] += 1
        group["total_replications"] += record["n"]
        group["total_result_rows"] += record["n"]
        group["verdicts"][record["verdict"]] = (
            group["verdicts"].get(record["verdict"], 0) + 1
        )
        group["all_assertions_passed"] = group["all_assertions_passed"] and record[
            "assertions_passed"
        ]
        group["all_replays_passed"] = group["all_replays_passed"] and record[
            "replay_all_passed"
        ]
        if record["stage_total_s"] is not None:
            group["sum_stage_total_s"] += record["stage_total_s"]
    return {group: groups[group] for group in sorted(groups)}


def precision_illustration(evidence, scenario_records, assert_results):
    """Return the Experiment-B offline common-sequence precision illustration.

    From the longest recorded B row set (ties broken by design scenario
    order), in the deterministic seed-sorted order (recorded
    ``effective_seed`` ascending), the smallest prefix whose sample
    half-width satisfies each illustration epsilon.
    """
    b_records = [
        (record, evidence["scenarios"][record["label"]]["rows"])
        for record in scenario_records
        if record["group"] == "B"
    ]
    if not b_records:
        raise AnalysisError("no B scenario rows for the precision illustration")
    # Longest row set first; ties keep the design scenario order (max keeps
    # the first maximal element over the creation-ordered records).
    best_record, rows = max(b_records, key=lambda entry: len(entry[1]))
    label = best_record["label"]
    ordered = sorted(rows, key=lambda row: row["effective_seed"])
    observations = [float(row["mean_wait_time"]) for row in ordered]

    prefixes = {}
    for epsilon in ILLUSTRATION_EPSILONS:
        key = "{:.2f}".format(epsilon)
        satisfied = None
        for n in range(2, len(observations) + 1):
            if half_width(observations[:n], assert_results) <= epsilon:
                satisfied = n
                break
        prefixes[key] = satisfied
    ordered_values = [
        prefixes["{:.2f}".format(epsilon)] if prefixes["{:.2f}".format(epsilon)]
        is not None
        else len(observations) + 1
        for epsilon in ILLUSTRATION_EPSILONS
    ]
    return {
        "scenario_label": label,
        "scenario_id": best_record["scenario_id"],
        "rows": len(observations),
        "order": "recorded effective_seed ascending (deterministic; not the live order)",
        "epsilon_prefix": prefixes,
        "nondecreasing_relation_holds": ordered_values == sorted(ordered_values),
        "caveat": (
            "the seed-sorted order is a recorded-data ordering, not the "
            "live hostname-derived replication order; this illustrates the "
            "precision-replication trade on one common sequence, not the "
            "live adaptive allocation"
        ),
    }


def experiment_c_rows(analysis, evidence_dir):
    """Rebuild the concatenated Experiment-A rows (design scenario order)
    from the evidence directory."""
    rows = []
    for record in analysis["scenarios"]:
        if record["group"] == "A":
            rows.extend(
                _read_json(
                    os.path.join(
                        evidence_dir, "scenarios", record["label"], "result-rows.json"
                    ),
                    "Experiment-C rows",
                )
            )
    if not rows:
        raise AnalysisError("no A scenario rows for the Experiment-C replay")
    return rows


def run_experiment_c(evidence_dir, rows, python_exe=None):
    """Run ``replay-seeds.py`` over every Experiment-A row (P6-R5).

    Writes ``experiment-c-replay-report.json`` into the evidence directory
    and returns the report dict. Raises ``ReplayContractError`` (SimPy
    contract) or ``AnalysisError`` (mismatch/malformed, usage error).
    """
    python_exe = python_exe or sys.executable
    report_path = os.path.join(evidence_dir, "experiment-c-replay-report.json")
    rows_path = os.path.join(evidence_dir, "experiment-c-rows.json")
    with open(rows_path, "w", encoding="utf-8") as fh:
        json.dump(rows, fh, indent=2)
        fh.write("\n")
    try:
        proc = subprocess.run(
            [
                python_exe,
                REPLAY_SEEDS_PY,
                "--rows",
                rows_path,
                "--report",
                report_path,
            ],
            capture_output=True,
            text=True,
            timeout=3600,
        )
    finally:
        # The row file is an intermediate; the report is the evidence.
        try:
            os.remove(rows_path)
        except OSError:
            pass
    if proc.returncode == 0:
        return _read_json(report_path, "Experiment-C replay report")
    if proc.returncode == 3:
        raise ReplayContractError(
            "SimPy 4.1.2 replay contract not met: {}".format(
                proc.stderr.strip().splitlines()[-1] if proc.stderr else "unknown"
            )
        )
    raise AnalysisError(
        "Experiment-C replay failed (exit {}): {}\n{}".format(
            proc.returncode, proc.stderr.strip(), proc.stdout[-2000:]
        )
    )


# ---- outputs ----------------------------------------------------------------


def build_analysis(design, scenario_records, illustration, c_report=None):
    """Assemble the full analysis dict (deterministic key order)."""
    campaign = load_campaign()
    planned = campaign.plan(design)
    analysis = {
        "campaign": design["campaign"],
        "model": design["model"],
        "design_summary": {
            "entries": planned["entries"],
            "recipes": len(planned["recipes"]),
            "scenarios": len(planned["scenarios"]),
        },
        "scenarios": scenario_records,
        "aggregates": aggregate_groups(scenario_records),
        "illustration": illustration,
    }
    if c_report is not None:
        analysis["experiment_c"] = {
            "scenarios": [
                record["label"] for record in scenario_records if record["group"] == "A"
            ],
            "rows_total": c_report.get("rows_total"),
            "rows_passed": c_report.get("rows_passed"),
            "all_passed": c_report.get("all_passed"),
            "simpy_version": c_report.get("simpy_version"),
            "python_version": c_report.get("python_version"),
            "report_file": "experiment-c-replay-report.json",
        }
    return analysis


def write_json(evidence_dir, analysis):
    path = os.path.join(evidence_dir, "analysis.json")
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(analysis, fh, indent=2)
        fh.write("\n")
    return path


CSV_COLUMNS = (
    "label",
    "group",
    "scenario_id",
    "parameterset_id",
    "recipe_label",
    "arrival_rate",
    "service_rate",
    "run_duration",
    "seed_policy",
    "rho",
    "priority",
    "number_of_reps_initial",
    "confidence_metric",
    "n",
    "mean_wait_time",
    "mean_wait_time_half_width_95",
    "verdict",
    "abandonment_probability_mean",
    "abandonment_probability_half_width_95",
    "throughput_mean",
    "server_utilization_mean",
    "runner_round",
    "round_reps",
    "round_computed_reps",
    "stage_created_to_translation_published_s",
    "stage_translation_published_to_terminal_s",
    "stage_total_s",
    "assertions_passed",
    "replay_all_passed",
)


def write_csv(evidence_dir, analysis):
    path = os.path.join(evidence_dir, "analysis.csv")
    with open(path, "w", encoding="utf-8", newline="") as fh:
        writer = csv.writer(fh, lineterminator="\n")
        writer.writerow(CSV_COLUMNS)
        for record in analysis["scenarios"]:
            writer.writerow(
                [
                    "" if record[column] is None else record[column]
                    for column in CSV_COLUMNS
                ]
            )
    return path


def _format_k(value, total):
    if value is None:
        return "unmet (half-width still above epsilon at the full {} rows)".format(total)
    return "k={}".format(value)


def render_experiment_c_block(analysis):
    c = analysis["experiment_c"]
    lines = [
        "- Replay: `replay-seeds.py` run offline over every Experiment-A row "
        "({} scenarios, {} rows)".format(
            len(c["scenarios"]), c["rows_total"]
        ),
        "- Outcome: {} of {} rows replayed exactly "
        "(SimPy {}, Python {})".format(
            c["rows_passed"],
            c["rows_total"],
            c["simpy_version"],
            c["python_version"],
        ),
        "- Report: `{}`".format(c["report_file"]),
    ]
    return "\n".join(lines)


def render_illustration_block(illustration):
    lines = [
        "- Longest recorded B row set: {} ({} rows)".format(
            illustration["scenario_label"], illustration["rows"]
        ),
        "- Deterministic order: {}".format(illustration["order"]),
        "- Smallest seed-sorted prefix with 95% half-width <= epsilon:",
    ]
    for epsilon in ILLUSTRATION_EPSILONS:
        key = "{:.2f}".format(epsilon)
        lines.append(
            "  - epsilon={}: {}".format(
                key, _format_k(illustration["epsilon_prefix"][key], illustration["rows"])
            )
        )
    relation = "holds" if illustration["nondecreasing_relation_holds"] else "VIOLATED"
    lines.append(
        "- Expected nondecreasing prefix relation (k(0.20) <= k(0.10) <= "
        "k(0.05)): {}".format(relation)
    )
    lines.append("- Caveat: {}".format(illustration["caveat"]))
    return "\n".join(lines)


def _package_version(name):
    try:
        import importlib.metadata as metadata

        return metadata.version(name)
    except Exception:
        return None


def render_analysis_env_block():
    lines = [
        "- Analysis interpreter: Python {}.{}.{}".format(
            sys.version_info.major, sys.version_info.minor, sys.version_info.micro
        ),
        "- Analysis tooling: `analyze.py` (pure stdlib) and `plot-figures.py` "
        "(matplotlib, pinned in `requirements-analysis.txt`)",
    ]
    matplotlib_version = _package_version("matplotlib")
    if matplotlib_version:
        lines.append("- matplotlib: {}".format(matplotlib_version))
        numpy_version = _package_version("numpy")
        if numpy_version:
            lines.append("- numpy: {}".format(numpy_version))
    else:
        lines.append(
            "- matplotlib: not importable in this interpreter; figures must be "
            "generated by `plot-figures.py` in the pinned environment from "
            "`requirements-analysis.txt`"
        )
    lines.append(
        "- Figures: `figures/f1-*.svg` ... `figures/f4-*.svg` (deterministic "
        "SVG output, fixed svg.hashsalt)"
    )
    return "\n".join(lines)


def complete_run_md(evidence_dir, analysis):
    """Replace the ``{{P6-ANALYSIS:...}}`` markers in ``run.md``.

    Returns the number of markers replaced. A missing marker (or a run.md
    without the skeleton) raises ``AnalysisError``.
    """
    path = os.path.join(evidence_dir, "run.md")
    with open(path, "r", encoding="utf-8") as fh:
        text = fh.read()
    replacements = {
        MARKER_EXPERIMENT_C: render_experiment_c_block(analysis),
        MARKER_ILLUSTRATION: render_illustration_block(analysis["illustration"]),
        MARKER_ANALYSIS_ENV: render_analysis_env_block(),
    }
    replaced = 0
    for marker, block in replacements.items():
        if marker not in text:
            raise AnalysisError("run.md is missing the marker {}".format(marker))
        text = text.replace(marker, block)
        replaced += 1
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(text)
    return replaced


def check_outputs(evidence_dir, analysis):
    """Verify the analysis outputs landed in the evidence directory."""
    for name in ("analysis.json", "analysis.csv"):
        if not os.path.isfile(os.path.join(evidence_dir, name)):
            raise AnalysisError("output {} is missing".format(name))
    if analysis.get("experiment_c") and not os.path.isfile(
        os.path.join(evidence_dir, "experiment-c-replay-report.json")
    ):
        raise AnalysisError("experiment-c-replay-report.json is missing")


def analyze_evidence(design, evidence_dir):
    """Run the pure (SimPy-free) analysis over the evidence directory.

    Returns the analysis dict without the Experiment-C section.
    """
    assert_results = load_assert_results()
    campaign = load_campaign()
    evidence = load_evidence(evidence_dir, design)
    mapping = map_scenarios(evidence)
    scenario_records = analyze_scenario(evidence, mapping, assert_results, campaign)
    illustration = precision_illustration(evidence, scenario_records, assert_results)
    return build_analysis(design, scenario_records, illustration)


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Analyze the PADS initial-results campaign evidence (P6-R3)."
    )
    parser.add_argument(
        "--evidence", required=True, help="sanitized campaign evidence directory"
    )
    parser.add_argument(
        "--design",
        help="campaign design JSON (default: <evidence>/design.json)",
    )
    args = parser.parse_args(argv)

    evidence_dir = os.path.abspath(args.evidence)
    design_path = args.design or os.path.join(evidence_dir, "design.json")
    campaign = load_campaign()
    try:
        design = campaign.load_design(design_path)
    except OSError as exc:
        sys.stderr.write("analyze: cannot read design: {}\n".format(exc))
        return 2
    except campaign.DesignError as exc:
        sys.stderr.write("analyze: design invalid: {}\n".format(exc))
        return 2

    try:
        analysis = analyze_evidence(design, evidence_dir)
    except AnalysisError as exc:
        sys.stderr.write("analyze: {}\n".format(exc))
        return 1

    try:
        c_report = run_experiment_c(evidence_dir, experiment_c_rows(analysis, evidence_dir))
    except ReplayContractError as exc:
        sys.stderr.write("analyze: {}\n".format(exc))
        return 3
    except AnalysisError as exc:
        sys.stderr.write("analyze: {}\n".format(exc))
        return 1

    analysis = build_analysis(
        design,
        analysis["scenarios"],
        analysis["illustration"],
        c_report=c_report,
    )
    try:
        write_json(evidence_dir, analysis)
        write_csv(evidence_dir, analysis)
        complete_run_md(evidence_dir, analysis)
        check_outputs(evidence_dir, analysis)
    except AnalysisError as exc:
        sys.stderr.write("analyze: {}\n".format(exc))
        return 1

    sys.stdout.write(
        "analyze: OK - {} scenarios analyzed, Experiment C {} of {} rows, "
        "outputs in {}\n".format(
            len(analysis["scenarios"]),
            analysis["experiment_c"]["rows_passed"],
            analysis["experiment_c"]["rows_total"],
            evidence_dir,
        )
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
