// Package messaging defines the canonical alpha4 NATS messaging contract for
// the Scenario Manager: subject-template validation, the two shared JetStream
// streams, the two SM-owned durable consumers, and the per-experiment
// Translator consumer naming, configuration, and ownership verification.
//
// All canonical values are fixed for alpha4. Deployment compatibility env
// variables, when explicitly set, must equal the canonical value or startup
// fails. The Scenario Manager reconciles the two shared streams and its two
// durable consumers to the exact configuration before business processing.
//
// This package is additive and isolated until the alpha4 cutover in a later
// slice: it does not replace the active alpha3 messaging wiring.
package messaging

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

// MaxAckPendingSM is the fixed max-ack-pending for the two SM-owned consumers.
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
)

// Canonical stream names and the legacy env vars that may carry them.
const (
	edsStreamNameEnv        = "SCENARIO_MANAGER_EDS_STREAM_NAME"
	translatorStreamNameEnv = "SCENARIO_MANAGER_TRANSLATOR_STREAM_NAME"

	// EDSStreamName is the canonical JetStream stream for EDS batches.
	EDSStreamName = "cbse_eds_scenarios"
	// TranslatorStreamName is the canonical JetStream stream for Translator
	// requests and readiness.
	TranslatorStreamName = "cbse_translator"
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

// CanonicalTemplates returns the four canonical subject templates and their
// compatibility env vars in a fixed order.
func CanonicalTemplates() []TemplateEnv {
	return []TemplateEnv{
		{Env: edsAvailableTemplateEnv, Canonical: CanonicalEDSAvailableTemplate},
		{Env: edsBatchTemplateEnv, Canonical: CanonicalEDSBatchTemplate},
		{Env: transRequestTemplateEnv, Canonical: CanonicalTranslatorRequestTemplate},
		{Env: transReadyTemplateEnv, Canonical: CanonicalTranslatorReadyTemplate},
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

// EDSConsumerConfig returns the canonical alpha4 SM-owned EDS batch consumer
// configuration: durable scenario-manager-eds-consumer, queue group
// scenario-manager-eds, exact filter cbse.*.*.eds.scenarios, explicit ACK,
// DeliverAll, AckWait 2m, MaxAckPending 1024, MaxDeliver -1.
func EDSConsumerConfig() *natsgo.ConsumerConfig {
	return &natsgo.ConsumerConfig{
		Durable:       EDSConsumerName,
		DeliverPolicy: natsgo.DeliverAllPolicy,
		AckPolicy:     natsgo.AckExplicitPolicy,
		AckWait:       AckWaitCanonical,
		MaxDeliver:    MaxDeliverCanonical,
		FilterSubject: "cbse.*.*.eds.scenarios",
		MaxAckPending: MaxAckPendingSM,
		DeliverGroup:  "scenario-manager-eds",
	}
}

// TranslatorReadyConsumerConfig returns the canonical alpha4 SM-owned
// Translator-ready consumer configuration: durable and queue group
// scenario-manager-translator-ready, exact filter cbse.*.*.trans.*.ready,
// explicit ACK, DeliverAll, AckWait 2m, MaxAckPending 1024, MaxDeliver -1.
func TranslatorReadyConsumerConfig() *natsgo.ConsumerConfig {
	return &natsgo.ConsumerConfig{
		Durable:       TranslatorReadyConsumerName,
		DeliverPolicy: natsgo.DeliverAllPolicy,
		AckPolicy:     natsgo.AckExplicitPolicy,
		AckWait:       AckWaitCanonical,
		MaxDeliver:    MaxDeliverCanonical,
		FilterSubject: "cbse.*.*.trans.*.ready",
		MaxAckPending: MaxAckPendingSM,
		DeliverGroup:  "scenario-manager-translator-ready",
	}
}

// SM consumer durable names.
const (
	// EDSConsumerName is the durable name of the SM-owned EDS batch consumer.
	EDSConsumerName = "scenario-manager-eds-consumer"
	// TranslatorReadyConsumerName is the durable name (and queue group) of the
	// SM-owned Translator-ready consumer.
	TranslatorReadyConsumerName = "scenario-manager-translator-ready"
)

// SMConsumers returns the canonical SM-owned consumer configurations in a
// fixed order, each tagged with the stream it belongs to.
func SMConsumers() []SMConsumer {
	return []SMConsumer{
		{Stream: EDSStreamName, Config: EDSConsumerConfig()},
		{Stream: TranslatorStreamName, Config: TranslatorReadyConsumerConfig()},
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

// ErrOwnershipCollision is returned when an existing consumer's identity does
// not match the expected owning experiment. The caller must not delete,
// update, or adopt such a consumer.
var ErrOwnershipCollision = errors.New("consumer ownership collision")
