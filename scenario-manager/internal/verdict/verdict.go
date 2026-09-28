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

// Package verdict implements the alpha4 PPS-evaluation workflow as a
// communication.PPSEvaluationHandler. The workflow owns the semantic decision
// for one evaluation verdict after the transport adapter has validated subject
// shape, JSON payload shape, and subject/payload identity and applied the
// lifecycle gate.
//
// The workflow fetches the live experiment, requires the payload's
// experiment_uid to match the live experiment's UID (a mismatch is permanent
// poison: ACK, no mutation), reads the scenario's current state and
// runner_round, and treats a missing scenario or a verdict for a different
// round than the scenario's current runner_round as a stale no-op (Handled,
// ACK, no mutation). It then applies the guarded verdict transition through
// the S1 persistence primitives:
//
//   - met                -> persistence.MarkScenarioFinished
//   - additional_runners -> persistence.ClaimScenarioForEvaluationRound
//   - stop_unmet         -> persistence.MarkScenarioFailedFrom(PostProcessing)
//
// Every transition is guarded on the row's current PostProcessing state, so a
// stale verdict (the row already finished, failed, or began a new round) is a
// false transition and a Handled no-op. A transient database or Kubernetes
// lookup failure returns PPSEvaluationRetry so the adapter NAKs for
// redelivery.
package verdict

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Handler is the alpha4 PPS-evaluation workflow. It fetches the live
// experiment for the identity check, reads the scenario's current round, and
// applies the guarded verdict transition. It satisfies the
// communication.PPSEvaluationHandler func type via its Handle method.
//
// The database surfaces are function fields (the production wiring binds them
// to the persistence primitives in NewHandler) so tests exercise the workflow
// decision with isolated fakes; the loop logic itself depends only on the
// function fields.
type Handler struct {
	k8s        client.Client
	loadRound  func(ctx context.Context, scenarioID int) (state string, round int, found bool, err error)
	finish     func(ctx context.Context, scenarioID int) (bool, error)
	claimRound func(ctx context.Context, scenarioID, additionalRunners int) (int, bool, error)
	failFrom   func(ctx context.Context, scenarioID int, fromState string) (bool, error)
}

// NewHandler returns a verdict-workflow handler. k8s must have a scheme that
// knows the alpha4 SimulationExperiment. db backs both the scenario round
// read and the guarded verdict transitions.
func NewHandler(k8s client.Client, db persistence.DB) *Handler {
	return &Handler{
		k8s: k8s,
		loadRound: func(ctx context.Context, scenarioID int) (string, int, bool, error) {
			return loadScenarioRound(ctx, db, scenarioID)
		},
		finish: func(ctx context.Context, scenarioID int) (bool, error) {
			return persistence.MarkScenarioFinished(ctx, db, scenarioID)
		},
		claimRound: func(ctx context.Context, scenarioID, additionalRunners int) (int, bool, error) {
			return persistence.ClaimScenarioForEvaluationRound(ctx, db, scenarioID, additionalRunners)
		},
		failFrom: func(ctx context.Context, scenarioID int, fromState string) (bool, error) {
			return persistence.MarkScenarioFailedFrom(ctx, db, scenarioID, fromState)
		},
	}
}

// Handle applies the semantic PPS-evaluation workflow to one validated
// verdict. It returns PPSEvaluationHandled for every terminal semantic
// outcome (transition, duplicate, stale, or poison) and PPSEvaluationRetry
// only for a transient dependency failure.
func (h *Handler) Handle(ctx context.Context, m communication.PPSEvaluationMessage) communication.PPSEvaluationHandlingResult {
	if h == nil {
		return communication.PPSEvaluationHandlingResult{Status: communication.PPSEvaluationRetry, Reason: "verdict handler is nil"}
	}

	// Fetch the live experiment for the identity check. The transport adapter
	// already applied lifecycle.AdmitExperiment and only invokes the handler
	// for an admitted (live, non-deleting, InProgress) experiment; a NotFound
	// here is a race where the experiment disappeared between the adapter gate
	// and this fetch, which is permanent poison (ACK, no mutation). A transient
	// lookup error is retryable (NAK).
	exp := &experimentalpha4.SimulationExperiment{}
	if err := h.k8s.Get(ctx, types.NamespacedName{Namespace: m.ProjectNamespace, Name: m.ProjectName}, exp); err != nil {
		if apierrors.IsNotFound(err) {
			return handled("experiment gone: poison verdict")
		}
		return retry(fmt.Sprintf("fetch experiment: %v", err))
	}
	if m.ExperimentUID != string(exp.UID) {
		return handled(fmt.Sprintf("experiment UID %q != live experiment %q: poison", m.ExperimentUID, exp.UID))
	}

	// Read the scenario's current state and round. A missing scenario is a
	// stale no-op; a verdict for a different round than the scenario's current
	// runner_round is stale (a newer round is underway or the round already
	// finished or failed).
	state, round, found, err := h.loadRound(ctx, m.ScenarioID)
	if err != nil {
		return retry(fmt.Sprintf("load scenario round: %v", err))
	}
	if !found {
		return handled(fmt.Sprintf("scenario %d gone: stale verdict", m.ScenarioID))
	}
	if round != m.RunnerRound {
		return handled(fmt.Sprintf("verdict round %d != scenario runner_round %d: stale", m.RunnerRound, round))
	}

	switch m.Verdict {
	case communication.VerdictMet:
		return h.applyFinished(ctx, m, state)
	case communication.VerdictAdditionalRunners:
		return h.applyRoundClaim(ctx, m, state)
	case communication.VerdictStopUnmet:
		return h.applyStopUnmet(ctx, m, state)
	default:
		// The strict wire type rejects unknown enums on decode; this branch is
		// defensive against a future handler caller bypassing that check.
		return handled(fmt.Sprintf("unknown verdict %q: poison", m.Verdict))
	}
}

// applyFinished applies the guarded PostProcessing -> Finished transition for
// a met verdict. A false result is a stale no-op (the row left PostProcessing
// between the round read and the transition). A transient DB error is retryable.
func (h *Handler) applyFinished(ctx context.Context, m communication.PPSEvaluationMessage, state string) communication.PPSEvaluationHandlingResult {
	ok, err := h.finish(ctx, m.ScenarioID)
	if err != nil {
		return retry(fmt.Sprintf("db update failed: %v", err))
	}
	if !ok {
		return handled(fmt.Sprintf("stale met verdict: no longer PostProcessing (state %s)", state))
	}
	return handled("scenario -> Finished")
}

// applyRoundClaim applies the guarded PostProcessing -> StartingRunners
// round-claim transition for an additional_runners verdict. A false result is a
// stale no-op. A transient DB error is retryable.
func (h *Handler) applyRoundClaim(ctx context.Context, m communication.PPSEvaluationMessage, state string) communication.PPSEvaluationHandlingResult {
	nextRound, ok, err := h.claimRound(ctx, m.ScenarioID, m.AdditionalRunners)
	if err != nil {
		return retry(fmt.Sprintf("db update failed: %v", err))
	}
	if !ok {
		return handled(fmt.Sprintf("stale additional_runners verdict: no longer PostProcessing (state %s)", state))
	}
	return handled(fmt.Sprintf("round claim -> StartingRunners (round %d, %d additional runners)", nextRound, m.AdditionalRunners))
}

// applyStopUnmet applies the guarded PostProcessing -> Failed transition for a
// stop_unmet verdict (the maximum-replications stop criterion). A false result
// is a stale no-op. A transient DB error is retryable.
func (h *Handler) applyStopUnmet(ctx context.Context, m communication.PPSEvaluationMessage, state string) communication.PPSEvaluationHandlingResult {
	ok, err := h.failFrom(ctx, m.ScenarioID, persistence.ScenarioStatePostProcessing)
	if err != nil {
		return retry(fmt.Sprintf("db update failed: %v", err))
	}
	if !ok {
		return handled(fmt.Sprintf("stale stop_unmet verdict: no longer PostProcessing (state %s)", state))
	}
	return handled("scenario -> Failed (stop unmet)")
}

// handled builds a terminal Handled result.
func handled(reason string) communication.PPSEvaluationHandlingResult {
	return communication.PPSEvaluationHandlingResult{Status: communication.PPSEvaluationHandled, Reason: reason}
}

// retry builds a transient Retry result.
func retry(reason string) communication.PPSEvaluationHandlingResult {
	return communication.PPSEvaluationHandlingResult{Status: communication.PPSEvaluationRetry, Reason: reason}
}

// loadScenarioRound reads the scenario's current state and runner_round from
// the Core DB. A false found with nil error means the scenario row is missing
// (stale verdict). The read is unguarded: the verdict transitions below
// re-guard on the live state.
func loadScenarioRound(ctx context.Context, db persistence.DB, scenarioID int) (string, int, bool, error) {
	row := db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT state, runner_round
		FROM %s
		WHERE id = $1`, persistence.ScenarioStatusTable()), scenarioID)
	var state string
	var round int
	if err := row.Scan(&state, &round); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", 0, false, nil
		}
		return "", 0, false, fmt.Errorf("load scenario %d round: %w", scenarioID, err)
	}
	return state, round, true, nil
}
