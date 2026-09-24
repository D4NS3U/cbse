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

package persistence

import (
	"context"
	"encoding/json"
	"testing"
)

// The validation guards run before any database access, so a nil DB exercises
// them without panicking. The integration test exercises the SQL paths.

func TestInsertScenarioBatchValidation(t *testing.T) {
	ctx := context.Background()
	var db DB

	// Empty namespace or project is rejected without a DB round-trip.
	if _, err := InsertScenarioBatch(ctx, db, "", "proj", nil); err == nil {
		t.Fatal("empty namespace: want error")
	}
	if _, err := InsertScenarioBatch(ctx, db, "ns", "", nil); err == nil {
		t.Fatal("empty project: want error")
	}
	if _, err := InsertScenarioBatch(ctx, db, "   ", "proj", nil); err == nil {
		t.Fatal("blank namespace: want error")
	}

	// A nil DB with valid namespace/project is rejected before SQL access.
	if _, err := InsertScenarioBatch(ctx, db, "ns", "proj", []ScenarioIntakeRecord{{Priority: 1, NumberOfReps: 1}}); err == nil {
		t.Fatal("nil db with records: want error")
	}

	// An empty record slice short-circuits to (0, nil) without touching the DB.
	got, err := InsertScenarioBatch(ctx, db, "ns", "proj", nil)
	if err != nil || got != 0 {
		t.Fatalf("empty records: want (0, nil); got (%d, %v)", got, err)
	}
	got, err = InsertScenarioBatch(ctx, db, "ns", "proj", []ScenarioIntakeRecord{})
	if err != nil || got != 0 {
		t.Fatalf("empty record slice: want (0, nil); got (%d, %v)", got, err)
	}

	// RecipeInfo and ConfidenceMetric are accepted in the record shape; the
	// SQL path is exercised by the integration test.
	rec := ScenarioIntakeRecord{
		Priority:         2,
		NumberOfReps:     3,
		RecipeInfo:       json.RawMessage(`{"k":"v"}`),
		ConfidenceMetric: nil,
	}
	if rec.Priority != 2 || rec.NumberOfReps != 3 {
		t.Fatal("record field mapping sanity check failed")
	}
}

func TestMarkScenarioStartingRunnersValidation(t *testing.T) {
	ctx := context.Background()
	var db DB

	if _, err := MarkScenarioStartingRunners(ctx, db, 0, "repo@sha256:abc"); err == nil {
		t.Fatal("zero id: want error")
	}
	if _, err := MarkScenarioStartingRunners(ctx, db, -1, "repo@sha256:abc"); err == nil {
		t.Fatal("negative id: want error")
	}
	if _, err := MarkScenarioStartingRunners(ctx, db, 1, ""); err == nil {
		t.Fatal("empty container image: want error")
	}
	if _, err := MarkScenarioStartingRunners(ctx, db, 1, "   "); err == nil {
		t.Fatal("blank container image: want error")
	}
	if _, err := MarkScenarioStartingRunners(ctx, db, 1, "repo@sha256:abc"); err == nil {
		t.Fatal("valid args with nil db: want error")
	}
}
