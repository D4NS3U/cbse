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
	"errors"
	"strings"
	"testing"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/lifecycle"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
	"github.com/D4NS3U/cbse/scenario-manager/internal/subject"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := experimentalpha4.AddToScheme(s); err != nil {
		t.Fatalf("add alpha4: %v", err)
	}
	return s
}

func fakeK8s(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&experimentalpha4.SimulationExperiment{}).
		Build()
}

func phaseExperiment(namespace, name, phase string) *experimentalpha4.SimulationExperiment {
	return &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: types.UID("uid-1")},
		Status:     experimentalpha4.SimulationExperimentStatus{Phase: phase},
	}
}

// testAdapters builds an Adapters with a fake k8s client, a recording insert
// function, and a ready handler whose result is configurable. nc/js are nil
// (the Handle* methods do not use them).
func testAdapters(t *testing.T, k8s client.Client, insertErr error, readyStatus communication.TranslatorReadyHandlingStatus) (*Adapters, *insertRecorder, *readyRecorder) {
	t.Helper()
	ins := &insertRecorder{err: insertErr}
	rr := &readyRecorder{status: readyStatus}
	a := &Adapters{
		k8s: k8s,
		insertBatch: func(ctx context.Context, namespace, project string, records []persistence.ScenarioIntakeRecord) (int, error) {
			ins.record(namespace, project, records)
			if ins.err != nil {
				return 0, ins.err
			}
			return len(records), nil
		},
		readyHandler: func(ctx context.Context, m communication.TranslatorReadyMessage) communication.TranslatorReadyHandlingResult {
			rr.record(m)
			return communication.TranslatorReadyHandlingResult{Status: rr.status, Reason: "test"}
		},
	}
	return a, ins, rr
}

type insertRecorder struct {
	called    bool
	namespace string
	project   string
	records   []persistence.ScenarioIntakeRecord
	err       error
}

func (r *insertRecorder) record(namespace, project string, records []persistence.ScenarioIntakeRecord) {
	r.called = true
	r.namespace = namespace
	r.project = project
	r.records = records
}

type readyRecorder struct {
	called bool
	msg    communication.TranslatorReadyMessage
	status communication.TranslatorReadyHandlingStatus
}

func (r *readyRecorder) record(m communication.TranslatorReadyMessage) {
	r.called = true
	r.msg = m
}

func batchPayload(project string, reps int) []byte {
	batch := communication.ScenarioBatch{
		BatchID:   "b1",
		Project:   project,
		Scenarios: []communication.ScenarioRecord{{Priority: 1, NumberOfReps: reps}},
	}
	data, _ := json.Marshal(batch)
	return data
}

func readyPayload(attempt int, image string) []byte {
	p := translatorReadyPayload{TranslationAttempt: attempt, ContainerImage: image}
	data, _ := json.Marshal(p)
	return data
}

// --- EDS availability ---

func TestAvailabilityAdmittedRepliesReadyWithBatchSubject(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, _, _ := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	r := NewEDSAvailabilityResponder(a)

	reply, err := r.HandleAvailability(context.Background(), subject.EDSAvailabilitySubject("ns", "proj"), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if reply.Status != communication.AvailabilityStatusReady {
		t.Fatalf("status = %q; want ready", reply.Status)
	}
	if reply.BatchSubject != subject.EDSBatchSubject("ns", "proj") {
		t.Fatalf("batch subject = %q; want exact", reply.BatchSubject)
	}
}

func TestAvailabilityUnavailableRepliesError(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhasePending))
	a, _, _ := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	r := NewEDSAvailabilityResponder(a)

	reply, _ := r.HandleAvailability(context.Background(), subject.EDSAvailabilitySubject("ns", "proj"), nil)
	if reply.Status != communication.AvailabilityStatusError {
		t.Fatalf("status = %q; want error", reply.Status)
	}
	if reply.BatchSubject != "" {
		t.Fatalf("unavailable batch subject = %q; want empty", reply.BatchSubject)
	}
}

func TestAvailabilityTerminalRepliesErrorNoBatchSubject(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseFailed))
	a, _, _ := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	r := NewEDSAvailabilityResponder(a)

	reply, _ := r.HandleAvailability(context.Background(), subject.EDSAvailabilitySubject("ns", "proj"), nil)
	if reply.Status != communication.AvailabilityStatusError || reply.BatchSubject != "" {
		t.Fatalf("terminal reply = %+v; want error no batch subject", reply)
	}
}

func TestAvailabilityNotFoundRepliesError(t *testing.T) {
	k8s := fakeK8s(t) // no experiment
	a, _, _ := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	r := NewEDSAvailabilityResponder(a)

	reply, _ := r.HandleAvailability(context.Background(), subject.EDSAvailabilitySubject("ns", "missing"), nil)
	if reply.Status != communication.AvailabilityStatusError {
		t.Fatalf("status = %q; want error", reply.Status)
	}
}

func TestAvailabilityInvalidSubjectRepliesError(t *testing.T) {
	k8s := fakeK8s(t)
	a, _, _ := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	r := NewEDSAvailabilityResponder(a)

	reply, _ := r.HandleAvailability(context.Background(), "not-a-subject", nil)
	if reply.Status != communication.AvailabilityStatusError {
		t.Fatalf("status = %q; want error", reply.Status)
	}
}

// --- EDS batch ---

func TestEDSBatchAdmittedInsertsAndACKs(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, ins, _ := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	c := NewEDSBatchConsumer(a)

	decision, err := c.HandleEDSBatch(context.Background(), subject.EDSBatchSubject("ns", "proj"), batchPayload("proj", 10))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK", decision)
	}
	if !ins.called || ins.namespace != "ns" || ins.project != "proj" || len(ins.records) != 1 || ins.records[0].NumberOfReps != 10 {
		t.Fatalf("insert not called as expected: %+v", ins)
	}
}

func TestEDSBatchSubjectPayloadMismatchIsPoisonACK(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, ins, _ := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	c := NewEDSBatchConsumer(a)

	// Subject says proj; payload project says other.
	decision, _ := c.HandleEDSBatch(context.Background(), subject.EDSBatchSubject("ns", "proj"), batchPayload("other", 10))
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (poison)", decision)
	}
	if ins.called {
		t.Fatal("poison mismatch must not insert")
	}
}

func TestEDSBatchOutOfRangeRepsIsPoisonACK(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, ins, _ := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	c := NewEDSBatchConsumer(a)

	decision, _ := c.HandleEDSBatch(context.Background(), subject.EDSBatchSubject("ns", "proj"), batchPayload("proj", communication.MaxReps+1))
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (poison)", decision)
	}
	if ins.called {
		t.Fatal("out-of-range reps must not insert")
	}
}

func TestEDSBatchInvalidSubjectIsPoisonACK(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, ins, _ := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	c := NewEDSBatchConsumer(a)

	decision, _ := c.HandleEDSBatch(context.Background(), "cbse.ns.proj.eds.bogus", batchPayload("proj", 1))
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (poison)", decision)
	}
	if ins.called {
		t.Fatal("invalid subject must not insert")
	}
}

func TestEDSBatchTerminalACKDiscardNoInsert(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseFailed))
	a, ins, _ := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	c := NewEDSBatchConsumer(a)

	decision, _ := c.HandleEDSBatch(context.Background(), subject.EDSBatchSubject("ns", "proj"), batchPayload("proj", 1))
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (terminal discard)", decision)
	}
	if ins.called {
		t.Fatal("terminal must not insert")
	}
}

func TestEDSBatchUnavailableNAKs(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhasePending))
	a, ins, _ := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	c := NewEDSBatchConsumer(a)

	decision, _ := c.HandleEDSBatch(context.Background(), subject.EDSBatchSubject("ns", "proj"), batchPayload("proj", 1))
	if decision != decisionNAK {
		t.Fatalf("decision = %v; want NAK (unavailable)", decision)
	}
	if ins.called {
		t.Fatal("unavailable must not insert")
	}
}

func TestEDSBatchInsertErrorNAKs(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, ins, _ := testAdapters(t, k8s, errors.New("connection refused"), communication.TranslatorReadyHandled)
	c := NewEDSBatchConsumer(a)

	decision, _ := c.HandleEDSBatch(context.Background(), subject.EDSBatchSubject("ns", "proj"), batchPayload("proj", 1))
	if decision != decisionNAK {
		t.Fatalf("decision = %v; want NAK (insert error)", decision)
	}
	if !ins.called {
		t.Fatal("insert should have been attempted")
	}
}

// --- Translator ready ---

func TestTranslatorReadyAdmittedHandledACKs(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, _, rr := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	c := NewTranslatorReadyConsumer(a)

	decision, err := c.handleTranslatorReady(context.Background(), subject.TranslatorReadySubject("ns", "proj", "7"), readyPayload(1, "img@sha256:abc"), a.readyHandler)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK", decision)
	}
	if !rr.called || rr.msg.ScenarioID != 7 || rr.msg.TranslationAttempt != 1 || rr.msg.ProjectNamespace != "ns" || rr.msg.ProjectName != "proj" {
		t.Fatalf("handler not called as expected: %+v", rr)
	}
}

func TestTranslatorReadyAdmittedRetryNAKs(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, _, _ := testAdapters(t, k8s, nil, communication.TranslatorReadyRetry)
	c := NewTranslatorReadyConsumer(a)

	decision, _ := c.handleTranslatorReady(context.Background(), subject.TranslatorReadySubject("ns", "proj", "7"), readyPayload(1, "img"), a.readyHandler)
	if decision != decisionNAK {
		t.Fatalf("decision = %v; want NAK", decision)
	}
}

func TestTranslatorReadyTerminalACKDiscardNoHandler(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseFinished))
	a, _, rr := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	c := NewTranslatorReadyConsumer(a)

	decision, _ := c.handleTranslatorReady(context.Background(), subject.TranslatorReadySubject("ns", "proj", "7"), readyPayload(1, "img"), a.readyHandler)
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (terminal discard)", decision)
	}
	if rr.called {
		t.Fatal("terminal must not invoke the ready handler")
	}
}

func TestTranslatorReadyUnavailableNAKsNoHandler(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhasePending))
	a, _, rr := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	c := NewTranslatorReadyConsumer(a)

	decision, _ := c.handleTranslatorReady(context.Background(), subject.TranslatorReadySubject("ns", "proj", "7"), readyPayload(1, "img"), a.readyHandler)
	if decision != decisionNAK {
		t.Fatalf("decision = %v; want NAK", decision)
	}
	if rr.called {
		t.Fatal("unavailable must not invoke the ready handler")
	}
}

func TestTranslatorReadyInvalidSubjectPoisonACK(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, _, rr := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	c := NewTranslatorReadyConsumer(a)

	decision, _ := c.handleTranslatorReady(context.Background(), "cbse.ns.proj.trans.request", readyPayload(1, "img"), a.readyHandler)
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (poison)", decision)
	}
	if rr.called {
		t.Fatal("invalid subject must not invoke the handler")
	}
}

func TestTranslatorReadyNonPositiveScenarioIDPoisonACK(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, _, rr := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	c := NewTranslatorReadyConsumer(a)

	// scenario id "0" is not a canonical positive decimal.
	decision, _ := c.handleTranslatorReady(context.Background(), subject.TranslatorReadySubject("ns", "proj", "0"), readyPayload(1, "img"), a.readyHandler)
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (poison)", decision)
	}
	if rr.called {
		t.Fatal("non-positive scenario id must not invoke the handler")
	}
}

func TestTranslatorReadyNonPositiveAttemptPoisonACK(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, _, rr := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	c := NewTranslatorReadyConsumer(a)

	decision, _ := c.handleTranslatorReady(context.Background(), subject.TranslatorReadySubject("ns", "proj", "7"), readyPayload(0, "img"), a.readyHandler)
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (poison)", decision)
	}
	if rr.called {
		t.Fatal("non-positive attempt must not invoke the handler")
	}
}

func TestTranslatorReadyMalformedPayloadPoisonACK(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, _, rr := testAdapters(t, k8s, nil, communication.TranslatorReadyHandled)
	c := NewTranslatorReadyConsumer(a)

	decision, _ := c.handleTranslatorReady(context.Background(), subject.TranslatorReadySubject("ns", "proj", "7"), []byte("{not json"), a.readyHandler)
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (poison)", decision)
	}
	if rr.called {
		t.Fatal("malformed payload must not invoke the handler")
	}
}

// --- PPS evaluation ---

func evaluationPayload(namespace, project, uid, metric, verdict string, additionalRunners int) []byte {
	payload := communication.PPSEvaluationVerdict{
		ExperimentUID:     uid,
		Namespace:         namespace,
		Project:           project,
		ScenarioID:        7,
		RunnerRound:       1,
		Metric:            metric,
		Verdict:           verdict,
		SampleMean:        10.1,
		HalfWidth:         0.9,
		Replications:      40,
		ConfidenceMetric:  0.5,
		AdditionalRunners: additionalRunners,
		MaxReplications:   10000,
	}
	data, _ := json.Marshal(payload)
	return data
}

type evalRecorder struct {
	called bool
	msg    communication.PPSEvaluationMessage
	status communication.PPSEvaluationHandlingStatus
}

func (r *evalRecorder) record(m communication.PPSEvaluationMessage) {
	r.called = true
	r.msg = m
}

// testEvalAdapters builds an Adapters whose readyHandler slot carries the
// PPS-evaluation handler under test (the Adapters struct holds the handler
// function directly).
func testEvalAdapters(t *testing.T, k8s client.Client, status communication.PPSEvaluationHandlingStatus) (*Adapters, *evalRecorder) {
	t.Helper()
	er := &evalRecorder{status: status}
	a := &Adapters{
		k8s: k8s,
		readyHandler: func(ctx context.Context, m communication.TranslatorReadyMessage) communication.TranslatorReadyHandlingResult {
			// Never invoked in the PPS evaluation tests.
			t.Fatal("translator ready handler invoked by pps evaluation consumer")
			return communication.TranslatorReadyHandlingResult{}
		},
	}
	return a, er
}

func TestPPSEvaluationAdmittedHandledACKs(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, er := testEvalAdapters(t, k8s, communication.PPSEvaluationHandled)
	c := NewPPSEvaluationConsumer(a)
	handler := communication.PPSEvaluationHandler(func(ctx context.Context, m communication.PPSEvaluationMessage) communication.PPSEvaluationHandlingResult {
		er.record(m)
		return communication.PPSEvaluationHandlingResult{Status: er.status, Reason: "test"}
	})

	decision, err := c.handlePPSEvaluation(context.Background(), subject.PPSEvaluationSubject("ns", "proj", "7"), evaluationPayload("ns", "proj", "uid-1", "mean_wait_time", communication.VerdictMet, 0), handler)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK", decision)
	}
	if !er.called || er.msg.ScenarioID != 7 || er.msg.RunnerRound != 1 || er.msg.Verdict != communication.VerdictMet || er.msg.ProjectNamespace != "ns" || er.msg.ProjectName != "proj" || er.msg.ExperimentUID != "uid-1" {
		t.Fatalf("handler not called as expected: %+v", er)
	}
}

func TestPPSEvaluationAdmittedRetryNAKs(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, _ := testEvalAdapters(t, k8s, communication.PPSEvaluationRetry)
	c := NewPPSEvaluationConsumer(a)
	handler := communication.PPSEvaluationHandler(func(ctx context.Context, m communication.PPSEvaluationMessage) communication.PPSEvaluationHandlingResult {
		return communication.PPSEvaluationHandlingResult{Status: communication.PPSEvaluationRetry, Reason: "transient"}
	})

	decision, _ := c.handlePPSEvaluation(context.Background(), subject.PPSEvaluationSubject("ns", "proj", "7"), evaluationPayload("ns", "proj", "uid-1", "mean_wait_time", communication.VerdictMet, 0), handler)
	if decision != decisionNAK {
		t.Fatalf("decision = %v; want NAK", decision)
	}
}

func TestPPSEvaluationTerminalACKDiscardNoHandler(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseFinished))
	a, er := testEvalAdapters(t, k8s, communication.PPSEvaluationHandled)
	c := NewPPSEvaluationConsumer(a)
	handler := communication.PPSEvaluationHandler(func(ctx context.Context, m communication.PPSEvaluationMessage) communication.PPSEvaluationHandlingResult {
		er.record(m)
		return communication.PPSEvaluationHandlingResult{Status: er.status}
	})

	decision, _ := c.handlePPSEvaluation(context.Background(), subject.PPSEvaluationSubject("ns", "proj", "7"), evaluationPayload("ns", "proj", "uid-1", "mean_wait_time", communication.VerdictMet, 0), handler)
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (terminal discard)", decision)
	}
	if er.called {
		t.Fatal("terminal must not invoke the evaluation handler")
	}
}

func TestPPSEvaluationUnavailableNAKsNoHandler(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhasePending))
	a, er := testEvalAdapters(t, k8s, communication.PPSEvaluationHandled)
	c := NewPPSEvaluationConsumer(a)
	handler := communication.PPSEvaluationHandler(func(ctx context.Context, m communication.PPSEvaluationMessage) communication.PPSEvaluationHandlingResult {
		er.record(m)
		return communication.PPSEvaluationHandlingResult{Status: er.status}
	})

	decision, _ := c.handlePPSEvaluation(context.Background(), subject.PPSEvaluationSubject("ns", "proj", "7"), evaluationPayload("ns", "proj", "uid-1", "mean_wait_time", communication.VerdictMet, 0), handler)
	if decision != decisionNAK {
		t.Fatalf("decision = %v; want NAK (unavailable)", decision)
	}
	if er.called {
		t.Fatal("unavailable must not invoke the evaluation handler")
	}
}

func TestPPSEvaluationInvalidSubjectPoisonACK(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, er := testEvalAdapters(t, k8s, communication.PPSEvaluationHandled)
	c := NewPPSEvaluationConsumer(a)
	handler := communication.PPSEvaluationHandler(func(ctx context.Context, m communication.PPSEvaluationMessage) communication.PPSEvaluationHandlingResult {
		er.record(m)
		return communication.PPSEvaluationHandlingResult{Status: er.status}
	})

	// The PPS request subject is not an evaluation subject.
	decision, _ := c.handlePPSEvaluation(context.Background(), subject.PPSRequestSubject("ns", "proj"), evaluationPayload("ns", "proj", "uid-1", "m", communication.VerdictMet, 0), handler)
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (poison)", decision)
	}
	if er.called {
		t.Fatal("invalid subject must not invoke the handler")
	}
}

func TestPPSEvaluationNonPositiveScenarioIDPoisonACK(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, er := testEvalAdapters(t, k8s, communication.PPSEvaluationHandled)
	c := NewPPSEvaluationConsumer(a)
	handler := communication.PPSEvaluationHandler(func(ctx context.Context, m communication.PPSEvaluationMessage) communication.PPSEvaluationHandlingResult {
		er.record(m)
		return communication.PPSEvaluationHandlingResult{Status: er.status}
	})

	// scenario id "0" is not a canonical positive decimal; the subject itself
	// is malformed and rejected by Parse.
	decision, _ := c.handlePPSEvaluation(context.Background(), "cbse.ns.proj.pps.0.evaluation", evaluationPayload("ns", "proj", "uid-1", "m", communication.VerdictMet, 0), handler)
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (poison)", decision)
	}
	if er.called {
		t.Fatal("non-positive scenario id must not invoke the handler")
	}
}

func TestPPSEvaluationMalformedPayloadPoisonACK(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, er := testEvalAdapters(t, k8s, communication.PPSEvaluationHandled)
	c := NewPPSEvaluationConsumer(a)
	handler := communication.PPSEvaluationHandler(func(ctx context.Context, m communication.PPSEvaluationMessage) communication.PPSEvaluationHandlingResult {
		er.record(m)
		return communication.PPSEvaluationHandlingResult{Status: er.status}
	})

	decision, _ := c.handlePPSEvaluation(context.Background(), subject.PPSEvaluationSubject("ns", "proj", "7"), []byte("{not json"), handler)
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (poison)", decision)
	}
	if er.called {
		t.Fatal("malformed payload must not invoke the handler")
	}
}

func TestPPSEvaluationInvalidEnumPoisonACK(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, er := testEvalAdapters(t, k8s, communication.PPSEvaluationHandled)
	c := NewPPSEvaluationConsumer(a)
	handler := communication.PPSEvaluationHandler(func(ctx context.Context, m communication.PPSEvaluationMessage) communication.PPSEvaluationHandlingResult {
		er.record(m)
		return communication.PPSEvaluationHandlingResult{Status: er.status}
	})

	// An unknown verdict enum is rejected by the strict wire type on decode.
	bad := strings.Replace(string(evaluationPayload("ns", "proj", "uid-1", "m", "met", 0)), `"verdict":"met"`, `"verdict":"bogus"`, 1)
	decision, _ := c.handlePPSEvaluation(context.Background(), subject.PPSEvaluationSubject("ns", "proj", "7"), []byte(bad), handler)
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (poison)", decision)
	}
	if er.called {
		t.Fatal("invalid verdict enum must not invoke the handler")
	}
}

func TestPPSEvaluationIdentityMismatchPoisonACK(t *testing.T) {
	k8s := fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress))
	a, er := testEvalAdapters(t, k8s, communication.PPSEvaluationHandled)
	c := NewPPSEvaluationConsumer(a)
	handler := communication.PPSEvaluationHandler(func(ctx context.Context, m communication.PPSEvaluationMessage) communication.PPSEvaluationHandlingResult {
		er.record(m)
		return communication.PPSEvaluationHandlingResult{Status: er.status}
	})

	// Payload identity differs from the subject identity: permanent poison.
	decision, _ := c.handlePPSEvaluation(context.Background(), subject.PPSEvaluationSubject("ns", "proj", "7"), evaluationPayload("other", "proj", "uid-1", "m", communication.VerdictMet, 0), handler)
	if decision != decisionACK {
		t.Fatalf("decision = %v; want ACK (poison)", decision)
	}
	if er.called {
		t.Fatal("identity mismatch must not invoke the handler")
	}
}

func TestPPSEvaluationGateFetchErrorNAKs(t *testing.T) {
	k8s := &errK8s{Client: fakeK8s(t, phaseExperiment("ns", "proj", lifecycle.PhaseInProgress)), err: errors.New("apiserver unavailable")}
	a, er := testEvalAdapters(t, k8s, communication.PPSEvaluationHandled)
	c := NewPPSEvaluationConsumer(a)
	handler := communication.PPSEvaluationHandler(func(ctx context.Context, m communication.PPSEvaluationMessage) communication.PPSEvaluationHandlingResult {
		er.record(m)
		return communication.PPSEvaluationHandlingResult{Status: er.status}
	})

	decision, _ := c.handlePPSEvaluation(context.Background(), subject.PPSEvaluationSubject("ns", "proj", "7"), evaluationPayload("ns", "proj", "uid-1", "m", communication.VerdictMet, 0), handler)
	if decision != decisionNAK {
		t.Fatalf("decision = %v; want NAK (transient gate fetch failure)", decision)
	}
	if er.called {
		t.Fatal("gate fetch failure must not invoke the handler")
	}
}

// errK8s wraps a real client.Client and overrides Get to always fail with a
// transient (non-NotFound) error, exercising the retry branch.
type errK8s struct {
	client.Client
	err error
}

func (e *errK8s) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return e.err
}
