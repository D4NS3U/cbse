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

"""Runner Result DB client.

Connects to PostgreSQL through the database_endpoint dispatcher (shared
endpoint contract: classify, resolve, de-dup, fallback, 10-second shared
deadline, separate host/port, sslmode=disable), uses a 30-second statement
timeout, and in a SINGLE transaction obtains pg_advisory_xact_lock(0,
<scenario-id>), creates the scenario table with IF NOT EXISTS, and inserts the
JSONB result. The statement timeout, advisory lock, schema check, and insert
share one transaction because psycopg 3 closes the connection when a second
``with conn:`` transaction block opens after the first commits against
PostgreSQL 18.6, so the original two-transaction shape (lock+CREATE, then a
separate INSERT) fails with OperationalError("the connection is closed"). The
pg_advisory_xact_lock still releases at the single transaction's commit, so it
continues to serialize concurrent first-use table creation for the same
scenario. A timeout or permission failure exits non-zero and consumes the
current Pod attempt. The runner performs no inner retry.
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
    """Connect, set the statement timeout, create the scenario table under an
    advisory lock, and insert the JSONB result.

    The statement timeout, advisory lock, schema check, and insert run in a
    SINGLE transaction. psycopg 3 closes the connection when a second
    ``with conn:`` transaction block is opened after the first commits against
    PostgreSQL 18.6 (the server, or the driver, tears down the connection
    between successive explicit transactions), so the original two-transaction
    shape (advisory lock + CREATE, then a separate INSERT) fails with
    OperationalError("the connection is closed"). A single transaction keeps
    the connection alive across the whole write, and the pg_advisory_xact_lock
    still releases at that transaction's commit.
    """
    conn = _connect(db_config)
    try:
        table = _table_name(scenario_id)
        with conn:
            with conn.cursor() as cur:
                cur.execute("SET statement_timeout = 30000")
                cur.execute("SELECT pg_advisory_xact_lock(0, %s)", (int(scenario_id),))
                cur.execute(
                    "CREATE TABLE IF NOT EXISTS {table} (id BIGSERIAL PRIMARY KEY, result JSONB NOT NULL)".format(table=table)
                )
                cur.execute(
                    "INSERT INTO {table} (result) VALUES (%s)".format(table=table),
                    (json.dumps(result_obj),),
                )
    finally:
        conn.close()
