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

package alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:generate=true

// SimulationExperimentSpec (alpha2) — BREAKING changes carried here
// - ExperimentalDesign(string) -> ExperimentalDesignService(struct)
// - Add Port to Translator and PostProcessingService
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
	ExperimentalDesignService ExperimentalDesignServiceSpec `json:"experimentalDesignService"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +kubebuilder:default=ClusterIP
	DefaultServiceType ServiceType `json:"defaultServiceType,omitempty"`
}

type ServiceType string

const (
	ServiceTypeClusterIP    ServiceType = "ClusterIP"
	ServiceTypeNodePort     ServiceType = "NodePort"
	ServiceTypeLoadBalancer ServiceType = "LoadBalancer"
)

// DatabaseSpec is unchanged from alpha1 (keep wire-compat where possible).
// +kubebuilder:validation:Required
type DatabaseSpec struct {
	// Either Image or Host must be specified (enforced by controller).
	Image    string `json:"image,omitempty"`
	Host     string `json:"host,omitempty"`
	DBName   string `json:"dbname"`
	User     string `json:"user"`
	Password string `json:"password"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +kubebuilder:default=ClusterIP
	ServiceType ServiceType `json:"serviceType,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=30000
	// +kubebuilder:validation:Maximum=32767
	NodePort *int32 `json:"nodePort,omitempty"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	Command []string `json:"command,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	Args []string `json:"args,omitempty"`
}

// TranslatorSpec now includes Port for service routing.
// +kubebuilder:validation:Required
type TranslatorSpec struct {
	// This is the image that contains the Translator logic.
	Image string `json:"image"`

	Repository string `json:"repository"`

	// Base image used to build executable model per scenario.
	BaseImage string `json:"baseimage"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +kubebuilder:default=ClusterIP
	ServiceType ServiceType `json:"serviceType,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=30000
	// +kubebuilder:validation:Maximum=32767
	NodePort *int32 `json:"nodePort,omitempty"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=8080
	Port int32 `json:"port,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	Command []string `json:"command,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	Args []string `json:"args,omitempty"`
}

// PostProcessingSpec now includes Port for service routing.
// +kubebuilder:validation:Required
type PostProcessingSpec struct {
	Image string `json:"image"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +kubebuilder:default=ClusterIP
	ServiceType ServiceType `json:"serviceType,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=30000
	// +kubebuilder:validation:Maximum=32767
	NodePort *int32 `json:"nodePort,omitempty"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=8080
	Port int32 `json:"port,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	Command []string `json:"command,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	Args []string `json:"args,omitempty"`
}

// ExperimentalDesignServiceSpec replaces the former string.
// Start minimal with Name to retain prior meaning; extensible later.
type ExperimentalDesignServiceSpec struct {

	// +kubebuilder:validation:MinLength=1
	Design string `json:"design,omitempty"`
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image,omitempty"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	Command []string `json:"command,omitempty"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	Args []string `json:"args,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +kubebuilder:default=ClusterIP
	ServiceType ServiceType `json:"serviceType,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=30000
	// +kubebuilder:validation:Maximum=32767
	NodePort *int32 `json:"nodePort,omitempty"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=8080
	Port int32 `json:"port,omitempty"`
}

// SimulationExperimentStatus is unchanged; keep wire-compat for clients.
type SimulationExperimentStatus struct {
	// +kubebuilder:validation:Enum=Pending;Provisioning;InProgress;Completed;Failed;Error
	Phase   string         `json:"phase,omitempty"`
	Message string         `json:"message,omitempty"`
	Metrics *StatusMetrics `json:"metrics,omitempty"`
}

type StatusMetrics struct {
	// +kubebuilder:validation:Minimum=0
	ScenarioCount int64 `json:"scenarioCount,omitempty"`
}

// +kubebuilder:storageversion
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:conversion:hub
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=simulationexperiments,shortName=simexp,scope=Namespaced
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Scenarios",type=integer,priority=1,JSONPath=".status.metrics.scenarioCount"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type SimulationExperiment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SimulationExperimentSpec   `json:"spec,omitempty"`
	Status            SimulationExperimentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type SimulationExperimentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SimulationExperiment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SimulationExperiment{}, &SimulationExperimentList{})
}
