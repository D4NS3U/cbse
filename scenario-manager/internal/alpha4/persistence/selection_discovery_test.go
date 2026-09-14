package persistence

import (
	"context"
	"testing"
	"time"
)

// The validation guards run before any database access, so a nil DB and a
// cancelled context exercise them without panicking or issuing SQL.

func TestNextCreatedScenarioForTranslationValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var db DB
	if _, err := NextCreatedScenarioForTranslation(ctx, db); err == nil {
		t.Fatal("cancelled ctx: want error")
	}
	if _, err := NextCreatedScenarioForTranslation(context.Background(), nil); err == nil {
		t.Fatal("nil db: want error")
	}
}

func TestNextStaleUnpublishedTranslationClaimValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var db DB
	if _, err := NextStaleUnpublishedTranslationClaim(ctx, db, time.Now()); err == nil {
		t.Fatal("cancelled ctx: want error")
	}
	if _, err := NextStaleUnpublishedTranslationClaim(context.Background(), nil, time.Now()); err == nil {
		t.Fatal("nil db: want error")
	}
	if _, err := NextStaleUnpublishedTranslationClaim(context.Background(), db, time.Time{}); err == nil {
		t.Fatal("zero claimedBefore: want error")
	}
}

// Ensure the discovery candidate structs compile and carry the documented
// fields so the selection loop can rely on them without importing coredb.
func TestTranslationCandidateFields(t *testing.T) {
	c := TranslationCandidate{ID: 7, ProjectNamespace: "ns", ProjectName: "proj", TranslationAttempt: 2}
	if c.ID != 7 || c.ProjectNamespace != "ns" || c.ProjectName != "proj" || c.TranslationAttempt != 2 {
		t.Fatalf("unexpected candidate: %+v", c)
	}
}

func TestStaleTranslationClaimFields(t *testing.T) {
	c := StaleTranslationClaim{ID: 9, TranslationAttempt: 4}
	if c.ID != 9 || c.TranslationAttempt != 4 {
		t.Fatalf("unexpected claim: %+v", c)
	}
}
