// Package persistence implements the alpha4 Core DB persistence layer for the
// Scenario Manager: the namespace/name project table, the publication-boundary
// scenario-status transitions, and the terminal-action bulk update.
//
// The alpha4 project table is keyed by the static (project_namespace,
// project_name) pair and has no mutable status, component-count, or
// experiment-UID columns. The scenario_status table adds the operational
// translation_publish_started_at field. Existing tables are validation-only:
// the package never issues ALTER TABLE repair; an incompatible installation
// fails startup.
//
// All functions take an injected DB so the package is independent of the active
// alpha3 coredb pool and testable in isolation. Table names default to
// "project" and "scenario_status" and may be overridden by the legacy env vars
// for integration tests that run against an isolated schema.
//
// This package is additive and isolated until the alpha4 cutover in a later
// slice: it does not replace the active alpha3 persistence wiring.
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
)

// DB is the query surface satisfied by both *sql.DB and *sql.Tx (via the
// adapters below) and by test fakes. Functions that perform a single guarded
// statement take DB; functions that need a transaction take Store and begin
// their own Tx.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}

// Tx is a transaction handle satisfied by *sql.Tx (via dbTx) and by test fakes.
type Tx interface {
	DB
	Commit() error
	Rollback() error
}

// Store adds transaction begin/commit to DB. *sql.DB satisfies Store through the
// NewStore adapter; test fakes implement Store directly.
type Store interface {
	DB
	BeginTx(ctx context.Context, opts *sql.TxOptions) (Tx, error)
}

// NewStore returns a Store backed by the given database pool. The adapter
// wraps each *sql.Tx in a dbTx so the transactional functions receive a Tx
// interface that test fakes can also implement.
func NewStore(db *sql.DB) Store { return &dbStore{db: db} }

type dbStore struct{ db *sql.DB }

func (s *dbStore) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return s.db.ExecContext(ctx, query, args...)
}
func (s *dbStore) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return s.db.QueryRowContext(ctx, query, args...)
}
func (s *dbStore) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, query, args...)
}
func (s *dbStore) BeginTx(ctx context.Context, opts *sql.TxOptions) (Tx, error) {
	tx, err := s.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &dbTx{tx: tx}, nil
}

type dbTx struct{ tx *sql.Tx }

func (t *dbTx) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return t.tx.ExecContext(ctx, query, args...)
}
func (t *dbTx) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}
func (t *dbTx) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, query, args...)
}
func (t *dbTx) Commit() error   { return t.tx.Commit() }
func (t *dbTx) Rollback() error { return t.tx.Rollback() }

// Env var names for the two tables, retained from alpha3 for deployment
// compatibility. Defaults are the canonical alpha4 table names.
const (
	projectTableEnv        = "SCENARIO_MANAGER_CORE_DB_PROJECT_TABLE"
	scenarioStatusTableEnv = "SCENARIO_MANAGER_CORE_DB_SCENARIO_STATUS_TABLE"
)

// Canonical alpha4 table names.
const (
	ProjectTableDefault        = "project"
	ScenarioStatusTableDefault = "scenario_status"
)

// ProjectTable returns the configured project table name, trimmed and
// defaulting to "project".
func ProjectTable() string {
	return tableEnv(projectTableEnv, ProjectTableDefault)
}

// ScenarioStatusTable returns the configured scenario_status table name,
// trimmed and defaulting to "scenario_status".
func ScenarioStatusTable() string {
	return tableEnv(scenarioStatusTableEnv, ScenarioStatusTableDefault)
}

func tableEnv(env, def string) string {
	v := strings.TrimSpace(os.Getenv(env))
	if v == "" {
		return def
	}
	return v
}

// Scenario lifecycle states (mirrored from the alpha3 coredb package so the
// alpha4 persistence layer is self-contained).
const (
	ScenarioStateCreated         = "Created"
	ScenarioStateScheduled       = "Scheduled"
	ScenarioStateStartingRunners = "StartingRunners"
	ScenarioStateInProcessing    = "InProcessing"
	ScenarioStatePostProcessing  = "PostProcessing"
	ScenarioStateFinished        = "Finished"
	ScenarioStateFailed          = "Failed"
)

// ErrSchemaIncompatible is returned when an existing table does not match the
// alpha4 compatibility contract. The package never repairs such a table; the
// operator must provide a fresh compatible database or recreate the tables.
var ErrSchemaIncompatible = errors.New("alpha4 schema is incompatible")

// ErrProjectNotFound is returned by lookup functions when no project row
// matches the supplied namespace/name pair.
var ErrProjectNotFound = errors.New("project not found")
