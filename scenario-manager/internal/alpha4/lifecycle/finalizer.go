package lifecycle

import (
	"context"
	"fmt"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// EnsureFinalizer adds the SM cleanup finalizer to exp when absent, patches
// the object, and re-gets it. It returns the re-got object and a deleted flag.
// If deletion began between the patch and the re-get, deleted is true and the
// caller must enter deletion cleanup instead of registering or reusing a
// project row. The patch is a strategic-merge patch of the finalizers slice so
// only the SM finalizer field is touched.
func EnsureFinalizer(ctx context.Context, k8s client.Client, exp *experimentalpha4.SimulationExperiment) (*experimentalpha4.SimulationExperiment, bool, error) {
	if !controllerutil.ContainsFinalizer(exp, FinalizerName) {
		base := exp.DeepCopy()
		modified := exp.DeepCopy()
		controllerutil.AddFinalizer(modified, FinalizerName)
		if err := k8s.Patch(ctx, modified, client.MergeFrom(base)); err != nil {
			return nil, false, fmt.Errorf("add finalizer: %w", err)
		}
	}
	current, err := regetExperiment(ctx, k8s, exp)
	if err != nil {
		return nil, false, err
	}
	return current, !current.DeletionTimestamp.IsZero(), nil
}

// RemoveFinalizer removes the SM cleanup finalizer and patches the object. It
// is called only after verified deletion cleanup succeeds. The patch touches
// only the finalizers slice.
func RemoveFinalizer(ctx context.Context, k8s client.Client, exp *experimentalpha4.SimulationExperiment) error {
	if !controllerutil.ContainsFinalizer(exp, FinalizerName) {
		return nil
	}
	base := exp.DeepCopy()
	modified := exp.DeepCopy()
	controllerutil.RemoveFinalizer(modified, FinalizerName)
	if err := k8s.Patch(ctx, modified, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("remove finalizer: %w", err)
	}
	return nil
}

// regetExperiment fetches the current object so the caller observes the live
// deletion state and UID after a finalizer patch. A NotFound result is
// returned as a typed error so the caller can treat an already-gone object as
// satisfied cleanup.
func regetExperiment(ctx context.Context, k8s client.Client, exp *experimentalpha4.SimulationExperiment) (*experimentalpha4.SimulationExperiment, error) {
	current := &experimentalpha4.SimulationExperiment{}
	if err := k8s.Get(ctx, types.NamespacedName{Name: exp.Name, Namespace: exp.Namespace}, current); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrExperimentNotFound
		}
		return nil, fmt.Errorf("re-get experiment %s/%s: %w", exp.Namespace, exp.Name, err)
	}
	return current, nil
}

// ErrExperimentNotFound indicates the experiment object is gone. For deletion
// cleanup it means the object is already satisfied; for the finalizer path it
// means the object was deleted before the re-get.
var ErrExperimentNotFound = fmtErr("experiment not found")

// fmtErr wraps a string as a sentinel error comparable with errors.Is via
// pointer identity for package-internal use.
type sentinelError struct{ msg string }

func (e *sentinelError) Error() string { return e.msg }

func fmtErr(msg string) error { return &sentinelError{msg: msg} }
