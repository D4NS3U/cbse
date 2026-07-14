package coredb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestBSSLCoreDBValidationPrecedesPoolAccess runs without PostgreSQL. Setting
// the shared handle aside proves invalid input is rejected by each new public
// boundary instead of being hidden behind a connection error.
func TestBSSLCoreDBValidationPrecedesPoolAccess(t *testing.T) {
	originalPool := coreDBPool
	coreDBPool = nil
	t.Cleanup(func() { coreDBPool = originalPool })

	cutoff := time.Now()
	tests := []struct {
		name string
		call func() error
	}{
		{name: "actionable nil context", call: func() error { _, err := NextActionableScenario(nil); return err }},
		{name: "recovery lookup nil context", call: func() error { _, err := NextStaleUnpublishedTranslationClaim(nil, cutoff); return err }},
		{name: "recovery lookup zero cutoff", call: func() error {
			_, err := NextStaleUnpublishedTranslationClaim(context.Background(), time.Time{})
			return err
		}},
		{name: "exact recovery nil context", call: func() error { _, _, err := RecoverUnpublishedTranslationClaim(nil, 1, 1, cutoff, 3); return err }},
		{name: "exact recovery invalid id", call: func() error {
			_, _, err := RecoverUnpublishedTranslationClaim(context.Background(), 0, 1, cutoff, 3)
			return err
		}},
		{name: "exact recovery invalid attempt", call: func() error {
			_, _, err := RecoverUnpublishedTranslationClaim(context.Background(), 1, 0, cutoff, 3)
			return err
		}},
		{name: "exact recovery zero cutoff", call: func() error {
			_, _, err := RecoverUnpublishedTranslationClaim(context.Background(), 1, 1, time.Time{}, 3)
			return err
		}},
		{name: "exact recovery invalid max", call: func() error {
			_, _, err := RecoverUnpublishedTranslationClaim(context.Background(), 1, 1, cutoff, 0)
			return err
		}},
		{name: "guard nil context", call: func() error { _, err := MarkScenarioInProcessing(nil, 1); return err }},
		{name: "guard invalid id", call: func() error { _, err := MarkScenarioFinished(context.Background(), 0); return err }},
		{name: "failure guard invalid state", call: func() error {
			_, err := MarkScenarioFailedFrom(context.Background(), 1, ScenarioStateFinished)
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if strings.Contains(err.Error(), "pool is not initialized") {
				t.Fatalf("validation touched the DB pool first: %v", err)
			}
		})
	}
}

// TestBSSLCoreDBIntegration exercises the PostgreSQL behavior that cannot be
// proved by selector unit tests. It follows the package's existing environment
// gate, so ordinary local test runs need neither PostgreSQL nor SQL mocks.
func TestBSSLCoreDBIntegration(t *testing.T) {
	requireEnv(t, coreDBDSNEnv, coreDBUserEnv, coreDBPasswordEnv)

	if !CoreDBConnect() {
		t.Fatal("CoreDBConnect returned false; ensure the integration database is reachable")
	}
	t.Cleanup(func() {
		if coreDBPool != nil {
			_ = coreDBPool.Close()
			coreDBPool = nil
		}
	})

	// A unique schema keeps explicit IDs and table assertions independent from
	// developer data and from any other integration-test run.
	schema := fmt.Sprintf("bssl_it_%d", time.Now().UnixNano())
	projectTable := schema + ".project"
	scenarioTable := schema + ".scenario_status"
	t.Setenv(projectTableEnv, projectTable)
	t.Setenv(scenarioStatusTableEnv, scenarioTable)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := coreDBPool.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = coreDBPool.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	})

	if err := createSchema(ctx); err != nil {
		t.Fatalf("create isolated BSSL schema: %v", err)
	}

	t.Run("actionable candidates use global positive ID order", func(t *testing.T) {
		resetBSSLTables(t, ctx, scenarioTable, projectTable)
		pendingProject := insertBSSLProject(t, ctx, projectTable, "pending", "Pending")
		completedProject := insertBSSLProject(t, ctx, projectTable, "completed", "Completed")

		old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		insertBSSLScenario(t, ctx, scenarioTable, -4, pendingProject, ScenarioStateCreated, 0, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 1, pendingProject, ScenarioStateInProcessing, 0, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 2, pendingProject, "Unknown", 0, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 5, completedProject, ScenarioStatePostProcessing, 0, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 9, pendingProject, ScenarioStateCreated, 0, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 12, completedProject, ScenarioStateStartingRunners, 1, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 13, pendingProject, ScenarioStateScheduled, 1, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 14, pendingProject, ScenarioStateFinished, 0, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 15, pendingProject, ScenarioStateFailed, 0, old, nil)

		assertNextActionable(t, ctx, 5, ScenarioStatePostProcessing)
		setBSSLState(t, ctx, scenarioTable, 5, ScenarioStateFinished)
		assertNextActionable(t, ctx, 9, ScenarioStateCreated)
		setBSSLState(t, ctx, scenarioTable, 9, ScenarioStateFinished)
		assertNextActionable(t, ctx, 12, ScenarioStateStartingRunners)
		setBSSLState(t, ctx, scenarioTable, 12, ScenarioStateInProcessing)

		candidate, err := NextActionableScenario(ctx)
		if err != nil {
			t.Fatalf("lookup after exhausting actionable rows: %v", err)
		}
		if candidate != nil {
			t.Fatalf("candidate after exhausting actionable rows = %+v, want nil", candidate)
		}
	})

	t.Run("recovery uses strict cutoff and exact attempt", func(t *testing.T) {
		resetBSSLTables(t, ctx, scenarioTable, projectTable)
		projectID := insertBSSLProject(t, ctx, projectTable, "recovery", "InProgress")
		cutoff := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
		old := cutoff.Add(-time.Second)

		insertBSSLScenario(t, ctx, scenarioTable, 1, projectID, ScenarioStateScheduled, 2, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 2, projectID, ScenarioStateScheduled, 3, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 3, projectID, ScenarioStateScheduled, 1, cutoff, nil)
		publishedAt := old
		insertBSSLScenario(t, ctx, scenarioTable, 4, projectID, ScenarioStateScheduled, 1, old, &publishedAt)
		insertBSSLScenario(t, ctx, scenarioTable, 5, projectID, ScenarioStateCreated, 1, old, nil)

		candidate, err := NextStaleUnpublishedTranslationClaim(ctx, cutoff)
		if err != nil {
			t.Fatalf("discover recovery candidate: %v", err)
		}
		if candidate == nil || candidate.ID != 1 || candidate.TranslationAttempt != 2 {
			t.Fatalf("recovery candidate = %+v, want id=1 attempt=2", candidate)
		}

		changed, finalState, err := RecoverUnpublishedTranslationClaim(ctx, 1, 1, cutoff, 3)
		if err != nil {
			t.Fatalf("recover using stale attempt: %v", err)
		}
		if changed || finalState != "" {
			t.Fatalf("stale-attempt recovery = (%v, %q), want (false, empty)", changed, finalState)
		}
		assertBSSLState(t, ctx, scenarioTable, 1, ScenarioStateScheduled)

		changed, finalState, err = RecoverUnpublishedTranslationClaim(ctx, 1, 2, cutoff, 3)
		if err != nil || !changed || finalState != ScenarioStateCreated {
			t.Fatalf("below-limit recovery = (%v, %q, %v), want (true, Created, nil)", changed, finalState, err)
		}

		candidate, err = NextStaleUnpublishedTranslationClaim(ctx, cutoff)
		if err != nil {
			t.Fatalf("discover second recovery candidate: %v", err)
		}
		if candidate == nil || candidate.ID != 2 || candidate.TranslationAttempt != 3 {
			t.Fatalf("second recovery candidate = %+v, want id=2 attempt=3", candidate)
		}
		changed, finalState, err = RecoverUnpublishedTranslationClaim(ctx, 2, 3, cutoff, 3)
		if err != nil || !changed || finalState != ScenarioStateFailed {
			t.Fatalf("at-limit recovery = (%v, %q, %v), want (true, Failed, nil)", changed, finalState, err)
		}

		// Equality is deliberately fresh because the SQL contract uses updated_at < cutoff.
		changed, finalState, err = RecoverUnpublishedTranslationClaim(ctx, 3, 1, cutoff, 3)
		if err != nil || changed || finalState != "" {
			t.Fatalf("cutoff-equality recovery = (%v, %q, %v), want stale no-op", changed, finalState, err)
		}
		candidate, err = NextStaleUnpublishedTranslationClaim(ctx, cutoff)
		if err != nil || candidate != nil {
			t.Fatalf("recovery lookup after guarded cases = (%+v, %v), want nil, nil", candidate, err)
		}
	})

	t.Run("late ready from a superseded attempt is ignored", func(t *testing.T) {
		resetBSSLTables(t, ctx, scenarioTable, projectTable)
		projectID := insertBSSLProject(t, ctx, projectTable, "late-ready", "InProgress")
		insertBSSLScenario(t, ctx, scenarioTable, 1, projectID, ScenarioStateScheduled, 2, time.Now().Add(-time.Minute), nil)

		result, err := MarkScenarioTranslatorReady(ctx, 1, 1, "example/old-attempt:latest")
		if err != nil {
			t.Fatalf("apply late ready message: %v", err)
		}
		if result.Status != TranslatorReadyIgnoredStale {
			t.Fatalf("late ready status = %q, want %q", result.Status, TranslatorReadyIgnoredStale)
		}
		assertBSSLState(t, ctx, scenarioTable, 1, ScenarioStateScheduled)
	})

	t.Run("guarded lifecycle transitions reject stale rows", func(t *testing.T) {
		resetBSSLTables(t, ctx, scenarioTable, projectTable)
		projectID := insertBSSLProject(t, ctx, projectTable, "guards", "InProgress")
		old := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
		insertBSSLScenario(t, ctx, scenarioTable, 1, projectID, ScenarioStateStartingRunners, 2, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 2, projectID, ScenarioStatePostProcessing, 2, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 3, projectID, ScenarioStatePostProcessing, 2, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 4, projectID, ScenarioStateInProcessing, 2, old, nil)
		insertBSSLScenario(t, ctx, scenarioTable, 5, projectID, ScenarioStateCreated, 0, old, nil)

		immutableBefore := make(map[int]string, 4)
		updatedBefore := make(map[int]time.Time, 4)
		for id := 1; id <= 4; id++ {
			immutableBefore[id] = bsslImmutableFingerprint(t, ctx, scenarioTable, id)
			updatedBefore[id] = bsslUpdatedAt(t, ctx, scenarioTable, id)
		}

		changed, err := MarkScenarioInProcessing(ctx, 1)
		assertBSSLTransition(t, changed, err, true)
		assertBSSLState(t, ctx, scenarioTable, 1, ScenarioStateInProcessing)
		assertBSSLOnlyStateAndUpdatedAtChanged(t, ctx, scenarioTable, 1, immutableBefore[1], updatedBefore[1])
		changed, err = MarkScenarioInProcessing(ctx, 1)
		assertBSSLTransition(t, changed, err, false)

		changed, err = MarkScenarioFinished(ctx, 2)
		assertBSSLTransition(t, changed, err, true)
		assertBSSLState(t, ctx, scenarioTable, 2, ScenarioStateFinished)
		assertBSSLOnlyStateAndUpdatedAtChanged(t, ctx, scenarioTable, 2, immutableBefore[2], updatedBefore[2])
		changed, err = MarkScenarioFinished(ctx, 2)
		assertBSSLTransition(t, changed, err, false)
		changed, err = MarkScenarioRequiresMoreRuns(ctx, 3)
		assertBSSLTransition(t, changed, err, true)
		assertBSSLState(t, ctx, scenarioTable, 3, ScenarioStateStartingRunners)
		assertBSSLOnlyStateAndUpdatedAtChanged(t, ctx, scenarioTable, 3, immutableBefore[3], updatedBefore[3])
		changed, err = MarkScenarioRequiresMoreRuns(ctx, 3)
		assertBSSLTransition(t, changed, err, false)
		changed, err = MarkScenarioFailedFrom(ctx, 4, ScenarioStateInProcessing)
		assertBSSLTransition(t, changed, err, true)
		assertBSSLState(t, ctx, scenarioTable, 4, ScenarioStateFailed)
		assertBSSLOnlyStateAndUpdatedAtChanged(t, ctx, scenarioTable, 4, immutableBefore[4], updatedBefore[4])
		changed, err = MarkScenarioFailedFrom(ctx, 4, ScenarioStateInProcessing)
		assertBSSLTransition(t, changed, err, false)

		// Exercise every state on the deliberately exact failure allowlist.
		allowedFailureStates := []string{
			ScenarioStateCreated,
			ScenarioStateScheduled,
			ScenarioStateStartingRunners,
			ScenarioStateInProcessing,
			ScenarioStatePostProcessing,
		}
		for offset, state := range allowedFailureStates {
			id := 10 + offset
			insertBSSLScenario(t, ctx, scenarioTable, id, projectID, state, 2, old, nil)
			changed, err := MarkScenarioFailedFrom(ctx, id, state)
			assertBSSLTransition(t, changed, err, true)
			assertBSSLState(t, ctx, scenarioTable, id, ScenarioStateFailed)
		}

		for _, invalidState := range []string{"", " Created", "created", "Unknown", ScenarioStateFinished, ScenarioStateFailed} {
			if changed, err := MarkScenarioFailedFrom(ctx, 5, invalidState); err == nil || changed {
				t.Fatalf("invalid state %q allowlist result = (%v, %v), want validation error", invalidState, changed, err)
			}
		}
		assertBSSLState(t, ctx, scenarioTable, 5, ScenarioStateCreated)
		changed, err = MarkScenarioFinished(ctx, 5)
		assertBSSLTransition(t, changed, err, false)
	})

	t.Run("legacy Scenario Status schema fails without repair", func(t *testing.T) {
		legacyTable := schema + ".legacy_status"
		t.Setenv(scenarioStatusTableEnv, legacyTable)

		// container_image is intentionally absent. Startup must report operator
		// remediation instead of silently adding this Translator-era column.
		query := fmt.Sprintf(`CREATE TABLE %s (
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
			confidence_metric DOUBLE PRECISION
		)`, legacyTable, projectTable)
		if _, err := coreDBPool.ExecContext(ctx, query); err != nil {
			t.Fatalf("create incompatible legacy table: %v", err)
		}

		err := createSchema(ctx)
		if err == nil || !strings.Contains(err.Error(), legacyTable) || !strings.Contains(err.Error(), "migrate or recreate") {
			t.Fatalf("legacy validation error = %v, want named manual-remediation error", err)
		}
		var columnExists bool
		if err := coreDBPool.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_attribute
				WHERE attrelid = to_regclass($1)
					AND attname = 'container_image'
					AND NOT attisdropped
			)`, legacyTable).Scan(&columnExists); err != nil {
			t.Fatalf("inspect incompatible table after validation: %v", err)
		}
		if columnExists {
			t.Fatal("legacy validation silently repaired the missing column")
		}
	})

	t.Run("legacy project-link incompatibilities fail validation", func(t *testing.T) {
		tests := []struct {
			name              string
			tableBase         string
			projectDefinition string
			prepareOrphan     bool
		}{
			{
				name:              "nullable project id",
				tableBase:         "nullable_status",
				projectDefinition: "INTEGER REFERENCES " + projectTable + "(id) ON DELETE CASCADE",
			},
			{
				name:              "foreign key without cascade",
				tableBase:         "noncascade_status",
				projectDefinition: "INTEGER NOT NULL REFERENCES " + projectTable + "(id)",
			},
			{
				name:              "orphan row behind not-valid constraint",
				tableBase:         "orphan_status",
				projectDefinition: "INTEGER NOT NULL",
				prepareOrphan:     true,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				table := schema + "." + test.tableBase
				t.Setenv(scenarioStatusTableEnv, table)
				createBSSLLegacyStatusTable(t, ctx, table, test.projectDefinition)
				if test.prepareOrphan {
					if _, err := coreDBPool.ExecContext(ctx, "INSERT INTO "+table+" (project_id, state) VALUES (999999, 'Created')"); err != nil {
						t.Fatalf("insert orphan scenario: %v", err)
					}
					constraint := fmt.Sprintf(
						"ALTER TABLE %s ADD FOREIGN KEY (project_id) REFERENCES %s(id) ON DELETE CASCADE NOT VALID",
						table,
						projectTable,
					)
					if _, err := coreDBPool.ExecContext(ctx, constraint); err != nil {
						t.Fatalf("add not-valid project constraint: %v", err)
					}
				}

				err := createSchema(ctx)
				if err == nil || !strings.Contains(err.Error(), table) || !strings.Contains(err.Error(), "migrate or recreate") {
					t.Fatalf("project-link validation error = %v, want named manual-remediation error", err)
				}
			})
		}
	})

	t.Run("schema setup is idempotent and creates both partial indexes", func(t *testing.T) {
		if err := createSchema(ctx); err != nil {
			t.Fatalf("second schema setup: %v", err)
		}
		if err := createSchema(ctx); err != nil {
			t.Fatalf("third schema setup: %v", err)
		}

		rows, err := coreDBPool.QueryContext(ctx, `
			SELECT indexname, indexdef
			FROM pg_indexes
			WHERE schemaname = $1
				AND tablename = 'scenario_status'
				AND indexname IN (
					'scenario_status_actionable_id_idx',
					'scenario_status_unpublished_translation_id_idx'
				)`, schema)
		if err != nil {
			t.Fatalf("query BSSL indexes: %v", err)
		}
		defer rows.Close()

		definitions := make(map[string]string)
		for rows.Next() {
			var name, definition string
			if err := rows.Scan(&name, &definition); err != nil {
				t.Fatalf("scan BSSL index: %v", err)
			}
			definitions[name] = definition
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate BSSL indexes: %v", err)
		}

		actionable := definitions["scenario_status_actionable_id_idx"]
		if !strings.Contains(actionable, "(id)") ||
			!strings.Contains(actionable, ScenarioStateCreated) ||
			!strings.Contains(actionable, ScenarioStateStartingRunners) ||
			!strings.Contains(actionable, ScenarioStatePostProcessing) {
			t.Fatalf("unexpected actionable index definition: %q", actionable)
		}
		recovery := definitions["scenario_status_unpublished_translation_id_idx"]
		if !strings.Contains(recovery, "(id)") ||
			!strings.Contains(recovery, ScenarioStateScheduled) ||
			!strings.Contains(recovery, "translation_request_published_at IS NULL") {
			t.Fatalf("unexpected recovery index definition: %q", recovery)
		}
	})

	t.Run("custom table base derives table-specific index names", func(t *testing.T) {
		customTable := schema + ".custom_status"
		t.Setenv(scenarioStatusTableEnv, customTable)
		if err := createSchema(ctx); err != nil {
			t.Fatalf("create custom Scenario Status table: %v", err)
		}
		if err := createSchema(ctx); err != nil {
			t.Fatalf("repeat custom Scenario Status setup: %v", err)
		}

		rows, err := coreDBPool.QueryContext(ctx, `
			SELECT indexname, indexdef
			FROM pg_indexes
			WHERE schemaname = $1
				AND tablename = 'custom_status'
				AND indexname IN (
					'custom_status_actionable_id_idx',
					'custom_status_unpublished_translation_id_idx'
				)`, schema)
		if err != nil {
			t.Fatalf("query custom-table BSSL indexes: %v", err)
		}
		defer rows.Close()

		definitions := make(map[string]string)
		for rows.Next() {
			var name, definition string
			if err := rows.Scan(&name, &definition); err != nil {
				t.Fatalf("scan custom-table BSSL index: %v", err)
			}
			definitions[name] = definition
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate custom-table BSSL indexes: %v", err)
		}
		if len(definitions) != 2 {
			t.Fatalf("custom-table BSSL index count = %d, want 2", len(definitions))
		}
		for name, definition := range definitions {
			if !strings.Contains(definition, " ON "+schema+".custom_status USING btree (id)") {
				t.Fatalf("index %s targets the wrong table or columns: %q", name, definition)
			}
		}
	})
}

func createBSSLLegacyStatusTable(
	t *testing.T,
	ctx context.Context,
	table string,
	projectDefinition string,
) {
	t.Helper()
	query := fmt.Sprintf(`CREATE TABLE %s (
		id SERIAL PRIMARY KEY,
		project_id %s,
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
	)`, table, projectDefinition)
	if _, err := coreDBPool.ExecContext(ctx, query); err != nil {
		t.Fatalf("create legacy Scenario Status table %s: %v", table, err)
	}
}

func resetBSSLTables(t *testing.T, ctx context.Context, scenarioTable, projectTable string) {
	t.Helper()
	if _, err := coreDBPool.ExecContext(ctx, "TRUNCATE "+scenarioTable+", "+projectTable+" RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("reset isolated tables: %v", err)
	}
}

func insertBSSLProject(t *testing.T, ctx context.Context, table, name, status string) int {
	t.Helper()
	var id int
	query := "INSERT INTO " + table + " (project_name, number_of_components, status) VALUES ($1, 1, $2) RETURNING id"
	if err := coreDBPool.QueryRowContext(ctx, query, name, status).Scan(&id); err != nil {
		t.Fatalf("insert project %q: %v", name, err)
	}
	return id
}

func insertBSSLScenario(
	t *testing.T,
	ctx context.Context,
	table string,
	id int,
	projectID int,
	state string,
	attempt int,
	updatedAt time.Time,
	publishedAt *time.Time,
) {
	t.Helper()
	var marker interface{}
	if publishedAt != nil {
		marker = *publishedAt
	}
	query := "INSERT INTO " + table + ` (
		id, project_id, state, created_at, updated_at, priority,
		number_of_reps, number_of_computed_reps, translation_attempts,
		translation_request_published_at, recipe_info, container_image, confidence_metric
	) VALUES ($1, $2, $3, $4, $5, 17, 23, 11, $6, $7, '{"integration":true}'::jsonb, 'example/runner:test', 0.75)`
	if _, err := coreDBPool.ExecContext(
		ctx,
		query,
		id,
		projectID,
		state,
		updatedAt.Add(-time.Hour),
		updatedAt,
		attempt,
		marker,
	); err != nil {
		t.Fatalf("insert scenario %d: %v", id, err)
	}
}

func assertNextActionable(t *testing.T, ctx context.Context, id int, state string) {
	t.Helper()
	candidate, err := NextActionableScenario(ctx)
	if err != nil {
		t.Fatalf("find actionable candidate: %v", err)
	}
	if candidate == nil || candidate.ID != id || candidate.State != state {
		t.Fatalf("actionable candidate = %+v, want id=%d state=%s", candidate, id, state)
	}
}

func setBSSLState(t *testing.T, ctx context.Context, table string, id int, state string) {
	t.Helper()
	if _, err := coreDBPool.ExecContext(ctx, "UPDATE "+table+" SET state = $2 WHERE id = $1", id, state); err != nil {
		t.Fatalf("set scenario %d state: %v", id, err)
	}
}

func assertBSSLState(t *testing.T, ctx context.Context, table string, id int, want string) {
	t.Helper()
	var got string
	if err := coreDBPool.QueryRowContext(ctx, "SELECT state FROM "+table+" WHERE id = $1", id).Scan(&got); err != nil {
		t.Fatalf("read scenario %d state: %v", id, err)
	}
	if got != want {
		t.Fatalf("scenario %d state = %q, want %q", id, got, want)
	}
}

func assertBSSLTransition(t *testing.T, changed bool, err error, wantChanged bool) {
	t.Helper()
	if err != nil {
		t.Fatalf("guarded transition: %v", err)
	}
	if changed != wantChanged {
		t.Fatalf("guarded transition changed = %v, want %v", changed, wantChanged)
	}
}

func bsslImmutableFingerprint(t *testing.T, ctx context.Context, table string, id int) string {
	t.Helper()
	var fingerprint sql.NullString
	query := "SELECT ROW(project_id, created_at, priority, number_of_reps, number_of_computed_reps, " +
		"translation_attempts, translation_request_published_at, recipe_info, container_image, confidence_metric)::text " +
		"FROM " + table + " WHERE id = $1"
	if err := coreDBPool.QueryRowContext(ctx, query, id).Scan(&fingerprint); err != nil {
		t.Fatalf("read immutable fingerprint for scenario %d: %v", id, err)
	}
	return fingerprint.String
}

func bsslUpdatedAt(t *testing.T, ctx context.Context, table string, id int) time.Time {
	t.Helper()
	var updatedAt time.Time
	if err := coreDBPool.QueryRowContext(ctx, "SELECT updated_at FROM "+table+" WHERE id = $1", id).Scan(&updatedAt); err != nil {
		t.Fatalf("read updated_at for scenario %d: %v", id, err)
	}
	return updatedAt
}

func assertBSSLOnlyStateAndUpdatedAtChanged(
	t *testing.T,
	ctx context.Context,
	table string,
	id int,
	wantFingerprint string,
	previousUpdatedAt time.Time,
) {
	t.Helper()
	if got := bsslImmutableFingerprint(t, ctx, table, id); got != wantFingerprint {
		t.Fatalf("transition changed non-state scenario data for %d\nbefore: %s\nafter:  %s", id, wantFingerprint, got)
	}
	if got := bsslUpdatedAt(t, ctx, table, id); !got.After(previousUpdatedAt) {
		t.Fatalf("scenario %d updated_at = %s, want later than %s", id, got, previousUpdatedAt)
	}
}
