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

package controller

import (
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// The bounded-retry readiness watchdog (ruling R, user-spec'd): provisioning
// does not go on indefinitely. The initial not-ready readiness evaluation
// starts the watchdog (no inventory yet) and requeues at the 5s cadence; each
// counted retry - an evaluation occurring >= ReadinessRetryInterval after the
// previous counted one, so the 5s polls in between do not consume the budget -
// collects the not-ready component inventory; and the third counted retry that
// still finds not-ready components transitions the experiment to Error without
// further retry, the final message aggregating the per-retry inventories (the
// concrete cause: the component(s) that did not become ready and their
// failures at each retry).
//
// State is in-memory, keyed by experiment UID. Restart semantics (deliberate,
// documented per ruling R): an operator restart clears the map and every
// budget restarts from scratch - no API surface change, no persisted
// deadline. Stale-entry hygiene: entries are cleared on InProgress (all
// ready), on the Error write, and on experiment deletion, and pruned when
// their last counted evaluation is older than
// readinessWatchdogStaleHorizon, so the map never grows unbounded.

// defaultReadinessRetryInterval and defaultReadinessMaxRetries are the
// production watchdog parameters (ruling R, manager-grounded): counted
// retries are spaced ~60s apart (a 3x5s budget would false-Error the green
// experiment, which readies in well under ~90s observed), and the third
// still-not-ready counted retry transitions the experiment to Error.
const (
	defaultReadinessRetryInterval = 60 * time.Second
	defaultReadinessMaxRetries    = 3
)

// readinessWatchdogStaleHorizon bounds the in-memory watchdog map: an entry
// whose last counted evaluation is older than this horizon can only belong to
// a deleted experiment - a live not-ready experiment reaches its Error within
// ReadinessMaxRetries * ReadinessRetryInterval of its last count and clears
// its entry on the Error write - so the entry is pruned opportunistically.
const readinessWatchdogStaleHorizon = time.Hour

// readinessRetryState is the in-memory bounded-retry watchdog state for one
// experiment (ruling R): the time of the last counted retry (the budget
// clock), the counted retry count, and the per-retry not-ready inventories
// collected on each counted retry.
type readinessRetryState struct {
	lastCountedAt time.Time
	retries       int
	inventories   []string
}

// readinessWatchdogTick advances the bounded-retry watchdog for one not-ready
// readiness evaluation. The first not-ready evaluation starts the watchdog
// (no inventory collected yet). A counted retry occurs only when the retry
// interval has elapsed since the previous counted one, and each counted retry
// records the not-ready inventory passed in. Once the counted retry budget is
// exhausted, the aggregated Error message (the per-retry inventories, each
// labeled by its retry) is returned so the caller transitions the experiment
// to Error without further retry; "" means the budget remains and the caller
// requeues at the 5s cadence.
func (r *Alpha4SimulationExperimentReconciler) readinessWatchdogTick(uid types.UID, inventory string) string {
	interval := r.ReadinessRetryInterval
	if interval <= 0 {
		interval = defaultReadinessRetryInterval
	}
	maxRetries := r.ReadinessMaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultReadinessMaxRetries
	}
	now := time.Now()
	r.readinessWatchdogMu.Lock()
	defer r.readinessWatchdogMu.Unlock()
	if r.readinessWatchdog == nil {
		r.readinessWatchdog = map[types.UID]*readinessRetryState{}
	}
	r.pruneStaleReadinessWatchdog(now)
	st, ok := r.readinessWatchdog[uid]
	if !ok {
		// The initial not-ready evaluation starts the watchdog: the budget
		// clock begins now, no inventory is collected yet.
		r.readinessWatchdog[uid] = &readinessRetryState{lastCountedAt: now}
		return ""
	}
	if now.Sub(st.lastCountedAt) < interval {
		// Time-gated counting: the 5s requeue polls between counted
		// retries do not consume the budget.
		return ""
	}
	st.retries++
	st.lastCountedAt = now
	st.inventories = append(st.inventories, inventory)
	if st.retries < maxRetries {
		return ""
	}
	return aggregateReadinessWatchdogMessage(maxRetries, st.inventories)
}

// pruneStaleReadinessWatchdog drops watchdog entries whose last counted
// evaluation is older than readinessWatchdogStaleHorizon. The caller holds
// readinessWatchdogMu.
func (r *Alpha4SimulationExperimentReconciler) pruneStaleReadinessWatchdog(now time.Time) {
	for uid, st := range r.readinessWatchdog {
		if now.Sub(st.lastCountedAt) > readinessWatchdogStaleHorizon {
			delete(r.readinessWatchdog, uid)
		}
	}
}

// clearReadinessWatchdog removes the watchdog state for uid. It is called on
// the InProgress transition (all components ready), on the Error write, and
// on experiment deletion, so the map is bounded by live provisioning
// experiments.
func (r *Alpha4SimulationExperimentReconciler) clearReadinessWatchdog(uid types.UID) {
	r.readinessWatchdogMu.Lock()
	defer r.readinessWatchdogMu.Unlock()
	delete(r.readinessWatchdog, uid)
}

// ReadinessWatchdogRetries reports the counted retry budget consumed so far
// for the experiment with uid and whether a watchdog entry exists (false once
// the entry was cleared on InProgress, Error, or deletion). It is the envtest
// seam for verifying the state lifecycle (start, per-retry accumulation, and
// clearing); the production reconcile path never reads it.
func (r *Alpha4SimulationExperimentReconciler) ReadinessWatchdogRetries(uid types.UID) (int, bool) {
	r.readinessWatchdogMu.Lock()
	defer r.readinessWatchdogMu.Unlock()
	st, ok := r.readinessWatchdog[uid]
	if !ok {
		return 0, false
	}
	return st.retries, true
}

// aggregateReadinessWatchdogMessage renders the final Error message: the
// per-retry not-ready inventories, each labeled by its retry (ruling R: the
// concrete cause is the component(s) that did not become ready and their
// failures at each retry).
func aggregateReadinessWatchdogMessage(maxRetries int, inventories []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "readiness watchdog: %d counted retries without all components ready; not-ready inventory per retry:\n", maxRetries)
	for i, inv := range inventories {
		fmt.Fprintf(&b, "retry %d: %s", i+1, inv)
		if i < len(inventories)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// deploymentNotReadyFailure describes a not-ready component Deployment's
// observed failure for the watchdog inventory: the ready/observed replica
// counts, plus the first False Deployment status condition's reason where
// cheaply available (read from the status already fetched - no extra API
// call).
func deploymentNotReadyFailure(dep *appsv1.Deployment) string {
	s := fmt.Sprintf("%d/%d ready", dep.Status.ReadyReplicas, dep.Status.Replicas)
	for _, cond := range dep.Status.Conditions {
		if cond.Status == corev1.ConditionFalse && cond.Reason != "" {
			return fmt.Sprintf("%s, condition %s %s", s, cond.Type, cond.Reason)
		}
	}
	return s
}
