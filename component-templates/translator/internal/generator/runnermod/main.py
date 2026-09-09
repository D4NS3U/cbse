"""Runner entrypoint for the reference example generator.

Reads the baked scenario parameters and Result DB connection, derives a local
PRNG seed from the seed policy and the runtime hostname, runs the single-server
queue, and inserts one JSONB result. It starts the baked scenario without
command, arguments, or environment variables from SM and obtains its Pod
hostname from the container runtime with socket.gethostname().
"""

import json
import random
import socket
import sys

import model
import result
import resultdb
from seed import derive_seed

SCENARIO_PATH = "/runner/scenario.json"
RESULTDB_PATH = "/runner/resultdb.json"


def load_json(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def main():
    scenario = load_json(SCENARIO_PATH)
    db_config = load_json(RESULTDB_PATH)

    hostname = socket.gethostname()
    effective_seed = derive_seed(scenario["seed_policy"], hostname)
    rng = random.Random(effective_seed)

    completed, mean_wait = model.run(
        scenario["arrival_rate"],
        scenario["service_rate"],
        scenario["run_duration"],
        rng,
    )

    result_record = result.build_result(scenario, completed, mean_wait, effective_seed)

    try:
        resultdb.insert(scenario["scenario_id"], result_record, db_config)
    except Exception as exc:  # noqa: BLE001 - any DB failure must fail the Pod
        sys.stderr.write("result db insert failed: {}\n".format(exc))
        sys.exit(1)


if __name__ == "__main__":
    main()
