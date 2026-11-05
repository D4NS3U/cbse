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

package resultdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleDB() DatabaseConfig {
	return DatabaseConfig{Host: "db.example.com", Port: 5432, DBName: "results", User: "u", Password: "p"}
}

// fakeConn records the statements it executes and serves a fixed row set.
type fakeConn struct {
	execSQL    []string
	querySQL   string
	rows       [][]byte
	queryErr   error
	execErr    error
	scanErr    error
	rowsErr    error
	connectErr error
	connected  int
	closed     int
}

func (c *fakeConn) Exec(_ context.Context, sql string, args ...any) error {
	if c.execErr != nil {
		return c.execErr
	}
	c.execSQL = append(c.execSQL, sql)
	return nil
}

func (c *fakeConn) Query(_ context.Context, sql string, args ...any) (Rows, error) {
	if c.queryErr != nil {
		return nil, c.queryErr
	}
	c.querySQL = sql
	return &fakeRows{rows: c.rows, scanErr: c.scanErr, rowsErr: c.rowsErr}, nil
}

func (c *fakeConn) Close(_ context.Context) error {
	c.closed++
	return nil
}

// fakeRows serves a fixed slice of rows; a nil cell produces a scan error to
// emulate a NULL scanned into a non-nullable column.
type fakeRows struct {
	rows    [][]byte
	idx     int
	scanErr error
	rowsErr error
}

func (r *fakeRows) Next() bool {
	if r.idx < len(r.rows) {
		r.idx++
		return true
	}
	return false
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if r.idx == 0 || r.idx > len(r.rows) {
		return errors.New("scan past end")
	}
	cell := r.rows[r.idx-1]
	for _, d := range dest {
		if p, ok := d.(*[]byte); ok {
			*p = cell
		} else {
			return errors.New("unsupported scan dest")
		}
	}
	return nil
}

func (r *fakeRows) Close() error { return nil }
func (r *fakeRows) Err() error   { return r.rowsErr }

// fakeConnector returns the single fake connection.
type fakeConnector struct{ conn *fakeConn }

func (f fakeConnector) Connect(_ context.Context, _ string, _ int32, _, _, _ string) (Conn, error) {
	if f.conn.connectErr != nil {
		return nil, f.conn.connectErr
	}
	f.conn.connected++
	return f.conn, nil
}

func TestFetchExtractsFiniteMeanWaitTimes(t *testing.T) {
	conn := &fakeConn{rows: [][]byte{
		[]byte(`{"parameterset_id":1,"arrival_rate":1.0,"service_rate":2.0,"run_duration":60,"seed_policy":1,"effective_seed":7,"completed_customers":9,"mean_wait_time":1.5}`),
		[]byte(`{"mean_wait_time":2.5}`),
		[]byte(`{"mean_wait_time":0}`),
	}}
	res, err := Fetch(context.Background(), sampleDB(), 7, fakeConnector{conn})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if res.Rows != 3 || res.Malformed != 0 || len(res.Values) != 3 {
		t.Fatalf("result = %+v, want rows=3 malformed=0 values=3", res)
	}
	if res.Values[0] != 1.5 || res.Values[1] != 2.5 || res.Values[2] != 0 {
		t.Fatalf("values = %v, want [1.5 2.5 0]", res.Values)
	}
	if conn.connected != 1 || conn.closed != 1 {
		t.Fatalf("connect/close = %d/%d, want 1/1", conn.connected, conn.closed)
	}
	// The 30-second statement timeout is applied before the query.
	if len(conn.execSQL) != 1 || !strings.Contains(conn.execSQL[0], "SET statement_timeout") || !strings.Contains(conn.execSQL[0], "30000") {
		t.Fatalf("statement timeout exec = %v", conn.execSQL)
	}
	if conn.querySQL != "SELECT result FROM public.scenario_7_results" {
		t.Fatalf("query = %q, want SELECT result FROM public.scenario_7_results", conn.querySQL)
	}
}

func TestFetchCountsMalformedRows(t *testing.T) {
	conn := &fakeConn{rows: [][]byte{
		[]byte(`{"mean_wait_time":1.5}`),
		[]byte(`{}`),                        // missing key
		[]byte(`{"mean_wait_time":null}`),   // JSON null
		[]byte(`{"mean_wait_time":"fast"}`), // not a number
		[]byte(`[1,2,3]`),                   // not an object
		[]byte(``),                          // empty record
		[]byte(`{"mean_wait_time":3.25}`),
	}}
	res, err := Fetch(context.Background(), sampleDB(), 42, fakeConnector{conn})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if res.Rows != 7 || res.Malformed != 5 {
		t.Fatalf("rows/malformed = %d/%d, want 7/5", res.Rows, res.Malformed)
	}
	if len(res.Values) != 2 || res.Values[0] != 1.5 || res.Values[1] != 3.25 {
		t.Fatalf("values = %v, want [1.5 3.25] (malformed rows excluded)", res.Values)
	}
}

func TestFetchEmptyTable(t *testing.T) {
	conn := &fakeConn{rows: nil}
	res, err := Fetch(context.Background(), sampleDB(), 1, fakeConnector{conn})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if res.Rows != 0 || res.Malformed != 0 || len(res.Values) != 0 {
		t.Fatalf("result = %+v, want zero rows", res)
	}
}

func TestFetchRejectsNonPositiveScenarioID(t *testing.T) {
	conn := &fakeConn{}
	for _, id := range []int64{0, -42} {
		if _, err := Fetch(context.Background(), sampleDB(), id, fakeConnector{conn}); err == nil {
			t.Fatalf("scenario id %d accepted", id)
		}
	}
	if conn.connected != 0 {
		t.Fatalf("connect called %d times for invalid ids", conn.connected)
	}
}

func TestFetchErrorPropagation(t *testing.T) {
	t.Run("connect failure", func(t *testing.T) {
		conn := &fakeConn{connectErr: errors.New("connection refused")}
		if _, err := Fetch(context.Background(), sampleDB(), 1, fakeConnector{conn}); err == nil || !strings.Contains(err.Error(), "connection refused") {
			t.Fatalf("err = %v, want connect failure", err)
		}
	})
	t.Run("set statement_timeout failure", func(t *testing.T) {
		conn := &fakeConn{execErr: errors.New("statement timeout")}
		if _, err := Fetch(context.Background(), sampleDB(), 1, fakeConnector{conn}); err == nil || !strings.Contains(err.Error(), "statement timeout") {
			t.Fatalf("err = %v, want statement timeout failure", err)
		}
	})
	t.Run("query failure", func(t *testing.T) {
		conn := &fakeConn{queryErr: errors.New("relation does not exist")}
		if _, err := Fetch(context.Background(), sampleDB(), 1, fakeConnector{conn}); err == nil || !strings.Contains(err.Error(), "relation does not exist") {
			t.Fatalf("err = %v, want query failure", err)
		}
	})
	t.Run("scan failure", func(t *testing.T) {
		conn := &fakeConn{rows: [][]byte{[]byte(`{"mean_wait_time":1}`)}, scanErr: errors.New("NULL into non-nullable jsonb")}
		if _, err := Fetch(context.Background(), sampleDB(), 1, fakeConnector{conn}); err == nil || !strings.Contains(err.Error(), "NULL") {
			t.Fatalf("err = %v, want scan failure", err)
		}
	})
	t.Run("rows error", func(t *testing.T) {
		conn := &fakeConn{rows: [][]byte{[]byte(`{"mean_wait_time":1}`)}, rowsErr: errors.New("row iteration failed")}
		if _, err := Fetch(context.Background(), sampleDB(), 1, fakeConnector{conn}); err == nil || !strings.Contains(err.Error(), "row iteration failed") {
			t.Fatalf("err = %v, want rows error", err)
		}
	})
}

func TestResultTableName(t *testing.T) {
	if ResultTableName(7) != "scenario_7_results" {
		t.Fatalf("table = %q", ResultTableName(7))
	}
	if ResultTableName(1234) != "scenario_1234_results" {
		t.Fatalf("table = %q", ResultTableName(1234))
	}
}

func TestDatabaseConfigValidate(t *testing.T) {
	c := sampleDB()
	if err := c.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*DatabaseConfig)
	}{
		{"empty host", func(c *DatabaseConfig) { c.Host = "" }},
		{"port zero", func(c *DatabaseConfig) { c.Port = 0 }},
		{"port high", func(c *DatabaseConfig) { c.Port = 70000 }},
		{"port negative", func(c *DatabaseConfig) { c.Port = -1 }},
		{"empty dbname", func(c *DatabaseConfig) { c.DBName = "" }},
		{"empty user", func(c *DatabaseConfig) { c.User = "" }},
		{"empty password", func(c *DatabaseConfig) { c.Password = "" }},
	}
	for _, tc := range cases {
		c := sampleDB()
		tc.mut(&c)
		if err := c.Validate(); err == nil {
			t.Fatalf("%s: invalid config accepted", tc.name)
		}
	}
}

func TestLoadSecret(t *testing.T) {
	dir := t.TempDir()
	for k, v := range map[string]string{
		"host": "db.example.com", "port": "5432", "dbname": "results", "user": "u", "password": "p",
	} {
		if err := os.WriteFile(filepath.Join(dir, k), []byte(v), 0o600); err != nil {
			t.Fatalf("write %s: %v", k, err)
		}
	}
	cfg, err := LoadSecret(dir)
	if err != nil {
		t.Fatalf("LoadSecret: %v", err)
	}
	if cfg != sampleDB() {
		t.Fatalf("config = %+v, want %+v", cfg, sampleDB())
	}
}

func TestLoadSecretErrors(t *testing.T) {
	cases := []struct {
		name string
		mut  func(dir string)
	}{
		{"missing file", func(dir string) { os.Remove(filepath.Join(dir, "port")) }},
		{"empty value", func(dir string) { os.WriteFile(filepath.Join(dir, "host"), []byte("  "), 0o600) }},
		{"non-integer port", func(dir string) { os.WriteFile(filepath.Join(dir, "port"), []byte("abc"), 0o600) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for k, v := range map[string]string{
				"host": "h", "port": "5432", "dbname": "d", "user": "u", "password": "p",
			} {
				os.WriteFile(filepath.Join(dir, k), []byte(v), 0o600)
			}
			tc.mut(dir)
			if _, err := LoadSecret(dir); err == nil {
				t.Fatalf("%s: secret accepted", tc.name)
			}
		})
	}
	if _, err := LoadSecret(""); err == nil {
		t.Fatal("empty directory accepted")
	}
}
