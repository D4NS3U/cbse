package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ScenarioForTranslation is the DB projection returned when the Scenario
// Manager claims a scenario row for translation handoff. It carries the
// explicit (ProjectNamespace, ProjectName) identity so the publisher can
// construct the exact request subject.
type ScenarioForTranslation struct {
	ID                 int
	ProjectNamespace   string
	ProjectName        string
	TranslationAttempt int
	RecipeInfo         json.RawMessage
	ConfidenceMetric   *float64
}

// ClaimScenarioForTranslation claims exactly the supplied scenario id for the
// translator handoff workflow. It locks the row with FOR UPDATE, refuses rows
// that are missing or no longer in Created, increments translation_attempts
// once for the new claim, clears BOTH translation_publish_started_at and
// translation_request_published_at, and returns the claim projection needed for
// publishing. A nil result with nil error means "not claimable", not a DB
// failure.
func ClaimScenarioForTranslation(ctx context.Context, store Store, scenarioID int) (*ScenarioForTranslation, error) {
	if scenarioID <= 0 {
		return nil, errPositiveID
	}
	tx, err := store.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin translation claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	sTable := ScenarioStatusTable()
	pTable := ProjectTable()
	query := fmt.Sprintf(`
		SELECT s.state, p.project_namespace, p.project_name, s.recipe_info, s.confidence_metric
		FROM %s s
		JOIN %s p ON p.id = s.project_id
		WHERE s.id = $1
		FOR UPDATE`, sTable, pTable)

	var (
		state            string
		namespace        string
		project          string
		recipeInfo       []byte
		confidenceMetric sql.NullFloat64
	)
	err = tx.QueryRowContext(ctx, query, scenarioID).Scan(&state, &namespace, &project, &recipeInfo, &confidenceMetric)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lock scenario %d for translation: %w", scenarioID, err)
	}
	if state != ScenarioStateCreated {
		return nil, nil
	}

	update := fmt.Sprintf(`
		UPDATE %s
		SET state = $2,
			translation_attempts = translation_attempts + 1,
			translation_publish_started_at = NULL,
			translation_request_published_at = NULL,
			updated_at = NOW()
		WHERE id = $1
		RETURNING translation_attempts`, sTable)
	var nextAttempt int
	if err := tx.QueryRowContext(ctx, update, scenarioID, ScenarioStateScheduled).Scan(&nextAttempt); err != nil {
		return nil, fmt.Errorf("mark scenario %d scheduled for translation: %w", scenarioID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit translation claim for scenario %d: %w", scenarioID, err)
	}
	claimed := &ScenarioForTranslation{
		ID:                 scenarioID,
		ProjectNamespace:   namespace,
		ProjectName:        project,
		TranslationAttempt: nextAttempt,
		RecipeInfo:         json.RawMessage(recipeInfo),
	}
	if confidenceMetric.Valid {
		v := confidenceMetric.Float64
		claimed.ConfidenceMetric = &v
	}
	return claimed, nil
}

// MarkTranslationPublishStarted records that the owning worker is about to
// call NATS publication for the exact Scheduled attempt. It sets
// translation_publish_started_at = NOW() only for the exact Scheduled attempt
// while BOTH translation_publish_started_at and translation_request_published_at
// are null. Only a caller that changes that row may invoke NATS publication;
// from this point the attempt is never decremented or reused because NATS may
// have accepted the request even when confirmation is lost. A false result
// means the row was no longer an unpublished Scheduled claim for that attempt.
func MarkTranslationPublishStarted(ctx context.Context, db DB, scenarioID, attempt int) (bool, error) {
	if scenarioID <= 0 {
		return false, errPositiveID
	}
	if attempt <= 0 {
		return false, errPositiveAttempt
	}
	query := fmt.Sprintf(`
		UPDATE %s
		SET translation_publish_started_at = NOW(),
			updated_at = NOW()
		WHERE id = $1
			AND state = $2
			AND translation_attempts = $3
			AND translation_publish_started_at IS NULL
			AND translation_request_published_at IS NULL`,
		ScenarioStatusTable())
	res, err := db.ExecContext(ctx, query, scenarioID, ScenarioStateScheduled, attempt)
	if err != nil {
		return false, fmt.Errorf("mark scenario %d attempt %d publish started: %w", scenarioID, attempt, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect publish-start update for scenario %d attempt %d: %w", scenarioID, attempt, err)
	}
	return rows > 0, nil
}

// MarkScenarioTranslationRequestPublished records that the exact claimed
// Scheduled attempt was durably accepted by the transport. It sets
// translation_request_published_at = NOW() for the exact Scheduled attempt and
// requires a non-null translation_publish_started_at marker. A false result
// means no row matched the exact attempt guard (a stale no-op).
func MarkScenarioTranslationRequestPublished(ctx context.Context, db DB, scenarioID, attempt int) (bool, error) {
	if scenarioID <= 0 {
		return false, errPositiveID
	}
	if attempt <= 0 {
		return false, errPositiveAttempt
	}
	query := fmt.Sprintf(`
		UPDATE %s
		SET translation_request_published_at = NOW(),
			updated_at = NOW()
		WHERE id = $1
			AND state = $2
			AND translation_attempts = $3
			AND translation_publish_started_at IS NOT NULL
			AND translation_request_published_at IS NULL`,
		ScenarioStatusTable())
	res, err := db.ExecContext(ctx, query, scenarioID, ScenarioStateScheduled, attempt)
	if err != nil {
		return false, fmt.Errorf("mark scenario %d attempt %d publish confirmed: %w", scenarioID, attempt, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect publish marker update for scenario %d attempt %d: %w", scenarioID, attempt, err)
	}
	return rows > 0, nil
}

// CancelUnpublishedTranslationClaim cancels a claim whose publication never
// began. It requires the exact Scheduled attempt with BOTH publication
// timestamps null, restores Created, decrements translation_attempts by one,
// and updates updated_at. A zero-row result is a successful stale no-op:
// either the row already advanced or publication already began. This is
// compensation for work that never began and is allowed even after the
// lifecycle gate closes.
func CancelUnpublishedTranslationClaim(ctx context.Context, db DB, scenarioID, attempt int) (bool, error) {
	if scenarioID <= 0 {
		return false, errPositiveID
	}
	if attempt <= 0 {
		return false, errPositiveAttempt
	}
	query := fmt.Sprintf(`
		UPDATE %s
		SET state = $4,
			translation_attempts = translation_attempts - 1,
			translation_publish_started_at = NULL,
			translation_request_published_at = NULL,
			updated_at = NOW()
		WHERE id = $1
			AND state = $2
			AND translation_attempts = $3
			AND translation_publish_started_at IS NULL
			AND translation_request_published_at IS NULL`,
		ScenarioStatusTable())
	res, err := db.ExecContext(ctx, query, scenarioID, ScenarioStateScheduled, attempt, ScenarioStateCreated)
	if err != nil {
		return false, fmt.Errorf("cancel unpublished translation claim for scenario %d attempt %d: %w", scenarioID, attempt, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect cancel-unpublished update for scenario %d attempt %d: %w", scenarioID, attempt, err)
	}
	return rows > 0, nil
}

// RecoverUnpublishedTranslationClaim recovers an old Scheduled claim whose
// publication is ambiguous. For an old attempt with BOTH publication timestamps
// null it refunds the attempt (restoring Created, or Failed at the attempt
// limit). For an old attempt with translation_publish_started_at set and
// translation_request_published_at null, publication is ambiguous, so recovery
// applies the attempt-consuming Created/Failed policy WITHOUT refunding the
// attempt. A confirmed request (translation_request_published_at non-null) is
// not recovered. Zero rows means newer work superseded discovery (a no-op).
func RecoverUnpublishedTranslationClaim(ctx context.Context, db DB, scenarioID, expectedAttempt int, claimedBefore time.Time, maxAttempts int) (bool, string, error) {
	if scenarioID <= 0 {
		return false, "", errPositiveID
	}
	if expectedAttempt <= 0 {
		return false, "", errPositiveAttempt
	}
	if claimedBefore.IsZero() {
		return false, "", fmt.Errorf("claimed-before threshold must be set")
	}
	if maxAttempts <= 0 {
		return false, "", fmt.Errorf("max attempts must be positive")
	}
	finalState := ScenarioStateCreated
	if expectedAttempt >= maxAttempts {
		finalState = ScenarioStateFailed
	}
	// Two branches in one guarded update:
	//  - both-null: refund the attempt (translation_attempts - 1).
	//  - started-set + published-null: consume the attempt (no refund).
	// A confirmed request is excluded by the published-at IS NULL guard and
	// the state = Scheduled guard.
	query := fmt.Sprintf(`
		UPDATE %s
		SET state = $4,
			translation_attempts = CASE
				WHEN translation_publish_started_at IS NULL AND translation_request_published_at IS NULL
					THEN translation_attempts - 1
				ELSE translation_attempts
			END,
			updated_at = NOW()
		WHERE id = $1
			AND state = $2
			AND translation_attempts = $3
			AND translation_request_published_at IS NULL
			AND updated_at < $5
		RETURNING state`,
		ScenarioStatusTable())
	var got string
	err := db.QueryRowContext(ctx, query, scenarioID, ScenarioStateScheduled, expectedAttempt, finalState, claimedBefore).Scan(&got)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("recover unpublished translation claim for scenario %d: %w", scenarioID, err)
	}
	return true, got, nil
}

// MarkScenarioTranslationPublishFailed handles a transport publish failure
// before the publish-start marker could be confirmed. It targets only the
// exact Scheduled attempt with both publication timestamps null, restores
// Created (or Failed at the limit), and never refunds the attempt once the
// publish-start marker was set.
func MarkScenarioTranslationPublishFailed(ctx context.Context, db DB, scenarioID, attempt, maxAttempts int) (bool, string, error) {
	if scenarioID <= 0 {
		return false, "", errPositiveID
	}
	if attempt <= 0 {
		return false, "", errPositiveAttempt
	}
	if maxAttempts <= 0 {
		return false, "", fmt.Errorf("max attempts must be positive")
	}
	finalState := ScenarioStateCreated
	if attempt >= maxAttempts {
		finalState = ScenarioStateFailed
	}
	query := fmt.Sprintf(`
		UPDATE %s
		SET state = $4,
			updated_at = NOW()
		WHERE id = $1
			AND state = $2
			AND translation_attempts = $3
			AND translation_publish_started_at IS NULL
			AND translation_request_published_at IS NULL`,
		ScenarioStatusTable())
	res, err := db.ExecContext(ctx, query, scenarioID, ScenarioStateScheduled, attempt, finalState)
	if err != nil {
		return false, "", fmt.Errorf("mark scenario %d attempt %d publish failed: %w", scenarioID, attempt, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, "", fmt.Errorf("inspect publish-failed update for scenario %d attempt %d: %w", scenarioID, attempt, err)
	}
	return rows > 0, finalState, nil
}

// terminalFailureStates lists the non-terminal scenario states that the
// terminal-action bulk update moves to Failed.
var terminalFailureStates = []string{
	ScenarioStateCreated,
	ScenarioStateScheduled,
	ScenarioStateStartingRunners,
	ScenarioStateInProcessing,
	ScenarioStatePostProcessing,
}

// MarkScenariosFailedForProject applies the Error/Failed terminal action in a
// single transaction: it moves every scenario row of the project whose state
// is one of the non-terminal lifecycle states (Created, Scheduled,
// StartingRunners, InProcessing, PostProcessing) to Failed, preserving computed
// repetitions, attempts, timestamps, the runner image, and diagnostics. An
// absent project or zero matching rows is success (cleanup already satisfied).
// It returns the number of rows moved to Failed.
func MarkScenariosFailedForProject(ctx context.Context, store Store, projectID int) (int64, error) {
	if projectID <= 0 {
		return 0, fmt.Errorf("project id must be positive")
	}
	tx, err := store.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin terminal bulk update for project %d: %w", projectID, err)
	}
	defer func() { _ = tx.Rollback() }()

	// Confirm the project row still exists; an absent project is success.
	var exists bool
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1)`, ProjectTable()), projectID).Scan(&exists)
	if err != nil {
		return 0, fmt.Errorf("verify project %d exists for terminal update: %w", projectID, err)
	}
	if !exists {
		return 0, nil
	}

	// Build an IN-list for the non-terminal states. Layout: $1 = Failed,
	// $2 = project_id, $3.. = non-terminal states.
	args := make([]interface{}, 0, 2+len(terminalFailureStates))
	args = append(args, ScenarioStateFailed, projectID)
	placeholders := make([]string, 0, len(terminalFailureStates))
	for i := range terminalFailureStates {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+3))
		args = append(args, terminalFailureStates[i])
	}
	query := fmt.Sprintf(`
		UPDATE %s
		SET state = $1, updated_at = NOW()
		WHERE project_id = $2 AND state IN (%s)`,
		ScenarioStatusTable(), strings_join(placeholders, ", "))

	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("bulk-mark scenarios Failed for project %d: %w", projectID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("inspect terminal bulk update for project %d: %w", projectID, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit terminal bulk update for project %d: %w", projectID, err)
	}
	return rows, nil
}

// strings_join avoids importing strings solely for Join in the SQL builder.
func strings_join(elems []string, sep string) string {
	out := ""
	for i, e := range elems {
		if i > 0 {
			out += sep
		}
		out += e
	}
	return out
}
