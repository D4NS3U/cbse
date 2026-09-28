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
	"fmt"
	"log"

	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/subject"
	natsgo "github.com/nats-io/nats.go"
)

// PPSEvaluationConsumer implements communication.PPSEvaluationConsumer. It
// consumes PPS evaluation verdicts from the JetStream durable consumer
// scenario-manager-pps-evaluation (queue group
// scenario-manager-pps-evaluation, filter cbse.*.*.pps.*.evaluation). It
// parses and validates the subject, decodes and validates the payload with
// the strict wire type, checks the subject/payload identity, applies the
// lifecycle gate, and delegates to the verdict workflow's
// PPSEvaluationHandler. An invalid subject shape, invalid scenario id,
// malformed payload, subject/payload identity mismatch, or a terminal
// experiment is permanent poison (ACK, no mutation); an unavailable experiment
// or a transient dependency failure is NAK; the handler's
// PPSEvaluationHandled maps to ACK and PPSEvaluationRetry maps to NAK.
type PPSEvaluationConsumer struct {
	a *Adapters
}

// NewPPSEvaluationConsumer returns a consumer bound to the adapter set.
func NewPPSEvaluationConsumer(a *Adapters) *PPSEvaluationConsumer {
	return &PPSEvaluationConsumer{a: a}
}

// StartPPSEvaluationConsumer attaches to the reconciled SM-owned
// PPS-evaluation durable consumer and routes each delivery to the verdict
// workflow. It returns an error if the subscription fails.
func (c *PPSEvaluationConsumer) StartPPSEvaluationConsumer(ctx context.Context, handler communication.PPSEvaluationHandler) error {
	if c.a == nil || c.a.js == nil {
		return fmt.Errorf("JetStream context is not initialized")
	}
	if handler == nil {
		return fmt.Errorf("pps evaluation handler must not be nil")
	}
	cfg := PPSEvaluationConsumerConfig()
	if _, err := c.a.js.QueueSubscribe(subject.PPSEvaluationStreamSubject, cfg.DeliverGroup, func(msg *natsgo.Msg) {
		decision, err := c.handlePPSEvaluation(ctx, msg.Subject, msg.Data, handler)
		if err != nil {
			log.Printf("alpha4 pps evaluation: subject=%q: %v", msg.Subject, err)
		}
		applyDecision(msg, decision)
	}, ppsEvaluationConsumerOptions()...); err != nil {
		return fmt.Errorf("subscribe to pps evaluation subject %q: %w", subject.PPSEvaluationStreamSubject, err)
	}
	return nil
}

// handlePPSEvaluation applies the PPS-evaluation workflow to one delivery and
// returns the ACK/NAK decision. It is pure with respect to the transport so
// it can be unit-tested without a NATS server.
func (c *PPSEvaluationConsumer) handlePPSEvaluation(ctx context.Context, subjectStr string, data []byte, handler communication.PPSEvaluationHandler) (deliveryDecision, error) {
	if handler == nil {
		return decisionACK, fmt.Errorf("evaluation handler is nil")
	}
	parsed, err := subject.Parse(subjectStr)
	if err != nil {
		return decisionACK, fmt.Errorf("invalid evaluation subject: %w", err)
	}
	if parsed.Event != subject.EventPPSEvaluation {
		return decisionACK, fmt.Errorf("subject %q is not a pps evaluation subject", subjectStr)
	}
	scenarioID := parsed.EvaluationScenarioID
	if scenarioID == "" {
		return decisionACK, fmt.Errorf("empty pps evaluation scenario-id token: %q", subjectStr)
	}
	id, err := parsePositiveInt(scenarioID)
	if err != nil || id <= 0 {
		return decisionACK, fmt.Errorf("evaluation scenario id %q must be a positive integer", scenarioID)
	}
	identity := subject.Identity{Namespace: parsed.Namespace, Project: parsed.Project}

	// The verdict payload carries its own identity; a subject/payload
	// mismatch is permanent poison. The strict wire type applies the full
	// field-for-field validation on decode.
	var payload communication.PPSEvaluationVerdict
	if err := decodeStrict(data, &payload); err != nil {
		return decisionACK, fmt.Errorf("decode evaluation payload: %w", err)
	}
	if payload.Namespace != identity.Namespace.String() || payload.Project != identity.Project.String() {
		return decisionACK, fmt.Errorf("evaluation payload identity %q/%q != subject identity %q/%q: poison", payload.Namespace, payload.Project, identity.Namespace.String(), identity.Project.String())
	}

	// Lifecycle gate: terminal -> ACK-and-discard; unavailable -> NAK; admitted
	// -> delegate to the verdict workflow.
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

	msg := communication.PPSEvaluationMessage{
		ProjectNamespace:  identity.Namespace.String(),
		ProjectName:       identity.Project.String(),
		ExperimentUID:     payload.ExperimentUID,
		ScenarioID:        id,
		RunnerRound:       payload.RunnerRound,
		Metric:            payload.Metric,
		Verdict:           payload.Verdict,
		SampleMean:        payload.SampleMean,
		HalfWidth:         payload.HalfWidth,
		Replications:      payload.Replications,
		ConfidenceMetric:  payload.ConfidenceMetric,
		AdditionalRunners: payload.AdditionalRunners,
		MaxReplications:   payload.MaxReplications,
	}
	result := handler(ctx, msg)
	switch result.Status {
	case communication.PPSEvaluationHandled:
		return decisionACK, nil
	case communication.PPSEvaluationRetry:
		return decisionNAK, nil
	default:
		// An unknown handler status is conservatively ACKed as poison so a
		// buggy handler cannot redeliver forever.
		return decisionACK, fmt.Errorf("unknown evaluation handler status %q", result.Status)
	}
}

// ppsEvaluationConsumerOptions returns the JetStream subscribe options that
// attach to the reconciled SM-owned PPS-evaluation durable consumer.
func ppsEvaluationConsumerOptions() []natsgo.SubOpt {
	cfg := PPSEvaluationConsumerConfig()
	return []natsgo.SubOpt{
		natsgo.Durable(cfg.Durable),
		natsgo.ManualAck(),
		natsgo.AckWait(cfg.AckWait),
		natsgo.MaxAckPending(cfg.MaxAckPending),
	}
}
