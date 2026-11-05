// Copyright 2025-2026 Daniel Seufferth
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// pgx.go provides the real PostgreSQL connector for the design layer store
// using pgx v5. It builds a pgx.ConnConfig from separate host and port fields
// with sslmode=disable and no connection URI string concatenation. A pgx v5
// *pgx.ConnConfig must be created by pgx.ParseConfig (pgx.Connect panics on a
// hand-constructed struct literal); the keyword/value DSN passed to
// ParseConfig uses separate host and port fields and the password is set on
// the parsed config afterward so it never appears in the parsed string.
package recipestore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// PgxConnector is the real pgx v5 Connector for the Scenario Detail
// Database.
type PgxConnector struct{}

// Connect opens a pgx connection to the candidate address. It uses separate
// host and port fields, disables TLS, and applies no per-attempt timeout
// beyond the caller's context deadline.
func (PgxConnector) Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error) {
	cfg, err := pgx.ParseConfig(fmt.Sprintf("host=%s port=%d dbname=%s user=%s sslmode=disable", host, port, dbname, user))
	if err != nil {
		return nil, err
	}
	// Set the password on the parsed config so it is never part of the parsed
	// string and remains a separate driver field.
	cfg.Password = password
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return pgxConn{conn: conn}, nil
}

// pgxConn adapts *pgx.Conn to the recipestore.Conn interface.
type pgxConn struct{ conn *pgx.Conn }

func (c pgxConn) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := c.conn.Exec(ctx, sql, args...)
	return err
}

func (c pgxConn) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := c.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgxRows{rows: rows}, nil
}

func (c pgxConn) Begin(ctx context.Context) (Tx, error) {
	tx, err := c.conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return pgxTx{tx: tx}, nil
}

func (c pgxConn) Close(ctx context.Context) error {
	return c.conn.Close(ctx)
}

// pgxTx adapts pgx.Tx to the recipestore.Tx interface.
type pgxTx struct{ tx pgx.Tx }

func (t pgxTx) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := t.tx.Exec(ctx, sql, args...)
	return err
}

func (t pgxTx) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := t.tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgxRows{rows: rows}, nil
}

func (t pgxTx) Commit(ctx context.Context) error {
	return t.tx.Commit(ctx)
}

func (t pgxTx) Rollback(ctx context.Context) error {
	return t.tx.Rollback(ctx)
}

// pgxRows adapts pgx.Rows (Close() returns no value) to the recipestore.Rows
// interface.
type pgxRows struct{ rows pgx.Rows }

func (r pgxRows) Next() bool             { return r.rows.Next() }
func (r pgxRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r pgxRows) Close() error           { r.rows.Close(); return nil }
func (r pgxRows) Err() error             { return r.rows.Err() }
