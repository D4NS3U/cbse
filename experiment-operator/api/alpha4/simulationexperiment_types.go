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

package alpha4

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:generate=true

// SimulationExperimentSpec defines the desired state of an alpha4 experiment.
// +kubebuilder:validation:XValidation:rule="self.detailDatabase == oldSelf.detailDatabase",message="spec.detailDatabase is immutable; delete and recreate the SimulationExperiment"
// +kubebuilder:validation:XValidation:rule="self.resultDatabase == oldSelf.resultDatabase",message="spec.resultDatabase is immutable; delete and recreate the SimulationExperiment"
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

	// Runner contains optional Simulation Runner workload customization.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	Runner RunnerSpec `json:"runner,omitempty"`

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

// +kubebuilder:validation:Required
type DatabaseSpec struct {
	// Either Image or Host must be specified (enforced by the controller).
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

// +kubebuilder:validation:Required
// +kubebuilder:validation:XValidation:rule="self.image == oldSelf.image",message="spec.translator.image is immutable; delete and recreate the SimulationExperiment"
// +kubebuilder:validation:XValidation:rule="self.repository == oldSelf.repository",message="spec.translator.repository is immutable; delete and recreate the SimulationExperiment"
// +kubebuilder:validation:XValidation:rule="self.baseimage == oldSelf.baseimage",message="spec.translator.baseimage is immutable; delete and recreate the SimulationExperiment"
// +kubebuilder:validation:XValidation:rule="self.builderImage == oldSelf.builderImage",message="spec.translator.builderImage is immutable; delete and recreate the SimulationExperiment"
// +kubebuilder:validation:XValidation:rule="self.serviceType == oldSelf.serviceType",message="spec.translator.serviceType is immutable; delete and recreate the SimulationExperiment"
// +kubebuilder:validation:XValidation:rule="self.port == oldSelf.port",message="spec.translator.port is immutable; delete and recreate the SimulationExperiment"
// +kubebuilder:validation:XValidation:rule="has(self.builderResources) == has(oldSelf.builderResources) && (!has(self.builderResources) || self.builderResources == oldSelf.builderResources)",message="spec.translator.builderResources is immutable; delete and recreate the SimulationExperiment"
// +kubebuilder:validation:XValidation:rule="has(self.command) == has(oldSelf.command) && (!has(self.command) || self.command == oldSelf.command)",message="spec.translator.command is immutable; delete and recreate the SimulationExperiment"
// +kubebuilder:validation:XValidation:rule="has(self.args) == has(oldSelf.args) && (!has(self.args) || self.args == oldSelf.args)",message="spec.translator.args is immutable; delete and recreate the SimulationExperiment"
// +kubebuilder:validation:XValidation:rule="has(self.nodePort) == has(oldSelf.nodePort) && (!has(self.nodePort) || self.nodePort == oldSelf.nodePort)",message="spec.translator.nodePort is immutable; delete and recreate the SimulationExperiment"
type TranslatorSpec struct {
	Image string `json:"image"`

	Repository string `json:"repository"`

	BaseImage string `json:"baseimage"`

	BuilderImage string `json:"builderImage"`

	// RegistryAuthSecretRef identifies the namespace-local Docker configuration Secret.
	// +kubebuilder:validation:XValidation:rule="self.name == 'cbse-registry-auth'",message="spec.translator.registryAuthSecretRef.name must equal cbse-registry-auth"
	RegistryAuthSecretRef corev1.LocalObjectReference `json:"registryAuthSecretRef"`

	// BuilderResources configures the rootless BuildKit sidecar. The controller
	// accepts only CPU and memory requests and limits.
	// +kubebuilder:validation:Optional
	BuilderResources *corev1.ResourceRequirements `json:"builderResources,omitempty"`

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
	Command []string `json:"command,omitempty"`

	// +kubebuilder:validation:Optional
	Args []string `json:"args,omitempty"`
}

// RunnerSpec defines optional runner Job customization.
// +kubebuilder:validation:XValidation:rule="has(self.jobTemplate) == has(oldSelf.jobTemplate) && (!has(self.jobTemplate) || self.jobTemplate == oldSelf.jobTemplate)",message="spec.runner.jobTemplate is immutable; delete and recreate the SimulationExperiment"
type RunnerSpec struct {
	// +kubebuilder:validation:Optional
	JobTemplate *batchv1.JobTemplateSpec `json:"jobTemplate,omitempty"`
}

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

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=simulationexperiments,shortName=simexp,scope=Namespaced
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Scenarios",type=integer,priority=1,JSONPath=".status.metrics.scenarioCount"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="self.metadata.name.size() <= 63 && self.metadata.name.matches('^[a-z0-9]([-a-z0-9]*[a-z0-9])?$')",message="metadata.name must be a lowercase DNS label of 1 to 63 characters"
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
