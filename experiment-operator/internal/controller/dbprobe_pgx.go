package controller

import (
	"context"
	"fmt"

	"github.com/D4NS3U/cbse/experiment-operator/internal/dbendpoint"
	"github.com/jackc/pgx/v5"
)

// pgxConnector opens a single PostgreSQL connection for the availability probe
// using pgx directly (not pgxpool), so the probe retains no pool. The connection
// runs SELECT 1 exactly once and is closed; it performs no other SQL.
type pgxConnector struct{}

// Connect implements dbendpoint.Connector by parsing a pgx config for the
// resolved host, setting the password, and opening a single connection. It
// disables TLS because the probe targets in-cluster databases over a trusted
// network and never stores the password beyond the connection.
func (pgxConnector) Connect(ctx context.Context, host string, port int32, user, password, dbname string) (dbendpoint.Conn, error) {
	cfg, err := pgx.ParseConfig(fmt.Sprintf("host=%s port=%d dbname=%s user=%s sslmode=disable", host, port, dbname, user))
	if err != nil {
		return nil, err
	}
	cfg.Password = password
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return pgxConn{c: conn}, nil
}

// pgxConn wraps a single *pgx.Conn and implements dbendpoint.Conn for the
// availability probe.
type pgxConn struct{ c *pgx.Conn }

// Ping implements dbendpoint.Conn by running SELECT 1 exactly once.
func (p pgxConn) Ping(ctx context.Context) error {
	if _, err := p.c.Exec(ctx, "SELECT 1"); err != nil {
		return err
	}
	return nil
}

// Close implements dbendpoint.Conn by closing the underlying pgx connection.
func (p pgxConn) Close(ctx context.Context) error {
	return p.c.Close(ctx)
}
