package coredb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ProjectRecord captures the subset of SimulationExperiment data persisted in
// the project table.
type ProjectRecord struct {
	ID                 int
	Name               string
	NumberOfComponents int
	Status             string
}

// CreateProject inserts the initial project row for the supplied project name.
// If the project already exists, the existing row is returned unchanged.
func CreateProject(ctx context.Context, project ProjectRecord) (ProjectRecord, error) {
	if err := ensureCoreDBPool(); err != nil {
		return ProjectRecord{}, err
	}

	project.Name = strings.TrimSpace(project.Name)
	if project.Name == "" {
		return ProjectRecord{}, fmt.Errorf("project name must not be empty")
	}

	table := projectTableName()
	insertQuery := fmt.Sprintf(`INSERT INTO %s (project_name, number_of_components, status)
SELECT $1, $2, $3
WHERE NOT EXISTS (SELECT 1 FROM %s WHERE project_name=$1)
RETURNING id, project_name, number_of_components, status`, table, table)

	created := ProjectRecord{}
	err := coreDBPool.QueryRowContext(
		ctx,
		insertQuery,
		project.Name,
		project.NumberOfComponents,
		project.Status,
	).Scan(&created.ID, &created.Name, &created.NumberOfComponents, &created.Status)
	if err == nil {
		return created, nil
	}

	if err != sql.ErrNoRows {
		return ProjectRecord{}, fmt.Errorf("insert project %q: %w", project.Name, err)
	}

	// Row already existed; return the current record.
	return ProjectByName(ctx, project.Name)
}

// UpdateProjectFields updates only the mutable project columns that changed.
// Supported fields are number_of_components and status.
func UpdateProjectFields(ctx context.Context, projectName string, numberOfComponents *int, status *string) (ProjectRecord, error) {
	if err := ensureCoreDBPool(); err != nil {
		return ProjectRecord{}, err
	}

	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return ProjectRecord{}, fmt.Errorf("project name must not be empty")
	}

	if numberOfComponents == nil && status == nil {
		return ProjectRecord{}, nil
	}

	sets := make([]string, 0, 2)
	args := make([]interface{}, 0, 3)
	argPos := 1

	if numberOfComponents != nil {
		sets = append(sets, fmt.Sprintf("number_of_components=$%d", argPos))
		args = append(args, *numberOfComponents)
		argPos++
	}
	if status != nil {
		sets = append(sets, fmt.Sprintf("status=$%d", argPos))
		args = append(args, *status)
		argPos++
	}

	args = append(args, projectName)
	updateQuery := fmt.Sprintf(
		`UPDATE %s SET %s WHERE project_name=$%d RETURNING id, project_name, number_of_components, status`,
		projectTableName(),
		strings.Join(sets, ", "),
		argPos,
	)

	updated := ProjectRecord{}
	if err := coreDBPool.QueryRowContext(ctx, updateQuery, args...).Scan(
		&updated.ID,
		&updated.Name,
		&updated.NumberOfComponents,
		&updated.Status,
	); err != nil {
		if err == sql.ErrNoRows {
			return ProjectRecord{}, fmt.Errorf("project %q not found", projectName)
		}
		return ProjectRecord{}, fmt.Errorf("update project %q: %w", projectName, err)
	}

	return updated, nil
}

// DeleteProject removes a project row keyed by project name.
func DeleteProject(ctx context.Context, projectName string) error {
	if err := ensureCoreDBPool(); err != nil {
		return err
	}

	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return fmt.Errorf("project name must not be empty")
	}

	deleteQuery := fmt.Sprintf(`DELETE FROM %s WHERE project_name=$1`, projectTableName())
	if _, err := coreDBPool.ExecContext(ctx, deleteQuery, projectName); err != nil {
		return fmt.Errorf("delete project %q: %w", projectName, err)
	}

	return nil
}

// ProjectByName resolves the first project row by project name.
func ProjectByName(ctx context.Context, projectName string) (ProjectRecord, error) {
	if err := ensureCoreDBPool(); err != nil {
		return ProjectRecord{}, err
	}

	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return ProjectRecord{}, fmt.Errorf("project name must not be empty")
	}

	query := fmt.Sprintf(
		`SELECT id, project_name, number_of_components, status FROM %s WHERE project_name=$1 ORDER BY id ASC LIMIT 1`,
		projectTableName(),
	)
	project := ProjectRecord{}
	if err := coreDBPool.QueryRowContext(ctx, query, projectName).Scan(
		&project.ID,
		&project.Name,
		&project.NumberOfComponents,
		&project.Status,
	); err != nil {
		if err == sql.ErrNoRows {
			return ProjectRecord{}, fmt.Errorf("project %q not found", projectName)
		}
		return ProjectRecord{}, fmt.Errorf("lookup project %q: %w", projectName, err)
	}

	return project, nil
}

// ProjectIDByName resolves the numeric project identifier for a project row.
func ProjectIDByName(ctx context.Context, projectName string) (int, error) {
	project, err := ProjectByName(ctx, projectName)
	if err != nil {
		return 0, err
	}
	return project.ID, nil
}

func ensureCoreDBPool() error {
	if coreDBPool == nil {
		return fmt.Errorf("core DB pool is not initialized")
	}
	return nil
}
