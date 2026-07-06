// Package communication defines transport-neutral boundaries between Scenario
// Manager core workflows and concrete broker adapters.
//
// The translator workflow uses this package so core orchestration can reason in
// semantic terms such as "publish a translation request" or "handle a ready
// message" without importing NATS- or JetStream-specific types. Concrete
// adapters own subject parsing, ACK/NAK details, publish confirmation, and
// retry mechanics; core owns business state transitions and persistence.
package communication

import (
	"context"

	"github.com/D4NS3U/cbse/scenario-manager/internal/coredb"
)

// TranslationRequestPublisher is the transport-neutral publishing surface used
// by the translator handoff workflow in core.
//
// Implementations are expected to return nil only after the underlying
// transport has accepted the message durably enough for the caller to treat the
// publish as confirmed. For JetStream this means a successful PubAck. Returning
// an error signals that core should treat the publish as failed and run the
// appropriate recovery path for the claimed attempt.
type TranslationRequestPublisher interface {
	PublishTranslationRequest(ctx context.Context, scenario coredb.ScenarioForTranslation) error
}

// TranslatorReadyConsumer is the process-scoped consumer surface used to start
// delivery of Translator ready messages into a core-owned semantic handler.
//
// The consumer implementation owns broker subscriptions, raw payload decoding,
// transport validation, and ACK/NAK mapping. The provided handler receives a
// transport-neutral message only after the adapter has validated enough broker
// details to identify the scenario attempt safely.
type TranslatorReadyConsumer interface {
	StartTranslatorReadyConsumer(ctx context.Context, handler TranslatorReadyHandler) error
}

// TranslatorReadyHandler is the semantic callback invoked by transport
// adapters after transport-level validation has succeeded.
//
// The handler returns a transport-neutral result so adapters can translate
// semantic outcomes into their own acknowledgement model. In the v1 NATS
// adapter, Handled maps to ACK and Retry maps to NAK.
type TranslatorReadyHandler func(ctx context.Context, ready TranslatorReadyMessage) TranslatorReadyHandlingResult

// TranslatorReadyMessage is the transport-neutral representation of a ready
// message after the adapter has validated subject shape and JSON payload shape.
//
// Project is the normalized broker subject token, not the raw project name from
// the database. ScenarioID and TranslationAttempt identify the exact scenario
// attempt, while ContainerImage carries the semantic payload that core will
// persist or classify as poison when empty.
type TranslatorReadyMessage struct {
	Project            string
	ScenarioID         int
	TranslationAttempt int
	ContainerImage     string
}

// TranslatorReadyHandlingStatus expresses the semantic outcome of ready
// handling without leaking broker-specific acknowledgement concepts.
type TranslatorReadyHandlingStatus string

const (
	// TranslatorReadyHandled indicates that the message reached a terminal
	// semantic outcome. Adapters should ACK such messages, whether the underlying
	// outcome was a successful transition, a duplicate, or a poison/stale case.
	TranslatorReadyHandled TranslatorReadyHandlingStatus = "Handled"
	// TranslatorReadyRetry indicates that the message could not be handled due
	// to a transient dependency failure such as a database outage. Adapters
	// should request redelivery, such as a JetStream NAK.
	TranslatorReadyRetry TranslatorReadyHandlingStatus = "Retry"
)

// TranslatorReadyHandlingResult carries the semantic handling decision plus a
// short reason string that adapters can include in logs.
type TranslatorReadyHandlingResult struct {
	Status TranslatorReadyHandlingStatus
	Reason string
}
