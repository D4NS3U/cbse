package lifecycle

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/persistence"
)

func TestRunTerminalActionDeletesJobsAndBulkUpdates(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-abc123", PhaseFailed, true, false)
	job := verifiedJob(exp, 5, 1)
	k8s := fakeK8s(t, exp, job)
	store := &fakeStore{projectID: 7, failRows: 3}

	if err := RunTerminalAction(context.Background(), k8s, store, exp); err != nil {
		t.Fatalf("RunTerminalAction: %v", err)
	}
	// The verified Job was deleted.
	got := &batchv1.Job{}
	if err := k8s.Get(context.Background(), clientKey(job), got); err == nil {
		t.Fatal("job still present after terminal action")
	}
	// The bulk update targeted the resolved project id.
	if len(store.failCalls) != 1 || store.failCalls[0] != 7 {
		t.Fatalf("failCalls = %v; want [7]", store.failCalls)
	}
	// Project lookup happened exactly once.
	if len(store.projectIDCalls) != 1 || store.projectIDCalls[0] != "ns/proj" {
		t.Fatalf("projectIDCalls = %v; want [ns/proj]", store.projectIDCalls)
	}
	// No project delete during a terminal action (rows retained for diagnosis).
	if len(store.deleteCalls) != 0 {
		t.Fatalf("deleteCalls = %v; want none", store.deleteCalls)
	}
}

func TestRunTerminalActionAbsentProjectIsSuccess(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-abc123", PhaseError, true, false)
	k8s := fakeK8s(t, exp)
	store := &fakeStore{projectErr: persistence.ErrProjectNotFound}

	if err := RunTerminalAction(context.Background(), k8s, store, exp); err != nil {
		t.Fatalf("RunTerminalAction: %v", err)
	}
	if len(store.failCalls) != 0 {
		t.Fatalf("absent project: failCalls = %v; want none", store.failCalls)
	}
}

func TestRunTerminalActionCollisionFails(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-abc123", PhaseFailed, true, false)
	verified := verifiedJob(exp, 5, 1)
	collision := verifiedJob(exp, 6, 2)
	collision.OwnerReferences[0].UID = "someone-else"
	k8s := fakeK8s(t, exp, verified, collision)
	store := &fakeStore{projectID: 7, failRows: 1}

	err := RunTerminalAction(context.Background(), k8s, store, exp)
	if err == nil || !strings.Contains(err.Error(), "ownership collision") {
		t.Fatalf("err = %v; want collision", err)
	}
	// The bulk update must not run when the Job pass fails.
	if len(store.failCalls) != 0 {
		t.Fatalf("collision: failCalls = %v; want none", store.failCalls)
	}
}
