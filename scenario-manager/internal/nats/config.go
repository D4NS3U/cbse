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

// Package nats defines the canonical alpha4 NATS messaging contract for
// the Scenario Manager: subject-template validation, the three shared
// JetStream streams, the three SM-owned durable consumers, and the
// per-experiment Translator and PPS consumer naming, configuration, and
// ownership verification.
//
// All canonical values are fixed for alpha4. Deployment compatibility env
// variables, when explicitly set, must equal the canonical value or startup
// fails. The Scenario Manager reconciles the three shared streams and its
// three durable consumers to the exact configuration before business
// processing.
package nats

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	natsgo "github.com/nats-io/nats.go"
)

// AckWaitCanonical is the fixed alpha4 consumer ACK wait: two minutes.
const AckWaitCanonical = 2 * time.Minute

// MaxAckPendingSM is the fixed max-ack-pending for the three SM-owned consumers.
const MaxAckPendingSM = 1024

// MaxAckPendingTranslator is the fixed max-ack-pending for a per-experiment
// Translator consumer: a single in-flight request per Translator.
const MaxAckPendingTranslator = 1

// MaxDeliverCanonical is -1, meaning unlimited redelivery.
const MaxDeliverCanonical = -1

// Canonical subject templates and the env vars that may carry them. An
// explicitly configured value must equal the canonical value or startup fails.
const (
	edsAvailableTemplateEnv = "SCENARIO_MANAGER_EDS_AVAILABLE_SUBJECT_TEMPLATE"
	edsBatchTemplateEnv     = "SCENARIO_MANAGER_EDS_BATCH_SUBJECT_TEMPLATE"
	transRequestTemplateEnv = "SCENARIO_MANAGER_TRANS_REQUEST_SUBJECT_TEMPLATE"
	transReadyTemplateEnv   = "SCENARIO_MANAGER_TRANS_READY_SUBJECT_TEMPLATE"
	ppsRequestTemplateEnv   = "SCENARIO_MANAGER_PPS_REQUEST_SUBJECT_TEMPLATE"
	ppsEvalTemplateEnv      = "SCENARIO_MANAGER_PPS_EVALUATION_SUBJECT_TEMPLATE"

	// CanonicalEDSAvailableTemplate is the canonical EDS availability subject
	// template.
	CanonicalEDSAvailableTemplate = "cbse.{namespace}.{project}.eds.scenarios.available"
	// CanonicalEDSBatchTemplate is the canonical EDS batch subject template.
	CanonicalEDSBatchTemplate = "cbse.{namespace}.{project}.eds.scenarios"
	// CanonicalTranslatorRequestTemplate is the canonical Translator request
	// subject template.
	CanonicalTranslatorRequestTemplate = "cbse.{namespace}.{project}.trans.request"
	// CanonicalTranslatorReadyTemplate is the canonical Translator ready
	// subject template.
	CanonicalTranslatorReadyTemplate = "cbse.{namespace}.{project}.trans.{scenario_id}.ready"
	// CanonicalPPSRequestTemplate is the canonical PPS evaluation request
	// subject template.
	CanonicalPPSRequestTemplate = "cbse.{namespace}.{project}.pps.request"
	// CanonicalPPSEvaluationTemplate is the canonical PPS evaluation verdict
	// subject template.
	CanonicalPPSEvaluationTemplate = "cbse.{namespace}.{project}.pps.{scenario_id}.evaluation"
)

// Canonical stream names and the legacy env vars that may carry them.
const (
	edsStreamNameEnv        = "SCENARIO_MANAGER_EDS_STREAM_NAME"
	translatorStreamNameEnv = "SCENARIO_MANAGER_TRANSLATOR_STREAM_NAME"
	ppsStreamNameEnv        = "SCENARIO_MANAGER_PPS_STREAM_NAME"

	// EDSStreamName is the canonical JetStream stream for EDS batches.
	EDSStreamName = "cbse_eds_scenarios"
	// TranslatorStreamName is the canonical JetStream stream for Translator
	// requests and readiness.
	TranslatorStreamName = "cbse_translator"
	// PPSStreamName is the canonical JetStream stream for PPS evaluation
	// requests and verdicts.
	PPSStreamName = "cbse_pps"
)

// ErrNoncanonicalConfig is returned when an explicitly configured deployment
// compatibility value does not equal the canonical alpha4 value.
var ErrNoncanonicalConfig = errors.New("alpha4 messaging config is not canonical")

// TemplateEnv is a canonical subject template and the env var that may carry
// a compatibility override.
type TemplateEnv struct {
	Env       string
	Canonical string
}

// CanonicalTemplates returns the six canonical subject templates and their
// compatibility env vars in a fixed order.
func CanonicalTemplates() []TemplateEnv {
	return []TemplateEnv{
		{Env: edsAvailableTemplateEnv, Canonical: CanonicalEDSAvailableTemplate},
		{Env: edsBatchTemplateEnv, Canonical: CanonicalEDSBatchTemplate},
		{Env: transRequestTemplateEnv, Canonical: CanonicalTranslatorRequestTemplate},
		{Env: transReadyTemplateEnv, Canonical: CanonicalTranslatorReadyTemplate},
		{Env: ppsRequestTemplateEnv, Canonical: CanonicalPPSRequestTemplate},
		{Env: ppsEvalTemplateEnv, Canonical: CanonicalPPSEvaluationTemplate},
	}
}

// ValidateTemplates validates that any explicitly configured subject-template
// env var equals its canonical value. An empty (unset) env var is accepted:
// alpha4 uses the canonical value. A non-empty value that differs is a startup
// error. getenv defaults to os.Getenv; tests inject a fake.
func ValidateTemplates(getenv func(string) string) error {
	for _, t := range CanonicalTemplates() {
		raw := getenv(t.Env)
		if raw == "" {
			continue
		}
		if raw != t.Canonical {
			return fmt.Errorf("%w: %s=%q must equal %q", ErrNoncanonicalConfig, t.Env, raw, t.Canonical)
		}
	}
	// Alpha4 removes support for %s, fixed project-independent subjects, alternate
	// prefixes, and templates missing {namespace} or {project}. The canonical
	// values all contain {namespace} and {project} (or {scenario_id}); a value
	// that equals canonical cannot violate this, so no further check is needed.
	return nil
}

// ValidateTemplatesFromEnv validates the subject templates using os.Getenv.
func ValidateTemplatesFromEnv() error {
	return ValidateTemplates(os.Getenv)
}

// ValidateStreamNames validates that any explicitly configured legacy
// stream-name env var equals its canonical value. An empty (unset) env var is
// accepted; a non-empty value that differs is a startup error.
func ValidateStreamNames(getenv func(string) string) error {
	pairs := []struct {
		Env       string
		Canonical string
	}{
		{Env: edsStreamNameEnv, Canonical: EDSStreamName},
		{Env: translatorStreamNameEnv, Canonical: TranslatorStreamName},
		{Env: ppsStreamNameEnv, Canonical: PPSStreamName},
	}
	for _, p := range pairs {
		raw := getenv(p.Env)
		if raw == "" {
			continue
		}
		if raw != p.Canonical {
			return fmt.Errorf("%w: %s=%q must equal %q", ErrNoncanonicalConfig, p.Env, raw, p.Canonical)
		}
	}
	return nil
}

// ValidateStreamNamesFromEnv validates the stream names using os.Getenv.
func ValidateStreamNamesFromEnv() error {
	return ValidateStreamNames(os.Getenv)
}

// EDSStreamConfig returns the canonical alpha4 JetStream stream configuration
// for EDS batches: WorkQueuePolicy, FileStorage, DiscardOld, and the single
// subject cbse.*.*.eds.scenarios.
func EDSStreamConfig() *natsgo.StreamConfig {
	return &natsgo.StreamConfig{
		Name:      EDSStreamName,
		Retention: natsgo.WorkQueuePolicy,
		Storage:   natsgo.FileStorage,
		Discard:   natsgo.DiscardOld,
		Subjects:  []string{"cbse.*.*.eds.scenarios"},
	}
}

// TranslatorStreamConfig returns the canonical alpha4 JetStream stream
// configuration for Translator requests and readiness: WorkQueuePolicy,
// FileStorage, DiscardOld, and subjects cbse.*.*.trans.request and
// cbse.*.*.trans.*.ready.
func TranslatorStreamConfig() *natsgo.StreamConfig {
	return &natsgo.StreamConfig{
		Name:      TranslatorStreamName,
		Retention: natsgo.WorkQueuePolicy,
		Storage:   natsgo.FileStorage,
		Discard:   natsgo.DiscardOld,
		Subjects:  []string{"cbse.*.*.trans.request", "cbse.*.*.trans.*.ready"},
	}
}

// PPSStreamConfig returns the canonical alpha4 JetStream stream configuration
// for PPS evaluation requests and verdicts: WorkQueuePolicy, FileStorage,
// DiscardOld, and subjects cbse.*.*.pps.request and cbse.*.*.pps.*.evaluation.
// It mirrors the Translator stream's retention and limits policy exactly.
func PPSStreamConfig() *natsgo.StreamConfig {
	return &natsgo.StreamConfig{
		Name:      PPSStreamName,
		Retention: natsgo.WorkQueuePolicy,
		Storage:   natsgo.FileStorage,
		Discard:   natsgo.DiscardOld,
		Subjects:  []string{"cbse.*.*.pps.request", "cbse.*.*.pps.*.evaluation"},
	}
}

// EDSConsumerConfig returns the canonical alpha4 SM-owned EDS batch consumer
// configuration: durable scenario-manager-eds-consumer, queue group
// scenario-manager-eds, exact filter cbse.*.*.eds.scenarios, explicit ACK,
// DeliverAll, AckWait 2m, MaxAckPending 1024, MaxDeliver -1.
func EDSConsumerConfig() *natsgo.ConsumerConfig {
	return &natsgo.ConsumerConfig{
		Durable:        EDSConsumerName,
		DeliverPolicy:  natsgo.DeliverAllPolicy,
		AckPolicy:      natsgo.AckExplicitPolicy,
		AckWait:        AckWaitCanonical,
		MaxDeliver:     MaxDeliverCanonical,
		FilterSubject:  "cbse.*.*.eds.scenarios",
		MaxAckPending:  MaxAckPendingSM,
		DeliverGroup:   "scenario-manager-eds",
		DeliverSubject: EDSDeliverSubject,
	}
}

// TranslatorReadyConsumerConfig returns the canonical alpha4 SM-owned
// Translator-ready consumer configuration: durable and queue group
// scenario-manager-translator-ready, exact filter cbse.*.*.trans.*.ready,
// explicit ACK, DeliverAll, AckWait 2m, MaxAckPending 1024, MaxDeliver -1.
func TranslatorReadyConsumerConfig() *natsgo.ConsumerConfig {
	return &natsgo.ConsumerConfig{
		Durable:        TranslatorReadyConsumerName,
		DeliverPolicy:  natsgo.DeliverAllPolicy,
		AckPolicy:      natsgo.AckExplicitPolicy,
		AckWait:        AckWaitCanonical,
		MaxDeliver:     MaxDeliverCanonical,
		FilterSubject:  "cbse.*.*.trans.*.ready",
		MaxAckPending:  MaxAckPendingSM,
		DeliverGroup:   "scenario-manager-translator-ready",
		DeliverSubject: TranslatorReadyDeliverSubject,
	}
}

// PPSEvaluationConsumerConfig returns the canonical alpha4 SM-owned
// PPS-evaluation consumer configuration: durable and queue group
// scenario-manager-pps-evaluation, exact filter cbse.*.*.pps.*.evaluation,
// explicit ACK, DeliverAll, AckWait 2m, MaxAckPending 1024, MaxDeliver -1.
// It mirrors the Translator-ready consumer exactly.
func PPSEvaluationConsumerConfig() *natsgo.ConsumerConfig {
	return &natsgo.ConsumerConfig{
		Durable:        PPSEvaluationConsumerName,
		DeliverPolicy:  natsgo.DeliverAllPolicy,
		AckPolicy:      natsgo.AckExplicitPolicy,
		AckWait:        AckWaitCanonical,
		MaxDeliver:     MaxDeliverCanonical,
		FilterSubject:  "cbse.*.*.pps.*.evaluation",
		MaxAckPending:  MaxAckPendingSM,
		DeliverGroup:   "scenario-manager-pps-evaluation",
		DeliverSubject: PPSEvaluationDeliverSubject,
	}
}

// SM consumer deliver subjects. These are internal push-delivery subjects used
// by the SM-owned durable queue consumers; they are 4-token subjects that do
// not match any stream filter (cbse.*.*.eds.scenarios is 5 tokens;
// cbse.*.*.trans.*.ready is 6 tokens), so delivered messages never re-enter a
// stream. The queue group on each consumer distributes a given delivery among
// SM replicas.
const (
	EDSDeliverSubject             = "cbse.sm.eds-batch.deliver"
	TranslatorReadyDeliverSubject = "cbse.sm.translator-ready.deliver"
	PPSEvaluationDeliverSubject   = "cbse.sm.pps-evaluation.deliver"
)

// SM consumer durable names.
const (
	// EDSConsumerName is the durable name of the SM-owned EDS batch consumer.
	EDSConsumerName = "scenario-manager-eds-consumer"
	// TranslatorReadyConsumerName is the durable name (and queue group) of the
	// SM-owned Translator-ready consumer.
	TranslatorReadyConsumerName = "scenario-manager-translator-ready"
	// PPSEvaluationConsumerName is the durable name (and queue group) of the
	// SM-owned PPS-evaluation consumer.
	PPSEvaluationConsumerName = "scenario-manager-pps-evaluation"
)

// SMConsumers returns the canonical SM-owned consumer configurations in a
// fixed order, each tagged with the stream it belongs to.
func SMConsumers() []SMConsumer {
	return []SMConsumer{
		{Stream: EDSStreamName, Config: EDSConsumerConfig()},
		{Stream: TranslatorStreamName, Config: TranslatorReadyConsumerConfig()},
		{Stream: PPSStreamName, Config: PPSEvaluationConsumerConfig()},
	}
}

// SMConsumer pairs a canonical SM-owned consumer config with its stream.
type SMConsumer struct {
	Stream string
	Config *natsgo.ConsumerConfig
}

// Translator consumer metadata keys. These four entries are part of the
// consumer identity and make deletion-time ownership verifiable without
// relying only on the truncated durable name.
const (
	metaManagedBy     = "experiment.cbse.terministic.de/managed-by"
	metaExperimentUID = "experiment.cbse.terministic.de/experiment-uid"
	metaNamespace     = "experiment.cbse.terministic.de/namespace"
	metaProject       = "experiment.cbse.terministic.de/project"

	// metaManagedByValue identifies a per-experiment Translator consumer.
	metaManagedByValue = "translator"
)

// UIDPrefix returns the canonical experiment-UID prefix used by per-experiment
// deterministic names: the full UID is lowercased, hyphens are removed, and the
// first 12 characters are taken. It is shared by the Translator durable consumer
// name and the runner Job name so both follow the same incarnation identity.
func UIDPrefix(uid string) string {
	stripped := strings.ToLower(strings.ReplaceAll(uid, "-", ""))
	if len(stripped) > 12 {
		stripped = stripped[:12]
	}
	return stripped
}

// TranslatorConsumerName returns the canonical per-experiment Translator
// durable consumer name: translator-<12-char-UID-prefix>.
func TranslatorConsumerName(uid string) string {
	return "translator-" + UIDPrefix(uid)
}

// translatorMetadata returns the four canonical ownership metadata entries
// for a per-experiment Translator consumer.
func translatorMetadata(uid, namespace, project string) map[string]string {
	return map[string]string{
		metaManagedBy:     metaManagedByValue,
		metaExperimentUID: uid,
		metaNamespace:     namespace,
		metaProject:       project,
	}
}

// metaManagedByPPS identifies a per-experiment PPS consumer in the canonical
// ownership metadata.
const metaManagedByPPS = "pps"

// ppsMetadata returns the four canonical ownership metadata entries for a
// per-experiment PPS consumer.
func ppsMetadata(uid, namespace, project string) map[string]string {
	return map[string]string{
		metaManagedBy:     metaManagedByPPS,
		metaExperimentUID: uid,
		metaNamespace:     namespace,
		metaProject:       project,
	}
}

// TranslatorConsumerConfig returns the canonical per-experiment Translator
// durable consumer configuration for the given experiment UID, namespace, and
// project. The filter is the exact request subject
// cbse.<namespace>.<project>.trans.request; the durable name is
// translator-<12-char-UID-prefix>; settings are explicit ACK, DeliverAll,
// AckWait 2m, MaxAckPending 1, MaxDeliver -1; and the four ownership metadata
// entries are set.
func TranslatorConsumerConfig(uid, namespace, project string) *natsgo.ConsumerConfig {
	return &natsgo.ConsumerConfig{
		Durable:       TranslatorConsumerName(uid),
		DeliverPolicy: natsgo.DeliverAllPolicy,
		AckPolicy:     natsgo.AckExplicitPolicy,
		AckWait:       AckWaitCanonical,
		MaxDeliver:    MaxDeliverCanonical,
		FilterSubject: fmt.Sprintf("cbse.%s.%s.trans.request", namespace, project),
		MaxAckPending: MaxAckPendingTranslator,
		Metadata:      translatorMetadata(uid, namespace, project),
	}
}

// PPSConsumerName returns the canonical per-experiment PPS durable consumer
// name: pps-<12-char-UID-prefix>.
func PPSConsumerName(uid string) string {
	return "pps-" + UIDPrefix(uid)
}

// PPSConsumerConfig returns the canonical per-experiment PPS durable consumer
// configuration for the given experiment UID, namespace, and project. The
// filter is the exact evaluation request subject
// cbse.<namespace>.<project>.pps.request; the durable name is
// pps-<12-char-UID-prefix>; settings are explicit ACK, DeliverAll, AckWait 2m,
// MaxAckPending 1, MaxDeliver -1 (the Translator consumer's exact settings);
// and the four ownership metadata entries are set.
func PPSConsumerConfig(uid, namespace, project string) *natsgo.ConsumerConfig {
	return &natsgo.ConsumerConfig{
		Durable:       PPSConsumerName(uid),
		DeliverPolicy: natsgo.DeliverAllPolicy,
		AckPolicy:     natsgo.AckExplicitPolicy,
		AckWait:       AckWaitCanonical,
		MaxDeliver:    MaxDeliverCanonical,
		FilterSubject: fmt.Sprintf("cbse.%s.%s.pps.request", namespace, project),
		MaxAckPending: MaxAckPendingTranslator,
		Metadata:      ppsMetadata(uid, namespace, project),
	}
}

// ErrOwnershipCollision is returned when an existing consumer's identity does
// not match the expected owning experiment. The caller must not delete,
// update, or adopt such a consumer.
var ErrOwnershipCollision = errors.New("consumer ownership collision")
