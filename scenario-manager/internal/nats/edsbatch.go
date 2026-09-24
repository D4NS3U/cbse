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

package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
	"github.com/D4NS3U/cbse/scenario-manager/internal/subject"
	natsgo "github.com/nats-io/nats.go"
)

// EDSBatchConsumer consumes EDS scenario batches from the JetStream durable
// consumer scenario-manager-eds-consumer (queue group scenario-manager-eds,
// filter cbse.*.*.eds.scenarios). It parses and validates the subject and
// payload, applies the lifecycle gate, and inserts the batch via
// persistence.InsertScenarioBatch. Invalid subject shape, invalid identity
// tokens, subject/payload mismatch, or an out-of-range number_of_reps is
// permanent poison (ACK, no insert); a terminal experiment is ACK-and-discard;
// an unavailable experiment or a transient dependency failure is NAK.
type EDSBatchConsumer struct {
	a *Adapters
}

// NewEDSBatchConsumer returns a consumer bound to the adapter set.
func NewEDSBatchConsumer(a *Adapters) *EDSBatchConsumer {
	return &EDSBatchConsumer{a: a}
}

// Start attaches to the reconciled SM-owned EDS durable consumer and handles
// each batch. It returns an error if the subscription fails. The durable is
// not explicitly unsubscribed on shutdown (the connection close cleans up the
// local subscription without deleting the shared durable, mirroring the
// existing EDS adapter).
func (c *EDSBatchConsumer) Start(ctx context.Context) error {
	if c.a == nil || c.a.js == nil {
		return fmt.Errorf("JetStream context is not initialized")
	}
	cfg := EDSConsumerConfig()
	if _, err := c.a.js.QueueSubscribe(subject.EDSBatchStreamSubject, cfg.DeliverGroup, func(msg *natsgo.Msg) {
		decision, err := c.HandleEDSBatch(ctx, msg.Subject, msg.Data)
		if err != nil {
			log.Printf("alpha4 eds batch: subject=%q: %v", msg.Subject, err)
		}
		applyDecision(msg, decision)
	}, edsConsumerOptions()...); err != nil {
		return fmt.Errorf("subscribe to EDS batch subject %q: %w", subject.EDSBatchStreamSubject, err)
	}
	return nil
}

// HandleEDSBatch applies the EDS batch workflow to one delivery and returns the
// ACK/NAK decision. It is pure with respect to the transport: it takes the raw
// subject and payload and returns a decision, so it can be unit-tested without
// a NATS server.
func (c *EDSBatchConsumer) HandleEDSBatch(ctx context.Context, subjectStr string, data []byte) (deliveryDecision, error) {
	parsed, err := subject.Parse(subjectStr)
	if err != nil {
		// Invalid subject shape: permanent poison.
		return decisionACK, fmt.Errorf("invalid batch subject: %w", err)
	}
	if parsed.Event != subject.EventBatch {
		return decisionACK, fmt.Errorf("subject %q is not an EDS batch subject", subjectStr)
	}
	identity := subject.Identity{Namespace: parsed.Namespace, Project: parsed.Project}

	if len(data) == 0 {
		return decisionACK, fmt.Errorf("empty batch payload")
	}
	var batch communication.ScenarioBatch
	if err := json.Unmarshal(data, &batch); err != nil {
		// Malformed JSON: permanent poison.
		return decisionACK, fmt.Errorf("decode batch payload: %w", err)
	}
	batch.ProjectNamespace = identity.Namespace.String()
	batch.ProjectName = identity.Project.String()

	// Subject/payload mismatch and out-of-range reps are permanent poison.
	if err := communication.ValidateBatchIdentity(batch, identity); err != nil {
		return decisionACK, err
	}
	if err := communication.ValidateBatchReps(batch.Scenarios); err != nil {
		return decisionACK, err
	}

	// Lifecycle gate: a terminal experiment is ACK-and-discard; an unavailable
	// experiment is NAK; an admitted experiment inserts the batch.
	decision, err := c.a.admit(ctx, identity.Namespace.String(), identity.Project.String())
	if err != nil {
		return decisionNAK, fmt.Errorf("fetch experiment for gate: %w", err)
	}
	if decision.IsTerminal() {
		return decisionACK, nil
	}
	if !decision.IsAdmitted() {
		return decisionNAK, nil
	}

	records := make([]persistence.ScenarioIntakeRecord, 0, len(batch.Scenarios))
	for _, s := range batch.Scenarios {
		records = append(records, persistence.ScenarioIntakeRecord{
			Priority:         s.Priority,
			NumberOfReps:     s.NumberOfReps,
			RecipeInfo:       s.RecipeInfo,
			ConfidenceMetric: s.ConfidenceMetric,
		})
	}
	if _, err := c.a.insertBatch(ctx, identity.Namespace.String(), identity.Project.String(), records); err != nil {
		// Transient database failure: NAK for redelivery.
		return decisionNAK, fmt.Errorf("insert scenario batch: %w", err)
	}
	return decisionACK, nil
}

// applyDecision ACKs or NAKs a JetStream delivery.
func applyDecision(msg *natsgo.Msg, d deliveryDecision) {
	if msg == nil {
		return
	}
	switch d {
	case decisionACK:
		if err := msg.Ack(); err != nil {
			log.Printf("alpha4 JetStream ack failed: %v", err)
		}
	case decisionNAK:
		if err := msg.Nak(); err != nil {
			log.Printf("alpha4 JetStream nak failed: %v", err)
		}
	}
}
