package observation

import (
	"context"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/eventlog"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/scheduler"
)

// action is the coordinator instruction a worker returns after one observation.
type action int

const (
	// actRequeue means the scenario is still InProcessing and eligible again
	// only at the first strictly subsequent five-second discovery tick. The
	// coordinator records eligibleAt[id] = currentTick + 1.
	actRequeue action = iota
	// actRemove means the scenario left InProcessing (terminal transition
	// applied, stale projection, or terminal-action move). The coordinator
	// drops the key from all process-local state.
	actRemove
)

// workerResult is what a worker sends back to the coordinator.
type workerResult struct {
	scenarioID int
	action     action
	worker     int
}

// reconcile runs one observation for a scenario key. It loads the fresh
// projection, observes the deterministic Job via the adapter, applies the
// guarded database transitions dictated by the outcome, and returns the
// coordinator instruction. It never mutates scheduler sets.
//
// The ctx is the coordinator's lifetime context; cancelling it (SM shutdown)
// short-circuits the 10-second per-call Kubernetes deadlines so in-flight
// observations fail fast. On shutdown the coordinator ignores the returned
// action and simply drops the key.
//
// Scenario-observability records (S06-M3) are emitted only when this replica
// wins the guarded terminal transition (ok=true): exactly one terminal record
// per scenario that this replica moves to PostProcessing or Failed. A stale
// zero-row transition (another replica won, or the terminal action moved the
// row) emits no record, so a scenario is logged exactly once even under
// retries and concurrent replicas. A non-terminal Retry (active/pending Job,
// missing Job, transport, or a terminal/unavailable experiment) emits no record
// at all — SM does not log every unchanged observation poll.
func (s *Scheduler) reconcile(ctx context.Context, scenarioID int) workerResult {
	proj, err := s.store.LoadProjection(ctx, scenarioID)
	if err != nil {
		// Core DB transport failure: requeue without a state change.
		return workerResult{scenarioID: scenarioID, action: actRequeue}
	}
	if proj == nil {
		// Row absent or no longer InProcessing: stale. Remove without a
		// transition (terminal action or another path moved the row).
		return workerResult{scenarioID: scenarioID, action: actRemove}
	}

	res := s.adapter.Observe(ctx, scheduler.ObservationRequest{
		Namespace:          proj.ProjectNamespace,
		ExperimentName:     proj.ProjectName,
		ScenarioID:         proj.ID,
		TranslationAttempt: proj.TranslationAttempt,
		NumberOfReps:       proj.NumberOfReps,
	})

	switch res.Outcome {
	case scheduler.ObservationCompleted:
		// First record the full repetition count, then the guarded
		// InProcessing -> PostProcessing transition.
		_, ok, err := s.store.UpdateComputedRepsMonotonic(ctx, scenarioID, res.CompletedReps)
		if err != nil {
			return workerResult{scenarioID: scenarioID, action: actRequeue}
		} else if !ok {
			// The row is no longer InProcessing: stale, nothing more to do.
			return workerResult{scenarioID: scenarioID, action: actRemove}
		}
		postOK, err := s.store.MarkPostProcessing(ctx, scenarioID)
		if err != nil {
			// Count recorded; the PostProcessing transition failed on transport.
			// Requeue to retry the transition on the next tick. No record yet.
			return workerResult{scenarioID: scenarioID, action: actRequeue}
		}
		if postOK {
			// This replica won the terminal transition. Emit the one complete
			// record carrying the full repetition count.
			s.log.Log(eventlog.Record{
				Event:         eventlog.EventComplete,
				Namespace:     proj.ProjectNamespace,
				Experiment:    proj.ProjectName,
				ScenarioID:    proj.ID,
				Attempt:       proj.TranslationAttempt,
				JobName:       res.JobName,
				RequestedReps: proj.NumberOfReps,
				ComputedReps:  res.CompletedReps,
				Outcome:       res.Outcome.String(),
			})
		}
		return workerResult{scenarioID: scenarioID, action: actRemove}

	case scheduler.ObservationFailed:
		// First preserve the observed partial successful-index count, then
		// the guarded InProcessing -> Failed transition. A malformed or
		// out-of-range completedIndexes string yields CompletedReps == 0, so
		// no count update is applied and the current count is preserved.
		var computedReps int
		if res.CompletedReps > 0 {
			got, ok, err := s.store.UpdateComputedRepsMonotonic(ctx, scenarioID, res.CompletedReps)
			if err != nil {
				return workerResult{scenarioID: scenarioID, action: actRequeue}
			} else if !ok {
				return workerResult{scenarioID: scenarioID, action: actRemove}
			}
			computedReps = got
		}
		failOK, err := s.store.MarkFailedFrom(ctx, scenarioID)
		if err != nil {
			return workerResult{scenarioID: scenarioID, action: actRequeue}
		}
		if failOK {
			s.log.Log(eventlog.Record{
				Event:         eventlog.EventFail,
				Namespace:     proj.ProjectNamespace,
				Experiment:    proj.ProjectName,
				ScenarioID:    proj.ID,
				Attempt:       proj.TranslationAttempt,
				JobName:       res.JobName,
				RequestedReps: proj.NumberOfReps,
				ComputedReps:  computedReps,
				Outcome:       res.Outcome.String(),
				Reason:        terminalReason(res.Err),
			})
		}
		return workerResult{scenarioID: scenarioID, action: actRemove}

	case scheduler.ObservationCollision, scheduler.ObservationForbidden:
		// Permanent failure: apply the guarded InProcessing -> Failed
		// transition without changing the computed-reps count (the current
		// successful-index count is preserved).
		failOK, err := s.store.MarkFailedFrom(ctx, scenarioID)
		if err != nil {
			return workerResult{scenarioID: scenarioID, action: actRequeue}
		}
		if failOK {
			s.log.Log(eventlog.Record{
				Event:         eventlog.EventFail,
				Namespace:     proj.ProjectNamespace,
				Experiment:    proj.ProjectName,
				ScenarioID:    proj.ID,
				Attempt:       proj.TranslationAttempt,
				JobName:       res.JobName,
				RequestedReps: proj.NumberOfReps,
				ComputedReps:  0,
				Outcome:       res.Outcome.String(),
				Reason:        terminalReason(res.Err),
			})
		}
		return workerResult{scenarioID: scenarioID, action: actRemove}

	default: // scheduler.ObservationRetry
		// Non-terminal: active/pending Job, missing Job, operation deadline,
		// transport failure, or a terminal/unavailable/deleting experiment. A
		// running Job records its monotonic partial successful-index count
		// (CompletedReps > 0); a missing/transport Retry carries zero. No state
		// change and no observability record; the key rejoins the queue at the
		// next strictly subsequent tick.
		if res.CompletedReps > 0 {
			if _, ok, err := s.store.UpdateComputedRepsMonotonic(ctx, scenarioID, res.CompletedReps); err != nil {
				return workerResult{scenarioID: scenarioID, action: actRequeue}
			} else if !ok {
				// The row left InProcessing: stale. Remove; the next discovery
				// will not re-list it.
				return workerResult{scenarioID: scenarioID, action: actRemove}
			}
		}
		return workerResult{scenarioID: scenarioID, action: actRequeue}
	}
}

// terminalReason returns a short, credential-free reason for a terminal
// scenario-observability record.
func terminalReason(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
