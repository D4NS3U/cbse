// Package messaging owns the per-experiment JetStream request consumer, the
// ready-message publication, and the in-progress acknowledgement contract for
// the reference Translator.
//
// The framework consumes one request at a time (MaxAckPending 1). It creates or
// attaches a UID-specific durable pull consumer with the exact alpha4
// configuration: durable translator-<12-char-UID-prefix>, filter
// cbse.<namespace>.<project>.trans.request, explicit ACK, DeliverAll, AckWait
// two minutes, MaxAckPending one, MaxDeliver unlimited, and the four ownership
// metadata entries. An existing durable with a matching name but mismatched
// settings is an identity collision: the framework rejects it without
// deleting, updating, or adopting it.
//
// Translator publishes the ready message with JetStream confirmation before
// acknowledging the request. During long operations it sends 30-second
// in-progress acknowledgements so the server does not redeliver under the
// two-minute AckWait. NATS transport or ready-publication failures leave the
// request unacknowledged and retryable; they never manufacture an empty result.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/D4NS3U/cbse/component-templates/translator/internal/imageref"
	nats "github.com/nats-io/nats.go"
)

// Canonical alpha4 per-experiment Translator consumer settings. These mirror
// the Scenario Manager's messaging package; the Translator module is
// standalone and does not import it, so the canonical values are restated here
// and validated by S05-A2.
const (
	// AckWait is the fixed consumer ACK wait: two minutes.
	AckWait = 2 * time.Minute
	// MaxAckPending is one in-flight request per Translator.
	MaxAckPending = 1
	// MaxDeliver is unlimited redelivery.
	MaxDeliver = -1

	metaManagedBy      = "experiment.cbse.terministic.de/managed-by"
	metaExperimentUID  = "experiment.cbse.terministic.de/experiment-uid"
	metaNamespace      = "experiment.cbse.terministic.de/namespace"
	metaProject        = "experiment.cbse.terministic.de/project"
	metaManagedByValue = "translator"

	// InProgressInterval is the cadence at which the framework sends
	// in-progress acknowledgements during long operations.
	InProgressInterval = 30 * time.Second
)

// ErrOwnershipCollision is returned when an existing consumer's identity does
// not match the expected owning experiment. The caller must not delete, update,
// or adopt such a consumer.
var ErrOwnershipCollision = errors.New("consumer ownership collision")

// ConsumerConfig returns the canonical per-experiment Translator durable
// consumer configuration for the given experiment UID, namespace, project, and
// exact request subject. The durable name is translator-<UIDPrefix>; the filter
// is the exact request subject; settings are explicit ACK, DeliverAll, AckWait
// 2m, MaxAckPending 1, MaxDeliver -1; and the four ownership metadata entries
// are set.
func ConsumerConfig(uid, namespace, project, requestSubject string) *nats.ConsumerConfig {
	return &nats.ConsumerConfig{
		Durable:       "translator-" + imageref.UIDPrefix(uid),
		DeliverPolicy: nats.DeliverAllPolicy,
		AckPolicy:     nats.AckExplicitPolicy,
		AckWait:       AckWait,
		MaxDeliver:    MaxDeliver,
		FilterSubject: requestSubject,
		MaxAckPending: MaxAckPending,
		Metadata:      translatorMetadata(uid, namespace, project),
	}
}

func translatorMetadata(uid, namespace, project string) map[string]string {
	return map[string]string{
		metaManagedBy:     metaManagedByValue,
		metaExperimentUID: uid,
		metaNamespace:     namespace,
		metaProject:       project,
	}
}

// CompareConsumer reports whether an existing consumer's identity and
// immutable settings match the expected configuration. Durable name, filter
// subject, acknowledgement policy, delivery policy, AckWait, MaxAckPending,
// MaxDeliver, and all four ownership metadata entries must match exactly. Any
// mismatch is an identity collision; the caller must not modify the consumer.
func CompareConsumer(got *nats.ConsumerInfo, want *nats.ConsumerConfig) error {
	if got == nil {
		return errors.New("nil consumer info")
	}
	g := got.Config
	if g.Durable != want.Durable {
		return fmt.Errorf("%w: durable %q != %q", ErrOwnershipCollision, g.Durable, want.Durable)
	}
	if g.FilterSubject != want.FilterSubject {
		return fmt.Errorf("%w: filter %q != %q", ErrOwnershipCollision, g.FilterSubject, want.FilterSubject)
	}
	if g.DeliverPolicy != want.DeliverPolicy {
		return fmt.Errorf("%w: deliver policy %v != %v", ErrOwnershipCollision, g.DeliverPolicy, want.DeliverPolicy)
	}
	if g.AckPolicy != want.AckPolicy {
		return fmt.Errorf("%w: ack policy %v != %v", ErrOwnershipCollision, g.AckPolicy, want.AckPolicy)
	}
	if g.AckWait != want.AckWait {
		return fmt.Errorf("%w: ack wait %v != %v", ErrOwnershipCollision, g.AckWait, want.AckWait)
	}
	if g.MaxAckPending != want.MaxAckPending {
		return fmt.Errorf("%w: max ack pending %d != %d", ErrOwnershipCollision, g.MaxAckPending, want.MaxAckPending)
	}
	if g.MaxDeliver != want.MaxDeliver {
		return fmt.Errorf("%w: max deliver %d != %d", ErrOwnershipCollision, g.MaxDeliver, want.MaxDeliver)
	}
	if err := compareMetadata(g.Metadata, want.Metadata); err != nil {
		return fmt.Errorf("%w: %v", ErrOwnershipCollision, err)
	}
	return nil
}

// compareMetadata reports whether got contains every entry in want with the
// same value.
func compareMetadata(got, want map[string]string) error {
	if want == nil {
		want = map[string]string{}
	}
	for k, v := range want {
		if got[k] != v {
			return fmt.Errorf("metadata %q=%q != %q", k, got[k], v)
		}
	}
	return nil
}

// Consumer fetches one request at a time (serial processing).
type Consumer interface {
	// Fetch blocks until one request is available or ctx is cancelled.
	Fetch(ctx context.Context) (Message, error)
}

// Message is a single JetStream request delivery.
type Message interface {
	Data() []byte
	Subject() string
	Ack() error
	Nak() error
	InProgress() error
}

// Publisher publishes a ready message with JetStream confirmation.
type Publisher interface {
	// Publish blocks until the server confirms publication (PubAck) or returns
	// an error. A returned error is a retryable transport/publication failure.
	Publish(subject string, data []byte) error
}

// Manager creates or attaches the per-experiment request consumer.
type Manager interface {
	EnsureConsumer() error
}
