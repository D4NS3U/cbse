package kube

import (
	"context"
	"fmt"

	experimentalpha2 "github.com/D4NS3U/cbse/experiment-operator/api/alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListSimulationExperiments fetches SimulationExperiment CRs from the cluster,
// optionally scoping the list to a namespace when one is provided. The slice of
// domain objects is returned so callers can reconcile desired state.
func ListSimulationExperiments(ctx context.Context, k8sClient client.Client, namespace string) ([]experimentalpha2.SimulationExperiment, error) {
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
