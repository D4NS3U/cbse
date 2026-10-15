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

// Package server is the reference ExperimentalDesignService's HTTP surface:
// the net/http JSON API and the embedded static UI page (a thin Facade over
// the API — every button maps to exactly one endpoint, and the e2e drives
// the same API, ruling R1). It owns the four-step flow state
// (designed → saved → started → finished) derived from the in-memory design
// state + the Detail DB + the last handshake/publish results + the
// finalization flag — never a second source of truth:
//
//   - Design: POST /api/entries, POST /api/recipes, POST /api/scenarios
//   - Save Experiment: POST /api/save (one transaction into the Detail DB)
//   - Start Experiment: POST /api/start (the pre-flight, the handshake, the
//     batch publish of exactly the not-yet-published scenarios)
//   - Finish Experiment: POST /api/finish (the finalization batch,
//     irrevocable: a second Finish and any Start after finalization are
//     refused)
//
// Errors are JSON {"error": "..."} with a 4xx for client-side refusals
// (malformed input, flow-state conflicts) and a 5xx for transport/DB
// failures (the handshake refusal from the SM — the experiment not
// InProgress — surfaces as a 409 carrying the SM's reason).
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/D4NS3U/cbse/component-templates/experimental-design-service/internal/publisher"
	"github.com/D4NS3U/cbse/component-templates/experimental-design-service/internal/recipestore"
	"github.com/D4NS3U/cbse/component-templates/experimental-design-service/internal/wire"
)

//go:embed ui/index.html
var uiFS embed.FS

// Store is the design-layer seam the server drives (the Adapter row: the
// pgx-backed recipestore.Store is the production implementation; the
// in-memory fake in the tests satisfies the same interface). The write
// surface is exactly the entry tables + recipes; the reads are the view
// projection and the pre-flight resolve.
type Store interface {
	// NextID returns the next EDS-assigned id in the parameter's entry
	// table (MAX(id)+1, floor 5) from the persisted rows — the server's
	// issued-id ceiling layers on top for pending (unsaved) designs.
	NextID(ctx context.Context, parameter string) (int, error)
	// NextRecipeID returns the next EDS-assigned parameterset_id (floor 5)
	// from the persisted rows (same visibility as NextID).
	NextRecipeID(ctx context.Context) (int, error)
	// Save upserts the design's entries and recipe locator rows in one
	// transaction; ids 1-4 are refused.
	Save(ctx context.Context, entries []recipestore.Entry, recipes []recipestore.Recipe) (int, int, error)
	// ViewRows returns the full simulation_parameters view projection.
	ViewRows(ctx context.Context) ([]recipestore.ViewRow, error)
	// Resolve reports which of the given parameterset_ids do not resolve in
	// the view (the Start pre-flight).
	Resolve(ctx context.Context, ids []int) ([]int, error)
}

var _ Store = (*recipestore.Store)(nil)

// Deps are the server's dependencies (wired in main).
type Deps struct {
	// Identity is the process identity from the downward-API environment;
	// every payload's project field equals Identity.Project (a mismatch is
	// poison — the wire/publisher layers enforce it before publication).
	Identity  wire.Identity
	Store     Store
	Publisher *publisher.Publisher
}

// App is the service's single stateful object: the design state, the flow
// state, and the HTTP handler.
type App struct {
	deps Deps

	mu        sync.Mutex
	entries   []designEntry
	recipes   []designRecipe
	scenarios []designScenario

	// Issued-id ceilings (settlement erratum 6): the store's NextID/
	// NextRecipeID see only persisted rows, so pending in-memory designs
	// must advance the ceiling here — two unsaved entries of one parameter
	// would otherwise collide on one id (the canonical multi-recipe design
	// flow: design the whole experiment, then Save). The ceilings are
	// monotonic per process and re-seed upward from the store whenever the
	// DB-derived next id is higher (e.g. after a Save persisted rows).
	issuedEntryID  map[string]int
	issuedRecipeID int

	// Flow state (derived; not a second source of truth).
	savedOnce bool
	dirty     bool // entries/recipes changed since the last successful save
	started   bool
	finalized bool

	lastHandshake *handshakeInfo
	lastPublish   *publishInfo
}

// designEntry is one in-memory parameter entry (an EDS-assigned id >= 5 with
// its current value).
type designEntry struct {
	ID        int
	Parameter string
	Value     int
}

// designRecipe is one in-memory recipe locator row.
type designRecipe struct {
	ParametersetID int
	ArrivalRateID  int
	ServiceRateID  int
	RunDurationID  int
	SeedPolicyID   int
}

// designScenario is one in-memory scenario bound to a recipe in this
// process; published records it has left for the Scenario Manager.
type designScenario struct {
	Priority         int
	NumberOfReps     int
	ConfidenceMetric float64
	ParametersetID   int
	Published        bool
}

// handshakeInfo is the last handshake outcome (the API's last_handshake).
type handshakeInfo struct {
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	BatchSubject string `json:"batch_subject,omitempty"`
}

// publishInfo is the last publish outcome (the API's last_publish). The
// landed wire contract defines the ack shape; the current SM delivers no
// ack message, so a successful (PubAck-gated) publish reports status
// "inserted" and the ack object is present only when an ack payload was
// observed for the batch.
type publishInfo struct {
	Status string                 `json:"status"`
	Reason string                 `json:"reason,omitempty"`
	Ack    *wire.ScenarioBatchAck `json:"ack,omitempty"`
}

// stateJSON is the GET /api/state shape (normative contract).
type stateJSON struct {
	Design struct {
		Entries   []entryJSON    `json:"entries"`
		Recipes   []recipeJSON   `json:"recipes"`
		Scenarios []scenarioJSON `json:"scenarios"`
	} `json:"design"`
	Flow struct {
		Saved     bool           `json:"saved"`
		Started   bool           `json:"started"`
		Finalized bool           `json:"finalized"`
		Handshake *handshakeInfo `json:"last_handshake,omitempty"`
		Publish   *publishInfo   `json:"last_publish,omitempty"`
	} `json:"flow"`
	DBRows []viewRowJSON `json:"db_rows"`
}

type entryJSON struct {
	ID        int    `json:"id"`
	Parameter string `json:"parameter"`
	Value     int    `json:"value"`
}

type recipeJSON struct {
	ParametersetID int `json:"parameterset_id"`
	ArrivalRateID  int `json:"arrival_rate_id"`
	ServiceRateID  int `json:"service_rate_id"`
	RunDurationID  int `json:"run_duration_id"`
	SeedPolicyID   int `json:"seed_policy_id"`
}

type scenarioJSON struct {
	Index            int     `json:"index"`
	Priority         int     `json:"priority"`
	NumberOfReps     int     `json:"number_of_reps"`
	ConfidenceMetric float64 `json:"confidence_metric"`
	ParametersetID   int     `json:"parameterset_id"`
	Published        bool    `json:"published"`
}

type viewRowJSON struct {
	ParametersetID int `json:"parameterset_id"`
	ArrivalRate    int `json:"arrival_rate"`
	ServiceRate    int `json:"service_rate"`
	RunDuration    int `json:"run_duration"`
	SeedPolicy     int `json:"seed_policy"`
}

// New returns the App over the deps. The in-memory design state is empty at
// process start (the ruled in-memory state): a restart starts with an empty
// design, and saved rows remain in the Detail DB.
func New(deps Deps) *App {
	return &App{deps: deps, issuedEntryID: map[string]int{}}
}

// nextEntryID returns the next EDS id for the parameter: the store's
// DB-derived next id (MAX(id)+1, floor 5 — persisted rows only) or one
// above the highest id issued in this process, whichever is greater.
func (a *App) nextEntryID(ctx context.Context, parameter string) (int, error) {
	dbNext, err := a.deps.Store.NextID(ctx, parameter)
	if err != nil {
		return 0, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	id := max(dbNext, a.issuedEntryID[parameter]+1)
	a.issuedEntryID[parameter] = id
	return id, nil
}

// nextRecipeID mirrors nextEntryID for the recipes table's parameterset_id.
func (a *App) nextRecipeID(ctx context.Context) (int, error) {
	dbNext, err := a.deps.Store.NextRecipeID(ctx)
	if err != nil {
		return 0, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	id := max(dbNext, a.issuedRecipeID+1)
	a.issuedRecipeID = id
	return id, nil
}

// ServeHTTP is the single HTTP handler (the mux over the API surface).
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/":
		a.serveUI(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/state":
		a.handleState(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/entries":
		a.handleAddEntry(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/recipes":
		a.handleAddRecipe(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/scenarios":
		a.handleAddScenario(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/scenarios/"):
		a.handleDeleteScenario(w, r, strings.TrimPrefix(r.URL.Path, "/api/scenarios/"))
	case r.Method == http.MethodPost && r.URL.Path == "/api/save":
		a.handleSave(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/start":
		a.handleStart(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/finish":
		a.handleFinish(w, r)
	default:
		writeError(w, http.StatusNotFound, "no such endpoint: "+r.Method+" "+r.URL.Path)
	}
}

// serveUI serves the embedded static page (no server-side templating).
func (a *App) serveUI(w http.ResponseWriter, r *http.Request) {
	data, err := fs.Sub(uiFS, "ui")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, err := data.Open("index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := io.Copy(w, f); err != nil {
		log.Printf("serve ui: %v", err)
	}
}

// ---- GET /api/state ----

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	st := stateJSON{}
	st.Design.Entries = make([]entryJSON, 0, len(a.entries))
	for _, e := range a.entries {
		st.Design.Entries = append(st.Design.Entries, entryJSON{ID: e.ID, Parameter: e.Parameter, Value: e.Value})
	}
	st.Design.Recipes = make([]recipeJSON, 0, len(a.recipes))
	for _, rc := range a.recipes {
		st.Design.Recipes = append(st.Design.Recipes, recipeJSON{
			ParametersetID: rc.ParametersetID,
			ArrivalRateID:  rc.ArrivalRateID,
			ServiceRateID:  rc.ServiceRateID,
			RunDurationID:  rc.RunDurationID,
			SeedPolicyID:   rc.SeedPolicyID,
		})
	}
	st.Design.Scenarios = make([]scenarioJSON, 0, len(a.scenarios))
	for i, s := range a.scenarios {
		st.Design.Scenarios = append(st.Design.Scenarios, scenarioJSON{
			Index:            i,
			Priority:         s.Priority,
			NumberOfReps:     s.NumberOfReps,
			ConfidenceMetric: s.ConfidenceMetric,
			ParametersetID:   s.ParametersetID,
			Published:        s.Published,
		})
	}
	st.Flow.Saved = a.savedOnce && !a.dirty
	st.Flow.Started = a.started
	st.Flow.Finalized = a.finalized
	st.Flow.Handshake = a.lastHandshake
	st.Flow.Publish = a.lastPublish
	a.mu.Unlock()

	rows, err := a.deps.Store.ViewRows(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	st.DBRows = make([]viewRowJSON, 0, len(rows))
	for _, v := range rows {
		st.DBRows = append(st.DBRows, viewRowJSON{
			ParametersetID: v.ParametersetID,
			ArrivalRate:    v.ArrivalRate,
			ServiceRate:    v.ServiceRate,
			RunDuration:    v.RunDuration,
			SeedPolicy:     v.SeedPolicy,
		})
	}
	writeJSON(w, http.StatusOK, st)
}

// ---- POST /api/entries ----

type addEntryBody struct {
	Parameter string `json:"parameter"`
	Value     int    `json:"value"`
}

// handleAddEntry assigns the next EDS id (MAX(id)+1, floor 5) for the
// parameter's entry table and adds the in-memory entry.
func (a *App) handleAddEntry(w http.ResponseWriter, r *http.Request) {
	var body addEntryBody
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := recipestore.ValueDomain(body.Parameter, body.Value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := a.nextEntryID(r.Context(), body.Parameter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.mu.Lock()
	a.entries = append(a.entries, designEntry{ID: id, Parameter: body.Parameter, Value: body.Value})
	a.dirty = true
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]int{"id": id})
}

// ---- POST /api/recipes ----

type addRecipeBody struct {
	ArrivalRateID int `json:"arrival_rate_id"`
	ServiceRateID int `json:"service_rate_id"`
	RunDurationID int `json:"run_duration_id"`
	SeedPolicyID  int `json:"seed_policy_id"`
}

// handleAddRecipe assigns the next parameterset_id and adds the in-memory
// recipe locator row. Every FK must reference an entry assigned in this
// process (per parameter).
func (a *App) handleAddRecipe(w http.ResponseWriter, r *http.Request) {
	var body addRecipeBody
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.mu.Lock()
	own := map[string]map[int]bool{}
	for _, e := range a.entries {
		m := own[e.Parameter]
		if m == nil {
			m = map[int]bool{}
			own[e.Parameter] = m
		}
		m[e.ID] = true
	}
	refers := func(param string, id int) bool { return own[param][id] }
	ok := refers(recipestore.ParamArrivalRate, body.ArrivalRateID) &&
		refers(recipestore.ParamServiceRate, body.ServiceRateID) &&
		refers(recipestore.ParamRunDuration, body.RunDurationID) &&
		refers(recipestore.ParamSeedPolicy, body.SeedPolicyID)
	a.mu.Unlock()
	if !ok {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("recipe must reference entries assigned in this process (got arrival_rate_id=%d service_rate_id=%d run_duration_id=%d seed_policy_id=%d)",
				body.ArrivalRateID, body.ServiceRateID, body.RunDurationID, body.SeedPolicyID))
		return
	}
	id, err := a.nextRecipeID(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.mu.Lock()
	a.recipes = append(a.recipes, designRecipe{
		ParametersetID: id,
		ArrivalRateID:  body.ArrivalRateID,
		ServiceRateID:  body.ServiceRateID,
		RunDurationID:  body.RunDurationID,
		SeedPolicyID:   body.SeedPolicyID,
	})
	a.dirty = true
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]int{"parameterset_id": id})
}

// ---- POST /api/scenarios ----

type addScenarioBody struct {
	Priority         int     `json:"priority"`
	NumberOfReps     int     `json:"number_of_reps"`
	ConfidenceMetric float64 `json:"confidence_metric"`
	ParametersetID   int     `json:"parameterset_id"`
}

// handleAddScenario appends an unpublished scenario. The domain contract is
// the landed guard: priority > 0, number_of_reps in 1..100000,
// confidence_metric finite > 0, and the parameterset must reference a recipe
// in this process.
func (a *App) handleAddScenario(w http.ResponseWriter, r *http.Request) {
	var body addScenarioBody
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Priority <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("priority %d must be > 0", body.Priority))
		return
	}
	if body.NumberOfReps < wire.MinReps || body.NumberOfReps > wire.MaxReps {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("number_of_reps %d is outside %d..%d", body.NumberOfReps, wire.MinReps, wire.MaxReps))
		return
	}
	if !(body.ConfidenceMetric > 0) || math.IsNaN(body.ConfidenceMetric) || math.IsInf(body.ConfidenceMetric, 0) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("confidence_metric %v must be finite and > 0", body.ConfidenceMetric))
		return
	}
	a.mu.Lock()
	known := map[int]bool{}
	for _, rc := range a.recipes {
		known[rc.ParametersetID] = true
	}
	a.mu.Unlock()
	if !known[body.ParametersetID] {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("parameterset_id %d does not reference a recipe in this process", body.ParametersetID))
		return
	}
	a.mu.Lock()
	a.scenarios = append(a.scenarios, designScenario{
		Priority:         body.Priority,
		NumberOfReps:     body.NumberOfReps,
		ConfidenceMetric: body.ConfidenceMetric,
		ParametersetID:   body.ParametersetID,
	})
	newIndex := len(a.scenarios) - 1
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]int{"index": newIndex})
}

// ---- DELETE /api/scenarios/<index> ----

// handleDeleteScenario removes an unpublished scenario by index. A
// published scenario is refused (it already exists in the Scenario Manager).
func (a *App) handleDeleteScenario(w http.ResponseWriter, r *http.Request, indexStr string) {
	idx, err := strconv.Atoi(indexStr)
	if err != nil || idx < 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("scenario index %q is not a non-negative integer", indexStr))
		return
	}
	a.mu.Lock()
	if idx >= len(a.scenarios) {
		a.mu.Unlock()
		writeError(w, http.StatusNotFound, fmt.Sprintf("no scenario at index %d", idx))
		return
	}
	if a.scenarios[idx].Published {
		a.mu.Unlock()
		writeError(w, http.StatusConflict, fmt.Sprintf("scenario at index %d is already published; it cannot be deleted", idx))
		return
	}
	a.scenarios = append(a.scenarios[:idx], a.scenarios[idx+1:]...)
	a.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// ---- POST /api/save ----

// handleSave runs the single-transaction save: the design's entries and
// recipe locator rows into the Detail DB (INSERT new ids, UPDATE the values
// of ids this process assigned — the live-locators ruling).
func (a *App) handleSave(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	entries := make([]recipestore.Entry, 0, len(a.entries))
	for _, e := range a.entries {
		entries = append(entries, recipestore.Entry{Parameter: e.Parameter, ID: e.ID, Value: e.Value})
	}
	recipes := make([]recipestore.Recipe, 0, len(a.recipes))
	for _, rc := range a.recipes {
		recipes = append(recipes, recipestore.Recipe{
			ParametersetID: rc.ParametersetID,
			ArrivalRateID:  rc.ArrivalRateID,
			ServiceRateID:  rc.ServiceRateID,
			RunDurationID:  rc.RunDurationID,
			SeedPolicyID:   rc.SeedPolicyID,
		})
	}
	a.mu.Unlock()

	n, m, err := a.deps.Store.Save(r.Context(), entries, recipes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.mu.Lock()
	a.savedOnce = true
	a.dirty = false
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]int{"saved_entries": n, "saved_recipes": m})
}

// ---- POST /api/start ----

type startResponse struct {
	BatchID      string                 `json:"batch_id"`
	Status       string                 `json:"status"`
	Reason       string                 `json:"reason,omitempty"`
	Ack          *wire.ScenarioBatchAck `json:"ack,omitempty"`
	Published    int                    `json:"published"`
	BatchSubject string                 `json:"batch_subject"`
}

// handleStart runs the Start action: the empty-delta refusal, the pre-flight
// (every batch-referenced parameterset_id must resolve in the
// simulation_parameters view, else a clean refusal naming the missing ids —
// preventing the silent RecipeError stall), the handshake (declaring
// standard), and the publish of one batch containing exactly the
// scenarios not yet published. Each Start runs the full retry skeleton
// fresh. After finalization it is refused (irrevocable).
func (a *App) handleStart(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	if a.finalized {
		a.mu.Unlock()
		writeError(w, http.StatusConflict, "experiment finalized; no further starts accepted (finalization is irrevocable)")
		return
	}
	records := make([]wire.ScenarioRecord, 0, len(a.scenarios))
	requested := make([]int, 0)
	type deltaRef struct {
		scenarioIdx int
		psid        int
	}
	deltas := make([]deltaRef, 0, len(a.scenarios))
	for i, s := range a.scenarios {
		if s.Published {
			continue
		}
		deltas = append(deltas, deltaRef{scenarioIdx: i, psid: s.ParametersetID})
	}
	if len(deltas) == 0 {
		a.mu.Unlock()
		writeError(w, http.StatusConflict, "no new scenarios to publish")
		return
	}
	for _, d := range deltas {
		records = append(records, wire.ScenarioRecord{
			Priority:         a.scenarios[d.scenarioIdx].Priority,
			NumberOfReps:     a.scenarios[d.scenarioIdx].NumberOfReps,
			RecipeInfo:       wire.RecipeInfo{ParametersetID: d.psid},
			ConfidenceMetric: a.scenarios[d.scenarioIdx].ConfidenceMetric,
		})
		requested = append(requested, d.psid)
	}
	a.mu.Unlock()

	// The pre-flight: every batch-referenced parameterset_id must resolve in
	// the view, else a clean refusal naming the missing ids.
	miss, err := a.deps.Store.Resolve(r.Context(), requested)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(miss) > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf("pre-flight failed: parameterset_id(s) %v do not resolve in the simulation_parameters view; save the design first", miss))
		return
	}

	res, err := a.deps.Publisher.Start(r.Context(), records)
	if err != nil {
		a.recordStartFailure(err)
		writePublishError(w, err)
		return
	}

	a.mu.Lock()
	for _, d := range deltas {
		a.scenarios[d.scenarioIdx].Published = true
	}
	a.started = true
	a.lastHandshake = &handshakeInfo{Status: "ready", BatchSubject: res.BatchSubject}
	a.lastPublish = &publishInfo{Status: wire.BatchAckStatusInserted}
	a.mu.Unlock()

	writeJSON(w, http.StatusOK, startResponse{
		BatchID:      res.BatchID,
		Status:       wire.BatchAckStatusInserted,
		Ack:          res.Ack,
		Published:    len(records),
		BatchSubject: res.BatchSubject,
	})
}

// recordStartFailure stores the handshake/publish failure in the flow state
// so the UI can surface the reason. A clean SM refusal updates only the
// handshake result (no publish was attempted); a protocol or publish failure
// is reported in the publish result.
func (a *App) recordStartFailure(err error) {
	var unav *publisher.ErrUnavailable
	if errors.As(err, &unav) {
		a.mu.Lock()
		a.lastHandshake = &handshakeInfo{Status: wire.AvailabilityStatusError, Reason: unav.Reason}
		a.mu.Unlock()
		return
	}
	a.mu.Lock()
	a.lastPublish = &publishInfo{Status: wire.BatchAckStatusError, Reason: err.Error()}
	a.mu.Unlock()
}

// ---- POST /api/finish ----

type finishResponse struct {
	BatchID string `json:"batch_id"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

// handleFinish runs the Finish action: only after a successful Start and
// before any finalization, it publishes the finalization batch
// (scenarios [], final true) and sets finalized. A second Finish and any
// Start after finalization are refused (irrevocable).
func (a *App) handleFinish(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	if a.finalized {
		a.mu.Unlock()
		writeError(w, http.StatusConflict, "experiment already finalized; finalization is irrevocable")
		return
	}
	if !a.started {
		a.mu.Unlock()
		writeError(w, http.StatusConflict, "finish requires a successful start first")
		return
	}
	a.mu.Unlock()

	res, err := a.deps.Publisher.Finish(r.Context())
	if err != nil {
		var unav *publisher.ErrUnavailable
		if errors.As(err, &unav) {
			a.mu.Lock()
			a.lastHandshake = &handshakeInfo{Status: wire.AvailabilityStatusError, Reason: unav.Reason}
			a.mu.Unlock()
			writeError(w, http.StatusConflict, unav.Reason)
			return
		}
		a.mu.Lock()
		a.lastPublish = &publishInfo{Status: wire.BatchAckStatusError, Reason: err.Error()}
		a.mu.Unlock()
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	a.mu.Lock()
	a.finalized = true
	a.lastHandshake = &handshakeInfo{Status: "ready", BatchSubject: res.BatchSubject}
	a.lastPublish = &publishInfo{Status: wire.BatchAckStatusInserted}
	a.mu.Unlock()

	writeJSON(w, http.StatusOK, finishResponse{BatchID: res.BatchID, Status: wire.BatchAckStatusInserted})
}

// ---- helpers ----

// decodeBody strictly decodes a JSON request body (unknown fields and
// trailing tokens are rejected).
func decodeBody(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("malformed JSON body: %w", err)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err == nil {
		return errors.New("malformed JSON body: unexpected trailing content")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("malformed JSON body: %v", err)
	}
	return nil
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

// writeError writes the {error} JSON refusal.
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// writePublishError maps a publisher error to the API's error taxonomy: the
// clean SM refusal (the experiment is not InProgress) is a client-side flow
// refusal (409) carrying the SM's reason; a protocol/transport failure
// (echo mismatch, missing batch subject, exhausted retries, PubAck loss) is
// a 502.
func writePublishError(w http.ResponseWriter, err error) {
	var unav *publisher.ErrUnavailable
	if errors.As(err, &unav) {
		writeError(w, http.StatusConflict, unav.Reason)
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}
