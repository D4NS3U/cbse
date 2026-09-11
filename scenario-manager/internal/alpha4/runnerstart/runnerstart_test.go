package runnerstart

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/eventlog"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/scheduler"
)

// ---- fakeStore: an in-memory Core DB state machine ----

type fakeStore struct {
	mu sync.Mutex

	// starting is the set of scenario IDs currently in StartingRunners.
	starting map[int]struct{}
	// projections carries the projection returned for a StartingRunners ID.
	projections map[int]*Projection

	loadErr       error // forces LoadProjection to return this transport error
	listErr       error // forces ListStartingRunners to return this error
	markInProcErr error
	markFailedErr error
	// guardFailInProc makes MarkInProcessing return false (zero rows) to simulate
	// lifecycle-gate closure racing the guarded transition.
	gateClosedInProc map[int]bool

	markInProcCalls []int
	markFailedCalls []int

	// markInProcErrOnce makes the next n MarkInProcessing calls for an id
	// return a transport error before resuming normal behavior (transient retry).
	markInProcErrOnce map[int]int
}

func newFakeStore(ids ...int) *fakeStore {
	f := &fakeStore{
		starting:          make(map[int]struct{}),
		projections:       make(map[int]*Projection),
		markInProcErrOnce: make(map[int]int),
	}
	for _, id := range ids {
		f.starting[id] = struct{}{}
		f.projections[id] = &Projection{
			ID: id, TranslationAttempt: 1, NumberOfReps: 4,
			ContainerImage:   "reg.example.com/runner@sha256:" + repeat("a", 64),
			ProjectNamespace: "ns-x", ProjectName: "exp-x",
		}
	}
	return f
}

func repeat(s string, n int) string {
	b := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		b = append(b, s...)
	}
	return string(b)
}

func (f *fakeStore) ListStartingRunners(ctx context.Context) ([]int, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]int, 0, len(f.starting))
	for id := range f.starting {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids, nil
}

func (f *fakeStore) LoadProjection(ctx context.Context, scenarioID int) (*Projection, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.starting[scenarioID]; !ok {
		return nil, nil // stale
	}
	p := f.projections[scenarioID]
	if p == nil {
		return &Projection{ID: scenarioID, TranslationAttempt: 1, NumberOfReps: 4,
			ContainerImage:   "reg.example.com/runner@sha256:" + repeat("a", 64),
			ProjectNamespace: "ns-x", ProjectName: "exp-x"}, nil
	}
	cp := *p
	return &cp, nil
}

func (f *fakeStore) MarkInProcessing(ctx context.Context, scenarioID int) (bool, error) {
	f.mu.Lock()
	f.markInProcCalls = append(f.markInProcCalls, scenarioID)
	if n, ok := f.markInProcErrOnce[scenarioID]; ok && n > 0 {
		f.markInProcErrOnce[scenarioID] = n - 1
		f.mu.Unlock()
		return false, errors.New("simulated MarkInProcessing transport failure")
	}
	if f.markInProcErr != nil {
		f.mu.Unlock()
		return false, f.markInProcErr
	}
	if f.gateClosedInProc[scenarioID] {
		// Gate closed before the guarded transition: zero rows.
		delete(f.starting, scenarioID)
		f.mu.Unlock()
		return false, nil
	}
	if _, ok := f.starting[scenarioID]; !ok {
		f.mu.Unlock()
		return false, nil // stale
	}
	delete(f.starting, scenarioID) // StartingRunners -> InProcessing
	f.mu.Unlock()
	return true, nil
}

func (f *fakeStore) MarkFailed(ctx context.Context, scenarioID int) (bool, error) {
	f.mu.Lock()
	f.markFailedCalls = append(f.markFailedCalls, scenarioID)
	if f.markFailedErr != nil {
		f.mu.Unlock()
		return false, f.markFailedErr
	}
	if _, ok := f.starting[scenarioID]; !ok {
		f.mu.Unlock()
		return false, nil
	}
	delete(f.starting, scenarioID)
	f.mu.Unlock()
	return true, nil
}

// markInProcFailNext makes the next n MarkInProcessing calls for the id fail.
func (f *fakeStore) markInProcFailNext(id, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markInProcErrOnce[id] = n
}

func (f *fakeStore) isStarting(id int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.starting[id]
	return ok
}

func (f *fakeStore) markClosed(id int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.starting, id) // terminal action moved it out of StartingRunners
}

func (f *fakeStore) addStarting(id int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starting[id] = struct{}{}
	if f.projections[id] == nil {
		f.projections[id] = &Projection{ID: id, TranslationAttempt: 1, NumberOfReps: 4,
			ContainerImage:   "reg.example.com/runner@sha256:" + repeat("a", 64),
			ProjectNamespace: "ns-x", ProjectName: "exp-x"}
	}
}

// ---- fakeAdapter: a stand-in for the scheduler.RunnerStartAdapter ----

type startCall struct {
	req scheduler.RunnerStartRequest
}

type deleteCall struct {
	namespace, jobName, uid string
}

type fakeAdapter struct {
	mu sync.Mutex

	// Per-id outcome override. If nil for an id, default is Created.
	outcomes map[int]scheduler.RunnerStartResult
	// defaultResult is used when no per-id override is set.
	defaultResult *scheduler.RunnerStartResult

	// deleteResults controls DeleteCreated: error per call sequence per id.
	// deleteErr returns an error if set (applies to all deletes unless per-id).
	deleteErr     error
	deleteErrOnce map[int]int // id -> number of remaining errors before success

	// slowStart blocks Start for an id until its chan is closed.
	slowStart map[int]chan struct{}
	// onStart is invoked once per Start call (after recording) to let a test
	// simulate side effects of the experiment phase action (e.g. moving a row
	// out of StartingRunners on an ExperimentTerminal outcome).
	onStart func(scheduler.RunnerStartRequest)

	startCalls  []startCall
	deleteCalls []deleteCall

	inFlightStart atomic.Int32
	maxInFlight   atomic.Int32
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{
		outcomes:      make(map[int]scheduler.RunnerStartResult),
		deleteErrOnce: make(map[int]int),
		slowStart:     make(map[int]chan struct{}),
	}
}

func createdResult(id int) scheduler.RunnerStartResult {
	return scheduler.RunnerStartResult{
		Outcome:       scheduler.RunnerStartCreated,
		JobName:       fmt.Sprintf("job-s%d", id),
		CreatedJobUID: fmt.Sprintf("uid-s%d", id),
	}
}

func (a *fakeAdapter) setOutcome(id int, r scheduler.RunnerStartResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.outcomes[id] = r
}

func (a *fakeAdapter) makeSlow(id int) chan struct{} {
	ch := make(chan struct{})
	a.mu.Lock()
	a.slowStart[id] = ch
	a.mu.Unlock()
	return ch
}

func (a *fakeAdapter) Start(ctx context.Context, req scheduler.RunnerStartRequest) scheduler.RunnerStartResult {
	cur := a.inFlightStart.Add(1)
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
	defer a.inFlightStart.Add(-1)

	a.mu.Lock()
	a.startCalls = append(a.startCalls, startCall{req: req})
	slow := a.slowStart[req.ScenarioID]
	r, ok := a.outcomes[req.ScenarioID]
	hook := a.onStart
	a.mu.Unlock()

	if hook != nil {
		hook(req)
	}

	if slow != nil {
		select {
		case <-slow:
		case <-ctx.Done():
			return scheduler.RunnerStartResult{Outcome: scheduler.RunnerStartTransient, Err: ctx.Err()}
		}
	}
	if ok {
		return r
	}
	if a.defaultResult != nil {
		return *a.defaultResult
	}
	return createdResult(req.ScenarioID)
}

func (a *fakeAdapter) DeleteCreated(ctx context.Context, namespace, name, uid string) error {
	a.mu.Lock()
	a.deleteCalls = append(a.deleteCalls, deleteCall{namespace: namespace, jobName: name, uid: uid})
	id := parseJobID(name)
	if n, ok := a.deleteErrOnce[id]; ok && n > 0 {
		a.deleteErrOnce[id] = n - 1
		a.mu.Unlock()
		return errors.New("simulated cleanup transport failure")
	}
	err := a.deleteErr
	a.mu.Unlock()
	return err
}

// deleteFailNext makes the next n DeleteCreated calls for the id fail.
func (a *fakeAdapter) deleteFailNext(id int, n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.deleteErrOnce[id] = n
}

func parseJobID(name string) int {
	var id int
	fmt.Sscanf(name, "job-s%d", &id)
	return id
}

func (a *fakeAdapter) maxConcurrent() int32 { return a.maxInFlight.Load() }
func (a *fakeAdapter) startCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.startCalls)
}
func (a *fakeAdapter) deleteCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.deleteCalls)
}

// ---- helpers ----

func fastCfg(workers int) Config {
	return Config{
		Workers:           workers,
		DiscoveryInterval: 15 * time.Millisecond,
		TransientDelay:    40 * time.Millisecond,
	}
}

// poll waits for cond to return true or the deadline to expire.
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

// ---- tests ----

func TestNewSchedulerValidatesWorkers(t *testing.T) {
	store := newFakeStore()
	adapter := newFakeAdapter()
	for _, w := range []int{0, -1, 65, 100} {
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

func TestImmediateDiscoveryAndAscendingDispatch(t *testing.T) {
	store := newFakeStore(5, 3, 9, 1) // unsorted input
	adapter := newFakeAdapter()
	s := startScheduler(t, store, adapter, 4)
	defer s.Shutdown(context.Background())

	// All four are discovered and dispatched (Created) -> InProcessing, which
	// removes them from StartingRunners.
	poll(t, func() bool { return adapter.startCount() >= 4 }, "all 4 dispatched")
	poll(t, func() bool {
		snap := s.Snapshot()
		return len(snap.Ready) == 0 && len(snap.Inflight) == 0 && len(snap.Delayed) == 0
	}, "scheduler drains to empty")

	// Dispatch order is ascending (lowest eligible first). With 4 workers and 4
	// IDs, the Start calls may interleave, but the coordinator dispatches the
	// lowest ready IDs first. Assert every ID was started exactly once.
	adapter.mu.Lock()
	seen := map[int]int{}
	for _, c := range adapter.startCalls {
		seen[c.req.ScenarioID]++
	}
	adapter.mu.Unlock()
	for _, id := range []int{1, 3, 5, 9} {
		if seen[id] != 1 {
			t.Fatalf("id %d started %d times, want 1 (calls=%v)", id, seen[id], seen)
		}
	}
}

func TestLowestEligibleDispatchedFirst(t *testing.T) {
	// With one reconciler, the dispatch order is exactly the recording order, so
	// ascending dispatch is observable: the lowest eligible ID is always sent
	// to the single worker before any higher ID.
	store := newFakeStore(50, 40, 30, 20, 10) // unsorted input
	adapter := newFakeAdapter()
	s := startScheduler(t, store, adapter, 1)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return adapter.startCount() >= 5 }, "all five dispatched")
	adapter.mu.Lock()
	order := make([]int, len(adapter.startCalls))
	for i, c := range adapter.startCalls {
		order[i] = c.req.ScenarioID
	}
	adapter.mu.Unlock()
	want := []int{10, 20, 30, 40, 50}
	if len(order) < len(want) || !equalInts(order[:len(want)], want) {
		t.Fatalf("dispatch order = %v, want ascending %v", order, want)
	}
	poll(t, func() bool {
		snap := s.Snapshot()
		return len(snap.Inflight) == 0 && len(snap.Delayed) == 0 && len(snap.Ready) == 0
	}, "drained")
}

func TestSlowIDOccupiesWorkerWhileHigherIDsWait(t *testing.T) {
	// With two reconcilers and a slow lowest ID, the second worker takes the
	// next-lowest ID while the lowest ID holds a worker.
	store := newFakeStore(10, 20, 30)
	adapter := newFakeAdapter()
	gate := adapter.makeSlow(10) // id 10 occupies a worker
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return adapter.startCount() >= 2 }, "first two dispatched")
	// id 10 is in-flight (slow); id 20 was dispatched too and proceeds (Created).
	poll(t, func() bool { return !store.isStarting(20) }, "id 20 transitioned while id 10 slow")
	close(gate)
	poll(t, func() bool { return adapter.startCount() >= 3 }, "all three dispatched")
	poll(t, func() bool {
		snap := s.Snapshot()
		return len(snap.Inflight) == 0 && len(snap.Delayed) == 0 && len(snap.Ready) == 0
	}, "drained")
}

func TestSlowReconcileDoesNotBlockLaterIDs(t *testing.T) {
	store := newFakeStore(1, 2, 3)
	adapter := newFakeAdapter()
	gate := adapter.makeSlow(1) // id 1 is slow
	s := startScheduler(t, store, adapter, 3)
	defer s.Shutdown(context.Background())

	// ids 2 and 3 are submitted (Created) while id 1 is still in-flight.
	poll(t, func() bool {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		counted := 0
		for _, c := range adapter.startCalls {
			if c.req.ScenarioID == 2 || c.req.ScenarioID == 3 {
				counted++
			}
		}
		return counted >= 2
	}, "ids 2 and 3 submitted while id 1 slow")

	close(gate) // release id 1
	poll(t, func() bool {
		snap := s.Snapshot()
		return len(snap.Inflight) == 0 && len(snap.Delayed) == 0
	}, "all drained after releasing slow id")
}

func TestTransientFailureDelaysFiveSecondsAndRegainsPosition(t *testing.T) {
	store := newFakeStore(1, 2, 3)
	adapter := newFakeAdapter()
	// id 1 fails transiently on first Start, then Created on retry.
	adapter.setOutcome(1, scheduler.RunnerStartResult{Outcome: scheduler.RunnerStartTransient})
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	// id 1 fails transiently and goes delayed; ids 2,3 proceed (Created).
	poll(t, func() bool { return adapter.startCount() >= 3 }, "three starts (1 transient + 2 created)")
	// id 1 is delayed (not eligible), not in-flight.
	poll(t, func() bool {
		snap := s.Snapshot()
		return contains(snap.Delayed, 1) && !contains(snap.Inflight, 1)
	}, "id 1 delayed after transient failure")

	// Reset id 1 to Created so the retry succeeds.
	adapter.setOutcome(1, createdResult(1))

	// id 1 retries after the transient delay and succeeds (Created -> InProcessing).
	poll(t, func() bool {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		count := 0
		for _, c := range adapter.startCalls {
			if c.req.ScenarioID == 1 {
				count++
			}
		}
		return count >= 2
	}, "id 1 retried after delay")

	poll(t, func() bool { return !store.isStarting(1) }, "id 1 transitioned to InProcessing")
	poll(t, func() bool {
		snap := s.Snapshot()
		return len(snap.Delayed) == 0 && len(snap.Inflight) == 0 && len(snap.Ready) == 0
	}, "fully drained")
}

func TestTransientDelayNotEligibleBeforeDelay(t *testing.T) {
	cfg := fastCfg(1)
	cfg.TransientDelay = 200 * time.Millisecond
	store := newFakeStore(7)
	adapter := newFakeAdapter()
	adapter.setOutcome(7, scheduler.RunnerStartResult{Outcome: scheduler.RunnerStartTransient})
	s, err := NewScheduler(store, adapter, cfg)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	s.Start()
	defer s.Shutdown(context.Background())

	// First attempt fails transiently.
	poll(t, func() bool { return adapter.startCount() >= 1 }, "first attempt")
	// id 7 is delayed; not re-dispatched before the 200ms delay.
	poll(t, func() bool {
		snap := s.Snapshot()
		return contains(snap.Delayed, 7)
	}, "id 7 delayed")
	startCountAt := adapter.startCount()
	time.Sleep(100 * time.Millisecond) // well before the 200ms delay
	if adapter.startCount() != startCountAt {
		t.Fatalf("id 7 retried before its transient delay elapsed: %d != %d", adapter.startCount(), startCountAt)
	}
	// After the delay it retries.
	poll(t, func() bool { return adapter.startCount() >= 2 }, "retry after delay")
}

func TestPermanentFailureRemovesID(t *testing.T) {
	store := newFakeStore(11)
	adapter := newFakeAdapter()
	adapter.setOutcome(11, scheduler.RunnerStartResult{Outcome: scheduler.RunnerStartForbidden, Err: errors.New("rbac")})
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isStarting(11) }, "id 11 failed out of StartingRunners")
	poll(t, func() bool {
		snap := s.Snapshot()
		return !contains(snap.Delayed, 11) && !contains(snap.Inflight, 11) && !contains(snap.Ready, 11)
	}, "id 11 removed from all sets")
	store.mu.Lock()
	calls := len(store.markFailedCalls)
	store.mu.Unlock()
	if calls == 0 {
		t.Fatal("expected MarkFailed to be called for permanent failure")
	}
}

func TestStaleProjectionRemovesID(t *testing.T) {
	store := newFakeStore(21)
	adapter := newFakeAdapter()
	// Move the row out of StartingRunners before the worker loads it.
	store.markClosed(21)
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool {
		snap := s.Snapshot()
		return !contains(snap.Inflight, 21) && !contains(snap.Delayed, 21) && !contains(snap.Ready, 21)
	}, "id 21 removed after stale projection")
	// No Start (the worker removes on stale projection without creating a Job).
	poll(t, func() bool { return adapter.startCount() == 0 }, "no Start for stale projection", 1*time.Second)
}

func TestLifecycleGateClosureRemovesID(t *testing.T) {
	store := newFakeStore(33)
	adapter := newFakeAdapter()
	adapter.setOutcome(33, scheduler.RunnerStartResult{Outcome: scheduler.RunnerStartExperimentTerminal})
	// The experiment phase action owns the row transition; simulate it moving
	// id 33 out of StartingRunners when Start reports the terminal gate so the
	// scheduler does not re-discover it forever.
	once := sync.Once{}
	adapter.onStart = func(req scheduler.RunnerStartRequest) {
		if req.ScenarioID == 33 {
			once.Do(func() { store.markClosed(33) })
		}
	}
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool {
		snap := s.Snapshot()
		return !contains(snap.Inflight, 33) && !contains(snap.Delayed, 33) && !contains(snap.Ready, 33)
	}, "id 33 removed after terminal experiment")
	// The experiment phase action owns the DB transition; SM made none.
	store.mu.Lock()
	inp := len(store.markInProcCalls)
	failed := len(store.markFailedCalls)
	store.mu.Unlock()
	if inp != 0 || failed != 0 {
		t.Fatalf("terminal experiment: MarkInProcessing=%d MarkFailed=%d, want 0/0", inp, failed)
	}
}

func TestGateClosurePrunesReadyAndDelayed(t *testing.T) {
	cfg := fastCfg(1)
	store := newFakeStore(1, 2, 3)
	adapter := newFakeAdapter()
	// id 1 transiently fails -> delayed. ids 2,3 need the single worker after 1.
	adapter.setOutcome(1, scheduler.RunnerStartResult{Outcome: scheduler.RunnerStartTransient})
	s, err := NewScheduler(store, adapter, cfg)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	s.Start()
	defer s.Shutdown(context.Background())

	// id 1 fails and goes delayed; ids 2 and 3 get dispatched and transition.
	poll(t, func() bool { return !store.isStarting(2) && !store.isStarting(3) }, "ids 2,3 done")
	poll(t, func() bool { return contains(s.Snapshot().Delayed, 1) }, "id 1 delayed")

	// Gate closes: terminal action bulk-fails id 1 out of StartingRunners.
	store.markClosed(1)
	// Next discovery tick prunes the non-cleanup delayed id 1.
	poll(t, func() bool {
		snap := s.Snapshot()
		return !contains(snap.Delayed, 1) && !contains(snap.Ready, 1) && !contains(snap.Inflight, 1)
	}, "delayed id 1 pruned after gate closure")
}

func TestGateRaceCleanupDeletesCreatedJob(t *testing.T) {
	store := newFakeStore(42)
	adapter := newFakeAdapter()
	// Start returns Created; MarkInProcessing returns false (gate closed).
	store.gateClosedInProc = map[int]bool{42: true}
	s := startScheduler(t, store, adapter, 1)
	defer s.Shutdown(context.Background())

	// The worker creates the Job, then the guarded transition loses to gate
	// closure, then it deletes the created Job using the returned UID.
	poll(t, func() bool { return adapter.deleteCount() >= 1 }, "gate-race cleanup delete issued")
	adapter.mu.Lock()
	d := adapter.deleteCalls[len(adapter.deleteCalls)-1]
	adapter.mu.Unlock()
	if d.uid != "uid-s42" {
		t.Fatalf("cleanup delete uid = %q, want uid-s42 (returned UID precondition)", d.uid)
	}
	if d.jobName != "job-s42" {
		t.Fatalf("cleanup delete jobName = %q, want job-s42", d.jobName)
	}
	poll(t, func() bool {
		snap := s.Snapshot()
		return !contains(snap.Inflight, 42) && !contains(snap.Delayed, 42)
	}, "id 42 removed after cleanup")
}

func TestGateRaceCleanupRetriesAfterForbidden(t *testing.T) {
	store := newFakeStore(42)
	adapter := newFakeAdapter()
	store.gateClosedInProc = map[int]bool{42: true}
	// Make every cleanup delete fail (Forbidden) so the retry cadence runs.
	adapter.deleteErr = errors.New("forbidden")
	s := startScheduler(t, store, adapter, 1)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return adapter.deleteCount() >= 1 }, "first cleanup attempt")
	first := adapter.deleteCount()
	// The cleanup retries on the transient-delay cadence.
	poll(t, func() bool { return adapter.deleteCount() > first }, "cleanup retried after failure")
	// Now allow it to succeed and confirm removal.
	adapter.mu.Lock()
	adapter.deleteErr = nil
	adapter.mu.Unlock()
	poll(t, func() bool {
		snap := s.Snapshot()
		return !contains(snap.Delayed, 42) && !contains(snap.Inflight, 42)
	}, "id 42 removed after cleanup succeeds")
}

func TestConfirmedAlreadyExistsNoCleanupOnStaleSuccess(t *testing.T) {
	store := newFakeStore(7)
	adapter := newFakeAdapter()
	adapter.setOutcome(7, scheduler.RunnerStartResult{Outcome: scheduler.RunnerStartConfirmed, JobName: "job-s7"})
	// Guarded transition loses (another replica won) -> zero rows.
	store.gateClosedInProc = map[int]bool{7: true}
	s := startScheduler(t, store, adapter, 1)
	defer s.Shutdown(context.Background())

	poll(t, func() bool {
		snap := s.Snapshot()
		return !contains(snap.Inflight, 7) && !contains(snap.Delayed, 7)
	}, "id 7 removed after confirmed+stale")
	// Confirmed path performs no cleanup delete (the Job was not created here).
	if adapter.deleteCount() != 0 {
		t.Fatalf("Confirmed+stale performed %d cleanup deletes, want 0", adapter.deleteCount())
	}
}

func TestMoreScenariosThanWorkersAllCreatedNoCapExceeded(t *testing.T) {
	store := newFakeStore()
	for i := 1; i <= 10; i++ {
		store.addStarting(i)
	}
	adapter := newFakeAdapter()
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return adapter.startCount() >= 10 }, "all 10 started")
	poll(t, func() bool {
		snap := s.Snapshot()
		return len(snap.Inflight) == 0 && len(snap.Delayed) == 0 && len(snap.Ready) == 0
	}, "all drained")
	if max := adapter.maxConcurrent(); max > 2 {
		t.Fatalf("max concurrent runner-start workflows = %d, want <= 2 (workers)", max)
	}
}

func TestPendingPodsConsumeNoReconciler(t *testing.T) {
	// A Created outcome returns immediately; the reconciler is released at once.
	// Pod scheduling is irrelevant to the runner-start slot.
	store := newFakeStore(1, 2)
	adapter := newFakeAdapter()
	s := startScheduler(t, store, adapter, 2)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return adapter.startCount() >= 2 }, "both started")
	poll(t, func() bool {
		snap := s.Snapshot()
		return len(snap.Inflight) == 0
	}, "no reconciler retained after Created")
}

func TestConcurrentReplicasConvergeOnAlreadyExists(t *testing.T) {
	// Two schedulers share one store. Both discover id 5. The "first" replica
	// creates the Job (Created); the "second" sees AlreadyExists (Confirmed).
	// Exactly one guarded StartingRunners -> InProcessing transition wins.
	store := newFakeStore(5)
	a1 := newFakeAdapter()
	a2 := newFakeAdapter()
	a1.setOutcome(5, createdResult(5)) // replica 1 creates
	a2.setOutcome(5, scheduler.RunnerStartResult{Outcome: scheduler.RunnerStartConfirmed, JobName: "job-s5"})
	// Gate both Starts so each replica discovers id 5 while it is still
	// StartingRunners; otherwise the winner's MarkInProcessing removes the row
	// before the loser's immediate discovery runs.
	gate1 := a1.makeSlow(5)
	gate2 := a2.makeSlow(5)

	s1 := startScheduler(t, store, a1, 1)
	s2 := startScheduler(t, store, a2, 1)
	defer s1.Shutdown(context.Background())
	defer s2.Shutdown(context.Background())

	poll(t, func() bool { return a1.startCount()+a2.startCount() >= 2 }, "both replicas entered Start")
	close(gate1)
	close(gate2)
	poll(t, func() bool { return !store.isStarting(5) }, "id 5 left StartingRunners exactly once")

	store.mu.Lock()
	inp := len(store.markInProcCalls)
	store.mu.Unlock()
	// Both replicas call MarkInProcessing, but the guarded UPDATE means exactly
	// one returns true; the other is stale success. The store records both
	// calls but the row moved once.
	if inp < 1 {
		t.Fatalf("expected at least 1 MarkInProcessing call, got %d", inp)
	}
	poll(t, func() bool {
		s1n := s1.Snapshot()
		s2n := s2.Snapshot()
		return len(s1n.Inflight) == 0 && len(s2n.Inflight) == 0
	}, "both replicas drained")
}

func TestShutdownCancelsAndJoinsWorkers(t *testing.T) {
	store := newFakeStore(1, 2, 3)
	adapter := newFakeAdapter()
	gate := adapter.makeSlow(1) // id 1 is in-flight and slow
	s := startScheduler(t, store, adapter, 3)

	poll(t, func() bool {
		snap := s.Snapshot()
		return len(snap.Inflight) >= 1
	}, "at least one in-flight before shutdown")

	// Shutdown cancels in-flight (fail-fast via coordCtx) and joins.
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
	close(gate) // release the slow gate so the worker goroutine can exit cleanly
	// After Shutdown returns, the coordinator + all workers have exited. Give
	// a brief moment for the slow worker's fail-fast path to unwind.
	poll(t, func() bool {
		snap := s.Snapshot()
		return len(snap.Inflight) == 0 && len(snap.Delayed) == 0 && len(snap.Ready) == 0
	}, "all sets cleared after shutdown")
}

func TestRediscoveryAfterRestart(t *testing.T) {
	store := newFakeStore(8)
	adapter := newFakeAdapter()
	s1 := startScheduler(t, store, adapter, 2)
	poll(t, func() bool { return !store.isStarting(8) }, "id 8 transitioned")
	s1.Shutdown(context.Background())

	// Re-add id 8 to StartingRunners and start a fresh scheduler; the immediate
	// discovery reconstructs the in-memory state from the DB row.
	store.addStarting(8)
	s2 := startScheduler(t, store, adapter, 2)
	defer s2.Shutdown(context.Background())
	poll(t, func() bool { return !store.isStarting(8) }, "id 8 rediscovered and transitioned after restart")
}

func TestDedupAcrossReadyDelayedInflight(t *testing.T) {
	cfg := fastCfg(1)
	store := newFakeStore(1, 2)
	adapter := newFakeAdapter()
	adapter.setOutcome(1, scheduler.RunnerStartResult{Outcome: scheduler.RunnerStartTransient}) // id 1 -> delayed
	gate := adapter.makeSlow(2)                                                                 // id 2 occupies the single worker
	s, err := NewScheduler(store, adapter, cfg)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	s.Start()
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return contains(s.Snapshot().Delayed, 1) && contains(s.Snapshot().Inflight, 2) }, "id 1 delayed, id 2 in-flight")
	// id 1 must appear in exactly one set (delayed), not ready or in-flight.
	snap := s.Snapshot()
	if contains(snap.Ready, 1) || contains(snap.Inflight, 1) {
		t.Fatalf("id 1 appears in multiple sets: %+v", snap)
	}
	close(gate)
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// ---- S06-M3 scenario-observability logging ----

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

// TestRunnerStartLogsCreationRecord asserts the worker emits exactly one
// creation record when the adapter creates the deterministic Job, carrying the
// required scenario fields and no computed-reps count.
func TestRunnerStartLogsCreationRecord(t *testing.T) {
	store := newFakeStore(11)
	store.projections[11] = &Projection{
		ID: 11, TranslationAttempt: 3, NumberOfReps: 8,
		ContainerImage:   "reg.example.com/runner@sha256:" + repeat("a", 64),
		ProjectNamespace: "team-a", ProjectName: "beam-exp",
	}
	adapter := newFakeAdapter()
	rec := &eventlog.Recorder{}
	s := startSchedulerWithLogger(t, store, adapter, 1, rec)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isStarting(11) }, "scenario 11 leaves StartingRunners")
	poll(t, func() bool { return len(rec.Records()) == 1 }, "one creation record")

	got := rec.Records()
	if got[0].Event != eventlog.EventCreate {
		t.Fatalf("event = %s, want create", got[0].Event)
	}
	if got[0].Namespace != "team-a" || got[0].Experiment != "beam-exp" {
		t.Errorf("namespace/experiment = %q/%q", got[0].Namespace, got[0].Experiment)
	}
	if got[0].ScenarioID != 11 || got[0].Attempt != 3 {
		t.Errorf("scenarioID/attempt = %d/%d", got[0].ScenarioID, got[0].Attempt)
	}
	if got[0].JobName != "job-s11" {
		t.Errorf("jobName = %q", got[0].JobName)
	}
	if got[0].RequestedReps != 8 || got[0].ComputedReps != 0 {
		t.Errorf("requested/computed = %d/%d", got[0].RequestedReps, got[0].ComputedReps)
	}
	if got[0].Outcome != scheduler.RunnerStartCreated.String() {
		t.Errorf("outcome = %q", got[0].Outcome)
	}
	if got[0].Reason != "" {
		t.Errorf("creation record reason should be empty, got %q", got[0].Reason)
	}
}

// TestRunnerStartLogsAdoptionRecord asserts a Confirmed (AlreadyExists recovery)
// outcome emits exactly one adoption record.
func TestRunnerStartLogsAdoptionRecord(t *testing.T) {
	store := newFakeStore(4)
	adapter := newFakeAdapter()
	adapter.setOutcome(4, scheduler.RunnerStartResult{
		Outcome: scheduler.RunnerStartConfirmed,
		JobName: "simrunner-deadbeef-4-1",
	})
	rec := &eventlog.Recorder{}
	s := startSchedulerWithLogger(t, store, adapter, 1, rec)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isStarting(4) }, "scenario 4 leaves StartingRunners")
	poll(t, func() bool { return len(rec.Records()) == 1 }, "one adoption record")

	got := rec.Records()
	if got[0].Event != eventlog.EventAdopt {
		t.Fatalf("event = %s, want adopt", got[0].Event)
	}
	if got[0].JobName != "simrunner-deadbeef-4-1" || got[0].Outcome != scheduler.RunnerStartConfirmed.String() {
		t.Errorf("record mismatch: %+v", got[0])
	}
}

// TestRunnerStartLogsTerminalFailRecord asserts a permanent startup failure
// (Forbidden) emits exactly one terminal fail record carrying a short reason
// and no job created (JobName may be present but no UID transition).
func TestRunnerStartLogsTerminalFailRecord(t *testing.T) {
	store := newFakeStore(9)
	adapter := newFakeAdapter()
	adapter.setOutcome(9, scheduler.RunnerStartResult{
		Outcome: scheduler.RunnerStartForbidden,
		JobName: "job-s9",
		Err:     errors.New("jobs.create is forbidden"),
	})
	rec := &eventlog.Recorder{}
	s := startSchedulerWithLogger(t, store, adapter, 1, rec)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isStarting(9) }, "scenario 9 leaves StartingRunners")
	poll(t, func() bool { return len(rec.Records()) == 1 }, "one fail record")

	got := rec.Records()
	if got[0].Event != eventlog.EventFail {
		t.Fatalf("event = %s, want fail", got[0].Event)
	}
	if got[0].Outcome != scheduler.RunnerStartForbidden.String() {
		t.Errorf("outcome = %q", got[0].Outcome)
	}
	if got[0].Reason != "jobs.create is forbidden" {
		t.Errorf("reason = %q", got[0].Reason)
	}
	if got[0].ComputedReps != 0 {
		t.Errorf("computed reps = %d, want 0", got[0].ComputedReps)
	}
}

// TestRunnerStartNoLogOnExperimentTerminal asserts an ExperimentTerminal
// outcome (no Job created, no state transition) emits no record.
func TestRunnerStartNoLogOnExperimentTerminal(t *testing.T) {
	store := newFakeStore(2)
	adapter := newFakeAdapter()
	adapter.setOutcome(2, scheduler.RunnerStartResult{Outcome: scheduler.RunnerStartExperimentTerminal})
	rec := &eventlog.Recorder{}
	s := startSchedulerWithLogger(t, store, adapter, 1, rec)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return adapter.startCount() >= 1 }, "start called")
	// The scenario stays StartingRunners (no transition) and no record is
	// emitted. Give the scheduler a moment to settle.
	time.Sleep(50 * time.Millisecond)
	if got := rec.Records(); len(got) != 0 {
		t.Fatalf("ExperimentTerminal should emit no record, got %d: %+v", len(got), got)
	}
}

// TestRunnerStartNoLogOnGateRace asserts a Created outcome that loses the
// guarded transition to lifecycle-gate closure (zero rows) emits NO creation
// record: the experiment terminal action owns the scenario's terminal
// outcome, and the created Job is cleaned up rather than becoming the live
// runner.
func TestRunnerStartNoLogOnGateRace(t *testing.T) {
	store := newFakeStore(6)
	store.gateClosedInProc = map[int]bool{6: true} // MarkInProcessing returns false (zero rows)
	adapter := newFakeAdapter()                    // default Created
	rec := &eventlog.Recorder{}
	s := startSchedulerWithLogger(t, store, adapter, 1, rec)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isStarting(6) }, "scenario 6 leaves StartingRunners")
	poll(t, func() bool { return adapter.deleteCount() >= 1 }, "gate-race cleanup delete")
	time.Sleep(40 * time.Millisecond)
	if got := rec.Records(); len(got) != 0 {
		t.Fatalf("gate-race Created should emit no record, got %d: %+v", len(got), got)
	}
}

// TestRunnerStartNoLogOnStaleFail asserts a permanent failure whose guarded
// StartingRunners -> Failed transition loses (zero rows, another path moved the
// row) emits NO terminal fail record.
func TestRunnerStartNoLogOnStaleFail(t *testing.T) {
	store := newFakeStore(13)
	adapter := newFakeAdapter()
	adapter.setOutcome(13, scheduler.RunnerStartResult{
		Outcome: scheduler.RunnerStartForbidden,
		JobName: "job-s13",
		Err:     errors.New("forbidden"),
	})
	// Close the gate during Start (before MarkFailed) so the guarded transition
	// is zero-row. Set the hook before Start to avoid racing the immediate first
	// discovery.
	adapter.onStart = func(req scheduler.RunnerStartRequest) {
		store.markClosed(req.ScenarioID) // row no longer StartingRunners -> MarkFailed zero-row
	}
	rec := &eventlog.Recorder{}
	s := startSchedulerWithLogger(t, store, adapter, 1, rec)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return adapter.startCount() >= 1 }, "start called")
	time.Sleep(40 * time.Millisecond)
	if got := rec.Records(); len(got) != 0 {
		t.Fatalf("stale MarkFailed should emit no record, got %d: %+v", len(got), got)
	}
}

// TestRunnerStartLogsExactlyOnceOnTransientRetry asserts that a transient
// MarkInProcessing transport failure (no record) followed by a successful
// retry emits exactly one creation record, not one per attempt.
func TestRunnerStartLogsExactlyOnceOnTransientRetry(t *testing.T) {
	store := newFakeStore(7)
	store.markInProcFailNext(7, 1) // first MarkInProcessing errors, then succeeds
	adapter := newFakeAdapter()    // default Created on every attempt
	rec := &eventlog.Recorder{}
	s := startSchedulerWithLogger(t, store, adapter, 1, rec)
	defer s.Shutdown(context.Background())

	poll(t, func() bool { return !store.isStarting(7) }, "scenario 7 leaves StartingRunners")
	poll(t, func() bool { return len(store.markInProcCalls) >= 2 }, "two MarkInProcessing attempts")
	poll(t, func() bool { return len(rec.Records()) == 1 }, "exactly one creation record")

	got := rec.Records()
	if got[0].Event != eventlog.EventCreate {
		t.Fatalf("event = %s, want create", got[0].Event)
	}
	if got[0].JobName != "job-s7" {
		t.Errorf("jobName = %q", got[0].JobName)
	}
}
