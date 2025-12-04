package coredb

import (
	"context"
	"fmt"
)

// ProjectRecord captures the subset of SimulationExperiment data persisted in
// the project table.
type ProjectRecord struct {
	Name               string
	NumberOfComponents int
	Status             string
}

// UpsertProject inserts or updates a project row keyed by project name.
func UpsertProject(ctx context.Context, project ProjectRecord) error {
	if coreDBPool == nil {
		return fmt.Errorf("core DB pool is not initialized")
	}

	if project.Name == "" {
		return fmt.Errorf("project name must not be empty")
	}

	updateQuery := fmt.Sprintf(`UPDATE %s SET number_of_components=$1, status=$2 WHERE project_name=$3`, projectTableName())
	res, err := coreDBPool.ExecContext(ctx, updateQuery, project.NumberOfComponents, project.Status, project.Name)
	if err != nil {
		return fmt.Errorf("update project %q: %w", project.Name, err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected for project %q: %w", project.Name, err)
	}

	if rows > 0 {
		return nil
	}

	insertQuery := fmt.Sprintf(`INSERT INTO %s (project_name, number_of_components, status) VALUES ($1, $2, $3)`, projectTableName())
	if _, err := coreDBPool.ExecContext(ctx, insertQuery, project.Name, project.NumberOfComponents, project.Status); err != nil {
		return fmt.Errorf("insert project %q: %w", project.Name, err)
	}

	return nil
}

// DeleteProject removes a project row keyed by project name.
func DeleteProject(ctx context.Context, projectName string) error {
	if coreDBPool == nil {
		return fmt.Errorf("core DB pool is not initialized")
	}

	if projectName == "" {
		return fmt.Errorf("project name must not be empty")
	}

	deleteQuery := fmt.Sprintf(`DELETE FROM %s WHERE project_name=$1`, projectTableName())
	if _, err := coreDBPool.ExecContext(ctx, deleteQuery, projectName); err != nil {
		return fmt.Errorf("delete project %q: %w", projectName, err)
	}

	return nil
}
