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

//go:build integration

package nats

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/subject"
	natsgo "github.com/nats-io/nats.go"
)

// TestTranslationRequestPublisherRoundTrip proves the publisher publishes on
// the exact namespace-aware subject and returns only after a JetStream PubAck,
// so the caller's MarkScenarioTranslationRequestPublished reflects a durably
// accepted message.
func TestTranslationRequestPublisherRoundTrip(t *testing.T) {
	_, js := connectITNATS(t)
	dropStreams(t, js)
	t.Cleanup(func() { dropStreams(t, js) })
	if err := ReconcileStreamsAndConsumers(js); err != nil {
		t.Fatalf("ReconcileStreamsAndConsumers: %v", err)
	}

	// Subscribe to the exact request subject on the translator stream and wait
	// for the published message.
	sub, err := js.SubscribeSync(subject.TranslatorRequestSubject("ns", "proj"),
		natsgo.Durable("it-publisher-receiver"),
		natsgo.ManualAck(),
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	publisher := NewTranslationRequestPublisher(js)
	cm := 0.5
	scenario := communication.ScenarioForTranslation{
		ID:                 42,
		ProjectNamespace:   "ns",
		ProjectName:        "proj",
		TranslationAttempt: 3,
		RecipeInfo:         json.RawMessage(`{"k":"v"}`),
		ConfidenceMetric:   &cm,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := publisher.PublishTranslationRequest(ctx, scenario); err != nil {
		t.Fatalf("publish: %v", err)
	}

	msg, err := sub.NextMsgWithContext(ctx)
	if err != nil {
		t.Fatalf("next msg: %v", err)
	}
	if msg.Subject != subject.TranslatorRequestSubject("ns", "proj") {
		t.Fatalf("subject = %q; want exact namespace-aware subject", msg.Subject)
	}
	var got translationRequestPayload
	if err := json.Unmarshal(msg.Data, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got.ID != 42 || got.TranslationAttempt != 3 {
		t.Fatalf("payload = %+v; want id=42 attempt=3", got)
	}
}
