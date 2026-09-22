/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package alpha4 defines the SimulationExperiment custom resource for the
// experiment.cbse.terministic.de/v1alpha4 API group. It is the only active API
// version: the CRD serves and stores alpha4, the controller-manager registers
// only alpha4 into its runtime scheme, and the alpha4 reconciler reconciles
// it. The retired alpha2 and alpha3 groups are not served and have no
// conversion webhook.
//
// These types are consumed by the experiment-operator controller, the
// scenario-manager lifecycle gate, and the e2e harness. Many spec fields are
// immutable after creation (enforced by CEL validations) so that an in-flight
// experiment's topology cannot change underneath running workloads; the
// intended update path is to delete and recreate the SimulationExperiment.
// +kubebuilder:object:generate=true
// +groupName=experiment.cbse.terministic.de
package alpha4

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "experiment.cbse.terministic.de", Version: "alpha4"}

	// SchemeBuilder is used to add Go types to the GroupVersionKind scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
