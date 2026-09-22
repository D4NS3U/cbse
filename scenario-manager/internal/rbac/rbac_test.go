package rbac

import (
	"context"
	"errors"
	"fmt"
	"testing"

	authv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

// policyClient builds a fake kubernetes.Interface whose SelfSubjectAccessReview
// responses are decided by the supplied policy function. A policy returns
// (allowed, reason). The "default" behaviour when the policy returns ok=false
// is to deny.
func policyClient(policy func(c Check) (allowed bool, reason string)) kubernetes.Interface {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(action clienttesting.Action) (bool, runtime.Object, error) {
		ca := action.(clienttesting.CreateAction)
		ssar := ca.GetObject().(*authv1.SelfSubjectAccessReview)
		ra := ssar.Spec.ResourceAttributes
		if ra == nil {
			return true, &authv1.SelfSubjectAccessReview{Status: authv1.SubjectAccessReviewStatus{Allowed: false, Reason: "no resource attributes"}}, nil
		}
		c := Check{Group: ra.Group, Resource: ra.Resource, Verb: ra.Verb, Name: ra.Name, Subresource: ra.Subresource, Namespace: ra.Namespace}
		allowed, reason := policy(c)
		return true, &authv1.SelfSubjectAccessReview{
			Status: authv1.SubjectAccessReviewStatus{Allowed: allowed, Reason: reason},
		}, nil
	})
	return cs
}

func errClient(err error) kubernetes.Interface {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, err
	})
	return cs
}

func TestRequiredChecksCoversExactContract(t *testing.T) {
	checks := RequiredChecks()
	want := []Check{
		{Group: Alpha4ExperimentGroup, Resource: "simulationexperiments", Verb: "get"},
		{Group: Alpha4ExperimentGroup, Resource: "simulationexperiments", Verb: "list"},
		{Group: Alpha4ExperimentGroup, Resource: "simulationexperiments", Verb: "watch"},
		{Group: Alpha4ExperimentGroup, Resource: "simulationexperiments", Verb: "patch"},
		{Group: "batch", Resource: "jobs", Verb: "create"},
		{Group: "batch", Resource: "jobs", Verb: "delete"},
		{Group: "batch", Resource: "jobs", Verb: "get"},
		{Group: "batch", Resource: "jobs", Verb: "list"},
		{Group: "batch", Resource: "jobs", Verb: "watch"},
		{Group: "", Resource: "secrets", Verb: "get", Name: RegistrySecretName},
		{Group: "", Resource: "serviceaccounts", Verb: "get"},
	}
	if len(checks) != len(want) {
		t.Fatalf("RequiredChecks has %d entries, want %d", len(checks), len(want))
	}
	for i, c := range checks {
		if c != want[i] {
			t.Fatalf("check %d = %+v, want %+v", i, c, want[i])
		}
	}
}

func TestVerifyAllAllowedReturnsNil(t *testing.T) {
	cs := policyClient(func(c Check) (bool, string) { return true, "ok" })
	if _, err := Verify(context.Background(), cs); err != nil {
		t.Fatalf("Verify with all allowed: unexpected error %v", err)
	}
}

func TestVerifyDeniedJobDeleteIsFatal(t *testing.T) {
	cs := policyClient(func(c Check) (bool, string) {
		if c.Group == "batch" && c.Resource == "jobs" && c.Verb == "delete" {
			return false, "RBAC: missing jobs/delete"
		}
		return true, "ok"
	})
	_, err := Verify(context.Background(), cs)
	if err == nil {
		t.Fatal("expected fatal error when jobs/delete is denied")
	}
	var sae *StartupAuthorizationError
	if !errors.As(err, &sae) {
		t.Fatalf("error type %T, want *StartupAuthorizationError", err)
	}
	if len(sae.Failures) != 1 {
		t.Fatalf("failures = %d, want 1", len(sae.Failures))
	}
	f := sae.Failures[0]
	if f.Check.Verb != "delete" || f.Check.Resource != "jobs" {
		t.Fatalf("failed check = %+v, want jobs/delete", f.Check)
	}
	if f.Allowed {
		t.Fatal("denied check reported Allowed=true")
	}
}

func TestVerifyDeniedExperimentPatchIsFatal(t *testing.T) {
	cs := policyClient(func(c Check) (bool, string) {
		if c.Group == Alpha4ExperimentGroup && c.Verb == "patch" {
			return false, "no experiment patch"
		}
		return true, "ok"
	})
	if _, err := Verify(context.Background(), cs); err == nil {
		t.Fatal("expected fatal error when experiment patch is denied")
	}
}

func TestVerifyDeniedSecretGetOnWrongNameSucceedsForNamedCheck(t *testing.T) {
	// The Secret check is constrained by resourceName. A policy that allows get
	// only on cbse-registry-auth must pass Verify; one that denies the named
	// check must fail.
	cs := policyClient(func(c Check) (bool, string) {
		if c.Resource == "secrets" {
			if c.Name == RegistrySecretName {
				return true, "ok"
			}
			return false, "wrong secret name"
		}
		return true, "ok"
	})
	if _, err := Verify(context.Background(), cs); err != nil {
		t.Fatalf("Verify with named-secret allowed: %v", err)
	}
}

func TestVerifyAggregatesMultipleDenials(t *testing.T) {
	cs := policyClient(func(c Check) (bool, string) {
		// Deny every verb on jobs and the secret.
		if c.Resource == "jobs" || c.Resource == "secrets" {
			return false, "denied"
		}
		return true, "ok"
	})
	_, err := Verify(context.Background(), cs)
	if err == nil {
		t.Fatal("expected error")
	}
	var sae *StartupAuthorizationError
	if !errors.As(err, &sae) {
		t.Fatalf("error type %T, want *StartupAuthorizationError", err)
	}
	// jobs has 5 verbs + secrets get = 6 denials.
	if len(sae.Failures) != 6 {
		t.Fatalf("failures = %d, want 6", len(sae.Failures))
	}
}

func TestVerifyReviewCallErrorIsFatal(t *testing.T) {
	cs := errClient(errors.New("apiserver unavailable"))
	_, err := Verify(context.Background(), cs)
	if err == nil {
		t.Fatal("expected fatal error when the review call fails")
	}
	var sae *StartupAuthorizationError
	if !errors.As(err, &sae) {
		t.Fatalf("error type %T, want *StartupAuthorizationError", err)
	}
	if len(sae.Failures) == 0 {
		t.Fatal("expected at least one failure")
	}
	for _, f := range sae.Failures {
		if f.Err == nil {
			t.Fatal("expected Err set on each review-call failure")
		}
	}
}

func TestVerifyEvaluatesEveryRequiredCheck(t *testing.T) {
	seen := map[Check]bool{}
	cs := policyClient(func(c Check) (bool, string) {
		seen[Check{Group: c.Group, Resource: c.Resource, Verb: c.Verb, Name: c.Name}] = true
		return true, "ok"
	})
	if _, err := Verify(context.Background(), cs); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	for _, c := range RequiredChecks() {
		key := Check{Group: c.Group, Resource: c.Resource, Verb: c.Verb, Name: c.Name}
		if !seen[key] {
			t.Fatalf("check not evaluated: %+v", c)
		}
	}
	if len(seen) != len(RequiredChecks()) {
		t.Fatalf("evaluated %d distinct checks, want %d", len(seen), len(RequiredChecks()))
	}
}

func TestStartupAuthorizationErrorMessage(t *testing.T) {
	e := &StartupAuthorizationError{Failures: []Decision{
		{Check: Check{Group: "batch", Resource: "jobs", Verb: "delete"}, Reason: "missing"},
	}}
	if e.Error() == "" {
		t.Fatal("empty error message")
	}
	// Multiple failures produce a multi-line message.
	e2 := &StartupAuthorizationError{Failures: []Decision{
		{Check: Check{Group: "batch", Resource: "jobs", Verb: "delete"}, Reason: "a"},
		{Check: Check{Group: "batch", Resource: "jobs", Verb: "create"}, Reason: "b"},
	}}
	msg := e2.Error()
	if msg == "" {
		t.Fatal("empty multi-failure message")
	}
	if !contains(msg, "2 startup authorization checks failed") {
		t.Fatalf("multi-failure message not aggregated: %s", msg)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

var _ = fmt.Sprintf
