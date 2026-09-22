// natsadapter.go implements the four concrete NATS adapters that bind the
// transport-neutral communication interfaces to a nats.go client: the
// translation-request publisher, the EDS availability responder, the EDS batch
// JetStream consumer, and the translator-ready JetStream consumer.
//
// The adapters own transport-level concerns only: exact subject construction,
// subject parsing and identity validation, strict JSON decode, the lifecycle
// gate (lifecycle.AdmitExperiment on the fetched experiment), and ACK/NAK
// mapping. All domain decisions belong to lifecycle, communication, registry,
// persistence, and the ready workflow. Stream and consumer reconciliation is
// delegated to ReconcileStreamsAndConsumers at startup; the adapters
// attach to the reconciled SM-owned durables for EDS batch and translator-ready
// and use a fresh JetStream publish for translation requests.
//
// The handler logic is split from the nats.go subscription so it can be
// unit-tested without a real NATS server: each handler takes the raw subject
// and payload bytes and returns an ACK/NAK decision (or an availability reply);
// the subscription wrapper applies the decision to the nats.go message.

package nats

import (
	"context"
	"encoding/json"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/lifecycle"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
	natsgo "github.com/nats-io/nats.go"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// translationRequestPayload is the four-field translation request the Scenario
// Manager publishes on cbse.<namespace>.<project>.trans.request. It matches the
// Translator's wire.Request decode (id, translation_attempt, recipe_info,
// confidence_metric).
type translationRequestPayload struct {
	ID                 int             `json:"id"`
	TranslationAttempt int             `json:"translation_attempt"`
	RecipeInfo         json.RawMessage `json:"recipe_info"`
	ConfidenceMetric   *float64        `json:"confidence_metric"`
}

// translatorReadyPayload is the two-field ready-message payload the Translator
// publishes on cbse.<namespace>.<project>.trans.<scenario-id>.ready. It
// matches the Translator's wire.ReadyPayload (translation_attempt,
// container_image).
type translatorReadyPayload struct {
	TranslationAttempt int    `json:"translation_attempt"`
	ContainerImage     string `json:"container_image"`
}

// deliveryDecision is the transport ACK/NAK decision a JetStream handler
// returns. decisionACK covers a successful domain outcome, a permanent poison
// (ACK-and-discard), and a terminal experiment (ACK-and-discard); decisionNAK
// covers a transient dependency failure or an unavailable (pre-InProgress)
// experiment.
type deliveryDecision int

const (
	decisionACK deliveryDecision = iota
	decisionNAK
)

// Adapters holds the shared dependencies for the four NATS adapters: the NATS
// connection and JetStream context, the controller-runtime client used to
// fetch the live experiment for the lifecycle gate, the EDS batch insert
// function, and the translator-ready semantic handler.
type Adapters struct {
	nc           *natsgo.Conn
	js           natsgo.JetStreamContext
	k8s          client.Client
	insertBatch  func(ctx context.Context, namespace, project string, records []persistence.ScenarioIntakeRecord) (int, error)
	readyHandler communication.TranslatorReadyHandler
}

// NewAdapters constructs the adapter set. nc and js must be a connected NATS
// connection and its JetStream context (reconciled by
// ReconcileStreamsAndConsumers at startup). k8s must have a scheme
// that knows the alpha4 SimulationExperiment. db backs the EDS batch insert.
// readyHandler is the semantic translator-ready workflow (e.g.
// ready.Handler.Handle).
func NewAdapters(nc *natsgo.Conn, js natsgo.JetStreamContext, k8s client.Client, db persistence.DB, readyHandler communication.TranslatorReadyHandler) *Adapters {
	return &Adapters{
		nc:  nc,
		js:  js,
		k8s: k8s,
		insertBatch: func(ctx context.Context, namespace, project string, records []persistence.ScenarioIntakeRecord) (int, error) {
			return persistence.InsertScenarioBatch(ctx, db, namespace, project, records)
		},
		readyHandler: readyHandler,
	}
}

// fetchExperiment loads the live alpha4 SimulationExperiment for the
// (namespace, name) pair. A NotFound result returns nil with no error so the
// caller can apply lifecycle.AdmitExperiment(nil) (terminal); a transient
// lookup error is returned so the caller can NAK or reply error.
func (a *Adapters) fetchExperiment(ctx context.Context, namespace, project string) (*experimentalpha4.SimulationExperiment, error) {
	exp := &experimentalpha4.SimulationExperiment{}
	if err := a.k8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: project}, exp); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return exp, nil
}

// admit fetches the live experiment and applies the lifecycle gate. It returns
// AdmitTerminal for a gone experiment (NotFound maps to AdmitExperiment(nil)),
// the live decision for a fetched experiment, and a non-nil error only for a
// transient lookup failure.
func (a *Adapters) admit(ctx context.Context, namespace, project string) (lifecycle.AdmitDecision, error) {
	exp, err := a.fetchExperiment(ctx, namespace, project)
	if err != nil {
		return 0, err
	}
	return lifecycle.AdmitExperiment(exp), nil
}

// edsConsumerOptions returns the JetStream subscribe options that attach to the
// reconciled SM-owned EDS durable consumer: the canonical durable name, manual
// ACK, AckWait, and MaxAckPending from EDSConsumerConfig.
func edsConsumerOptions() []natsgo.SubOpt {
	cfg := EDSConsumerConfig()
	return []natsgo.SubOpt{
		natsgo.Durable(cfg.Durable),
		natsgo.ManualAck(),
		natsgo.AckWait(cfg.AckWait),
		natsgo.MaxAckPending(cfg.MaxAckPending),
	}
}

// translatorReadyConsumerOptions returns the JetStream subscribe options that
// attach to the reconciled SM-owned translator-ready durable consumer.
func translatorReadyConsumerOptions() []natsgo.SubOpt {
	cfg := TranslatorReadyConsumerConfig()
	return []natsgo.SubOpt{
		natsgo.Durable(cfg.Durable),
		natsgo.ManualAck(),
		natsgo.AckWait(cfg.AckWait),
		natsgo.MaxAckPending(cfg.MaxAckPending),
	}
}

// queueGroup returns the canonical SM queue group for an SM-owned consumer.
func queueGroup(cfg *natsgo.ConsumerConfig) string {
	if cfg.DeliverGroup == "" {
		return cfg.Durable
	}
	return cfg.DeliverGroup
}
