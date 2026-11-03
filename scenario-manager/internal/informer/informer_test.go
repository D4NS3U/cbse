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

package informer

import (
	"sort"
	"testing"

	"k8s.io/client-go/rest"
)

// TestNewInformerRejectsEmptyNamespace pins the v0.1 single-namespace
// contract: the informer has no cluster-wide fallback, so a config without a
// watch namespace is a construction error.
func TestNewInformerRejectsEmptyNamespace(t *testing.T) {
	if _, err := NewInformer(Config{RESTConfig: &rest.Config{}}, NewDispatcher(nil, nil, nil, nil)); err == nil {
		t.Fatal("NewInformer with an empty namespace must return an error")
	}
}

// TestNewInformerRejectsMissingRESTConfigAndDispatcher pins the remaining
// construction preconditions.
func TestNewInformerRejectsMissingRESTConfigAndDispatcher(t *testing.T) {
	if _, err := NewInformer(Config{Namespace: "cbse-system"}, NewDispatcher(nil, nil, nil, nil)); err == nil {
		t.Fatal("NewInformer without a REST config must return an error")
	}
	if _, err := NewInformer(Config{RESTConfig: &rest.Config{}, Namespace: "cbse-system"}, nil); err == nil {
		t.Fatal("NewInformer without a dispatcher must return an error")
	}
}

// TestInformerCacheOptionsScopedToNamespace proves, without a live cluster,
// that the controller-runtime cache options scope the SimulationExperiment
// informer to exactly the configured release namespace and nothing else.
func TestInformerCacheOptionsScopedToNamespace(t *testing.T) {
	for _, ns := range []string{"cbse-system", "a", "exp-ns-2"} {
		inf, err := NewInformer(
			Config{RESTConfig: &rest.Config{}, Namespace: ns},
			NewDispatcher(nil, nil, nil, nil),
		)
		if err != nil {
			t.Fatalf("NewInformer: %v", err)
		}
		opts, err := inf.cacheOptions()
		if err != nil {
			t.Fatalf("cacheOptions: %v", err)
		}
		watched := make([]string, 0, len(opts.DefaultNamespaces))
		for name := range opts.DefaultNamespaces {
			watched = append(watched, name)
		}
		sort.Strings(watched)
		if len(watched) != 1 || watched[0] != ns {
			t.Fatalf("cache watches namespaces %v, want exactly [%s]", watched, ns)
		}
		if opts.Scheme == nil {
			t.Fatalf("cache options scheme is nil for namespace %q", ns)
		}
	}
}
