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

package alpha4

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/experiment-operator/internal/controller"
	"github.com/D4NS3U/cbse/experiment-operator/internal/dbendpoint"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const alpha4TestNamespace = "alpha4-ctrl-tests"

var (
	alpha4Client client.Client
	alpha4Env    *envtest.Environment
	alpha4Scheme *runtime.Scheme
)

func TestMain(m *testing.M) {
	s := runtime.NewScheme()
	if err := experimentalpha4.AddToScheme(s); err != nil {
		fmt.Fprintf(os.Stderr, "add alpha4 to scheme: %v\n", err)
		os.Exit(1)
	}
	if err := corev1.AddToScheme(s); err != nil {
		fmt.Fprintf(os.Stderr, "add core to scheme: %v\n", err)
		os.Exit(1)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		fmt.Fprintf(os.Stderr, "add apps to scheme: %v\n", err)
		os.Exit(1)
	}
	alpha4Scheme = s

	alpha4Env = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if assets := localEnvtestAssets(); assets != "" {
		alpha4Env.BinaryAssetsDirectory = assets
	}

	cfg, err := alpha4Env.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start alpha4 controller envtest: %v\n", err)
		os.Exit(1)
	}
	alpha4Client, err = client.New(cfg, client.Options{Scheme: s})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create alpha4 controller client: %v\n", err)
		_ = alpha4Env.Stop()
		os.Exit(1)
	}

	if err := alpha4Client.Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: alpha4TestNamespace},
	}); err != nil && !apierrors.IsAlreadyExists(err) {
		fmt.Fprintf(os.Stderr, "create alpha4 controller test namespace: %v\n", err)
		_ = alpha4Env.Stop()
		os.Exit(1)
	}

	code := m.Run()
	if err := alpha4Env.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "stop alpha4 controller envtest: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// localEnvtestAssets locates the envtest kube-apiserver/etcd binaries under the
// module's bin/k8s directory when KUBEBUILDER_ASSETS is unset, returning the
// absolute path or "" if it cannot be found.
func localEnvtestAssets() string {
	if os.Getenv("KUBEBUILDER_ASSETS") != "" {
		return ""
	}
	entries, err := os.ReadDir(filepath.Join("..", "..", "..", "bin", "k8s"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if assets, err := filepath.Abs(filepath.Join("..", "..", "..", "bin", "k8s", entry.Name())); err == nil {
				return assets
			}
		}
	}
	return ""
}

// --- fixtures ----------------------------------------------------------------

const shaA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// validExperiment builds a SimulationExperiment fixture in the test namespace
// with a digest-pinned image-based detail DB, a host-based result DB, a fully
// specified translator that passes provisioning validation, and a fully
// specified post-processing service that passes provisioning validation.
func validExperiment(name string) *experimentalpha4.SimulationExperiment {
	return &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: alpha4TestNamespace},
		Spec: experimentalpha4.SimulationExperimentSpec{
			DetailDatabase: experimentalpha4.DatabaseSpec{
				Image:    "registry.unibw.de/i31bdase/cbse-test/detaildb@sha256:" + shaA,
				DBName:   "detail",
				User:     "du",
				Password: "dp",
				Port:     5432,
			},
			ResultDatabase: experimentalpha4.DatabaseSpec{
				Host:     "resultdb.example.com",
				DBName:   "result",
				User:     "ru",
				Password: "rp",
				Port:     5432,
			},
			Translator: experimentalpha4.TranslatorSpec{
				Image:                 "registry.unibw.de/i31bdase/cbse-test/translator@sha256:" + shaA,
				Repository:            "registry.unibw.de/i31bdase/cbse-test-runner",
				BaseImage:             "registry.unibw.de/i31bdase/cbse-test/base@sha256:" + shaA,
				BuilderImage:          "registry.unibw.de/i31bdase/cbse-test/buildkit@sha256:" + shaA,
				RegistryAuthSecretRef: corev1.LocalObjectReference{Name: "cbse-registry-auth"},
				Port:                  8080,
			},
			PostProcessingService: experimentalpha4.PostProcessingSpec{
				Image:       "registry.unibw.de/i31bdase/cbse-test/pps@sha256:" + shaA,
				ServiceType: experimentalpha4.ServiceTypeClusterIP,
				Port:        8081,
				Command:     []string{"/usr/local/bin/pps"},
				Args:        []string{"--evaluate"},
			},
		},
	}
}

// goodRegistrySecret is the valid cbse-registry-auth Secret with basic auth for
// registry.unibw.de.
func goodRegistrySecret() *corev1.Secret {
	return dockerConfigSecret(map[string]string{"registry.unibw.de": "robot:secret"}, nil, "")
}

// dockerConfigSecret builds a cbse-registry-auth Secret from raw auths, optional
// credHelpers, and an optional credsStore. When auths is nil and both helpers
// fields are empty, the JSON has no "auths" key.
func dockerConfigSecret(auths map[string]string, credHelpers map[string]string, credsStore string) *corev1.Secret {
	type entry struct {
		Auth          string `json:"auth,omitempty"`
		Username      string `json:"username,omitempty"`
		Password      string `json:"password,omitempty"`
		IdentityToken string `json:"identitytoken,omitempty"`
	}
	doc := map[string]any{}
	if auths != nil {
		amap := map[string]entry{}
		for host, up := range auths {
			amap[host] = entry{Auth: base64.StdEncoding.EncodeToString([]byte(up))}
		}
		doc["auths"] = amap
	}
	if credHelpers != nil {
		doc["credHelpers"] = credHelpers
	}
	if credsStore != "" {
		doc["credsStore"] = credsStore
	}
	raw, _ := json.Marshal(doc)
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cbse-registry-auth", Namespace: alpha4TestNamespace},
		Type:       corev1.SecretType("kubernetes.io/dockerconfigjson"),
		Data:       map[string][]byte{".dockerconfigjson": raw},
	}
}

// ensureRegistrySecret deletes any existing cbse-registry-auth Secret and, when
// sec is non-nil, (re)creates it in the test namespace.
func ensureRegistrySecret(t *testing.T, sec *corev1.Secret) {
	t.Helper()
	ctx := context.Background()
	_ = alpha4Client.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cbse-registry-auth", Namespace: alpha4TestNamespace}})
	if sec == nil {
		return
	}
	sec.Name = "cbse-registry-auth"
	sec.Namespace = alpha4TestNamespace
	if err := alpha4Client.Create(ctx, sec); err != nil {
		t.Fatalf("create registry secret: %v", err)
	}
}

// deleteRegistrySecret removes the cbse-registry-auth Secret from the test
// namespace.
func deleteRegistrySecret(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_ = alpha4Client.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cbse-registry-auth", Namespace: alpha4TestNamespace}})
}

// newReconciler builds an Alpha4SimulationExperimentReconciler against the
// shared envtest client and scheme, injecting probe as the DB availability
// probe (nil uses the default pgx probe). The watchdog knobs keep the
// production defaults (zero values select ~60s spacing and 3 counted
// retries), so the tests that use this constructor exercise the compiled-in
// production behavior.
func newReconciler(probe func(ctx context.Context, ep dbendpoint.Endpoint) error) *controller.Alpha4SimulationExperimentReconciler {
	return &controller.Alpha4SimulationExperimentReconciler{
		Client:  alpha4Client,
		Scheme:  alpha4Scheme,
		DBProbe: probe,
	}
}

// newReconcilerWithWatchdog builds an Alpha4SimulationExperimentReconciler
// against the shared envtest client and scheme, injecting probe as the DB
// availability probe (nil uses the default pgx probe) and the bounded-retry
// watchdog knobs (ruling R): envtest injects milliseconds so the counted
// retries are observable within the drive loop's time budget.
func newReconcilerWithWatchdog(probe func(ctx context.Context, ep dbendpoint.Endpoint) error, interval time.Duration, maxRetries int) *controller.Alpha4SimulationExperimentReconciler {
	return &controller.Alpha4SimulationExperimentReconciler{
		Client:                 alpha4Client,
		Scheme:                 alpha4Scheme,
		DBProbe:                probe,
		ReadinessRetryInterval: interval,
		ReadinessMaxRetries:    maxRetries,
	}
}

// createExperiment creates exp in the test namespace and returns its
// namespaced name, failing the test on a create error.
func createExperiment(t *testing.T, exp *experimentalpha4.SimulationExperiment) types.NamespacedName {
	t.Helper()
	ctx := context.Background()
	if err := alpha4Client.Create(ctx, exp); err != nil {
		t.Fatalf("create experiment %q: %v", exp.Name, err)
	}
	return types.NamespacedName{Name: exp.Name, Namespace: exp.Namespace}
}

// drive repeatedly reconciles until the experiment reaches a terminal phase
// (InProgress or Error). While Provisioning, it simulates Translator and PPS
// Deployment readiness so the readiness check can progress (the G-ruled gate
// covers the PPS in addition to the databases and the translator). It
// returns the final phase, or the last observed phase if maxIter is
// exhausted.
func drive(t *testing.T, r *controller.Alpha4SimulationExperimentReconciler, key types.NamespacedName, maxIter int) string {
	t.Helper()
	ctx := context.Background()
	var last string
	for i := 0; i < maxIter; i++ {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("reconcile %d for %q: %v", i, key.Name, err)
		}
		inst := &experimentalpha4.SimulationExperiment{}
		if err := alpha4Client.Get(ctx, key, inst); err != nil {
			t.Fatalf("get experiment %q after reconcile %d: %v", key.Name, i, err)
		}
		last = inst.Status.Phase
		if last == "InProgress" || last == "Error" {
			return last
		}
		if last == "Provisioning" {
			markTranslatorReady(t, key)
			markPPSReady(t, key)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return last
}

// markTranslatorReady flips the experiment's Translator Deployment status to one
// ready replica so the readiness check can advance. It is a no-op if the
// Deployment is absent or already ready.
func markTranslatorReady(t *testing.T, key types.NamespacedName) {
	t.Helper()
	ctx := context.Background()
	dep := &appsv1.Deployment{}
	if err := alpha4Client.Get(ctx, types.NamespacedName{Name: key.Name + "-translator", Namespace: key.Namespace}, dep); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		t.Fatalf("get translator deployment: %v", err)
	}
	if dep.Status.ReadyReplicas >= 1 {
		return
	}
	dep.Status.Replicas = 1
	dep.Status.ReadyReplicas = 1
	dep.Status.AvailableReplicas = 1
	dep.Status.UpdatedReplicas = 1
	if err := alpha4Client.Status().Update(ctx, dep); err != nil {
		t.Fatalf("update translator deployment status: %v", err)
	}
}

// markPPSReady flips the experiment's PPS Deployment status to one ready
// replica so the G-ruled readiness gate can advance. It is a no-op if the
// Deployment is absent or already ready.
func markPPSReady(t *testing.T, key types.NamespacedName) {
	t.Helper()
	ctx := context.Background()
	dep := &appsv1.Deployment{}
	if err := alpha4Client.Get(ctx, types.NamespacedName{Name: key.Name + "-pps", Namespace: key.Namespace}, dep); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		t.Fatalf("get pps deployment: %v", err)
	}
	if dep.Status.ReadyReplicas >= 1 {
		return
	}
	dep.Status.Replicas = 1
	dep.Status.ReadyReplicas = 1
	dep.Status.AvailableReplicas = 1
	dep.Status.UpdatedReplicas = 1
	if err := alpha4Client.Status().Update(ctx, dep); err != nil {
		t.Fatalf("update pps deployment status: %v", err)
	}
}

// keepDeploymentNotReady sets the named component Deployment's status to one
// observed replica with zero ready replicas and an Available=False condition
// carrying the given reason, simulating a component that cannot become
// ready. It is a no-op if the Deployment is absent.
func keepDeploymentNotReady(t *testing.T, name, reason string) {
	t.Helper()
	ctx := context.Background()
	dep := &appsv1.Deployment{}
	if err := alpha4Client.Get(ctx, types.NamespacedName{Name: name, Namespace: alpha4TestNamespace}, dep); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		t.Fatalf("get deployment %q: %v", name, err)
	}
	if dep.Status.ReadyReplicas >= 1 {
		t.Fatalf("deployment %q already has %d ready replicas", name, dep.Status.ReadyReplicas)
	}
	dep.Status.Replicas = 1
	dep.Status.ReadyReplicas = 0
	dep.Status.AvailableReplicas = 0
	dep.Status.UpdatedReplicas = 1
	dep.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:    appsv1.DeploymentAvailable,
		Status:  corev1.ConditionFalse,
		Reason:  reason,
		Message: "simulated not-ready condition for the readiness watchdog test",
	}}
	if err := alpha4Client.Status().Update(ctx, dep); err != nil {
		t.Fatalf("update deployment %q status: %v", name, err)
	}
}

// getExperiment fetches the experiment for key, failing the test on error.
func getExperiment(t *testing.T, key types.NamespacedName) *experimentalpha4.SimulationExperiment {
	t.Helper()
	inst := &experimentalpha4.SimulationExperiment{}
	if err := alpha4Client.Get(context.Background(), key, inst); err != nil {
		t.Fatalf("get experiment %q: %v", key.Name, err)
	}
	return inst
}

// mustExist fails the test unless the named object exists in the test namespace.
func mustExist(t *testing.T, obj client.Object, name string) {
	t.Helper()
	if err := alpha4Client.Get(context.Background(), types.NamespacedName{Name: name, Namespace: alpha4TestNamespace}, obj); err != nil {
		t.Fatalf("expected %T %q to exist: %v", obj, name, err)
	}
}

// mustNotExist fails the test unless the named object is absent from the test
// namespace.
func mustNotExist(t *testing.T, obj client.Object, name string) {
	t.Helper()
	err := alpha4Client.Get(context.Background(), types.NamespacedName{Name: name, Namespace: alpha4TestNamespace}, obj)
	if err == nil {
		t.Fatalf("expected %T %q to be absent, but it exists", obj, name)
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("get %T %q: %v", obj, name, err)
	}
}

// containerByName returns the container named name from dep, failing the test
// if it is absent.
func containerByName(t *testing.T, dep *appsv1.Deployment, name string) corev1.Container {
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("container %q not found in deployment %q", name, dep.Name)
	return corev1.Container{}
}

// hasVolume reports whether dep declares a volume named name.
func hasVolume(dep *appsv1.Deployment, name string) bool {
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == name {
			return true
		}
	}
	return false
}

// hasMount reports whether container c mounts the volume name with the given
// read-only flag.
func hasMount(c corev1.Container, name string, readOnly bool) bool {
	for _, m := range c.VolumeMounts {
		if m.Name == name && m.ReadOnly == readOnly {
			return true
		}
	}
	return false
}

// --- tests -------------------------------------------------------------------

func TestAlpha4HappyPathProvisioning(t *testing.T) {
	key := types.NamespacedName{Name: "exp-happy", Namespace: alpha4TestNamespace}
	ensureRegistrySecret(t, goodRegistrySecret())
	key = createExperiment(t, validExperiment("exp-happy"))

	var probeCalls int
	r := newReconciler(func(_ context.Context, _ dbendpoint.Endpoint) error {
		probeCalls++
		return nil
	})
	if phase := drive(t, r, key, 30); phase != "InProgress" {
		t.Fatalf("phase = %q, want InProgress", phase)
	}
	if probeCalls != 2 {
		t.Fatalf("availability probe calls = %d, want exactly 2 (one per database)", probeCalls)
	}

	// Image-form database (detail): Deployment + Service + 5-key connection
	// Secret, with exactly one pull-Secret and no registry Secret volume/mount.
	detailDep := &appsv1.Deployment{}
	mustExist(t, detailDep, "exp-happy-detaildb")
	if len(detailDep.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("detail DB Deployment has %d containers, want 1", len(detailDep.Spec.Template.Spec.Containers))
	}
	if got := detailDep.Spec.Template.Spec.Containers[0].Image; !strings.Contains(got, "detaildb@sha256:") {
		t.Fatalf("detail DB container image = %q", got)
	}
	if len(detailDep.Spec.Template.Spec.ImagePullSecrets) != 1 ||
		detailDep.Spec.Template.Spec.ImagePullSecrets[0].Name != "cbse-registry-auth" {
		t.Fatalf("detail DB imagePullSecrets = %#v, want exactly [cbse-registry-auth]", detailDep.Spec.Template.Spec.ImagePullSecrets)
	}
	for _, v := range detailDep.Spec.Template.Spec.Volumes {
		if v.Secret != nil && v.Secret.SecretName == "cbse-registry-auth" {
			t.Fatalf("image-form DB Deployment must not mount the registry Secret as a volume: %#v", v)
		}
	}
	for _, m := range detailDep.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.Name == "registry-auth" {
			t.Fatalf("image-form DB container must not mount the registry Secret: %#v", m)
		}
	}
	if c := detailDep.Spec.Template.Spec.Containers[0]; len(c.Ports) != 1 || c.Ports[0].ContainerPort != 5432 {
		t.Fatalf("detail DB container ports = %#v, want [{5432}]", c.Ports)
	}
	detailSvc := &corev1.Service{}
	mustExist(t, detailSvc, "exp-happy-detaildb-svc")
	if detailSvc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Fatalf("detail DB Service type = %q, want ClusterIP", detailSvc.Spec.Type)
	}
	detailSct := &corev1.Secret{}
	mustExist(t, detailSct, "exp-happy-detaildb-sct")
	wantHost := "exp-happy-detaildb-svc." + alpha4TestNamespace + ".svc.cluster.local"
	if got := string(detailSct.Data["host"]); got != wantHost {
		t.Fatalf("detail connection Secret host = %q, want %q", got, wantHost)
	}
	for _, k := range []string{"port", "dbname", "user", "password"} {
		if len(detailSct.Data[k]) == 0 {
			t.Fatalf("detail connection Secret missing key %q", k)
		}
	}
	if got := string(detailSct.Data["port"]); got != "5432" {
		t.Fatalf("detail connection Secret port = %q, want 5432", got)
	}

	// Host-form database (result): connection Secret only; no Deployment/Service.
	mustNotExist(t, &appsv1.Deployment{}, "exp-happy-resultdb")
	mustNotExist(t, &corev1.Service{}, "exp-happy-resultdb-svc")
	resultSct := &corev1.Secret{}
	mustExist(t, resultSct, "exp-happy-resultdb-sct")
	if got := string(resultSct.Data["host"]); got != "resultdb.example.com" {
		t.Fatalf("result connection Secret host = %q, want resultdb.example.com", got)
	}
	for _, k := range []string{"port", "dbname", "user", "password"} {
		if len(resultSct.Data[k]) == 0 {
			t.Fatalf("result connection Secret missing key %q", k)
		}
	}

	// Translator Deployment: two containers, rootless BuildKit, shared volumes.
	transDep := &appsv1.Deployment{}
	mustExist(t, transDep, "exp-happy-translator")
	if len(transDep.Spec.Template.Spec.Containers) != 2 {
		t.Fatalf("translator Deployment has %d containers, want 2", len(transDep.Spec.Template.Spec.Containers))
	}
	if got := []string{transDep.Spec.Template.Spec.Containers[0].Name, transDep.Spec.Template.Spec.Containers[1].Name}; got[0] != "translator" || got[1] != "buildkit" {
		t.Fatalf("translator container order = %#v, want [translator, buildkit]", got)
	}
	if transDep.Spec.Template.Spec.SecurityContext == nil ||
		transDep.Spec.Template.Spec.SecurityContext.FSGroup == nil ||
		*transDep.Spec.Template.Spec.SecurityContext.FSGroup != 1000 {
		t.Fatalf("translator Pod fsGroup = %#v, want 1000", transDep.Spec.Template.Spec.SecurityContext)
	}
	if transDep.Spec.Template.Spec.HostUsers == nil || *transDep.Spec.Template.Spec.HostUsers {
		t.Fatalf("translator Pod hostUsers = %#v, want false (per-Pod user namespace for rootless BuildKit)", transDep.Spec.Template.Spec.HostUsers)
	}

	tr := containerByName(t, transDep, "translator")
	if tr.SecurityContext == nil || tr.SecurityContext.RunAsUser == nil || *tr.SecurityContext.RunAsUser != 1000 {
		t.Fatalf("translator RunAsUser = %#v, want 1000", tr.SecurityContext)
	}
	if tr.SecurityContext.RunAsNonRoot == nil || !*tr.SecurityContext.RunAsNonRoot {
		t.Fatalf("translator RunAsNonRoot = %#v, want true", tr.SecurityContext.RunAsNonRoot)
	}
	if tr.SecurityContext.AllowPrivilegeEscalation == nil || *tr.SecurityContext.AllowPrivilegeEscalation {
		t.Fatalf("translator AllowPrivilegeEscalation = %#v, want false", tr.SecurityContext.AllowPrivilegeEscalation)
	}
	if tr.SecurityContext.SeccompProfile == nil || tr.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("translator SeccompProfile = %#v, want RuntimeDefault", tr.SecurityContext.SeccompProfile)
	}
	if !hasMount(tr, "registry-auth", true) || !hasMount(tr, "detaildb-connection", true) || !hasMount(tr, "resultdb-connection", true) {
		t.Fatalf("translator missing required read-only mounts: %+v", tr.VolumeMounts)
	}

	bk := containerByName(t, transDep, "buildkit")
	// The rootless moby/buildkit:*-rootless sidecar runs as the mapped root
	// (UID 0) inside the Pod's user namespace (hostUsers=false), so RunAsNonRoot
	// is false rather than the restricted-profile default.
	if bk.SecurityContext == nil || bk.SecurityContext.RunAsUser == nil || *bk.SecurityContext.RunAsUser != 0 {
		t.Fatalf("buildkit RunAsUser = %#v, want 0 (mapped root in the Pod user namespace)", bk.SecurityContext)
	}
	if bk.SecurityContext.RunAsNonRoot == nil || *bk.SecurityContext.RunAsNonRoot {
		t.Fatalf("buildkit RunAsNonRoot = %#v, want false", bk.SecurityContext)
	}
	if bk.SecurityContext.Privileged == nil || *bk.SecurityContext.Privileged {
		t.Fatalf("buildkit Privileged = %#v, want false", bk.SecurityContext.Privileged)
	}
	if bk.SecurityContext.AllowPrivilegeEscalation == nil || *bk.SecurityContext.AllowPrivilegeEscalation {
		t.Fatalf("buildkit AllowPrivilegeEscalation = %#v, want false", bk.SecurityContext.AllowPrivilegeEscalation)
	}
	if bk.SecurityContext.SeccompProfile == nil || bk.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeUnconfined {
		t.Fatalf("buildkit SeccompProfile = %#v, want Unconfined", bk.SecurityContext.SeccompProfile)
	}
	if bk.SecurityContext.AppArmorProfile == nil || bk.SecurityContext.AppArmorProfile.Type != corev1.AppArmorProfileTypeUnconfined {
		t.Fatalf("buildkit AppArmorProfile = %#v, want Unconfined", bk.SecurityContext.AppArmorProfile)
	}
	if got := bk.Command; len(got) != 11 || got[0] != "buildkitd" || got[1] != "--rootless" || got[2] != "--oci-worker-snapshotter" || got[3] != "overlayfs" ||
		got[4] != "--addr" || got[5] != "unix:///run/buildkit/buildkitd.sock" || got[6] != "--root" || got[7] != "/run/buildkit/data" ||
		got[8] != "--group" || got[9] != "1000" || got[10] != "--oci-worker-no-process-sandbox" {
		t.Fatalf("buildkit command = %#v, want rootless buildkitd with --oci-worker-snapshotter overlayfs/--root/--group/--oci-worker-no-process-sandbox", got)
	}
	// buildkitd runs as the mapped root (UID 0) but with primary GID 1000 so its
	// sockets on the shared /run/buildkit emptyDir are group-owned by the
	// Translator's GID; the CHOWN capability lets rootless buildkitd chown its
	// trace and listening sockets to that group under the per-Pod user namespace.
	if bk.SecurityContext.RunAsGroup == nil || *bk.SecurityContext.RunAsGroup != 1000 {
		t.Fatalf("buildkit RunAsGroup = %#v, want 1000", bk.SecurityContext)
	}
	// CHOWN, DAC_OVERRIDE, FOWNER, SETGID, SETUID, and SYS_ADMIN support the
	// rootless overlayfs worker inside the per-Pod user namespace
	// (hostUsers=false). SYS_ADMIN lets the overlayfs snapshotter perform the
	// bind mounts it needs to assemble build inputs; the others let buildkitd
	// adjust/read its snapshots and sockets and spawn the fuse-overlayfs
	// helper. The overlayfs snapshotter avoids the native-snapshotter
	// "permission denied" failures on layer files owned by mapped non-root
	// image UIDs.
	if bk.SecurityContext.Capabilities == nil || len(bk.SecurityContext.Capabilities.Add) != 6 ||
		bk.SecurityContext.Capabilities.Add[0] != "CHOWN" || bk.SecurityContext.Capabilities.Add[1] != "DAC_OVERRIDE" ||
		bk.SecurityContext.Capabilities.Add[2] != "FOWNER" || bk.SecurityContext.Capabilities.Add[3] != "SETGID" ||
		bk.SecurityContext.Capabilities.Add[4] != "SETUID" || bk.SecurityContext.Capabilities.Add[5] != "SYS_ADMIN" {
		t.Fatalf("buildkit capabilities.Add = %#v, want [CHOWN, DAC_OVERRIDE, FOWNER, SETGID, SETUID, SYS_ADMIN]", bk.SecurityContext.Capabilities)
	}
	if bk.Resources.Limits.Cpu().Cmp(kresource.MustParse("1")) != 0 {
		t.Fatalf("buildkit cpu limit = %s, want 1", bk.Resources.Limits.Cpu())
	}
	if bk.Resources.Limits.Memory().Cmp(kresource.MustParse("2Gi")) != 0 {
		t.Fatalf("buildkit memory limit = %s, want 2Gi", bk.Resources.Limits.Memory())
	}
	if len(bk.Resources.Requests) != 0 {
		t.Fatalf("buildkit resource requests = %#v, want none (defaults apply to limits only)", bk.Resources.Requests)
	}
	if hasMount(bk, "detaildb-connection", true) || hasMount(bk, "resultdb-connection", true) {
		t.Fatalf("buildkit must NOT mount database connection Secrets: %+v", bk.VolumeMounts)
	}
	if !hasMount(bk, "registry-auth", true) {
		t.Fatalf("buildkit must mount the registry Secret read-only: %+v", bk.VolumeMounts)
	}
	if bk.StartupProbe == nil ||
		bk.StartupProbe.PeriodSeconds != 1 || bk.StartupProbe.FailureThreshold != 60 ||
		bk.StartupProbe.Exec == nil ||
		len(bk.StartupProbe.Exec.Command) != 5 ||
		bk.StartupProbe.Exec.Command[0] != "buildctl" {
		t.Fatalf("buildkit startupProbe = %#v, want buildctl workers probe (period 1s, failureThreshold 60)", bk.StartupProbe)
	}

	for _, v := range []string{"workspace", "run-buildkit", "registry-auth", "detaildb-connection", "resultdb-connection"} {
		if !hasVolume(transDep, v) {
			t.Fatalf("translator Deployment missing volume %q", v)
		}
	}
	for _, v := range transDep.Spec.Template.Spec.Volumes {
		if v.Name == "registry-auth" {
			if v.Secret == nil || v.Secret.SecretName != "cbse-registry-auth" {
				t.Fatalf("registry-auth volume = %#v, want secret cbse-registry-auth", v)
			}
			if len(v.Secret.Items) != 1 || v.Secret.Items[0].Key != ".dockerconfigjson" || v.Secret.Items[0].Path != "config.json" {
				t.Fatalf("registry-auth volume items = %#v, want [{.dockerconfigjson -> config.json}]", v.Secret.Items)
			}
		}
	}

	// Translator ConfigMap with REPOSITORY and BASEIMAGE.
	cm := &corev1.ConfigMap{}
	mustExist(t, cm, "exp-happy-translator-cfg")
	if cm.Data["REPOSITORY"] != "registry.unibw.de/i31bdase/cbse-test-runner" {
		t.Fatalf("configmap REPOSITORY = %q", cm.Data["REPOSITORY"])
	}
	if !strings.HasPrefix(cm.Data["BASEIMAGE"], "registry.unibw.de/") {
		t.Fatalf("configmap BASEIMAGE = %q", cm.Data["BASEIMAGE"])
	}

	// PPS Deployment: single container under the restricted profile, the
	// resultdb connection Secret mounted read-only as the only volume, the
	// verbatim spec image/port/command/args, the workload labels and owner
	// reference, and the exact alpha4 PPS env contract.
	ppsDep := &appsv1.Deployment{}
	mustExist(t, ppsDep, "exp-happy-pps")
	if len(ppsDep.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("pps Deployment has %d containers, want 1 (no BuildKit sidecar)", len(ppsDep.Spec.Template.Spec.Containers))
	}
	if got := ppsDep.Spec.Template.Spec.Containers[0].Name; got != "pps" {
		t.Fatalf("pps container name = %q, want pps", got)
	}
	// Workload labels on the object, the Pod template, and the selector.
	ppsInst := getExperiment(t, key)
	for k, want := range map[string]string{
		"app":                                    "exp-happy-pps",
		"experiment.cbse.terministic.de/project": "exp-happy",
		"experiment.cbse.terministic.de/experiment-uid": string(ppsInst.UID),
	} {
		if ppsDep.Labels[k] != want {
			t.Fatalf("pps Deployment label %s = %q, want %q", k, ppsDep.Labels[k], want)
		}
		if ppsDep.Spec.Template.Labels[k] != want {
			t.Fatalf("pps Pod template label %s = %q, want %q", k, ppsDep.Spec.Template.Labels[k], want)
		}
		if ppsDep.Spec.Selector.MatchLabels[k] != want {
			t.Fatalf("pps selector label %s = %q, want %q", k, ppsDep.Spec.Selector.MatchLabels[k], want)
		}
	}
	// Owner reference to the owning experiment.
	if len(ppsDep.OwnerReferences) != 1 {
		t.Fatalf("pps Deployment ownerReferences = %#v, want exactly one", ppsDep.OwnerReferences)
	}
	or := ppsDep.OwnerReferences[0]
	if or.Kind != "SimulationExperiment" || or.Name != "exp-happy" || or.UID != ppsInst.UID || or.Controller == nil || !*or.Controller {
		t.Fatalf("pps Deployment ownerReference = %#v, want controller reference to the experiment", or)
	}

	ppsCtr := containerByName(t, ppsDep, "pps")
	// Verbatim image/port/command/args pass-through from spec.postProcessingService.
	if got := ppsCtr.Image; got != "registry.unibw.de/i31bdase/cbse-test/pps@sha256:"+shaA {
		t.Fatalf("pps image = %q", got)
	}
	if len(ppsCtr.Ports) != 1 || ppsCtr.Ports[0].ContainerPort != 8081 {
		t.Fatalf("pps container ports = %#v, want [{8081}]", ppsCtr.Ports)
	}
	if got := ppsCtr.Command; len(got) != 1 || got[0] != "/usr/local/bin/pps" {
		t.Fatalf("pps command = %#v, want [/usr/local/bin/pps]", got)
	}
	if got := ppsCtr.Args; len(got) != 1 || got[0] != "--evaluate" {
		t.Fatalf("pps args = %#v, want [--evaluate]", got)
	}
	// Restricted security profile: UID/GID 1000, non-root, no escalation,
	// runtime-default seccomp, all capabilities dropped.
	sc := ppsCtr.SecurityContext
	if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 1000 ||
		sc.RunAsGroup == nil || *sc.RunAsGroup != 1000 ||
		sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot ||
		sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation ||
		sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault ||
		sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("pps security context = %#v, want the restricted profile (UID/GID 1000, non-root, no escalation, runtime-default seccomp, drop ALL)", sc)
	}
	// The resultdb connection Secret is the only volume, mounted read-only.
	if len(ppsDep.Spec.Template.Spec.Volumes) != 1 {
		t.Fatalf("pps Deployment volumes = %#v, want exactly [resultdb-connection]", ppsDep.Spec.Template.Spec.Volumes)
	}
	v := ppsDep.Spec.Template.Spec.Volumes[0]
	if v.Name != "resultdb-connection" || v.Secret == nil || v.Secret.SecretName != "exp-happy-resultdb-sct" || len(v.Secret.Items) != 0 {
		t.Fatalf("pps volume = %#v, want resultdb-connection secret exp-happy-resultdb-sct without items", v)
	}
	if !hasMount(ppsCtr, "resultdb-connection", true) {
		t.Fatalf("pps missing read-only resultdb-connection mount: %+v", ppsCtr.VolumeMounts)
	}
	// The exact alpha4 PPS env contract: five fixed values plus the three
	// downward-API identity vars, nothing else.
	if len(ppsCtr.Env) != 8 {
		t.Fatalf("pps env vars = %d, want 8", len(ppsCtr.Env))
	}
	env := map[string]corev1.EnvVar{}
	for _, e := range ppsCtr.Env {
		env[e.Name] = e
	}
	for name, want := range map[string]string{
		"NATS_URL":                        "nats://sm-eds-nats:4222",
		"PPS_STREAM":                      "cbse_pps",
		"PPS_REQUEST_SUBJECT":             "cbse." + alpha4TestNamespace + ".exp-happy.pps.request",
		"PPS_EVALUATION_SUBJECT_TEMPLATE": "cbse." + alpha4TestNamespace + ".exp-happy.pps.%s.evaluation",
		"PPS_CONSUMER":                    "pps-" + controller.RunnerUIDPrefix(ppsInst.UID),
	} {
		got, ok := env[name]
		if !ok || got.Value != want {
			t.Fatalf("pps env %s = %#v, want value %q", name, got, want)
		}
	}
	for name, fieldPath := range map[string]string{
		"SIMULATIONPROJECTNAMESPACE": "metadata.namespace",
		"SIMULATIONPROJECTNAME":      "metadata.labels['experiment.cbse.terministic.de/project']",
		"SIMULATIONEXPERIMENTUID":    "metadata.labels['experiment.cbse.terministic.de/experiment-uid']",
	} {
		got, ok := env[name]
		if !ok || got.ValueFrom == nil || got.ValueFrom.FieldRef == nil || got.ValueFrom.FieldRef.FieldPath != fieldPath {
			t.Fatalf("pps env %s = %#v, want downward-API field ref %q", name, got, fieldPath)
		}
	}

	// PPS Service: ClusterIP on the spec port selecting the pps app label,
	// owned by the experiment.
	ppsSvc := &corev1.Service{}
	mustExist(t, ppsSvc, "exp-happy-pps-svc")
	if ppsSvc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Fatalf("pps Service type = %q, want ClusterIP", ppsSvc.Spec.Type)
	}
	if ppsSvc.Spec.Selector["app"] != "exp-happy-pps" {
		t.Fatalf("pps Service selector = %#v, want app=exp-happy-pps", ppsSvc.Spec.Selector)
	}
	if len(ppsSvc.Spec.Ports) != 1 || ppsSvc.Spec.Ports[0].Port != 8081 || ppsSvc.Spec.Ports[0].TargetPort.IntValue() != 8081 {
		t.Fatalf("pps Service ports = %#v, want [{port: 8081, targetPort: 8081}]", ppsSvc.Spec.Ports)
	}
	if len(ppsSvc.OwnerReferences) != 1 || ppsSvc.OwnerReferences[0].Kind != "SimulationExperiment" || ppsSvc.OwnerReferences[0].Name != "exp-happy" {
		t.Fatalf("pps Service ownerReferences = %#v, want the owning experiment", ppsSvc.OwnerReferences)
	}

	// Translator Service and runner ServiceAccount.
	transSvc := &corev1.Service{}
	mustExist(t, transSvc, "exp-happy-translator-svc")
	if transSvc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Fatalf("translator Service type = %q, want ClusterIP", transSvc.Spec.Type)
	}
	if transSvc.Spec.Selector["app"] != "exp-happy-translator" {
		t.Fatalf("translator Service selector = %#v", transSvc.Spec.Selector)
	}
	// The runner ServiceAccount uses the deterministic simrunner-<12-char-UID-prefix>
	// name derived from the live experiment UID, matching the Scenario Manager
	// runner Job contract that references this ServiceAccount by exact name.
	inst := getExperiment(t, key)
	saName := controller.RunnerServiceAccountName(inst.UID)
	sa := &corev1.ServiceAccount{}
	mustExist(t, sa, saName)
	if sa.AutomountServiceAccountToken == nil || *sa.AutomountServiceAccountToken {
		t.Fatalf("runner ServiceAccount automount = %#v, want false", sa.AutomountServiceAccountToken)
	}

	// Experiment status reflects the readiness message.
	if inst.Status.Phase != "InProgress" {
		t.Fatalf("status phase = %q, want InProgress", inst.Status.Phase)
	}
}

// TestAlpha4DatabaseSpecRejectedByOperator verifies that a host form the CRD
// admits (an embedded port) is rejected by the Operator with a descriptive
// Error before any component is created.
func TestAlpha4DatabaseSpecRejectedByOperator(t *testing.T) {
	key := types.NamespacedName{Name: "exp-dbspec", Namespace: alpha4TestNamespace}
	ensureRegistrySecret(t, goodRegistrySecret())
	exp := validExperiment("exp-dbspec")
	exp.Spec.ResultDatabase.Host = "resultdb.example.com:5432" // CRD allows; Operator rejects
	key = createExperiment(t, exp)

	r := newReconciler(func(_ context.Context, _ dbendpoint.Endpoint) error { return nil })
	if phase := drive(t, r, key, 10); phase != "Error" {
		t.Fatalf("phase = %q, want Error", phase)
	}
	mustNotExist(t, &appsv1.Deployment{}, "exp-dbspec-detaildb")
	mustNotExist(t, &corev1.Service{}, "exp-dbspec-detaildb-svc")
	mustNotExist(t, &appsv1.Deployment{}, "exp-dbspec-translator")
	mustNotExist(t, &corev1.Secret{}, "exp-dbspec-detaildb-sct")
}

// TestAlpha4ProbeFailureRequeues verifies that a transient database probe
// failure keeps the experiment Provisioning (requeue) and never moves it to
// Error or performs application database work.
func TestAlpha4ProbeFailureRequeues(t *testing.T) {
	key := types.NamespacedName{Name: "exp-probe", Namespace: alpha4TestNamespace}
	ensureRegistrySecret(t, goodRegistrySecret())
	key = createExperiment(t, validExperiment("exp-probe"))

	var probeCalls int
	r := newReconciler(func(_ context.Context, _ dbendpoint.Endpoint) error {
		probeCalls++
		return fmt.Errorf("synthetic probe failure")
	})
	phase := drive(t, r, key, 12)
	if phase != "Provisioning" {
		t.Fatalf("phase = %q, want Provisioning (probe failure is a requeue, not Error)", phase)
	}
	if probeCalls == 0 {
		t.Fatalf("expected the availability probe to be invoked")
	}
	// Components were provisioned during the first reconcile; the probe failure
	// must not roll them back or create additional runtime objects.
	mustExist(t, &appsv1.Deployment{}, "exp-probe-translator")
	inst := getExperiment(t, key)
	if inst.Status.Phase == "Error" {
		t.Fatalf("probe failure must not move the experiment to Error")
	}
}

// TestAlpha4RegistrySecretValidation covers the registry Secret cases the
// Operator must reject with a descriptive Error and no provisioned components.
func TestAlpha4RegistrySecretValidation(t *testing.T) {
	cases := []struct {
		name   string
		secret *corev1.Secret
	}{
		{"missing", nil},
		{"wrong type", func() *corev1.Secret {
			s := goodRegistrySecret()
			s.Type = corev1.SecretTypeOpaque
			return s
		}()},
		{"missing creds", dockerConfigSecret(map[string]string{"registry.example.test": "u:p"}, nil, "")},
		{"token only", func() *corev1.Secret {
			raw, _ := json.Marshal(map[string]any{"auths": map[string]any{"registry.unibw.de": map[string]any{"identitytoken": "tok"}}})
			return &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "cbse-registry-auth", Namespace: alpha4TestNamespace},
				Type:       corev1.SecretType("kubernetes.io/dockerconfigjson"),
				Data:       map[string][]byte{".dockerconfigjson": raw},
			}
		}()},
		{"helper only", dockerConfigSecret(nil, map[string]string{"registry.unibw.de": "desktop"}, "")},
		{"creds store only", dockerConfigSecret(nil, nil, "desktop")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expName := "exp-reg-" + strings.ReplaceAll(c.name, " ", "-")
			key := types.NamespacedName{Name: expName, Namespace: alpha4TestNamespace}
			if c.secret == nil {
				deleteRegistrySecret(t)
			} else {
				ensureRegistrySecret(t, c.secret)
			}
			key = createExperiment(t, validExperiment(expName))

			r := newReconciler(func(_ context.Context, _ dbendpoint.Endpoint) error { return nil })
			if phase := drive(t, r, key, 10); phase != "Error" {
				t.Fatalf("phase = %q, want Error", phase)
			}
			mustNotExist(t, &appsv1.Deployment{}, expName+"-detaildb")
			mustNotExist(t, &appsv1.Deployment{}, expName+"-translator")
			mustNotExist(t, &corev1.Secret{}, expName+"-detaildb-sct")
		})
	}
}

// TestAlpha4BuilderResourcesRejected verifies the Operator rejects invalid
// builder resources (request exceeds limit, and rejected extended resources)
// with Error before provisioning components.
func TestAlpha4BuilderResourcesRejected(t *testing.T) {
	cases := []struct {
		name string
		res  *corev1.ResourceRequirements
	}{
		{
			"request exceeds limit",
			&corev1.ResourceRequirements{
				Limits:   corev1.ResourceList{corev1.ResourceCPU: kresource.MustParse("1")},
				Requests: corev1.ResourceList{corev1.ResourceCPU: kresource.MustParse("2")},
			},
		},
		{
			"claims rejected",
			&corev1.ResourceRequirements{
				Limits: corev1.ResourceList{corev1.ResourceCPU: kresource.MustParse("1")},
				Claims: []corev1.ResourceClaim{{Name: "x"}},
			},
		},
		{
			"hugepages rejected",
			&corev1.ResourceRequirements{
				Limits: corev1.ResourceList{corev1.ResourceName("hugepages-2Mi"): kresource.MustParse("1")},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expName := "exp-res-" + strings.ReplaceAll(c.name, " ", "-")
			ensureRegistrySecret(t, goodRegistrySecret())
			exp := validExperiment(expName)
			exp.Spec.Translator.BuilderResources = c.res
			key := createExperiment(t, exp)

			r := newReconciler(func(_ context.Context, _ dbendpoint.Endpoint) error { return nil })
			if phase := drive(t, r, key, 10); phase != "Error" {
				t.Fatalf("phase = %q, want Error", phase)
			}
			mustNotExist(t, &appsv1.Deployment{}, expName+"-translator")
		})
	}
}

// TestAlpha4BuilderResourcesApplied verifies that explicitly provided builder
// resources are honored on the BuildKit container, and that a nil value
// applies the independent CPU/memory defaults to limits only.
func TestAlpha4BuilderResourcesApplied(t *testing.T) {
	t.Run("explicit resources", func(t *testing.T) {
		ensureRegistrySecret(t, goodRegistrySecret())
		exp := validExperiment("exp-res-explicit")
		exp.Spec.Translator.BuilderResources = &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    kresource.MustParse("500m"),
				corev1.ResourceMemory: kresource.MustParse("1Gi"),
			},
		}
		key := createExperiment(t, exp)
		r := newReconciler(func(_ context.Context, _ dbendpoint.Endpoint) error { return nil })
		if phase := drive(t, r, key, 30); phase != "InProgress" {
			t.Fatalf("phase = %q, want InProgress", phase)
		}
		dep := &appsv1.Deployment{}
		mustExist(t, dep, "exp-res-explicit-translator")
		bk := containerByName(t, dep, "buildkit")
		if bk.Resources.Limits.Cpu().Cmp(kresource.MustParse("500m")) != 0 {
			t.Fatalf("buildkit cpu limit = %s, want 500m", bk.Resources.Limits.Cpu())
		}
		if bk.Resources.Limits.Memory().Cmp(kresource.MustParse("1Gi")) != 0 {
			t.Fatalf("buildkit memory limit = %s, want 1Gi", bk.Resources.Limits.Memory())
		}
	})

	t.Run("defaults when nil", func(t *testing.T) {
		ensureRegistrySecret(t, goodRegistrySecret())
		key := createExperiment(t, validExperiment("exp-res-default"))
		r := newReconciler(func(_ context.Context, _ dbendpoint.Endpoint) error { return nil })
		if phase := drive(t, r, key, 30); phase != "InProgress" {
			t.Fatalf("phase = %q, want InProgress", phase)
		}
		dep := &appsv1.Deployment{}
		mustExist(t, dep, "exp-res-default-translator")
		bk := containerByName(t, dep, "buildkit")
		if bk.Resources.Limits.Cpu().Cmp(kresource.MustParse("1")) != 0 {
			t.Fatalf("default buildkit cpu limit = %s, want 1", bk.Resources.Limits.Cpu())
		}
		if bk.Resources.Limits.Memory().Cmp(kresource.MustParse("2Gi")) != 0 {
			t.Fatalf("default buildkit memory limit = %s, want 2Gi", bk.Resources.Limits.Memory())
		}
	})
}

// TestAlpha4InvalidJobTemplateErrors verifies an invalid runner Job template is
// rejected with Error and never reaches InProgress.
func TestAlpha4InvalidJobTemplateErrors(t *testing.T) {
	ensureRegistrySecret(t, goodRegistrySecret())
	exp := validExperiment("exp-jt")
	exp.Spec.Runner.JobTemplate = &batchv1.JobTemplateSpec{
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "runner", Image: "forbidden"}},
				},
			},
		},
	}
	key := createExperiment(t, exp)

	r := newReconciler(func(_ context.Context, _ dbendpoint.Endpoint) error { return nil })
	if phase := drive(t, r, key, 10); phase != "Error" {
		t.Fatalf("phase = %q, want Error", phase)
	}
	mustNotExist(t, &appsv1.Deployment{}, "exp-jt-translator")
}

// TestAlpha4ImmutableUpdateRejected verifies that an immutable Translator field
// update is rejected by the CRD and leaves the already-provisioned
// Deployment/Service unchanged.
func TestAlpha4ImmutableUpdateRejected(t *testing.T) {
	ctx := context.Background()
	ensureRegistrySecret(t, goodRegistrySecret())
	key := createExperiment(t, validExperiment("exp-immut"))
	r := newReconciler(func(_ context.Context, _ dbendpoint.Endpoint) error { return nil })
	if phase := drive(t, r, key, 30); phase != "InProgress" {
		t.Fatalf("phase = %q, want InProgress", phase)
	}

	dep := &appsv1.Deployment{}
	mustExist(t, dep, "exp-immut-translator")
	originalImage := containerByName(t, dep, "translator").Image

	// Attempt an immutable update: change translator.image (CRD XValidation rejects).
	inst := getExperiment(t, key)
	inst.Spec.Translator.Image = "registry.unibw.de/i31bdase/cbse-test/translator@sha256:" + strings.Repeat("b", 64)
	if err := alpha4Client.Update(ctx, inst); err == nil {
		t.Fatalf("immutable translator.image update was accepted by the CRD; want rejection")
	}

	// The Deployment must remain unchanged after the rejected update.
	dep2 := &appsv1.Deployment{}
	mustExist(t, dep2, "exp-immut-translator")
	if got := containerByName(t, dep2, "translator").Image; got != originalImage {
		t.Fatalf("translator image changed after rejected update: %q -> %q", originalImage, got)
	}
}

// TestAlpha4PPSIdempotentReconcile verifies that a metadata-only update
// (an annotation timestamp) is reconciled idempotently: the owned PPS
// Deployment and Service are not duplicated and the PPS env contract stays
// intact. It is the envtest-level mirror of the e2e idempotence spec.
func TestAlpha4PPSIdempotentReconcile(t *testing.T) {
	ctx := context.Background()
	ensureRegistrySecret(t, goodRegistrySecret())
	key := createExperiment(t, validExperiment("exp-pps-idem"))
	r := newReconciler(func(_ context.Context, _ dbendpoint.Endpoint) error { return nil })
	if phase := drive(t, r, key, 30); phase != "InProgress" {
		t.Fatalf("phase = %q, want InProgress", phase)
	}

	countPPSChildren := func() (deps int, svc bool) {
		depList := &appsv1.DeploymentList{}
		if err := alpha4Client.List(ctx, depList, client.InNamespace(alpha4TestNamespace), client.MatchingLabels{"app": "exp-pps-idem-pps"}); err != nil {
			t.Fatalf("list pps deployments: %v", err)
		}
		// The PPS Service carries no labels (like the Translator Service, whose
		// selector app label is the only app identity), so it is counted by name.
		svcObj := &corev1.Service{}
		svcErr := alpha4Client.Get(ctx, types.NamespacedName{Name: "exp-pps-idem-pps-svc", Namespace: alpha4TestNamespace}, svcObj)
		if svcErr != nil && !apierrors.IsNotFound(svcErr) {
			t.Fatalf("get pps service: %v", svcErr)
		}
		return len(depList.Items), svcErr == nil
	}
	if deps, svc := countPPSChildren(); deps != 1 || !svc {
		t.Fatalf("before metadata update: pps deployments = %d, pps service present = %v, want 1 of each", deps, svc)
	}

	// Metadata-only update: an annotation timestamp, mirroring the e2e spec.
	inst := getExperiment(t, key)
	if inst.Annotations == nil {
		inst.Annotations = map[string]string{}
	}
	inst.Annotations["cbse.terministic.de/idempotence-check"] = time.Now().UTC().Format(time.RFC3339Nano)
	if err := alpha4Client.Update(ctx, inst); err != nil {
		t.Fatalf("update experiment annotation: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("reconcile %d after metadata update: %v", i, err)
		}
	}

	if phase := getExperiment(t, key).Status.Phase; phase != "InProgress" {
		t.Fatalf("phase after metadata update = %q, want InProgress", phase)
	}
	if deps, svc := countPPSChildren(); deps != 1 || !svc {
		t.Fatalf("after metadata update: pps deployments = %d, pps service present = %v, want 1 of each (no duplication)", deps, svc)
	}

	// The PPS env contract survives the re-reconcile verbatim.
	dep := &appsv1.Deployment{}
	mustExist(t, dep, "exp-pps-idem-pps")
	ctr := containerByName(t, dep, "pps")
	env := map[string]string{}
	for _, e := range ctr.Env {
		env[e.Name] = e.Value
	}
	if env["PPS_REQUEST_SUBJECT"] != "cbse."+alpha4TestNamespace+".exp-pps-idem.pps.request" {
		t.Fatalf("pps PPS_REQUEST_SUBJECT after re-reconcile = %q", env["PPS_REQUEST_SUBJECT"])
	}
	if env["PPS_CONSUMER"] != "pps-"+controller.RunnerUIDPrefix(inst.UID) {
		t.Fatalf("pps PPS_CONSUMER after re-reconcile = %q", env["PPS_CONSUMER"])
	}
}

// --- readiness watchdog (ruling R) + G-ruled PPS gate -----------------------

// TestAlpha4ReadinessWatchdogErrorsAfterBudget verifies ruling R: a
// not-ready component across the counted retry budget transitions the
// experiment to Error without further retry, and the final message aggregates
// the per-retry not-ready inventories (each retry labeled, the component named
// with its observed failure at each retry). The PPS is marked ready so the
// translator alone is the not-ready component; the watchdog knobs are injected
// in milliseconds so the counted retries are observable in the drive loop.
func TestAlpha4ReadinessWatchdogErrorsAfterBudget(t *testing.T) {
	key := types.NamespacedName{Name: "exp-wd-budget", Namespace: alpha4TestNamespace}
	ensureRegistrySecret(t, goodRegistrySecret())
	key = createExperiment(t, validExperiment("exp-wd-budget"))
	uid := getExperiment(t, key).UID

	r := newReconcilerWithWatchdog(
		func(_ context.Context, _ dbendpoint.Endpoint) error { return nil },
		10*time.Millisecond, 3,
	)
	ctx := context.Background()
	phase := ""
	for i := 0; i < 30; i++ {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
		phase = getExperiment(t, key).Status.Phase
		if phase == "Error" {
			break
		}
		if phase == "InProgress" {
			t.Fatalf("phase = InProgress: the translator is not ready; the gate must block")
		}
		markPPSReady(t, key)
		keepDeploymentNotReady(t, "exp-wd-budget-translator", "MinimumReplicasUnavailable")
		time.Sleep(20 * time.Millisecond)
	}
	if phase != "Error" {
		t.Fatalf("phase = %q, want Error after the counted retry budget is exhausted", phase)
	}
	inst := getExperiment(t, key)
	msg := inst.Status.Message
	for _, label := range []string{"retry 1:", "retry 2:", "retry 3:"} {
		if !strings.Contains(msg, label) {
			t.Fatalf("aggregated message %q must label %q (one entry per counted retry)", msg, label)
		}
	}
	if got := strings.Count(msg, "translator:"); got != 3 {
		t.Fatalf("message names the translator %d times, want exactly once per counted retry (3): %q", got, msg)
	}
	if !strings.Contains(msg, "condition Available MinimumReplicasUnavailable") {
		t.Fatalf("message %q must carry the translator's observed condition reason", msg)
	}
	if retries, exists := r.ReadinessWatchdogRetries(uid); exists || retries != 0 {
		t.Fatalf("watchdog state after the Error write = (%d, %v), want cleared", retries, exists)
	}
}

// TestAlpha4ReadinessWatchdogClearsWhenReadyMidBudget verifies ruling R: a
// component that becomes ready mid-budget transitions the experiment to
// InProgress with the watchdog state cleared and no false Error. The
// translator stays not-ready until one counted retry has accumulated (so the
// state is observably started and counting), then the gate passes.
func TestAlpha4ReadinessWatchdogClearsWhenReadyMidBudget(t *testing.T) {
	key := types.NamespacedName{Name: "exp-wd-clear", Namespace: alpha4TestNamespace}
	ensureRegistrySecret(t, goodRegistrySecret())
	key = createExperiment(t, validExperiment("exp-wd-clear"))
	uid := getExperiment(t, key).UID

	r := newReconcilerWithWatchdog(
		func(_ context.Context, _ dbendpoint.Endpoint) error { return nil },
		10*time.Millisecond, 3,
	)
	ctx := context.Background()

	// Keep the translator not-ready until at least one counted retry has
	// accumulated (the state is observably started).
	for i := 0; i < 30; i++ {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
		if phase := getExperiment(t, key).Status.Phase; phase == "Error" {
			t.Fatalf("phase = Error: a false Error before the component became ready mid-budget")
		}
		markPPSReady(t, key)
		keepDeploymentNotReady(t, "exp-wd-clear-translator", "MinimumReplicasUnavailable")
		if retries, exists := r.ReadinessWatchdogRetries(uid); exists && retries >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if retries, exists := r.ReadinessWatchdogRetries(uid); !exists || retries < 1 {
		t.Fatalf("watchdog state before recovery = (%d, %v), want a started state with >= 1 counted retry", retries, exists)
	}

	// Mid-budget recovery: the translator becomes ready; the gate passes and
	// the watchdog state clears on InProgress.
	markTranslatorReady(t, key)
	phase := ""
	for i := 0; i < 10; i++ {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("reconcile %d after recovery: %v", i, err)
		}
		phase = getExperiment(t, key).Status.Phase
		if phase == "InProgress" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if phase != "InProgress" {
		t.Fatalf("phase = %q, want InProgress (the component became ready mid-budget)", phase)
	}
	if retries, exists := r.ReadinessWatchdogRetries(uid); exists {
		t.Fatalf("watchdog state after InProgress = retries %d, want cleared on all-ready", retries)
	}
}

// TestAlpha4ReadinessGateRequiresPPS verifies ruling G: a not-ready PPS
// Deployment blocks InProgress (the gate covers the PPS in addition to the
// databases and the translator) even when the translator is ready, and a
// never-ready PPS drives the bounded-retry watchdog to Error with the PPS in
// the per-retry inventory.
func TestAlpha4ReadinessGateRequiresPPS(t *testing.T) {
	key := types.NamespacedName{Name: "exp-wd-pps", Namespace: alpha4TestNamespace}
	ensureRegistrySecret(t, goodRegistrySecret())
	key = createExperiment(t, validExperiment("exp-wd-pps"))
	uid := getExperiment(t, key).UID

	r := newReconcilerWithWatchdog(
		func(_ context.Context, _ dbendpoint.Endpoint) error { return nil },
		10*time.Millisecond, 3,
	)
	ctx := context.Background()
	phase := ""
	gateBlockedWhileTranslatorReady := false
	for i := 0; i < 30; i++ {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
		phase = getExperiment(t, key).Status.Phase
		if phase == "InProgress" {
			t.Fatalf("phase = InProgress: a not-ready PPS must block the gate (ruling G)")
		}
		if phase == "Error" {
			break
		}
		// The translator is ready but the PPS is not: the gate must still
		// block InProgress.
		markTranslatorReady(t, key)
		keepDeploymentNotReady(t, "exp-wd-pps-pps", "MinimumReplicasUnavailable")
		gateBlockedWhileTranslatorReady = true
		time.Sleep(20 * time.Millisecond)
	}
	if phase != "Error" {
		t.Fatalf("phase = %q, want Error (the never-ready PPS exhausts the counted retry budget)", phase)
	}
	if !gateBlockedWhileTranslatorReady {
		t.Fatalf("the gate never observed the translator-ready / PPS-not-ready state; cannot prove the G-ruled gate")
	}
	inst := getExperiment(t, key)
	if !strings.Contains(inst.Status.Message, "pps:") {
		t.Fatalf("aggregated message %q must name the PPS in the not-ready inventory", inst.Status.Message)
	}
	for _, label := range []string{"retry 1:", "retry 2:", "retry 3:"} {
		if !strings.Contains(inst.Status.Message, label) {
			t.Fatalf("aggregated message %q must label %q", inst.Status.Message, label)
		}
	}
	if retries, exists := r.ReadinessWatchdogRetries(uid); exists {
		t.Fatalf("watchdog state after the Error write = retries %d, want cleared", retries)
	}
}

// TestAlpha4ReadinessWatchdogUnaffectedOnHealthyRollout verifies ruling R: a
// healthy fast rollout reaches InProgress with the production watchdog
// defaults (the zero knobs select ~60s spacing and 3 counted retries) and the
// watchdog never fires - every readiness evaluation finds all components
// ready, so no state is ever started.
func TestAlpha4ReadinessWatchdogUnaffectedOnHealthyRollout(t *testing.T) {
	key := types.NamespacedName{Name: "exp-wd-healthy", Namespace: alpha4TestNamespace}
	ensureRegistrySecret(t, goodRegistrySecret())
	key = createExperiment(t, validExperiment("exp-wd-healthy"))
	uid := getExperiment(t, key).UID

	r := newReconciler(func(_ context.Context, _ dbendpoint.Endpoint) error { return nil })
	if phase := drive(t, r, key, 30); phase != "InProgress" {
		t.Fatalf("phase = %q, want InProgress (the healthy rollout is unaffected by the watchdog)", phase)
	}
	inst := getExperiment(t, key)
	if inst.Status.Message != "All components provisioned and ready" {
		t.Fatalf("message = %q, want the all-ready message", inst.Status.Message)
	}
	if retries, exists := r.ReadinessWatchdogRetries(uid); exists || retries != 0 {
		t.Fatalf("watchdog state = (%d, %v), want none (the watchdog never fires on the healthy path)", retries, exists)
	}
}
