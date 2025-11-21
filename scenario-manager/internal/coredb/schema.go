package coredb

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
)

// EnsureTablesAvailable verifies and, when necessary, creates the tables needed
// for tracking Scenario execution state and registered Projects. It relies on
// the shared Core DB connection and returns false if any prerequisite is
// missing or unreachable.
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

// createSchema ensures that the Scenario Manager specific tables exist by
// issuing idempotent CREATE TABLE statements for each required entity.
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

// tableExists reports whether PostgreSQL can resolve the supplied table via
// the to_regclass helper, allowing the caller to short-circuit schema creation.
func tableExists(ctx context.Context, tableName string) (bool, error) {
	var regclass sql.NullString
	err := coreDBPool.QueryRowContext(ctx, "SELECT to_regclass($1)", tableName).Scan(&regclass)
	if err != nil {
		return false, err
	}
	return regclass.Valid, nil
}

// scenarioStatusTableName resolves the Scenario Status table name from the
// environment, defaulting to a stable identifier when no override is present.
func scenarioStatusTableName() string {
	return tableNameFromEnv(scenarioStatusTableEnv, defaultScenarioStatusTbl)
}

// projectTableName resolves the Project table name from the environment,
// falling back to the default schema when unspecified.
func projectTableName() string {
	return tableNameFromEnv(projectTableEnv, defaultProjectTbl)
}

// tableNameFromEnv returns the custom table name stored under envKey or the
// provided fallback if the environment variable is unset.
func tableNameFromEnv(envKey, fallback string) string {
	if value := os.Getenv(envKey); value != "" {
		return value
	}
	return fallback
}
