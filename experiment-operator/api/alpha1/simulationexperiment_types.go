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

package alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SimulationExperimentSpec struct {
	// +kubebuilder:validation:Required
	DetailDatabase DatabaseSpec `json:"detailDatabase"`
	// +kubebuilder:validation:Required
	ResultDatabase DatabaseSpec `json:"resultDatabase"`
	// +kubebuilder:validation:Required
	Translator TranslatorSpec `json:"translator"`
	// +kubebuilder:validation:Required
	PostProcessingService PostProcessingSpec `json:"postProcessingService"`
	// +kubebuilder:validation:Required
	ExperimentalDesign string `json:"experimentalDesign"`
}

// DatabaseSpec model either a containerized or external database
// +kubebuilder:validation:Required
// Either Image or Host must be specified, not both
// +kubebuilder:validation:Xor=host,image
// Note: Xor requires custom validation logic; for CRD we enforce in controller
// +kubebuilder:validation:Required
type DatabaseSpec struct {
	// Either Image or Host have to specified, not both
	Image    string `json:"image,omitempty"`
	Host     string `json:"host,omitempty"`
	DBName   string `json:"dbname"`
	User     string `json:"user"`
	Password string `json:"password"`
	Port     int32  `json:"port"` // +kubebuilder:validation:Minimum=1 +kubebuilder:validation:Maximum=65535

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	// Command to override the container's entrypoint
	Command []string `json:"command,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	// Args to pass to the container
	Args []string `json:"args,omitempty"`
}

// TranslatorSpec defines the container that performs translation from scenario data to executable simulation models
// +kubebuilder:validation:Required
type TranslatorSpec struct {
	Image      string `json:"image"` // This is the image that contains the Translator logic
	Repository string `json:"repository"`
	BaseImage  string `json:"baseimage"` // This is the base image that is used by the Translator to create executable simulation models for each specific scenario

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	// Command to override the container's entrypoint
	Command []string `json:"command,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	// Args to pass to the container
	Args []string `json:"args,omitempty"`
}

// PostProcessingSpec defines the service used for evaluation after simulation
// +kubebuilder:validation:Required
type PostProcessingSpec struct {
	Image string `json:"image"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	// Command to override the container's entrypoint
	Command []string `json:"command,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	// Args to pass to the container
	Args []string `json:"args,omitempty"`
}

// SimulationExperimentStatus defines the observed state of SimulationExperiment.
// +kubebuilder:validation:Required
type SimulationExperimentStatus struct {
	// +kubebuilder:validation:Enum=Pending;Provisioning;InProgress;Completed;Failed;Error
	Phase   string         `json:"phase,omitempty"`   // Phase of the experiment (e.g., Pending, Provisioning, InProgress, Completed, Failed, Error)
	Message string         `json:"message,omitempty"` // Additional information about the experiment status
	Metrics *StatusMetrics `json:"metrics,omitempty"` // Optional nested metrics
}

type StatusMetrics struct {
	// +kubebuilder:validation:Minimum=0
	ScenarioCount int64 `json:"scenarioCount,omitempty"` // Number of scenarios processed
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=simulationexperiments,shortName=simexp,scope=Namespaced
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Scenarios",type=integer,priority=1,JSONPath=".status.metrics.scenarioCount"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// SimulationExperiment is the Schema for the simulationexperiments API.
type SimulationExperiment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SimulationExperimentSpec   `json:"spec,omitempty"`
	Status SimulationExperimentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SimulationExperimentList contains a list of SimulationExperiment.
type SimulationExperimentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SimulationExperiment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SimulationExperiment{}, &SimulationExperimentList{})
}
