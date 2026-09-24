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

// Package informer implements the alpha4 SimulationExperiment informer and
// lifecycle dispatch: a cluster-wide controller-runtime cache over the alpha4
// SimulationExperiment with add/update handlers that re-get the live object,
// require the namespace/name/UID to match the event, close the per-incarnation
// lifecycle gate, and dispatch lifecycle.RunTerminalAction,
// lifecycle.RunCompletedAction, or lifecycle.RunDeletionCleanup. Failed
// terminal or deletion-cleanup actions retry on the fixed five-second
// lifecycle.RetryCadence with no backoff or limit; one delayed action cannot
// block another experiment. The informer does not write
// SimulationExperiment.status.
//
// The dispatch logic (this file) is decoupled from controller-runtime so it
// can be unit-tested with a fake client. The cache wiring (informer.go) binds a
// controller-runtime cache to the Dispatcher.
package informer

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/lifecycle"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// retryCadence is the fixed delay between failed terminal/cleanup action
// retries. It mirrors lifecycle.RetryCadence (5 seconds) with no backoff or
// limit.
func retryCadence() time.Duration {
	return time.Duration(lifecycle.RetryCadence) * time.Second
}

// gate is the per-(namespace/name) incarnation retry coordinator. A non-nil
// cancel means a retry goroutine is in flight for this incarnation; closing the
// gate cancels that goroutine so a stale retry for an old incarnation cannot
// run against a replacement.
type gate struct {
	uid    string
	cancel context.CancelFunc
	done   chan struct{} // closed when the action goroutine exits
}

// Dispatcher applies the alpha4 lifecycle dispatch policy to informer events.
// It owns the per-(namespace/name) gate map and the action retry goroutines.
// The Kubernetes client, project store, messaging cleaner, project
// registration, and the two canonical JetStream stream names used by deletion
// cleanup are injected so the dispatch logic is unit-testable with fakes. The
// stream names are injected (not imported from the NATS package) so the
// transport-neutral lifecycle package has no dependency on the transport.
type Dispatcher struct {
	k8s             client.Client
	store           lifecycle.ProjectStore
	msg             lifecycle.MessagingCleaner
	registerProject func(ctx context.Context, namespace, project string) error
	cadence         time.Duration
	// edsStreamName and translatorStreamName are the canonical JetStream stream
	// names passed to lifecycle.RunDeletionCleanup for the subject-filtered
	// purges. They are populated by the production wiring (NewProductionDispatcher);
	// unit tests that do not assert purge stream values leave them zero.
	edsStreamName        string
	translatorStreamName string

	mu      sync.Mutex
	gates   map[string]*gate
	rootCtx context.Context
}

// NewDispatcher constructs a Dispatcher. rootCtx is used to derive per-action
// retry contexts so shutdown cancels every in-flight retry. It must be set
// (via SetRootContext) before any event is dispatched.
func NewDispatcher(k8s client.Client, store lifecycle.ProjectStore, msg lifecycle.MessagingCleaner, registerProject func(context.Context, string, string) error) *Dispatcher {
	return &Dispatcher{
		k8s:             k8s,
		store:           store,
		msg:             msg,
		registerProject: registerProject,
		cadence:         retryCadence(),
		gates:           make(map[string]*gate),
	}
}

// SetRetryCadence overrides the fixed retry delay. It is intended for tests
// that need to assert retry ordering without waiting five seconds; production
// uses the default lifecycle.RetryCadence.
func (d *Dispatcher) SetRetryCadence(c time.Duration) {
	if c > 0 {
		d.cadence = c
	}
}

// SetRootContext sets the context used to derive per-action retry contexts. It
// must be called once before the first event is dispatched (the cache Start
// caller supplies the informer's run context).
func (d *Dispatcher) SetRootContext(ctx context.Context) {
	d.mu.Lock()
	d.rootCtx = ctx
	d.mu.Unlock()
}

// HandleAdd processes an informer add event. For a non-deleting experiment it
// installs the SM finalizer, re-gets the object, and registers the project
// idempotently; if the re-get shows deletion has begun it runs deletion cleanup
// instead and does not register. A nil or type-mismatched object is ignored.
func (d *Dispatcher) HandleAdd(obj interface{}) {
	exp, ok := obj.(*experimentalpha4.SimulationExperiment)
	if !ok || exp == nil {
		return
	}
	ctx, cancel := d.actionContext()
	defer cancel()

	current, deleted, err := lifecycle.EnsureFinalizer(ctx, d.k8s, exp)
	if err != nil {
		log.Printf("alpha4 informer add: ensure finalizer %s/%s: %v", exp.Namespace, exp.Name, err)
		return
	}
	if deleted {
		// Deletion began between the patch and the re-get: run deletion cleanup
		// instead of registering.
		d.dispatch(current, lifecycle.ActionDeletionCleanup)
		return
	}
	if err := d.registerProject(ctx, current.Namespace, current.Name); err != nil {
		log.Printf("alpha4 informer add: register project %s/%s: %v", current.Namespace, current.Name, err)
		return
	}
}

// HandleUpdate processes an informer update event. It re-gets the current
// object and requires the namespace, name, and full UID to match the event; a
// stale event for an old UID neither closes nor cleans a replacement. Once
// accepted it closes the per-incarnation lifecycle gate synchronously and
// dispatches lifecycle.DispatchAction: deletionTimestamp takes precedence
// (RunDeletionCleanup only), Error/Failed to RunTerminalAction, Completed to
// RunCompletedAction. It does not write status.
func (d *Dispatcher) HandleUpdate(oldObj, newObj interface{}) {
	newExp, ok := newObj.(*experimentalpha4.SimulationExperiment)
	if !ok || newExp == nil {
		return
	}
	// Re-get the current object so the dispatch observes the live deletion
	// state and UID after any finalizer patch.
	current, err := d.reget(newExp)
	if err != nil {
		// NotFound means the object is gone (e.g. force-deleted without the
		// finalizer): there is nothing to close or clean. A transient lookup
		// error is logged; the next event retries.
		if err != lifecycle.ErrExperimentNotFound {
			log.Printf("alpha4 informer update: re-get %s/%s: %v", newExp.Namespace, newExp.Name, err)
		}
		return
	}
	// Require namespace, name, and full UID to match the event; a stale event
	// for an old UID cannot close or clean a replacement.
	if current.Namespace != newExp.Namespace || current.Name != newExp.Name || current.UID != newExp.UID {
		return
	}
	d.dispatch(current, lifecycle.DispatchAction(current))
}

// HandleDelete is a no-op: deletion is driven by the deletionTimestamp update
// event while the SM finalizer keeps the object readable. Removing the
// finalizer in RunDeletionCleanup is what allows the delete to complete.
func (d *Dispatcher) HandleDelete(obj interface{}) {}

// dispatch closes the per-incarnation gate for the experiment's namespace/name
// synchronously, then runs the action in a per-experiment goroutine. A failed
// terminal or deletion-cleanup action retries on the fixed cadence until
// success or gate-close; a completed action (no-op) and a none action do not
// retry. The gate is removed once the action succeeds.
func (d *Dispatcher) dispatch(exp *experimentalpha4.SimulationExperiment, kind lifecycle.ActionKind) {
	if exp == nil {
		return
	}
	key := exp.Namespace + "/" + exp.Name

	// Close the per-incarnation lifecycle gate synchronously before queuing any
	// slower action: cancel any in-flight retry for this namespace/name.
	d.closeGate(key)

	switch kind {
	case lifecycle.ActionTerminal, lifecycle.ActionDeletionCleanup:
		d.startAction(key, exp, kind)
	case lifecycle.ActionCompleted:
		// Completed only closes the gate (already closed above); the completed
		// action is a documented no-op, run synchronously without a retry.
		ctx, cancel := d.actionContext()
		defer cancel()
		if err := lifecycle.RunCompletedAction(ctx, d.k8s, d.store, exp); err != nil {
			log.Printf("alpha4 informer: completed action %s: %v", key, err)
		}
	case lifecycle.ActionNone:
		// No phase action (e.g. still InProgress, or Pending/Provisioning): the
		// gate is closed and no slower work is queued.
	}
}

// startAction runs a terminal or deletion-cleanup action in a per-experiment
// goroutine, retrying on the fixed cadence until success or gate-close. The
// gate's cancel context is the "close" handle: a subsequent event for the same
// key cancels it, interrupting an in-flight action and stopping the retry.
func (d *Dispatcher) startAction(key string, exp *experimentalpha4.SimulationExperiment, kind lifecycle.ActionKind) {
	d.mu.Lock()
	if d.rootCtx == nil {
		d.mu.Unlock()
		log.Printf("alpha4 informer: cannot start action %s: root context not set", key)
		return
	}
	ctx, cancel := context.WithCancel(d.rootCtx)
	g := &gate{uid: string(exp.UID), cancel: cancel, done: make(chan struct{})}
	d.gates[key] = g
	d.mu.Unlock()

	go func() {
		defer close(g.done)
		cadence := d.cadence
		for {
			if err := d.runOnce(ctx, exp, kind); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("alpha4 informer: action %s %s failed, retrying in %s: %v", key, kind, cadence, err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(cadence):
				}
				continue
			}
			// Success: remove this incarnation's gate if it is still ours.
			d.completeGate(key, g)
			return
		}
	}()
}

// runOnce executes one action attempt.
func (d *Dispatcher) runOnce(ctx context.Context, exp *experimentalpha4.SimulationExperiment, kind lifecycle.ActionKind) error {
	switch kind {
	case lifecycle.ActionTerminal:
		return lifecycle.RunTerminalAction(ctx, d.k8s, d.store, exp)
	case lifecycle.ActionDeletionCleanup:
		return lifecycle.RunDeletionCleanup(ctx, d.k8s, d.store, d.msg, d.edsStreamName, d.translatorStreamName, exp)
	default:
		return fmt.Errorf("unexpected action kind %s", kind)
	}
}

// closeGate cancels the in-flight retry goroutine for key and removes the gate.
func (d *Dispatcher) closeGate(key string) {
	d.mu.Lock()
	g, ok := d.gates[key]
	if ok {
		delete(d.gates, key)
	}
	d.mu.Unlock()
	if ok {
		g.cancel()
		// Wait for the action goroutine to observe cancellation so the close is
		// synchronous with respect to a new dispatch for the same key.
		<-g.done
	}
}

// completeGate removes the gate for key only if it still points at g (the
// successful action's own incarnation), so a newer event that already replaced
// the gate is not disturbed.
func (d *Dispatcher) completeGate(key string, g *gate) {
	d.mu.Lock()
	if cur, ok := d.gates[key]; ok && cur == g {
		delete(d.gates, key)
	}
	d.mu.Unlock()
	g.cancel()
}

// actionContext returns a short-lived context for synchronous (non-retry)
// operations such as EnsureFinalizer and project registration. It is derived
// from the root context so shutdown cancels it.
func (d *Dispatcher) actionContext() (context.Context, context.CancelFunc) {
	d.mu.Lock()
	root := d.rootCtx
	d.mu.Unlock()
	if root == nil {
		return context.WithCancel(context.Background())
	}
	return context.WithCancel(root)
}

// reget fetches the current object so the dispatch observes the live deletion
// state and UID. A NotFound result is returned as lifecycle.ErrExperimentNotFound.
func (d *Dispatcher) reget(exp *experimentalpha4.SimulationExperiment) (*experimentalpha4.SimulationExperiment, error) {
	ctx, cancel := d.actionContext()
	defer cancel()
	current := &experimentalpha4.SimulationExperiment{}
	if err := d.k8s.Get(ctx, client.ObjectKey{Namespace: exp.Namespace, Name: exp.Name}, current); err != nil {
		return nil, err
	}
	return current, nil
}

// Shutdown cancels every in-flight action goroutine and waits for them to exit.
// It is idempotent.
func (d *Dispatcher) Shutdown() {
	d.mu.Lock()
	keys := make([]string, 0, len(d.gates))
	for k := range d.gates {
		keys = append(keys, k)
	}
	d.mu.Unlock()
	for _, k := range keys {
		d.closeGate(k)
	}
}

// Snapshot returns the set of (key, uid) gates with in-flight actions, for
// observability and tests.
func (d *Dispatcher) Snapshot() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]string, len(d.gates))
	for k, g := range d.gates {
		out[k] = g.uid
	}
	return out
}
