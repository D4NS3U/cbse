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

// Package aggregate implements the scheduler-cadence scenario-aggregate
// verdict pass of the Scenario Manager. Per tick it enumerates the
// cluster-wide SimulationExperiments (the same namespace scope as the
// informer's watch), gates each through lifecycle.AdmitExperiment, and for
// every admitted, not-yet-reported experiment aggregates the scenario-state
// counts over the Core DB and reports the verdict through the status
// subresource.
//
// Field ownership is the contract: the status merge patch carries ONLY
// scenarioManagerVerdict - never phase, never message. The verdict is
// absorbing (write-if-absent): an experiment whose field is already set is
// skipped, so the pass is idempotent and the field never changes once
// written. Per-experiment failures are logged in the established operation=
// style and skipped; the next tick retries (self-healing). The pass holds no
// queue, row lock, leader flag, or cross-replica coordination state; the
// absorbing field and the fixed cadence are the durability mechanism.
package aggregate

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/lifecycle"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
)

// Operation names for the pass's established operation= log lines.
const (
	opPass          = "alpha4_scenario_aggregate_pass"
	opProjectLookup = "alpha4_aggregate_project_lookup"
	opAggregate     = "alpha4_aggregate_scenario_counts"
	opVerdictPatch  = "alpha4_aggregate_verdict_patch"
)

// Config is the aggregate scheduler configuration. Defaults are applied via
// withDefaults; the startup wiring uses the production defaults.
type Config struct {
	// Interval is the fixed cadence between ticks.
	Interval time.Duration
	// TickTimeout bounds one tick so no single database or Kubernetes call
	// can hold the pass indefinitely.
	TickTimeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = 5 * time.Second
	}
	if c.TickTimeout <= 0 {
		c.TickTimeout = 30 * time.Second
	}
	return c
}

// Scheduler is the bounded scheduler-cadence aggregate verdict pass. One tick
// runs one serial, idempotent pass over every experiment; ticks never overlap
// and no tick spins (the fixed cadence is the only work driver).
type Scheduler struct {
	store Store
	kube  Kube
	cfg   Config

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewScheduler constructs an aggregate scheduler. store and kube must be
// non-nil.
func NewScheduler(store Store, kube Kube, cfg Config) (*Scheduler, error) {
	if store == nil {
		return nil, fmt.Errorf("aggregate store must not be nil")
	}
	if kube == nil {
		return nil, fmt.Errorf("aggregate kube must not be nil")
	}
	cfg = cfg.withDefaults()
	return &Scheduler{
		store:  store,
		kube:   kube,
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}, nil
}

// Start launches the single pass goroutine. It returns immediately. The first
// tick runs immediately; subsequent ticks run every Interval.
func (s *Scheduler) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run()
	}()
}

// run executes the fixed-cadence loop: an immediate first tick, then one tick
// per interval until Shutdown closes stopCh. A tick blocks the loop, so a
// slow tick only delays the next one - it never creates a backlog or a hot
// loop.
func (s *Scheduler) run() {
	s.tick(context.Background())
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.tick(context.Background())
		}
	}
}

// Shutdown stops the pass and joins its goroutine. It blocks until the pass
// has exited or ctx expires. An in-flight tick runs to completion (bounded by
// TickTimeout) before the goroutine observes the stop.
func (s *Scheduler) Shutdown(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.stopCh) })

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("aggregate scheduler shutdown timed out")
	}
}

// tick runs one aggregate pass over every experiment. A list failure is a
// transient dependency failure: it is logged and the next tick retries (no
// state change). Per-experiment failures are handled inside
// processExperiment; a tick deadline ends the remaining work for this tick.
func (s *Scheduler) tick(root context.Context) {
	tickCtx, cancel := context.WithTimeout(root, s.cfg.TickTimeout)
	defer cancel()

	exps, err := s.kube.ListExperiments(tickCtx)
	if err != nil {
		log.Printf("operation=%q error_class=%q error=%v", opPass, "dependency", err)
		return
	}
	for i := range exps {
		if tickCtx.Err() != nil {
			return
		}
		s.processExperiment(tickCtx, &exps[i])
	}
}

// processExperiment applies the D3 aggregation rule to one experiment and,
// when a verdict is due, reports it through the status subresource. It never
// fails the pass: every step's error is logged in the established operation=
// style and the experiment is skipped; the next tick retries (self-healing).
func (s *Scheduler) processExperiment(ctx context.Context, exp *experimentalpha4.SimulationExperiment) {
	// Gate: only admitted experiments aggregate. Pending/Provisioning skip
	// (unavailable), terminal phases skip, an unknown phase conservatively
	// skips, and a deleting experiment skips.
	if decision := lifecycle.AdmitExperiment(exp); decision != lifecycle.Admit {
		return
	}
	// Absorbing verdict (write-if-absent): once reported, the field never
	// changes, so an already-set verdict is a no-op.
	if exp.Status.ScenarioManagerVerdict != "" {
		return
	}

	projectID, err := s.store.ProjectIDByNamespaceAndName(ctx, exp.Namespace, exp.Name)
	if err != nil {
		if errors.Is(err, persistence.ErrProjectNotFound) {
			// Not yet registered (or the row was deleted): a no-op for this
			// tick.
			log.Printf("operation=%q namespace=%s project=%s error_class=%q", opProjectLookup, exp.Namespace, exp.Name, "not-found")
			return
		}
		log.Printf("operation=%q namespace=%s project=%s error_class=%q error=%v", opProjectLookup, exp.Namespace, exp.Name, "dependency", err)
		return
	}

	counts, err := s.store.ScenarioStateCounts(ctx, projectID)
	if err != nil {
		log.Printf("operation=%q project_id=%d error_class=%q error=%v", opAggregate, projectID, "dependency", err)
		return
	}

	verdict := verdictForCounts(counts)
	if verdict == "" {
		// The experiment has no scenarios yet, or not every scenario is
		// terminal: no verdict this tick.
		return
	}

	if err := s.kube.PatchVerdict(ctx, exp.Namespace, exp.Name, verdict); err != nil {
		// The verdict stays unreported; the next tick re-derives and retries.
		log.Printf("operation=%q namespace=%s project=%s verdict=%s error_class=%q error=%v", opVerdictPatch, exp.Namespace, exp.Name, verdict, "dependency", err)
		return
	}
	log.Printf("operation=%q namespace=%s project=%s verdict=%s scenarios_total=%d", opVerdictPatch, exp.Namespace, exp.Name, verdict, counts.Total)
}

// verdictForCounts applies the D3 aggregation rule over the scenario-state
// counts: any scenario Failed fails fast (the experiment's failure is decided
// by the first failing scenario); else a non-empty set of all-Finished
// scenarios finishes; otherwise no verdict. The two written values are
// mutually exclusive and each absorbing, so the field stays monotone.
func verdictForCounts(c persistence.ScenarioStateCounts) string {
	if c.Failed > 0 {
		return lifecycle.PhaseFailed
	}
	if c.Total > 0 && c.Finished == c.Total {
		return lifecycle.PhaseFinished
	}
	return ""
}
