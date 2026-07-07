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

	experimentalpha2 "github.com/D4NS3U/cbse/experiment-operator/api/alpha2"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const legacyAlpha2UnsupportedMessage = "SimulationExperiment alpha2 is no longer supported; recreate this resource using experiment.cbse.terministic.de/alpha3."

// LegacySimulationExperimentReconciler rejects alpha2 resources without provisioning workloads.
type LegacySimulationExperimentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *LegacySimulationExperimentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	instance := &experimentalpha2.SimulationExperiment{}
	if err := r.Get(ctx, req.NamespacedName, instance); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !instance.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if instance.Status.Phase == "Error" && instance.Status.Message == legacyAlpha2UnsupportedMessage {
		return ctrl.Result{}, nil
	}

	base := instance.DeepCopy()
	patch := client.MergeFrom(base)
	instance.Status.Phase = "Error"
	instance.Status.Message = legacyAlpha2UnsupportedMessage

	if err := r.Status().Patch(ctx, instance, patch); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *LegacySimulationExperimentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&experimentalpha2.SimulationExperiment{}).
		Named("simulationexperiment-legacy-alpha2").
		Complete(r)
}
