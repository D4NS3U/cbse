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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/subject"
	natsgo "github.com/nats-io/nats.go"
)

// TestPPSEvaluationRequestPublishAndConsumerDelivery proves the request side
// of the PPS wire contract against a real NATS broker: the SM publishes an
// evaluation request with a PubAck on cbse.<ns>.<proj>.pps.request, and the
// per-experiment PPS durable consumer (ensured by EnsurePPSConsumer) receives
// the exact payload. It uses the documented env pattern from
// messaging_integration_test.go (SCENARIO_MANAGER_NATS_URL; unset = skip).
func TestPPSEvaluationRequestPublishAndConsumerDelivery(t *testing.T) {
	_, js := connectITNATS(t)
	dropStreams(t, js)
	t.Cleanup(func() { dropStreams(t, js) })

	if err := ReconcileStreamsAndConsumers(js); err != nil {
		t.Fatalf("ReconcileStreamsAndConsumers: %v", err)
	}
	uid := "11111111-2222-3333-4444-555555555555"
	ns, proj := "alpha4-it", "exp1"

	// The per-experiment PPS consumer is ensured at admission.
	if err := EnsurePPSConsumer(js, PPSStreamName, uid, ns, proj); err != nil {
		t.Fatalf("EnsurePPSConsumer: %v", err)
	}
	t.Cleanup(func() { _ = DeletePPSConsumer(js, PPSStreamName, uid, ns, proj) })

	// Attach to the ensured pull consumer (the SM per-experiment PPS durable
	// is a pull consumer; the reference PPS binds with PullSubscribe).
	filter := subject.PPSRequestSubject(ident(t, ns), ident(t, proj))
	sub, err := js.PullSubscribe(filter, PPSConsumerName(uid))
	if err != nil {
		t.Fatalf("attach pps consumer: %v", err)
	}
	defer sub.Unsubscribe()

	// Publish through the PubAck-gated SM publisher.
	pub := NewEvaluationRequestPublisher(js)
	scenario := communication.ScenarioForEvaluation{
		ExperimentUID:     uid,
		ProjectNamespace:  ns,
		ProjectName:       proj,
		ScenarioID:        42,
		EvaluationAttempt: 1,
		RunnerRound:       1,
		NumberOfReps:      40,
		ConfidenceMetric:  0.5,
	}
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pubCancel()
	if err := pub.PublishEvaluationRequest(pubCtx, scenario); err != nil {
		t.Fatalf("PublishEvaluationRequest: %v", err)
	}

	// The consumer delivers the exact message.
	msgs, err := sub.Fetch(1, natsgo.MaxWait(10*time.Second))
	if err != nil && !errors.Is(err, natsgo.ErrTimeout) {
		t.Fatalf("wait for delivery: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("delivered %d messages; want 1", len(msgs))
	}
	msg := msgs[0]
	if msg.Subject != filter {
		t.Fatalf("delivery subject = %q; want %q", msg.Subject, filter)
	}
	var got communication.ScenarioEvaluationRequest
	if err := json.Unmarshal(msg.Data, &got); err != nil {
		t.Fatalf("decode delivered request: %v", err)
	}
	if got.ExperimentUID != uid || got.Namespace != ns || got.Project != proj {
		t.Fatalf("delivered identity = %+v", got)
	}
	if got.ScenarioID != 42 || got.RunnerRound != 1 || got.NumberOfReps != 40 || got.ConfidenceMetric != 0.5 {
		t.Fatalf("delivered counts = %+v", got)
	}
	_ = msg.Ack()
}

// TestPPSEvaluationVerdictDeliveredToSMConsumer proves the verdict side of the
// PPS wire contract against a real NATS broker: a verdict published on
// cbse.<ns>.<proj>.pps.<id>.evaluation is retained on the cbse_pps stream and
// delivered to the SM-anchored scenario-manager-pps-evaluation durable
// consumer. It uses the documented env pattern from
// messaging_integration_test.go (SCENARIO_MANAGER_NATS_URL; unset = skip).
func TestPPSEvaluationVerdictDeliveredToSMConsumer(t *testing.T) {
	_, js := connectITNATS(t)
	dropStreams(t, js)
	t.Cleanup(func() { dropStreams(t, js) })

	if err := ReconcileStreamsAndConsumers(js); err != nil {
		t.Fatalf("ReconcileStreamsAndConsumers: %v", err)
	}
	ns, proj := "alpha4-it", "exp1"

	cfg := PPSEvaluationConsumerConfig()
	delivered := make(chan *natsgo.Msg, 1)
	// Attach exactly as the production consumer does: queue-subscribe on the
	// stream subject so nats.go resolves the stream, binds the reconciled
	// durable, and receives deliveries on its fixed deliver subject.
	if _, err := js.QueueSubscribe(subject.PPSEvaluationStreamSubject, cfg.DeliverGroup, func(msg *natsgo.Msg) {
		delivered <- msg
	}, ppsEvaluationConsumerOptions()...); err != nil {
		t.Fatalf("attach SM pps evaluation consumer: %v", err)
	}

	// The PPS publishes the verdict on the exact scenario subject.
	subj := subject.PPSEvaluationSubject(ident(t, ns), ident(t, proj), "42")
	payload := fmt.Sprintf(`{"experiment_uid":"11111111-2222-3333-4444-555555555555","namespace":%q,"project":%q,"scenario_id":42,"runner_round":1,"metric":"mean_wait_time","verdict":"met","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"additional_runners":0,"max_replications":10000}`, ns, proj)
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pubCancel()
	if _, err := js.PublishMsg(&natsgo.Msg{Subject: subj, Data: []byte(payload)}, natsgo.Context(pubCtx)); err != nil {
		t.Fatalf("publish verdict: %v", err)
	}

	select {
	case msg := <-delivered:
		if msg.Subject != subj {
			t.Fatalf("delivery subject = %q; want %q", msg.Subject, subj)
		}
		var got communication.PPSEvaluationVerdict
		if err := json.Unmarshal(msg.Data, &got); err != nil {
			t.Fatalf("decode delivered verdict: %v", err)
		}
		if got.ScenarioID != 42 || got.Verdict != communication.VerdictMet || got.Metric != "mean_wait_time" {
			t.Fatalf("delivered verdict = %+v", got)
		}
		_ = msg.Ack()
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for SM pps evaluation delivery")
	}
}

func ident(t *testing.T, s string) subject.Ident {
	t.Helper()
	i, err := subject.ValidateIdent(s)
	if err != nil {
		t.Fatalf("ident %q: %v", s, err)
	}
	return i
}
