package nats

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/subject"
	natsgo "github.com/nats-io/nats.go"
)

// TranslationRequestPublisher implements communication.TranslationRequestPublisher
// over a JetStream context. It publishes on the exact
// cbse.<namespace>.<project>.trans.request subject and returns only after a
// JetStream PubAck, so the caller's MarkScenarioTranslationRequestPublished
// reflects a durably accepted message. The selection loop applies the
// lifecycle gate before publishing; this adapter performs no gate check.
type TranslationRequestPublisher struct {
	js natsgo.JetStreamContext
}

// NewTranslationRequestPublisher returns a publisher over the given JetStream
// context. It is the production translation-request publisher used by the
// selection loop.
func NewTranslationRequestPublisher(js natsgo.JetStreamContext) *TranslationRequestPublisher {
	return &TranslationRequestPublisher{js: js}
}

// PublishTranslationRequest publishes one claimed scenario's translation
// request on the exact namespace-aware subject and returns nil only after a
// JetStream PubAck. The subject is constructed from the scenario's explicit
// (ProjectNamespace, ProjectName) identity; the identifiers are validated as
// DNS labels so a malformed identity is a publish failure rather than a
// silently wrong subject.
func (p *TranslationRequestPublisher) PublishTranslationRequest(ctx context.Context, scenario communication.ScenarioForTranslation) error {
	if p == nil || p.js == nil {
		return fmt.Errorf("translation request publisher is not initialized")
	}
	if scenario.ID <= 0 {
		return fmt.Errorf("scenario ID must be positive")
	}
	if scenario.TranslationAttempt <= 0 {
		return fmt.Errorf("translation attempt must be positive")
	}
	nsIdent, err := subject.ValidateIdent(scenario.ProjectNamespace)
	if err != nil {
		return fmt.Errorf("invalid project namespace %q: %w", scenario.ProjectNamespace, err)
	}
	projIdent, err := subject.ValidateIdent(scenario.ProjectName)
	if err != nil {
		return fmt.Errorf("invalid project name %q: %w", scenario.ProjectName, err)
	}
	subj := subject.TranslatorRequestSubject(nsIdent, projIdent)

	payload := translationRequestPayload{
		ID:                 scenario.ID,
		TranslationAttempt: scenario.TranslationAttempt,
		RecipeInfo:         scenario.RecipeInfo,
		ConfidenceMetric:   scenario.ConfidenceMetric,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal translation request for scenario %d attempt %d: %w", scenario.ID, scenario.TranslationAttempt, err)
	}

	ack, err := p.js.PublishMsg(&natsgo.Msg{Subject: subj, Data: data}, natsgo.Context(ctx))
	if err != nil {
		return fmt.Errorf("publish translation request for scenario %d attempt %d: %w", scenario.ID, scenario.TranslationAttempt, err)
	}
	if ack == nil {
		return fmt.Errorf("publish translation request for scenario %d attempt %d returned nil PubAck", scenario.ID, scenario.TranslationAttempt)
	}
	return nil
}
