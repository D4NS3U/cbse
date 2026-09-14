package persistence

import (
	"context"
	"testing"
)

// The validation guards run before any database access, so a nil DB exercises
// them without panicking.

func TestMarkScenarioTranslationAttemptFailedValidation(t *testing.T) {
	var db DB
	ctx := context.Background()
	if _, _, err := MarkScenarioTranslationAttemptFailed(ctx, db, 0, 1, 3); err == nil {
		t.Fatal("zero id: want error")
	}
	if _, _, err := MarkScenarioTranslationAttemptFailed(ctx, db, 1, 0, 3); err == nil {
		t.Fatal("zero attempt: want error")
	}
	if _, _, err := MarkScenarioTranslationAttemptFailed(ctx, db, 1, 1, 0); err == nil {
		t.Fatal("zero maxAttempts: want error")
	}
}
