"""Runner Result DB client.

Connects to PostgreSQL through the database_endpoint dispatcher (shared
endpoint contract: classify, resolve, de-dup, fallback, 10-second shared
deadline, separate host/port, sslmode=disable), uses a 30-second statement
timeout, obtains pg_advisory_xact_lock(0, <scenario-id>) and creates the
scenario table with IF NOT EXISTS in one short transaction, then inserts the
JSONB result in a separate transaction. A timeout or permission failure exits
non-zero and consumes the current Pod attempt. The runner performs no inner
retry.
"""

import json

from database_endpoint import dispatcher


def _connect(db_config, deadline=None):
    """Dial the Result DB through the shared endpoint contract and return a psycopg connection."""
    import psycopg  # imported lazily so the module's SQL/table logic is testable without the driver

    def connector(host, port, user, password, dbname, timeout):
        connect_timeout = int(timeout) if int(timeout) >= 1 else 1
        return psycopg.connect(
            host=host,
            port=port,
            user=user,
            password=password,
            dbname=dbname,
            sslmode="disable",
            connect_timeout=connect_timeout,
        )

    ep = {
        "host": db_config["host"],
        "port": int(db_config["port"]),
        "user": db_config["user"],
        "password": db_config["password"],
        "dbname": db_config["dbname"],
    }
    result = dispatcher.dial(ep, connector=connector, deadline=deadline)
    return result["conn"]


def _table_name(scenario_id):
    return "scenario_{}_results".format(int(scenario_id))


def insert(scenario_id, result_obj, db_config):
    """Connect, create the scenario table under an advisory lock, and insert the JSONB result."""
    conn = _connect(db_config)
    try:
        with conn.cursor() as cur:
            cur.execute("SET statement_timeout = 30000")
        table = _table_name(scenario_id)
        # Short transaction: advisory lock + schema check, commit immediately.
        with conn:
            with conn.cursor() as cur:
                cur.execute("SELECT pg_advisory_xact_lock(0, %s)", (int(scenario_id),))
                cur.execute(
                    "CREATE TABLE IF NOT EXISTS {table} (id BIGSERIAL PRIMARY KEY, result JSONB NOT NULL)".format(table=table)
                )
        # Separate transaction for the result insert.
        with conn:
            with conn.cursor() as cur:
                cur.execute(
                    "INSERT INTO {table} (result) VALUES (%s)".format(table=table),
                    (json.dumps(result_obj),),
                )
    finally:
        conn.close()
