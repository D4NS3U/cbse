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

package verdict

import (
	"context"
	"errors"
	"strings"
	"testing"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	corev1 "k8s.io/api/core/v1"
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
		t.Fatalf("add alpha4 to scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add core/v1 to scheme: %v", err)
	}
	return s
}

func fakeK8s(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build()
}

func inProgressExperiment(namespace, name string, uid types.UID) *experimentalpha4.SimulationExperiment {
	return &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: uid},
		Status:     experimentalpha4.SimulationExperimentStatus{Phase: "InProgress"},
	}
}

// fakeVerdictDeps wires a Handler with recording, configurable database
// surfaces.
type fakeVerdictDeps struct {
	state      string
	round      int
	found      bool
	loadErr    error
	finishOK   bool
	finishErr  error
	claimNext  int
	claimOK    bool
	claimErr   error
	failOK     bool
	failErr    error
	loadCalled bool
	ops        []string
}

func (f *fakeVerdictDeps) handler(k8s client.Client) *Handler {
	return &Handler{
		k8s: k8s,
		loadRound: func(ctx context.Context, scenarioID int) (string, int, bool, error) {
			f.loadCalled = true
			if f.loadErr != nil {
				return "", 0, false, f.loadErr
			}
			return f.state, f.round, f.found, nil
		},
		finish: func(ctx context.Context, scenarioID int) (bool, error) {
			f.ops = append(f.ops, "finish")
			return f.finishOK, f.finishErr
		},
		claimRound: func(ctx context.Context, scenarioID, additionalRunners int) (int, bool, error) {
			f.ops = append(f.ops, "claim-round")
			if f.claimErr != nil {
				return 0, false, f.claimErr
			}
			return f.claimNext, f.claimOK, nil
		},
		failFrom: func(ctx context.Context, scenarioID int, fromState string) (bool, error) {
			f.ops = append(f.ops, "fail-from:"+fromState)
			return f.failOK, f.failErr
		},
	}
}

func verdictMsg(namespace, project, uid string, scenarioID, round int, verdict string, additional int) communication.PPSEvaluationMessage {
	return communication.PPSEvaluationMessage{
		ProjectNamespace:  namespace,
		ProjectName:       project,
		ExperimentUID:     uid,
		ScenarioID:        scenarioID,
		RunnerRound:       round,
		Metric:            "mean_wait_time",
		Verdict:           verdict,
		SampleMean:        10.1,
		HalfWidth:         0.9,
		Replications:      40,
		ConfidenceMetric:  0.5,
		AdditionalRunners: additional,
		MaxReplications:   10000,
	}
}

func TestHandleMetTransitionsFinished(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", "uid-1")
	deps := &fakeVerdictDeps{state: "PostProcessing", round: 1, found: true, finishOK: true}
	h := deps.handler(fakeK8s(t, exp))

	res := h.Handle(context.Background(), verdictMsg("ns", "proj", "uid-1", 7, 1, communication.VerdictMet, 0))
	if res.Status != communication.PPSEvaluationHandled {
		t.Fatalf("status = %q; want Handled", res.Status)
	}
	if !strings.Contains(res.Reason, "Finished") {
		t.Fatalf("reason = %q; want Finished transition", res.Reason)
	}
	if len(deps.ops) != 1 || deps.ops[0] != "finish" {
		t.Fatalf("ops = %v; want [finish]", deps.ops)
	}
}

func TestHandleAdditionalRunnersClaimsRound(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", "uid-1")
	deps := &fakeVerdictDeps{state: "PostProcessing", round: 1, found: true, claimNext: 2, claimOK: true}
	h := deps.handler(fakeK8s(t, exp))

	res := h.Handle(context.Background(), verdictMsg("ns", "proj", "uid-1", 7, 1, communication.VerdictAdditionalRunners, 45))
	if res.Status != communication.PPSEvaluationHandled {
		t.Fatalf("status = %q; want Handled", res.Status)
	}
	if !strings.Contains(res.Reason, "round 2") || !strings.Contains(res.Reason, "45 additional") {
		t.Fatalf("reason = %q; want round claim details", res.Reason)
	}
	if len(deps.ops) != 1 || deps.ops[0] != "claim-round" {
		t.Fatalf("ops = %v; want [claim-round]", deps.ops)
	}
}

func TestHandleStopUnmetFailsFromPostProcessing(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", "uid-1")
	deps := &fakeVerdictDeps{state: "PostProcessing", round: 1, found: true, failOK: true}
	h := deps.handler(fakeK8s(t, exp))

	res := h.Handle(context.Background(), verdictMsg("ns", "proj", "uid-1", 7, 1, communication.VerdictStopUnmet, 0))
	if res.Status != communication.PPSEvaluationHandled {
		t.Fatalf("status = %q; want Handled", res.Status)
	}
	if !strings.Contains(res.Reason, "Failed") {
		t.Fatalf("reason = %q; want Failed transition", res.Reason)
	}
	if len(deps.ops) != 1 || deps.ops[0] != "fail-from:PostProcessing" {
		t.Fatalf("ops = %v; want [fail-from:PostProcessing]", deps.ops)
	}
}

func TestHandleStaleStateIsHandledNoOp(t *testing.T) {
	// Each verdict applied to a row that already left PostProcessing is a
	// stale no-op: Handled, ACK, no further mutation recorded.
	exp := inProgressExperiment("ns", "proj", "uid-1")
	cases := []struct {
		name       string
		verdict    string
		additional int
		deps       *fakeVerdictDeps
	}{
		{"met stale", communication.VerdictMet, 0, &fakeVerdictDeps{state: "Finished", round: 1, found: true, finishOK: false}},
		{"additional_runners stale", communication.VerdictAdditionalRunners, 10, &fakeVerdictDeps{state: "StartingRunners", round: 2, found: true, claimOK: false}},
		{"stop_unmet stale", communication.VerdictStopUnmet, 0, &fakeVerdictDeps{state: "Failed", round: 1, found: true, failOK: false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := c.deps.handler(fakeK8s(t, exp))
			res := h.Handle(context.Background(), verdictMsg("ns", "proj", "uid-1", 7, c.deps.round, c.verdict, c.additional))
			if res.Status != communication.PPSEvaluationHandled {
				t.Fatalf("status = %q; want Handled (stale)", res.Status)
			}
			if !strings.Contains(res.Reason, "stale") {
				t.Fatalf("reason = %q; want stale", res.Reason)
			}
			// The stale transition guard matched no row, so no mutation.
			if c.deps.finishOK || c.deps.claimOK || c.deps.failOK {
				t.Fatal("stale case must not report a transition")
			}
		})
	}
}

func TestHandleRoundMismatchIsStale(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", "uid-1")
	deps := &fakeVerdictDeps{state: "PostProcessing", round: 2, found: true, finishOK: true}
	h := deps.handler(fakeK8s(t, exp))

	// The verdict is for round 1 but the scenario is already in round 2.
	res := h.Handle(context.Background(), verdictMsg("ns", "proj", "uid-1", 7, 1, communication.VerdictMet, 0))
	if res.Status != communication.PPSEvaluationHandled {
		t.Fatalf("status = %q; want Handled (stale round)", res.Status)
	}
	if !strings.Contains(res.Reason, "stale") {
		t.Fatalf("reason = %q; want stale round mismatch", res.Reason)
	}
	// The mismatch is decided before any transition: no mutation.
	if len(deps.ops) != 0 {
		t.Fatalf("ops = %v; want none (round mismatch is pre-transition)", deps.ops)
	}
}

func TestHandleScenarioGoneIsStale(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", "uid-1")
	deps := &fakeVerdictDeps{found: false}
	h := deps.handler(fakeK8s(t, exp))

	res := h.Handle(context.Background(), verdictMsg("ns", "proj", "uid-1", 7, 1, communication.VerdictMet, 0))
	if res.Status != communication.PPSEvaluationHandled {
		t.Fatalf("status = %q; want Handled (scenario gone)", res.Status)
	}
	if !strings.Contains(res.Reason, "stale") {
		t.Fatalf("reason = %q; want stale", res.Reason)
	}
	if len(deps.ops) != 0 {
		t.Fatalf("ops = %v; want none", deps.ops)
	}
}

func TestHandleExperimentUIDMismatchIsPoison(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", "uid-live")
	deps := &fakeVerdictDeps{state: "PostProcessing", round: 1, found: true, finishOK: true}
	h := deps.handler(fakeK8s(t, exp))

	res := h.Handle(context.Background(), verdictMsg("ns", "proj", "uid-other", 7, 1, communication.VerdictMet, 0))
	if res.Status != communication.PPSEvaluationHandled {
		t.Fatalf("status = %q; want Handled (UID mismatch poison)", res.Status)
	}
	if !strings.Contains(res.Reason, "poison") {
		t.Fatalf("reason = %q; want poison", res.Reason)
	}
	// The mismatch is decided before the round read or any transition.
	if deps.loadCalled || len(deps.ops) != 0 {
		t.Fatalf("loadCalled=%v ops=%v; UID mismatch must not read or mutate", deps.loadCalled, deps.ops)
	}
}

func TestHandleExperimentGoneIsPoison(t *testing.T) {
	// No experiment in the fake client.
	deps := &fakeVerdictDeps{state: "PostProcessing", round: 1, found: true, finishOK: true}
	h := deps.handler(fakeK8s(t))

	res := h.Handle(context.Background(), verdictMsg("ns", "missing", "uid-1", 7, 1, communication.VerdictMet, 0))
	if res.Status != communication.PPSEvaluationHandled {
		t.Fatalf("status = %q; want Handled (experiment gone poison)", res.Status)
	}
	if len(deps.ops) != 0 {
		t.Fatalf("ops = %v; want none", deps.ops)
	}
}

func TestHandleTransientDBErrorIsRetry(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", "uid-1")
	dbErr := errors.New("connection refused")
	for name, deps := range map[string]*fakeVerdictDeps{
		"load":   {state: "PostProcessing", round: 1, found: true, loadErr: dbErr},
		"finish": {state: "PostProcessing", round: 1, found: true, finishErr: dbErr},
		"claim":  {state: "PostProcessing", round: 1, found: true, claimErr: dbErr},
		"fail":   {state: "PostProcessing", round: 1, found: true, failErr: dbErr},
	} {
		t.Run(name, func(t *testing.T) {
			h := deps.handler(fakeK8s(t, exp))
			verdict := communication.VerdictMet
			switch name {
			case "claim":
				verdict = communication.VerdictAdditionalRunners
			case "fail":
				verdict = communication.VerdictStopUnmet
			}
			res := h.Handle(context.Background(), verdictMsg("ns", "proj", "uid-1", 7, 1, verdict, 10))
			if res.Status != communication.PPSEvaluationRetry {
				t.Fatalf("status = %q; want Retry", res.Status)
			}
		})
	}
}

func TestHandleTransientK8sErrorIsRetry(t *testing.T) {
	k8s := &errK8s{Client: fakeK8s(t, inProgressExperiment("ns", "proj", "uid-1")), err: errors.New("apiserver unavailable")}
	deps := &fakeVerdictDeps{state: "PostProcessing", round: 1, found: true, finishOK: true}
	h := deps.handler(k8s)

	res := h.Handle(context.Background(), verdictMsg("ns", "proj", "uid-1", 7, 1, communication.VerdictMet, 0))
	if res.Status != communication.PPSEvaluationRetry {
		t.Fatalf("status = %q; want Retry", res.Status)
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

func TestHandleUnknownVerdictIsPoison(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", "uid-1")
	deps := &fakeVerdictDeps{state: "PostProcessing", round: 1, found: true}
	h := deps.handler(fakeK8s(t, exp))

	res := h.Handle(context.Background(), verdictMsg("ns", "proj", "uid-1", 7, 1, "bogus", 0))
	if res.Status != communication.PPSEvaluationHandled {
		t.Fatalf("status = %q; want Handled (unknown verdict poison)", res.Status)
	}
	if len(deps.ops) != 0 {
		t.Fatalf("ops = %v; want none", deps.ops)
	}
}
