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

package subject

import (
	"errors"
	"testing"
)

func TestValidateIdent(t *testing.T) {
	cases := []struct {
		in   string
		want string // expected value, or "" if error
		err  bool
	}{
		{in: "default", want: "default"},
		{in: "my-project", want: "my-project"},
		{in: "p1", want: "p1"},
		{in: "a", want: "a"},
		{in: "", err: true},
		{in: ".foo", err: true},
		{in: "foo.", err: true},
		{in: "foo.bar", err: true},    // dots not allowed (single label)
		{in: "FOO", err: true},        // uppercase not normalized/allowed
		{in: "-foo", err: true},       // leading dash
		{in: "foo-", err: true},       // trailing dash
		{in: "has space", err: true},  // whitespace
		{in: "snake_case", err: true}, // underscore not a DNS char
		{in: pad(63), want: pad(63)},  // exactly 63 chars
		{in: pad(64), err: true},      // 64 chars too long
	}
	for _, c := range cases {
		got, err := ValidateIdent(c.in)
		if c.err {
			if err == nil {
				t.Errorf("ValidateIdent(%q) = %q; want error", c.in, got)
			}
			if !errors.Is(err, ErrInvalidIdent) {
				t.Errorf("ValidateIdent(%q) err = %v; want ErrInvalidIdent", c.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ValidateIdent(%q) err = %v; want nil", c.in, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("ValidateIdent(%q) = %q; want %q", c.in, got, c.want)
		}
		// No normalization: input must equal output.
		if string(got) != c.in {
			t.Errorf("ValidateIdent normalized %q to %q", c.in, got)
		}
	}
}

// pad returns an n-length string of 'a' bytes, used to exercise identifier
// length boundaries (e.g. the 63-character DNS-label limit).
func pad(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

func TestConstructorsAndParseRoundtrip(t *testing.T) {
	ns, proj := Ident("default"), Ident("smoke-project")

	avail := EDSAvailabilitySubject(ns, proj)
	if avail != "cbse.default.smoke-project.eds.scenarios.available" {
		t.Fatalf("EDSAvailabilitySubject = %q", avail)
	}
	batch := EDSBatchSubject(ns, proj)
	if batch != "cbse.default.smoke-project.eds.scenarios" {
		t.Fatalf("EDSBatchSubject = %q", batch)
	}
	req := TranslatorRequestSubject(ns, proj)
	if req != "cbse.default.smoke-project.trans.request" {
		t.Fatalf("TranslatorRequestSubject = %q", req)
	}
	ready := TranslatorReadySubject(ns, proj, "scen-7")
	if ready != "cbse.default.smoke-project.trans.scen-7.ready" {
		t.Fatalf("TranslatorReadySubject = %q", ready)
	}
	evalReq := PPSRequestSubject(ns, proj)
	if evalReq != "cbse.default.smoke-project.pps.request" {
		t.Fatalf("PPSRequestSubject = %q", evalReq)
	}
	eval := PPSEvaluationSubject(ns, proj, "42")
	if eval != "cbse.default.smoke-project.pps.42.evaluation" {
		t.Fatalf("PPSEvaluationSubject = %q", eval)
	}
	if got := PPSEvaluationWildcardSubject(ns, proj); got != "cbse.default.smoke-project.pps.*.evaluation" {
		t.Fatalf("PPSEvaluationWildcardSubject = %q", got)
	}

	for _, s := range []string{avail, batch, req, ready, evalReq, eval} {
		p, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q) err = %v", s, err)
		}
		if p.Namespace != ns || p.Project != proj {
			t.Fatalf("Parse(%q) = %+v; want ns=%s proj=%s", s, p, ns, proj)
		}
	}

	p, err := Parse(ready)
	if err != nil {
		t.Fatalf("Parse(ready) err = %v", err)
	}
	if p.ReadyScenarioID != "scen-7" {
		t.Fatalf("ReadyScenarioID = %q; want scen-7", p.ReadyScenarioID)
	}
	if p.Event != EventReady {
		t.Fatalf("Event = %q; want ready", p.Event)
	}
	// The PPS evaluation subject parses with a positive-integer scenario id.
	ep, err := Parse(eval)
	if err != nil {
		t.Fatalf("Parse(evaluation) err = %v", err)
	}
	if ep.Domain != DomainPPS || ep.Event != EventPPSEvaluation {
		t.Fatalf("evaluation parse = %+v; want domain=pps event=evaluation", ep)
	}
	if ep.EvaluationScenarioID != "42" {
		t.Fatalf("EvaluationScenarioID = %q; want 42", ep.EvaluationScenarioID)
	}
	if ep.ReadyScenarioID != "" {
		t.Fatalf("ReadyScenarioID must stay empty for PPS subjects: %q", ep.ReadyScenarioID)
	}
	epr, err := Parse(evalReq)
	if err != nil {
		t.Fatalf("Parse(pps request) err = %v", err)
	}
	if epr.Domain != DomainPPS || epr.Event != EventRequest {
		t.Fatalf("pps request parse = %+v; want domain=pps event=request", epr)
	}
	if got, _ := Parse(avail); got.Event != EventAvailable {
		t.Fatalf("avail Event = %q; want available", got.Event)
	}
	if got, _ := Parse(batch); got.Event != EventBatch {
		t.Fatalf("batch Event = %q; want scenarios", got.Event)
	}
	if got, _ := Parse(req); got.Event != EventRequest {
		t.Fatalf("req Event = %q; want request", got.Event)
	}
}

func TestWildcards(t *testing.T) {
	want := map[string]string{
		EDSAvailabilityWildcard:        "cbse.*.*.eds.scenarios.available",
		EDSBatchStreamSubject:          "cbse.*.*.eds.scenarios",
		TranslatorRequestStreamSubject: "cbse.*.*.trans.request",
		TranslatorReadyStreamSubject:   "cbse.*.*.trans.*.ready",
		PPSRequestStreamSubject:        "cbse.*.*.pps.request",
		PPSEvaluationStreamSubject:     "cbse.*.*.pps.*.evaluation",
	}
	for got, w := range want {
		if got != w {
			t.Fatalf("wildcard %q != %q", got, w)
		}
	}
	// Wildcards are NOT parseable identities (they contain '*' which is not a
	// DNS label). Parse must reject them rather than silently accept.
	for _, s := range []string{EDSAvailabilityWildcard, EDSBatchStreamSubject, TranslatorRequestStreamSubject, TranslatorReadyStreamSubject, PPSRequestStreamSubject, PPSEvaluationStreamSubject} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) succeeded; want error", s)
		}
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	bad := []string{
		"",                                   // empty
		"cbse.",                              // empty after prefix
		"foo.default.p.eds.scenarios",        // no prefix
		"cbse.default..eds.scenarios",        // empty project token
		"cbse..p.eds.scenarios",              // empty namespace token
		"cbse.default.p.eds",                 // too few tokens
		"cbse.default.p.eds.scenarios.extra", // too many tokens (eds batch)
		"cbse.default.p.eds.scenarios.available.x", // too many (eds availability)
		"cbse.default.p.trans",                     // too few for trans
		"cbse.default.p.trans.request.x",           // too many for request
		"cbse.default.p.trans.scen.ready.x",        // too many for ready
		"cbse.default.p.unknown.scenarios",         // unknown domain
		"cbse.default.p.eds.request",               // eds domain but 3rd token not scenarios
		"cbse.default.p.eds.scenarios.unavailable", // eds 5th token not available
		"cbse.default.p.trans.scenarios",           // trans domain, 4 tokens but not request
		"cbse.default.p.trans..ready",              // empty scenario-id token
		"cbse.default.p.eds.scenarios.",            // trailing dot
		"cbse.default.p.pps",                       // too few for pps
		"cbse.default.p.pps.request.x",             // too many for pps request
		"cbse.default.p.pps.scen.ready",            // wrong 5th token for evaluation
		"cbse.default.p.pps.scen.evaluation.x",     // too many for evaluation
		"cbse.default.p.pps.request.ready",         // 5 tokens but 4th not scenario-id form
		"cbse.default.p.pps.evaluation",            // 4 tokens but not request
		"cbse.default.p.pps.-7.evaluation",         // signed scenario-id
		"cbse.default.p.pps.0.evaluation",          // zero scenario-id
		"cbse.default.p.pps.007.evaluation",        // leading zeros
		"cbse.default.p.pps.7x.evaluation",         // non-digit scenario-id
		"cbse.DEFAULT.p.eds.scenarios",             // uppercase namespace
	}
	for _, s := range bad {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) succeeded; want error", s)
		}
	}
}

func TestParseIdentity(t *testing.T) {
	id, err := ParseIdentity("cbse.ns.proj.eds.scenarios")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id.Namespace != "ns" || id.Project != "proj" {
		t.Fatalf("identity = %+v", id)
	}
	if got := id.String(); got != "ns/proj" {
		t.Fatalf("String = %q", got)
	}
	if _, err := ParseIdentity("not-a-subject"); err == nil {
		t.Fatal("ParseIdentity(bad) succeeded")
	}
}
