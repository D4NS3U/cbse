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

"""Unit tests for the pure seed-derivation module (runs without simpy/psycopg)."""

import unittest

import seed


class TestSeed(unittest.TestCase):
    def test_stable_for_one_hostname(self):
        a = seed.derive_seed("policy-a", "host-1")
        b = seed.derive_seed("policy-a", "host-1")
        self.assertEqual(a, b)

    def test_different_for_different_hostnames(self):
        a = seed.derive_seed("policy-a", "host-1")
        b = seed.derive_seed("policy-a", "host-2")
        self.assertNotEqual(a, b)

    def test_policy_changes_seed(self):
        self.assertNotEqual(
            seed.derive_seed("p1", "h"), seed.derive_seed("p2", "h")
        )

    def test_63bit_nonnegative(self):
        s = seed.derive_seed("policy-a", "host-1")
        self.assertGreaterEqual(s, 0)
        self.assertLess(s, 2 ** 63)

    def test_empty_hostname_raises(self):
        with self.assertRaises(SystemExit):
            seed.derive_seed("policy-a", "")


if __name__ == "__main__":
    unittest.main()
