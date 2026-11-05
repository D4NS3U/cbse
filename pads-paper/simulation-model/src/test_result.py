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

"""Unit tests for the strict ``erlang_a_v1`` result-record builder.

Covers the deterministic output field order, successful construction, and
every ruled malformed-result rejection: missing/unexpected fields, wrong
model id, bad count and aggregate types, non-finite values, out-of-domain
values, inconsistent accounting, alias mismatch, and bad scenario/seed
inputs. Runs without simpy or psycopg.
"""

import unittest

import result

AGGREGATE_METRIC_KEYS = (
    "mean_wait_time",
    "abandonment_probability",
    "throughput",
    "server_utilization",
)


class TestResult(unittest.TestCase):
    def _scenario(self):
        return {
            "parameterset_id": 5,
            "arrival_rate": 10,
            "service_rate": 12,
            "run_duration": 100,
            "seed_policy": 2027,
        }

    def _metrics(self, **overrides):
        metrics = {
            "model_id": "erlang_a_v1",
            "completed_customers": 3,
            "served_customers": 3,
            "abandoned_customers": 2,
            "total_customers": 5,
            "mean_wait_time": 1.25,
            "abandonment_probability": 0.4,
            "throughput": 0.03,
            "server_utilization": 0.75,
        }
        metrics.update(overrides)
        return metrics

    def test_result_fields_enumerate_deterministic_order(self):
        self.assertEqual(
            result.RESULT_FIELDS,
            (
                "parameterset_id",
                "arrival_rate",
                "service_rate",
                "run_duration",
                "seed_policy",
                "effective_seed",
                "model_id",
                "completed_customers",
                "served_customers",
                "abandoned_customers",
                "total_customers",
                "mean_wait_time",
                "abandonment_probability",
                "throughput",
                "server_utilization",
            ),
        )

    def test_build_result_success_exact_fields_and_order(self):
        rec = result.build_result(self._scenario(), self._metrics(), 12345)
        self.assertEqual(list(rec.keys()), list(result.RESULT_FIELDS))
        self.assertEqual(rec["parameterset_id"], 5)
        self.assertEqual(rec["arrival_rate"], 10)
        self.assertEqual(rec["service_rate"], 12)
        self.assertEqual(rec["run_duration"], 100)
        self.assertEqual(rec["seed_policy"], 2027)
        self.assertEqual(rec["effective_seed"], 12345)
        self.assertEqual(rec["model_id"], "erlang_a_v1")
        self.assertEqual(rec["completed_customers"], 3)
        self.assertEqual(rec["served_customers"], 3)
        self.assertEqual(rec["abandoned_customers"], 2)
        self.assertEqual(rec["total_customers"], 5)
        self.assertEqual(rec["mean_wait_time"], 1.25)
        self.assertEqual(rec["abandonment_probability"], 0.4)
        self.assertEqual(rec["throughput"], 0.03)
        self.assertEqual(rec["server_utilization"], 0.75)

    def test_build_result_excludes_scenario_id_and_credentials(self):
        rec = result.build_result(self._scenario(), self._metrics(), 1)
        for forbidden in (
            "scenario_id",
            "host",
            "port",
            "user",
            "password",
            "sslmode",
            "dsn",
            "nats_url",
        ):
            self.assertNotIn(forbidden, rec)

    def _reject_metric(self, key, value):
        metrics = self._metrics(**{key: value})
        with self.assertRaises(ValueError, msg=f"{key}={value!r}"):
            result.build_result(self._scenario(), metrics, 12345)

    def test_rejects_missing_metric_field(self):
        for key in result.METRIC_FIELDS:
            metrics = self._metrics()
            del metrics[key]
            with self.assertRaises(ValueError, msg=f"missing {key}"):
                result.build_result(self._scenario(), metrics, 12345)

    def test_rejects_unexpected_metric_field(self):
        self._reject_metric("extra_metric", 0.0)

    def test_rejects_non_dict_metrics(self):
        for bad in (None, ["erlang_a_v1"], "metrics", 42):
            with self.assertRaises(ValueError, msg=f"metrics={bad!r}"):
                result.build_result(self._scenario(), bad, 12345)

    def test_rejects_wrong_model_id(self):
        self._reject_metric("model_id", "erlang_a_v0")
        self._reject_metric("model_id", "mm1_v1")

    def test_rejects_bad_count_types_and_domains(self):
        for key in (
            "completed_customers",
            "served_customers",
            "abandoned_customers",
            "total_customers",
        ):
            for bad in (True, False, 1.5, -1, "3", None):
                self._reject_metric(key, bad)

    def test_rejects_non_finite_and_non_float_aggregates(self):
        for key in AGGREGATE_METRIC_KEYS:
            for bad in (float("nan"), float("inf"), float("-inf"), 0, "0.0", True):
                self._reject_metric(key, bad)

    def test_rejects_negative_or_unbounded_aggregates(self):
        self._reject_metric("mean_wait_time", -1e-9)
        self._reject_metric("throughput", -0.001)
        self._reject_metric("abandonment_probability", 1.0000001)
        self._reject_metric("abandonment_probability", -1e-9)
        self._reject_metric("server_utilization", 1.5)
        self._reject_metric("server_utilization", -0.5)

    def test_rejects_inconsistent_accounting(self):
        # served + abandoned = 5 != total 4
        self._reject_metric("total_customers", 4)
        # alias mismatch: completed != served
        self._reject_metric("completed_customers", 2)

    def test_rejects_missing_scenario_field(self):
        for key in result.SCENARIO_FIELDS:
            scenario = self._scenario()
            del scenario[key]
            with self.assertRaises(ValueError, msg=f"missing {key}"):
                result.build_result(scenario, self._metrics(), 12345)

    def test_rejects_unexpected_scenario_field(self):
        for extra in ("scenario_id", "password", "extra"):
            scenario = self._scenario()
            scenario[extra] = "x"
            with self.assertRaises(ValueError, msg=f"unexpected {extra}"):
                result.build_result(scenario, self._metrics(), 12345)

    def test_rejects_non_dict_scenario(self):
        for bad in (None, [1, 2], "scenario"):
            with self.assertRaises(ValueError, msg=f"scenario={bad!r}"):
                result.build_result(bad, self._metrics(), 12345)

    def test_rejects_bad_scenario_value_types_and_domains(self):
        for key, bad in (
            ("parameterset_id", True),
            ("parameterset_id", -1),
            ("parameterset_id", 1.0),
            ("arrival_rate", 0),
            ("arrival_rate", -1),
            ("arrival_rate", "10"),
            ("service_rate", 1.5),
            ("run_duration", "100"),
            ("seed_policy", -1),
            ("seed_policy", 1.0),
        ):
            scenario = self._scenario()
            scenario[key] = bad
            with self.assertRaises(ValueError, msg=f"{key}={bad!r}"):
                result.build_result(scenario, self._metrics(), 12345)

    def test_rejects_bad_effective_seed(self):
        for bad in (None, True, False, -1, 1.5, "12345"):
            with self.assertRaises(ValueError, msg=f"effective_seed={bad!r}"):
                result.build_result(self._scenario(), self._metrics(), bad)


if __name__ == "__main__":
    unittest.main()
