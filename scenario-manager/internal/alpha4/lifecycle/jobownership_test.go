package lifecycle

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestParsePositiveDecimal(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"1", 1, true},
		{"42", 42, true},
		{"0", 0, false},  // not positive
		{"", 0, false},   // empty
		{"01", 0, false}, // leading zero
		{"-1", 0, false}, // sign
		{"1a", 0, false}, // non-digit
		{" 1", 0, false}, // space
		{"999999999", 999999999, true},
	}
	for _, c := range cases {
		got, ok := parsePositiveDecimal(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parsePositiveDecimal(%q) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestRunnerJobName(t *testing.T) {
	// UID 12 chars after lowercasing and hyphen stripping.
	uid := types.UID("ABCD-1234-EFGH")
	got := RunnerJobName(uid, 7, 2)
	want := "simrun-abcd1234efgh-s7-a2"
	if got != want {
		t.Fatalf("RunnerJobName = %q; want %q", got, want)
	}
}

func TestVerifyRunnerJob(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-abc123", PhaseInProgress, true, false)
	// Verified job passes.
	if err := VerifyRunnerJob(verifiedJob(exp, 5, 1), exp); err != nil {
		t.Fatalf("verified job: %v", err)
	}

	// Wrong project label.
	bad := verifiedJob(exp, 5, 1)
	bad.Labels[LabelProject] = "other"
	if err := VerifyRunnerJob(bad, exp); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("wrong project: %v", err)
	}

	// Wrong UID label.
	bad = verifiedJob(exp, 5, 1)
	bad.Labels[LabelExperimentUID] = "other-uid"
	if err := VerifyRunnerJob(bad, exp); err == nil || !strings.Contains(err.Error(), "experiment-uid") {
		t.Fatalf("wrong uid: %v", err)
	}

	// Non-canonical scenario-id label (leading zero).
	bad = verifiedJob(exp, 5, 1)
	bad.Labels[LabelScenarioID] = "05"
	if err := VerifyRunnerJob(bad, exp); err == nil || !strings.Contains(err.Error(), "scenario-id") {
		t.Fatalf("bad scenario-id: %v", err)
	}

	// Deterministic name mismatch: scenario-id label does not reproduce the name.
	bad = verifiedJob(exp, 5, 1)
	bad.Labels[LabelScenarioID] = "9"
	if err := VerifyRunnerJob(bad, exp); err == nil || !strings.Contains(err.Error(), "does not reproduce") {
		t.Fatalf("name mismatch: %v", err)
	}

	// Wrong namespace.
	bad = verifiedJob(exp, 5, 1)
	bad.Namespace = "other"
	if err := VerifyRunnerJob(bad, exp); err == nil || !strings.Contains(err.Error(), "namespace mismatch") {
		t.Fatalf("wrong namespace: %v", err)
	}

	// Missing controller owner reference.
	bad = verifiedJob(exp, 5, 1)
	bad.OwnerReferences = nil
	if err := VerifyRunnerJob(bad, exp); err == nil || !strings.Contains(err.Error(), "owner reference") {
		t.Fatalf("missing owner ref: %v", err)
	}

	// Wrong controller UID in owner reference.
	bad = verifiedJob(exp, 5, 1)
	bad.OwnerReferences[0].UID = "other-uid"
	if err := VerifyRunnerJob(bad, exp); err == nil || !strings.Contains(err.Error(), "owner reference") {
		t.Fatalf("wrong owner uid: %v", err)
	}
}

func TestDeleteVerifiedRunnerJobs(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-abc123", PhaseInProgress, true, false)
	verified := verifiedJob(exp, 5, 1)
	collision := verifiedJob(exp, 6, 2)
	// Collision: passes the project+UID label list filter, but its controller
	// owner reference points at a different UID.
	collision.OwnerReferences[0].UID = "someone-else"
	k8s := fakeK8s(t, verified, collision)

	deleted, err := DeleteVerifiedRunnerJobs(context.Background(), k8s, exp)
	if err == nil {
		t.Fatal("collision: want error")
	}
	if !strings.Contains(err.Error(), "ownership collision") {
		t.Fatalf("err = %v; want collision", err)
	}
	if len(deleted) != 1 || deleted[0] != verified.Name {
		t.Fatalf("deleted = %v; want [%s]", deleted, verified.Name)
	}
	// Verified Job is gone.
	got := &batchv1.Job{}
	if apiErr := k8s.Get(context.Background(), clientKey(verified), got); !isNotFound(apiErr) {
		t.Fatalf("verified job still present: %v", apiErr)
	}
	// Collision Job remains.
	if apiErr := k8s.Get(context.Background(), clientKey(collision), got); apiErr != nil {
		t.Fatalf("collision job should remain: %v", apiErr)
	}
}

func TestDeleteVerifiedRunnerJobsEmpty(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-abc123", PhaseInProgress, true, false)
	k8s := fakeK8s(t)
	deleted, err := DeleteVerifiedRunnerJobs(context.Background(), k8s, exp)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if deleted != nil {
		t.Fatalf("deleted = %v; want nil", deleted)
	}
}

func TestConfirmRunnerJobsAbsent(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-abc123", PhaseInProgress, true, false)
	present := verifiedJob(exp, 5, 1)
	k8s := fakeK8s(t, present)
	// A still-present job fails the absence check.
	if err := ConfirmRunnerJobsAbsent(context.Background(), k8s, exp.Namespace, []string{present.Name}); err == nil {
		t.Fatal("present job: want absence error")
	}
	// An absent job passes.
	if err := ConfirmRunnerJobsAbsent(context.Background(), k8s, exp.Namespace, []string{"does-not-exist"}); err != nil {
		t.Fatalf("absent job: %v", err)
	}
}

func TestConfirmAllRunnerJobsAbsent(t *testing.T) {
	exp := newExperiment("ns", "proj", "uid-abc123", PhaseInProgress, true, false)
	// Empty candidate set is absent.
	k8s := fakeK8s(t)
	if err := ConfirmAllRunnerJobsAbsent(context.Background(), k8s, exp); err != nil {
		t.Fatalf("empty: %v", err)
	}
	// A remaining job fails the final check.
	k8s = fakeK8s(t, verifiedJob(exp, 5, 1))
	if err := ConfirmAllRunnerJobsAbsent(context.Background(), k8s, exp); err == nil {
		t.Fatal("remaining job: want absence error")
	}
}
