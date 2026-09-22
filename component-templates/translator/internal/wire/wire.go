// Package wire defines the alpha4 translation request and ready-message JSON
// payloads the reference Translator framework decodes and publishes. The
// four-field request and two-field ready message match the Scenario Manager
// wire contract exactly.
//
// Request:
//
//	{"id": <positive int>, "translation_attempt": <positive int>,
//	 "recipe_info": <object>, "confidence_metric": <number|null>}
//
// Ready:
//
//	{"translation_attempt": <positive int>, "container_image": "<digest ref>"}
//
// Decoding is strict: unknown fields and trailing JSON tokens are rejected so a
// malformed or shape-incompatible request is detected before any workspace or
// generator work. A request that fails strict decoding, or that carries a
// non-positive id or translation_attempt, has no usable attempt identity and is
// treated as raw poison: the framework ACKs it without creating an outcome
// marker or publishing a ready message. A request with usable identity but a
// bad recipe_info is a generator-input failure handled through the empty-image
// workflow.
package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Request is the four-field translation request payload.
type Request struct {
	ID                 int             `json:"id"`
	TranslationAttempt int             `json:"translation_attempt"`
	RecipeInfo         json.RawMessage `json:"recipe_info"`
	ConfidenceMetric   *float64        `json:"confidence_metric"`
}

// ReadyPayload is the two-field ready-message payload.
type ReadyPayload struct {
	TranslationAttempt int    `json:"translation_attempt"`
	ContainerImage     string `json:"container_image"`
}

// DecodeRequest strictly decodes a translation request payload. It rejects
// malformed JSON, unknown fields, and trailing tokens. It does NOT validate
// positivity of id or translation_attempt; the caller classifies the request
// after decoding.
func DecodeRequest(data []byte) (*Request, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var req Request
	if err := dec.Decode(&req); err != nil {
		return nil, fmt.Errorf("decode translation request: %w", err)
	}
	// Reject trailing tokens after the single object.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err == nil {
		return nil, errors.New("decode translation request: unexpected trailing JSON content")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode translation request: %w", err)
	}
	return &req, nil
}

// HasUsableIdentity reports whether the decoded request carries a positive id
// and translation_attempt, the minimum identity needed to derive a ready
// subject and a deterministic registry tag. A request without usable identity
// is raw poison.
func (r *Request) HasUsableIdentity() bool {
	return r != nil && r.ID > 0 && r.TranslationAttempt > 0
}

// EncodeReady encodes a ready-message payload for publication. A success
// outcome carries a non-empty container_image (the digest reference); an
// empty-image failure outcome carries an empty container_image. The caller is
// responsible for publishing an empty image only after an empty-failure
// outcome marker.
func EncodeReady(attempt int, containerImage string) ([]byte, error) {
	if attempt <= 0 {
		return nil, fmt.Errorf("encode ready message: translation attempt must be positive")
	}
	payload := ReadyPayload{TranslationAttempt: attempt, ContainerImage: containerImage}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode ready message: %w", err)
	}
	return data, nil
}
