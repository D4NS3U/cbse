package subject

import "testing"

func TestParseRequestSubject(t *testing.T) {
	ns, proj, err := ParseRequestSubject("cbse.default.proj.trans.request")
	if err != nil || ns != "default" || proj != "proj" {
		t.Fatalf("ns/proj/err = %q/%q/%v", ns, proj, err)
	}
}

func TestParseRequestSubjectRejects(t *testing.T) {
	for _, s := range []string{
		"",
		"cbse.default.proj.other.request",
		"cbse.default.proj.trans.created",
		"x.default.proj.trans.request",
		"cbse..proj.trans.request",
		"cbse.default.proj.trans.request.extra",
		"cbse.default.proj.trans.request.",
		"default.proj.trans.request",
		"cbse.default.proj.trans",
	} {
		t.Run(s, func(t *testing.T) {
			if _, _, err := ParseRequestSubject(s); err == nil {
				t.Fatalf("ParseRequestSubject(%q) must be rejected", s)
			}
		})
	}
}

func TestValidateReadyTemplate(t *testing.T) {
	if err := ValidateReadyTemplate("cbse.{namespace}.{project}.trans.{scenario_id}.ready"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReadyTemplate("cbse.{namespace}.{project}.trans.{scenario_id}.created"); err == nil {
		t.Fatal("non-ready template must be rejected")
	}
	if err := ValidateReadyTemplate("cbse.{namespace}.{project}.trans.{scenario}.ready"); err == nil {
		t.Fatal("wrong placeholder must be rejected")
	}
}

func TestReadySubject(t *testing.T) {
	if got := ReadySubject("ns", "proj", 42); got != "cbse.ns.proj.trans.42.ready" {
		t.Fatalf("ReadySubject = %q", got)
	}
}

func TestValidateIdent(t *testing.T) {
	for _, s := range []string{"a", "ns", "proj", "a-b", "abc123", strings63()} {
		if err := ValidateIdent(s); err != nil {
			t.Errorf("ValidateIdent(%q) = %v", s, err)
		}
	}
	for _, s := range []string{"", "-a", "a-", "A", "a.b", strings64(), "a_b"} {
		if err := ValidateIdent(s); err == nil {
			t.Errorf("ValidateIdent(%q) must be rejected", s)
		}
	}
}

func TestValidateScenarioIDToken(t *testing.T) {
	if err := ValidateScenarioIDToken("42"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"", "4.2", "4*2", "4 2"} {
		if err := ValidateScenarioIDToken(s); err == nil {
			t.Errorf("ValidateScenarioIDToken(%q) must be rejected", s)
		}
	}
}

func strings63() string {
	b := make([]byte, 63)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
func strings64() string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
