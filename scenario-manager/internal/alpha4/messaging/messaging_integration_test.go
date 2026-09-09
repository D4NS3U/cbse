//go:build integration

package messaging

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/subject"
	natsgo "github.com/nats-io/nats.go"
)

const itNatsURLEnv = "SCENARIO_MANAGER_NATS_URL"

func connectITNATS(t *testing.T) (*natsgo.Conn, natsgo.JetStreamContext) {
	t.Helper()
	url := os.Getenv(itNatsURLEnv)
	if url == "" {
		t.Skipf("Environment variable %s is not set; skipping alpha4 NATS integration test", itNatsURLEnv)
	}
	// Alpha4 is authentication-free; connect with no credentials. If legacy
	// user/password env vars are set, use them so the test also works against a
	// credentialed broker.
	opts := []natsgo.Option{natsgo.Name("alpha4-messaging-it")}
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

// dropStreams deletes the two alpha4 streams if present so the test starts and
// ends from a clean state. A missing stream is success.
func dropStreams(t *testing.T, js natsgo.JetStreamContext) {
	t.Helper()
	for _, s := range []string{EDSStreamName, TranslatorStreamName} {
		if err := js.DeleteStream(s); err != nil && !errors.Is(err, natsgo.ErrStreamNotFound) {
			t.Logf("drop stream %s: %v", s, err)
		}
	}
}

func TestReconcileStreamsAndConsumersAgainstBroker(t *testing.T) {
	_, js := connectITNATS(t)
	dropStreams(t, js)
	t.Cleanup(func() { dropStreams(t, js) })

	if err := ReconcileStreamsAndConsumers(js); err != nil {
		t.Fatalf("ReconcileStreamsAndConsumers: %v", err)
	}
	// Both streams exist.
	for _, s := range []string{EDSStreamName, TranslatorStreamName} {
		if _, err := js.StreamInfo(s); err != nil {
			t.Fatalf("StreamInfo %s: %v", s, err)
		}
	}
	// Both SM consumers exist and pass ownership verification.
	for _, c := range SMConsumers() {
		info, err := js.ConsumerInfo(c.Stream, c.Config.Durable)
		if err != nil {
			t.Fatalf("ConsumerInfo %s/%s: %v", c.Stream, c.Config.Durable, err)
		}
		if err := VerifySMConsumerOwnership(info, c.Config); err != nil {
			t.Fatalf("VerifySMConsumerOwnership %s/%s: %v", c.Stream, c.Config.Durable, err)
		}
	}
	// Idempotent: a second reconcile on matching streams is success.
	if err := ReconcileStreamsAndConsumers(js); err != nil {
		t.Fatalf("idempotent ReconcileStreamsAndConsumers: %v", err)
	}
}

func TestTranslatorConsumerOwnershipAndDeletion(t *testing.T) {
	_, js := connectITNATS(t)
	dropStreams(t, js)
	t.Cleanup(func() { dropStreams(t, js) })

	if err := ReconcileStreamsAndConsumers(js); err != nil {
		t.Fatalf("ReconcileStreamsAndConsumers: %v", err)
	}
	uid := "11111111-2222-3333-4444-555555555555"
	ns, proj := "alpha4-it", "exp1"
	if _, err := js.AddConsumer(TranslatorStreamName, TranslatorConsumerConfig(uid, ns, proj)); err != nil {
		t.Fatalf("AddConsumer: %v", err)
	}
	info, err := js.ConsumerInfo(TranslatorStreamName, TranslatorConsumerName(uid))
	if err != nil {
		t.Fatalf("ConsumerInfo: %v", err)
	}
	if err := VerifyTranslatorConsumerOwnership(info, TranslatorStreamName, uid, ns, proj); err != nil {
		t.Fatalf("VerifyTranslatorConsumerOwnership: %v", err)
	}
	// A wrong-UID consumer is a collision: deletion must fail and retain it.
	if err := DeleteTranslatorConsumer(js, TranslatorStreamName, "deadbeef-dead-beef", ns, proj); err == nil {
		t.Fatal("DeleteTranslatorConsumer with wrong UID: want collision error")
	}
	// The consumer is still present.
	if _, err := js.ConsumerInfo(TranslatorStreamName, TranslatorConsumerName(uid)); err != nil {
		t.Fatalf("consumer missing after collision: %v", err)
	}
	// Correct-UID deletion succeeds.
	if err := DeleteTranslatorConsumer(js, TranslatorStreamName, uid, ns, proj); err != nil {
		t.Fatalf("DeleteTranslatorConsumer: %v", err)
	}
	// A second deletion is success (missing consumer).
	if err := DeleteTranslatorConsumer(js, TranslatorStreamName, uid, ns, proj); err != nil {
		t.Fatalf("DeleteTranslatorConsumer (second): %v", err)
	}
}

func TestPurgeSubjectAgainstBroker(t *testing.T) {
	_, js := connectITNATS(t)
	dropStreams(t, js)
	t.Cleanup(func() { dropStreams(t, js) })

	if err := ReconcileStreamsAndConsumers(js); err != nil {
		t.Fatalf("ReconcileStreamsAndConsumers: %v", err)
	}
	nsIdent, err := subject.ValidateIdent("alpha4-it")
	if err != nil {
		t.Fatalf("ns ident: %v", err)
	}
	projIdent, err := subject.ValidateIdent("exp1")
	if err != nil {
		t.Fatalf("proj ident: %v", err)
	}
	batchSubject := subject.EDSBatchSubject(nsIdent, projIdent)

	// Publish a couple of messages on the project batch subject.
	for i := 0; i < 2; i++ {
		if _, err := js.Publish(batchSubject, []byte(fmt.Sprintf("msg-%d", i))); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	// Purge the project subject.
	if err := PurgeSubject(js, EDSStreamName, batchSubject); err != nil {
		t.Fatalf("PurgeSubject: %v", err)
	}
	// Purge on a missing stream is success.
	if err := PurgeSubject(js, "cbse_does_not_exist", "cbse.*.*.eds.scenarios"); err != nil {
		t.Fatalf("PurgeSubject missing stream: %v", err)
	}
}
