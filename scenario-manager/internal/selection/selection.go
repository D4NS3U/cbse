// Package selection implements the translation-request selection loop: one
// process-local serial worker that owns the Created -> Scheduled transition and
// the publication boundary.
//
// Each iteration runs recovery-first: it discovers stale unpublished Scheduled
// claims and applies persistence.RecoverUnpublishedTranslationClaim, then
// discovers the next Created scenario, claims it with
// persistence.ClaimScenarioForTranslation, re-applies lifecycle.AdmitExperiment
// on the live experiment, calls persistence.MarkTranslationPublishStarted,
// publishes via the TranslationRequestPublisher, and applies
// persistence.MarkScenarioTranslationRequestPublished on success or
// persistence.MarkScenarioTranslationPublishFailed on failure. A pre-publish
// gate rejection calls persistence.CancelUnpublishedTranslationClaim and does
// not call NATS.
//
// The loop owns Created -> Scheduled only. It must not handle StartingRunners
// (owned by the runnerstart scheduler) or PostProcessing (a boundary no-op in
// this branch). It holds no row lock, queue, leader flag, or cross-replica
// coordination state; database guarded transitions remain the durable
// ownership mechanism. Each iteration has a bounded timeout (30s) and a fixed
// delay (5s); cancellation and deadline errors are normal workflow control.
package selection

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/lifecycle"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
)

const (
	// iterationTimeout prevents one database, Kubernetes, or NATS call from
	// holding the single selector worker indefinitely, provided each dependency
	// observes context cancellation as required by its contract. It is the
	// canonical 30s selection-loop iteration timeout.
	iterationTimeout = 30 * time.Second
	// iterationDelay is the fixed delay between iterations. It starts after an
	// iteration finishes so slow work never creates a backlog of missed ticks. It
	// is the canonical 5s selection-loop delay.
	iterationDelay = 5 * time.Second
)

// Dependencies contains every operation one selection iteration may perform.
// Keeping these as function fields makes the production wiring explicit and
// lets tests provide isolated fakes without replacing mutable package-level
// functions.
type Dependencies struct {
	now                                     func() time.Time
	wait                                    func(context.Context, time.Duration) error
	nextStaleUnpublishedTranslationClaim    func(context.Context, time.Time) (*persistence.StaleTranslationClaim, error)
	recoverUnpublishedTranslationClaim      func(context.Context, int, int, time.Time) (bool, string, error)
	nextCreatedScenario                     func(context.Context) (*persistence.TranslationCandidate, error)
	claimScenario                           func(context.Context, int) (*persistence.ScenarioForTranslation, error)
	getExperiment                           func(context.Context, string, string) (*experimentalpha4.SimulationExperiment, error)
	markTranslationPublishStarted           func(context.Context, int, int) (bool, error)
	publish                                 func(context.Context, communication.ScenarioForTranslation) error
	markScenarioTranslationRequestPublished func(context.Context, int, int) (bool, error)
	markScenarioTranslationPublishFailed    func(context.Context, int, int) (bool, string, error)
	cancelUnpublishedTranslationClaim       func(context.Context, int, int) (bool, error)
}

// Selector is one process-local, serial worker. Database state is the durable
// ownership mechanism; this object intentionally holds no row lock, queue,
// leader flag, or cross-replica coordination state.
type Selector struct {
	publisher              communication.TranslationRequestPublisher
	publishRecoveryTimeout time.Duration
	delay                  time.Duration
	iterationTimeout       time.Duration
	deps                   Dependencies
}

// NewSelector validates and constructs a selector. publisher must be non-nil.
// publishRecoveryTimeout is the unpublished-claim staleness cutoff (loaded
// once from translatorconfig at startup). The delay and iteration timeout
// default to the canonical 5s and 30s; tests may shorten them.
func NewSelector(publisher communication.TranslationRequestPublisher, publishRecoveryTimeout time.Duration, deps Dependencies) (*Selector, error) {
	if publisher == nil {
		return nil, fmt.Errorf("translation request publisher must not be nil")
	}
	if publishRecoveryTimeout <= 0 {
		return nil, fmt.Errorf("publish recovery timeout must be positive")
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.wait == nil {
		deps.wait = waitDelay
	}
	return &Selector{
		publisher:              publisher,
		publishRecoveryTimeout: publishRecoveryTimeout,
		delay:                  iterationDelay,
		iterationTimeout:       iterationTimeout,
		deps:                   deps,
	}, nil
}

// Start launches exactly one joinable selector worker. The returned channel
// closes after the worker has observed shutdown. ctx must be non-nil and active.
func (s *Selector) Start(ctx context.Context) (<-chan struct{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("selector context must be active: %w", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.run(ctx)
	}()
	return done, nil
}

// run executes one iteration at a time. Each iteration receives its own
// deadline, while the delay uses the long-lived root context so cancelling the
// just-finished child context cannot accidentally skip the wait.
func (s *Selector) run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		iterationCtx, cancel := context.WithTimeout(ctx, s.iterationTimeout)
		err := s.processNext(iterationCtx)
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("operation=%q error_class=%q error=%v", "alpha4_selection_iteration", "workflow", err)
		}
		if ctx.Err() != nil {
			return
		}
		if err := s.deps.wait(ctx, s.delay); err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				log.Printf("operation=%q error_class=%q error=%v", "alpha4_selection_wait", "workflow", err)
			}
			return
		}
	}
}

// processNext performs one recovery-first selection pass and considers at most
// one row. A stale result or error always consumes this iteration; the
// selector never hides a race by silently moving on to a different scenario.
func (s *Selector) processNext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// One clock reading produces one cutoff. Passing the exact same value to
	// discovery and recovery prevents the eligibility boundary from drifting
	// between the read and the guarded update.
	claimedBefore := s.deps.now().Add(-s.publishRecoveryTimeout)
	recovery, err := s.deps.nextStaleUnpublishedTranslationClaim(ctx, claimedBefore)
	if err != nil {
		return fmt.Errorf("discover stale unconfirmed translation publish: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if recovery != nil {
		changed, finalState, rerr := s.deps.recoverUnpublishedTranslationClaim(ctx, recovery.ID, recovery.TranslationAttempt, claimedBefore)
		if rerr != nil {
			return fmt.Errorf("recover scenario %d translation attempt %d: %w", recovery.ID, recovery.TranslationAttempt, rerr)
		}
		if !changed {
			log.Printf("operation=%q scenario_id=%d expected_state=%q translation_attempt=%d error_class=%q", "alpha4_recover_unconfirmed_publish", recovery.ID, persistence.ScenarioStateScheduled, recovery.TranslationAttempt, "stale")
			return nil
		}
		log.Printf("operation=%q scenario_id=%d expected_state=%q resulting_state=%q translation_attempt=%d", "alpha4_recover_unconfirmed_publish", recovery.ID, persistence.ScenarioStateScheduled, finalState, recovery.TranslationAttempt)
		return nil
	}

	candidate, err := s.deps.nextCreatedScenario(ctx)
	if err != nil {
		return fmt.Errorf("discover next created scenario: %w", err)
	}
	if candidate == nil {
		// Idle iterations are intentionally silent.
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.handleCreated(ctx, candidate)
}

// handleCreated claims the discovered Created scenario, re-applies the
// lifecycle gate on the live experiment, and publishes the translation request
// with the publication boundary. A pre-publish gate rejection cancels the
// unpublished claim and does not call NATS.
func (s *Selector) handleCreated(ctx context.Context, candidate *persistence.TranslationCandidate) error {
	claimed, err := s.deps.claimScenario(ctx, candidate.ID)
	if err != nil {
		return fmt.Errorf("claim scenario %d for translation: %w", candidate.ID, err)
	}
	if claimed == nil {
		log.Printf("operation=%q scenario_id=%d error_class=%q", "alpha4_claim_translation", candidate.ID, "stale")
		return nil
	}
	log.Printf("operation=%q scenario_id=%d project=%q/%q old=%s new=%s attempt=%d", "alpha4_claim_translation", claimed.ID, claimed.ProjectNamespace, claimed.ProjectName, persistence.ScenarioStateCreated, persistence.ScenarioStateScheduled, claimed.TranslationAttempt)

	// Re-apply the lifecycle gate on the live experiment. A terminal or
	// unavailable experiment is a pre-publish gate rejection: cancel the
	// unpublished claim (restoring Created and refunding the attempt) and do
	// not call NATS. A transient experiment fetch error is a retryable
	// dependency failure: leave the claim for stale-claim recovery and return.
	exp, err := s.deps.getExperiment(ctx, claimed.ProjectNamespace, claimed.ProjectName)
	if err != nil {
		return fmt.Errorf("fetch experiment %s/%s for gate: %w", claimed.ProjectNamespace, claimed.ProjectName, err)
	}
	decision := lifecycle.AdmitExperiment(exp)
	if !decision.IsAdmitted() {
		cancelled, cerr := s.deps.cancelUnpublishedTranslationClaim(ctx, claimed.ID, claimed.TranslationAttempt)
		if cerr != nil {
			return fmt.Errorf("cancel unpublished claim for scenario %d after gate %s: %w", claimed.ID, decision, cerr)
		}
		if cancelled {
			log.Printf("operation=%q scenario_id=%d gate=%s error_class=%q", "alpha4_gate_rejection_cancel", claimed.ID, decision, "gate")
		} else {
			log.Printf("operation=%q scenario_id=%d gate=%s error_class=%q", "alpha4_gate_rejection_cancel", claimed.ID, decision, "stale")
		}
		return nil
	}

	// Mark publish started: only the owner of the unpublished Scheduled claim
	// may invoke NATS publication. A false result is a stale no-op (another
	// replica or recovery owns the row); do not publish.
	started, err := s.deps.markTranslationPublishStarted(ctx, claimed.ID, claimed.TranslationAttempt)
	if err != nil {
		return fmt.Errorf("mark publish started for scenario %d attempt %d: %w", claimed.ID, claimed.TranslationAttempt, err)
	}
	if !started {
		log.Printf("operation=%q scenario_id=%d attempt=%d error_class=%q", "alpha4_mark_publish_started", claimed.ID, claimed.TranslationAttempt, "stale")
		return nil
	}

	if err := s.deps.publish(ctx, toCommunication(*claimed)); err != nil {
		log.Printf("operation=%q scenario_id=%d attempt=%d error_class=%q error=%v", "alpha4_publish_translation", claimed.ID, claimed.TranslationAttempt, "publish", err)
		changed, finalState, ferr := s.deps.markScenarioTranslationPublishFailed(ctx, claimed.ID, claimed.TranslationAttempt)
		if ferr != nil {
			return fmt.Errorf("persist publish failure for scenario %d attempt %d: %w", claimed.ID, claimed.TranslationAttempt, ferr)
		}
		if changed {
			log.Printf("operation=%q scenario_id=%d old=%s new=%s attempt=%d", "alpha4_publish_failed", claimed.ID, persistence.ScenarioStateScheduled, finalState, claimed.TranslationAttempt)
		} else {
			log.Printf("operation=%q scenario_id=%d attempt=%d error_class=%q", "alpha4_publish_failed", claimed.ID, claimed.TranslationAttempt, "stale")
		}
		return err
	}

	published, err := s.deps.markScenarioTranslationRequestPublished(ctx, claimed.ID, claimed.TranslationAttempt)
	if err != nil {
		return fmt.Errorf("mark publish confirmed for scenario %d attempt %d: %w", claimed.ID, claimed.TranslationAttempt, err)
	}
	if !published {
		log.Printf("operation=%q scenario_id=%d attempt=%d error_class=%q", "alpha4_mark_publish_confirmed", claimed.ID, claimed.TranslationAttempt, "stale")
		return nil
	}
	log.Printf("operation=%q scenario_id=%d attempt=%d", "alpha4_publish_confirmed", claimed.ID, claimed.TranslationAttempt)
	return nil
}

// toCommunication converts the persistence claim projection to the
// transport-neutral communication projection the publisher consumes. The two
// types have identical fields; the conversion keeps the selection package
// decoupled from the persistence struct layout.
func toCommunication(c persistence.ScenarioForTranslation) communication.ScenarioForTranslation {
	return communication.ScenarioForTranslation{
		ID:                 c.ID,
		ProjectNamespace:   c.ProjectNamespace,
		ProjectName:        c.ProjectName,
		TranslationAttempt: c.TranslationAttempt,
		RecipeInfo:         c.RecipeInfo,
		ConfidenceMetric:   c.ConfidenceMetric,
	}
}

// waitDelay waits without leaking a timer and returns promptly on shutdown.
func waitDelay(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
