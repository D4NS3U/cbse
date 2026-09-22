"""Unit tests for the pure result-record builder (runs without simpy/psycopg)."""

import unittest

import result


class TestResult(unittest.TestCase):
    def _scenario(self):
        return {
            "scenario_id": 7,
            "parameterset_id": 5,
            "arrival_rate": 10,
            "service_rate": 12,
            "run_duration": 100,
            "seed_policy": "stable-seed",
        }

    def test_build_result_has_exact_fields(self):
        rec = result.build_result(self._scenario(), 8, 0.25, 12345)
        self.assertEqual(set(rec.keys()), set(result.RESULT_FIELDS))

    def test_build_result_values(self):
        rec = result.build_result(self._scenario(), 8, 0.25, 12345)
        self.assertEqual(rec["parameterset_id"], 5)
        self.assertEqual(rec["arrival_rate"], 10)
        self.assertEqual(rec["service_rate"], 12)
        self.assertEqual(rec["run_duration"], 100)
        self.assertEqual(rec["seed_policy"], "stable-seed")
        self.assertEqual(rec["effective_seed"], 12345)
        self.assertEqual(rec["completed_customers"], 8)
        self.assertEqual(rec["mean_wait_time"], 0.25)

    def test_build_result_excludes_scenario_id_and_credentials(self):
        rec = result.build_result(self._scenario(), 0, 0.0, 1)
        self.assertNotIn("scenario_id", rec)
        self.assertNotIn("host", rec)
        self.assertNotIn("password", rec)


if __name__ == "__main__":
    unittest.main()
