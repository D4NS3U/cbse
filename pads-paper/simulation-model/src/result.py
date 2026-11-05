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

"""Strict result-record construction for the ``erlang_a_v1`` runner.

Builds the JSONB result record a runner inserts into the Result DB from the
baked scenario, the exact metric dict returned by ``model.run``, and the
effective seed. The builder rejects malformed data — missing or unexpected
fields, non-finite values, inconsistent accounting, alias mismatch, and
probability/utilization outside ``[0, 1]`` — instead of persisting it. The
record carries no ``scenario_id`` (the table/partition key) and no database
credentials. This module has no third-party dependencies so the exact result
fields can be unit-tested without simpy or psycopg.
"""

import math

#: Baked scenario keys the builder copies into every result record.
SCENARIO_FIELDS = (
    "parameterset_id",
    "arrival_rate",
    "service_rate",
    "run_duration",
    "seed_policy",
)

#: Exact ``model.run`` metric fields, in deterministic order.
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

#: Deterministic output field order of the result record.
RESULT_FIELDS = SCENARIO_FIELDS + ("effective_seed",) + METRIC_FIELDS

#: The only model version this builder accepts.
EXPECTED_MODEL_ID = "erlang_a_v1"


def _require_int(name, value, minimum):
    if isinstance(value, bool) or not isinstance(value, int) or value < minimum:
        raise ValueError(f"{name} must be an integer >= {minimum}, got {value!r}")


def _require_finite_float(name, value, low, high):
    if isinstance(value, bool) or not isinstance(value, float) or not math.isfinite(value):
        raise ValueError(f"{name} must be a finite float, got {value!r}")
    if value < low:
        raise ValueError(f"{name} must be >= {low!r}, got {value!r}")
    if high is not None and value > high:
        raise ValueError(f"{name} must be <= {high!r}, got {value!r}")


def _require_exact_keys(name, value, fields):
    if not isinstance(value, dict):
        raise ValueError(f"{name} must be a dict, got {type(value).__name__}")
    missing = set(fields) - set(value)
    if missing:
        raise ValueError(f"{name} is missing fields: {sorted(missing)}")
    unknown = set(value) - set(fields)
    if unknown:
        raise ValueError(f"{name} has unexpected fields: {sorted(unknown)}")


def build_result(scenario, metrics, effective_seed):
    """Return the result dict inserted into the scenario result table.

    ``scenario`` carries exactly the baked keys ``parameterset_id``,
    ``arrival_rate``, ``service_rate``, ``run_duration``, and
    ``seed_policy`` (no ``scenario_id``, no credentials). ``metrics`` is the
    exact dict returned by ``model.run``. ``effective_seed`` is the
    non-negative integer seed the replication was drawn from.

    Raises ``ValueError`` when any field is missing, unexpected, non-finite,
    out of domain, or inconsistent (accounting identity, alias, bounds).
    """
    _require_exact_keys("scenario", scenario, SCENARIO_FIELDS)
    _require_int("parameterset_id", scenario["parameterset_id"], 0)
    _require_int("arrival_rate", scenario["arrival_rate"], 1)
    _require_int("service_rate", scenario["service_rate"], 1)
    _require_int("run_duration", scenario["run_duration"], 1)
    _require_int("seed_policy", scenario["seed_policy"], 0)

    _require_exact_keys("metrics", metrics, METRIC_FIELDS)
    if metrics["model_id"] != EXPECTED_MODEL_ID:
        raise ValueError(
            f"model_id must be {EXPECTED_MODEL_ID!r}, got {metrics['model_id']!r}"
        )
    _require_int("completed_customers", metrics["completed_customers"], 0)
    _require_int("served_customers", metrics["served_customers"], 0)
    _require_int("abandoned_customers", metrics["abandoned_customers"], 0)
    _require_int("total_customers", metrics["total_customers"], 0)
    _require_finite_float("mean_wait_time", metrics["mean_wait_time"], 0.0, None)
    _require_finite_float(
        "abandonment_probability", metrics["abandonment_probability"], 0.0, 1.0
    )
    _require_finite_float("throughput", metrics["throughput"], 0.0, None)
    _require_finite_float("server_utilization", metrics["server_utilization"], 0.0, 1.0)
    if (
        metrics["served_customers"] + metrics["abandoned_customers"]
        != metrics["total_customers"]
    ):
        raise ValueError(
            "accounting mismatch: served_customers + abandoned_customers "
            "!= total_customers"
        )
    if metrics["completed_customers"] != metrics["served_customers"]:
        raise ValueError(
            "alias mismatch: completed_customers != served_customers"
        )

    _require_int("effective_seed", effective_seed, 0)

    record = {}
    for key in SCENARIO_FIELDS:
        record[key] = scenario[key]
    record["effective_seed"] = effective_seed
    for key in METRIC_FIELDS:
        record[key] = metrics[key]
    return record
