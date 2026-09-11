package effectivejob

import (
	"strconv"
	"strings"
	"testing"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/messaging"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var validDigest = "registry.example.com/cbse/runner@sha256:" + strings.Repeat("a", 64)

func newExperiment(uid string, tmpl *batchv1.JobTemplateSpec) *experimentalpha4.SimulationExperiment {
	return &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Name: "exp-x", Namespace: "ns-x", UID: types.UID(uid)},
		Spec: experimentalpha4.SimulationExperimentSpec{
			Runner:     experimentalpha4.RunnerSpec{JobTemplate: tmpl},
			Translator: experimentalpha4.TranslatorSpec{RegistryAuthSecretRef: corev1.LocalObjectReference{Name: "cbse-registry-auth"}},
		},
	}
}

// requireDefaultTemplateShape asserts the SM-controlled fields that the default
// (no custom template) Job must carry.
func requireDefaultTemplateShape(t *testing.T, job *batchv1.Job, exp *experimentalpha4.SimulationExperiment, scenarioID, attempt, reps int) {
	t.Helper()

	wantName := "simrun-" + messaging.UIDPrefix(string(exp.UID)) + "-s" + strconv.Itoa(scenarioID) + "-a" + strconv.Itoa(attempt)
	if job.Name != wantName {
		t.Fatalf("Job name = %q, want %q", job.Name, wantName)
	}
	if job.Namespace != exp.Namespace {
		t.Fatalf("Job namespace = %q, want %q", job.Namespace, exp.Namespace)
	}

	if job.Spec.CompletionMode == nil || *job.Spec.CompletionMode != batchv1.IndexedCompletion {
		t.Fatalf("Job completionMode = %v, want Indexed", job.Spec.CompletionMode)
	}
	if job.Spec.Completions == nil || *job.Spec.Completions != int32(reps) {
		t.Fatalf("Job completions = %v, want %d", job.Spec.Completions, reps)
	}
	if job.Spec.Parallelism == nil || *job.Spec.Parallelism != int32(reps) {
		t.Fatalf("Job parallelism = %v, want %d", job.Spec.Parallelism, reps)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 4 {
		t.Fatalf("Job backoffLimit = %v, want 4", job.Spec.BackoffLimit)
	}
	if job.Spec.BackoffLimitPerIndex != nil {
		t.Fatalf("Job backoffLimitPerIndex = %v, want nil", job.Spec.BackoffLimitPerIndex)
	}
	if job.Spec.MaxFailedIndexes != nil {
		t.Fatalf("Job maxFailedIndexes = %v, want nil", job.Spec.MaxFailedIndexes)
	}
	if job.Spec.TTLSecondsAfterFinished != nil {
		t.Fatalf("Job ttlSecondsAfterFinished = %v, want nil", job.Spec.TTLSecondsAfterFinished)
	}
	if job.Spec.ActiveDeadlineSeconds != nil {
		t.Fatalf("Job activeDeadlineSeconds = %v, want nil for default template", job.Spec.ActiveDeadlineSeconds)
	}

	if len(job.OwnerReferences) != 1 {
		t.Fatalf("Job ownerReferences len = %d, want 1", len(job.OwnerReferences))
	}
	or := job.OwnerReferences[0]
	if or.Controller == nil || !*or.Controller || or.BlockOwnerDeletion != nil && *or.BlockOwnerDeletion {
		t.Fatalf("Job owner ref controller=%v blockOwnerDeletion=%v, want controller=true blockOwnerDeletion=false", or.Controller, or.BlockOwnerDeletion)
	}
	if or.UID != exp.UID || or.Name != exp.Name {
		t.Fatalf("Job owner ref uid=%q name=%q, want uid=%q name=%q", or.UID, or.Name, exp.UID, exp.Name)
	}
	if or.APIVersion != "experiment.cbse.terministic.de/alpha4" || or.Kind != "SimulationExperiment" {
		t.Fatalf("Job owner ref apiVersion=%q kind=%q, want alpha4/SimulationExperiment", or.APIVersion, or.Kind)
	}

	// Labels on both Job and Pod template.
	wantLabels := map[string]string{
		"experiment.cbse.terministic.de/project":             exp.Name,
		"experiment.cbse.terministic.de/experiment-uid":      string(exp.UID),
		"experiment.cbse.terministic.de/scenario-id":         strconv.Itoa(scenarioID),
		"experiment.cbse.terministic.de/translation-attempt": strconv.Itoa(attempt),
	}
	for k, v := range wantLabels {
		if got := job.Labels[k]; got != v {
			t.Fatalf("Job label %s = %q, want %q", k, got, v)
		}
		if got := job.Spec.Template.Labels[k]; got != v {
			t.Fatalf("Pod label %s = %q, want %q", k, got, v)
		}
	}

	// Pod contract.
	pod := &job.Spec.Template.Spec
	if pod.ServiceAccountName != "simrunner-"+messaging.UIDPrefix(string(exp.UID)) {
		t.Fatalf("Pod serviceAccountName = %q, want simrunner-<uidPrefix>", pod.ServiceAccountName)
	}
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Fatalf("Pod automountServiceAccountToken = %v, want false", pod.AutomountServiceAccountToken)
	}
	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("Pod restartPolicy = %q, want Never", pod.RestartPolicy)
	}
	if pod.SecurityContext == nil || pod.SecurityContext.SeccompProfile == nil || pod.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("Pod seccomp not RuntimeDefault: %+v", pod.SecurityContext)
	}

	// imagePullSecrets: exactly the registry secret.
	if len(pod.ImagePullSecrets) != 1 || pod.ImagePullSecrets[0].Name != "cbse-registry-auth" {
		t.Fatalf("Pod imagePullSecrets = %+v, want [cbse-registry-auth]", pod.ImagePullSecrets)
	}

	// Exactly one runner container with the accepted image and non-root security.
	if len(pod.Containers) != 1 {
		t.Fatalf("default Pod containers len = %d, want 1", len(pod.Containers))
	}
	c := pod.Containers[0]
	if c.Name != "runner" {
		t.Fatalf("container name = %q, want runner", c.Name)
	}
	if c.Image != validDigest {
		t.Fatalf("runner image = %q, want %q", c.Image, validDigest)
	}
	requireContainerSecurity(t, &c)
}

func requireContainerSecurity(t *testing.T, c *corev1.Container) {
	t.Helper()
	if c.SecurityContext == nil {
		t.Fatalf("container %q has nil security context", c.Name)
	}
	if c.SecurityContext.RunAsNonRoot == nil || !*c.SecurityContext.RunAsNonRoot {
		t.Fatalf("container %q runAsNonRoot = %v, want true", c.Name, c.SecurityContext.RunAsNonRoot)
	}
	if c.SecurityContext.AllowPrivilegeEscalation == nil || *c.SecurityContext.AllowPrivilegeEscalation {
		t.Fatalf("container %q allowPrivilegeEscalation = %v, want false", c.Name, c.SecurityContext.AllowPrivilegeEscalation)
	}
	if c.SecurityContext.Capabilities == nil || len(c.SecurityContext.Capabilities.Drop) != 1 || c.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("container %q capabilities.drop = %+v, want [ALL]", c.Name, c.SecurityContext.Capabilities)
	}
}

func TestBuildDefaultTemplate(t *testing.T) {
	exp := newExperiment("1234abcd-5678-4321-abcd-9999ccccdddd", nil)
	job, err := Build(BuildRequest{Experiment: exp, ScenarioID: 42, TranslationAttempt: 1, NumberOfReps: 7, ContainerImage: validDigest})
	if err != nil {
		t.Fatalf("Build default template: %v", err)
	}
	requireDefaultTemplateShape(t, job, exp, 42, 1, 7)
}

func TestBuildMaxValidReps(t *testing.T) {
	exp := newExperiment("1234abcd-5678-4321-abcd-9999ccccdddd", nil)
	job, err := Build(BuildRequest{Experiment: exp, ScenarioID: 1, TranslationAttempt: 1, NumberOfReps: 100000, ContainerImage: validDigest})
	if err != nil {
		t.Fatalf("Build 100000 reps: %v", err)
	}
	if *job.Spec.Completions != 100000 || *job.Spec.Parallelism != 100000 {
		t.Fatalf("completions/parallelism = %d/%d, want 100000/100000", *job.Spec.Completions, *job.Spec.Parallelism)
	}
}

func TestBuildRejectsRepsOutOfRange(t *testing.T) {
	cases := []int{0, -1, 100001, 1_000_000}
	for _, reps := range cases {
		exp := newExperiment("1234abcd-5678-4321-abcd-9999ccccdddd", nil)
		_, err := Build(BuildRequest{Experiment: exp, ScenarioID: 1, TranslationAttempt: 1, NumberOfReps: reps, ContainerImage: validDigest})
		if err == nil {
			t.Fatalf("reps %d: expected error, got nil", reps)
		}
	}
}

func TestBuildRejectsNonDigestImage(t *testing.T) {
	exp := newExperiment("1234abcd-5678-4321-abcd-9999ccccdddd", nil)
	_, err := Build(BuildRequest{Experiment: exp, ScenarioID: 1, TranslationAttempt: 1, NumberOfReps: 1, ContainerImage: "registry.example.com/runner:latest"})
	if err == nil {
		t.Fatalf("non-digest image: expected error, got nil")
	}
}

func TestBuildRejectsNilExperiment(t *testing.T) {
	if _, err := Build(BuildRequest{ScenarioID: 1, TranslationAttempt: 1, NumberOfReps: 1, ContainerImage: validDigest}); err == nil {
		t.Fatal("nil experiment: expected error")
	}
}

func TestBuildRejectsBadScenarioAndAttempt(t *testing.T) {
	exp := newExperiment("1234abcd-5678-4321-abcd-9999ccccdddd", nil)
	for _, req := range []BuildRequest{
		{Experiment: exp, ScenarioID: 0, TranslationAttempt: 1, NumberOfReps: 1, ContainerImage: validDigest},
		{Experiment: exp, ScenarioID: 1, TranslationAttempt: 0, NumberOfReps: 1, ContainerImage: validDigest},
	} {
		if _, err := Build(req); err == nil {
			t.Fatalf("ScenarioID=%d TranslationAttempt=%d: expected error", req.ScenarioID, req.TranslationAttempt)
		}
	}
}

func TestBuildCustomTemplateMergesUserFields(t *testing.T) {
	deadline := int64(120)
	uid := int64(1000)
	ro := true
	tmpl := &batchv1.JobTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      map[string]string{"app.example.com/team": "sims"},
			Annotations: map[string]string{"app.example.com/owner": "research"},
		},
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: &deadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      map[string]string{"app.example.com/cost-center": "lab-7"},
					Annotations: map[string]string{"prometheus.io/scrape": "true"},
				},
				Spec: corev1.PodSpec{
					ImagePullSecrets: []corev1.LocalObjectReference{
						{Name: "registry.example.com-pull"},
						{Name: "cbse-registry-auth"}, // already present; must not duplicate
					},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:      &uid,
						FSGroup:        &uid,
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined}, // ignored template field; would be rejected by operator, but builder overrides to RuntimeDefault
					},
					Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
					Containers: []corev1.Container{
						{
							Name:  "runner",
							Image: "", // Operator forbids runner image; SM supplies accepted digest.
							SecurityContext: &corev1.SecurityContext{
								RunAsUser:              &uid,
								ReadOnlyRootFilesystem: &ro,
							},
							VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data"}},
						},
						{Name: "sidecar", Image: validDigest}, // auxiliary container image preserved, security added.
					},
					InitContainers: []corev1.Container{
						{Name: "init-setup", Image: validDigest},
					},
				},
			},
		},
	}
	exp := newExperiment("1234abcd-5678-4321-abcd-9999ccccdddd", tmpl)

	job, err := Build(BuildRequest{Experiment: exp, ScenarioID: 9, TranslationAttempt: 2, NumberOfReps: 3, ContainerImage: validDigest})
	if err != nil {
		t.Fatalf("Build custom template: %v", err)
	}

	// Job-level annotations preserved from template; reserved labels added.
	if got := job.Annotations["app.example.com/owner"]; got != "research" {
		t.Fatalf("Job annotation lost: %q", got)
	}
	if got := job.Labels["app.example.com/team"]; got != "sims" {
		t.Fatalf("Job user label lost: %q", got)
	}
	for k, v := range map[string]string{
		"experiment.cbse.terministic.de/project":             exp.Name,
		"experiment.cbse.terministic.de/experiment-uid":      string(exp.UID),
		"experiment.cbse.terministic.de/scenario-id":         "9",
		"experiment.cbse.terministic.de/translation-attempt": "2",
	} {
		if got := job.Labels[k]; got != v {
			t.Fatalf("Job reserved label %s = %q, want %q", k, got, v)
		}
		if got := job.Spec.Template.Labels[k]; got != v {
			t.Fatalf("Pod reserved label %s = %q, want %q", k, got, v)
		}
	}

	// activeDeadlineSeconds preserved from template.
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != 120 {
		t.Fatalf("activeDeadlineSeconds = %v, want 120", job.Spec.ActiveDeadlineSeconds)
	}

	pod := &job.Spec.Template.Spec

	// User pod security context identity preserved; seccomp overridden to RuntimeDefault.
	if pod.SecurityContext == nil || pod.SecurityContext.RunAsUser == nil || *pod.SecurityContext.RunAsUser != 1000 {
		t.Fatalf("pod RunAsUser not preserved: %+v", pod.SecurityContext)
	}
	if pod.SecurityContext.FSGroup == nil || *pod.SecurityContext.FSGroup != 1000 {
		t.Fatalf("pod FSGroup not preserved: %+v", pod.SecurityContext)
	}
	if pod.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("pod seccomp = %v, want RuntimeDefault", pod.SecurityContext.SeccompProfile.Type)
	}

	// Volumes preserved.
	if len(pod.Volumes) != 1 || pod.Volumes[0].Name != "data" {
		t.Fatalf("volumes not preserved: %+v", pod.Volumes)
	}

	// imagePullSecrets: user secret + registry secret, sorted and de-duped (registry not duplicated).
	wantSecrets := []string{"cbse-registry-auth", "registry.example.com-pull"}
	if len(pod.ImagePullSecrets) != 2 {
		t.Fatalf("imagePullSecrets len = %d, want 2", len(pod.ImagePullSecrets))
	}
	for i, name := range wantSecrets {
		if pod.ImagePullSecrets[i].Name != name {
			t.Fatalf("imagePullSecrets[%d] = %q, want %q (sorted+deduped)", i, pod.ImagePullSecrets[i].Name, name)
		}
	}

	// Runner image replaced with accepted digest; user identity preserved; non-root added.
	runner := pod.Containers[0]
	if runner.Name != "runner" {
		t.Fatalf("containers[0] name = %q, want runner", runner.Name)
	}
	if runner.Image != validDigest {
		t.Fatalf("runner image = %q, want accepted digest", runner.Image)
	}
	if runner.SecurityContext == nil || runner.SecurityContext.RunAsUser == nil || *runner.SecurityContext.RunAsUser != 1000 {
		t.Fatalf("runner RunAsUser not preserved: %+v", runner.SecurityContext)
	}
	if runner.SecurityContext.ReadOnlyRootFilesystem == nil || !*runner.SecurityContext.ReadOnlyRootFilesystem {
		t.Fatalf("runner ReadOnlyRootFilesystem not preserved: %+v", runner.SecurityContext)
	}
	requireContainerSecurity(t, &runner)
	if len(runner.VolumeMounts) != 1 || runner.VolumeMounts[0].Name != "data" {
		t.Fatalf("runner volumeMounts not preserved: %+v", runner.VolumeMounts)
	}

	// Sidecar image preserved; security added.
	side := pod.Containers[1]
	if side.Image != validDigest {
		t.Fatalf("sidecar image = %q, want preserved %q", side.Image, validDigest)
	}
	requireContainerSecurity(t, &side)

	// Init container image preserved; security added.
	if len(pod.InitContainers) != 1 {
		t.Fatalf("initContainers len = %d, want 1", len(pod.InitContainers))
	}
	requireContainerSecurity(t, &pod.InitContainers[0])
}

func TestBuildCustomTemplateRejectsMissingRunner(t *testing.T) {
	tmpl := &batchv1.JobTemplateSpec{
		Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "other", Image: validDigest}},
			},
		}},
	}
	exp := newExperiment("1234abcd-5678-4321-abcd-9999ccccdddd", tmpl)
	if _, err := Build(BuildRequest{Experiment: exp, ScenarioID: 1, TranslationAttempt: 1, NumberOfReps: 1, ContainerImage: validDigest}); err == nil {
		t.Fatal("missing runner container: expected error")
	}
}

func TestBuildCustomTemplateRejectsDuplicateRunner(t *testing.T) {
	tmpl := &batchv1.JobTemplateSpec{
		Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "runner"},
					{Name: "runner"},
				},
			},
		}},
	}
	exp := newExperiment("1234abcd-5678-4321-abcd-9999ccccdddd", tmpl)
	if _, err := Build(BuildRequest{Experiment: exp, ScenarioID: 1, TranslationAttempt: 1, NumberOfReps: 1, ContainerImage: validDigest}); err == nil {
		t.Fatal("duplicate runner container: expected error")
	}
}

func TestBuildDoesNotMutateExperiment(t *testing.T) {
	tmpl := &batchv1.JobTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"keep": "me"}},
		Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "runner"}},
			},
		}},
	}
	exp := newExperiment("1234abcd-5678-4321-abcd-9999ccccdddd", tmpl)
	if _, err := Build(BuildRequest{Experiment: exp, ScenarioID: 1, TranslationAttempt: 1, NumberOfReps: 1, ContainerImage: validDigest}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	// The CR's template runner container must still have an empty image.
	if c := exp.Spec.Runner.JobTemplate.Spec.Template.Spec.Containers[0]; c.Image != "" {
		t.Fatalf("Build mutated experiment template runner image to %q", c.Image)
	}
	// The CR's labels must be untouched.
	if got := exp.Spec.Runner.JobTemplate.Labels["keep"]; got != "me" {
		t.Fatalf("Build mutated experiment template labels: %q", got)
	}
}

func TestBuildDeterministicNameAndSA(t *testing.T) {
	exp := newExperiment("1234abcd-5678-4321-abcd-9999ccccdddd", nil)
	j1, _ := Build(BuildRequest{Experiment: exp, ScenarioID: 5, TranslationAttempt: 1, NumberOfReps: 1, ContainerImage: validDigest})
	exp2 := newExperiment("1234abcd-5678-4321-abcd-9999ccccdddd", nil)
	j2, _ := Build(BuildRequest{Experiment: exp2, ScenarioID: 5, TranslationAttempt: 1, NumberOfReps: 1, ContainerImage: validDigest})
	if j1.Name != j2.Name || j1.Spec.Template.Spec.ServiceAccountName != j2.Spec.Template.Spec.ServiceAccountName {
		t.Fatalf("name/SA not deterministic across equal UIDs: %q/%q vs %q/%q",
			j1.Name, j1.Spec.Template.Spec.ServiceAccountName, j2.Name, j2.Spec.Template.Spec.ServiceAccountName)
	}
}

func TestBuildImagePullSecretsSortsUnordered(t *testing.T) {
	tmpl := &batchv1.JobTemplateSpec{
		Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				ImagePullSecrets: []corev1.LocalObjectReference{
					{Name: "z-late"},
					{Name: "a-early"},
				},
				Containers: []corev1.Container{{Name: "runner"}},
			},
		}},
	}
	exp := newExperiment("1234abcd-5678-4321-abcd-9999ccccdddd", tmpl)
	job, err := Build(BuildRequest{Experiment: exp, ScenarioID: 1, TranslationAttempt: 1, NumberOfReps: 1, ContainerImage: validDigest})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ips := job.Spec.Template.Spec.ImagePullSecrets
	want := []string{"a-early", "cbse-registry-auth", "z-late"}
	if len(ips) != 3 {
		t.Fatalf("imagePullSecrets len = %d, want 3", len(ips))
	}
	for i, w := range want {
		if ips[i].Name != w {
			t.Fatalf("imagePullSecrets[%d] = %q, want %q", i, ips[i].Name, w)
		}
	}
}
