// Package jobadapter implements the resource-neutral scheduler boundary
// (package scheduler) with the batch/v1 Kubernetes Job adapter. It owns all
// Kubernetes work for runner-start and observation: getting the live
// experiment, validating the runner digest, image repository, registry Secret,
// and deterministic ServiceAccount, building the effective Job, creating or
// confirming the deterministic Job, observing Job status, parsing the
// compressed completedIndexes set, and performing gate-race cleanup deletion.
// Core orchestration supplies the database projection and applies the guarded
// state transitions based on the outcomes returned here.
package jobadapter

import (
	"context"
	"fmt"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/effectivejob"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/lifecycle"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/registry"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/scheduler"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

// opDeadline is the fixed 10-second deadline applied to each Kubernetes get,
// create, or delete call made for one scenario.
const opDeadline = 10 * time.Second

// admitOK is an internal sentinel distinct from the scheduler outcomes. It
// signals that the experiment was admitted and the caller should proceed; it is
// never returned to core orchestration.
const (
	admitOK   scheduler.RunnerStartOutcome = -1
	observeOK scheduler.ObservationOutcome = -1
)

// k8sClient is the Kubernetes surface the adapter uses. It is satisfied by a
// controller-runtime client wrapper in production and by a fake in tests; the
// fake can mutate the create response to prove the adapter retains only the
// returned UID.
type k8sClient interface {
	GetExperiment(ctx context.Context, namespace, name string) (*experimentalpha4.SimulationExperiment, error)
	GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error)
	GetServiceAccount(ctx context.Context, namespace, name string) (*corev1.ServiceAccount, error)
	CreateJob(ctx context.Context, job *batchv1.Job) (*batchv1.Job, error)
	GetJob(ctx context.Context, namespace, name string) (*batchv1.Job, error)
	DeleteJob(ctx context.Context, namespace, name string, uid types.UID) error
}

// Adapter is the batch/v1 Kubernetes Job adapter implementing both
// scheduler.RunnerStartAdapter and scheduler.ObservationAdapter.
type Adapter struct {
	k8s k8sClient
}

// NewAdapter returns an Adapter over the given Kubernetes client surface. It is
// used by tests; production wiring uses NewControllerRuntimeAdapter.
func NewAdapter(k8s k8sClient) *Adapter { return &Adapter{k8s: k8s} }

// Start validates the live experiment and projection, builds the effective Job,
// and creates or confirms the deterministic runner Job. See the
// scheduler.RunnerStartAdapter contract for the outcome taxonomy.
func (a *Adapter) Start(ctx context.Context, req scheduler.RunnerStartRequest) scheduler.RunnerStartResult {
	exp, outcome := a.admitLiveExperiment(ctx, req.Namespace, req.ExperimentName)
	if outcome != admitOK {
		return runnerStartResult(outcome, "", "", nil)
	}

	// Revalidate the persisted runner digest and require its normalized
	// repository to equal the live experiment's translator repository.
	if err := registry.ValidateDigestImage(req.ContainerImage); err != nil {
		return runnerStartResult(scheduler.RunnerStartProjectionInvalid, "", "", fmt.Errorf("runner image: %w", err))
	}
	repo, err := registry.RepositoryFromDigest(req.ContainerImage)
	if err != nil {
		return runnerStartResult(scheduler.RunnerStartProjectionInvalid, "", "", fmt.Errorf("runner repository: %w", err))
	}
	if repo != exp.Spec.Translator.Repository {
		return runnerStartResult(scheduler.RunnerStartProjectionInvalid, "", "", fmt.Errorf("runner repository %q does not equal spec.translator.repository %q", repo, exp.Spec.Translator.Repository))
	}

	// Direct named GET + validation for the registry Secret.
	secret, outcome := a.getRegistrySecret(ctx, req.Namespace)
	if outcome != admitOK {
		return runnerStartResult(outcome, "", "", nil)
	}
	dockerconfigjson, err := registry.ValidateRegistrySecret(secret)
	if err != nil {
		return runnerStartResult(scheduler.RunnerStartProjectionInvalid, "", "", err)
	}
	authority, err := registry.RegistryAuthority(req.ContainerImage)
	if err != nil {
		return runnerStartResult(scheduler.RunnerStartProjectionInvalid, "", "", err)
	}
	if _, _, err := registry.ResolveDockerAuth(dockerconfigjson, authority); err != nil {
		return runnerStartResult(scheduler.RunnerStartProjectionInvalid, "", "", fmt.Errorf("registry auth for %s: %w", authority, err))
	}

	// Direct named GET for the deterministic runner ServiceAccount.
	if outcome := a.confirmServiceAccount(ctx, exp.UID, req.Namespace); outcome != admitOK {
		return runnerStartResult(outcome, "", "", nil)
	}

	// Build the effective Job from the Operator-validated template.
	job, err := effectivejob.Build(effectivejob.BuildRequest{
		Experiment:         exp,
		ScenarioID:         req.ScenarioID,
		TranslationAttempt: req.TranslationAttempt,
		NumberOfReps:       req.NumberOfReps,
		ContainerImage:     req.ContainerImage,
	})
	if err != nil {
		return runnerStartResult(scheduler.RunnerStartProjectionInvalid, "", "", fmt.Errorf("effective job: %w", err))
	}
	jobName := job.Name

	// Create the Job. A successful create is authoritative: the adapter
	// retains only the returned UID for possible gate-race cleanup.
	created, err := a.createJob(ctx, job)
	if err == nil {
		return runnerStartResult(scheduler.RunnerStartCreated, jobName, string(created.UID), nil)
	}
	if apierrors.IsAlreadyExists(err) {
		return a.confirmAlreadyExists(ctx, exp, jobName)
	}
	if apierrors.IsForbidden(err) {
		return runnerStartResult(scheduler.RunnerStartForbidden, jobName, "", err)
	}
	return runnerStartResult(scheduler.RunnerStartTransient, jobName, "", err)
}

// confirmAlreadyExists gets the existing Job at the deterministic name and
// verifies the exact alpha4 ownership identity. A match is Confirmed; a
// mismatch is a Collision.
func (a *Adapter) confirmAlreadyExists(ctx context.Context, exp *experimentalpha4.SimulationExperiment, jobName string) scheduler.RunnerStartResult {
	job, err := a.getJob(ctx, exp.Namespace, jobName)
	if err != nil {
		if apierrors.IsForbidden(err) {
			return runnerStartResult(scheduler.RunnerStartForbidden, jobName, "", err)
		}
		return runnerStartResult(scheduler.RunnerStartTransient, jobName, "", err)
	}
	if verr := lifecycle.VerifyRunnerJob(job, exp); verr != nil {
		return runnerStartResult(scheduler.RunnerStartCollision, jobName, "", verr)
	}
	return runnerStartResult(scheduler.RunnerStartConfirmed, jobName, "", nil)
}

// DeleteCreated deletes the Job at the deterministic namespace/name using the
// returned UID as a precondition, without an ownership GET or identity check.
// A NotFound or UID mismatch is a successful no-op.
func (a *Adapter) DeleteCreated(ctx context.Context, namespace, name, createdUID string) error {
	cctx, cancel := context.WithTimeout(ctx, opDeadline)
	defer cancel()
	if err := a.k8s.DeleteJob(cctx, namespace, name, types.UID(createdUID)); err != nil {
		if apierrors.IsNotFound(err) || apierrors.IsConflict(err) {
			return nil
		}
		return err
	}
	return nil
}

// Observe gets the live experiment and the named Job, verifies the exact
// ownership identity, parses completedIndexes, and classifies the result. See
// the scheduler.ObservationAdapter contract for the outcome taxonomy.
func (a *Adapter) Observe(ctx context.Context, req scheduler.ObservationRequest) scheduler.ObservationResult {
	exp, outcome := a.observeExperiment(ctx, req.Namespace, req.ExperimentName)
	if outcome != observeOK {
		return observationResult(outcome, 0, "", nil)
	}

	jobName := lifecycle.RunnerJobName(exp.UID, req.ScenarioID, req.TranslationAttempt)
	job, err := a.getJob(ctx, exp.Namespace, jobName)
	if err != nil {
		if apierrors.IsForbidden(err) {
			return observationResult(scheduler.ObservationForbidden, 0, jobName, err)
		}
		// NotFound (missing Job) and transport/deadline failures leave the
		// scenario InProcessing for the next five-second tick.
		return observationResult(scheduler.ObservationRetry, 0, jobName, err)
	}

	// Exact ownership identity before reading any status field.
	if verr := lifecycle.VerifyRunnerJob(job, exp); verr != nil {
		return observationResult(scheduler.ObservationCollision, 0, jobName, verr)
	}

	// Parse the authoritative unique-index set for every observation.
	parsedCount, err := parseCompletedIndexes(job.Status.CompletedIndexes, req.NumberOfReps-1)
	if err != nil {
		// Malformed or out-of-range server-owned index string is invalid Job
		// status and fails only this scenario; preserve the current count.
		return observationResult(scheduler.ObservationFailed, 0, jobName, err)
	}

	if jobHasCondition(job, batchv1.JobFailed) {
		return observationResult(scheduler.ObservationFailed, parsedCount, jobName, nil)
	}
	if jobHasCondition(job, batchv1.JobComplete) {
		// A complete Job records the full repetition count.
		return observationResult(scheduler.ObservationCompleted, req.NumberOfReps, jobName, nil)
	}
	// Non-terminal active or pending Job: record the monotonic partial count
	// and remain InProcessing for the next five-second tick.
	return observationResult(scheduler.ObservationRetry, parsedCount, jobName, nil)
}

// admitLiveExperiment gets the experiment and applies the lifecycle gate. It
// returns the experiment and either admitOK (proceed) or a permanent /
// transient / terminal outcome.
func (a *Adapter) admitLiveExperiment(ctx context.Context, namespace, name string) (*experimentalpha4.SimulationExperiment, scheduler.RunnerStartOutcome) {
	exp, err := a.getExperiment(ctx, namespace, name)
	if err != nil {
		if apierrors.IsForbidden(err) {
			return nil, scheduler.RunnerStartForbidden
		}
		if apierrors.IsNotFound(err) {
			// A missing experiment outside the supported finalizer lifecycle
			// is a permanent startup failure.
			return nil, scheduler.RunnerStartProjectionInvalid
		}
		return nil, scheduler.RunnerStartTransient
	}
	switch lifecycle.AdmitExperiment(exp) {
	case lifecycle.Admit:
		return exp, admitOK
	case lifecycle.AdmitTerminal:
		return nil, scheduler.RunnerStartExperimentTerminal
	default:
		return nil, scheduler.RunnerStartTransient
	}
}

// getRegistrySecret gets the cbse-registry-auth Secret and maps the error to an
// outcome.
func (a *Adapter) getRegistrySecret(ctx context.Context, namespace string) (*corev1.Secret, scheduler.RunnerStartOutcome) {
	secret, err := a.getSecret(ctx, namespace, registry.RegistryAuthSecretName)
	if err != nil {
		if apierrors.IsForbidden(err) {
			return nil, scheduler.RunnerStartForbidden
		}
		if apierrors.IsNotFound(err) {
			return nil, scheduler.RunnerStartProjectionInvalid
		}
		return nil, scheduler.RunnerStartTransient
	}
	return secret, admitOK
}

// confirmServiceAccount gets the deterministic runner ServiceAccount and maps
// the error to an outcome.
func (a *Adapter) confirmServiceAccount(ctx context.Context, uid types.UID, namespace string) scheduler.RunnerStartOutcome {
	saName := lifecycle.RunnerServiceAccountName(uid)
	if _, err := a.getServiceAccount(ctx, namespace, saName); err != nil {
		if apierrors.IsForbidden(err) {
			return scheduler.RunnerStartForbidden
		}
		if apierrors.IsNotFound(err) {
			return scheduler.RunnerStartProjectionInvalid
		}
		return scheduler.RunnerStartTransient
	}
	return admitOK
}

// observeExperiment gets the experiment and applies the lifecycle gate for the
// observation path. Terminal, unavailable, missing, and forbidden experiments
// map to no-op retry or permanent forbidden outcomes; only InProgress proceeds.
func (a *Adapter) observeExperiment(ctx context.Context, namespace, name string) (*experimentalpha4.SimulationExperiment, scheduler.ObservationOutcome) {
	exp, err := a.getExperiment(ctx, namespace, name)
	if err != nil {
		if apierrors.IsForbidden(err) {
			return nil, scheduler.ObservationForbidden
		}
		// NotFound (experiment gone), transport, and deadline failures leave
		// the scenario InProcessing for the next five-second tick.
		return nil, scheduler.ObservationRetry
	}
	switch lifecycle.AdmitExperiment(exp) {
	case lifecycle.Admit:
		return exp, observeOK
	default:
		// Terminal and unavailable experiments cause a successful no-op.
		return nil, scheduler.ObservationRetry
	}
}

// getExperiment wraps the get with the 10-second operation deadline.
func (a *Adapter) getExperiment(ctx context.Context, namespace, name string) (*experimentalpha4.SimulationExperiment, error) {
	cctx, cancel := context.WithTimeout(ctx, opDeadline)
	defer cancel()
	return a.k8s.GetExperiment(cctx, namespace, name)
}

func (a *Adapter) getSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	cctx, cancel := context.WithTimeout(ctx, opDeadline)
	defer cancel()
	return a.k8s.GetSecret(cctx, namespace, name)
}

func (a *Adapter) getServiceAccount(ctx context.Context, namespace, name string) (*corev1.ServiceAccount, error) {
	cctx, cancel := context.WithTimeout(ctx, opDeadline)
	defer cancel()
	return a.k8s.GetServiceAccount(cctx, namespace, name)
}

func (a *Adapter) createJob(ctx context.Context, job *batchv1.Job) (*batchv1.Job, error) {
	cctx, cancel := context.WithTimeout(ctx, opDeadline)
	defer cancel()
	return a.k8s.CreateJob(cctx, job)
}

func (a *Adapter) getJob(ctx context.Context, namespace, name string) (*batchv1.Job, error) {
	cctx, cancel := context.WithTimeout(ctx, opDeadline)
	defer cancel()
	return a.k8s.GetJob(cctx, namespace, name)
}

// jobHasCondition reports whether the Job has the given condition type with
// status True.
func jobHasCondition(job *batchv1.Job, ct batchv1.JobConditionType) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == ct && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// runnerStartResult is a small constructor for clarity at return sites.
func runnerStartResult(o scheduler.RunnerStartOutcome, name, uid string, err error) scheduler.RunnerStartResult {
	return scheduler.RunnerStartResult{Outcome: o, JobName: name, CreatedJobUID: uid, Err: err}
}

// observationResult is a small constructor for clarity at return sites.
func observationResult(o scheduler.ObservationOutcome, count int, jobName string, err error) scheduler.ObservationResult {
	return scheduler.ObservationResult{Outcome: o, CompletedReps: count, JobName: jobName, Err: err}
}

var _ scheduler.RunnerStartAdapter = (*Adapter)(nil)
var _ scheduler.ObservationAdapter = (*Adapter)(nil)
