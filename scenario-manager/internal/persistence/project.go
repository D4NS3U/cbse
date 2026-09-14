package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ProjectIDByNamespaceAndName resolves the project id for the exact
// (namespace, name) pair. It returns ErrProjectNotFound (wrapping
// sql.ErrNoRows) when no row matches, so callers can distinguish "not yet
// registered" from a database error.
func ProjectIDByNamespaceAndName(ctx context.Context, db DB, namespace, project string) (int, error) {
	if err := validateIdentPair(namespace, project); err != nil {
		return 0, err
	}
	var id int
	query := fmt.Sprintf(`SELECT id FROM %s WHERE project_namespace = $1 AND project_name = $2`, ProjectTable())
	err := db.QueryRowContext(ctx, query, namespace, project).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrProjectNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("resolve project %s/%s: %w", namespace, project, err)
	}
	return id, nil
}

// RegisterProject idempotently registers the exact (namespace, name) pair and
// returns its project id. If the row already exists, the existing id is
// returned unchanged. project_namespace and project_name are copied without
// normalization from the experiment's metadata.namespace and metadata.name.
//
// RegisterProject does not manage the SM finalizer; the caller adds the
// finalizer before registering and re-gets the object after the patch. If the
// experiment has begun deletion between the finalizer patch and registration,
// the caller enters deletion cleanup instead of registering.
func RegisterProject(ctx context.Context, db DB, namespace, project string) (int, error) {
	if err := validateIdentPair(namespace, project); err != nil {
		return 0, err
	}
	table := ProjectTable()
	// INSERT ... ON CONFLICT DO NOTHING; if the conflict suppressed the insert,
	// RETURNING yields no row, so fall back to a SELECT for the existing id.
	query := fmt.Sprintf(`INSERT INTO %s (project_namespace, project_name) VALUES ($1, $2)
ON CONFLICT (project_namespace, project_name) DO NOTHING
RETURNING id`, table)
	var id int
	err := db.QueryRowContext(ctx, query, namespace, project).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("register project %s/%s: %w", namespace, project, err)
	}
	// Conflict suppressed the insert; return the existing id.
	return ProjectIDByNamespaceAndName(ctx, db, namespace, project)
}

// DeleteProjectByNamespaceAndName deletes the project row for the exact
// (namespace, name) pair. Deleting the project row cascades to its scenario
// rows via the scenario_status.project_id ON DELETE CASCADE foreign key. A
// missing row is success (cleanup is already satisfied).
func DeleteProjectByNamespaceAndName(ctx context.Context, db DB, namespace, project string) error {
	if err := validateIdentPair(namespace, project); err != nil {
		return err
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE project_namespace = $1 AND project_name = $2`, ProjectTable())
	if _, err := db.ExecContext(ctx, query, namespace, project); err != nil {
		return fmt.Errorf("delete project %s/%s: %w", namespace, project, err)
	}
	return nil
}

func validateIdentPair(namespace, project string) error {
	namespace = strings.TrimSpace(namespace)
	project = strings.TrimSpace(project)
	if namespace == "" {
		return fmt.Errorf("project namespace must not be empty")
	}
	if project == "" {
		return fmt.Errorf("project name must not be empty")
	}
	return nil
}
