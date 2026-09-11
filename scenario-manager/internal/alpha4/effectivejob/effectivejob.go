// Package effectivejob builds the effective batch/v1 runner Job for one scenario
// attempt by merging the Operator-validated runner template (or the default
// template) with the Scenario-Manager-controlled fields. The Operator alone
// validates and persists the immutable template; SM treats an InProgress
// experiment's template as already validated and does not run a second
// template-policy validation pass. The builder only applies the SM executable
// contract: the deterministic name, the indexed manifest shape, the exact
// owner reference, the reserved identity labels, the deterministic
// ServiceAccount, the non-root security context, the accepted runner image,
// and the required registry image-pull Secret.
package effectivejob

import (
	"fmt"
	"sort"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/lifecycle"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/registry"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// globalBackoffLimit is the global Kubernetes Job retry budget shared across
// all completion indexes. The Job is indexed with parallelism == completions,
// so failed Pods anywhere in the Job consume this one budget; Kubernetes
// creates replacement Pods while the budget remains and marks the Job Failed
// when it is exhausted.
const globalBackoffLimit int32 = 4

// runnerContainerName is the single regular container the Operator requires in
// a custom runner template and that the default template supplies.
const runnerContainerName = "runner"

// BuildRequest carries the inputs to build one effective runner Job. The
// experiment must be a live, non-deleting InProgress object whose template SM
// treats as already validated. NumberOfReps is the validated 1..100000
// repetition count; ContainerImage is the accepted Translator digest that
// replaces the runner container image.
type BuildRequest struct {
	Experiment         *experimentalpha4.SimulationExperiment
	ScenarioID         int
	TranslationAttempt int
	NumberOfReps       int
	ContainerImage     string
}

// Build returns the effective batch/v1 Job for the request. It merges the
// Operator-validated runner template (or the default template) with the
// SM-controlled fields and applies the non-root security context. An error
// means SM could not construct the effective Job from an accepted template;
// the caller treats it as a permanent scenario startup failure and makes no
// Kubernetes create call. This is a defensive construction failure, not a
// second template-policy validation pass.
func Build(req BuildRequest) (*batchv1.Job, error) {
	if req.Experiment == nil {
		return nil, fmt.Errorf("experiment must not be nil")
	}
	if req.ScenarioID <= 0 {
		return nil, fmt.Errorf("scenario id must be positive")
	}
	if req.TranslationAttempt <= 0 {
		return nil, fmt.Errorf("translation attempt must be positive")
	}
	if req.NumberOfReps < 1 || req.NumberOfReps > maxIndexedCompletions {
		return nil, fmt.Errorf("number of reps %d is outside 1..%d", req.NumberOfReps, maxIndexedCompletions)
	}
	if err := registry.ValidateDigestImage(req.ContainerImage); err != nil {
		return nil, fmt.Errorf("runner image: %w", err)
	}

	exp := req.Experiment
	uid := exp.UID
	labels := reservedLabels(exp.Name, uid, req.ScenarioID, req.TranslationAttempt)

	podTemplate, jobLabels, jobAnnotations, activeDeadline, err := basePodTemplate(exp, req, labels)
	if err != nil {
		return nil, err
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:            lifecycle.RunnerJobName(uid, req.ScenarioID, req.TranslationAttempt),
			Namespace:       exp.Namespace,
			Labels:          mergeLabels(jobLabels, labels),
			Annotations:     jobAnnotations,
			OwnerReferences: []metav1.OwnerReference{lifecycle.ControllerOwnerReference(exp)},
		},
		Spec: batchv1.JobSpec{
			CompletionMode:        completionModePtr(batchv1.IndexedCompletion),
			Completions:           int32Ptr(int32(req.NumberOfReps)),
			Parallelism:           int32Ptr(int32(req.NumberOfReps)),
			BackoffLimit:          int32Ptr(globalBackoffLimit),
			ActiveDeadlineSeconds: activeDeadline,
			Template:              *podTemplate,
		},
	}
	normalizeJob(job)
	return job, nil
}

// maxIndexedCompletions is the Kubernetes Indexed Job maximum when parallelism
// equals completions. number_of_reps is limited to 1..100000.
const maxIndexedCompletions = 100000

// basePodTemplate returns the effective Pod template (with SM-controlled fields
// applied), the Job-level labels and annotations copied from the template, and
// the optional activeDeadlineSeconds. It merges a custom template or builds the
// default template.
func basePodTemplate(exp *experimentalpha4.SimulationExperiment, req BuildRequest, labels map[string]string) (*corev1.PodTemplateSpec, map[string]string, map[string]string, *int64, error) {
	tmpl := exp.Spec.Runner.JobTemplate
	if tmpl == nil {
		// Default template: one runner container, no volumes, no sidecars.
		pod := &corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: runnerContainerName, Image: req.ContainerImage},
				},
			},
		}
		applyPodContract(pod, exp.UID)
		return pod, nil, nil, nil, nil
	}

	// Custom template: deep-copy so the builder never mutates the CR. The
	// Operator validated and normalized this template before InProgress.
	spec := tmpl.Spec.Template.DeepCopy()
	jobLabels := copyMap(tmpl.ObjectMeta.Labels)
	jobAnnotations := copyMap(tmpl.ObjectMeta.Annotations)

	// Merge reserved labels onto both Job and Pod metadata; reserved keys are
	// absent from the validated template, so this does not mutate user input.
	spec.ObjectMeta.Labels = mergeLabels(spec.ObjectMeta.Labels, labels)

	// Apply the SM contract to the copied pod template.
	applyPodContract(spec, exp.UID)
	// Replace the runner image with the accepted Translator digest.
	if err := setRunnerImage(spec, req.ContainerImage); err != nil {
		return nil, nil, nil, nil, err
	}
	return spec, jobLabels, jobAnnotations, tmpl.Spec.ActiveDeadlineSeconds, nil
}

// applyPodContract applies the SM-controlled pod-template fields: the
// deterministic ServiceAccount, disabled token automount, Never restart
// policy, RuntimeDefault seccomp, non-root/least-privilege container security,
// and the required registry image-pull Secret.
func applyPodContract(pod *corev1.PodTemplateSpec, uid types.UID) {
	pod.Spec.ServiceAccountName = lifecycle.RunnerServiceAccountName(uid)
	automount := false
	pod.Spec.AutomountServiceAccountToken = &automount
	pod.Spec.RestartPolicy = corev1.RestartPolicyNever

	pod.Spec.SecurityContext = mergePodSeccomp(pod.Spec.SecurityContext)

	pod.Spec.ImagePullSecrets = mergeImagePullSecrets(pod.Spec.ImagePullSecrets, registry.RegistryAuthSecretName)

	for i := range pod.Spec.Containers {
		applyContainerSecurity(&pod.Spec.Containers[i])
	}
	for i := range pod.Spec.InitContainers {
		applyContainerSecurity(&pod.Spec.InitContainers[i])
	}
}

// mergePodSeccomp returns a pod security context that preserves any validated
// user identity fields and sets RuntimeDefault seccomp. The Operator's
// template policy does not allow seccomp in the template, so this is the
// SM-owned field.
func mergePodSeccomp(existing *corev1.PodSecurityContext) *corev1.PodSecurityContext {
	if existing == nil {
		existing = &corev1.PodSecurityContext{}
	}
	existing.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
	return existing
}

// applyContainerSecurity sets the non-root, least-privilege container security
// context the SM executable contract requires on every container, preserving
// any validated user identity fields (RunAsUser, RunAsGroup,
// ReadOnlyRootFilesystem). The Operator's template policy does not allow
// RunAsNonRoot, AllowPrivilegeEscalation, or Capabilities, so these are
// SM-owned fields.
func applyContainerSecurity(container *corev1.Container) {
	if container.SecurityContext == nil {
		container.SecurityContext = &corev1.SecurityContext{}
	}
	runAsNonRoot := true
	container.SecurityContext.RunAsNonRoot = &runAsNonRoot
	allowPrivEscalation := false
	container.SecurityContext.AllowPrivilegeEscalation = &allowPrivEscalation
	if container.SecurityContext.Capabilities == nil {
		container.SecurityContext.Capabilities = &corev1.Capabilities{}
	}
	container.SecurityContext.Capabilities.Drop = []corev1.Capability{"ALL"}
}

// mergeImagePullSecrets returns a sorted, de-duplicated image-pull Secret list
// that always includes the required registry Secret. The Operator's template
// policy already sorts and de-duplicates user-supplied pull Secrets; this
// re-applies the set-like ordering after adding the required entry.
func mergeImagePullSecrets(existing []corev1.LocalObjectReference, required string) []corev1.LocalObjectReference {
	merged := make([]corev1.LocalObjectReference, 0, len(existing)+1)
	seen := map[string]struct{}{}
	for _, ref := range existing {
		if ref.Name == "" {
			continue
		}
		if _, ok := seen[ref.Name]; ok {
			continue
		}
		seen[ref.Name] = struct{}{}
		merged = append(merged, ref)
	}
	if _, ok := seen[required]; !ok {
		merged = append(merged, corev1.LocalObjectReference{Name: required})
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })
	return merged
}

// setRunnerImage replaces the single runner container's image with the
// accepted Translator digest. The Operator requires exactly one container
// named runner; a missing or duplicate runner is a defensive construction
// failure, not a template-policy re-validation.
func setRunnerImage(pod *corev1.PodTemplateSpec, image string) error {
	idx := -1
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == runnerContainerName {
			if idx >= 0 {
				return fmt.Errorf("template has more than one runner container")
			}
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("template has no runner container")
	}
	pod.Spec.Containers[idx].Image = image
	return nil
}

// reservedLabels returns the four reserved identity labels for the Job and Pod
// template metadata.
func reservedLabels(project string, uid types.UID, scenarioID, attempt int) map[string]string {
	return map[string]string{
		lifecycle.LabelProject:            project,
		lifecycle.LabelExperimentUID:      string(uid),
		lifecycle.LabelScenarioID:         fmt.Sprintf("%d", scenarioID),
		lifecycle.LabelTranslationAttempt: fmt.Sprintf("%d", attempt),
	}
}

// mergeLabels returns a new label map that combines the user labels with the
// reserved labels, letting the reserved labels win any (unexpected) conflict.
func mergeLabels(user, reserved map[string]string) map[string]string {
	out := make(map[string]string, len(user)+len(reserved))
	for k, v := range user {
		out[k] = v
	}
	for k, v := range reserved {
		out[k] = v
	}
	return out
}

// copyMap returns a shallow copy of a string map, or nil if the input is nil so
// the Job never aliases the CR.
func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// normalizeJob ensures the effective Job's label maps are non-nil and the pod
// template carries the reserved labels, applying nil/empty normalization so
// the manifest shape is stable for unit tests.
func normalizeJob(job *batchv1.Job) {
	if job.ObjectMeta.Labels == nil {
		job.ObjectMeta.Labels = map[string]string{}
	}
	if job.Spec.Template.ObjectMeta.Labels == nil {
		job.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}
}

func completionModePtr(m batchv1.CompletionMode) *batchv1.CompletionMode { return &m }
func int32Ptr(v int32) *int32                                            { return &v }
