package controller

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

const (
	simulationProjectNameEnvVar     = "SIMULATIONPROJECTNAME"
	simulationProjectLabelKey       = "experiment.cbse.terministic.de/project"
	simulationExperimentUIDLabelKey = "experiment.cbse.terministic.de/experiment-uid"
)

// alpha4 Translator NATS/JetStream configuration injected by the Operator.
// These values are fixed for alpha4 (see slice 04): the NATS URL and stream
// name are the in-cluster smoke deployment values, and the ready-subject
// template is the canonical literal the reference framework validates verbatim.
const (
	translatorNATSURL              = "nats://sm-eds-nats:4222"
	translatorStream               = "cbse_translator"
	translatorReadySubjectTemplate = "cbse.{namespace}.{project}.trans.{scenario_id}.ready"
)

// workloadLabels returns the shared labels carried by every Operator-managed
// workload: a short app name and the two reserved identity labels the downward
// API exposes to containers as SIMULATIONPROJECTNAME and SIMULATIONEXPERIMENTUID.
// Per the alpha4 identity contract (slice 04) every Operator-managed Pod template
// carries both the project and full experiment UID labels; metadata.name and
// metadata.uid would identify the Pod rather than the owning experiment and are
// not used for these two values.
func workloadLabels(appName, projectName, experimentUID string) map[string]string {
	return map[string]string{
		"app":                           appName,
		simulationProjectLabelKey:       projectName,
		simulationExperimentUIDLabelKey: experimentUID,
	}
}

// translatorRequestSubject builds the canonical alpha4 Translator request
// subject cbse.<namespace>.<project>.trans.request from the owning
// experiment's namespace and name. The namespace and project tokens are already
// validated DNS labels by the CRD admission rule.
func translatorRequestSubject(namespace, project string) string {
	return fmt.Sprintf("cbse.%s.%s.trans.request", namespace, project)
}

// simulationProjectEnvVar returns the downward-API env var that surfaces the
// reserved project label to workload containers as SIMULATIONPROJECTNAME.
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

// simulationProjectNamespaceEnvVar returns the downward-API env var that
// surfaces the Pod namespace to workload containers as
// SIMULATIONPROJECTNAMESPACE. The Pod namespace is the owning experiment's
// namespace.
func simulationProjectNamespaceEnvVar() corev1.EnvVar {
	return corev1.EnvVar{
		Name: "SIMULATIONPROJECTNAMESPACE",
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
		},
	}
}

// simulationExperimentUIDEnvVar returns the downward-API env var that surfaces
// the full owning experiment UID to workload containers as
// SIMULATIONEXPERIMENTUID. The UID is read from the reserved
// experiment.cbse.terministic.de/experiment-uid Pod label injected by
// workloadLabels; metadata.uid would identify the Pod rather than the owning
// experiment and is not used.
func simulationExperimentUIDEnvVar() corev1.EnvVar {
	return corev1.EnvVar{
		Name: "SIMULATIONEXPERIMENTUID",
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath: fmt.Sprintf("metadata.labels['%s']", simulationExperimentUIDLabelKey),
			},
		},
	}
}
