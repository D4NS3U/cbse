package kube

import (
	"log"

	experimentalpha2 "github.com/D4NS3U/cbse/experiment-operator/api/alpha2"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	k8sClient        client.Client
	runningInCluster bool
)

// RunningInCluster reports whether an in-cluster configuration was detected.
func RunningInCluster() bool {
	return runningInCluster
}

// KubeConnect initializes and retains the shared Kubernetes client tailored for SimulationExperiment resources.
// It automatically attempts to load the in-cluster configuration and reports whether a client was established.
func KubeConnect() bool {
	if k8sClient != nil {
		return true
	}

	cfg, err := rest.InClusterConfig()
	if err != nil {
		runningInCluster = false
		log.Printf("Kubernetes in-cluster config unavailable: %v", err)
		return false
	}

	runningInCluster = true

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		log.Printf("add core scheme: %v", err)
		return false
	}
	if err := experimentalpha2.AddToScheme(scheme); err != nil {
		log.Printf("add simulation experiment scheme: %v", err)
		return false
	}

	clientSet, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Printf("create Kubernetes client: %v", err)
		return false
	}

	k8sClient = clientSet
	log.Println("Kubernetes client connection established.")
	return true
}

// Client returns the shared Kubernetes client reference.
func Client() client.Client {
	return k8sClient
}
