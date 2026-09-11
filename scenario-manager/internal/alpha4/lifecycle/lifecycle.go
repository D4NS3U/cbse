// Package lifecycle implements the alpha4 Scenario Manager experiment lifecycle
// gate, finalizer sequencing, terminal action, and deleted-experiment cleanup.
//
// The package is transport-neutral where possible: it depends on the alpha4
// SimulationExperiment API, the namespace-aware subject grammar, the alpha4
// persistence layer, and a controller-runtime client for Kubernetes Job and
// finalizer operations. NATS messaging cleanup is injected through the
// MessagingCleaner interface so unit tests exercise the ordering with fakes.
//
// This package is additive and isolated until the alpha4 cutover in a later
// slice: it does not replace the active alpha3 lifecycle wiring.
package lifecycle

import (
	"fmt"
	"strconv"
	"strings"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/messaging"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// FinalizerName is the SM-owned cleanup finalizer. SM adds it before
// registering a non-deleting experiment in Core DB and removes it only after
// verified deletion cleanup. Kubernetes cannot admit a replacement CR with the
// same namespace/name while the finalizer remains, so the previous
// incarnation's durable state is gone before a fresh row is registered.
const FinalizerName = "scenario-manager.cbse.terministic.de/cleanup"

// Reserved identity labels carried by every Operator- and SM-managed Pod
// template. The runner Job ownership check requires the exact project,
// experiment-UID, scenario-id, and translation-attempt label values.
const (
	LabelProject            = "experiment.cbse.terministic.de/project"
	LabelExperimentUID      = "experiment.cbse.terministic.de/experiment-uid"
	LabelScenarioID         = "experiment.cbse.terministic.de/scenario-id"
	LabelTranslationAttempt = "experiment.cbse.terministic.de/translation-attempt"
)

// Alpha4 experiment phase values observed on SimulationExperiment.status.phase.
const (
	PhasePending      = "Pending"
	PhaseProvisioning = "Provisioning"
	PhaseInProgress   = "InProgress"
	PhaseCompleted    = "Completed"
	PhaseFailed       = "Failed"
	PhaseError        = "Error"
)

// RetryCadence is the fixed delay applied to a failed terminal action or
// deletion cleanup attempt. There is no exponential backoff or retry limit.
const RetryCadence = 5 // seconds; the caller's worker applies time.Duration.

// RunnerJobName returns the deterministic runner Job name
// simrun-<12-char-UID-prefix>-s<scenario-id>-a<attempt> for the given experiment
// UID, scenario id, and translation attempt. The UID prefix follows the same
// lowercase, hyphen-stripped, 12-character rule as the Translator durable
// consumer name, so the Job name is stable across SM replicas and restarts.
func RunnerJobName(uid types.UID, scenarioID, attempt int) string {
	return fmt.Sprintf("simrun-%s-s%d-a%d", messaging.UIDPrefix(string(uid)), scenarioID, attempt)
}

// RunnerServiceAccountName returns the deterministic runner ServiceAccount name
// simrunner-<12-char-UID-prefix>. The Operator creates this ServiceAccount from
// the same live UID; SM resolves it with a get-only RBAC grant and references it
// by exact name in the runner Job pod template. The derivation mirrors the
// Experiment Operator's RunnerServiceAccountName so both processes compute the
// same name from the same UID without a cross-module import.
func RunnerServiceAccountName(uid types.UID) string {
	return "simrunner-" + messaging.UIDPrefix(string(uid))
}

// ControllerOwnerReference returns the exact alpha4 controller owner reference
// that a runner Job must carry: apiVersion
// experiment.cbse.terministic.de/alpha4, kind SimulationExperiment, the live
// experiment name and full UID, controller true, and blockOwnerDeletion false.
// It is the exported form of ownerReference used by the effective-Job builder.
func ControllerOwnerReference(exp *experimentalpha4.SimulationExperiment) metav1.OwnerReference {
	return ownerReference(exp)
}

// ExperimentIdentity is the (namespace, name, UID) triple used to match an
// informer event to the current live incarnation. A stale event for an old UID
// must neither close nor clean a replacement.
type ExperimentIdentity struct {
	Namespace string
	Name      string
	UID       types.UID
}

// IdentityFromExperiment extracts the current identity from a fetched object.
func IdentityFromExperiment(exp *experimentalpha4.SimulationExperiment) ExperimentIdentity {
	return ExperimentIdentity{
		Namespace: exp.Namespace,
		Name:      exp.Name,
		UID:       exp.UID,
	}
}

// Matches reports whether the event identity equals the live object identity.
func (i ExperimentIdentity) Matches(other ExperimentIdentity) bool {
	return i.Namespace == other.Namespace && i.Name == other.Name && i.UID == other.UID
}

// ownerReference returns the exact alpha4 controller owner reference that a
// runner Job must carry: apiVersion experiment.cbse.terministic.de/alpha4, kind
// SimulationExperiment, the live experiment name and full UID, controller true,
// and blockOwnerDeletion false.
func ownerReference(exp *experimentalpha4.SimulationExperiment) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         experimentalpha4.GroupVersion.String(),
		Kind:               "SimulationExperiment",
		Name:               exp.Name,
		UID:                exp.UID,
		Controller:         boolPtr(true),
		BlockOwnerDeletion: boolPtr(false),
	}
}

func boolPtr(b bool) *bool { return &b }

// parsePositiveDecimal reports whether s is a canonical positive decimal integer
// with no leading zeros (except "0" itself, which is not positive) and no sign.
// The scenario-id and translation-attempt labels must be canonical positive
// decimal values that reproduce the deterministic Job name.
func parsePositiveDecimal(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	// No leading sign, no leading zero for multi-digit values, no spaces.
	if s[0] == '0' {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// labelsFromJob returns the reserved identity labels from a Job, or an error
// naming the first missing or malformed value.
func labelsFromJob(job *batchv1.Job, wantProject string, wantUID types.UID) (scenarioID, attempt int, err error) {
	labels := job.Labels
	if labels[LabelProject] != wantProject {
		return 0, 0, fmt.Errorf("job %s/%s label %s = %q; want %q", job.Namespace, job.Name, LabelProject, labels[LabelProject], wantProject)
	}
	if labels[LabelExperimentUID] != string(wantUID) {
		return 0, 0, fmt.Errorf("job %s/%s label %s = %q; want %q", job.Namespace, job.Name, LabelExperimentUID, labels[LabelExperimentUID], wantUID)
	}
	sid, ok := parsePositiveDecimal(labels[LabelScenarioID])
	if !ok {
		return 0, 0, fmt.Errorf("job %s/%s label %s = %q; want canonical positive decimal", job.Namespace, job.Name, LabelScenarioID, labels[LabelScenarioID])
	}
	att, ok := parsePositiveDecimal(labels[LabelTranslationAttempt])
	if !ok {
		return 0, 0, fmt.Errorf("job %s/%s label %s = %q; want canonical positive decimal", job.Namespace, job.Name, LabelTranslationAttempt, labels[LabelTranslationAttempt])
	}
	return sid, att, nil
}

// hasControllerOwnerReference reports whether job carries exactly the alpha4
// controller owner reference for the given experiment.
func hasControllerOwnerReference(job *batchv1.Job, exp *experimentalpha4.SimulationExperiment) bool {
	want := ownerReference(exp)
	for _, ref := range job.OwnerReferences {
		if ref.APIVersion == want.APIVersion &&
			ref.Kind == want.Kind &&
			ref.Name == want.Name &&
			ref.UID == want.UID &&
			ref.Controller != nil && *ref.Controller == true &&
			ref.BlockOwnerDeletion != nil && *ref.BlockOwnerDeletion == false {
			return true
		}
	}
	return false
}

// jobNameMatchesIdentity rebuilds the deterministic Job name from the
// experiment UID and the scenario-id/attempt labels and compares it to the
// observed Job name.
func jobNameMatchesIdentity(job *batchv1.Job, exp *experimentalpha4.SimulationExperiment, scenarioID, attempt int) bool {
	return job.Name == RunnerJobName(exp.UID, scenarioID, attempt)
}

// verifyJobOwnership returns nil only when job belongs to the experiment: it
// runs in the experiment namespace, its project and full-UID labels match, its
// scenario-id and translation-attempt labels are canonical positive decimals
// that reproduce the deterministic Job name, and it carries the exact alpha4
// controller owner reference. Any mismatch is an identity collision: the Job
// must not be deleted and the caller must fail the attempt.
func verifyJobOwnership(job *batchv1.Job, exp *experimentalpha4.SimulationExperiment) error {
	if job.Namespace != exp.Namespace {
		return fmt.Errorf("job %s/%s namespace mismatch; want %s", job.Namespace, job.Name, exp.Namespace)
	}
	scenarioID, attempt, err := labelsFromJob(job, exp.Name, exp.UID)
	if err != nil {
		return err
	}
	if !jobNameMatchesIdentity(job, exp, scenarioID, attempt) {
		return fmt.Errorf("job %s/%s name does not reproduce the deterministic name for s%d-a%d", job.Namespace, job.Name, scenarioID, attempt)
	}
	if !hasControllerOwnerReference(job, exp) {
		return fmt.Errorf("job %s/%s missing exact alpha4 controller owner reference", job.Namespace, job.Name)
	}
	return nil
}

// terminalFailureStatesString returns the non-terminal scenario states joined
// for diagnostics; it mirrors persistence.terminalFailureStates.
func terminalFailureStatesString() string {
	return strings.Join([]string{
		"Created", "Scheduled", "StartingRunners", "InProcessing", "PostProcessing",
	}, ", ")
}
