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

"""Recorded-effective-seed replay against the canonical ``erlang_a_v1`` model
(P5 acceptance, P5-R6).

Imports the canonical model from ``pads-paper/simulation-model/src/model.py``
(located relative to this script, no path configuration required),
reconstructs ``random.Random(effective_seed)`` per row, reruns
``model.run(arrival_rate, service_rate, run_duration, rng)``, and compares
every metric in the row:

- ``model_id`` and the four customer counts: exact parsed-value equality;
- ``mean_wait_time``, ``abandonment_probability``, ``throughput``, and
  ``server_utilization``: strict binary equality (``float.hex``) - same seed,
  same inputs, same code yields identical IEEE-754 values; the replay
  interpreter must therefore match the runner-base CPython minor version
  (3.14) so the stdlib RNG and libm values are bit-identical.

It reports per-row pass/fail and exits non-zero on any mismatch or malformed
row. The row-contract validation (exact 15 fields, ruled types, internal
consistency) is the shared contract in ``assert-results.py``, loaded from this
directory through importlib (hyphenated file name).

SimPy 4.1.2 is required at live-replay time. The module imports simpy lazily,
so this file stays importable - and its self-tests in ``test_acceptance.py``
skip cleanly - on hosts without simpy.

CLI:

    python3 replay-seeds.py --rows ROWS_JSON [--report REPORT_JSON]

The per-row report (index, effective seed, pass/fail, mismatch list) is always
printed to stdout as a single JSON object; ``--report`` additionally writes it
to the given path (the ``replay-report.json`` evidence file).

Exit codes:

    0  every row replayed and matched exactly.
    1  one or more rows mismatched or are malformed.
    2  usage or input-file error.
    3  the SimPy contract is not met (missing or not version 4.1.2).
"""

import argparse
import importlib.util
import json
import os
import random
import sys

#: The only SimPy version the live replay contract accepts (pinned by the
#: runner-base contract).
REQUIRED_SIMPY_VERSION = "4.1.2"

HERE = os.path.dirname(os.path.abspath(__file__))

#: Canonical model source, located relative to this script (P5-R6).
CANONICAL_MODEL_PY = os.path.normpath(
    os.path.join(HERE, os.pardir, "simulation-model", "src", "model.py")
)

#: The shared row-contract module (same directory, hyphenated file name).
ASSERT_RESULTS_PY = os.path.join(HERE, "assert-results.py")


def _load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise ImportError("cannot load module from {}".format(path))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def load_assert_results():
    """Load the shared row-contract module (``assert-results.py``)."""
    return _load_module("pads_acceptance_assert_results", ASSERT_RESULTS_PY)


def load_canonical_model():
    """Load the canonical ``model.py`` (imports simpy on module load)."""
    return _load_module("pads_paper_canonical_model", CANONICAL_MODEL_PY)


def simpy_version():
    """Return the installed SimPy version string, or None when absent.

    Prefers importlib metadata; falls back to the package ``__version__``
    attribute for installs without metadata.
    """
    try:
        import simpy  # noqa: F401
    except ImportError:
        return None
    try:
        import importlib.metadata as metadata

        return metadata.version("simpy")
    except Exception:
        pass
    try:
        import simpy

        version = getattr(simpy, "__version__", None)
        return version if isinstance(version, str) else None
    except ImportError:
        return None


def require_simpy():
    """Enforce the SimPy 4.1.2 live-replay contract.

    Returns the version on success; raises ``RuntimeError`` when simpy is
    missing or a different version is installed.
    """
    version = simpy_version()
    if version is None:
        raise RuntimeError(
            "SimPy is not importable; the live replay contract requires SimPy "
            "4.1.2 (its self-tests skip cleanly without it)"
        )
    if version != REQUIRED_SIMPY_VERSION:
        raise RuntimeError(
            "SimPy {} is required for replay; found {}".format(
                REQUIRED_SIMPY_VERSION, version
            )
        )
    return version


def compare_row(row, metrics):
    """Compare one well-formed row against replayed model metrics.

    Returns a list of mismatch strings (empty on exact match). Counts and
    probability use exact parsed-value equality; float metrics use strict
    binary equality via ``float.hex``.
    """
    mismatches = []
    if row["model_id"] != metrics["model_id"]:
        mismatches.append(
            "model_id: row={!r} replayed={!r}".format(row["model_id"], metrics["model_id"])
        )
    for field in (
        "completed_customers",
        "served_customers",
        "abandoned_customers",
        "total_customers",
    ):
        if row[field] != metrics[field]:
            mismatches.append(
                "{}: row={} replayed={}".format(field, row[field], metrics[field])
            )
    for field in (
        "mean_wait_time",
        "abandonment_probability",
        "throughput",
        "server_utilization",
    ):
        row_value = float(row[field])
        replayed_value = float(metrics[field])
        if row_value.hex() != replayed_value.hex():
            mismatches.append(
                "{}: row={} replayed={}".format(field, row_value.hex(), replayed_value.hex())
            )
    return mismatches


def replay_rows(rows):
    """Replay every row and return the per-row report list.

    Raises ``RuntimeError`` when the SimPy contract is not met.
    """
    assert_results = load_assert_results()
    model = load_canonical_model()
    report = []
    for index, row in enumerate(rows):
        shape_violations = assert_results.validate_row_shape(row, index)
        if shape_violations:
            report.append(
                {
                    "index": index,
                    "effective_seed": row.get("effective_seed") if isinstance(row, dict) else None,
                    "passed": False,
                    "mismatches": ["malformed row: " + v for v in shape_violations],
                }
            )
            continue
        effective_seed = row["effective_seed"]
        rng = random.Random(effective_seed)
        metrics = model.run(
            row["arrival_rate"], row["service_rate"], row["run_duration"], rng
        )
        mismatches = compare_row(row, metrics)
        report.append(
            {
                "index": index,
                "effective_seed": effective_seed,
                "passed": not mismatches,
                "mismatches": mismatches,
            }
        )
    return report


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--rows", required=True, help="JSON array of exported result rows")
    parser.add_argument(
        "--report",
        help="write the per-row replay report JSON to this path",
    )
    args = parser.parse_args(argv)

    try:
        with open(args.rows, "r", encoding="utf-8") as fh:
            rows = json.load(fh)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        sys.stderr.write("replay-seeds: cannot read rows: {}\n".format(exc))
        return 2
    if not isinstance(rows, list):
        sys.stderr.write("replay-seeds: rows file is not a JSON array\n")
        return 2
    if len(rows) == 0:
        sys.stderr.write("replay-seeds: no exported result rows\n")
        return 2

    try:
        version = require_simpy()
    except RuntimeError as exc:
        sys.stderr.write("replay-seeds: {}\n".format(exc))
        return 3

    row_reports = replay_rows(rows)
    rows_passed = sum(1 for r in row_reports if r["passed"])
    all_passed = rows_passed == len(row_reports)
    report = {
        "model_id": "erlang_a_v1",
        "simpy_version": version,
        "python_version": "{}.{}.{}".format(sys.version_info.major, sys.version_info.minor, sys.version_info.micro),
        "rows_total": len(row_reports),
        "rows_passed": rows_passed,
        "all_passed": all_passed,
        "rows": row_reports,
    }
    rendered = json.dumps(report, indent=2) + "\n"
    if args.report:
        with open(args.report, "w", encoding="utf-8") as fh:
            fh.write(rendered)
    sys.stdout.write(rendered)
    if not all_passed:
        sys.stderr.write(
            "replay-seeds: FAILED - {} of {} rows matched exactly\n".format(
                len(row_reports) - rows_passed, len(row_reports)
            )
        )
        return 1
    print("replay-seeds: PASS - {} of {} rows replayed exactly (SimPy {})".format(
        rows_passed, len(row_reports), version
    ))
    return 0


if __name__ == "__main__":
    sys.exit(main())
