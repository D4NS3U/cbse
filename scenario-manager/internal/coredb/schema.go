package coredb

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
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

// createSchema creates new tables, but deliberately does not repair an existing
// Scenario Status table. Silent repair could reinterpret rows from an older
// lifecycle. Existing tables are therefore validated read-only and operators
// receive a migration error before the selector starts.
func createSchema(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context must not be nil")
	}
	if err := ensureCoreDBPool(); err != nil {
		return err
	}

	// Determine the Scenario Status path before issuing any maintenance DDL.
	// Existing Scenario Status tables are validation-only throughout startup.
	scenarioStatusTable := scenarioStatusTableName()
	scenarioStatusExists, err := tableExists(ctx, scenarioStatusTable)
	if err != nil {
		return fmt.Errorf("check presence of %s: %w", scenarioStatusTable, err)
	}

	projectTable := projectTableName()
	createProjectTable := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id SERIAL PRIMARY KEY,
		project_name TEXT NOT NULL,
		number_of_components INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT ''
	)`, projectTable)

	if _, err := coreDBPool.ExecContext(ctx, createProjectTable); err != nil {
		return fmt.Errorf("create %s: %w", projectTable, err)
	}

	if err := ensureProjectTableColumns(ctx, projectTable); err != nil {
		return err
	}
	if err := validateProjectTable(ctx, projectTable); err != nil {
		return err
	}

	if !scenarioStatusExists {
		// IF NOT EXISTS keeps concurrent Scenario Manager startups safe. The
		// validation immediately below still catches a different legacy table
		// created between the existence check and this statement.
		createScenarioStatusTable := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id SERIAL PRIMARY KEY,
			project_id INTEGER NOT NULL REFERENCES %s(id) ON DELETE CASCADE,
			state TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			priority INTEGER NOT NULL DEFAULT 0,
			number_of_reps INTEGER NOT NULL DEFAULT 0,
			number_of_computed_reps INTEGER NOT NULL DEFAULT 0,
			translation_attempts INTEGER NOT NULL DEFAULT 0,
			translation_request_published_at TIMESTAMPTZ,
			recipe_info JSONB,
			container_image TEXT,
			confidence_metric DOUBLE PRECISION
		)`, scenarioStatusTable, projectTable)

		if _, err := coreDBPool.ExecContext(ctx, createScenarioStatusTable); err != nil {
			return fmt.Errorf("create %s: %w", scenarioStatusTable, err)
		}
	}

	if err := validateScenarioStatusTable(ctx, scenarioStatusTable, projectTable); err != nil {
		return err
	}
	if err := createScenarioStatusIndexes(ctx, scenarioStatusTable); err != nil {
		return err
	}

	return nil
}

// ensureProjectTableColumns migrates older schema versions where the project
// table existed before number_of_components and status were introduced.
func ensureProjectTableColumns(ctx context.Context, projectTable string) error {
	addComponents := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS number_of_components INTEGER NOT NULL DEFAULT 0`, projectTable)
	if _, err := coreDBPool.ExecContext(ctx, addComponents); err != nil {
		return fmt.Errorf("ensure %s.number_of_components column: %w", projectTable, err)
	}

	addStatus := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT ''`, projectTable)
	if _, err := coreDBPool.ExecContext(ctx, addStatus); err != nil {
		return fmt.Errorf("ensure %s.status column: %w", projectTable, err)
	}

	return nil
}

// catalogColumn is the small subset of PostgreSQL catalog metadata needed to
// decide whether an existing table is safe for current Scenario Manager code.
type catalogColumn struct {
	postgresType      string
	notNull           bool
	defaultExpression sql.NullString
}

// validateProjectTable checks the columns used by inserts, lookups, and the
// Translator join. The two older optional columns continue to be maintained by
// ensureProjectTableColumns above.
func validateProjectTable(ctx context.Context, table string) error {
	columns, err := loadTableColumns(ctx, table)
	if err != nil {
		return fmt.Errorf("inspect project table %s: %w", table, err)
	}

	if err := requireTableColumn(table, columns, "id", "int4", true); err != nil {
		return err
	}
	if err := requireTableColumn(table, columns, "project_name", "text", true); err != nil {
		return err
	}
	if err := validateSoleIDPrimaryKey(ctx, table); err != nil {
		return err
	}
	if err := validateGeneratedIDSequence(ctx, table); err != nil {
		return err
	}

	return nil
}

// validateScenarioStatusTable applies the full compatibility contract required
// by BSSL. Extra columns are harmless, but every column used by current code
// must have the exact PostgreSQL type, nullability, and required default.
func validateScenarioStatusTable(ctx context.Context, table, projectTable string) error {
	columns, err := loadTableColumns(ctx, table)
	if err != nil {
		return fmt.Errorf("inspect Scenario Status table %s: %w", table, err)
	}

	requiredColumns := []struct {
		name         string
		postgresType string
		notNull      bool
	}{
		{name: "id", postgresType: "int4", notNull: true},
		{name: "project_id", postgresType: "int4", notNull: true},
		{name: "state", postgresType: "text", notNull: true},
		{name: "created_at", postgresType: "timestamptz", notNull: true},
		{name: "updated_at", postgresType: "timestamptz", notNull: true},
		{name: "priority", postgresType: "int4", notNull: true},
		{name: "number_of_reps", postgresType: "int4", notNull: true},
		{name: "number_of_computed_reps", postgresType: "int4", notNull: true},
		{name: "translation_attempts", postgresType: "int4", notNull: true},
		{name: "translation_request_published_at", postgresType: "timestamptz", notNull: false},
		{name: "recipe_info", postgresType: "jsonb", notNull: false},
		{name: "container_image", postgresType: "text", notNull: false},
		{name: "confidence_metric", postgresType: "float8", notNull: false},
	}
	for _, required := range requiredColumns {
		if err := requireTableColumn(table, columns, required.name, required.postgresType, required.notNull); err != nil {
			return err
		}
	}

	for _, columnName := range []string{"created_at", "updated_at"} {
		column := columns[columnName]
		if !column.defaultExpression.Valid {
			return incompatibleTable(table, "column %s must default to now() or CURRENT_TIMESTAMP", columnName)
		}
		normalized := normalizeDefaultExpression(column.defaultExpression.String)
		if normalized != "now()" && normalized != "current_timestamp" {
			return incompatibleTable(
				table,
				"column %s has incompatible default %q; expected now() or CURRENT_TIMESTAMP",
				columnName,
				column.defaultExpression.String,
			)
		}
	}

	for _, columnName := range []string{
		"priority",
		"number_of_reps",
		"number_of_computed_reps",
		"translation_attempts",
	} {
		column := columns[columnName]
		if !column.defaultExpression.Valid || normalizeDefaultExpression(column.defaultExpression.String) != "0" {
			actual := "<none>"
			if column.defaultExpression.Valid {
				actual = column.defaultExpression.String
			}
			return incompatibleTable(table, "column %s has incompatible default %q; expected 0", columnName, actual)
		}
	}

	if err := validateSoleIDPrimaryKey(ctx, table); err != nil {
		return err
	}
	if err := validateGeneratedIDSequence(ctx, table); err != nil {
		return err
	}

	validForeignKey, err := hasExactProjectForeignKey(ctx, table, projectTable)
	if err != nil {
		return fmt.Errorf("inspect %s project foreign key: %w", table, err)
	}
	if !validForeignKey {
		return incompatibleTable(
			table,
			"project_id must be a single-column foreign key to %s.id with ON DELETE CASCADE",
			projectTable,
		)
	}

	invalidProjectLink, err := hasInvalidProjectLink(ctx, table, projectTable)
	if err != nil {
		return fmt.Errorf("inspect existing project links in %s: %w", table, err)
	}
	if invalidProjectLink {
		return incompatibleTable(table, "existing rows contain a null project_id or reference a missing row in %s", projectTable)
	}

	return nil
}

// loadTableColumns reads PostgreSQL's real type and constraint metadata rather
// than relying on column names reported by information_schema.
func loadTableColumns(ctx context.Context, table string) (map[string]catalogColumn, error) {
	rows, err := coreDBPool.QueryContext(ctx, `
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
		var (
			name   string
			column catalogColumn
		)
		if err := rows.Scan(
			&name,
			&column.postgresType,
			&column.notNull,
			&column.defaultExpression,
		); err != nil {
			return nil, err
		}
		columns[name] = column
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return columns, nil
}

func requireTableColumn(
	table string,
	columns map[string]catalogColumn,
	name string,
	postgresType string,
	notNull bool,
) error {
	column, exists := columns[name]
	if !exists {
		return incompatibleTable(table, "required column %s is missing", name)
	}
	if column.postgresType != postgresType {
		return incompatibleTable(
			table,
			"column %s has PostgreSQL type %s; expected %s",
			name,
			column.postgresType,
			postgresType,
		)
	}
	if column.notNull != notNull {
		expected := "nullable"
		if notNull {
			expected = "NOT NULL"
		}
		return incompatibleTable(table, "column %s has incompatible nullability; expected %s", name, expected)
	}

	return nil
}

// validateSoleIDPrimaryKey rejects composite keys and keys on a legacy column.
func validateSoleIDPrimaryKey(ctx context.Context, table string) error {
	rows, err := coreDBPool.QueryContext(ctx, `
		SELECT a.attname
		FROM pg_constraint c
		JOIN LATERAL unnest(c.conkey) WITH ORDINALITY AS key(attnum, position) ON TRUE
		JOIN pg_attribute a
			ON a.attrelid = c.conrelid
			AND a.attnum = key.attnum
		WHERE c.conrelid = to_regclass($1)
			AND c.contype = 'p'
		ORDER BY key.position`,
		table,
	)
	if err != nil {
		return fmt.Errorf("inspect primary key for %s: %w", table, err)
	}
	defer rows.Close()

	var primaryKeyColumns []string
	for rows.Next() {
		var columnName string
		if err := rows.Scan(&columnName); err != nil {
			return fmt.Errorf("inspect primary key for %s: %w", table, err)
		}
		primaryKeyColumns = append(primaryKeyColumns, columnName)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect primary key for %s: %w", table, err)
	}
	if len(primaryKeyColumns) != 1 || primaryKeyColumns[0] != "id" {
		return incompatibleTable(table, "primary key must contain only id")
	}

	return nil
}

// pg_get_serial_sequence supports both SERIAL and identity-backed INTEGER IDs.
// Sequence ownership alone is not enough: a legacy SERIAL column can retain an
// owned sequence after its nextval default was dropped. In that case ordinary
// inserts no longer generate IDs, so validation also proves either identity
// metadata or a default dependency on the owned sequence.
func validateGeneratedIDSequence(ctx context.Context, table string) error {
	var (
		sequence          sql.NullString
		identityGenerated bool
		serialGenerated   bool
	)
	if err := coreDBPool.QueryRowContext(ctx, `
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
			AND NOT a.attisdropped`, table).Scan(&sequence, &identityGenerated, &serialGenerated); err != nil {
		return fmt.Errorf("inspect generated ID sequence for %s: %w", table, err)
	}
	if !sequence.Valid || strings.TrimSpace(sequence.String) == "" {
		return incompatibleTable(table, "id must be generated by a serial- or identity-backed sequence")
	}
	if !identityGenerated && !serialGenerated {
		return incompatibleTable(table, "id has an owned sequence but no serial nextval default or identity generation")
	}
	return nil
}

// hasExactProjectForeignKey accepts only project_id -> project.id with cascade
// deletion. The cardinality checks prevent a composite foreign key from being
// mistaken for the required single-column relationship.
func hasExactProjectForeignKey(ctx context.Context, table, projectTable string) (bool, error) {
	var exists bool
	err := coreDBPool.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_constraint c
			JOIN pg_attribute source_column
				ON source_column.attrelid = c.conrelid
				AND source_column.attnum = c.conkey[1]
			JOIN pg_attribute target_column
				ON target_column.attrelid = c.confrelid
				AND target_column.attnum = c.confkey[1]
			WHERE c.contype = 'f'
				AND c.conrelid = to_regclass($1)
				AND c.confrelid = to_regclass($2)
				AND cardinality(c.conkey) = 1
				AND cardinality(c.confkey) = 1
				AND source_column.attname = 'project_id'
				AND target_column.attname = 'id'
				AND c.confdeltype = 'c'
		)`,
		table,
		projectTable,
	).Scan(&exists)
	return exists, err
}

// hasInvalidProjectLink checks the data as well as the declared constraint. A
// nullable legacy column or a NOT VALID foreign key can otherwise hide rows
// that current code cannot safely join to a Project.
func hasInvalidProjectLink(ctx context.Context, table, projectTable string) (bool, error) {
	query := fmt.Sprintf(`
		SELECT EXISTS (
			SELECT 1
			FROM %s scenario
			LEFT JOIN %s project ON project.id = scenario.project_id
			WHERE scenario.project_id IS NULL
				OR project.id IS NULL
		)`,
		table,
		projectTable,
	)

	var invalid bool
	err := coreDBPool.QueryRowContext(ctx, query).Scan(&invalid)
	return invalid, err
}

// normalizeDefaultExpression removes only presentation details PostgreSQL may
// add around a catalog expression. It does not evaluate arbitrary SQL, so an
// expression such as "1 - 1" remains incompatible even though it yields zero.
func normalizeDefaultExpression(expression string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(expression), ""))
	castSuffixes := []string{
		"::integer",
		"::int4",
		"::timestampwithtimezone",
		"::timestamptz",
	}

	for {
		previous := normalized
		for _, suffix := range castSuffixes {
			if strings.HasSuffix(normalized, suffix) {
				normalized = strings.TrimSuffix(normalized, suffix)
				break
			}
		}
		normalized = stripOuterParentheses(normalized)
		if normalized == previous {
			return normalized
		}
	}
}

func stripOuterParentheses(expression string) string {
	if len(expression) < 2 || expression[0] != '(' || expression[len(expression)-1] != ')' {
		return expression
	}

	depth := 0
	for position := 0; position < len(expression); position++ {
		switch expression[position] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && position != len(expression)-1 {
				return expression
			}
			if depth < 0 {
				return expression
			}
		}
	}
	if depth != 0 {
		return expression
	}
	return expression[1 : len(expression)-1]
}

// createScenarioStatusIndexes adds lookup accelerators only after the table is
// known to be compatible. PostgreSQL creates an unqualified index name in the
// same schema as the table named by ON, so default and custom schemas coexist.
func createScenarioStatusIndexes(ctx context.Context, table string) error {
	baseName := unqualifiedTableName(table)
	if baseName == "" {
		return fmt.Errorf("derive index names for Scenario Status table %q", table)
	}

	indexes := []struct {
		name      string
		predicate string
	}{
		{name: baseName + "_actionable_id_idx", predicate: actionableScenarioPredicate},
		{name: baseName + "_unpublished_translation_id_idx", predicate: unpublishedTranslationPredicate},
	}
	for _, index := range indexes {
		query := fmt.Sprintf(
			"CREATE INDEX IF NOT EXISTS %s ON %s (id) WHERE %s",
			index.name,
			table,
			index.predicate,
		)
		if _, err := coreDBPool.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("create index %s on %s: %w", index.name, table, err)
		}
	}

	return nil
}

func unqualifiedTableName(table string) string {
	if separator := strings.LastIndex(table, "."); separator >= 0 {
		table = table[separator+1:]
	}
	return strings.Trim(table, `"`)
}

func incompatibleTable(table, format string, args ...interface{}) error {
	detail := fmt.Sprintf(format, args...)
	return fmt.Errorf(
		"table %s is incompatible: %s; migrate or recreate it before starting Scenario Manager",
		table,
		detail,
	)
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
