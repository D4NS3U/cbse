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
	"testing"
)

// testIdentity is the pod identity the golden payloads reference.
var testIdentity = Identity{
	ExperimentUID: "aabbccdd-1122-3344-5566-77889900aabb",
	Namespace:     "default",
	Project:       "proj",
}

// TestDecodeRequestGolden is the golden-JSON wire test for the evaluation
// request: the normative contract example decodes field-for-field, and the
// decoded struct re-marshals byte-for-byte to the same JSON.
func TestDecodeRequestGolden(t *testing.T) {
	const golden = `{"experiment_uid":"aabbccdd-1122-3344-5566-77889900aabb","namespace":"default","project":"proj","scenario_id":42,"runner_round":1,"number_of_reps":40,"confidence_metric":0.5}`
	req, err := DecodeRequest([]byte(golden))
	if err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	if req.ExperimentUID != testIdentity.ExperimentUID || req.Namespace != testIdentity.Namespace || req.Project != testIdentity.Project {
		t.Fatalf("identity = %q/%q/%q", req.ExperimentUID, req.Namespace, req.Project)
	}
	if req.ScenarioID != 42 || req.RunnerRound != 1 || req.NumberOfReps != 40 {
		t.Fatalf("counts = %d/%d/%d", req.ScenarioID, req.RunnerRound, req.NumberOfReps)
	}
	if req.ConfidenceMetric != 0.5 {
		t.Fatalf("confidence = %v", req.ConfidenceMetric)
	}
	if err := req.ValidateRequest(testIdentity); err != nil {
		t.Fatalf("golden request fails validation: %v", err)
	}
}

// TestEncodeRequestGolden re-marshals the decoded golden request and compares
// the bytes to the normative contract example.
func TestEncodeRequestGolden(t *testing.T) {
	const golden = `{"experiment_uid":"aabbccdd-1122-3344-5566-77889900aabb","namespace":"default","project":"proj","scenario_id":42,"runner_round":1,"number_of_reps":40,"confidence_metric":0.5}`
	req, err := DecodeRequest([]byte(golden))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(data) != golden {
		t.Fatalf("re-marshal = %s, want %s", data, golden)
	}
}

// TestDecodeVerdictGolden is the golden-JSON wire test for the evaluation
// verdict: the normative contract example decodes field-for-field, and
// EncodeVerdict re-marshals it byte-for-byte.
func TestDecodeVerdictGolden(t *testing.T) {
	const golden = `{"experiment_uid":"aabbccdd-1122-3344-5566-77889900aabb","namespace":"default","project":"proj","scenario_id":42,"runner_round":1,"metric":"mean_wait_time","verdict":"additional_runners","sample_mean":10.1,"half_width":0.9,"replications":40,"confidence_metric":0.5,"additional_runners":45,"max_replications":10000}`
	v, err := DecodeVerdict([]byte(golden))
	if err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	if v.ExperimentUID != testIdentity.ExperimentUID || v.Namespace != testIdentity.Namespace || v.Project != testIdentity.Project {
		t.Fatalf("identity = %q/%q/%q", v.ExperimentUID, v.Namespace, v.Project)
	}
	if v.ScenarioID != 42 || v.RunnerRound != 1 {
		t.Fatalf("scenario/round = %d/%d", v.ScenarioID, v.RunnerRound)
	}
	if v.Metric != MetricMeanWaitTime || v.Verdict != VerdictAdditionalRunners {
		t.Fatalf("metric/verdict = %q/%q", v.Metric, v.Verdict)
	}
	if v.SampleMean != 10.1 || v.HalfWidth != 0.9 || v.Replications != 40 {
		t.Fatalf("stats = %v/%v/%d", v.SampleMean, v.HalfWidth, v.Replications)
	}
	if v.ConfidenceMetric != 0.5 || v.AdditionalRunners != 45 || v.MaxReplications != 10000 {
		t.Fatalf("bounds = %v/%d/%d", v.ConfidenceMetric, v.AdditionalRunners, v.MaxReplications)
	}
	data, err := EncodeVerdict(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(data) != golden {
		t.Fatalf("re-marshal = %s, want %s", data, golden)
	}
}

// TestValidateRequestPoison covers every permanent-poison case: wrong
// identity, malformed identity fields, non-positive ids, non-positive round
// and reps, and non-finite or non-positive confidence_metric.
func TestValidateRequestPoison(t *testing.T) {
	valid := func() *Request {
		return &Request{
			ExperimentUID:    testIdentity.ExperimentUID,
			Namespace:        testIdentity.Namespace,
			Project:          testIdentity.Project,
			ScenarioID:       42,
			RunnerRound:      1,
			NumberOfReps:     40,
			ConfidenceMetric: 0.5,
		}
	}
	cases := []struct {
		name string
		mut  func(*Request)
	}{
		{"wrong uid", func(r *Request) { r.ExperimentUID = "00000000-1111-2222-3333-444444444444" }},
		{"malformed uid", func(r *Request) { r.ExperimentUID = "AABBCCDD-1122-3344-5566-77889900AABB" }},
		{"empty uid", func(r *Request) { r.ExperimentUID = "" }},
		{"wrong namespace", func(r *Request) { r.Namespace = "other" }},
		{"uppercase namespace", func(r *Request) { r.Namespace = "Default" }},
		{"empty namespace", func(r *Request) { r.Namespace = "" }},
		{"wrong project", func(r *Request) { r.Project = "other" }},
		{"empty project", func(r *Request) { r.Project = "" }},
		{"zero scenario id", func(r *Request) { r.ScenarioID = 0 }},
		{"negative scenario id", func(r *Request) { r.ScenarioID = -42 }},
		{"zero runner round", func(r *Request) { r.RunnerRound = 0 }},
		{"zero number of reps", func(r *Request) { r.NumberOfReps = 0 }},
		{"negative number of reps", func(r *Request) { r.NumberOfReps = -1 }},
		{"zero confidence metric", func(r *Request) { r.ConfidenceMetric = 0 }},
		{"negative confidence metric", func(r *Request) { r.ConfidenceMetric = -0.5 }},
		{"NaN confidence metric", func(r *Request) { r.ConfidenceMetric = math.NaN() }},
		{"+Inf confidence metric", func(r *Request) { r.ConfidenceMetric = math.Inf(1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := valid()
			tc.mut(r)
			if err := r.ValidateRequest(testIdentity); err == nil {
				t.Fatalf("%s: poison request accepted", tc.name)
			}
		})
	}
	if err := (*Request)(nil).ValidateRequest(testIdentity); err == nil {
		t.Fatal("nil request accepted")
	}
}

// TestDecodeRequestRejects covers strict-decode poison: unknown fields,
// trailing content, and malformed JSON.
func TestDecodeRequestRejects(t *testing.T) {
	valid := `{"experiment_uid":"aabbccdd-1122-3344-5566-77889900aabb","namespace":"default","project":"proj","scenario_id":42,"runner_round":1,"number_of_reps":40,"confidence_metric":0.5}`
	for _, data := range []string{
		valid + ` extra`,
		valid + `{"scenario_id":1}`,
		valid[:100] + `,"bogus_field":1}`,
		`[1,2,3]`,
		`42`,
		`"request"`,
		`{`,
		``,
	} {
		if _, err := DecodeRequest([]byte(data)); err == nil {
			t.Fatalf("malformed %q accepted", data)
		}
	}
}

// TestEncodeVerdictPoison covers every verdict-contract poison case: invalid
// verdict enum, additional_runners inconsistency, non-finite floats,
// non-positive ids, and out-of-range bounds.
func TestEncodeVerdictPoison(t *testing.T) {
	valid := func() *Verdict {
		return &Verdict{
			ExperimentUID:     testIdentity.ExperimentUID,
			Namespace:         testIdentity.Namespace,
			Project:           testIdentity.Project,
			ScenarioID:        42,
			RunnerRound:       1,
			Metric:            MetricMeanWaitTime,
			Verdict:           VerdictAdditionalRunners,
			SampleMean:        10.1,
			HalfWidth:         0.9,
			Replications:      40,
			ConfidenceMetric:  0.5,
			AdditionalRunners: 45,
			MaxReplications:   10000,
		}
	}
	cases := []struct {
		name string
		mut  func(*Verdict)
	}{
		{"invalid verdict enum", func(v *Verdict) { v.Verdict = "maybe" }},
		{"empty verdict", func(v *Verdict) { v.Verdict = "" }},
		{"additional zero with additional_runners", func(v *Verdict) { v.AdditionalRunners = 0 }},
		{"additional negative with additional_runners", func(v *Verdict) { v.AdditionalRunners = -3 }},
		{"additional nonzero with met", func(v *Verdict) { v.Verdict = VerdictMet; v.AdditionalRunners = 5 }},
		{"additional nonzero with stop_unmet", func(v *Verdict) { v.Verdict = VerdictStopUnmet; v.AdditionalRunners = 5 }},
		{"NaN sample mean", func(v *Verdict) { v.SampleMean = math.NaN() }},
		{"+Inf half width", func(v *Verdict) { v.HalfWidth = math.Inf(1) }},
		{"zero confidence metric", func(v *Verdict) { v.ConfidenceMetric = 0 }},
		{"NaN confidence metric", func(v *Verdict) { v.ConfidenceMetric = math.NaN() }},
		{"negative replications", func(v *Verdict) { v.Replications = -1 }},
		{"zero max replications", func(v *Verdict) { v.MaxReplications = 0 }},
		{"zero scenario id", func(v *Verdict) { v.ScenarioID = 0 }},
		{"zero runner round", func(v *Verdict) { v.RunnerRound = 0 }},
		{"wrong metric", func(v *Verdict) { v.Metric = "completed_customers" }},
		{"wrong uid", func(v *Verdict) { v.ExperimentUID = "not-a-uid" }},
		{"bad namespace", func(v *Verdict) { v.Namespace = "UPPER" }},
		{"bad project", func(v *Verdict) { v.Project = "has dot" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := valid()
			tc.mut(v)
			if _, err := EncodeVerdict(v); err == nil {
				t.Fatalf("%s: poison verdict accepted", tc.name)
			}
		})
	}
	// met and stop_unmet with zero additional encode fine.
	for _, verdict := range []string{VerdictMet, VerdictStopUnmet} {
		v := valid()
		v.Verdict = verdict
		v.AdditionalRunners = 0
		if _, err := EncodeVerdict(v); err != nil {
			t.Fatalf("%s with zero additional rejected: %v", verdict, err)
		}
	}
	if _, err := EncodeVerdict(nil); err == nil {
		t.Fatal("nil verdict accepted")
	}
}
