package runnerstart

import (
	"context"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/eventlog"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/scheduler"
)

// action is the coordinator instruction a worker returns after one
// reconciliation. The coordinator owns set mutation; the worker only reports
// the outcome so the scheduler's ready/delayed/in-flight state stays
// single-threaded.
type action int

const (
	// actRemove means the ID is done: success, permanent failure recorded,
	// stale projection/state, lifecycle-gate closure, or successful gate-race
	// cleanup. The coordinator removes the ID from the in-flight set and drops
	// it.
	actRemove action = iota
	// actDelayStart means a transient failure (DB transport, Kubernetes
	// transport, the 10-second per-call deadline, or an unavailable
	// experiment). The coordinator moves the ID to the delayed set with
	// nextEligibleAt = now + TransientDelay and no retained UID; the next
	// attempt re-runs the full workflow.
	actDelayStart
	// actDelayCleanup means a gate-race cleanup delete failed (transport or
	// Forbidden). The coordinator moves the ID to the delayed set in cleanup
	// mode, retaining the process-local returned UID, and re-attempts only the
	// delete after the delay. This follows the terminal cleanup retry cadence
	// instead of leaving the stale Job behind.
	actDelayCleanup
)

// cleanupWork carries the process-local returned UID and deterministic
// namespace/name for a gate-race cleanup (or its delayed retry). It is never
// persisted and is lost on SM restart.
type cleanupWork struct {
	namespace  string
	jobName    string
	createdUID string
}

// dispatch is the unit of work the coordinator sends to a free reconciler.
// A nil cleanup means a normal runner-start reconciliation; a non-nil cleanup
// means a gate-race cleanup retry (DeleteCreated only).
type dispatch struct {
	scenarioID int
	cleanup    *cleanupWork
}

// workerResult is what a worker sends back to the coordinator.
type workerResult struct {
	scenarioID int
	action     action
	cleanup    *cleanupWork
	worker     int
}

// reconcile runs one runner-start workflow for a dispatch. It loads the fresh
// projection, creates or confirms the deterministic Job via the adapter, and
// applies the guarded database transition dictated by the outcome. It returns
// the coordinator instruction; it never mutates scheduler sets.
//
// The ctx is the coordinator's lifetime context; cancelling it (SM shutdown)
// short-circuits the 10-second per-call Kubernetes deadlines so in-flight
// workflows fail fast. On shutdown the coordinator ignores the returned action
// (it does not re-delay or transition) and simply drops the ID.
//
// Scenario-observability records (S06-M3) are emitted only when this replica
// wins the guarded transition (ok=true): exactly one creation or adoption
// record per scenario that advances to InProcessing, and exactly one terminal
// fail record per scenario that this replica moves to Failed. A stale
// zero-row transition (another replica won, or the lifecycle gate closed) emits
// no record, so a scenario is logged exactly once per kind even under retries
// and concurrent replicas.
func (s *Scheduler) reconcile(ctx context.Context, d dispatch) workerResult {
	// Gate-race cleanup retry: only delete, no projection load, no Start, no
	// state transition. This follows the terminal cleanup retry cadence,
	// including after Forbidden, until the delete succeeds.
	if d.cleanup != nil {
		if err := s.adapter.DeleteCreated(ctx, d.cleanup.namespace, d.cleanup.jobName, d.cleanup.createdUID); err != nil {
			return workerResult{scenarioID: d.scenarioID, action: actDelayCleanup, cleanup: d.cleanup}
		}
		return workerResult{scenarioID: d.scenarioID, action: actRemove}
	}

	proj, err := s.store.LoadProjection(ctx, d.scenarioID)
	if err != nil {
		// Core DB transport failure: transient, no state change.
		return workerResult{scenarioID: d.scenarioID, action: actDelayStart}
	}
	if proj == nil {
		// Row absent or no longer StartingRunners: stale. Remove without a
		// transition (another replica won, or the lifecycle gate closed and
		// the terminal action already moved the row).
		return workerResult{scenarioID: d.scenarioID, action: actRemove}
	}

	res := s.adapter.Start(ctx, scheduler.RunnerStartRequest{
		Namespace:          proj.ProjectNamespace,
		ExperimentName:     proj.ProjectName,
		ScenarioID:         proj.ID,
		TranslationAttempt: proj.TranslationAttempt,
		NumberOfReps:       proj.NumberOfReps,
		ContainerImage:     proj.ContainerImage,
	})

	switch res.Outcome {
	case scheduler.RunnerStartCreated:
		ok, err := s.store.MarkInProcessing(ctx, d.scenarioID)
		if err != nil {
			// DB transport failure on the guarded transition: transient. The
			// process-local UID is not persisted; the next attempt re-runs Start
			// and re-confirms the deterministic Job. No observability record: the
			// scenario has not yet advanced to InProcessing.
			return workerResult{scenarioID: d.scenarioID, action: actDelayStart}
		}
		if ok {
			// This replica won the guarded transition: the created Job is the
			// live runner. Emit the one creation record.
			s.log.Log(eventlog.Record{
				Event:         eventlog.EventCreate,
				Namespace:     proj.ProjectNamespace,
				Experiment:    proj.ProjectName,
				ScenarioID:    proj.ID,
				Attempt:       proj.TranslationAttempt,
				JobName:       res.JobName,
				RequestedReps: proj.NumberOfReps,
				Outcome:       res.Outcome.String(),
			})
			return workerResult{scenarioID: d.scenarioID, action: actRemove}
		}
		// Zero rows: the guarded transition lost to lifecycle-gate closure (the
		// row is no longer StartingRunners). The successful create crossed the
		// gate, so delete the created Job at the submitted deterministic
		// namespace/name using the returned UID as the sole precondition,
		// without an ownership GET or identity check. No observability record:
		// the experiment terminal action owns the scenario's terminal outcome.
		cw := &cleanupWork{namespace: proj.ProjectNamespace, jobName: res.JobName, createdUID: res.CreatedJobUID}
		if err := s.adapter.DeleteCreated(ctx, cw.namespace, cw.jobName, cw.createdUID); err != nil {
			return workerResult{scenarioID: d.scenarioID, action: actDelayCleanup, cleanup: cw}
		}
		return workerResult{scenarioID: d.scenarioID, action: actRemove}

	case scheduler.RunnerStartConfirmed:
		ok, err := s.store.MarkInProcessing(ctx, d.scenarioID)
		if err != nil {
			// DB transport failure on the guarded transition: transient. No
			// observability record yet.
			return workerResult{scenarioID: d.scenarioID, action: actDelayStart}
		}
		if ok {
			// This replica won the guarded transition for an already-existing
			// owned Job (AlreadyExists recovery). Emit the one adoption record.
			s.log.Log(eventlog.Record{
				Event:         eventlog.EventAdopt,
				Namespace:     proj.ProjectNamespace,
				Experiment:    proj.ProjectName,
				ScenarioID:    proj.ID,
				Attempt:       proj.TranslationAttempt,
				JobName:       res.JobName,
				RequestedReps: proj.NumberOfReps,
				Outcome:       res.Outcome.String(),
			})
		}
		// Whether the guarded transition won (ok) or lost to stale success
		// (zero rows), the ID is removed. The Job was not created by this
		// attempt, so a zero-row result performs no cleanup and emits no record
		// (another replica already logged the adoption).
		return workerResult{scenarioID: d.scenarioID, action: actRemove}

	case scheduler.RunnerStartCollision,
		scheduler.RunnerStartForbidden,
		scheduler.RunnerStartProjectionInvalid:
		// Permanent startup failure: apply the guarded StartingRunners -> Failed
		// transition. A zero-row result is a stale no-op (the row already moved);
		// either way the ID is removed. A DB transport error on the transition
		// is transient: the next attempt re-runs Start (idempotent for these
		// permanent outcomes) and re-attempts the failure transition.
		ok, err := s.store.MarkFailed(ctx, d.scenarioID)
		if err != nil {
			return workerResult{scenarioID: d.scenarioID, action: actDelayStart}
		}
		if ok {
			s.log.Log(eventlog.Record{
				Event:         eventlog.EventFail,
				Namespace:     proj.ProjectNamespace,
				Experiment:    proj.ProjectName,
				ScenarioID:    proj.ID,
				Attempt:       proj.TranslationAttempt,
				JobName:       res.JobName,
				RequestedReps: proj.NumberOfReps,
				Outcome:       res.Outcome.String(),
				Reason:        terminalReason(res.Err),
			})
		}
		return workerResult{scenarioID: d.scenarioID, action: actRemove}

	case scheduler.RunnerStartExperimentTerminal:
		// Error, Failed, Completed, or deleting experiment: no state
		// transition and no Job create. The experiment phase action owns
		// cancellation and any bulk scenario update. No observability record.
		return workerResult{scenarioID: d.scenarioID, action: actRemove}

	default: // scheduler.RunnerStartTransient and any unexpected outcome.
		// Ordinary Kubernetes transport failure or the 10-second per-call
		// deadline, or an empty/Pending/Provisioning experiment: retryable
		// without changing scenario state. No observability record.
		return workerResult{scenarioID: d.scenarioID, action: actDelayStart}
	}
}

// terminalReason returns a short, credential-free reason for a terminal
// scenario-observability record. It never echoes credential material or recipe
// payloads; the adapter's projection-invalid errors describe registry-auth
// failures by authority, not by credential value.
func terminalReason(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
