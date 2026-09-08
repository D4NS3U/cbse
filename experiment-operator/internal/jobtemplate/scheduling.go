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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1validation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var strictLabelSelectorOptions = metav1validation.LabelSelectorValidationOptions{}

func validateScheduling(spec *corev1.PodSpec, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, sanitizeErrors(metav1validation.ValidateLabels(spec.NodeSelector, path.Child("nodeSelector")))...)
	errs = append(errs, validateAffinity(spec.Affinity, path.Child("affinity"))...)
	errs = append(errs, validateTolerations(spec.Tolerations, path.Child("tolerations"))...)
	errs = append(errs, validateTopologySpread(spec.TopologySpreadConstraints, path.Child("topologySpreadConstraints"))...)
	if spec.PriorityClassName != "" {
		errs = append(errs, messagesAsErrors(path.Child("priorityClassName"), apierrors.NameIsDNSSubdomain(spec.PriorityClassName, false))...)
	}
	if spec.RuntimeClassName != nil {
		if *spec.RuntimeClassName == "" {
			errs = append(errs, required(path.Child("runtimeClassName"), "runtimeClassName must be non-empty when supplied"))
		} else {
			errs = append(errs, messagesAsErrors(path.Child("runtimeClassName"), apierrors.NameIsDNSSubdomain(*spec.RuntimeClassName, false))...)
		}
	}
	if spec.PreemptionPolicy != nil && *spec.PreemptionPolicy != corev1.PreemptLowerPriority && *spec.PreemptionPolicy != corev1.PreemptNever {
		errs = append(errs, invalid(path.Child("preemptionPolicy"), "preemptionPolicy must be PreemptLowerPriority or Never"))
	}
	return errs
}

func validateAffinity(affinity *corev1.Affinity, path *field.Path) field.ErrorList {
	if affinity == nil {
		return nil
	}
	var errs field.ErrorList
	if affinity.NodeAffinity != nil {
		errs = append(errs, validateNodeAffinity(affinity.NodeAffinity, path.Child("nodeAffinity"))...)
	}
	if affinity.PodAffinity != nil {
		errs = append(errs, validatePodAffinity(affinity.PodAffinity, path.Child("podAffinity"))...)
	}
	if affinity.PodAntiAffinity != nil {
		errs = append(errs, validatePodAntiAffinity(affinity.PodAntiAffinity, path.Child("podAntiAffinity"))...)
	}
	return errs
}

func validateNodeAffinity(affinity *corev1.NodeAffinity, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if affinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		errs = append(errs, validateNodeSelector(affinity.RequiredDuringSchedulingIgnoredDuringExecution, path.Child("requiredDuringSchedulingIgnoredDuringExecution"))...)
	}
	for i := range affinity.PreferredDuringSchedulingIgnoredDuringExecution {
		term := &affinity.PreferredDuringSchedulingIgnoredDuringExecution[i]
		termPath := path.Child("preferredDuringSchedulingIgnoredDuringExecution").Index(i)
		if term.Weight <= 0 || term.Weight > 100 {
			errs = append(errs, invalid(termPath.Child("weight"), "weight must be in the range 1..100"))
		}
		errs = append(errs, validateNodeSelectorTerm(&term.Preference, termPath.Child("preference"))...)
	}
	return errs
}

func validateNodeSelector(selector *corev1.NodeSelector, path *field.Path) field.ErrorList {
	if len(selector.NodeSelectorTerms) == 0 {
		return field.ErrorList{required(path.Child("nodeSelectorTerms"), "at least one node selector term is required")}
	}
	var errs field.ErrorList
	for i := range selector.NodeSelectorTerms {
		errs = append(errs, validateNodeSelectorTerm(&selector.NodeSelectorTerms[i], path.Child("nodeSelectorTerms").Index(i))...)
	}
	return errs
}

func validateNodeSelectorTerm(term *corev1.NodeSelectorTerm, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	for i := range term.MatchExpressions {
		errs = append(errs, validateNodeSelectorRequirement(&term.MatchExpressions[i], path.Child("matchExpressions").Index(i), false)...)
	}
	for i := range term.MatchFields {
		errs = append(errs, validateNodeSelectorRequirement(&term.MatchFields[i], path.Child("matchFields").Index(i), true)...)
	}
	return errs
}

func validateNodeSelectorRequirement(requirement *corev1.NodeSelectorRequirement, path *field.Path, fieldSelector bool) field.ErrorList {
	var errs field.ErrorList
	if fieldSelector {
		if requirement.Key != metav1.ObjectNameField {
			errs = append(errs, invalid(path.Child("key"), "only metadata.name is supported in matchFields"))
		}
		if requirement.Operator != corev1.NodeSelectorOpIn && requirement.Operator != corev1.NodeSelectorOpNotIn {
			errs = append(errs, invalid(path.Child("operator"), "matchFields supports only In or NotIn"))
		}
		if len(requirement.Values) != 1 {
			errs = append(errs, invalid(path.Child("values"), "matchFields requires exactly one value"))
		} else {
			errs = append(errs, messagesAsErrors(path.Child("values").Index(0), utilvalidation.IsDNS1123Subdomain(requirement.Values[0]))...)
		}
		return errs
	}

	errs = append(errs, sanitizeErrors(metav1validation.ValidateLabelName(requirement.Key, path.Child("key")))...)
	switch requirement.Operator {
	case corev1.NodeSelectorOpIn, corev1.NodeSelectorOpNotIn:
		if len(requirement.Values) == 0 {
			errs = append(errs, required(path.Child("values"), "In and NotIn require values"))
		}
	case corev1.NodeSelectorOpExists, corev1.NodeSelectorOpDoesNotExist:
		if len(requirement.Values) != 0 {
			errs = append(errs, forbidden(path.Child("values"), "Exists and DoesNotExist cannot have values"))
		}
	case corev1.NodeSelectorOpGt, corev1.NodeSelectorOpLt:
		if len(requirement.Values) != 1 {
			errs = append(errs, invalid(path.Child("values"), "Gt and Lt require exactly one value"))
		}
	default:
		errs = append(errs, invalid(path.Child("operator"), "unsupported node selector operator"))
	}
	return errs
}

func validatePodAffinity(affinity *corev1.PodAffinity, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	for i := range affinity.RequiredDuringSchedulingIgnoredDuringExecution {
		errs = append(errs, validatePodAffinityTerm(&affinity.RequiredDuringSchedulingIgnoredDuringExecution[i], path.Child("requiredDuringSchedulingIgnoredDuringExecution").Index(i))...)
	}
	for i := range affinity.PreferredDuringSchedulingIgnoredDuringExecution {
		weighted := &affinity.PreferredDuringSchedulingIgnoredDuringExecution[i]
		weightedPath := path.Child("preferredDuringSchedulingIgnoredDuringExecution").Index(i)
		if weighted.Weight <= 0 || weighted.Weight > 100 {
			errs = append(errs, invalid(weightedPath.Child("weight"), "weight must be in the range 1..100"))
		}
		errs = append(errs, validatePodAffinityTerm(&weighted.PodAffinityTerm, weightedPath.Child("podAffinityTerm"))...)
	}
	return errs
}

func validatePodAntiAffinity(affinity *corev1.PodAntiAffinity, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	for i := range affinity.RequiredDuringSchedulingIgnoredDuringExecution {
		errs = append(errs, validatePodAffinityTerm(&affinity.RequiredDuringSchedulingIgnoredDuringExecution[i], path.Child("requiredDuringSchedulingIgnoredDuringExecution").Index(i))...)
	}
	for i := range affinity.PreferredDuringSchedulingIgnoredDuringExecution {
		weighted := &affinity.PreferredDuringSchedulingIgnoredDuringExecution[i]
		weightedPath := path.Child("preferredDuringSchedulingIgnoredDuringExecution").Index(i)
		if weighted.Weight <= 0 || weighted.Weight > 100 {
			errs = append(errs, invalid(weightedPath.Child("weight"), "weight must be in the range 1..100"))
		}
		errs = append(errs, validatePodAffinityTerm(&weighted.PodAffinityTerm, weightedPath.Child("podAffinityTerm"))...)
	}
	return errs
}

func validatePodAffinityTerm(term *corev1.PodAffinityTerm, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, sanitizeErrors(metav1validation.ValidateLabelSelector(term.LabelSelector, strictLabelSelectorOptions, path.Child("labelSelector")))...)
	errs = append(errs, sanitizeErrors(metav1validation.ValidateLabelSelector(term.NamespaceSelector, strictLabelSelectorOptions, path.Child("namespaceSelector")))...)
	for i, namespace := range term.Namespaces {
		errs = append(errs, messagesAsErrors(path.Child("namespaces").Index(i), utilvalidation.IsDNS1123Label(namespace))...)
	}
	if term.TopologyKey == "" {
		errs = append(errs, required(path.Child("topologyKey"), "topologyKey is required"))
	} else {
		errs = append(errs, sanitizeErrors(metav1validation.ValidateLabelName(term.TopologyKey, path.Child("topologyKey")))...)
	}
	errs = append(errs, validateAffinityLabelKeys(term.MatchLabelKeys, term.MismatchLabelKeys, term.LabelSelector, path)...)
	return errs
}

func validateAffinityLabelKeys(match, mismatch []string, selector *metav1.LabelSelector, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if (len(match) != 0 || len(mismatch) != 0) && selector == nil {
		errs = append(errs, forbidden(path, "matchLabelKeys and mismatchLabelKeys require labelSelector"))
	}
	selectorKeys := labelSelectorKeys(selector)
	mismatchSet := map[string]struct{}{}
	for _, key := range mismatch {
		mismatchSet[key] = struct{}{}
	}
	for i, key := range match {
		keyPath := path.Child("matchLabelKeys").Index(i)
		errs = append(errs, sanitizeErrors(metav1validation.ValidateLabelName(key, keyPath))...)
		if _, duplicate := selectorKeys[key]; duplicate {
			errs = append(errs, invalid(keyPath, "key also exists in labelSelector"))
		}
		if _, overlap := mismatchSet[key]; overlap {
			errs = append(errs, invalid(keyPath, "key also exists in mismatchLabelKeys"))
		}
	}
	for i, key := range mismatch {
		errs = append(errs, sanitizeErrors(metav1validation.ValidateLabelName(key, path.Child("mismatchLabelKeys").Index(i)))...)
	}
	return errs
}

func labelSelectorKeys(selector *metav1.LabelSelector) map[string]struct{} {
	keys := map[string]struct{}{}
	if selector == nil {
		return keys
	}
	for key := range selector.MatchLabels {
		keys[key] = struct{}{}
	}
	for _, expression := range selector.MatchExpressions {
		keys[expression.Key] = struct{}{}
	}
	return keys
}

func validateTolerations(tolerations []corev1.Toleration, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	for i := range tolerations {
		toleration := &tolerations[i]
		itemPath := path.Index(i)
		if toleration.Key != "" {
			errs = append(errs, sanitizeErrors(metav1validation.ValidateLabelName(toleration.Key, itemPath.Child("key")))...)
		} else if toleration.Operator != corev1.TolerationOpExists {
			errs = append(errs, invalid(itemPath.Child("operator"), "an empty key requires Exists"))
		}
		switch toleration.Operator {
		case "", corev1.TolerationOpEqual:
			errs = append(errs, messagesAsErrors(itemPath.Child("value"), utilvalidation.IsValidLabelValue(toleration.Value))...)
		case corev1.TolerationOpExists:
			if toleration.Value != "" {
				errs = append(errs, forbidden(itemPath.Child("value"), "Exists requires an empty value"))
			}
		default:
			errs = append(errs, invalid(itemPath.Child("operator"), "unsupported toleration operator"))
		}
		if toleration.Effect != "" && toleration.Effect != corev1.TaintEffectNoSchedule && toleration.Effect != corev1.TaintEffectPreferNoSchedule && toleration.Effect != corev1.TaintEffectNoExecute {
			errs = append(errs, invalid(itemPath.Child("effect"), "unsupported toleration effect"))
		}
		if toleration.TolerationSeconds != nil {
			if toleration.Effect != corev1.TaintEffectNoExecute {
				errs = append(errs, invalid(itemPath.Child("effect"), "NoExecute is required when tolerationSeconds is set"))
			}
		}
	}
	return errs
}

func validateTopologySpread(constraints []corev1.TopologySpreadConstraint, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	seenPairs := map[string]struct{}{}
	for i := range constraints {
		constraint := &constraints[i]
		itemPath := path.Index(i)
		if constraint.MaxSkew <= 0 {
			errs = append(errs, invalid(itemPath.Child("maxSkew"), "maxSkew must be positive"))
		}
		if constraint.TopologyKey == "" {
			errs = append(errs, required(itemPath.Child("topologyKey"), "topologyKey is required"))
		}
		if constraint.WhenUnsatisfiable != corev1.DoNotSchedule && constraint.WhenUnsatisfiable != corev1.ScheduleAnyway {
			errs = append(errs, invalid(itemPath.Child("whenUnsatisfiable"), "unsupported scheduling action"))
		}
		pair := constraint.TopologyKey + "\x00" + string(constraint.WhenUnsatisfiable)
		if _, duplicate := seenPairs[pair]; duplicate {
			errs = append(errs, invalid(itemPath, "topologyKey and whenUnsatisfiable pair must be unique"))
		} else {
			seenPairs[pair] = struct{}{}
		}
		if constraint.MinDomains != nil {
			if *constraint.MinDomains <= 0 {
				errs = append(errs, invalid(itemPath.Child("minDomains"), "minDomains must be positive"))
			}
			if constraint.WhenUnsatisfiable != corev1.DoNotSchedule {
				errs = append(errs, invalid(itemPath.Child("minDomains"), "minDomains requires DoNotSchedule"))
			}
		}
		errs = append(errs, validateNodeInclusionPolicy(constraint.NodeAffinityPolicy, itemPath.Child("nodeAffinityPolicy"))...)
		errs = append(errs, validateNodeInclusionPolicy(constraint.NodeTaintsPolicy, itemPath.Child("nodeTaintsPolicy"))...)
		errs = append(errs, sanitizeErrors(metav1validation.ValidateLabelSelector(constraint.LabelSelector, strictLabelSelectorOptions, itemPath.Child("labelSelector")))...)
		errs = append(errs, validateTopologyMatchLabelKeys(constraint.MatchLabelKeys, constraint.LabelSelector, itemPath.Child("matchLabelKeys"))...)
	}
	return errs
}

func validateNodeInclusionPolicy(policy *corev1.NodeInclusionPolicy, path *field.Path) field.ErrorList {
	if policy != nil && *policy != corev1.NodeInclusionPolicyHonor && *policy != corev1.NodeInclusionPolicyIgnore {
		return field.ErrorList{invalid(path, "policy must be Honor or Ignore")}
	}
	return nil
}

func validateTopologyMatchLabelKeys(keys []string, selector *metav1.LabelSelector, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if len(keys) != 0 && selector == nil {
		errs = append(errs, forbidden(path, "matchLabelKeys requires labelSelector"))
	}
	selectorKeys := labelSelectorKeys(selector)
	for i, key := range keys {
		keyPath := path.Index(i)
		errs = append(errs, sanitizeErrors(metav1validation.ValidateLabelName(key, keyPath))...)
		if _, duplicate := selectorKeys[key]; duplicate {
			errs = append(errs, invalid(keyPath, "key also exists in labelSelector"))
		}
	}
	return errs
}
