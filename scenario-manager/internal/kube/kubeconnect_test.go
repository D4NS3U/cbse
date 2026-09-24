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

//go:build integration

package kube

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

const (
	testNamespaceEnv     = "SCENARIO_MANAGER_TEST_NAMESPACE"
	requireInClusterTest = "SCENARIO_MANAGER_REQUIRE_INCLUSTER_TEST"
)

func TestKubeConnectAndListSimulationExperiments(t *testing.T) {
	if err := KubeConnect(); err != nil {
		if os.Getenv(requireInClusterTest) != "" {
			t.Fatalf("KubeConnect failed while %s is set; ensure the test runs inside the cluster: runningInCluster=%v: %v", requireInClusterTest, RunningInCluster(), err)
		}
		t.Skipf("KubeConnect could not find an in-cluster configuration; skipping Kubernetes integration test (runningInCluster=%v): %v", RunningInCluster(), err)
	}

	t.Log("Starting Kubernetes integration test...")

	client := Client()

	var errs []string
	if client == nil {
		errs = append(errs, "Client returned nil after a successful KubeConnect call")
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		namespace := os.Getenv(testNamespaceEnv)
		if _, err := ListSimulationExperiments(ctx, client, namespace); err != nil {
			errs = append(errs, fmt.Sprintf("ListSimulationExperiments failed: %v", err))
		}
	}

	if len(errs) > 0 {
		for _, err := range errs {
			t.Logf("Kubernetes integration error: %s", err)
		}
		t.Fatalf("Kubernetes integration test failed with %d error(s)", len(errs))
	}

	t.Log("Kubernetes integration test succeeded.")
}
