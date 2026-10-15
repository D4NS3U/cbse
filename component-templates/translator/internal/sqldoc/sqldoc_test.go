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

// Package sqldoc asserts the reference Scenario Detail Database SQL schema
// against the checked-in initialization SQL so the ruled R2 design-layer
// contract stays in lockstep with the repository-owned SQL: the four
// per-parameter entry tables (arrival_rate, service_rate, run_duration,
// seed_policy) with their id/value columns and CHECK constraints, the
// recipes locator table (one foreign key per parameter), the
// simulation_parameters view over their join with the exact legacy
// projection, the explicit grants (the Translator's SELECT on the view and
// the EDS user's write access on the entry and recipes tables), and the four
// immutable baseline rows (ids 1-4, read-only; the EDS id-space starts at 5).
// The tests skip when the reference SQL file is absent.
package sqldoc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// scenarioDetailSQLPath is the repository-owned reference Scenario Detail
// Database initialization SQL. Resolved relative to the package dir so the
// test runs under `go test` without a symlink.
const scenarioDetailSQLPath = "../../../scenario-detail-database/10-simulation-parameters.sql"

func readSQL(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(scenarioDetailSQLPath))
	if err != nil {
		t.Skipf("reference Detail DB SQL not found (%v); skipping schema test", err)
	}
	return string(b)
}

// TestSimulationParametersSchema asserts the six ruled R2 objects: the four
// entry tables with their exact column/CHECK lines, the recipes locator
// table with one FK per parameter, the view's exact projection columns, and
// the explicit grants.
func TestSimulationParametersSchema(t *testing.T) {
	sql := readSQL(t)

	// The four per-parameter entry tables, verbatim.
	for _, table := range []string{
		"CREATE TABLE IF NOT EXISTS public.arrival_rate (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value > 0));",
		"CREATE TABLE IF NOT EXISTS public.service_rate (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value > 0));",
		"CREATE TABLE IF NOT EXISTS public.run_duration (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value > 0));",
		"CREATE TABLE IF NOT EXISTS public.seed_policy  (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value >= 0));",
	} {
		if !strings.Contains(sql, table) {
			t.Fatalf("missing entry table DDL: %q", table)
		}
	}

	// The recipes locator table: the parameterset_id primary key plus one
	// foreign key per parameter.
	for _, line := range []string{
		"CREATE TABLE IF NOT EXISTS public.recipes (",
		"parameterset_id INTEGER PRIMARY KEY CHECK (parameterset_id > 0),",
		"arrival_rate_id INTEGER NOT NULL REFERENCES public.arrival_rate(id),",
		"service_rate_id INTEGER NOT NULL REFERENCES public.service_rate(id),",
		"run_duration_id INTEGER NOT NULL REFERENCES public.run_duration(id),",
		"seed_policy_id  INTEGER NOT NULL REFERENCES public.seed_policy(id)",
	} {
		if !strings.Contains(sql, line) {
			t.Fatalf("missing recipes locator line: %q", line)
		}
	}

	// The view over recipes joined with the entry tables, with the exact
	// legacy projection (the Translator's landed lookup is view-transparent).
	if !strings.Contains(sql, "CREATE OR REPLACE VIEW public.simulation_parameters AS") {
		t.Fatal("must create the public.simulation_parameters view")
	}
	for _, col := range []string{
		"r.parameterset_id,",
		"a.value AS arrival_rate,",
		"s.value AS service_rate,",
		"d.value AS run_duration,",
		"p.value AS seed_policy",
	} {
		if !strings.Contains(sql, col) {
			t.Fatalf("missing view projection column: %q", col)
		}
	}
	for _, join := range []string{
		"JOIN public.arrival_rate a ON a.id = r.arrival_rate_id",
		"JOIN public.service_rate  s ON s.id = r.service_rate_id",
		"JOIN public.run_duration  d ON d.id = r.run_duration_id",
		"JOIN public.seed_policy   p ON p.id = r.seed_policy_id",
	} {
		if !strings.Contains(sql, join) {
			t.Fatalf("missing view join: %q", join)
		}
	}

	// The explicit grants: the Translator's SELECT on the view and the EDS
	// user's write access on the entry and recipes tables (the view is
	// read-only by construction).
	if !strings.Contains(sql, "GRANT USAGE ON SCHEMA public TO CURRENT_USER;") {
		t.Fatal("must grant USAGE on schema public to CURRENT_USER")
	}
	if !strings.Contains(sql, "GRANT SELECT ON TABLE public.simulation_parameters TO CURRENT_USER;") {
		t.Fatal("must grant SELECT on the simulation_parameters view to CURRENT_USER")
	}
	for _, table := range []string{
		"GRANT SELECT, INSERT, UPDATE ON TABLE public.arrival_rate TO CURRENT_USER;",
		"GRANT SELECT, INSERT, UPDATE ON TABLE public.service_rate TO CURRENT_USER;",
		"GRANT SELECT, INSERT, UPDATE ON TABLE public.run_duration TO CURRENT_USER;",
		"GRANT SELECT, INSERT, UPDATE ON TABLE public.seed_policy TO CURRENT_USER;",
		"GRANT SELECT, INSERT, UPDATE ON TABLE public.recipes TO CURRENT_USER;",
	} {
		if !strings.Contains(sql, table) {
			t.Fatalf("missing write-surface grant: %q", table)
		}
	}
}

// TestSimulationParametersRows asserts the four immutable baseline rows in
// each of the five tables (ids 1-4, read-only in all five tables) with the
// exact column ordering of the INSERTs. The view computes the legacy rows
// exactly; no EDS row (id >= 5) is seeded.
func TestSimulationParametersRows(t *testing.T) {
	sql := readSQL(t)

	wantBlocks := map[string]string{
		"public.arrival_rate": `INSERT INTO public.arrival_rate (id, value) VALUES
    (1, 2),
    (2, 3),
    (3, 4),
    (4, 5);`,
		"public.service_rate": `INSERT INTO public.service_rate (id, value) VALUES
    (1, 4),
    (2, 5),
    (3, 7),
    (4, 8);`,
		"public.run_duration": `INSERT INTO public.run_duration (id, value) VALUES
    (1, 100),
    (2, 120),
    (3, 150),
    (4, 180);`,
		"public.seed_policy": `INSERT INTO public.seed_policy (id, value) VALUES
    (1, 1001),
    (2, 1002),
    (3, 1003),
    (4, 1004);`,
		"public.recipes": `INSERT INTO public.recipes
    (parameterset_id, arrival_rate_id, service_rate_id, run_duration_id, seed_policy_id)
VALUES
    (1, 1, 1, 1, 1),
    (2, 2, 2, 2, 2),
    (3, 3, 3, 3, 3),
    (4, 4, 4, 4, 4);`,
	}
	for table, block := range wantBlocks {
		if !strings.Contains(sql, block) {
			t.Fatalf("missing baseline INSERT block for %s: %q", table, block)
		}
		if strings.Count(sql, "INSERT INTO "+table) != 1 {
			t.Fatalf("%s must have exactly one baseline INSERT statement", table)
		}
		// No EDS row (id >= 5) is seeded by the reference image: every row of
		// the INSERT block carries a baseline id 1-4.
		start := strings.Index(sql, "INSERT INTO "+table)
		rest := sql[start+len("INSERT INTO "):]
		if next := strings.Index(rest, "\nINSERT INTO "); next >= 0 {
			rest = rest[:next]
		}
		for _, rowLine := range strings.Split(rest, "\n") {
			trimmed := strings.TrimSpace(rowLine)
			if !strings.HasPrefix(trimmed, "(") {
				continue
			}
			id, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(strings.TrimPrefix(trimmed, "("), ",", 2)[0]))
			if err != nil {
				continue
			}
			if id > 4 {
				t.Fatalf("%s seeds the row id %d beyond the ids 1-4 baseline", table, id)
			}
		}
	}
}
