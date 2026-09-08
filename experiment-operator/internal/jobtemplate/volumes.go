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
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/validation"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func validateVolumes(volumes []corev1.Volume, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	seen := map[string]struct{}{}
	for i := range volumes {
		volume := &volumes[i]
		volumePath := path.Index(i)
		if volume.Name == "" {
			errs = append(errs, required(volumePath.Child("name"), "name is required"))
		} else {
			errs = append(errs, messagesAsErrors(volumePath.Child("name"), utilvalidation.IsDNS1123Label(volume.Name))...)
			if _, duplicate := seen[volume.Name]; duplicate {
				errs = append(errs, invalid(volumePath.Child("name"), "volume name must be unique"))
			} else {
				seen[volume.Name] = struct{}{}
			}
		}
		errs = append(errs, validateVolumeSource(&volume.VolumeSource, volumePath)...)
	}
	return errs
}

func validateVolumeSource(source *corev1.VolumeSource, path *field.Path) field.ErrorList {
	allowed := fieldSet("EmptyDir", "ConfigMap", "Secret", "PersistentVolumeClaim", "DownwardAPI", "Projected")
	errs := rejectNonZeroFields(reflect.ValueOf(source).Elem(), path, allowed)

	count := 0
	if source.EmptyDir != nil {
		count++
		errs = append(errs, validateEmptyDir(source.EmptyDir, path.Child("emptyDir"))...)
	}
	if source.ConfigMap != nil {
		count++
		errs = append(errs, validateConfigMapVolume(source.ConfigMap, path.Child("configMap"))...)
	}
	if source.Secret != nil {
		count++
		errs = append(errs, validateSecretVolume(source.Secret, path.Child("secret"))...)
	}
	if source.PersistentVolumeClaim != nil {
		count++
		errs = append(errs, validatePVCVolume(source.PersistentVolumeClaim, path.Child("persistentVolumeClaim"))...)
	}
	if source.DownwardAPI != nil {
		count++
		errs = append(errs, validateDownwardAPIVolume(source.DownwardAPI, path.Child("downwardAPI"))...)
	}
	if source.Projected != nil {
		count++
		errs = append(errs, validateProjectedVolume(source.Projected, path.Child("projected"))...)
	}
	if count == 0 {
		errs = append(errs, required(path, "exactly one supported volume source is required"))
	} else if count > 1 {
		errs = append(errs, invalid(path, "exactly one volume source may be specified"))
	}
	return errs
}

func validateEmptyDir(source *corev1.EmptyDirVolumeSource, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if source.SizeLimit != nil && source.SizeLimit.Sign() < 0 {
		errs = append(errs, invalid(path.Child("sizeLimit"), "must be non-negative"))
	}
	return errs
}

func validateConfigMapVolume(source *corev1.ConfigMapVolumeSource, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if source.Name == "" {
		errs = append(errs, required(path.Child("name"), "name is required"))
	} else {
		errs = append(errs, messagesAsErrors(path.Child("name"), apierrors.NameIsDNSSubdomain(source.Name, false))...)
	}
	errs = append(errs, validateMode(source.DefaultMode, path.Child("defaultMode"))...)
	errs = append(errs, validateKeyToPaths(source.Items, path.Child("items"))...)
	return errs
}

func validateSecretVolume(source *corev1.SecretVolumeSource, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if source.SecretName == "" {
		errs = append(errs, required(path.Child("secretName"), "secretName is required"))
	} else {
		errs = append(errs, messagesAsErrors(path.Child("secretName"), apierrors.NameIsDNSSubdomain(source.SecretName, false))...)
	}
	errs = append(errs, validateMode(source.DefaultMode, path.Child("defaultMode"))...)
	errs = append(errs, validateKeyToPaths(source.Items, path.Child("items"))...)
	return errs
}

func validatePVCVolume(source *corev1.PersistentVolumeClaimVolumeSource, path *field.Path) field.ErrorList {
	if source.ClaimName == "" {
		return field.ErrorList{required(path.Child("claimName"), "claimName is required")}
	}
	return messagesAsErrors(path.Child("claimName"), apierrors.NameIsDNSSubdomain(source.ClaimName, false))
}

func validateDownwardAPIVolume(source *corev1.DownwardAPIVolumeSource, path *field.Path) field.ErrorList {
	errs := validateMode(source.DefaultMode, path.Child("defaultMode"))
	seen := map[string]struct{}{}
	for i := range source.Items {
		itemPath := path.Child("items").Index(i)
		errs = append(errs, validateDownwardAPIFile(&source.Items[i], itemPath, true)...)
		errs = append(errs, recordUniquePath(source.Items[i].Path, itemPath.Child("path"), seen)...)
	}
	return errs
}

func validateProjectedVolume(source *corev1.ProjectedVolumeSource, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, validateMode(source.DefaultMode, path.Child("defaultMode"))...)
	seenPaths := map[string]struct{}{}
	for i := range source.Sources {
		projection := &source.Sources[i]
		projectionPath := path.Child("sources").Index(i)
		errs = append(errs, rejectNonZeroFields(reflect.ValueOf(projection).Elem(), projectionPath, fieldSet("Secret", "ConfigMap", "DownwardAPI"))...)
		count := 0
		if projection.Secret != nil {
			count++
			errs = append(errs, validateSecretProjection(projection.Secret, projectionPath.Child("secret"), seenPaths)...)
		}
		if projection.ConfigMap != nil {
			count++
			errs = append(errs, validateConfigMapProjection(projection.ConfigMap, projectionPath.Child("configMap"), seenPaths)...)
		}
		if projection.DownwardAPI != nil {
			count++
			errs = append(errs, validateDownwardAPIProjection(projection.DownwardAPI, projectionPath.Child("downwardAPI"), seenPaths)...)
		}
		if count == 0 {
			errs = append(errs, required(projectionPath, "one supported projection source is required"))
		} else if count > 1 {
			errs = append(errs, invalid(projectionPath, "only one projection source may be specified"))
		}
	}
	return errs
}

func validateSecretProjection(source *corev1.SecretProjection, path *field.Path, seen map[string]struct{}) field.ErrorList {
	var errs field.ErrorList
	if source.Name == "" {
		errs = append(errs, required(path.Child("name"), "name is required"))
	} else {
		errs = append(errs, messagesAsErrors(path.Child("name"), apierrors.NameIsDNSSubdomain(source.Name, false))...)
	}
	for i := range source.Items {
		itemPath := path.Child("items").Index(i)
		errs = append(errs, validateKeyToPath(&source.Items[i], itemPath)...)
		errs = append(errs, recordUniquePath(source.Items[i].Path, itemPath.Child("path"), seen)...)
	}
	return errs
}

func validateConfigMapProjection(source *corev1.ConfigMapProjection, path *field.Path, seen map[string]struct{}) field.ErrorList {
	var errs field.ErrorList
	if source.Name == "" {
		errs = append(errs, required(path.Child("name"), "name is required"))
	} else {
		errs = append(errs, messagesAsErrors(path.Child("name"), apierrors.NameIsDNSSubdomain(source.Name, false))...)
	}
	for i := range source.Items {
		itemPath := path.Child("items").Index(i)
		errs = append(errs, validateKeyToPath(&source.Items[i], itemPath)...)
		errs = append(errs, recordUniquePath(source.Items[i].Path, itemPath.Child("path"), seen)...)
	}
	return errs
}

func validateDownwardAPIProjection(source *corev1.DownwardAPIProjection, path *field.Path, seen map[string]struct{}) field.ErrorList {
	var errs field.ErrorList
	for i := range source.Items {
		itemPath := path.Child("items").Index(i)
		errs = append(errs, validateDownwardAPIFile(&source.Items[i], itemPath, true)...)
		errs = append(errs, recordUniquePath(source.Items[i].Path, itemPath.Child("path"), seen)...)
	}
	return errs
}

func validateKeyToPaths(items []corev1.KeyToPath, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	seen := map[string]struct{}{}
	for i := range items {
		itemPath := path.Index(i)
		errs = append(errs, validateKeyToPath(&items[i], itemPath)...)
		errs = append(errs, recordUniquePath(items[i].Path, itemPath.Child("path"), seen)...)
	}
	return errs
}

func validateKeyToPath(item *corev1.KeyToPath, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if item.Key == "" {
		errs = append(errs, required(path.Child("key"), "key is required"))
	} else {
		errs = append(errs, messagesAsErrors(path.Child("key"), utilvalidation.IsConfigMapKey(item.Key))...)
	}
	if item.Path == "" {
		errs = append(errs, required(path.Child("path"), "path is required"))
	} else {
		errs = append(errs, validateLocalPath(item.Path, path.Child("path"))...)
	}
	errs = append(errs, validateMode(item.Mode, path.Child("mode"))...)
	return errs
}

func validateDownwardAPIFile(item *corev1.DownwardAPIVolumeFile, path *field.Path, volume bool) field.ErrorList {
	var errs field.ErrorList
	if item.Path == "" {
		errs = append(errs, required(path.Child("path"), "path is required"))
	} else {
		errs = append(errs, validateLocalPath(item.Path, path.Child("path"))...)
	}
	sources := 0
	if item.FieldRef != nil {
		sources++
		errs = append(errs, validateObjectFieldSelector(item.FieldRef, path.Child("fieldRef"), downwardVolumeFields)...)
	}
	if item.ResourceFieldRef != nil {
		sources++
		errs = append(errs, validateResourceFieldSelector(item.ResourceFieldRef, path.Child("resourceFieldRef"), volume)...)
	}
	if sources == 0 {
		errs = append(errs, required(path, "one downward API source is required"))
	} else if sources > 1 {
		errs = append(errs, invalid(path, "fieldRef and resourceFieldRef are mutually exclusive"))
	}
	errs = append(errs, validateMode(item.Mode, path.Child("mode"))...)
	return errs
}

var (
	downwardVolumeFields = map[string]struct{}{
		"metadata.name": {}, "metadata.namespace": {}, "metadata.labels": {},
		"metadata.annotations": {}, "metadata.uid": {},
	}
	downwardEnvFields = map[string]struct{}{
		"metadata.name": {}, "metadata.namespace": {}, "metadata.uid": {},
		"spec.nodeName": {}, "spec.serviceAccountName": {},
		"status.hostIP": {}, "status.hostIPs": {}, "status.podIP": {}, "status.podIPs": {},
	}
)

func validateObjectFieldSelector(selector *corev1.ObjectFieldSelector, path *field.Path, allowed map[string]struct{}) field.ErrorList {
	var errs field.ErrorList
	if selector.APIVersion != "" && selector.APIVersion != "v1" {
		errs = append(errs, invalid(path.Child("apiVersion"), "only v1 is supported"))
	}
	if selector.FieldPath == "" {
		return append(errs, required(path.Child("fieldPath"), "fieldPath is required"))
	}
	base, subscript, hasSubscript := splitSubscriptedPath(selector.FieldPath)
	if hasSubscript {
		if base != "metadata.labels" && base != "metadata.annotations" {
			return append(errs, invalid(path.Child("fieldPath"), "field does not support a subscript"))
		}
		if _, ok := allowed[base]; !ok {
			return append(errs, invalid(path.Child("fieldPath"), "field path is not supported"))
		}
		candidate := subscript
		if base == "metadata.annotations" {
			candidate = strings.ToLower(candidate)
		}
		if subscript == "" || len(utilvalidation.IsQualifiedName(candidate)) != 0 {
			return append(errs, invalid(path.Child("fieldPath"), "field-path subscript is invalid"))
		}
		return errs
	}
	if _, ok := allowed[selector.FieldPath]; !ok {
		errs = append(errs, invalid(path.Child("fieldPath"), "field path is not supported"))
	}
	return errs
}

func splitSubscriptedPath(value string) (string, string, bool) {
	open := strings.Index(value, "['")
	if open < 0 || !strings.HasSuffix(value, "']") {
		return value, "", false
	}
	return value[:open], value[open+2 : len(value)-2], true
}

func validateResourceFieldSelector(selector *corev1.ResourceFieldSelector, path *field.Path, volume bool) field.ErrorList {
	var errs field.ErrorList
	if volume && selector.ContainerName == "" {
		errs = append(errs, required(path.Child("containerName"), "containerName is required for volume projections"))
	}
	resourceName := selector.Resource
	if !isAllowedDownwardResource(resourceName) {
		errs = append(errs, invalid(path.Child("resource"), "resource is not supported by Kubernetes 1.30 downward API"))
	}
	if !selector.Divisor.IsZero() && !validResourceDivisor(resourceName, selector.Divisor.String()) {
		errs = append(errs, invalid(path.Child("divisor"), "divisor is not supported for this resource"))
	}
	return errs
}

func isAllowedDownwardResource(name string) bool {
	switch name {
	case "limits.cpu", "limits.memory", "limits.ephemeral-storage", "requests.cpu", "requests.memory", "requests.ephemeral-storage":
		return true
	default:
		return strings.HasPrefix(name, "limits.hugepages-") || strings.HasPrefix(name, "requests.hugepages-")
	}
}

func validResourceDivisor(resourceName, divisor string) bool {
	if strings.HasSuffix(resourceName, ".cpu") {
		return divisor == "1m" || divisor == "1"
	}
	switch divisor {
	case "1", "1k", "1M", "1G", "1T", "1P", "1E", "1Ki", "1Mi", "1Gi", "1Ti", "1Pi", "1Ei":
		return true
	default:
		return false
	}
}

func validateMode(mode *int32, path *field.Path) field.ErrorList {
	if mode != nil && (*mode < 0 || *mode > 0777) {
		return field.ErrorList{invalid(path, "file mode must be in the range 0000..0777")}
	}
	return nil
}

func validateLocalPath(value string, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if filepath.IsAbs(value) {
		errs = append(errs, invalid(path, "must be a relative path"))
	}
	for _, part := range strings.Split(filepath.ToSlash(value), "/") {
		if part == ".." {
			errs = append(errs, invalid(path, "must not contain backsteps"))
			break
		}
	}
	if strings.HasPrefix(value, "..") && !strings.HasPrefix(value, "../") {
		errs = append(errs, invalid(path, "must not start with '..'"))
	}
	return errs
}

func recordUniquePath(value string, path *field.Path, seen map[string]struct{}) field.ErrorList {
	if value == "" {
		return nil
	}
	if _, duplicate := seen[value]; duplicate {
		return field.ErrorList{invalid(path, "path must be unique within the volume")}
	}
	seen[value] = struct{}{}
	return nil
}
