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

// Package recipestore is the reference ExperimentalDesignService's design
// layer writer over the experiment's Scenario Detail Database. It owns the
// startup self-provision of the canonical six-object schema (the four
// per-parameter entry tables, the recipes locator table, and the
// simulation_parameters view — the ruled R2/R3 DDL, run verbatim), the entry
// CRUD and recipe locator rows, the single-transaction save, the EDS id
// assignment (MAX(id)+1 with the floor 5; ids 1-4 are the read-only
// reference baseline and are refused on every write), and the view reads the
// API surfaces.
//
// The store is the EDS's only write surface: it writes exactly the entry
// tables and the recipes table, never the view (it is a view), never ids
// 1-4, and never any other schema or table. The Result DB is out of reach
// entirely.
//
// The fetch and write paths are transport-agnostic: the caller supplies a
// Connector that returns a query-capable Conn so the package is
// independently testable without a real PostgreSQL driver. The real pgx
// connector lives in pgx.go in this package.
package recipestore

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// The four varyable parameters of the reference simulation, by their literal
// table names (ruled R2).
const (
	ParamArrivalRate = "arrival_rate"
	ParamServiceRate = "service_rate"
	ParamRunDuration = "run_duration"
	ParamSeedPolicy  = "seed_policy"
)

// allParameters is the fixed parameter set, in canonical order.
var allParameters = []string{ParamArrivalRate, ParamServiceRate, ParamRunDuration, ParamSeedPolicy}

// ErrReservedID is the clean refusal for any write targeting ids 1-4: the
// seeded reference baseline is read-only in all five tables and the EDS
// id-space starts at 5 in every table.
var ErrReservedID = errors.New("ids 1-4 are the read-only reference baseline; the EDS id-space starts at 5")

// The canonical six-object DDL (ruled R2/R3, pinned in FEATURE.md). E1's
// startup self-provision runs exactly these statements; against a
// pre-created matching schema (the seeded reference image, or a
// user-pre-created host schema) every statement is idempotent: the tables
// by the IF NOT EXISTS form, the view by OR REPLACE (the only valid
// idempotent view form in PostgreSQL — CREATE VIEW IF NOT EXISTS does not
// exist; verified live against the locked postgres image).
const (
	ddlArrivalRate = "CREATE TABLE IF NOT EXISTS public.arrival_rate (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value > 0));"
	ddlServiceRate = "CREATE TABLE IF NOT EXISTS public.service_rate (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value > 0));"
	ddlRunDuration = "CREATE TABLE IF NOT EXISTS public.run_duration (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value > 0));"
	ddlSeedPolicy  = "CREATE TABLE IF NOT EXISTS public.seed_policy  (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value >= 0));"
	ddlRecipes     = "CREATE TABLE IF NOT EXISTS public.recipes (\n" +
		"    parameterset_id INTEGER PRIMARY KEY CHECK (parameterset_id > 0),\n" +
		"    arrival_rate_id INTEGER NOT NULL REFERENCES public.arrival_rate(id),\n" +
		"    service_rate_id INTEGER NOT NULL REFERENCES public.service_rate(id),\n" +
		"    run_duration_id INTEGER NOT NULL REFERENCES public.run_duration(id),\n" +
		"    seed_policy_id  INTEGER NOT NULL REFERENCES public.seed_policy(id)\n" +
		");"
	ddlView = "CREATE OR REPLACE VIEW public.simulation_parameters AS\n" +
		"    SELECT r.parameterset_id,\n" +
		"           a.value AS arrival_rate,\n" +
		"           s.value AS service_rate,\n" +
		"           d.value AS run_duration,\n" +
		"           p.value AS seed_policy\n" +
		"    FROM public.recipes r\n" +
		"    JOIN public.arrival_rate a ON a.id = r.arrival_rate_id\n" +
		"    JOIN public.service_rate  s ON s.id = r.service_rate_id\n" +
		"    JOIN public.run_duration  d ON d.id = r.run_duration_id\n" +
		"    JOIN public.seed_policy   p ON p.id = r.seed_policy_id;"
)

// canonicalDDL is the self-provision statement list in dependency order.
var canonicalDDL = []string{
	ddlArrivalRate,
	ddlServiceRate,
	ddlRunDuration,
	ddlSeedPolicy,
	ddlRecipes,
	ddlView,
}

// Entry is one design entry: a value in one of the four per-parameter
// tables, addressed by its EDS-assigned id.
type Entry struct {
	Parameter string
	ID        int
	Value     int
}

// Recipe is one recipe locator row: one FK per parameter locating the entry
// rows the scenario resolves against.
type Recipe struct {
	ParametersetID int
	ArrivalRateID  int
	ServiceRateID  int
	RunDurationID  int
	SeedPolicyID   int
}

// ViewRow is one row of the simulation_parameters view projection:
// parameterset_id, arrival_rate, service_rate, run_duration, seed_policy.
type ViewRow struct {
	ParametersetID int
	ArrivalRate    int
	ServiceRate    int
	RunDuration    int
	SeedPolicy     int
}

// Rows is the row-set returned by Conn.Query and Tx.Query.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

// Tx is the transactional scope the single-transaction save runs in.
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) error
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Conn is the query-capable PostgreSQL connection the store uses.
type Conn interface {
	Exec(ctx context.Context, sql string, args ...any) error
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	Begin(ctx context.Context) (Tx, error)
	Close(ctx context.Context) error
}

// Connector opens a query-capable PostgreSQL connection.
type Connector interface {
	Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error)
}

// Store is the design layer writer bound to a Detail DB endpoint and
// connector.
type Store struct {
	Host      string
	Port      int32
	User      string
	Password  string
	DBName    string
	connector Connector
}

// New returns a Store over the given endpoint fields and connector.
func New(host string, port int32, user, password, dbname string, connector Connector) *Store {
	return &Store{Host: host, Port: port, User: user, Password: password, DBName: dbname, connector: connector}
}

// open connects to the Detail DB.
func (s *Store) open(ctx context.Context) (Conn, error) {
	if s.connector == nil {
		return nil, errors.New("recipestore: nil connector")
	}
	return s.connector.Connect(ctx, s.Host, s.Port, s.User, s.Password, s.DBName)
}

// parameterTable maps a parameter name to its entry table.
func parameterTable(parameter string) (string, error) {
	for _, p := range allParameters {
		if p == parameter {
			return p, nil
		}
	}
	return "", fmt.Errorf("parameter %q must be one of %s", parameter, strings.Join(allParameters, ", "))
}

// ValueDomain applies the DDL CHECKs as the API-level domain rule:
// arrival_rate/service_rate/run_duration are positive integers, seed_policy
// is an integer >= 0. It also rejects an unknown parameter name.
func ValueDomain(parameter string, value int) error {
	if _, err := parameterTable(parameter); err != nil {
		return err
	}
	if parameter == ParamSeedPolicy {
		if value < 0 {
			return fmt.Errorf("seed_policy value %d must be >= 0", value)
		}
		return nil
	}
	if value <= 0 {
		return fmt.Errorf("%s value %d must be > 0", parameter, value)
	}
	return nil
}

// writableID refuses any write targeting ids 1-4 in any of the five tables
// (the read-only reference baseline).
func writableID(id int) error {
	if id < 5 {
		return fmt.Errorf("%w: id %d", ErrReservedID, id)
	}
	return nil
}

// EnsureSchema executes the canonical six-object DDL at startup. Against a
// pre-created matching schema every statement is a no-op (both delivery
// branches — the seeded reference image and a user-pre-created host schema —
// work); the Result DB is never touched.
func (s *Store) EnsureSchema(ctx context.Context) error {
	conn, err := s.open(ctx)
	if err != nil {
		return fmt.Errorf("recipestore: connect: %w", err)
	}
	defer conn.Close(ctx)
	for _, ddl := range canonicalDDL {
		if err := conn.Exec(ctx, ddl); err != nil {
			return fmt.Errorf("recipestore: self-provision %q: %w", ddl, err)
		}
	}
	return nil
}

// nextIDSQL is the per-table next-id statement: MAX(id)+1 with the floor 5
// (an empty table, or one holding only the baseline 1-4, assigns 5).
func nextIDSQL(table string) string {
	return "SELECT GREATEST(COALESCE(MAX(id), 0) + 1, 5) FROM public." + table
}

// NextID returns the next EDS-assigned id in the parameter's entry table
// (MAX(id)+1, floor 5).
func (s *Store) NextID(ctx context.Context, parameter string) (int, error) {
	table, err := parameterTable(parameter)
	if err != nil {
		return 0, err
	}
	conn, err := s.open(ctx)
	if err != nil {
		return 0, fmt.Errorf("recipestore: connect: %w", err)
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, nextIDSQL(table))
	if err != nil {
		return 0, fmt.Errorf("recipestore: next id: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("recipestore: next id: %w", err)
		}
		return 0, errors.New("recipestore: next id returned no row")
	}
	var id int
	if err := rows.Scan(&id); err != nil {
		return 0, fmt.Errorf("recipestore: next id: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("recipestore: next id: %w", err)
	}
	return id, nil
}

// NextRecipeID returns the next EDS-assigned parameterset_id in the recipes
// table (MAX(parameterset_id)+1, floor 5).
func (s *Store) NextRecipeID(ctx context.Context) (int, error) {
	conn, err := s.open(ctx)
	if err != nil {
		return 0, fmt.Errorf("recipestore: connect: %w", err)
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, "SELECT GREATEST(COALESCE(MAX(parameterset_id), 0) + 1, 5) FROM public.recipes")
	if err != nil {
		return 0, fmt.Errorf("recipestore: next recipe id: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("recipestore: next recipe id: %w", err)
		}
		return 0, errors.New("recipestore: next recipe id returned no row")
	}
	var id int
	if err := rows.Scan(&id); err != nil {
		return 0, fmt.Errorf("recipestore: next recipe id: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("recipestore: next recipe id: %w", err)
	}
	return id, nil
}

// Save upserts the design's entries and recipe locator rows in one
// transaction (ruled R2: the persistence is a step in the flow, one
// transaction). Entries are INSERTed when the row is new and UPDATEd when
// the id was assigned earlier (the live-locators ruling: a saved design's
// resolved values track its entries until the next save); recipes are
// INSERTed for new parameterset_ids and their four FK columns UPDATEd for
// ids this process assigned. Every write targets an id >= 5: ids 1-4 are
// refused with ErrReservedID. It returns the number of entries and recipes
// written.
func (s *Store) Save(ctx context.Context, entries []Entry, recipes []Recipe) (int, int, error) {
	// Pre-validate every write before touching the database: a refused id or
	// an out-of-domain value must not open a partial transaction.
	for _, e := range entries {
		if _, err := parameterTable(e.Parameter); err != nil {
			return 0, 0, err
		}
		if err := ValueDomain(e.Parameter, e.Value); err != nil {
			return 0, 0, err
		}
		if err := writableID(e.ID); err != nil {
			return 0, 0, err
		}
	}
	for _, r := range recipes {
		if err := writableID(r.ParametersetID); err != nil {
			return 0, 0, err
		}
	}

	conn, err := s.open(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("recipestore: connect: %w", err)
	}
	defer conn.Close(ctx)

	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("recipestore: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	for _, e := range entries {
		table, _ := parameterTable(e.Parameter)
		if err := upsertEntry(ctx, tx, table, e); err != nil {
			return 0, 0, fmt.Errorf("recipestore: save entry %s/%d: %w", e.Parameter, e.ID, err)
		}
	}
	for _, r := range recipes {
		if err := upsertRecipe(ctx, tx, r); err != nil {
			return 0, 0, fmt.Errorf("recipestore: save recipe %d: %w", r.ParametersetID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("recipestore: commit: %w", err)
	}
	committed = true
	return len(entries), len(recipes), nil
}

// upsertEntry INSERTs a new entry row or UPDATEs the value of an existing
// row (the live-locators ruling).
func upsertEntry(ctx context.Context, tx Tx, table string, e Entry) error {
	rows, err := tx.Query(ctx, "SELECT 1 FROM public."+table+" WHERE id = $1", e.ID)
	if err != nil {
		return fmt.Errorf("check entry: %w", err)
	}
	exists := false
	if rows.Next() {
		var one int
		if err := rows.Scan(&one); err != nil {
			rows.Close()
			return fmt.Errorf("check entry: %w", err)
		}
		exists = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("check entry: %w", err)
	}
	if exists {
		if err := tx.Exec(ctx, "UPDATE public."+table+" SET value = $2 WHERE id = $1", e.ID, e.Value); err != nil {
			return fmt.Errorf("update entry: %w", err)
		}
		return nil
	}
	if err := tx.Exec(ctx, "INSERT INTO public."+table+" (id, value) VALUES ($1, $2)", e.ID, e.Value); err != nil {
		return fmt.Errorf("insert entry: %w", err)
	}
	return nil
}

// upsertRecipe INSERTs a new locator row or UPDATEs the four FK columns of
// an existing row.
func upsertRecipe(ctx context.Context, tx Tx, r Recipe) error {
	rows, err := tx.Query(ctx, "SELECT 1 FROM public.recipes WHERE parameterset_id = $1", r.ParametersetID)
	if err != nil {
		return fmt.Errorf("check recipe: %w", err)
	}
	exists := false
	if rows.Next() {
		var one int
		if err := rows.Scan(&one); err != nil {
			rows.Close()
			return fmt.Errorf("check recipe: %w", err)
		}
		exists = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("check recipe: %w", err)
	}
	if exists {
		if err := tx.Exec(ctx,
			"UPDATE public.recipes SET arrival_rate_id = $2, service_rate_id = $3, run_duration_id = $4, seed_policy_id = $5 WHERE parameterset_id = $1",
			r.ParametersetID, r.ArrivalRateID, r.ServiceRateID, r.RunDurationID, r.SeedPolicyID); err != nil {
			return fmt.Errorf("update recipe: %w", err)
		}
		return nil
	}
	if err := tx.Exec(ctx,
		"INSERT INTO public.recipes (parameterset_id, arrival_rate_id, service_rate_id, run_duration_id, seed_policy_id) VALUES ($1, $2, $3, $4, $5)",
		r.ParametersetID, r.ArrivalRateID, r.ServiceRateID, r.RunDurationID, r.SeedPolicyID); err != nil {
		return fmt.Errorf("insert recipe: %w", err)
	}
	return nil
}

const viewSelectSQL = "SELECT parameterset_id, arrival_rate, service_rate, run_duration, seed_policy FROM public.simulation_parameters ORDER BY parameterset_id"

// ViewRows returns the full simulation_parameters view projection — all
// rows, baseline included — for the API's db_rows surface.
func (s *Store) ViewRows(ctx context.Context) ([]ViewRow, error) {
	conn, err := s.open(ctx)
	if err != nil {
		return nil, fmt.Errorf("recipestore: connect: %w", err)
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, viewSelectSQL)
	if err != nil {
		return nil, fmt.Errorf("recipestore: view rows: %w", err)
	}
	defer rows.Close()
	var out []ViewRow
	for rows.Next() {
		var v ViewRow
		if err := rows.Scan(&v.ParametersetID, &v.ArrivalRate, &v.ServiceRate, &v.RunDuration, &v.SeedPolicy); err != nil {
			return nil, fmt.Errorf("recipestore: view rows: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recipestore: view rows: %w", err)
	}
	return out, nil
}

const resolveSQL = "SELECT parameterset_id FROM public.simulation_parameters WHERE parameterset_id = ANY($1)"

// Resolve reports which of the given parameterset_ids do NOT resolve in the
// simulation_parameters view (the Start pre-flight: every batch-referenced
// id must resolve, else the scenario would reach the Translator's
// RecipeError path — a silent stall). It returns the missing ids in the
// requested order (deduplicated).
func (s *Store) Resolve(ctx context.Context, ids []int) ([]int, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	conn, err := s.open(ctx)
	if err != nil {
		return nil, fmt.Errorf("recipestore: connect: %w", err)
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, resolveSQL, ids)
	if err != nil {
		return nil, fmt.Errorf("recipestore: resolve: %w", err)
	}
	defer rows.Close()
	resolved := map[int]bool{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("recipestore: resolve: %w", err)
		}
		resolved[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recipestore: resolve: %w", err)
	}
	var missing []int
	seen := map[int]bool{}
	for _, id := range ids {
		if !resolved[id] && !seen[id] {
			missing = append(missing, id)
			seen[id] = true
		}
	}
	return missing, nil
}
