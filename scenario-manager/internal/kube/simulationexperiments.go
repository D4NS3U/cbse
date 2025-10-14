package kube

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	experimentalpha2 "github.com/D4NS3U/cbse/experiment-operator/api/alpha2"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListSimulationExperiments discovers SimulationExperiment resources from the cluster.
// It tries the in-cluster configuration first and falls back to kubeconfig on disk.
func ListSimulationExperiments(ctx context.Context, namespace string) ([]experimentalpha2.SimulationExperiment, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes config: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("add core scheme: %w", err)
	}
	if err := experimentalpha2.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("add simulation experiment scheme: %w", err)
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}

	var list experimentalpha2.SimulationExperimentList
	var opts []client.ListOption
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}

	if err := k8sClient.List(ctx, &list, opts...); err != nil {
		return nil, fmt.Errorf("list SimulationExperiments: %w", err)
	}

	return list.Items, nil
}

func loadConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return nil, fmt.Errorf("HOME environment variable not set for kubeconfig lookup")
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("build config from %s: %w", kubeconfig, err)
	}

	return cfg, nil
}
