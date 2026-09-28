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

// Package wire defines the alpha4 PPS evaluation request and evaluation
// verdict JSON payloads the reference PostProcessingService consumes and
// publishes. The request (Scenario Manager to PPS) carries the identity
// context; the verdict (PPS to Scenario Manager) carries the evaluation
// outcome. The field-for-field contract is shared verbatim with the Scenario
// Manager's communication package; this module defines its own types because
// Go internal-package rules forbid importing scenario-manager/internal/**.
//
// Request (all fields required):
//
//	{"experiment_uid": "<uid>", "namespace": "<ns>", "project": "<proj>",
//	 "scenario_id": 42, "runner_round": 1, "number_of_reps": 40,
//	 "confidence_metric": 0.5}
//
// Verdict (all fields required):
//
//	{"experiment_uid": "<uid>", "namespace": "<ns>", "project": "<proj>",
//	 "scenario_id": 42, "runner_round": 1, "metric": "mean_wait_time",
//	 "verdict": "met"|"additional_runners"|"stop_unmet",
//	 "sample_mean": 10.1, "half_width": 0.9, "replications": 40,
//	 "confidence_metric": 0.5, "additional_runners": 45,
//	 "max_replications": 10000}
//
// Decoding is strict: unknown fields and trailing JSON tokens are rejected so
// a malformed or shape-incompatible request is detected before any Result DB
// work. A request that fails strict decoding, or that fails ValidateRequest
// (wrong identity, non-positive scenario id, non-positive round or
// number_of_reps, non-finite or non-positive confidence_metric), has no
// usable identity or criterion and is permanent poison: the PPS ACKs it
// without querying the Result DB or publishing a verdict.
package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
)

// MetricMeanWaitTime is the reference performance metric: the paper's "mean
// waiting time" KPI of the reference runner model, in SimPy time units. The
// wire contract carries the metric name so custom PPS images can evaluate
// other metrics of the result rows.
const MetricMeanWaitTime = "mean_wait_time"

// Verdict enums. additional_runners carries a positive additional_runners
// count; met and stop_unmet carry additional_runners 0.
const (
	VerdictMet               = "met"
	VerdictAdditionalRunners = "additional_runners"
	VerdictStopUnmet         = "stop_unmet"
)

// Identity is the pod identity the PPS derives from its downward-API
// environment: the full experiment UID and the namespace/project DNS labels.
type Identity struct {
	ExperimentUID string
	Namespace     string
	Project       string
}

// Request is the seven-field evaluation request payload.
type Request struct {
	ExperimentUID    string  `json:"experiment_uid"`
	Namespace        string  `json:"namespace"`
	Project          string  `json:"project"`
	ScenarioID       int64   `json:"scenario_id"`
	RunnerRound      int     `json:"runner_round"`
	NumberOfReps     int     `json:"number_of_reps"`
	ConfidenceMetric float64 `json:"confidence_metric"`
}

// Verdict is the thirteen-field evaluation verdict payload.
type Verdict struct {
	ExperimentUID     string  `json:"experiment_uid"`
	Namespace         string  `json:"namespace"`
	Project           string  `json:"project"`
	ScenarioID        int64   `json:"scenario_id"`
	RunnerRound       int     `json:"runner_round"`
	Metric            string  `json:"metric"`
	Verdict           string  `json:"verdict"`
	SampleMean        float64 `json:"sample_mean"`
	HalfWidth         float64 `json:"half_width"`
	Replications      int     `json:"replications"`
	ConfidenceMetric  float64 `json:"confidence_metric"`
	AdditionalRunners int     `json:"additional_runners"`
	MaxReplications   int     `json:"max_replications"`
}

// uidRe matches a Kubernetes-style UID: 8-4-4-4-12 lowercase hex groups.
var uidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// identRe repeats the CRD admission rule: a lowercase DNS label of 1-63
// characters.
var identRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// DecodeRequest strictly decodes an evaluation request payload. It rejects
// malformed JSON, unknown fields, and trailing tokens. It does NOT validate
// the domain constraints of the fields; the caller classifies the request
// after decoding.
func DecodeRequest(data []byte) (*Request, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var req Request
	if err := dec.Decode(&req); err != nil {
		return nil, fmt.Errorf("decode evaluation request: %w", err)
	}
	// Reject trailing tokens after the single object.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err == nil {
		return nil, errors.New("decode evaluation request: unexpected trailing JSON content")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode evaluation request: %w", err)
	}
	return &req, nil
}

// DecodeVerdict strictly decodes an evaluation verdict payload. It rejects
// malformed JSON, unknown fields, and trailing tokens. It is provided for
// conformance testing; the PPS encodes verdicts, it does not consume them.
func DecodeVerdict(data []byte) (*Verdict, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var v Verdict
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("decode evaluation verdict: %w", err)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err == nil {
		return nil, errors.New("decode evaluation verdict: unexpected trailing JSON content")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode evaluation verdict: %w", err)
	}
	return &v, nil
}

// ValidateRequest applies the field-for-field request contract against the
// pod identity. Every field is required: the three identity fields must
// exactly equal the pod's downward-API identity (a mismatch is permanent
// poison); scenario_id must be positive; runner_round and number_of_reps
// must be >= 1; confidence_metric must be finite and > 0 (it is the desired
// precision threshold epsilon in the metric's units).
func (r *Request) ValidateRequest(id Identity) error {
	if r == nil {
		return errors.New("evaluation request is nil")
	}
	if !uidRe.MatchString(r.ExperimentUID) {
		return fmt.Errorf("experiment_uid %q is not a valid UID (8-4-4-4-12 lowercase hex)", r.ExperimentUID)
	}
	if r.ExperimentUID != id.ExperimentUID {
		return fmt.Errorf("experiment_uid %q does not match pod identity %q", r.ExperimentUID, id.ExperimentUID)
	}
	if len(r.Namespace) < 1 || len(r.Namespace) > 63 || !identRe.MatchString(r.Namespace) {
		return fmt.Errorf("namespace %q is not a valid DNS label", r.Namespace)
	}
	if r.Namespace != id.Namespace {
		return fmt.Errorf("namespace %q does not match pod identity %q", r.Namespace, id.Namespace)
	}
	if len(r.Project) < 1 || len(r.Project) > 63 || !identRe.MatchString(r.Project) {
		return fmt.Errorf("project %q is not a valid DNS label", r.Project)
	}
	if r.Project != id.Project {
		return fmt.Errorf("project %q does not match pod identity %q", r.Project, id.Project)
	}
	if r.ScenarioID <= 0 {
		return fmt.Errorf("scenario_id %d must be > 0", r.ScenarioID)
	}
	if r.RunnerRound < 1 {
		return fmt.Errorf("runner_round %d must be >= 1", r.RunnerRound)
	}
	if r.NumberOfReps < 1 {
		return fmt.Errorf("number_of_reps %d must be >= 1", r.NumberOfReps)
	}
	if !isFinitePositive(r.ConfidenceMetric) {
		return fmt.Errorf("confidence_metric %v must be finite and > 0", r.ConfidenceMetric)
	}
	return nil
}

// EncodeVerdict encodes an evaluation verdict payload for publication after
// validating the field-for-field verdict contract: valid identity fields,
// scenario_id > 0, runner_round >= 1, metric exactly mean_wait_time, verdict
// in the three-value enum, finite sample_mean and half_width, replications
// >= 0, confidence_metric finite and > 0, max_replications > 0, and
// additional_runners >= 1 iff verdict is additional_runners (0 otherwise).
func EncodeVerdict(v *Verdict) ([]byte, error) {
	if err := v.ValidateVerdict(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode evaluation verdict: %w", err)
	}
	return data, nil
}

// ValidateVerdict applies the field-for-field verdict contract. It is the
// single shared validation so EncodeVerdict and the golden conformance tests
// enforce exactly the same rules.
func (v *Verdict) ValidateVerdict() error {
	if v == nil {
		return errors.New("evaluation verdict is nil")
	}
	if !uidRe.MatchString(v.ExperimentUID) {
		return fmt.Errorf("experiment_uid %q is not a valid UID (8-4-4-4-12 lowercase hex)", v.ExperimentUID)
	}
	if len(v.Namespace) < 1 || len(v.Namespace) > 63 || !identRe.MatchString(v.Namespace) {
		return fmt.Errorf("namespace %q is not a valid DNS label", v.Namespace)
	}
	if len(v.Project) < 1 || len(v.Project) > 63 || !identRe.MatchString(v.Project) {
		return fmt.Errorf("project %q is not a valid DNS label", v.Project)
	}
	if v.ScenarioID <= 0 {
		return fmt.Errorf("scenario_id %d must be > 0", v.ScenarioID)
	}
	if v.RunnerRound < 1 {
		return fmt.Errorf("runner_round %d must be >= 1", v.RunnerRound)
	}
	if v.Metric != MetricMeanWaitTime {
		return fmt.Errorf("metric %q must be %q", v.Metric, MetricMeanWaitTime)
	}
	switch v.Verdict {
	case VerdictMet, VerdictAdditionalRunners, VerdictStopUnmet:
	default:
		return fmt.Errorf("verdict %q must be one of %q, %q, %q", v.Verdict, VerdictMet, VerdictAdditionalRunners, VerdictStopUnmet)
	}
	if !isFiniteFloat(v.SampleMean) {
		return fmt.Errorf("sample_mean %v must be finite", v.SampleMean)
	}
	if !isFiniteFloat(v.HalfWidth) {
		return fmt.Errorf("half_width %v must be finite", v.HalfWidth)
	}
	if v.Replications < 0 {
		return fmt.Errorf("replications %d must be >= 0", v.Replications)
	}
	if !isFinitePositive(v.ConfidenceMetric) {
		return fmt.Errorf("confidence_metric %v must be finite and > 0", v.ConfidenceMetric)
	}
	if v.MaxReplications <= 0 {
		return fmt.Errorf("max_replications %d must be > 0", v.MaxReplications)
	}
	if v.Verdict == VerdictAdditionalRunners {
		if v.AdditionalRunners < 1 {
			return fmt.Errorf("additional_runners %d must be >= 1 when verdict is %q", v.AdditionalRunners, VerdictAdditionalRunners)
		}
	} else if v.AdditionalRunners != 0 {
		return fmt.Errorf("additional_runners %d must be 0 when verdict is %q", v.AdditionalRunners, v.Verdict)
	}
	return nil
}

// isFinitePositive reports whether x is finite and > 0.
func isFinitePositive(x float64) bool {
	return isFiniteFloat(x) && x > 0
}

// isFiniteFloat reports whether x is neither NaN nor +/-Inf.
func isFiniteFloat(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}
