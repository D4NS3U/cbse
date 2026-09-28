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

// Package messaging owns the per-experiment JetStream request consumer
// attachment, the evaluation verdict publication, the AckExplicit processing
// loop, and the in-progress acknowledgement contract for the reference
// PostProcessingService.
//
// The PPS consumes one request at a time (MaxAckPending 1). Unlike the
// Translator — which creates or attaches its own durable — the per-experiment
// PPS durable pps-<12-char-UID-prefix> is created and deleted by the Scenario
// Manager with the experiment. The PPS binds to it and never creates,
// updates, deletes, or adopts it: a missing consumer is reported as not
// ready and retried until the Scenario Manager ensures it; an existing
// consumer with mismatched identity or settings is an ownership collision and
// fails startup without modification.
//
// The PPS publishes the verdict with JetStream confirmation before
// acknowledging the request. During the Result DB query it sends 30-second
// in-progress acknowledgements so the server does not redeliver under the
// two-minute AckWait. NATS transport, Result DB, or verdict-publication
// failures NAK the request for redelivery; they never manufacture a verdict.
// Permanent poison (a request that fails strict decoding or identity/domain
// validation) is acknowledged and logged.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	nats "github.com/nats-io/nats.go"
)

// Canonical alpha4 per-experiment PPS consumer settings. These mirror the
// Scenario Manager's messaging package; the PPS module is standalone and does
// not import it, so the canonical values are restated here and validated by
// the smoke suite.
const (
	// AckWait is the fixed consumer ACK wait: two minutes.
	AckWait = 2 * time.Minute
	// MaxAckPending is one in-flight request per PPS.
	MaxAckPending = 1
	// MaxDeliver is unlimited redelivery.
	MaxDeliver = -1

	metaManagedBy      = "experiment.cbse.terministic.de/managed-by"
	metaExperimentUID  = "experiment.cbse.terministic.de/experiment-uid"
	metaNamespace      = "experiment.cbse.terministic.de/namespace"
	metaProject        = "experiment.cbse.terministic.de/project"
	metaManagedByValue = "pps"

	// InProgressInterval is the cadence at which the PPS sends in-progress
	// acknowledgements during the Result DB query.
	InProgressInterval = 30 * time.Second

	// BindRetryInterval is the cadence at which the PPS retries attaching to
	// the Scenario Manager's per-experiment consumer while it is not created
	// yet.
	BindRetryInterval = 10 * time.Second
)

// ErrOwnershipCollision is returned when an existing consumer's identity does
// not match the expected owning experiment or its canonical settings. The
// caller must not delete, update, or adopt such a consumer.
var ErrOwnershipCollision = errors.New("consumer ownership collision")

// ErrConsumerNotReady is returned when the per-experiment PPS consumer does
// not exist (yet) on the stream. The consumer is Scenario Manager-owned; the
// PPS does not create it and the processing loop retries the attachment at
// BindRetryInterval until the Scenario Manager ensures it.
var ErrConsumerNotReady = errors.New("consumer not ready")

// UIDPrefix returns the first 12 lowercase hexadecimal characters of uid
// after removing hyphens. It matches the canonical per-experiment prefix
// shared by the PPS durable consumer name and the operator's PPS_CONSUMER
// environment value.
func UIDPrefix(uid string) string {
	stripped := strings.ToLower(strings.ReplaceAll(uid, "-", ""))
	if len(stripped) > 12 {
		stripped = stripped[:12]
	}
	return stripped
}

// ConsumerName returns the canonical per-experiment PPS durable consumer
// name: pps-<12-char-UID-prefix>.
func ConsumerName(uid string) string {
	return "pps-" + UIDPrefix(uid)
}

// ConsumerConfig returns the canonical per-experiment PPS durable consumer
// configuration for the given experiment UID, namespace, project, and exact
// request subject. The durable name is pps-<UIDPrefix>; the filter is the
// exact request subject; settings are explicit ACK, DeliverAll, AckWait 2m,
// MaxAckPending 1, MaxDeliver -1; and the four ownership metadata entries
// are set. The PPS builds this only to bind to the consumer the Scenario
// Manager created; it never publishes it.
func ConsumerConfig(uid, namespace, project, requestSubject string) *nats.ConsumerConfig {
	return &nats.ConsumerConfig{
		Durable:       ConsumerName(uid),
		DeliverPolicy: nats.DeliverAllPolicy,
		AckPolicy:     nats.AckExplicitPolicy,
		AckWait:       AckWait,
		MaxDeliver:    MaxDeliver,
		FilterSubject: requestSubject,
		MaxAckPending: MaxAckPending,
		Metadata:      ppsMetadata(uid, namespace, project),
	}
}

func ppsMetadata(uid, namespace, project string) map[string]string {
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
// MaxDeliver, and all four ownership metadata entries must match exactly.
// Any mismatch is an identity collision; the caller must not modify the
// consumer.
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

// Publisher publishes a verdict with JetStream confirmation.
type Publisher interface {
	// Publish blocks until the server confirms publication (PubAck) or
	// returns an error. A returned error is a retryable
	// transport/publication failure.
	Publish(subject string, data []byte) error
}

// Manager attaches to the per-experiment request consumer that the Scenario
// Manager created. The PPS never creates, updates, or deletes the consumer.
type Manager interface {
	// EnsureConsumer verifies the existing consumer's identity and settings
	// and binds to it. A missing consumer returns ErrConsumerNotReady; a
	// mismatched consumer returns ErrOwnershipCollision.
	EnsureConsumer() error
}
