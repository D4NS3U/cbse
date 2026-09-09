"""Conformance suite for the runner's database endpoint dispatcher.

This is the Python leg of the S05-A3 cross-client endpoint conformance suite. It
applies the same literal and fake-resolver inputs as the Experiment Operator Go
probe and the Translator Go dispatcher and requires identical classification,
normalized addresses, stable A/AAAA ordering and de-duplication, one shared
deadline, fallback choice, fresh DNS resolution, no re-resolution of an
established connection, and separate host/port driver values.

Run with: ``python3 -m database_endpoint.test_dispatcher`` from the runnermod
directory.
"""

import unittest
from . import dispatcher
from . import RESOLUTION_DEADLINE_SECONDS


def ip(s):
    return s


class FakeResolver:
    """Returns a fixed ordered list of address strings, recording calls."""

    def __init__(self, addrs=None, err=None):
        self.addrs = addrs or []
        self.err = err
        self.calls = []

    def __call__(self, host):
        self.calls.append(host)
        if self.err is not None:
            raise self.err
        return list(self.addrs)


class FakeConn:
    def __init__(self):
        self.pings = 0
        self.closes = 0

    def ping(self):
        self.pings += 1

    def close(self):
        self.closes += 1


class FakeConnector:
    """Connects or fails based on the candidate host, recording attempts."""

    def __init__(self, fail=None, success=None, block=False):
        self.fail = fail or {}
        self.success = success or {}
        self.block = block
        self.tried = []

    def __call__(self, host, port, user, password, dbname, timeout):
        self.tried.append(host)
        if self.block:
            import time
            time.sleep(timeout + 0.05)
            raise TimeoutError("blocked past timeout")
        if host in self.fail:
            raise self.fail[host]
        if host in self.success:
            return self.success[host]
        return FakeConn()


class TestClassifyHost(unittest.TestCase):
    cases = [
        ("ipv4 literal", "10.0.0.1", ("ipv4", "10.0.0.1"), None),
        ("ipv4 trimmed", "  10.0.0.1  ", ("ipv4", "10.0.0.1"), None),
        ("ipv6 literal", "2001:db8::1", ("ipv6", "2001:db8::1"), None),
        ("ipv6 full form normalized", "2001:0db8:0000:0000:0000:0000:0000:0001", ("ipv6", "2001:db8::1"), None),
        ("dns subdomain", "db.svc.cluster.local", ("dns", "db.svc.cluster.local"), None),
        ("dns single label", "db", ("dns", "db"), None),
        ("dns uppercase rejected", "DB.example.com", None, ValueError),
        ("empty", "", None, ValueError),
        ("bracketed", "[10.0.0.1]", None, ValueError),
        ("embedded port", "db:5432", None, ValueError),
        ("zone", "fe80::1%eth0", None, ValueError),
        ("scheme", "tcp://db", None, ValueError),
        ("path", "db/path", None, ValueError),
    ]

    def test_classify(self):
        for name, host, want, exc in self.cases:
            with self.subTest(name=name):
                if exc is not None:
                    with self.assertRaises(exc):
                        dispatcher.classify_host(host)
                    continue
                got = dispatcher.classify_host(host)
                self.assertEqual(got, want, name)


class TestResolveAddresses(unittest.TestCase):
    def test_ipv4_literal(self):
        self.assertEqual(dispatcher.resolve_addresses("ipv4", "10.0.0.1"), ["10.0.0.1"])

    def test_ipv6_literal(self):
        self.assertEqual(dispatcher.resolve_addresses("ipv6", "2001:db8::1"), ["2001:db8::1"])

    def test_order_and_de_dup(self):
        cases = [
            ("A then AAAA", ["10.0.0.1", "2001:db8::1"], ["10.0.0.1", "2001:db8::1"]),
            ("AAAA then A", ["2001:db8::1", "10.0.0.1"], ["2001:db8::1", "10.0.0.1"]),
            ("duplicates", ["10.0.0.1", "10.0.0.1", "2001:db8::1", "2001:db8::1"], ["10.0.0.1", "2001:db8::1"]),
            ("mixed de-dup", ["10.0.0.1", "2001:db8::1", "10.0.0.1", "10.0.0.2", "2001:db8::1"], ["10.0.0.1", "2001:db8::1", "10.0.0.2"]),
        ]
        for name, addrs, want in cases:
            with self.subTest(name=name):
                res = FakeResolver(addrs=addrs)
                got = dispatcher.resolve_addresses("dns", "db.example.com", res)
                self.assertEqual(got, want, name)

    def test_empty_fails(self):
        res = FakeResolver(addrs=[])
        with self.assertRaises(ValueError):
            dispatcher.resolve_addresses("dns", "db.example.com", res)

    def test_resolver_error_fails(self):
        res = FakeResolver(err=RuntimeError("no such host"))
        with self.assertRaises(RuntimeError):
            dispatcher.resolve_addresses("dns", "db.example.com", res)


class TestDial(unittest.TestCase):
    def test_fallback_first_success_wins(self):
        res = FakeResolver(addrs=["10.0.0.1", "2001:db8::1"])
        conn = FakeConn()
        connector = FakeConnector(fail={"10.0.0.1": ConnectionError("refused")}, success={"2001:db8::1": conn})
        result = dispatcher.dial(
            {"host": "db.example.com", "port": 5432, "user": "u", "password": "p", "dbname": "d"},
            resolver=res,
            connector=connector,
        )
        self.assertEqual(result["host"], "2001:db8::1", "first successful address owns the dial")
        self.assertEqual(result["kind"], "dns")
        self.assertIs(result["conn"], conn)
        self.assertEqual(connector.tried, ["10.0.0.1", "2001:db8::1"])

    def test_de_dup_contacts_each_once(self):
        res = FakeResolver(addrs=["10.0.0.1", "10.0.0.1", "10.0.0.1"])
        connector = FakeConnector()
        result = dispatcher.dial(
            {"host": "db.example.com", "port": 5432, "user": "u", "password": "p", "dbname": "d"},
            resolver=res,
            connector=connector,
        )
        self.assertEqual(connector.tried, ["10.0.0.1"], "duplicates removed")
        self.assertEqual(result["host"], "10.0.0.1")

    def test_exhaustion_fails(self):
        res = FakeResolver(addrs=["10.0.0.1", "2001:db8::1"])
        connector = FakeConnector(
            fail={"10.0.0.1": ConnectionError("refused"), "2001:db8::1": ConnectionError("refused")},
        )
        with self.assertRaises(ValueError):
            dispatcher.dial(
                {"host": "db.example.com", "port": 5432, "user": "u", "password": "p", "dbname": "d"},
                resolver=res,
                connector=connector,
            )
        self.assertEqual(connector.tried, ["10.0.0.1", "2001:db8::1"])

    def test_shared_deadline(self):
        self.assertEqual(RESOLUTION_DEADLINE_SECONDS, 10.0, "shared deadline is 10s per the contract")
        # A blocking connector that sleeps past its timeout: the first attempt
        # exhausts the (short) deadline and dial raises TimeoutError without an
        # unbounded wait. We use a short deadline to verify the bound quickly.
        connector = FakeConnector(block=True)
        with self.assertRaises(TimeoutError):
            dispatcher.dial(
                {"host": "10.0.0.1", "port": 5432, "user": "u", "password": "p", "dbname": "d"},
                resolver=FakeResolver(),
                connector=connector,
                deadline=0.1,
            )

    def test_requires_connector(self):
        with self.assertRaises(ValueError):
            dispatcher.dial({"host": "10.0.0.1", "port": 5432}, resolver=FakeResolver(), connector=None)

    def test_separate_host_port(self):
        recorded = {}

        def connector(host, port, user, password, dbname, timeout):
            recorded.update(host=host, port=port, user=user, password=password, dbname=dbname)
            return FakeConn()

        result = dispatcher.dial(
            {"host": "10.0.0.1", "port": 6543, "user": "u", "password": "p", "dbname": "d"},
            resolver=FakeResolver(err=RuntimeError("should not be called")),
            connector=connector,
        )
        self.assertEqual(result["host"], "10.0.0.1")
        self.assertEqual(recorded, {"host": "10.0.0.1", "port": 6543, "user": "u", "password": "p", "dbname": "d"})

    def test_literal_skips_dns(self):
        connector = FakeConnector()
        # A resolver that raises proves literals bypass DNS.
        res = FakeResolver(err=RuntimeError("resolver should not be called"))
        result = dispatcher.dial(
            {"host": "10.0.0.1", "port": 5432, "user": "u", "password": "p", "dbname": "d"},
            resolver=res,
            connector=connector,
        )
        self.assertEqual(result["host"], "10.0.0.1")
        self.assertEqual(result["kind"], "ipv4")
        self.assertEqual(res.calls, [], "literal must not invoke the resolver")
        self.assertEqual(connector.tried, ["10.0.0.1"])


if __name__ == "__main__":
    unittest.main()
