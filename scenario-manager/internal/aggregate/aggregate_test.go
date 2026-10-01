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

package aggregate

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ---- fake store: an in-memory Core DB state machine for the aggregate pass ----

// patchCall records one PatchVerdict attempt and its outcome.
type patchCall struct {
	namespace string
	name      string
	verdict   string
	err       error
}

type fakeKube struct {
	mu          sync.Mutex
	experiments []experimentalpha4.SimulationExperiment
	listErr     error
	listCalls   int
	patchErr    error
	failFirstN  int
	patchCalls  []patchCall
}

func (f *fakeKube) ListExperiments(ctx context.Context) ([]experimentalpha4.SimulationExperiment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]experimentalpha4.SimulationExperiment(nil), f.experiments...), nil
}

func (f *fakeKube) PatchVerdict(ctx context.Context, namespace, name, verdict string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failFirstN > 0 {
		f.failFirstN--
		f.patchCalls = append(f.patchCalls, patchCall{namespace, name, verdict, errors.New("patch refused")})
		return errors.New("patch refused")
	}
	f.patchCalls = append(f.patchCalls, patchCall{namespace, name, verdict, f.patchErr})
	if f.patchErr != nil {
		return f.patchErr
	}
	// Apply the report to the listed object the way the API server would:
	// the status subresource now carries the absorbing verdict, so the next
	// tick's write-if-absent check sees it.
	for i := range f.experiments {
		if f.experiments[i].Namespace == namespace && f.experiments[i].Name == name {
			f.experiments[i].Status.ScenarioManagerVerdict = verdict
		}
	}
	return nil
}

func (f *fakeKube) successfulCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.patchCalls {
		if c.err == nil {
			n++
		}
	}
	return n
}

type fakeStore struct {
	mu         sync.Mutex
	projectIDs map[string]int
	notFound   map[string]bool
	lookupErr  error
	counts     map[int]persistence.ScenarioStateCounts
	countsErr  error

	lookupCalls []string
	countsCalls []int
}

func newFakeStore(counts map[int]persistence.ScenarioStateCounts) *fakeStore {
	return &fakeStore{
		projectIDs: map[string]int{"ns/proj": 7},
		notFound:   make(map[string]bool),
		counts:     counts,
	}
}

func (f *fakeStore) ProjectIDByNamespaceAndName(ctx context.Context, namespace, project string) (int, error) {
	key := namespace + "/" + project
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookupCalls = append(f.lookupCalls, key)
	if f.lookupErr != nil {
		return 0, f.lookupErr
	}
	if f.notFound[key] {
		return 0, persistence.ErrProjectNotFound
	}
	return f.projectIDs[key], nil
}

func (f *fakeStore) ScenarioStateCounts(ctx context.Context, projectID int) (persistence.ScenarioStateCounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.countsCalls = append(f.countsCalls, projectID)
	if f.countsErr != nil {
		return persistence.ScenarioStateCounts{}, f.countsErr
	}
	return f.counts[projectID], nil
}

func (f *fakeStore) lookups() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lookupCalls...)
}

func (f *fakeStore) countsCallsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.countsCalls)
}

// ---- helpers ----

// experiment builds an experiment with the given phase and (optional)
// already-reported verdict.
func experiment(ns, name, phase, verdict string) *experimentalpha4.SimulationExperiment {
	return &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID("uid-" + name)},
		Status: experimentalpha4.SimulationExperimentStatus{
			Phase:                  phase,
			ScenarioManagerVerdict: verdict,
		},
	}
}

func newTestScheduler(t *testing.T, store *fakeStore, k *fakeKube) *Scheduler {
	t.Helper()
	s, err := NewScheduler(store, k, Config{})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	return s
}

func oneInProgress(k *fakeKube) {
	k.experiments = []experimentalpha4.SimulationExperiment{*experiment("ns", "proj", "InProgress", "")}
}

// ---- constructor and configuration ----

func TestNewSchedulerValidation(t *testing.T) {
	var store Store
	var kube Kube
	if _, err := NewScheduler(store, kube, Config{}); err == nil {
		t.Fatal("nil store: want error")
	}
	if _, err := NewScheduler(newFakeStore(nil), kube, Config{}); err == nil {
		t.Fatal("nil kube: want error")
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}.withDefaults()
	if cfg.Interval <= 0 || cfg.TickTimeout <= 0 {
		t.Fatalf("defaults = %+v; want positive interval and tick timeout", cfg)
	}
}

// ---- the D3 aggregation rule ----

func TestVerdictForCounts(t *testing.T) {
	cases := []struct {
		name   string
		counts persistence.ScenarioStateCounts
		want   string
	}{
		{"fail-fast: one failed with unfinished present", persistence.ScenarioStateCounts{Total: 3, Finished: 1, Failed: 1}, "Failed"},
		{"fail-fast: failed alongside finished", persistence.ScenarioStateCounts{Total: 5, Finished: 3, Failed: 2}, "Failed"},
		{"all finished", persistence.ScenarioStateCounts{Total: 4, Finished: 4, Failed: 0}, "Finished"},
		{"single finished", persistence.ScenarioStateCounts{Total: 1, Finished: 1, Failed: 0}, "Finished"},
		{"zero scenarios never", persistence.ScenarioStateCounts{}, ""},
		{"mixed non-terminal no-op", persistence.ScenarioStateCounts{Total: 3, Finished: 2, Failed: 0}, ""},
		{"none terminal yet", persistence.ScenarioStateCounts{Total: 2, Finished: 0, Failed: 0}, ""},
	}
	for _, c := range cases {
		if got := verdictForCounts(c.counts); got != c.want {
			t.Errorf("%s: verdictForCounts(%+v) = %q; want %q", c.name, c.counts, got, c.want)
		}
	}
}

// ---- the tick: gate, absorbing skip, D3, failure taxonomy ----

func TestTickReportsVerdictPerD3(t *testing.T) {
	cases := []struct {
		name        string
		counts      persistence.ScenarioStateCounts
		wantVerdict string
	}{
		{"fail-fast one failed with unfinished", persistence.ScenarioStateCounts{Total: 3, Finished: 1, Failed: 1}, "Failed"},
		{"all finished", persistence.ScenarioStateCounts{Total: 4, Finished: 4, Failed: 0}, "Finished"},
		{"zero scenarios never reports", persistence.ScenarioStateCounts{}, ""},
		{"mixed non-terminal reports nothing", persistence.ScenarioStateCounts{Total: 3, Finished: 2, Failed: 0}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := newFakeStore(map[int]persistence.ScenarioStateCounts{7: c.counts})
			k := &fakeKube{}
			oneInProgress(k)
			s := newTestScheduler(t, store, k)

			s.tick(context.Background())

			k.mu.Lock()
			defer k.mu.Unlock()
			if c.wantVerdict == "" {
				if got := len(k.patchCalls); got != 0 {
					t.Fatalf("patch calls = %d; want 0: %+v", got, k.patchCalls)
				}
			} else {
				if len(k.patchCalls) != 1 {
					t.Fatalf("patch calls = %d; want 1: %+v", len(k.patchCalls), k.patchCalls)
				}
				call := k.patchCalls[0]
				if call.namespace != "ns" || call.name != "proj" || call.verdict != c.wantVerdict || call.err != nil {
					t.Fatalf("patch call = %+v; want ns/proj %q success", call, c.wantVerdict)
				}
			}
			if store.countsCallsCount() != 1 {
				t.Fatalf("counts calls = %d; want 1", store.countsCallsCount())
			}
		})
	}
}

func TestTickSkipsGateRejectedExperiments(t *testing.T) {
	phases := []string{"Pending", "Provisioning", "", "Error", "Failed", "Finished", "MysteryPhase"}
	for _, phase := range phases {
		t.Run("phase="+phase, func(t *testing.T) {
			store := newFakeStore(map[int]persistence.ScenarioStateCounts{7: {Total: 1, Finished: 1}})
			k := &fakeKube{experiments: []experimentalpha4.SimulationExperiment{*experiment("ns", "proj", phase, "")}}
			s := newTestScheduler(t, store, k)

			s.tick(context.Background())

			if got := store.lookups(); len(got) != 0 {
				t.Fatalf("gate-rejected %q: project lookups = %v; want none", phase, got)
			}
			if k.successfulCalls() != 0 || len(k.patchCalls) != 0 {
				t.Fatalf("gate-rejected %q: patch calls = %+v; want none", phase, k.patchCalls)
			}
		})
	}

	// A deleting InProgress experiment is terminal (gate closed for deletion).
	deleting := experiment("ns", "proj", "InProgress", "")
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	store := newFakeStore(map[int]persistence.ScenarioStateCounts{7: {Total: 1, Finished: 1}})
	k := &fakeKube{experiments: []experimentalpha4.SimulationExperiment{*deleting}}
	s := newTestScheduler(t, store, k)
	s.tick(context.Background())
	if got := store.lookups(); len(got) != 0 {
		t.Fatalf("deleting experiment: project lookups = %v; want none", got)
	}
}

func TestTickSkipsAlreadyReportedVerdict(t *testing.T) {
	store := newFakeStore(map[int]persistence.ScenarioStateCounts{7: {Total: 1, Finished: 1}})
	k := &fakeKube{experiments: []experimentalpha4.SimulationExperiment{*experiment("ns", "proj", "InProgress", "Finished")}}
	s := newTestScheduler(t, store, k)

	s.tick(context.Background())

	if got := store.lookups(); len(got) != 0 {
		t.Fatalf("already-reported verdict: project lookups = %v; want none (absorbing skip before the DB)", got)
	}
	if store.countsCallsCount() != 0 {
		t.Fatalf("already-reported verdict: counts calls = %d; want none", store.countsCallsCount())
	}
	if len(k.patchCalls) != 0 {
		t.Fatalf("already-reported verdict: patch calls = %+v; want none (write-if-absent)", k.patchCalls)
	}
}

func TestTickProjectLookupErrorSkipsExperiment(t *testing.T) {
	store := newFakeStore(map[int]persistence.ScenarioStateCounts{7: {Total: 1, Finished: 1}})
	store.lookupErr = errors.New("core db unavailable")
	k := &fakeKube{}
	oneInProgress(k)
	s := newTestScheduler(t, store, k)

	s.tick(context.Background())

	if store.countsCallsCount() != 0 {
		t.Fatalf("lookup error: counts calls = %d; want none", store.countsCallsCount())
	}
	if len(k.patchCalls) != 0 {
		t.Fatalf("lookup error: patch calls = %+v; want none", k.patchCalls)
	}
}

func TestTickProjectNotFoundIsNoOp(t *testing.T) {
	store := newFakeStore(map[int]persistence.ScenarioStateCounts{7: {Total: 1, Finished: 1}})
	store.notFound["ns/proj"] = true
	k := &fakeKube{}
	oneInProgress(k)
	s := newTestScheduler(t, store, k)

	s.tick(context.Background())

	if store.countsCallsCount() != 0 {
		t.Fatalf("project not found: counts calls = %d; want none (no row, no aggregation)", store.countsCallsCount())
	}
	if len(k.patchCalls) != 0 {
		t.Fatalf("project not found: patch calls = %+v; want none", k.patchCalls)
	}
}

func TestTickCountsErrorSkipsExperiment(t *testing.T) {
	store := newFakeStore(map[int]persistence.ScenarioStateCounts{7: {Total: 1, Finished: 1}})
	store.countsErr = errors.New("aggregation failed")
	k := &fakeKube{}
	oneInProgress(k)
	s := newTestScheduler(t, store, k)

	s.tick(context.Background())

	if len(k.patchCalls) != 0 {
		t.Fatalf("counts error: patch calls = %+v; want none", k.patchCalls)
	}
}

func TestTickListFailureRetriesNextTick(t *testing.T) {
	store := newFakeStore(map[int]persistence.ScenarioStateCounts{7: {Total: 1, Finished: 1}})
	k := &fakeKube{listErr: errors.New("apiserver unavailable")}
	oneInProgress(k)
	k.listCalls = 0
	s := newTestScheduler(t, store, k)

	s.tick(context.Background())
	if store.countsCallsCount() != 0 || len(k.patchCalls) != 0 {
		t.Fatalf("list failure: work must be skipped entirely (lookups=%d, patches=%+v)", store.countsCallsCount(), k.patchCalls)
	}

	k.listErr = nil
	s.tick(context.Background())
	if k.successfulCalls() != 1 {
		t.Fatalf("after list recovery: successful patches = %d; want 1", k.successfulCalls())
	}
}

func TestTickPatchErrorRetriesNextTick(t *testing.T) {
	store := newFakeStore(map[int]persistence.ScenarioStateCounts{7: {Total: 4, Finished: 4}})
	k := &fakeKube{failFirstN: 1}
	oneInProgress(k)
	s := newTestScheduler(t, store, k)

	s.tick(context.Background())
	if k.successfulCalls() != 0 || len(k.patchCalls) != 1 {
		t.Fatalf("first tick: patch attempts = %+v; want one failed attempt", k.patchCalls)
	}

	// The verdict is still unreported, so the next tick re-derives and retries.
	s.tick(context.Background())
	if k.successfulCalls() != 1 || len(k.patchCalls) != 2 {
		t.Fatalf("second tick: patch attempts = %+v; want one success (self-healing)", k.patchCalls)
	}
	if k.patchCalls[1].verdict != "Finished" {
		t.Fatalf("retry verdict = %q; want Finished", k.patchCalls[1].verdict)
	}
}

// ---- the status patch payload (D9: only scenarioManagerVerdict) ----

// recordingClient wraps a controller-runtime client and records every
// pre-encoded merge patch payload and its target before delegating, so a test
// can assert on the actual patch bytes. The production write goes through the
// status subresource writer (Status().Patch), which is recorded by the
// recordingStatusWriter; the main-resource Patch override records any
// main-resource raw merge patch as well, so a regression to the wrong
// endpoint is captured here.
type recordingClient struct {
	client.WithWatch

	mu      sync.Mutex
	patches []recordedPatch
}

type recordedPatch struct {
	namespace string
	name      string
	data      []byte
}

// record appends one captured patch payload under the recorder's lock.
func (r *recordingClient) record(obj client.Object, data []byte) {
	r.mu.Lock()
	r.patches = append(r.patches, recordedPatch{
		namespace: obj.GetNamespace(),
		name:      obj.GetName(),
		data:      append([]byte(nil), data...),
	})
	r.mu.Unlock()
}

func (r *recordingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if patch.Type() == types.MergePatchType {
		if data, err := patch.Data(obj); err == nil && len(data) > 0 {
			r.record(obj, data)
		}
	}
	return r.WithWatch.Patch(ctx, obj, patch, opts...)
}

// Status returns the status subresource writer with raw merge patches
// recorded before delegation.
func (r *recordingClient) Status() client.SubResourceWriter {
	return &recordingStatusWriter{SubResourceWriter: r.WithWatch.Status(), rec: r}
}

// recordingStatusWriter records the status-subresource patch payload and
// delegates the write to the wrapped client.
type recordingStatusWriter struct {
	client.SubResourceWriter
	rec *recordingClient
}

func (r *recordingStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	if patch.Type() == types.MergePatchType {
		if data, err := patch.Data(obj); err == nil && len(data) > 0 {
			r.rec.record(obj, data)
		}
	}
	return r.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := experimentalpha4.AddToScheme(s); err != nil {
		t.Fatalf("add alpha4 scheme: %v", err)
	}
	return s
}

func TestKubePatchVerdictPayloadCarriesOnlyScenarioManagerVerdict(t *testing.T) {
	obj := &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "proj", UID: types.UID("uid-1")},
		Status:     experimentalpha4.SimulationExperimentStatus{Phase: "InProgress"},
	}
	cs := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(obj).
		// Enforce the live API-server semantics: with the status subresource
		// enabled, a main-resource write never alters status (the alpha4 CRD
		// enables subresources.status).
		WithStatusSubresource(&experimentalpha4.SimulationExperiment{}).Build()
	rc := &recordingClient{WithWatch: cs}

	if err := NewKube(rc).PatchVerdict(context.Background(), "ns", "proj", "Finished"); err != nil {
		t.Fatalf("PatchVerdict: %v", err)
	}

	rc.mu.Lock()
	if len(rc.patches) != 1 {
		t.Fatalf("patches = %d; want 1", len(rc.patches))
	}
	p := rc.patches[0]
	rc.mu.Unlock()

	if p.namespace != "ns" || p.name != "proj" {
		t.Fatalf("patch target = %s/%s; want ns/proj", p.namespace, p.name)
	}
	want := []byte(`{"status":{"scenarioManagerVerdict":"Finished"}}`)
	if !bytes.Equal(p.data, want) {
		t.Fatalf("patch payload = %s; want exactly %s (only scenarioManagerVerdict - never phase, never message)", p.data, want)
	}

	// The patch must land on the object: the verdict is set, and the
	// operator-owned phase is untouched.
	got := &experimentalpha4.SimulationExperiment{}
	if err := cs.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "proj"}, got); err != nil {
		t.Fatalf("get after patch: %v", err)
	}
	if got.Status.ScenarioManagerVerdict != "Finished" {
		t.Fatalf("status.scenarioManagerVerdict = %q; want Finished", got.Status.ScenarioManagerVerdict)
	}
	if got.Status.Phase != "InProgress" {
		t.Fatalf("status.phase = %q; want InProgress untouched", got.Status.Phase)
	}
}

// TestPassWriteIfAbsentIsIdempotent drives the whole pass (production Kube
// adapter, fake Core DB) over two ticks: the first tick reports the verdict,
// and the second tick must be a no-op because the verdict is absorbing.
func TestPassWriteIfAbsentIsIdempotent(t *testing.T) {
	store := newFakeStore(map[int]persistence.ScenarioStateCounts{7: {Total: 4, Finished: 4}})
	obj := &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "proj", UID: types.UID("uid-1")},
		Status:     experimentalpha4.SimulationExperimentStatus{Phase: "InProgress"},
	}
	cs := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(obj).
		// Same live semantics as TestKubePatchVerdictPayloadCarriesOnlyScenarioManagerVerdict:
		// the status subresource is enabled, so only a Status().Patch lands the
		// verdict.
		WithStatusSubresource(&experimentalpha4.SimulationExperiment{}).Build()
	rc := &recordingClient{WithWatch: cs}
	s, err := NewScheduler(store, NewKube(rc), Config{})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	s.tick(context.Background())
	s.tick(context.Background())

	rc.mu.Lock()
	after := len(rc.patches)
	if after != 1 {
		t.Fatalf("patches after two ticks = %d; want exactly 1 (write-if-absent, absorbing)", after)
	}
	want := []byte(`{"status":{"scenarioManagerVerdict":"Finished"}}`)
	if !bytes.Equal(rc.patches[0].data, want) {
		t.Fatalf("patch payload = %s; want %s", rc.patches[0].data, want)
	}
	rc.mu.Unlock()

	got := &experimentalpha4.SimulationExperiment{}
	if err := cs.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "proj"}, got); err != nil {
		t.Fatalf("get after pass: %v", err)
	}
	if got.Status.ScenarioManagerVerdict != "Finished" {
		t.Fatalf("status.scenarioManagerVerdict = %q; want Finished", got.Status.ScenarioManagerVerdict)
	}
	if got.Status.Phase != "InProgress" {
		t.Fatalf("status.phase = %q; want InProgress (the operator owns phase)", got.Status.Phase)
	}
}

// ---- the scheduler lifecycle ----

func TestSchedulerStartShutdown(t *testing.T) {
	store := newFakeStore(map[int]persistence.ScenarioStateCounts{7: {Total: 2, Finished: 2}})
	k := &fakeKube{}
	oneInProgress(k)
	s, err := NewScheduler(store, k, Config{Interval: 10 * time.Millisecond, TickTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	s.Start()

	// The immediate first tick (or the next cadence tick) reports the verdict.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if k.successfulCalls() >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no verdict reported within 2s; patches = %+v", k.patchCalls)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The pass is write-if-absent: after the report, no further patch lands
	// even across many cadence ticks.
	first := len(k.patchCalls)
	time.Sleep(100 * time.Millisecond)
	if got := len(k.patchCalls); got != first {
		t.Fatalf("patches grew from %d to %d; want no further writes (absorbing)", first, got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestSchedulerShutdownWithoutStart(t *testing.T) {
	s, err := NewScheduler(newFakeStore(nil), &fakeKube{}, Config{})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown without Start: %v", err)
	}
}
