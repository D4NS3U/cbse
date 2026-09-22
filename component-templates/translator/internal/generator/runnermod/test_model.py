"""Unit tests for the SimPy single-server queue model.

These exercise the exact queue horizon and no-completion behavior and local
PRNG use. They import simpy lazily and skip when simpy is not installed, so the
suite can run in the runner-base image (and locally when simpy is available)
without affecting the pure-logic tests.
"""

import random
import unittest

try:
    import simpy  # noqa: F401
    HAS_SIMPY = True
except ImportError:
    HAS_SIMPY = False


@unittest.skipUnless(HAS_SIMPY, "simpy not available")
class TestModel(unittest.TestCase):
    def test_zero_horizon_no_arrivals(self):
        import model
        rng = random.Random(1)
        completed, mean = model.run(100, 100, 0, rng)
        self.assertEqual(completed, 0)
        self.assertEqual(mean, 0.0)

    def test_no_completion_when_service_exceeds_horizon(self):
        import model
        rng = random.Random(1)
        # Frequent arrivals but mean service time far exceeds the horizon, so no
        # customer's service finishes before run_duration.
        completed, mean = model.run(arrival_rate=100, service_rate=0.001, run_duration=1, rng=rng)
        self.assertEqual(completed, 0)
        self.assertEqual(mean, 0.0)

    def test_completed_mean_is_for_completed_only(self):
        import model
        rng = random.Random(42)
        completed, mean = model.run(arrival_rate=10, service_rate=20, run_duration=100, rng=rng)
        self.assertGreater(completed, 0)
        self.assertGreaterEqual(mean, 0.0)

    def test_local_prng_determinism(self):
        import model
        r1 = random.Random(123)
        r2 = random.Random(123)
        a = model.run(10, 12, 100, r1)
        b = model.run(10, 12, 100, r2)
        self.assertEqual(a, b)

    def test_mean_zero_when_no_completion(self):
        import model
        rng = random.Random(7)
        completed, mean = model.run(100, 0.001, 1, rng)
        self.assertEqual(completed, 0)
        self.assertEqual(mean, 0.0)


if __name__ == "__main__":
    unittest.main()
