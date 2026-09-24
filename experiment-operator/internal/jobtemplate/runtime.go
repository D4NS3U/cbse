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

package jobtemplate

import (
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// alpha4 runner Pod DNS-config limits, mirroring the Kubernetes 1.30
// admission defaults: at most three nameservers, at most 32 search paths, and a
// joined search list of at most 2048 characters.
const (
	maxDNSNameservers = 3
	maxDNSSearchPaths = 32
	maxDNSSearchChars = 2048
)

// validatePodRuntime validates the Pod runtime fields: a non-negative
// terminationGracePeriodSeconds, a supported dnsPolicy, the dnsConfig, and
// hostAliases.
func validatePodRuntime(spec *corev1.PodSpec, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if spec.TerminationGracePeriodSeconds != nil && *spec.TerminationGracePeriodSeconds < 0 {
		errs = append(errs, invalid(path.Child("terminationGracePeriodSeconds"), "must be non-negative"))
	}
	if spec.DNSPolicy != "" {
		switch spec.DNSPolicy {
		case corev1.DNSClusterFirstWithHostNet, corev1.DNSClusterFirst, corev1.DNSDefault, corev1.DNSNone:
		default:
			errs = append(errs, invalid(path.Child("dnsPolicy"), "unsupported DNS policy"))
		}
	}
	errs = append(errs, validateDNSConfig(spec.DNSConfig, spec.DNSPolicy, path.Child("dnsConfig"))...)
	errs = append(errs, validateHostAliases(spec.HostAliases, path.Child("hostAliases"))...)
	return errs
}

// validateDNSConfig validates a PodDNSConfig. When dnsPolicy is None a config
// with at least one nameserver is required; otherwise it caps nameservers at
// three, validates each as a legacy IP, caps search paths at 32 and their
// joined length at 2048, validates each search as a DNS subdomain, and requires
// each option to have a name.
func validateDNSConfig(config *corev1.PodDNSConfig, policy corev1.DNSPolicy, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if policy == corev1.DNSNone {
		if config == nil {
			return field.ErrorList{required(path, "dnsConfig is required when dnsPolicy is None")}
		}
		if len(config.Nameservers) == 0 {
			errs = append(errs, required(path.Child("nameservers"), "at least one nameserver is required when dnsPolicy is None"))
		}
	}
	if config == nil {
		return errs
	}
	if len(config.Nameservers) > maxDNSNameservers {
		errs = append(errs, invalid(path.Child("nameservers"), "at most three nameservers are supported"))
	}
	for i, nameserver := range config.Nameservers {
		errs = append(errs, validateIP130(path.Child("nameservers").Index(i), nameserver)...)
	}
	if len(config.Searches) > maxDNSSearchPaths {
		errs = append(errs, invalid(path.Child("searches"), "at most 32 search paths are supported"))
	}
	if len(strings.Join(config.Searches, " ")) > maxDNSSearchChars {
		errs = append(errs, invalid(path.Child("searches"), "joined DNS search list is too long"))
	}
	for i, search := range config.Searches {
		search = strings.TrimSuffix(search, ".")
		errs = append(errs, messagesAsErrors(path.Child("searches").Index(i), utilvalidation.IsDNS1123Subdomain(search))...)
	}
	for i, option := range config.Options {
		if option.Name == "" {
			errs = append(errs, required(path.Child("options").Index(i).Child("name"), "option name is required"))
		}
	}
	return errs
}

// validateHostAliases validates each host alias: a legacy IP and DNS-subdomain
// hostnames.
func validateHostAliases(aliases []corev1.HostAlias, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	for i, alias := range aliases {
		itemPath := path.Index(i)
		errs = append(errs, validateIP130(itemPath.Child("ip"), alias.IP)...)
		for j, hostname := range alias.Hostnames {
			errs = append(errs, messagesAsErrors(itemPath.Child("hostnames").Index(j), utilvalidation.IsDNS1123Subdomain(hostname))...)
		}
	}
	return errs
}

// Kubernetes 1.30 validated these legacy fields with ParseIPSloppy. The
// current apimachinery helper is strict, so use its explicit legacy mode to
// preserve the pinned acceptance behavior without importing Kubernetes
// internal validation packages.
func validateIP130(path *field.Path, value string) field.ErrorList {
	return sanitizeErrors(utilvalidation.IsValidIPForLegacyField(path, value, false, nil))
}

// validatePodSecurityContext validates a Pod security context. Only RunAsUser,
// RunAsGroup, FSGroup, FSGroupChangePolicy, and SupplementalGroups are allowed;
// each ID must be a positive valid user/group ID, and FSGroupChangePolicy must
// be OnRootMismatch or Always. Privileged mode, host namespaces, seccomp,
// AppArmor, and the other hardening fields are owned by CBSE and rejected
// here.
func validatePodSecurityContext(context *corev1.PodSecurityContext, path *field.Path) field.ErrorList {
	if context == nil {
		return nil
	}
	allowed := fieldSet("RunAsUser", "RunAsGroup", "FSGroup", "SupplementalGroups", "FSGroupChangePolicy")
	errs := rejectNonZeroFields(reflect.ValueOf(context).Elem(), path, allowed)
	errs = append(errs, validatePositiveID(context.RunAsUser, path.Child("runAsUser"), true)...)
	errs = append(errs, validatePositiveID(context.RunAsGroup, path.Child("runAsGroup"), false)...)
	errs = append(errs, validatePositiveID(context.FSGroup, path.Child("fsGroup"), false)...)
	for i := range context.SupplementalGroups {
		group := context.SupplementalGroups[i]
		errs = append(errs, validatePositiveID(&group, path.Child("supplementalGroups").Index(i), false)...)
	}
	if context.FSGroupChangePolicy != nil && *context.FSGroupChangePolicy != corev1.FSGroupChangeOnRootMismatch && *context.FSGroupChangePolicy != corev1.FSGroupChangeAlways {
		errs = append(errs, invalid(path.Child("fsGroupChangePolicy"), "unsupported fsGroupChangePolicy"))
	}
	return errs
}
