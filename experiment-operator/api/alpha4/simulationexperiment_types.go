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

// SimulationExperimentSpec defines the desired state of an alpha4 experiment:
// the detail and result PostgreSQL databases, the translator, and the three
// scenario services (post-processing, experimental-design, and an optional
// runner). The detailDatabase and resultDatabase fields are immutable after
// creation (enforced by CEL); to change either, delete and recreate the
// SimulationExperiment so an in-flight experiment's data sources cannot move.
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

// ServiceType selects how a component Service is exposed on the cluster. It
// is a constrained subset of corev1.ServiceType that the controller is
// permitted to render for the databases, translator, and scenario services.
type ServiceType string

const (
	// ServiceTypeClusterIP exposes the service on a cluster-internal IP only.
	ServiceTypeClusterIP ServiceType = "ClusterIP"
	// ServiceTypeNodePort exposes the service on each node's IP at a static
	// port in the NodePort range (30000-32767).
	ServiceTypeNodePort ServiceType = "NodePort"
	// ServiceTypeLoadBalancer exposes the service through a cloud-provided load
	// balancer.
	ServiceTypeLoadBalancer ServiceType = "LoadBalancer"
)

// DatabaseSpec configures one of the experiment's PostgreSQL databases: the
// detail DB (scenario inputs) or the result DB (scenario outputs). Exactly one
// of Image (a container image the controller deploys and exposes) or Host (an
// existing, reachable database) must be set; the controller rejects a spec
// that supplies neither or both. Port is required, and NodePort applies only
// when ServiceType is NodePort.
//
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

// TranslatorSpec configures the reference translator deployment: its runtime
// image, the Git repository and base image it builds from, the rootless
// BuildKit builder sidecar image and resources, and the Service that fronts
// it. The image, repository, baseimage, builderImage, serviceType, port,
// builderResources, command, args, and nodePort fields are all immutable after
// creation (enforced by CEL); a change requires deleting and recreating the
// SimulationExperiment. RegistryAuthSecretRef must reference a Secret named
// cbse-registry-auth holding the Docker config the builder uses to pull images.
//
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

// RunnerSpec defines optional customization for the Simulation Runner Job.
// The jobTemplate field is immutable after creation (enforced by CEL); to
// change it, delete and recreate the SimulationExperiment. When jobTemplate is
// omitted the controller applies its default runner Job template.
// +kubebuilder:validation:XValidation:rule="has(self.jobTemplate) == has(oldSelf.jobTemplate) && (!has(self.jobTemplate) || self.jobTemplate == oldSelf.jobTemplate)",message="spec.runner.jobTemplate is immutable; delete and recreate the SimulationExperiment"
type RunnerSpec struct {
	// +kubebuilder:validation:Optional
	JobTemplate *batchv1.JobTemplateSpec `json:"jobTemplate,omitempty"`
}

// PostProcessingSpec configures the post-processing scenario service
// deployment and the Service that fronts it.
//
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

// ExperimentalDesignServiceSpec configures the experimental-design scenario
// service deployment: the design name, its image, and the Service that fronts
// it.
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

// SimulationExperimentStatus is the observed state the reconciler writes to
// the status subresource. Phase is the lifecycle phase (Pending,
// Provisioning, InProgress, Completed, Failed, Error), Message carries a
// human-readable detail string for the current phase, and Metrics holds
// count-based progress observed by the controller.
type SimulationExperimentStatus struct {
	// +kubebuilder:validation:Enum=Pending;Provisioning;InProgress;Completed;Failed;Error
	Phase   string         `json:"phase,omitempty"`
	Message string         `json:"message,omitempty"`
	Metrics *StatusMetrics `json:"metrics,omitempty"`
}

// StatusMetrics holds scenario-count progress the controller observes during
// a run.
type StatusMetrics struct {
	// +kubebuilder:validation:Minimum=0
	ScenarioCount int64 `json:"scenarioCount,omitempty"`
}

// SimulationExperiment is the root CRD kind for the alpha4 API group. A
// single instance describes a complete experiment topology (databases,
// translator, and scenario services) that the alpha4 reconciler materializes
// into Kubernetes Deployments, Services, and Jobs. It is the storage version,
// is namespaced, and reports progress through the status subresource. The
// object name must be a lowercase DNS label of at most 63 characters (enforced
// by CEL) so it is safe to use as a label value on owned resources.
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

// SimulationExperimentList is the list kind for SimulationExperiment, required
// by the Kubernetes API machinery for collection (LIST) operations.
// +kubebuilder:object:root=true
type SimulationExperimentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SimulationExperiment `json:"items"`
}

// init registers the SimulationExperiment and SimulationExperimentList types
// with the scheme builder so they are added to any runtime scheme that calls
// AddToScheme.
func init() {
	SchemeBuilder.Register(&SimulationExperiment{}, &SimulationExperimentList{})
}
