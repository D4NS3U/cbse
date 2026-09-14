package jobadapter

import (
	"context"
	"fmt"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// crClient adapts a controller-runtime client.Client to the jobadapter k8sClient
// interface. It is the production Kubernetes surface; tests use a fake k8sClient
// that can mutate create responses.
type crClient struct {
	c client.Client
}

// NewControllerRuntimeAdapter returns a scheduler adapter backed by a
// controller-runtime client. The client must have a scheme that knows the
// alpha4 SimulationExperiment, batch/v1 Job, and core/v1 Secret and
// ServiceAccount types.
func NewControllerRuntimeAdapter(c client.Client) *Adapter {
	return NewAdapter(&crClient{c: c})
}

func (k *crClient) GetExperiment(ctx context.Context, namespace, name string) (*experimentalpha4.SimulationExperiment, error) {
	exp := &experimentalpha4.SimulationExperiment{}
	if err := k.c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, exp); err != nil {
		return nil, err
	}
	return exp, nil
}

func (k *crClient) GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := k.c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func (k *crClient) GetServiceAccount(ctx context.Context, namespace, name string) (*corev1.ServiceAccount, error) {
	sa := &corev1.ServiceAccount{}
	if err := k.c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, sa); err != nil {
		return nil, err
	}
	return sa, nil
}

// CreateJob creates the Job and returns the server response. controller-runtime
// mutates the passed object with the server-assigned UID and any admission
// defaults; the adapter retains only the UID and ignores every other returned
// field per the successful-create trust boundary.
func (k *crClient) CreateJob(ctx context.Context, job *batchv1.Job) (*batchv1.Job, error) {
	if err := k.c.Create(ctx, job); err != nil {
		return nil, err
	}
	return job, nil
}

func (k *crClient) GetJob(ctx context.Context, namespace, name string) (*batchv1.Job, error) {
	job := &batchv1.Job{}
	if err := k.c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, job); err != nil {
		return nil, err
	}
	return job, nil
}

// DeleteJob deletes the Job at the deterministic name using the given UID as the
// sole precondition. A NotFound or UID-mismatch conflict is a successful no-op
// for the caller; other errors are returned.
func (k *crClient) DeleteJob(ctx context.Context, namespace, name string, uid types.UID) error {
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	uidCopy := uid
	if err := k.c.Delete(ctx, job, client.Preconditions{UID: &uidCopy}); err != nil {
		if apierrors.IsNotFound(err) || apierrors.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("delete job %s/%s: %w", namespace, name, err)
	}
	return nil
}
