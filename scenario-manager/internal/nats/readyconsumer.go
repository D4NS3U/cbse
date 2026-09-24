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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/subject"
	natsgo "github.com/nats-io/nats.go"
)

// TranslatorReadyConsumer implements communication.TranslatorReadyConsumer. It
// consumes translator-ready messages from the JetStream durable consumer
// scenario-manager-translator-ready (queue group
// scenario-manager-translator-ready, filter cbse.*.*.trans.*.ready). It parses
// and validates the subject and payload, applies the lifecycle gate, and
// delegates to the ready workflow's TranslatorReadyHandler. An invalid subject
// shape, invalid identity, non-positive scenario id or translation attempt, or
// a terminal experiment is permanent poison (ACK, no mutation); an unavailable
// experiment or a transient dependency failure is NAK; the handler's
// TranslatorReadyHandled maps to ACK and TranslatorReadyRetry maps to NAK.
type TranslatorReadyConsumer struct {
	a *Adapters
}

// NewTranslatorReadyConsumer returns a consumer bound to the adapter set.
func NewTranslatorReadyConsumer(a *Adapters) *TranslatorReadyConsumer {
	return &TranslatorReadyConsumer{a: a}
}

// StartTranslatorReadyConsumer attaches to the reconciled SM-owned
// translator-ready durable consumer and routes each delivery to the ready
// workflow. It returns an error if the subscription fails.
func (c *TranslatorReadyConsumer) StartTranslatorReadyConsumer(ctx context.Context, handler communication.TranslatorReadyHandler) error {
	if c.a == nil || c.a.js == nil {
		return fmt.Errorf("JetStream context is not initialized")
	}
	if handler == nil {
		return fmt.Errorf("translator ready handler must not be nil")
	}
	cfg := TranslatorReadyConsumerConfig()
	if _, err := c.a.js.QueueSubscribe(subject.TranslatorReadyStreamSubject, cfg.DeliverGroup, func(msg *natsgo.Msg) {
		decision, err := c.handleTranslatorReady(ctx, msg.Subject, msg.Data, handler)
		if err != nil {
			log.Printf("alpha4 translator ready: subject=%q: %v", msg.Subject, err)
		}
		applyDecision(msg, decision)
	}, translatorReadyConsumerOptions()...); err != nil {
		return fmt.Errorf("subscribe to translator ready subject %q: %w", subject.TranslatorReadyStreamSubject, err)
	}
	return nil
}

// handleTranslatorReady applies the translator-ready workflow to one delivery
// and returns the ACK/NAK decision. It is pure with respect to the transport so
// it can be unit-tested without a NATS server.
func (c *TranslatorReadyConsumer) handleTranslatorReady(ctx context.Context, subjectStr string, data []byte, handler communication.TranslatorReadyHandler) (deliveryDecision, error) {
	if handler == nil {
		return decisionACK, fmt.Errorf("ready handler is nil")
	}
	parsed, err := subject.Parse(subjectStr)
	if err != nil {
		return decisionACK, fmt.Errorf("invalid ready subject: %w", err)
	}
	if parsed.Event != subject.EventReady {
		return decisionACK, fmt.Errorf("subject %q is not a translator ready subject", subjectStr)
	}
	scenarioID := parsed.ReadyScenarioID
	if scenarioID == "" {
		return decisionACK, fmt.Errorf("empty translator scenario-id token: %q", subjectStr)
	}
	id, err := parsePositiveInt(scenarioID)
	if err != nil || id <= 0 {
		return decisionACK, fmt.Errorf("ready scenario id %q must be a positive integer", scenarioID)
	}
	identity := subject.Identity{Namespace: parsed.Namespace, Project: parsed.Project}

	var payload translatorReadyPayload
	if err := decodeStrict(data, &payload); err != nil {
		return decisionACK, fmt.Errorf("decode ready payload: %w", err)
	}
	if payload.TranslationAttempt <= 0 {
		return decisionACK, fmt.Errorf("ready translation attempt must be positive")
	}

	// Lifecycle gate: terminal -> ACK-and-discard; unavailable -> NAK; admitted
	// -> delegate to the ready workflow.
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

	ready := communication.TranslatorReadyMessage{
		ProjectNamespace:   identity.Namespace.String(),
		ProjectName:        identity.Project.String(),
		ScenarioID:         id,
		TranslationAttempt: payload.TranslationAttempt,
		ContainerImage:     payload.ContainerImage,
	}
	result := handler(ctx, ready)
	switch result.Status {
	case communication.TranslatorReadyHandled:
		return decisionACK, nil
	case communication.TranslatorReadyRetry:
		return decisionNAK, nil
	default:
		// An unknown handler status is conservatively ACKed as poison so a
		// buggy handler cannot redeliver forever.
		return decisionACK, fmt.Errorf("unknown ready handler status %q", result.Status)
	}
}

// decodeStrict decodes exactly one JSON object and rejects unknown fields and
// trailing tokens so the ready payload shape stays tightly controlled.
func decodeStrict(data []byte, target interface{}) error {
	if len(data) == 0 {
		return errors.New("empty payload")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("unexpected trailing JSON content: %w", err)
	}
	return errors.New("unexpected trailing JSON content")
}

// parsePositiveInt parses a canonical positive decimal integer with no sign.
func parsePositiveInt(s string) (int, error) {
	if s == "" || s[0] == '0' {
		return 0, fmt.Errorf("not a canonical positive decimal: %q", s)
	}
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a canonical positive decimal: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
