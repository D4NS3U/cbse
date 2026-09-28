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

// Package evaluationpub implements the evaluation-request publication loop:
// one process-local serial worker that owns the PostProcessing evaluation
// publication boundary.
//
// Each iteration discovers the next PostProcessing scenario, claims it with
// persistence.ClaimScenarioForEvaluation (the exact-attempt claim), loads the
// round-scoped projection, re-applies lifecycle.AdmitExperiment on the live
// experiment, ensures the experiment's per-experiment PPS consumer exists
// (admission-time creation), calls persistence.MarkEvaluationPublishStarted
// for the exact attempt, publishes via the EvaluationRequestPublisher, and
// applies persistence.MarkEvaluationRequestPublished on PubAck. A pre-publish
// gate rejection does not call NATS and leaves the row claimable again on the
// next pass. A publish failure or lost PubAck leaves the row claimable again
// on the next pass; the exact-attempt publication guards make retries safe
// (a stale attempt number can never mark publication for a newer claim).
//
// The loop owns evaluation publication only. It must not handle StartingRunners
// (owned by the runnerstart scheduler) or InProcessing (owned by the
// observation scheduler). It holds no row lock, queue, leader flag, or
// cross-replica coordination state; database guarded transitions remain the
// durable ownership mechanism. Each iteration has a bounded timeout (30s) and
// a fixed delay (5s); cancellation and deadline errors are normal workflow
// control.
package evaluationpub

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/lifecycle"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
)

const (
	// iterationTimeout prevents one database, Kubernetes, or NATS call from
	// holding the single publication worker indefinitely, provided each
	// dependency observes context cancellation as required by its contract. It
	// is the canonical 30s evaluation-publication iteration timeout.
	iterationTimeout = 30 * time.Second
	// iterationDelay is the fixed delay between iterations. It starts after an
	// iteration finishes so slow work never creates a backlog of missed ticks.
	// It is the canonical 5s evaluation-publication delay.
	iterationDelay = 5 * time.Second
)

// PostProcessingCandidate is the small projection returned by
// PostProcessing-scenario discovery for the evaluation publication loop. It
// carries the positive scenario id and the static (namespace, name) project
// identity so the loop can fetch the live experiment and re-apply the
// lifecycle gate before publishing. Discovery only observes the row; the
// caller claims the exact id with persistence.ClaimScenarioForEvaluation.
type PostProcessingCandidate struct {
	ID               int
	ProjectNamespace string
	ProjectName      string
}

// EvaluationProjection is the round-scoped projection loaded after a claim for
// the evaluation request payload. NumberOfReps is the cross-round computed
// total (the replications so far); ConfidenceMetric is the nullable
// per-scenario precision threshold (nil when the EDS batch supplied none).
type EvaluationProjection struct {
	ScenarioID       int
	ProjectNamespace string
	ProjectName      string
	RunnerRound      int
	NumberOfReps     int
	ConfidenceMetric *float64
}

// Dependencies contains every operation one publication iteration may perform.
// Keeping these as function fields makes the production wiring explicit and
// lets tests provide isolated fakes without replacing mutable package-level
// functions.
type Dependencies struct {
	now                            func() time.Time
	wait                           func(context.Context, time.Duration) error
	nextPostProcessingScenario     func(context.Context) (*PostProcessingCandidate, error)
	claimScenarioForEvaluation     func(context.Context, int) (int, bool, error)
	loadEvaluationProjection       func(context.Context, int) (*EvaluationProjection, error)
	getExperiment                  func(context.Context, string, string) (*experimentalpha4.SimulationExperiment, error)
	ensurePPSConsumer              func(ctx context.Context, uid, namespace, project string) error
	markEvaluationPublishStarted   func(context.Context, int, int) (bool, error)
	publish                        func(context.Context, communication.ScenarioForEvaluation) error
	markEvaluationRequestPublished func(context.Context, int, int) (bool, error)
}

// Publisher is one process-local, serial worker. Database state is the durable
// ownership mechanism; this object intentionally holds no row lock, queue,
// leader flag, or cross-replica coordination state.
type Publisher struct {
	publisher        communication.EvaluationRequestPublisher
	delay            time.Duration
	iterationTimeout time.Duration
	deps             Dependencies
}

// NewPublisher validates and constructs a publisher. publisher must be
// non-nil. The delay and iteration timeout default to the canonical 5s and
// 30s; tests may shorten them.
func NewPublisher(publisher communication.EvaluationRequestPublisher, deps Dependencies) (*Publisher, error) {
	if publisher == nil {
		return nil, fmt.Errorf("evaluation request publisher must not be nil")
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.wait == nil {
		deps.wait = waitDelay
	}
	return &Publisher{
		publisher:        publisher,
		delay:            iterationDelay,
		iterationTimeout: iterationTimeout,
		deps:             deps,
	}, nil
}

// Start launches exactly one joinable publication worker. The returned channel
// closes after the worker has observed shutdown. ctx must be non-nil and active.
func (p *Publisher) Start(ctx context.Context) (<-chan struct{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("publisher context must be active: %w", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.run(ctx)
	}()
	return done, nil
}

// run executes one iteration at a time. Each iteration receives its own
// deadline, while the delay uses the long-lived root context so cancelling the
// just-finished child context cannot accidentally skip the wait.
func (p *Publisher) run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		iterationCtx, cancel := context.WithTimeout(ctx, p.iterationTimeout)
		err := p.processNext(iterationCtx)
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("operation=%q error_class=%q error=%v", "alpha4_evaluation_publication_iteration", "workflow", err)
		}
		if ctx.Err() != nil {
			return
		}
		if err := p.deps.wait(ctx, p.delay); err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				log.Printf("operation=%q error_class=%q error=%v", "alpha4_evaluation_publication_wait", "workflow", err)
			}
			return
		}
	}
}

// processNext performs one evaluation-publication pass and considers at most
// one row. A stale result or error always consumes this iteration; the
// publisher never hides a race by silently moving on to a different scenario.
func (p *Publisher) processNext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	candidate, err := p.deps.nextPostProcessingScenario(ctx)
	if err != nil {
		return fmt.Errorf("discover next post-processing scenario: %w", err)
	}
	if candidate == nil {
		// Idle iterations are intentionally silent.
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.handleDiscovered(ctx, candidate)
}

// handleDiscovered claims the discovered PostProcessing scenario, re-applies
// the lifecycle gate on the live experiment, ensures the per-experiment PPS
// consumer, and publishes the evaluation request with the publication
// boundary. A gate rejection or publish failure does not call any further
// mutation; the exact-attempt guards leave the row claimable again on the
// next pass.
func (p *Publisher) handleDiscovered(ctx context.Context, candidate *PostProcessingCandidate) error {
	claimedAttempt, ok, err := p.deps.claimScenarioForEvaluation(ctx, candidate.ID)
	if err != nil {
		return fmt.Errorf("claim scenario %d for evaluation: %w", candidate.ID, err)
	}
	if !ok {
		log.Printf("operation=%q scenario_id=%d error_class=%q", "alpha4_claim_evaluation", candidate.ID, "stale")
		return nil
	}
	log.Printf("operation=%q scenario_id=%d state=%s attempt=%d", "alpha4_claim_evaluation", candidate.ID, persistence.ScenarioStatePostProcessing, claimedAttempt)

	proj, err := p.deps.loadEvaluationProjection(ctx, candidate.ID)
	if err != nil {
		return fmt.Errorf("load evaluation projection for scenario %d: %w", candidate.ID, err)
	}
	if proj == nil {
		log.Printf("operation=%q scenario_id=%d error_class=%q", "alpha4_load_evaluation_projection", candidate.ID, "stale")
		return nil
	}
	if err := validateProjection(proj); err != nil {
		// The row stays PostProcessing and claimable on the next pass; the
		// fixed cadence (not a hot loop) republishes once the projection is
		// valid. A NULL confidence_metric is a permanent validation failure
		// that the EDS intake is expected to have supplied.
		log.Printf("operation=%q scenario_id=%d attempt=%d error_class=%q error=%v", "alpha4_evaluate_projection", candidate.ID, claimedAttempt, "invalid", err)
		return fmt.Errorf("evaluation request for scenario %d attempt %d: %w", candidate.ID, claimedAttempt, err)
	}

	// Re-apply the lifecycle gate on the live experiment. A terminal or
	// unavailable experiment is a pre-publish gate rejection: do not call
	// NATS and leave the row claimable (the terminal sweep or a later pass
	// resolves it). A transient experiment fetch error is a retryable
	// dependency failure: return and let the next pass retry.
	exp, err := p.deps.getExperiment(ctx, proj.ProjectNamespace, proj.ProjectName)
	if err != nil {
		return fmt.Errorf("fetch experiment %s/%s for gate: %w", proj.ProjectNamespace, proj.ProjectName, err)
	}
	decision := lifecycle.AdmitExperiment(exp)
	if !decision.IsAdmitted() {
		log.Printf("operation=%q scenario_id=%d attempt=%d gate=%s error_class=%q", "alpha4_gate_rejection", candidate.ID, claimedAttempt, decision, "gate")
		return nil
	}

	// Ensure the per-experiment PPS consumer exists at admission: the
	// PostProcessingService attaches to this durable for the experiment's
	// evaluation requests. A missing consumer is created; a matching consumer
	// is success; an ownership collision is returned so the iteration retries.
	if err := p.deps.ensurePPSConsumer(ctx, string(exp.UID), proj.ProjectNamespace, proj.ProjectName); err != nil {
		return fmt.Errorf("ensure pps consumer for scenario %d experiment %s: %w", candidate.ID, exp.UID, err)
	}

	// Mark publish started: only the owner of the exact PostProcessing
	// evaluation attempt may invoke NATS publication. A false result is a
	// stale no-op (another replica or a newer claim owns the row); do not
	// publish.
	started, err := p.deps.markEvaluationPublishStarted(ctx, candidate.ID, claimedAttempt)
	if err != nil {
		return fmt.Errorf("mark publish started for scenario %d attempt %d: %w", candidate.ID, claimedAttempt, err)
	}
	if !started {
		log.Printf("operation=%q scenario_id=%d attempt=%d error_class=%q", "alpha4_mark_evaluation_publish_started", candidate.ID, claimedAttempt, "stale")
		return nil
	}

	if err := p.deps.publish(ctx, toCommunication(proj, claimedAttempt, string(exp.UID))); err != nil {
		// Publish failure or lost PubAck: leave the row claimable again on the
		// next pass (no attempt-consuming failure path exists for evaluation;
		// the exact-attempt guards make the retry safe).
		log.Printf("operation=%q scenario_id=%d attempt=%d error_class=%q error=%v", "alpha4_publish_evaluation", candidate.ID, claimedAttempt, "publish", err)
		return err
	}

	published, err := p.deps.markEvaluationRequestPublished(ctx, candidate.ID, claimedAttempt)
	if err != nil {
		return fmt.Errorf("mark publish confirmed for scenario %d attempt %d: %w", candidate.ID, claimedAttempt, err)
	}
	if !published {
		log.Printf("operation=%q scenario_id=%d attempt=%d error_class=%q", "alpha4_mark_evaluation_publish_confirmed", candidate.ID, claimedAttempt, "stale")
		return nil
	}
	log.Printf("operation=%q scenario_id=%d attempt=%d", "alpha4_evaluation_publish_confirmed", candidate.ID, claimedAttempt)
	return nil
}

// validateProjection checks the round-scoped fields the wire contract requires
// before the exact-attempt publish-start marker is written.
func validateProjection(proj *EvaluationProjection) error {
	if proj.ScenarioID <= 0 {
		return fmt.Errorf("scenario id %d must be positive", proj.ScenarioID)
	}
	if proj.RunnerRound < 1 {
		return fmt.Errorf("runner round %d must be at least 1", proj.RunnerRound)
	}
	if proj.NumberOfReps < 1 {
		return fmt.Errorf("reps so far %d must be at least 1", proj.NumberOfReps)
	}
	if proj.ConfidenceMetric == nil {
		return fmt.Errorf("confidence_metric is null: evaluation request requires a precision threshold")
	}
	eps := *proj.ConfidenceMetric
	if math.IsNaN(eps) || math.IsInf(eps, 0) {
		return fmt.Errorf("confidence_metric %v must be finite", eps)
	}
	if eps <= 0 {
		return fmt.Errorf("confidence_metric %v must be positive", eps)
	}
	return nil
}

// toCommunication converts the evaluation projection and claim attempt to the
// transport-neutral communication projection the publisher consumes.
func toCommunication(proj *EvaluationProjection, attempt int, experimentUID string) communication.ScenarioForEvaluation {
	return communication.ScenarioForEvaluation{
		ExperimentUID:     experimentUID,
		ProjectNamespace:  proj.ProjectNamespace,
		ProjectName:       proj.ProjectName,
		ScenarioID:        proj.ScenarioID,
		EvaluationAttempt: attempt,
		RunnerRound:       proj.RunnerRound,
		NumberOfReps:      proj.NumberOfReps,
		ConfidenceMetric:  *proj.ConfidenceMetric,
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
