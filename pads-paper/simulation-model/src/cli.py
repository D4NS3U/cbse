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

"""Credential-free host CLI for one ``erlang_a_v1`` replication.

Accepts exactly the four scenario parameters plus an effective seed:

    python3 cli.py --arrival-rate INTEGER --service-rate INTEGER \
        --run-duration INTEGER --seed INTEGER

The first three values are positive integers; ``seed`` is a non-negative
integer. The CLI constructs ``random.Random(seed)``, runs one replication,
and writes exactly one compact, key-sorted JSON object plus a newline to
stdout containing the scenario parameters, the effective seed, and all
exact model metric fields. No credentials, database configuration, hostname
derivation, NATS, or other infrastructure input is accepted. Diagnostics go
to stderr; invalid input exits non-zero with empty stdout.
"""

import argparse
import json
import random
import sys

import model

PROG = "erlang-a"


def build_parser():
    parser = argparse.ArgumentParser(
        prog=PROG,
        description=(
            "Run one deterministic erlang_a_v1 replication and print one "
            "compact, key-sorted JSON result object to stdout."
        ),
    )
    parser.add_argument(
        "--arrival-rate",
        required=True,
        type=int,
        help="Poisson arrival rate lambda; positive integer",
    )
    parser.add_argument(
        "--service-rate",
        required=True,
        type=int,
        help="exponential service rate mu per server; positive integer",
    )
    parser.add_argument(
        "--run-duration",
        required=True,
        type=int,
        help="measured arrival window length T; positive integer",
    )
    parser.add_argument(
        "--seed",
        required=True,
        type=int,
        help="effective seed; non-negative integer",
    )
    return parser


def main(argv=None):
    args = build_parser().parse_args(argv)
    try:
        if args.seed < 0:
            raise ValueError(f"seed must be a non-negative integer, got {args.seed!r}")
        rng = random.Random(args.seed)
        metrics = model.run(
            args.arrival_rate, args.service_rate, args.run_duration, rng
        )
    except ValueError as exc:
        print(f"{PROG}: {exc}", file=sys.stderr)
        return 2

    record = {
        "arrival_rate": args.arrival_rate,
        "service_rate": args.service_rate,
        "run_duration": args.run_duration,
        "effective_seed": args.seed,
    }
    record.update(metrics)
    try:
        payload = json.dumps(record, sort_keys=True, separators=(",", ":"), allow_nan=False)
    except ValueError as exc:
        print(f"{PROG}: non-finite result value: {exc}", file=sys.stderr)
        return 1
    print(payload)
    return 0


if __name__ == "__main__":
    sys.exit(main())
