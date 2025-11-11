package coredb

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
)

// EnsureTablesAvailable verifies that the required Scenario Status and Project tables exist.
func EnsureTablesAvailable(ctx context.Context) bool {
	if coreDBPool == nil {
		log.Println("Core DB pool is not initialized; cannot verify tables.")
		return false
	}

	if err := createSchema(ctx); err != nil {
		log.Printf("Failed to ensure core DB schema: %v", err)
		return false
	}

	requiredTables := []string{
		scenarioStatusTableName(),
		projectTableName(),
	}

	for _, tbl := range requiredTables {
		exists, err := tableExists(ctx, tbl)
		if err != nil {
			log.Printf("Failed to check presence of table %s: %v", tbl, err)
			return false
		}

		if !exists {
			log.Printf("Required core DB table %s is missing.", tbl)
			return false
		}
	}

	log.Println("Required core DB tables verified.")
	return true
}

// createSchema ensures that required Core DB tables exist, creating them when necessary.
func createSchema(ctx context.Context) error {
	projectTable := projectTableName()
	createProjectTable := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id SERIAL PRIMARY KEY,
		project_name TEXT NOT NULL,
		number_of_components INTEGER NOT NULL DEFAULT 0
	)`, projectTable)

	if _, err := coreDBPool.ExecContext(ctx, createProjectTable); err != nil {
		return fmt.Errorf("create %s: %w", projectTable, err)
	}

	scenarioStatusTable := scenarioStatusTableName()
	createScenarioStatusTable := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id SERIAL PRIMARY KEY,
		state TEXT NOT NULL,
		priority INTEGER NOT NULL DEFAULT 0,
		number_of_reps INTEGER NOT NULL DEFAULT 0,
		number_of_computed_reps INTEGER NOT NULL DEFAULT 0,
		recipe_info JSONB,
		container_image TEXT,
		confidence_metric DOUBLE PRECISION
	)`, scenarioStatusTable)

	if _, err := coreDBPool.ExecContext(ctx, createScenarioStatusTable); err != nil {
		return fmt.Errorf("create %s: %w", scenarioStatusTable, err)
	}

	return nil
}

func tableExists(ctx context.Context, tableName string) (bool, error) {
	var regclass sql.NullString
	err := coreDBPool.QueryRowContext(ctx, "SELECT to_regclass($1)", tableName).Scan(&regclass)
	if err != nil {
		return false, err
	}
	return regclass.Valid, nil
}

func scenarioStatusTableName() string {
	return tableNameFromEnv(scenarioStatusTableEnv, defaultScenarioStatusTbl)
}

func projectTableName() string {
	return tableNameFromEnv(projectTableEnv, defaultProjectTbl)
}

func tableNameFromEnv(envKey, fallback string) string {
	if value := os.Getenv(envKey); value != "" {
		return value
	}
	return fallback
}
