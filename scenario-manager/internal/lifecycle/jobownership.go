package lifecycle

import (
	"context"
	"fmt"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// runnerJobListOptions returns the list options for runner Jobs belonging to
// the experiment: the experiment namespace plus the reserved project and
// experiment-UID labels. Label-based pre-filtering narrows the candidate set;
// full ownership verification (deterministic name, all four labels, and the
// controller owner reference) is applied to each candidate.
func runnerJobListOptions(exp *experimentalpha4.SimulationExperiment) []client.ListOption {
	return []client.ListOption{
		client.InNamespace(exp.Namespace),
		client.MatchingLabels{LabelProject: exp.Name, LabelExperimentUID: string(exp.UID)},
	}
}

// ListRunnerJobs returns the candidate runner Jobs matching the experiment's
// project and experiment-UID labels in its namespace. It performs no ownership
// verification; callers must verify each candidate before relying on it.
func ListRunnerJobs(ctx context.Context, k8s client.Client, exp *experimentalpha4.SimulationExperiment) ([]batchv1.Job, error) {
	jobs := &batchv1.JobList{}
	if err := k8s.List(ctx, jobs, runnerJobListOptions(exp)...); err != nil {
		return nil, fmt.Errorf("list runner jobs for %s/%s: %w", exp.Namespace, exp.Name, err)
	}
	return jobs.Items, nil
}

// VerifyRunnerJob returns nil only when job is owned by the experiment per the
// full ownership contract: experiment namespace, exact project and full-UID
// labels, canonical positive-decimal scenario-id and translation-attempt labels
// that reproduce the deterministic Job name, and the exact alpha4 controller
// owner reference. A mismatch is an identity collision.
func VerifyRunnerJob(job *batchv1.Job, exp *experimentalpha4.SimulationExperiment) error {
	return verifyJobOwnership(job, exp)
}

// DeleteVerifiedRunnerJobs lists candidate runner Jobs, verifies each, and
// deletes every verified Job with foreground propagation. It returns the
// names of the Jobs it deleted (or began deleting) and an error if any
// candidate is an identity collision; a collision does not prevent deleting
// the verified Jobs in the same pass, but it fails the attempt so the caller
// retries on the fixed cadence. A missing candidate list is success.
func DeleteVerifiedRunnerJobs(ctx context.Context, k8s client.Client, exp *experimentalpha4.SimulationExperiment) ([]string, error) {
	candidates, err := ListRunnerJobs(ctx, k8s, exp)
	if err != nil {
		return nil, err
	}
	var (
		deleted   []string
		collision error
	)
	for i := range candidates {
		job := &candidates[i]
		if verr := verifyJobOwnership(job, exp); verr != nil {
			// Record the first collision; keep deleting verified Jobs in this
			// pass so the attempt makes progress on owned work.
			if collision == nil {
				collision = fmt.Errorf("ownership collision: %w", verr)
			}
			continue
		}
		if derr := deleteJobForeground(ctx, k8s, job); derr != nil && !apierrors.IsNotFound(derr) {
			return deleted, fmt.Errorf("delete job %s/%s: %w", job.Namespace, job.Name, derr)
		}
		deleted = append(deleted, job.Name)
	}
	return deleted, collision
}

// deleteJobForeground deletes a Job with foreground propagation so the API
// server removes the Job's Pods before the Job itself. A NotFound result is
// success (the Job is already absent).
func deleteJobForeground(ctx context.Context, k8s client.Client, job *batchv1.Job) error {
	if err := k8s.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil {
		return err
	}
	return nil
}

// ConfirmRunnerJobsAbsent reports whether every named Job is absent from the
// experiment namespace. A Job still present (terminating or recreated) is not
// absent: the caller must keep retrying until the named read reports it gone.
func ConfirmRunnerJobsAbsent(ctx context.Context, k8s client.Client, namespace string, names []string) error {
	for _, name := range names {
		job := &batchv1.Job{}
		if err := k8s.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, job); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("confirm job %s/%s absent: %w", namespace, name, err)
		}
		return fmt.Errorf("job %s/%s still present", namespace, name)
	}
	return nil
}

// ConfirmAllRunnerJobsAbsent lists candidate runner Jobs and requires the set
// to be empty. This is the final verified Job absence check that catches a
// create that crossed gate closure or the first deletion pass.
func ConfirmAllRunnerJobsAbsent(ctx context.Context, k8s client.Client, exp *experimentalpha4.SimulationExperiment) error {
	candidates, err := ListRunnerJobs(ctx, k8s, exp)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}
	// A remaining candidate may be a verified Job still terminating, or a
	// collision. Either way the absence check is not satisfied.
	present := make([]string, 0, len(candidates))
	for _, j := range candidates {
		present = append(present, j.Name)
	}
	return fmt.Errorf("runner jobs still present for %s/%s: %v", exp.Namespace, exp.Name, present)
}
