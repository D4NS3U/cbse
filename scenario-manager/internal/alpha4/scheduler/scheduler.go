// Package scheduler defines the resource-neutral internal boundary between
// Scenario-Manager core orchestration and the workload adapter that creates,
// confirms, observes, and deletes runner Jobs. Core orchestration owns the
// database projection and guarded state transitions; the adapter owns all
// Kubernetes work. This package imports neither batch/v1 nor the alpha4
// experiment API so the core state-machine contract does not depend on a
// specific workload type. The batch/v1 Kubernetes Job adapter in package
// jobadapter is the only implementation in this branch; a future scheduler
// adapter must add its own explicit configuration and RBAC without changing
// this contract.
package scheduler

import (
	"context"
)

// RunnerStartRequest is the resource-neutral input for starting one scenario
// attempt's runner Job. Core orchestration loads and validates the runner-start
// projection (reps in 1..100000 and zero computed reps) before constructing this
// request. The adapter fetches the live experiment to obtain its current UID,
// derives the deterministic Job name, and performs all Kubernetes work; the UID
// is never loaded from Core DB.
type RunnerStartRequest struct {
	Namespace          string
	ExperimentName     string
	ScenarioID         int
	TranslationAttempt int
	NumberOfReps       int
	// ContainerImage is the persisted runner digest from the runner-start
	// projection. The adapter revalidates it and requires its normalized
	// repository to equal the live experiment's spec.translator.repository.
	ContainerImage string
}

// RunnerStartOutcome classifies the result of a runner-start attempt.
type RunnerStartOutcome int

const (
	// RunnerStartCreated means the adapter created the Job. Core orchestration
	// applies the guarded StartingRunners -> InProcessing transition; on a
	// zero-row (gate-race) transition it deletes the created Job at the
	// deterministic namespace/name using the returned UID as a precondition.
	RunnerStartCreated RunnerStartOutcome = iota
	// RunnerStartConfirmed means a Job with the deterministic name and exact
	// alpha4 controller owner reference already exists. Core orchestration
	// applies the guarded StartingRunners -> InProcessing transition; a
	// zero-row transition is stale success without cleanup because the Job
	// was not created by this attempt.
	RunnerStartConfirmed
	// RunnerStartCollision means a Job with the deterministic name exists but
	// its owner reference or reserved identity labels do not match this
	// experiment. A permanent startup failure: core orchestration applies the
	// guarded StartingRunners -> Failed transition and retains the Job.
	RunnerStartCollision
	// RunnerStartForbidden means a Kubernetes 403 Forbidden was returned for an
	// individual scenario operation. A permanent startup failure: core
	// orchestration applies the guarded StartingRunners -> Failed transition.
	// The process is not terminated.
	RunnerStartForbidden
	// RunnerStartProjectionInvalid means the live experiment phase, runner
	// digest, image repository, registry Secret, deterministic ServiceAccount,
	// effective-Job construction, or experiment existence failed a permanent
	// validation. Core orchestration applies the guarded StartingRunners ->
	// Failed transition and makes no Job create call.
	RunnerStartProjectionInvalid
	// RunnerStartExperimentTerminal means the live experiment is Error, Failed,
	// Completed, or deleting. Core orchestration makes no state transition or
	// Job create; the experiment phase action owns cancellation and bulk
	// updates.
	RunnerStartExperimentTerminal
	// RunnerStartTransient means an ordinary transport failure or a 10-second
	// operation deadline was exceeded, or the experiment is in an empty,
	// Pending, or Provisioning phase. Core orchestration records a five-second
	// delayed eligibility and makes no state change. Retries target the same
	// deterministic Job.
	RunnerStartTransient
)

// String returns a stable name for logs and tests.
func (o RunnerStartOutcome) String() string {
	switch o {
	case RunnerStartCreated:
		return "created"
	case RunnerStartConfirmed:
		return "confirmed"
	case RunnerStartCollision:
		return "collision"
	case RunnerStartForbidden:
		return "forbidden"
	case RunnerStartProjectionInvalid:
		return "projection-invalid"
	case RunnerStartExperimentTerminal:
		return "experiment-terminal"
	case RunnerStartTransient:
		return "transient"
	default:
		return "unknown"
	}
}

// IsPermanent reports whether the outcome is a permanent startup failure that
// must move the scenario to Failed.
func (o RunnerStartOutcome) IsPermanent() bool {
	switch o {
	case RunnerStartCollision, RunnerStartForbidden, RunnerStartProjectionInvalid:
		return true
	}
	return false
}

// RunnerStartResult carries the runner-start outcome and the deterministic Job
// identity retained for gate-race cleanup. For a Created outcome the adapter
// reports the server-assigned Job UID; core orchestration uses it as the delete
// precondition when a successful create races lifecycle-gate closure. The
// adapter does not validate or compare any other returned Job field, so only
// the UID is retained.
type RunnerStartResult struct {
	Outcome RunnerStartOutcome
	// JobName is the deterministic Job name the adapter targeted. Set for
	// Created, Confirmed, and Collision outcomes.
	JobName string
	// CreatedJobUID is the UID Kubernetes assigned to the created Job. Set only
	// for the Created outcome; empty otherwise.
	CreatedJobUID string
	// Err is the underlying error for Transient, Forbidden, and
	// ProjectionInvalid outcomes. It is for diagnostics only and may be nil.
	Err error
}

// RunnerStartAdapter creates and confirms runner Jobs and performs gate-race
// cleanup deletion. It owns all Kubernetes work and exposes only primitive
// types.
type RunnerStartAdapter interface {
	// Start validates the live experiment and projection, builds the effective
	// Job, and creates or confirms the deterministic runner Job. The context is
	// used for shutdown cancellation; each Kubernetes call additionally
	// receives a fixed 10-second operation deadline.
	Start(ctx context.Context, req RunnerStartRequest) RunnerStartResult
	// DeleteCreated deletes the Job at the given deterministic namespace/name
	// using createdUID as the delete precondition, without an ownership GET or
	// identity check. It is used for gate-race cleanup when a successful create
	// crossed lifecycle-gate closure. A not-found result or UID mismatch is a
	// successful no-op. A Forbidden or transport failure is reported as an
	// error so the caller follows the terminal cleanup retry cadence.
	DeleteCreated(ctx context.Context, namespace, name, createdUID string) error
}

// ObservationRequest is the resource-neutral input for observing one scenario's
// running Job. Core orchestration loads the observation projection (scenario
// ID, requested and computed repetition counts, translation attempt, project
// namespace, and project name) before constructing this request. The adapter
// fetches the live experiment to obtain its UID and phase, derives the
// deterministic Job name, and reads the Job status.
type ObservationRequest struct {
	Namespace          string
	ExperimentName     string
	ScenarioID         int
	TranslationAttempt int
	NumberOfReps       int
}

// ObservationOutcome classifies the result of an observation poll.
type ObservationOutcome int

const (
	// ObservationCompleted means the Job has Kubernetes Complete=True. Core
	// orchestration records the full repetition count and applies the guarded
	// InProcessing -> PostProcessing transition.
	ObservationCompleted ObservationOutcome = iota
	// ObservationFailed means the Job has Kubernetes Failed=True. Core
	// orchestration records the observed partial successful-index count and
	// applies the guarded InProcessing -> Failed transition.
	ObservationFailed
	// ObservationCollision means a Job exists under the deterministic name but
	// its owner reference or reserved identity labels do not match the
	// observation projection. Core orchestration applies the guarded
	// InProcessing -> Failed transition and does not adopt the Job.
	ObservationCollision
	// ObservationForbidden means a Kubernetes 403 Forbidden was returned while
	// getting the experiment or Job. Core orchestration preserves the current
	// successful-index count and applies the guarded InProcessing -> Failed
	// transition. The process is not terminated.
	ObservationForbidden
	// ObservationRetry means no terminal condition applies: the Job is active or
	// pending, the Job is missing, an operation deadline or transport failure
	// occurred, or the experiment is terminal, deleting, unavailable, or
	// missing. Core orchestration makes no state change and the key rejoins the
	// queue at the first strictly subsequent five-second discovery tick.
	ObservationRetry
)

// String returns a stable name for logs and tests.
func (o ObservationOutcome) String() string {
	switch o {
	case ObservationCompleted:
		return "completed"
	case ObservationFailed:
		return "failed"
	case ObservationCollision:
		return "collision"
	case ObservationForbidden:
		return "forbidden"
	case ObservationRetry:
		return "retry"
	default:
		return "unknown"
	}
}

// IsPermanent reports whether the outcome must move the scenario to Failed.
func (o ObservationOutcome) IsPermanent() bool {
	switch o {
	case ObservationFailed, ObservationCollision, ObservationForbidden:
		return true
	}
	return false
}

// IsTerminalSuccess reports whether the outcome records the full repetition
// count and advances InProcessing -> PostProcessing.
func (o ObservationOutcome) IsTerminalSuccess() bool { return o == ObservationCompleted }

// ObservationResult carries the observation outcome and the monotonic
// completed-index count parsed from job.status.completedIndexes. CompletedReps
// is set for Completed (equal to NumberOfReps), Failed (the partial
// successful-index count; zero when the index string is malformed or
// out-of-range), and Retry (the partial successful-index count of an active or
// pending Job). It is zero for Collision and Forbidden, which preserve the
// current count, and for a Retry caused by a missing Job, operation deadline,
// transport failure, or a terminal/unavailable experiment.
type ObservationResult struct {
	Outcome       ObservationOutcome
	CompletedReps int
	// JobName is the deterministic Job name the adapter observed. Set for
	// terminal outcomes (Completed, Failed, Collision, Forbidden) so the
	// observation worker can emit the terminal scenario-observability record.
	JobName string
	// Err is the underlying error for Retry and Forbidden outcomes. It is for
	// diagnostics only and may be nil.
	Err error
}

// ObservationAdapter observes running Jobs and parses the completed-index set.
type ObservationAdapter interface {
	// Observe gets the live experiment and the named Job, verifies the exact
	// ownership identity, parses completedIndexes, and classifies the result.
	// The context is used for shutdown cancellation; each Kubernetes call
	// additionally receives a fixed 10-second operation deadline.
	Observe(ctx context.Context, req ObservationRequest) ObservationResult
}
