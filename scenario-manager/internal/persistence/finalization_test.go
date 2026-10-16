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
// them without panicking. The SQL paths (idempotent mark, marker read, the
// project cascade) are exercised by the integration test.

func TestFinalizationTableDefault(t *testing.T) {
	if got := FinalizationTable(); got != "experiment_finalization" {
		t.Fatalf("FinalizationTable() = %q; want experiment_finalization", got)
	}
}

func TestMarkFinalizedValidation(t *testing.T) {
	ctx := context.Background()
	var db DB

	// Empty or blank namespace/project is rejected before SQL access (the
	// guard shared with RegisterProject / ProjectIDByNamespaceAndName).
	if err := MarkFinalized(ctx, db, "", "proj"); err == nil {
		t.Fatal("empty namespace: want error")
	}
	if err := MarkFinalized(ctx, db, "ns", ""); err == nil {
		t.Fatal("empty project: want error")
	}
	if err := MarkFinalized(ctx, db, "   ", "proj"); err == nil {
		t.Fatal("blank namespace: want error")
	}
	if err := MarkFinalized(ctx, db, "ns", "  "); err == nil {
		t.Fatal("blank project: want error")
	}
}

func TestIsFinalizedValidation(t *testing.T) {
	ctx := context.Background()
	var db DB

	if _, err := IsFinalized(ctx, db, "", "proj"); err == nil {
		t.Fatal("empty namespace: want error")
	}
	if _, err := IsFinalized(ctx, db, "ns", ""); err == nil {
		t.Fatal("empty project: want error")
	}
}
