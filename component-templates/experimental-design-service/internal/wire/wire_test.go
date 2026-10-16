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

package wire

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// testIdentity is the process identity the golden payloads reference.
var testIdentity = Identity{
	ExperimentUID: "aabbccdd-1122-3344-5566-77889900aabb",
	Namespace:     "ns",
	Project:       "exp",
}

// TestAvailabilityRequestGolden is the golden-JSON wire test for the
// availability request with the declared experiment type: the canonical
// payload decodes field-for-field and the decoded struct re-marshals
// byte-for-byte to the same JSON.
func TestAvailabilityRequestGolden(t *testing.T) {
	const golden = `{"batch_id":"eds-exp-1","project":"exp","scenario_count":2,"experiment_type":"standard"}`
	dec := json.NewDecoder(strings.NewReader(golden))
	dec.DisallowUnknownFields()
	var req AvailabilityRequest
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	if req.BatchID != "eds-exp-1" || req.Project != "exp" || req.ScenarioCount != 2 || req.ExperimentType != "standard" {
		t.Fatalf("fields = %+v", req)
	}
	if err := ValidateAvailabilityRequest(&req, testIdentity); err != nil {
		t.Fatalf("golden request fails validation: %v", err)
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != golden {
		t.Fatalf("re-marshal mismatch:\n got  %s\n want %s", data, golden)
	}
}

// TestAvailabilityReplyGolden is the golden-JSON wire test for the ready
// availability reply with the verbatim experiment_type echo, and for the
// error reply (reason present, no batch_subject).
func TestAvailabilityReplyGolden(t *testing.T) {
	ready, err := DecodeAvailabilityReply([]byte(`{"status":"ready","batch_subject":"cbse.ns.exp.eds.scenarios","experiment_type":"standard"}`))
	if err != nil {
		t.Fatalf("decode ready reply: %v", err)
	}
	subject, reason, ok := ReplyError(ready)
	if !ok || reason != "" || subject != "cbse.ns.exp.eds.scenarios" {
		t.Fatalf("ready reply classification = subject %q reason %q ok %v", subject, reason, ok)
	}

	// The error reply carries the reason and no batch subject.
	errReply, err := DecodeAvailabilityReply([]byte(`{"status":"error","reason":"experiment Pending"}`))
	if err != nil {
		t.Fatalf("decode error reply: %v", err)
	}
	if _, reason, ok := ReplyError(errReply); ok || reason != "experiment Pending" {
		t.Fatalf("error reply classification = reason %q ok %v", reason, ok)
	}
}

// TestScenarioBatchGoldenNonFinal is the golden-JSON wire test for the
// non-final scenario batch: the canonical payload decodes field-for-field,
// re-marshals byte-for-byte, and marshals WITHOUT the final field
// (the omitempty behavior of the contract).
func TestScenarioBatchGoldenNonFinal(t *testing.T) {
	const golden = `{"batch_id":"eds-exp-1","project":"exp","scenarios":[{"priority":1,"number_of_reps":20,"recipe_info":{"parameterset_id":5},"confidence_metric":0.91},{"priority":2,"number_of_reps":30,"recipe_info":{"parameterset_id":6},"confidence_metric":0.5}]}`
	batch, err := DecodeScenarioBatch([]byte(golden))
	if err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	if batch.BatchID != "eds-exp-1" || batch.Project != "exp" || len(batch.Scenarios) != 2 || batch.Final {
		t.Fatalf("fields = %+v", batch)
	}
	if err := ValidateBatch(batch, testIdentity); err != nil {
		t.Fatalf("golden batch fails validation: %v", err)
	}
	data, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != golden {
		t.Fatalf("re-marshal mismatch:\n got  %s\n want %s", data, golden)
	}
	if strings.Contains(string(data), "final") {
		t.Fatalf("non-final batch must marshal without the final field: %s", data)
	}
}

// TestFinalizationBatchGolden is the golden-JSON wire test for the
// finalization batch: the empty scenarios array is present on the wire and
// final is true, byte-for-byte.
func TestFinalizationBatchGolden(t *testing.T) {
	batch := ScenarioBatch{
		BatchID:   "eds-exp-2",
		Project:   "exp",
		Scenarios: []ScenarioRecord{},
		Final:     true,
	}
	if err := ValidateBatch(&batch, testIdentity); err != nil {
		t.Fatalf("golden finalization batch fails validation: %v", err)
	}
	data, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const golden = `{"batch_id":"eds-exp-2","project":"exp","scenarios":[],"final":true}`
	if string(data) != golden {
		t.Fatalf("marshal mismatch:\n got  %s\n want %s", data, golden)
	}
	round, err := DecodeScenarioBatch(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if round.Final != true || len(round.Scenarios) != 0 || round.Scenarios == nil {
		t.Fatalf("finalization fields = %+v (scenarios must be the present empty array)", round)
	}
}

// TestScenarioBatchAckGolden is the golden-JSON wire test for the batch ack:
// the canonical payload decodes field-for-field and re-marshals
// byte-for-byte.
func TestScenarioBatchAckGolden(t *testing.T) {
	const golden = `{"status":"inserted","batch_id":"eds-exp-1","received":2,"inserted":2,"failed":0}`
	ack, err := DecodeBatchAck([]byte(golden))
	if err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	if ack.Status != "inserted" || ack.BatchID != "eds-exp-1" || ack.Received != 2 || ack.Inserted != 2 || ack.Failed != 0 {
		t.Fatalf("fields = %+v", ack)
	}
	data, err := json.Marshal(ack)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != golden {
		t.Fatalf("re-marshal mismatch:\n got  %s\n want %s", data, golden)
	}

	// A poison ack carries the reason and re-marshals with it.
	const poisonGolden = `{"status":"poison","batch_id":"eds-exp-1","received":1,"inserted":0,"failed":1,"reason":"number_of_reps out of range"}`
	poison, err := DecodeBatchAck([]byte(poisonGolden))
	if err != nil {
		t.Fatalf("decode poison golden: %v", err)
	}
	if poison.Reason != "number_of_reps out of range" {
		t.Fatalf("reason = %q", poison.Reason)
	}
	poisonData, err := json.Marshal(poison)
	if err != nil {
		t.Fatalf("marshal poison: %v", err)
	}
	if string(poisonData) != poisonGolden {
		t.Fatalf("poison re-marshal mismatch:\n got  %s\n want %s", poisonData, poisonGolden)
	}
}

// TestReplyEchoMismatchHardError is the hard-error test for the verbatim
// experiment_type echo: a missing or mismatched echo on an otherwise ready
// reply is a clean, UI-visible hard protocol error (no retry).
func TestReplyEchoMismatchHardError(t *testing.T) {
	cases := map[string][]byte{
		"missing echo":    []byte(`{"status":"ready","batch_subject":"cbse.ns.exp.eds.scenarios"}`),
		"mismatched echo": []byte(`{"status":"ready","batch_subject":"cbse.ns.exp.eds.scenarios","experiment_type":"optimization"}`),
	}
	for name, payload := range cases {
		reply, err := DecodeAvailabilityReply(payload)
		if err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		if _, reason, ok := ReplyError(reply); ok {
			t.Fatalf("%s: echo mismatch must be a hard error, got ok", name)
		} else if reason == "" {
			t.Fatalf("%s: hard error must carry a UI-visible reason", name)
		}
	}
}

// TestReplyErrorHardError covers the remaining hard refusals: status "error"
// surfaces the SM's reason; a ready reply without batch_subject is a hard
// error; an unknown status is a hard error.
func TestReplyErrorHardError(t *testing.T) {
	readyNoSubject, err := DecodeAvailabilityReply([]byte(`{"status":"ready","experiment_type":"standard"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, reason, ok := ReplyError(readyNoSubject); ok || !strings.Contains(reason, "batch_subject") {
		t.Fatalf("ready without batch_subject must hard-fail: reason %q ok %v", reason, ok)
	}
	weird, err := DecodeAvailabilityReply([]byte(`{"status":"maybe","experiment_type":"standard"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, _, ok := ReplyError(weird); ok {
		t.Fatalf("unknown status must hard-fail")
	}
	if _, _, ok := ReplyError(nil); ok {
		t.Fatalf("nil reply must hard-fail")
	}
}

// TestBatchIdentityMismatchPoison is the poison test: a batch whose project
// does not equal the identity project is refused before publication with a
// descriptive error.
func TestBatchIdentityMismatchPoison(t *testing.T) {
	batch := ScenarioBatch{BatchID: "b1", Project: "other-project", Scenarios: []ScenarioRecord{{
		Priority: 1, NumberOfReps: 10, RecipeInfo: RecipeInfo{ParametersetID: 5}, ConfidenceMetric: 0.9,
	}}}
	err := ValidateBatch(&batch, testIdentity)
	if err == nil {
		t.Fatalf("mismatched project must be poison")
	}
	if !strings.Contains(err.Error(), "poison") {
		t.Fatalf("poison error must be descriptive: %v", err)
	}
}

// TestScenarioRecordValidation covers the per-scenario domain contract:
// priority, reps range, parameterset_id, and the finite-positive metric.
func TestScenarioRecordValidation(t *testing.T) {
	valid := ScenarioRecord{Priority: 1, NumberOfReps: 10, RecipeInfo: RecipeInfo{ParametersetID: 5}, ConfidenceMetric: 0.9}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid record fails: %v", err)
	}
	cases := []struct {
		name   string
		record ScenarioRecord
	}{
		{"zero priority", ScenarioRecord{Priority: 0, NumberOfReps: 10, RecipeInfo: RecipeInfo{ParametersetID: 5}, ConfidenceMetric: 0.9}},
		{"negative priority", ScenarioRecord{Priority: -1, NumberOfReps: 10, RecipeInfo: RecipeInfo{ParametersetID: 5}, ConfidenceMetric: 0.9}},
		{"reps below min", ScenarioRecord{Priority: 1, NumberOfReps: 0, RecipeInfo: RecipeInfo{ParametersetID: 5}, ConfidenceMetric: 0.9}},
		{"reps above max", ScenarioRecord{Priority: 1, NumberOfReps: MaxReps + 1, RecipeInfo: RecipeInfo{ParametersetID: 5}, ConfidenceMetric: 0.9}},
		{"zero parameterset", ScenarioRecord{Priority: 1, NumberOfReps: 10, RecipeInfo: RecipeInfo{ParametersetID: 0}, ConfidenceMetric: 0.9}},
		{"zero metric", ScenarioRecord{Priority: 1, NumberOfReps: 10, RecipeInfo: RecipeInfo{ParametersetID: 5}, ConfidenceMetric: 0}},
		{"nan metric", ScenarioRecord{Priority: 1, NumberOfReps: 10, RecipeInfo: RecipeInfo{ParametersetID: 5}, ConfidenceMetric: math.NaN()}},
		{"inf metric", ScenarioRecord{Priority: 1, NumberOfReps: 10, RecipeInfo: RecipeInfo{ParametersetID: 5}, ConfidenceMetric: math.Inf(1)}},
	}
	for _, tc := range cases {
		if err := tc.record.Validate(); err == nil {
			t.Fatalf("%s: must fail", tc.name)
		}
	}
}

// TestStrictDecodeRejections verifies that unknown fields and trailing
// tokens are rejected on every consumed payload type.
func TestStrictDecodeRejections(t *testing.T) {
	unknown := []byte(`{"status":"ready","batch_subject":"cbse.ns.exp.eds.scenarios","experiment_type":"standard","bogus":1}`)
	if _, err := DecodeAvailabilityReply(unknown); err == nil {
		t.Fatalf("unknown field must be rejected on the reply")
	}
	unknownBatch := []byte(`{"batch_id":"b1","project":"exp","scenarios":[],"surprise":true}`)
	if _, err := DecodeScenarioBatch(unknownBatch); err == nil {
		t.Fatalf("unknown field must be rejected on the batch")
	}
	unknownAck := []byte(`{"status":"inserted","bogus":1}`)
	if _, err := DecodeBatchAck(unknownAck); err == nil {
		t.Fatalf("unknown field must be rejected on the ack")
	}
	trailing := []byte(`{"status":"inserted"} {}`)
	if _, err := DecodeBatchAck(trailing); err == nil {
		t.Fatalf("trailing token must be rejected on the ack")
	}
	badStatus := []byte(`{"status":"wat"}`)
	if _, err := DecodeBatchAck(badStatus); err == nil {
		t.Fatalf("non-enum ack status must be rejected")
	}
}
