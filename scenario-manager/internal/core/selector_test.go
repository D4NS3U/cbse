package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/coredb"
)

// selectorTestPublisher is deliberately inert. Selector tests verify that the
// publisher is handed to the Translator workflow, not how NATS publishes it.
type selectorTestPublisher struct{}

func (*selectorTestPublisher) PublishTranslationRequest(context.Context, coredb.ScenarioForTranslation) error {
	return nil
}

func newBasicScenarioSelectorForTest() *basicScenarioSelector {
	return &basicScenarioSelector{
		publisher:              &selectorTestPublisher{},
		publishRecoveryTimeout: time.Minute,
		delay:                  basicScenarioSelectorDelay,
		iterationTimeout:       basicScenarioSelectorIterationTimeout,
		dependencies: basicScenarioSelectorDependencies{
			now: func() time.Time { return time.Unix(1_700_000_000, 0) },
			wait: func(ctx context.Context, _ time.Duration) error {
				<-ctx.Done()
				return ctx.Err()
			},
			nextStaleUnpublishedTranslationClaim: func(context.Context, time.Time) (*coredb.TranslationRecoveryCandidate, error) {
				return nil, nil
			},
			recoverUnpublishedTranslationClaim: func(context.Context, int, int, time.Time) (bool, string, error) {
				return false, "", nil
			},
			nextActionableScenario: func(context.Context) (*coredb.ScenarioActionCandidate, error) {
				return nil, nil
			},
			processScenarioTrans: func(context.Context, int, communication.TranslationRequestPublisher) error {
				return nil
			},
			markScenarioInProcessing: func(context.Context, int) (bool, error) {
				return false, nil
			},
			createSimulationRunnerJob: func(context.Context, int) error { return nil },
			doPostProcessing:          func(context.Context, int) (bool, error) { return true, nil },
			markScenarioFinished: func(context.Context, int) (bool, error) {
				return true, nil
			},
			markScenarioRequiresMoreRuns: func(context.Context, int) (bool, error) {
				return true, nil
			},
			markScenarioFailedFrom: func(context.Context, int, string) (bool, error) {
				return true, nil
			},
		},
	}
}

func requireSelectorSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()

	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func requireSelectorDone(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("selector worker did not stop")
	}

	// A closed completion channel must remain readable; this also detects a
	// worker that merely sent one value instead of closing its join handle.
	select {
	case <-done:
	default:
		t.Fatal("selector completion channel was not closed")
	}
}

func TestStartBasicScenarioSelectorRejectsInvalidStartup(t *testing.T) {
	publisher := &selectorTestPublisher{}

	t.Run("nil context", func(t *testing.T) {
		done, err := StartBasicScenarioSelector(nil, publisher)
		if err == nil {
			t.Fatal("expected nil context to be rejected")
		}
		if done != nil {
			t.Fatal("validation failure returned a completion channel")
		}
	})

	t.Run("already cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		done, err := StartBasicScenarioSelector(ctx, publisher)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
		if done != nil {
			t.Fatal("validation failure returned a completion channel")
		}
	})

	t.Run("nil publisher", func(t *testing.T) {
		done, err := StartBasicScenarioSelector(context.Background(), nil)
		if err == nil {
			t.Fatal("expected nil publisher to be rejected")
		}
		if done != nil {
			t.Fatal("validation failure returned a completion channel")
		}
	})
}

func TestBasicScenarioSelectorWorkerStartsImmediatelyThenUsesFixedDelay(t *testing.T) {
	selector := newBasicScenarioSelectorForTest()
	iterationStarted := make(chan struct{}, 1)
	waitCalled := make(chan time.Duration, 1)

	selector.dependencies.nextStaleUnpublishedTranslationClaim = func(context.Context, time.Time) (*coredb.TranslationRecoveryCandidate, error) {
		iterationStarted <- struct{}{}
		return nil, nil
	}
	selector.dependencies.wait = func(ctx context.Context, delay time.Duration) error {
		waitCalled <- delay
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := selector.start(ctx)
	requireSelectorSignal(t, iterationStarted, "the immediate first iteration")

	select {
	case delay := <-waitCalled:
		if delay != 5*time.Second {
			t.Fatalf("worker delay = %s, want 5s", delay)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not wait after its completed iteration")
	}

	cancel()
	requireSelectorDone(t, done)
}

func TestBasicScenarioSelectorWorkerProvidesFixedIterationDeadline(t *testing.T) {
	selector := newBasicScenarioSelectorForTest()
	deadlineRemaining := make(chan time.Duration, 1)

	selector.dependencies.nextStaleUnpublishedTranslationClaim = func(ctx context.Context, _ time.Time) (*coredb.TranslationRecoveryCandidate, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("selector iteration has no deadline")
			deadlineRemaining <- 0
			return nil, nil
		}
		deadlineRemaining <- time.Until(deadline)
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := selector.start(ctx)

	select {
	case remaining := <-deadlineRemaining:
		// Allow generous scheduler tolerance while still proving that production
		// uses the fixed 30-second policy rather than the five-second throttle.
		if remaining < 25*time.Second || remaining > 31*time.Second {
			t.Fatalf("iteration deadline remaining = %s, want approximately 30s", remaining)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start its first iteration")
	}

	cancel()
	requireSelectorDone(t, done)
}

func TestBasicScenarioSelectorWorkerCancellationDuringIteration(t *testing.T) {
	selector := newBasicScenarioSelectorForTest()
	entered := make(chan struct{}, 1)
	var ordinaryCalls atomic.Int32
	var waitCalls atomic.Int32

	selector.dependencies.nextStaleUnpublishedTranslationClaim = func(ctx context.Context, _ time.Time) (*coredb.TranslationRecoveryCandidate, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	selector.dependencies.nextActionableScenario = func(context.Context) (*coredb.ScenarioActionCandidate, error) {
		ordinaryCalls.Add(1)
		return nil, nil
	}
	selector.dependencies.wait = func(context.Context, time.Duration) error {
		waitCalls.Add(1)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := selector.start(ctx)
	requireSelectorSignal(t, entered, "iteration dependency")
	cancel()
	requireSelectorDone(t, done)

	if ordinaryCalls.Load() != 0 {
		t.Fatal("ordinary lookup ran after cancellation during recovery discovery")
	}
	if waitCalls.Load() != 0 {
		t.Fatal("worker waited after root cancellation during an iteration")
	}
}

func TestBasicScenarioSelectorWorkerCancellationDuringWait(t *testing.T) {
	selector := newBasicScenarioSelectorForTest()
	waitEntered := make(chan struct{}, 1)
	var recoveryCalls atomic.Int32

	selector.dependencies.nextStaleUnpublishedTranslationClaim = func(context.Context, time.Time) (*coredb.TranslationRecoveryCandidate, error) {
		recoveryCalls.Add(1)
		return nil, nil
	}
	selector.dependencies.wait = func(ctx context.Context, _ time.Duration) error {
		waitEntered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := selector.start(ctx)
	requireSelectorSignal(t, waitEntered, "post-iteration wait")
	cancel()
	requireSelectorDone(t, done)

	if recoveryCalls.Load() != 1 {
		t.Fatalf("recovery lookup calls = %d, want exactly one", recoveryCalls.Load())
	}
}

func TestBasicScenarioSelectorWorkerIsSerialAndContinuesAfterError(t *testing.T) {
	selector := newBasicScenarioSelectorForTest()
	firstRelease := make(chan struct{})
	waitRelease := make(chan struct{})
	lookupEntered := make(chan int, 2)
	waitEntered := make(chan struct{}, 1)
	operationalErr := errors.New("database unavailable")
	var calls atomic.Int32
	var active atomic.Int32
	var maximumActive atomic.Int32

	// Recovery deliberately reports no work so the injected failure comes from
	// the ordinary lookup. The next ordinary lookup must happen only after the
	// first call has returned and the post-iteration wait has completed.
	selector.dependencies.nextActionableScenario = func(ctx context.Context) (*coredb.ScenarioActionCandidate, error) {
		current := active.Add(1)
		for {
			maximum := maximumActive.Load()
			if current <= maximum || maximumActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		defer active.Add(-1)

		call := int(calls.Add(1))
		lookupEntered <- call
		if call == 1 {
			select {
			case <-firstRelease:
				return nil, operationalErr
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		<-ctx.Done()
		return nil, ctx.Err()
	}
	selector.dependencies.wait = func(ctx context.Context, _ time.Duration) error {
		waitEntered <- struct{}{}
		select {
		case <-waitRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := selector.start(ctx)

	select {
	case call := <-lookupEntered:
		if call != 1 {
			t.Fatalf("first lookup call number = %d", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first lookup did not start")
	}
	close(firstRelease)
	requireSelectorSignal(t, waitEntered, "wait after operational error")

	select {
	case call := <-lookupEntered:
		t.Fatalf("iteration %d started before the wait completed", call)
	default:
	}

	close(waitRelease)
	select {
	case call := <-lookupEntered:
		if call != 2 {
			t.Fatalf("second lookup call number = %d", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not continue after an operational error")
	}

	cancel()
	requireSelectorDone(t, done)
	if maximumActive.Load() != 1 {
		t.Fatalf("maximum concurrent iterations = %d, want 1", maximumActive.Load())
	}
}

func TestProcessNextScenarioRecoveryUsesOneExactCutoffAndConsumesIteration(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 123, time.UTC)
	wantCutoff := now.Add(-90 * time.Second)
	recoveryErr := errors.New("recovery failed")

	tests := []struct {
		name       string
		changed    bool
		finalState string
		err        error
	}{
		{name: "recovered for retry", changed: true, finalState: coredb.ScenarioStateCreated},
		{name: "recovered as failed", changed: true, finalState: coredb.ScenarioStateFailed},
		{name: "stale recovery", changed: false},
		{name: "recovery error", err: recoveryErr},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selector := newBasicScenarioSelectorForTest()
			selector.publishRecoveryTimeout = 90 * time.Second
			var clockCalls int
			var ordinaryCalls int
			var discoveryCutoff time.Time
			var recoveryCutoff time.Time

			selector.dependencies.now = func() time.Time {
				clockCalls++
				return now
			}
			selector.dependencies.nextStaleUnpublishedTranslationClaim = func(_ context.Context, cutoff time.Time) (*coredb.TranslationRecoveryCandidate, error) {
				discoveryCutoff = cutoff
				return &coredb.TranslationRecoveryCandidate{ID: 42, TranslationAttempt: 7}, nil
			}
			selector.dependencies.recoverUnpublishedTranslationClaim = func(_ context.Context, id, attempt int, cutoff time.Time) (bool, string, error) {
				if id != 42 || attempt != 7 {
					t.Fatalf("recovery received id=%d attempt=%d, want 42/7", id, attempt)
				}
				recoveryCutoff = cutoff
				return test.changed, test.finalState, test.err
			}
			selector.dependencies.nextActionableScenario = func(context.Context) (*coredb.ScenarioActionCandidate, error) {
				ordinaryCalls++
				return nil, nil
			}

			err := selector.processNextScenario(context.Background())
			if test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("process error = %v, want wrapped %v", err, test.err)
			}
			if test.err == nil && err != nil {
				t.Fatalf("process returned unexpected error: %v", err)
			}
			if clockCalls != 1 {
				t.Fatalf("clock calls = %d, want 1", clockCalls)
			}
			if !discoveryCutoff.Equal(wantCutoff) || !recoveryCutoff.Equal(wantCutoff) {
				t.Fatalf("cutoffs discovery=%s recovery=%s, want %s", discoveryCutoff, recoveryCutoff, wantCutoff)
			}
			if ordinaryCalls != 0 {
				t.Fatal("ordinary lookup ran after a recovery candidate was found")
			}
		})
	}
}

func TestProcessNextScenarioLookupOrderingAndErrors(t *testing.T) {
	t.Run("discovery error prevents ordinary lookup", func(t *testing.T) {
		selector := newBasicScenarioSelectorForTest()
		discoveryErr := errors.New("discovery")
		ordinaryCalls := 0
		selector.dependencies.nextStaleUnpublishedTranslationClaim = func(context.Context, time.Time) (*coredb.TranslationRecoveryCandidate, error) {
			return nil, discoveryErr
		}
		selector.dependencies.nextActionableScenario = func(context.Context) (*coredb.ScenarioActionCandidate, error) {
			ordinaryCalls++
			return nil, nil
		}

		err := selector.processNextScenario(context.Background())
		if !errors.Is(err, discoveryErr) {
			t.Fatalf("process error = %v, want wrapped discovery error", err)
		}
		if ordinaryCalls != 0 {
			t.Fatal("ordinary lookup ran after recovery discovery failed")
		}
	})

	t.Run("recovery miss falls through once", func(t *testing.T) {
		selector := newBasicScenarioSelectorForTest()
		var order []string
		selector.dependencies.nextStaleUnpublishedTranslationClaim = func(context.Context, time.Time) (*coredb.TranslationRecoveryCandidate, error) {
			order = append(order, "recovery")
			return nil, nil
		}
		selector.dependencies.nextActionableScenario = func(context.Context) (*coredb.ScenarioActionCandidate, error) {
			order = append(order, "ordinary")
			return nil, nil
		}

		if err := selector.processNextScenario(context.Background()); err != nil {
			t.Fatalf("process returned unexpected error: %v", err)
		}
		if len(order) != 2 || order[0] != "recovery" || order[1] != "ordinary" {
			t.Fatalf("lookup order = %v, want [recovery ordinary]", order)
		}
	})

	t.Run("ordinary lookup error invokes no handler", func(t *testing.T) {
		selector := newBasicScenarioSelectorForTest()
		ordinaryErr := errors.New("ordinary")
		handlerCalls := 0
		selector.dependencies.nextActionableScenario = func(context.Context) (*coredb.ScenarioActionCandidate, error) {
			return nil, ordinaryErr
		}
		selector.dependencies.processScenarioTrans = func(context.Context, int, communication.TranslationRequestPublisher) error {
			handlerCalls++
			return nil
		}

		err := selector.processNextScenario(context.Background())
		if !errors.Is(err, ordinaryErr) {
			t.Fatalf("process error = %v, want wrapped ordinary error", err)
		}
		if handlerCalls != 0 {
			t.Fatal("state handler ran after ordinary lookup failed")
		}
	})
}

func TestProcessNextScenarioCancellationCheckpoints(t *testing.T) {
	t.Run("after recovery discovery", func(t *testing.T) {
		selector := newBasicScenarioSelectorForTest()
		ctx, cancel := context.WithCancel(context.Background())
		recoveryCalls := 0
		ordinaryCalls := 0
		selector.dependencies.nextStaleUnpublishedTranslationClaim = func(context.Context, time.Time) (*coredb.TranslationRecoveryCandidate, error) {
			cancel()
			return &coredb.TranslationRecoveryCandidate{ID: 1, TranslationAttempt: 1}, nil
		}
		selector.dependencies.recoverUnpublishedTranslationClaim = func(context.Context, int, int, time.Time) (bool, string, error) {
			recoveryCalls++
			return true, coredb.ScenarioStateCreated, nil
		}
		selector.dependencies.nextActionableScenario = func(context.Context) (*coredb.ScenarioActionCandidate, error) {
			ordinaryCalls++
			return nil, nil
		}

		err := selector.processNextScenario(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("process error = %v, want context cancellation", err)
		}
		if recoveryCalls != 0 || ordinaryCalls != 0 {
			t.Fatalf("calls after cancellation: recovery=%d ordinary=%d", recoveryCalls, ordinaryCalls)
		}
	})

	t.Run("after ordinary lookup", func(t *testing.T) {
		selector := newBasicScenarioSelectorForTest()
		ctx, cancel := context.WithCancel(context.Background())
		handlerCalls := 0
		selector.dependencies.nextActionableScenario = func(context.Context) (*coredb.ScenarioActionCandidate, error) {
			cancel()
			return &coredb.ScenarioActionCandidate{ID: 1, State: coredb.ScenarioStateCreated}, nil
		}
		selector.dependencies.processScenarioTrans = func(context.Context, int, communication.TranslationRequestPublisher) error {
			handlerCalls++
			return nil
		}

		err := selector.processNextScenario(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("process error = %v, want context cancellation", err)
		}
		if handlerCalls != 0 {
			t.Fatal("handler ran after cancellation following ordinary lookup")
		}
	})
}

func TestProcessNextScenarioCreatedDelegatesOnlyToTranslator(t *testing.T) {
	selector := newBasicScenarioSelectorForTest()
	translatorErr := errors.New("translator publish")
	wantPublisher := selector.publisher
	translatorCalls := 0
	failureCalls := 0

	selector.dependencies.nextActionableScenario = func(context.Context) (*coredb.ScenarioActionCandidate, error) {
		return &coredb.ScenarioActionCandidate{ID: 73, State: coredb.ScenarioStateCreated}, nil
	}
	selector.dependencies.processScenarioTrans = func(_ context.Context, id int, publisher communication.TranslationRequestPublisher) error {
		translatorCalls++
		if id != 73 {
			t.Fatalf("translator scenario id = %d, want 73", id)
		}
		if publisher != wantPublisher {
			t.Fatal("selector did not pass its configured publisher")
		}
		return translatorErr
	}
	selector.dependencies.markScenarioFailedFrom = func(context.Context, int, string) (bool, error) {
		failureCalls++
		return true, nil
	}

	err := selector.processNextScenario(context.Background())
	if !errors.Is(err, translatorErr) {
		t.Fatalf("process error = %v, want wrapped translator error", err)
	}
	if translatorCalls != 1 {
		t.Fatalf("translator calls = %d, want 1", translatorCalls)
	}
	if failureCalls != 0 {
		t.Fatal("selector marked a scenario failed for a Translator-owned error")
	}
}

func TestProcessNextScenarioUnexpectedStateDoesNothing(t *testing.T) {
	selector := newBasicScenarioSelectorForTest()
	actionCalls := 0
	selector.dependencies.nextActionableScenario = func(context.Context) (*coredb.ScenarioActionCandidate, error) {
		return &coredb.ScenarioActionCandidate{ID: 9, State: "FutureState"}, nil
	}
	selector.dependencies.processScenarioTrans = func(context.Context, int, communication.TranslationRequestPublisher) error {
		actionCalls++
		return nil
	}
	selector.dependencies.markScenarioInProcessing = func(context.Context, int) (bool, error) {
		actionCalls++
		return true, nil
	}
	selector.dependencies.doPostProcessing = func(context.Context, int) (bool, error) {
		actionCalls++
		return true, nil
	}

	if err := selector.processNextScenario(context.Background()); err != nil {
		t.Fatalf("process returned unexpected error: %v", err)
	}
	if actionCalls != 0 {
		t.Fatalf("unexpected state invoked %d state actions", actionCalls)
	}
}

func TestHandleStartingRunnersClaimOutcomes(t *testing.T) {
	claimErr := errors.New("claim")
	tests := []struct {
		name      string
		changed   bool
		claimErr  error
		wantError error
	}{
		{name: "stale claim", changed: false},
		{name: "claim error", claimErr: claimErr, wantError: claimErr},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selector := newBasicScenarioSelectorForTest()
			placeholderCalls := 0
			failureCalls := 0
			selector.dependencies.markScenarioInProcessing = func(_ context.Context, id int) (bool, error) {
				if id != 12 {
					t.Fatalf("claim id = %d, want 12", id)
				}
				return test.changed, test.claimErr
			}
			selector.dependencies.createSimulationRunnerJob = func(context.Context, int) error {
				placeholderCalls++
				return nil
			}
			selector.dependencies.markScenarioFailedFrom = func(context.Context, int, string) (bool, error) {
				failureCalls++
				return true, nil
			}

			err := selector.handleStartingRunners(context.Background(), 12)
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("handler error = %v, want wrapped %v", err, test.wantError)
			}
			if test.wantError == nil && err != nil {
				t.Fatalf("handler returned unexpected error: %v", err)
			}
			if placeholderCalls != 0 || failureCalls != 0 {
				t.Fatalf("calls after unsuccessful claim: placeholder=%d failure=%d", placeholderCalls, failureCalls)
			}
		})
	}
}

func TestHandleStartingRunnersClaimsBeforeSuccessfulPlaceholder(t *testing.T) {
	selector := newBasicScenarioSelectorForTest()
	var order []string
	selector.dependencies.markScenarioInProcessing = func(context.Context, int) (bool, error) {
		order = append(order, "claim")
		return true, nil
	}
	selector.dependencies.createSimulationRunnerJob = func(context.Context, int) error {
		order = append(order, "placeholder")
		return nil
	}
	selector.dependencies.markScenarioFailedFrom = func(context.Context, int, string) (bool, error) {
		order = append(order, "failure")
		return true, nil
	}

	if err := selector.handleStartingRunners(context.Background(), 12); err != nil {
		t.Fatalf("handler returned unexpected error: %v", err)
	}
	if len(order) != 2 || order[0] != "claim" || order[1] != "placeholder" {
		t.Fatalf("runner order = %v, want [claim placeholder]", order)
	}
}

func TestHandleStartingRunnersPlaceholderFailure(t *testing.T) {
	placeholderErr := errors.New("runner placeholder")
	persistenceErr := errors.New("persist failure")
	tests := []struct {
		name           string
		failureChanged bool
		failureErr     error
	}{
		{name: "failure persisted", failureChanged: true},
		{name: "failure write stale", failureChanged: false},
		{name: "failure write error", failureErr: persistenceErr},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selector := newBasicScenarioSelectorForTest()
			failureCalls := 0
			selector.dependencies.markScenarioInProcessing = func(context.Context, int) (bool, error) {
				return true, nil
			}
			selector.dependencies.createSimulationRunnerJob = func(context.Context, int) error {
				return placeholderErr
			}
			selector.dependencies.markScenarioFailedFrom = func(_ context.Context, id int, expectedState string) (bool, error) {
				failureCalls++
				if id != 12 || expectedState != coredb.ScenarioStateInProcessing {
					t.Fatalf("failure guard id/state = %d/%q", id, expectedState)
				}
				return test.failureChanged, test.failureErr
			}

			err := selector.handleStartingRunners(context.Background(), 12)
			if !errors.Is(err, placeholderErr) {
				t.Fatalf("handler error = %v, want placeholder error", err)
			}
			if test.failureErr != nil && !errors.Is(err, test.failureErr) {
				t.Fatalf("handler error = %v, want persistence error to be preserved", err)
			}
			if failureCalls != 1 {
				t.Fatalf("failure write calls = %d, want 1", failureCalls)
			}
		})
	}
}

func TestHandleStartingRunnersCancellationNeverFailsScenario(t *testing.T) {
	tests := []struct {
		name                string
		cancelAfterClaim    bool
		cancelInPlaceholder bool
		placeholderErr      error
	}{
		{name: "cancelled after claim", cancelAfterClaim: true},
		{name: "cancelled by placeholder", cancelInPlaceholder: true, placeholderErr: errors.New("runner interrupted")},
		{name: "placeholder returns cancellation", placeholderErr: context.Canceled},
		{name: "placeholder returns deadline", placeholderErr: context.DeadlineExceeded},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selector := newBasicScenarioSelectorForTest()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			placeholderCalls := 0
			failureCalls := 0
			selector.dependencies.markScenarioInProcessing = func(context.Context, int) (bool, error) {
				if test.cancelAfterClaim {
					cancel()
				}
				return true, nil
			}
			selector.dependencies.createSimulationRunnerJob = func(context.Context, int) error {
				placeholderCalls++
				if test.cancelInPlaceholder {
					cancel()
				}
				return test.placeholderErr
			}
			selector.dependencies.markScenarioFailedFrom = func(context.Context, int, string) (bool, error) {
				failureCalls++
				return true, nil
			}

			err := selector.handleStartingRunners(ctx, 12)
			wantErr := test.placeholderErr
			if test.cancelAfterClaim || test.cancelInPlaceholder {
				wantErr = context.Canceled
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("handler error = %v, want cancellation %v", err, wantErr)
			}
			if test.cancelAfterClaim && placeholderCalls != 0 {
				t.Fatal("placeholder ran after cancellation following the durable claim")
			}
			if failureCalls != 0 {
				t.Fatal("workflow cancellation triggered a scenario failure write")
			}
		})
	}
}

func TestHandlePostProcessingSuccessfulResults(t *testing.T) {
	tests := []struct {
		name              string
		confidenceReached bool
		transitionChanged bool
	}{
		{name: "confidence reached", confidenceReached: true, transitionChanged: true},
		{name: "more runs required", confidenceReached: false, transitionChanged: true},
		{name: "finished transition stale", confidenceReached: true, transitionChanged: false},
		{name: "more-runs transition stale", confidenceReached: false, transitionChanged: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selector := newBasicScenarioSelectorForTest()
			finishedCalls := 0
			moreRunsCalls := 0
			failureCalls := 0
			selector.dependencies.doPostProcessing = func(context.Context, int) (bool, error) {
				return test.confidenceReached, nil
			}
			selector.dependencies.markScenarioFinished = func(_ context.Context, id int) (bool, error) {
				finishedCalls++
				if id != 31 {
					t.Fatalf("finished id = %d, want 31", id)
				}
				return test.transitionChanged, nil
			}
			selector.dependencies.markScenarioRequiresMoreRuns = func(_ context.Context, id int) (bool, error) {
				moreRunsCalls++
				if id != 31 {
					t.Fatalf("more-runs id = %d, want 31", id)
				}
				return test.transitionChanged, nil
			}
			selector.dependencies.markScenarioFailedFrom = func(context.Context, int, string) (bool, error) {
				failureCalls++
				return true, nil
			}

			if err := selector.handlePostProcessing(context.Background(), 31); err != nil {
				t.Fatalf("handler returned unexpected error: %v", err)
			}
			if test.confidenceReached {
				if finishedCalls != 1 || moreRunsCalls != 0 {
					t.Fatalf("transition calls finished=%d more-runs=%d", finishedCalls, moreRunsCalls)
				}
			} else if finishedCalls != 0 || moreRunsCalls != 1 {
				t.Fatalf("transition calls finished=%d more-runs=%d", finishedCalls, moreRunsCalls)
			}
			if failureCalls != 0 {
				t.Fatal("successful post-processing result triggered failure write")
			}
		})
	}
}

func TestHandlePostProcessingTransitionErrorIsNotScenarioFailure(t *testing.T) {
	transitionErr := errors.New("transition persistence")
	for _, confidenceReached := range []bool{true, false} {
		name := "more runs"
		if confidenceReached {
			name = "finished"
		}
		t.Run(name, func(t *testing.T) {
			selector := newBasicScenarioSelectorForTest()
			failureCalls := 0
			selector.dependencies.doPostProcessing = func(context.Context, int) (bool, error) {
				return confidenceReached, nil
			}
			selector.dependencies.markScenarioFinished = func(context.Context, int) (bool, error) {
				return false, transitionErr
			}
			selector.dependencies.markScenarioRequiresMoreRuns = func(context.Context, int) (bool, error) {
				return false, transitionErr
			}
			selector.dependencies.markScenarioFailedFrom = func(context.Context, int, string) (bool, error) {
				failureCalls++
				return true, nil
			}

			err := selector.handlePostProcessing(context.Background(), 31)
			if !errors.Is(err, transitionErr) {
				t.Fatalf("handler error = %v, want transition error", err)
			}
			if failureCalls != 0 {
				t.Fatal("result persistence error triggered failure write")
			}
		})
	}
}

func TestHandlePostProcessingPlaceholderFailureIgnoresBoolean(t *testing.T) {
	placeholderErr := errors.New("post-processing placeholder")
	persistenceErr := errors.New("persist failure")
	tests := []struct {
		name           string
		failureChanged bool
		failureErr     error
	}{
		{name: "failure persisted", failureChanged: true},
		{name: "failure write stale", failureChanged: false},
		{name: "failure write error", failureErr: persistenceErr},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selector := newBasicScenarioSelectorForTest()
			finishedCalls := 0
			moreRunsCalls := 0
			failureCalls := 0
			selector.dependencies.doPostProcessing = func(context.Context, int) (bool, error) {
				// Returning true with an error proves that the handler gives the
				// error precedence over the confidence boolean.
				return true, placeholderErr
			}
			selector.dependencies.markScenarioFinished = func(context.Context, int) (bool, error) {
				finishedCalls++
				return true, nil
			}
			selector.dependencies.markScenarioRequiresMoreRuns = func(context.Context, int) (bool, error) {
				moreRunsCalls++
				return true, nil
			}
			selector.dependencies.markScenarioFailedFrom = func(_ context.Context, id int, expectedState string) (bool, error) {
				failureCalls++
				if id != 31 || expectedState != coredb.ScenarioStatePostProcessing {
					t.Fatalf("failure guard id/state = %d/%q", id, expectedState)
				}
				return test.failureChanged, test.failureErr
			}

			err := selector.handlePostProcessing(context.Background(), 31)
			if !errors.Is(err, placeholderErr) {
				t.Fatalf("handler error = %v, want placeholder error", err)
			}
			if test.failureErr != nil && !errors.Is(err, test.failureErr) {
				t.Fatalf("handler error = %v, want persistence error to be preserved", err)
			}
			if finishedCalls != 0 || moreRunsCalls != 0 {
				t.Fatalf("placeholder error applied result: finished=%d more-runs=%d", finishedCalls, moreRunsCalls)
			}
			if failureCalls != 1 {
				t.Fatalf("failure write calls = %d, want 1", failureCalls)
			}
		})
	}
}

func TestHandlePostProcessingCancellationNeverWritesState(t *testing.T) {
	tests := []struct {
		name              string
		confidenceReached bool
		placeholderErr    error
		cancelContext     bool
	}{
		{name: "cancelled before successful result write", confidenceReached: true, cancelContext: true},
		{name: "cancelled placeholder", confidenceReached: true, placeholderErr: errors.New("placeholder interrupted"), cancelContext: true},
		{name: "placeholder returns cancellation", placeholderErr: context.Canceled},
		{name: "placeholder returns deadline", placeholderErr: context.DeadlineExceeded},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selector := newBasicScenarioSelectorForTest()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stateWrites := 0
			selector.dependencies.doPostProcessing = func(context.Context, int) (bool, error) {
				if test.cancelContext {
					cancel()
				}
				return test.confidenceReached, test.placeholderErr
			}
			selector.dependencies.markScenarioFinished = func(context.Context, int) (bool, error) {
				stateWrites++
				return true, nil
			}
			selector.dependencies.markScenarioRequiresMoreRuns = func(context.Context, int) (bool, error) {
				stateWrites++
				return true, nil
			}
			selector.dependencies.markScenarioFailedFrom = func(context.Context, int, string) (bool, error) {
				stateWrites++
				return true, nil
			}

			err := selector.handlePostProcessing(ctx, 31)
			wantErr := test.placeholderErr
			if test.cancelContext {
				wantErr = context.Canceled
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("handler error = %v, want cancellation %v", err, wantErr)
			}
			if stateWrites != 0 {
				t.Fatalf("workflow cancellation performed %d state writes", stateWrites)
			}
		})
	}
}

func TestBasicScenarioSelectorPlaceholders(t *testing.T) {
	t.Run("runner validation and success", func(t *testing.T) {
		if err := createSimulationRunnerJob(nil, 1); err == nil {
			t.Fatal("runner placeholder accepted nil context")
		}
		if err := createSimulationRunnerJob(context.Background(), 0); err == nil {
			t.Fatal("runner placeholder accepted non-positive id")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := createSimulationRunnerJob(ctx, 1); !errors.Is(err, context.Canceled) {
			t.Fatalf("runner placeholder error = %v, want context cancellation", err)
		}
		if err := createSimulationRunnerJob(context.Background(), 1); err != nil {
			t.Fatalf("runner placeholder returned unexpected error: %v", err)
		}
	})

	t.Run("post-processing validation and synthetic success", func(t *testing.T) {
		if _, err := doPostProcessing(nil, 1); err == nil {
			t.Fatal("post-processing placeholder accepted nil context")
		}
		if _, err := doPostProcessing(context.Background(), 0); err == nil {
			t.Fatal("post-processing placeholder accepted non-positive id")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := doPostProcessing(ctx, 1); !errors.Is(err, context.Canceled) {
			t.Fatalf("post-processing placeholder error = %v, want context cancellation", err)
		}
		confidenceReached, err := doPostProcessing(context.Background(), 1)
		if err != nil || !confidenceReached {
			t.Fatalf("post-processing placeholder returned confidence=%t error=%v, want true/nil", confidenceReached, err)
		}
	})
}
