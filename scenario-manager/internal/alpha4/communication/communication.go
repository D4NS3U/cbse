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
//
// This package is additive and isolated until the alpha4 cutover in a later
// slice: it does not replace the active alpha3 communication wiring.
package communication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/subject"
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
