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

package recipestore

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// fakeModel is the in-memory relation model the fake DB maintains: the four
// per-parameter entry tables (id -> value) and the recipes locator table
// (parameterset_id -> [arrival, service, run, seed] FK ids).
type fakeModel struct {
	objects map[string]bool
	entries map[string]map[int]int
	recipes map[int][4]int
}

func newFakeModel() *fakeModel {
	return &fakeModel{
		objects: map[string]bool{},
		entries: map[string]map[int]int{
			ParamArrivalRate: {}, ParamServiceRate: {},
			ParamRunDuration: {}, ParamSeedPolicy: {},
		},
		recipes: map[int][4]int{},
	}
}

// precreatedSchema models the seeded reference image branch (or a
// user-pre-created host schema): all six objects exist and, when seed is
// true, the baseline rows 1-4 are present in all five tables.
func precreatedSchema(seed bool) *fakeModel {
	m := newFakeModel()
	for _, obj := range []string{ParamArrivalRate, ParamServiceRate, ParamRunDuration, ParamSeedPolicy, "recipes", "simulation_parameters"} {
		m.objects[obj] = true
	}
	if !seed {
		return m
	}
	m.entries[ParamArrivalRate] = map[int]int{1: 2, 2: 3, 3: 4, 4: 5}
	m.entries[ParamServiceRate] = map[int]int{1: 4, 2: 5, 3: 7, 4: 8}
	m.entries[ParamRunDuration] = map[int]int{1: 100, 2: 120, 3: 150, 4: 180}
	m.entries[ParamSeedPolicy] = map[int]int{1: 1001, 2: 1002, 3: 1003, 4: 1004}
	m.recipes = map[int][4]int{1: {1, 1, 1, 1}, 2: {2, 2, 2, 2}, 3: {3, 3, 3, 3}, 4: {4, 4, 4, 4}}
	return m
}

func (m *fakeModel) clone() *fakeModel {
	c := newFakeModel()
	for k, v := range m.objects {
		c.objects[k] = v
	}
	for table, rows := range m.entries {
		cp := map[int]int{}
		for id, val := range rows {
			cp[id] = val
		}
		c.entries[table] = cp
	}
	for id, fks := range m.recipes {
		c.recipes[id] = fks
	}
	return c
}

// viewRows derives the simulation_parameters projection (inner-join
// semantics: a recipe projects only when all four FK targets exist).
func (m *fakeModel) viewRows() []ViewRow {
	var out []ViewRow
	for id, fks := range m.recipes {
		a, ok1 := m.entries[ParamArrivalRate][fks[0]]
		s, ok2 := m.entries[ParamServiceRate][fks[1]]
		d, ok3 := m.entries[ParamRunDuration][fks[2]]
		p, ok4 := m.entries[ParamSeedPolicy][fks[3]]
		if !ok1 || !ok2 || !ok3 || !ok4 {
			continue
		}
		out = append(out, ViewRow{ParametersetID: id, ArrivalRate: a, ServiceRate: s, RunDuration: d, SeedPolicy: p})
	}
	// Deterministic order by parameterset_id (mirrors ORDER BY).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].ParametersetID > out[j].ParametersetID; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// fakeDB is the transport-agnostic fake PostgreSQL backend the recipestore
// tests run against. It executes exactly the statements the store issues,
// enforces the PG constraint semantics (value-domain CHECKs, primary-key
// uniqueness, foreign keys) and buffers writes in the open transaction until
// commit.
type fakeDB struct {
	mu     sync.Mutex
	model  *fakeModel
	tx     *fakeModel // non-nil while a transaction is open
	log    []string
	failOn func(sql string) bool
}

func newFakeDB(m *fakeModel) *fakeDB {
	return &fakeDB{model: m}
}

// fresh is the dynamic branch: an empty database with no schema objects.
func fresh() *fakeDB { return newFakeDB(newFakeModel()) }

// seeded models the image branch: pre-created schema with the baseline rows.
func seeded() *fakeDB { return newFakeDB(precreatedSchema(true)) }

func (f *fakeDB) modelFor() *fakeModel {
	if f.tx != nil {
		return f.tx
	}
	return f.model
}

// record stores the executed statement for assertions.
func (f *fakeDB) record(sql string) { f.log = append(f.log, strings.TrimSpace(sql)) }

func (f *fakeDB) executed() []string { return f.log }

// injectFail makes every statement containing needle fail until reset.
func (f *fakeDB) injectFail(needle string) {
	f.failOn = func(sql string) bool { return strings.Contains(sql, needle) }
}

// ---- statement execution (PG-faithful for the store's statement set) ----

// exec dispatches one statement against the active model. Queries return the
// result rows as []map entries (one any per column).
func (f *fakeDB) exec(sql string, args []any) ([][]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn != nil && f.failOn(sql) {
		f.record(sql)
		return nil, fmt.Errorf("fake: injected failure on %q", sql)
	}
	m := f.modelFor()
	sql = strings.TrimSpace(sql)

	// Self-provision DDL (IF NOT EXISTS: create-or-no-op).
	switch {
	case sql == ddlArrivalRate:
		f.record(sql)
		m.objects[ParamArrivalRate] = true
		return nil, nil
	case sql == ddlServiceRate:
		f.record(sql)
		m.objects[ParamServiceRate] = true
		return nil, nil
	case sql == ddlRunDuration:
		f.record(sql)
		m.objects[ParamRunDuration] = true
		return nil, nil
	case sql == ddlSeedPolicy:
		f.record(sql)
		m.objects[ParamSeedPolicy] = true
		return nil, nil
	case sql == ddlRecipes:
		f.record(sql)
		m.objects["recipes"] = true
		return nil, nil
	case sql == ddlView:
		f.record(sql)
		m.objects["simulation_parameters"] = true
		return nil, nil
	}

	// Next-id per table: MAX(id)+1 with the floor 5.
	if strings.HasPrefix(sql, "SELECT GREATEST(COALESCE(MAX(id), 0) + 1, 5) FROM public.") {
		table := strings.TrimPrefix(sql, "SELECT GREATEST(COALESCE(MAX(id), 0) + 1, 5) FROM public.")
		f.record(sql)
		next := 5
		for id := range m.entries[table] {
			if id+1 > next {
				next = id + 1
			}
		}
		return [][]any{{next}}, nil
	}
	if sql == "SELECT GREATEST(COALESCE(MAX(parameterset_id), 0) + 1, 5) FROM public.recipes" {
		f.record(sql)
		next := 5
		for id := range m.recipes {
			if id+1 > next {
				next = id + 1
			}
		}
		return [][]any{{next}}, nil
	}

	// Entry existence check.
	if strings.HasPrefix(sql, "SELECT 1 FROM public.") && strings.HasSuffix(sql, " WHERE id = $1") {
		table := strings.TrimSuffix(strings.TrimPrefix(sql, "SELECT 1 FROM public."), " WHERE id = $1")
		f.record(sql)
		if _, ok := m.entries[table][toInt(args[0])]; ok {
			return [][]any{{1}}, nil
		}
		return nil, nil
	}
	if sql == "SELECT 1 FROM public.recipes WHERE parameterset_id = $1" {
		f.record(sql)
		if _, ok := m.recipes[toInt(args[0])]; ok {
			return [][]any{{1}}, nil
		}
		return nil, nil
	}

	// Entry insert (CHECK value-domain, PRIMARY KEY uniqueness).
	if strings.HasPrefix(sql, "INSERT INTO public.") && strings.HasSuffix(sql, " (id, value) VALUES ($1, $2)") {
		table := strings.TrimSuffix(strings.TrimPrefix(sql, "INSERT INTO public."), " (id, value) VALUES ($1, $2)")
		id, value := toInt(args[0]), toInt(args[1])
		f.record(sql)
		if id <= 0 {
			return nil, fmt.Errorf("fake: check constraint failed (id > 0)")
		}
		if err := domainErr(table, value); err != nil {
			return nil, err
		}
		if _, dup := m.entries[table][id]; dup {
			return nil, fmt.Errorf("fake: duplicate key value violates unique constraint (id)")
		}
		m.entries[table][id] = value
		return nil, nil
	}
	// Entry update (CHECK value-domain on the new value).
	if strings.HasPrefix(sql, "UPDATE public.") && strings.HasSuffix(sql, " SET value = $2 WHERE id = $1") {
		table := strings.TrimSuffix(strings.TrimPrefix(sql, "UPDATE public."), " SET value = $2 WHERE id = $1")
		id, value := toInt(args[0]), toInt(args[1])
		f.record(sql)
		if err := domainErr(table, value); err != nil {
			return nil, err
		}
		if _, ok := m.entries[table][id]; !ok {
			return nil, nil // UPDATE matches no row
		}
		m.entries[table][id] = value
		return nil, nil
	}

	// Recipe insert (PRIMARY KEY, FOREIGN KEYs).
	if sql == "INSERT INTO public.recipes (parameterset_id, arrival_rate_id, service_rate_id, run_duration_id, seed_policy_id) VALUES ($1, $2, $3, $4, $5)" {
		id := toInt(args[0])
		fks := [4]int{toInt(args[1]), toInt(args[2]), toInt(args[3]), toInt(args[4])}
		f.record(sql)
		if id <= 0 {
			return nil, fmt.Errorf("fake: check constraint failed (parameterset_id > 0)")
		}
		if _, dup := m.recipes[id]; dup {
			return nil, fmt.Errorf("fake: duplicate key value violates unique constraint (parameterset_id)")
		}
		if err := fksOK(m, fks); err != nil {
			return nil, err
		}
		m.recipes[id] = fks
		return nil, nil
	}
	// Recipe update (FOREIGN KEYs on the new FK values).
	if sql == "UPDATE public.recipes SET arrival_rate_id = $2, service_rate_id = $3, run_duration_id = $4, seed_policy_id = $5 WHERE parameterset_id = $1" {
		id := toInt(args[0])
		fks := [4]int{toInt(args[1]), toInt(args[2]), toInt(args[3]), toInt(args[4])}
		f.record(sql)
		if _, ok := m.recipes[id]; !ok {
			return nil, nil
		}
		if err := fksOK(m, fks); err != nil {
			return nil, err
		}
		m.recipes[id] = fks
		return nil, nil
	}

	// View projection (inner-join semantics, ORDER BY parameterset_id).
	if sql == viewSelectSQL {
		f.record(sql)
		var out [][]any
		for _, v := range m.viewRows() {
			out = append(out, []any{v.ParametersetID, v.ArrivalRate, v.ServiceRate, v.RunDuration, v.SeedPolicy})
		}
		return out, nil
	}

	// Pre-flight resolve: the referenced ids present in the view.
	if sql == resolveSQL {
		f.record(sql)
		want := toIntSlice(args[0])
		var out [][]any
		for _, id := range want {
			for _, v := range m.viewRows() {
				if v.ParametersetID == id {
					out = append(out, []any{id})
					break
				}
			}
		}
		return out, nil
	}

	f.record(sql)
	return nil, fmt.Errorf("fake: unrecognized statement %q", sql)
}

// domainErr mirrors the DDL CHECKs (PG enforces them on INSERT and UPDATE).
func domainErr(table string, value int) error {
	if table == ParamSeedPolicy {
		if value < 0 {
			return fmt.Errorf("fake: check constraint failed (value >= 0)")
		}
		return nil
	}
	if value <= 0 {
		return fmt.Errorf("fake: check constraint failed (value > 0)")
	}
	return nil
}

// fksOK mirrors the recipes FK constraints.
func fksOK(m *fakeModel, fks [4]int) error {
	tables := []string{ParamArrivalRate, ParamServiceRate, ParamRunDuration, ParamSeedPolicy}
	for i, table := range tables {
		if _, ok := m.entries[table][fks[i]]; !ok {
			return fmt.Errorf("fake: insert or update on table \"recipes\" violates foreign key constraint (%s_id=%d)", table, fks[i])
		}
	}
	return nil
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	}
	panic(fmt.Sprintf("fake: unexpected arg type %T", v))
}

func toIntSlice(v any) []int {
	switch s := v.(type) {
	case []int:
		return s
	case []any:
		out := make([]int, 0, len(s))
		for _, e := range s {
			out = append(out, toInt(e))
		}
		return out
	}
	panic(fmt.Sprintf("fake: unexpected arg type %T", v))
}

// ---- the Conn / Tx / Rows adapters ----

type fakeConn struct{ db *fakeDB }

type fakeTx struct {
	db *fakeDB
}

type fakeRows struct {
	rows [][]any
	idx  int
}

func (f *fakeDB) Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error) {
	return &fakeConn{db: f}, nil
}

func (c *fakeConn) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := c.db.exec(sql, args)
	return err
}

func (c *fakeConn) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := c.db.exec(sql, args)
	if err != nil {
		return nil, err
	}
	return &fakeRows{rows: rows}, nil
}

func (c *fakeConn) Begin(ctx context.Context) (Tx, error) {
	c.db.mu.Lock()
	defer c.db.mu.Unlock()
	if c.db.tx != nil {
		return nil, fmt.Errorf("fake: transaction already open")
	}
	c.db.tx = c.db.model.clone()
	return &fakeTx{db: c.db}, nil
}

func (c *fakeConn) Close(ctx context.Context) error { return nil }

func (t *fakeTx) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := t.db.exec(sql, args)
	return err
}

func (t *fakeTx) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := t.db.exec(sql, args)
	if err != nil {
		return nil, err
	}
	return &fakeRows{rows: rows}, nil
}

func (t *fakeTx) Commit(ctx context.Context) error {
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	if t.db.tx == nil {
		return fmt.Errorf("fake: no open transaction")
	}
	t.db.model = t.db.tx
	t.db.tx = nil
	return nil
}

func (t *fakeTx) Rollback(ctx context.Context) error {
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	if t.db.tx == nil {
		return fmt.Errorf("fake: no open transaction")
	}
	t.db.tx = nil
	return nil
}

func (r *fakeRows) Next() bool {
	if r.idx >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.rows) {
		return fmt.Errorf("fake: no row")
	}
	row := r.rows[r.idx-1]
	for i, d := range dest {
		if i >= len(row) {
			return fmt.Errorf("fake: not enough columns")
		}
		switch p := d.(type) {
		case *int:
			*p = toInt(row[i])
		default:
			return fmt.Errorf("fake: unsupported scan target %T", d)
		}
	}
	return nil
}

func (r *fakeRows) Close() error { return nil }
func (r *fakeRows) Err() error   { return nil }

// ---- the tests ----

// TestEnsureSchemaNoOpOnPreCreatedSchema is the self-provision no-op test:
// against a pre-created matching schema (the seeded reference image branch)
// the startup DDL runs, is a no-op, and leaves the baseline rows intact.
func TestEnsureSchemaNoOpOnPreCreatedSchema(t *testing.T) {
	db := seeded()
	store := New("h", 5432, "u", "p", "d", db)
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	got := db.executed()
	if len(got) != 6 {
		t.Fatalf("self-provision must issue exactly the six canonical DDL statements, got %d: %v", len(got), got)
	}
	for i, want := range canonicalDDL {
		if got[i] != strings.TrimSpace(want) {
			t.Fatalf("statement %d mismatch:\n got  %s\n want %s", i, got[i], want)
		}
	}
	// The baseline rows are untouched (the no-op): the view still projects
	// exactly the seeded ids 1-4 with the seeded values.
	rows, err := store.ViewRows(context.Background())
	if err != nil {
		t.Fatalf("view rows: %v", err)
	}
	want := []ViewRow{
		{1, 2, 4, 100, 1001}, {2, 3, 5, 120, 1002}, {3, 4, 7, 150, 1003}, {4, 5, 8, 180, 1004},
	}
	if len(rows) != len(want) {
		t.Fatalf("baseline view rows = %v, want %v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("baseline row %d = %v, want %v (the self-provision must be a no-op)", i, rows[i], want[i])
		}
	}
}

// TestEnsureSchemaFreshDB is the dynamic branch: an empty host-based DB
// gains all six objects from the startup DDL, with no baseline INSERTs.
func TestEnsureSchemaFreshDB(t *testing.T) {
	db := fresh()
	store := New("h", 5432, "u", "p", "d", db)
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	for _, obj := range []string{ParamArrivalRate, ParamServiceRate, ParamRunDuration, ParamSeedPolicy, "recipes", "simulation_parameters"} {
		if !db.model.objects[obj] {
			t.Fatalf("object %q not created", obj)
		}
	}
	// R3: no baseline INSERTs on the dynamic branch.
	if len(db.model.recipes) != 0 {
		t.Fatalf("dynamic branch must not seed baseline recipes: %v", db.model.recipes)
	}
}

// TestNextIDFloorAndAssignment covers the id rules: an empty table (or one
// holding only the baseline 1-4) assigns 5; subsequent ids grow from
// MAX(id)+1.
func TestNextIDFloorAndAssignment(t *testing.T) {
	empty := fresh()
	// Simulate a self-provisioned empty schema (no rows).
	for _, obj := range []string{ParamArrivalRate, ParamServiceRate, ParamRunDuration, ParamSeedPolicy, "recipes", "simulation_parameters"} {
		empty.model.objects[obj] = true
	}
	store := New("h", 5432, "u", "p", "d", empty)
	ctx := context.Background()
	for _, p := range allParameters {
		id, err := store.NextID(ctx, p)
		if err != nil {
			t.Fatalf("next id %s: %v", p, err)
		}
		if id != 5 {
			t.Fatalf("empty table %s next id = %d, want floor 5", p, id)
		}
	}
	id, err := store.NextRecipeID(ctx)
	if err != nil {
		t.Fatalf("next recipe id: %v", err)
	}
	if id != 5 {
		t.Fatalf("empty recipes next id = %d, want floor 5", id)
	}

	// Baseline-only tables assign 5 as well (ids 1-4 are not EDS space).
	seededDB := seeded()
	s2 := New("h", 5432, "u", "p", "d", seededDB)
	for _, p := range allParameters {
		id, err := s2.NextID(ctx, p)
		if err != nil {
			t.Fatalf("seeded next id %s: %v", p, err)
		}
		if id != 5 {
			t.Fatalf("baseline-only table %s next id = %d, want 5", p, id)
		}
	}

	// Growth: after ids 5 and 7 are assigned, the next is 8.
	empty.model.entries[ParamArrivalRate][5] = 2
	empty.model.entries[ParamArrivalRate][7] = 4
	id, err = store.NextID(ctx, ParamArrivalRate)
	if err != nil {
		t.Fatalf("growth next id: %v", err)
	}
	if id != 8 {
		t.Fatalf("next id after 5,7 = %d, want 8", id)
	}
}

// TestSaveRefusedReservedID covers the 1-4 write refusal in every table:
// an attempted write of an id below the floor is a clean refusal and never
// reaches the database.
func TestSaveRefusedReservedID(t *testing.T) {
	db := fresh()
	for _, obj := range []string{ParamArrivalRate, ParamServiceRate, ParamRunDuration, ParamSeedPolicy, "recipes", "simulation_parameters"} {
		db.model.objects[obj] = true
	}
	store := New("h", 5432, "u", "p", "d", db)
	ctx := context.Background()

	_, _, err := store.Save(ctx,
		[]Entry{{Parameter: ParamArrivalRate, ID: 4, Value: 9}},
		nil)
	if err == nil {
		t.Fatalf("entry id 4 must be refused")
	}
	if !strings.Contains(err.Error(), "read-only reference baseline") {
		t.Fatalf("refusal must be descriptive: %v", err)
	}

	_, _, err = store.Save(ctx,
		nil,
		[]Recipe{{ParametersetID: 2, ArrivalRateID: 5, ServiceRateID: 5, RunDurationID: 5, SeedPolicyID: 5}})
	if err == nil {
		t.Fatalf("recipe id 2 must be refused")
	}

	// Nothing reached the database: no statements were executed.
	if got := len(db.executed()); got != 0 {
		t.Fatalf("refused save must not reach the database, executed %d statements", got)
	}
}

// TestSaveOutOfDomainValue covers the API-level domain rule (mirroring the
// DDL CHECKs): zero/negative values are refused before any write.
func TestSaveOutOfDomainValue(t *testing.T) {
	db := fresh()
	for _, obj := range []string{ParamArrivalRate, ParamServiceRate, ParamRunDuration, ParamSeedPolicy, "recipes", "simulation_parameters"} {
		db.model.objects[obj] = true
	}
	store := New("h", 5432, "u", "p", "d", db)
	ctx := context.Background()
	if _, _, err := store.Save(ctx, []Entry{{Parameter: ParamArrivalRate, ID: 5, Value: 0}}, nil); err == nil {
		t.Fatalf("zero arrival_rate must be refused")
	}
	if _, _, err := store.Save(ctx, []Entry{{Parameter: ParamSeedPolicy, ID: 5, Value: -1}}, nil); err == nil {
		t.Fatalf("negative seed_policy must be refused")
	}
	if _, _, err := store.Save(ctx, []Entry{{Parameter: "bogus", ID: 5, Value: 1}}, nil); err == nil {
		t.Fatalf("unknown parameter must be refused")
	}
	if got := len(db.executed()); got != 0 {
		t.Fatalf("refused save must not reach the database, executed %d statements", got)
	}
}

// TestSaveSingleTransactionUpserts is the single-transaction save test:
// entries are INSERTed when new and UPDATEd when the process assigned them
// before (the live-locators ruling); recipes are INSERTed for new
// parameterset_ids and their locator columns UPDATEd for owned ids — all in
// one transaction.
func TestSaveSingleTransactionUpserts(t *testing.T) {
	db := fresh()
	for _, obj := range []string{ParamArrivalRate, ParamServiceRate, ParamRunDuration, ParamSeedPolicy, "recipes", "simulation_parameters"} {
		db.model.objects[obj] = true
	}
	store := New("h", 5432, "u", "p", "d", db)
	ctx := context.Background()

	// First save: four fresh entries + one fresh recipe (all INSERTs).
	n, m, err := store.Save(ctx,
		[]Entry{
			{Parameter: ParamArrivalRate, ID: 5, Value: 2},
			{Parameter: ParamServiceRate, ID: 5, Value: 4},
			{Parameter: ParamRunDuration, ID: 5, Value: 100},
			{Parameter: ParamSeedPolicy, ID: 5, Value: 1001},
		},
		[]Recipe{{ParametersetID: 5, ArrivalRateID: 5, ServiceRateID: 5, RunDurationID: 5, SeedPolicyID: 5}})
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	if n != 4 || m != 1 {
		t.Fatalf("first save counts = %d/%d, want 4/1", n, m)
	}
	if db.model.entries[ParamArrivalRate][5] != 2 || db.model.recipes[5] != [4]int{5, 5, 5, 5} {
		t.Fatalf("first save did not land: %+v", db.model)
	}

	// Second save: the same owned ids with edited values and an edited
	// locator, plus one new recipe — the entries UPDATE, the owned recipe
	// UPDATEs its FKs, the new recipe INSERTs.
	n, m, err = store.Save(ctx,
		[]Entry{
			{Parameter: ParamArrivalRate, ID: 5, Value: 3},
			{Parameter: ParamServiceRate, ID: 5, Value: 5},
			{Parameter: ParamRunDuration, ID: 5, Value: 120},
			{Parameter: ParamSeedPolicy, ID: 5, Value: 1002},
		},
		[]Recipe{
			{ParametersetID: 5, ArrivalRateID: 5, ServiceRateID: 5, RunDurationID: 5, SeedPolicyID: 5},
			{ParametersetID: 6, ArrivalRateID: 5, ServiceRateID: 5, RunDurationID: 5, SeedPolicyID: 5},
		})
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if n != 4 || m != 2 {
		t.Fatalf("second save counts = %d/%d, want 4/2", n, m)
	}
	// The live-locators ruling: the saved design's resolved values track the
	// edited entries until the next save.
	rows, err := store.ViewRows(ctx)
	if err != nil {
		t.Fatalf("view rows: %v", err)
	}
	want := []ViewRow{{5, 3, 5, 120, 1002}, {6, 3, 5, 120, 1002}}
	if len(rows) != len(want) || rows[0] != want[0] || rows[1] != want[1] {
		t.Fatalf("view after live-locator update = %v, want %v", rows, want)
	}
}

// TestSaveRollbackOnMidTransactionFailure proves atomicity: a failure on a
// late statement rolls back the whole transaction — the earlier entry
// writes in the same save do not land.
func TestSaveRollbackOnMidTransactionFailure(t *testing.T) {
	db := fresh()
	for _, obj := range []string{ParamArrivalRate, ParamServiceRate, ParamRunDuration, ParamSeedPolicy, "recipes", "simulation_parameters"} {
		db.model.objects[obj] = true
	}
	db.injectFail("INSERT INTO public.recipes")
	store := New("h", 5432, "u", "p", "d", db)
	ctx := context.Background()

	_, _, err := store.Save(ctx,
		[]Entry{{Parameter: ParamArrivalRate, ID: 5, Value: 2}},
		[]Recipe{{ParametersetID: 5, ArrivalRateID: 5, ServiceRateID: 5, RunDurationID: 5, SeedPolicyID: 5}})
	if err == nil {
		t.Fatalf("injected failure must surface")
	}
	if db.tx != nil {
		t.Fatalf("transaction must be closed after the failed save")
	}
	if v, ok := db.model.entries[ParamArrivalRate][5]; ok && v != 0 {
		t.Fatalf("the entry write must roll back with the recipe failure, got %d", v)
	}
	if len(db.model.recipes) != 0 {
		t.Fatalf("the recipe write must not land, got %v", db.model.recipes)
	}
}

// TestViewRowsProjection covers the view read: the full projection, all
// rows, baseline included, sorted by parameterset_id.
func TestViewRowsProjection(t *testing.T) {
	db := seeded()
	store := New("h", 5432, "u", "p", "d", db)
	ctx := context.Background()
	rows, err := store.ViewRows(ctx)
	if err != nil {
		t.Fatalf("view rows: %v", err)
	}
	want := []ViewRow{
		{1, 2, 4, 100, 1001}, {2, 3, 5, 120, 1002}, {3, 4, 7, 150, 1003}, {4, 5, 8, 180, 1004},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("row %d = %v, want %v", i, rows[i], want[i])
		}
	}
}

// TestResolvePreflight covers the pre-flight read: resolvable ids return
// empty, unresolvable ids are reported in the requested order (deduped).
func TestResolvePreflight(t *testing.T) {
	db := seeded()
	store := New("h", 5432, "u", "p", "d", db)
	ctx := context.Background()
	if missing, err := store.Resolve(ctx, []int{1, 4}); err != nil || len(missing) != 0 {
		t.Fatalf("baseline ids must resolve: missing %v err %v", missing, err)
	}
	if missing, err := store.Resolve(ctx, []int{99, 7, 99}); err != nil {
		t.Fatalf("resolve: %v", err)
	} else if len(missing) != 2 || missing[0] != 99 || missing[1] != 7 {
		t.Fatalf("missing = %v, want [99 7]", missing)
	}
	if missing, err := store.Resolve(ctx, nil); err != nil || missing != nil {
		t.Fatalf("empty resolve: %v %v", missing, err)
	}
}
