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

package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// testEDSStream and testTranslatorStream are the canonical JetStream stream
// names injected into RunDeletionCleanup in tests. They mirror the nats
// package's exported constants without this transport-neutral package's tests
// importing the transport package (which would create an import cycle, since
// nats imports lifecycle).
const (
	testEDSStream        = "cbse_eds_scenarios"
	testTranslatorStream = "cbse_translator"
	testPPSStream        = "cbse_pps"
)

var (
	errFakeCollision = errors.New("consumer collision")
	errFakePurge     = errors.New("purge failed")
)

// fakePPSCleaner is a lifecycle.PPSCleaner fake that records the PPS consumer
// deletions in order.
type fakePPSCleaner struct {
	deletions []string
	err       error
}

func (f *fakePPSCleaner) DeletePPSConsumer(_ context.Context, uid, namespace, project string) error {
	f.deletions = append(f.deletions, uid+"/"+namespace+"/"+project)
	return f.err
}

func TestRunDeletionCleanupHappyPath(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-abc123", PhaseInProgress, true, true)
	k8s := fakeK8s(t, exp)
	store := &fakeStore{}
	msg := &fakeMsg{}
	pps := &fakePPSCleaner{}

	if err := RunDeletionCleanup(context.Background(), k8s, store, msg, pps, testEDSStream, testTranslatorStream, testPPSStream, exp); err != nil {
		t.Fatalf("RunDeletionCleanup: %v", err)
	}

	// Step 2: one ownership-verified Translator consumer deletion.
	if len(msg.consumerDeletions) != 1 || msg.consumerDeletions[0] != "uid-abc123/ns/proj" {
		t.Fatalf("consumerDeletions = %v; want [uid-abc123/ns/proj]", msg.consumerDeletions)
	}

	// Steps 3-5: three purges on the two shared streams.
	wantPurges := []purgeCall{
		{testEDSStream, "cbse.ns.proj.eds.scenarios"},
		{testTranslatorStream, "cbse.ns.proj.trans.request"},
		{testTranslatorStream, "cbse.ns.proj.trans.*.ready"},
		// Steps 9-10: the PPS extension purges on the PPS stream.
		{testPPSStream, "cbse.ns.proj.pps.request"},
		{testPPSStream, "cbse.ns.proj.pps.*.evaluation"},
	}
	if len(msg.purges) != len(wantPurges) {
		t.Fatalf("purges = %v; want %v", msg.purges, wantPurges)
	}
	for i, want := range wantPurges {
		if msg.purges[i] != want {
			t.Errorf("purge[%d] = %+v; want %+v", i, msg.purges[i], want)
		}
	}

	// Step 6: the project row is deleted (cascade removes scenarios).
	if len(store.deleteCalls) != 1 || store.deleteCalls[0] != "ns/proj" {
		t.Fatalf("deleteCalls = %v; want [ns/proj]", store.deleteCalls)
	}

	// Step 11: one ownership-verified PPS consumer deletion.
	if len(pps.deletions) != 1 || pps.deletions[0] != "uid-abc123/ns/proj" {
		t.Fatalf("pps deletions = %v; want [uid-abc123/ns/proj]", pps.deletions)
	}

	// Step 8: the SM finalizer is removed. With the finalizer gone on a deleting
	// object the fake client completes deletion, so the object is no longer
	// present.
	gone := &experimentalpha4.SimulationExperiment{}
	if err := k8s.Get(context.Background(), types.NamespacedName{Name: exp.Name, Namespace: exp.Namespace}, gone); err == nil {
		t.Fatalf("experiment still present after finalizer removal: %v", gone.Finalizers)
	}
}

func TestRunDeletionCleanupConsumerCollisionRetainsFinalizer(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-abc123", PhaseInProgress, true, true)
	k8s := fakeK8s(t, exp)
	store := &fakeStore{}
	msg := &fakeMsg{consumerErr: errFakeCollision}

	err := RunDeletionCleanup(context.Background(), k8s, store, msg, &fakePPSCleaner{}, testEDSStream, testTranslatorStream, testPPSStream, exp)
	if err == nil || !strings.Contains(err.Error(), "translator consumer") {
		t.Fatalf("err = %v; want translator consumer error", err)
	}
	// Purges must not run when the consumer collision fails the attempt.
	if len(msg.purges) != 0 {
		t.Fatalf("purges = %v; want none on collision", msg.purges)
	}
	// The project row must not be deleted.
	if len(store.deleteCalls) != 0 {
		t.Fatalf("deleteCalls = %v; want none on collision", store.deleteCalls)
	}
	// The finalizer must remain so the caller retries.
	got := getExperiment(t, k8s, exp)
	if !containsFinalizer(got.Finalizers, FinalizerName) {
		t.Fatal("finalizer removed on collision; must be retained")
	}
}

func TestRunDeletionCleanupJobStillPresentFailsEarly(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-abc123", PhaseInProgress, true, true)
	verified := verifiedJob(exp, 5, 1, 1)
	collision := verifiedJob(exp, 6, 2, 1)
	collision.OwnerReferences[0].UID = "someone-else"
	k8s := fakeK8s(t, exp, verified, collision)
	store := &fakeStore{}
	msg := &fakeMsg{}
	pps := &fakePPSCleaner{}

	err := RunDeletionCleanup(context.Background(), k8s, store, msg, pps, testEDSStream, testTranslatorStream, testPPSStream, exp)
	if err == nil || !strings.Contains(err.Error(), "ownership collision") {
		t.Fatalf("err = %v; want ownership collision at step 1", err)
	}
	// The consumer deletion and purges must not run when step 1 fails.
	if len(msg.consumerDeletions) != 0 || len(msg.purges) != 0 {
		t.Fatalf("msg = %+v; want no consumer/purge activity", msg)
	}
	if len(pps.deletions) != 0 {
		t.Fatalf("pps = %+v; want no PPS consumer activity", pps)
	}
	if len(store.deleteCalls) != 0 {
		t.Fatalf("deleteCalls = %v; want none", store.deleteCalls)
	}
	// The finalizer must remain so the caller retries.
	got := getExperiment(t, k8s, exp)
	if !containsFinalizer(got.Finalizers, FinalizerName) {
		t.Fatal("finalizer removed on collision; must be retained")
	}
}

func TestRunDeletionCleanupPurgeFailureRetainsFinalizer(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-abc123", PhaseInProgress, true, true)
	k8s := fakeK8s(t, exp)
	store := &fakeStore{}
	msg := &fakeMsg{purgeErr: errFakePurge}

	err := RunDeletionCleanup(context.Background(), k8s, store, msg, &fakePPSCleaner{}, testEDSStream, testTranslatorStream, testPPSStream, exp)
	if err == nil || !strings.Contains(err.Error(), "purge") {
		t.Fatalf("err = %v; want purge error", err)
	}
	// The project row must not be deleted after a purge failure.
	if len(store.deleteCalls) != 0 {
		t.Fatalf("deleteCalls = %v; want none on purge failure", store.deleteCalls)
	}
	got := getExperiment(t, k8s, exp)
	if !containsFinalizer(got.Finalizers, FinalizerName) {
		t.Fatal("finalizer removed on purge failure; must be retained")
	}
}

func TestRunDeletionCleanupPPSConsumerCollision(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-abc123", PhaseInProgress, true, true)
	k8s := fakeK8s(t, exp)
	store := &fakeStore{}
	msg := &fakeMsg{}
	pps := &fakePPSCleaner{err: errFakeCollision}

	err := RunDeletionCleanup(context.Background(), k8s, store, msg, pps, testEDSStream, testTranslatorStream, testPPSStream, exp)
	if err == nil || !strings.Contains(err.Error(), "pps consumer") {
		t.Fatalf("err = %v; want pps consumer error at step 11", err)
	}
	if len(pps.deletions) != 1 {
		t.Fatalf("pps deletions = %v; want exactly one attempt", pps.deletions)
	}
}

func TestDispatchAction(t *testing.T) {
	cases := []struct {
		phase    string
		deleting bool
		want     ActionKind
	}{
		{PhaseError, false, ActionTerminal},
		{PhaseFailed, false, ActionTerminal},
		{PhaseCompleted, false, ActionCompleted},
		{PhaseInProgress, false, ActionNone},
		{PhasePending, false, ActionNone},
		{PhaseInProgress, true, ActionDeletionCleanup},
		{PhaseCompleted, true, ActionDeletionCleanup},
	}
	for _, c := range cases {
		exp := newExperiment("ns", "proj", "uid-1", c.phase, true, c.deleting)
		if got := DispatchAction(exp); got != c.want {
			t.Errorf("phase=%q deleting=%v: got %s; want %s", c.phase, c.deleting, got, c.want)
		}
	}
	if got := DispatchAction(nil); got != ActionNone {
		t.Errorf("nil: got %s; want none", got)
	}
}

func getExperiment(t *testing.T, k8s client.Client, exp *experimentalpha4.SimulationExperiment) *experimentalpha4.SimulationExperiment {
	t.Helper()
	got := &experimentalpha4.SimulationExperiment{}
	if err := k8s.Get(context.Background(), types.NamespacedName{Name: exp.Name, Namespace: exp.Namespace}, got); err != nil {
		t.Fatalf("get experiment %s/%s: %v", exp.Namespace, exp.Name, err)
	}
	return got
}
