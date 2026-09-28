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

// Package resultdb implements the reference PostProcessingService's read-only
// Scenario Result Database client. It owns the mounted connection Secret
// parsing (host, port, dbname, user, password — always sslmode=disable, no
// sslmode key), the single schema-qualified parameterized-free SQL statement
// that selects a scenario's result rows, the 30-second statement timeout, and
// the extraction of the mean_wait_time observations from the runner's JSONB
// result records.
//
// The PPS is the sole Result DB reader and only ever issues SELECTs: it
// never writes the Result DB and never touches the Core DB.
//
// The fetch is transport-agnostic: the caller supplies a Connector that
// returns a query-capable Conn so the package is independently testable
// without a real PostgreSQL driver. The real pgx connector lives in pgx.go in
// this package. Fetch failures are returned to the caller, which leaves the
// evaluation request unacknowledged (NAK) for JetStream redelivery; there is
// no built-in retry, mirroring the translator's taxonomy where the transport
// retry is the message redelivery.
package resultdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DatabaseConfig is the exact set of PostgreSQL connection fields the PPS
// reads from the mounted Result DB connection Secret: host, port, dbname,
// user, and password. The PPS always sets sslmode=disable when connecting;
// the Secret carries no sslmode field.
type DatabaseConfig struct {
	Host     string
	Port     int
	DBName   string
	User     string
	Password string
}

// Validate reports whether all five fields are present and the port is in the
// valid TCP range. The PPS calls this at startup before handling any request
// and rejects missing or malformed values.
func (c DatabaseConfig) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("result database connection field host is empty")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("result database connection field port %d is outside 1..65535", c.Port)
	}
	if c.DBName == "" {
		return fmt.Errorf("result database connection field dbname is empty")
	}
	if c.User == "" {
		return fmt.Errorf("result database connection field user is empty")
	}
	if c.Password == "" {
		return fmt.Errorf("result database connection field password is empty")
	}
	return nil
}

// LoadSecret reads the five connection files from dir (the mounted Result DB
// connection Secret) and returns the validated DatabaseConfig. An empty file
// or a missing file is an error.
func LoadSecret(dir string) (DatabaseConfig, error) {
	var c DatabaseConfig
	if dir == "" {
		return c, fmt.Errorf("result database connection directory is empty")
	}
	host, err := readKey(dir, "host")
	if err != nil {
		return c, err
	}
	portStr, err := readKey(dir, "port")
	if err != nil {
		return c, err
	}
	dbname, err := readKey(dir, "dbname")
	if err != nil {
		return c, err
	}
	user, err := readKey(dir, "user")
	if err != nil {
		return c, err
	}
	password, err := readKey(dir, "password")
	if err != nil {
		return c, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil {
		return c, fmt.Errorf("result database port %q is not an integer", portStr)
	}
	c = DatabaseConfig{Host: host, Port: port, DBName: dbname, User: user, Password: password}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

// readKey reads and trims a single connection file. An empty value is an
// error.
func readKey(dir, key string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, key))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", key, err)
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return "", fmt.Errorf("%s is empty", key)
	}
	return v, nil
}

// Conn is the query-capable PostgreSQL connection the fetch uses for one
// evaluation.
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

// Connector opens a query-capable PostgreSQL connection to the configured
// Result DB endpoint.
type Connector interface {
	Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error)
}

// FetchResult is the validated observation set of a successful fetch.
type FetchResult struct {
	// Values are the finite mean_wait_time observations, one per usable
	// result row, in row order.
	Values []float64
	// Malformed counts the result rows whose mean_wait_time was missing,
	// non-numeric, or non-finite; they are included in no computation.
	Malformed int
	// Rows is the total number of result rows selected.
	Rows int
}

// statementTimeout is the per-statement timeout applied before the result
// query, mirroring the runner's 30-second statement-timeout discipline.
const statementTimeout = 30 * time.Second

// ResultTableName returns the scenario's result table name:
// scenario_<scenario-id>_results. scenarioID must be positive; the caller
// validates the request scenario id before calling.
func ResultTableName(scenarioID int64) string {
	return fmt.Sprintf("scenario_%d_results", scenarioID)
}

// Fetch reads all result rows of the scenario's result table from the
// Scenario Result Database and extracts the mean_wait_time observations. It
// connects through the supplied connector (the mounted Secret's endpoint),
// sets the 30-second statement timeout, selects the JSONB result records
// (SELECT result FROM public.scenario_<id>_results), and extracts the
// finite-float mean_wait_time values. A row with a missing, non-numeric, or
// non-finite mean_wait_time is skipped and counted as malformed. An empty
// table returns a zero-row result without error.
func Fetch(ctx context.Context, ep DatabaseConfig, scenarioID int64, connector Connector) (FetchResult, error) {
	if scenarioID <= 0 {
		return FetchResult{}, fmt.Errorf("result db fetch: scenario id %d must be > 0", scenarioID)
	}
	conn, err := connector.Connect(ctx, ep.Host, int32(ep.Port), ep.User, ep.Password, ep.DBName)
	if err != nil {
		return FetchResult{}, fmt.Errorf("result db connect: %w", err)
	}
	defer conn.Close(ctx)

	if err := conn.Exec(ctx, fmt.Sprintf("SET statement_timeout = '%d'", int(statementTimeout/time.Millisecond))); err != nil {
		return FetchResult{}, fmt.Errorf("result db set statement_timeout: %w", err)
	}

	sql := "SELECT result FROM " + "public." + ResultTableName(scenarioID)
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return FetchResult{}, fmt.Errorf("result db query: %w", err)
	}
	defer rows.Close()

	res := FetchResult{Values: []float64{}}
	for rows.Next() {
		var record []byte
		if err := rows.Scan(&record); err != nil {
			return FetchResult{}, fmt.Errorf("result db scan: %w", err)
		}
		res.Rows++
		v, ok := extractMeanWaitTime(record)
		if !ok {
			res.Malformed++
			continue
		}
		res.Values = append(res.Values, v)
	}
	if err := rows.Err(); err != nil {
		return FetchResult{}, fmt.Errorf("result db rows: %w", err)
	}
	return res, nil
}

// resultRecord is the subset of the runner's JSONB result record the PPS
// reads. mean_wait_time is the reference KPI; the remaining fields are the
// baked scenario parameters and secondary metrics, which the PPS ignores.
type resultRecord struct {
	MeanWaitTime json.RawMessage `json:"mean_wait_time"`
}

// extractMeanWaitTime extracts the finite-float mean_wait_time value from a
// runner JSONB result record. It reports false for a record that is not a
// JSON object, that lacks mean_wait_time or carries JSON null, whose
// mean_wait_time is not a JSON number, or whose numeric value is not finite.
func extractMeanWaitTime(record []byte) (float64, bool) {
	var rec resultRecord
	if err := json.Unmarshal(record, &rec); err != nil {
		return 0, false
	}
	raw := bytes.TrimSpace(rec.MeanWaitTime)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 0, false // key absent or JSON null
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, false // not a JSON number
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}
