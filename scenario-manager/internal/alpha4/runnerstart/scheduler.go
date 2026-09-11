package runnerstart

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/eventlog"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/scheduler"
)

// delayEntry is a process-local delayed ID. cleanup != nil means a gate-race
// cleanup retry (DeleteCreated only); cleanup == nil means a transient
// runner-start failure that re-runs the full workflow when nextEligibleAt
// expires.
type delayEntry struct {
	nextEligibleAt time.Time
	cleanup        *cleanupWork
}

// Scheduler is the bounded ordered runner-start scheduler. It owns the
// process-local ready, delayed, and in-flight sets keyed by positive
// scenario-status ID. All set mutation happens in the single coordinator
// goroutine; workers only reconcile one ID at a time and report the outcome.
type Scheduler struct {
	store   Store
	adapter scheduler.RunnerStartAdapter
	cfg     Config
	log     eventlog.Logger

	mu       sync.Mutex
	ready    map[int]*dispatch // eligible, ordered by ascending ID at dispatch
	delayed  map[int]*delayEntry
	inflight map[int]struct{}

	workerChans []chan dispatch
	free        []int // indices of workers waiting for work
	doneCh      chan workerResult

	stopCh      chan struct{}
	stopOnce    sync.Once
	coordCtx    context.Context
	cancelCoord context.CancelFunc

	discoveryTicker *time.Ticker
	delayedTimer    *time.Timer
	shuttingDown    bool

	wgWorkers sync.WaitGroup
	wgCoord   sync.WaitGroup
}

// NewScheduler constructs a scheduler. Workers must be in [1,64]; the startup
// configuration package performs the env-parse and SM-startup fatal check, and
// this constructor rejects out-of-range values defensively.
func NewScheduler(store Store, adapter scheduler.RunnerStartAdapter, cfg Config) (*Scheduler, error) {
	if err := validateWorkers(cfg.Workers); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	s := &Scheduler{
		store:    store,
		adapter:  adapter,
		cfg:      cfg,
		log:      eventlog.NopLogger{},
		ready:    make(map[int]*dispatch),
		delayed:  make(map[int]*delayEntry),
		inflight: make(map[int]struct{}),
		doneCh:   make(chan workerResult, cfg.Workers),
		stopCh:   make(chan struct{}),
	}
	s.workerChans = make([]chan dispatch, cfg.Workers)
	for i := range s.workerChans {
		s.workerChans[i] = make(chan dispatch, 1)
	}
	return s, nil
}

// SetLogger sets the scenario-observability logger. It must be called before
// Start. If unset, the scheduler discards records.
func (s *Scheduler) SetLogger(l eventlog.Logger) {
	if l != nil {
		s.log = l
	}
}

// Start launches the coordinator and the configured number of reconciler
// goroutines. It returns immediately. The first discovery runs immediately;
// subsequent discoveries run every DiscoveryInterval.
func (s *Scheduler) Start() {
	s.coordCtx, s.cancelCoord = context.WithCancel(context.Background())
	s.discoveryTicker = time.NewTicker(s.cfg.DiscoveryInterval)
	s.delayedTimer = time.NewTimer(0)
	if !s.delayedTimer.Stop() {
		<-s.delayedTimer.C
	}
	s.wgWorkers.Add(s.cfg.Workers)
	for i := 0; i < s.cfg.Workers; i++ {
		go s.worker(i)
	}
	s.mu.Lock()
	s.free = make([]int, s.cfg.Workers)
	for i := range s.free {
		s.free[i] = i
	}
	s.mu.Unlock()
	s.wgCoord.Add(1)
	go s.run()
}

// run is the coordinator loop. It owns all set mutation and dispatches the
// lowest currently eligible ID to a free reconciler whenever one is available.
func (s *Scheduler) run() {
	defer s.wgCoord.Done()
	// Immediate first discovery.
	s.mu.Lock()
	s.discoverLocked(s.coordCtx)
	s.dispatchLocked()
	s.mu.Unlock()

	discC := s.discoveryTicker.C
	delayC := s.delayedTimer.C
	for {
		s.mu.Lock()
		if s.shuttingDown {
			done := len(s.inflight) == 0
			s.mu.Unlock()
			if done {
				s.closeWorkers()
				return
			}
			// Drain in-flight completions; do not dispatch or re-delay.
			r := <-s.doneCh
			s.mu.Lock()
			delete(s.inflight, r.scenarioID)
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()

		select {
		case <-discC:
			s.mu.Lock()
			s.discoverLocked(s.coordCtx)
			s.expireDelayedLocked(time.Now())
			s.dispatchLocked()
			s.mu.Unlock()
		case now := <-delayC:
			s.mu.Lock()
			s.expireDelayedLocked(now)
			s.dispatchLocked()
			s.mu.Unlock()
		case r := <-s.doneCh:
			s.mu.Lock()
			s.handleDone(r)
			s.dispatchLocked()
			s.mu.Unlock()
		case <-s.stopCh:
			s.mu.Lock()
			s.shuttingDown = true
			// Cancel queued and delayed work; in-flight workers self-detect and
			// their fail-fast completions are drained above.
			s.ready = make(map[int]*dispatch)
			s.delayed = make(map[int]*delayEntry)
			s.free = s.free[:0]
			s.discoveryTicker.Stop()
			s.delayedTimer.Stop()
			discC = nil
			delayC = nil
			s.cancelCoord()
			s.mu.Unlock()
		}
	}
}

// closeWorkers closes every worker channel so idle reconcilers exit. It is
// called once after all in-flight work has drained.
func (s *Scheduler) closeWorkers() {
	for i := range s.workerChans {
		close(s.workerChans[i])
	}
}

// discoverLocked queries StartingRunners, prunes ready and non-cleanup delayed
// IDs that are no longer StartingRunners (lifecycle-gate closure or a move by
// another replica), and adds newly discovered IDs to ready. It must be called
// with s.mu held.
func (s *Scheduler) discoverLocked(ctx context.Context) {
	ids, err := s.store.ListStartingRunners(ctx)
	if err != nil {
		// Transient discovery failure: retry on the next tick. No state change.
		return
	}
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		seen[id] = struct{}{}
	}
	// Prune ready IDs no longer StartingRunners, except gate-race cleanup
	// retries (whose scenario is no longer StartingRunners by construction).
	for id, d := range s.ready {
		if d.cleanup != nil {
			continue
		}
		if _, ok := seen[id]; !ok {
			delete(s.ready, id)
		}
	}
	// Prune non-cleanup delayed IDs no longer StartingRunners. Cleanup-mode
	// delayed IDs are retained until their delete succeeds.
	for id, e := range s.delayed {
		if e.cleanup == nil {
			if _, ok := seen[id]; !ok {
				delete(s.delayed, id)
			}
		}
	}
	// Add newly discovered IDs (de-duplicated across all sets).
	for _, id := range ids {
		if _, ok := s.ready[id]; ok {
			continue
		}
		if _, ok := s.delayed[id]; ok {
			continue
		}
		if _, ok := s.inflight[id]; ok {
			continue
		}
		s.ready[id] = &dispatch{scenarioID: id}
	}
}

// expireDelayedLocked moves delayed entries whose nextEligibleAt is at or
// before now back into ready so they regain their ascending-ID position. A
// cleanup-mode entry carries its cleanup work into ready so the same dispatch
// path delivers it to a free reconciler. It must be called with s.mu held.
func (s *Scheduler) expireDelayedLocked(now time.Time) {
	for id, e := range s.delayed {
		if now.Before(e.nextEligibleAt) {
			continue
		}
		delete(s.delayed, id)
		s.ready[id] = &dispatch{scenarioID: id, cleanup: e.cleanup}
	}
	s.resetDelayedLocked(now)
}

// dispatchLocked fills free reconcilers with the lowest eligible ready IDs. It
// must be called with s.mu held.
func (s *Scheduler) dispatchLocked() {
	for len(s.free) > 0 && len(s.ready) > 0 {
		id := s.lowestReadyLocked()
		d := s.ready[id]
		delete(s.ready, id)
		s.inflight[id] = struct{}{}
		w := s.free[len(s.free)-1]
		s.free = s.free[:len(s.free)-1]
		s.workerChans[w] <- *d
	}
}

// lowestReadyLocked returns the lowest ready ID. The ready set must be
// non-empty.
func (s *Scheduler) lowestReadyLocked() int {
	ids := make([]int, 0, len(s.ready))
	for id := range s.ready {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids[0]
}

// handleDone applies a worker's outcome while running. It must be called with
// s.mu held.
func (s *Scheduler) handleDone(r workerResult) {
	delete(s.inflight, r.scenarioID)
	switch r.action {
	case actRemove:
		// the ID is dropped
	case actDelayStart:
		s.delayed[r.scenarioID] = &delayEntry{nextEligibleAt: time.Now().Add(s.cfg.TransientDelay)}
	case actDelayCleanup:
		s.delayed[r.scenarioID] = &delayEntry{nextEligibleAt: time.Now().Add(s.cfg.TransientDelay), cleanup: r.cleanup}
	}
	s.free = append(s.free, r.worker)
	s.resetDelayedLocked(time.Now())
}

// resetDelayedLocked resets the single delayed timer to the earliest
// nextEligibleAt, or stops it if no delayed entries remain. The timer channel
// is fixed for the timer's lifetime and read by the coordinator through the
// delayC closure variable. It must be called with s.mu held and is
// coordinator-only, so there is no race with the select.
func (s *Scheduler) resetDelayedLocked(now time.Time) {
	earliest := time.Time{}
	for _, e := range s.delayed {
		if earliest.IsZero() || e.nextEligibleAt.Before(earliest) {
			earliest = e.nextEligibleAt
		}
	}
	if earliest.IsZero() {
		if !s.delayedTimer.Stop() {
			select {
			case <-s.delayedTimer.C:
			default:
			}
		}
		return
	}
	d := earliest.Sub(now)
	if d < 0 {
		d = 0
	}
	s.delayedTimer.Reset(d)
}

// worker is one reconciler goroutine. It reconciles one dispatch at a time and
// reports the outcome to the coordinator. On shutdown (coordCtx cancelled) it
// finishes its in-flight reconcile (fail-fast) and exits.
func (s *Scheduler) worker(idx int) {
	defer s.wgWorkers.Done()
	for {
		select {
		case d, ok := <-s.workerChans[idx]:
			if !ok {
				return
			}
			res := s.reconcile(s.coordCtx, d)
			res.worker = idx
			// Always deliver the result so the coordinator can account for the
			// in-flight slot. The coordinator drains doneCh during shutdown.
			s.doneCh <- res
		case <-s.coordCtx.Done():
			// Shutdown raced a buffered dispatch: the coordinator already recorded
			// the slot as in-flight, so drain the buffered dispatch (fail-fast via
			// the cancelled ctx) and deliver its result before exiting. Otherwise
			// the coordinator would wait forever for a done that never comes.
			select {
			case d, ok := <-s.workerChans[idx]:
				if !ok {
					return
				}
				res := s.reconcile(s.coordCtx, d)
				res.worker = idx
				s.doneCh <- res
			default:
				return
			}
		}
	}
}

// Shutdown stops discovery, cancels all ready, delayed, and in-flight work, and
// joins every reconciler goroutine. It blocks until the coordinator and all
// workers have exited or ctx expires.
func (s *Scheduler) Shutdown(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.stopCh) })

	done := make(chan struct{})
	go func() {
		s.wgCoord.Wait()
		s.wgWorkers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("runner-start scheduler shutdown timed out")
	}
}

// SchedulerSnapshot is a point-in-time view of the scheduler's process-local
// sets, for observability and tests.
type SchedulerSnapshot struct {
	Ready    []int
	Delayed  []int
	Inflight []int
}

// Snapshot returns a copy of the ready, delayed, and in-flight ID sets.
func (s *Scheduler) Snapshot() SchedulerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SchedulerSnapshot{
		Ready:    sortedKeysReady(s.ready),
		Delayed:  sortedKeysDelayed(s.delayed),
		Inflight: sortedKeysInflight(s.inflight),
	}
}

func sortedKeysReady(m map[int]*dispatch) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
func sortedKeysDelayed(m map[int]*delayEntry) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
func sortedKeysInflight(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
