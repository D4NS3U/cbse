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
	// IsFinalized reports whether the exact (namespace, name) pair carries the
	// durable finalization marker (the SBO finalization protocol). A missing
	// project is (false, nil), not an error.
	IsFinalized(ctx context.Context, namespace, project string) (bool, error)
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

func (s *PersistenceStore) IsFinalized(ctx context.Context, namespace, project string) (bool, error) {
	return persistence.IsFinalized(ctx, s.DB, namespace, project)
}

// Kube is the Kubernetes surface the aggregate pass uses: the cluster-wide
// SimulationExperiment list (the same namespace scope the informer's watch
// uses) and the status-subresource patches.
//
// Field ownership (the additive amendment of the SBO finalization protocol):
// the SM never writes phase; the SM writes status fields in exactly two
// cases - the absorbing scenarioManagerVerdict report and the
// awaiting-EDS-finalization InProgress diagnostic message. The operator
// retains full ownership of phase and all other messages (its terminal
// derivation overwrites the diagnostic message at the phase transition).
type Kube interface {
	// ListExperiments lists every SimulationExperiment cluster-wide.
	ListExperiments(ctx context.Context) ([]experimentalpha4.SimulationExperiment, error)
	// PatchVerdict reports the verdict through the status subresource writer
	// (client.Status().Patch) with a merge patch whose status payload carries
	// only scenarioManagerVerdict (never phase, never message).
	PatchVerdict(ctx context.Context, namespace, name, verdict string) error
	// PatchAwaitingFinalization writes the one SM-owned status.message case -
	// the InProgress awaiting-EDS-finalization diagnostic - through the
	// status subresource writer with a merge patch whose status payload
	// carries only message (never phase, never scenarioManagerVerdict).
	PatchAwaitingFinalization(ctx context.Context, namespace, name string) error
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

// patchPayload is the exact status merge patch the SM writes for the verdict
// report. Field ownership: the status payload carries only
// scenarioManagerVerdict - never phase, never message.
type patchPayload struct {
	Status verdictStatus `json:"status"`
}

// verdictStatus is the SM-owned slice of the status subresource carried by
// the verdict merge patch.
type verdictStatus struct {
	ScenarioManagerVerdict string `json:"scenarioManagerVerdict"`
}

// awaitingFinalizationMessage is the one status message the SM writes: the
// InProgress diagnostic while every scenario is terminal and the EDS
// finalization batch is still outstanding. The operator's terminal
// derivation (the Finished verdict requires the finalization, so it lands
// later in the sequence) overwrites the message at the phase transition.
const awaitingFinalizationMessage = "awaiting EDS finalization"

// messagePatchPayload is the exact status merge patch of the awaiting-
// finalization diagnostic. Field ownership: the status payload carries only
// message - never phase, never scenarioManagerVerdict.
type messagePatchPayload struct {
	Status messageStatus `json:"status"`
}

// messageStatus is the SM-owned slice of the status subresource carried by
// the diagnostic merge patch.
type messageStatus struct {
	Message string `json:"message"`
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

// PatchAwaitingFinalization writes the awaiting-EDS-finalization InProgress
// diagnostic through the status subresource. The merge patch carries only
// status.message (never phase, never scenarioManagerVerdict); the
// write-if-different guard lives in the pass, which skips the patch when the
// enumerated CR already carries the message.
func (k *kubeAdapter) PatchAwaitingFinalization(ctx context.Context, namespace, name string) error {
	patch := messagePatchPayload{}
	patch.Status.Message = awaitingFinalizationMessage
	payload, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("encode awaiting-finalization patch for %s/%s: %w", namespace, name, err)
	}
	exp := &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
	}
	if err := k.k8s.Status().Patch(ctx, exp, client.RawPatch(types.MergePatchType, payload)); err != nil {
		return fmt.Errorf("patch status.message on %s/%s: %w", namespace, name, err)
	}
	return nil
}
