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

package evaluationpub

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// fakeDeps is a recording fake for the evaluationpub Dependencies. Each
// function field is configured per-test; the recorder captures call order.
type recorder struct {
	mu sync.Mutex

	claimAttempts  []int
	claimOK        bool
	claimErr       error
	loadAttempts   []int
	loadProjection *EvaluationProjection
	loadErr        error
	ensureAttempts []string
	ensureErr      error
	startAttempts  []string
	startOK        bool
	publishCalls   int
	publishScen    communication.ScenarioForEvaluation
	publishErr     error
	confirmedCalls []string
	confirmOK      bool
}

func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimAttempts = nil
	r.loadAttempts = nil
	r.ensureAttempts = nil
	r.startAttempts = nil
	r.publishCalls = 0
	r.publishScen = communication.ScenarioForEvaluation{}
	r.confirmedCalls = nil
}

// depsFromRecorder builds Dependencies around the recorder with the given
// experiment decision and projection behavior.
func depsFromRecorder(rec *recorder, candidate *PostProcessingCandidate, attempt int, exp *experimentalpha4.SimulationExperiment, getErr error) Dependencies {
	return Dependencies{
		now:  time.Now,
		wait: func(ctx context.Context, d time.Duration) error { return ctx.Err() },
		nextPostProcessingScenario: func(ctx context.Context) (*PostProcessingCandidate, error) {
			return candidate, nil
		},
		claimScenarioForEvaluation: func(ctx context.Context, id int) (int, bool, error) {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.claimAttempts = append(rec.claimAttempts, id)
			if rec.claimErr != nil {
				return 0, false, rec.claimErr
			}
			return attempt, rec.claimOK, nil
		},
		loadEvaluationProjection: func(ctx context.Context, id int) (*EvaluationProjection, error) {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.loadAttempts = append(rec.loadAttempts, id)
			if rec.loadErr != nil {
				return nil, rec.loadErr
			}
			return rec.loadProjection, nil
		},
		getExperiment: func(ctx context.Context, namespace, name string) (*experimentalpha4.SimulationExperiment, error) {
			if getErr != nil {
				return nil, getErr
			}
			return exp, nil
		},
		ensurePPSConsumer: func(ctx context.Context, uid, namespace, project string) error {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.ensureAttempts = append(rec.ensureAttempts, uid+"/"+namespace+"/"+project)
			return rec.ensureErr
		},
		markEvaluationPublishStarted: func(ctx context.Context, id, attempt int) (bool, error) {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.startAttempts = append(rec.startAttempts, itoa(id)+"/"+itoa(attempt))
			return rec.startOK, nil
		},
		publish: func(ctx context.Context, s communication.ScenarioForEvaluation) error {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.publishCalls++
			rec.publishScen = s
			return rec.publishErr
		},
		markEvaluationRequestPublished: func(ctx context.Context, id, attempt int) (bool, error) {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.confirmedCalls = append(rec.confirmedCalls, itoa(id)+"/"+itoa(attempt))
			return rec.confirmOK, nil
		},
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

func candidate() *PostProcessingCandidate {
	return &PostProcessingCandidate{ID: 42, ProjectNamespace: "ns", ProjectName: "proj"}
}

func projection() *EvaluationProjection {
	eps := 0.5
	return &EvaluationProjection{
		ScenarioID:       42,
		ProjectNamespace: "ns",
		ProjectName:      "proj",
		RunnerRound:      1,
		NumberOfReps:     40,
		ConfidenceMetric: &eps,
	}
}

func inProgressExp(ns, name string) *experimentalpha4.SimulationExperiment {
	return &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID("uid-1")},
		Status:     experimentalpha4.SimulationExperimentStatus{Phase: "InProgress"},
	}
}

func phaseExp(ns, name, phase string) *experimentalpha4.SimulationExperiment {
	e := inProgressExp(ns, name)
	e.Status.Phase = phase
	return e
}

type fakePublisher struct {
	mu    sync.Mutex
	calls int
}

func (f *fakePublisher) PublishEvaluationRequest(ctx context.Context, s communication.ScenarioForEvaluation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return nil
}

func TestProcessNextHappyPath(t *testing.T) {
	rec := &recorder{claimOK: true, loadProjection: projection(), startOK: true, confirmOK: true}
	deps := depsFromRecorder(rec, candidate(), 1, inProgressExp("ns", "proj"), nil)
	p, err := NewPublisher(&fakePublisher{}, deps)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	if err := p.processNext(context.Background()); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	if len(rec.claimAttempts) != 1 || rec.claimAttempts[0] != 42 {
		t.Fatalf("claims = %v", rec.claimAttempts)
	}
	if len(rec.ensureAttempts) != 1 || rec.ensureAttempts[0] != "uid-1/ns/proj" {
		t.Fatalf("ensure = %v", rec.ensureAttempts)
	}
	if len(rec.startAttempts) != 1 || rec.startAttempts[0] != "42/1" {
		t.Fatalf("start = %v", rec.startAttempts)
	}
	if rec.publishCalls != 1 {
		t.Fatalf("publishCalls = %d", rec.publishCalls)
	}
	s := rec.publishScen
	if s.ExperimentUID != "uid-1" || s.ScenarioID != 42 || s.EvaluationAttempt != 1 || s.RunnerRound != 1 || s.NumberOfReps != 40 || s.ConfidenceMetric != 0.5 || s.ProjectNamespace != "ns" || s.ProjectName != "proj" {
		t.Fatalf("published scenario = %+v", s)
	}
	if len(rec.confirmedCalls) != 1 || rec.confirmedCalls[0] != "42/1" {
		t.Fatalf("confirmed = %v", rec.confirmedCalls)
	}
}

func TestProcessNextNoCandidateIsIdle(t *testing.T) {
	rec := &recorder{claimOK: true}
	deps := depsFromRecorder(rec, nil, 1, inProgressExp("ns", "proj"), nil)
	p, _ := NewPublisher(&fakePublisher{}, deps)
	if err := p.processNext(context.Background()); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	if len(rec.claimAttempts) != 0 || rec.publishCalls != 0 {
		t.Fatalf("idle iteration must not claim or publish: %+v", rec)
	}
}

func TestProcessNextStaleClaimIsNoOp(t *testing.T) {
	rec := &recorder{claimOK: false}
	deps := depsFromRecorder(rec, candidate(), 1, inProgressExp("ns", "proj"), nil)
	p, _ := NewPublisher(&fakePublisher{}, deps)
	if err := p.processNext(context.Background()); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	// The claim was attempted; nothing else happens.
	if len(rec.claimAttempts) != 1 || rec.publishCalls != 0 || len(rec.startAttempts) != 0 {
		t.Fatalf("stale claim must stop the pass: %+v", rec)
	}
}

func TestProcessNextGateRejectionSkipsPublish(t *testing.T) {
	// A terminal experiment is a pre-publish gate rejection: no NATS call, no
	// publish-start marker; the row stays claimable on the next pass.
	rec := &recorder{claimOK: true, loadProjection: projection(), startOK: true}
	deps := depsFromRecorder(rec, candidate(), 1, phaseExp("ns", "proj", "Failed"), nil)
	p, _ := NewPublisher(&fakePublisher{}, deps)
	if err := p.processNext(context.Background()); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	if len(rec.startAttempts) != 0 || rec.publishCalls != 0 {
		t.Fatalf("gate rejection must not mark started or publish: %+v", rec)
	}
	if len(rec.ensureAttempts) != 0 {
		t.Fatalf("gate rejection must not ensure the consumer: %+v", rec)
	}
}

func TestProcessNextGateFetchErrorRetries(t *testing.T) {
	rec := &recorder{claimOK: true, loadProjection: projection()}
	deps := depsFromRecorder(rec, candidate(), 1, nil, errors.New("apiserver unavailable"))
	p, _ := NewPublisher(&fakePublisher{}, deps)
	if err := p.processNext(context.Background()); err == nil {
		t.Fatal("gate fetch error: want error")
	}
	if rec.publishCalls != 0 {
		t.Fatalf("fetch error must not publish: %+v", rec)
	}
}

func TestProcessNextNullConfidenceMetricLeavesRowClaimable(t *testing.T) {
	// A NULL confidence_metric fails validation before the publish-start
	// marker: the row stays PostProcessing and claimable on the next pass.
	proj := projection()
	proj.ConfidenceMetric = nil
	rec := &recorder{claimOK: true, loadProjection: proj}
	deps := depsFromRecorder(rec, candidate(), 1, inProgressExp("ns", "proj"), nil)
	p, _ := NewPublisher(&fakePublisher{}, deps)
	if err := p.processNext(context.Background()); err == nil {
		t.Fatal("null confidence_metric: want error")
	}
	if len(rec.startAttempts) != 0 || rec.publishCalls != 0 {
		t.Fatalf("invalid projection must not mark started or publish: %+v", rec)
	}
}

func TestProcessNextPublishFailureLeavesRowClaimable(t *testing.T) {
	// A publish failure (or lost PubAck) does not mark confirmed and does not
	// consume the attempt: the exact-attempt guards leave the row claimable on
	// the next pass.
	rec := &recorder{claimOK: true, loadProjection: projection(), startOK: true, publishErr: errors.New("no PubAck")}
	deps := depsFromRecorder(rec, candidate(), 1, inProgressExp("ns", "proj"), nil)
	p, _ := NewPublisher(&fakePublisher{}, deps)
	if err := p.processNext(context.Background()); err == nil {
		t.Fatal("publish failure: want error")
	}
	if len(rec.startAttempts) != 1 || len(rec.confirmedCalls) != 0 {
		t.Fatalf("publish failure must mark started once and confirm never: %+v", rec)
	}
}

func TestProcessNextStalePublishStartIsNoOp(t *testing.T) {
	rec := &recorder{claimOK: true, loadProjection: projection(), startOK: false}
	deps := depsFromRecorder(rec, candidate(), 1, inProgressExp("ns", "proj"), nil)
	p, _ := NewPublisher(&fakePublisher{}, deps)
	if err := p.processNext(context.Background()); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	if rec.publishCalls != 0 || len(rec.confirmedCalls) != 0 {
		t.Fatalf("stale publish-start must not publish or confirm: %+v", rec)
	}
}

func TestProcessNextStaleConfirmationIsNoOp(t *testing.T) {
	rec := &recorder{claimOK: true, loadProjection: projection(), startOK: true, confirmOK: false}
	deps := depsFromRecorder(rec, candidate(), 1, inProgressExp("ns", "proj"), nil)
	p, _ := NewPublisher(&fakePublisher{}, deps)
	if err := p.processNext(context.Background()); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	if rec.publishCalls != 1 || len(rec.confirmedCalls) != 1 {
		t.Fatalf("stale confirmation: publish once, confirm attempt recorded: %+v", rec)
	}
}

func TestProcessNextEnsurePPSConsumerFailureRetries(t *testing.T) {
	rec := &recorder{claimOK: true, loadProjection: projection(), startOK: true, ensureErr: errors.New("consumer collision")}
	deps := depsFromRecorder(rec, candidate(), 1, inProgressExp("ns", "proj"), nil)
	p, _ := NewPublisher(&fakePublisher{}, deps)
	if err := p.processNext(context.Background()); err == nil {
		t.Fatal("ensure consumer failure: want error")
	}
	if len(rec.startAttempts) != 0 || rec.publishCalls != 0 {
		t.Fatalf("ensure failure must not mark started or publish: %+v", rec)
	}
}

func TestStartShutsDown(t *testing.T) {
	rec := &recorder{}
	deps := depsFromRecorder(rec, nil, 1, inProgressExp("ns", "proj"), nil)
	p, err := NewPublisher(&fakePublisher{}, deps)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	p.delay = 5 * time.Millisecond
	p.iterationTimeout = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done, err := p.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not join on cancel")
	}
}

func TestNewPublisherRejectsNilPublisher(t *testing.T) {
	if _, err := NewPublisher(nil, Dependencies{now: time.Now}); err == nil {
		t.Fatal("nil publisher: want error")
	}
}
