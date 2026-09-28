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

// Package subject defines the canonical alpha4 NATS subject grammar for the
// Scenario Manager's communication with the EDS, the Translator, and the
// PostProcessingService (PPS).
//
// The grammar is namespace-aware and fixed: every subject has the form
//
//	cbse.<namespace>.<project>.<domain>.<event>
//
// where <namespace> and <project> are each a single DNS label (no dots) and
// the remaining tokens are reserved names owned by this package. The
// Translator-ready and PPS-evaluation subjects carry a per-scenario id as
// their fourth token:
//
//	cbse.<namespace>.<project>.trans.<scenario-id>.ready
//	cbse.<namespace>.<project>.pps.<scenario-id>.evaluation
//
// The grammar is intentionally strict: project and namespace identifiers are
// never normalized (no lowercasing, no token replacement) and never contain
// dots, so a subject tokenizes to a fixed, unambiguous sequence.
package subject

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Domain identifies the three communication domains the Scenario Manager
// owns.
type Domain string

const (
	// DomainEDS is the EDS-to-Scenario-Manager domain. The Scenario Manager
	// answers availability requests and receives scenario batches from the
	// EDS, and publishes translation requests to the Translator.
	DomainEDS Domain = "eds"
	// DomainTranslator is the Scenario-Manager-to-Translator domain. The
	// Translator publishes readiness on TranslatorReadySubject and consumes
	// translation requests from TranslatorRequestSubject.
	DomainTranslator Domain = "trans"
	// DomainPPS is the Scenario-Manager-to-PostProcessingService domain. The
	// Scenario Manager publishes evaluation requests on PPSRequestSubject and
	// consumes evaluation verdicts from PPSEvaluationSubject.
	DomainPPS Domain = "pps"
)

// Event names the concrete subjects within a domain. EventRequest is shared
// by the Translator and PPS domains: the domain token disambiguates the two
// four-token request subjects in Parse.
type Event string

const (
	// EventBatch is the EDS batch subject:
	// cbse.<ns>.<project>.eds.scenarios.
	EventBatch Event = "scenarios"
	// EventAvailable is the EDS availability request/reply subject:
	// cbse.<ns>.<project>.eds.scenarios.available.
	EventAvailable Event = "available"
	// EventRequest is the translation request subject
	// cbse.<ns>.<project>.trans.request and the PPS evaluation request
	// subject cbse.<ns>.<project>.pps.request.
	EventRequest Event = "request"
	// EventReady is the Translator readiness subject:
	// cbse.<ns>.<project>.trans.<scenario-id>.ready.
	EventReady Event = "ready"
	// EventPPSEvaluation is the PPS evaluation verdict subject:
	// cbse.<ns>.<project>.pps.<scenario-id>.evaluation.
	EventPPSEvaluation Event = "evaluation"
)

// Ident is a validated namespace or project identifier: a single lowercase
// DNS label of 1-63 characters, matching the CRD admission rule.
type Ident string

// dnsLabelRe repeats the CRD admission rule for descriptive Operator-side
// errors: a lowercase DNS label of 1 to 63 characters.
var dnsLabelRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// ErrInvalidIdent is returned when a namespace or project identifier is not a
// single lowercase DNS label of 1-63 characters.
var ErrInvalidIdent = errors.New("alpha4 subject identifier must be a single lowercase DNS label (1-63 chars)")

// ValidateIdent validates that s is a single lowercase DNS label of 1-63
// characters. It performs NO normalization: no lowercasing, no token
// substitution, no trimming beyond rejecting the empty string. The caller is
// responsible for any desired canonicalization before validation; the Scenario
// Manager treats identifiers as opaque tokens.
func ValidateIdent(s string) (Ident, error) {
	// The CRD admission rule is a lowercase DNS label of 1-63 characters. The
	// character-class regex alone does not bound length, so enforce it here.
	if len(s) < 1 || len(s) > 63 || !dnsLabelRe.MatchString(s) {
		return "", fmt.Errorf("%w: %q", ErrInvalidIdent, s)
	}
	return Ident(s), nil
}

// String returns the identifier as a string.
func (i Ident) String() string { return string(i) }

// Subject is a parsed alpha4 subject. The populated fields depend on the
// subject's domain and event, as documented on each constructor and Parse.
type Subject struct {
	Namespace Ident
	Project   Ident
	Domain    Domain
	Event     Event
	// ReadyScenarioID is the per-scenario id token in a Translator-ready
	// subject (the <scenario-id> in cbse.<ns>.<project>.trans.<scenario-id>.ready).
	// It is populated only for TranslatorReady subjects.
	ReadyScenarioID string
	// EvaluationScenarioID is the per-scenario id token in a PPS-evaluation
	// subject (the <scenario-id> in
	// cbse.<ns>.<project>.pps.<scenario-id>.evaluation). It is validated as a
	// canonical positive integer (no sign, no leading zeros) and is populated
	// only for PPSEvaluation subjects.
	EvaluationScenarioID string
}

// EDSAvailabilitySubject is the Core NATS request/reply subject for an EDS
// availability probe of a given (namespace, project):
// cbse.<namespace>.<project>.eds.scenarios.available.
func EDSAvailabilitySubject(namespace, project Ident) string {
	return fmt.Sprintf("cbse.%s.%s.eds.scenarios.available", namespace, project)
}

// EDSBatchSubject is the subject on which the EDS publishes scenario batches for
// a given (namespace, project): cbse.<namespace>.<project>.eds.scenarios.
func EDSBatchSubject(namespace, project Ident) string {
	return fmt.Sprintf("cbse.%s.%s.eds.scenarios", namespace, project)
}

// TranslatorRequestSubject is the subject on which the Scenario Manager
// publishes a translation request for a given (namespace, project):
// cbse.<namespace>.<project>.trans.request.
func TranslatorRequestSubject(namespace, project Ident) string {
	return fmt.Sprintf("cbse.%s.%s.trans.request", namespace, project)
}

// TranslatorReadySubject is the subject on which the Translator publishes
// readiness for a given (namespace, project) and scenario id:
// cbse.<namespace>.<project>.trans.<scenario-id>.ready.
//
// scenarioID is the per-scenario id carried as the subject's fourth token. It
// must be non-empty; the Scenario Manager parses but does not otherwise
// constrain it.
func TranslatorReadySubject(namespace, project Ident, scenarioID string) string {
	return fmt.Sprintf("cbse.%s.%s.trans.%s.ready", namespace, project, scenarioID)
}

// TranslatorReadyWildcardSubject is the per-project wildcard subject used to
// purge all Translator ready messages for a (namespace, project) at deletion
// time: cbse.<namespace>.<project>.trans.*.ready.
func TranslatorReadyWildcardSubject(namespace, project Ident) string {
	return fmt.Sprintf("cbse.%s.%s.trans.*.ready", namespace, project)
}

// PPSRequestSubject is the subject on which the Scenario Manager publishes a
// scenario evaluation request for a given (namespace, project):
// cbse.<namespace>.<project>.pps.request.
func PPSRequestSubject(namespace, project Ident) string {
	return fmt.Sprintf("cbse.%s.%s.pps.request", namespace, project)
}

// PPSEvaluationSubject is the subject on which the PostProcessingService
// publishes an evaluation verdict for a given (namespace, project) and
// scenario id: cbse.<namespace>.<project>.pps.<scenario-id>.evaluation.
//
// scenarioID is the per-scenario id carried as the subject's fourth token. It
// must be a canonical positive decimal integer (no sign, no leading zeros);
// the subject is the transport of the evaluation round's scenario identity.
func PPSEvaluationSubject(namespace, project Ident, scenarioID string) string {
	return fmt.Sprintf("cbse.%s.%s.pps.%s.evaluation", namespace, project, scenarioID)
}

// PPSEvaluationWildcardSubject is the per-project wildcard subject used to
// purge all PPS evaluation verdicts for a (namespace, project) at deletion
// time: cbse.<namespace>.<project>.pps.*.evaluation.
func PPSEvaluationWildcardSubject(namespace, project Ident) string {
	return fmt.Sprintf("cbse.%s.%s.pps.*.evaluation", namespace, project)
}

// Wildcard subscriptions and stream subjects. The Scenario Manager derives
// these only by replacing the namespace, project, and scenario placeholders
// with *; callers do not configure a narrower namespace wildcard.
const (
	// EDSAvailabilityWildcard is the cluster-wide wildcard subscription for
	// EDS availability requests: cbse.*.*.eds.scenarios.available.
	EDSAvailabilityWildcard = "cbse.*.*.eds.scenarios.available"
	// EDSBatchStreamSubject is the JetStream stream subject for EDS batches:
	// cbse.*.*.eds.scenarios.
	EDSBatchStreamSubject = "cbse.*.*.eds.scenarios"
	// TranslatorRequestStreamSubject is the JetStream stream subject for
	// translation requests: cbse.*.*.trans.request.
	TranslatorRequestStreamSubject = "cbse.*.*.trans.request"
	// TranslatorReadyStreamSubject is the JetStream stream subject for
	// Translator readiness: cbse.*.*.trans.*.ready.
	TranslatorReadyStreamSubject = "cbse.*.*.trans.*.ready"
	// PPSRequestStreamSubject is the JetStream stream subject for PPS
	// evaluation requests: cbse.*.*.pps.request.
	PPSRequestStreamSubject = "cbse.*.*.pps.request"
	// PPSEvaluationStreamSubject is the JetStream stream subject for PPS
	// evaluation verdicts: cbse.*.*.pps.*.evaluation.
	PPSEvaluationStreamSubject = "cbse.*.*.pps.*.evaluation"
)

// errInvalidSubject is the base error for malformed subjects.
var errInvalidSubject = errors.New("invalid alpha4 subject")

// Parse parses an alpha4 subject produced by one of the constructors
// (EDSAvailabilitySubject, EDSBatchSubject, TranslatorRequestSubject,
// TranslatorReadySubject, PPSRequestSubject, PPSEvaluationSubject) into a
// Subject. It rejects any subject that does not match the canonical grammar.
//
// The namespace and project tokens are validated as DNS labels; they are NOT
// normalized. The <scenario-id> token in a ready subject is returned as a
// non-empty string but is not otherwise constrained.
func Parse(s string) (Subject, error) {
	const prefix = "cbse."
	if !strings.HasPrefix(s, prefix) {
		return Subject{}, fmt.Errorf("%w: missing cbse. prefix: %q", errInvalidSubject, s)
	}
	rest := s[len(prefix):]
	if rest == "" || strings.HasPrefix(rest, ".") || strings.HasSuffix(rest, ".") || strings.Contains(rest, "..") {
		return Subject{}, fmt.Errorf("%w: empty token: %q", errInvalidSubject, s)
	}
	tokens := strings.Split(rest, ".")
	// tokens[0]=namespace, tokens[1]=project, tokens[2]=domain, then 1-2 more.
	if len(tokens) < 4 {
		return Subject{}, fmt.Errorf("%w: too few tokens: %q", errInvalidSubject, s)
	}
	if len(tokens) > 5 {
		return Subject{}, fmt.Errorf("%w: too many tokens: %q", errInvalidSubject, s)
	}
	ns, err := ValidateIdent(tokens[0])
	if err != nil {
		return Subject{}, fmt.Errorf("%w: namespace: %v", errInvalidSubject, err)
	}
	proj, err := ValidateIdent(tokens[1])
	if err != nil {
		return Subject{}, fmt.Errorf("%w: project: %v", errInvalidSubject, err)
	}
	dom := Domain(tokens[2])
	switch dom {
	case DomainEDS:
		// EDS domain: tokens[3] must be "scenarios". A fifth token, if present,
		// must be "available".
		if Event(tokens[3]) != EventBatch {
			return Subject{}, fmt.Errorf("%w: not an EDS subject: %q", errInvalidSubject, s)
		}
		if len(tokens) == 4 {
			return Subject{Namespace: ns, Project: proj, Domain: dom, Event: EventBatch}, nil
		}
		// len == 5
		if Event(tokens[4]) != EventAvailable {
			return Subject{}, fmt.Errorf("%w: not an EDS availability subject: %q", errInvalidSubject, s)
		}
		return Subject{Namespace: ns, Project: proj, Domain: dom, Event: EventAvailable}, nil
	case DomainTranslator:
		switch len(tokens) {
		case 4: // cbse.<ns>.<proj>.trans.request
			if Event(tokens[3]) != EventRequest {
				return Subject{}, fmt.Errorf("%w: not a translator request subject: %q", errInvalidSubject, s)
			}
			return Subject{Namespace: ns, Project: proj, Domain: dom, Event: EventRequest}, nil
		case 5: // cbse.<ns>.<proj>.trans.<scenario-id>.ready
			if Event(tokens[4]) != EventReady {
				return Subject{}, fmt.Errorf("%w: not a translator ready subject: %q", errInvalidSubject, s)
			}
			scenarioID := tokens[3]
			if scenarioID == "" {
				return Subject{}, fmt.Errorf("%w: empty translator scenario-id token: %q", errInvalidSubject, s)
			}
			return Subject{Namespace: ns, Project: proj, Domain: dom, Event: EventReady, ReadyScenarioID: scenarioID}, nil
		default:
			return Subject{}, fmt.Errorf("%w: translator subject has wrong arity: %q", errInvalidSubject, s)
		}
	case DomainPPS:
		switch len(tokens) {
		case 4: // cbse.<ns>.<proj>.pps.request
			if Event(tokens[3]) != EventRequest {
				return Subject{}, fmt.Errorf("%w: not a pps request subject: %q", errInvalidSubject, s)
			}
			return Subject{Namespace: ns, Project: proj, Domain: dom, Event: EventRequest}, nil
		case 5: // cbse.<ns>.<proj>.pps.<scenario-id>.evaluation
			if Event(tokens[4]) != EventPPSEvaluation {
				return Subject{}, fmt.Errorf("%w: not a pps evaluation subject: %q", errInvalidSubject, s)
			}
			scenarioID := tokens[3]
			if !isCanonicalPositiveInt(scenarioID) {
				return Subject{}, fmt.Errorf("%w: pps evaluation scenario-id token %q must be a positive integer: %q", errInvalidSubject, scenarioID, s)
			}
			return Subject{Namespace: ns, Project: proj, Domain: dom, Event: EventPPSEvaluation, EvaluationScenarioID: scenarioID}, nil
		default:
			return Subject{}, fmt.Errorf("%w: pps subject has wrong arity: %q", errInvalidSubject, s)
		}
	default:
		return Subject{}, fmt.Errorf("%w: unknown domain %q: %q", errInvalidSubject, dom, s)
	}
}

// isCanonicalPositiveInt reports whether s is a canonical positive decimal
// integer: digits only, no sign, no leading zeros. It is the validation the
// PPS-evaluation subject applies to its <scenario-id> token, stricter than the
// Translator-ready token which is only required to be non-empty.
func isCanonicalPositiveInt(s string) bool {
	if s == "" || s[0] == '0' {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Identity carries the validated (namespace, project) pair extracted from a
// subject, used to resolve the exact persisted project row.
type Identity struct {
	Namespace Ident
	Project   Ident
}

// ParseIdentity parses an alpha4 subject and returns its (namespace, project)
// identity without inspecting the domain-specific event tokens beyond the
// minimum needed to confirm the subject is well-formed. It is a convenience
// for handlers that only need the identity.
func ParseIdentity(s string) (Identity, error) {
	parsed, err := Parse(s)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Namespace: parsed.Namespace, Project: parsed.Project}, nil
}

// String renders the identity as "<namespace>/<project>".
func (i Identity) String() string { return fmt.Sprintf("%s/%s", i.Namespace, i.Project) }
