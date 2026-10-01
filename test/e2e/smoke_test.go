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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	It("drives one scenario through the full reference Translator chain to Finished and persists results", func() {
		// 1. Wait for the met-path scenario to reach the terminal Finished
		// state. The chain runs into the real PPS evaluation and the met
		// verdict lands the scenario in Finished; the full chain (Detail DB
		// lookup, rootless BuildKit build, authenticated push, runner Job,
		// SimPy run, Result DB insert) takes minutes.
		var doneScenarioID string
		var doneParametersetID string
		Eventually(func(g Gomega) bool {
			row := queryDatabase(fmt.Sprintf(
				"SELECT ss.id, ss.state, ss.recipe_info->>'parameterset_id' FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s' AND ss.state='Finished' AND ss.priority = 1 ORDER BY ss.id LIMIT 1",
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

	// Convergence spec for the live smoke profile (ruling Q6: determinism
	// through fleet-size variation, never ε manipulation). The EDS batch is
	// two met-path scenarios (priority 1, number_of_reps 40) that are met on
	// the first wave, and two loop-path scenarios (priority 2,
	// number_of_reps 1) that are deterministically not-met via the
	// degenerate n<2 rule and each top themselves up naturally under the
	// real statistical policy. This spec pins the per-family bookkeeping of
	// that loop plus the round-Job identities: the met path stays
	// single-wave (runner_round 1, round_reps == number_of_reps == computed
	// == 40, byte-identical single-round Job name); the loop path shows the
	// natural top-up (runner_round >= 2 — never pinned equal, computed > 1)
	// with the final wave bounded by the experiment's -max-runners-per-round
	// 30 (rulings Q4/Q7: the per-round safety clamp made observable; 0
	// disables pacing). Each round is one runner Job, so exactly two Jobs
	// carry the runner-round "2" label — one per loop scenario, each with
	// the -r2 name suffix — and no met-path Job carries a round label above
	// "1". Wave counts are the estimator's output and stay unpinned beyond
	// the structural invariants.
	It("converges all four scenarios: met-path single-wave bookkeeping and loop-path natural top-up", func() {
		// 1. Convergence gate: all four scenarios reach the terminal
		// Finished state. The chain spec above already proved one met-path
		// Finished; in the observed runs the loop-path scenarios converge
		// within minutes of the met-path scenarios.
		Eventually(func() string {
			return queryDatabase(fmt.Sprintf(
				"SELECT COUNT(*) FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s' AND ss.state='Finished'",
				project,
			))
		}, 8*time.Minute, 5*time.Second).Should(Equal("4"),
			"not all four scenarios reached Finished within 8 minutes; loop stalled (inspect SM/PPS logs and the runner Jobs)")

		// 2a. Met-path bookkeeping (both priority=1 rows): single wave.
		// Round 1's round_reps equals the intake number_of_reps (40), the
		// computed total equals it, and the verdict was published at least
		// once; >= tolerates the at-least-once redelivery the settlement
		// runs observed.
		var metScenarioIDs []string
		Eventually(func(g Gomega) bool {
			rows := strings.Split(queryDatabase(fmt.Sprintf(
				"SELECT ss.id, ss.runner_round, ss.round_reps, ss.number_of_reps, ss.number_of_computed_reps, ss.evaluation_attempts FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s' AND ss.priority = 1 AND ss.state='Finished' ORDER BY ss.id",
				project,
			)), "\n")
			g.Expect(len(rows)).To(Equal(2), "expected both priority-1 met-path rows, got %d", len(rows))
			metScenarioIDs = metScenarioIDs[:0]
			for _, row := range rows {
				parts := strings.Split(row, "|")
				g.Expect(parts).To(HaveLen(6), "malformed met-path row %q", row)
				runnerRound, err := strconv.Atoi(parts[1])
				g.Expect(err).NotTo(HaveOccurred(), row)
				roundReps, err := strconv.Atoi(parts[2])
				g.Expect(err).NotTo(HaveOccurred(), row)
				numberOfReps, err := strconv.Atoi(parts[3])
				g.Expect(err).NotTo(HaveOccurred(), row)
				computedReps, err := strconv.Atoi(parts[4])
				g.Expect(err).NotTo(HaveOccurred(), row)
				evaluationAttempts, err := strconv.Atoi(parts[5])
				g.Expect(err).NotTo(HaveOccurred(), row)
				metScenarioIDs = append(metScenarioIDs, parts[0])
				g.Expect(runnerRound).To(Equal(1),
					"met-path scenario %s: runner_round %d, want single-wave 1", parts[0], runnerRound)
				g.Expect(numberOfReps).To(Equal(40),
					"met-path scenario %s: number_of_reps %d, want the smoke batch's 40", parts[0], numberOfReps)
				g.Expect(roundReps).To(Equal(40),
					"met-path scenario %s: round_reps %d, want 40 (round 1 = number_of_reps)", parts[0], roundReps)
				g.Expect(computedReps).To(Equal(40),
					"met-path scenario %s: number_of_computed_reps %d, want 40", parts[0], computedReps)
				g.Expect(evaluationAttempts).To(BeNumerically(">=", 1),
					"met-path scenario %s: evaluation_attempts %d, want >= 1", parts[0], evaluationAttempts)
			}
			return true
		}, 2*time.Minute, 2*time.Second).Should(BeTrue(),
			"met-path single-wave bookkeeping did not settle within 2 minutes")

		// 2b. Loop-path bookkeeping (both priority=2 rows): natural top-up.
		// runner_round is >= 2 and never pinned equal — observed runs
		// converged at round 3, but wave counts vary with the estimator.
		// The final wave's round_reps must respect the experiment's
		// -max-runners-per-round 30 cap (rulings Q4/Q7). Each round
		// publishes one verdict, so evaluation_attempts >= runner_round
		// (>= tolerates the at-least-once redelivery the settlement runs
		// observed).
		var loopScenarioIDs []string
		Eventually(func(g Gomega) bool {
			rows := strings.Split(queryDatabase(fmt.Sprintf(
				"SELECT ss.id, ss.runner_round, ss.round_reps, ss.number_of_reps, ss.number_of_computed_reps, ss.evaluation_attempts FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s' AND ss.priority = 2 AND ss.state='Finished' ORDER BY ss.id",
				project,
			)), "\n")
			g.Expect(len(rows)).To(Equal(2), "expected both priority-2 loop-path rows, got %d", len(rows))
			loopScenarioIDs = loopScenarioIDs[:0]
			for _, row := range rows {
				parts := strings.Split(row, "|")
				g.Expect(parts).To(HaveLen(6), "malformed loop-path row %q", row)
				runnerRound, err := strconv.Atoi(parts[1])
				g.Expect(err).NotTo(HaveOccurred(), row)
				roundReps, err := strconv.Atoi(parts[2])
				g.Expect(err).NotTo(HaveOccurred(), row)
				numberOfReps, err := strconv.Atoi(parts[3])
				g.Expect(err).NotTo(HaveOccurred(), row)
				computedReps, err := strconv.Atoi(parts[4])
				g.Expect(err).NotTo(HaveOccurred(), row)
				evaluationAttempts, err := strconv.Atoi(parts[5])
				g.Expect(err).NotTo(HaveOccurred(), row)
				loopScenarioIDs = append(loopScenarioIDs, parts[0])
				g.Expect(runnerRound).To(BeNumerically(">=", 2),
					"loop-path scenario %s: runner_round %d, want >= 2 (natural top-up)", parts[0], runnerRound)
				g.Expect(numberOfReps).To(Equal(1),
					"loop-path scenario %s: number_of_reps %d, want the smoke batch's 1", parts[0], numberOfReps)
				g.Expect(computedReps).To(BeNumerically(">", 1),
					"loop-path scenario %s: number_of_computed_reps %d, want > 1", parts[0], computedReps)
				g.Expect(roundReps).To(BeNumerically("<=", 30),
					"loop-path scenario %s: round_reps %d exceeds the -max-runners-per-round 30 wave cap", parts[0], roundReps)
				g.Expect(evaluationAttempts).To(BeNumerically(">=", runnerRound),
					"loop-path scenario %s: evaluation_attempts %d < runner_round %d", parts[0], evaluationAttempts, runnerRound)
			}
			return true
		}, 2*time.Minute, 2*time.Second).Should(BeTrue(),
			"loop-path natural top-up bookkeeping did not settle within 2 minutes")

		// Echo the full four-row dump for diagnostics.
		dump := queryDatabase(fmt.Sprintf(
			"SELECT ss.id, ss.state, ss.priority, ss.number_of_reps, ss.runner_round, ss.round_reps, ss.number_of_computed_reps, ss.evaluation_attempts, ss.confidence_metric FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s' ORDER BY ss.id",
			project,
		))
		Expect(dump).NotTo(HavePrefix("query-error"))
		Expect(strings.Count(dump, "\n")+1).To(Equal(4), "expected the full four-row scenario dump, got %q", dump)
		writeDatabaseArtifact(dump)

		// 3. Round-Job identity (the chain spec's Job-listing pattern): each
		// round is exactly one runner Job. Exactly two Jobs carry the
		// runner-round "2" label — one per loop scenario — each named with
		// the -r2 suffix; no met-path Job carries a round label above "1".
		var allJobs batchv1.JobList
		Eventually(func(g Gomega) bool {
			g.Expect(k8sClient.List(ctx, &allJobs,
				client.InNamespace(namespace),
				client.MatchingLabels{"experiment.cbse.terministic.de/project": project},
			)).To(Succeed())
			roundTwoScenarioIDs := map[string]bool{}
			roundTwo := 0
			for i := range allJobs.Items {
				j := &allJobs.Items[i]
				if j.Labels["experiment.cbse.terministic.de/runner-round"] != "2" {
					continue
				}
				roundTwo++
				g.Expect(strings.Contains(j.Name, "-r2")).To(BeTrue(),
					"round-2 Job %s name lacks the -r2 suffix", j.Name)
				g.Expect(loopScenarioIDs).To(ContainElement(j.Labels["experiment.cbse.terministic.de/scenario-id"]),
					"round-2 Job %s belongs to scenario %q, not a loop-path scenario",
					j.Name, j.Labels["experiment.cbse.terministic.de/scenario-id"])
				roundTwoScenarioIDs[j.Labels["experiment.cbse.terministic.de/scenario-id"]] = true
			}
			g.Expect(roundTwo).To(Equal(2),
				"expected exactly two runner-round-2 Jobs (one per loop scenario), got %d", roundTwo)
			g.Expect(len(roundTwoScenarioIDs)).To(Equal(2),
				"the two round-2 Jobs must belong to two distinct loop scenarios")
			return true
		}, 2*time.Minute, 2*time.Second).Should(BeTrue(),
			"exactly two round-2 runner Jobs (one per loop scenario, -r2 names) did not appear within 2 minutes")

		// Round-3 Jobs may or may not exist depending on where the estimator
		// converged; their presence is intentionally unpinned.
		roundThree := 0
		for i := range allJobs.Items {
			if allJobs.Items[i].Labels["experiment.cbse.terministic.de/runner-round"] == "3" {
				roundThree++
			}
		}
		Expect(roundThree).To(BeNumerically(">=", 0),
			"round-3 Job count is convergence-dependent and deliberately unpinned")

		// No met-path scenario's Job may carry a round label above "1":
		// every priority-1 Job keeps runner-round "1" and the
		// byte-identical single-round name (no -r<round> suffix).
		for _, sid := range metScenarioIDs {
			for i := range allJobs.Items {
				j := &allJobs.Items[i]
				if j.Labels["experiment.cbse.terministic.de/scenario-id"] != sid {
					continue
				}
				Expect(j.Labels["experiment.cbse.terministic.de/runner-round"]).To(Equal("1"),
					"met-path scenario %s Job %s carries runner-round %q; the met path is single-wave",
					sid, j.Name, j.Labels["experiment.cbse.terministic.de/runner-round"])
				Expect(strings.Contains(j.Name, "-r")).To(BeFalse(),
					"met-path scenario %s Job name %q must keep the byte-identical single-round format (no -r suffix)",
					sid, j.Name)
			}
		}
	})

	// Proves the live Finished chain end-to-end (D10): after the convergence
	// spec above has driven all four scenarios to the terminal Finished
	// state, the Scenario Manager's aggregation pass reports
	// status.scenarioManagerVerdict = "Finished", and the Experiment Operator
	// derives the absorbing terminal phase from that report. The spec
	// observes the verdict at or before the phase (the report precedes the
	// derivation), proves stickiness under a follow-up reconcile triggered
	// the same way as the idempotent-metadata spec (a metadata-only
	// annotation update plus a bounded Consistently window), and persists
	// the terminal experiment status to the artifact directory for triage.
	It("derives the experiment's terminal phase from the scenario-aggregate verdict", func() {
		key := types.NamespacedName{Namespace: namespace, Name: project}

		// 1. The verdict and the phase. The aggregation pass runs at its
		// 5-second cadence, so the verdict lands at most one tick after the
		// scenarios converge, and the operator's watch reacts to the status
		// patch immediately - a ~2-minute bound is generous for the live
		// chain. Both values are observed in this single poll loop: the poll
		// on which the verdict first reads "Finished" is recorded, likewise
		// for the phase, so the report-precedes-derivation ordering is
		// asserted from what the spec actually observed, not just from the
		// phase value.
		var poll int
		var verdictSeenAtPoll int
		var phaseSeenAtPoll int
		Eventually(func(g Gomega) bool {
			poll++
			experiment := &experimentalpha4.SimulationExperiment{}
			g.Expect(k8sClient.Get(ctx, key, experiment)).To(Succeed())
			if verdictSeenAtPoll == 0 && experiment.Status.ScenarioManagerVerdict == "Finished" {
				verdictSeenAtPoll = poll
			}
			if phaseSeenAtPoll == 0 && experiment.Status.Phase == "Finished" {
				phaseSeenAtPoll = poll
			}
			return verdictSeenAtPoll != 0 && phaseSeenAtPoll != 0
		}, 2*time.Minute, 2*time.Second).Should(BeTrue(),
			"the scenario-aggregate verdict and the derived terminal phase did not both read Finished within 2 minutes")

		Expect(verdictSeenAtPoll).To(BeNumerically("<=", phaseSeenAtPoll),
			"the scenarioManagerVerdict report (first observed at poll %d) must appear at or before the derived phase (poll %d): the report precedes the derivation",
			verdictSeenAtPoll, phaseSeenAtPoll)

		// 2. Stickiness under a follow-up reconcile. A metadata-only
		// annotation update (the idempotent-metadata spec's reconcile
		// trigger) forces a fresh reconcile after the terminal phase is
		// reached; the absorbing verdict (the aggregation pass no-ops once
		// written) and the parked terminal case (the reconcile parks without
		// re-deriving) guarantee neither value can regress. The window spans
		// several of the aggregation pass's 5-second ticks.
		experiment := &experimentalpha4.SimulationExperiment{}
		Expect(k8sClient.Get(ctx, key, experiment)).To(Succeed())
		if experiment.Annotations == nil {
			experiment.Annotations = map[string]string{}
		}
		experiment.Annotations["cbse.terministic.de/phase-stickiness-probe"] = time.Now().UTC().Format(time.RFC3339Nano)
		Expect(k8sClient.Update(ctx, experiment)).To(Succeed())

		Consistently(func(g Gomega) {
			terminal := &experimentalpha4.SimulationExperiment{}
			g.Expect(k8sClient.Get(ctx, key, terminal)).To(Succeed())
			g.Expect(terminal.Status.ScenarioManagerVerdict).To(Equal("Finished"),
				"the absorbing scenarioManagerVerdict regressed from Finished")
			g.Expect(terminal.Status.Phase).To(Equal("Finished"),
				"the terminal phase regressed from Finished under a follow-up reconcile")
		}, 30*time.Second, time.Second).Should(Succeed())

		// 3. Diagnostics artifact: persist the terminal experiment status
		// (phase, message, verdict) under CBSE_ARTIFACT_DIR so the live
		// Finished chain's outcome is captured alongside the run's JUnit
		// artifacts, following the suite's writeDatabaseArtifact discipline.
		terminal := &experimentalpha4.SimulationExperiment{}
		Expect(k8sClient.Get(ctx, key, terminal)).To(Succeed())
		writeExperimentStatusArtifact("experiment-terminal-status.txt", fmt.Sprintf(
			"phase=%s\nmessage=%s\nscenarioManagerVerdict=%s\n",
			terminal.Status.Phase,
			terminal.Status.Message,
			terminal.Status.ScenarioManagerVerdict,
		))
	})

	// Verifies a metadata-only update (an annotation timestamp) is reconciled
	// idempotently: the operator does not create duplicate owned Deployments,
	// so the project-labeled Deployment count stays at four.
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
			g.Expect(deployments.Items).To(HaveLen(4))
		}, 10*time.Second, time.Second).Should(Succeed())
	})

	// Proves the pre-creation validation gate live (FEATURE.md D1, Error
	// flavor 1): the builder deep-copies the live green experiment's CR and
	// applies exactly one red delta - translator.image as a tag-form
	// reference without a digest - so validateExperiment rejects the
	// experiment before any component is created. The spec asserts the Error
	// phase with the digest-form message, zero owned children (the full
	// owned-suffix set enumerated the way the InProgress spec does),
	// stickiness across an annotation-triggered reconcile (the parked Error
	// phase never re-transitions and no child appears), and the CR's own
	// removal on user deletion. The green experiment is read by the builder
	// and never touched.
	It("lands a tag-form translator.image in Error before any component exists", func() {
		redName := project + "-errval"
		redKey := types.NamespacedName{Namespace: namespace, Name: redName}

		// Builder: deep-copy the live green experiment, rename, apply the
		// single red delta (a tag without a digest on translator.image),
		// create.
		red := buildRedExperiment(ctx, k8sClient, types.NamespacedName{Namespace: namespace, Name: project}, redName)
		red.Spec.Translator.Image = errValImage
		Expect(k8sClient.Create(ctx, red)).To(Succeed())

		// 1. The pre-creation gate: the phase is Error and the message names
		// the image validation (the digest-form error). Phase and message
		// land in one status patch, so both are read from the same observed
		// object.
		var phase, message string
		Eventually(func(g Gomega) bool {
			experiment := &experimentalpha4.SimulationExperiment{}
			g.Expect(k8sClient.Get(ctx, redKey, experiment)).To(Succeed())
			if experiment.Status.Phase != "Error" {
				return false
			}
			phase = experiment.Status.Phase
			message = experiment.Status.Message
			return true
		}, 2*time.Minute, 2*time.Second).Should(BeTrue(),
			"the Validation-Error experiment did not reach Error within 2 minutes")

		Expect(message).To(ContainSubstring("translator.image"),
			"the Error message must name the translator.image validation; got %q", message)
		Expect(message).To(ContainSubstring(errValImage),
			"the Error message must name the offending reference; got %q", message)
		Expect(message).To(ContainSubstring("must be an OCI digest reference in the exact form name@sha256:<64 lowercase hex>"),
			"the Error message must carry the digest-form rejection; got %q", message)

		// 2. Zero children: the validation failed before any component was
		// created. Enumerate the owned-suffix set the way the InProgress
		// spec does (per kind, per suffix) and assert every owned name -
		// including the PPS Deployment and Service and the deterministic
		// runner ServiceAccount - is absent.
		states := observeOwnedChildren(ctx, k8sClient, namespace, redName, red.UID)
		for _, state := range states {
			Expect(state.state).To(Equal("absent"),
				"owned %s %s must not exist: the validation gate failed before any component was created", state.kind, state.name)
		}

		// 3. Stickiness: an annotation-triggered reconcile (the same trigger
		// as the idempotent-metadata spec) parks on the Error phase - the
		// operator never re-transitions a parked phase - and no child
		// appears.
		Expect(k8sClient.Get(ctx, redKey, red)).To(Succeed())
		if red.Annotations == nil {
			red.Annotations = map[string]string{}
		}
		red.Annotations["cbse.terministic.de/error-validation-stickiness"] = time.Now().UTC().Format(time.RFC3339Nano)
		Expect(k8sClient.Update(ctx, red)).To(Succeed())

		Consistently(func(g Gomega) {
			experiment := &experimentalpha4.SimulationExperiment{}
			g.Expect(k8sClient.Get(ctx, redKey, experiment)).To(Succeed())
			g.Expect(experiment.Status.Phase).To(Equal("Error"),
				"the Error phase regressed under a follow-up reconcile")
			for _, state := range observeOwnedChildren(ctx, k8sClient, namespace, redName, red.UID) {
				g.Expect(state.state).To(Equal("absent"),
					"owned %s %s appeared under a follow-up reconcile", state.kind, state.name)
			}
		}, 30*time.Second, time.Second).Should(Succeed())

		// 4. Diagnostics artifact: persist the terminal evidence (phase,
		// message, the children inventory) under CBSE_ARTIFACT_DIR per the
		// suite's triage discipline.
		writeExperimentStatusArtifact("experiment-error-validation-status.txt", fmt.Sprintf(
			"experiment=%s\nphase=%s\nmessage=%s\nowned children:\n%s\n",
			redName, phase, message, ownedChildrenText(states),
		))

		// 5. Cleanup: the user deletes the red experiment and the CR goes
		// away (there are no children to cascade - the gate failed before
		// creation).
		Expect(k8sClient.Delete(ctx, red)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, redKey, &experimentalpha4.SimulationExperiment{})
			return apierrors.IsNotFound(err)
		}, 90*time.Second, 2*time.Second).Should(BeTrue(),
			"the deleted Validation-Error experiment did not disappear within 90 seconds")
	})

	// Proves the mid-sequence provisioning failure live (FEATURE.md D1,
	// Error flavor 2, creation variant): a spec-created blocker Service
	// first holds a constant NodePort, then the builder deep-copies the live
	// green experiment's CR with exactly one red delta -
	// postProcessingService serviceType NodePort requesting that same port -
	// so the API server hard-rejects the PPS Service creation mid-sequence
	// ("provided port is already allocated") and reconcilePPS fails. The
	// spec asserts the Error phase with the rejection named, the partial
	// children persisting exactly per provisionComponents' order (detaildb
	// -> resultdb -> translator -> PPS: everything before the failing PPS
	// Service, report-only semantics with no auto-teardown), stickiness
	// across a follow-up reconcile, the GC cascade over the partial set on
	// user deletion, and the blocker fixture's own teardown. The green
	// experiment is read by the builder and never touched.
	It("lands a PPS NodePort conflict in Error with the partial child set persisting", func() {
		redName := project + "-errprov"
		redKey := types.NamespacedName{Namespace: namespace, Name: redName}

		// Fixture: the blocker Service holds the constant NodePort before the
		// red experiment requests it, so the conflict pre-exists the
		// provisioning attempt (FEATURE.md design pattern 2).
		blocker := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      redName + "-blocker",
				Namespace: namespace,
			},
			Spec: corev1.ServiceSpec{
				Type:     corev1.ServiceTypeNodePort,
				Selector: map[string]string{"app": redName + "-blocker"},
				Ports: []corev1.ServicePort{{
					Name:     "blocker",
					Protocol: corev1.ProtocolTCP,
					Port:     8080,
					NodePort: errProvBlockerNodePort,
				}},
			},
		}
		Expect(k8sClient.Create(ctx, blocker)).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: blocker.Name}, blocker)).To(Succeed())
		Expect(blocker.Spec.Ports).To(HaveLen(1))
		Expect(blocker.Spec.Ports[0].NodePort).To(Equal(errProvBlockerNodePort),
			"the blocker Service must hold the constant NodePort %d", errProvBlockerNodePort)

		// Builder: deep-copy the live green experiment, rename, apply the
		// single red delta (the PPS requests the blocker's NodePort), create.
		red := buildRedExperiment(ctx, k8sClient, types.NamespacedName{Namespace: namespace, Name: project}, redName)
		nodePort := errProvBlockerNodePort
		red.Spec.PostProcessingService.ServiceType = experimentalpha4.ServiceTypeNodePort
		red.Spec.PostProcessingService.NodePort = &nodePort
		Expect(k8sClient.Create(ctx, red)).To(Succeed())

		// The expected partial state: provisionComponents reconciles
		// detaildb -> resultdb -> translator -> PPS -> runner
		// ServiceAccount, and reconcilePPS creates the PPS Deployment before
		// its Service - so everything before the failing PPS Service exists
		// and everything after it was never created.
		wantState := func(state ownedChildState) string {
			if state.name == redName+"-pps-svc" || state.kind == "ServiceAccount" {
				return "absent"
			}
			return "present"
		}

		// 1. The mid-sequence failure: the API server rejects the PPS
		// Service creation and reconcilePPS fails, so the phase is Error
		// with the rejection named.
		var phase, message string
		Eventually(func(g Gomega) bool {
			experiment := &experimentalpha4.SimulationExperiment{}
			g.Expect(k8sClient.Get(ctx, redKey, experiment)).To(Succeed())
			if experiment.Status.Phase != "Error" {
				return false
			}
			phase = experiment.Status.Phase
			message = experiment.Status.Message
			return true
		}, 2*time.Minute, 2*time.Second).Should(BeTrue(),
			"the Provisioning-Error experiment did not reach Error within 2 minutes")

		Expect(message).To(ContainSubstring("reconcile PPS Service"),
			"the Error message must name the failing step; got %q", message)
		Expect(message).To(ContainSubstring("provided port is already allocated"),
			"the Error message must carry the API server's NodePort rejection; got %q", message)

		// 2. The partial child set persists (report-only semantics: no
		// auto-teardown): the two databases' Deployments, Services, and
		// Secrets, the translator ConfigMap, Deployment, and Service, and
		// the PPS Deployment all exist; the PPS Service and the runner
		// ServiceAccount were never created.
		states := observeOwnedChildren(ctx, k8sClient, namespace, redName, red.UID)
		for _, state := range states {
			Expect(state.state).To(Equal(wantState(state)),
				"owned %s %s is %s, want %s: the partial set is everything before the failing PPS Service",
				state.kind, state.name, state.state, wantState(state))
		}

		// 3. Stickiness: an annotation-triggered reconcile (the same trigger
		// as the idempotent-metadata spec) parks on the Error phase and
		// leaves the partial set untouched.
		Expect(k8sClient.Get(ctx, redKey, red)).To(Succeed())
		if red.Annotations == nil {
			red.Annotations = map[string]string{}
		}
		red.Annotations["cbse.terministic.de/error-provisioning-stickiness"] = time.Now().UTC().Format(time.RFC3339Nano)
		Expect(k8sClient.Update(ctx, red)).To(Succeed())

		Consistently(func(g Gomega) {
			experiment := &experimentalpha4.SimulationExperiment{}
			g.Expect(k8sClient.Get(ctx, redKey, experiment)).To(Succeed())
			g.Expect(experiment.Status.Phase).To(Equal("Error"),
				"the Error phase regressed under a follow-up reconcile")
			for _, state := range observeOwnedChildren(ctx, k8sClient, namespace, redName, red.UID) {
				g.Expect(state.state).To(Equal(wantState(state)),
					"owned %s %s changed to %s across a follow-up reconcile; the partial set must stay untouched",
					state.kind, state.name, state.state)
			}
		}, 30*time.Second, time.Second).Should(Succeed())

		// 4. Diagnostics artifact: persist the terminal evidence (phase,
		// message, the partial-children inventory) under CBSE_ARTIFACT_DIR
		// per the suite's triage discipline.
		writeExperimentStatusArtifact("experiment-error-provisioning-status.txt", fmt.Sprintf(
			"experiment=%s\nphase=%s\nmessage=%s\nowned children:\n%s\n",
			redName, phase, message, ownedChildrenText(states),
		))

		// 5. The GC cascade over the partial set: the user deletes the red
		// experiment and every owned child - the partial set included -
		// disappears via the owner-reference cascade.
		Expect(k8sClient.Delete(ctx, red)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, redKey, &experimentalpha4.SimulationExperiment{})
			if !apierrors.IsNotFound(err) {
				return false
			}
			for _, state := range observeOwnedChildren(ctx, k8sClient, namespace, redName, red.UID) {
				if state.state != "absent" {
					return false
				}
			}
			return true
		}, 90*time.Second, 2*time.Second).Should(BeTrue(),
			"the GC cascade did not remove the red experiment and its partial child set within 90 seconds")

		// 6. Teardown the blocker Service: the spec's own fixture, never an
		// owned child - the cascade above must not have touched it.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: blocker.Name}, blocker)).To(Succeed())
		Expect(k8sClient.Delete(ctx, blocker)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: blocker.Name}, &corev1.Service{})
			return apierrors.IsNotFound(err)
		}, 90*time.Second, 2*time.Second).Should(BeTrue(),
			"the blocker Service fixture did not disappear within 90 seconds")
	})

	// Proves the readiness watchdog live (FEATURE.md D1, flavor 3; ruling R):
	// the builder deep-copies the live green experiment's CR with exactly one
	// red delta - translator.image as a well-formed digest reference that
	// does not exist (a syntactically valid name@sha256:<64 hex> pointing at
	// an unreachable registry) - so the validation gate passes, provisioning
	// completes (the full child set exists), the translator image pull fails
	// observably, and the bounded-retry watchdog collects the per-retry
	// not-ready inventory across three counted retries spaced ~60s before
	// transitioning the experiment to Error with the aggregated per-retry
	// message. The spec asserts the Error phase with the aggregated message
	// (naming the translator and its observed failure, each retry labeled),
	// the full-but-not-ready child set (the databases healthy and probed
	// ready on the green images), stickiness across a follow-up reconcile,
	// the GC cascade over the complete owned set, and terminal-evidence
	// persistence. The green experiment is read by the builder and never
	// touched.
	It("drives an image-pull failure through the readiness watchdog to an aggregated Error", func() {
		redName := project + "-errready"
		redKey := types.NamespacedName{Namespace: namespace, Name: redName}

		// Builder: deep-copy the live green experiment, rename, apply the
		// single red delta (a well-formed digest that does not exist),
		// create.
		red := buildRedExperiment(ctx, k8sClient, types.NamespacedName{Namespace: namespace, Name: project}, redName)
		red.Spec.Translator.Image = errReadyImage
		Expect(k8sClient.Create(ctx, red)).To(Succeed())

		// 1. The watchdog budget: the initial not-ready evaluation starts
		// the watchdog, the three counted retries are spaced ~60s apart
		// (production defaults), and the third still-not-ready counted retry
		// transitions to Error - ~3 minutes after the first not-ready
		// evaluation, plus pod-scheduling and pull-failure latency, so a
		// 6-minute bound is generous for the live chain.
		var phase, message string
		Eventually(func(g Gomega) bool {
			experiment := &experimentalpha4.SimulationExperiment{}
			g.Expect(k8sClient.Get(ctx, redKey, experiment)).To(Succeed())
			if experiment.Status.Phase != "Error" {
				return false
			}
			phase = experiment.Status.Phase
			message = experiment.Status.Message
			return true
		}, 6*time.Minute, 5*time.Second).Should(BeTrue(),
			"the readiness-watchdog experiment did not reach Error within 6 minutes (watchdog: 3 counted retries at ~60s spacing)")

		// The final message aggregates the per-retry inventories: each
		// counted retry is labeled, and the not-ready translator is named
		// with its observed failure at each retry.
		Expect(message).To(ContainSubstring("translator"),
			"the Error message must name the translator component; got %q", message)
		Expect(message).To(ContainSubstring("retry 1:"),
			"the Error message must label the first counted retry; got %q", message)
		Expect(message).To(ContainSubstring("retry 2:"),
			"the Error message must label the second counted retry; got %q", message)
		Expect(message).To(ContainSubstring("retry 3:"),
			"the Error message must label the third counted retry; got %q", message)

		// 2. The full-but-not-ready child set: provisioning completed
		// (every owned child exists - the databases, the translator, the
		// PPS, and the runner ServiceAccount), the databases (the green
		// images) are healthy and probed ready, and the translator is stuck
		// not-ready on the failing image pull.
		states := observeOwnedChildren(ctx, k8sClient, namespace, redName, red.UID)
		for _, state := range states {
			Expect(state.state).To(Equal("present"),
				"owned %s %s must exist: provisioning completed before the readiness failure", state.kind, state.name)
		}
		for _, suffix := range []string{"detaildb", "resultdb"} {
			Eventually(func() int32 {
				deployment := &appsv1.Deployment{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: redName + "-" + suffix}, deployment)).To(Succeed())
				return deployment.Status.ReadyReplicas
			}, 2*time.Minute, 5*time.Second).Should(BeNumerically(">=", 1),
				"the %s Deployment (the green image) must be ready: the databases are healthy and probed", suffix)
		}
		translatorDep := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: redName + "-translator"}, translatorDep)).To(Succeed())
		Expect(translatorDep.Status.ReadyReplicas).To(BeNumerically("<", 1),
			"the translator Deployment must stay not-ready: its image pull fails on the nonexistent digest")

		// 3. Stickiness: an annotation-triggered reconcile (the same
		// trigger as the idempotent-metadata spec) parks on the Error phase
		// and leaves the full child set untouched.
		Expect(k8sClient.Get(ctx, redKey, red)).To(Succeed())
		if red.Annotations == nil {
			red.Annotations = map[string]string{}
		}
		red.Annotations["cbse.terministic.de/error-readiness-stickiness"] = time.Now().UTC().Format(time.RFC3339Nano)
		Expect(k8sClient.Update(ctx, red)).To(Succeed())

		Consistently(func(g Gomega) {
			experiment := &experimentalpha4.SimulationExperiment{}
			g.Expect(k8sClient.Get(ctx, redKey, experiment)).To(Succeed())
			g.Expect(experiment.Status.Phase).To(Equal("Error"),
				"the Error phase regressed under a follow-up reconcile")
			for _, state := range observeOwnedChildren(ctx, k8sClient, namespace, redName, red.UID) {
				g.Expect(state.state).To(Equal("present"),
					"owned %s %s disappeared across a follow-up reconcile; the full set must stay untouched", state.kind, state.name)
			}
		}, 30*time.Second, time.Second).Should(Succeed())

		// 4. Diagnostics artifact: persist the terminal evidence (phase,
		// message, the full-children inventory) under CBSE_ARTIFACT_DIR per
		// the suite's triage discipline.
		writeExperimentStatusArtifact("experiment-error-readiness-status.txt", fmt.Sprintf(
			"experiment=%s\nphase=%s\nmessage=%s\nowned children:\n%s\n",
			redName, phase, message, ownedChildrenText(states),
		))

		// 5. The GC cascade over the complete owned set: the user deletes
		// the red experiment and every owned child disappears via the
		// owner-reference cascade.
		Expect(k8sClient.Delete(ctx, red)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, redKey, &experimentalpha4.SimulationExperiment{})
			if !apierrors.IsNotFound(err) {
				return false
			}
			for _, state := range observeOwnedChildren(ctx, k8sClient, namespace, redName, red.UID) {
				if state.state != "absent" {
					return false
				}
			}
			return true
		}, 90*time.Second, 2*time.Second).Should(BeTrue(),
			"the GC cascade did not remove the red experiment and its complete child set within 90 seconds")
	})

	// Proves the live Failed chain end-to-end (FEATURE.md D4, ruling F - the
	// single observed-failure route; the trigger is zero component changes):
	// the builder deep-copies the live green experiment's CR with exactly two
	// deltas - the name and a runner jobTemplate carrying
	// activeDeadlineSeconds 1 (the JobTemplate policy's allow-listed Job-level
	// field, validated > 0; the SM's effective-Job builder passes it through
	// to every created runner Job). The one-shot eds-mock (the green CR's
	// experimentalDesignService.image, launched with the installation's env
	// contract, PROJECT_NAME = the red project, and EDS_KEEP_ALIVE disabled
	// so it publishes the standard batch and exits) serves the red batch;
	// the mock's own bounded retry covers the window before the red
	// experiment reaches InProgress. Every runner Job is killed one second
	// after it becomes active, so the Job's own Failed condition
	// (DeadlineExceeded) is the observed failure the state machine consumes:
	// ObservationFailed -> the guarded InProcessing -> Failed -> the
	// scenario-aggregate verdict Failed (fail-fast: any scenario Failed) ->
	// the operator's terminal-phase derivation with the D7 PhaseTransition
	// Event. The spec asserts the verdict at or before the phase (the report
	// precedes the derivation - the Finished-chain spec's ordering idiom),
	// the D7 PhaseTransition Event, stickiness under an annotation-triggered
	// reconcile, the GC cascade over the complete owned set including the
	// runner Jobs on user deletion, and terminal-evidence persistence (the
	// runner Job's DeadlineExceeded condition as triage). The green
	// experiment is read by the builder and never touched.
	It("drives a deadline-exceeded runner Job through the live Failed chain", func() {
		redName := project + "-fcrash"
		redKey := types.NamespacedName{Namespace: namespace, Name: redName}
		mockPodName := redName + "-eds-mock"
		mockPodKey := types.NamespacedName{Namespace: namespace, Name: mockPodName}

		// Builder: deep-copy the live green experiment, rename, apply the
		// single red delta - a runner jobTemplate with activeDeadlineSeconds
		// 1. The JobTemplate policy (validator) admits exactly the Job-level
		// ActiveDeadlineSeconds plus the Pod template, and the Pod template
		// requires exactly one regular container named runner with no image
		// (CBSE supplies the runner image: the SM's effective-Job builder
		// replaces it with the accepted Translator digest), so the minimal
		// valid jobTemplate is the name-only runner container plus the
		// one-second deadline.
		red := buildRedExperiment(ctx, k8sClient, types.NamespacedName{Namespace: namespace, Name: project}, redName)
		deadlineSeconds := int64(1)
		red.Spec.Runner.JobTemplate = &batchv1.JobTemplateSpec{
			Spec: batchv1.JobSpec{
				ActiveDeadlineSeconds: &deadlineSeconds,
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "runner"}},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, red)).To(Succeed())

		// Batch: launch the unchanged eds-mock one-shot for the red project
		// with the installation's env contract (test/e2e/manifests/base/
		// stack.yaml): the stack's NATS_URL, the red project's canonical
		// availability subject (cbse.<ns>.<project>.eds.scenarios.available),
		// PROJECT_NAME = the red project, and the green profile's two-batch
		// shape - with EDS_KEEP_ALIVE disabled so the mock publishes the
		// standard batch and exits instead of the installation's keep-alive
		// loop. The mock's own bounded retry (availability requests every
		// ~2s) covers the window before the red experiment reaches
		// InProgress.
		launchEdsMockOneShot(red.Spec.ExperimentalDesignService.Image, namespace, redName)

		// 1. The readiness gate passes (the standard image-form profile):
		// the experiment reaches InProgress, so its scenarios are admitted
		// and the chain below is the standard run.
		Eventually(func(g Gomega) string {
			experiment := &experimentalpha4.SimulationExperiment{}
			g.Expect(k8sClient.Get(ctx, redKey, experiment)).To(Succeed())
			return experiment.Status.Phase
		}, 4*time.Minute, 2*time.Second).Should(Equal("InProgress"),
			"the Failed-chain experiment did not reach InProgress within 4 minutes")

		// 2. The one-shot mock fixture published both standard batches and
		// exited: the red project's batch intake is in.
		Eventually(func(g Gomega) corev1.PodPhase {
			pod := &corev1.Pod{}
			g.Expect(k8sClient.Get(ctx, mockPodKey, pod)).To(Succeed())
			return pod.Status.Phase
		}, 5*time.Minute, 2*time.Second).Should(Equal(corev1.PodSucceeded),
			"the eds-mock one-shot did not publish the standard batch and exit within 5 minutes")

		// 3. The chain. Every runner Job is killed one second after it
		// becomes active (the jobTemplate's activeDeadlineSeconds 1), so the
		// Job's own Failed condition (DeadlineExceeded) is the observed
		// failure the state machine consumes: ObservationFailed -> the
		// guarded InProcessing -> Failed -> the scenario-aggregate verdict
		// Failed (fail-fast: any scenario Failed) -> the operator derives the
		// absorbing terminal phase. The bound covers provisioning (~1 min) +
		// the batch + the Translator build/push + the deadline kill + the
		// observation and aggregation ticks; 10 minutes is generous. Both
		// values are observed in this single poll loop (the Finished-chain
		// spec's ordering idiom): the poll on which the verdict first reads
		// "Failed" is recorded, likewise for the phase, so the
		// report-precedes-derivation ordering is asserted from what the spec
		// actually observed.
		var poll int
		var verdictSeenAtPoll int
		var phaseSeenAtPoll int
		Eventually(func(g Gomega) bool {
			poll++
			experiment := &experimentalpha4.SimulationExperiment{}
			g.Expect(k8sClient.Get(ctx, redKey, experiment)).To(Succeed())
			if verdictSeenAtPoll == 0 && experiment.Status.ScenarioManagerVerdict == "Failed" {
				verdictSeenAtPoll = poll
			}
			if phaseSeenAtPoll == 0 && experiment.Status.Phase == "Failed" {
				phaseSeenAtPoll = poll
			}
			return verdictSeenAtPoll != 0 && phaseSeenAtPoll != 0
		}, 10*time.Minute, 5*time.Second).Should(BeTrue(),
			"the scenario-aggregate verdict and the derived terminal phase did not both read Failed within 10 minutes (inspect the runner Jobs, the SM/PPS logs, and the mock Pod)")

		Expect(verdictSeenAtPoll).To(BeNumerically("<=", phaseSeenAtPoll),
			"the scenarioManagerVerdict report (first observed at poll %d) must appear at or before the derived phase (poll %d): the report precedes the derivation",
			verdictSeenAtPoll, phaseSeenAtPoll)

		// The derivation's message names the report.
		terminal := &experimentalpha4.SimulationExperiment{}
		Expect(k8sClient.Get(ctx, redKey, terminal)).To(Succeed())
		Expect(terminal.Status.Message).To(Equal("A scenario failed (scenarioManagerVerdict report)"),
			"the Failed phase message must carry the operator's derivation text; got %q", terminal.Status.Message)

		// 4. The D7 PhaseTransition Event: a Normal Event on the red
		// experiment with reason PhaseTransition and the Failed transition
		// named in the message.
		Eventually(func(g Gomega) bool {
			events := &corev1.EventList{}
			g.Expect(k8sClient.List(ctx, events, client.InNamespace(namespace))).To(Succeed())
			for i := range events.Items {
				event := &events.Items[i]
				if event.InvolvedObject.Name != redName {
					continue
				}
				if event.Reason == "PhaseTransition" && strings.Contains(event.Message, "Failed") {
					return true
				}
			}
			return false
		}, 2*time.Minute, 2*time.Second).Should(BeTrue(),
			"no PhaseTransition Event naming the Failed transition was found on the red experiment within 2 minutes")

		// 5. Stickiness under a follow-up reconcile. A metadata-only
		// annotation update (the idempotent-metadata spec's reconcile
		// trigger) forces a fresh reconcile after the terminal phase is
		// reached; the absorbing verdict (the aggregation pass no-ops once
		// written) and the parked terminal case (the reconcile parks without
		// re-deriving) guarantee neither value can regress. The window spans
		// several of the aggregation pass's 5-second ticks.
		fresh := &experimentalpha4.SimulationExperiment{}
		Expect(k8sClient.Get(ctx, redKey, fresh)).To(Succeed())
		if fresh.Annotations == nil {
			fresh.Annotations = map[string]string{}
		}
		fresh.Annotations["cbse.terministic.de/failed-stickiness"] = time.Now().UTC().Format(time.RFC3339Nano)
		Expect(k8sClient.Update(ctx, fresh)).To(Succeed())

		Consistently(func(g Gomega) {
			probe := &experimentalpha4.SimulationExperiment{}
			g.Expect(k8sClient.Get(ctx, redKey, probe)).To(Succeed())
			g.Expect(probe.Status.ScenarioManagerVerdict).To(Equal("Failed"),
				"the absorbing scenarioManagerVerdict regressed from Failed")
			g.Expect(probe.Status.Phase).To(Equal("Failed"),
				"the terminal phase regressed from Failed under a follow-up reconcile")
		}, 30*time.Second, time.Second).Should(Succeed())

		// 6. The complete owned set is present before deletion (the
		// image-form profile owns everything: the two databases'
		// Deployments/Services/Secrets, the translator ConfigMap/Deployment/
		// Service, the PPS Deployment/Service, and the deterministic runner
		// ServiceAccount) - so the GC cascade below has the full set to
		// cascade over.
		states := observeOwnedChildren(ctx, k8sClient, namespace, redName, red.UID)
		for _, state := range states {
			Expect(state.state).To(Equal("present"),
				"owned %s %s must exist before deletion: the full owned set is provisioned", state.kind, state.name)
		}

		// 7. The observed failure on the wire: at least one simrun-* runner
		// Job of the red project carries the JobFailed condition - the
		// DeadlineExceeded failure the state machine consumed. Captured as
		// triage evidence.
		var jobFailure string
		Eventually(func(g Gomega) bool {
			jobs := &batchv1.JobList{}
			g.Expect(k8sClient.List(ctx, jobs,
				client.InNamespace(namespace),
				client.MatchingLabels{"experiment.cbse.terministic.de/project": redName},
			)).To(Succeed())
			for i := range jobs.Items {
				job := &jobs.Items[i]
				if !strings.HasPrefix(job.Name, "simrun-") {
					continue
				}
				for _, cond := range job.Status.Conditions {
					if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
						jobFailure = fmt.Sprintf("job=%s reason=%s message=%s", job.Name, cond.Reason, cond.Message)
						return true
					}
				}
			}
			return false
		}, 2*time.Minute, 2*time.Second).Should(BeTrue(),
			"no simrun-* runner Job of the red project carried the JobFailed condition within 2 minutes")

		// 8. Diagnostics artifact: persist the terminal evidence (phase,
		// message, verdict, the scenario states via the suite's Core DB
		// query helper, and the runner Job's failure condition) under
		// CBSE_ARTIFACT_DIR per the suite's triage discipline.
		var scenarioRows string
		Eventually(func(g Gomega) string {
			scenarioRows = queryDatabase(fmt.Sprintf(
				"SELECT ss.id, ss.state, ss.priority, ss.number_of_reps, ss.runner_round, ss.number_of_computed_reps FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s' ORDER BY ss.id",
				redName,
			))
			g.Expect(scenarioRows).NotTo(HavePrefix("query-error"))
			return scenarioRows
		}, 2*time.Minute, 2*time.Second).ShouldNot(BeEmpty(),
			"the red project's scenario rows did not appear in the Core DB within 2 minutes")
		writeExperimentStatusArtifact("experiment-failed-status.txt", fmt.Sprintf(
			"experiment=%s\nphase=%s\nmessage=%s\nscenarioManagerVerdict=%s\nscenario states:\n%s\nrunner job failure:\n%s\n",
			redName, terminal.Status.Phase, terminal.Status.Message, terminal.Status.ScenarioManagerVerdict, scenarioRows, jobFailure,
		))

		// 9. The GC cascade over the complete owned set: the user deletes
		// the red experiment and every owned child - the databases' and
		// translator's and PPS's Deployments/Services/Secrets, the
		// translator ConfigMap, the runner ServiceAccount, and the runner
		// Jobs - disappears (owner-reference cascade plus the SM's
		// verified-Job cleanup), and the red project's persisted rows are
		// removed by the SM's deletion cleanup.
		Expect(k8sClient.Delete(ctx, red)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, redKey, &experimentalpha4.SimulationExperiment{})
			if !apierrors.IsNotFound(err) {
				return false
			}
			for _, state := range observeOwnedChildren(ctx, k8sClient, namespace, redName, red.UID) {
				if state.state != "absent" {
					return false
				}
			}
			jobs := &batchv1.JobList{}
			if err := k8sClient.List(ctx, jobs, client.InNamespace(namespace), client.MatchingLabels{
				"experiment.cbse.terministic.de/project": redName,
			}); err != nil {
				return false
			}
			return len(jobs.Items) == 0
		}, 90*time.Second, 2*time.Second).Should(BeTrue(),
			"the GC cascade did not remove the red experiment, its complete child set, and its runner Jobs within 90 seconds")

		Eventually(func() string {
			return queryDatabase(fmt.Sprintf("SELECT COUNT(*) FROM project WHERE project_name='%s'", redName))
		}, 90*time.Second, 2*time.Second).Should(Equal("0"),
			"the red project's persisted project row did not disappear within 90 seconds")
		Expect(queryDatabase(fmt.Sprintf(
			"SELECT COUNT(*) FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='%s'",
			redName,
		))).To(Equal("0"), "the red project's scenario rows must be cascade-deleted with the project row")

		// 10. Teardown the one-shot mock fixture: the spec's own Pod, never
		// an owned child - it is completed (Succeeded) and simply removed.
		Expect(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: mockPodName, Namespace: namespace}})).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, mockPodKey, &corev1.Pod{})
			return apierrors.IsNotFound(err)
		}, 90*time.Second, 2*time.Second).Should(BeTrue(),
			"the eds-mock one-shot fixture did not disappear within 90 seconds")
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

// writeExperimentStatusArtifact persists an experiment status snapshot to the
// named file under CBSE_ARTIFACT_DIR so a run's terminal phase, message, and
// scenario-aggregate verdict can be triaged alongside the JUnit artifacts.
// It mirrors writeDatabaseArtifact's discipline: a no-op when
// CBSE_ARTIFACT_DIR is unset.
func writeExperimentStatusArtifact(name, contents string) {
	directory := strings.TrimSpace(os.Getenv("CBSE_ARTIFACT_DIR"))
	if directory == "" {
		return
	}
	Expect(os.MkdirAll(directory, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(directory, name), []byte(contents+"\n"), 0o644)).To(Succeed())
}

// errValImage is the single red delta of the Validation-Error spec: a
// tag-form image reference without a digest, which the operator's
// validateExperiment rejects at the pre-creation gate (FEATURE.md D1,
// flavor 1). The unresolvable host keeps the reference unmistakably
// tag-form without needing a live registry.
const errValImage = "registry.example.invalid/translator:v1"

// errProvBlockerNodePort is the constant NodePort the Provisioning-Error
// spec's blocker Service holds before the red experiment requests the same
// port (FEATURE.md D1, flavor 2, creation variant). A fixed value in the
// valid 30000-32767 NodePort range keeps the API server's allocation
// conflict deterministic.
const errProvBlockerNodePort = int32(32700)

// errReadyImage is the single red delta of the readiness-watchdog spec
// (FEATURE.md D1, flavor 3): a well-formed OCI digest reference that does not
// exist in any registry - syntactically valid name@sha256:<64 lowercase hex>
// (so the operator's validation gate passes and provisioning completes),
// unresolvable (so the translator image pull fails observably and the
// readiness watchdog eventually drives the experiment to Error).
const errReadyImage = "registry.example.invalid/translator@sha256:" +
	"0000000000000000000000000000000000000000000000000000000000000000"

// buildRedExperiment is the red-experiment builder (FEATURE.md design
// pattern 1): it Gets the live green experiment, deep-copies the CR, renames
// the copy to name, and strips the live object's identity and status so the
// copy is a fresh CR. The caller applies exactly one red delta to the
// returned spec. The copy keeps the green databases, translator, and PPS
// contracts verbatim, so the delta alone drives the failure and the specs
// never hand-write a CR apart from the live profile. The name must satisfy
// the operator's lowercase DNS-label rule (<= 63 chars), which the red names
// inherit from the green name the cluster already admitted.
func buildRedExperiment(ctx context.Context, k8sClient client.Client, greenKey types.NamespacedName, name string) *experimentalpha4.SimulationExperiment {
	Expect(len(name)).To(BeNumerically("<=", 63),
		"the red experiment name %q must satisfy the operator's lowercase DNS-label rule (<= 63 chars)", name)
	green := &experimentalpha4.SimulationExperiment{}
	Expect(k8sClient.Get(ctx, greenKey, green)).To(Succeed())
	red := green.DeepCopy()
	red.Name = name
	red.ResourceVersion = ""
	red.UID = ""
	red.CreationTimestamp = metav1.Time{}
	red.Status = experimentalpha4.SimulationExperimentStatus{}
	return red
}

// ownedChild pairs one operator-owned resource kind with the name it carries
// for an experiment.
type ownedChild struct {
	kind string
	obj  client.Object
	name string
}

// ownedChildren enumerates every owned resource name the operator provisions
// for an experiment named name, in provisionComponents order (detaildb ->
// resultdb -> translator -> PPS): the four component Deployments, the four
// Services, the two database connection Secrets, and the translator
// ConfigMap. The runner ServiceAccount is named from the experiment UID
// rather than the experiment name and is appended by observeOwnedChildren
// through runnerServiceAccountName.
func ownedChildren(name string) []ownedChild {
	return []ownedChild{
		{kind: "Deployment", obj: &appsv1.Deployment{}, name: name + "-detaildb"},
		{kind: "Service", obj: &corev1.Service{}, name: name + "-detaildb-svc"},
		{kind: "Secret", obj: &corev1.Secret{}, name: name + "-detaildb-sct"},
		{kind: "Deployment", obj: &appsv1.Deployment{}, name: name + "-resultdb"},
		{kind: "Service", obj: &corev1.Service{}, name: name + "-resultdb-svc"},
		{kind: "Secret", obj: &corev1.Secret{}, name: name + "-resultdb-sct"},
		{kind: "ConfigMap", obj: &corev1.ConfigMap{}, name: name + "-translator-cfg"},
		{kind: "Deployment", obj: &appsv1.Deployment{}, name: name + "-translator"},
		{kind: "Service", obj: &corev1.Service{}, name: name + "-translator-svc"},
		{kind: "Deployment", obj: &appsv1.Deployment{}, name: name + "-pps"},
		{kind: "Service", obj: &corev1.Service{}, name: name + "-pps-svc"},
	}
}

// runnerServiceAccountName derives the deterministic runner ServiceAccount
// name simrunner-<12-char-UID-prefix> the operator provisions last in
// provisionComponents: the lowercased UID with hyphens stripped, truncated
// to 12 characters. The operator and the Scenario Manager derive the same
// name from the experiment UID by contract.
func runnerServiceAccountName(uid types.UID) string {
	prefix := strings.ToLower(strings.ReplaceAll(string(uid), "-", ""))
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return "simrunner-" + prefix
}

// ownedChildState is one owned child's observed state in the namespace.
type ownedChildState struct {
	kind  string
	name  string
	state string // "present", "absent", or "error: ..."
}

// observeOwnedChildren Gets every owned child of the experiment named name -
// the provisionComponents set (detaildb, resultdb, translator, PPS) plus the
// deterministic runner ServiceAccount, in that order - and reports each
// child's observed state. An unexpected Get failure is reported as
// "error: ..." rather than "absent" so the inventory never hides a client
// error behind an absence.
func observeOwnedChildren(ctx context.Context, k8sClient client.Client, namespace, name string, uid types.UID) []ownedChildState {
	children := ownedChildren(name)
	children = append(children, ownedChild{kind: "ServiceAccount", obj: &corev1.ServiceAccount{}, name: runnerServiceAccountName(uid)})
	states := make([]ownedChildState, 0, len(children))
	for _, child := range children {
		state := "present"
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: child.name}, child.obj)
		switch {
		case err == nil:
		case apierrors.IsNotFound(err):
			state = "absent"
		default:
			state = "error: " + err.Error()
		}
		states = append(states, ownedChildState{kind: child.kind, name: child.name, state: state})
	}
	return states
}

// ownedChildrenText renders the observed inventory as one
// "<kind> <name>=<state>" line per owned child: the children-inventory
// evidence the Error-flavor specs persist to the artifact directory.
func ownedChildrenText(states []ownedChildState) string {
	lines := make([]string, 0, len(states))
	for _, state := range states {
		lines = append(lines, state.kind+" "+state.name+"="+state.state)
	}
	return strings.Join(lines, "\n")
}

// launchEdsMockOneShot launches the green experiment's eds-mock image as a
// one-shot Pod named <redName>-eds-mock in the smoke namespace, replicating
// the installation's eds-mock env contract (test/e2e/manifests/base/
// stack.yaml) for the red project: the stack's NATS_URL, the red project's
// canonical availability subject (cbse.<ns>.<project>.eds.scenarios.available),
// PROJECT_NAME = the red project, and the green profile's two-batch shape -
// with EDS_KEEP_ALIVE disabled so the mock publishes the standard batch and
// exits instead of the installation's keep-alive loop. It follows the suite's
// kubectl pattern (requiredEnv KUBECTL/KUBECONFIG, the smoke namespace).
func launchEdsMockOneShot(image, namespace, redName string) {
	kubectl := requiredEnv("KUBECTL")
	cmd := exec.Command(
		kubectl,
		"--kubeconfig", requiredEnv("KUBECONFIG"),
		"run", redName+"-eds-mock",
		"--namespace", namespace,
		"--image", image,
		"--restart=Never",
		"--env", "NATS_URL=nats://sm-eds-nats:4222",
		"--env", "AVAILABILITY_SUBJECT="+fmt.Sprintf("cbse.%s.%s.eds.scenarios.available", namespace, redName),
		"--env", "PROJECT_NAME="+redName,
		"--env", "TOTAL_BATCHES=2",
		"--env", "SCENARIOS_PER_BATCH=2",
		"--env", "EDS_KEEP_ALIVE=false",
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	Expect(cmd.Run()).To(Succeed(),
		"the eds-mock one-shot launch failed: stdout=%q stderr=%q", strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
}
