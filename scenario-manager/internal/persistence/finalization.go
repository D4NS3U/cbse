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
	"database/sql"
	"errors"
	"fmt"
)

// FinalizationTableDefault is the canonical name of the durable per-project
// finalization marker table. The table is additive to the alpha4 Core DB
// schema and carries no env override: it has no alpha3 predecessor, so there
// is no legacy name to honor.
const FinalizationTableDefault = "experiment_finalization"

// FinalizationTable returns the finalization marker table name.
func FinalizationTable() string { return FinalizationTableDefault }

// MarkFinalized durably marks the exact (namespace, project) pair as
// finalized: row presence in experiment_finalization is the marker. It is
// idempotent (INSERT ... ON CONFLICT DO NOTHING), so redelivered final
// batches are a no-op.
//
// The pair is registered with RegisterProject first: a finalization batch may
// arrive for a not-yet-registered project (the finalization-only batch shape
// carries no scenarios, and InsertScenarioBatch registers its project only as
// a side effect of inserting at least one record). The marker row references
// the project row with ON DELETE CASCADE, so its lifecycle follows the
// project's: DeleteProjectByNamespaceAndName removes the marker with the
// project.
func MarkFinalized(ctx context.Context, db DB, namespace, project string) error {
	projectID, err := RegisterProject(ctx, db, namespace, project)
	if err != nil {
		return fmt.Errorf("mark project %s/%s finalized: %w", namespace, project, err)
	}
	query := fmt.Sprintf(`INSERT INTO %s (project_id) VALUES ($1)
ON CONFLICT (project_id) DO NOTHING`, FinalizationTable())
	if _, err := db.ExecContext(ctx, query, projectID); err != nil {
		return fmt.Errorf("mark project %s/%s finalized: %w", namespace, project, err)
	}
	return nil
}

// IsFinalized reports whether the exact (namespace, project) pair carries the
// durable finalization marker. A missing project (never registered, or
// already deleted) is (false, nil), not an error: the marker's lifecycle
// follows the project's, so without the project row there is nothing
// finalized.
func IsFinalized(ctx context.Context, db DB, namespace, project string) (bool, error) {
	id, err := ProjectIDByNamespaceAndName(ctx, db, namespace, project)
	if errors.Is(err, ErrProjectNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("resolve project %s/%s for finalization: %w", namespace, project, err)
	}
	var finalized bool
	query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE project_id = $1)`, FinalizationTable())
	if err := db.QueryRowContext(ctx, query, id).Scan(&finalized); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("read finalization marker for project %s/%s: %w", namespace, project, err)
	}
	return finalized, nil
}
