package jobadapter

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/scheduler"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

const (
	ns        = "ns-x"
	expName   = "exp-x"
	expUID    = "1234abcd-5678-4321-abcd-9999ccccdddd"
	uidPrefix = "1234abcd5678" // UIDPrefix("1234abcd-5678-4321-abcd-9999ccccdddd")
	repo      = "registry.example.com/cbse/runner"
	saName    = "simrunner-" + uidPrefix
)

var validDigest = "registry.example.com/cbse/runner@sha256:" + strings.Repeat("a", 64)

func validDockerConfig() []byte {
	return []byte(`{"auths":{"registry.example.com":{"username":"u","password":"p","auth":"dTpw"}}}`)
}

func newExperiment(phase string) *experimentalpha4.SimulationExperiment {
	return &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Name: expName, Namespace: ns, UID: types.UID(expUID)},
		Status:     experimentalpha4.SimulationExperimentStatus{Phase: phase},
		Spec: experimentalpha4.SimulationExperimentSpec{
			Translator: experimentalpha4.TranslatorSpec{
				Repository:            repo,
				RegistryAuthSecretRef: corev1.LocalObjectReference{Name: "cbse-registry-auth"},
			},
		},
	}
}

func validSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cbse-registry-auth", Namespace: ns},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{".dockerconfigjson": validDockerConfig()},
	}
}

// jobLabels returns the four reserved labels for a scenario/attempt.
func jobLabels(scenarioID, attempt int) map[string]string {
	return map[string]string{
		"experiment.cbse.terministic.de/project":             expName,
		"experiment.cbse.terministic.de/experiment-uid":      expUID,
		"experiment.cbse.terministic.de/scenario-id":         intToStr(scenarioID),
		"experiment.cbse.terministic.de/translation-attempt": intToStr(attempt),
	}
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func int32Ptr(v int32) *int32 { return &v }

func alpha4GR(name string) schema.GroupResource {
	return schema.GroupResource{Group: experimentalpha4.GroupVersion.Group, Resource: name}
}

func ownerRef() metav1.OwnerReference {
	ctrl := true
	block := false
	return metav1.OwnerReference{
		APIVersion: "experiment.cbse.terministic.de/alpha4", Kind: "SimulationExperiment",
		Name: expName, UID: types.UID(expUID), Controller: &ctrl, BlockOwnerDeletion: &block,
	}
}

// ownedJob returns a Job with the deterministic name, namespace, reserved
// labels, and exact controller owner reference.
func ownedJob(scenarioID, attempt int) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:            jobName(scenarioID, attempt),
			Namespace:       ns,
			Labels:          jobLabels(scenarioID, attempt),
			OwnerReferences: []metav1.OwnerReference{ownerRef()},
			UID:             types.UID("job-uid-existing"),
		},
	}
}

func jobName(scenarioID, attempt int) string {
	return "simrun-" + uidPrefix + "-s" + intToStr(scenarioID) + "-a" + intToStr(attempt)
}

// fakeK8s is a configurable fake of the jobadapter k8sClient. Tests set the
// fields they need; unset fields yield the happy path.
type fakeK8s struct {
	mu sync.Mutex

	exp       *experimentalpha4.SimulationExperiment
	expErr    error // overrides GetExperiment (NotFound, Forbidden, transport)
	secret    *corev1.Secret
	secretErr error
	saErr     error

	jobs          map[string]*batchv1.Job                   // pre-populated for AlreadyExists / Observe
	createErr     error                                     // returned by CreateJob (AlreadyExists, Forbidden, transport)
	createdUID    string                                    // UID assigned to the stored created Job
	createMutator func(submitted *batchv1.Job) *batchv1.Job // mutates the CreateJob RESPONSE (admission fixture)
	getJobErr     error                                     // overrides GetJob (Forbidden, transport)
	deleteErr     error
	deleted       []string // names of DeleteJob calls that attempted
}

func (f *fakeK8s) GetExperiment(ctx context.Context, namespace, name string) (*experimentalpha4.SimulationExperiment, error) {
	if f.expErr != nil {
		return nil, f.expErr
	}
	if f.exp == nil {
		return nil, apierrors.NewNotFound(alpha4GR("simulationexperiments"), name)
	}
	return f.exp.DeepCopy(), nil
}

func (f *fakeK8s) GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	if f.secretErr != nil {
		return nil, f.secretErr
	}
	if f.secret == nil {
		return nil, apierrors.NewNotFound(corev1.Resource("secrets"), name)
	}
	return f.secret.DeepCopy(), nil
}

func (f *fakeK8s) GetServiceAccount(ctx context.Context, namespace, name string) (*corev1.ServiceAccount, error) {
	if f.saErr != nil {
		return nil, f.saErr
	}
	return &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}, nil
}

func (f *fakeK8s) CreateJob(ctx context.Context, job *batchv1.Job) (*batchv1.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return nil, f.createErr
	}
	key := job.Namespace + "/" + job.Name
	if f.jobs == nil {
		f.jobs = map[string]*batchv1.Job{}
	}
	stored := job.DeepCopy()
	if f.createdUID != "" {
		stored.UID = types.UID(f.createdUID)
	}
	f.jobs[key] = stored
	resp := stored.DeepCopy()
	if f.createMutator != nil {
		resp = f.createMutator(resp)
	}
	return resp, nil
}

func (f *fakeK8s) GetJob(ctx context.Context, namespace, name string) (*batchv1.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getJobErr != nil {
		return nil, f.getJobErr
	}
	key := namespace + "/" + name
	if j, ok := f.jobs[key]; ok {
		return j.DeepCopy(), nil
	}
	return nil, apierrors.NewNotFound(batchv1.Resource("jobs"), name)
}

func (f *fakeK8s) DeleteJob(ctx context.Context, namespace, name string, uid types.UID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, name)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if f.jobs != nil {
		key := namespace + "/" + name
		if j, ok := f.jobs[key]; ok {
			if uid != "" && j.UID != uid {
				return apierrors.NewConflict(batchv1.Resource("jobs"), name, errors.New("uid mismatch"))
			}
			delete(f.jobs, key)
		} else {
			return apierrors.NewNotFound(batchv1.Resource("jobs"), name)
		}
	}
	return nil
}

func startReq(scenarioID, attempt, reps int, image string) scheduler.RunnerStartRequest {
	if image == "" {
		image = validDigest
	}
	return scheduler.RunnerStartRequest{
		Namespace: ns, ExperimentName: expName,
		ScenarioID: scenarioID, TranslationAttempt: attempt,
		NumberOfReps: reps, ContainerImage: image,
	}
}

func observeReq(scenarioID, attempt, reps int) scheduler.ObservationRequest {
	return scheduler.ObservationRequest{
		Namespace: ns, ExperimentName: expName,
		ScenarioID: scenarioID, TranslationAttempt: attempt, NumberOfReps: reps,
	}
}

func happyFake() *fakeK8s {
	return &fakeK8s{
		exp:        newExperiment("InProgress"),
		secret:     validSecret(),
		createdUID: "server-uid-new",
	}
}

// TestStartSuccessfulCreateIsAuthoritative proves the adapter returns Created and
// retains ONLY the returned UID, ignoring admission-mutated namespace, name,
// owner references, reserved labels, and specification values.
func TestStartSuccessfulCreateIsAuthoritative(t *testing.T) {
	f := happyFake()
	// Mutate the create response to change every identity and spec field; the
	// adapter must ignore all of them and keep only the UID.
	f.createMutator = func(submitted *batchv1.Job) *batchv1.Job {
		mutated := submitted.DeepCopy()
		mutated.Namespace = "mutated-ns"
		mutated.Name = "mutated-name"
		mutated.Labels = map[string]string{"admission": "changed"}
		mutated.OwnerReferences = []metav1.OwnerReference{{UID: "wrong"}}
		mutated.Spec.Completions = int32Ptr(999)
		mutated.UID = "admission-returned-uid-xyz"
		return mutated
	}
	a := NewAdapter(f)

	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartCreated {
		t.Fatalf("outcome = %s, want created", res.Outcome)
	}
	if res.JobName != jobName(7, 1) {
		t.Fatalf("JobName = %q, want %q", res.JobName, jobName(7, 1))
	}
	if res.CreatedJobUID != "admission-returned-uid-xyz" {
		t.Fatalf("CreatedJobUID = %q, want admission-returned-uid-xyz (the only retained field)", res.CreatedJobUID)
	}
}

func TestStartAlreadyExistsConfirmed(t *testing.T) {
	f := happyFake()
	f.jobs = map[string]*batchv1.Job{"ns-x/" + jobName(7, 1): ownedJob(7, 1)}
	f.createErr = apierrors.NewAlreadyExists(batchv1.Resource("jobs"), jobName(7, 1))
	a := NewAdapter(f)

	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartConfirmed {
		t.Fatalf("outcome = %s, want confirmed", res.Outcome)
	}
	if res.JobName != jobName(7, 1) {
		t.Fatalf("JobName = %q, want %q", res.JobName, jobName(7, 1))
	}
}

func TestStartAlreadyExistsCollisionWrongOwner(t *testing.T) {
	f := happyFake()
	colliding := ownedJob(7, 1)
	colliding.OwnerReferences[0].UID = "different-uid"
	f.jobs = map[string]*batchv1.Job{"ns-x/" + jobName(7, 1): colliding}
	f.createErr = apierrors.NewAlreadyExists(batchv1.Resource("jobs"), jobName(7, 1))
	a := NewAdapter(f)

	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartCollision {
		t.Fatalf("outcome = %s, want collision", res.Outcome)
	}
}

func TestStartAlreadyExistsCollisionWrongLabel(t *testing.T) {
	f := happyFake()
	colliding := ownedJob(7, 1)
	colliding.Labels["experiment.cbse.terministic.de/scenario-id"] = "999"
	// Adjust the name to match the mutated scenario-id label so the job name
	// still reproduces (s9-a1) — but the requested scenario is 7, so the label
	// mismatch vs the deterministic name is the collision. Use a name that does
	// NOT reproduce the label scenario-id to trigger the name-reproduce check.
	colliding.Name = jobName(7, 1) // keep deterministic get target
	f.jobs = map[string]*batchv1.Job{"ns-x/" + jobName(7, 1): colliding}
	f.createErr = apierrors.NewAlreadyExists(batchv1.Resource("jobs"), jobName(7, 1))
	a := NewAdapter(f)

	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartCollision {
		t.Fatalf("outcome = %s, want collision (wrong scenario-id label)", res.Outcome)
	}
}

func TestStartForbiddenOnCreate(t *testing.T) {
	f := happyFake()
	f.createErr = apierrors.NewForbidden(batchv1.Resource("jobs"), jobName(7, 1), errors.New("rbac"))
	a := NewAdapter(f)
	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartForbidden {
		t.Fatalf("outcome = %s, want forbidden", res.Outcome)
	}
}

func TestStartForbiddenOnGetExperiment(t *testing.T) {
	f := happyFake()
	f.expErr = apierrors.NewForbidden(alpha4GR("simulationexperiments"), expName, errors.New("rbac"))
	a := NewAdapter(f)
	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartForbidden {
		t.Fatalf("outcome = %s, want forbidden", res.Outcome)
	}
}

func TestStartNotFoundExperimentIsPermanent(t *testing.T) {
	f := happyFake()
	f.exp = nil // NotFound
	a := NewAdapter(f)
	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartProjectionInvalid {
		t.Fatalf("outcome = %s, want projection-invalid (missing experiment outside finalizer lifecycle)", res.Outcome)
	}
}

func TestStartTerminalExperimentIsNoOp(t *testing.T) {
	for _, phase := range []string{"Error", "Failed", "Completed"} {
		f := happyFake()
		f.exp = newExperiment(phase)
		a := NewAdapter(f)
		res := a.Start(context.Background(), startReq(7, 1, 4, ""))
		if res.Outcome != scheduler.RunnerStartExperimentTerminal {
			t.Fatalf("phase %s: outcome = %s, want experiment-terminal", phase, res.Outcome)
		}
	}
}

func TestStartDeletingExperimentIsNoOp(t *testing.T) {
	f := happyFake()
	f.exp = newExperiment("InProgress")
	ts := metav1.Now()
	f.exp.DeletionTimestamp = &ts
	a := NewAdapter(f)
	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartExperimentTerminal {
		t.Fatalf("outcome = %s, want experiment-terminal (deleting)", res.Outcome)
	}
}

func TestStartUnavailableExperimentIsTransient(t *testing.T) {
	for _, phase := range []string{"Pending", "Provisioning", ""} {
		f := happyFake()
		f.exp = newExperiment(phase)
		a := NewAdapter(f)
		res := a.Start(context.Background(), startReq(7, 1, 4, ""))
		if res.Outcome != scheduler.RunnerStartTransient {
			t.Fatalf("phase %q: outcome = %s, want transient", phase, res.Outcome)
		}
	}
}

func TestStartInvalidDigestIsPermanent(t *testing.T) {
	f := happyFake()
	a := NewAdapter(f)
	res := a.Start(context.Background(), startReq(7, 1, 4, "registry.example.com/runner:latest"))
	if res.Outcome != scheduler.RunnerStartProjectionInvalid {
		t.Fatalf("outcome = %s, want projection-invalid (non-digest image)", res.Outcome)
	}
}

func TestStartRepositoryMismatchIsPermanent(t *testing.T) {
	f := happyFake()
	f.exp = newExperiment("InProgress")
	f.exp.Spec.Translator.Repository = "other.example.com/runner"
	a := NewAdapter(f)
	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartProjectionInvalid {
		t.Fatalf("outcome = %s, want projection-invalid (repository mismatch)", res.Outcome)
	}
}

func TestStartMissingSecretIsPermanent(t *testing.T) {
	f := happyFake()
	f.secret = nil // NotFound
	a := NewAdapter(f)
	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartProjectionInvalid {
		t.Fatalf("outcome = %s, want projection-invalid (missing pull secret)", res.Outcome)
	}
}

func TestStartWrongTypeSecretIsPermanent(t *testing.T) {
	f := happyFake()
	f.secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cbse-registry-auth", Namespace: ns},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{".dockerconfigjson": validDockerConfig()},
	}
	a := NewAdapter(f)
	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartProjectionInvalid {
		t.Fatalf("outcome = %s, want projection-invalid (wrong-type secret)", res.Outcome)
	}
}

// TestStartReplacedSecretCredentialsAreRevalidated covers S01-D06: a Secret that
// still exists and is well-formed but whose basic credentials no longer resolve
// for the runner image's registry authority (e.g. replaced after provisioning)
// is revalidated by SM and fails permanently without logging credentials or
// attempting a Job create.
func TestStartReplacedSecretCredentialsAreRevalidated(t *testing.T) {
	f := happyFake()
	// Well-formed dockerconfigjson for a DIFFERENT registry authority; the
	// runner image lives at registry.example.com, so ResolveDockerAuth fails.
	// Use distinctive credential values so the assertion can prove they are not
	// echoed back in the failure reason.
	replaced := []byte(`{"auths":{"other.example.com":{"username":"leaked-user","password":"leaked-pass","auth":"bGVha2VkLXVzZXI6bGVha2VkLXBhc3M="}}}`)
	f.secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cbse-registry-auth", Namespace: ns},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{".dockerconfigjson": replaced},
	}
	a := NewAdapter(f)
	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartProjectionInvalid {
		t.Fatalf("outcome = %s, want projection-invalid (replaced secret credentials)", res.Outcome)
	}
	if res.Err == nil {
		t.Fatal("expected an error describing the registry-auth failure")
	}
	// The failure reason must not leak the credential material.
	for _, leak := range []string{"leaked-user", "leaked-pass", "bGVha2Vk"} {
		if strings.Contains(res.Err.Error(), leak) {
			t.Fatalf("credential material leaked in error: %v (contained %q)", res.Err, leak)
		}
	}
	// No Job create must be attempted for a permanent validation failure.
	if len(f.jobs) != 0 {
		t.Fatalf("stored %d jobs, want 0 (no create on revalidation failure)", len(f.jobs))
	}
}

func TestStartMissingServiceAccountIsPermanent(t *testing.T) {
	f := happyFake()
	f.saErr = apierrors.NewNotFound(corev1.Resource("serviceaccounts"), saName)
	a := NewAdapter(f)
	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartProjectionInvalid {
		t.Fatalf("outcome = %s, want projection-invalid (missing SA)", res.Outcome)
	}
}

func TestStartRepsOutOfRangeIsPermanent(t *testing.T) {
	f := happyFake()
	a := NewAdapter(f)
	res := a.Start(context.Background(), startReq(7, 1, 100001, ""))
	if res.Outcome != scheduler.RunnerStartProjectionInvalid {
		t.Fatalf("outcome = %s, want projection-invalid (reps out of range)", res.Outcome)
	}
}

func TestStartTransportOnCreateIsTransient(t *testing.T) {
	f := happyFake()
	f.createErr = errors.New("connection refused")
	a := NewAdapter(f)
	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartTransient {
		t.Fatalf("outcome = %s, want transient (transport)", res.Outcome)
	}
}

func TestStartForbiddenOnGetSecretIsPermanent(t *testing.T) {
	f := happyFake()
	f.secretErr = apierrors.NewForbidden(corev1.Resource("secrets"), "cbse-registry-auth", errors.New("rbac"))
	a := NewAdapter(f)
	res := a.Start(context.Background(), startReq(7, 1, 4, ""))
	if res.Outcome != scheduler.RunnerStartForbidden {
		t.Fatalf("outcome = %s, want forbidden", res.Outcome)
	}
}

// --- DeleteCreated ---

func TestDeleteCreatedSuccess(t *testing.T) {
	f := happyFake()
	f.jobs = map[string]*batchv1.Job{"ns-x/" + jobName(7, 1): ownedJob(7, 1)}
	a := NewAdapter(f)
	if err := a.DeleteCreated(context.Background(), ns, jobName(7, 1), "job-uid-existing"); err != nil {
		t.Fatalf("DeleteCreated: %v", err)
	}
	if len(f.deleted) != 1 || f.deleted[0] != jobName(7, 1) {
		t.Fatalf("deleted = %v, want [%s]", f.deleted, jobName(7, 1))
	}
}

func TestDeleteCreatedNotFoundIsNoOp(t *testing.T) {
	f := happyFake()
	a := NewAdapter(f)
	if err := a.DeleteCreated(context.Background(), ns, jobName(7, 1), "any"); err != nil {
		t.Fatalf("DeleteCreated NotFound: %v", err)
	}
}

func TestDeleteCreatedUIDMismatchIsNoOp(t *testing.T) {
	f := happyFake()
	f.jobs = map[string]*batchv1.Job{"ns-x/" + jobName(7, 1): ownedJob(7, 1)}
	a := NewAdapter(f)
	if err := a.DeleteCreated(context.Background(), ns, jobName(7, 1), "stale-uid"); err != nil {
		t.Fatalf("DeleteCreated UID mismatch: %v", err)
	}
	// Job must remain (not deleted) because the UID precondition did not match.
	if _, ok := f.jobs["ns-x/"+jobName(7, 1)]; !ok {
		t.Fatal("UID-mismatch delete removed the Job; it should remain")
	}
}

func TestDeleteCreatedForbiddenReturnsError(t *testing.T) {
	f := happyFake()
	f.deleteErr = apierrors.NewForbidden(batchv1.Resource("jobs"), jobName(7, 1), errors.New("rbac"))
	a := NewAdapter(f)
	if err := a.DeleteCreated(context.Background(), ns, jobName(7, 1), "any"); err == nil {
		t.Fatal("DeleteCreated Forbidden: expected error for cleanup retry cadence")
	}
}

// --- Observe ---

func setConditions(job *batchv1.Job, conds ...batchv1.JobCondition) {
	job.Status.Conditions = conds
}

func trueCond(ct batchv1.JobConditionType) batchv1.JobCondition {
	return batchv1.JobCondition{Type: ct, Status: corev1.ConditionTrue}
}

func TestObserveCompleted(t *testing.T) {
	f := happyFake()
	j := ownedJob(7, 1)
	setConditions(j, trueCond(batchv1.JobComplete))
	j.Status.CompletedIndexes = "0-3"
	f.jobs = map[string]*batchv1.Job{"ns-x/" + jobName(7, 1): j}
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationCompleted {
		t.Fatalf("outcome = %s, want completed", res.Outcome)
	}
	if res.CompletedReps != 4 {
		t.Fatalf("CompletedReps = %d, want 4 (full count)", res.CompletedReps)
	}
}

func TestObserveFailedPreservesPartialCount(t *testing.T) {
	f := happyFake()
	j := ownedJob(7, 1)
	setConditions(j, trueCond(batchv1.JobFailed))
	j.Status.CompletedIndexes = "0-2" // 3 succeeded before failure
	f.jobs = map[string]*batchv1.Job{"ns-x/" + jobName(7, 1): j}
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationFailed {
		t.Fatalf("outcome = %s, want failed", res.Outcome)
	}
	if res.CompletedReps != 3 {
		t.Fatalf("CompletedReps = %d, want 3 (partial count)", res.CompletedReps)
	}
}

func TestObserveBothTerminalPrefersFailed(t *testing.T) {
	f := happyFake()
	j := ownedJob(7, 1)
	setConditions(j, trueCond(batchv1.JobComplete), trueCond(batchv1.JobFailed))
	j.Status.CompletedIndexes = "0-3"
	f.jobs = map[string]*batchv1.Job{"ns-x/" + jobName(7, 1): j}
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationFailed {
		t.Fatalf("outcome = %s, want failed (Failed wins over Complete)", res.Outcome)
	}
}

func TestObserveRunningRecordsPartialCount(t *testing.T) {
	f := happyFake()
	j := ownedJob(7, 1)
	j.Status.CompletedIndexes = "0-1" // 2 of 4 done, still running
	f.jobs = map[string]*batchv1.Job{"ns-x/" + jobName(7, 1): j}
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationRetry {
		t.Fatalf("outcome = %s, want retry (running)", res.Outcome)
	}
	if res.CompletedReps != 2 {
		t.Fatalf("CompletedReps = %d, want 2 (partial monotonic)", res.CompletedReps)
	}
}

func TestObserveMissingJobIsRetry(t *testing.T) {
	f := happyFake()
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationRetry {
		t.Fatalf("outcome = %s, want retry (missing job)", res.Outcome)
	}
	if res.CompletedReps != 0 {
		t.Fatalf("CompletedReps = %d, want 0", res.CompletedReps)
	}
}

func TestObserveCollision(t *testing.T) {
	f := happyFake()
	colliding := ownedJob(7, 1)
	colliding.OwnerReferences[0].UID = "different"
	f.jobs = map[string]*batchv1.Job{"ns-x/" + jobName(7, 1): colliding}
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationCollision {
		t.Fatalf("outcome = %s, want collision", res.Outcome)
	}
}

func TestObserveForbiddenOnGetJob(t *testing.T) {
	f := happyFake()
	f.getJobErr = apierrors.NewForbidden(batchv1.Resource("jobs"), jobName(7, 1), errors.New("rbac"))
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationForbidden {
		t.Fatalf("outcome = %s, want forbidden", res.Outcome)
	}
}

func TestObserveForbiddenOnGetExperiment(t *testing.T) {
	f := happyFake()
	f.expErr = apierrors.NewForbidden(alpha4GR("simulationexperiments"), expName, errors.New("rbac"))
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationForbidden {
		t.Fatalf("outcome = %s, want forbidden", res.Outcome)
	}
}

func TestObserveTerminalExperimentIsRetry(t *testing.T) {
	for _, phase := range []string{"Error", "Failed", "Completed"} {
		f := happyFake()
		f.exp = newExperiment(phase)
		a := NewAdapter(f)
		res := a.Observe(context.Background(), observeReq(7, 1, 4))
		if res.Outcome != scheduler.ObservationRetry {
			t.Fatalf("phase %s: outcome = %s, want retry (no-op)", phase, res.Outcome)
		}
	}
}

func TestObserveDeletingExperimentIsRetry(t *testing.T) {
	f := happyFake()
	f.exp = newExperiment("InProgress")
	ts := metav1.Now()
	f.exp.DeletionTimestamp = &ts
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationRetry {
		t.Fatalf("outcome = %s, want retry (deleting no-op)", res.Outcome)
	}
}

func TestObserveUnavailableExperimentIsRetry(t *testing.T) {
	f := happyFake()
	f.exp = newExperiment("Pending")
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationRetry {
		t.Fatalf("outcome = %s, want retry (unavailable)", res.Outcome)
	}
}

func TestObserveNotFoundExperimentIsRetry(t *testing.T) {
	f := happyFake()
	f.exp = nil
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationRetry {
		t.Fatalf("outcome = %s, want retry (experiment gone)", res.Outcome)
	}
}

func TestObserveMalformedCompletedIndexesFails(t *testing.T) {
	f := happyFake()
	j := ownedJob(7, 1)
	setConditions(j, trueCond(batchv1.JobFailed))
	j.Status.CompletedIndexes = "0-2,abc"
	f.jobs = map[string]*batchv1.Job{"ns-x/" + jobName(7, 1): j}
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationFailed {
		t.Fatalf("outcome = %s, want failed (malformed completedIndexes)", res.Outcome)
	}
	if res.CompletedReps != 0 {
		t.Fatalf("CompletedReps = %d, want 0 (preserve current on malformed)", res.CompletedReps)
	}
}

func TestObserveOutOfRangeIndexFails(t *testing.T) {
	f := happyFake()
	j := ownedJob(7, 1)
	j.Status.CompletedIndexes = "0-4" // max index is 3 for 4 reps
	f.jobs = map[string]*batchv1.Job{"ns-x/" + jobName(7, 1): j}
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationFailed {
		t.Fatalf("outcome = %s, want failed (out-of-range index)", res.Outcome)
	}
}

func TestObserveTransportOnGetJobIsRetry(t *testing.T) {
	f := happyFake()
	f.getJobErr = errors.New("timeout")
	a := NewAdapter(f)
	res := a.Observe(context.Background(), observeReq(7, 1, 4))
	if res.Outcome != scheduler.ObservationRetry {
		t.Fatalf("outcome = %s, want retry (transport)", res.Outcome)
	}
}

// --- parseCompletedIndexes unit tests ---

func TestParseCompletedIndexes(t *testing.T) {
	cases := []struct {
		in      string
		max     int
		want    int
		wantErr bool
	}{
		{"", 4, 0, false},
		{"0", 4, 1, false},
		{"3", 4, 1, false},
		{"0-3", 4, 4, false},
		{"0,2,4", 4, 3, false},
		{"0-1,3-4", 4, 4, false},
		{"1-1", 4, 1, false},
		// 100000 reps, full completion, no expansion.
		{"0-99999", 99999, 100000, false},
		// out of range
		{"4", 3, 0, true},
		{"0-4", 3, 0, true},
		// overlap / non-increasing
		{"1-2,2-3", 9, 0, true},
		{"2-3,0-1", 9, 0, true},
		{"1-3,1-2", 9, 0, true},
		// malformed
		{"abc", 9, 0, true},
		{"0-", 9, 0, true},
		{"-2", 9, 0, true},
		{"3-1", 9, 0, true}, // start > end
		{"0,,1", 9, 0, true},
		{" 0 ", 9, 0, true}, // whitespace inside index rejected
	}
	for _, c := range cases {
		got, err := parseCompletedIndexes(c.in, c.max)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseCompletedIndexes(%q, %d): expected error, got %d", c.in, c.max, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseCompletedIndexes(%q, %d): unexpected error: %v", c.in, c.max, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseCompletedIndexes(%q, %d) = %d, want %d", c.in, c.max, got, c.want)
		}
	}
}
