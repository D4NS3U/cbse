// Package kube contains helpers for establishing Kubernetes API clients and
// interacting with SimulationExperiment custom resources.
package kube

import (
	"fmt"

	experimentalpha3 "github.com/D4NS3U/cbse/experiment-operator/api/alpha3"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// k8sClient stores the shared controller-runtime client instance used across
	// the Scenario Manager to query and mutate cluster resources.
	k8sClient client.Client
	// runningInCluster tracks whether an in-cluster configuration was detected
	// so callers can distinguish between local development and controller pods.
	runningInCluster bool
)

// RunningInCluster reports whether an in-cluster configuration was detected.
func RunningInCluster() bool {
	return runningInCluster
}

// KubeConnect initializes and retains the shared Kubernetes client tailored for SimulationExperiment resources.
// It attempts to load the in-cluster configuration and returns a descriptive
// error to the caller. Library code deliberately does not terminate the
// process, which keeps the package testable and lets the application decide
// how dependency failures should be handled.
func KubeConnect() error {
	if k8sClient != nil {
		return nil
	}

	cfg, err := rest.InClusterConfig()
	if err != nil {
		runningInCluster = false
		return fmt.Errorf("Kubernetes in-cluster config unavailable: %w", err)
	}

	runningInCluster = true

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add core scheme: %w", err)
	}
	if err := experimentalpha3.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add simulation experiment scheme: %w", err)
	}

	clientSet, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}

	k8sClient = clientSet
	return nil
}

// Client returns the shared Kubernetes client reference.
func Client() client.Client {
	return k8sClient
}
