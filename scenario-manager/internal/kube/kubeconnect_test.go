package kube

import (
	"context"
	"os"
	"testing"
	"time"
)

const (
	testNamespaceEnv     = "SCENARIO_MANAGER_TEST_NAMESPACE"
	requireInClusterTest = "SCENARIO_MANAGER_REQUIRE_INCLUSTER_TEST"
)

func TestKubeConnectAndListSimulationExperiments(t *testing.T) {
	if !KubeConnect() {
		if os.Getenv(requireInClusterTest) != "" {
			t.Fatalf("KubeConnect failed while %s is set; ensure the test runs inside the cluster: runningInCluster=%v", requireInClusterTest, RunningInCluster())
		}
		t.Skipf("KubeConnect could not find an in-cluster configuration; skipping Kubernetes integration test (runningInCluster=%v)", RunningInCluster())
	}

	client := Client()
	if client == nil {
		t.Fatalf("Client returned nil after a successful KubeConnect call")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	namespace := os.Getenv(testNamespaceEnv)
	if _, err := ListSimulationExperiments(ctx, client, namespace); err != nil {
		t.Fatalf("ListSimulationExperiments failed: %v", err)
	}
}
