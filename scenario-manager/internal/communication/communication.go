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

// Package communication defines the transport-neutral alpha4 communication
// boundaries between Scenario Manager core workflows and concrete broker
// adapters.
//
// Alpha4 replaces the single ambiguous Project field with an explicit
// (ProjectNamespace, ProjectName) identity carried through every
// transport-neutral and persistence projection. Wire payloads retain the
// legacy `project` field for fixture compatibility; the adapter populates the
// non-JSON ProjectNamespace and ProjectName fields after subject parsing, and
// core orchestration receives the explicit identity without reconstructing a
// namespace from process configuration or a normalized subject token.
package communication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/D4NS3U/cbse/scenario-manager/internal/subject"
)

// MinReps and MaxReps bound the per-scenario repetition count accepted by the
// Scenario Manager. A batch containing a count outside [MinReps, MaxReps] is
// permanent poison: the Scenario Manager returns an error acknowledgement,
// ACKs the JetStream delivery, and inserts no rows.
const (
	MinReps = 1
	MaxReps = 100000
)

// EDSAvailabilityRequest is published by the EDS to announce pending scenarios
// as a Core NATS request/reply on
// cbse.<namespace>.<project>.eds.scenarios.available. The wire payload retains
// the legacy `project` field; ProjectNamespace and ProjectName are populated by
// the adapter from the subject and are excluded from JSON.
type EDSAvailabilityRequest struct {
	BatchID       string `json:"batch_id,omitempty"`
	Project       string `json:"project,omitempty"`
	ScenarioCount int    `json:"scenario_count,omitempty"`

	// ProjectNamespace and ProjectName are populated by the adapter from the
	// request subject after parsing; they are not part of the wire payload.
	ProjectNamespace string `json:"-"`
	ProjectName      string `json:"-"`
}

// EDSAvailabilityReply is the Scenario Manager's reply to an EDS availability
// request. For an active InProgress experiment it returns the exact
// namespace-aware batch subject; otherwise it returns status=error with no
// batch_subject.
type EDSAvailabilityReply struct {
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	BatchSubject string `json:"batch_subject,omitempty"`
}

// Availability status values.
const (
	AvailabilityStatusReady = "ready"
	AvailabilityStatusError = "error"
)

// ScenarioBatch contains the scenarios sent by the EDS to populate the
// scenario_status table. The wire payload retains the legacy `project` field;
// ProjectNamespace and ProjectName are populated by the adapter from the batch
// subject after parsing and are excluded from JSON.
type ScenarioBatch struct {
	BatchID   string           `json:"batch_id,omitempty"`
	Project   string           `json:"project,omitempty"`
	Scenarios []ScenarioRecord `json:"scenarios"`

	// ProjectNamespace and ProjectName are populated by the adapter from the
	// batch subject after parsing; they are not part of the wire payload.
	ProjectNamespace string `json:"-"`
	ProjectName      string `json:"-"`
}

// ScenarioRecord is the per-scenario subset provided by the EDS. The Scenario
// Manager fills the remaining scenario_status columns on insert.
type ScenarioRecord struct {
	Priority         int             `json:"priority"`
	NumberOfReps     int             `json:"number_of_reps"`
	RecipeInfo       json.RawMessage `json:"recipe_info,omitempty"`
	ConfidenceMetric *float64        `json:"confidence_metric,omitempty"`
}

// ScenarioBatchAck reports how many scenarios were persisted from the batch.
type ScenarioBatchAck struct {
	Status   string `json:"status"`
	BatchID  string `json:"batch_id,omitempty"`
	Received int    `json:"received"`
	Inserted int    `json:"inserted"`
	Failed   int    `json:"failed"`
	Reason   string `json:"reason,omitempty"`
}

// BatchAck status values.
const (
	BatchAckStatusInserted = "inserted"
	BatchAckStatusPoison   = "poison"
	BatchAckStatusError    = "error"
)

// ScenarioForTranslation is the transport-neutral projection returned when the
// Scenario Manager claims a scenario row for translation handoff. It carries
// the explicit (ProjectNamespace, ProjectName) identity so the publisher can
// construct the exact request subject without reconstructing it from a single
// project token.
type ScenarioForTranslation struct {
	ID                 int
	ProjectNamespace   string
	ProjectName        string
	TranslationAttempt int
	RecipeInfo         json.RawMessage
	ConfidenceMetric   *float64
}

// TranslatorReadyMessage is the transport-neutral representation of a
// Translator ready message after the adapter has validated subject shape and
// JSON payload shape. It carries the explicit (ProjectNamespace, ProjectName)
// identity parsed from the ready subject.
type TranslatorReadyMessage struct {
	ProjectNamespace   string
	ProjectName        string
	ScenarioID         int
	TranslationAttempt int
	ContainerImage     string
}

// TranslatorReadyHandlingStatus expresses the semantic outcome of ready
// handling without leaking broker-specific acknowledgement concepts.
type TranslatorReadyHandlingStatus string

const (
	// TranslatorReadyHandled indicates a terminal semantic outcome. Adapters
	// ACK such messages, whether the outcome was a transition, a duplicate, or
	// a poison/stale case.
	TranslatorReadyHandled TranslatorReadyHandlingStatus = "Handled"
	// TranslatorReadyRetry indicates a transient dependency failure. Adapters
	// request redelivery (e.g. a JetStream NAK).
	TranslatorReadyRetry TranslatorReadyHandlingStatus = "Retry"
)

// TranslatorReadyHandlingResult carries the semantic handling decision plus a
// short reason string that adapters can include in logs.
type TranslatorReadyHandlingResult struct {
	Status TranslatorReadyHandlingStatus
	Reason string
}

// TranslationRequestPublisher is the transport-neutral publishing surface used
// by the translator handoff workflow. Implementations return nil only after
// the underlying transport has accepted the message durably (e.g. a JetStream
// PubAck). The publisher constructs the exact request subject from the
// scenario's ProjectNamespace and ProjectName.
type TranslationRequestPublisher interface {
	PublishTranslationRequest(ctx context.Context, scenario ScenarioForTranslation) error
}

// TranslatorReadyHandler is the semantic callback invoked by transport
// adapters after transport-level validation has succeeded.
type TranslatorReadyHandler func(ctx context.Context, ready TranslatorReadyMessage) TranslatorReadyHandlingResult

// TranslatorReadyConsumer is the process-scoped consumer surface used to
// start delivery of Translator ready messages into a core-owned handler.
type TranslatorReadyConsumer interface {
	StartTranslatorReadyConsumer(ctx context.Context, handler TranslatorReadyHandler) error
}

// ScenarioForEvaluation is the transport-neutral projection claimed for the
// evaluation-request publication workflow. It carries the explicit
// (ProjectNamespace, ProjectName) identity and the experiment UID so the
// publisher can construct the exact request subject and wire payload without
// reconstructing either from process configuration.
type ScenarioForEvaluation struct {
	ExperimentUID     string
	ProjectNamespace  string
	ProjectName       string
	ScenarioID        int
	EvaluationAttempt int
	RunnerRound       int
	NumberOfReps      int
	ConfidenceMetric  float64
}

// PPSEvaluationMessage is the transport-neutral representation of an
// evaluation verdict after the adapter has validated subject shape, JSON
// payload shape, and subject/payload identity. It carries the explicit
// (ProjectNamespace, ProjectName) identity parsed from the verdict subject.
type PPSEvaluationMessage struct {
	ProjectNamespace  string
	ProjectName       string
	ExperimentUID     string
	ScenarioID        int
	RunnerRound       int
	Metric            string
	Verdict           string
	SampleMean        float64
	HalfWidth         float64
	Replications      int
	ConfidenceMetric  float64
	AdditionalRunners int
	MaxReplications   int
}

// PPSEvaluationHandlingStatus expresses the semantic outcome of verdict
// handling without leaking broker-specific acknowledgement concepts.
type PPSEvaluationHandlingStatus string

const (
	// PPSEvaluationHandled indicates a terminal semantic outcome. Adapters ACK
	// such messages, whether the outcome was a transition, a duplicate, a
	// stale no-op, or a poison case.
	PPSEvaluationHandled PPSEvaluationHandlingStatus = "Handled"
	// PPSEvaluationRetry indicates a transient dependency failure. Adapters
	// request redelivery (e.g. a JetStream NAK).
	PPSEvaluationRetry PPSEvaluationHandlingStatus = "Retry"
)

// PPSEvaluationHandlingResult carries the semantic handling decision plus a
// short reason string that adapters can include in logs.
type PPSEvaluationHandlingResult struct {
	Status PPSEvaluationHandlingStatus
	Reason string
}

// EvaluationRequestPublisher is the transport-neutral publishing surface used
// by the evaluation handoff workflow. Implementations return nil only after
// the underlying transport has accepted the message durably (e.g. a JetStream
// PubAck). The publisher constructs the exact request subject from the
// scenario's ProjectNamespace and ProjectName.
type EvaluationRequestPublisher interface {
	PublishEvaluationRequest(ctx context.Context, scenario ScenarioForEvaluation) error
}

// PPSEvaluationHandler is the semantic callback invoked by transport adapters
// after transport-level validation has succeeded.
type PPSEvaluationHandler func(ctx context.Context, verdict PPSEvaluationMessage) PPSEvaluationHandlingResult

// PPSEvaluationConsumer is the process-scoped consumer surface used to start
// delivery of PPS evaluation verdicts into a core-owned handler.
type PPSEvaluationConsumer interface {
	StartPPSEvaluationConsumer(ctx context.Context, handler PPSEvaluationHandler) error
}

// EDSAvailabilityResponder answers an EDS availability request, applying the
// lifecycle gate and returning the exact batch subject for an active
// experiment or status=error with no batch_subject otherwise.
type EDSAvailabilityResponder interface {
	AnswerAvailability(ctx context.Context, req EDSAvailabilityRequest) (EDSAvailabilityReply, error)
}

// ErrBatchValidation is returned when an EDS batch fails permanent validation
// (subject/payload mismatch or an out-of-range repetition count). Such a
// batch is poison: the caller ACKs the delivery and inserts no rows.
var ErrBatchValidation = errors.New("eds batch validation failed")

// ErrEvaluationWire is returned when a PPS evaluation request or verdict wire
// payload fails strict validation: a missing or unknown JSON field, an out-of-
// domain value, or a subject/payload identity mismatch. Such a payload is
// permanent poison: the caller ACKs the delivery (or refuses to publish) and
// performs no persistence mutation.
var ErrEvaluationWire = errors.New("pps evaluation wire validation failed")

// ValidateBatchIdentity confirms that a decoded batch's wire `project` field
// is non-empty and matches the project token of the subject identity parsed
// by the adapter. A mismatch is permanent poison.
func ValidateBatchIdentity(batch ScenarioBatch, id subject.Identity) error {
	if batch.Project == "" {
		return fmt.Errorf("%w: empty project payload", ErrBatchValidation)
	}
	if batch.Project != id.Project.String() {
		return fmt.Errorf("%w: payload project %q != subject project %q", ErrBatchValidation, batch.Project, id.Project)
	}
	return nil
}

// ValidateBatchReps confirms that every scenario's number_of_reps is in
// [MinReps, MaxReps]. A batch containing an invalid count is permanent poison.
func ValidateBatchReps(scenarios []ScenarioRecord) error {
	for i, s := range scenarios {
		if s.NumberOfReps < MinReps || s.NumberOfReps > MaxReps {
			return fmt.Errorf("%w: scenario %d number_of_reps %d outside %d..%d", ErrBatchValidation, i, s.NumberOfReps, MinReps, MaxReps)
		}
	}
	return nil
}

// PPS evaluation verdict values. The PostProcessingService answers exactly one
// of these per evaluation round; the SM applies the matching guarded scenario
// transition. Any other value is permanent poison.
const (
	// VerdictMet is the precision-met verdict: the scenario finishes.
	VerdictMet = "met"
	// VerdictAdditionalRunners is the precision-not-met verdict: the scenario
	// starts a new runner round with the additional-runners count.
	VerdictAdditionalRunners = "additional_runners"
	// VerdictStopUnmet is the maximum-replications stop verdict: the scenario
	// fails through the guarded PostProcessing -> Failed path.
	VerdictStopUnmet = "stop_unmet"
)

// ScenarioEvaluationRequest is the evaluation request the Scenario Manager
// publishes on cbse.<namespace>.<project>.pps.request. Every field is
// required on the wire; UnmarshalJSON applies the strict shape and semantic
// validation, so a decoded value always satisfies Validate.
type ScenarioEvaluationRequest struct {
	ExperimentUID    string  `json:"experiment_uid"`
	Namespace        string  `json:"namespace"`
	Project          string  `json:"project"`
	ScenarioID       int     `json:"scenario_id"`
	RunnerRound      int     `json:"runner_round"`
	NumberOfReps     int     `json:"number_of_reps"`
	ConfidenceMetric float64 `json:"confidence_metric"`
}

// ppSEvaluationRequestWireFields is the exact required field set of the
// evaluation request payload; unknown or missing fields are poison.
var ppSEvaluationRequestWireFields = map[string]struct{}{
	"experiment_uid":    {},
	"namespace":         {},
	"project":           {},
	"scenario_id":       {},
	"runner_round":      {},
	"number_of_reps":    {},
	"confidence_metric": {},
}

// UnmarshalJSON decodes exactly one evaluation request and applies the strict
// wire validation: every field present, no unknown fields, and the semantic
// domain checks in Validate. Any violation is ErrEvaluationWire.
func (r *ScenarioEvaluationRequest) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("%w: %v", ErrEvaluationWire, err)
	}
	if err := checkWireFields(raw, ppSEvaluationRequestWireFields); err != nil {
		return err
	}
	// Decode into an intermediate shape to avoid re-entering this method.
	var wire struct {
		ExperimentUID    string  `json:"experiment_uid"`
		Namespace        string  `json:"namespace"`
		Project          string  `json:"project"`
		ScenarioID       int     `json:"scenario_id"`
		RunnerRound      int     `json:"runner_round"`
		NumberOfReps     int     `json:"number_of_reps"`
		ConfidenceMetric float64 `json:"confidence_metric"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("%w: %v", ErrEvaluationWire, err)
	}
	*r = ScenarioEvaluationRequest{
		ExperimentUID:    wire.ExperimentUID,
		Namespace:        wire.Namespace,
		Project:          wire.Project,
		ScenarioID:       wire.ScenarioID,
		RunnerRound:      wire.RunnerRound,
		NumberOfReps:     wire.NumberOfReps,
		ConfidenceMetric: wire.ConfidenceMetric,
	}
	return r.Validate()
}

// Validate applies the semantic domain checks of the evaluation request
// contract: positive scenario id, runner round at least 1, at least one
// replication, a finite positive confidence metric (the precision threshold
// epsilon), and non-empty identity fields.
func (r *ScenarioEvaluationRequest) Validate() error {
	if r.ExperimentUID == "" {
		return fmt.Errorf("%w: empty experiment_uid", ErrEvaluationWire)
	}
	if r.Namespace == "" {
		return fmt.Errorf("%w: empty namespace", ErrEvaluationWire)
	}
	if r.Project == "" {
		return fmt.Errorf("%w: empty project", ErrEvaluationWire)
	}
	if r.ScenarioID <= 0 {
		return fmt.Errorf("%w: scenario_id %d must be positive", ErrEvaluationWire, r.ScenarioID)
	}
	if r.RunnerRound < 1 {
		return fmt.Errorf("%w: runner_round %d must be at least 1", ErrEvaluationWire, r.RunnerRound)
	}
	if r.NumberOfReps < 1 {
		return fmt.Errorf("%w: number_of_reps %d must be at least 1", ErrEvaluationWire, r.NumberOfReps)
	}
	if !isFinitePositive(r.ConfidenceMetric) {
		return fmt.Errorf("%w: confidence_metric %v must be finite and positive", ErrEvaluationWire, r.ConfidenceMetric)
	}
	return nil
}

// PPSEvaluationVerdict is the evaluation verdict the PostProcessingService
// publishes on cbse.<namespace>.<project>.pps.<scenario-id>.evaluation.
// Every field is required on the wire; UnmarshalJSON applies the strict shape
// and semantic validation, so a decoded value always satisfies Validate.
type PPSEvaluationVerdict struct {
	ExperimentUID     string  `json:"experiment_uid"`
	Namespace         string  `json:"namespace"`
	Project           string  `json:"project"`
	ScenarioID        int     `json:"scenario_id"`
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

// ppSEvaluationVerdictWireFields is the exact required field set of the
// evaluation verdict payload; unknown or missing fields are poison.
var ppSEvaluationVerdictWireFields = map[string]struct{}{
	"experiment_uid":     {},
	"namespace":          {},
	"project":            {},
	"scenario_id":        {},
	"runner_round":       {},
	"metric":             {},
	"verdict":            {},
	"sample_mean":        {},
	"half_width":         {},
	"replications":       {},
	"confidence_metric":  {},
	"additional_runners": {},
	"max_replications":   {},
}

// UnmarshalJSON decodes exactly one evaluation verdict and applies the strict
// wire validation: every field present, no unknown fields, and the semantic
// domain checks in Validate. Any violation is ErrEvaluationWire.
func (v *PPSEvaluationVerdict) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("%w: %v", ErrEvaluationWire, err)
	}
	if err := checkWireFields(raw, ppSEvaluationVerdictWireFields); err != nil {
		return err
	}
	// Decode into an intermediate shape to avoid re-entering this method.
	var wire struct {
		ExperimentUID     string  `json:"experiment_uid"`
		Namespace         string  `json:"namespace"`
		Project           string  `json:"project"`
		ScenarioID        int     `json:"scenario_id"`
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
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("%w: %v", ErrEvaluationWire, err)
	}
	*v = PPSEvaluationVerdict{
		ExperimentUID:     wire.ExperimentUID,
		Namespace:         wire.Namespace,
		Project:           wire.Project,
		ScenarioID:        wire.ScenarioID,
		RunnerRound:       wire.RunnerRound,
		Metric:            wire.Metric,
		Verdict:           wire.Verdict,
		SampleMean:        wire.SampleMean,
		HalfWidth:         wire.HalfWidth,
		Replications:      wire.Replications,
		ConfidenceMetric:  wire.ConfidenceMetric,
		AdditionalRunners: wire.AdditionalRunners,
		MaxReplications:   wire.MaxReplications,
	}
	return v.Validate()
}

// Validate applies the semantic domain checks of the evaluation verdict
// contract: non-empty identity and metric, positive scenario id, runner round
// at least 1, a known verdict, finite sample mean and half width, a finite
// positive confidence metric, non-negative replications, and
// additional_runners >= 1 exactly when the verdict is additional_runners
// (zero otherwise).
func (v *PPSEvaluationVerdict) Validate() error {
	if v.ExperimentUID == "" {
		return fmt.Errorf("%w: empty experiment_uid", ErrEvaluationWire)
	}
	if v.Namespace == "" {
		return fmt.Errorf("%w: empty namespace", ErrEvaluationWire)
	}
	if v.Project == "" {
		return fmt.Errorf("%w: empty project", ErrEvaluationWire)
	}
	if v.Metric == "" {
		return fmt.Errorf("%w: empty metric", ErrEvaluationWire)
	}
	if v.ScenarioID <= 0 {
		return fmt.Errorf("%w: scenario_id %d must be positive", ErrEvaluationWire, v.ScenarioID)
	}
	if v.RunnerRound < 1 {
		return fmt.Errorf("%w: runner_round %d must be at least 1", ErrEvaluationWire, v.RunnerRound)
	}
	switch v.Verdict {
	case VerdictMet, VerdictAdditionalRunners, VerdictStopUnmet:
	default:
		return fmt.Errorf("%w: unknown verdict %q", ErrEvaluationWire, v.Verdict)
	}
	if !isFinite(v.SampleMean) {
		return fmt.Errorf("%w: sample_mean %v must be finite", ErrEvaluationWire, v.SampleMean)
	}
	if !isFinite(v.HalfWidth) {
		return fmt.Errorf("%w: half_width %v must be finite", ErrEvaluationWire, v.HalfWidth)
	}
	if !isFinitePositive(v.ConfidenceMetric) {
		return fmt.Errorf("%w: confidence_metric %v must be finite and positive", ErrEvaluationWire, v.ConfidenceMetric)
	}
	if v.Replications < 0 {
		return fmt.Errorf("%w: replications %d must be non-negative", ErrEvaluationWire, v.Replications)
	}
	if v.Verdict == VerdictAdditionalRunners {
		if v.AdditionalRunners < 1 {
			return fmt.Errorf("%w: additional_runners %d must be at least 1 for verdict %q", ErrEvaluationWire, v.AdditionalRunners, VerdictAdditionalRunners)
		}
	} else if v.AdditionalRunners != 0 {
		return fmt.Errorf("%w: additional_runners %d must be zero unless the verdict is %q", ErrEvaluationWire, v.AdditionalRunners, VerdictAdditionalRunners)
	}
	return nil
}

// checkWireFields confirms that raw carries exactly the required field set:
// every required key present and no unknown keys. It is the shared strict-shape
// check of the evaluation request and verdict payloads.
func checkWireFields(raw map[string]json.RawMessage, required map[string]struct{}) error {
	for k := range raw {
		if _, ok := required[k]; !ok {
			return fmt.Errorf("%w: unknown field %q", ErrEvaluationWire, k)
		}
	}
	for k := range required {
		if _, ok := raw[k]; !ok {
			return fmt.Errorf("%w: missing required field %q", ErrEvaluationWire, k)
		}
	}
	return nil
}

// isFinite reports whether f is a finite float (not NaN or infinity).
func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// isFinitePositive reports whether f is finite and strictly positive.
func isFinitePositive(f float64) bool {
	return isFinite(f) && f > 0
}
