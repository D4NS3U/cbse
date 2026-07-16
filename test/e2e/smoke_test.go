//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	experimentalpha3 "github.com/D4NS3U/cbse/experiment-operator/api/alpha3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("full-stack smoke", Ordered, func() {
	var (
		ctx       context.Context
		k8sClient client.Client
		namespace string
		project   string
	)

	BeforeAll(func() {
		ctx = context.Background()
		namespace = requiredEnv("CBSE_TEST_NAMESPACE")
		project = requiredEnv("CBSE_TEST_PROJECT")
		kubeconfig := requiredEnv("KUBECONFIG")

		config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		Expect(err).NotTo(HaveOccurred())
		scheme := runtime.NewScheme()
		Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
		Expect(experimentalpha3.AddToScheme(scheme)).To(Succeed())
		k8sClient, err = client.New(config, client.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred())
	})

	It("reaches InProgress with the complete owned resource set", func() {
		key := types.NamespacedName{Namespace: namespace, Name: project}
		Eventually(func(g Gomega) string {
			experiment := &experimentalpha3.SimulationExperiment{}
			g.Expect(k8sClient.Get(ctx, key, experiment)).To(Succeed())
			g.Expect(experiment.Status.Phase).NotTo(Equal("Error"), experiment.Status.Message)
			return experiment.Status.Phase
		}, 4*time.Minute, 2*time.Second).Should(Equal("InProgress"))

		for _, suffix := range []string{"detaildb", "resultdb", "translator", "postproc"} {
			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: project + "-" + suffix}, deployment)).To(Succeed())
			Expect(deployment.OwnerReferences).NotTo(BeEmpty())
			Expect(deployment.Spec.Template.Labels).To(HaveKeyWithValue("experiment.cbse.terministic.de/project", project))
		}

		design := &corev1.Pod{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: project + "-design"}, design)).To(Succeed())
		Expect(design.OwnerReferences).NotTo(BeEmpty())
		Expect(design.Labels).To(HaveKeyWithValue("experiment.cbse.terministic.de/project", project))

		for _, suffix := range []string{"detaildb-svc", "resultdb-svc", "translator-svc", "postproc-svc", "design-svc"} {
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: project + "-" + suffix}, &corev1.Service{})).To(Succeed())
		}
		for _, suffix := range []string{"detaildb-sct", "resultdb-sct"} {
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: project + "-" + suffix}, &corev1.Secret{})).To(Succeed())
		}
		for _, suffix := range []string{"translator-cfg", "design"} {
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: project + "-" + suffix}, &corev1.ConfigMap{})).To(Succeed())
		}
	})

	It("persists one project and four deterministic Created scenarios", func() {
		Eventually(func() string {
			return queryDatabase(fmt.Sprintf(
				"SELECT COUNT(*), MIN(number_of_components), MIN(status) FROM project WHERE project_name='%s'",
				project,
			))
		}, 2*time.Minute, 2*time.Second).Should(Equal("1|5|InProgress"))

		Eventually(func() string {
			return queryDatabase(fmt.Sprintf(
				"SELECT COUNT(*), COUNT(*) FILTER (WHERE ss.state='Created') FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s'",
				project,
			))
		}, 3*time.Minute, 2*time.Second).Should(Equal("4|4"))

		writeDatabaseArtifact(queryDatabase(fmt.Sprintf(
			"SELECT ss.id, ss.state, ss.priority, ss.number_of_reps, ss.recipe_info->>'seed' FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s' ORDER BY ss.id",
			project,
		)))
	})

	It("reconciles an idempotent metadata update without duplicating children", func() {
		key := types.NamespacedName{Namespace: namespace, Name: project}
		experiment := &experimentalpha3.SimulationExperiment{}
		Expect(k8sClient.Get(ctx, key, experiment)).To(Succeed())
		if experiment.Annotations == nil {
			experiment.Annotations = map[string]string{}
		}
		experiment.Annotations["cbse.terministic.de/idempotence-check"] = time.Now().UTC().Format(time.RFC3339Nano)
		Expect(k8sClient.Update(ctx, experiment)).To(Succeed())

		Consistently(func(g Gomega) {
			deployments := &appsv1.DeploymentList{}
			g.Expect(k8sClient.List(ctx, deployments, client.InNamespace(namespace), client.MatchingLabels{
				"experiment.cbse.terministic.de/project": project,
			})).To(Succeed())
			g.Expect(deployments.Items).To(HaveLen(4))
		}, 10*time.Second, time.Second).Should(Succeed())
	})

	It("garbage-collects owned resources and cascades persisted state", func() {
		key := types.NamespacedName{Namespace: namespace, Name: project}
		experiment := &experimentalpha3.SimulationExperiment{}
		Expect(k8sClient.Get(ctx, key, experiment)).To(Succeed())
		Expect(k8sClient.Delete(ctx, experiment)).To(Succeed())

		Eventually(func() bool {
			err := k8sClient.Get(ctx, key, &experimentalpha3.SimulationExperiment{})
			return apierrors.IsNotFound(err)
		}, 90*time.Second, 2*time.Second).Should(BeTrue())

		Eventually(func() bool {
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: project + "-translator"}, &appsv1.Deployment{})
			return apierrors.IsNotFound(err)
		}, 90*time.Second, 2*time.Second).Should(BeTrue())

		Eventually(func() string {
			return queryDatabase(fmt.Sprintf("SELECT COUNT(*) FROM project WHERE project_name='%s'", project))
		}, 90*time.Second, 2*time.Second).Should(Equal("0"))
		Expect(queryDatabase("SELECT COUNT(*) FROM scenario_status")).To(Equal("0"))
	})
})

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	ExpectWithOffset(1, value).NotTo(BeEmpty(), "%s must be set", name)
	return value
}

func queryDatabase(query string) string {
	kubectl := requiredEnv("KUBECTL")
	namespace := requiredEnv("CBSE_TEST_NAMESPACE")
	cmd := exec.Command(
		kubectl,
		"--kubeconfig", requiredEnv("KUBECONFIG"),
		"exec", "-n", namespace, "deployment/core-db", "--",
		"psql", "-U", "cbse_test", "-d", "scenarios", "-At", "-F", "|", "-c", query,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "query-error: " + strings.TrimSpace(string(output))
	}
	return strings.TrimSpace(string(output))
}

func writeDatabaseArtifact(contents string) {
	directory := strings.TrimSpace(os.Getenv("CBSE_ARTIFACT_DIR"))
	if directory == "" {
		return
	}
	Expect(os.MkdirAll(directory, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(directory, "database.txt"), []byte(contents+"\n"), 0o644)).To(Succeed())
}
