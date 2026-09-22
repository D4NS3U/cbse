// Package detaildb implements the reference Translator framework's predefined
// Scenario Detail Database parameter lookup. It owns the single schema-qualified
// parameterized SQL statement, the 10-second connection timeout (applied by the
// shared database-endpoint dial contract), the 30-second statement timeout, the
// one cancellable 30-second delayed retry on transient failure, and the
// SQLSTATE classification that distinguishes transient from permanent failures.
//
// The lookup is transport-agnostic: the caller supplies a Connector that returns
// a query-capable Conn so the package is independently testable without a real
// PostgreSQL driver. The real pgx connector lives in pgx.go in this package.
package detaildb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/D4NS3U/cbse/component-templates/translator/internal/databaseendpoint"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/dbconfig"
	"github.com/jackc/pgx/v5/pgconn"
)

// LookupResult is the one validated parameter row returned by a successful
// lookup.
type LookupResult struct {
	ArrivalRate int
	ServiceRate int
	RunDuration int
	SeedPolicy  int
}

// Conn is the query-capable PostgreSQL connection the lookup uses for one
// attempt. It extends the endpoint contract's narrow connection with the
// statement-timeout and query operations the lookup needs.
type Conn interface {
	// Exec executes a statement that does not return rows.
	Exec(ctx context.Context, sql string, args ...any) error
	// Query executes a statement that returns rows.
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	// Close releases the connection.
	Close(ctx context.Context) error
}

// Rows is the row-set returned by Conn.Query.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

// Connector opens a query-capable PostgreSQL connection to a resolved candidate
// address. It satisfies the databaseendpoint.DialConn connect callback shape.
type Connector interface {
	Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error)
}

// statementTimeout is the per-statement timeout applied before the lookup query.
const statementTimeout = 30 * time.Second

// transientRetryDelay is the one delayed retry wait after a first transient
// failure. It is a var so tests can shorten it.
var transientRetryDelay = 30 * time.Second

// lookupSQL is the single predefined parameterized SQL statement the generator
// owns. It is schema-qualified and depends on no search_path.
const lookupSQL = "SELECT arrival_rate, service_rate, run_duration, seed_policy " +
	"FROM public.simulation_parameters WHERE parameterset_id = $1"

// Lookup runs the predefined parameter lookup for parametersetID against the
// Scenario Detail Database. It applies the shared endpoint contract (classify,
// resolve, de-dup, fallback, 10-second shared deadline) through
// databaseendpoint.DialConn, sets a 30-second statement timeout, and requires
// exactly one row. On a first transient failure it waits exactly 30 seconds
// (cancellable by ctx) and retries the whole connect+query once; a second
// failure is permanent. Authentication, authorization, schema, recipe, and
// result failures are permanent and never retried. A cancelled context
// (shutdown or failed in-progress acknowledgement) returns the context error
// without an empty-failure outcome.
func Lookup(ctx context.Context, ep dbconfig.DatabaseConfig, parametersetID int, resolver databaseendpoint.Resolver, connector Connector) (LookupResult, error) {
	endpoint := databaseendpoint.Endpoint{Host: ep.Host, Port: int32(ep.Port), User: ep.User, Password: ep.Password, DBName: ep.DBName}

	res, err, transient := attemptLookup(ctx, endpoint, parametersetID, resolver, connector)
	if err == nil {
		return res, nil
	}
	if !transient {
		return res, err
	}
	// First transient failure: wait exactly 30 seconds, cancellable by ctx.
	select {
	case <-time.After(transientRetryDelay):
	case <-ctx.Done():
		return res, fmt.Errorf("detail db lookup cancelled during delayed retry: %w", ctx.Err())
	}
	// Second attempt: any failure is permanent.
	res2, err2, _ := attemptLookup(ctx, endpoint, parametersetID, resolver, connector)
	if err2 == nil {
		return res2, nil
	}
	return res2, fmt.Errorf("detail db lookup failed after retry: %w", err2)
}

// attemptLookup dials, sets the statement timeout, runs the query, and validates
// cardinality and values. It returns the result, an error, and whether the
// error is transient (eligible for the one delayed retry).
func attemptLookup(ctx context.Context, endpoint databaseendpoint.Endpoint, parametersetID int, resolver databaseendpoint.Resolver, connector Connector) (LookupResult, error, bool) {
	conn, _, _, err := databaseendpoint.DialConn[Conn](ctx, endpoint, resolver, connector.Connect)
	if err != nil {
		return LookupResult{}, fmt.Errorf("detail db connect: %w", err), isTransient(err)
	}
	defer conn.Close(ctx)

	if err := conn.Exec(ctx, fmt.Sprintf("SET statement_timeout = '%d'", int(statementTimeout/time.Millisecond))); err != nil {
		return LookupResult{}, fmt.Errorf("detail db set statement_timeout: %w", err), isTransient(err)
	}

	rows, err := conn.Query(ctx, lookupSQL, parametersetID)
	if err != nil {
		return LookupResult{}, fmt.Errorf("detail db query: %w", err), isTransient(err)
	}
	defer rows.Close()

	count := 0
	var res LookupResult
	for rows.Next() {
		count++
		if count == 1 {
			if err := rows.Scan(&res.ArrivalRate, &res.ServiceRate, &res.RunDuration, &res.SeedPolicy); err != nil {
				return LookupResult{}, fmt.Errorf("detail db scan: %w", err), false // NULL or non-integer is a permanent result failure
			}
			if err := validateValues(res); err != nil {
				return LookupResult{}, err, false
			}
		}
	}
	if err := rows.Err(); err != nil {
		return LookupResult{}, fmt.Errorf("detail db rows: %w", err), isTransient(err)
	}
	switch count {
	case 0:
		return LookupResult{}, fmt.Errorf("detail db lookup: parameterset_id %d returned no row", parametersetID), false
	case 1:
		return res, nil, false
	default:
		return LookupResult{}, fmt.Errorf("detail db lookup: parameterset_id %d returned %d rows, want 1", parametersetID, count), false
	}
}

// validateValues applies the table constraint checks defensively so a row that
// violates the schema's CHECK constraints is a permanent failure even if the
// database returned it.
func validateValues(r LookupResult) error {
	if r.ArrivalRate <= 0 {
		return fmt.Errorf("detail db: arrival_rate %d must be > 0", r.ArrivalRate)
	}
	if r.ServiceRate <= 0 {
		return fmt.Errorf("detail db: service_rate %d must be > 0", r.ServiceRate)
	}
	if r.RunDuration <= 0 {
		return fmt.Errorf("detail db: run_duration %d must be > 0", r.RunDuration)
	}
	if r.SeedPolicy < 0 {
		return fmt.Errorf("detail db: seed_policy %d must be >= 0", r.SeedPolicy)
	}
	return nil
}

// isTransient reports whether err is a transient PostgreSQL failure eligible for
// the one delayed retry: connection exception (08), transaction rollback (40),
// insufficient resource (53), or administrative shutdown/crash/cannot-connect
// (57P01/57P02/57P03). A network, timeout, or connection-refused error with no
// SQLSTATE is also transient (connection refusal or timeout). Authentication
// (28), insufficient privilege (42501), schema (42xxx), and all other server
// errors are permanent.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		code := pgErr.Code
		if strings.HasPrefix(code, "08") || strings.HasPrefix(code, "40") || strings.HasPrefix(code, "53") {
			return true
		}
		switch code {
		case "57P01", "57P02", "57P03":
			return true
		}
		return false
	}
	// No SQLSTATE: a network-level connection refusal, timeout, or DNS failure is
	// transient under the owning client's failure policy. Context cancellation is
	// not a transient retry condition; it propagates as cancellation.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}
