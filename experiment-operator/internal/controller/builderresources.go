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

package controller

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	kresource "k8s.io/apimachinery/pkg/api/resource"
)

var (
	// defaultBuilderCPULimit and defaultBuilderMemLimit are the BuildKit sidecar
	// limits EffectiveBuilderResources applies when the spec omits them.
	defaultBuilderCPULimit = kresource.MustParse("1")
	defaultBuilderMemLimit = kresource.MustParse("2Gi")
)

// EffectiveBuilderResources computes the effective BuildKit sidecar resources
// from the optional spec.translator.builderResources without mutating the CR.
//
// Only CPU and memory entries in requests and limits are accepted; resource
// claims, huge pages, extended resources, and every other key are rejected.
// Missing CPU and memory limits default independently to 1 CPU and 2Gi, while
// requests have no defaults and remain absent unless supplied. Every supplied
// quantity must be positive and each request must not exceed its corresponding
// effective limit.
func EffectiveBuilderResources(req *corev1.ResourceRequirements) (corev1.ResourceRequirements, error) {
	effective := corev1.ResourceRequirements{
		Limits:   corev1.ResourceList{},
		Requests: corev1.ResourceList{},
	}
	if req != nil {
		if len(req.Claims) > 0 {
			return effective, fmt.Errorf("builder resource claims are not supported")
		}
		for name, qty := range req.Limits {
			if !isCPUOrMemory(name) {
				return effective, fmt.Errorf("builder limit %q is not cpu or memory", name)
			}
			if qty.Sign() <= 0 {
				return effective, fmt.Errorf("builder limit %q must be positive", name)
			}
			effective.Limits[name] = qty
		}
		for name, qty := range req.Requests {
			if !isCPUOrMemory(name) {
				return effective, fmt.Errorf("builder request %q is not cpu or memory", name)
			}
			if qty.Sign() <= 0 {
				return effective, fmt.Errorf("builder request %q must be positive", name)
			}
			effective.Requests[name] = qty
		}
	}
	if _, ok := effective.Limits[corev1.ResourceCPU]; !ok {
		effective.Limits[corev1.ResourceCPU] = defaultBuilderCPULimit
	}
	if _, ok := effective.Limits[corev1.ResourceMemory]; !ok {
		effective.Limits[corev1.ResourceMemory] = defaultBuilderMemLimit
	}
	for name, reqQty := range effective.Requests {
		limitQty, ok := effective.Limits[name]
		if !ok {
			// Only cpu/mem requests are allowed and both limits are defaulted, so
			// this branch is unreachable for valid input.
			return effective, fmt.Errorf("builder request %q has no matching limit", name)
		}
		if reqQty.Cmp(limitQty) > 0 {
			return effective, fmt.Errorf("builder request %q exceeds its limit", name)
		}
	}
	return effective, nil
}

func isCPUOrMemory(name corev1.ResourceName) bool {
	return name == corev1.ResourceCPU || name == corev1.ResourceMemory
}
