// Package coredb centralizes persistence for the Scenario Manager. This file
// adds Scenario Status table helpers that support batch ingestion from EDS.
package coredb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

const (
	// DefaultScenarioState is assigned by Scenario Manager when EDS payloads
	// do not carry a scenario state.
	DefaultScenarioState = "Pending"
	// DefaultScenarioComputedReps initializes the computed repetitions counter
	// until downstream execution updates it.
	DefaultScenarioComputedReps = 0
	// DefaultScenarioContainerImage initializes the container image field when
	// the EDS payload does not provide one.
	DefaultScenarioContainerImage = ""
)

// ScenarioStatusRecord captures a single scenario entry destined for the
// scenario status table.
// Fields map directly to the scenario_status schema columns created in schema.go.
type ScenarioStatusRecord struct {
	ProjectID            int
	State                string
	Priority             int
	NumberOfReps         int
	NumberOfComputedReps int
	RecipeInfo           json.RawMessage
	ContainerImage       string
	ConfidenceMetric     *float64
}

// InsertScenarioStatusBatch inserts scenario status rows in a single transaction.
// It returns the number of successfully inserted rows and stops on the first
// error, rolling back the transaction so the batch is not partially persisted.
func InsertScenarioStatusBatch(ctx context.Context, records []ScenarioStatusRecord) (int, error) {
	if coreDBPool == nil {
		return 0, fmt.Errorf("core DB pool is not initialized")
	}

	if len(records) == 0 {
		return 0, nil
	}

	tx, err := coreDBPool.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin scenario status batch: %w", err)
	}

	insertQuery := fmt.Sprintf(`INSERT INTO %s (project_id, state, priority, number_of_reps, number_of_computed_reps, recipe_info, container_image, confidence_metric) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, scenarioStatusTableName())
	stmt, err := tx.PrepareContext(ctx, insertQuery)
	if err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("prepare scenario status insert: %w", err)
	}
	defer stmt.Close()

	inserted := 0
	for i, record := range records {
		if record.ProjectID <= 0 {
			_ = tx.Rollback()
			return inserted, fmt.Errorf("insert scenario status %d: project ID must be positive", i)
		}

		var recipeInfo interface{}
		if len(record.RecipeInfo) > 0 {
			recipeInfo = []byte(record.RecipeInfo)
		}

		var confidence sql.NullFloat64
		if record.ConfidenceMetric != nil {
			confidence = sql.NullFloat64{Float64: *record.ConfidenceMetric, Valid: true}
		}

		if _, err := stmt.ExecContext(
			ctx,
			record.ProjectID,
			record.State,
			record.Priority,
			record.NumberOfReps,
			record.NumberOfComputedReps,
			recipeInfo,
			record.ContainerImage,
			confidence,
		); err != nil {
			_ = tx.Rollback()
			return inserted, fmt.Errorf("insert scenario status %d: %w", i, err)
		}
		inserted++
	}

	if err := tx.Commit(); err != nil {
		return inserted, fmt.Errorf("commit scenario status batch: %w", err)
	}

	return inserted, nil
}
