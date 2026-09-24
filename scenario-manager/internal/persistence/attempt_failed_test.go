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
