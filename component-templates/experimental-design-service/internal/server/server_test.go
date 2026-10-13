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

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/D4NS3U/cbse/component-templates/experimental-design-service/internal/publisher"
	"github.com/D4NS3U/cbse/component-templates/experimental-design-service/internal/recipestore"
	"github.com/D4NS3U/cbse/component-templates/experimental-design-service/internal/wire"
)

// ---- the in-memory fake store (the Store seam) ----

type viewRow struct {
	ParametersetID int
	ArrivalRate    int
	ServiceRate    int
	RunDuration    int
	SeedPolicy     int
}

// fakeStore is the in-memory Store the server tests drive: it models the
// EDS id assignment (floor 5), the single-transaction save into the entry
// tables + recipes, and the view projection the pre-flight resolves
// against.
type fakeStore struct {
	mu         sync.Mutex
	nextEntry  map[string]int
	nextRecipe int
	entries    map[string]map[int]int
	recipes    map[int][4]int
	view       map[int]viewRow
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		nextEntry: map[string]int{
			recipestore.ParamArrivalRate: 5,
			recipestore.ParamServiceRate: 5,
			recipestore.ParamRunDuration: 5,
			recipestore.ParamSeedPolicy:  5,
		},
		nextRecipe: 5,
		entries:    map[string]map[int]int{},
		recipes:    map[int][4]int{},
		view:       map[int]viewRow{},
	}
}

func (f *fakeStore) NextID(_ context.Context, parameter string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.nextEntry[parameter]; ok {
		return id, nil
	}
	return 0, fmt.Errorf("unknown parameter %q", parameter)
}

func (f *fakeStore) NextRecipeID(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nextRecipe, nil
}

func (f *fakeStore) Save(_ context.Context, entries []recipestore.Entry, recipes []recipestore.Recipe) (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range entries {
		if e.ID < 5 {
			return 0, 0, fmt.Errorf("%w: id %d", recipestore.ErrReservedID, e.ID)
		}
		m := f.entries[e.Parameter]
		if m == nil {
			m = map[int]int{}
			f.entries[e.Parameter] = m
		}
		m[e.ID] = e.Value
		if f.nextEntry[e.Parameter] <= e.ID {
			f.nextEntry[e.Parameter] = e.ID + 1
		}
	}
	for _, r := range recipes {
		if r.ParametersetID < 5 {
			return 0, 0, fmt.Errorf("%w: id %d", recipestore.ErrReservedID, r.ParametersetID)
		}
		f.recipes[r.ParametersetID] = [4]int{r.ArrivalRateID, r.ServiceRateID, r.RunDurationID, r.SeedPolicyID}
		if f.nextRecipe <= r.ParametersetID {
			f.nextRecipe = r.ParametersetID + 1
		}
		// The view projection tracks the saved entries (live locators).
		f.view[r.ParametersetID] = viewRow{
			ParametersetID: r.ParametersetID,
			ArrivalRate:    f.entries[recipestore.ParamArrivalRate][r.ArrivalRateID],
			ServiceRate:    f.entries[recipestore.ParamServiceRate][r.ServiceRateID],
			RunDuration:    f.entries[recipestore.ParamRunDuration][r.RunDurationID],
			SeedPolicy:     f.entries[recipestore.ParamSeedPolicy][r.SeedPolicyID],
		}
	}
	return len(entries), len(recipes), nil
}

func (f *fakeStore) ViewRows(context.Context) ([]recipestore.ViewRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recipestore.ViewRow, 0, len(f.view))
	for _, v := range f.view {
		out = append(out, recipestore.ViewRow{
			ParametersetID: v.ParametersetID,
			ArrivalRate:    v.ArrivalRate,
			ServiceRate:    v.ServiceRate,
			RunDuration:    v.RunDuration,
			SeedPolicy:     v.SeedPolicy,
		})
	}
	return out, nil
}

func (f *fakeStore) Resolve(_ context.Context, ids []int) ([]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var missing []int
	for _, id := range ids {
		if _, ok := f.view[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing, nil
}

// ---- the fake NATS transport (the fake-NATS pattern) ----

type fakeNats struct {
	mu            sync.Mutex
	reply         string
	requestFails  int
	publishErr    error
	publishedData []string
	publishedSubj []string
	requested     [][]byte
}

func (f *fakeNats) Request(_ context.Context, _ string, data []byte, _ time.Duration) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requested = append(f.requested, append([]byte(nil), data...))
	if f.requestFails > 0 {
		f.requestFails--
		return nil, errors.New("fake: request timeout")
	}
	return []byte(f.reply), nil
}

func (f *fakeNats) Publish(_ context.Context, subject string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publishErr != nil {
		return f.publishErr
	}
	f.publishedData = append(f.publishedData, string(data))
	f.publishedSubj = append(f.publishedSubj, subject)
	return nil
}

func (f *fakeNats) Close() {}

func (f *fakeNats) lastPublish() (subject, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.publishedData) == 0 {
		return "", ""
	}
	return f.publishedSubj[len(f.publishedSubj)-1], f.publishedData[len(f.publishedData)-1]
}

// ---- the app under test ----

var testIdentity = wire.Identity{
	ExperimentUID: "aabbccdd-1122-3344-5566-77889900aabb",
	Namespace:     "ns",
	Project:       "exp",
}

const readyReply = `{"status":"ready","batch_subject":"cbse.ns.exp.eds.scenarios","experiment_type":"standard"}`

// newApp wires the App over the fake store and a real Publisher over the
// fake NATS transport (fast, bounded skeleton knobs).
func newApp(t *testing.T, store *fakeStore, reply string) (*App, *fakeNats) {
	t.Helper()
	nc := &fakeNats{reply: reply}
	dial := func(ctx context.Context) (publisher.Conn, error) { return nc, nil }
	pub, err := publisher.New(testIdentity, publisher.Config{
		AvailabilitySubject: "cbse.ns.exp.eds.scenarios.available",
		ConnectAttempts:     3,
		RequestAttempts:     3,
		RetryDelay:          time.Millisecond,
		RequestTimeout:      10 * time.Millisecond,
	}, dial)
	if err != nil {
		t.Fatalf("publisher: %v", err)
	}
	app := New(Deps{Identity: testIdentity, Store: store, Publisher: pub})
	return app, nc
}

// do issues one HTTP request against the app and returns the recorder.
func do(t *testing.T, app *App, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

// decodeBody decodes a JSON response body into v.
func mustDecode(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
}

// designA builds the minimal full design: four entries (ids 5-8), one recipe
// (parameterset 5), one scenario (parameterset 5).
func designA(t *testing.T, app *App) {
	t.Helper()
	for param, value := range map[string]int{
		"arrival_rate": 2, "service_rate": 4, "run_duration": 100, "seed_policy": 1001,
	} {
		w := do(t, app, http.MethodPost, "/api/entries", fmt.Sprintf(`{"parameter":%q,"value":%d}`, param, value))
		if w.Code != http.StatusOK {
			t.Fatalf("add entry %s: %d %s", param, w.Code, w.Body.String())
		}
	}
	w := do(t, app, http.MethodPost, "/api/recipes", `{"arrival_rate_id":5,"service_rate_id":5,"run_duration_id":5,"seed_policy_id":5}`)
	if w.Code != http.StatusOK {
		t.Fatalf("add recipe: %d %s", w.Code, w.Body.String())
	}
	w = do(t, app, http.MethodPost, "/api/scenarios", `{"priority":1,"number_of_reps":20,"confidence_metric":0.91,"parameterset_id":5}`)
	if w.Code != http.StatusOK {
		t.Fatalf("add scenario: %d %s", w.Code, w.Body.String())
	}
}

// TestServeUIAsset is the UI-asset test: GET / serves the embedded page.
func TestServeUIAsset(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	w := do(t, app, http.MethodGet, "/", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content type = %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Experimental Design Service") || !strings.Contains(body, "Finish Experiment") {
		t.Fatalf("the embedded page must render the four action areas")
	}
}

// TestStateEmpty is the state test for the fresh process: empty design,
// no flow, no db rows (the ruled in-memory state at startup).
func TestStateEmpty(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	w := do(t, app, http.MethodGet, "/api/state", "")
	if w.Code != http.StatusOK {
		t.Fatalf("state = %d", w.Code)
	}
	var st stateJSON
	mustDecode(t, w, &st)
	if len(st.Design.Entries) != 0 || len(st.Design.Recipes) != 0 || len(st.Design.Scenarios) != 0 {
		t.Fatalf("design must be empty at start: %+v", st.Design)
	}
	if st.Flow.Saved || st.Flow.Started || st.Flow.Finalized {
		t.Fatalf("flow must be fresh: %+v", st.Flow)
	}
	if len(st.DBRows) != 0 {
		t.Fatalf("db_rows must be empty on the fresh store: %+v", st.DBRows)
	}
}

// TestEntryCreate covers the entry endpoint: valid creation returns the
// assigned id; an unknown parameter and an out-of-domain value are 400s.
func TestEntryCreate(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	w := do(t, app, http.MethodPost, "/api/entries", `{"parameter":"arrival_rate","value":2}`)
	if w.Code != http.StatusOK {
		t.Fatalf("entry = %d %s", w.Code, w.Body.String())
	}
	var r struct {
		ID int `json:"id"`
	}
	mustDecode(t, w, &r)
	if r.ID != 5 {
		t.Fatalf("first entry id = %d, want the floor 5", r.ID)
	}
	if w := do(t, app, http.MethodPost, "/api/entries", `{"parameter":"bogus","value":2}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown parameter = %d", w.Code)
	}
	if w := do(t, app, http.MethodPost, "/api/entries", `{"parameter":"arrival_rate","value":0}`); w.Code != http.StatusBadRequest {
		t.Fatalf("zero value = %d", w.Code)
	}
	if w := do(t, app, http.MethodPost, "/api/entries", `{"parameter":"seed_policy","value":-1}`); w.Code != http.StatusBadRequest {
		t.Fatalf("negative seed_policy = %d", w.Code)
	}
}

// TestRecipeCreate covers the recipe endpoint: all four FKs must reference
// entries assigned in this process.
func TestRecipeCreate(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	if w := do(t, app, http.MethodPost, "/api/recipes", `{"arrival_rate_id":5,"service_rate_id":5,"run_duration_id":5,"seed_policy_id":5}`); w.Code != http.StatusBadRequest {
		t.Fatalf("recipe before any entry = %d (want 400)", w.Code)
	}
	designA(t, app)
	// designA already added a recipe; a second one is fresh.
	if w := do(t, app, http.MethodPost, "/api/recipes", `{"arrival_rate_id":5,"service_rate_id":5,"run_duration_id":5,"seed_policy_id":5}`); w.Code != http.StatusOK {
		t.Fatalf("second recipe = %d %s", w.Code, w.Body.String())
	}
	// A FK that references no entry in this process is refused.
	if w := do(t, app, http.MethodPost, "/api/recipes", `{"arrival_rate_id":99,"service_rate_id":5,"run_duration_id":5,"seed_policy_id":5}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unassigned FK = %d (want 400)", w.Code)
	}
}

// TestScenarioCreate covers the scenario endpoint: the domain contract and
// the recipe reference.
func TestScenarioCreate(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	if w := do(t, app, http.MethodPost, "/api/scenarios", `{"priority":1,"number_of_reps":20,"confidence_metric":0.91,"parameterset_id":5}`); w.Code != http.StatusBadRequest {
		t.Fatalf("scenario before any recipe = %d (want 400)", w.Code)
	}
	designA(t, app)
	if w := do(t, app, http.MethodPost, "/api/scenarios", `{"priority":0,"number_of_reps":20,"confidence_metric":0.91,"parameterset_id":5}`); w.Code != http.StatusBadRequest {
		t.Fatalf("zero priority = %d", w.Code)
	}
	if w := do(t, app, http.MethodPost, "/api/scenarios", `{"priority":1,"number_of_reps":100001,"confidence_metric":0.91,"parameterset_id":5}`); w.Code != http.StatusBadRequest {
		t.Fatalf("reps above max = %d", w.Code)
	}
	if w := do(t, app, http.MethodPost, "/api/scenarios", `{"priority":1,"number_of_reps":20,"confidence_metric":0,"parameterset_id":5}`); w.Code != http.StatusBadRequest {
		t.Fatalf("zero metric = %d", w.Code)
	}
	if w := do(t, app, http.MethodPost, "/api/scenarios", `{"priority":1,"number_of_reps":20,"confidence_metric":0.91,"parameterset_id":99}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown recipe = %d", w.Code)
	}
	w := do(t, app, http.MethodPost, "/api/scenarios", `{"priority":2,"number_of_reps":30,"confidence_metric":0.5,"parameterset_id":5}`)
	if w.Code != http.StatusOK {
		t.Fatalf("valid scenario = %d %s", w.Code, w.Body.String())
	}
}

// TestDeleteScenario covers the delete endpoint: an unpublished scenario is
// removed; a missing index is a 404.
func TestDeleteScenario(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	designA(t, app)
	if w := do(t, app, http.MethodDelete, "/api/scenarios/0", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}
	if w := do(t, app, http.MethodDelete, "/api/scenarios/0", ""); w.Code != http.StatusNotFound {
		t.Fatalf("delete missing = %d", w.Code)
	}
	if w := do(t, app, http.MethodDelete, "/api/scenarios/x", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad index = %d", w.Code)
	}
}

// TestSave is the save test: the design's entries and recipes land in one
// transaction and the response reports the counts.
func TestSave(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	designA(t, app)
	w := do(t, app, http.MethodPost, "/api/save", "")
	if w.Code != http.StatusOK {
		t.Fatalf("save = %d %s", w.Code, w.Body.String())
	}
	var r struct {
		SavedEntries int `json:"saved_entries"`
		SavedRecipes int `json:"saved_recipes"`
	}
	mustDecode(t, w, &r)
	if r.SavedEntries != 4 || r.SavedRecipes != 1 {
		t.Fatalf("save counts = %d/%d, want 4/1", r.SavedEntries, r.SavedRecipes)
	}
	// The flow state reflects the save.
	st := getState(t, app)
	if !st.Flow.Saved {
		t.Fatalf("flow.saved must be true after the save")
	}
	// The db_rows surface now shows the saved recipe's projection.
	if len(st.DBRows) != 1 || st.DBRows[0].ParametersetID != 5 || st.DBRows[0].ArrivalRate != 2 {
		t.Fatalf("db_rows after save = %+v", st.DBRows)
	}
}

// TestStartPreflightRefusal is the pre-flight test: a batch-referenced
// parameterset_id that does not resolve in the view (the design was never
// saved) is a clean refusal naming the missing ids — the guard against the
// silent RecipeError stall.
func TestStartPreflightRefusal(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	designA(t, app)
	// No save: the recipe does not resolve in the view.
	w := do(t, app, http.MethodPost, "/api/start", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("start = %d (want 409), body %s", w.Code, w.Body.String())
	}
	var e struct {
		Error string `json:"error"`
	}
	mustDecode(t, w, &e)
	if !strings.Contains(e.Error, "parameterset_id") || !strings.Contains(e.Error, "5") {
		t.Fatalf("refusal must name the missing id(s): %q", e.Error)
	}
}

// TestStartEmptyDeltaRefusal is the empty-delta test: with nothing new to
// publish (no scenarios at all, or all already published) Start is a clean
// refusal.
func TestStartEmptyDeltaRefusal(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	w := do(t, app, http.MethodPost, "/api/start", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("start with no scenarios = %d (want 409)", w.Code)
	}
	var e struct {
		Error string `json:"error"`
	}
	mustDecode(t, w, &e)
	if !strings.Contains(e.Error, "no new scenarios to publish") {
		t.Fatalf("refusal must be descriptive: %q", e.Error)
	}
}

// TestStartSuccess is the happy path: pre-flight passes, the handshake
// declares standard with the verbatim echo, and the batch of exactly the
// not-yet-published scenarios publishes to the reply's batch subject.
func TestStartSuccess(t *testing.T) {
	app, nc := newApp(t, newFakeStore(), readyReply)
	designA(t, app)
	do(t, app, http.MethodPost, "/api/save", "")
	w := do(t, app, http.MethodPost, "/api/start", "")
	if w.Code != http.StatusOK {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	var r struct {
		BatchID      string `json:"batch_id"`
		Status       string `json:"status"`
		Published    int    `json:"published"`
		BatchSubject string `json:"batch_subject"`
	}
	mustDecode(t, w, &r)
	if r.Status != "inserted" || r.Published != 1 || r.BatchSubject != "cbse.ns.exp.eds.scenarios" {
		t.Fatalf("start result = %+v", r)
	}
	subject, data := nc.lastPublish()
	if subject != "cbse.ns.exp.eds.scenarios" {
		t.Fatalf("published to %q, want the reply's batch subject", subject)
	}
	batch, err := wire.DecodeScenarioBatch([]byte(data))
	if err != nil {
		t.Fatalf("published payload: %v", err)
	}
	if batch.Final || len(batch.Scenarios) != 1 || batch.Scenarios[0].RecipeInfo.ParametersetID != 5 {
		t.Fatalf("published batch = %+v", batch)
	}
	// The flow state: started, the scenario published, the handshake ready.
	st := getState(t, app)
	if !st.Flow.Started || !st.Flow.Saved {
		t.Fatalf("flow = %+v", st.Flow)
	}
	if len(st.Design.Scenarios) != 1 || !st.Design.Scenarios[0].Published {
		t.Fatalf("scenario must be marked published: %+v", st.Design.Scenarios)
	}
	if st.Flow.Handshake == nil || st.Flow.Handshake.Status != "ready" || st.Flow.Handshake.BatchSubject != "cbse.ns.exp.eds.scenarios" {
		t.Fatalf("last_handshake = %+v", st.Flow.Handshake)
	}
}

// TestStartOnlyPublishesUnpublished is the delta test: a second Start
// publishes only the new scenario, not the already-published one.
func TestStartOnlyPublishesUnpublished(t *testing.T) {
	app, nc := newApp(t, newFakeStore(), readyReply)
	designA(t, app)
	do(t, app, http.MethodPost, "/api/save", "")
	if w := do(t, app, http.MethodPost, "/api/start", ""); w.Code != http.StatusOK {
		t.Fatalf("first start = %d %s", w.Code, w.Body.String())
	}
	// All scenarios are now published: the immediate re-start is refused.
	if w := do(t, app, http.MethodPost, "/api/start", ""); w.Code != http.StatusConflict {
		t.Fatalf("start with nothing new = %d (want 409)", w.Code)
	}
	// A fresh scenario re-arms the flow.
	do(t, app, http.MethodPost, "/api/scenarios", `{"priority":2,"number_of_reps":30,"confidence_metric":0.5,"parameterset_id":5}`)
	w := do(t, app, http.MethodPost, "/api/start", "")
	if w.Code != http.StatusOK {
		t.Fatalf("second start = %d %s", w.Code, w.Body.String())
	}
	_, data := nc.lastPublish()
	batch, err := wire.DecodeScenarioBatch([]byte(data))
	if err != nil {
		t.Fatalf("second batch payload: %v", err)
	}
	if len(batch.Scenarios) != 1 || batch.Scenarios[0].Priority != 2 {
		t.Fatalf("second batch must contain exactly the new scenario: %+v", batch.Scenarios)
	}
}

// TestStartBatchProjectMatchesIdentity is the identity poison test at the
// API level: every batch the server drives carries the identity's project
// (SIMULATIONPROJECTNAME) — a mismatched project is refused by the wire
// layer before publication (wire.TestBatchIdentityMismatchPoison), and the
// server never constructs a payload with another project.
func TestStartBatchProjectMatchesIdentity(t *testing.T) {
	app, nc := newApp(t, newFakeStore(), readyReply)
	designA(t, app)
	do(t, app, http.MethodPost, "/api/save", "")
	if w := do(t, app, http.MethodPost, "/api/start", ""); w.Code != http.StatusOK {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	_, data := nc.lastPublish()
	if !strings.Contains(data, `"project":"exp"`) {
		t.Fatalf("the published batch must carry the identity project: %s", data)
	}
	// And the wire layer refuses a mismatched project outright.
	batch := &wire.ScenarioBatch{BatchID: "b", Project: "other", Scenarios: []wire.ScenarioRecord{}}
	if err := wire.ValidateBatch(batch, testIdentity); err == nil || !strings.Contains(err.Error(), "poison") {
		t.Fatalf("mismatched project must be poison: %v", err)
	}
}

// TestStartCleanSMRefusal is the handshake refusal test: while the
// experiment is not InProgress the SM replies status=error — the Start is a
// clean 409 carrying the SM's reason, and the flow state surfaces it.
func TestStartCleanSMRefusal(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), `{"status":"error","reason":"experiment Pending"}`)
	designA(t, app)
	do(t, app, http.MethodPost, "/api/save", "")
	w := do(t, app, http.MethodPost, "/api/start", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("start = %d (want 409), body %s", w.Code, w.Body.String())
	}
	var e struct {
		Error string `json:"error"`
	}
	mustDecode(t, w, &e)
	if e.Error != "experiment Pending" {
		t.Fatalf("the SM's reason must be surfaced verbatim: %q", e.Error)
	}
	st := getState(t, app)
	if st.Flow.Handshake == nil || st.Flow.Handshake.Status != "error" || st.Flow.Handshake.Reason != "experiment Pending" {
		t.Fatalf("last_handshake = %+v", st.Flow.Handshake)
	}
	if st.Flow.Started {
		t.Fatalf("the flow must not be started after a refused handshake")
	}
}

// TestStartProtocolError is the protocol-failure test: a ready reply with a
// missing experiment_type echo (an SM without the finalization protocol) is
// a 502 with the clean, UI-visible reason.
func TestStartProtocolError(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), `{"status":"ready","batch_subject":"cbse.ns.exp.eds.scenarios"}`)
	designA(t, app)
	do(t, app, http.MethodPost, "/api/save", "")
	w := do(t, app, http.MethodPost, "/api/start", "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("start = %d (want 502), body %s", w.Code, w.Body.String())
	}
	var e struct {
		Error string `json:"error"`
	}
	mustDecode(t, w, &e)
	if !strings.Contains(e.Error, "experiment_type") {
		t.Fatalf("reason must name the missing echo: %q", e.Error)
	}
}

// TestStartPublishFailure is the transport-failure test: a publish the
// server does not confirm (no PubAck) is a 502.
func TestStartPublishFailure(t *testing.T) {
	app, nc := newApp(t, newFakeStore(), readyReply)
	nc.publishErr = errors.New("fake: no puback")
	designA(t, app)
	do(t, app, http.MethodPost, "/api/save", "")
	w := do(t, app, http.MethodPost, "/api/start", "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("start = %d (want 502)", w.Code)
	}
	st := getState(t, app)
	if st.Flow.Publish == nil || st.Flow.Publish.Status != "error" {
		t.Fatalf("last_publish must report the failure: %+v", st.Flow.Publish)
	}
}

// TestFinishBeforeStartRefused is the Finish precondition test: Finish is
// only allowed after a successful Start.
func TestFinishBeforeStartRefused(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	w := do(t, app, http.MethodPost, "/api/finish", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("finish before start = %d (want 409), body %s", w.Code, w.Body.String())
	}
}

// TestFinishSuccess is the Finish test: the finalization batch (scenarios
// [], final true) publishes and the flow is finalized.
func TestFinishSuccess(t *testing.T) {
	app, nc := newApp(t, newFakeStore(), readyReply)
	designA(t, app)
	do(t, app, http.MethodPost, "/api/save", "")
	if w := do(t, app, http.MethodPost, "/api/start", ""); w.Code != http.StatusOK {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	w := do(t, app, http.MethodPost, "/api/finish", "")
	if w.Code != http.StatusOK {
		t.Fatalf("finish = %d %s", w.Code, w.Body.String())
	}
	var r struct {
		BatchID string `json:"batch_id"`
		Status  string `json:"status"`
	}
	mustDecode(t, w, &r)
	if r.Status != "inserted" || r.BatchID == "" {
		t.Fatalf("finish result = %+v", r)
	}
	// The finalization payload: scenarios [] present, final true.
	_, data := nc.lastPublish()
	if !strings.Contains(data, `"scenarios":[]`) || !strings.Contains(data, `"final":true`) {
		t.Fatalf("finalization payload = %s", data)
	}
	st := getState(t, app)
	if !st.Flow.Finalized {
		t.Fatalf("flow must be finalized: %+v", st.Flow)
	}
}

// TestFinishIrrevocability is the irrevocability test: a second Finish is
// refused, and any Start after finalization is refused.
func TestFinishIrrevocability(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	designA(t, app)
	do(t, app, http.MethodPost, "/api/save", "")
	if w := do(t, app, http.MethodPost, "/api/start", ""); w.Code != http.StatusOK {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	if w := do(t, app, http.MethodPost, "/api/finish", ""); w.Code != http.StatusOK {
		t.Fatalf("finish = %d %s", w.Code, w.Body.String())
	}
	// The second Finish is refused.
	w := do(t, app, http.MethodPost, "/api/finish", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("second finish = %d (want 409), body %s", w.Code, w.Body.String())
	}
	// A Start after finalization is refused.
	do(t, app, http.MethodPost, "/api/scenarios", `{"priority":2,"number_of_reps":30,"confidence_metric":0.5,"parameterset_id":5}`)
	w = do(t, app, http.MethodPost, "/api/start", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("start after finish = %d (want 409), body %s", w.Code, w.Body.String())
	}
}

// TestDeletePublishedScenarioRefused is the delete guard: a published
// scenario already exists in the Scenario Manager and cannot be deleted.
func TestDeletePublishedScenarioRefused(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	designA(t, app)
	do(t, app, http.MethodPost, "/api/save", "")
	if w := do(t, app, http.MethodPost, "/api/start", ""); w.Code != http.StatusOK {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	w := do(t, app, http.MethodDelete, "/api/scenarios/0", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("delete published = %d (want 409), body %s", w.Code, w.Body.String())
	}
}

// TestMalformedBodyRejections covers the strict decode: unknown fields and
// trailing tokens are refused with a 400.
func TestMalformedBodyRejections(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	if w := do(t, app, http.MethodPost, "/api/entries", `{"parameter":"arrival_rate","value":2,"bogus":1}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", w.Code)
	}
	if w := do(t, app, http.MethodPost, "/api/entries", `{"parameter":"arrival_rate","value":2} {}`); w.Code != http.StatusBadRequest {
		t.Fatalf("trailing token = %d", w.Code)
	}
	if w := do(t, app, http.MethodPost, "/api/entries", `{not json`); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed json = %d", w.Code)
	}
}

// TestUnknownEndpoint is the 404 for anything outside the API surface.
func TestUnknownEndpoint(t *testing.T) {
	app, _ := newApp(t, newFakeStore(), readyReply)
	if w := do(t, app, http.MethodGet, "/api/nope", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown endpoint = %d", w.Code)
	}
}

// getState fetches and decodes the current state.
func getState(t *testing.T, app *App) stateJSON {
	t.Helper()
	w := do(t, app, http.MethodGet, "/api/state", "")
	if w.Code != http.StatusOK {
		t.Fatalf("state = %d %s", w.Code, w.Body.String())
	}
	var st stateJSON
	mustDecode(t, w, &st)
	return st
}
