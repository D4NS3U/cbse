package buildkit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdmissionGateOpensImmediately(t *testing.T) {
	calls := 0
	f := func(context.Context) (int, error) {
		calls++
		return 1, nil
	}
	if err := AdmissionGate(context.Background(), f); err != nil {
		t.Fatalf("AdmissionGate: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestAdmissionGateRetriesUntilSuccess(t *testing.T) {
	// Shorten the backoff so the test is fast.
	old := gateBackoff
	gateBackoff = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond, 5 * time.Millisecond}
	t.Cleanup(func() { gateBackoff = old })
	calls := 0
	f := func(context.Context) (int, error) {
		calls++
		if calls < 3 {
			return 0, errors.New("not ready")
		}
		return 2, nil
	}
	if err := AdmissionGate(context.Background(), f); err != nil {
		t.Fatalf("AdmissionGate: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestAdmissionGateZeroWorkersIsNotOpen(t *testing.T) {
	old := gateBackoff
	gateBackoff = []time.Duration{2 * time.Millisecond}
	t.Cleanup(func() { gateBackoff = old })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	f := func(context.Context) (int, error) { return 0, nil } // zero workers
	err := AdmissionGate(ctx, f)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestAdmissionGateHonorsCancellation(t *testing.T) {
	old := gateBackoff
	gateBackoff = []time.Duration{50 * time.Millisecond}
	t.Cleanup(func() { gateBackoff = old })
	ctx, cancel := context.WithCancel(context.Background())
	f := func(context.Context) (int, error) { return 0, errors.New("down") }
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	err := AdmissionGate(ctx, f)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
}

func TestBuildInvokesSolve(t *testing.T) {
	called := false
	f := func(ctx context.Context, opt SolveOptions, status chan<- Status) (string, error) {
		called = true
		if opt.Tag != "repo:t" {
			t.Fatalf("tag = %q", opt.Tag)
		}
		if opt.Annotations["experiment.cbse.terministic.de/experiment-uid"] != "uid" {
			t.Fatalf("annotations = %v", opt.Annotations)
		}
		// Drain status to let Build's goroutine finish.
		return "sha256:abc", nil
	}
	digest, err := Build(context.Background(), f, SolveOptions{Tag: "repo:t", Annotations: map[string]string{"experiment.cbse.terministic.de/experiment-uid": "uid"}})
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:abc" {
		t.Fatalf("digest = %q", digest)
	}
	if !called {
		t.Fatal("solve not called")
	}
}

func TestBuildNilSolve(t *testing.T) {
	if _, err := Build(context.Background(), nil, SolveOptions{}); err == nil {
		t.Fatal("nil solve must error")
	}
}
