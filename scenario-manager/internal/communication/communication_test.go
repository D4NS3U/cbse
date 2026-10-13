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

package communication

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/D4NS3U/cbse/scenario-manager/internal/subject"
)

func TestScenarioBatchDecodesLegacyFixture(t *testing.T) {
	// Existing payload fixtures must continue to decode without namespace or
	// UID fields.
	raw := `{"batch_id":"b1","project":"smoke","scenarios":[{"priority":1,"number_of_reps":10,"recipe_info":{"k":"v"}}]}`
	var batch ScenarioBatch
	if err := json.Unmarshal([]byte(raw), &batch); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if batch.Project != "smoke" || len(batch.Scenarios) != 1 || batch.Scenarios[0].NumberOfReps != 10 {
		t.Fatalf("decoded batch = %+v", batch)
	}
	// Non-JSON identity fields are zero after decode.
	if batch.ProjectNamespace != "" || batch.ProjectName != "" {
		t.Fatalf("non-JSON identity fields populated from JSON: %+v", batch)
	}
}

func TestValidateBatchIdentity(t *testing.T) {
	id := subject.Identity{Namespace: "default", Project: "smoke"}
	if err := ValidateBatchIdentity(ScenarioBatch{Project: "smoke"}, id); err != nil {
		t.Fatalf("matching: %v", err)
	}
	if err := ValidateBatchIdentity(ScenarioBatch{Project: ""}, id); !errors.Is(err, ErrBatchValidation) {
		t.Fatalf("empty project: %v", err)
	}
	if err := ValidateBatchIdentity(ScenarioBatch{Project: "other"}, id); !errors.Is(err, ErrBatchValidation) {
		t.Fatalf("mismatch: %v", err)
	}
}

func TestValidateBatchReps(t *testing.T) {
	valid := []ScenarioRecord{{NumberOfReps: 1}, {NumberOfReps: 100000}}
	if err := ValidateBatchReps(valid); err != nil {
		t.Fatalf("valid: %v", err)
	}
	invalid := []struct {
		recs []ScenarioRecord
		ok   bool
	}{
		{recs: []ScenarioRecord{{NumberOfReps: 0}}, ok: false},
		{recs: []ScenarioRecord{{NumberOfReps: 100001}}, ok: false},
		{recs: []ScenarioRecord{{NumberOfReps: 1}, {NumberOfReps: -1}}, ok: false},
		{recs: []ScenarioRecord{}, ok: true},
	}
	for i, c := range invalid {
		err := ValidateBatchReps(c.recs)
		if c.ok && err != nil {
			t.Errorf("case %d: unexpected error %v", i, err)
		}
		if !c.ok && !errors.Is(err, ErrBatchValidation) {
			t.Errorf("case %d: err = %v; want ErrBatchValidation", i, err)
		}
	}
}

// goldenEvaluationRequest is the normative evaluation-request wire payload
// from the S2 contract, used for field-for-field golden tests.
const goldenEvaluationRequest = `{"experiment_uid":"11111111-2222-3333-4444-555555555555","namespace":"default","project":"smoke-project","scenario_id":42,"runner_round":1,"number_of_reps":40,"confidence_metric":0.5}`

// goldenEvaluationVerdict is the normative evaluation-verdict wire payload
// from the S2 contract, used for field-for-field golden tests.
const goldenEvaluationVerdict = `{"experiment_uid":"11111111-2222-3333-4444-555555555555","namespace":"default","project":"smoke-project","scenario_id":42,"runner_round":1,"metric":"mean_wait_time","verdict":"additional_runners","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"additional_runners":45,"max_replications":10000}`

func TestScenarioEvaluationRequestGoldenWire(t *testing.T) {
	var req ScenarioEvaluationRequest
	if err := json.Unmarshal([]byte(goldenEvaluationRequest), &req); err != nil {
		t.Fatalf("decode golden request: %v", err)
	}
	// Field-for-field assertion against the normative contract.
	if req.ExperimentUID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("experiment_uid = %q", req.ExperimentUID)
	}
	if req.Namespace != "default" || req.Project != "smoke-project" {
		t.Errorf("identity = %q/%q", req.Namespace, req.Project)
	}
	if req.ScenarioID != 42 || req.RunnerRound != 1 || req.NumberOfReps != 40 {
		t.Errorf("counts = id=%d round=%d reps=%d", req.ScenarioID, req.RunnerRound, req.NumberOfReps)
	}
	if req.ConfidenceMetric != 0.5 {
		t.Errorf("confidence_metric = %v", req.ConfidenceMetric)
	}
	// Re-marshal and compare field-for-field against the golden document.
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got, want map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal re-marshaled: %v", err)
	}
	if err := json.Unmarshal([]byte(goldenEvaluationRequest), &want); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("field count = %d; want %d", len(got), len(want))
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || !jsonEqual(g, w) {
			t.Errorf("field %q: got %s; want %s", k, g, w)
		}
	}
}

func TestPPSEvaluationVerdictGoldenWire(t *testing.T) {
	var verdict PPSEvaluationVerdict
	if err := json.Unmarshal([]byte(goldenEvaluationVerdict), &verdict); err != nil {
		t.Fatalf("decode golden verdict: %v", err)
	}
	// Field-for-field assertion against the normative contract.
	if verdict.ExperimentUID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("experiment_uid = %q", verdict.ExperimentUID)
	}
	if verdict.Namespace != "default" || verdict.Project != "smoke-project" {
		t.Errorf("identity = %q/%q", verdict.Namespace, verdict.Project)
	}
	if verdict.ScenarioID != 42 || verdict.RunnerRound != 1 {
		t.Errorf("id/round = %d/%d", verdict.ScenarioID, verdict.RunnerRound)
	}
	if verdict.Metric != "mean_wait_time" {
		t.Errorf("metric = %q", verdict.Metric)
	}
	if verdict.Verdict != VerdictAdditionalRunners {
		t.Errorf("verdict = %q", verdict.Verdict)
	}
	if verdict.SampleMean != 10.1 || verdict.HalfWidth != 0.9 {
		t.Errorf("stats = mean %v half-width %v", verdict.SampleMean, verdict.HalfWidth)
	}
	if verdict.Replications != 40 || verdict.ConfidenceMetric != 0.5 {
		t.Errorf("reps/conf = %d/%v", verdict.Replications, verdict.ConfidenceMetric)
	}
	if verdict.AdditionalRunners != 45 || verdict.MaxReplications != 10000 {
		t.Errorf("runners = %d/%d", verdict.AdditionalRunners, verdict.MaxReplications)
	}
	// Re-marshal and compare field-for-field against the golden document.
	out, err := json.Marshal(verdict)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got, want map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal re-marshaled: %v", err)
	}
	if err := json.Unmarshal([]byte(goldenEvaluationVerdict), &want); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("field count = %d; want %d", len(got), len(want))
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || !jsonEqual(g, w) {
			t.Errorf("field %q: got %s; want %s", k, g, w)
		}
	}
}

func TestScenarioEvaluationRequestPoison(t *testing.T) {
	bad := []string{
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"number_of_reps":40,"confidence_metric":0.5,"extra":1}`, // unknown field
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"number_of_reps":40}`,                                   // missing confidence_metric
		`{"namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"number_of_reps":40,"confidence_metric":0.5}`,                                // missing experiment_uid
		`{"experiment_uid":"","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"number_of_reps":40,"confidence_metric":0.5}`,            // empty experiment_uid
		`{"experiment_uid":"u","namespace":"","project":"p","scenario_id":42,"runner_round":1,"number_of_reps":40,"confidence_metric":0.5}`,             // empty namespace
		`{"experiment_uid":"u","namespace":"ns","project":"","scenario_id":42,"runner_round":1,"number_of_reps":40,"confidence_metric":0.5}`,            // empty project
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":0,"runner_round":1,"number_of_reps":40,"confidence_metric":0.5}`,            // non-positive scenario_id
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":0,"number_of_reps":40,"confidence_metric":0.5}`,           // zero runner_round
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"number_of_reps":0,"confidence_metric":0.5}`,            // zero number_of_reps
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"number_of_reps":40,"confidence_metric":0}`,             // zero confidence_metric
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"number_of_reps":40,"confidence_metric":-1}`,            // negative confidence_metric
		`[1,2,3]`, // not an object
	}
	for i, raw := range bad {
		var req ScenarioEvaluationRequest
		err := json.Unmarshal([]byte(raw), &req)
		if err == nil {
			t.Errorf("case %d: decode %s succeeded; want error", i, raw)
			continue
		}
		if !errors.Is(err, ErrEvaluationWire) {
			t.Errorf("case %d: err = %v; want ErrEvaluationWire", i, err)
		}
	}
	// Empty input is rejected by the standard decoder before UnmarshalJSON
	// runs; the transport's strict decode guards against it upstream, so any
	// rejection is sufficient here.
	var req ScenarioEvaluationRequest
	if err := json.Unmarshal(nil, &req); err == nil {
		t.Error("empty input: decode succeeded; want error")
	}
}

func TestPPSEvaluationVerdictPoison(t *testing.T) {
	metOK := `{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"metric":"mean_wait_time","verdict":"met","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"additional_runners":0,"max_replications":10000}`
	var ok PPSEvaluationVerdict
	if err := json.Unmarshal([]byte(metOK), &ok); err != nil {
		t.Fatalf("valid met verdict rejected: %v", err)
	}
	if ok.Verdict != VerdictMet || ok.AdditionalRunners != 0 {
		t.Fatalf("decoded met verdict = %+v", ok)
	}

	bad := []string{
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"metric":"mean_wait_time","verdict":"met","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"additional_runners":0,"max_replications":10000,"extra":1}`,      // unknown field
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"metric":"mean_wait_time","verdict":"met","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"max_replications":10000}`,                                       // missing additional_runners
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"metric":"mean_wait_time","verdict":"met","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"additional_runners":0}`,                                         // missing max_replications
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"metric":"mean_wait_time","verdict":"met","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"additional_runners":1,"max_replications":10000}`,                // additional_runners nonzero with met
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"metric":"mean_wait_time","verdict":"additional_runners","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"additional_runners":0,"max_replications":10000}`, // additional_runners zero with additional_runners verdict
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"metric":"mean_wait_time","verdict":"stop_unmet","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"additional_runners":5,"max_replications":10000}`,         // additional_runners nonzero with stop_unmet
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"metric":"mean_wait_time","verdict":"unknown","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"additional_runners":0,"max_replications":10000}`,            // unknown verdict enum
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"metric":"","verdict":"met","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"additional_runners":0,"max_replications":10000}`,                              // empty metric
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":0,"runner_round":1,"metric":"m","verdict":"met","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"additional_runners":0,"max_replications":10000}`,                              // zero scenario_id
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":0,"metric":"m","verdict":"met","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"additional_runners":0,"max_replications":10000}`,                             // zero runner_round
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"metric":"m","verdict":"met","sample_mean":10.1,"half_width":0.9,"replications":-1,"confidence_metric":0.5,"additional_runners":0,"max_replications":10000}`,                             // negative replications
		`{"experiment_uid":"u","namespace":"ns","project":"p","scenario_id":42,"runner_round":1,"metric":"m","verdict":"met","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0,"additional_runners":0,"max_replications":10000}`,                               // zero confidence_metric
	}
	for i, raw := range bad {
		var verdict PPSEvaluationVerdict
		err := json.Unmarshal([]byte(raw), &verdict)
		if err == nil {
			t.Errorf("case %d: decode succeeded; want error", i)
			continue
		}
		if !errors.Is(err, ErrEvaluationWire) {
			t.Errorf("case %d: err = %v; want ErrEvaluationWire", i, err)
		}
	}
}

// jsonEqual reports whether two raw JSON values are byte-identical after
// trimming; the golden tests re-marshal decoded values, so key order and number
// formatting are deterministic for the same decoded content.
func jsonEqual(a, b json.RawMessage) bool {
	return string(bytes.TrimSpace(a)) == string(bytes.TrimSpace(b))
}

// ---- the SBO wire pin (FEATURE.md section 5: byte-exact marshaled bytes) ----
//
// These goldens are the Scenario Manager side of the manager's byte-comparison
// against the reference EDS wire goldens: the four SBO payloads must marshal
// to exactly the pinned canonical JSON, including the omitempty behavior
// (experiment_type absent when undeclared; final absent on a non-final batch).

// assertExactWire marshals v and requires byte-identity with want.
func assertExactWire(t *testing.T, v interface{}, want string) {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(out, []byte(want)) {
		t.Fatalf("marshaled bytes = %s; want exactly %s", out, want)
	}
}

func TestEDSAvailabilityRequestGoldenWireDeclaredStandard(t *testing.T) {
	req := EDSAvailabilityRequest{
		BatchID:        "b1",
		Project:        "smoke-project",
		ScenarioCount:  2,
		ExperimentType: "standard",
	}
	assertExactWire(t, req, `{"batch_id":"b1","project":"smoke-project","scenario_count":2,"experiment_type":"standard"}`)

	// Decode round-trip: the declared type must survive.
	var decoded EDSAvailabilityRequest
	if err := json.Unmarshal([]byte(`{"batch_id":"b1","project":"smoke-project","scenario_count":2,"experiment_type":"standard"}`), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.ExperimentType != "standard" {
		t.Fatalf("decoded experiment_type = %q; want standard", decoded.ExperimentType)
	}
}

func TestEDSAvailabilityRequestGoldenWireLegacyUndeclared(t *testing.T) {
	// No declaration: experiment_type is absent on the wire (the legacy state).
	req := EDSAvailabilityRequest{BatchID: "b1", Project: "smoke-project", ScenarioCount: 2}
	assertExactWire(t, req, `{"batch_id":"b1","project":"smoke-project","scenario_count":2}`)

	// A payload without experiment_type decodes to the empty declaration.
	var decoded EDSAvailabilityRequest
	if err := json.Unmarshal([]byte(`{"batch_id":"b1","project":"smoke-project","scenario_count":2}`), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.ExperimentType != "" {
		t.Fatalf("decoded experiment_type = %q; want empty (absent on the wire)", decoded.ExperimentType)
	}
}

func TestEDSAvailabilityReplyGoldenWireEchoOnReadyAndError(t *testing.T) {
	ready := EDSAvailabilityReply{
		Status:         AvailabilityStatusReady,
		BatchSubject:   "cbse.ns.smoke-project.eds.scenarios",
		ExperimentType: "standard",
	}
	assertExactWire(t, ready, `{"status":"ready","batch_subject":"cbse.ns.smoke-project.eds.scenarios","experiment_type":"standard"}`)

	errorEcho := EDSAvailabilityReply{
		Status:         AvailabilityStatusError,
		Reason:         "experiment pending",
		ExperimentType: "standard",
	}
	// The echo is status-independent: the error reply carries it verbatim.
	assertExactWire(t, errorEcho, `{"status":"error","reason":"experiment pending","experiment_type":"standard"}`)

	// No declaration means no echo: the legacy error reply stays byte-identical.
	legacyError := EDSAvailabilityReply{Status: AvailabilityStatusError, Reason: "experiment pending"}
	assertExactWire(t, legacyError, `{"status":"error","reason":"experiment pending"}`)

	legacyReady := EDSAvailabilityReply{Status: AvailabilityStatusReady, BatchSubject: "cbse.ns.smoke-project.eds.scenarios"}
	assertExactWire(t, legacyReady, `{"status":"ready","batch_subject":"cbse.ns.smoke-project.eds.scenarios"}`)
}

func TestScenarioBatchGoldenWireNonFinalHasNoFinalField(t *testing.T) {
	batch := ScenarioBatch{
		BatchID: "b1",
		Project: "smoke-project",
		Scenarios: []ScenarioRecord{{
			Priority:         1,
			NumberOfReps:     10,
			RecipeInfo:       json.RawMessage(`{"parameterset_id":1}`),
			ConfidenceMetric: func() *float64 { f := 0.91; return &f }(),
		}},
	}
	// final absent or false is not final: the marshaled form has no final
	// field at all.
	assertExactWire(t, batch, `{"batch_id":"b1","project":"smoke-project","scenarios":[{"priority":1,"number_of_reps":10,"recipe_info":{"parameterset_id":1},"confidence_metric":0.91}]}`)
}

func TestScenarioBatchGoldenWireFinalizationBatch(t *testing.T) {
	// The finalization batch: the empty scenario array is present, final is
	// true.
	batch := ScenarioBatch{BatchID: "fin", Project: "smoke-project", Scenarios: []ScenarioRecord{}, Final: true}
	assertExactWire(t, batch, `{"batch_id":"fin","project":"smoke-project","scenarios":[],"final":true}`)

	// A final batch with scenarios carries them plus final.
	withScenarios := ScenarioBatch{
		BatchID:   "fin2",
		Project:   "smoke-project",
		Scenarios: []ScenarioRecord{{Priority: 1, NumberOfReps: 2, RecipeInfo: json.RawMessage(`{"parameterset_id":5}`)}},
		Final:     true,
	}
	assertExactWire(t, withScenarios, `{"batch_id":"fin2","project":"smoke-project","scenarios":[{"priority":1,"number_of_reps":2,"recipe_info":{"parameterset_id":5}}],"final":true}`)

	// Decode round-trip of the canonical finalization batch.
	var decoded ScenarioBatch
	if err := json.Unmarshal([]byte(`{"batch_id":"fin","project":"smoke-project","scenarios":[],"final":true}`), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoded.Final || len(decoded.Scenarios) != 0 {
		t.Fatalf("decoded finalization batch = %+v; want final with zero scenarios", decoded)
	}
}

func TestScenarioBatchAckGoldenWireUnchanged(t *testing.T) {
	// The ack shape is unchanged by the finalization protocol.
	inserted := ScenarioBatchAck{Status: BatchAckStatusInserted, BatchID: "b1", Received: 2, Inserted: 2, Failed: 0}
	assertExactWire(t, inserted, `{"status":"inserted","batch_id":"b1","received":2,"inserted":2,"failed":0}`)

	// The pinned post-final rejection: status error, the batch id echoed,
	// nothing inserted, the pinned reason byte-exact.
	rejected := ScenarioBatchAck{Status: BatchAckStatusError, BatchID: "b2", Received: 1, Inserted: 0, Failed: 0, Reason: ReasonExperimentFinalized}
	assertExactWire(t, rejected, `{"status":"error","batch_id":"b2","received":1,"inserted":0,"failed":0,"reason":"experiment finalized; no further batches accepted"}`)
}
