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
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	apierrors "k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

type containerKind int

const (
	regularContainer containerKind = iota
	ordinaryInitContainer
	nativeSidecarContainer
)

func validateContainerComposition(spec *corev1.PodSpec, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	seenNames := map[string]struct{}{}
	runnerCount := 0
	volumeTypes := map[string]bool{}
	for _, volume := range spec.Volumes {
		volumeTypes[volume.Name] = volume.PersistentVolumeClaim != nil
	}

	for i := range spec.Containers {
		container := &spec.Containers[i]
		containerPath := path.Child("containers").Index(i)
		errs = append(errs, validateContainerName(container.Name, containerPath.Child("name"), seenNames)...)
		isRunner := container.Name == "runner"
		if isRunner {
			runnerCount++
		}
		errs = append(errs, validateContainer(container, containerPath, regularContainer, isRunner, volumeTypes, spec.TerminationGracePeriodSeconds)...)
	}
	if runnerCount != 1 {
		errs = append(errs, invalid(path.Child("containers"), "exactly one regular container named runner is required"))
	}

	for i := range spec.InitContainers {
		container := &spec.InitContainers[i]
		containerPath := path.Child("initContainers").Index(i)
		errs = append(errs, validateContainerName(container.Name, containerPath.Child("name"), seenNames)...)
		kind := ordinaryInitContainer
		if container.RestartPolicy != nil {
			if *container.RestartPolicy != corev1.ContainerRestartPolicyAlways {
				errs = append(errs, invalid(containerPath.Child("restartPolicy"), "only Always selects the supported native-sidecar behavior"))
			} else {
				kind = nativeSidecarContainer
			}
		}
		errs = append(errs, validateContainer(container, containerPath, kind, false, volumeTypes, spec.TerminationGracePeriodSeconds)...)
	}

	errs = append(errs, validateHostPortConflicts(spec.Containers, path.Child("containers"))...)
	return errs
}

func validateContainerName(name string, path *field.Path, seen map[string]struct{}) field.ErrorList {
	var errs field.ErrorList
	if name == "" {
		return field.ErrorList{required(path, "name is required")}
	}
	errs = append(errs, messagesAsErrors(path, utilvalidation.IsDNS1123Label(name))...)
	if _, duplicate := seen[name]; duplicate {
		errs = append(errs, invalid(path, "container names must be unique across regular and init containers"))
	} else {
		seen[name] = struct{}{}
	}
	return errs
}

func validateContainer(container *corev1.Container, path *field.Path, kind containerKind, runner bool, volumeTypes map[string]bool, terminationGrace *int64) field.ErrorList {
	allowed := fieldSet(
		"Name", "Image", "Command", "Args", "WorkingDir", "Ports", "EnvFrom", "Env",
		"Resources", "VolumeMounts", "VolumeDevices", "LivenessProbe", "ReadinessProbe",
		"StartupProbe", "Lifecycle", "TerminationMessagePath", "TerminationMessagePolicy",
		"ImagePullPolicy", "SecurityContext", "RestartPolicy",
	)
	errs := rejectNonZeroFields(reflect.ValueOf(container).Elem(), path, allowed)

	if runner {
		if container.Image != "" {
			errs = append(errs, forbidden(path.Child("image"), "CBSE supplies the runner image"))
		}
		if len(container.Command) != 0 {
			errs = append(errs, forbidden(path.Child("command"), "CBSE supplies the runner command"))
		}
		if len(container.Args) != 0 {
			errs = append(errs, forbidden(path.Child("args"), "CBSE supplies the runner arguments"))
		}
		if container.WorkingDir != "" {
			errs = append(errs, forbidden(path.Child("workingDir"), "runner workingDir is part of the CBSE executable contract"))
		}
		if container.Lifecycle != nil {
			errs = append(errs, forbidden(path.Child("lifecycle"), "runner lifecycle hooks are owned by CBSE"))
		}
	} else if container.Image == "" {
		errs = append(errs, required(path.Child("image"), "auxiliary container image is required"))
	} else if strings.TrimSpace(container.Image) != container.Image {
		errs = append(errs, invalid(path.Child("image"), "image must not have leading or trailing whitespace"))
	}

	if kind == regularContainer && container.RestartPolicy != nil {
		errs = append(errs, forbidden(path.Child("restartPolicy"), "restartPolicy is supported only for native init sidecars"))
	}
	if kind == ordinaryInitContainer {
		if container.Lifecycle != nil {
			errs = append(errs, forbidden(path.Child("lifecycle"), "ordinary init containers cannot define lifecycle hooks"))
		}
		if container.LivenessProbe != nil {
			errs = append(errs, forbidden(path.Child("livenessProbe"), "ordinary init containers cannot define probes"))
		}
		if container.ReadinessProbe != nil {
			errs = append(errs, forbidden(path.Child("readinessProbe"), "ordinary init containers cannot define probes"))
		}
		if container.StartupProbe != nil {
			errs = append(errs, forbidden(path.Child("startupProbe"), "ordinary init containers cannot define probes"))
		}
	}

	errs = append(errs, validateEnv(container.Env, path.Child("env"))...)
	errs = append(errs, validateEnvFrom(container.EnvFrom, path.Child("envFrom"))...)
	errs = append(errs, validateResources(&container.Resources, path.Child("resources"))...)
	errs = append(errs, validateContainerPorts(container.Ports, path.Child("ports"))...)
	errs = append(errs, validateMountsAndDevices(container, volumeTypes, path)...)
	grace := defaultTerminationSeconds
	if terminationGrace != nil {
		grace = *terminationGrace
	}
	if kind != ordinaryInitContainer {
		errs = append(errs, validateProbe(container.LivenessProbe, path.Child("livenessProbe"), grace, probeLiveness)...)
		errs = append(errs, validateProbe(container.ReadinessProbe, path.Child("readinessProbe"), grace, probeReadiness)...)
		errs = append(errs, validateProbe(container.StartupProbe, path.Child("startupProbe"), grace, probeStartup)...)
	}
	if container.Lifecycle != nil && !runner && kind != ordinaryInitContainer {
		errs = append(errs, validateLifecycle(container.Lifecycle, path.Child("lifecycle"), grace)...)
	}
	errs = append(errs, validatePullAndTermination(container, path)...)
	errs = append(errs, validateContainerSecurityContext(container.SecurityContext, path.Child("securityContext"))...)
	return errs
}

func validateEnv(vars []corev1.EnvVar, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	for i := range vars {
		variable := &vars[i]
		itemPath := path.Index(i)
		if variable.Name == "" {
			errs = append(errs, required(itemPath.Child("name"), "name is required"))
		} else {
			errs = append(errs, messagesAsErrors(itemPath.Child("name"), utilvalidation.IsEnvVarName(variable.Name))...)
		}
		if variable.ValueFrom != nil {
			if variable.Value != "" {
				errs = append(errs, invalid(itemPath.Child("valueFrom"), "value and valueFrom are mutually exclusive"))
			}
			errs = append(errs, validateEnvVarSource(variable.ValueFrom, itemPath.Child("valueFrom"))...)
		}
	}
	return errs
}

func validateEnvVarSource(source *corev1.EnvVarSource, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	count := 0
	if source.FieldRef != nil {
		count++
		errs = append(errs, validateObjectFieldSelector(source.FieldRef, path.Child("fieldRef"), downwardEnvFields)...)
	}
	if source.ResourceFieldRef != nil {
		count++
		errs = append(errs, validateResourceFieldSelector(source.ResourceFieldRef, path.Child("resourceFieldRef"), false)...)
	}
	if source.ConfigMapKeyRef != nil {
		count++
		errs = append(errs, validateKeySelector(source.ConfigMapKeyRef.Name, source.ConfigMapKeyRef.Key, path.Child("configMapKeyRef"))...)
	}
	if source.SecretKeyRef != nil {
		count++
		errs = append(errs, validateKeySelector(source.SecretKeyRef.Name, source.SecretKeyRef.Key, path.Child("secretKeyRef"))...)
	}
	if count == 0 {
		errs = append(errs, required(path, "one valueFrom source is required"))
	} else if count > 1 {
		errs = append(errs, invalid(path, "only one valueFrom source may be specified"))
	}
	return errs
}

func validateKeySelector(name, key string, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if name == "" {
		errs = append(errs, required(path.Child("name"), "name is required"))
	} else {
		errs = append(errs, messagesAsErrors(path.Child("name"), apierrors.NameIsDNSSubdomain(name, false))...)
	}
	if key == "" {
		errs = append(errs, required(path.Child("key"), "key is required"))
	} else {
		errs = append(errs, messagesAsErrors(path.Child("key"), utilvalidation.IsConfigMapKey(key))...)
	}
	return errs
}

func validateEnvFrom(sources []corev1.EnvFromSource, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	for i := range sources {
		source := &sources[i]
		itemPath := path.Index(i)
		if source.Prefix != "" {
			errs = append(errs, messagesAsErrors(itemPath.Child("prefix"), utilvalidation.IsEnvVarName(source.Prefix))...)
		}
		count := 0
		if source.ConfigMapRef != nil {
			count++
			errs = append(errs, validateObjectReferenceName(source.ConfigMapRef.Name, itemPath.Child("configMapRef", "name"))...)
		}
		if source.SecretRef != nil {
			count++
			errs = append(errs, validateObjectReferenceName(source.SecretRef.Name, itemPath.Child("secretRef", "name"))...)
		}
		if count == 0 {
			errs = append(errs, required(itemPath, "one envFrom source is required"))
		} else if count > 1 {
			errs = append(errs, invalid(itemPath, "only one envFrom source may be specified"))
		}
	}
	return errs
}

func validateObjectReferenceName(name string, path *field.Path) field.ErrorList {
	if name == "" {
		return field.ErrorList{required(path, "name is required")}
	}
	return messagesAsErrors(path, apierrors.NameIsDNSSubdomain(name, false))
}

func validateResources(resources *corev1.ResourceRequirements, path *field.Path) field.ErrorList {
	errs := rejectNonZeroFields(reflect.ValueOf(resources).Elem(), path, fieldSet("Limits", "Requests"))
	resourceNames := map[corev1.ResourceName]struct{}{}
	for name := range resources.Limits {
		resourceNames[name] = struct{}{}
	}
	for name := range resources.Requests {
		resourceNames[name] = struct{}{}
	}
	names := make([]string, 0, len(resourceNames))
	for name := range resourceNames {
		names = append(names, string(name))
	}
	sort.Strings(names)

	hasCompute := false
	hasHugePages := false
	for _, rawName := range names {
		name := corev1.ResourceName(rawName)
		if name == corev1.ResourceCPU || name == corev1.ResourceMemory {
			hasCompute = true
		}
		if isHugePageResource(name) {
			hasHugePages = true
		}
		if !isAllowedContainerResourceName(name) {
			if _, ok := resources.Limits[name]; ok {
				errs = append(errs, invalid(path.Child("limits").Key(rawName), "resource name is not supported for alpha4 containers"))
			}
			if _, ok := resources.Requests[name]; ok {
				errs = append(errs, invalid(path.Child("requests").Key(rawName), "resource name is not supported for alpha4 containers"))
			}
		}
		if limit, ok := resources.Limits[name]; ok {
			errs = append(errs, validateResourceQuantity(name, limit, path.Child("limits").Key(rawName))...)
		}
		if request, ok := resources.Requests[name]; ok {
			errs = append(errs, validateResourceQuantity(name, request, path.Child("requests").Key(rawName))...)
			limit, hasLimit := resources.Limits[name]
			if isNonOvercommittable(name) {
				if !hasLimit {
					errs = append(errs, required(path.Child("limits").Key(rawName), "a matching limit is required"))
				} else if request.Cmp(limit) != 0 {
					errs = append(errs, invalid(path.Child("requests").Key(rawName), "request must equal limit for a non-overcommittable resource"))
				}
			} else if hasLimit && request.Cmp(limit) > 0 {
				errs = append(errs, invalid(path.Child("requests").Key(rawName), "request must not exceed limit"))
			}
		}
	}
	if hasHugePages && !hasCompute {
		errs = append(errs, invalid(path, "huge-page resources require a CPU or memory request or limit"))
	}
	return errs
}

func isAllowedContainerResourceName(name corev1.ResourceName) bool {
	if len(utilvalidation.IsQualifiedName(string(name))) != 0 {
		return false
	}
	if name == corev1.ResourceCPU || name == corev1.ResourceMemory || name == corev1.ResourceEphemeralStorage {
		return true
	}
	if isHugePageResource(name) {
		suffix := strings.TrimPrefix(string(name), corev1.ResourceHugePagesPrefix)
		pageSize, err := resource.ParseQuantity(suffix)
		return err == nil && pageSize.Sign() > 0 && pageSize.MilliValue()%1000 == 0
	}
	raw := string(name)
	if !strings.Contains(raw, "/") || strings.HasPrefix(raw, "requests.") || strings.Contains(raw, "kubernetes.io/") {
		return false
	}
	return len(utilvalidation.IsQualifiedName("requests."+raw)) == 0
}

func validateResourceQuantity(name corev1.ResourceName, quantity resource.Quantity, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if quantity.Sign() < 0 {
		errs = append(errs, invalid(path, "quantity must be non-negative"))
	}
	rounded := quantity.DeepCopy()
	rounded.RoundUp(resource.Milli)
	if rounded.Cmp(quantity) != 0 {
		errs = append(errs, invalid(path, "quantity changes when rounded to Kubernetes milli precision"))
	}
	if isExtendedResource(name) && quantity.MilliValue()%1000 != 0 {
		errs = append(errs, invalid(path, "extended-resource quantities must be whole numbers"))
	}
	if isHugePageResource(name) {
		pageSize, err := resource.ParseQuantity(strings.TrimPrefix(string(name), corev1.ResourceHugePagesPrefix))
		if err != nil || pageSize.Sign() <= 0 || pageSize.MilliValue()%1000 != 0 || pageSize.Value() == 0 || quantity.Value()%pageSize.Value() != 0 {
			errs = append(errs, invalid(path, "huge-page quantity must be an integer multiple of the declared page size"))
		}
	}
	return errs
}

func isHugePageResource(name corev1.ResourceName) bool {
	return strings.HasPrefix(string(name), corev1.ResourceHugePagesPrefix)
}

func isExtendedResource(name corev1.ResourceName) bool {
	return strings.Contains(string(name), "/") && !strings.Contains(string(name), "kubernetes.io/")
}

func isNonOvercommittable(name corev1.ResourceName) bool {
	return isHugePageResource(name) || isExtendedResource(name)
}

func validateContainerPorts(ports []corev1.ContainerPort, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	seenNames := map[string]struct{}{}
	for i := range ports {
		port := &ports[i]
		itemPath := path.Index(i)
		if port.Name != "" {
			errs = append(errs, messagesAsErrors(itemPath.Child("name"), utilvalidation.IsValidPortName(port.Name))...)
			if _, duplicate := seenNames[port.Name]; duplicate {
				errs = append(errs, invalid(itemPath.Child("name"), "port name must be unique within the container"))
			} else {
				seenNames[port.Name] = struct{}{}
			}
		}
		if port.ContainerPort == 0 {
			errs = append(errs, required(itemPath.Child("containerPort"), "containerPort is required"))
		} else {
			errs = append(errs, messagesAsErrors(itemPath.Child("containerPort"), utilvalidation.IsValidPortNum(int(port.ContainerPort)))...)
		}
		if port.HostPort != 0 {
			errs = append(errs, messagesAsErrors(itemPath.Child("hostPort"), utilvalidation.IsValidPortNum(int(port.HostPort)))...)
		}
		if port.Protocol != "" && port.Protocol != corev1.ProtocolTCP && port.Protocol != corev1.ProtocolUDP && port.Protocol != corev1.ProtocolSCTP {
			errs = append(errs, invalid(itemPath.Child("protocol"), "protocol must be TCP, UDP, or SCTP"))
		}
	}
	return errs
}

func validateHostPortConflicts(containers []corev1.Container, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	seen := map[string]struct{}{}
	for i := range containers {
		for j, port := range containers[i].Ports {
			if port.HostPort == 0 {
				continue
			}
			protocol := port.Protocol
			if protocol == "" {
				protocol = corev1.ProtocolTCP
			}
			key := string(protocol) + "/" + port.HostIP + "/" + resource.NewQuantity(int64(port.HostPort), resource.DecimalSI).String()
			if _, duplicate := seen[key]; duplicate {
				errs = append(errs, invalid(path.Index(i).Child("ports").Index(j).Child("hostPort"), "host port tuple is already used by a regular container"))
			} else {
				seen[key] = struct{}{}
			}
		}
	}
	return errs
}

func validateMountsAndDevices(container *corev1.Container, volumeTypes map[string]bool, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	mountPaths := map[string]struct{}{}
	mountNames := map[string]struct{}{}
	for i := range container.VolumeMounts {
		mount := &container.VolumeMounts[i]
		itemPath := path.Child("volumeMounts").Index(i)
		if mount.Name == "" {
			errs = append(errs, required(itemPath.Child("name"), "name is required"))
		} else if _, exists := volumeTypes[mount.Name]; !exists {
			errs = append(errs, invalid(itemPath.Child("name"), "referenced volume does not exist"))
		}
		mountNames[mount.Name] = struct{}{}
		if mount.MountPath == "" {
			errs = append(errs, required(itemPath.Child("mountPath"), "mountPath is required"))
		} else {
			errs = append(errs, validateNoBacksteps(mount.MountPath, itemPath.Child("mountPath"))...)
			if _, duplicate := mountPaths[mount.MountPath]; duplicate {
				errs = append(errs, invalid(itemPath.Child("mountPath"), "mountPath must be unique"))
			} else {
				mountPaths[mount.MountPath] = struct{}{}
			}
		}
		if mount.SubPath != "" {
			errs = append(errs, validateLocalPath(mount.SubPath, itemPath.Child("subPath"))...)
		}
		if mount.SubPathExpr != "" {
			errs = append(errs, validateLocalPath(mount.SubPathExpr, itemPath.Child("subPathExpr"))...)
			if mount.SubPath != "" {
				errs = append(errs, invalid(itemPath.Child("subPathExpr"), "subPath and subPathExpr are mutually exclusive"))
			}
		}
		if mount.MountPropagation != nil {
			switch *mount.MountPropagation {
			case corev1.MountPropagationNone, corev1.MountPropagationHostToContainer:
			case corev1.MountPropagationBidirectional:
				errs = append(errs, forbidden(itemPath.Child("mountPropagation"), "Bidirectional propagation requires prohibited privileged mode"))
			default:
				errs = append(errs, invalid(itemPath.Child("mountPropagation"), "unsupported mount propagation mode"))
			}
		}
		if mount.RecursiveReadOnly != nil {
			switch *mount.RecursiveReadOnly {
			case corev1.RecursiveReadOnlyDisabled:
			case corev1.RecursiveReadOnlyIfPossible, corev1.RecursiveReadOnlyEnabled:
				if !mount.ReadOnly {
					errs = append(errs, invalid(itemPath.Child("recursiveReadOnly"), "recursive read-only requires readOnly=true"))
				}
				if mount.MountPropagation != nil && *mount.MountPropagation != corev1.MountPropagationNone {
					errs = append(errs, invalid(itemPath.Child("recursiveReadOnly"), "recursive read-only requires None mount propagation"))
				}
			default:
				errs = append(errs, invalid(itemPath.Child("recursiveReadOnly"), "unsupported recursive read-only mode"))
			}
		}
	}

	devicePaths := map[string]struct{}{}
	deviceNames := map[string]struct{}{}
	for i := range container.VolumeDevices {
		device := &container.VolumeDevices[i]
		itemPath := path.Child("volumeDevices").Index(i)
		if device.Name == "" {
			errs = append(errs, required(itemPath.Child("name"), "name is required"))
		} else if pvc, exists := volumeTypes[device.Name]; !exists {
			errs = append(errs, invalid(itemPath.Child("name"), "referenced volume does not exist"))
		} else if !pvc {
			errs = append(errs, invalid(itemPath.Child("name"), "block devices require a persistentVolumeClaim volume"))
		}
		if _, duplicate := deviceNames[device.Name]; duplicate {
			errs = append(errs, invalid(itemPath.Child("name"), "device name must be unique"))
		} else {
			deviceNames[device.Name] = struct{}{}
		}
		if _, mounted := mountNames[device.Name]; mounted {
			errs = append(errs, invalid(itemPath.Child("name"), "volume cannot be both mounted and exposed as a device"))
		}
		if device.DevicePath == "" {
			errs = append(errs, required(itemPath.Child("devicePath"), "devicePath is required"))
		} else {
			errs = append(errs, validateNoBacksteps(device.DevicePath, itemPath.Child("devicePath"))...)
			if _, duplicate := devicePaths[device.DevicePath]; duplicate {
				errs = append(errs, invalid(itemPath.Child("devicePath"), "devicePath must be unique"))
			} else {
				devicePaths[device.DevicePath] = struct{}{}
			}
			if _, mounted := mountPaths[device.DevicePath]; mounted {
				errs = append(errs, invalid(itemPath.Child("devicePath"), "path cannot also be used by a volume mount"))
			}
		}
	}
	return errs
}

func validateNoBacksteps(value string, path *field.Path) field.ErrorList {
	for _, part := range strings.Split(filepath.ToSlash(value), "/") {
		if part == ".." {
			return field.ErrorList{invalid(path, "path must not contain backsteps")}
		}
	}
	return nil
}

type probeClass int

const (
	probeLiveness probeClass = iota
	probeReadiness
	probeStartup
)

func validateProbe(probe *corev1.Probe, path *field.Path, grace int64, class probeClass) field.ErrorList {
	if probe == nil {
		return nil
	}
	errs := validateProbeHandler(&probe.ProbeHandler, path)
	for name, value := range map[string]int32{
		"initialDelaySeconds": probe.InitialDelaySeconds,
		"timeoutSeconds":      probe.TimeoutSeconds,
		"periodSeconds":       probe.PeriodSeconds,
		"successThreshold":    probe.SuccessThreshold,
		"failureThreshold":    probe.FailureThreshold,
	} {
		if value < 0 {
			errs = append(errs, invalid(path.Child(name), "must be non-negative"))
		}
	}
	if probe.TerminationGracePeriodSeconds != nil && *probe.TerminationGracePeriodSeconds <= 0 {
		errs = append(errs, invalid(path.Child("terminationGracePeriodSeconds"), "must be greater than zero"))
	}
	if class == probeReadiness && probe.TerminationGracePeriodSeconds != nil {
		errs = append(errs, forbidden(path.Child("terminationGracePeriodSeconds"), "readiness probes cannot set termination grace"))
	}
	if (class == probeLiveness || class == probeStartup) && probe.SuccessThreshold != 0 && probe.SuccessThreshold != 1 {
		errs = append(errs, invalid(path.Child("successThreshold"), "must equal one when supplied"))
	}
	_ = grace
	return errs
}

func validateProbeHandler(handler *corev1.ProbeHandler, path *field.Path) field.ErrorList {
	common := handlerView{exec: handler.Exec, httpGet: handler.HTTPGet, tcpSocket: handler.TCPSocket, grpc: handler.GRPC}
	return validateHandler(common, path, 0, false)
}

func validateLifecycle(lifecycle *corev1.Lifecycle, path *field.Path, grace int64) field.ErrorList {
	errs := rejectNonZeroFields(reflect.ValueOf(lifecycle).Elem(), path, fieldSet("PostStart", "PreStop"))
	if lifecycle.PostStart != nil {
		errs = append(errs, validateLifecycleHandler(lifecycle.PostStart, path.Child("postStart"), grace)...)
	}
	if lifecycle.PreStop != nil {
		errs = append(errs, validateLifecycleHandler(lifecycle.PreStop, path.Child("preStop"), grace)...)
	}
	return errs
}

func validateLifecycleHandler(handler *corev1.LifecycleHandler, path *field.Path, grace int64) field.ErrorList {
	return validateHandler(handlerView{exec: handler.Exec, httpGet: handler.HTTPGet, tcpSocket: handler.TCPSocket, sleep: handler.Sleep}, path, grace, true)
}

type handlerView struct {
	exec      *corev1.ExecAction
	httpGet   *corev1.HTTPGetAction
	tcpSocket *corev1.TCPSocketAction
	grpc      *corev1.GRPCAction
	sleep     *corev1.SleepAction
}

func validateHandler(handler handlerView, path *field.Path, grace int64, lifecycle bool) field.ErrorList {
	var errs field.ErrorList
	count := 0
	if handler.exec != nil {
		count++
		if len(handler.exec.Command) == 0 {
			errs = append(errs, required(path.Child("exec", "command"), "command is required"))
		}
	}
	if handler.httpGet != nil {
		count++
		errs = append(errs, validateHTTPGet(handler.httpGet, path.Child("httpGet"))...)
	}
	if handler.tcpSocket != nil {
		count++
		errs = append(errs, validatePort(handler.tcpSocket.Port, path.Child("tcpSocket", "port"))...)
	}
	if handler.grpc != nil {
		count++
		if lifecycle {
			errs = append(errs, forbidden(path.Child("grpc"), "gRPC is not a Kubernetes 1.30 lifecycle action"))
		} else {
			errs = append(errs, messagesAsErrors(path.Child("grpc", "port"), utilvalidation.IsValidPortNum(int(handler.grpc.Port)))...)
		}
	}
	if handler.sleep != nil {
		count++
		if !lifecycle {
			errs = append(errs, forbidden(path.Child("sleep"), "sleep is not a probe action"))
		} else if handler.sleep.Seconds <= 0 || handler.sleep.Seconds > grace {
			errs = append(errs, invalid(path.Child("sleep", "seconds"), "sleep must be positive and no longer than Pod termination grace"))
		}
	}
	if count == 0 {
		errs = append(errs, required(path, "exactly one handler action is required"))
	} else if count > 1 {
		errs = append(errs, invalid(path, "exactly one handler action may be specified"))
	}
	return errs
}

func validateHTTPGet(action *corev1.HTTPGetAction, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if action.Path == "" {
		errs = append(errs, required(path.Child("path"), "path is required"))
	}
	errs = append(errs, validatePort(action.Port, path.Child("port"))...)
	if action.Scheme != "" && action.Scheme != corev1.URISchemeHTTP && action.Scheme != corev1.URISchemeHTTPS {
		errs = append(errs, invalid(path.Child("scheme"), "scheme must be HTTP or HTTPS"))
	}
	for i, header := range action.HTTPHeaders {
		errs = append(errs, messagesAsErrors(path.Child("httpHeaders").Index(i).Child("name"), utilvalidation.IsHTTPHeaderName(header.Name))...)
	}
	return errs
}

func validatePort(port intstr.IntOrString, path *field.Path) field.ErrorList {
	switch port.Type {
	case intstr.Int:
		return messagesAsErrors(path, utilvalidation.IsValidPortNum(port.IntValue()))
	case intstr.String:
		if port.StrVal == "" {
			return field.ErrorList{required(path, "port is required")}
		}
		return messagesAsErrors(path, utilvalidation.IsValidPortName(port.StrVal))
	default:
		return field.ErrorList{invalid(path, "unknown IntOrString port type")}
	}
}

func validatePullAndTermination(container *corev1.Container, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if container.ImagePullPolicy != "" && container.ImagePullPolicy != corev1.PullAlways && container.ImagePullPolicy != corev1.PullIfNotPresent && container.ImagePullPolicy != corev1.PullNever {
		errs = append(errs, invalid(path.Child("imagePullPolicy"), "unsupported image pull policy"))
	}
	if container.TerminationMessagePolicy != "" && container.TerminationMessagePolicy != corev1.TerminationMessageReadFile && container.TerminationMessagePolicy != corev1.TerminationMessageFallbackToLogsOnError {
		errs = append(errs, invalid(path.Child("terminationMessagePolicy"), "unsupported termination message policy"))
	}
	return errs
}

func validateContainerSecurityContext(context *corev1.SecurityContext, path *field.Path) field.ErrorList {
	if context == nil {
		return nil
	}
	errs := rejectNonZeroFields(reflect.ValueOf(context).Elem(), path, fieldSet("RunAsUser", "RunAsGroup", "ReadOnlyRootFilesystem"))
	errs = append(errs, validatePositiveID(context.RunAsUser, path.Child("runAsUser"), true)...)
	errs = append(errs, validatePositiveID(context.RunAsGroup, path.Child("runAsGroup"), false)...)
	return errs
}

func validatePositiveID(value *int64, path *field.Path, user bool) field.ErrorList {
	if value == nil {
		return nil
	}
	if *value <= 0 {
		return field.ErrorList{invalid(path, "identity must be greater than zero")}
	}
	var messages []string
	if user {
		messages = utilvalidation.IsValidUserID(*value)
	} else {
		messages = utilvalidation.IsValidGroupID(*value)
	}
	return messagesAsErrors(path, messages)
}
