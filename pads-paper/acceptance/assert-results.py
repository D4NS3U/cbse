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

"""Pure validation of exported ``erlang_a_v1`` result rows (P5 acceptance, P5-R5).

Validates every exported Result DB row against the ruled result contract and
exits non-zero on any violation:

- every row has exactly the 15 ``RESULT_FIELDS`` keys with the ruled types
  (scenario integers, ``effective_seed`` non-negative integer,
  ``model_id == "erlang_a_v1"``, non-negative integer counts, finite floats,
  probability/utilization in ``[0, 1]``);
- ``completed_customers == served_customers`` and
  ``served + abandoned == total``;
- ``throughput == served / run_duration`` and
  ``abandonment_probability == abandoned / total`` (zero-safe) within float
  tolerance;
- ``mean_wait_time`` finite and non-negative;
- every row's baked scenario parameters equal the experiment's design values
  (supplied via the context file);
- effective seeds are distinct across rows and their count matches the Core
  DB computed-replication count from the context.

On success it also computes an informational verdict summary over the pooled
``mean_wait_time`` observations with the PPS's paper-exact statistics (sample
mean, two-pass sample standard deviation, and the 95% confidence-interval
half-width ``h = t(0.975, n-1) * s / sqrt(n)``; the Student-t quantile is a
pure-stdlib regularized-incomplete-beta implementation, no third-party
dependencies) and the recomputed verdict against the context's
``confidence_metric``. The summary is informational evidence, not a gate:
the authoritative verdict is the Scenario Manager's Finished transition
driven by the live PPS met verdict.

CLI:

    python3 assert-results.py --rows ROWS_JSON --context CONTEXT_JSON \
        [--summary-out SUMMARY_JSON]

``ROWS_JSON`` is a JSON array of exported result-row objects. ``CONTEXT_JSON``
is a JSON object with exactly the keys ``parameterset_id``,
``arrival_rate``, ``service_rate``, ``run_duration``, ``seed_policy``,
``number_of_computed_reps``, ``confidence_metric`` (and optionally
``number_of_reps``). ``--summary-out`` writes the machine-readable verdict
summary (row count, seed distinctness, sample mean, sample standard
deviation, half-width, recomputed verdict) as JSON; it is written only when
all assertions pass.

Exit codes:

    0  every assertion passed (summary written when requested).
    1  one or more violations (each printed to stderr).
    2  usage, input-file, or context-contract error.

This module has no third-party dependencies and is importable (through
importlib, because of the hyphenated file name) so the self-tests in
``test_acceptance.py`` and ``replay-seeds.py`` can reuse the row contract.
"""

import argparse
import json
import math
import sys

#: The only model version this acceptance run accepts.
EXPECTED_MODEL_ID = "erlang_a_v1"

#: Baked scenario keys every result row carries.
SCENARIO_FIELDS = (
    "parameterset_id",
    "arrival_rate",
    "service_rate",
    "run_duration",
    "seed_policy",
)

#: Exact ``erlang_a_v1`` metric fields, in deterministic order.
METRIC_FIELDS = (
    "model_id",
    "completed_customers",
    "served_customers",
    "abandoned_customers",
    "total_customers",
    "mean_wait_time",
    "abandonment_probability",
    "throughput",
    "server_utilization",
)

#: Deterministic field order of the exported result row (15 fields).
RESULT_FIELDS = SCENARIO_FIELDS + ("effective_seed",) + METRIC_FIELDS

COUNT_FIELDS = (
    "completed_customers",
    "served_customers",
    "abandoned_customers",
    "total_customers",
)

FLOAT_FIELDS = (
    "mean_wait_time",
    "abandonment_probability",
    "throughput",
    "server_utilization",
)

#: Relative tolerance for the derived-metric checks (P5-R5 "float tolerance").
RELATIVE_TOLERANCE = 1e-9


def _is_int(value):
    """True for real integers (booleans are not integers here)."""
    return isinstance(value, int) and not isinstance(value, bool)


def _is_finite_number(value):
    """True for finite JSON numbers (int or float; booleans excluded)."""
    return (
        isinstance(value, (int, float))
        and not isinstance(value, bool)
        and math.isfinite(value)
    )


def _within_tolerance(value, expected):
    return abs(value - expected) <= RELATIVE_TOLERANCE * max(1.0, abs(expected))


def validate_row_shape(row, index):
    """Validate one row's exact key set, types, and internal consistency.

    Returns a list of violation strings (empty when the row is well-formed).
    Row-internal checks only: the baked scenario parameters are checked
    against the design values by :func:`validate_row_design`.
    """
    violations = []
    prefix = "row {}".format(index)
    if not isinstance(row, dict):
        return ["{}: not a JSON object (got {})".format(prefix, type(row).__name__)]

    missing = sorted(set(RESULT_FIELDS) - set(row))
    if missing:
        violations.append("{}: missing fields: {}".format(prefix, missing))
    unknown = sorted(set(row) - set(RESULT_FIELDS))
    if unknown:
        violations.append("{}: unexpected fields: {}".format(prefix, unknown))
    if missing or unknown:
        return violations

    for field in ("parameterset_id", "arrival_rate", "service_rate", "run_duration"):
        if not _is_int(row[field]):
            violations.append(
                "{}: {} must be an integer, got {!r}".format(prefix, field, row[field])
            )
        elif row[field] <= 0:
            violations.append(
                "{}: {} must be positive, got {}".format(prefix, field, row[field])
            )
    if not _is_int(row["seed_policy"]) or row["seed_policy"] < 0:
        violations.append(
            "{}: seed_policy must be a non-negative integer, got {!r}".format(prefix, row["seed_policy"])
        )
    if not _is_int(row["effective_seed"]) or row["effective_seed"] < 0:
        violations.append(
            "{}: effective_seed must be a non-negative integer, got {!r}".format(prefix, row["effective_seed"])
        )
    if row["model_id"] != EXPECTED_MODEL_ID:
        violations.append(
            "{}: model_id must be {!r}, got {!r}".format(prefix, EXPECTED_MODEL_ID, row["model_id"])
        )
    for field in COUNT_FIELDS:
        if not _is_int(row[field]) or row[field] < 0:
            violations.append(
                "{}: {} must be a non-negative integer, got {!r}".format(prefix, field, row[field])
            )
    for field in FLOAT_FIELDS:
        if not _is_finite_number(row[field]):
            violations.append(
                "{}: {} must be a finite number, got {!r}".format(prefix, field, row[field])
            )
        elif field in ("abandonment_probability", "server_utilization") and not (
            0.0 <= row[field] <= 1.0
        ):
            violations.append(
                "{}: {} must be in [0, 1], got {!r}".format(prefix, field, row[field])
            )
    if _is_finite_number(row["mean_wait_time"]) and row["mean_wait_time"] < 0.0:
        violations.append(
            "{}: mean_wait_time must be non-negative, got {!r}".format(prefix, row["mean_wait_time"])
        )

    counts_ok = all(_is_int(row[f]) and row[f] >= 0 for f in COUNT_FIELDS)
    if counts_ok:
        if row["completed_customers"] != row["served_customers"]:
            violations.append(
                "{}: alias mismatch: completed_customers {} != served_customers {}".format(
                    prefix, row["completed_customers"], row["served_customers"]
                )
            )
        if row["served_customers"] + row["abandoned_customers"] != row["total_customers"]:
            violations.append(
                "{}: accounting mismatch: served {} + abandoned {} != total {}".format(
                    prefix,
                    row["served_customers"],
                    row["abandoned_customers"],
                    row["total_customers"],
                )
            )
        if _is_int(row["run_duration"]) and row["run_duration"] > 0:
            expected_throughput = row["served_customers"] / row["run_duration"]
            if _is_finite_number(row["throughput"]) and not _within_tolerance(
                row["throughput"], expected_throughput
            ):
                violations.append(
                    "{}: throughput {} != served/run_duration {}".format(
                        prefix, row["throughput"], expected_throughput
                    )
                )
        if _is_int(row["total_customers"]) and row["total_customers"] >= 0:
            expected_abandonment = (
                row["abandoned_customers"] / row["total_customers"]
                if row["total_customers"] > 0
                else 0.0
            )
            if _is_finite_number(row["abandonment_probability"]) and not _within_tolerance(
                row["abandonment_probability"], expected_abandonment
            ):
                violations.append(
                    "{}: abandonment_probability {} != abandoned/total {}".format(
                        prefix, row["abandonment_probability"], expected_abandonment
                    )
                )
    return violations


def validate_row_design(row, index, design):
    """Validate one row's baked scenario parameters against the design values.

    ``design`` maps the five scenario field names to the experiment's design
    integers. Returns a list of violation strings.
    """
    violations = []
    prefix = "row {}".format(index)
    if not isinstance(row, dict):
        return violations
    for field in SCENARIO_FIELDS:
        if field in row and _is_int(row[field]) and row[field] != design[field]:
            violations.append(
                "{}: baked {} is {}, expected design value {}".format(
                    prefix, field, row[field], design[field]
                )
            )
    return violations


def validate_rows(rows, context):
    """Validate every row against the shape contract and the design context.

    ``context`` maps the scenario fields to design values and must carry
    ``number_of_computed_reps`` (positive int) and ``confidence_metric``
    (finite float > 0). Returns ``(violations, summary)`` where summary is the
    informational verdict-summary dict (None while violations exist).
    """
    violations = []
    design = {field: context[field] for field in SCENARIO_FIELDS}
    if not isinstance(rows, list):
        return ["rows file is not a JSON array"], None
    if len(rows) == 0:
        return ["no exported result rows"], None

    for index, row in enumerate(rows):
        violations.extend(validate_row_shape(row, index))
        violations.extend(validate_row_design(row, index, design))

    seeds = [
        row["effective_seed"]
        for row in rows
        if isinstance(row, dict) and _is_int(row.get("effective_seed"))
    ]
    duplicates = sorted({s for s in seeds if seeds.count(s) > 1})
    if duplicates:
        violations.append("duplicate effective seeds across rows: {}".format(duplicates))
    computed = context["number_of_computed_reps"]
    if len(rows) != computed:
        violations.append(
            "row count {} does not match the Core DB computed-replication count {}".format(
                len(rows), computed
            )
        )

    if violations:
        return violations, None
    return [], verdict_summary(rows, context)


def _betacf(x, a, b):
    """Continued fraction for the regularized incomplete beta (Lentz's method)."""
    maxiter = 300
    eps = 3e-15
    fpmin = 1e-300
    qab = a + b
    qap = a + 1.0
    qam = a - 1.0
    c = 1.0
    d = 1.0 - qab * x / qap
    if abs(d) < fpmin:
        d = fpmin
    d = 1.0 / d
    h = d
    for m in range(1, maxiter + 1):
        m2 = 2 * m
        aa = m * (b - m) * x / ((qam + m2) * (a + m2))
        d = 1.0 + aa * d
        if abs(d) < fpmin:
            d = fpmin
        c = 1.0 + aa / c
        if abs(c) < fpmin:
            c = fpmin
        d = 1.0 / d
        h *= d * c
        aa = -(a + m) * (qab + m) * x / ((a + m2) * (qap + m2))
        d = 1.0 + aa * d
        if abs(d) < fpmin:
            d = fpmin
        c = 1.0 + aa / c
        if abs(c) < fpmin:
            c = fpmin
        d = 1.0 / d
        delta = d * c
        h *= delta
        if abs(delta - 1.0) < eps:
            break
    return h


def _betainc_regularized(x, a, b):
    """Regularized incomplete beta I_x(a, b) in double precision."""
    if x <= 0.0:
        return 0.0
    if x >= 1.0:
        return 1.0
    if x > (a + 1.0) / (a + b + 2.0):
        return 1.0 - _betainc_regularized(1.0 - x, b, a)
    ln_front = (
        a * math.log(x)
        + b * math.log(1.0 - x)
        + math.lgamma(a + b)
        - math.lgamma(a)
        - math.lgamma(b)
    )
    front = math.exp(ln_front)
    return front * _betacf(x, a, b) / a


def student_t_quantile(probability, df):
    """Return t such that P(T <= t) = probability for T ~ Student-t(df).

    Pure-stdlib implementation: for t > 0, the upper tail is
    P(T > t) = I_{df/(df+t^2)}(df/2, 1/2) / 2, so the upper-tail quantile
    solves the regularized-incomplete-beta equation
    I_x(df/2, 1/2) = 2 * (1 - probability) by bisection on
    ``x = df / (df + t^2)``, and t = sqrt(df * (1 - x) / x).
    ``df`` must be >= 1.
    """
    if df < 1:
        raise ValueError("df must be >= 1, got {}".format(df))
    if not (0.5 < probability < 1.0):
        raise ValueError("probability must be in (0.5, 1.0), got {}".format(probability))
    target = 2.0 * (1.0 - probability)  # I_x(df/2, 1/2) = 2 * upper tail
    a = df / 2.0
    b = 0.5
    lo, hi = 0.0, 1.0
    for _ in range(300):
        mid = 0.5 * (lo + hi)
        if _betainc_regularized(mid, a, b) < target:
            lo = mid
        else:
            hi = mid
        if hi - lo < 1e-16:
            break
    x = 0.5 * (lo + hi)
    return math.sqrt(df * (1.0 - x) / x)


def sample_mean_std(observations):
    """Two-pass sample mean and sample standard deviation (n-1 denominator).

    Mirrors the PPS evaluation package: mean first, then the sum of squared
    deviations about the mean divided by n-1. For n = 0 returns (0.0, 0.0);
    for n = 1 returns (mean, 0.0) (the sample variance is undefined).
    """
    n = len(observations)
    if n == 0:
        return 0.0, 0.0
    mean = sum(observations) / n
    if n == 1:
        return mean, 0.0
    ss = sum((v - mean) ** 2 for v in observations)
    return mean, math.sqrt(ss / (n - 1))


def verdict_summary(rows, context):
    """Compute the informational PPS-equivalent verdict summary over the rows.

    Returns a JSON-serializable dict: row count, seed distinctness, sample
    mean and standard deviation of the pooled ``mean_wait_time`` observations,
    the 95% confidence-interval half-width ``h = t(0.975, n-1) * s / sqrt(n)``
    (0.0 while n < 2, the PPS degenerate convention), the context's
    confidence metric, and the recomputed verdict (``met`` iff n >= 2 and
    h <= confidence_metric, the PPS precision criterion).
    """
    observations = [float(row["mean_wait_time"]) for row in rows]
    mean, std = sample_mean_std(observations)
    n = len(observations)
    if n >= 2:
        half_width = student_t_quantile(0.975, n - 1) * std / math.sqrt(n)
        recomputed = "met" if half_width <= context["confidence_metric"] else "unmet"
    else:
        half_width = 0.0
        recomputed = "unmet"  # the PPS counts n < 2 as not-met
    seeds = [row["effective_seed"] for row in rows]
    return {
        "rows": n,
        "seeds_distinct": len(set(seeds)) == n,
        "sample_mean": mean,
        "sample_std": std,
        "half_width": half_width,
        "confidence_metric": context["confidence_metric"],
        "recomputed_verdict": recomputed,
        "number_of_computed_reps": context["number_of_computed_reps"],
        "assertions_passed": True,
    }


def load_context(path):
    """Load and validate the design context file. Raises ValueError on a bad
    context (usage error, exit 2)."""
    with open(path, "r", encoding="utf-8") as fh:
        context = json.load(fh)
    if not isinstance(context, dict):
        raise ValueError("context file is not a JSON object")
    required_ints = (
        "parameterset_id",
        "arrival_rate",
        "service_rate",
        "run_duration",
        "seed_policy",
        "number_of_computed_reps",
    )
    for field in required_ints:
        if field not in context or not _is_int(context[field]):
            raise ValueError("context field {} must be an integer".format(field))
    for field in ("arrival_rate", "service_rate", "run_duration"):
        if context[field] <= 0:
            raise ValueError("context field {} must be positive".format(field))
    if context["seed_policy"] < 0 or context["number_of_computed_reps"] <= 0:
        raise ValueError(
            "seed_policy must be non-negative and number_of_computed_reps must be positive"
        )
    if (
        "confidence_metric" not in context
        or not _is_finite_number(context["confidence_metric"])
        or context["confidence_metric"] <= 0.0
    ):
        raise ValueError("context field confidence_metric must be a finite number > 0")
    if "number_of_reps" in context and (
        not _is_int(context["number_of_reps"]) or context["number_of_reps"] <= 0
    ):
        raise ValueError("context field number_of_reps must be a positive integer")
    return context


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--rows", required=True, help="JSON array of exported result rows")
    parser.add_argument("--context", required=True, help="JSON design context object")
    parser.add_argument(
        "--summary-out",
        help="write the machine-readable verdict summary JSON to this path (only on success)",
    )
    args = parser.parse_args(argv)

    try:
        context = load_context(args.context)
        with open(args.rows, "r", encoding="utf-8") as fh:
            rows = json.load(fh)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        sys.stderr.write("assert-results: cannot read input: {}\n".format(exc))
        return 2

    violations, summary = validate_rows(rows, context)
    if violations:
        for violation in violations:
            sys.stderr.write("assert-results: VIOLATION {}\n".format(violation))
        sys.stderr.write(
            "assert-results: FAILED with {} violation(s) over {} row(s)\n".format(
                len(violations), len(rows) if isinstance(rows, list) else "?"
            )
        )
        return 1

    if args.summary_out:
        with open(args.summary_out, "w", encoding="utf-8") as fh:
            json.dump(summary, fh, indent=2)
            fh.write("\n")
    seeds = [row["effective_seed"] for row in rows]
    print(
        "assert-results: PASS - {} rows, exact 15-field contract, internal "
        "consistency, design values, {} distinct effective seeds, count "
        "matches computed replications ({})".format(
            len(rows), len(set(seeds)), len(rows)
        )
    )
    print(
        "assert-results: verdict summary - sample_mean={} half_width={} "
        "recomputed_verdict={}".format(
            summary["sample_mean"], summary["half_width"], summary["recomputed_verdict"]
        )
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
