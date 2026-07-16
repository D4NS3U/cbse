package kube

import (
	"testing"
)

func TestKubeConnectReturnsErrorOutsideCluster(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	k8sClient = nil
	runningInCluster = false
	t.Cleanup(func() {
		k8sClient = nil
		runningInCluster = false
	})

	if err := KubeConnect(); err == nil {
		t.Fatal("KubeConnect returned nil outside a cluster")
	}
	if RunningInCluster() {
		t.Fatal("RunningInCluster returned true after in-cluster configuration failed")
	}
}
