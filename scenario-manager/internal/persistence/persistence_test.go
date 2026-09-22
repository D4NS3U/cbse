package persistence

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The validation guards run before any database access, so a nil DB exercises
// them without panicking.

func TestClaimScenarioForTranslationValidation(t *testing.T) {
	var store Store
	if _, err := ClaimScenarioForTranslation(context.Background(), store, 0); !errors.Is(err, errPositiveID) {
		t.Fatalf("err = %v; want errPositiveID", err)
	}
	if _, err := ClaimScenarioForTranslation(context.Background(), store, -1); !errors.Is(err, errPositiveID) {
		t.Fatalf("err = %v; want errPositiveID", err)
	}
}

func TestMarkTranslationPublishStartedValidation(t *testing.T) {
	var db DB
	if _, err := MarkTranslationPublishStarted(context.Background(), db, 0, 1); !errors.Is(err, errPositiveID) {
		t.Fatalf("id: %v", err)
	}
	if _, err := MarkTranslationPublishStarted(context.Background(), db, 1, 0); !errors.Is(err, errPositiveAttempt) {
		t.Fatalf("attempt: %v", err)
	}
}

func TestMarkScenarioTranslationRequestPublishedValidation(t *testing.T) {
	var db DB
	if _, err := MarkScenarioTranslationRequestPublished(context.Background(), db, 0, 1); !errors.Is(err, errPositiveID) {
		t.Fatalf("id: %v", err)
	}
	if _, err := MarkScenarioTranslationRequestPublished(context.Background(), db, 1, 0); !errors.Is(err, errPositiveAttempt) {
		t.Fatalf("attempt: %v", err)
	}
}

func TestCancelUnpublishedTranslationClaimValidation(t *testing.T) {
	var db DB
	if _, err := CancelUnpublishedTranslationClaim(context.Background(), db, 0, 1); !errors.Is(err, errPositiveID) {
		t.Fatalf("id: %v", err)
	}
	if _, err := CancelUnpublishedTranslationClaim(context.Background(), db, 1, 0); !errors.Is(err, errPositiveAttempt) {
		t.Fatalf("attempt: %v", err)
	}
}

func TestRecoverUnpublishedTranslationClaimValidation(t *testing.T) {
	var db DB
	ctx := context.Background()
	before := time.Now()
	if _, _, err := RecoverUnpublishedTranslationClaim(ctx, db, 0, 1, before, 3); !errors.Is(err, errPositiveID) {
		t.Fatalf("id: %v", err)
	}
	if _, _, err := RecoverUnpublishedTranslationClaim(ctx, db, 1, 0, before, 3); !errors.Is(err, errPositiveAttempt) {
		t.Fatalf("attempt: %v", err)
	}
	if _, _, err := RecoverUnpublishedTranslationClaim(ctx, db, 1, 1, time.Time{}, 3); err == nil {
		t.Fatal("zero claimedBefore: want error")
	}
	if _, _, err := RecoverUnpublishedTranslationClaim(ctx, db, 1, 1, before, 0); err == nil {
		t.Fatal("zero maxAttempts: want error")
	}
}

func TestMarkScenarioTranslationPublishFailedValidation(t *testing.T) {
	var db DB
	ctx := context.Background()
	if _, _, err := MarkScenarioTranslationPublishFailed(ctx, db, 0, 1, 3); !errors.Is(err, errPositiveID) {
		t.Fatalf("id: %v", err)
	}
	if _, _, err := MarkScenarioTranslationPublishFailed(ctx, db, 1, 0, 3); !errors.Is(err, errPositiveAttempt) {
		t.Fatalf("attempt: %v", err)
	}
	if _, _, err := MarkScenarioTranslationPublishFailed(ctx, db, 1, 1, 0); err == nil {
		t.Fatal("zero maxAttempts: want error")
	}
}

func TestMarkScenariosFailedForProjectValidation(t *testing.T) {
	var store Store
	if _, err := MarkScenariosFailedForProject(context.Background(), store, 0); err == nil {
		t.Fatal("zero projectID: want error")
	}
}

func TestProjectIdentValidation(t *testing.T) {
	var db DB
	ctx := context.Background()
	if _, err := ProjectIDByNamespaceAndName(ctx, db, "", "p"); err == nil {
		t.Fatal("empty namespace: want error")
	}
	if _, err := ProjectIDByNamespaceAndName(ctx, db, "ns", ""); err == nil {
		t.Fatal("empty project: want error")
	}
	if _, err := RegisterProject(ctx, db, "", "p"); err == nil {
		t.Fatal("empty namespace: want error")
	}
	if _, err := RegisterProject(ctx, db, "ns", ""); err == nil {
		t.Fatal("empty project: want error")
	}
	if err := DeleteProjectByNamespaceAndName(ctx, db, "", "p"); err == nil {
		t.Fatal("empty namespace: want error")
	}
	if err := DeleteProjectByNamespaceAndName(ctx, db, "ns", ""); err == nil {
		t.Fatal("empty project: want error")
	}
}

func TestEnsureSchemaValidation(t *testing.T) {
	if err := EnsureSchema(nil, nil); err == nil {
		t.Fatal("nil ctx: want error")
	}
	if err := EnsureSchema(context.Background(), nil); err == nil {
		t.Fatal("nil db: want error")
	}
}

func TestTableNameDefaults(t *testing.T) {
	// Env vars are unset in unit tests; defaults must be canonical.
	if got := ProjectTable(); got != ProjectTableDefault {
		t.Fatalf("ProjectTable = %q; want %q", got, ProjectTableDefault)
	}
	if got := ScenarioStatusTable(); got != ScenarioStatusTableDefault {
		t.Fatalf("ScenarioStatusTable = %q; want %q", got, ScenarioStatusTableDefault)
	}
}
