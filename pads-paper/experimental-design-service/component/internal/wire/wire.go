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

// Package wire defines the alpha4 EDS protocol payloads the reference
// ExperimentalDesignService publishes and consumes on the Scenario Manager's
// landed wire contract, byte-pinned by FEATURE.md §5 (the parallelism
// contract pin). The four payloads:
//
//   - AvailabilityRequest — published by the EDS on
//     cbse.<ns>.<proj>.eds.scenarios.available; the reference EDS always
//     declares experiment_type "standard".
//   - AvailabilityReply — consumed from the Scenario Manager; requires
//     status "ready" and a non-empty batch_subject, and the verbatim
//     experiment_type echo (a missing or mismatched echo is a hard protocol
//     error indicating an SM without the finalization protocol).
//   - ScenarioBatch — published to the reply's batch subject; a non-final
//     batch marshals without the final field; the finalization batch carries
//     scenarios [] and final true.
//   - ScenarioBatchAck — consumed from the Scenario Manager; "inserted" is
//     success; status and reason are surfaced in the API response.
//
// The field-for-field contract is shared verbatim with the Scenario Manager's
// communication package; this module defines its own types because
// Go internal-package rules forbid importing scenario-manager/internal/**.
//
// Decoding is strict: unknown fields and trailing JSON tokens are rejected so
// a malformed or shape-incompatible payload is detected before any action.
package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

// ExperimentTypeStandard is the reference EDS's declared experiment type. The
// reference EDS always declares it on the availability request; the Scenario
// Manager must echo it verbatim (capability handshake of the finalization
// protocol).
const ExperimentTypeStandard = "standard"

// MinReps and MaxReps bound the per-scenario repetition count, mirroring the
// Scenario Manager's landed guards.
const (
	MinReps = 1
	MaxReps = 100000
)

// Availability status values.
const (
	AvailabilityStatusReady = "ready"
	AvailabilityStatusError = "error"
)

// Batch ack status values.
const (
	BatchAckStatusInserted = "inserted"
	BatchAckStatusPoison   = "poison"
	BatchAckStatusError    = "error"
)

// Identity is the process identity the EDS derives from its downward-API
// environment: the full experiment UID and the namespace/project DNS labels.
type Identity struct {
	ExperimentUID string
	Namespace     string
	Project       string
}

// AvailabilityRequest is the EDS's availability probe published on the
// per-project availability subject. The reference EDS always declares
// experiment_type "standard"; all fields are always present on the wire.
type AvailabilityRequest struct {
	BatchID        string `json:"batch_id"`
	Project        string `json:"project"`
	ScenarioCount  int    `json:"scenario_count"`
	ExperimentType string `json:"experiment_type"`
}

// AvailabilityReply is the Scenario Manager's reply to the availability
// request. For an active InProgress experiment it carries status "ready" and
// the exact batch subject; otherwise status "error" with a reason and no
// batch subject. experiment_type is echoed verbatim, status-independently,
// only when declared.
type AvailabilityReply struct {
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
	BatchSubject   string `json:"batch_subject,omitempty"`
	ExperimentType string `json:"experiment_type,omitempty"`
}

// RecipeInfo is the per-scenario recipe locator: the parameterset_id whose
// rows resolve in the Detail DB's simulation_parameters view.
type RecipeInfo struct {
	ParametersetID int `json:"parameterset_id"`
}

// ScenarioRecord is the per-scenario subset the EDS provides; the Scenario
// Manager fills the remaining scenario_status columns on insert.
type ScenarioRecord struct {
	Priority         int        `json:"priority"`
	NumberOfReps     int        `json:"number_of_reps"`
	RecipeInfo       RecipeInfo `json:"recipe_info"`
	ConfidenceMetric float64    `json:"confidence_metric"`
}

// ScenarioBatch is the scenario batch the EDS publishes to the
// SM-provided batch subject. Final is absent (false) on a normal batch and
// true only on the finalization batch, which also carries scenarios []
// (the empty array is always present on the wire).
type ScenarioBatch struct {
	BatchID   string           `json:"batch_id"`
	Project   string           `json:"project"`
	Scenarios []ScenarioRecord `json:"scenarios"`
	Final     bool             `json:"final,omitempty"`
}

// ScenarioBatchAck reports how many scenarios the Scenario Manager persisted
// from the batch. "inserted" is success; "poison" and "error" carry a
// reason.
type ScenarioBatchAck struct {
	Status   string `json:"status"`
	BatchID  string `json:"batch_id,omitempty"`
	Received int    `json:"received"`
	Inserted int    `json:"inserted"`
	Failed   int    `json:"failed"`
	Reason   string `json:"reason,omitempty"`
}

// DecodeAvailabilityReply strictly decodes an availability reply payload. It
// rejects malformed JSON, unknown fields, and trailing tokens.
func DecodeAvailabilityReply(data []byte) (*AvailabilityReply, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var reply AvailabilityReply
	if err := dec.Decode(&reply); err != nil {
		return nil, fmt.Errorf("decode availability reply: %w", err)
	}
	if err := rejectTrailing(dec); err != nil {
		return nil, fmt.Errorf("decode availability reply: %w", err)
	}
	return &reply, nil
}

// DecodeScenarioBatch strictly decodes a scenario batch payload. It rejects
// malformed JSON, unknown fields, and trailing tokens.
func DecodeScenarioBatch(data []byte) (*ScenarioBatch, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var batch ScenarioBatch
	if err := dec.Decode(&batch); err != nil {
		return nil, fmt.Errorf("decode scenario batch: %w", err)
	}
	if err := rejectTrailing(dec); err != nil {
		return nil, fmt.Errorf("decode scenario batch: %w", err)
	}
	return &batch, nil
}

// DecodeBatchAck strictly decodes a batch acknowledgement payload. It
// rejects malformed JSON, unknown fields, and trailing tokens, and requires
// a status from the three-value enum.
func DecodeBatchAck(data []byte) (*ScenarioBatchAck, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var ack ScenarioBatchAck
	if err := dec.Decode(&ack); err != nil {
		return nil, fmt.Errorf("decode batch ack: %w", err)
	}
	if err := rejectTrailing(dec); err != nil {
		return nil, fmt.Errorf("decode batch ack: %w", err)
	}
	if ack.Status != BatchAckStatusInserted && ack.Status != BatchAckStatusPoison && ack.Status != BatchAckStatusError {
		return nil, fmt.Errorf("decode batch ack: status %q must be one of %q, %q, %q",
			ack.Status, BatchAckStatusInserted, BatchAckStatusPoison, BatchAckStatusError)
	}
	return &ack, nil
}

// rejectTrailing fails when a second JSON token follows the decoded object.
func rejectTrailing(dec *json.Decoder) error {
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err == nil {
		return errors.New("unexpected trailing JSON content")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// ValidateAvailabilityRequest applies the field-for-field request contract
// against the process identity: batch_id non-empty, project exactly equal to
// the identity project (a mismatch is poison), scenario_count >= 0 (zero on
// the finalization handshake; a non-final Start never has an empty delta —
// the server refuses it before the handshake), and experiment_type exactly
// "standard" (the reference EDS always declares it).
func ValidateAvailabilityRequest(r *AvailabilityRequest, id Identity) error {
	if r == nil {
		return errors.New("availability request is nil")
	}
	if r.BatchID == "" {
		return errors.New("availability request batch_id is empty")
	}
	if r.Project != id.Project {
		return fmt.Errorf("availability request project %q does not match identity project %q (poison)", r.Project, id.Project)
	}
	if r.ScenarioCount < 0 {
		return fmt.Errorf("availability request scenario_count %d must be >= 0", r.ScenarioCount)
	}
	if r.ExperimentType != ExperimentTypeStandard {
		return fmt.Errorf("availability request experiment_type %q must be %q", r.ExperimentType, ExperimentTypeStandard)
	}
	return nil
}

// ReplyError classifies a consumed availability reply. A nil or malformed
// reply is a hard protocol error; status "error" is a clean refusal (the SM's
// reason is surfaced to the UI); status "ready" requires a non-empty
// batch_subject; and the verbatim experiment_type echo must equal
// "standard" — a missing or mismatched echo is a hard protocol error
// (an SM without the finalization protocol). The returned reason is the
// clean, UI-visible wording.
func ReplyError(reply *AvailabilityReply) (subject string, reason string, ok bool) {
	if reply == nil {
		return "", "availability reply is empty", false
	}
	switch reply.Status {
	case AvailabilityStatusReady:
		if reply.BatchSubject == "" {
			return "", "availability reply is ready but batch_subject is missing", false
		}
	case AvailabilityStatusError:
		if reply.Reason == "" {
			reply.Reason = "experiment unavailable"
		}
		return "", reply.Reason, false
	default:
		return "", fmt.Sprintf("availability reply status %q is not usable", reply.Status), false
	}
	if reply.ExperimentType != ExperimentTypeStandard {
		if reply.ExperimentType == "" {
			return "", "availability reply does not echo experiment_type \"standard\" (SM without the finalization protocol)", false
		}
		return "", fmt.Sprintf("availability reply echoes experiment_type %q, expected %q", reply.ExperimentType, ExperimentTypeStandard), false
	}
	return reply.BatchSubject, "", true
}

// ValidateBatch applies the field-for-field batch contract against the
// process identity: batch_id non-empty, project exactly equal to the
// identity project (a mismatch is poison), and every scenario record within
// the landed domain (priority > 0, number_of_reps in [MinReps, MaxReps],
// parameterset_id > 0, confidence_metric finite and > 0).
func ValidateBatch(b *ScenarioBatch, id Identity) error {
	if b == nil {
		return errors.New("scenario batch is nil")
	}
	if b.BatchID == "" {
		return errors.New("scenario batch batch_id is empty")
	}
	if b.Project != id.Project {
		return fmt.Errorf("scenario batch project %q does not match identity project %q (poison)", b.Project, id.Project)
	}
	for i, s := range b.Scenarios {
		if err := s.Validate(); err != nil {
			return fmt.Errorf("scenario %d: %w", i, err)
		}
	}
	return nil
}

// Validate applies the per-scenario domain contract: priority > 0,
// number_of_reps in [MinReps, MaxReps] (the SM's landed guard),
// parameterset_id > 0, and confidence_metric finite and > 0.
func (s *ScenarioRecord) Validate() error {
	if s == nil {
		return errors.New("scenario record is nil")
	}
	if s.Priority <= 0 {
		return fmt.Errorf("priority %d must be > 0", s.Priority)
	}
	if s.NumberOfReps < MinReps || s.NumberOfReps > MaxReps {
		return fmt.Errorf("number_of_reps %d is outside %d..%d", s.NumberOfReps, MinReps, MaxReps)
	}
	if s.RecipeInfo.ParametersetID <= 0 {
		return fmt.Errorf("recipe_info.parameterset_id %d must be > 0", s.RecipeInfo.ParametersetID)
	}
	if !isFinitePositive(s.ConfidenceMetric) {
		return fmt.Errorf("confidence_metric %v must be finite and > 0", s.ConfidenceMetric)
	}
	return nil
}

// isFinitePositive reports whether x is finite and > 0.
func isFinitePositive(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0) && x > 0
}
