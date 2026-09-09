"""Pure result-record construction for the reference runner.

Builds the JSONB result record the runner inserts into the Result DB. The
record carries the baked scenario parameters, the effective seed, and the
queue-model outputs. This module has no third-party dependencies so the exact
result fields can be unit-tested without simpy or psycopg.
"""


def build_result(scenario, completed_customers, mean_wait_time, effective_seed):
    """Return the result dict inserted into the scenario result table.

    scenario is the baked scenario.json dict (scenario_id, parameterset_id,
    arrival_rate, service_rate, run_duration, seed_policy). The record excludes
    scenario_id (it is the table/partition key) and any database credentials.
    """
    return {
        "parameterset_id": scenario["parameterset_id"],
        "arrival_rate": scenario["arrival_rate"],
        "service_rate": scenario["service_rate"],
        "run_duration": scenario["run_duration"],
        "seed_policy": scenario["seed_policy"],
        "effective_seed": effective_seed,
        "completed_customers": completed_customers,
        "mean_wait_time": mean_wait_time,
    }


RESULT_FIELDS = (
    "parameterset_id",
    "arrival_rate",
    "service_rate",
    "run_duration",
    "seed_policy",
    "effective_seed",
    "completed_customers",
    "mean_wait_time",
)
