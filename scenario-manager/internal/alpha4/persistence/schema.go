package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// EnsureSchema creates the alpha4 project and scenario_status tables when they
// are absent and validates them when they already exist. It never issues
// ALTER TABLE repair: an incompatible table returns ErrSchemaIncompatible so
// startup fails before the informer, NATS consumers, or selector starts.
//
// The project table has exactly three columns (id, project_namespace,
// project_name) with a UNIQUE (project_namespace, project_name) constraint and
// no mutable status, component-count, or experiment-UID columns. The
// scenario_status table requires the alpha4 translation_publish_started_at
// TIMESTAMPTZ NULL column in addition to the alpha3 scenario columns and a
// single-column FK project_id -> project.id ON DELETE CASCADE.
func EnsureSchema(ctx context.Context, db DB) error {
	if ctx == nil {
		return fmt.Errorf("context must not be nil")
	}
	if db == nil {
		return fmt.Errorf("db must not be nil")
	}

	projectTable := ProjectTable()
	scenarioTable := ScenarioStatusTable()

	scenarioExists, err := tableExists(ctx, db, scenarioTable)
	if err != nil {
		return fmt.Errorf("check presence of %s: %w", scenarioTable, err)
	}

	// Create the project table if absent. CREATE TABLE IF NOT EXISTS keeps
	// concurrent Scenario Manager startups safe; the validation below still
	// catches a different legacy table created in the race window.
	createProject := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id SERIAL PRIMARY KEY,
		project_namespace TEXT NOT NULL,
		project_name TEXT NOT NULL,
		CONSTRAINT %s_namespace_name_key UNIQUE (project_namespace, project_name)
	)`, projectTable, projectTable)
	if _, err := db.ExecContext(ctx, createProject); err != nil {
		return fmt.Errorf("create %s: %w", projectTable, err)
	}

	if err := validateProjectTable(ctx, db, projectTable); err != nil {
		return err
	}

	if !scenarioExists {
		createScenario := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id SERIAL PRIMARY KEY,
			project_id INTEGER NOT NULL REFERENCES %s(id) ON DELETE CASCADE,
			state TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			priority INTEGER NOT NULL DEFAULT 0,
			number_of_reps INTEGER NOT NULL DEFAULT 0,
			number_of_computed_reps INTEGER NOT NULL DEFAULT 0,
			translation_attempts INTEGER NOT NULL DEFAULT 0,
			translation_publish_started_at TIMESTAMPTZ,
			translation_request_published_at TIMESTAMPTZ,
			recipe_info JSONB,
			container_image TEXT,
			confidence_metric DOUBLE PRECISION
		)`, scenarioTable, projectTable)
		if _, err := db.ExecContext(ctx, createScenario); err != nil {
			return fmt.Errorf("create %s: %w", scenarioTable, err)
		}
	}

	if err := validateScenarioStatusTable(ctx, db, scenarioTable, projectTable); err != nil {
		return err
	}
	return nil
}

// tableExists reports whether the named table is present in the current schema.
func tableExists(ctx context.Context, db DB, table string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists)
	return exists, err
}

// validateProjectTable enforces the alpha4 project contract: exactly the three
// columns id, project_namespace, project_name with the stated types and
// nullability, a sole id primary key backed by a generated sequence, and a
// UNIQUE (project_namespace, project_name) constraint. Any extra column (e.g.
// legacy number_of_components, status, experiment_uid) is incompatible.
func validateProjectTable(ctx context.Context, db DB, table string) error {
	columns, err := loadTableColumns(ctx, db, table)
	if err != nil {
		return fmt.Errorf("inspect project table %s: %w", table, err)
	}
	required := []struct {
		name    string
		typ     string
		notNull bool
	}{
		{"id", "int4", true},
		{"project_namespace", "text", true},
		{"project_name", "text", true},
	}
	for _, c := range required {
		if err := requireColumn(table, columns, c.name, c.typ, c.notNull); err != nil {
			return err
		}
	}
	// Exactly the three columns; any extra column is a legacy/incompatible table.
	if len(columns) != 3 {
		extras := make([]string, 0, len(columns))
		for name := range columns {
			if name != "id" && name != "project_namespace" && name != "project_name" {
				extras = append(extras, name)
			}
		}
		return incompatible(table, "project table must have exactly id, project_namespace, project_name; extra columns: %s", strings.Join(extras, ", "))
	}
	if err := validateSoleIDPrimaryKey(ctx, db, table); err != nil {
		return err
	}
	if err := validateGeneratedIDSequence(ctx, db, table); err != nil {
		return err
	}
	if err := validateNamespaceNameUnique(ctx, db, table); err != nil {
		return err
	}
	return nil
}

// validateScenarioStatusTable enforces the alpha4 scenario_status contract. It
// requires the alpha4 translation_publish_started_at TIMESTAMPTZ NULL column
// and a single-column FK project_id -> project.id ON DELETE CASCADE.
func validateScenarioStatusTable(ctx context.Context, db DB, table, projectTable string) error {
	columns, err := loadTableColumns(ctx, db, table)
	if err != nil {
		return fmt.Errorf("inspect scenario_status table %s: %w", table, err)
	}
	required := []struct {
		name    string
		typ     string
		notNull bool
	}{
		{"id", "int4", true},
		{"project_id", "int4", true},
		{"state", "text", true},
		{"created_at", "timestamptz", true},
		{"updated_at", "timestamptz", true},
		{"priority", "int4", true},
		{"number_of_reps", "int4", true},
		{"number_of_computed_reps", "int4", true},
		{"translation_attempts", "int4", true},
		{"translation_publish_started_at", "timestamptz", false},
		{"translation_request_published_at", "timestamptz", false},
		{"recipe_info", "jsonb", false},
		{"container_image", "text", false},
		{"confidence_metric", "float8", false},
	}
	for _, c := range required {
		if err := requireColumn(table, columns, c.name, c.typ, c.notNull); err != nil {
			return err
		}
	}
	// translation_publish_started_at must have no default.
	if c, ok := columns["translation_publish_started_at"]; ok && c.defaultExpression.Valid {
		return incompatible(table, "column translation_publish_started_at must have no default; got %q", c.defaultExpression.String)
	}
	if err := validateSoleIDPrimaryKey(ctx, db, table); err != nil {
		return err
	}
	if err := validateGeneratedIDSequence(ctx, db, table); err != nil {
		return err
	}
	validFK, err := hasExactProjectForeignKey(ctx, db, table, projectTable)
	if err != nil {
		return fmt.Errorf("inspect %s project foreign key: %w", table, err)
	}
	if !validFK {
		return incompatible(table, "project_id must be a single-column foreign key to %s.id with ON DELETE CASCADE", projectTable)
	}
	return nil
}

// catalogColumn is the PostgreSQL catalog metadata needed for compatibility
// checks.
type catalogColumn struct {
	postgresType      string
	notNull           bool
	defaultExpression sql.NullString
}

// loadTableColumns reads PostgreSQL's real type and constraint metadata.
func loadTableColumns(ctx context.Context, db DB, table string) (map[string]catalogColumn, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT a.attname,
			t.typname,
			a.attnotnull,
			pg_get_expr(d.adbin, d.adrelid)
		FROM pg_attribute a
		JOIN pg_type t ON t.oid = a.atttypid
		LEFT JOIN pg_attrdef d
			ON d.adrelid = a.attrelid
			AND d.adnum = a.attnum
		WHERE a.attrelid = to_regclass($1)
			AND a.attnum > 0
			AND NOT a.attisdropped`,
		table,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]catalogColumn)
	for rows.Next() {
		var name string
		var c catalogColumn
		if err := rows.Scan(&name, &c.postgresType, &c.notNull, &c.defaultExpression); err != nil {
			return nil, err
		}
		columns[name] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func requireColumn(table string, columns map[string]catalogColumn, name, postgresType string, notNull bool) error {
	c, exists := columns[name]
	if !exists {
		return incompatible(table, "required column %s is missing", name)
	}
	if c.postgresType != postgresType {
		return incompatible(table, "column %s has PostgreSQL type %s; expected %s", name, c.postgresType, postgresType)
	}
	if c.notNull != notNull {
		expected := "nullable"
		if notNull {
			expected = "NOT NULL"
		}
		return incompatible(table, "column %s has incompatible nullability; expected %s", name, expected)
	}
	return nil
}

// validateSoleIDPrimaryKey rejects composite keys and keys on a legacy column.
func validateSoleIDPrimaryKey(ctx context.Context, db DB, table string) error {
	rows, err := db.QueryContext(ctx, `
		SELECT a.attname
		FROM pg_constraint c
		JOIN LATERAL unnest(c.conkey) WITH ORDINALITY AS key(attnum, position) ON TRUE
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = key.attnum
		WHERE c.conrelid = to_regclass($1) AND c.contype = 'p'
		ORDER BY key.position`,
		table,
	)
	if err != nil {
		return fmt.Errorf("inspect primary key for %s: %w", table, err)
	}
	defer rows.Close()
	var pk []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return fmt.Errorf("inspect primary key for %s: %w", table, err)
		}
		pk = append(pk, col)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect primary key for %s: %w", table, err)
	}
	if len(pk) != 1 || pk[0] != "id" {
		return incompatible(table, "primary key must contain only id")
	}
	return nil
}

// validateGeneratedIDSequence accepts SERIAL- or identity-backed id columns.
func validateGeneratedIDSequence(ctx context.Context, db DB, table string) error {
	var (
		sequence          sql.NullString
		identityGenerated bool
		serialGenerated   bool
	)
	err := db.QueryRowContext(ctx, `
		SELECT pg_get_serial_sequence($1, 'id'),
			a.attidentity IN ('a', 'd'),
			EXISTS (
				SELECT 1
				FROM pg_attrdef definition
				JOIN pg_depend dependency
					ON dependency.classid = 'pg_attrdef'::regclass
					AND dependency.objid = definition.oid
					AND dependency.refclassid = 'pg_class'::regclass
				WHERE definition.adrelid = a.attrelid
					AND definition.adnum = a.attnum
					AND lower(pg_get_expr(definition.adbin, definition.adrelid)) LIKE 'nextval(%'
					AND dependency.refobjid = to_regclass(pg_get_serial_sequence($1, 'id'))
			)
		FROM pg_attribute a
		WHERE a.attrelid = to_regclass($1)
			AND a.attname = 'id'
			AND NOT a.attisdropped`,
		table).Scan(&sequence, &identityGenerated, &serialGenerated)
	if err != nil {
		return fmt.Errorf("inspect generated ID sequence for %s: %w", table, err)
	}
	if !sequence.Valid || strings.TrimSpace(sequence.String) == "" {
		return incompatible(table, "id must be generated by a serial- or identity-backed sequence")
	}
	if !identityGenerated && !serialGenerated {
		return incompatible(table, "id has an owned sequence but no serial nextval default or identity generation")
	}
	return nil
}

// validateNamespaceNameUnique requires a UNIQUE constraint over exactly
// (project_namespace, project_name).
func validateNamespaceNameUnique(ctx context.Context, db DB, table string) error {
	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_constraint c
			JOIN LATERAL unnest(c.conkey) WITH ORDINALITY AS key(attnum, position) ON TRUE
			JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = key.attnum
			WHERE c.conrelid = to_regclass($1)
				AND c.contype = 'u'
				AND cardinality(c.conkey) = 2
			GROUP BY c.oid
			HAVING array_agg(a.attname ORDER BY key.position) = ARRAY['project_namespace', 'project_name']
		)`,
		table,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("inspect unique constraint for %s: %w", table, err)
	}
	if !exists {
		return incompatible(table, "project table must have a UNIQUE (project_namespace, project_name) constraint")
	}
	return nil
}

// hasExactProjectForeignKey accepts only project_id -> project.id with cascade
// deletion and a single-column key.
func hasExactProjectForeignKey(ctx context.Context, db DB, table, projectTable string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_constraint c
			JOIN pg_attribute src ON src.attrelid = c.conrelid AND src.attnum = c.conkey[1]
			JOIN pg_attribute tgt ON tgt.attrelid = c.confrelid AND tgt.attnum = c.confkey[1]
			WHERE c.contype = 'f'
				AND c.conrelid = to_regclass($1)
				AND c.confrelid = to_regclass($2)
				AND cardinality(c.conkey) = 1
				AND cardinality(c.confkey) = 1
				AND src.attname = 'project_id'
				AND tgt.attname = 'id'
				AND c.confdeltype = 'c'
		)`,
		table, projectTable,
	).Scan(&exists)
	return exists, err
}

func incompatible(table, format string, args ...interface{}) error {
	return fmt.Errorf("%w: %s: %s", ErrSchemaIncompatible, table, fmt.Sprintf(format, args...))
}

// errPositiveID is the validation guard for non-positive scenario IDs.
var errPositiveID = errors.New("scenario ID must be positive")

// errPositiveAttempt is the validation guard for non-positive translation
// attempts.
var errPositiveAttempt = errors.New("translation attempt must be positive")
