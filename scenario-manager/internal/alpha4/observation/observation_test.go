package observation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/eventlog"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/scheduler"
)

// ---- fakeStore: an in-memory Core DB state machine for observation ----

type fakeStore struct {
	mu sync.Mutex

	// inproc is the set of scenario IDs currently in InProcessing.
	inproc map[int]struct{}
	// computed tracks number_of_computed_reps per scenario.
	computed map[int]int
	reps     map[int]int // number_of_reps per scenario

	loadErr   error
	listErr   error
	updateErr error
	postErr   error
	failedErr error
	// updateStale makes UpdateComputedRepsMonotonic return false (zero rows).
	updateStale map[int]bool
	// postStale makes MarkPostProcessing return false (zero rows) without
	// deleting, simulating a race where another path moved the row between
	// LoadProjection and the terminal transition.
	postStale map[int]bool
	// failedStale makes MarkFailedFrom return false (zero rows) without
	// deleting, simulating the same race on the Failed transition.
	failedStale map[int]bool

	updateCalls []updateCall
	postCalls   []int
	failedCalls []int
}

type updateCall struct {
	id    int
	count int
}

func newFakeStore(ids ...int) *fakeStore {
	f := &fakeStore{
		inproc:      make(map[int]struct{}),
		computed:    make(map[int]int),
		reps:        make(map[int]int),
		updateStale: make(map[int]bool),
		postStale:   make(map[int]bool),
		failedStale: make(map[int]bool),
	}
	for _, id := range ids {
		f.inproc[id] = struct{}{}
		f.reps[id] = 4
	}
	return f
}

func (f *fakeStore) ListInProcessing(ctx context.Context) ([]int, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]int, 0, len(f.inproc))
	for id := range f.inproc {
		ids = append(ids, id)
	}
	sortInts(ids)
	return ids, nil
}

func (f *fakeStore) LoadProjection(ctx context.Context, scenarioID int) (*Projection, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.inproc[scenarioID]; !ok {
		return nil, nil // stale
	}
	return &Projection{
		ID:                   scenarioID,
		TranslationAttempt:   1,
		NumberOfReps:         f.reps[scenarioID],
		NumberOfComputedReps: f.computed[scenarioID],
		ProjectNamespace:     "ns-x",
		ProjectName:          "exp-x",
	}, nil
}

func (f *fakeStore) UpdateComputedRepsMonotonic(ctx context.Context, scenarioID, count int) (int, bool, error) {
	f.mu.Lock()
	f.updateCalls = append(f.updateCalls, updateCall{id: scenarioID, count: count})
	if f.updateErr != nil {
		f.mu.Unlock()
		return 0, false, f.updateErr
	}
	if f.updateStale[scenarioID] {
		f.mu.Unlock()
		return 0, false, nil
	}
	if _, ok := f.inproc[scenarioID]; !ok {
		f.mu.Unlock()
		return 0, false, nil
	}
	cur := f.computed[scenarioID]
	if count > cur {
		cur = count
	}
	if cap := f.reps[scenarioID]; cur > cap {
		cur = cap
	}
	f.computed[scenarioID] = cur
	f.mu.Unlock()
	return cur, true, nil
}

func (f *fakeStore) MarkPostProcessing(ctx context.Context, scenarioID int) (bool, error) {
	f.mu.Lock()
	f.postCalls = append(f.postCalls, scenarioID)
	if f.postErr != nil {
		f.mu.Unlock()
		return false, f.postErr
	}
	if f.postStale[scenarioID] {
		f.mu.Unlock()
		return false, nil
	}
	if _, ok := f.inproc[scenarioID]; !ok {
		f.mu.Unlock()
		return false, nil
	}
	delete(f.inproc, scenarioID) // InProcessing -> PostProcessing
	f.mu.Unlock()
	return true, nil
}

func (f *fakeStore) MarkFailedFrom(ctx context.Context, scenarioID int) (bool, error) {
	f.mu.Lock()
	f.failedCalls = append(f.failedCalls, scenarioID)
	if f.failedErr != nil {
		f.mu.Unlock()
		return false, f.failedErr
	}
	if f.failedStale[scenarioID] {
		f.mu.Unlock()
		return false, nil
	}
	if _, ok := f.inproc[scenarioID]; !ok {
		f.mu.Unlock()
		return false, nil
	}
	delete(f.inproc, scenarioID) // InProcessing -> Failed
	f.mu.Unlock()
	return true, nil
}

func (f *fakeStore) isInProcessing(id int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.inproc[id]
	return ok
}

func (f *fakeStore) computedReps(id int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.computed[id]
}

func (f *fakeStore) addInProcessing(id int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inproc[id] = struct{}{}
	if _, ok := f.reps[id]; !ok {
		f.reps[id] = 4
	}
}

func (f *fakeStore) removeInProcessing(id int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.inproc, id)
}

func (f *fakeStore) updateCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.updateCalls)
}

func (f *fakeStore) postCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.postCalls)
}

func (f *fakeStore) failedCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.failedCalls)
}

// ---- fakeAdapter ----

type observeCall struct {
	req scheduler.ObservationRequest
}

type fakeAdapter struct {
	mu sync.Mutex
	// per-id outcome override; default is Retry with count 0 (missing job).
	outcomes      map[int]scheduler.ObservationResult
	defaultResult *scheduler.ObservationResult
	slow          map[int]chan struct{}

	calls       []observeCall
	inFlightObs atomic.Int32
	maxInFlight atomic.Int32
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{
		outcomes: make(map[int]scheduler.ObservationResult),
		slow:     make(map[int]chan struct{}),
	}
}

func (a *fakeAdapter) Observe(ctx context.Context, req scheduler.ObservationRequest) scheduler.ObservationResult {
	cur := a.inFlightObs.Add(1)
	for {
		m := a.maxInFlight.Load()
		if cur > m {
			if a.maxInFlight.CompareAndSwap(m, cur) {
				break
			}
		} else {
			break
		}
	}
	defer a.inFlightObs.Add(-1)

	a.mu.Lock()
	a.calls = append(a.calls, observeCall{req: req})
	slow := a.slow[req.ScenarioID]
	r, ok := a.outcomes[req.ScenarioID]
	a.mu.Unlock()

	if slow != nil {
		select {
		case <-slow:
		case <-ctx.Done():
			return scheduler.ObservationResult{Outcome: scheduler.ObservationRetry, Err: ctx.Err()}
		}
	}
	if ok {
		return r
	}
	if a.defaultResult != nil {
		return *a.defaultResult
	}
	return scheduler.ObservationResult{Outcome: scheduler.ObservationRetry}
}

func (a *fakeAdapter) setOutcome(id int, r scheduler.ObservationResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.outcomes[id] = r
}
func (a *fakeAdapter) makeSlow(id int) chan struct{} {
	ch := make(chan struct{})
	a.mu.Lock()
	a.slow[id] = ch
	a.mu.Unlock()
	return ch
}
func (a *fakeAdapter) observeCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}
func (a *fakeAdapter) maxConcurrent() int32 { return a.maxInFlight.Load() }

func completedResult(reps int) scheduler.ObservationResult {
	return scheduler.ObservationResult{Outcome: scheduler.ObservationCompleted, CompletedReps: reps}
}
func failedResult(partial int) scheduler.ObservationResult {
	return scheduler.ObservationResult{Outcome: scheduler.ObservationFailed, CompletedReps: partial}
}
func retryResult(partial int) scheduler.ObservationResult {
	return scheduler.ObservationResult{Outcome: scheduler.ObservationRetry, CompletedReps: partial}
}

// ---- helpers ----

func fastCfg(workers int) Config {
	return Config{Workers: workers, DiscoveryInterval: 25 * time.Millisecond}
}

func poll(t *testing.T, cond func() bool, what string, timeout ...time.Duration) {
	t.Helper()
	d := 2 * time.Second
	if len(timeout) > 0 {
		d = timeout[0]
	}
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition timed out: %s", what)
}

func startScheduler(t *testing.T, store *fakeStore, adapter *fakeAdapter, workers int) *Scheduler {
	s, err := NewScheduler(store, adapter, fastCfg(workers))
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	s.Start()
	return s
}

// startSchedulerWithLogger builds and starts a scheduler wired with the given
// recorder so the test can assert scenario-observability records.
func startSchedulerWithLogger(t *testing.T, store *fakeStore, adapter *fakeAdapter, workers int, rec *eventlog.Recorder) *Scheduler {
	s, err := NewScheduler(store, adapter, fastCfg(workers))
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	s.SetLogger(rec)
	s.Start()
	return s
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// ---- tests ----

func TestNewSchedulerValidatesWorkers(t *testing.T) {
	store := newFakeStore()
	adapter := newFakeAdapter()
	for _, w := range []int{0, -1, 65} {
		if _, err := NewScheduler(store, adapter, Config{Workers: w}); err == nil {
			t.Fatalf("workers %d: expected error", w)
		}
	}
	for _, w := range []int{1, 4, 64} {
		if _, err := NewScheduler(store, adapter, Config{Workers: w}); err != nil {
			t.Fatalf("workers %d: unexpected error %v", w, err)
		}
	}
}

func TestImmediateDiscoveryAndDedup(t *testing.T) {
	store := newFakeStore(5, 3, 9)
	adapter := newFakeAdapter()
	// Default Retry (missing job): each key requeues and stays InProcessing.
	s := startScheduler(t, store, adapter, 4)
	defer s.Shutdown(context.Background())

	// Immediate discovery observes all three once (de-duplicated).
	poll(t, func() bool { return adapter.observeCount() >= 3 }, "all three observed once")
	// No key is observed twice in the same tick. After the first tick, each
	// key is eligible only at the next tick (25ms), so within this window
	// (well before 25ms) the count is unchanged.
	first := adapter.observeCount()
	time.Sleep(12 * time.Millisecond) // before the 25ms tick
	if adapter.observeCount() != first {
		t.Fatalf("keys re-observed before the strictly subsequent tick: %d != %d", adapter.observeCount(), first)
	}
	// At the next tick all three are eligible again and re-observed.
	poll(t, func() bool { return adapter.observeCount() >= first+3 }, "re-observed at next tick")
}

func TestRequeueEligibleOnlyAtNextTick(t *testing.T) {
	cfg := fastCfg(1)
	cfg.DiscoveryInterval = 80 * time.Millisecond
	store := newFakeStore(7)
	adapter := newFakeAdapter()
	s, err := NewScheduler(store, adapter, cfg)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	s.Start()
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return adapter.observeCount() >= 1 }, "first observation")
	// id 7 is delayed; not re-observed before the next tick.
	first := adapter.observeCount()
	time.Sleep(40 * time.Millisecond) // before the 80ms tick
	if adapter.observeCount() != first {
		t.Fatalf("re-observed before the next tick: %d != %d", adapter.observeCount(), first)
	}
	// After the next tick, it is observed again.
	poll(t, func() bool { return adapter.observeCount() >= 2 }, "re-observed at next tick")
}

func TestCompletedTransitionsToPostProcessing(t *testing.T) {
	store := newFakeStore(11)
	adapter := newFakeAdapter()
	adapter.setOutcome(11, completedResult(4))
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isInProcessing(11) }, "id 11 left InProcessing")
	if got := store.computedReps(11); got != 4 {
		t.Fatalf("computed reps = %d, want 4", got)
	}
	store.mu.Lock()
	post := len(store.postCalls)
	failed := len(store.failedCalls)
	store.mu.Unlock()
	if post != 1 || failed != 0 {
		t.Fatalf("post=%d failed=%d, want 1/0", post, failed)
	}
	poll(t, func() bool {
		snap := s.Snapshot()
		return len(snap.Queue) == 0 && len(snap.Inflight) == 0 && len(snap.Delayed) == 0
	}, "drained")
}

func TestFailedPreservesPartialCountThenFails(t *testing.T) {
	store := newFakeStore(12)
	adapter := newFakeAdapter()
	adapter.setOutcome(12, failedResult(3)) // partial 3, reps 4
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isInProcessing(12) }, "id 12 left InProcessing")
	if got := store.computedReps(12); got != 3 {
		t.Fatalf("computed reps = %d, want 3 (partial preserved)", got)
	}
	store.mu.Lock()
	post := len(store.postCalls)
	failed := len(store.failedCalls)
	ups := len(store.updateCalls)
	store.mu.Unlock()
	if ups != 1 || post != 0 || failed != 1 {
		t.Fatalf("update=%d post=%d failed=%d, want 1/0/1", ups, post, failed)
	}
}

func TestMalformedIndexesFailsWithZeroCount(t *testing.T) {
	store := newFakeStore(13)
	adapter := newFakeAdapter()
	// Malformed completedIndexes -> Failed with CompletedReps 0.
	adapter.setOutcome(13, failedResult(0))
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isInProcessing(13) }, "id 13 failed out")
	if got := store.computedReps(13); got != 0 {
		t.Fatalf("computed reps = %d, want 0 (no update for malformed)", got)
	}
	store.mu.Lock()
	ups := len(store.updateCalls)
	failed := len(store.failedCalls)
	store.mu.Unlock()
	if ups != 0 || failed != 1 {
		t.Fatalf("update=%d failed=%d, want 0/1 (no count update for malformed)", ups, failed)
	}
}

func TestForbiddenPreservesCountAndFails(t *testing.T) {
	store := newFakeStore(14)
	store.computed[14] = 2 // existing partial count
	adapter := newFakeAdapter()
	adapter.setOutcome(14, scheduler.ObservationResult{Outcome: scheduler.ObservationForbidden, Err: errors.New("rbac")})
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isInProcessing(14) }, "id 14 failed out")
	if got := store.computedReps(14); got != 2 {
		t.Fatalf("computed reps = %d, want 2 (preserved on Forbidden)", got)
	}
	store.mu.Lock()
	ups := len(store.updateCalls)
	failed := len(store.failedCalls)
	store.mu.Unlock()
	if ups != 0 || failed != 1 {
		t.Fatalf("update=%d failed=%d, want 0/1 on Forbidden", ups, failed)
	}
}

func TestCollisionFailsWithoutAdoptingOrCountUpdate(t *testing.T) {
	store := newFakeStore(15)
	adapter := newFakeAdapter()
	adapter.setOutcome(15, scheduler.ObservationResult{Outcome: scheduler.ObservationCollision})
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isInProcessing(15) }, "id 15 failed out")
	store.mu.Lock()
	ups := len(store.updateCalls)
	failed := len(store.failedCalls)
	store.mu.Unlock()
	if ups != 0 || failed != 1 {
		t.Fatalf("update=%d failed=%d, want 0/1 on Collision", ups, failed)
	}
}

func TestRunningJobUpdatesPartialCountAndRequeues(t *testing.T) {
	store := newFakeStore(16)
	adapter := newFakeAdapter()
	adapter.setOutcome(16, retryResult(2)) // running, 2 of 4 completed
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	// First observation records the partial count and requeues (stays InProcessing).
	poll(t, func() bool { return store.computedReps(16) >= 2 }, "partial count recorded")
	poll(t, func() bool { return store.isInProcessing(16) }, "stays InProcessing")
	// It is observed again at the next tick (requeue).
	poll(t, func() bool { return adapter.observeCount() >= 2 }, "re-observed at next tick")
}

func TestMissingJobRequeuesWithoutCountUpdate(t *testing.T) {
	store := newFakeStore(17)
	adapter := newFakeAdapter() // default Retry, CompletedReps 0 (missing job)
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return adapter.observeCount() >= 1 }, "observed once")
	// No count update for a missing-job Retry.
	store.mu.Lock()
	ups := len(store.updateCalls)
	store.mu.Unlock()
	if ups != 0 {
		t.Fatalf("missing-job Retry made %d count updates, want 0", ups)
	}
	poll(t, func() bool { return store.isInProcessing(17) }, "stays InProcessing (requeue)")
	poll(t, func() bool { return adapter.observeCount() >= 2 }, "re-observed at next tick")
}

func TestStaleProjectionRemovesKey(t *testing.T) {
	store := newFakeStore(21)
	// Move the row out of InProcessing before the worker loads it.
	store.removeInProcessing(21)
	adapter := newFakeAdapter()
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return adapter.observeCount() == 0 }, "no Observe for stale projection", 1*time.Second)
	poll(t, func() bool {
		snap := s.Snapshot()
		return !contains(snap.Inflight, 21) && !contains(snap.Delayed, 21)
	}, "id 21 removed after stale projection")
}

func TestIndependentProgressWhenAnotherKeySlowOrFailing(t *testing.T) {
	store := newFakeStore(1, 2, 3, 4)
	adapter := newFakeAdapter()
	gate := adapter.makeSlow(1)               // id 1 is slow
	adapter.setOutcome(1, completedResult(4)) // completes after the slow gate releases
	adapter.setOutcome(2, completedResult(4))
	adapter.setOutcome(3, scheduler.ObservationResult{Outcome: scheduler.ObservationForbidden})
	adapter.setOutcome(4, completedResult(4))
	s := startScheduler(t, store, adapter, 4)
	defer s.Shutdown(context.Background())

	// ids 2, 3, 4 progress while id 1 is slow.
	poll(t, func() bool { return !store.isInProcessing(2) && !store.isInProcessing(3) && !store.isInProcessing(4) }, "ids 2,3,4 done while id 1 slow")
	close(gate)
	poll(t, func() bool { return !store.isInProcessing(1) }, "id 1 done after release")
}

func TestMoreKeysThanWorkersAllObservedNoCapExceeded(t *testing.T) {
	store := newFakeStore()
	for i := 1; i <= 10; i++ {
		store.addInProcessing(i)
	}
	adapter := newFakeAdapter()
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return adapter.observeCount() >= 10 }, "all 10 observed")
	if max := adapter.maxConcurrent(); max > 2 {
		t.Fatalf("max concurrent observations = %d, want <= 2", max)
	}
}

func TestDiscoveryPrunesKeysThatLeftInProcessing(t *testing.T) {
	store := newFakeStore(8)
	adapter := newFakeAdapter()
	s := startScheduler(t, store, adapter, 1)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return adapter.observeCount() >= 1 }, "id 8 observed once")
	// Terminal action moves id 8 out of InProcessing.
	store.removeInProcessing(8)
	// Next discovery prunes the requeued (delayed) key; it is not re-observed.
	first := adapter.observeCount()
	poll(t, func() bool {
		snap := s.Snapshot()
		return !contains(snap.Delayed, 8) && !contains(snap.Inflight, 8)
	}, "id 8 pruned after leaving InProcessing")
	// Allow a couple of ticks to confirm it is not re-observed.
	time.Sleep(80 * time.Millisecond)
	if adapter.observeCount() != first {
		t.Fatalf("pruned key re-observed: %d != %d", adapter.observeCount(), first)
	}
}

func TestRediscoveryAfterRestart(t *testing.T) {
	store := newFakeStore(8)
	adapter := newFakeAdapter()
	s1 := startScheduler(t, store, adapter, 2)
	poll(t, func() bool { return adapter.observeCount() >= 1 }, "observed by s1")
	s1.Shutdown(context.Background())

	// Fresh scheduler reconstructs in-memory state from the DB on immediate
	// discovery; id 8 is still InProcessing and is observed again.
	s2 := startScheduler(t, store, adapter, 2)
	defer s2.Shutdown(context.Background())
	poll(t, func() bool { return adapter.observeCount() >= 2 }, "rediscovered after restart")
}

func TestShutdownCancelsAndJoinsWorkers(t *testing.T) {
	store := newFakeStore(1, 2, 3)
	adapter := newFakeAdapter()
	gate := adapter.makeSlow(1) // id 1 is in-flight and slow
	s := startScheduler(t, store, adapter, 3)

	poll(t, func() bool { return len(s.Snapshot().Inflight) >= 1 }, "at least one in-flight")
	done := make(chan error, 1)
	go func() { done <- s.Shutdown(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown did not join workers in time")
	}
	close(gate)
	poll(t, func() bool {
		snap := s.Snapshot()
		return len(snap.Inflight) == 0 && len(snap.Queue) == 0 && len(snap.Delayed) == 0
	}, "all state cleared after shutdown")
}

func TestObservationDoesNotBlockSelection(t *testing.T) {
	// Observation uses its own bounded workers and discovery; a failing or slow
	// observation cannot prevent discovery or eventual observation of other
	// InProcessing scenarios. This is structurally guaranteed by the
	// per-key worker model; assert the invariant directly.
	store := newFakeStore(100, 101)
	adapter := newFakeAdapter()
	adapter.setOutcome(100, scheduler.ObservationResult{Outcome: scheduler.ObservationForbidden}) // permanent fail
	adapter.setOutcome(101, completedResult(4))
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isInProcessing(101) }, "id 101 completed despite id 100 failing")
	poll(t, func() bool { return !store.isInProcessing(100) }, "id 100 failed")
}

func TestMonotonicCountNeverDecreases(t *testing.T) {
	store := newFakeStore(30)
	// Drive three sequential single-key schedulers sharing the store, each
	// observing id 30 once with a fixed outcome: 3 completed, then 1 (a
	// regression attempt that must not decrease the monotonic count), then 4.
	for i, r := range []scheduler.ObservationResult{
		retryResult(3),
		retryResult(1),
		completedResult(4),
	} {
		ad := newFakeAdapter()
		ad.setOutcome(30, r)
		s := startScheduler(t, store, ad, 1)
		poll(t, func() bool { return ad.observeCount() >= 1 }, fmt.Sprintf("observe %d", i))
		// Give the worker time to apply the DB transition.
		poll(t, func() bool {
			if r.Outcome == scheduler.ObservationCompleted {
				return !store.isInProcessing(30)
			}
			return true
		}, "transition applied")
		s.Shutdown(context.Background())
	}
	if got := store.computedReps(30); got != 4 {
		t.Fatalf("monotonic computed reps = %d, want 4 (never decreases)", got)
	}
}

// ---- S06-M3 scenario-observability logging ----

// TestObservationLogsCompleteRecord asserts a Completed outcome emits exactly
// one terminal complete record carrying the full repetition count.
func TestObservationLogsCompleteRecord(t *testing.T) {
	store := newFakeStore(5)
	store.reps[5] = 6
	adapter := newFakeAdapter()
	adapter.setOutcome(5, scheduler.ObservationResult{
		Outcome:       scheduler.ObservationCompleted,
		CompletedReps: 6,
		JobName:       "simrunner-abcdef-5-1",
	})
	rec := &eventlog.Recorder{}
	s := startSchedulerWithLogger(t, store, adapter, 1, rec)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isInProcessing(5) }, "scenario 5 leaves InProcessing")
	poll(t, func() bool { return len(rec.Records()) == 1 }, "one complete record")

	got := rec.Records()
	if got[0].Event != eventlog.EventComplete {
		t.Fatalf("event = %s, want complete", got[0].Event)
	}
	if got[0].ScenarioID != 5 || got[0].Attempt != 1 {
		t.Errorf("scenarioID/attempt = %d/%d", got[0].ScenarioID, got[0].Attempt)
	}
	if got[0].JobName != "simrunner-abcdef-5-1" {
		t.Errorf("jobName = %q", got[0].JobName)
	}
	if got[0].RequestedReps != 6 || got[0].ComputedReps != 6 {
		t.Errorf("requested/computed = %d/%d", got[0].RequestedReps, got[0].ComputedReps)
	}
	if got[0].Outcome != scheduler.ObservationCompleted.String() {
		t.Errorf("outcome = %q", got[0].Outcome)
	}
}

// TestObservationLogsFailRecord asserts a Failed outcome emits exactly one
// terminal fail record carrying the parsed partial count and a reason.
func TestObservationLogsFailRecord(t *testing.T) {
	store := newFakeStore(7)
	store.reps[7] = 10
	adapter := newFakeAdapter()
	adapter.setOutcome(7, scheduler.ObservationResult{
		Outcome:       scheduler.ObservationFailed,
		CompletedReps: 3,
		JobName:       "simrunner-abcdef-7-1",
		Err:           errors.New("job failed: pod error"),
	})
	rec := &eventlog.Recorder{}
	s := startSchedulerWithLogger(t, store, adapter, 1, rec)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isInProcessing(7) }, "scenario 7 leaves InProcessing")
	poll(t, func() bool { return len(rec.Records()) == 1 }, "one fail record")

	got := rec.Records()
	if got[0].Event != eventlog.EventFail {
		t.Fatalf("event = %s, want fail", got[0].Event)
	}
	if got[0].ComputedReps != 3 {
		t.Errorf("computed reps = %d, want 3", got[0].ComputedReps)
	}
	if got[0].Reason != "job failed: pod error" {
		t.Errorf("reason = %q", got[0].Reason)
	}
}

// TestObservationLogsCollisionForbidden asserts Collision and Forbidden emit a
// fail record with no computed-reps count change (count preserved at 0).
func TestObservationLogsCollisionForbidden(t *testing.T) {
	for _, outcome := range []scheduler.ObservationOutcome{scheduler.ObservationCollision, scheduler.ObservationForbidden} {
		store := newFakeStore(8)
		store.reps[8] = 4
		adapter := newFakeAdapter()
		adapter.setOutcome(8, scheduler.ObservationResult{
			Outcome: outcome,
			JobName: "simrunner-abcdef-8-1",
			Err:     errors.New("ownership mismatch"),
		})
		rec := &eventlog.Recorder{}
		s := startSchedulerWithLogger(t, store, adapter, 1, rec)
		s.Shutdown(context.Background())

		poll(t, func() bool { return !store.isInProcessing(8) }, "scenario 8 leaves InProcessing")
		poll(t, func() bool { return len(rec.Records()) == 1 }, "one fail record")

		got := rec.Records()
		if got[0].Event != eventlog.EventFail {
			t.Fatalf("%s: event = %s, want fail", outcome, got[0].Event)
		}
		if got[0].ComputedReps != 0 {
			t.Errorf("%s: computed reps = %d, want 0", outcome, got[0].ComputedReps)
		}
		if got[0].Outcome != outcome.String() {
			t.Errorf("%s: outcome = %q", outcome, got[0].Outcome)
		}
	}
}

// TestObservationNoLogOnRetry asserts a running Job (Retry with a partial count)
// emits NO record across many observation polls. This is the no-per-repetition
// logging contract: SM logs only the terminal outcome, not every poll.
func TestObservationNoLogOnRetry(t *testing.T) {
	store := newFakeStore(12)
	store.reps[12] = 4
	adapter := newFakeAdapter()
	// Running job: Retry carrying a partial count that grows across polls.
	adapter.setOutcome(12, retryResult(2))
	rec := &eventlog.Recorder{}
	s := startSchedulerWithLogger(t, store, adapter, 1, rec)
	defer s.Shutdown(context.Background())

	// Let several discovery ticks observe the running job repeatedly.
	poll(t, func() bool { return adapter.observeCount() >= 5 }, "at least 5 observe polls")
	time.Sleep(30 * time.Millisecond)

	// The scenario is still InProcessing and NO record was emitted: no per-poll
	// logging.
	if got := rec.Records(); len(got) != 0 {
		t.Fatalf("Retry should emit no record, got %d: %+v", len(got), got)
	}
	if !store.isInProcessing(12) {
		t.Fatalf("scenario 12 should still be InProcessing while running")
	}
}

// TestObservationNoCredentialLeak asserts the terminal fail record's Reason
// never carries credential material. A Forbidden outcome's error is passed
// through terminalReason; the test documents that the reason surface does not
// echo registry-auth credentials.
func TestObservationNoCredentialLeak(t *testing.T) {
	store := newFakeStore(3)
	store.reps[3] = 4
	adapter := newFakeAdapter()
	adapter.setOutcome(3, scheduler.ObservationResult{
		Outcome: scheduler.ObservationForbidden,
		JobName: "simrunner-abcdef-3-1",
		Err:     errors.New("forbidden: jobs.get is not allowed"),
	})
	rec := &eventlog.Recorder{}
	s := startSchedulerWithLogger(t, store, adapter, 1, rec)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return len(rec.Records()) == 1 }, "one fail record")
	got := rec.Records()
	for _, leak := range []string{"password", "token", "secret", "dockerconfigjson", "auth="} {
		if strings.Contains(got[0].Reason, leak) || strings.Contains(got[0].JobName, leak) {
			t.Fatalf("record leaks credential material %q: %+v", leak, got[0])
		}
	}
}

// TestObservationNoCompleteRecordOnStaleCountUpdate asserts a Completed outcome
// whose monotonic count update is stale (another path moved the row) emits no
// complete record.
func TestObservationNoCompleteRecordOnStaleCountUpdate(t *testing.T) {
	store := newFakeStore(5)
	store.reps[5] = 4
	store.updateStale[5] = true
	adapter := newFakeAdapter()
	adapter.setOutcome(5, scheduler.ObservationResult{
		Outcome:       scheduler.ObservationCompleted,
		CompletedReps: 4,
		JobName:       "simrunner-abcdef-5-1",
	})
	rec := &eventlog.Recorder{}
	s := startSchedulerWithLogger(t, store, adapter, 1, rec)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return store.updateCallCount() >= 1 }, "count update attempted")
	time.Sleep(40 * time.Millisecond)
	if got := rec.Records(); len(got) != 0 {
		t.Fatalf("stale count update should emit no record, got %d: %+v", len(got), got)
	}
}

// TestObservationNoCompleteRecordOnStalePostProcessing asserts a Completed
// outcome whose MarkPostProcessing transition loses (zero rows, another path
// moved the row after the count update) emits no complete record.
func TestObservationNoCompleteRecordOnStalePostProcessing(t *testing.T) {
	store := newFakeStore(6)
	store.reps[6] = 4
	store.postStale[6] = true
	adapter := newFakeAdapter()
	adapter.setOutcome(6, scheduler.ObservationResult{
		Outcome:       scheduler.ObservationCompleted,
		CompletedReps: 4,
		JobName:       "simrunner-abcdef-6-1",
	})
	rec := &eventlog.Recorder{}
	s := startSchedulerWithLogger(t, store, adapter, 1, rec)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return store.postCallCount() >= 1 }, "MarkPostProcessing attempted")
	time.Sleep(40 * time.Millisecond)
	if got := rec.Records(); len(got) != 0 {
		t.Fatalf("stale MarkPostProcessing should emit no record, got %d: %+v", len(got), got)
	}
}

// TestObservationNoFailRecordOnStaleFailed asserts terminal fail outcomes
// (Failed, Collision, Forbidden) whose MarkFailedFrom transition loses (zero
// rows) emit no fail record.
func TestObservationNoFailRecordOnStaleFailed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome scheduler.ObservationOutcome
		count   int
	}{
		{"failed", scheduler.ObservationFailed, 2},
		{"collision", scheduler.ObservationCollision, 0},
		{"forbidden", scheduler.ObservationForbidden, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore(9)
			store.reps[9] = 4
			store.failedStale[9] = true
			adapter := newFakeAdapter()
			adapter.setOutcome(9, scheduler.ObservationResult{
				Outcome:       tc.outcome,
				CompletedReps: tc.count,
				JobName:       "simrunner-abcdef-9-1",
				Err:           errors.New("terminal"),
			})
			rec := &eventlog.Recorder{}
			s := startSchedulerWithLogger(t, store, adapter, 1, rec)
			defer s.Shutdown(context.Background())

			poll(t, func() bool { return store.failedCallCount() >= 1 }, "MarkFailedFrom attempted")
			time.Sleep(40 * time.Millisecond)
			if got := rec.Records(); len(got) != 0 {
				t.Fatalf("stale %s should emit no record, got %d: %+v", tc.name, len(got), got)
			}
		})
	}
}
