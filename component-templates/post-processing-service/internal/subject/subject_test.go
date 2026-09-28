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
	"strings"
	"testing"
)

func TestParseRequestSubject(t *testing.T) {
	ns, proj, err := ParseRequestSubject("cbse.default.proj.pps.request")
	if err != nil {
		t.Fatalf("valid subject rejected: %v", err)
	}
	if ns != "default" || proj != "proj" {
		t.Fatalf("parsed %q/%q, want default/proj", ns, proj)
	}
	ns, proj, err = ParseRequestSubject("cbse.a-b.c9.pps.request")
	if err != nil {
		t.Fatalf("valid subject rejected: %v", err)
	}
	if ns != "a-b" || proj != "c9" {
		t.Fatalf("parsed %q/%q, want a-b/c9", ns, proj)
	}
}

func TestParseRequestSubjectRejects(t *testing.T) {
	for _, s := range []string{
		"",
		"default.proj.pps.request",
		"cbse.default.proj.pps.req",
		"cbse.default.proj.pps.request.extra",
		"cbse..proj.pps.request",
		"cbse.default..pps.request",
		"cbse.default.proj.pps.request.",
		".cbse.default.proj.pps.request",
		"cbse.Default.proj.pps.request",
		"cbse.default.Proj.pps.request",
		"cbse.default.proj.trans.request",
		"cbse.default.proj.pps.ready",
		"cbse.default.proj..pps.request",
	} {
		if _, _, err := ParseRequestSubject(s); err == nil {
			t.Fatalf("invalid subject %q accepted", s)
		}
	}
}

func TestValidateEvaluationTemplate(t *testing.T) {
	ns, proj, err := ValidateEvaluationTemplate("cbse.default.proj.pps.%s.evaluation")
	if err != nil {
		t.Fatalf("valid template rejected: %v", err)
	}
	if ns != "default" || proj != "proj" {
		t.Fatalf("embedded %q/%q, want default/proj", ns, proj)
	}
	ns, proj, err = ValidateEvaluationTemplate("cbse.a-b.c9.pps.%s.evaluation")
	if err != nil {
		t.Fatalf("valid template rejected: %v", err)
	}
	if ns != "a-b" || proj != "c9" {
		t.Fatalf("embedded %q/%q, want a-b/c9", ns, proj)
	}
}

func TestValidateEvaluationTemplateRejects(t *testing.T) {
	for _, s := range []string{
		"",
		"cbse.default.proj.pps.evaluation",       // missing %s
		"cbse.default.proj.pps.%s.%s.evaluation", // two placeholders
		"cbse.default.proj.pps.42.evaluation",    // literal id, no placeholder
		"cbse.%s.proj.pps.%s.evaluation",         // placeholder in namespace
		"cbse.default.%s.pps.%s.evaluation",      // placeholder in project
		"cbse.default.proj.trans.%s.evaluation",  // wrong domain
		"cbse.default.proj.pps.%s.ready",         // wrong event
		"cbse.default.proj.pps.request",          // request subject, not a template
		"cbse..proj.pps.%s.evaluation",           // empty namespace token
		"cbse.default..pps.%s.evaluation",        // empty project token
		"cbse.Default.proj.pps.%s.evaluation",    // uppercase namespace
		"cbse.default.proj.pps.%s.evaluation.",   // trailing dot
		".cbse.default.proj.pps.%s.evaluation",   // leading dot
	} {
		if _, _, err := ValidateEvaluationTemplate(s); err == nil {
			t.Fatalf("invalid template %q accepted", s)
		}
	}
}

func TestEvaluationSubject(t *testing.T) {
	got, err := EvaluationSubject("cbse.default.proj.pps.%s.evaluation", 42)
	if err != nil {
		t.Fatalf("derivation failed: %v", err)
	}
	if got != "cbse.default.proj.pps.42.evaluation" {
		t.Fatalf("subject = %q", got)
	}
	got, err = EvaluationSubject("cbse.a-b.c9.pps.%s.evaluation", 1)
	if err != nil {
		t.Fatalf("derivation failed: %v", err)
	}
	if got != "cbse.a-b.c9.pps.1.evaluation" {
		t.Fatalf("subject = %q", got)
	}
}

func TestEvaluationSubjectRejects(t *testing.T) {
	for _, s := range []string{
		"cbse.default.proj.pps.evaluation",
		"cbse.default.proj.pps.%s.%s.evaluation",
	} {
		if _, err := EvaluationSubject(s, 42); err == nil {
			t.Fatalf("invalid template %q accepted", s)
		}
	}
	for _, id := range []int64{0, -1} {
		if _, err := EvaluationSubject("cbse.default.proj.pps.%s.evaluation", id); err == nil {
			t.Fatalf("scenario id %d accepted", id)
		}
	}
}

func TestValidateIdent(t *testing.T) {
	for _, s := range []string{"a", "ab", "a-b", "a9", "0", "abcdefghij", "a" + strings.Repeat("1", 62)} {
		if err := ValidateIdent(s); err != nil {
			t.Fatalf("valid ident %q rejected: %v", s, err)
		}
	}
	for _, s := range []string{"", "A", "ab-", "-ab", "a_b", "a.b", "a b", "A9", "a b-c"} {
		if err := ValidateIdent(s); err == nil {
			t.Fatalf("invalid ident %q accepted", s)
		}
	}
	if err := ValidateIdent("a" + strings.Repeat("1", 63)); err == nil {
		t.Fatal("64-char ident accepted")
	}
}

func TestValidateScenarioIDToken(t *testing.T) {
	for _, s := range []string{"1", "42", "999999999999"} {
		if err := ValidateScenarioIDToken(s); err != nil {
			t.Fatalf("valid token %q rejected: %v", s, err)
		}
	}
	for _, s := range []string{"", "4.2", "*42", "4 2", "-1", "42\t"} {
		if err := ValidateScenarioIDToken(s); err == nil {
			t.Fatalf("invalid token %q accepted", s)
		}
	}
}
