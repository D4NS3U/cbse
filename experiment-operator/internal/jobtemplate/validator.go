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

// Package jobtemplate owns the alpha4 runner Job-template policy. The policy is
// intentionally internal to the Experiment Operator: it validates the persisted
// external Kubernetes API types without defaulting them or consulting a cluster.
package jobtemplate

import (
	"reflect"
	"sort"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	apierrors "k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1validation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// alpha4 runner Job-template policy constants: the root field path label, the
// required registry-auth Secret name, the reserved CBSE metadata prefix, and the
// default Pod termination grace (seconds) used when the template omits one.
const (
	rootField                 = "spec.runner.jobTemplate"
	registryAuthSecretName    = "cbse-registry-auth"
	identityMetadataPrefix    = "experiment.cbse.terministic.de/"
	defaultTerminationSeconds = int64(30)
)

var (
	// rootPath is the field.Path root for every validation error in this package.
	rootPath = field.NewPath("spec", "runner", "jobTemplate")

	// jobControllerMetadataKeys are the labels/annotations the Job controller
	// injects into Pods and which a template must not set.
	jobControllerMetadataKeys = map[string]struct{}{
		"controller-uid":                     {},
		"job-name":                           {},
		"batch.kubernetes.io/controller-uid": {},
		"batch.kubernetes.io/job-name":       {},
	}

	// protectedPodAnnotationKeys are individual Pod annotations that control
	// behavior CBSE owns and which a template must not set.
	protectedPodAnnotationKeys = map[string]struct{}{
		"kubernetes.io/config.mirror":               {},
		"scheduler.alpha.kubernetes.io/tolerations": {},
		"seccomp.security.alpha.kubernetes.io/pod":  {},
	}

	// protectedPodAnnotationPrefixes are per-container seccomp/AppArmor
	// annotation prefixes a Pod template must not set.
	protectedPodAnnotationPrefixes = []string{
		"container.seccomp.security.alpha.kubernetes.io/",
		"container.apparmor.security.beta.kubernetes.io/",
	}
)

// ValidateAndNormalizeJobTemplate validates the persisted alpha4 Job template
// against the Kubernetes-1.30-pinned allow-list. A present template is copied
// before any inspection. The copy is returned on both success and failure so
// callers can prove that validation never aliases or mutates the CR.
func ValidateAndNormalizeJobTemplate(template *batchv1.JobTemplateSpec) (*batchv1.JobTemplateSpec, field.ErrorList) {
	if template == nil {
		return nil, nil
	}

	copy := template.DeepCopy()
	errs := validateTemplate(copy)
	if len(errs) != 0 {
		return copy, errs
	}

	normalizeTemplate(copy)
	return copy, nil
}

// validateTemplate runs the full alpha4 Job-template validation over a copied
// template: metadata, the ActiveDeadlineSeconds/Template field allow-list, the
// pod template metadata, and the Pod spec. It returns the accumulated field
// errors without mutating the template.
func validateTemplate(template *batchv1.JobTemplateSpec) field.ErrorList {
	var errs field.ErrorList

	errs = append(errs, validateMetadata(&template.ObjectMeta, rootPath.Child("metadata"), false)...)
	errs = append(errs, rejectNonZeroFields(reflect.ValueOf(&template.ObjectMeta).Elem(), rootPath.Child("metadata"), fieldSet("Labels", "Annotations"))...)
	errs = append(errs, rejectNonZeroFields(reflect.ValueOf(&template.Spec).Elem(), rootPath.Child("spec"), fieldSet("ActiveDeadlineSeconds", "Template"))...)

	if template.Spec.ActiveDeadlineSeconds != nil && *template.Spec.ActiveDeadlineSeconds <= 0 {
		errs = append(errs, invalid(rootPath.Child("spec", "activeDeadlineSeconds"), "must be greater than zero"))
	}

	podTemplatePath := rootPath.Child("spec", "template")
	errs = append(errs, validateMetadata(&template.Spec.Template.ObjectMeta, podTemplatePath.Child("metadata"), true)...)
	errs = append(errs, rejectNonZeroFields(reflect.ValueOf(&template.Spec.Template.ObjectMeta).Elem(), podTemplatePath.Child("metadata"), fieldSet("Labels", "Annotations"))...)
	errs = append(errs, validatePodSpec(&template.Spec.Template.Spec, podTemplatePath.Child("spec"))...)

	return errs
}

// validateMetadata validates Job or Pod template metadata. Labels and
// annotations are validated structurally; reserved CBSE keys are rejected in
// both; and, for Pod templates (podTemplate true), annotations that control Pod
// behavior CBSE owns are rejected too.
func validateMetadata(meta *metav1.ObjectMeta, path *field.Path, podTemplate bool) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, sanitizeErrors(metav1validation.ValidateLabels(meta.Labels, path.Child("labels")))...)
	errs = append(errs, sanitizeErrors(apierrors.ValidateAnnotations(meta.Annotations, path.Child("annotations")))...)

	for key := range meta.Labels {
		if isReservedMetadataKey(key) {
			errs = append(errs, forbidden(path.Child("labels").Key(key), "metadata key is reserved by CBSE"))
		}
	}
	for key := range meta.Annotations {
		if isReservedMetadataKey(key) {
			errs = append(errs, forbidden(path.Child("annotations").Key(key), "metadata key is reserved by CBSE"))
			continue
		}
		if podTemplate && isProtectedPodAnnotation(key) {
			errs = append(errs, forbidden(path.Child("annotations").Key(key), "Pod annotation controls behavior owned by CBSE"))
		}
	}

	return errs
}

// isReservedMetadataKey reports whether a label or annotation key is reserved
// for CBSE: anything under the experiment.cbse.terministic.de/ prefix or one of
// the Job controller's injected keys.
func isReservedMetadataKey(key string) bool {
	if strings.HasPrefix(key, identityMetadataPrefix) {
		return true
	}
	_, reserved := jobControllerMetadataKeys[key]
	return reserved
}

// isProtectedPodAnnotation reports whether a Pod annotation controls behavior
// CBSE owns (config mirror, scheduler tolerations, pod seccomp, or a
// per-container seccomp/AppArmor profile).
func isProtectedPodAnnotation(key string) bool {
	if _, protected := protectedPodAnnotationKeys[key]; protected {
		return true
	}
	for _, prefix := range protectedPodAnnotationPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// validatePodSpec validates the Pod spec against the alpha4 allow-list of
// fields, then delegates runtime, volumes, image pull secrets, scheduling, Pod
// security context, and container composition validation.
func validatePodSpec(spec *corev1.PodSpec, path *field.Path) field.ErrorList {
	allowed := fieldSet(
		"Volumes", "InitContainers", "Containers", "ImagePullSecrets",
		"SecurityContext", "NodeSelector", "Affinity", "Tolerations",
		"TopologySpreadConstraints", "PriorityClassName", "PreemptionPolicy",
		"RuntimeClassName", "TerminationGracePeriodSeconds", "DNSPolicy",
		"DNSConfig", "HostAliases", "EnableServiceLinks",
	)
	errs := rejectNonZeroFields(reflect.ValueOf(spec).Elem(), path, allowed)
	errs = append(errs, validatePodRuntime(spec, path)...)
	errs = append(errs, validateVolumes(spec.Volumes, path.Child("volumes"))...)
	errs = append(errs, validateImagePullSecrets(spec.ImagePullSecrets, path.Child("imagePullSecrets"))...)
	errs = append(errs, validateScheduling(spec, path)...)
	errs = append(errs, validatePodSecurityContext(spec.SecurityContext, path.Child("securityContext"))...)
	errs = append(errs, validateContainerComposition(spec, path)...)
	return errs
}

// validateImagePullSecrets validates each image pull Secret reference: a
// required DNS-subdomain name and no duplicates.
func validateImagePullSecrets(refs []corev1.LocalObjectReference, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	seen := map[string]struct{}{}
	for i, ref := range refs {
		itemPath := path.Index(i).Child("name")
		if ref.Name == "" {
			errs = append(errs, required(itemPath, "name is required"))
			continue
		}
		if messages := apierrors.NameIsDNSSubdomain(ref.Name, false); len(messages) != 0 {
			errs = append(errs, messagesAsErrors(itemPath, messages)...)
		}
		if _, duplicate := seen[ref.Name]; duplicate {
			errs = append(errs, invalid(itemPath, "duplicate image pull Secret name"))
		} else {
			seen[ref.Name] = struct{}{}
		}
	}
	return errs
}

// normalizeTemplate canonicalizes a validated template so equal templates
// compare equal: it nils out empty collections throughout the template (via
// normalizeNilCollections) and sorts imagePullSecrets by name for a stable
// comparison.
func normalizeTemplate(template *batchv1.JobTemplateSpec) {
	normalizeNilCollections(reflect.ValueOf(template))
	sort.Slice(template.Spec.Template.Spec.ImagePullSecrets, func(i, j int) bool {
		return template.Spec.Template.Spec.ImagePullSecrets[i].Name < template.Spec.Template.Spec.ImagePullSecrets[j].Name
	})
}

// normalizeNilCollections recursively walks value and zeroes any empty slice or
// map it can set, so an omitted collection and an explicitly empty collection
// normalize to the same (nil) value. Quantity, IntOrString, Time, and
// MicroTime are treated as leaves and never recursed into.
func normalizeNilCollections(value reflect.Value) {
	if !value.IsValid() {
		return
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return
		}
		normalizeNilCollections(value.Elem())
		return
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() || isNormalizationLeaf(value.Type().Elem()) {
			return
		}
		normalizeNilCollections(value.Elem())
		return
	}
	if isNormalizationLeaf(value.Type()) {
		return
	}

	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			fieldValue := value.Field(i)
			if fieldValue.CanSet() || fieldValue.Kind() == reflect.Pointer || fieldValue.Kind() == reflect.Struct {
				normalizeNilCollections(fieldValue)
			}
		}
	case reflect.Slice, reflect.Map:
		if value.Len() == 0 && value.CanSet() {
			value.Set(reflect.Zero(value.Type()))
			return
		}
		if value.Kind() == reflect.Slice {
			for i := 0; i < value.Len(); i++ {
				normalizeNilCollections(value.Index(i))
			}
		}
	}
}

// isNormalizationLeaf reports whether a type is a primitive Kubernetes value
// that normalizeNilCollections must not recurse into (Quantity, IntOrString,
// Time, MicroTime).
func isNormalizationLeaf(t reflect.Type) bool {
	return t == reflect.TypeOf(resource.Quantity{}) ||
		t == reflect.TypeOf(intstr.IntOrString{}) ||
		t == reflect.TypeOf(metav1.Time{}) ||
		t == reflect.TypeOf(metav1.MicroTime{})
}

// rejectNonZeroFields reflects over a struct and returns a Forbidden field
// error for every non-zero field whose name is not in the allowed set. The
// error path uses the struct's json tag name (or the Go field name when
// absent), so messages match the external API spelling.
func rejectNonZeroFields(value reflect.Value, path *field.Path, allowed map[string]struct{}) field.ErrorList {
	var errs field.ErrorList
	typeOfValue := value.Type()
	for i := 0; i < value.NumField(); i++ {
		structField := typeOfValue.Field(i)
		if _, ok := allowed[structField.Name]; ok {
			continue
		}
		fieldValue := value.Field(i)
		if isOmitted(fieldValue) {
			continue
		}
		jsonName := strings.Split(structField.Tag.Get("json"), ",")[0]
		if jsonName == "" || jsonName == "-" {
			jsonName = structField.Name
		}
		errs = append(errs, forbidden(path.Child(jsonName), "field is not supported by the alpha4 runner Job-template policy"))
	}
	return errs
}

// isOmitted reports whether a reflect.Value is effectively unset: an empty
// slice/map, a nil pointer/interface, or a zero scalar.
func isOmitted(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Map, reflect.Slice:
		return value.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return value.IsNil()
	default:
		return value.IsZero()
	}
}

// fieldSet builds a set of field names from the variadic arguments.
func fieldSet(names ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

// invalid returns a field.Invalid error with a redacted BadValue so the
// template's contents are not echoed into error messages.
func invalid(path *field.Path, reason string) *field.Error {
	return field.Invalid(path, "<redacted>", reason)
}

// required returns a field.Required error with a redacted BadValue.
func required(path *field.Path, reason string) *field.Error {
	return field.Required(path, reason)
}

// forbidden returns a field.Forbidden error with a redacted BadValue.
func forbidden(path *field.Path, reason string) *field.Error {
	return field.Forbidden(path, reason)
}

// messagesAsErrors converts a list of validation messages into field.Invalid
// errors at path, each with a redacted BadValue.
func messagesAsErrors(path *field.Path, messages []string) field.ErrorList {
	errs := make(field.ErrorList, 0, len(messages))
	for _, message := range messages {
		errs = append(errs, invalid(path, message))
	}
	return errs
}

// sanitizeErrors returns a copy of errs with every BadValue replaced by
// "<redacted>" so template contents are not leaked into error messages.
func sanitizeErrors(errs field.ErrorList) field.ErrorList {
	sanitized := make(field.ErrorList, 0, len(errs))
	for _, err := range errs {
		copy := *err
		copy.BadValue = "<redacted>"
		sanitized = append(sanitized, &copy)
	}
	return sanitized
}
