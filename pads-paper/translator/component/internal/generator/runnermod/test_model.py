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

"""Unit tests for the canonical ``erlang_a_v1`` SimPy model.

Covers the exact constants and result-key set, input validation (including
boolean rejection), seed-replay determinism, accounting and bound
invariants, zero-arrival zeros, scripted warm-up/drain/tie/concurrency/
abandonment semantics with a scripted RNG, robust fixed-seed ensembles, and
static invariants that ``model.py`` never uses module-global random state and
never prints. Tests import simpy lazily and skip when simpy is not
installed, so the pure-logic tests can run anywhere.
"""

import ast
import math
import os
import random
import unittest

try:
    import simpy  # noqa: F401
    HAS_SIMPY = True
except ImportError:
    HAS_SIMPY = False

EXPECTED_METRIC_KEYS = {
    "model_id",
    "completed_customers",
    "served_customers",
    "abandoned_customers",
    "total_customers",
    "mean_wait_time",
    "abandonment_probability",
    "throughput",
    "server_utilization",
}

COUNT_KEYS = (
    "completed_customers",
    "served_customers",
    "abandoned_customers",
    "total_customers",
)

AGGREGATE_KEYS = (
    "mean_wait_time",
    "abandonment_probability",
    "throughput",
    "server_utilization",
)


class ScriptedRNG:
    """Deterministic stand-in for the injected RNG (duck-typed contract)."""

    def __init__(self, values):
        self._values = list(values)
        self.draws = []

    def expovariate(self, rate):
        value = self._values.pop(0)
        self.draws.append((rate, value))
        return value


def scripted_values(spec, tail=1e9):
    """Build the flat draw list for the model's fixed draw order.

    ``spec`` is ``((interarrival, patience, service), ...)`` for admitted
    customers in arrival order. The model draws interarrival #1, then — for
    each customer i — interarrival #(i+1), patience i, and service i; the
    final interarrival (``tail``) is sampled after the last admitted
    customer and must exceed the measured-window end so the source stops.
    """
    values = [spec[0][0]]
    for i in range(len(spec)):
        nxt = tail if i + 1 >= len(spec) else spec[i + 1][0]
        values.extend([nxt, spec[i][1], spec[i][2]])
    return values


@unittest.skipUnless(HAS_SIMPY, "simpy not available")
class TestModelConstants(unittest.TestCase):
    def test_exact_constants(self):
        import model

        self.assertEqual(model.MODEL_ID, "erlang_a_v1")
        self.assertEqual(model.SERVERS, 2)
        self.assertEqual(model.PATIENCE_RATE, 1.0)
        self.assertEqual(model.WARMUP_FRACTION, 0.10)
        self.assertEqual(model.MINIMUM_WARMUP, 10.0)

    def test_metric_fields_constant_matches_result_keys(self):
        import model

        rng = random.Random(1)
        res = model.run(3, 4, 100, rng)
        self.assertEqual(set(res), set(model.METRIC_FIELDS))
        self.assertEqual(list(res), list(model.METRIC_FIELDS))


@unittest.skipUnless(HAS_SIMPY, "simpy not available")
class TestModelResultShape(unittest.TestCase):
    def test_exact_result_key_set_and_types(self):
        import model

        res = model.run(3, 4, 100, random.Random(1))
        self.assertEqual(set(res), EXPECTED_METRIC_KEYS)
        self.assertIsInstance(res["model_id"], str)
        for key in COUNT_KEYS:
            self.assertIsInstance(res[key], int)
            self.assertNotIsInstance(res[key], bool)
            self.assertGreaterEqual(res[key], 0)
        for key in AGGREGATE_KEYS:
            self.assertIsInstance(res[key], float)
            self.assertTrue(math.isfinite(res[key]))

    def test_same_inputs_and_seed_identical_metrics(self):
        import model

        a = model.run(7, 5, 100, random.Random(42))
        b = model.run(7, 5, 100, random.Random(42))
        self.assertEqual(a, b)

    def test_distinct_seeds_exercise_distinct_outputs(self):
        import model

        outputs = set()
        for seed in (1, 2, 3, 4, 5, 6, 7, 8):
            res = model.run(3, 4, 100, random.Random(seed))
            outputs.add(tuple(res.values()))
        # A fixed seed set must exercise more than one output; it need not
        # be that every pair of seeds differs.
        self.assertGreater(len(outputs), 1)


@unittest.skipUnless(HAS_SIMPY, "simpy not available")
class TestModelValidation(unittest.TestCase):
    def _run(self, **overrides):
        import model

        kwargs = {"arrival_rate": 5, "service_rate": 5, "run_duration": 50}
        kwargs.update(overrides)
        rng = kwargs.pop("rng", None)
        if rng is None:
            rng = ScriptedRNG([1e9])
        return model.run(rng=rng, **kwargs)

    def test_accepts_valid_inputs(self):
        res = self._run()
        self.assertEqual(res["total_customers"], 0)

    def test_rejects_non_positive_and_non_integer_inputs(self):
        for bad in (0, -1, 1.5, "5", None, True, False):
            with self.assertRaises(ValueError, msg=f"arrival_rate={bad!r}"):
                self._run(arrival_rate=bad)
            with self.assertRaises(ValueError, msg=f"service_rate={bad!r}"):
                self._run(service_rate=bad)
            with self.assertRaises(ValueError, msg=f"run_duration={bad!r}"):
                self._run(run_duration=bad)

    def test_rejects_invalid_rng(self):
        import model

        for bad in (None, object(), 42):
            with self.assertRaises(ValueError, msg=f"rng={bad!r}"):
                model.run(5, 5, 50, bad)


@unittest.skipUnless(HAS_SIMPY, "simpy not available")
class TestModelInvariants(unittest.TestCase):
    def test_accounting_finiteness_and_bounds(self):
        import model

        for lam, mu, dur in ((2, 5, 100), (8, 4, 150), (20, 5, 200), (1, 2, 100)):
            for seed in (11, 22, 33):
                res = model.run(lam, mu, dur, random.Random(seed))
                self.assertEqual(
                    res["served_customers"] + res["abandoned_customers"],
                    res["total_customers"],
                    msg=f"lambda={lam} mu={mu} T={dur} seed={seed}",
                )
                self.assertEqual(
                    res["completed_customers"], res["served_customers"]
                )
                for key in AGGREGATE_KEYS:
                    self.assertTrue(math.isfinite(res[key]), msg=key)
                self.assertGreaterEqual(res["mean_wait_time"], 0.0)
                self.assertGreaterEqual(res["throughput"], 0.0)
                self.assertGreaterEqual(res["abandonment_probability"], 0.0)
                self.assertLessEqual(res["abandonment_probability"], 1.0)
                self.assertGreaterEqual(res["server_utilization"], 0.0)
                self.assertLessEqual(res["server_utilization"], 1.0)


@unittest.skipUnless(HAS_SIMPY, "simpy not available")
class TestModelScriptedScenarios(unittest.TestCase):
    def test_zero_arrivals_finite_zeros(self):
        import model

        # The single sampled interarrival exceeds the measured window end, so
        # no customer is admitted at all.
        res = model.run(5, 5, 100, ScriptedRNG([1e9]))
        self.assertEqual(res["total_customers"], 0)
        self.assertEqual(res["served_customers"], 0)
        self.assertEqual(res["abandoned_customers"], 0)
        self.assertEqual(res["completed_customers"], 0)
        self.assertEqual(res["mean_wait_time"], 0.0)
        self.assertEqual(res["abandonment_probability"], 0.0)
        self.assertEqual(res["throughput"], 0.0)
        self.assertEqual(res["server_utilization"], 0.0)

    def test_warmup_customers_affect_utilization_not_measured_counts(self):
        import model

        # T=100 -> measurement window [10, 110). A warm-up customer arriving
        # at 1.0 occupies a server on [1.0, 31.0); 21.0 of that lies inside
        # the measurement window. No customer arrives inside the window.
        res = model.run(5, 5, 100, ScriptedRNG(scripted_values([(1.0, 1000.0, 30.0)])))
        self.assertEqual(res["total_customers"], 0)
        self.assertEqual(res["mean_wait_time"], 0.0)
        self.assertEqual(res["abandonment_probability"], 0.0)
        self.assertEqual(res["throughput"], 0.0)
        # Busy overlap [10, 31) = 21.0 over 2 servers x 100 time units.
        self.assertEqual(res["server_utilization"], 21.0 / (2 * 100))

    def test_warmup_fraction_window_for_long_runs(self):
        import model

        # T=200 -> warm-up = max(10, 0.10 * 200) = 20; window [20, 220).
        # The arrival at 15.0 is warm-up (excluded); the arrival at 25.0 is
        # measured and served immediately.
        rng = ScriptedRNG(scripted_values([(15.0, 1000.0, 10.0), (10.0, 1000.0, 10.0)]))
        res = model.run(5, 5, 200, rng)
        self.assertEqual(res["total_customers"], 1)
        self.assertEqual(res["served_customers"], 1)
        self.assertEqual(res["abandoned_customers"], 0)
        self.assertEqual(res["mean_wait_time"], 0.0)

    def test_drain_accounts_outcomes_after_window(self):
        import model

        # T=50 -> window [10, 60). A measured customer arriving at 59.5 is
        # served on [59.5, 1059.5): its completion occurs long after the
        # window ends, but the drain must still account for it.
        rng = ScriptedRNG(scripted_values([(59.5, 1000.0, 1000.0)]))
        res = model.run(5, 5, 50, rng)
        self.assertEqual(res["served_customers"], 1)
        self.assertEqual(res["abandoned_customers"], 0)
        self.assertEqual(res["total_customers"], 1)
        self.assertEqual(res["mean_wait_time"], 0.0)
        self.assertEqual(res["throughput"], 1 / 50)
        # Busy overlap is truncated at the window end: [59.5, 60) = 0.5.
        self.assertEqual(res["server_utilization"], 0.5 / (2 * 50))

    def test_abandonment_releases_queue_without_ghost_capacity(self):
        import model

        # T=50 -> window [10, 60).
        #   c1 arrives at 10.0, occupies a server on [10, 110)
        #   c2 arrives at 11.0, occupies a server on [11, 111)
        #   c3 arrives at 12.0, patience 5.0 -> abandons at 17.0
        #   c4 arrives at 50.0, queued while both servers are busy
        # c1 frees its server at 110.0. With c3's pending request cancelled,
        # c4 is served there (wait 60.0). A ghost pending request would be
        # granted first, delaying c4 to 111.0 (wait 61.0) and changing
        # mean_wait_time from 16.25 to 16.5.
        rng = ScriptedRNG(
            scripted_values(
                [
                    (10.0, 1000.0, 100.0),
                    (1.0, 1000.0, 100.0),
                    (1.0, 5.0, 100.0),
                    (38.0, 1000.0, 10.0),
                ]
            )
        )
        res = model.run(5, 5, 50, rng)
        self.assertEqual(res["served_customers"], 3)
        self.assertEqual(res["abandoned_customers"], 1)
        self.assertEqual(res["total_customers"], 4)
        self.assertEqual(res["mean_wait_time"], (0.0 + 0.0 + 5.0 + 60.0) / 4)
        self.assertEqual(res["abandonment_probability"], 0.25)
        self.assertEqual(res["throughput"], 3 / 50)
        self.assertEqual(res["server_utilization"], (50.0 + 49.0) / 100.0)

    def test_two_server_concurrency_scripted(self):
        import model

        # T=50 -> window [10, 60). Four customers arrive at 10, 11, 12, 13
        # with service times of 10. With two servers the waits are
        # 0, 0, 8, 8; a single server would give 0, 0, 9, 19.
        rng = ScriptedRNG(
            scripted_values(
                [
                    (10.0, 1000.0, 10.0),
                    (1.0, 1000.0, 10.0),
                    (1.0, 1000.0, 10.0),
                    (1.0, 1000.0, 10.0),
                ]
            )
        )
        res = model.run(5, 5, 50, rng)
        self.assertEqual(res["served_customers"], 4)
        self.assertEqual(res["abandoned_customers"], 0)
        self.assertEqual(res["total_customers"], 4)
        self.assertEqual(res["mean_wait_time"], (0.0 + 0.0 + 8.0 + 8.0) / 4)
        self.assertEqual(res["server_utilization"], 40.0 / 100.0)

    def test_exact_tie_resolves_in_favor_of_granted_request(self):
        import model

        # T=100 -> window [10, 110).
        #   b1 arrives at 0.5 (warm-up), occupies a server on [0.5, 20.0)
        #   b2 arrives at 1.0 (warm-up), occupies a server on [1.0, 51.0)
        #   c  arrives at 11.0 (measured), patience 9.0 -> deadline 20.0,
        #      exactly when b1 finishes: an exact grant/timeout tie.
        # The zero-probability tie must resolve in favor of the granted
        # request; a timeout-favoring resolution would count c as abandoned.
        rng = ScriptedRNG(
            scripted_values(
                [
                    (0.5, 1000.0, 19.5),
                    (0.5, 1000.0, 50.0),
                    (10.0, 9.0, 50.0),
                ]
            )
        )
        res = model.run(5, 5, 100, rng)
        self.assertEqual(res["served_customers"], 1)
        self.assertEqual(res["abandoned_customers"], 0)
        self.assertEqual(res["total_customers"], 1)
        self.assertEqual(res["mean_wait_time"], 9.0)


@unittest.skipUnless(HAS_SIMPY, "simpy not available")
class TestModelEnsembles(unittest.TestCase):
    """Fixed-seed ensembles with strongly separated configurations.

    These assert ensemble-level trends over a robust fixed seed set, not
    per-replication monotonicity.
    """

    SEEDS = (1, 2, 3, 4, 5, 6, 7, 8)

    def _ensemble(self, lam, mu, dur=200):
        import model

        results = [model.run(lam, mu, dur, random.Random(seed)) for seed in self.SEEDS]
        n = len(results)
        return (
            sum(r["mean_wait_time"] for r in results) / n,
            sum(r["abandonment_probability"] for r in results) / n,
            results,
        )

    def test_higher_arrival_increases_waiting_and_abandonment(self):
        low_wait, low_aban, low_results = self._ensemble(lam=1, mu=5)
        high_wait, high_aban, high_results = self._ensemble(lam=20, mu=5)
        for res in low_results + high_results:
            self.assertGreater(res["total_customers"], 0)
        self.assertGreater(high_wait, low_wait)
        self.assertGreater(high_aban, low_aban)

    def test_higher_service_decreases_waiting_and_abandonment(self):
        slow_wait, slow_aban, slow_results = self._ensemble(lam=6, mu=2)
        fast_wait, fast_aban, fast_results = self._ensemble(lam=6, mu=12)
        for res in slow_results + fast_results:
            self.assertGreater(res["total_customers"], 0)
        self.assertLess(fast_wait, slow_wait)
        self.assertLess(fast_aban, slow_aban)


class TestModelModuleHygiene(unittest.TestCase):
    """Static source invariants for model.py (run without simpy)."""

    def _module_source(self):
        path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "model.py")
        with open(path, "r", encoding="utf-8") as fh:
            return fh.read()

    def test_no_module_global_random_use(self):
        tree = ast.parse(self._module_source())
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                self.assertNotIn("random", [alias.name for alias in node.names])
            elif isinstance(node, ast.ImportFrom):
                self.assertNotEqual(node.module, "random")

    def test_no_event_level_logging(self):
        tree = ast.parse(self._module_source())
        for node in ast.walk(tree):
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
                self.assertNotEqual(node.func.id, "print")


if __name__ == "__main__":
    unittest.main()
