package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/coredb"
	"github.com/D4NS3U/cbse/scenario-manager/internal/translatorconfig"
)

const (
	// basicScenarioSelectorDelay deliberately slows the first selector down. The
	// delay starts after an iteration finishes, so slow work never creates a
	// backlog of missed timer ticks for the worker to catch up on.
	basicScenarioSelectorDelay = 5 * time.Second
	// basicScenarioSelectorIterationTimeout prevents one database or workflow
	// call from holding the single selector worker indefinitely, provided that
	// dependency observes context cancellation as required by its contract.
	basicScenarioSelectorIterationTimeout = 30 * time.Second
)

// basicScenarioSelectorDependencies contains every operation that one selector
// iteration may perform. Keeping these functions on an instance makes the
// production wiring explicit and lets tests provide isolated fakes without
// replacing mutable package-level functions.
type basicScenarioSelectorDependencies struct {
	now                                  func() time.Time
	wait                                 func(context.Context, time.Duration) error
	nextStaleUnpublishedTranslationClaim func(context.Context, time.Time) (*coredb.TranslationRecoveryCandidate, error)
	recoverUnpublishedTranslationClaim   func(context.Context, int, int, time.Time) (bool, string, error)
	nextActionableScenario               func(context.Context) (*coredb.ScenarioActionCandidate, error)
	processScenarioTrans                 func(context.Context, int, communication.TranslationRequestPublisher) error
	markScenarioInProcessing             func(context.Context, int) (bool, error)
	createSimulationRunnerJob            func(context.Context, int) error
	doPostProcessing                     func(context.Context, int) (bool, error)
	markScenarioFinished                 func(context.Context, int) (bool, error)
	markScenarioRequiresMoreRuns         func(context.Context, int) (bool, error)
	markScenarioFailedFrom               func(context.Context, int, string) (bool, error)
}

// basicScenarioSelector is one process-local, serial worker. Database state is
// the durable ownership mechanism; this object intentionally holds no row lock,
// queue, leader flag, or cross-replica coordination state.
type basicScenarioSelector struct {
	publisher              communication.TranslationRequestPublisher
	publishRecoveryTimeout time.Duration
	delay                  time.Duration
	iterationTimeout       time.Duration
	dependencies           basicScenarioSelectorDependencies
}

// StartBasicScenarioSelector validates and launches exactly one joinable BSSL
// worker. The returned channel closes after the worker has observed shutdown
// and its active context-aware dependency has returned.
func StartBasicScenarioSelector(
	ctx context.Context,
	publisher communication.TranslationRequestPublisher,
) (<-chan struct{}, error) {
	if ctx == nil {
		return nil, fmt.Errorf("selector context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("selector context must be active: %w", err)
	}
	if publisher == nil {
		return nil, fmt.Errorf("translation request publisher must not be nil")
	}

	// Configuration is loaded once here. All later iterations reuse this value,
	// which avoids repeated environment reads and repeated fallback log lines.
	selector := &basicScenarioSelector{
		publisher:              publisher,
		publishRecoveryTimeout: translatorconfig.LoadPublishRecoveryTimeout(),
		delay:                  basicScenarioSelectorDelay,
		iterationTimeout:       basicScenarioSelectorIterationTimeout,
		dependencies:           productionBasicScenarioSelectorDependencies(),
	}

	return selector.start(ctx), nil
}

// productionBasicScenarioSelectorDependencies connects the small selector
// algorithm to the existing workflows. The runner and post-processing entries
// are intentionally local placeholders in this first version.
func productionBasicScenarioSelectorDependencies() basicScenarioSelectorDependencies {
	return basicScenarioSelectorDependencies{
		now:                                  time.Now,
		wait:                                 waitForBasicScenarioSelectorDelay,
		nextStaleUnpublishedTranslationClaim: coredb.NextStaleUnpublishedTranslationClaim,
		recoverUnpublishedTranslationClaim:   RecoverUnpublishedTranslationClaim,
		nextActionableScenario:               coredb.NextActionableScenario,
		processScenarioTrans:                 ProcessScenarioTrans,
		markScenarioInProcessing:             coredb.MarkScenarioInProcessing,
		createSimulationRunnerJob:            createSimulationRunnerJob,
		doPostProcessing:                     doPostProcessing,
		markScenarioFinished:                 coredb.MarkScenarioFinished,
		markScenarioRequiresMoreRuns:         coredb.MarkScenarioRequiresMoreRuns,
		markScenarioFailedFrom:               coredb.MarkScenarioFailedFrom,
	}
}

// start launches the worker and provides a simple join handle. Only the worker
// owns and closes done, so callers can safely wait on it without coordinating a
// second close path.
func (s *basicScenarioSelector) start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.run(ctx)
	}()
	return done
}

// run executes one iteration at a time. Each iteration receives its own
// deadline, while the delay uses the long-lived root context so cancelling the
// just-finished child context cannot accidentally skip the five-second wait.
func (s *basicScenarioSelector) run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		iterationCtx, cancel := context.WithTimeout(ctx, s.iterationTimeout)
		err := s.processNextScenario(iterationCtx)
		cancel()

		// Cancellation and deadline errors are normal workflow control. Other
		// errors are reported once here, at the worker boundary.
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("operation=%q error_class=%q error=%v", "basic_scenario_selector_iteration", "workflow", err)
		}

		// A root cancellation during an iteration ends the worker immediately;
		// there is no shutdown delay and no replacement iteration.
		if ctx.Err() != nil {
			return
		}

		if err := s.dependencies.wait(ctx, s.delay); err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				log.Printf("operation=%q error_class=%q error=%v", "basic_scenario_selector_wait", "workflow", err)
			}
			return
		}
	}
}

// processNextScenario performs one recovery-first selection pass and considers
// at most one row. A stale result or error always consumes this iteration; the
// selector never hides a race by silently moving on to a different scenario.
func (s *basicScenarioSelector) processNextScenario(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("selector iteration context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// One clock reading produces one cutoff. Passing the exact same value to
	// discovery and recovery prevents the eligibility boundary from drifting
	// between the read and guarded update.
	claimedBefore := s.dependencies.now().Add(-s.publishRecoveryTimeout)
	recoveryCandidate, err := s.dependencies.nextStaleUnpublishedTranslationClaim(ctx, claimedBefore)
	if err != nil {
		return fmt.Errorf("discover stale unconfirmed translation publish: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if recoveryCandidate != nil {
		changed, finalState, err := s.dependencies.recoverUnpublishedTranslationClaim(
			ctx,
			recoveryCandidate.ID,
			recoveryCandidate.TranslationAttempt,
			claimedBefore,
		)
		if err != nil {
			return fmt.Errorf("recover scenario %d translation attempt %d: %w", recoveryCandidate.ID, recoveryCandidate.TranslationAttempt, err)
		}
		if !changed {
			log.Printf("operation=%q scenario_id=%d expected_state=%q translation_attempt=%d error_class=%q", "recover_unconfirmed_translation_publish", recoveryCandidate.ID, coredb.ScenarioStateScheduled, recoveryCandidate.TranslationAttempt, "stale")
			return nil
		}
		log.Printf("operation=%q scenario_id=%d expected_state=%q resulting_state=%q translation_attempt=%d", "recover_unconfirmed_translation_publish", recoveryCandidate.ID, coredb.ScenarioStateScheduled, finalState, recoveryCandidate.TranslationAttempt)
		return nil
	}

	candidate, err := s.dependencies.nextActionableScenario(ctx)
	if err != nil {
		return fmt.Errorf("select next actionable scenario: %w", err)
	}
	if candidate == nil {
		// Idle iterations are intentionally silent.
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	switch candidate.State {
	case coredb.ScenarioStateCreated:
		return s.handleCreated(ctx, candidate.ID)
	case coredb.ScenarioStateStartingRunners:
		return s.handleStartingRunners(ctx, candidate.ID)
	case coredb.ScenarioStatePostProcessing:
		return s.handlePostProcessing(ctx, candidate.ID)
	default:
		// The production query cannot return another state, but keeping dispatch
		// defensive protects the row if a future lookup or test double does.
		log.Printf("operation=%q scenario_id=%d expected_state=%q error_class=%q", "dispatch_actionable_scenario", candidate.ID, candidate.State, "validation")
		return nil
	}
}

// handleCreated delegates the entire claim and publish workflow to the existing
// Translator handoff. The selector must not second-guess that workflow's state
// or mark the scenario failed when publication returns an error.
func (s *basicScenarioSelector) handleCreated(ctx context.Context, scenarioID int) error {
	if err := s.dependencies.processScenarioTrans(ctx, scenarioID, s.publisher); err != nil {
		return fmt.Errorf("translator handoff for scenario %d: %w", scenarioID, err)
	}
	return nil
}

// handleStartingRunners first takes a durable InProcessing claim and only then
// invokes the local placeholder. That ordering leaves a safe seam for a future
// idempotent Kubernetes Job integration.
func (s *basicScenarioSelector) handleStartingRunners(ctx context.Context, scenarioID int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	changed, err := s.dependencies.markScenarioInProcessing(ctx, scenarioID)
	if err != nil {
		return fmt.Errorf("claim scenario %d for runner startup: %w", scenarioID, err)
	}
	if !changed {
		log.Printf("operation=%q scenario_id=%d expected_state=%q resulting_state=%q error_class=%q", "claim_runner_startup", scenarioID, coredb.ScenarioStateStartingRunners, coredb.ScenarioStateInProcessing, "stale")
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	placeholderErr := s.dependencies.createSimulationRunnerJob(ctx, scenarioID)
	if placeholderErr == nil {
		// V1 deliberately leaves the durable row in InProcessing. No real runner
		// or completion signal exists in this change.
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if errors.Is(placeholderErr, context.Canceled) || errors.Is(placeholderErr, context.DeadlineExceeded) {
		return placeholderErr
	}

	failed, persistenceErr := s.dependencies.markScenarioFailedFrom(ctx, scenarioID, coredb.ScenarioStateInProcessing)
	if persistenceErr != nil {
		return fmt.Errorf(
			"runner placeholder and failure persistence for scenario %d: %w",
			scenarioID,
			errors.Join(placeholderErr, persistenceErr),
		)
	}
	if !failed {
		log.Printf("operation=%q scenario_id=%d expected_state=%q resulting_state=%q error_class=%q", "fail_runner_placeholder", scenarioID, coredb.ScenarioStateInProcessing, coredb.ScenarioStateFailed, "stale")
	} else {
		log.Printf("operation=%q scenario_id=%d expected_state=%q resulting_state=%q error_class=%q", "fail_runner_placeholder", scenarioID, coredb.ScenarioStateInProcessing, coredb.ScenarioStateFailed, "placeholder")
	}
	return fmt.Errorf("runner placeholder for scenario %d: %w", scenarioID, placeholderErr)
}

// handlePostProcessing invokes the side-effect-free v1 placeholder and then
// applies one guarded result. A real side-effecting service will need its own
// durable in-flight claim before replacing this scaffold.
func (s *basicScenarioSelector) handlePostProcessing(ctx context.Context, scenarioID int) error {
	confidenceReached, placeholderErr := s.dependencies.doPostProcessing(ctx, scenarioID)
	if placeholderErr != nil {
		// The error takes precedence over confidenceReached, even if a faulty or
		// future dependency returns true and an error together.
		if err := ctx.Err(); err != nil {
			return err
		}
		if errors.Is(placeholderErr, context.Canceled) || errors.Is(placeholderErr, context.DeadlineExceeded) {
			return placeholderErr
		}

		failed, persistenceErr := s.dependencies.markScenarioFailedFrom(ctx, scenarioID, coredb.ScenarioStatePostProcessing)
		if persistenceErr != nil {
			return fmt.Errorf(
				"post-processing placeholder and failure persistence for scenario %d: %w",
				scenarioID,
				errors.Join(placeholderErr, persistenceErr),
			)
		}
		if !failed {
			log.Printf("operation=%q scenario_id=%d expected_state=%q resulting_state=%q error_class=%q", "fail_post_processing_placeholder", scenarioID, coredb.ScenarioStatePostProcessing, coredb.ScenarioStateFailed, "stale")
		} else {
			log.Printf("operation=%q scenario_id=%d expected_state=%q resulting_state=%q error_class=%q", "fail_post_processing_placeholder", scenarioID, coredb.ScenarioStatePostProcessing, coredb.ScenarioStateFailed, "placeholder")
		}
		return fmt.Errorf("post-processing placeholder for scenario %d: %w", scenarioID, placeholderErr)
	}

	// A result calculated after cancellation must never decide a durable state.
	if err := ctx.Err(); err != nil {
		return err
	}

	var (
		changed        bool
		transitionErr  error
		resultingState string
	)
	if confidenceReached {
		resultingState = coredb.ScenarioStateFinished
		changed, transitionErr = s.dependencies.markScenarioFinished(ctx, scenarioID)
	} else {
		resultingState = coredb.ScenarioStateStartingRunners
		changed, transitionErr = s.dependencies.markScenarioRequiresMoreRuns(ctx, scenarioID)
	}
	if transitionErr != nil {
		// Keeping PostProcessing intact makes the calculation retryable. A failed
		// result write is not evidence that the scenario itself failed.
		return fmt.Errorf("persist post-processing result for scenario %d: %w", scenarioID, transitionErr)
	}
	if !changed {
		log.Printf("operation=%q scenario_id=%d expected_state=%q resulting_state=%q error_class=%q", "persist_post_processing_result", scenarioID, coredb.ScenarioStatePostProcessing, resultingState, "stale")
		return nil
	}

	log.Printf("operation=%q scenario_id=%d expected_state=%q resulting_state=%q", "persist_post_processing_result", scenarioID, coredb.ScenarioStatePostProcessing, resultingState)
	return nil
}

// waitForBasicScenarioSelectorDelay waits without leaking a timer and returns
// promptly when Scenario Manager begins shutdown.
func waitForBasicScenarioSelectorDelay(ctx context.Context, delay time.Duration) error {
	if ctx == nil {
		return fmt.Errorf("selector wait context must not be nil")
	}
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

// createSimulationRunnerJob is lifecycle scaffolding only. It records that the
// handoff point was reached but intentionally creates no Kubernetes Job or
// other external runner resource in BSSL v1.
func createSimulationRunnerJob(ctx context.Context, scenarioID int) error {
	if ctx == nil {
		return fmt.Errorf("runner placeholder context must not be nil")
	}
	if scenarioID <= 0 {
		return fmt.Errorf("scenario ID must be positive")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	log.Printf("operation=%q scenario_id=%d expected_state=%q error_class=%q", "create_simulation_runner_job", scenarioID, coredb.ScenarioStateInProcessing, "placeholder")
	return nil
}

// doPostProcessing is lifecycle scaffolding only. It reads no repetition or
// confidence data, calls no service, and always reports synthetic confidence
// success while its context remains active.
func doPostProcessing(ctx context.Context, scenarioID int) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("post-processing placeholder context must not be nil")
	}
	if scenarioID <= 0 {
		return false, fmt.Errorf("scenario ID must be positive")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	log.Printf("operation=%q scenario_id=%d expected_state=%q error_class=%q", "do_post_processing", scenarioID, coredb.ScenarioStatePostProcessing, "placeholder")
	return true, nil
}
