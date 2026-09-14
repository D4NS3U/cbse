package selection

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/lifecycle"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// fakeDeps is a recording fake for the selection Dependencies. Each function
// field is configured per-test; the *recorder captures call order and args.
type recorder struct {
	mu sync.Mutex

	recoverCalled        bool
	recoverID            int
	recoverAttempt       int
	recoverClaimedBefore time.Time

	claimCalled         bool
	claimID             int
	cancelCalled        bool
	cancelID            int
	cancelAttempt       int
	startCalled         bool
	publishCalled       bool
	publishScen         communication.ScenarioForTranslation
	publishedCalled     bool
	publishFailedCalled bool
}

// recorderSnapshot is a mutex-free copy of the recorder fields so tests can
// assert state without copying the recorder's lock.
type recorderSnapshot struct {
	recoverCalled        bool
	recoverID            int
	recoverAttempt       int
	recoverClaimedBefore time.Time
	claimCalled          bool
	claimID              int
	cancelCalled         bool
	cancelID             int
	cancelAttempt        int
	startCalled          bool
	publishCalled        bool
	publishScen          communication.ScenarioForTranslation
	publishedCalled      bool
	publishFailedCalled  bool
}

func (r *recorder) snapshot() recorderSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return recorderSnapshot{
		recoverCalled:        r.recoverCalled,
		recoverID:            r.recoverID,
		recoverAttempt:       r.recoverAttempt,
		recoverClaimedBefore: r.recoverClaimedBefore,
		claimCalled:          r.claimCalled,
		claimID:              r.claimID,
		cancelCalled:         r.cancelCalled,
		cancelID:             r.cancelID,
		cancelAttempt:        r.cancelAttempt,
		startCalled:          r.startCalled,
		publishCalled:        r.publishCalled,
		publishScen:          r.publishScen,
		publishedCalled:      r.publishedCalled,
		publishFailedCalled:  r.publishFailedCalled,
	}
}

func inProgressExp(ns, name string) *experimentalpha4.SimulationExperiment {
	return &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID("uid-1")},
		Status:     experimentalpha4.SimulationExperimentStatus{Phase: lifecycle.PhaseInProgress},
	}
}

func phaseExp(ns, name, phase string) *experimentalpha4.SimulationExperiment {
	e := inProgressExp(ns, name)
	e.Status.Phase = phase
	return e
}

func newSelector(t *testing.T, pub communication.TranslationRequestPublisher, deps Dependencies) *Selector {
	t.Helper()
	s, err := NewSelector(pub, time.Minute, deps)
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	// Shorten timeouts so a full run() loop test exits promptly.
	s.delay = 5 * time.Millisecond
	s.iterationTimeout = 200 * time.Millisecond
	return s
}

// runOnce runs a single processNext iteration with a fresh timeout context.
func runOnce(t *testing.T, s *Selector) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return s.processNext(ctx)
}

func TestSelectionRecoveryFirstOrdering(t *testing.T) {
	rec := &recorder{}
	var createdCalled bool
	deps := Dependencies{
		now: func() time.Time { return time.Unix(1000, 0) },
		nextStaleUnpublishedTranslationClaim: func(ctx context.Context, before time.Time) (*persistence.StaleTranslationClaim, error) {
			return &persistence.StaleTranslationClaim{ID: 5, TranslationAttempt: 2}, nil
		},
		recoverUnpublishedTranslationClaim: func(ctx context.Context, id, attempt int, before time.Time) (bool, string, error) {
			rec.mu.Lock()
			rec.recoverCalled = true
			rec.recoverID = id
			rec.recoverAttempt = attempt
			rec.recoverClaimedBefore = before
			rec.mu.Unlock()
			return true, persistence.ScenarioStateCreated, nil
		},
		nextCreatedScenario: func(ctx context.Context) (*persistence.TranslationCandidate, error) {
			createdCalled = true
			return nil, nil
		},
	}
	s := newSelector(t, &errPublisher{}, deps)
	if err := runOnce(t, s); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	snap := rec.snapshot()
	if !snap.recoverCalled || snap.recoverID != 5 || snap.recoverAttempt != 2 {
		t.Fatalf("recovery not applied as expected: %+v", snap)
	}
	// Recovery consumes the iteration; Created discovery must not run.
	if createdCalled {
		t.Fatal("Created discovery must not run when a stale claim is recovered")
	}
	// The cutoff is now - publishRecoveryTimeout = 1000s - 60s = 940s.
	if want := time.Unix(940, 0); !snap.recoverClaimedBefore.Equal(want) {
		t.Fatalf("claimedBefore = %v; want %v", snap.recoverClaimedBefore, want)
	}
}

func TestSelectionCreatedAdmitPublishConfirmed(t *testing.T) {
	rec := &recorder{}
	deps := Dependencies{
		now: func() time.Time { return time.Unix(1000, 0) },
		nextStaleUnpublishedTranslationClaim: func(ctx context.Context, _ time.Time) (*persistence.StaleTranslationClaim, error) {
			return nil, nil
		},
		nextCreatedScenario: func(ctx context.Context) (*persistence.TranslationCandidate, error) {
			return &persistence.TranslationCandidate{ID: 11, ProjectNamespace: "ns", ProjectName: "proj"}, nil
		},
		claimScenario: func(ctx context.Context, id int) (*persistence.ScenarioForTranslation, error) {
			rec.mu.Lock()
			rec.claimCalled = true
			rec.claimID = id
			rec.mu.Unlock()
			return &persistence.ScenarioForTranslation{ID: id, ProjectNamespace: "ns", ProjectName: "proj", TranslationAttempt: 1}, nil
		},
		getExperiment: func(ctx context.Context, ns, name string) (*experimentalpha4.SimulationExperiment, error) {
			return inProgressExp(ns, name), nil
		},
		markTranslationPublishStarted: func(ctx context.Context, id, attempt int) (bool, error) {
			rec.mu.Lock()
			rec.startCalled = true
			rec.mu.Unlock()
			return true, nil
		},
		publish: func(ctx context.Context, s communication.ScenarioForTranslation) error {
			rec.mu.Lock()
			rec.publishCalled = true
			rec.publishScen = s
			rec.mu.Unlock()
			return nil
		},
		markScenarioTranslationRequestPublished: func(ctx context.Context, id, attempt int) (bool, error) {
			rec.mu.Lock()
			rec.publishedCalled = true
			rec.mu.Unlock()
			return true, nil
		},
	}
	s := newSelector(t, &errPublisher{}, deps)
	if err := runOnce(t, s); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	snap := rec.snapshot()
	if !snap.claimCalled || !snap.startCalled || !snap.publishCalled || !snap.publishedCalled {
		t.Fatalf("expected full publish sequence; got %+v", snap)
	}
	if snap.publishScen.ID != 11 || snap.publishScen.ProjectNamespace != "ns" || snap.publishScen.ProjectName != "proj" || snap.publishScen.TranslationAttempt != 1 {
		t.Fatalf("published scenario = %+v; want identity preserved", snap.publishScen)
	}
	if snap.cancelCalled {
		t.Fatal("cancel must not be called on an admitted publish")
	}
}

func TestSelectionGateRejectionTerminalCancelsNoPublish(t *testing.T) {
	rec := &recorder{}
	deps := Dependencies{
		now:                                  func() time.Time { return time.Unix(1000, 0) },
		nextStaleUnpublishedTranslationClaim: func(ctx context.Context, _ time.Time) (*persistence.StaleTranslationClaim, error) { return nil, nil },
		nextCreatedScenario: func(ctx context.Context) (*persistence.TranslationCandidate, error) {
			return &persistence.TranslationCandidate{ID: 3, ProjectNamespace: "ns", ProjectName: "proj"}, nil
		},
		claimScenario: func(ctx context.Context, id int) (*persistence.ScenarioForTranslation, error) {
			return &persistence.ScenarioForTranslation{ID: id, ProjectNamespace: "ns", ProjectName: "proj", TranslationAttempt: 1}, nil
		},
		getExperiment: func(ctx context.Context, ns, name string) (*experimentalpha4.SimulationExperiment, error) {
			return phaseExp(ns, name, lifecycle.PhaseFailed), nil // terminal
		},
		cancelUnpublishedTranslationClaim: func(ctx context.Context, id, attempt int) (bool, error) {
			rec.mu.Lock()
			rec.cancelCalled = true
			rec.cancelID = id
			rec.cancelAttempt = attempt
			rec.mu.Unlock()
			return true, nil
		},
		markTranslationPublishStarted: func(ctx context.Context, id, attempt int) (bool, error) {
			rec.mu.Lock()
			rec.startCalled = true
			rec.mu.Unlock()
			return true, nil
		},
		publish: func(ctx context.Context, s communication.ScenarioForTranslation) error {
			rec.mu.Lock()
			rec.publishCalled = true
			rec.mu.Unlock()
			return nil
		},
	}
	s := newSelector(t, &errPublisher{}, deps)
	if err := runOnce(t, s); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	snap := rec.snapshot()
	if !snap.cancelCalled || snap.cancelID != 3 || snap.cancelAttempt != 1 {
		t.Fatalf("expected cancel on terminal gate; got %+v", snap)
	}
	if snap.startCalled || snap.publishCalled {
		t.Fatalf("terminal gate must not start publish or publish; got %+v", snap)
	}
}

func TestSelectionGateRejectionUnavailableCancelsNoPublish(t *testing.T) {
	rec := &recorder{}
	deps := Dependencies{
		now:                                  func() time.Time { return time.Unix(1000, 0) },
		nextStaleUnpublishedTranslationClaim: func(ctx context.Context, _ time.Time) (*persistence.StaleTranslationClaim, error) { return nil, nil },
		nextCreatedScenario: func(ctx context.Context) (*persistence.TranslationCandidate, error) {
			return &persistence.TranslationCandidate{ID: 4, ProjectNamespace: "ns", ProjectName: "proj"}, nil
		},
		claimScenario: func(ctx context.Context, id int) (*persistence.ScenarioForTranslation, error) {
			return &persistence.ScenarioForTranslation{ID: id, ProjectNamespace: "ns", ProjectName: "proj", TranslationAttempt: 1}, nil
		},
		getExperiment: func(ctx context.Context, ns, name string) (*experimentalpha4.SimulationExperiment, error) {
			return phaseExp(ns, name, lifecycle.PhasePending), nil // unavailable
		},
		cancelUnpublishedTranslationClaim: func(ctx context.Context, id, attempt int) (bool, error) {
			rec.mu.Lock()
			rec.cancelCalled = true
			rec.mu.Unlock()
			return true, nil
		},
		publish: func(ctx context.Context, s communication.ScenarioForTranslation) error {
			rec.mu.Lock()
			rec.publishCalled = true
			rec.mu.Unlock()
			return nil
		},
	}
	s := newSelector(t, &errPublisher{}, deps)
	if err := runOnce(t, s); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	snap := rec.snapshot()
	if !snap.cancelCalled {
		t.Fatalf("expected cancel on unavailable gate; got %+v", snap)
	}
	if snap.publishCalled {
		t.Fatal("unavailable gate must not publish")
	}
}

func TestSelectionTransientFetchErrorLeavesClaimNoCancel(t *testing.T) {
	rec := &recorder{}
	deps := Dependencies{
		now:                                  func() time.Time { return time.Unix(1000, 0) },
		nextStaleUnpublishedTranslationClaim: func(ctx context.Context, _ time.Time) (*persistence.StaleTranslationClaim, error) { return nil, nil },
		nextCreatedScenario: func(ctx context.Context) (*persistence.TranslationCandidate, error) {
			return &persistence.TranslationCandidate{ID: 9, ProjectNamespace: "ns", ProjectName: "proj"}, nil
		},
		claimScenario: func(ctx context.Context, id int) (*persistence.ScenarioForTranslation, error) {
			return &persistence.ScenarioForTranslation{ID: id, ProjectNamespace: "ns", ProjectName: "proj", TranslationAttempt: 1}, nil
		},
		getExperiment: func(ctx context.Context, ns, name string) (*experimentalpha4.SimulationExperiment, error) {
			return nil, errors.New("apiserver unavailable")
		},
		cancelUnpublishedTranslationClaim: func(ctx context.Context, id, attempt int) (bool, error) {
			rec.mu.Lock()
			rec.cancelCalled = true
			rec.mu.Unlock()
			return true, nil
		},
		publish: func(ctx context.Context, s communication.ScenarioForTranslation) error {
			rec.mu.Lock()
			rec.publishCalled = true
			rec.mu.Unlock()
			return nil
		},
	}
	s := newSelector(t, &errPublisher{}, deps)
	err := runOnce(t, s)
	if err == nil {
		t.Fatal("transient fetch error: want error returned")
	}
	snap := rec.snapshot()
	// A transient fetch is not a gate rejection: do not cancel, do not publish.
	// The claim is left for stale-claim recovery.
	if snap.cancelCalled || snap.publishCalled {
		t.Fatalf("transient fetch must not cancel or publish; got %+v", snap)
	}
}

func TestSelectionPublishFailureAppliesPublishFailed(t *testing.T) {
	rec := &recorder{}
	deps := Dependencies{
		now:                                  func() time.Time { return time.Unix(1000, 0) },
		nextStaleUnpublishedTranslationClaim: func(ctx context.Context, _ time.Time) (*persistence.StaleTranslationClaim, error) { return nil, nil },
		nextCreatedScenario: func(ctx context.Context) (*persistence.TranslationCandidate, error) {
			return &persistence.TranslationCandidate{ID: 7, ProjectNamespace: "ns", ProjectName: "proj"}, nil
		},
		claimScenario: func(ctx context.Context, id int) (*persistence.ScenarioForTranslation, error) {
			return &persistence.ScenarioForTranslation{ID: id, ProjectNamespace: "ns", ProjectName: "proj", TranslationAttempt: 1}, nil
		},
		getExperiment: func(ctx context.Context, ns, name string) (*experimentalpha4.SimulationExperiment, error) {
			return inProgressExp(ns, name), nil
		},
		markTranslationPublishStarted: func(ctx context.Context, id, attempt int) (bool, error) { return true, nil },
		publish: func(ctx context.Context, s communication.ScenarioForTranslation) error {
			rec.mu.Lock()
			rec.publishCalled = true
			rec.mu.Unlock()
			return errors.New("nats publish timeout")
		},
		markScenarioTranslationPublishFailed: func(ctx context.Context, id, attempt int) (bool, string, error) {
			rec.mu.Lock()
			rec.publishFailedCalled = true
			rec.mu.Unlock()
			return true, persistence.ScenarioStateCreated, nil
		},
		markScenarioTranslationRequestPublished: func(ctx context.Context, id, attempt int) (bool, error) {
			rec.mu.Lock()
			rec.publishedCalled = true
			rec.mu.Unlock()
			return true, nil
		},
	}
	s := newSelector(t, &errPublisher{}, deps)
	err := runOnce(t, s)
	if err == nil {
		t.Fatal("publish failure: want error returned")
	}
	snap := rec.snapshot()
	if !snap.publishFailedCalled {
		t.Fatalf("expected MarkScenarioTranslationPublishFailed; got %+v", snap)
	}
	if snap.publishedCalled {
		t.Fatal("publish failure must not mark request published")
	}
}

func TestSelectionStaleClaimIsNoOp(t *testing.T) {
	rec := &recorder{}
	var startCalled bool
	deps := Dependencies{
		now:                                  func() time.Time { return time.Unix(1000, 0) },
		nextStaleUnpublishedTranslationClaim: func(ctx context.Context, _ time.Time) (*persistence.StaleTranslationClaim, error) { return nil, nil },
		nextCreatedScenario: func(ctx context.Context) (*persistence.TranslationCandidate, error) {
			return &persistence.TranslationCandidate{ID: 2}, nil
		},
		claimScenario: func(ctx context.Context, id int) (*persistence.ScenarioForTranslation, error) {
			return nil, nil // stale: no longer Created
		},
		getExperiment: func(ctx context.Context, ns, name string) (*experimentalpha4.SimulationExperiment, error) {
			return inProgressExp(ns, name), nil
		},
		markTranslationPublishStarted: func(ctx context.Context, id, attempt int) (bool, error) {
			startCalled = true
			return true, nil
		},
		publish: func(ctx context.Context, s communication.ScenarioForTranslation) error {
			rec.mu.Lock()
			rec.publishCalled = true
			rec.mu.Unlock()
			return nil
		},
	}
	s := newSelector(t, &errPublisher{}, deps)
	if err := runOnce(t, s); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	if startCalled || rec.snapshot().publishCalled {
		t.Fatal("stale claim must not start publish or publish")
	}
}

func TestSelectionStaleMarkPublishStartedDoesNotPublish(t *testing.T) {
	rec := &recorder{}
	deps := Dependencies{
		now:                                  func() time.Time { return time.Unix(1000, 0) },
		nextStaleUnpublishedTranslationClaim: func(ctx context.Context, _ time.Time) (*persistence.StaleTranslationClaim, error) { return nil, nil },
		nextCreatedScenario: func(ctx context.Context) (*persistence.TranslationCandidate, error) {
			return &persistence.TranslationCandidate{ID: 8, ProjectNamespace: "ns", ProjectName: "proj"}, nil
		},
		claimScenario: func(ctx context.Context, id int) (*persistence.ScenarioForTranslation, error) {
			return &persistence.ScenarioForTranslation{ID: id, ProjectNamespace: "ns", ProjectName: "proj", TranslationAttempt: 1}, nil
		},
		getExperiment: func(ctx context.Context, ns, name string) (*experimentalpha4.SimulationExperiment, error) {
			return inProgressExp(ns, name), nil
		},
		markTranslationPublishStarted: func(ctx context.Context, id, attempt int) (bool, error) {
			return false, nil // stale: another owner won the marker
		},
		publish: func(ctx context.Context, s communication.ScenarioForTranslation) error {
			rec.mu.Lock()
			rec.publishCalled = true
			rec.mu.Unlock()
			return nil
		},
	}
	s := newSelector(t, &errPublisher{}, deps)
	if err := runOnce(t, s); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	if rec.snapshot().publishCalled {
		t.Fatal("stale MarkTranslationPublishStarted must not publish")
	}
}

func TestSelectionIdleWhenNoCreatedScenario(t *testing.T) {
	var claimCalled bool
	deps := Dependencies{
		now:                                  func() time.Time { return time.Unix(1000, 0) },
		nextStaleUnpublishedTranslationClaim: func(ctx context.Context, _ time.Time) (*persistence.StaleTranslationClaim, error) { return nil, nil },
		nextCreatedScenario:                  func(ctx context.Context) (*persistence.TranslationCandidate, error) { return nil, nil },
		claimScenario: func(ctx context.Context, id int) (*persistence.ScenarioForTranslation, error) {
			claimCalled = true
			return nil, nil
		},
	}
	s := newSelector(t, &errPublisher{}, deps)
	if err := runOnce(t, s); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	if claimCalled {
		t.Fatal("idle iteration must not claim")
	}
}

func TestSelectionRunStopsOnContextCancel(t *testing.T) {
	deps := Dependencies{
		now:                                  func() time.Time { return time.Unix(1000, 0) },
		nextStaleUnpublishedTranslationClaim: func(ctx context.Context, _ time.Time) (*persistence.StaleTranslationClaim, error) { return nil, nil },
		nextCreatedScenario:                  func(ctx context.Context) (*persistence.TranslationCandidate, error) { return nil, nil },
	}
	s := newSelector(t, &errPublisher{}, deps)
	s.delay = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done, err := s.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Let one idle iteration run, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("selector did not stop after context cancel")
	}
}

func TestSelectionNewSelectorValidation(t *testing.T) {
	if _, err := NewSelector(nil, time.Minute, Dependencies{}); err == nil {
		t.Fatal("nil publisher: want error")
	}
	if _, err := NewSelector(&errPublisher{}, 0, Dependencies{}); err == nil {
		t.Fatal("zero recovery timeout: want error")
	}
}

// errPublisher is a communication.TranslationRequestPublisher that always
// fails; tests that expect a successful publish inject their own publish dep
// so the publisher itself is never the source of a publish error here.
type errPublisher struct{}

func (errPublisher) PublishTranslationRequest(ctx context.Context, s communication.ScenarioForTranslation) error {
	return errors.New("publisher not configured")
}
