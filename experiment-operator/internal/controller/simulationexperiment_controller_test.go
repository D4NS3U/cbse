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

package controller

import (
	"context"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	experimentalpha2 "github.com/D4NS3U/cbse/experiment-operator/api/alpha2"
	experimentalpha3 "github.com/D4NS3U/cbse/experiment-operator/api/alpha3"
)

var _ = Describe("SimulationExperiment Controllers", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("injects the project label and downward-api env var into alpha3-managed workloads", func() {
		resourceName := "alpha3-env"
		key := types.NamespacedName{Name: resourceName, Namespace: "default"}
		resource := &experimentalpha3.SimulationExperiment{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"},
			Spec:       testSimulationExperimentSpec(),
		}
		Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		DeferCleanup(deleteObject, ctx, resource)

		reconciler := &SimulationExperimentReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}

		reconcileAlpha3ToProvisioning(ctx, reconciler, key)

		for _, deploymentName := range []string{
			resourceName + "-detaildb",
			resourceName + "-resultdb",
			resourceName + "-translator",
			resourceName + "-postproc",
		} {
			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: "default"}, deployment)).To(Succeed())
			Expect(deployment.Spec.Template.Labels).To(HaveKeyWithValue(simulationProjectLabelKey, resourceName))
			Expect(deployment.Spec.Template.Spec.Containers).NotTo(BeEmpty())
			Expect(deployment.Spec.Template.Spec.Containers[0].Env).To(ContainElement(MatchFields(IgnoreExtras, Fields{
				"Name": Equal(simulationProjectNameEnvVar),
				"ValueFrom": PointTo(MatchFields(IgnoreExtras, Fields{
					"FieldRef": PointTo(MatchFields(IgnoreExtras, Fields{
						"FieldPath": Equal("metadata.labels['" + simulationProjectLabelKey + "']"),
					})),
				})),
			})))
		}

		pod := &corev1.Pod{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: resourceName + "-design", Namespace: "default"}, pod)).To(Succeed())
		Expect(pod.Labels).To(HaveKeyWithValue(simulationProjectLabelKey, resourceName))
		Expect(pod.Spec.Containers).To(HaveLen(1))
		Expect(pod.Spec.Containers[0].Env).To(ContainElement(MatchFields(IgnoreExtras, Fields{
			"Name": Equal(simulationProjectNameEnvVar),
			"ValueFrom": PointTo(MatchFields(IgnoreExtras, Fields{
				"FieldRef": PointTo(MatchFields(IgnoreExtras, Fields{
					"FieldPath": Equal("metadata.labels['" + simulationProjectLabelKey + "']"),
				})),
			})),
		})))
	})

	It("skips database workload creation for host-based alpha3 databases", func() {
		listener := startTCPListener()
		DeferCleanup(func() { _ = listener.Close() })
		port := int32(listener.Addr().(*net.TCPAddr).Port)

		resourceName := "alpha3-hostdb"
		key := types.NamespacedName{Name: resourceName, Namespace: "default"}
		spec := testSimulationExperimentSpec()
		spec.DetailDatabase = hostDatabaseSpec(port)
		spec.ResultDatabase = hostDatabaseSpec(port)

		resource := &experimentalpha3.SimulationExperiment{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"},
			Spec:       spec,
		}
		Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		DeferCleanup(deleteObject, ctx, resource)

		reconciler := &SimulationExperimentReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		reconcileAlpha3ToProvisioning(ctx, reconciler, key)

		for _, name := range []string{
			resourceName + "-detaildb",
			resourceName + "-resultdb",
		} {
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &appsv1.Deployment{})).To(Satisfy(apierrors.IsNotFound))
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name + "-svc", Namespace: "default"}, &corev1.Service{})).To(Satisfy(apierrors.IsNotFound))
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name + "-sct", Namespace: "default"}, &corev1.Secret{})).To(Satisfy(apierrors.IsNotFound))
		}
	})

	It("marks alpha2 resources as unsupported without creating child resources", func() {
		resourceName := "alpha2-legacy"
		key := types.NamespacedName{Name: resourceName, Namespace: "default"}
		resource := &experimentalpha2.SimulationExperiment{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"},
			Spec:       legacySimulationExperimentSpec(),
		}
		Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		DeferCleanup(deleteObject, ctx, resource)

		reconciler := &LegacySimulationExperimentReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		stored := &experimentalpha2.SimulationExperiment{}
		Expect(k8sClient.Get(ctx, key, stored)).To(Succeed())
		Expect(stored.Status.Phase).To(Equal("Error"))
		Expect(stored.Status.Message).To(Equal(legacyAlpha2UnsupportedMessage))

		for _, obj := range []clientCheck{
			{key: types.NamespacedName{Name: resourceName + "-detaildb", Namespace: "default"}, object: &appsv1.Deployment{}},
			{key: types.NamespacedName{Name: resourceName + "-resultdb", Namespace: "default"}, object: &appsv1.Deployment{}},
			{key: types.NamespacedName{Name: resourceName + "-translator", Namespace: "default"}, object: &appsv1.Deployment{}},
			{key: types.NamespacedName{Name: resourceName + "-postproc", Namespace: "default"}, object: &appsv1.Deployment{}},
			{key: types.NamespacedName{Name: resourceName + "-design", Namespace: "default"}, object: &corev1.Pod{}},
			{key: types.NamespacedName{Name: resourceName + "-detaildb-svc", Namespace: "default"}, object: &corev1.Service{}},
			{key: types.NamespacedName{Name: resourceName + "-resultdb-svc", Namespace: "default"}, object: &corev1.Service{}},
			{key: types.NamespacedName{Name: resourceName + "-translator-svc", Namespace: "default"}, object: &corev1.Service{}},
			{key: types.NamespacedName{Name: resourceName + "-postproc-svc", Namespace: "default"}, object: &corev1.Service{}},
			{key: types.NamespacedName{Name: resourceName + "-design-svc", Namespace: "default"}, object: &corev1.Service{}},
			{key: types.NamespacedName{Name: resourceName + "-detaildb-sct", Namespace: "default"}, object: &corev1.Secret{}},
			{key: types.NamespacedName{Name: resourceName + "-resultdb-sct", Namespace: "default"}, object: &corev1.Secret{}},
			{key: types.NamespacedName{Name: resourceName + "-translator-cfg", Namespace: "default"}, object: &corev1.ConfigMap{}},
			{key: types.NamespacedName{Name: resourceName + "-design", Namespace: "default"}, object: &corev1.ConfigMap{}},
		} {
			Expect(k8sClient.Get(ctx, obj.key, obj.object)).To(Satisfy(apierrors.IsNotFound))
		}
	})
})

type clientCheck struct {
	key    types.NamespacedName
	object client.Object
}

func reconcileAlpha3ToProvisioning(ctx context.Context, reconciler *SimulationExperimentReconciler, key types.NamespacedName) {
	for i := 0; i < 3; i++ {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
	}
}

func testSimulationExperimentSpec() experimentalpha3.SimulationExperimentSpec {
	return experimentalpha3.SimulationExperimentSpec{
		DefaultServiceType: experimentalpha3.ServiceTypeClusterIP,
		DetailDatabase: experimentalpha3.DatabaseSpec{
			Image:       "busybox:latest",
			Command:     []string{"tail"},
			Args:        []string{"-f", "/dev/null"},
			DBName:      "simulation_db",
			User:        "postgres",
			Password:    "postgres",
			Port:        5432,
			ServiceType: experimentalpha3.ServiceTypeClusterIP,
		},
		ResultDatabase: experimentalpha3.DatabaseSpec{
			Image:       "busybox:latest",
			Command:     []string{"tail"},
			Args:        []string{"-f", "/dev/null"},
			DBName:      "result_db",
			User:        "postgres",
			Password:    "postgres",
			Port:        5432,
			ServiceType: experimentalpha3.ServiceTypeClusterIP,
		},
		Translator: experimentalpha3.TranslatorSpec{
			Image:       "busybox:latest",
			Command:     []string{"tail"},
			Args:        []string{"-f", "/dev/null"},
			Repository:  "translator-repo",
			BaseImage:   "translator-baseimage",
			ServiceType: experimentalpha3.ServiceTypeClusterIP,
			Port:        8080,
		},
		PostProcessingService: experimentalpha3.PostProcessingSpec{
			Image:       "busybox:latest",
			Command:     []string{"tail"},
			Args:        []string{"-f", "/dev/null"},
			ServiceType: experimentalpha3.ServiceTypeClusterIP,
			Port:        8081,
		},
		ExperimentalDesignService: experimentalpha3.ExperimentalDesignServiceSpec{
			Design:      "test-design",
			Image:       "busybox:latest",
			Command:     []string{"tail"},
			Args:        []string{"-f", "/dev/null"},
			ServiceType: experimentalpha3.ServiceTypeClusterIP,
			Port:        8082,
		},
	}
}

func legacySimulationExperimentSpec() experimentalpha2.SimulationExperimentSpec {
	return experimentalpha2.SimulationExperimentSpec{
		DefaultServiceType: experimentalpha2.ServiceTypeClusterIP,
		DetailDatabase: experimentalpha2.DatabaseSpec{
			Image:       "busybox:latest",
			Command:     []string{"tail"},
			Args:        []string{"-f", "/dev/null"},
			DBName:      "simulation_db",
			User:        "postgres",
			Password:    "postgres",
			Port:        5432,
			ServiceType: experimentalpha2.ServiceTypeClusterIP,
		},
		ResultDatabase: experimentalpha2.DatabaseSpec{
			Image:       "busybox:latest",
			Command:     []string{"tail"},
			Args:        []string{"-f", "/dev/null"},
			DBName:      "result_db",
			User:        "postgres",
			Password:    "postgres",
			Port:        5432,
			ServiceType: experimentalpha2.ServiceTypeClusterIP,
		},
		Translator: experimentalpha2.TranslatorSpec{
			Image:       "busybox:latest",
			Command:     []string{"tail"},
			Args:        []string{"-f", "/dev/null"},
			Repository:  "translator-repo",
			BaseImage:   "translator-baseimage",
			ServiceType: experimentalpha2.ServiceTypeClusterIP,
			Port:        8080,
		},
		PostProcessingService: experimentalpha2.PostProcessingSpec{
			Image:       "busybox:latest",
			Command:     []string{"tail"},
			Args:        []string{"-f", "/dev/null"},
			ServiceType: experimentalpha2.ServiceTypeClusterIP,
			Port:        8081,
		},
		ExperimentalDesignService: experimentalpha2.ExperimentalDesignServiceSpec{
			Design:      "legacy-design",
			Image:       "busybox:latest",
			Command:     []string{"tail"},
			Args:        []string{"-f", "/dev/null"},
			ServiceType: experimentalpha2.ServiceTypeClusterIP,
			Port:        8082,
		},
	}
}

func hostDatabaseSpec(port int32) experimentalpha3.DatabaseSpec {
	return experimentalpha3.DatabaseSpec{
		Host:     "127.0.0.1",
		DBName:   "simulation_db",
		User:     "postgres",
		Password: "postgres",
		Port:     port,
	}
}

func startTCPListener() net.Listener {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	return listener
}

func deleteObject(ctx context.Context, object client.Object) {
	Expect(k8sClient.Delete(ctx, object)).To(Succeed())
}
