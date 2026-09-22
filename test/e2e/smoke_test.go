//go:build e2e

// Package e2e is the CBSE full-stack smoke suite. It runs against a live
// cluster that smoke.sh has already provisioned (Experiment Operator, Scenario
// Manager, core DB, NATS/JetStream, EDS mock, and the alpha4 reference
// Translator) and verifies the end-to-end experiment lifecycle through
// Kubernetes resources and the three PostgreSQL databases (Core, Result, and
// Scenario Detail). The ordered specs cover owned-resource reconciliation,
// deterministic EDS intake, the full reference Translator build/run/publish
// chain, idempotent re-reconciliation, and garbage collection of owned
// resources and persisted rows.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The "full-stack smoke" container is the ordered Ginkgo root for the suite.
// It shares one controller-runtime client and the smoke.sh-provisioned
// namespace across all specs, which run in declaration order: owned-resource
// reconciliation, deterministic intake, the reference end-to-end chain,
// idempotent re-reconciliation, and finally cleanup.
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
		Expect(experimentalpha4.AddToScheme(scheme)).To(Succeed())
		k8sClient, err = client.New(config, client.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred())
	})

	// Verifies the experiment reaches InProgress and that the operator
	// reconciled the complete owned resource set: the detaildb, resultdb, and
	// translator Deployments carry owner references and the project label, and
	// their matching Services, DB credential Secrets, and translator ConfigMap
	// all exist.
	It("reaches InProgress with the complete owned resource set", func() {
		key := types.NamespacedName{Namespace: namespace, Name: project}
		Eventually(func(g Gomega) string {
			experiment := &experimentalpha4.SimulationExperiment{}
			g.Expect(k8sClient.Get(ctx, key, experiment)).To(Succeed())
			g.Expect(experiment.Status.Phase).NotTo(Equal("Error"), experiment.Status.Message)
			return experiment.Status.Phase
		}, 4*time.Minute, 2*time.Second).Should(Equal("InProgress"))

		for _, suffix := range []string{"detaildb", "resultdb", "translator"} {
			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: project + "-" + suffix}, deployment)).To(Succeed())
			Expect(deployment.OwnerReferences).NotTo(BeEmpty())
			Expect(deployment.Spec.Template.Labels).To(HaveKeyWithValue("experiment.cbse.terministic.de/project", project))
		}

		for _, suffix := range []string{"detaildb-svc", "resultdb-svc", "translator-svc"} {
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: project + "-" + suffix}, &corev1.Service{})).To(Succeed())
		}
		for _, suffix := range []string{"detaildb-sct", "resultdb-sct"} {
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: project + "-" + suffix}, &corev1.Secret{})).To(Succeed())
		}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: project + "-translator-cfg"}, &corev1.ConfigMap{})).To(Succeed())
	})

	// Verifies EDS intake persisted exactly one project and four deterministic
	// scenario_status rows in Created state, each carrying the parameterset_id
	// recipe_info the reference Translator's Detail DB lookup resolves. The full
	// Created -> PostProcessing chain is asserted by the reference end-to-end
	// spec below; this spec writes the persisted rows to the artifact directory
	// for triage.
	It("persists one project and four deterministic Created scenarios", func() {
		Eventually(func() string {
			return queryDatabase(fmt.Sprintf(
				"SELECT COUNT(*) FROM project WHERE project_name='%s'",
				project,
			))
		}, 2*time.Minute, 2*time.Second).Should(Equal("1"))

		if os.Getenv("CBSE_SELECTOR_ENABLED") == "1" {
			Eventually(func() string {
				return queryDatabase(fmt.Sprintf(
					"SELECT COUNT(*), COUNT(*) FILTER (WHERE ss.translation_attempts > 0 AND ss.container_image IS NOT NULL) FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s'",
					project,
				))
			}, 4*time.Minute, 2*time.Second).Should(Equal("4|4"))
		} else {
			// The alpha4 Scenario Manager always runs the selection loop (there
			// is no SELECTOR_ENABLED disable), so scenarios advance past
			// Created toward Scheduled/StartingRunners/InProcessing/
			// PostProcessing. This spec only verifies EDS intake persisted the
			// project and its four scenario_status rows with the
			// parameterset_id recipe_info the reference Translator's Detail DB
			// lookup expects; the full end-to-end chain (Created ->
			// PostProcessing + Result DB rows) is verified by the reference
			// end-to-end spec below.
			Eventually(func() string {
				return queryDatabase(fmt.Sprintf(
					"SELECT COUNT(*), COUNT(*) FILTER (WHERE ss.recipe_info ? 'parameterset_id') FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s'",
					project,
				))
			}, 3*time.Minute, 2*time.Second).Should(Equal("4|4"))
		}

		writeDatabaseArtifact(queryDatabase(fmt.Sprintf(
			"SELECT ss.id, ss.state, ss.priority, ss.number_of_reps, ss.recipe_info->>'parameterset_id' FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s' ORDER BY ss.id",
			project,
		)))
	})

	// Reference end-to-end smoke: the smoke path runs the real alpha4 reference
	// Translator (not a synthetic mock). With the alpha4 selection loop always
	// running (no SELECTOR_ENABLED disable), this spec drives one scenario
	// through the full chain: request consumption -> Detail DB parameter lookup
	// -> SimPy context generation from the digest-pinned runner base -> rootless
	// sidecar build -> authenticated registry push -> immutable digest
	// publication -> runner Job creation -> non-root model execution ->
	// PostgreSQL Result DB persistence -> scenario transition through
	// InProcessing to PostProcessing. This spec does NOT delete the experiment;
	// the cleanup spec owns deletion.
	It("drives one scenario through the full reference Translator chain to PostProcessing and persists results", func() {
		// 1. Wait for at least one scenario to reach PostProcessing. The full
		// chain (Detail DB lookup, rootless BuildKit build, authenticated
		// push, runner Job, SimPy run, Result DB insert) takes minutes.
		var doneScenarioID string
		var doneParametersetID string
		Eventually(func(g Gomega) bool {
			row := queryDatabase(fmt.Sprintf(
				"SELECT ss.id, ss.state, ss.recipe_info->>'parameterset_id' FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s' AND ss.state='PostProcessing' ORDER BY ss.id LIMIT 1",
				project,
			))
			if row == "" || strings.HasPrefix(row, "query-error") {
				return false
			}
			parts := strings.SplitN(row, "|", 3)
			if len(parts) != 3 || parts[0] == "" {
				return false
			}
			doneScenarioID = parts[0]
			doneParametersetID = parts[2]
			g.Expect(doneScenarioID).NotTo(BeEmpty())
			g.Expect(doneParametersetID).NotTo(BeEmpty())
			return true
		}, 8*time.Minute, 5*time.Second).Should(BeTrue(),
			"no scenario reached PostProcessing within 8 minutes; chain stalled (inspect translator/buildkit/SM logs)")

		scenarioID := doneScenarioID
		parametersetID := doneParametersetID

		// 2. Assert a simrun-* runner Job exists and completed for this
		// scenario (batchv1 client, listed by the reserved project label).
		Eventually(func(g Gomega) *batchv1.Job {
			jobs := &batchv1.JobList{}
			g.Expect(k8sClient.List(ctx, jobs,
				client.InNamespace(namespace),
				client.MatchingLabels{"experiment.cbse.terministic.de/project": project},
			)).To(Succeed())
			for i := range jobs.Items {
				j := &jobs.Items[i]
				if strings.HasPrefix(j.Name, "simrun-") {
					scenarioLabel := j.Labels["experiment.cbse.terministic.de/scenario-id"]
					if scenarioLabel == scenarioID {
						return j
					}
				}
			}
			return nil
		}, 2*time.Minute, 2*time.Second).ShouldNot(BeNil(),
			"no simrun-* Job found for scenario %s", scenarioID)

		var completedJob *batchv1.Job
		Eventually(func(g Gomega) bool {
			jobs := &batchv1.JobList{}
			g.Expect(k8sClient.List(ctx, jobs,
				client.InNamespace(namespace),
				client.MatchingLabels{
					"experiment.cbse.terministic.de/project":     project,
					"experiment.cbse.terministic.de/scenario-id": scenarioID,
				},
			)).To(Succeed())
			for i := range jobs.Items {
				j := &jobs.Items[i]
				if strings.HasPrefix(j.Name, "simrun-") {
					completedJob = j
					for _, cond := range j.Status.Conditions {
						if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
							return true
						}
					}
				}
			}
			return false
		}, 4*time.Minute, 2*time.Second).Should(BeTrue(),
			"simrun Job for scenario %s did not complete", scenarioID)

		// 3. Assert the completed Job was indexed (parallelism == completions
		// == number_of_reps) and ran the generated runner image.
		numberOfReps := 0
		Eventually(func(g Gomega) {
			// number_of_reps is a fixed, positive value set at intake.
			row := queryDatabase(fmt.Sprintf(
				"SELECT ss.number_of_reps, ss.number_of_computed_reps FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s' AND ss.id=%s",
				project, scenarioID,
			))
			g.Expect(row).NotTo(HavePrefix("query-error"))
			parts := strings.Split(row, "|")
			g.Expect(parts).To(HaveLen(2))
			nr, err := strconv.Atoi(parts[0])
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(nr).To(BeNumerically(">=", 2))
			numberOfReps = nr
		}, 2*time.Minute, 2*time.Second).Should(Succeed())
		Expect(completedJob.Spec.CompletionMode).NotTo(BeNil())
		Expect(*completedJob.Spec.CompletionMode).To(Equal(batchv1.IndexedCompletion))
		Expect(completedJob.Spec.Completions).NotTo(BeNil())
		Expect(int(*completedJob.Spec.Completions)).To(Equal(numberOfReps))
		Expect(completedJob.Spec.Parallelism).NotTo(BeNil())
		Expect(int(*completedJob.Spec.Parallelism)).To(Equal(numberOfReps))

		// The runner container image must be the digest-pinned generated
		// runner published to the configured runner repository (not a tag).
		Expect(completedJob.Spec.Template.Spec.Containers).NotTo(BeEmpty())
		runnerImage := completedJob.Spec.Template.Spec.Containers[0].Image
		Expect(runnerImage).To(HavePrefix("registry.unibw.de/i31bdase/cbse-test-runner@sha256:"),
			"runner image must be a digest reference in the generated-runner repository; got %q", runnerImage)
		Expect(strings.Contains(runnerImage, ":")).To(BeTrue())

		// 4. number_of_computed_reps == number_of_reps in Core DB.
		Eventually(func(g Gomega) string {
			return queryDatabase(fmt.Sprintf(
				"SELECT ss.number_of_computed_reps FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s' AND ss.id=%s",
				project, scenarioID,
			))
		}, 2*time.Minute, 2*time.Second).Should(Equal(strconv.Itoa(numberOfReps)),
			"number_of_computed_reps != number_of_reps (%d) for scenario %s", numberOfReps, scenarioID)

		// 5. Query the Detail DB for the fixed row the translator looked up,
		// so the result assertions below verify the exact lookup parameters.
		detailRow := queryDetailDatabase(fmt.Sprintf(
			"SELECT arrival_rate, service_rate, run_duration, seed_policy FROM public.simulation_parameters WHERE parameterset_id=%s",
			parametersetID,
		))
		Expect(detailRow).NotTo(HavePrefix("query-error"))
		Expect(detailRow).NotTo(BeEmpty())
		detailParts := strings.Split(detailRow, "|")
		Expect(detailParts).To(HaveLen(4))
		expectArrivalRate := detailParts[0]
		expectServiceRate := detailParts[1]
		expectRunDuration := detailParts[2]
		expectSeedPolicy := detailParts[3]

		// 6. Query scenario_<id>_results from the Result DB and assert exactly
		// number_of_reps rows in the no-failure path.
		resultTable := fmt.Sprintf("scenario_%s_results", scenarioID)
		rowCountRaw := queryResultDatabase(fmt.Sprintf("SELECT COUNT(*) FROM %s", resultTable))
		Expect(rowCountRaw).NotTo(HavePrefix("query-error"))
		Expect(rowCountRaw).NotTo(BeEmpty())
		Expect(rowCountRaw).To(Equal(strconv.Itoa(numberOfReps)),
			"%s row count != number_of_reps (%d); table: %s", resultTable, numberOfReps, resultTable)

		// Each row must carry parameterset_id, the four lookup parameters, the
		// derived effective_seed, and the result fields. effective_seed must
		// be distinct across the normal no-retry repetitions.
		resultRows := queryResultDatabase(fmt.Sprintf(
			"SELECT result->>'parameterset_id', result->>'arrival_rate', result->>'service_rate', result->>'run_duration', result->>'seed_policy', result->>'effective_seed', result->>'completed_customers', result->>'mean_wait_time' FROM %s ORDER BY id",
			resultTable,
		))
		Expect(resultRows).NotTo(HavePrefix("query-error"))
		Expect(resultRows).NotTo(BeEmpty())
		rows := strings.Split(resultRows, "\n")
		Expect(rows).To(HaveLen(numberOfReps))

		effectiveSeeds := map[string]struct{}{}
		for _, row := range rows {
			cols := strings.Split(row, "|")
			Expect(cols).To(HaveLen(8), "result row missing expected fields: %q", row)
			Expect(cols[0]).To(Equal(parametersetID), "parameterset_id mismatch: got %q want %q", cols[0], parametersetID)
			Expect(cols[1]).To(Equal(expectArrivalRate), "arrival_rate mismatch: got %q want %q", cols[1], expectArrivalRate)
			Expect(cols[2]).To(Equal(expectServiceRate), "service_rate mismatch: got %q want %q", cols[2], expectServiceRate)
			Expect(cols[3]).To(Equal(expectRunDuration), "run_duration mismatch: got %q want %q", cols[3], expectRunDuration)
			Expect(cols[4]).To(Equal(expectSeedPolicy), "seed_policy mismatch: got %q want %q", cols[4], expectSeedPolicy)
			Expect(cols[5]).NotTo(BeEmpty(), "effective_seed must be present: %q", row)
			Expect(cols[6]).NotTo(BeEmpty(), "completed_customers must be present: %q", row)
			Expect(cols[7]).NotTo(BeEmpty(), "mean_wait_time must be present: %q", row)
			Expect(effectiveSeeds).NotTo(HaveKey(cols[5]),
				"effective_seed %q is not distinct across repetitions", cols[5])
			effectiveSeeds[cols[5]] = struct{}{}
		}
		Expect(effectiveSeeds).To(HaveLen(numberOfReps),
			"expected %d distinct effective seeds, got %d", numberOfReps, len(effectiveSeeds))
	})

	// Verifies a metadata-only update (an annotation timestamp) is reconciled
	// idempotently: the operator does not create duplicate owned Deployments,
	// so the project-labeled Deployment count stays at three.
	It("reconciles an idempotent metadata update without duplicating children", func() {
		key := types.NamespacedName{Namespace: namespace, Name: project}
		experiment := &experimentalpha4.SimulationExperiment{}
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
			g.Expect(deployments.Items).To(HaveLen(3))
		}, 10*time.Second, time.Second).Should(Succeed())
	})

	// Verifies garbage collection: deleting the SimulationExperiment removes
	// the owned translator Deployment via owner-reference cascade and cascades
	// to the persisted database rows so the project and scenario_status tables
	// are empty. Skipped when CBSE_RETAIN_RESOURCES=1 leaves the run intact for
	// inspection.
	It("garbage-collects owned resources and cascades persisted state", func() {
		if os.Getenv("CBSE_RETAIN_RESOURCES") == "1" {
			Skip("retained E2E run requested; leaving SimulationExperiment, owned resources, and database rows intact")
		}
		key := types.NamespacedName{Namespace: namespace, Name: project}
		experiment := &experimentalpha4.SimulationExperiment{}
		Expect(k8sClient.Get(ctx, key, experiment)).To(Succeed())
		Expect(k8sClient.Delete(ctx, experiment)).To(Succeed())

		Eventually(func() bool {
			err := k8sClient.Get(ctx, key, &experimentalpha4.SimulationExperiment{})
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

// requiredEnv returns a required environment variable trimmed of surrounding
// whitespace, failing the spec at the caller's line when it is unset or empty.
// The smoke harness sets every CBSE_* variable before running the suite.
func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	ExpectWithOffset(1, value).NotTo(BeEmpty(), "%s must be set", name)
	return value
}

// queryDatabase executes a SQL query against the Core DB (deployment/core-db,
// database scenarios, user cbse_test) via kubectl exec + psql, returning the
// trimmed pipe-separated stdout. On any exec failure it returns a
// "query-error: ..." string rather than failing the spec, so Eventually callers
// can keep polling; assertions check for that prefix where relevant.
func queryDatabase(query string) string {
	kubectl := requiredEnv("KUBECTL")
	namespace := requiredEnv("CBSE_TEST_NAMESPACE")
	cmd := exec.Command(
		kubectl,
		"--kubeconfig", requiredEnv("KUBECONFIG"),
		"exec", "-n", namespace, "deployment/core-db", "--",
		"psql", "-U", "cbse_test", "-d", "scenarios", "-At", "-F", "|", "-c", query,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "query-error: " + strings.TrimSpace(stdout.String()+stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// queryResultDatabase executes a SQL query against the experiment's Result DB
// (the deployment/<project>-resultdb Deployment). It mirrors queryDatabase but
// targets the Result DB instead of the Core DB: the alpha4 smoke fixture
// configures the Result DB with dbname=result_db, user=smoke, password=smoke.
// PGPASSWORD is exported so psql authenticates without a TTY prompt. It is used
// by the reference end-to-end smoke to query scenario_<id>_results.
func queryResultDatabase(query string) string {
	kubectl := requiredEnv("KUBECTL")
	namespace := requiredEnv("CBSE_TEST_NAMESPACE")
	project := requiredEnv("CBSE_TEST_PROJECT")
	cmd := exec.Command(
		kubectl,
		"--kubeconfig", requiredEnv("KUBECONFIG"),
		"exec", "-n", namespace, "deployment/"+project+"-resultdb", "--",
		"sh", "-c", "PGPASSWORD=smoke psql -U smoke -d result_db -At -F '|' -c "+shQuote(query),
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "query-error: " + strings.TrimSpace(stdout.String()+stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// queryDetailDatabase executes a SQL query against the experiment's Scenario
// Detail DB (the repository-built deployment/<project>-detaildb Deployment that
// initializes public.simulation_parameters). It mirrors queryDatabase but
// targets the Detail DB: the alpha4 smoke fixture configures the Detail DB with
// dbname=simulation_db, user=smoke, password=smoke. It is used by the reference
// end-to-end smoke to look up the fixed parameter row identified by
// recipe_info.parameterset_id.
func queryDetailDatabase(query string) string {
	kubectl := requiredEnv("KUBECTL")
	namespace := requiredEnv("CBSE_TEST_NAMESPACE")
	project := requiredEnv("CBSE_TEST_PROJECT")
	cmd := exec.Command(
		kubectl,
		"--kubeconfig", requiredEnv("KUBECONFIG"),
		"exec", "-n", namespace, "deployment/"+project+"-detaildb", "--",
		"sh", "-c", "PGPASSWORD=smoke psql -U smoke -d simulation_db -At -F '|' -c "+shQuote(query),
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "query-error: " + strings.TrimSpace(stdout.String()+stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// shQuote single-quotes a string for safe interpolation into a sh -c command.
// It escapes embedded single quotes with the standard '\” sequence so the
// query passed to psql -c is preserved verbatim.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// writeDatabaseArtifact writes the given query result to database.txt under
// CBSE_ARTIFACT_DIR so a run's persisted scenario rows are captured alongside
// the JUnit artifacts. It is a no-op when CBSE_ARTIFACT_DIR is unset.
func writeDatabaseArtifact(contents string) {
	directory := strings.TrimSpace(os.Getenv("CBSE_ARTIFACT_DIR"))
	if directory == "" {
		return
	}
	Expect(os.MkdirAll(directory, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(directory, "database.txt"), []byte(contents+"\n"), 0o644)).To(Succeed())
}
