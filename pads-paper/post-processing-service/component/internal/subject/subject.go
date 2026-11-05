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

// Package subject carries the subset of the canonical alpha4 NATS subject
// grammar the reference PostProcessingService (PPS) needs to validate its
// startup configuration and to derive evaluation verdict subjects from
// request scenario IDs. The module is a standalone Go module that cannot
// import the Scenario Manager's internal subject package, so it carries its
// own copy of the namespace-aware PPS grammar:
//
//	cbse.<namespace>.<project>.pps.request
//	cbse.<namespace>.<project>.pps.<scenario-id>.evaluation
//
// The request subject is fixed per experiment; the evaluation subject is
// derived per scenario. The Operator injects the evaluation subject template
// cbse.<namespace>.<project>.pps.%s.evaluation with the concrete namespace
// and project substituted and the single %s placeholder left for the
// scenario id, which the PPS substitutes at publish time. <namespace> and
// <project> are each a single lowercase DNS label (1-63 chars, no dots);
// <scenario-id> is the decimal string of a validated positive integer, which
// is always a single dot-free token. Identifiers are never normalized.
package subject

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// dnsLabelRe repeats the CRD admission rule: a lowercase DNS label of 1-63
// characters.
var dnsLabelRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// evalTemplateRe matches the canonical PPS evaluation-subject template
// cbse.<ns>.<proj>.pps.%s.evaluation: the cbse. prefix, two DNS-label tokens
// (namespace, project), the pps domain, the single %s scenario-id placeholder,
// and the evaluation event.
var evalTemplateRe = regexp.MustCompile(`^cbse\.([a-z0-9]([-a-z0-9]*[a-z0-9])?)\.([a-z0-9]([-a-z0-9]*[a-z0-9])?)\.pps\.%s\.evaluation$`)

// ValidateIdent validates that s is a single lowercase DNS label of 1-63
// characters. It performs NO normalization.
func ValidateIdent(s string) error {
	if len(s) < 1 || len(s) > 63 || !dnsLabelRe.MatchString(s) {
		return fmt.Errorf("alpha4 subject identifier must be a single lowercase DNS label (1-63 chars): %q", s)
	}
	return nil
}

// ParseRequestSubject validates that s is the canonical PPS request subject
// cbse.<namespace>.<project>.pps.request and returns the parsed namespace and
// project identifiers.
func ParseRequestSubject(s string) (namespace, project string, err error) {
	const prefix = "cbse."
	if !strings.HasPrefix(s, prefix) {
		return "", "", fmt.Errorf("invalid alpha4 request subject: missing cbse. prefix: %q", s)
	}
	rest := s[len(prefix):]
	if rest == "" || strings.HasPrefix(rest, ".") || strings.HasSuffix(rest, ".") || strings.Contains(rest, "..") {
		return "", "", fmt.Errorf("invalid alpha4 request subject: empty token: %q", s)
	}
	tokens := strings.Split(rest, ".")
	if len(tokens) != 4 {
		return "", "", fmt.Errorf("invalid alpha4 request subject: want 4 tokens cbse.<ns>.<project>.pps.request, got %q", s)
	}
	if tokens[2] != "pps" {
		return "", "", fmt.Errorf("invalid alpha4 request subject: domain is not %q: %q", "pps", s)
	}
	if tokens[3] != "request" {
		return "", "", fmt.Errorf("invalid alpha4 request subject: event is not %q: %q", "request", s)
	}
	if err := ValidateIdent(tokens[0]); err != nil {
		return "", "", fmt.Errorf("invalid alpha4 request subject namespace: %w", err)
	}
	if err := ValidateIdent(tokens[1]); err != nil {
		return "", "", fmt.Errorf("invalid alpha4 request subject project: %w", err)
	}
	return tokens[0], tokens[1], nil
}

// ValidateEvaluationTemplate validates that t is a canonical PPS
// evaluation-subject template: cbse.<namespace>.<project>.pps.%s.evaluation
// with the concrete namespace and project tokens and exactly one %s
// scenario-id placeholder. It returns the embedded namespace and project so
// the caller can cross-check them against the pod identity. The PPS accepts
// only canonical templates so it never publishes outside the injected
// namespace or project.
func ValidateEvaluationTemplate(t string) (namespace, project string, err error) {
	m := evalTemplateRe.FindStringSubmatch(t)
	if m == nil {
		return "", "", fmt.Errorf("invalid alpha4 evaluation-subject template: want cbse.<namespace>.<project>.pps.%%s.evaluation, got %q", t)
	}
	return m[1], m[3], nil
}

// EvaluationSubject derives the concrete evaluation subject for a scenario ID
// by substituting the decimal scenario ID into the validated template's %s
// placeholder. scenarioID must be positive; it is rejected here defensively
// (the caller validates the request scenario id before calling).
func EvaluationSubject(template string, scenarioID int64) (string, error) {
	if _, _, err := ValidateEvaluationTemplate(template); err != nil {
		return "", err
	}
	if scenarioID <= 0 {
		return "", fmt.Errorf("scenario id %d must be > 0", scenarioID)
	}
	token := strconv.FormatInt(scenarioID, 10)
	if err := ValidateScenarioIDToken(token); err != nil {
		return "", err
	}
	return fmt.Sprintf(template, token), nil
}

// ValidateScenarioIDToken validates that the decimal scenario-id token derived
// from a positive request id is a single non-empty ASCII digit run: dot-free,
// wildcard-free, whitespace-free, and sign-free. A positive integer's decimal
// string always satisfies this; the function is defensive against future
// changes to id formatting. Positivity itself is the caller's responsibility
// (the request scenario id is validated positive before tokenization).
func ValidateScenarioIDToken(token string) error {
	if token == "" {
		return fmt.Errorf("scenario-id token must not be empty")
	}
	for _, r := range token {
		if r < '0' || r > '9' {
			return fmt.Errorf("scenario-id token must be decimal digits only: %q", token)
		}
	}
	return nil
}
