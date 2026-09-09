// pgx_connector.go provides the real PostgreSQL connector for the detaildb
// lookup using pgx v5. It builds a pgx.ConnConfig from separate host and port
// fields with sslmode=disable and no connection URI string concatenation. It
// does not set a per-attempt ConnectTimeout: the shared 10-second endpoint
// contract deadline (applied by databaseendpoint.DialConn through the context)
// bounds resolution and all candidate connection attempts together, so no
// candidate receives 10 seconds of its own.
package detaildb

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PgxConnector is the real pgx v5 Connector for the Scenario Detail Database
// lookup.
type PgxConnector struct{}

// Connect opens a pgx connection to the resolved candidate address. It uses
// separate host and port fields, disables TLS, and applies no per-attempt
// timeout beyond the caller's context deadline.
func (PgxConnector) Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error) {
	cfg := pgx.ConnConfig{
		Config: pgconn.Config{
			Host:          host,
			Port:          uint16(port),
			Database:      dbname,
			User:          user,
			Password:      password,
			TLSConfig:     nil, // nil disables TLS (sslmode=disable)
			RuntimeParams: map[string]string{"sslmode": "disable"},
		},
	}
	conn, err := pgx.ConnectConfig(ctx, &cfg)
	if err != nil {
		return nil, err
	}
	return pgxConn{conn: conn}, nil
}

// pgxConn adapts *pgx.Conn to the detaildb.Conn interface.
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

func (c pgxConn) Close(ctx context.Context) error {
	return c.conn.Close(ctx)
}

// pgxRows adapts pgx.Rows (Close() returns no value) to the detaildb.Rows
// interface (Close() returns an error).
type pgxRows struct{ rows pgx.Rows }

func (r pgxRows) Next() bool             { return r.rows.Next() }
func (r pgxRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r pgxRows) Close() error           { r.rows.Close(); return nil }
func (r pgxRows) Err() error             { return r.rows.Err() }
