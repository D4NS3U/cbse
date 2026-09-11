// Package rbac performs the Slice 06 startup authorization checks. Before
// Scenario Manager starts informers, NATS consumers, scenario selection,
// runner-start discovery, or observation, it verifies that its
// ServiceAccount holds every workload verb and resource the alpha4 contract
// requires, across namespaces. A denied check is a fatal installation error
// that prevents startup because the deployment cannot execute its contract.
//
// The checks use Kubernetes SelfSubjectAccessReview so the verification probes
// the effective RBAC decision (including bound ServiceAccount tokens and
// role bindings) rather than reading Role objects, which SM is not granted.
// Per-scenario runtime Forbidden responses during reconciliation do not call
// this package; they fail only the affected scenario and never terminate the
// process.
package rbac

import (
	"context"
	"fmt"

	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Alpha4ExperimentGroup is the API group of the alpha4 SimulationExperiment CR.
const Alpha4ExperimentGroup = "experiment.cbse.terministic.de"

// RegistrySecretName is the fixed core Secret SM is permitted to read for
// image-pull credentials. The Secret RBAC rule is constrained by resourceNames
// to this value; SM must not receive permission to read another Secret.
const RegistrySecretName = "cbse-registry-auth"

// Check is one required verb/resource authorization check.
type Check struct {
	Group       string // API group; "" for the core API
	Resource    string // plural resource name
	Verb        string // get, list, watch, create, delete, patch, ...
	Name        string // resourceName for named-resource rules (e.g. the Secret)
	Subresource string // optional subresource
	// Namespace is empty for cluster-wide checks. Alpha4 SM holds a
	// ClusterRole/ClusterRoleBinding because it discovers experiments and
	// creates, observes, and deletes Jobs across namespaces, so every required
	// check is cluster-wide.
	Namespace string
}

func (c Check) String() string {
	ns := c.Namespace
	if ns == "" {
		ns = "<cluster>"
	}
	name := c.Name
	if name == "" {
		name = "<any>"
	}
	return fmt.Sprintf("%s %s %q in group %q (ns=%s, name=%s)", c.Verb, c.Resource, c.Verb, c.Group, ns, name)
}

// RequiredChecks returns the exact set of workload permissions the default SM
// ServiceAccount must hold: get/list/watch/patch on alpha4
// simulationexperiments; create/delete/get/list/watch on batch jobs; get on the
// fixed registry Secret; and get on core serviceaccounts. SM receives no
// deletecollection, Pod, ConfigMap, Job update/patch, experiment
// status/update/delete, PriorityClass, RuntimeClass, or RBAC-management
// permission, so those are deliberately absent.
func RequiredChecks() []Check {
	return []Check{
		// Alpha4 SimulationExperiments: get, list, watch, patch (finalizer).
		{Group: Alpha4ExperimentGroup, Resource: "simulationexperiments", Verb: "get"},
		{Group: Alpha4ExperimentGroup, Resource: "simulationexperiments", Verb: "list"},
		{Group: Alpha4ExperimentGroup, Resource: "simulationexperiments", Verb: "watch"},
		{Group: Alpha4ExperimentGroup, Resource: "simulationexperiments", Verb: "patch"},
		// batch Jobs: create, delete, get, list, watch.
		{Group: "batch", Resource: "jobs", Verb: "create"},
		{Group: "batch", Resource: "jobs", Verb: "delete"},
		{Group: "batch", Resource: "jobs", Verb: "get"},
		{Group: "batch", Resource: "jobs", Verb: "list"},
		{Group: "batch", Resource: "jobs", Verb: "watch"},
		// Core Secret (fixed name): get only.
		{Group: "", Resource: "secrets", Verb: "get", Name: RegistrySecretName},
		// Core ServiceAccounts: get only (no list/watch).
		{Group: "", Resource: "serviceaccounts", Verb: "get"},
	}
}

// Decision reports the outcome of one check.
type Decision struct {
	Check   Check
	Allowed bool
	Reason  string
	Err     error
}

// Verify issues a SelfSubjectAccessReview for every RequiredChecks entry and
// returns nil only if every check is allowed. A denied check, or a check whose
// review call failed, is a fatal installation error. The returned error
// aggregates every failure so an operator sees the full permission gap. The
// allowed decisions are also returned for diagnostics and tests.
func Verify(ctx context.Context, k8s kubernetes.Interface) ([]Decision, error) {
	checks := RequiredChecks()
	decisions := make([]Decision, 0, len(checks))
	var failures []Decision
	for _, c := range checks {
		d := review(ctx, k8s, c)
		decisions = append(decisions, d)
		if !d.Allowed {
			failures = append(failures, d)
		}
	}
	if len(failures) == 0 {
		return decisions, nil
	}
	return decisions, &StartupAuthorizationError{Failures: failures}
}

func review(ctx context.Context, k8s kubernetes.Interface, c Check) Decision {
	ssar := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Group:       c.Group,
				Resource:    c.Resource,
				Verb:        c.Verb,
				Namespace:   c.Namespace,
				Name:        c.Name,
				Subresource: c.Subresource,
			},
		},
	}
	resp, err := k8s.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, ssar, metav1.CreateOptions{})
	if err != nil {
		return Decision{Check: c, Allowed: false, Err: err, Reason: err.Error()}
	}
	return Decision{Check: c, Allowed: resp.Status.Allowed, Reason: resp.Status.Reason}
}

// StartupAuthorizationError reports one or more denied or failed startup
// authorization checks.
type StartupAuthorizationError struct {
	Failures []Decision
}

func (e *StartupAuthorizationError) Error() string {
	if len(e.Failures) == 1 {
		f := e.Failures[0]
		if f.Err != nil {
			return fmt.Sprintf("startup authorization check failed for %s: %v", f.Check, f.Err)
		}
		return fmt.Sprintf("startup authorization denied for %s (reason: %s)", f.Check, f.Reason)
	}
	msg := fmt.Sprintf("%d startup authorization checks failed:", len(e.Failures))
	for _, f := range e.Failures {
		if f.Err != nil {
			msg += fmt.Sprintf("\n  - %s: %v", f.Check, f.Err)
		} else {
			msg += fmt.Sprintf("\n  - denied %s (reason: %s)", f.Check, f.Reason)
		}
	}
	return msg
}
