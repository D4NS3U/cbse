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

package aggregate

import (
	"context"
	"encoding/json"
	"fmt"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/kube"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Store is the Core DB surface the aggregate pass uses. It is the
// resource-neutral subset of the persistence layer the tick needs; the
// concrete adapter wraps a *persistence.DB.
type Store interface {
	// ProjectIDByNamespaceAndName resolves the project id for the exact
	// (namespace, name) pair. It returns persistence.ErrProjectNotFound when
	// no row matches, so the tick can distinguish "not yet registered" from a
	// database error.
	ProjectIDByNamespaceAndName(ctx context.Context, namespace, project string) (int, error)
	// ScenarioStateCounts aggregates the scenario-state counts of one project
	// (total, Finished, Failed) over the configured scenario-status table.
	ScenarioStateCounts(ctx context.Context, projectID int) (persistence.ScenarioStateCounts, error)
}

// PersistenceStore adapts a persistence.DB to the aggregate Store interface.
type PersistenceStore struct {
	DB persistence.DB
}

func (s *PersistenceStore) ProjectIDByNamespaceAndName(ctx context.Context, namespace, project string) (int, error) {
	return persistence.ProjectIDByNamespaceAndName(ctx, s.DB, namespace, project)
}

func (s *PersistenceStore) ScenarioStateCounts(ctx context.Context, projectID int) (persistence.ScenarioStateCounts, error) {
	return persistence.ScenarioStateCountsByProject(ctx, s.DB, projectID)
}

// Kube is the Kubernetes surface the aggregate pass uses: the cluster-wide
// SimulationExperiment list (the same namespace scope the informer's watch
// uses) and the status-subresource verdict patch.
type Kube interface {
	// ListExperiments lists every SimulationExperiment cluster-wide.
	ListExperiments(ctx context.Context) ([]experimentalpha4.SimulationExperiment, error)
	// PatchVerdict reports the verdict through the status subresource writer
	// (client.Status().Patch) with a merge patch whose status payload carries
	// only scenarioManagerVerdict (D9 field ownership: never phase, never
	// message). The alpha4 CRD enables the status subresource, so a
	// main-resource patch would be silently discarded by the API server.
	PatchVerdict(ctx context.Context, namespace, name, verdict string) error
}

// kubeAdapter is the production Kube over a controller-runtime client.
type kubeAdapter struct {
	k8s client.Client
}

// NewKube builds the production Kube adapter over the given controller-runtime
// client. The client's scheme must know the alpha4 SimulationExperiment type.
func NewKube(k8s client.Client) Kube {
	return &kubeAdapter{k8s: k8s}
}

// listNamespace is the list scope of the aggregate pass: empty = cluster-wide,
// the same scope the informer's watch uses.
const listNamespace = ""

func (k *kubeAdapter) ListExperiments(ctx context.Context) ([]experimentalpha4.SimulationExperiment, error) {
	return kube.ListSimulationExperiments(ctx, k.k8s, listNamespace)
}

// patchPayload is the exact status merge patch the SM writes. Field ownership
// (D9): the status payload carries only scenarioManagerVerdict - never
// phase, never message.
type patchPayload struct {
	Status verdictStatus `json:"status"`
}

// verdictStatus is the SM-owned slice of the status subresource.
type verdictStatus struct {
	ScenarioManagerVerdict string `json:"scenarioManagerVerdict"`
}

func (k *kubeAdapter) PatchVerdict(ctx context.Context, namespace, name, verdict string) error {
	patch := patchPayload{}
	patch.Status.ScenarioManagerVerdict = verdict
	payload, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("encode verdict patch for %s/%s: %w", namespace, name, err)
	}
	exp := &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
	}
	// The status subresource writer: the alpha4 CRD enables
	// subresources.status, so the verdict must be written through
	// client.Status().Patch; a main-resource patch carries the status stanza
	// to the wrong endpoint and the API server discards it (nil error, no
	// effect) - exactly the precedent of the operator's Status().Patch phase
	// writer.
	if err := k.k8s.Status().Patch(ctx, exp, client.RawPatch(types.MergePatchType, payload)); err != nil {
		return fmt.Errorf("patch scenarioManagerVerdict on %s/%s: %w", namespace, name, err)
	}
	return nil
}
