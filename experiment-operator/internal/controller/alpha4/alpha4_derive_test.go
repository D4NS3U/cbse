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

package alpha4

import (
	"context"
	"testing"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/experiment-operator/internal/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// --- derive-path fixtures -----------------------------------------------------

// newDeriveReconciler builds an alpha4 reconciler against the shared envtest
// client with the supplied event recorder (nil uses the nil-Recorder
// discard path). The derive path never probes a database, so no DBProbe is
// injected.
func newDeriveReconciler(rec record.EventRecorder) *controller.Alpha4SimulationExperimentReconciler {
	return &controller.Alpha4SimulationExperimentReconciler{
		Client:   alpha4Client,
		Scheme:   alpha4Scheme,
		Recorder: rec,
	}
}

// setExperimentStatus applies mutate to the live experiment's status through
// the status subresource (create discards user-supplied status).
func setExperimentStatus(t *testing.T, key types.NamespacedName, mutate func(*experimentalpha4.SimulationExperimentStatus)) {
	t.Helper()
	ctx := context.Background()
	inst := &experimentalpha4.SimulationExperiment{}
	if err := alpha4Client.Get(ctx, key, inst); err != nil {
		t.Fatalf("get experiment %q: %v", key.Name, err)
	}
	mutate(&inst.Status)
	if err := alpha4Client.Status().Update(ctx, inst); err != nil {
		t.Fatalf("status update for %q: %v", key.Name, err)
	}
}

// drainEvents returns every event string the fake recorder emitted so far.
func drainEvents(fr *record.FakeRecorder) []string {
	out := []string{}
	for {
		select {
		case e := <-fr.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

// reconcileOnce reconciles the experiment once, failing the test on error.
func reconcileOnce(t *testing.T, r *controller.Alpha4SimulationExperimentReconciler, key types.NamespacedName) {
	t.Helper()
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile %q: %v", key.Name, err)
	}
}

// inProgressExperimentWithVerdict creates a valid experiment, sets its status
// to InProgress with the given (possibly empty) scenarioManagerVerdict, and
// returns its namespaced name.
func inProgressExperimentWithVerdict(t *testing.T, name, verdict string) types.NamespacedName {
	t.Helper()
	key := createExperiment(t, validExperiment(name))
	setExperimentStatus(t, key, func(st *experimentalpha4.SimulationExperimentStatus) {
		st.Phase = "InProgress"
		st.Message = "All components provisioned and ready"
		st.ScenarioManagerVerdict = verdict
	})
	return key
}

// --- tests -------------------------------------------------------------------

// TestAlpha4DeriveFinishedFromVerdictReport verifies the Finished derivation:
// a synthetic scenarioManagerVerdict "Finished" on an InProgress experiment
// transitions the phase to Finished and emits the Normal transition Event.
func TestAlpha4DeriveFinishedFromVerdictReport(t *testing.T) {
	fr := record.NewFakeRecorder(10)
	r := newDeriveReconciler(fr)
	key := inProgressExperimentWithVerdict(t, "exp-derive-finished", "Finished")

	reconcileOnce(t, r, key)

	inst := getExperiment(t, key)
	if inst.Status.Phase != "Finished" {
		t.Fatalf("phase = %q, want Finished (derived from the verdict report)", inst.Status.Phase)
	}
	if inst.Status.Message == "" {
		t.Fatalf("message empty; want a message naming the verdict report")
	}
	evts := drainEvents(fr)
	if len(evts) != 1 {
		t.Fatalf("events = %v, want exactly one Normal transition event", evts)
	}
	if evts[0] != "Normal PhaseTransition experiment phase transitioned to Finished" {
		t.Fatalf("event = %q, want the Normal PhaseTransition to Finished", evts[0])
	}
}

// TestAlpha4DeriveFailedFromVerdictReport verifies the Failed derivation: a
// synthetic scenarioManagerVerdict "Failed" on an InProgress experiment
// transitions the phase to Failed and emits the Normal transition Event.
func TestAlpha4DeriveFailedFromVerdictReport(t *testing.T) {
	fr := record.NewFakeRecorder(10)
	r := newDeriveReconciler(fr)
	key := inProgressExperimentWithVerdict(t, "exp-derive-failed", "Failed")

	reconcileOnce(t, r, key)

	inst := getExperiment(t, key)
	if inst.Status.Phase != "Failed" {
		t.Fatalf("phase = %q, want Failed (derived from the verdict report)", inst.Status.Phase)
	}
	if inst.Status.Message == "" {
		t.Fatalf("message empty; want a message naming the verdict report")
	}
	evts := drainEvents(fr)
	if len(evts) != 1 {
		t.Fatalf("events = %v, want exactly one Normal transition event", evts)
	}
	if evts[0] != "Normal PhaseTransition experiment phase transitioned to Failed" {
		t.Fatalf("event = %q, want the Normal PhaseTransition to Failed", evts[0])
	}
}

// TestAlpha4StickyErrorPhaseNeverRegresses regression-proofs the existing
// behavior: a parked Error experiment stays Error across reconciles (terminal
// phases and Error are not Operator-owned beyond provisioning).
func TestAlpha4StickyErrorPhaseNeverRegresses(t *testing.T) {
	fr := record.NewFakeRecorder(10)
	r := newDeriveReconciler(fr)
	key := createExperiment(t, validExperiment("exp-sticky-error"))
	setExperimentStatus(t, key, func(st *experimentalpha4.SimulationExperimentStatus) {
		st.Phase = "Error"
		st.Message = "synthetic error detail"
	})

	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, key)
	}

	inst := getExperiment(t, key)
	if inst.Status.Phase != "Error" {
		t.Fatalf("phase = %q, want Error (never regressed)", inst.Status.Phase)
	}
	if inst.Status.Message != "synthetic error detail" {
		t.Fatalf("message = %q, want the original error detail untouched", inst.Status.Message)
	}
	if evts := drainEvents(fr); len(evts) != 0 {
		t.Fatalf("events = %v, want none (a parked phase never emits a transition event)", evts)
	}
}

// TestAlpha4UnknownVerdictIgnored verifies D4: an unknown
// scenarioManagerVerdict value is ignored - the phase stays InProgress with
// no write and no Event. A live API server can never hold such a value (the
// P1 CRD enum rejects it on write), so this case exercises the operator's
// defensive forward-compatibility path through a fake client that tolerates
// it.
func TestAlpha4UnknownVerdictIgnored(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := experimentalpha4.AddToScheme(scheme); err != nil {
		t.Fatalf("add alpha4 to scheme: %v", err)
	}
	exp := &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Name: "exp-derive-unknown", Namespace: "derive-tests", UID: "uid-unknown"},
		Status: experimentalpha4.SimulationExperimentStatus{
			Phase:                  "InProgress",
			Message:                "All components provisioned and ready",
			ScenarioManagerVerdict: "SomethingElse",
		},
	}
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(exp).Build()
	fr := record.NewFakeRecorder(10)
	r := &controller.Alpha4SimulationExperimentReconciler{Client: fc, Scheme: scheme, Recorder: fr}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "exp-derive-unknown", Namespace: "derive-tests"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := &experimentalpha4.SimulationExperiment{}
	if err := fc.Get(context.Background(), types.NamespacedName{Name: "exp-derive-unknown", Namespace: "derive-tests"}, got); err != nil {
		t.Fatalf("get experiment: %v", err)
	}
	if got.Status.Phase != "InProgress" {
		t.Fatalf("phase = %q, want InProgress (unknown verdict is ignored)", got.Status.Phase)
	}
	if got.Status.Message != "All components provisioned and ready" {
		t.Fatalf("message = %q, want the pre-derive message untouched", got.Status.Message)
	}
	if evts := drainEvents(fr); len(evts) != 0 {
		t.Fatalf("events = %v, want none", evts)
	}
}

// TestAlpha4AbsentVerdictDoesNotTransition verifies the pre-verdict temporal
// state: an InProgress experiment without a reported verdict is parked
// unchanged (absence means not yet reported, never a transition path).
func TestAlpha4AbsentVerdictDoesNotTransition(t *testing.T) {
	fr := record.NewFakeRecorder(10)
	r := newDeriveReconciler(fr)
	key := inProgressExperimentWithVerdict(t, "exp-derive-absent", "")

	reconcileOnce(t, r, key)

	inst := getExperiment(t, key)
	if inst.Status.Phase != "InProgress" {
		t.Fatalf("phase = %q, want InProgress (no verdict yet)", inst.Status.Phase)
	}
	if inst.Status.ScenarioManagerVerdict != "" {
		t.Fatalf("scenarioManagerVerdict = %q, want empty (the operator never writes it)", inst.Status.ScenarioManagerVerdict)
	}
	if evts := drainEvents(fr); len(evts) != 0 {
		t.Fatalf("events = %v, want none", evts)
	}
}

// TestAlpha4TerminalRereconcilePerformsNoPhaseWrite verifies idempotent
// re-reconcile: a second reconcile of a terminal experiment performs no phase
// write (phase and message unchanged, no second transition Event - the
// terminal phase parks in the switch's default case).
func TestAlpha4TerminalRereconcilePerformsNoPhaseWrite(t *testing.T) {
	fr := record.NewFakeRecorder(10)
	r := newDeriveReconciler(fr)
	key := inProgressExperimentWithVerdict(t, "exp-derive-idem", "Finished")

	reconcileOnce(t, r, key)
	afterFirst := getExperiment(t, key)
	if afterFirst.Status.Phase != "Finished" {
		t.Fatalf("phase after first reconcile = %q, want Finished", afterFirst.Status.Phase)
	}

	reconcileOnce(t, r, key)

	afterSecond := getExperiment(t, key)
	if afterSecond.Status.Phase != "Finished" {
		t.Fatalf("phase after second reconcile = %q, want Finished (sticky)", afterSecond.Status.Phase)
	}
	if afterSecond.Status.Message != afterFirst.Status.Message {
		t.Fatalf("message changed on re-reconcile: %q -> %q; want no phase write", afterFirst.Status.Message, afterSecond.Status.Message)
	}
	if evts := drainEvents(fr); len(evts) != 1 {
		t.Fatalf("events = %v, want exactly one (the original transition only)", evts)
	}
}

// TestAlpha4OperatorNeverWritesScenarioManagerVerdict is the D9 ownership
// proof at envtest level: across a terminal derivation the operator's status
// patch carries only phase/message - the SM-owned scenarioManagerVerdict
// report keeps the exact value the Scenario Manager wrote.
func TestAlpha4OperatorNeverWritesScenarioManagerVerdict(t *testing.T) {
	r := newDeriveReconciler(nil)
	finishedKey := inProgressExperimentWithVerdict(t, "exp-derive-own-f", "Finished")
	failedKey := inProgressExperimentWithVerdict(t, "exp-derive-own-a", "Failed")

	reconcileOnce(t, r, finishedKey)
	reconcileOnce(t, r, failedKey)

	fin := getExperiment(t, finishedKey)
	if fin.Status.Phase != "Finished" {
		t.Fatalf("finished experiment phase = %q, want Finished", fin.Status.Phase)
	}
	if fin.Status.ScenarioManagerVerdict != "Finished" {
		t.Fatalf("finished experiment scenarioManagerVerdict = %q, want the SM-written value untouched", fin.Status.ScenarioManagerVerdict)
	}
	fail := getExperiment(t, failedKey)
	if fail.Status.Phase != "Failed" {
		t.Fatalf("failed experiment phase = %q, want Failed", fail.Status.Phase)
	}
	if fail.Status.ScenarioManagerVerdict != "Failed" {
		t.Fatalf("failed experiment scenarioManagerVerdict = %q, want the SM-written value untouched", fail.Status.ScenarioManagerVerdict)
	}
}
