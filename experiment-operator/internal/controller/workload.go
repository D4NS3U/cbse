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
// These values are fixed for alpha4: the NATS URL and stream name are the
// in-cluster smoke deployment values, and the ready-subject template is the
// canonical literal the reference framework validates verbatim.
const (
	translatorNATSURL              = "nats://sm-eds-nats:4222"
	translatorStream               = "cbse_translator"
	translatorReadySubjectTemplate = "cbse.{namespace}.{project}.trans.{scenario_id}.ready"
)

// ppsStream is the alpha4 PPS NATS/JetStream stream name. The PPS shares the
// translator flow's in-cluster NATS URL (translatorNATSURL) and rides its own
// stream for evaluation traffic.
const ppsStream = "cbse_pps"

// workloadLabels returns the shared labels carried by every Operator-managed
// workload: a short app name and the two reserved identity labels the downward
// API exposes to containers as SIMULATIONPROJECTNAME and SIMULATIONEXPERIMENTUID.
// Per the alpha4 identity contract every Operator-managed Pod template carries
// both the project and full experiment UID labels; metadata.name and
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

// ppsRequestSubject builds the canonical alpha4 PPS request subject
// cbse.<namespace>.<project>.pps.request from the owning experiment's
// namespace and name, mirroring translatorRequestSubject. The namespace and
// project tokens are already validated DNS labels by the CRD admission rule.
func ppsRequestSubject(namespace, project string) string {
	return fmt.Sprintf("cbse.%s.%s.pps.request", namespace, project)
}

// ppsEvaluationSubjectTemplate builds the canonical alpha4 PPS evaluation
// subject template cbse.<namespace>.<project>.pps.%s.evaluation, where the %s
// token is substituted with the scenario id by the PPS at publish time. The
// namespace and project tokens are already validated DNS labels by the CRD
// admission rule.
func ppsEvaluationSubjectTemplate(namespace, project string) string {
	return fmt.Sprintf("cbse.%s.%s.pps.%%s.evaluation", namespace, project)
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
