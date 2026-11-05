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

"""Runner entrypoint for the PADS paper-local generator.

Reads the baked scenario parameters and Result DB connection, derives a local
PRNG seed from the seed policy and the runtime hostname, runs one replication
of the synchronized ``erlang_a_v1`` model, builds the strict result record,
and inserts one JSONB result. It starts the baked scenario without command,
arguments, or environment variables from SM and obtains its Pod hostname from
the container runtime with socket.gethostname().

This module is Translator-owned production glue, not a synchronized file:
model.py, result.py, seed.py, and their test suites are byte-identical copies
of the canonical model source in pads-paper/simulation-model/src/. The
inserted record carries exactly the five baked scenario fields,
``effective_seed``, and the nine ``erlang_a_v1`` metric fields (the
``result.RESULT_FIELDS`` order); it carries no ``scenario_id`` (the
table/partition key), no credentials, and no event-level logging.
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

    try:
        metrics = model.run(
            scenario["arrival_rate"],
            scenario["service_rate"],
            scenario["run_duration"],
            rng,
        )
        # build_result accepts exactly the five baked scenario fields; the
        # scenario_id stays out of the record (it is the table key).
        result_record = result.build_result(
            {
                "parameterset_id": scenario["parameterset_id"],
                "arrival_rate": scenario["arrival_rate"],
                "service_rate": scenario["service_rate"],
                "run_duration": scenario["run_duration"],
                "seed_policy": scenario["seed_policy"],
            },
            metrics,
            effective_seed,
        )
        resultdb.insert(scenario["scenario_id"], result_record, db_config)
    except Exception as exc:  # noqa: BLE001 - any run or DB failure must fail the Pod
        sys.stderr.write("runner failed: {}\n".format(exc))
        sys.exit(1)


if __name__ == "__main__":
    main()
