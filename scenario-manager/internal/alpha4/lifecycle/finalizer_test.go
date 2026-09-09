package lifecycle

import (
	"context"
	"testing"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"k8s.io/apimachinery/pkg/types"
)

func TestEnsureFinalizerAddsAndRegets(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-1", PhaseInProgress, false, false)
	k8s := fakeK8s(t, exp)

	current, deleted, err := EnsureFinalizer(context.Background(), k8s, exp)
	if err != nil {
		t.Fatalf("EnsureFinalizer: %v", err)
	}
	if deleted {
		t.Fatal("deleted = true; want false")
	}
	if !containsFinalizer(current.Finalizers, FinalizerName) {
		t.Fatalf("finalizer not present after EnsureFinalizer: %v", current.Finalizers)
	}
	// The re-got object carries the live UID.
	if current.UID != exp.UID {
		t.Fatalf("re-get UID = %q; want %q", current.UID, exp.UID)
	}
}

func TestEnsureFinalizerIdempotent(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-1", PhaseInProgress, true, false)
	k8s := fakeK8s(t, exp)

	current, deleted, err := EnsureFinalizer(context.Background(), k8s, exp)
	if err != nil {
		t.Fatalf("EnsureFinalizer: %v", err)
	}
	if deleted {
		t.Fatal("deleted = true; want false")
	}
	count := 0
	for _, f := range current.Finalizers {
		if f == FinalizerName {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("finalizer count = %d; want exactly 1", count)
	}
}

func TestEnsureFinalizerObservesDeletionAtReget(t *testing.T) {
	// The object already has the finalizer and a deletion timestamp: the re-get
	// observes deletion so the caller enters deletion cleanup instead of
	// registering a project row.
	exp := newExperiment("ns", "proj", "uid-1", PhaseInProgress, true, true)
	k8s := fakeK8s(t, exp)

	current, deleted, err := EnsureFinalizer(context.Background(), k8s, exp)
	if err != nil {
		t.Fatalf("EnsureFinalizer: %v", err)
	}
	if !deleted {
		t.Fatal("deleted = false; want true (deletion observed at re-get)")
	}
	if !containsFinalizer(current.Finalizers, FinalizerName) {
		t.Fatal("finalizer must remain while deletion is in progress")
	}
}

func TestRemoveFinalizer(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-1", PhaseInProgress, true, false)
	k8s := fakeK8s(t, exp)

	if err := RemoveFinalizer(context.Background(), k8s, exp); err != nil {
		t.Fatalf("RemoveFinalizer: %v", err)
	}
	got := &experimentalpha4.SimulationExperiment{}
	if err := k8s.Get(context.Background(), types.NamespacedName{Name: exp.Name, Namespace: exp.Namespace}, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if containsFinalizer(got.Finalizers, FinalizerName) {
		t.Fatalf("finalizer still present: %v", got.Finalizers)
	}
}

func TestRemoveFinalizerAbsent(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-1", PhaseInProgress, false, false)
	k8s := fakeK8s(t, exp)
	if err := RemoveFinalizer(context.Background(), k8s, exp); err != nil {
		t.Fatalf("RemoveFinalizer (absent): %v", err)
	}
}

func containsFinalizer(fs []string, name string) bool {
	for _, f := range fs {
		if f == name {
			return true
		}
	}
	return false
}
