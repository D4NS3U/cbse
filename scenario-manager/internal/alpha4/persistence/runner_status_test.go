package persistence

import (
	"context"
	"testing"
)

// The validation guards run before any database access, so a nil DB exercises
// them without panicking.

func TestMarkScenarioInProcessingValidation(t *testing.T) {
	var db DB
	if _, err := MarkScenarioInProcessing(context.Background(), db, 0); err == nil {
		t.Fatal("zero id: want error")
	}
	if _, err := MarkScenarioInProcessing(context.Background(), db, -1); err == nil {
		t.Fatal("negative id: want error")
	}
}

func TestMarkScenarioPostProcessingValidation(t *testing.T) {
	var db DB
	if _, err := MarkScenarioPostProcessing(context.Background(), db, 0); err == nil {
		t.Fatal("zero id: want error")
	}
}

func TestMarkScenarioFailedFromValidation(t *testing.T) {
	var db DB
	ctx := context.Background()
	if _, err := MarkScenarioFailedFrom(ctx, db, 0, ScenarioStateStartingRunners); err == nil {
		t.Fatal("zero id: want error")
	}
	// An unsupported source state is rejected without a SQL round-trip.
	if _, err := MarkScenarioFailedFrom(ctx, db, 1, ScenarioStateFinished); err == nil {
		t.Fatal("terminal source state: want error")
	}
	if _, err := MarkScenarioFailedFrom(ctx, db, 1, "Unknown"); err == nil {
		t.Fatal("unknown source state: want error")
	}
	// A valid id with a supported source state proceeds to DB access, so it is
	// exercised by the integration test, not by this nil-DB validation test.
}

func TestUpdateScenarioComputedRepsMonotonicValidation(t *testing.T) {
	var db DB
	ctx := context.Background()
	if _, _, err := UpdateScenarioComputedRepsMonotonic(ctx, db, 0, 1); err == nil {
		t.Fatal("zero id: want error")
	}
	if _, _, err := UpdateScenarioComputedRepsMonotonic(ctx, db, 1, -1); err == nil {
		t.Fatal("negative count: want error")
	}
}

func TestLoadRunnerStartProjectionValidation(t *testing.T) {
	var db DB
	if _, err := LoadRunnerStartProjection(context.Background(), db, 0); err == nil {
		t.Fatal("zero id: want error")
	}
}

func TestLoadObservationProjectionValidation(t *testing.T) {
	var db DB
	if _, err := LoadObservationProjection(context.Background(), db, 0); err == nil {
		t.Fatal("zero id: want error")
	}
}

func TestNonTerminalFailureStatesAllowList(t *testing.T) {
	// The single-row failure allow-list must match the terminal bulk set so a
	// runtime Forbidden or collision can fail a scenario from any non-terminal
	// state the runner workflows encounter.
	want := map[string]struct{}{
		ScenarioStateCreated:         {},
		ScenarioStateScheduled:       {},
		ScenarioStateStartingRunners: {},
		ScenarioStateInProcessing:    {},
		ScenarioStatePostProcessing:  {},
	}
	for _, s := range nonTerminalFailureStates {
		if _, ok := want[s]; !ok {
			t.Fatalf("unexpected non-terminal failure state %q", s)
		}
	}
	if len(nonTerminalFailureStates) != len(want) {
		t.Fatalf("non-terminal failure states = %v; want exactly %v", nonTerminalFailureStates, want)
	}
}
