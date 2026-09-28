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

package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

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

// EvaluationRequestPublisher implements communication.EvaluationRequestPublisher
// over a JetStream context. It publishes on the exact
// cbse.<namespace>.<project>.pps.request subject and returns only after a
// JetStream PubAck, so the caller's MarkEvaluationRequestPublished reflects a
// durably accepted message. The evaluation-publication loop applies the
// lifecycle gate before publishing; this adapter performs no gate check.
type EvaluationRequestPublisher struct {
	js natsgo.JetStreamContext
}

// NewEvaluationRequestPublisher returns a publisher over the given JetStream
// context. It is the production evaluation-request publisher used by the
// evaluation-publication loop.
func NewEvaluationRequestPublisher(js natsgo.JetStreamContext) *EvaluationRequestPublisher {
	return &EvaluationRequestPublisher{js: js}
}

// evaluationRequestPayload is the seven-field evaluation request the Scenario
// Manager publishes on cbse.<namespace>.<project>.pps.request. It matches the
// normative wire contract (all fields required) carried by
// communication.ScenarioEvaluationRequest.
type evaluationRequestPayload struct {
	ExperimentUID    string  `json:"experiment_uid"`
	Namespace        string  `json:"namespace"`
	Project          string  `json:"project"`
	ScenarioID       int     `json:"scenario_id"`
	RunnerRound      int     `json:"runner_round"`
	NumberOfReps     int     `json:"number_of_reps"`
	ConfidenceMetric float64 `json:"confidence_metric"`
}

// PublishEvaluationRequest publishes one claimed scenario's evaluation
// request on the exact namespace-aware subject and returns nil only after a
// JetStream PubAck. The subject is constructed from the scenario's explicit
// (ProjectNamespace, ProjectName) identity; the identifiers are validated as
// DNS labels so a malformed identity is a publish failure rather than a
// silently wrong subject. A payload that violates the wire contract (non-
// positive scenario id, round below 1, rep count below 1, or a non-finite or
// non-positive confidence metric) is a publish failure: no message is sent.
func (p *EvaluationRequestPublisher) PublishEvaluationRequest(ctx context.Context, scenario communication.ScenarioForEvaluation) error {
	if p == nil || p.js == nil {
		return fmt.Errorf("evaluation request publisher is not initialized")
	}
	if scenario.ScenarioID <= 0 {
		return fmt.Errorf("scenario ID must be positive")
	}
	if scenario.EvaluationAttempt <= 0 {
		return fmt.Errorf("evaluation attempt must be positive")
	}
	if scenario.RunnerRound < 1 {
		return fmt.Errorf("runner round must be at least 1")
	}
	if scenario.NumberOfReps < 1 {
		return fmt.Errorf("number of reps must be at least 1")
	}
	if !(scenario.ConfidenceMetric > 0) || math.IsNaN(scenario.ConfidenceMetric) || math.IsInf(scenario.ConfidenceMetric, 0) {
		return fmt.Errorf("confidence metric must be finite and positive")
	}
	if scenario.ExperimentUID == "" {
		return fmt.Errorf("experiment UID must not be empty")
	}
	nsIdent, err := subject.ValidateIdent(scenario.ProjectNamespace)
	if err != nil {
		return fmt.Errorf("invalid project namespace %q: %w", scenario.ProjectNamespace, err)
	}
	projIdent, err := subject.ValidateIdent(scenario.ProjectName)
	if err != nil {
		return fmt.Errorf("invalid project name %q: %w", scenario.ProjectName, err)
	}
	subj := subject.PPSRequestSubject(nsIdent, projIdent)

	payload := evaluationRequestPayload{
		ExperimentUID:    scenario.ExperimentUID,
		Namespace:        scenario.ProjectNamespace,
		Project:          scenario.ProjectName,
		ScenarioID:       scenario.ScenarioID,
		RunnerRound:      scenario.RunnerRound,
		NumberOfReps:     scenario.NumberOfReps,
		ConfidenceMetric: scenario.ConfidenceMetric,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal evaluation request for scenario %d attempt %d: %w", scenario.ScenarioID, scenario.EvaluationAttempt, err)
	}

	ack, err := p.js.PublishMsg(&natsgo.Msg{Subject: subj, Data: data}, natsgo.Context(ctx))
	if err != nil {
		return fmt.Errorf("publish evaluation request for scenario %d attempt %d: %w", scenario.ScenarioID, scenario.EvaluationAttempt, err)
	}
	if ack == nil {
		return fmt.Errorf("publish evaluation request for scenario %d attempt %d returned nil PubAck", scenario.ScenarioID, scenario.EvaluationAttempt)
	}
	return nil
}
