//go:build integration

package natsadapter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/messaging"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/subject"
	natsgo "github.com/nats-io/nats.go"
)

const itNatsURLEnv = "SCENARIO_MANAGER_NATS_URL"

func connectITNATS(t *testing.T) (*natsgo.Conn, natsgo.JetStreamContext) {
	t.Helper()
	url := os.Getenv(itNatsURLEnv)
	if url == "" {
		t.Skipf("Environment variable %s is not set; skipping alpha4 natsadapter integration test", itNatsURLEnv)
	}
	opts := []natsgo.Option{natsgo.Name("alpha4-natsadapter-it")}
	if user := os.Getenv("SCENARIO_MANAGER_NATS_USER"); user != "" {
		opts = append(opts, natsgo.UserInfo(user, os.Getenv("SCENARIO_MANAGER_NATS_PASSWORD")))
	}
	nc, err := natsgo.Connect(url, opts...)
	if err != nil {
		t.Fatalf("connect NATS %s: %v", url, err)
	}
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream context: %v", err)
	}
	return nc, js
}

func dropStreams(t *testing.T, js natsgo.JetStreamContext) {
	t.Helper()
	for _, s := range []string{messaging.EDSStreamName, messaging.TranslatorStreamName} {
		if err := js.DeleteStream(s); err != nil && !errors.Is(err, natsgo.ErrStreamNotFound) {
			t.Logf("drop stream %s: %v", s, err)
		}
	}
}

// TestTranslationRequestPublisherRoundTrip proves the publisher publishes on
// the exact namespace-aware subject and returns only after a JetStream PubAck,
// so the caller's MarkScenarioTranslationRequestPublished reflects a durably
// accepted message.
func TestTranslationRequestPublisherRoundTrip(t *testing.T) {
	_, js := connectITNATS(t)
	dropStreams(t, js)
	t.Cleanup(func() { dropStreams(t, js) })
	if err := messaging.ReconcileStreamsAndConsumers(js); err != nil {
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
