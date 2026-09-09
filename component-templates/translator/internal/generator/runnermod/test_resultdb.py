"""Unit tests for the runner Result DB client SQL/transaction structure.

These validate the table-name derivation, advisory-lock + CREATE TABLE IF NOT
EXISTS in one short transaction, the separate insert transaction, the 30-second
statement timeout, and connection close -- all against a fake connection, so no
psycopg or PostgreSQL is required. The concurrent advisory-lock serialization and
ambiguous-commit duplicate behavior are verified against a real database in the
smoke tier (S05-D07).
"""

import json
import unittest

import resultdb


class FakeCursor:
    def __init__(self, conn):
        self.conn = conn
        self.executed = []

    def execute(self, sql, params=None):
        self.executed.append((sql, params))
        self.conn.all_executed.append((sql, params))

    def __enter__(self):
        return self

    def __exit__(self, *a):
        return False


class FakeConn:
    def __init__(self):
        self.all_executed = []
        self.closed = False
        self.transactions = 0

    def cursor(self):
        return FakeCursor(self)

    def close(self):
        self.closed = True

    def __enter__(self):
        self.transactions += 1
        return self

    def __exit__(self, *a):
        return False


class TestResultDBSQL(unittest.TestCase):
    def setUp(self):
        self.conn = FakeConn()
        self._orig_connect = resultdb._connect
        resultdb._connect = lambda db_config, deadline=None: self.conn

    def tearDown(self):
        resultdb._connect = self._orig_connect

    def test_table_name(self):
        self.assertEqual(resultdb._table_name(7), "scenario_7_results")
        self.assertEqual(resultdb._table_name("7"), "scenario_7_results")

    def test_insert_sets_statement_timeout(self):
        resultdb.insert(7, {"x": 1}, {"host": "h", "port": 5432, "user": "u", "password": "p", "dbname": "d"})
        sqls = [s for s, _ in self.conn.all_executed]
        self.assertIn("SET statement_timeout = 30000", sqls)

    def test_insert_advisory_lock_and_create_table_one_transaction(self):
        resultdb.insert(7, {"x": 1}, {"host": "h", "port": 5432, "user": "u", "password": "p", "dbname": "d"})
        # Advisory lock and CREATE TABLE IF NOT EXISTS use the scenario id and table.
        lock = [e for e in self.conn.all_executed if e[0].startswith("SELECT pg_advisory_xact_lock")]
        self.assertEqual(len(lock), 1)
        self.assertEqual(lock[0][1], (7,))
        create = [e for e in self.conn.all_executed if e[0].startswith("CREATE TABLE IF NOT EXISTS scenario_7_results")]
        self.assertEqual(len(create), 1)
        self.assertIn("id BIGSERIAL PRIMARY KEY", create[0][0])
        self.assertIn("result JSONB NOT NULL", create[0][0])

    def test_insert_uses_two_transactions(self):
        resultdb.insert(7, {"x": 1}, {"host": "h", "port": 5432, "user": "u", "password": "p", "dbname": "d"})
        # Two `with conn:` blocks: lock+create, then insert.
        self.assertEqual(self.conn.transactions, 2)

    def test_insert_parameterized_jsonb(self):
        resultdb.insert(7, {"completed_customers": 8}, {"host": "h", "port": 5432, "user": "u", "password": "p", "dbname": "d"})
        ins = [e for e in self.conn.all_executed if e[0].startswith("INSERT INTO scenario_7_results")]
        self.assertEqual(len(ins), 1)
        self.assertEqual(ins[0][1], (json.dumps({"completed_customers": 8}),))

    def test_insert_closes_connection(self):
        resultdb.insert(7, {"x": 1}, {"host": "h", "port": 5432, "user": "u", "password": "p", "dbname": "d"})
        self.assertTrue(self.conn.closed)


if __name__ == "__main__":
    unittest.main()
