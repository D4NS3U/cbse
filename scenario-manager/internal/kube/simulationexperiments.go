// Copyright 2025-2026 Daniel Seufferth
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package kube

import (
	"context"
	"fmt"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListSimulationExperiments fetches SimulationExperiment CRs from the cluster,
// optionally scoping the list to a namespace when one is provided. The slice of
// domain objects is returned so callers can reconcile desired state.
func ListSimulationExperiments(ctx context.Context, k8sClient client.Client, namespace string) ([]experimentalpha4.SimulationExperiment, error) {
	var list experimentalpha4.SimulationExperimentList
	var opts []client.ListOption
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}

	if err := k8sClient.List(ctx, &list, opts...); err != nil {
		return nil, fmt.Errorf("list SimulationExperiments: %w", err)
	}

	return list.Items, nil
}
