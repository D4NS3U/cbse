package imageref

import (
	"strings"
	"testing"
)

func TestUIDPrefix(t *testing.T) {
	cases := []struct {
		uid  string
		want string
	}{
		{"a1b2c3d4-e5f6-7890-abcd-ef1234567890", "a1b2c3d4e5f6"},
		{"A1B2C3D4-E5F6-7890-ABCD-EF1234567890", "a1b2c3d4e5f6"},
		{"abcd", "abcd"},
		{"", ""},
		{"a-b-c-d-e-f-1-2-3", "abcdef123"},
	}
	for _, tc := range cases {
		if got := UIDPrefix(tc.uid); got != tc.want {
			t.Errorf("UIDPrefix(%q) = %q, want %q", tc.uid, got, tc.want)
		}
	}
}

func TestTag(t *testing.T) {
	got := Tag("registry.example.com/proj/runners", "a1b2c3d4e5f6", 7, 2)
	want := "registry.example.com/proj/runners:runner-a1b2c3d4e5f6-s7-a2"
	if got != want {
		t.Fatalf("Tag = %q, want %q", got, want)
	}
}

func TestAnnotationsAndVerify(t *testing.T) {
	ann := Annotations("a1b2c3d4-e5f6-7890-abcd-ef1234567890", 7, 2)
	if ann[AnnotationExperimentUID] != "a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Fatalf("experiment-uid annotation = %q", ann[AnnotationExperimentUID])
	}
	if ann[AnnotationScenarioID] != "7" || ann[AnnotationTranslationAttempt] != "2" {
		t.Fatalf("scenario-id/attempt annotations = %q/%q", ann[AnnotationScenarioID], ann[AnnotationTranslationAttempt])
	}
	if err := VerifyAnnotations(ann, "a1b2c3d4-e5f6-7890-abcd-ef1234567890", 7, 2); err != nil {
		t.Fatalf("VerifyAnnotations matched: %v", err)
	}
}

func TestVerifyAnnotationsMismatch(t *testing.T) {
	uid := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	// Missing annotation.
	miss := map[string]string{AnnotationExperimentUID: uid, AnnotationScenarioID: "7"}
	if err := VerifyAnnotations(miss, uid, 7, 2); err == nil {
		t.Fatal("missing annotation must mismatch")
	}
	// Wrong UID.
	wrong := Annotations("deadbeef-0000-0000-0000-000000000000", 7, 2)
	if err := VerifyAnnotations(wrong, uid, 7, 2); err == nil {
		t.Fatal("wrong UID must mismatch")
	}
	// Wrong scenario.
	wrongS := Annotations(uid, 8, 2)
	if err := VerifyAnnotations(wrongS, uid, 7, 2); err == nil {
		t.Fatal("wrong scenario must mismatch")
	}
	// Wrong attempt.
	wrongA := Annotations(uid, 7, 3)
	if err := VerifyAnnotations(wrongA, uid, 7, 2); err == nil {
		t.Fatal("wrong attempt must mismatch")
	}
}

func TestRepositoryAndSplit(t *testing.T) {
	repo, tag, err := SplitRepositoryTag("registry.example.com/proj/runners:runner-x-s1-a1")
	if err != nil || repo != "registry.example.com/proj/runners" || tag != "runner-x-s1-a1" {
		t.Fatalf("SplitRepositoryTag = %q %q %v", repo, tag, err)
	}
	if _, _, err := SplitRepositoryTag("repo@sha256:abc"); err == nil {
		t.Fatal("digest ref must be rejected")
	}
	if !strings.Contains(DigestRef("repo", "sha256:abc"), "repo@sha256:abc") {
		t.Fatal("DigestRef mismatch")
	}
	if RepositoryOf("REGISTRY.example.com/Proj@sha256:abc") != "registry.example.com/proj" {
		t.Fatalf("RepositoryOf(digest) not normalized: %q", RepositoryOf("REGISTRY.example.com/Proj@sha256:abc"))
	}
	if RepositoryOf("registry.example.com/proj:tag") != "registry.example.com/proj" {
		t.Fatalf("RepositoryOf(tag) mismatch")
	}
}
