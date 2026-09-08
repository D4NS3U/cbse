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

package jobtemplate

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestValidateAndNormalizeAbsentTemplate(t *testing.T) {
	got, errs := ValidateAndNormalizeJobTemplate(nil)
	if got != nil || len(errs) != 0 {
		t.Fatalf("absent template returned copy=%#v errors=%v", got, errs)
	}
}

func TestValidateAndNormalizeCompleteAllowedSurface(t *testing.T) {
	original := completeTemplate()
	want := original.DeepCopy()

	normalized, errs := ValidateAndNormalizeJobTemplate(original)
	if len(errs) != 0 {
		t.Fatalf("complete allowed template rejected:\n%s", errs.ToAggregate())
	}
	if normalized == original {
		t.Fatal("validator returned the original template pointer")
	}
	if !reflect.DeepEqual(original, want) {
		t.Fatal("validator mutated the persisted template")
	}

	normalized.Labels["integration.example/changed"] = "true"
	normalized.Spec.Template.Spec.Containers[0].Env[0].Value = "changed"
	normalized.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU] = resource.MustParse("2")
	if !reflect.DeepEqual(original, want) {
		t.Fatal("returned template shares nested mutable state with the persisted template")
	}

	names := pullSecretNames(normalized.Spec.Template.Spec.ImagePullSecrets)
	if got, want := strings.Join(names, ","), "a-extra,cbse-registry-auth,z-extra"; got != want {
		t.Fatalf("normalized pull Secrets = %q, want %q", got, want)
	}
}

func TestValidationFailureReturnsUnnormalizedDeepCopy(t *testing.T) {
	original := completeTemplate()
	original.Spec.ActiveDeadlineSeconds = int64Pointer(0)
	want := original.DeepCopy()

	copy, errs := ValidateAndNormalizeJobTemplate(original)
	if len(errs) == 0 {
		t.Fatal("invalid active deadline was accepted")
	}
	if copy == original || !reflect.DeepEqual(original, want) || !reflect.DeepEqual(copy, want) {
		t.Fatalf("failure copy contract violated: same=%v originalChanged=%v copyChanged=%v", copy == original, !reflect.DeepEqual(original, want), !reflect.DeepEqual(copy, want))
	}
	if got := strings.Join(pullSecretNames(copy.Spec.Template.Spec.ImagePullSecrets), ","); got != "z-extra,cbse-registry-auth,a-extra" {
		t.Fatalf("invalid copy was normalized before validation completed: %q", got)
	}
}

func TestInvalidTemplateMatrix(t *testing.T) {
	falseValue := false
	zero := int64(0)
	negative := int64(-1)
	badMode := int32(01000)
	badRestart := corev1.ContainerRestartPolicy("Sometimes")
	badPullPolicy := corev1.PullPolicy("sometimes")
	badTerminationPolicy := corev1.TerminationMessagePolicy("sometimes")
	badMountPropagation := corev1.MountPropagationMode("Everywhere")
	badRecursive := corev1.RecursiveReadOnlyMode("Maybe")
	badPreemption := corev1.PreemptionPolicy("Maybe")
	badFSGroupPolicy := corev1.PodFSGroupChangePolicy("Sometimes")

	tests := []struct {
		name   string
		path   string
		mutate func(*batchv1.JobTemplateSpec)
	}{
		{name: "job name", path: ".metadata.name", mutate: func(v *batchv1.JobTemplateSpec) { v.Name = "owned" }},
		{name: "job namespace", path: ".metadata.namespace", mutate: func(v *batchv1.JobTemplateSpec) { v.Namespace = "owned" }},
		{name: "job owner reference", path: ".metadata.ownerReferences", mutate: func(v *batchv1.JobTemplateSpec) {
			v.OwnerReferences = []metav1.OwnerReference{{Name: "owner"}}
		}},
		{name: "job finalizer", path: ".metadata.finalizers", mutate: func(v *batchv1.JobTemplateSpec) { v.Finalizers = []string{"example.com/final"} }},
		{name: "invalid job label", path: ".metadata.labels", mutate: func(v *batchv1.JobTemplateSpec) { v.Labels["invalid key"] = "value" }},
		{name: "oversized annotation", path: ".metadata.annotations", mutate: func(v *batchv1.JobTemplateSpec) { v.Annotations["example.com/large"] = strings.Repeat("x", 270000) }},
		{name: "reserved job label prefix", path: ".metadata.labels", mutate: func(v *batchv1.JobTemplateSpec) { v.Labels[identityMetadataPrefix+"project"] = "forged" }},
		{name: "reserved Job controller label", path: ".metadata.labels", mutate: func(v *batchv1.JobTemplateSpec) { v.Labels["batch.kubernetes.io/job-name"] = "forged" }},
		{name: "pod name", path: ".template.metadata.name", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Name = "owned" }},
		{name: "reserved pod annotation", path: ".template.metadata.annotations", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Annotations["controller-uid"] = "forged" }},
		{name: "zero active deadline", path: ".activeDeadlineSeconds", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.ActiveDeadlineSeconds = &zero }},
		{name: "negative active deadline", path: ".activeDeadlineSeconds", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.ActiveDeadlineSeconds = &negative }},
		{name: "parallelism", path: ".spec.parallelism", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Parallelism = int32Pointer(1) }},
		{name: "suspend false is observably present", path: ".spec.suspend", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Suspend = &falseValue }},
		{name: "success policy", path: ".spec.successPolicy", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.SuccessPolicy = &batchv1.SuccessPolicy{} }},
		{name: "missing runner", path: ".containers", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers = v.Spec.Template.Spec.Containers[1:]
		}},
		{name: "duplicate runner", path: ".containers", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers = append(v.Spec.Template.Spec.Containers, corev1.Container{Name: "runner"})
		}},
		{name: "invalid container name", path: ".containers[1].name", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[1].Name = "INVALID" }},
		{name: "duplicate init name", path: ".initContainers[0].name", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.InitContainers[0].Name = "sidecar" }},
		{name: "runner image", path: ".containers[0].image", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[0].Image = "forbidden" }},
		{name: "runner command", path: ".containers[0].command", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[0].Command = []string{"forbidden"} }},
		{name: "runner args", path: ".containers[0].args", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[0].Args = []string{"forbidden"} }},
		{name: "auxiliary image absent", path: ".containers[1].image", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[1].Image = "" }},
		{name: "auxiliary image whitespace", path: ".containers[1].image", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[1].Image = " image " }},
		{name: "regular restart policy", path: ".containers[1].restartPolicy", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[1].RestartPolicy = pointer(corev1.ContainerRestartPolicyAlways)
		}},
		{name: "invalid init restart policy", path: ".initContainers[0].restartPolicy", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.InitContainers[0].RestartPolicy = &badRestart }},
		{name: "ordinary init probe", path: ".initContainers[0].livenessProbe", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.InitContainers[0].LivenessProbe = execProbe() }},
		{name: "ordinary init lifecycle", path: ".initContainers[0].lifecycle", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.InitContainers[0].Lifecycle = sleepLifecycle(1) }},
		{name: "environment value and source", path: ".env[0].valueFrom", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].Env[0].ValueFrom = &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}
		}},
		{name: "empty envFrom source", path: ".envFrom[0]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].EnvFrom[0] = corev1.EnvFromSource{}
		}},
		{name: "invalid downward divisor", path: ".resourceFieldRef.divisor", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].Env[2].ValueFrom.ResourceFieldRef.Divisor = resource.MustParse("2m")
		}},
		{name: "unsupported volume", path: ".volumes[0].hostPath", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Volumes[0].HostPath = &corev1.HostPathVolumeSource{Path: "/host"}
		}},
		{name: "new image volume", path: ".volumes[0].image", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Volumes[0].Image = &corev1.ImageVolumeSource{Reference: "example.invalid/image:tag"}
		}},
		{name: "missing volume source", path: ".volumes[0]", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Volumes[0].VolumeSource = corev1.VolumeSource{} }},
		{name: "multiple volume sources", path: ".volumes[0]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Volumes[0].ConfigMap = &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "also"}}
		}},
		{name: "duplicate volume name", path: ".volumes[1].name", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Volumes[1].Name = "work" }},
		{name: "negative emptyDir size", path: ".volumes[0].emptyDir.sizeLimit", mutate: func(v *batchv1.JobTemplateSpec) {
			q := resource.MustParse("-1Mi")
			v.Spec.Template.Spec.Volumes[0].EmptyDir.SizeLimit = &q
		}},
		{name: "missing ConfigMap", path: ".volumes[1].configMap.name", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Volumes[1].ConfigMap.Name = "" }},
		{name: "bad item key", path: ".volumes[1].configMap.items[0].key", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Volumes[1].ConfigMap.Items[0].Key = "bad key" }},
		{name: "bad item path", path: ".volumes[1].configMap.items[0].path", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Volumes[1].ConfigMap.Items[0].Path = "../secret"
		}},
		{name: "bad file mode", path: ".defaultMode", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Volumes[1].ConfigMap.DefaultMode = &badMode }},
		{name: "missing Secret", path: ".volumes[2].secret.secretName", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Volumes[2].Secret.SecretName = "" }},
		{name: "missing PVC", path: ".volumes[3].persistentVolumeClaim.claimName", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Volumes[3].PersistentVolumeClaim.ClaimName = "" }},
		{name: "downward source union", path: ".volumes[4].downwardAPI.items[0]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Volumes[4].DownwardAPI.Items[0].ResourceFieldRef = validVolumeResourceFieldRef()
		}},
		{name: "downward unsupported field", path: ".fieldPath", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Volumes[4].DownwardAPI.Items[0].FieldRef.FieldPath = "spec.nodeName"
		}},
		{name: "downward missing container", path: ".containerName", mutate: func(v *batchv1.JobTemplateSpec) { projectedDownward(v).ResourceFieldRef.ContainerName = "" }},
		{name: "projected service account token", path: ".serviceAccountToken", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Volumes[5].Projected.Sources[0].ServiceAccountToken = &corev1.ServiceAccountTokenProjection{Path: "token"}
		}},
		{name: "projected multiple source union", path: ".sources[0]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Volumes[5].Projected.Sources[0].Secret = &corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "also"}}
		}},
		{name: "projected duplicate path", path: ".path", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Volumes[5].Projected.Sources[1].Secret.Items[0].Path = "projected/config"
		}},
		{name: "empty pull Secret", path: ".imagePullSecrets[0].name", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.ImagePullSecrets[0].Name = "" }},
		{name: "duplicate pull Secret", path: ".imagePullSecrets[2].name", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.ImagePullSecrets[2].Name = "z-extra" }},
		{name: "protected mirror annotation", path: ".template.metadata.annotations", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Annotations["kubernetes.io/config.mirror"] = "x" }},
		{name: "protected scheduler annotation", path: ".template.metadata.annotations", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Annotations["scheduler.alpha.kubernetes.io/tolerations"] = "x"
		}},
		{name: "protected seccomp pod annotation", path: ".template.metadata.annotations", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Annotations["seccomp.security.alpha.kubernetes.io/pod"] = "x"
		}},
		{name: "protected seccomp container annotation", path: ".template.metadata.annotations", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Annotations["container.seccomp.security.alpha.kubernetes.io/runner"] = "x"
		}},
		{name: "protected AppArmor annotation", path: ".template.metadata.annotations", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Annotations["container.apparmor.security.beta.kubernetes.io/runner"] = "x"
		}},
		{name: "pod restart policy", path: ".template.spec.restartPolicy", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyNever }},
		{name: "service account", path: ".serviceAccountName", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.ServiceAccountName = "forbidden" }},
		{name: "token automount false", path: ".automountServiceAccountToken", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.AutomountServiceAccountToken = &falseValue }},
		{name: "scheduler", path: ".schedulerName", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.SchedulerName = "other" }},
		{name: "node name", path: ".nodeName", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.NodeName = "node" }},
		{name: "scheduling gate", path: ".schedulingGates", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.SchedulingGates = []corev1.PodSchedulingGate{{Name: "gate"}}
		}},
		{name: "pod resource claim", path: ".resourceClaims", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.ResourceClaims = []corev1.PodResourceClaim{{Name: "claim"}}
		}},
		{name: "new Pod resources", path: ".template.spec.resources", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Resources = &corev1.ResourceRequirements{} }},
		{name: "host network", path: ".hostNetwork", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.HostNetwork = true }},
		{name: "ephemeral container", path: ".ephemeralContainers", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.EphemeralContainers = []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debug"}}}
		}},
		{name: "invalid nodeSelector", path: ".nodeSelector", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.NodeSelector["bad key"] = "x" }},
		{name: "node affinity empty terms", path: ".nodeSelectorTerms", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = nil
		}},
		{name: "node affinity bad operator", path: ".operator", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Operator = "Bad"
		}},
		{name: "node affinity bad weight", path: ".weight", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0].Weight = 101
		}},
		{name: "pod affinity missing topology", path: ".topologyKey", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].TopologyKey = ""
		}},
		{name: "pod affinity invalid namespace", path: ".namespaces[0]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].Namespaces[0] = "Bad"
		}},
		{name: "pod affinity key collision", path: ".matchLabelKeys[0]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].MatchLabelKeys[0] = "app"
		}},
		{name: "toleration invalid effect", path: ".tolerations[0].effect", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Tolerations[0].Effect = "Bad" }},
		{name: "topology zero skew", path: ".maxSkew", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.TopologySpreadConstraints[0].MaxSkew = 0 }},
		{name: "topology minDomains action", path: ".minDomains", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.TopologySpreadConstraints[0].WhenUnsatisfiable = corev1.ScheduleAnyway
		}},
		{name: "topology bad node policy", path: ".nodeAffinityPolicy", mutate: func(v *batchv1.JobTemplateSpec) {
			bad := corev1.NodeInclusionPolicy("Bad")
			v.Spec.Template.Spec.TopologySpreadConstraints[0].NodeAffinityPolicy = &bad
		}},
		{name: "topology duplicate pair", path: ".topologySpreadConstraints[1]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.TopologySpreadConstraints = append(v.Spec.Template.Spec.TopologySpreadConstraints, *v.Spec.Template.Spec.TopologySpreadConstraints[0].DeepCopy())
		}},
		{name: "bad preemption", path: ".preemptionPolicy", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.PreemptionPolicy = &badPreemption }},
		{name: "empty runtime class", path: ".runtimeClassName", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.RuntimeClassName = pointer("") }},
		{name: "negative termination grace", path: ".terminationGracePeriodSeconds", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.TerminationGracePeriodSeconds = int64Pointer(-1)
		}},
		{name: "bad DNS policy", path: ".dnsPolicy", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.DNSPolicy = "Bad" }},
		{name: "DNSNone missing config", path: ".dnsConfig", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.DNSConfig = nil }},
		{name: "too many nameservers", path: ".nameservers", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.DNSConfig.Nameservers = []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4"}
		}},
		{name: "invalid DNS search", path: ".searches[0]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.DNSConfig.Searches[0] = "Bad Search"
		}},
		{name: "empty DNS option", path: ".options[0].name", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.DNSConfig.Options[0].Name = ""
		}},
		{name: "invalid host alias IP", path: ".hostAliases[0].ip", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.HostAliases[0].IP = "invalid" }},
		{name: "pod UID zero", path: ".securityContext.runAsUser", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.SecurityContext.RunAsUser = int64Pointer(0) }},
		{name: "supplemental group zero", path: ".supplementalGroups[0]", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.SecurityContext.SupplementalGroups[0] = 0 }},
		{name: "bad fsGroup policy", path: ".fsGroupChangePolicy", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.SecurityContext.FSGroupChangePolicy = &badFSGroupPolicy
		}},
		{name: "Pod runAsNonRoot override", path: ".securityContext.runAsNonRoot", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.SecurityContext.RunAsNonRoot = &falseValue }},
		{name: "new supplementalGroupsPolicy", path: ".supplementalGroupsPolicy", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.SecurityContext.SupplementalGroupsPolicy = pointer(corev1.SupplementalGroupsPolicyStrict)
		}},
		{name: "container UID zero", path: ".containers[0].securityContext.runAsUser", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].SecurityContext.RunAsUser = int64Pointer(0)
		}},
		{name: "privileged false is present", path: ".securityContext.privileged", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].SecurityContext.Privileged = &falseValue
		}},
		{name: "capability override", path: ".securityContext.capabilities", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].SecurityContext.Capabilities = &corev1.Capabilities{Add: []corev1.Capability{"NET_ADMIN"}}
		}},
		{name: "resource claim", path: ".resources.claims", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{{Name: "claim"}}
		}},
		{name: "invalid resource name", path: ".resources.limits[requests.example.com/foo]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].Resources.Limits["requests.example.com/foo"] = resource.MustParse("1")
		}},
		{name: "negative resource", path: ".resources.requests[cpu]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("-1")
		}},
		{name: "milli precision change", path: ".resources.requests[cpu]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("0.0001")
		}},
		{name: "request greater than limit", path: ".resources.requests[memory]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse("1Gi")
		}},
		{name: "invalid huge-page name", path: ".resources.limits[hugepages-nope]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].Resources.Limits["hugepages-nope"] = resource.MustParse("1Mi")
		}},
		{name: "indivisible huge-page quantity", path: ".resources.limits[hugepages-2Mi]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].Resources.Limits["hugepages-2Mi"] = resource.MustParse("3Mi")
		}},
		{name: "huge-page request without limit", path: ".resources.limits[hugepages-2Mi]", mutate: func(v *batchv1.JobTemplateSpec) {
			delete(v.Spec.Template.Spec.Containers[0].Resources.Limits, "hugepages-2Mi")
		}},
		{name: "extended resource fractional", path: ".resources.limits[example.com/gpu]", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].Resources.Limits["example.com/gpu"] = resource.MustParse("1500m")
		}},
		{name: "bad container port", path: ".ports[0].containerPort", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort = 70000 }},
		{name: "bad container protocol", path: ".ports[0].protocol", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[0].Ports[0].Protocol = "BAD" }},
		{name: "duplicate port name", path: ".ports[1].name", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].Ports = append(v.Spec.Template.Spec.Containers[0].Ports, v.Spec.Template.Spec.Containers[0].Ports[0])
		}},
		{name: "duplicate host port", path: ".containers[1].ports[0].hostPort", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].Ports[0].HostPort = 9090
			v.Spec.Template.Spec.Containers[0].Ports[0].HostIP = ""
			v.Spec.Template.Spec.Containers[1].Ports[0].HostPort = 9090
		}},
		{name: "broken mount reference", path: ".volumeMounts[0].name", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[0].VolumeMounts[0].Name = "missing" }},
		{name: "duplicate mount path", path: ".volumeMounts[2].mountPath", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].VolumeMounts = append(v.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "auth", MountPath: "/config"})
		}},
		{name: "mount backstep", path: ".mountPath", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath = "/a/../b"
		}},
		{name: "subPath collision", path: ".subPathExpr", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].VolumeMounts[0].SubPath = "a"
			v.Spec.Template.Spec.Containers[0].VolumeMounts[0].SubPathExpr = "b"
		}},
		{name: "absolute subPath", path: ".subPath", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].VolumeMounts[0].SubPath = "/absolute"
		}},
		{name: "bad mount propagation", path: ".mountPropagation", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPropagation = &badMountPropagation
		}},
		{name: "bad recursive read-only", path: ".recursiveReadOnly", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].VolumeMounts[0].RecursiveReadOnly = &badRecursive
		}},
		{name: "device on non-PVC", path: ".volumeDevices[0].name", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[0].VolumeDevices[0].Name = "config" }},
		{name: "device mount overlap", path: ".volumeDevices[0].name", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].VolumeMounts = append(v.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "data", MountPath: "/data"})
		}},
		{name: "empty probe handler", path: ".livenessProbe", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].LivenessProbe.ProbeHandler = corev1.ProbeHandler{}
		}},
		{name: "multiple probe handlers", path: ".livenessProbe", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].LivenessProbe.HTTPGet = &corev1.HTTPGetAction{Path: "/", Port: intstr.FromInt32(8080)}
		}},
		{name: "bad HTTP scheme", path: ".scheme", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].ReadinessProbe.HTTPGet.Scheme = "FTP"
		}},
		{name: "negative probe timing", path: ".periodSeconds", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[0].LivenessProbe.PeriodSeconds = -1 }},
		{name: "liveness success threshold", path: ".successThreshold", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].LivenessProbe.SuccessThreshold = 2
		}},
		{name: "readiness termination grace", path: ".readinessProbe.terminationGracePeriodSeconds", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].ReadinessProbe.TerminationGracePeriodSeconds = int64Pointer(5)
		}},
		{name: "lifecycle sleep too long", path: ".sleep.seconds", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[1].Lifecycle = sleepLifecycle(31) }},
		{name: "bad pull policy", path: ".imagePullPolicy", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[0].ImagePullPolicy = badPullPolicy }},
		{name: "bad termination policy", path: ".terminationMessagePolicy", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].TerminationMessagePolicy = badTerminationPolicy
		}},
		{name: "container resize policy", path: ".resizePolicy", mutate: func(v *batchv1.JobTemplateSpec) {
			v.Spec.Template.Spec.Containers[0].ResizePolicy = []corev1.ContainerResizePolicy{{ResourceName: corev1.ResourceCPU}}
		}},
		{name: "container stdin", path: ".stdin", mutate: func(v *batchv1.JobTemplateSpec) { v.Spec.Template.Spec.Containers[0].Stdin = true }},
		{name: "new lifecycle stop signal", path: ".lifecycle.stopSignal", mutate: func(v *batchv1.JobTemplateSpec) {
			signal := corev1.SIGTERM
			v.Spec.Template.Spec.Containers[1].Lifecycle.StopSignal = &signal
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := completeTemplate()
			test.mutate(template)
			_, errs := ValidateAndNormalizeJobTemplate(template)
			if len(errs) == 0 {
				t.Fatal("invalid template was accepted")
			}
			if !strings.Contains(errs.ToAggregate().Error(), test.path) {
				t.Fatalf("errors do not identify field suffix %q:\n%s", test.path, errs.ToAggregate())
			}
		})
	}
}

func TestZeroValuesThatCannotBeDistinguishedFromOmission(t *testing.T) {
	template := completeTemplate()
	template.Spec.Template.Spec.RestartPolicy = ""
	template.Spec.Template.Spec.Containers[0].Stdin = false
	template.Spec.Template.Spec.Containers[0].ImagePullPolicy = ""
	template.Spec.Template.Spec.Containers[0].TerminationMessagePolicy = ""
	template.Spec.Template.Spec.Containers[0].LivenessProbe.SuccessThreshold = 0
	template.Spec.Template.Spec.Containers[0].LivenessProbe.PeriodSeconds = 0

	if _, errs := ValidateAndNormalizeJobTemplate(template); len(errs) != 0 {
		t.Fatalf("indistinguishable Go zero values rejected:\n%s", errs.ToAggregate())
	}
}

func TestKubernetes130PinnedGoldenFixtures(t *testing.T) {
	accepted := []struct {
		name   string
		mutate func(*batchv1.JobTemplateSpec)
	}{
		{
			name: "legacy IP spellings remain valid",
			mutate: func(template *batchv1.JobTemplateSpec) {
				template.Spec.Template.Spec.DNSConfig.Nameservers[0] = "010.002.003.004"
				template.Spec.Template.Spec.HostAliases[0].IP = "::ffff:1.2.3.4"
			},
		},
		{
			name: "emptyDir medium is not semantically constrained",
			mutate: func(template *batchv1.JobTemplateSpec) {
				template.Spec.Template.Spec.Volumes[0].EmptyDir.Medium = "FutureMedium"
			},
		},
		{
			name: "negative toleration seconds is accepted",
			mutate: func(template *batchv1.JobTemplateSpec) {
				template.Spec.Template.Spec.Tolerations[0].TolerationSeconds = int64Pointer(-1)
			},
		},
		{
			name: "topology match keys preserve duplicate entries",
			mutate: func(template *batchv1.JobTemplateSpec) {
				template.Spec.Template.Spec.TopologySpreadConstraints[0].MatchLabelKeys = []string{"rollout", "rollout"}
			},
		},
		{
			name: "topology key is constrained only to non-empty",
			mutate: func(template *batchv1.JobTemplateSpec) {
				template.Spec.Template.Spec.TopologySpreadConstraints[0].TopologyKey = "not a label key"
			},
		},
		{
			name: "relaxed environment variable name is accepted",
			mutate: func(template *batchv1.JobTemplateSpec) {
				template.Spec.Template.Spec.Containers[0].Env[0].Name = "feature.flag-name"
			},
		},
		{
			name: "k8s.io subdomain is an extended resource namespace",
			mutate: func(template *batchv1.JobTemplateSpec) {
				name := corev1.ResourceName("example.k8s.io/device")
				template.Spec.Template.Spec.Containers[0].Resources.Limits[name] = resource.MustParse("1")
				template.Spec.Template.Spec.Containers[0].Resources.Requests[name] = resource.MustParse("1")
			},
		},
	}
	for _, test := range accepted {
		t.Run("accept/"+test.name, func(t *testing.T) {
			template := completeTemplate()
			test.mutate(template)
			if _, errs := ValidateAndNormalizeJobTemplate(template); len(errs) != 0 {
				t.Fatalf("Kubernetes 1.30 golden fixture rejected:\n%s", errs.ToAggregate())
			}
		})
	}

	rejected := []struct {
		name   string
		path   string
		mutate func(*batchv1.JobTemplateSpec)
	}{
		{
			name: "invalid IP",
			path: ".dnsConfig.nameservers[0]",
			mutate: func(template *batchv1.JobTemplateSpec) {
				template.Spec.Template.Spec.DNSConfig.Nameservers[0] = "300.2.3.4"
			},
		},
		{
			name: "negative emptyDir size",
			path: ".emptyDir.sizeLimit",
			mutate: func(template *batchv1.JobTemplateSpec) {
				template.Spec.Template.Spec.Volumes[0].EmptyDir.SizeLimit = quantityPointer("-1Mi")
			},
		},
		{
			name: "toleration seconds requires NoExecute",
			path: ".tolerations[0].effect",
			mutate: func(template *batchv1.JobTemplateSpec) {
				template.Spec.Template.Spec.Tolerations[0].Effect = corev1.TaintEffectNoSchedule
			},
		},
	}
	for _, test := range rejected {
		t.Run("reject/"+test.name, func(t *testing.T) {
			template := completeTemplate()
			test.mutate(template)
			_, errs := ValidateAndNormalizeJobTemplate(template)
			if len(errs) == 0 || !strings.Contains(errs.ToAggregate().Error(), test.path) {
				t.Fatalf("Kubernetes 1.30 golden fixture did not reject %q at %q: %v", test.name, test.path, errs)
			}
		})
	}
}

func TestValidationCollectsFailuresAndDoesNotExposeTemplateValues(t *testing.T) {
	template := completeTemplate()
	template.Spec.ActiveDeadlineSeconds = int64Pointer(0)
	template.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "BAD=NAME", Value: "SENSITIVE_ENV_VALUE"}}
	template.Spec.Template.Spec.Containers[0].SecurityContext.Privileged = pointer(false)

	_, errs := ValidateAndNormalizeJobTemplate(template)
	if len(errs) < 3 {
		t.Fatalf("validator stopped early: %v", errs)
	}
	message := errs.ToAggregate().Error()
	if strings.Contains(message, "SENSITIVE_ENV_VALUE") || strings.Contains(message, "z-extra") {
		t.Fatalf("validation error exposed template content: %s", message)
	}
	for _, err := range errs {
		if !strings.HasPrefix(err.Field, rootField) {
			t.Errorf("error field %q is not rooted at %s", err.Field, rootField)
		}
	}
}

func TestNormalizationChangesOnlyNilRepresentationAndPullSecretOrder(t *testing.T) {
	template := completeTemplate()
	template.Labels = map[string]string{}
	template.Spec.Template.Spec.Containers[0].Command = []string{}
	template.Spec.Template.Spec.Containers[0].Args = []string{}
	template.Spec.Template.Spec.Containers[0].Env = append(template.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{Name: "ORDER_2", Value: "second"}, corev1.EnvVar{Name: "ORDER_1", Value: "first"})
	template.Spec.Template.Spec.SecurityContext.SupplementalGroups = []int64{3000, 2000}
	wantEnv := append([]corev1.EnvVar(nil), template.Spec.Template.Spec.Containers[0].Env...)
	wantGroups := append([]int64(nil), template.Spec.Template.Spec.SecurityContext.SupplementalGroups...)

	normalized, errs := ValidateAndNormalizeJobTemplate(template)
	if len(errs) != 0 {
		t.Fatalf("normalization fixture rejected: %s", errs.ToAggregate())
	}
	if normalized.Labels != nil || normalized.Spec.Template.Spec.Containers[0].Command != nil || normalized.Spec.Template.Spec.Containers[0].Args != nil {
		t.Fatal("empty collection normalization did not collapse to nil")
	}
	if !reflect.DeepEqual(normalized.Spec.Template.Spec.Containers[0].Env, wantEnv) || !reflect.DeepEqual(normalized.Spec.Template.Spec.SecurityContext.SupplementalGroups, wantGroups) {
		t.Fatal("normalization reordered an ordered list")
	}
}

func TestKubernetesFieldCensusIsPinnedAndDefaultDeny(t *testing.T) {
	lines := collectFieldCensus(reflect.TypeOf(batchv1.JobTemplateSpec{}), rootField)
	digest := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	gotDigest := hex.EncodeToString(digest[:])
	const expectedDigest = "ee340e40aab69df1fc44eca495a5d01a4da1031703cd099ab68571e76c24d269"
	if gotDigest != expectedDigest {
		t.Fatalf("Kubernetes field census changed: got sha256:%s, want sha256:%s (%d classified JSON paths)", gotDigest, expectedDigest, len(lines))
	}

	assertCensusClassification(t, lines, ".spec.template.spec.volumes[].image=prohibited")
	assertCensusClassification(t, lines, ".spec.template.spec.resources=prohibited")
	assertCensusClassification(t, lines, ".spec.template.spec.securityContext.supplementalGroupsPolicy=prohibited")
}

func TestInternalBoundaryUsesOnlyPublicExternalKubernetesAPIs(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve validator test location")
	}
	packageDir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(packageDir)
	if err != nil {
		t.Fatalf("read validator package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(packageDir, entry.Name())
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		for _, imported := range parsed.Imports {
			importPath := strings.Trim(imported.Path.Value, "\"")
			if strings.HasPrefix(importPath, "k8s.io/kubernetes") {
				t.Errorf("%s imports prohibited native Kubernetes package %q", entry.Name(), importPath)
			}
			if strings.HasPrefix(importPath, "k8s.io/") && !strings.HasPrefix(importPath, "k8s.io/api/") && !strings.HasPrefix(importPath, "k8s.io/apimachinery/") {
				t.Errorf("%s imports Kubernetes package outside the approved public boundary: %q", entry.Name(), importPath)
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && (strings.HasPrefix(selector.Sel.Name, "SetDefaults") || selector.Sel.Name == "Default") {
				t.Errorf("%s invokes prohibited Kubernetes defaulting function %s", entry.Name(), selector.Sel.Name)
			}
			return true
		})
	}

	repositoryRoot := filepath.Clean(filepath.Join(packageDir, "..", "..", ".."))
	err = filepath.Walk(filepath.Join(repositoryRoot, "scenario-manager"), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(contents), "experiment-operator/internal/jobtemplate") {
			t.Errorf("Scenario Manager depends on the Operator-internal validator through %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect Scenario Manager dependency boundary: %v", err)
	}
}

func collectFieldCensus(root reflect.Type, rootPath string) []string {
	restricted := map[reflect.Type]map[string]struct{}{
		reflect.TypeOf(batchv1.JobTemplateSpec{}): fieldSet("ObjectMeta", "Spec"),
		reflect.TypeOf(batchv1.JobSpec{}):         fieldSet("ActiveDeadlineSeconds", "Template"),
		reflect.TypeOf(metav1.ObjectMeta{}):       fieldSet("Labels", "Annotations"),
		reflect.TypeOf(corev1.PodTemplateSpec{}):  fieldSet("ObjectMeta", "Spec"),
		reflect.TypeOf(corev1.PodSpec{}): fieldSet(
			"Volumes", "InitContainers", "Containers", "ImagePullSecrets", "SecurityContext",
			"NodeSelector", "Affinity", "Tolerations", "TopologySpreadConstraints", "PriorityClassName",
			"PreemptionPolicy", "RuntimeClassName", "TerminationGracePeriodSeconds", "DNSPolicy",
			"DNSConfig", "HostAliases", "EnableServiceLinks",
		),
		reflect.TypeOf(corev1.Container{}): fieldSet(
			"Name", "Image", "Command", "Args", "WorkingDir", "Ports", "EnvFrom", "Env", "Resources",
			"VolumeMounts", "VolumeDevices", "LivenessProbe", "ReadinessProbe", "StartupProbe", "Lifecycle",
			"TerminationMessagePath", "TerminationMessagePolicy", "ImagePullPolicy", "SecurityContext", "RestartPolicy",
		),
		reflect.TypeOf(corev1.VolumeSource{}):         fieldSet("EmptyDir", "ConfigMap", "Secret", "PersistentVolumeClaim", "DownwardAPI", "Projected"),
		reflect.TypeOf(corev1.VolumeProjection{}):     fieldSet("Secret", "ConfigMap", "DownwardAPI"),
		reflect.TypeOf(corev1.ResourceRequirements{}): fieldSet("Limits", "Requests"),
		reflect.TypeOf(corev1.SecurityContext{}):      fieldSet("RunAsUser", "RunAsGroup", "ReadOnlyRootFilesystem"),
		reflect.TypeOf(corev1.PodSecurityContext{}):   fieldSet("RunAsUser", "RunAsGroup", "FSGroup", "SupplementalGroups", "FSGroupChangePolicy"),
		reflect.TypeOf(corev1.Lifecycle{}):            fieldSet("PostStart", "PreStop"),
	}
	visited := map[reflect.Type]struct{}{}
	var lines []string
	var walk func(reflect.Type, string, bool)
	walk = func(current reflect.Type, path string, ancestorAllowed bool) {
		for current.Kind() == reflect.Pointer || current.Kind() == reflect.Slice || current.Kind() == reflect.Array {
			if current.Kind() == reflect.Slice || current.Kind() == reflect.Array {
				path += "[]"
			}
			current = current.Elem()
		}
		if current.Kind() == reflect.Map || current.Kind() != reflect.Struct || isNormalizationLeaf(current) {
			return
		}
		if _, seen := visited[current]; seen {
			return
		}
		visited[current] = struct{}{}
		allowedFields, restrictedType := restricted[current]
		for i := 0; i < current.NumField(); i++ {
			structField := current.Field(i)
			if !structField.IsExported() {
				continue
			}
			tagParts := strings.Split(structField.Tag.Get("json"), ",")
			if len(tagParts) != 0 && tagParts[0] == "-" {
				continue
			}
			inline := len(tagParts) > 1 && containsString(tagParts[1:], "inline")
			jsonName := tagParts[0]
			if jsonName == "" && !inline {
				jsonName = structField.Name
			}
			allowed := ancestorAllowed
			if allowed && restrictedType {
				_, allowed = allowedFields[structField.Name]
			}
			childPath := path
			if !inline {
				childPath += "." + jsonName
				classification := "prohibited"
				if allowed {
					classification = "allowed"
				}
				lines = append(lines, childPath+"="+classification)
			}
			walk(structField.Type, childPath, allowed)
		}
	}
	walk(root, rootPath, true)
	sort.Strings(lines)
	return lines
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertCensusClassification(t *testing.T, lines []string, suffix string) {
	t.Helper()
	for _, line := range lines {
		if strings.HasSuffix(line, suffix) {
			return
		}
	}
	t.Fatalf("field census is missing classification suffix %q", suffix)
}

func completeTemplate() *batchv1.JobTemplateSpec {
	mode := int32(0640)
	terminationGrace := int64(30)
	enableServiceLinks := false
	runtimeClass := "runc"
	preemption := corev1.PreemptNever
	fsPolicy := corev1.FSGroupChangeOnRootMismatch
	nativeRestart := corev1.ContainerRestartPolicyAlways
	minDomains := int32(2)
	honor := corev1.NodeInclusionPolicyHonor
	ignore := corev1.NodeInclusionPolicyIgnore
	rro := corev1.RecursiveReadOnlyIfPossible

	return &batchv1.JobTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      map[string]string{"integration.example/job": "enabled"},
			Annotations: map[string]string{"integration.example/job-note": "enabled"},
		},
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: int64Pointer(120),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      map[string]string{"integration.example/pod": "enabled"},
					Annotations: map[string]string{"integration.example/pod-note": "enabled"},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{Name: "work", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: quantityPointer("64Mi")}}},
						{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "runner-config"}, DefaultMode: &mode, Items: []corev1.KeyToPath{{Key: "config.yaml", Path: "config.yaml", Mode: &mode}}}}},
						{Name: "auth", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "runner-auth", DefaultMode: &mode, Items: []corev1.KeyToPath{{Key: "token", Path: "token", Mode: &mode}}}}},
						{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "runner-data", ReadOnly: false}}},
						{Name: "metadata", VolumeSource: corev1.VolumeSource{DownwardAPI: &corev1.DownwardAPIVolumeSource{DefaultMode: &mode, Items: []corev1.DownwardAPIVolumeFile{{Path: "labels", FieldRef: &corev1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "metadata.labels"}, Mode: &mode}}}}},
						{Name: "projected", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{DefaultMode: &mode, Sources: []corev1.VolumeProjection{
							{ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "projected-config"}, Items: []corev1.KeyToPath{{Key: "config", Path: "projected/config"}}}},
							{Secret: &corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "projected-secret"}, Items: []corev1.KeyToPath{{Key: "secret", Path: "projected/secret"}}}},
							{DownwardAPI: &corev1.DownwardAPIProjection{Items: []corev1.DownwardAPIVolumeFile{{Path: "projected/cpu", ResourceFieldRef: validVolumeResourceFieldRef()}}}},
						}}}},
					},
					Containers: []corev1.Container{
						{
							Name: "runner",
							Env: []corev1.EnvVar{
								{Name: "PLAIN", Value: "value"},
								{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "metadata.name"}}},
								{Name: "CPU_LIMIT", ValueFrom: &corev1.EnvVarSource{ResourceFieldRef: &corev1.ResourceFieldSelector{Resource: "limits.cpu", Divisor: resource.MustParse("1m")}}},
								{Name: "CONFIG_KEY", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "runner-config"}, Key: "config.yaml"}}},
								{Name: "SECRET_KEY", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "runner-auth"}, Key: "token"}}},
							},
							EnvFrom: []corev1.EnvFromSource{{Prefix: "CFG_", ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "runner-env"}}}},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("256Mi"), corev1.ResourceEphemeralStorage: resource.MustParse("1Gi"),
									"hugepages-2Mi": resource.MustParse("4Mi"), "example.com/gpu": resource.MustParse("1"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("128Mi"), corev1.ResourceEphemeralStorage: resource.MustParse("512Mi"),
									"hugepages-2Mi": resource.MustParse("4Mi"), "example.com/gpu": resource.MustParse("1"),
								},
							},
							Ports:                    []corev1.ContainerPort{{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP, HostIP: "127.0.0.1"}},
							VolumeMounts:             []corev1.VolumeMount{{Name: "config", MountPath: "/config"}, {Name: "work", MountPath: "/work", ReadOnly: true, RecursiveReadOnly: &rro}},
							VolumeDevices:            []corev1.VolumeDevice{{Name: "data", DevicePath: "/dev/xvda"}},
							LivenessProbe:            execProbe(),
							ReadinessProbe:           &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/ready", Port: intstr.FromInt32(8080), Scheme: corev1.URISchemeHTTP, HTTPHeaders: []corev1.HTTPHeader{{Name: "X-Probe", Value: "ready"}}}}},
							StartupProbe:             &corev1.Probe{ProbeHandler: corev1.ProbeHandler{GRPC: &corev1.GRPCAction{Port: 8080}}, SuccessThreshold: 1},
							ImagePullPolicy:          corev1.PullIfNotPresent,
							TerminationMessagePath:   "/dev/termination-log",
							TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
							SecurityContext:          &corev1.SecurityContext{RunAsUser: int64Pointer(1000), RunAsGroup: int64Pointer(1000), ReadOnlyRootFilesystem: pointer(true)},
						},
						{
							Name: "sidecar", Image: "example.invalid/sidecar:tag", Command: []string{"sidecar"}, Args: []string{"--serve"}, WorkingDir: "/app",
							Env: []corev1.EnvVar{{Name: "MODE", Value: "sidecar"}}, Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")}},
							Ports: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 9090, Protocol: corev1.ProtocolTCP}}, VolumeMounts: []corev1.VolumeMount{{Name: "auth", MountPath: "/auth"}},
							LivenessProbe: execProbe(), Lifecycle: sleepLifecycle(5), ImagePullPolicy: corev1.PullAlways,
							SecurityContext: &corev1.SecurityContext{RunAsUser: int64Pointer(1001), RunAsGroup: int64Pointer(1001), ReadOnlyRootFilesystem: pointer(true)},
						},
					},
					InitContainers: []corev1.Container{
						{Name: "setup", Image: "example.invalid/setup:tag", Command: []string{"setup"}, Args: []string{"--once"}, WorkingDir: "/work", VolumeMounts: []corev1.VolumeMount{{Name: "metadata", MountPath: "/metadata"}}, SecurityContext: &corev1.SecurityContext{RunAsUser: int64Pointer(1002), RunAsGroup: int64Pointer(1002)}},
						{Name: "native-sidecar", Image: "example.invalid/native:tag", RestartPolicy: &nativeRestart, LivenessProbe: execProbe(), Lifecycle: sleepLifecycle(1), SecurityContext: &corev1.SecurityContext{RunAsUser: int64Pointer(1003), RunAsGroup: int64Pointer(1003)}},
					},
					ImagePullSecrets: []corev1.LocalObjectReference{{Name: "z-extra"}, {Name: registryAuthSecretName}, {Name: "a-extra"}},
					NodeSelector:     map[string]string{"disk.example/type": "ssd"},
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution:  &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "topology.kubernetes.io/region", Operator: corev1.NodeSelectorOpIn, Values: []string{"west"}}}}}},
							PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{Weight: 10, Preference: corev1.NodeSelectorTerm{MatchFields: []corev1.NodeSelectorRequirement{{Key: metav1.ObjectNameField, Operator: corev1.NodeSelectorOpNotIn, Values: []string{"retired-node"}}}}}},
						},
						PodAffinity: &corev1.PodAffinity{RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
							LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "runner"}}, Namespaces: []string{"team-a"},
							NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "a"}}, TopologyKey: "topology.kubernetes.io/zone",
							MatchLabelKeys: []string{"rollout"}, MismatchLabelKeys: []string{"tenant"},
						}}},
						PodAntiAffinity: &corev1.PodAntiAffinity{PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{Weight: 20, PodAffinityTerm: corev1.PodAffinityTerm{LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "other"}}, TopologyKey: "kubernetes.io/hostname"}}}},
					},
					Tolerations:               []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute, TolerationSeconds: int64Pointer(60)}},
					TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{MaxSkew: 1, TopologyKey: "topology.kubernetes.io/zone", WhenUnsatisfiable: corev1.DoNotSchedule, LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "runner"}}, MinDomains: &minDomains, NodeAffinityPolicy: &honor, NodeTaintsPolicy: &ignore, MatchLabelKeys: []string{"rollout"}}},
					PriorityClassName:         "high-priority", PreemptionPolicy: &preemption, RuntimeClassName: &runtimeClass,
					TerminationGracePeriodSeconds: &terminationGrace, DNSPolicy: corev1.DNSNone,
					DNSConfig:   &corev1.PodDNSConfig{Nameservers: []string{"1.1.1.1"}, Searches: []string{"svc.cluster.local."}, Options: []corev1.PodDNSConfigOption{{Name: "ndots", Value: pointer("2")}}},
					HostAliases: []corev1.HostAlias{{IP: "127.0.0.1", Hostnames: []string{"runner.local"}}}, EnableServiceLinks: &enableServiceLinks,
					SecurityContext: &corev1.PodSecurityContext{RunAsUser: int64Pointer(1000), RunAsGroup: int64Pointer(1000), FSGroup: int64Pointer(2000), SupplementalGroups: []int64{2001, 2002}, FSGroupChangePolicy: &fsPolicy},
				},
			},
		},
	}
}

func execProbe() *corev1.Probe {
	return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"check"}}}, SuccessThreshold: 1}
}

func sleepLifecycle(seconds int64) *corev1.Lifecycle {
	return &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Sleep: &corev1.SleepAction{Seconds: seconds}}}
}

func validVolumeResourceFieldRef() *corev1.ResourceFieldSelector {
	return &corev1.ResourceFieldSelector{ContainerName: "runner", Resource: "limits.cpu", Divisor: resource.MustParse("1m")}
}

func projectedDownward(template *batchv1.JobTemplateSpec) *corev1.DownwardAPIVolumeFile {
	return &template.Spec.Template.Spec.Volumes[5].Projected.Sources[2].DownwardAPI.Items[0]
}

func pullSecretNames(refs []corev1.LocalObjectReference) []string {
	names := make([]string, len(refs))
	for i, ref := range refs {
		names[i] = ref.Name
	}
	return names
}

func quantityPointer(value string) *resource.Quantity {
	quantity := resource.MustParse(value)
	return &quantity
}

func int32Pointer(value int32) *int32 { return &value }
func int64Pointer(value int64) *int64 { return &value }
func pointer[T any](value T) *T       { return &value }
