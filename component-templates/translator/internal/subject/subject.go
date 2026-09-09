// Package subject ports the subset of the canonical alpha4 NATS subject
// grammar the reference Translator framework needs to validate its startup
// configuration and to derive ready subjects from request scenario IDs. The
// framework is a standalone Go module that cannot import the Scenario
// Manager's internal subject package, so it carries its own copy of the
// namespace-aware three-token grammar:
//
//	cbse.<namespace>.<project>.trans.request
//	cbse.<namespace>.<project>.trans.<scenario-id>.ready
//
// <namespace> and <project> are each a single lowercase DNS label (1-63 chars,
// no dots). <scenario-id> is the decimal string of the validated positive
// integer request id, which is always a single dot-free token. Identifiers are
// never normalized.
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

// ValidateIdent validates that s is a single lowercase DNS label of 1-63
// characters. It performs NO normalization.
func ValidateIdent(s string) error {
	if len(s) < 1 || len(s) > 63 || !dnsLabelRe.MatchString(s) {
		return fmt.Errorf("alpha4 subject identifier must be a single lowercase DNS label (1-63 chars): %q", s)
	}
	return nil
}

// ParseRequestSubject validates that s is the canonical Translator request
// subject cbse.<namespace>.<project>.trans.request and returns the parsed
// namespace and project identifiers.
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
		return "", "", fmt.Errorf("invalid alpha4 request subject: want 4 tokens cbse.<ns>.<project>.trans.request, got %q", s)
	}
	if tokens[2] != "trans" {
		return "", "", fmt.Errorf("invalid alpha4 request subject: domain is not %q: %q", "trans", s)
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

// canonicalReadyTemplate is the canonical alpha4 Translator ready-subject
// template, using the exact placeholder names the Operator injects.
const canonicalReadyTemplate = "cbse.{namespace}.{project}.trans.{scenario_id}.ready"

// ValidateReadyTemplate validates that t is the canonical alpha4 ready-subject
// template cbse.{namespace}.{project}.trans.{scenario_id}.ready. The framework
// accepts only the canonical template so it never publishes outside the
// injected namespace or project.
func ValidateReadyTemplate(t string) error {
	if t != canonicalReadyTemplate {
		return fmt.Errorf("invalid alpha4 ready-subject template: want %q, got %q", canonicalReadyTemplate, t)
	}
	return nil
}

// ReadySubject derives the concrete ready subject for a request scenario ID by
// substituting the validated namespace, project, and decimal scenario ID into
// the canonical ready-subject template. scenarioID must be positive; the
// caller validates the request id before calling.
func ReadySubject(namespace, project string, scenarioID int) string {
	return fmt.Sprintf("cbse.%s.%s.trans.%s.ready", namespace, project, strconv.Itoa(scenarioID))
}

// ValidateScenarioIDToken validates that the decimal scenario-id token derived
// from a positive request id is a single dot-free, wildcard-free NATS token.
// A positive integer's decimal string always satisfies this; the function is
// defensive against future changes to id formatting.
func ValidateScenarioIDToken(token string) error {
	if token == "" {
		return fmt.Errorf("scenario-id token must not be empty")
	}
	if strings.ContainsAny(token, ".* \t\r\n") {
		return fmt.Errorf("scenario-id token must not contain '.', '*', or whitespace: %q", token)
	}
	return nil
}
