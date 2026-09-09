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
