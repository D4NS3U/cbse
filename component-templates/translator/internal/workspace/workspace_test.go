package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAttemptDir(t *testing.T) {
	w := New("/workspace")
	if got := w.AttemptDir(7, 2); got != "/workspace/scenario-7/attempt-2" {
		t.Fatalf("AttemptDir = %q", got)
	}
}

func TestMarkerAtomicWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	m := Marker{
		Outcome:       OutcomeSuccess,
		ScenarioID:    7,
		Attempt:       2,
		ReadySubject:  "cbse.ns.proj.trans.7.ready",
		ExperimentUID: "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		Tag:           "repo:runner-x-s7-a2",
		Digest:        "sha256:abc",
	}
	if err := WriteMarker(dir, m); err != nil {
		t.Fatal(err)
	}
	// The temp file must not linger after a successful rename.
	if _, err := os.Stat(filepath.Join(dir, markerTmpName)); err == nil {
		t.Fatal("temp marker file lingered after rename")
	}
	got, ok, err := ReadMarker(dir)
	if err != nil || !ok {
		t.Fatalf("ReadMarker ok=%v err=%v", ok, err)
	}
	if got.Tag != "repo:runner-x-s7-a2" || got.Digest != "sha256:abc" || got.Outcome != OutcomeSuccess {
		t.Fatalf("read marker = %+v", got)
	}
}

func TestMarkerValidation(t *testing.T) {
	uid := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	rs := "cbse.ns.proj.trans.7.ready"
	good := Marker{Outcome: OutcomeSuccess, ScenarioID: 7, Attempt: 2, ReadySubject: rs, ExperimentUID: uid, Tag: "t", Digest: "d"}
	if err := ValidateMarker(&good, uid, 7, 2, rs); err != nil {
		t.Fatalf("valid success marker rejected: %v", err)
	}
	empty := Marker{Outcome: OutcomeEmptyFailure, ScenarioID: 7, Attempt: 2, ReadySubject: rs, ExperimentUID: uid, EmptyImage: true, FailureClass: "build"}
	if err := ValidateMarker(&empty, uid, 7, 2, rs); err != nil {
		t.Fatalf("valid empty marker rejected: %v", err)
	}
	// Mismatched identity.
	if err := ValidateMarker(&good, "other-uid", 7, 2, rs); err == nil {
		t.Fatal("mismatched UID must be rejected")
	}
	if err := ValidateMarker(&good, uid, 8, 2, rs); err == nil {
		t.Fatal("mismatched scenario must be rejected")
	}
	if err := ValidateMarker(&good, uid, 7, 3, rs); err == nil {
		t.Fatal("mismatched attempt must be rejected")
	}
	if err := ValidateMarker(&good, uid, 7, 2, "other.ready"); err == nil {
		t.Fatal("mismatched ready subject must be rejected")
	}
}

func TestMarkerFieldsRejectInvalid(t *testing.T) {
	dir := t.TempDir()
	cases := []Marker{
		{Outcome: "bogus", ScenarioID: 1, Attempt: 1, ReadySubject: "r", ExperimentUID: "u"},
		{Outcome: OutcomeSuccess, ScenarioID: 1, Attempt: 1, ReadySubject: "r", ExperimentUID: "u"}, // missing tag/digest
		{Outcome: OutcomeSuccess, ScenarioID: 1, Attempt: 1, ReadySubject: "r", ExperimentUID: "u", Tag: "t", Digest: "d", EmptyImage: true},
		{Outcome: OutcomeEmptyFailure, ScenarioID: 1, Attempt: 1, ReadySubject: "r", ExperimentUID: "u", EmptyImage: true}, // missing failure class
		{Outcome: OutcomeEmptyFailure, ScenarioID: 1, Attempt: 1, ReadySubject: "r", ExperimentUID: "u", EmptyImage: true, FailureClass: "build", Tag: "t"},
	}
	for i, m := range cases {
		if err := WriteMarker(dir, m); err == nil {
			t.Fatalf("case %d: invalid marker accepted: %+v", i, m)
		}
	}
}

func TestReadMarkerMissing(t *testing.T) {
	got, ok, err := ReadMarker(t.TempDir())
	if err != nil || ok || got != nil {
		t.Fatalf("ReadMarker missing = %v %v %v", got, ok, err)
	}
}

func TestBuildInput(t *testing.T) {
	dir := t.TempDir()
	if BuildInputExists(dir) {
		t.Fatal("empty dir must not have build input")
	}
	if err := os.MkdirAll(filepath.Join(dir, "runner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !BuildInputExists(dir) {
		t.Fatal("runner/ dir must count as build input")
	}
	if err := RemoveBuildInput(dir); err != nil {
		t.Fatal(err)
	}
	if BuildInputExists(dir) {
		t.Fatal("RemoveBuildInput must remove runner/")
	}
}

func TestRemoveAttemptDir(t *testing.T) {
	w := New(t.TempDir())
	dir, err := w.EnsureAttemptDir(7, 2)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Marshal // keep import
	_ = dir
	if err := w.RemoveAttemptDir(7, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.AttemptDir(7, 2)); !os.IsNotExist(err) {
		t.Fatal("attempt dir must be removed")
	}
}
