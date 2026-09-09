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

type pgxConn struct{ c *pgx.Conn }

func (p pgxConn) Ping(ctx context.Context) error {
	if _, err := p.c.Exec(ctx, "SELECT 1"); err != nil {
		return err
	}
	return nil
}

func (p pgxConn) Close(ctx context.Context) error {
	return p.c.Close(ctx)
}
