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
//
// The finalization protocol rides on the same delivery: a final batch inserts
// its scenarios, if any, then durably marks the project finalized; a non-final
// batch arriving after finalization is rejected without insertion with the
// pinned reason. Every admitted batch carries a ScenarioBatchAck back on the
// delivery's reply subject when one is present (the reference EDS publishes
// with a reply to consume the insert report; publishers without a reply, such
// as the protocol-faithful mock, see no behavior change).
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
		decision, ack, err := c.HandleEDSBatch(ctx, msg.Subject, msg.Data)
		if err != nil {
			log.Printf("alpha4 eds batch: subject=%q: %v", msg.Subject, err)
		}
		respondBatchAck(msg, ack)
		applyDecision(msg, decision)
	}, edsConsumerOptions()...); err != nil {
		return fmt.Errorf("subscribe to EDS batch subject %q: %w", subject.EDSBatchStreamSubject, err)
	}
	return nil
}

// HandleEDSBatch applies the EDS batch workflow to one delivery and returns
// the ACK/NAK decision plus the batch ack to deliver (a zero-value ack -
// empty status - means no ack; the poison and gate outcomes keep the landed
// silent-discard behavior). It is pure with respect to the transport: it takes
// the raw subject and payload and returns the decision and ack, so it can be
// unit-tested without a NATS server.
func (c *EDSBatchConsumer) HandleEDSBatch(ctx context.Context, subjectStr string, data []byte) (deliveryDecision, communication.ScenarioBatchAck, error) {
	parsed, err := subject.Parse(subjectStr)
	if err != nil {
		// Invalid subject shape: permanent poison.
		return decisionACK, communication.ScenarioBatchAck{}, fmt.Errorf("invalid batch subject: %w", err)
	}
	if parsed.Event != subject.EventBatch {
		return decisionACK, communication.ScenarioBatchAck{}, fmt.Errorf("subject %q is not an EDS batch subject", subjectStr)
	}
	identity := subject.Identity{Namespace: parsed.Namespace, Project: parsed.Project}

	if len(data) == 0 {
		return decisionACK, communication.ScenarioBatchAck{}, fmt.Errorf("empty batch payload")
	}
	var batch communication.ScenarioBatch
	if err := json.Unmarshal(data, &batch); err != nil {
		// Malformed JSON: permanent poison.
		return decisionACK, communication.ScenarioBatchAck{}, fmt.Errorf("decode batch payload: %w", err)
	}
	batch.ProjectNamespace = identity.Namespace.String()
	batch.ProjectName = identity.Project.String()

	// Subject/payload mismatch and out-of-range reps are permanent poison.
	if err := communication.ValidateBatchIdentity(batch, identity); err != nil {
		return decisionACK, communication.ScenarioBatchAck{}, err
	}
	if err := communication.ValidateBatchReps(batch.Scenarios); err != nil {
		return decisionACK, communication.ScenarioBatchAck{}, err
	}

	// Lifecycle gate: a terminal experiment is ACK-and-discard; an unavailable
	// experiment is NAK; an admitted experiment takes the finalization-aware
	// intake below.
	decision, err := c.a.admit(ctx, identity.Namespace.String(), identity.Project.String())
	if err != nil {
		return decisionNAK, communication.ScenarioBatchAck{}, fmt.Errorf("fetch experiment for gate: %w", err)
	}
	if decision.IsTerminal() {
		return decisionACK, communication.ScenarioBatchAck{}, nil
	}
	if !decision.IsAdmitted() {
		return decisionNAK, communication.ScenarioBatchAck{}, nil
	}

	namespace, project := identity.Namespace.String(), identity.Project.String()
	records := make([]persistence.ScenarioIntakeRecord, 0, len(batch.Scenarios))
	for _, s := range batch.Scenarios {
		records = append(records, persistence.ScenarioIntakeRecord{
			Priority:         s.Priority,
			NumberOfReps:     s.NumberOfReps,
			RecipeInfo:       s.RecipeInfo,
			ConfidenceMetric: s.ConfidenceMetric,
		})
	}

	// Final batch: insert the scenarios, if any, then durably finalize the
	// project. InsertScenarioBatch is a no-op on an empty batch (the
	// finalization-only shape), and MarkFinalized registers a not-yet-
	// registered project; a redelivered final batch is an idempotent no-op.
	if batch.Final {
		inserted := 0
		if len(records) > 0 {
			var err error
			inserted, err = c.a.insertBatch(ctx, namespace, project, records)
			if err != nil {
				// Transient database failure: NAK for redelivery.
				return decisionNAK, communication.ScenarioBatchAck{}, fmt.Errorf("insert final scenario batch: %w", err)
			}
		}
		if err := c.a.markFinalized(ctx, namespace, project); err != nil {
			return decisionNAK, communication.ScenarioBatchAck{}, fmt.Errorf("mark project finalized: %w", err)
		}
		return decisionACK, insertedBatchAck(batch, inserted), nil
	}

	// Non-final batch after finalization: no insertion; the rejection is the
	// pinned error ack. It is permanent (finalization is irrevocable), so the
	// delivery is ACKed once the EDS has its answer.
	finalized, err := c.a.isFinalized(ctx, namespace, project)
	if err != nil {
		return decisionNAK, communication.ScenarioBatchAck{}, fmt.Errorf("read finalization marker: %w", err)
	}
	if finalized {
		return decisionACK, communication.ScenarioBatchAck{
			Status:   communication.BatchAckStatusError,
			BatchID:  batch.BatchID,
			Received: len(batch.Scenarios),
			Inserted: 0,
			Failed:   0,
			Reason:   communication.ReasonExperimentFinalized,
		}, nil
	}

	// Not finalized: the landed insert path.
	inserted, err := c.a.insertBatch(ctx, namespace, project, records)
	if err != nil {
		// Transient database failure: NAK for redelivery.
		return decisionNAK, communication.ScenarioBatchAck{}, fmt.Errorf("insert scenario batch: %w", err)
	}
	return decisionACK, insertedBatchAck(batch, inserted), nil
}

// insertedBatchAck builds the normal inserted ack for an admitted batch:
// received is the decoded scenario count, inserted the persisted rows.
func insertedBatchAck(batch communication.ScenarioBatch, inserted int) communication.ScenarioBatchAck {
	return communication.ScenarioBatchAck{
		Status:   communication.BatchAckStatusInserted,
		BatchID:  batch.BatchID,
		Received: len(batch.Scenarios),
		Inserted: inserted,
	}
}

// respondBatchAck delivers the batch ack on the delivery's reply subject when
// one is present. A delivery without a reply subject (the protocol-faithful
// mock; every publisher that landed before the finalization protocol) is
// ACK/NAK-only, the landed behavior, so the reply is purely additive.
func respondBatchAck(msg *natsgo.Msg, ack communication.ScenarioBatchAck) {
	if msg == nil || msg.Reply == "" || ack.Status == "" {
		return
	}
	data, err := json.Marshal(ack)
	if err != nil {
		log.Printf("alpha4 eds batch: marshal ack: %v", err)
		return
	}
	if err := msg.Respond(data); err != nil {
		log.Printf("alpha4 eds batch: publish ack: %v", err)
	}
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
