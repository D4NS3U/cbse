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
	"fmt"
	"regexp"
	"strings"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/experiment-operator/internal/dbendpoint"
	"github.com/D4NS3U/cbse/experiment-operator/internal/jobtemplate"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Alpha4 phase values written to SimulationExperiment.status.phase.
const (
	alpha4PhasePending      = "Pending"
	alpha4PhaseProvisioning = "Provisioning"
	alpha4PhaseInProgress   = "InProgress"
	alpha4PhaseError        = "Error"
)

// alpha4RequeueAfter is the transient retry cadence for provisioning and
// readiness rechecks. It is not a backoff and occupies no reconciler state.
const alpha4RequeueAfter = 5 * time.Second

// alpha4ExperimentNameRe repeats the CRD admission rule for descriptive
// Operator-side errors: a lowercase DNS label of 1 to 63 characters.
var alpha4ExperimentNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Alpha4SimulationExperimentReconciler reconciles an alpha4 SimulationExperiment
// by validating its configuration and provisioning the immutable runtime
// components: image- and host-based database connection Secrets, image-based
// database Deployments with exactly one cbse-registry-auth pull-Secret, the
// two-container rootless BuildKit Translator Deployment and Service, the
// mock-style Translator ConfigMap, and the runner ServiceAccount.
//
// This reconciler is additive and isolated until the alpha4 cutover in a later
// slice: it does not replace the active alpha3 reconciler wiring.
type Alpha4SimulationExperimentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// DBProbe performs the availability SELECT 1 probe against a database
	// endpoint. When nil, a default pgx-backed probe is used. Tests inject a
	// fake to exercise the readiness and requeue paths without a live
	// PostgreSQL instance.
	DBProbe func(ctx context.Context, ep dbendpoint.Endpoint) error
}

// Reconcile moves an alpha4 SimulationExperiment through Pending, Provisioning,
// and InProgress, or to Error for a provisioning validation problem.
func (r *Alpha4SimulationExperimentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	instance := &experimentalpha4.SimulationExperiment{}
	if err := r.Get(ctx, req.NamespacedName, instance); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	switch instance.Status.Phase {
	case "", alpha4PhasePending:
		return r.startProvisioning(ctx, instance)
	case alpha4PhaseProvisioning:
		return r.checkReadiness(ctx, instance)
	default:
		// InProgress, Error, and terminal phases are not Operator-owned beyond
		// provisioning in this slice.
		return ctrl.Result{}, nil
	}
}

// startProvisioning transitions a Pending experiment to Provisioning, validates
// the full configuration, and on success provisions the components. A
// validation failure moves the experiment to Error before any component is
// created.
func (r *Alpha4SimulationExperimentReconciler) startProvisioning(ctx context.Context, instance *experimentalpha4.SimulationExperiment) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if instance.Status.Phase != alpha4PhaseProvisioning {
		if err := r.patchPhase(ctx, instance, alpha4PhaseProvisioning, "Provisioning components"); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.validateExperiment(ctx, instance); err != nil {
		log.Error(err, "alpha4 provisioning validation failed")
		if perr := r.setErrorStatus(ctx, instance, err.Error()); perr != nil {
			return ctrl.Result{}, perr
		}
		return ctrl.Result{}, nil
	}

	if err := r.provisionComponents(ctx, instance); err != nil {
		log.Error(err, "alpha4 component provisioning failed")
		if perr := r.setErrorStatus(ctx, instance, err.Error()); perr != nil {
			return ctrl.Result{}, perr
		}
		return ctrl.Result{}, nil
	}

	if err := r.patchPhase(ctx, instance, alpha4PhaseProvisioning, "Components provisioned; checking readiness"); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: alpha4RequeueAfter}, nil
}

// checkReadiness probes both database endpoints and the Translator Deployment
// and moves the experiment to InProgress only when every check succeeds. A
// database probe failure is a retryable dependency: the experiment stays
// Provisioning and requeues without becoming Error and without performing
// application database work.
func (r *Alpha4SimulationExperimentReconciler) checkReadiness(ctx context.Context, instance *experimentalpha4.SimulationExperiment) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	for _, db := range []struct {
		spec   experimentalpha4.DatabaseSpec
		suffix string
	}{
		{instance.Spec.DetailDatabase, "detaildb"},
		{instance.Spec.ResultDatabase, "resultdb"},
	} {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: instance.Name + "-" + db.suffix + "-sct"}, secret); err != nil {
			log.V(1).Info("waiting for database connection Secret", "name", db.suffix, "err", err)
			return ctrl.Result{RequeueAfter: alpha4RequeueAfter}, nil
		}
		ep, err := dbEndpointFromSecret(secret)
		if err != nil {
			log.V(1).Info("database connection Secret not yet populated", "name", db.suffix, "err", err)
			return ctrl.Result{RequeueAfter: alpha4RequeueAfter}, nil
		}
		if err := r.probeDB(ctx, ep); err != nil {
			// A database availability failure is a retryable provisioning
			// dependency, not an application-database or configuration error.
			log.V(1).Info("database availability probe failed; requeueing", "name", db.suffix, "err", err)
			return ctrl.Result{RequeueAfter: alpha4RequeueAfter}, nil
		}
	}

	dep := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: instance.Name + "-translator"}, dep); err != nil {
		log.V(1).Info("waiting for Translator Deployment", "err", err)
		return ctrl.Result{RequeueAfter: alpha4RequeueAfter}, nil
	}
	if dep.Status.ReadyReplicas < 1 {
		return ctrl.Result{RequeueAfter: alpha4RequeueAfter}, nil
	}

	if err := r.patchPhase(ctx, instance, alpha4PhaseInProgress, "All components provisioned and ready"); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// probeDB runs the availability probe, using an injected DBProbe when set and
// the default pgx-backed probe otherwise.
func (r *Alpha4SimulationExperimentReconciler) probeDB(ctx context.Context, ep dbendpoint.Endpoint) error {
	if r.DBProbe != nil {
		return r.DBProbe(ctx, ep)
	}
	return dbendpoint.Probe(ctx, ep, nil, pgxConnector{})
}

// validateExperiment validates every alpha4 provisioning input and returns a
// descriptive error naming the exact field or referenced object on failure.
func (r *Alpha4SimulationExperimentReconciler) validateExperiment(ctx context.Context, instance *experimentalpha4.SimulationExperiment) error {
	if !alpha4ExperimentNameRe.MatchString(instance.Name) || len(instance.Name) > 63 {
		return fmt.Errorf("experiment name %q is not a lowercase DNS label of 1 to 63 characters", instance.Name)
	}
	for _, db := range []struct {
		spec   experimentalpha4.DatabaseSpec
		suffix string
	}{
		{instance.Spec.DetailDatabase, "detailDatabase"},
		{instance.Spec.ResultDatabase, "resultDatabase"},
	} {
		if err := validateDatabaseSpec(db.spec, db.suffix); err != nil {
			return err
		}
	}

	t := instance.Spec.Translator
	if err := ValidateDigestImage(t.Image); err != nil {
		return fmt.Errorf("translator.image: %w", err)
	}
	if err := ValidateRepository(t.Repository); err != nil {
		return err
	}
	if err := ValidateDigestImage(t.BaseImage); err != nil {
		return fmt.Errorf("translator.baseimage: %w", err)
	}
	if err := ValidateDigestImage(t.BuilderImage); err != nil {
		return fmt.Errorf("translator.builderImage: %w", err)
	}
	if t.RegistryAuthSecretRef.Name != registryAuthSecretName {
		return fmt.Errorf("translator.registryAuthSecretRef.name must equal %q", registryAuthSecretName)
	}

	if _, err := EffectiveBuilderResources(t.BuilderResources); err != nil {
		return fmt.Errorf("translator.builderResources: %w", err)
	}

	if instance.Spec.Runner.JobTemplate != nil {
		if _, errs := jobtemplate.ValidateAndNormalizeJobTemplate(instance.Spec.Runner.JobTemplate); len(errs) > 0 {
			return fmt.Errorf("runner.jobTemplate: %s", errs.ToAggregate())
		}
	}

	// Registry credential validation for the two required authorities only.
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: t.RegistryAuthSecretRef.Name}, secret); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("registry Secret %q does not exist in namespace %q", t.RegistryAuthSecretRef.Name, instance.Namespace)
		}
		return fmt.Errorf("get registry Secret %q: %w", t.RegistryAuthSecretRef.Name, err)
	}
	cfg, err := ValidateRegistrySecret(secret)
	if err != nil {
		return err
	}
	for _, ref := range []struct {
		name string
		ref  string
	}{
		{"translator.baseimage", t.BaseImage},
		{"translator.repository", t.Repository},
	} {
		authority, err := RegistryAuthority(ref.ref)
		if err != nil {
			return fmt.Errorf("%s: %w", ref.name, err)
		}
		if _, _, err := cfg.resolveAuth(authority); err != nil {
			return fmt.Errorf("%s: %w", ref.name, err)
		}
	}
	return nil
}

// validateDatabaseSpec validates an alpha4 DatabaseSpec for either image or host
// form and repeats the exact endpoint classification for descriptive errors.
func validateDatabaseSpec(spec experimentalpha4.DatabaseSpec, fieldPrefix string) error {
	hasImage := spec.Image != ""
	hasHost := spec.Host != ""
	if hasImage == hasHost {
		return fmt.Errorf("%s: exactly one of image or host is required", fieldPrefix)
	}
	if spec.DBName == "" {
		return fmt.Errorf("%s: dbname is required", fieldPrefix)
	}
	if spec.User == "" {
		return fmt.Errorf("%s: user is required", fieldPrefix)
	}
	if spec.Password == "" {
		return fmt.Errorf("%s: password is required", fieldPrefix)
	}
	if spec.Port < 1 || spec.Port > 65535 {
		return fmt.Errorf("%s: port must be in 1..65535", fieldPrefix)
	}
	if hasHost {
		if _, err := dbendpoint.ClassifyHost(spec.Host); err != nil {
			return fmt.Errorf("%s.host: %w", fieldPrefix, err)
		}
		if len(spec.Command) > 0 {
			return fmt.Errorf("%s: command is not allowed for a host-based database", fieldPrefix)
		}
		if len(spec.Args) > 0 {
			return fmt.Errorf("%s: args are not allowed for a host-based database", fieldPrefix)
		}
		if spec.NodePort != nil {
			return fmt.Errorf("%s: nodePort is not allowed for a host-based database", fieldPrefix)
		}
		if spec.ServiceType != "" && spec.ServiceType != experimentalpha4.ServiceTypeClusterIP {
			return fmt.Errorf("%s: serviceType must be ClusterIP for a host-based database", fieldPrefix)
		}
	} else {
		if err := ValidateDatabaseImage(spec.Image); err != nil {
			return fmt.Errorf("%s.image: %w", fieldPrefix, err)
		}
	}
	return nil
}

// provisionComponents creates or reconciles every alpha4 runtime component for
// an already-validated experiment.
func (r *Alpha4SimulationExperimentReconciler) provisionComponents(ctx context.Context, instance *experimentalpha4.SimulationExperiment) error {
	for _, db := range []struct {
		spec   experimentalpha4.DatabaseSpec
		suffix string
	}{
		{instance.Spec.DetailDatabase, "detaildb"},
		{instance.Spec.ResultDatabase, "resultdb"},
	} {
		if err := r.reconcileDatabase(ctx, instance, db.spec, db.suffix); err != nil {
			return err
		}
	}
	if err := r.reconcileTranslator(ctx, instance); err != nil {
		return err
	}
	if err := r.reconcileRunnerServiceAccount(ctx, instance); err != nil {
		return err
	}
	return nil
}

// reconcileDatabase reconciles an image-based database Deployment, Service, and
// connection Secret, or a host-based database connection Secret only.
func (r *Alpha4SimulationExperimentReconciler) reconcileDatabase(ctx context.Context, instance *experimentalpha4.SimulationExperiment, spec experimentalpha4.DatabaseSpec, suffix string) error {
	classified, _ := dbendpoint.ClassifyHost(spec.Host)
	imageForm := spec.Image != ""
	svcName := instance.Name + "-" + suffix + "-svc"
	secretName := instance.Name + "-" + suffix + "-sct"

	if imageForm {
		dep := &appsv1.Deployment{ObjectMeta: metav1ObjectName(instance, instance.Name+"-"+suffix)}
		if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
			if err := controllerutil.SetControllerReference(instance, dep, r.Scheme); err != nil {
				return err
			}
			labels := workloadLabels(instance.Name+"-"+suffix, instance.Name)
			dep.Labels = labels
			dep.Spec.Selector = metav1Selector(labels)
			dep.Spec.Template.ObjectMeta.Labels = labels
			ctr := corev1.Container{
				Name:  suffix,
				Image: spec.Image,
				Ports: []corev1.ContainerPort{{ContainerPort: spec.Port}},
				Env: []corev1.EnvVar{
					{Name: "DB_NAME", Value: spec.DBName},
					{Name: "DB_USER", Value: spec.User},
					{Name: "DB_PASSWORD", Value: spec.Password},
				},
			}
			ctr.Env = append(ctr.Env, simulationProjectEnvVar())
			if len(spec.Command) > 0 {
				ctr.Command = spec.Command
			}
			if len(spec.Args) > 0 {
				ctr.Args = spec.Args
			}
			dep.Spec.Template.Spec.Containers = []corev1.Container{ctr}
			// Exactly one pull-Secret reference; the registry Secret is never
			// mounted, exposed through env, or otherwise injected here.
			dep.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: registryAuthSecretName}}
			return nil
		}); err != nil {
			return fmt.Errorf("reconcile %s Deployment: %w", suffix, err)
		}

		svc := &corev1.Service{ObjectMeta: metav1ObjectName(instance, svcName)}
		if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
			if err := controllerutil.SetControllerReference(instance, svc, r.Scheme); err != nil {
				return err
			}
			applyServiceSpec(svc, spec.ServiceType, spec.Port, spec.NodePort, instance.Name+"-"+suffix)
			return nil
		}); err != nil {
			return fmt.Errorf("reconcile %s Service: %w", suffix, err)
		}
	}

	secret := &corev1.Secret{ObjectMeta: metav1ObjectName(instance, secretName)}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if err := controllerutil.SetControllerReference(instance, secret, r.Scheme); err != nil {
			return err
		}
		var host string
		if imageForm {
			host = fmt.Sprintf("%s.%s.svc.cluster.local", svcName, instance.Namespace)
		} else {
			host = classified.Normalized
		}
		secret.Type = corev1.SecretTypeOpaque
		secret.StringData = map[string]string{
			"host":     host,
			"port":     fmt.Sprintf("%d", spec.Port),
			"dbname":   spec.DBName,
			"user":     spec.User,
			"password": spec.Password,
		}
		return nil
	}); err != nil {
		return fmt.Errorf("reconcile %s connection Secret: %w", suffix, err)
	}
	return nil
}

// reconcileTranslator reconciles the mock-style ConfigMap, the two-container
// rootless BuildKit Deployment, and the Translator Service.
func (r *Alpha4SimulationExperimentReconciler) reconcileTranslator(ctx context.Context, instance *experimentalpha4.SimulationExperiment) error {
	t := instance.Spec.Translator

	cmName := instance.Name + "-translator-cfg"
	cm := &corev1.ConfigMap{ObjectMeta: metav1ObjectName(instance, cmName)}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if err := controllerutil.SetControllerReference(instance, cm, r.Scheme); err != nil {
			return err
		}
		cm.Data = map[string]string{"REPOSITORY": t.Repository, "BASEIMAGE": t.BaseImage}
		return nil
	}); err != nil {
		return fmt.Errorf("reconcile Translator ConfigMap: %w", err)
	}

	effective, err := EffectiveBuilderResources(t.BuilderResources)
	if err != nil {
		return fmt.Errorf("compute builder resources: %w", err)
	}

	dep := &appsv1.Deployment{ObjectMeta: metav1ObjectName(instance, instance.Name+"-translator")}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		if err := controllerutil.SetControllerReference(instance, dep, r.Scheme); err != nil {
			return err
		}
		labels := workloadLabels(instance.Name+"-translator", instance.Name)
		dep.Labels = labels
		dep.Spec.Selector = metav1Selector(labels)
		dep.Spec.Template.ObjectMeta.Labels = labels
		dep.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{FSGroup: int64Ptr(1000)}
		dep.Spec.Template.Spec.Volumes = translatorVolumes(instance.Name)

		translatorCtr := corev1.Container{
			Name:  "translator",
			Image: t.Image,
			Env: []corev1.EnvVar{
				{Name: "REPOSITORY", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: cmName}, Key: "REPOSITORY"}}},
				{Name: "BASEIMAGE", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: cmName}, Key: "BASEIMAGE"}}},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "workspace", MountPath: "/workspace"},
				{Name: "run-buildkit", MountPath: "/run/buildkit"},
				{Name: "registry-auth", MountPath: "/registry-auth", ReadOnly: true},
				{Name: "detaildb-connection", MountPath: "/detaildb-connection", ReadOnly: true},
				{Name: "resultdb-connection", MountPath: "/resultdb-connection", ReadOnly: true},
			},
			SecurityContext: restrictedSecurityContext(),
		}
		translatorCtr.Env = append(translatorCtr.Env, simulationProjectEnvVar())
		if len(t.Command) > 0 {
			translatorCtr.Command = t.Command
		}
		if len(t.Args) > 0 {
			translatorCtr.Args = t.Args
		}

		buildkitCtr := corev1.Container{
			Name:  "buildkit",
			Image: t.BuilderImage,
			Command: []string{
				"buildkitd",
				"--addr", "unix:///run/buildkit/buildkitd.sock",
				"--oci-worker-no-process-sandbox",
			},
			Resources: effective,
			VolumeMounts: []corev1.VolumeMount{
				{Name: "workspace", MountPath: "/workspace"},
				{Name: "run-buildkit", MountPath: "/run/buildkit"},
				{Name: "registry-auth", MountPath: "/registry-auth", ReadOnly: true},
			},
			SecurityContext: buildkitSecurityContext(),
			StartupProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					Exec: &corev1.ExecAction{Command: []string{"buildctl", "--addr", "unix:///run/buildkit/buildkitd.sock", "debug", "workers"}},
				},
				PeriodSeconds:    1,
				FailureThreshold: 60,
			},
		}

		dep.Spec.Template.Spec.Containers = []corev1.Container{translatorCtr, buildkitCtr}
		return nil
	}); err != nil {
		return fmt.Errorf("reconcile Translator Deployment: %w", err)
	}

	svc := &corev1.Service{ObjectMeta: metav1ObjectName(instance, instance.Name+"-translator-svc")}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(instance, svc, r.Scheme); err != nil {
			return err
		}
		applyServiceSpec(svc, t.ServiceType, t.Port, t.NodePort, instance.Name+"-translator")
		return nil
	}); err != nil {
		return fmt.Errorf("reconcile Translator Service: %w", err)
	}
	return nil
}

// reconcileRunnerServiceAccount reconciles the deterministic runner
// ServiceAccount. It grants no workload permissions and disables service
// account token automount. The ServiceAccount name is the deterministic
// simrunner-<12-char-UID-prefix> form required by the Scenario Manager runner
// Job contract: SM references this ServiceAccount by exact name in the runner
// Job pod template and resolves it with a get-only RBAC grant, so the Operator
// must produce the deterministic name that SM computes from the same UID.
func (r *Alpha4SimulationExperimentReconciler) reconcileRunnerServiceAccount(ctx context.Context, instance *experimentalpha4.SimulationExperiment) error {
	sa := &corev1.ServiceAccount{ObjectMeta: metav1ObjectName(instance, RunnerServiceAccountName(instance.UID))}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		if err := controllerutil.SetControllerReference(instance, sa, r.Scheme); err != nil {
			return err
		}
		automount := false
		sa.AutomountServiceAccountToken = &automount
		return nil
	}); err != nil {
		return fmt.Errorf("reconcile runner ServiceAccount: %w", err)
	}
	return nil
}

// translatorVolumes builds the shared workspace and BuildKit socket emptyDirs,
// the registry Secret (mounted read-only by both containers), and the two
// database connection Secrets (mounted read-only by the Translator only).
func translatorVolumes(experimentName string) []corev1.Volume {
	return []corev1.Volume{
		{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "run-buildkit", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "registry-auth", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName: registryAuthSecretName,
			Items:      []corev1.KeyToPath{{Key: dockerConfigSecretKey, Path: "config.json"}},
		}}},
		{Name: "detaildb-connection", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName: experimentName + "-detaildb-sct",
		}}},
		{Name: "resultdb-connection", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName: experimentName + "-resultdb-sct",
		}}},
	}
}

// restrictedSecurityContext is the normal restricted profile for the
// Translator container.
func restrictedSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsUser:                int64Ptr(1000),
		RunAsGroup:               int64Ptr(1000),
		RunAsNonRoot:             boolPtr(true),
		AllowPrivilegeEscalation: boolPtr(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

// buildkitSecurityContext is the required rootless, non-privileged BuildKit
// compatibility exception: Unconfined seccomp and AppArmor plus the
// --oci-worker-no-process-sandbox flag, without privilege escalation,
// privileged mode, host networking, host paths, or host runtime sockets.
func buildkitSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsUser:                int64Ptr(1000),
		RunAsGroup:               int64Ptr(1000),
		RunAsNonRoot:             boolPtr(true),
		AllowPrivilegeEscalation: boolPtr(false),
		Privileged:               boolPtr(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined},
		AppArmorProfile:          &corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeUnconfined},
	}
}

// applyServiceSpec sets a single-port Service spec for a component. It is
// idempotent and preserves the controller reference set by the caller.
func applyServiceSpec(svc *corev1.Service, serviceType experimentalpha4.ServiceType, port int32, nodePort *int32, selectorApp string) {
	svc.Spec.Type = corev1.ServiceType(serviceType)
	if svc.Spec.Type == "" {
		svc.Spec.Type = corev1.ServiceTypeClusterIP
	}
	svc.Spec.Selector = map[string]string{"app": selectorApp}
	portSpec := corev1.ServicePort{Port: port, TargetPort: intstr.FromInt(int(port))}
	if corev1.ServiceType(serviceType) == corev1.ServiceTypeNodePort && nodePort != nil {
		portSpec.NodePort = *nodePort
	}
	svc.Spec.Ports = []corev1.ServicePort{portSpec}
}

// dbEndpointFromSecret builds an availability-probe endpoint from the five
// connection Secret keys produced for a database.
func dbEndpointFromSecret(secret *corev1.Secret) (dbendpoint.Endpoint, error) {
	require := func(key string) (string, error) {
		v, ok := secret.Data[key]
		if !ok || len(v) == 0 {
			return "", fmt.Errorf("connection Secret %q missing key %q", secret.Name, key)
		}
		return string(v), nil
	}
	var ep dbendpoint.Endpoint
	var err error
	if ep.Host, err = require("host"); err != nil {
		return ep, err
	}
	portStr, err := require("port")
	if err != nil {
		return ep, err
	}
	var p int
	if _, err := fmt.Sscanf(portStr, "%d", &p); err != nil || p < 1 || p > 65535 {
		return ep, fmt.Errorf("connection Secret %q has invalid port %q", secret.Name, portStr)
	}
	ep.Port = int32(p)
	if ep.User, err = require("user"); err != nil {
		return ep, err
	}
	if ep.Password, err = require("password"); err != nil {
		return ep, err
	}
	if ep.DBName, err = require("dbname"); err != nil {
		return ep, err
	}
	return ep, nil
}

// patchPhase merges a phase and message update onto the live object.
func (r *Alpha4SimulationExperimentReconciler) patchPhase(ctx context.Context, instance *experimentalpha4.SimulationExperiment, phase, message string) error {
	base := instance.DeepCopy()
	patch := client.MergeFrom(base)
	instance.Status.Phase = phase
	instance.Status.Message = message
	if err := r.Status().Patch(ctx, instance, patch); err != nil {
		logf.FromContext(ctx).Error(err, "failed to patch alpha4 phase", "phase", phase)
		return err
	}
	return nil
}

// setErrorStatus moves the experiment to Error with a descriptive message.
func (r *Alpha4SimulationExperimentReconciler) setErrorStatus(ctx context.Context, instance *experimentalpha4.SimulationExperiment, message string) error {
	return r.patchPhase(ctx, instance, alpha4PhaseError, message)
}

// SetupWithManager wires the alpha4 reconciler. It is intentionally not called
// by main.go until the alpha4 cutover slice; tests may use it to start an
// isolated controller against the alpha4 envtest CRD.
func (r *Alpha4SimulationExperimentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&experimentalpha4.SimulationExperiment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		Named("alpha4-simulationexperiment").
		Complete(r)
}

// metav1ObjectName and metav1Selector are thin wrappers to keep call sites short.
func metav1ObjectName(instance *experimentalpha4.SimulationExperiment, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: instance.Namespace}
}

// runnerServiceAccountName returns the deterministic runner ServiceAccount
// name simrunner-<12-char-UID-prefix>, where the prefix is derived from the
// live experiment UID by lowercasing, stripping hyphens, and keeping the first
// 12 characters. This mirrors the UIDPrefix derivation in the Scenario Manager
// (scenario-manager/internal/alpha4/messaging) so both modules independently
// produce the same fixed contract name without a cross-module dependency. The
// derivation is duplicated by contract: the experiment-operator is a separate
// Go module and must not import the Scenario Manager internal package.

func metav1Selector(labels map[string]string) *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: labels}
}

// RunnerUIDPrefix derives the 12-character UID prefix from a full experiment
// UID by lowercasing, stripping hyphens, and keeping at most the first 12
// characters. It is the local replica of the Scenario Manager UIDPrefix rule.
func RunnerUIDPrefix(uid types.UID) string {
	stripped := strings.ToLower(strings.ReplaceAll(string(uid), "-", ""))
	if len(stripped) > 12 {
		stripped = stripped[:12]
	}
	return stripped
}

// RunnerServiceAccountName returns the deterministic runner ServiceAccount
// name for the given experiment UID.
func RunnerServiceAccountName(uid types.UID) string {
	return "simrunner-" + RunnerUIDPrefix(uid)
}

// int64Ptr and boolPtr are small helpers for security-context fields.
func int64Ptr(v int64) *int64 { return &v }
func boolPtr(v bool) *bool    { return &v }
