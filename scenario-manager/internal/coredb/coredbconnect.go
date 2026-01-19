// Package coredb centralizes connectivity and schema-management logic for the
// Scenario Manager's PostgreSQL-compatible persistence layer.
package coredb

import (
	"database/sql"
	"log"
	"net/url"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Environment variable names for Core DB connection parameters.
const (
	coreDBDSNEnv             = "SCENARIO_MANAGER_CORE_DB_DSN"
	coreDBUserEnv            = "SCENARIO_MANAGER_CORE_DB_USER"
	coreDBPasswordEnv        = "SCENARIO_MANAGER_CORE_DB_PASSWORD"
	scenarioStatusTableEnv   = "SCENARIO_MANAGER_CORE_DB_SCENARIO_STATUS_TABLE"
	projectTableEnv          = "SCENARIO_MANAGER_CORE_DB_PROJECT_TABLE"
	defaultScenarioStatusTbl = "scenario_status"
	defaultProjectTbl        = "project"
)

// coreDBPool stores the lazily initialized database handle shared throughout
// the Scenario Manager process.
var coreDBPool *sql.DB

// CoreDBConnect initializes and retains the shared core database connection pool.
// Scenario Manager startup treats a failure to connect as a critical error.
func CoreDBConnect() bool {
	if coreDBPool != nil {
		return true
	}

	baseDSN := os.Getenv(coreDBDSNEnv)
	if baseDSN == "" {
		log.Printf("%s is not set", coreDBDSNEnv)
		return false
	}

	username := os.Getenv(coreDBUserEnv)
	if username == "" {
		log.Printf("%s is not set", coreDBUserEnv)
		return false
	}

	password := os.Getenv(coreDBPasswordEnv)
	if password == "" {
		log.Printf("%s is not set", coreDBPasswordEnv)
		return false
	}

	finalDSN, err := buildCoreDBConnString(baseDSN, username, password)
	if err != nil {
		log.Printf("Failed to construct core database DSN: %v", err)
		return false
	}

	db, err := sql.Open("pgx", finalDSN)
	if err != nil {
		log.Printf("Failed to open core database handle: %v", err)
		return false
	}

	if err := db.Ping(); err != nil {
		log.Printf("Failed to ping core database: %v", err)
		_ = db.Close()
		return false
	}

	coreDBPool = db
	log.Println("Core database connection established.")
	return true
}

// buildCoreDBConnString merges the base DSN with the provided credentials so
// the pgx driver receives a fully-qualified connection string.
func buildCoreDBConnString(base, username, password string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}

	parsed.User = url.UserPassword(username, password)
	return parsed.String(), nil
}
