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
	"net"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	experimentalpha1 "cbse.terministic.de/experiment-operator/api/alpha1"
)

// SimulationExperimentReconciler reconciles a SimulationExperiment object
type SimulationExperimentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Markers for Deployment, Service, Secret, and ConfigMap ownership
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments/finalizers,verbs=update

// +kubebuilder:rbac:groups="",resources=secrets;services;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets/finalizers;services/finalizers;configmaps/finalizers,verbs=update

// +kubebuilder:rbac:groups=experiment.cbse.terministic.de,resources=simulationexperiments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=experiment.cbse.terministic.de,resources=simulationexperiments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=experiment.cbse.terministic.de,resources=simulationexperiments/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// The current implementation of this method is a sequential process that ensures availability
// of all components required for the SimulationExperiment.
// In a production-grade operator, this would be more complex, using the concept of Controller Decomposition.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *SimulationExperimentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. Fetch the SimulationExperiment instance
	instance := &experimentalpha1.SimulationExperiment{}
	if err := r.Get(ctx, req.NamespacedName, instance); err != nil {
		// Ignore not-found errors (object deleted)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Finalizer name
	const finalizerName = "experiment.cbse.terministic.de/finalizer"

	// 2. Handle deletion logic
	if !instance.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(instance, finalizerName) {
			// Remove the finalizer so the CR can be fully deleted
			// When the finalizer is removed, the Kubernetes garbage-collects the CR automatically,
			// given that it set the deletionTimestamp.
			log.Info("Removing finalizer from SimulationExperiment", "name", instance.Name)
			controllerutil.RemoveFinalizer(instance, finalizerName)
			if err := r.Update(ctx, instance); err != nil {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{}, err
			}
		}
		// Stop reconciliation as object is being deleted
		return ctrl.Result{}, nil
	}

	// 3. Add finalizer if absent
	if !controllerutil.ContainsFinalizer(instance, finalizerName) {
		controllerutil.AddFinalizer(instance, finalizerName)
		if err := r.Update(ctx, instance); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil // Requeue to ensure finalizer is set
	}

	switch instance.Status.Phase {
	case "":
		// Use a DeepCopy and a Path to merge status updates
		base := instance.DeepCopy()
		patch := client.MergeFrom(base)
		instance.Status.Phase = "Pending"
		instance.Status.Message = "Waiting to provision dependencies"
		if err := r.Status().Patch(ctx, instance, patch); err != nil {
			log.Error(err, "Failed to patch initial phase")
			return ctrl.Result{}, err
		}
		// Requeue to proceed to provisioning
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil

	case "Pending":
		// Transition to Provisioning
		// Use a DeepCopy and a Path to merge status updates
		base := instance.DeepCopy()
		patch := client.MergeFrom(base)
		instance.Status.Phase = "Provisioning"
		instance.Status.Message = "Provisioning components"
		if err := r.Status().Patch(ctx, instance, patch); err != nil {
			log.Error(err, "Failed to patch phase to Provisioning")
			return ctrl.Result{}, err
		}

		// Create both databases
		if res, err := r.reconcileDatabase(ctx, instance, instance.Spec.DetailDatabase, "detaildb"); err != nil || res.RequeueAfter > 0 {
			return res, err
		}
		if res, err := r.reconcileDatabase(ctx, instance, instance.Spec.ResultDatabase, "resultdb"); err != nil || res.RequeueAfter > 0 {
			return res, err
		}

		// Translator
		if res, err := r.reconcileTranslator(ctx, instance, instance.Spec.Translator); err != nil || res.RequeueAfter > 0 {
			return res, err
		}

		// PostProcessingService
		if res, err := r.reconcilePPS(ctx, instance, instance.Spec.PostProcessingService); err != nil || res.RequeueAfter > 0 {
			return res, err
		}

		// ExperimentalDesign
		if res, err := r.reconcileExperimentalDesign(ctx, instance, instance.Spec.ExperimentalDesign); err != nil || res.RequeueAfter > 0 {
			return res, err
		}

		// Requeue to check readiness of all components
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil

	case "Provisioning":
		ready, err := r.allComponentsReady(ctx, instance)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !ready {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		// If all components are ready:
		// Use a DeepCopy and a Path to merge status updates
		base := instance.DeepCopy()
		patch := client.MergeFrom(base)
		instance.Status.Phase = "InProgress"
		instance.Status.Message = "All components provisioned; validating spec"
		if err := r.Status().Patch(ctx, instance, patch); err != nil {
			log.Error(err, "Failed to patch phase to InProgress")
			return ctrl.Result{}, err
		}
		// Exiting reconcile loop at Phase "InProgress"
		return ctrl.Result{}, nil

	default:
		return ctrl.Result{}, nil
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *SimulationExperimentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create index function
	indexFn := func(rawObj client.Object) []string {
		if owner := metav1.GetControllerOf(rawObj); owner != nil {
			return []string{owner.Name}
		}
		return nil
	}
	// register an index on each of your sub-resource types:
	for _, obj := range []client.Object{
		&appsv1.Deployment{},
		&corev1.Service{},
		&corev1.Secret{},
		&corev1.ConfigMap{},
	} {
		if err := mgr.GetFieldIndexer().IndexField(context.Background(), obj, ".metadata.controller", indexFn); err != nil {
			return err
		}
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&experimentalpha1.SimulationExperiment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{}).
		Named("simulationexperiment").
		Complete(r)
}

// *****************************************************************************************************************
// Auxilliary methods to keep the Reconcile method more readable
// *****************************************************************************************************************
//
// 11-07-2025-11:23: Dropping the cleanupComponents method, as it is not used in the current implementation.

// reconcileDatabase handles the creation of a database deployment and service
func (r *SimulationExperimentReconciler) reconcileDatabase(
	ctx context.Context,
	instance *experimentalpha1.SimulationExperiment,
	spec experimentalpha1.DatabaseSpec,
	suffix string,
) (ctrl.Result, error) {
	// Host-based DB: smoke-test connectivity
	if spec.Host != "" {
		addr := net.JoinHostPort(spec.Host, fmt.Sprintf("%d", spec.Port))
		conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
		if err != nil {
			instance.Status.Phase = "Error"
			instance.Status.Message = fmt.Sprintf("Cannot connect to %s at %s", suffix, addr)
			_ = r.Status().Update(ctx, instance)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil // Requeue to retry connection
		}
		conn.Close()
		return ctrl.Result{}, nil
	}
	// Image-based DB: Deployment + Service
	// Prepare Deployment for the database
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name + "-" + suffix,
			Namespace: instance.Namespace,
		},
	}
	// Operation 1: Create or update the Deployment
	op1, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		labels := map[string]string{"app": instance.Name + "-" + suffix}
		if dep.Labels == nil {
			dep.Labels = labels
		}
		// Set controller reference to ensure garbage collection
		if err := controllerutil.SetControllerReference(instance, dep, r.Scheme); err != nil {
			return err
		}
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
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
		// Add optional command and args
		if len(spec.Command) > 0 {
			ctr.Command = spec.Command
		}
		if len(spec.Args) > 0 {
			ctr.Args = spec.Args
		}
		dep.Spec.Template.Spec.Containers = []corev1.Container{ctr}
		return nil
	})
	// Handle error during Deployment creation/update
	if err != nil {
		instance.Status.Phase = "Error"
		instance.Status.Message = fmt.Sprintf("Failed to create/update %s Deployment: %v", suffix, err)
		_ = r.Status().Update(ctx, instance)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil // Requeue to retry
	}
	logf.FromContext(ctx).Info("Reconciled database Deployment", "name", dep.Name, "op", op1)

	// Prepare Service for the database
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name + "-" + suffix + "-svc",
			Namespace: instance.Namespace,
		},
	}
	// Operation 2: Create or update the Service
	op2, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		// Set controller reference to ensure garbage collection
		if err := controllerutil.SetControllerReference(instance, svc, r.Scheme); err != nil {
			return err
		}

		svc.Spec.Selector = map[string]string{"app": instance.Name + "-" + suffix}
		svc.Spec.Ports = []corev1.ServicePort{{
			Port:       spec.Port,
			TargetPort: intstr.FromInt(int(spec.Port)),
		}}
		return nil
	})
	if err != nil {
		instance.Status.Phase = "Error"
		instance.Status.Message = fmt.Sprintf("Failed to create/update %s Service: %v", suffix, err)
		_ = r.Status().Update(ctx, instance)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil // Requeue to retry
	}
	logf.FromContext(ctx).Info("Reconciled database Service", "name", svc.Name, "op", op2)

	// Prepare Secret for the database connection
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      instance.Name + "-" + suffix + "-sct",
		Namespace: instance.Namespace,
	}}
	// Operation 3: Create or update the Secret
	op3, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		// Set controller reference to ensure garbage collection
		if err := controllerutil.SetControllerReference(instance, secret, r.Scheme); err != nil {
			return err
		}

		var dbHost string
		if spec.Host != "" {
			// Host-based DB
			dbHost = spec.Host
		} else {
			// fully qualified if you need it:
			dbHost = fmt.Sprintf("%s.%s.svc.cluster.local", svc.Name, svc.Namespace)
		}

		secret.Type = corev1.SecretTypeOpaque
		secret.StringData = map[string]string{
			"host":     dbHost,
			"port":     fmt.Sprintf("%d", spec.Port),
			"dbname":   spec.DBName,
			"user":     spec.User,
			"password": spec.Password,
		}
		return nil
	})
	if err != nil {
		instance.Status.Phase = "Error"
		instance.Status.Message = fmt.Sprintf("Failed to create/update %s connection Secret: %v", suffix, err)
		_ = r.Status().Update(ctx, instance)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil // Requeue to retry
	}
	logf.FromContext(ctx).Info("Reconciled database connection Secret", "name", secret.Name, "op", op3)

	// 11-07-2025-11:10: Dropping requeue logic for Deployment readiness, as we implement a new logic using indexing
	return ctrl.Result{}, nil
}

// reconcileTranslator handles Deployment and ConfigMap creation for the Translator
func (r *SimulationExperimentReconciler) reconcileTranslator(
	ctx context.Context,
	instance *experimentalpha1.SimulationExperiment,
	spec experimentalpha1.TranslatorSpec,
) (ctrl.Result, error) {
	// Prepare ConfigMap for Translator
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name + "-translator-cfg",
			Labels:    map[string]string{"app": instance.Name + "-translator"},
			Namespace: instance.Namespace,
		},
	}

	// Operation 1: Create or update ConfigMap containing repository URL and base image
	op1, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		// Set controller reference to ensure garbage collection
		if err := controllerutil.SetControllerReference(instance, cm, r.Scheme); err != nil {
			return err
		}
		cm.Data = map[string]string{
			"REPOSITORY": spec.Repository,
			"BASEIMAGE":  spec.BaseImage,
		}
		return nil
	})
	if err != nil {
		instance.Status.Phase = "Error"
		instance.Status.Message = fmt.Sprintf("Failed to create/update Translator ConfigMap: %v", err)
		_ = r.Status().Update(ctx, instance)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil // Requeue to retry
	}
	logf.FromContext(ctx).Info("Reconciled Translator ConfigMap", "name", cm.Name, "op", op1)

	// Prepare Translator Deployment
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name + "-translator",
			Namespace: instance.Namespace,
		},
	}

	// Operation 2: Create or update Translator Deployment
	op2, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		// Set controller reference to ensure garbage collection
		if err := controllerutil.SetControllerReference(instance, dep, r.Scheme); err != nil {
			return err
		}
		labels := map[string]string{"app": instance.Name + "-translator"}
		if dep.Labels == nil {
			dep.Labels = labels
		}
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		dep.Spec.Template.ObjectMeta.Labels = labels

		// Add ConfigMap as volume
		dep.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name: "translator-config",
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cm.Name},
			}},
		}}

		// Set container image and mount ConfigMap
		ctr := corev1.Container{
			Name:  "translator",
			Image: spec.Image,
			Env: []corev1.EnvVar{
				{Name: "REPOSITORY", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: cm.Name}, Key: "REPOSITORY"}}},
				{Name: "BASEIMAGE", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: cm.Name}, Key: "BASEIMAGE"}}},
			},
		}
		// Add optional command and args
		if len(spec.Command) > 0 {
			ctr.Command = spec.Command
		}
		if len(spec.Args) > 0 {
			ctr.Args = spec.Args
		}
		dep.Spec.Template.Spec.Containers = []corev1.Container{ctr}
		return nil
	})
	if err != nil {
		instance.Status.Phase = "Error"
		instance.Status.Message = fmt.Sprintf("Failed to create/update Translator Deployment: %v", err)
		_ = r.Status().Update(ctx, instance)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil // Requeue to retry
	}
	logf.FromContext(ctx).Info("Reconciled Translator Deployment", "name", dep.Name, "op", op2)

	// 11-07-2025-11:16: Dropping requeue logic for Deployment readiness, as we implement a new logic using indexing
	return ctrl.Result{}, nil
}

// reconcilePPS handles the creation of the PostProcessingService Deployment
func (r *SimulationExperimentReconciler) reconcilePPS(
	ctx context.Context,
	instance *experimentalpha1.SimulationExperiment,
	spec experimentalpha1.PostProcessingSpec,
) (ctrl.Result, error) {
	// Define Deployment object
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name + "-postproc",
			Namespace: instance.Namespace,
		},
	}
	// Create or update the Deployment
	op1, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		// Set controller reference to ensure garbage collection
		if err := controllerutil.SetControllerReference(instance, dep, r.Scheme); err != nil {
			return err
		}
		labels := map[string]string{"app": instance.Name + "-postproc"}
		if dep.Labels == nil {
			dep.Labels = labels
		}
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		dep.Spec.Template.ObjectMeta.Labels = labels
		// Single container using the specified image
		ctr := corev1.Container{
			Name:  "postprocessing",
			Image: spec.Image,
		}
		// Add optional command and args
		if len(spec.Command) > 0 {
			ctr.Command = spec.Command
		}
		if len(spec.Args) > 0 {
			ctr.Args = spec.Args
		}
		dep.Spec.Template.Spec.Containers = []corev1.Container{ctr}
		return nil
	})
	if err != nil {
		instance.Status.Phase = "Error"
		instance.Status.Message = fmt.Sprintf("Failed to reconcile PostProcessingService Deployment: %v", err)
		_ = r.Status().Update(ctx, instance)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil // Requeue to retry
	}
	logf.FromContext(ctx).Info("Reconciled PostProcessingService Deployment", "name", dep.Name, "op", op1)

	// 11-07-2025-11:17: Dropping requeue logic for Deployment readiness, as we implement a new logic using indexing
	return ctrl.Result{}, nil
}

// reconcileExperimentalDesign handles creation/update of the ExperimentalDesign ConfigMap
func (r *SimulationExperimentReconciler) reconcileExperimentalDesign(
	ctx context.Context,
	instance *experimentalpha1.SimulationExperiment,
	design string,
) (ctrl.Result, error) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name + "-design",
			Labels:    map[string]string{"app": instance.Name + "-design"},
			Namespace: instance.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		// Set controller reference to ensure garbage collection
		if err := controllerutil.SetControllerReference(instance, cm, r.Scheme); err != nil {
			return err
		}
		cm.Data = map[string]string{"experimentalDesign": design}
		return nil
	})
	if err != nil {
		instance.Status.Phase = "Error"
		instance.Status.Message = fmt.Sprintf("Failed to reconcile ExperimentalDesign ConfigMap: %v", err)
		_ = r.Status().Update(ctx, instance)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil // Requeue to retry
	}
	logf.FromContext(ctx).Info("Reconciled ExperimentalDesign ConfigMap", "name", cm.Name)

	// Config is static, no requeue needed
	return ctrl.Result{}, nil
}

// Helper function to check if all components are ready.
// Returns true if all components are ready, false otherwise.
// allComponentsReady returns true when all Deployments and ConfigMaps are present and healthy.
func (r *SimulationExperimentReconciler) allComponentsReady(
	ctx context.Context,
	instance *experimentalpha1.SimulationExperiment,
) (bool, error) {
	// Helper to check a Deployment with label app=<name>-<suffix>.
	checkDep := func(suffix string) (bool, error) {
		var list appsv1.DeploymentList
		if err := r.List(ctx, &list,
			client.InNamespace(instance.Namespace),
			client.MatchingLabels{"app": instance.Name + "-" + suffix},
			client.MatchingFields{".metadata.controller": instance.Name},
		); err != nil {
			return false, err
		}
		if len(list.Items) == 0 || list.Items[0].Status.ReadyReplicas < 1 {
			return false, nil
		}
		return true, nil
	}

	// 1) Check all four Deployments
	for _, suffix := range []string{"detaildb", "resultdb", "translator", "postproc"} {
		if suffix == "detaildb" && instance.Spec.DetailDatabase.Host != "" {
			continue
		}
		if suffix == "resultdb" && instance.Spec.ResultDatabase.Host != "" {
			continue
		}
		ok, err := checkDep(suffix)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}

		// Testing the connectivity for container-based databases
		// if suffix == "detaildb" && instance.Spec.DetailDatabase.Host == "" {
		// 	svcFQDN := fmt.Sprintf("%s-%s-svc.%s.svc.cluster.local",
		// 		instance.Name, suffix, instance.Namespace,
		// 	)
		// 	addr := net.JoinHostPort(svcFQDN,
		// 		fmt.Sprintf("%d", instance.Spec.DetailDatabase.Port),
		// 	)
		// 	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		// 	if err != nil {
		// 		return false, fmt.Errorf("cannot dial %s: %w", addr, err)
		// 	}
		// 	conn.Close()
		// }
		// if suffix == "resultdb" && instance.Spec.ResultDatabase.Host == "" {
		// 	svcFQDN := fmt.Sprintf("%s-%s-svc.%s.svc.cluster.local",
		// 		instance.Name, suffix, instance.Namespace,
		// 	)
		// 	addr := net.JoinHostPort(svcFQDN,
		// 		fmt.Sprintf("%d", instance.Spec.ResultDatabase.Port),
		// 	)
		// 	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		// 	if err != nil {
		// 		return false, fmt.Errorf("cannot dial %s: %w", addr, err)
		// 	}
		// 	conn.Close()
		// }
	}

	// 2) Check the two ConfigMaps: translator & design
	for _, suffix := range []string{"translator", "design"} {
		var cms corev1.ConfigMapList
		if err := r.List(ctx, &cms,
			client.InNamespace(instance.Namespace),
			client.MatchingLabels{"app": instance.Name + "-" + suffix},
			client.MatchingFields{".metadata.controller": instance.Name},
		); err != nil {
			return false, err
		}
		if len(cms.Items) == 0 {
			return false, nil
		}
	}

	// All checks passed
	return true, nil
}
