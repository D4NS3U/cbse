package controller

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

const (
	simulationProjectNameEnvVar = "SIMULATIONPROJECTNAME"
	simulationProjectLabelKey   = "experiment.cbse.terministic.de/project"
)

// workloadLabels returns the shared labels carried by every Operator-managed
// workload: a short app name and the reserved project label that the downward
// API exposes to containers as SIMULATIONPROJECTNAME.
func workloadLabels(appName, projectName string) map[string]string {
	return map[string]string{
		"app":                     appName,
		simulationProjectLabelKey: projectName,
	}
}

// simulationProjectEnvVar returns the downward-API env var that surfaces the
// reserved project label to workload containers.
func simulationProjectEnvVar() corev1.EnvVar {
	return corev1.EnvVar{
		Name: simulationProjectNameEnvVar,
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath: fmt.Sprintf("metadata.labels['%s']", simulationProjectLabelKey),
			},
		},
	}
}
