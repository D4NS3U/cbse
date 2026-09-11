package observation

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/eventlog"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/scheduler"
)

// Scheduler is the per-scenario deduplicated five-second observation queue. It
// discovers every InProcessing scenario immediately after startup and then on
// a fixed discovery interval (five seconds in production), adds newly eligible
// keys to one de-duplicating FIFO work queue, and reconciles up to Workers
// keys concurrently. A small process-local keyed state records whether a key is
// queued or in flight and the first discovery tick at which it is eligible
// again; after an observation completes the key is eligible only at the first
// strictly subsequent tick. All set mutation happens in the single coordinator
// goroutine.
type Scheduler struct {
	store   Store
	adapter scheduler.ObservationAdapter
	cfg     Config
	log     eventlog.Logger

	mu sync.Mutex

	queue       []int // FIFO of eligible IDs waiting for a free worker
	queued      map[int]struct{}
	inflight    map[int]struct{}
	eligibleAt  map[int]int // first tick at which the ID is eligible again; absent = eligible now
	currentTick int

	workerChans []chan int
	free        []int
	doneCh      chan workerResult

	stopCh      chan struct{}
	stopOnce    sync.Once
	coordCtx    context.Context
	cancelCoord context.CancelFunc

	discoveryTicker *time.Ticker
	shuttingDown    bool

	wgWorkers sync.WaitGroup
	wgCoord   sync.WaitGroup
}

// NewScheduler constructs an observation scheduler. Workers must be in [1,64].
func NewScheduler(store Store, adapter scheduler.ObservationAdapter, cfg Config) (*Scheduler, error) {
	if err := validateWorkers(cfg.Workers); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	s := &Scheduler{
		store:      store,
		adapter:    adapter,
		cfg:        cfg,
		log:        eventlog.NopLogger{},
		queued:     make(map[int]struct{}),
		inflight:   make(map[int]struct{}),
		eligibleAt: make(map[int]int),
		doneCh:     make(chan workerResult, cfg.Workers),
		stopCh:     make(chan struct{}),
	}
	s.workerChans = make([]chan int, cfg.Workers)
	for i := range s.workerChans {
		s.workerChans[i] = make(chan int, 1)
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

// Start launches the coordinator and reconciler goroutines. The first
// discovery runs immediately; subsequent discoveries run every
// DiscoveryInterval.
func (s *Scheduler) Start() {
	s.coordCtx, s.cancelCoord = context.WithCancel(context.Background())
	s.discoveryTicker = time.NewTicker(s.cfg.DiscoveryInterval)
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

// run is the coordinator loop. It owns all set mutation.
func (s *Scheduler) run() {
	defer s.wgCoord.Done()
	// Immediate first discovery at tick 0.
	s.mu.Lock()
	s.discoverLocked(0)
	s.dispatchLocked()
	s.mu.Unlock()

	discC := s.discoveryTicker.C
	tick := 0
	for {
		s.mu.Lock()
		if s.shuttingDown {
			done := len(s.inflight) == 0
			s.mu.Unlock()
			if done {
				s.closeWorkers()
				return
			}
			r := <-s.doneCh
			s.mu.Lock()
			delete(s.inflight, r.scenarioID)
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()

		select {
		case <-discC:
			tick++
			s.mu.Lock()
			s.discoverLocked(tick)
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
			s.queue = nil
			s.queued = make(map[int]struct{})
			s.eligibleAt = make(map[int]int)
			s.free = s.free[:0]
			s.discoveryTicker.Stop()
			discC = nil
			s.cancelCoord()
			s.mu.Unlock()
		}
	}
}

func (s *Scheduler) closeWorkers() {
	for i := range s.workerChans {
		close(s.workerChans[i])
	}
}

// discoverLocked lists InProcessing rows, prunes process-local state for keys
// that left InProcessing, and adds newly eligible keys to the FIFO queue. A
// tick never adds a queued or in-flight key, and after an observation completes
// a key is eligible only at the first strictly subsequent tick. It must be
// called with s.mu held.
func (s *Scheduler) discoverLocked(tick int) {
	s.currentTick = tick
	ids, err := s.store.ListInProcessing(s.coordCtx)
	if err != nil {
		// Transient discovery failure: retry on the next tick. No state change.
		return
	}
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		seen[id] = struct{}{}
	}
	// Prune eligibleAt for keys that left InProcessing and are not in flight.
	for id := range s.eligibleAt {
		if _, ok := seen[id]; !ok {
			if _, inflight := s.inflight[id]; !inflight {
				delete(s.eligibleAt, id)
			}
		}
	}
	// Add newly eligible keys in ascending discovery order (de-duplicated).
	for _, id := range ids {
		if _, q := s.queued[id]; q {
			continue
		}
		if _, inf := s.inflight[id]; inf {
			continue
		}
		if elig, ok := s.eligibleAt[id]; ok && elig > tick {
			// Not yet eligible: re-observe only at a strictly subsequent tick.
			continue
		}
		s.queue = append(s.queue, id)
		s.queued[id] = struct{}{}
	}
}

// dispatchLocked fills free reconcilers from the front of the FIFO queue. It
// must be called with s.mu held.
func (s *Scheduler) dispatchLocked() {
	for len(s.free) > 0 && len(s.queue) > 0 {
		id := s.queue[0]
		s.queue = s.queue[1:]
		delete(s.queued, id)
		s.inflight[id] = struct{}{}
		w := s.free[len(s.free)-1]
		s.free = s.free[:len(s.free)-1]
		s.workerChans[w] <- id
	}
}

// handleDone applies a worker's outcome while running. It must be called with
// s.mu held.
func (s *Scheduler) handleDone(r workerResult) {
	delete(s.inflight, r.scenarioID)
	switch r.action {
	case actRequeue:
		// Eligible again only at the first strictly subsequent tick.
		s.eligibleAt[r.scenarioID] = s.currentTick + 1
	case actRemove:
		delete(s.eligibleAt, r.scenarioID)
	}
	s.free = append(s.free, r.worker)
}

// worker is one reconciler goroutine. It observes one scenario key at a time
// and reports the outcome to the coordinator.
func (s *Scheduler) worker(idx int) {
	defer s.wgWorkers.Done()
	for {
		select {
		case id, ok := <-s.workerChans[idx]:
			if !ok {
				return
			}
			res := s.reconcile(s.coordCtx, id)
			res.worker = idx
			// Always deliver the result so the coordinator can account for the
			// in-flight slot. The coordinator drains doneCh during shutdown.
			s.doneCh <- res
		case <-s.coordCtx.Done():
			// Shutdown raced a buffered dispatch: the coordinator already
			// recorded the slot as in-flight, so drain the buffered dispatch
			// (fail-fast via the cancelled ctx) and deliver its result before
			// exiting.
			select {
			case id, ok := <-s.workerChans[idx]:
				if !ok {
					return
				}
				res := s.reconcile(s.coordCtx, id)
				res.worker = idx
				s.doneCh <- res
			default:
				return
			}
		}
	}
}

// Shutdown stops discovery, cancels all queued and in-flight work, and joins
// every reconciler goroutine. It blocks until the coordinator and all workers
// have exited or ctx expires.
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
		return errors.New("observation scheduler shutdown timed out")
	}
}

// SchedulerSnapshot is a point-in-time view of the scheduler's process-local
// state, for observability and tests.
type SchedulerSnapshot struct {
	Queue    []int
	Inflight []int
	Delayed  []int // IDs with a future eligibleAt (not eligible this tick)
}

// Snapshot returns a copy of the queued, in-flight, and delayed-eligible IDs.
func (s *Scheduler) Snapshot() SchedulerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := SchedulerSnapshot{
		Queue:    append([]int(nil), s.queue...),
		Inflight: sortedKeys(s.inflight),
	}
	for id, elig := range s.eligibleAt {
		if _, inf := s.inflight[id]; inf {
			continue
		}
		if _, q := s.queued[id]; q {
			continue
		}
		if elig > s.currentTick {
			snap.Delayed = append(snap.Delayed, id)
		}
	}
	sortInts(snap.Delayed)
	return snap
}

func sortedKeys(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortInts(out)
	return out
}

func sortInts(s []int) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
