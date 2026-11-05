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

"""Finite-horizon two-server M/M/c+M (Erlang-A) SimPy model for the PADS paper.

``erlang_a_v1`` is CBSE's original implementation of the impatient-customer
multi-server queue documented by the Erlang-A literature (Garnett, Mandelbaum
and Reiman, 2002) and the published SimPy reneging event pattern. It is built
independently from the documented queueing primitives and SimPy 4.1.2 API
(``Resource``, timeout events, and event conditions); no third-party example
source is copied.

One replication:

1. Admits Poisson arrivals (interarrival times drawn from the injected RNG)
   from time 0 until ``measurement_end``. An arrival sampled at or after the
   boundary is not admitted.
2. Runs an unmeasured warm-up of ``max(MINIMUM_WARMUP,
   WARMUP_FRACTION * run_duration)`` time units. Warm-up customers occupy
   queue and servers and count toward server utilization, but are excluded
   from measured-customer counts and waits.
3. Measures customers arriving in ``[measurement_start, measurement_end)``,
   where the window has length ``run_duration``.
4. Stops generating arrivals at ``measurement_end`` and drains every
   admitted customer to service completion or abandonment.

Each admitted customer samples exponential patience at the fixed rate
``PATIENCE_RATE`` and exponential service at ``service_rate`` from the
injected RNG, then races the ``simpy.Resource(capacity=SERVERS)`` request
against the patience timeout. A granted request enters service immediately;
a patience timeout first cancels the pending request so the abandoned
customer consumes no capacity. An exact tie between grant and deadline
(probability zero under the continuous RNG) settles for one additional event
step and resolves in favor of the granted request.

The module uses only the injected ``rng`` (an object with
``expovariate(rate)``); it never touches Python module-global random state
and performs no event-level printing or logging.
"""

import simpy

MODEL_ID = "erlang_a_v1"
SERVERS = 2
PATIENCE_RATE = 1.0
WARMUP_FRACTION = 0.10
MINIMUM_WARMUP = 10.0

#: Exact metric fields returned by :func:`run`, in deterministic order.
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


def _require_positive_int(name, value):
    if isinstance(value, bool) or not isinstance(value, int) or value <= 0:
        raise ValueError(f"{name} must be a positive integer, got {value!r}")


def _require_rng(rng):
    if rng is None or not callable(getattr(rng, "expovariate", None)):
        raise ValueError("rng must be an injected object with an expovariate(rate) method")


def run(arrival_rate, service_rate, run_duration, rng):
    """Run one replication of the ``erlang_a_v1`` model and return its metrics.

    ``arrival_rate`` (lambda), ``service_rate`` (mu per server), and
    ``run_duration`` (measured window length T) are positive integers; booleans
    are invalid. ``rng`` is a caller-created local random object exposing
    ``expovariate(rate)``; no module-global random state is used.

    Returns a dict with exactly the :data:`METRIC_FIELDS` keys: counts are
    non-negative integers, aggregate values are finite floats, and
    ``abandonment_probability``/``server_utilization`` lie in ``[0, 1]``.
    """
    _require_positive_int("arrival_rate", arrival_rate)
    _require_positive_int("service_rate", service_rate)
    _require_positive_int("run_duration", run_duration)
    _require_rng(rng)

    warmup = max(MINIMUM_WARMUP, WARMUP_FRACTION * run_duration)
    measurement_start = warmup
    measurement_end = warmup + run_duration

    env = simpy.Environment()
    resource = simpy.Resource(env, capacity=SERVERS)

    state = {"served": 0, "abandoned": 0, "waits": [], "busy": 0.0}

    def customer(arrival_time):
        measured = measurement_start <= arrival_time < measurement_end
        patience = rng.expovariate(PATIENCE_RATE)
        service_time = rng.expovariate(service_rate)
        request = resource.request()
        race = request | env.timeout(patience)
        yield race
        if not request.triggered:
            # The patience timeout fired first. Wait one additional event step
            # so a server that frees in the very same step (an exact tie,
            # probability zero under the continuous RNG) grants the request
            # before abandonment; the tie then resolves in favor of the
            # granted request.
            yield env.timeout(0)
        if request.triggered:
            service_start = env.now
            state["busy"] += max(
                0.0,
                min(service_start + service_time, measurement_end)
                - max(service_start, measurement_start),
            )
            if measured:
                state["served"] += 1
                state["waits"].append(service_start - arrival_time)
            yield env.timeout(service_time)
            resource.release(request)
        else:
            request.cancel()
            if measured:
                state["abandoned"] += 1
                state["waits"].append(env.now - arrival_time)

    def source():
        t = 0.0
        while True:
            t += rng.expovariate(arrival_rate)
            if t >= measurement_end:
                return
            yield env.timeout(t - env.now)
            env.process(customer(t))

    env.process(source())
    env.run()

    served = state["served"]
    abandoned = state["abandoned"]
    total = served + abandoned
    waits = state["waits"]
    mean_wait = sum(waits) / len(waits) if waits else 0.0
    utilization = state["busy"] / (SERVERS * run_duration)
    return {
        "model_id": MODEL_ID,
        "completed_customers": served,
        "served_customers": served,
        "abandoned_customers": abandoned,
        "total_customers": total,
        "mean_wait_time": mean_wait,
        "abandonment_probability": abandoned / total if total else 0.0,
        "throughput": served / run_duration,
        "server_utilization": min(1.0, max(0.0, utilization)),
    }
