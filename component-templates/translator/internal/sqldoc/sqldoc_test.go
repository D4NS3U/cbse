package sqldoc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scenarioDetailSQLPath is the repository-owned reference Detail DB
// initialization SQL, owned by Slice 05. Resolved relative to the package dir
// so the test runs under `go test` without a symlink.
const scenarioDetailSQLPath = "../../../scenario-detail-database/10-simulation-parameters.sql"

func readSQL(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(scenarioDetailSQLPath))
	if err != nil {
		t.Skipf("reference Detail DB SQL not found (%v); skipping schema test", err)
	}
	return string(b)
}

// TestSimulationParametersSchema asserts the exact reference Scenario Detail
// Database schema: the schema-qualified table, the five INTEGER columns, the
// PRIMARY KEY and NOT NULL/CHECK constraints, and the explicit grants.
func TestSimulationParametersSchema(t *testing.T) {
	sql := readSQL(t)
	if !strings.Contains(sql, "CREATE TABLE public.simulation_parameters (") {
		t.Fatal("must create public.simulation_parameters")
	}
	for _, col := range []string{
		"parameterset_id INTEGER PRIMARY KEY CHECK (parameterset_id > 0)",
		"arrival_rate    INTEGER NOT NULL CHECK (arrival_rate > 0)",
		"service_rate    INTEGER NOT NULL CHECK (service_rate > 0)",
		"run_duration    INTEGER NOT NULL CHECK (run_duration > 0)",
		"seed_policy     INTEGER NOT NULL CHECK (seed_policy >= 0)",
	} {
		if !strings.Contains(sql, col) {
			t.Fatalf("missing column/constraint line: %q", col)
		}
	}
	if !strings.Contains(sql, "GRANT USAGE ON SCHEMA public TO CURRENT_USER;") {
		t.Fatal("must grant USAGE on schema public to CURRENT_USER")
	}
	if !strings.Contains(sql, "GRANT SELECT ON TABLE public.simulation_parameters TO CURRENT_USER;") {
		t.Fatal("must grant SELECT on public.simulation_parameters to CURRENT_USER")
	}
}

// TestSimulationParametersRows asserts the four immutable reference rows with
// their checked-in values and the exact column ordering of the INSERT.
func TestSimulationParametersRows(t *testing.T) {
	sql := readSQL(t)
	if !strings.Contains(sql, "INSERT INTO public.simulation_parameters\n    (parameterset_id, arrival_rate, service_rate, run_duration, seed_policy)") {
		t.Fatal("INSERT must name all five columns in the documented order")
	}
	wantRows := []string{
		"(1, 2, 4, 100, 1001)",
		"(2, 3, 5, 120, 1002)",
		"(3, 4, 7, 150, 1003)",
		"(4, 5, 8, 180, 1004)",
	}
	for _, row := range wantRows {
		if !strings.Contains(sql, row) {
			t.Fatalf("missing reference row: %q", row)
		}
	}
	if strings.Count(sql, "INSERT INTO public.simulation_parameters") != 1 {
		t.Fatal("must have exactly one INSERT statement")
	}
	// Ensure no extra rows beyond the four reference rows.
	insertBlock := sql[strings.Index(sql, "VALUES"):]
	commas := strings.Count(insertBlock, "),")
	if commas != 3 {
		t.Fatalf("expected 4 rows (3 comma separators), found %d", commas)
	}
}
