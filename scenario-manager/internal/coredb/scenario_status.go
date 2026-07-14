// Package coredb centralizes persistence for the Scenario Manager. This file
// contains the Scenario Status reads and guarded lifecycle updates shared by
// ingestion, Translator communication, and scenario selection.
package coredb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	// These predicates stay as literal SQL so PostgreSQL can prove that the
	// candidate queries match the partial indexes created in schema.go. Turning
	// the state names into query parameters can prevent that match when the
	// database chooses a generic prepared-query plan.
	actionableScenarioPredicate     = `state IN ('Created', 'StartingRunners', 'PostProcessing')`
	unpublishedTranslationPredicate = `state = 'Scheduled' AND translation_request_published_at IS NULL`

	// DefaultScenarioState is assigned by Scenario Manager when EDS payloads
	// do not carry a scenario state.
	DefaultScenarioState = "Created"
	// DefaultScenarioComputedReps initializes the computed repetitions counter
	// until downstream execution updates it.
	DefaultScenarioComputedReps = 0
	// DefaultScenarioContainerImage initializes the container image field when
	// the EDS payload does not provide one.
	DefaultScenarioContainerImage = ""
	// ScenarioStateCreated is the initial state assigned to scenarios that have
	// been ingested but not yet claimed for translation.
	ScenarioStateCreated = "Created"
	// ScenarioStateScheduled is the durable in-flight claim state for the
	// translation workflow.
	ScenarioStateScheduled = "Scheduled"
	// ScenarioStateStartingRunners means Translator has returned a container
	// image and Scenario Manager may proceed with runner startup later.
	ScenarioStateStartingRunners = "StartingRunners"
	// ScenarioStateInProcessing is part of the canonical lifecycle but not owned
	// by the translator workflow implemented in this step.
	ScenarioStateInProcessing = "InProcessing"
	// ScenarioStatePostProcessing is part of the canonical lifecycle but not
	// owned by the translator workflow implemented in this step.
	ScenarioStatePostProcessing = "PostProcessing"
	// ScenarioStateFinished is the terminal success state in the canonical
	// lifecycle.
	ScenarioStateFinished = "Finished"
	// ScenarioStateFailed is the workflow-generic terminal failure state.
	ScenarioStateFailed = "Failed"
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

// ScenarioForTranslation is the DB projection returned when Scenario Manager
// successfully claims a specific scenario row for translation handoff.
//
// The struct intentionally contains only the fields required to publish a
// translation request. Project carries the raw project name from the project
// table; transport adapters normalize it only when building broker subjects.
type ScenarioForTranslation struct {
	ID                 int
	Project            string
	TranslationAttempt int
	RecipeInfo         json.RawMessage
	ConfidenceMetric   *float64
}

// ScenarioActionCandidate is the intentionally small projection used by the
// selector. Selection does not need recipe, project, timing, or priority data.
type ScenarioActionCandidate struct {
	ID    int
	State string
}

// TranslationRecoveryCandidate identifies the exact Scheduled translation
// attempt found by recovery discovery. Carrying the attempt with the ID lets
// the later update reject a row that was reclaimed between discovery and use.
type TranslationRecoveryCandidate struct {
	ID                 int
	TranslationAttempt int
}

// TranslatorReadyStatus classifies terminal DB outcomes for ready handling
// without overloading Go errors for expected stale, duplicate, or poison cases.
type TranslatorReadyStatus string

const (
	// TranslatorReadyApplied means the current Scheduled attempt advanced to
	// StartingRunners and the container image was persisted.
	TranslatorReadyApplied TranslatorReadyStatus = "Applied"
	// TranslatorReadyDuplicate means the row was already StartingRunners for the
	// same attempt and already stored the same image.
	TranslatorReadyDuplicate TranslatorReadyStatus = "Duplicate"
	// TranslatorReadyConflict means the row was already StartingRunners for the
	// same attempt but stored a different image than the message supplied.
	TranslatorReadyConflict TranslatorReadyStatus = "Conflict"
	// TranslatorReadyIgnoredStale means the message referred to a stale attempt
	// or a row state that should no longer change.
	TranslatorReadyIgnoredStale TranslatorReadyStatus = "IgnoredStale"
	// TranslatorReadyPoison means no scenario row existed for the supplied id,
	// so the ready message is treated as a permanent poison message.
	TranslatorReadyPoison TranslatorReadyStatus = "Poison"
)

// TranslatorReadyResult reports the classified terminal outcome of attempting
// to apply a Translator ready message to the database.
type TranslatorReadyResult struct {
	Status        TranslatorReadyStatus
	PreviousState string
	CurrentState  string
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

// NextActionableScenario returns the globally lowest positive Scenario Status
// ID whose current state is owned by the basic selector. It only observes the
// row; the selected workflow handler performs the guarded state claim later.
func NextActionableScenario(ctx context.Context) (*ScenarioActionCandidate, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context must not be nil")
	}
	if err := ensureCoreDBPool(); err != nil {
		return nil, fmt.Errorf("find next actionable scenario: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT id, state
		FROM %s
		WHERE id > 0
			AND %s
		ORDER BY id ASC
		LIMIT 1`,
		scenarioStatusTableName(),
		actionableScenarioPredicate,
	)

	candidate := &ScenarioActionCandidate{}
	if err := coreDBPool.QueryRowContext(ctx, query).Scan(&candidate.ID, &candidate.State); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("find next actionable scenario: %w", err)
	}

	return candidate, nil
}

// NextStaleUnpublishedTranslationClaim returns the globally lowest positive
// Scheduled row whose publish confirmation is missing and whose durable claim
// is strictly older than claimedBefore. Discovery does not modify or lock it.
func NextStaleUnpublishedTranslationClaim(ctx context.Context, claimedBefore time.Time) (*TranslationRecoveryCandidate, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context must not be nil")
	}
	if claimedBefore.IsZero() {
		return nil, fmt.Errorf("claimed-before threshold must be set")
	}
	if err := ensureCoreDBPool(); err != nil {
		return nil, fmt.Errorf("find next stale unpublished translation claim: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT id, translation_attempts
		FROM %s
		WHERE id > 0
			AND %s
			AND updated_at < $1
		ORDER BY id ASC
		LIMIT 1`,
		scenarioStatusTableName(),
		unpublishedTranslationPredicate,
	)

	candidate := &TranslationRecoveryCandidate{}
	if err := coreDBPool.QueryRowContext(ctx, query, claimedBefore).Scan(
		&candidate.ID,
		&candidate.TranslationAttempt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("find next stale unpublished translation claim: %w", err)
	}

	return candidate, nil
}

// ClaimScenarioForTranslation claims exactly one supplied scenario id for the
// translator handoff workflow.
//
// This helper belongs to the persistence layer and owns the durable claim
// transition from Created to Scheduled. It locks only the requested row with
// FOR UPDATE, refuses to claim rows that are missing or no longer in Created,
// increments translation_attempts once for the new claim, clears any stale
// publish marker, and returns the claim projection needed for publishing. A nil
// result with nil error means "not claimable", not a DB failure.
func ClaimScenarioForTranslation(ctx context.Context, scenarioID int) (*ScenarioForTranslation, error) {
	if err := ensureCoreDBPool(); err != nil {
		return nil, err
	}
	if scenarioID <= 0 {
		return nil, fmt.Errorf("scenario ID must be positive")
	}

	tx, err := coreDBPool.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin translation claim: %w", err)
	}

	table := scenarioStatusTableName()
	projectTable := projectTableName()
	query := fmt.Sprintf(`
		SELECT s.state, p.project_name, s.recipe_info, s.confidence_metric
		FROM %s s
		JOIN %s p ON p.id = s.project_id
		WHERE s.id = $1
		FOR UPDATE`,
		table,
		projectTable,
	)

	var (
		state            string
		project          string
		recipeInfo       []byte
		confidenceMetric sql.NullFloat64
	)
	err = tx.QueryRowContext(ctx, query, scenarioID).Scan(
		&state,
		&project,
		&recipeInfo,
		&confidenceMetric,
	)
	if err != nil {
		_ = tx.Rollback()
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("lock scenario %d for translation: %w", scenarioID, err)
	}

	if state != ScenarioStateCreated {
		_ = tx.Rollback()
		return nil, nil
	}

	updateQuery := fmt.Sprintf(`
		UPDATE %s
		SET state = $2,
			translation_attempts = translation_attempts + 1,
			translation_request_published_at = NULL,
			updated_at = NOW()
		WHERE id = $1
		RETURNING translation_attempts`,
		table,
	)

	var nextAttempt int
	if err := tx.QueryRowContext(ctx, updateQuery, scenarioID, ScenarioStateScheduled).Scan(&nextAttempt); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("mark scenario %d scheduled for translation: %w", scenarioID, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit translation claim for scenario %d: %w", scenarioID, err)
	}

	claimed := &ScenarioForTranslation{
		ID:                 scenarioID,
		Project:            project,
		TranslationAttempt: nextAttempt,
		RecipeInfo:         json.RawMessage(recipeInfo),
	}
	if confidenceMetric.Valid {
		value := confidenceMetric.Float64
		claimed.ConfidenceMetric = &value
	}

	return claimed, nil
}

// MarkScenarioTranslationRequestPublished records that the exact claimed
// Scheduled attempt was successfully accepted by the configured transport.
//
// The conditional update prevents stale publishers from writing a publish
// marker onto a row that has already advanced, been retried, or been recovered.
// A false,nil result means no row matched the exact attempt guard and should be
// treated as a stale/no-op outcome rather than as a database error.
func MarkScenarioTranslationRequestPublished(ctx context.Context, scenarioID int, attempt int) (bool, error) {
	if err := ensureCoreDBPool(); err != nil {
		return false, err
	}
	if scenarioID <= 0 {
		return false, fmt.Errorf("scenario ID must be positive")
	}
	if attempt <= 0 {
		return false, fmt.Errorf("translation attempt must be positive")
	}

	query := fmt.Sprintf(`
		UPDATE %s
		SET translation_request_published_at = NOW(),
			updated_at = NOW()
		WHERE id = $1
			AND state = $2
			AND translation_attempts = $3
			AND translation_request_published_at IS NULL`,
		scenarioStatusTableName(),
	)

	result, err := coreDBPool.ExecContext(ctx, query, scenarioID, ScenarioStateScheduled, attempt)
	if err != nil {
		return false, fmt.Errorf("mark scenario %d attempt %d publish confirmed: %w", scenarioID, attempt, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect publish marker update for scenario %d attempt %d: %w", scenarioID, attempt, err)
	}

	return rows > 0, nil
}

// MarkScenarioTranslationAttemptFailed recovers or fails the exact Scheduled
// attempt addressed by a ready message or other semantic failure path.
//
// The row-match guards ensure that only the current Scheduled attempt can be
// moved back to Created or forward to Failed. If no row matches, the result is
// stale/no-op because some newer workflow state already superseded this exact
// attempt.
func MarkScenarioTranslationAttemptFailed(ctx context.Context, scenarioID int, attempt int, maxAttempts int) (bool, string, error) {
	if err := ensureCoreDBPool(); err != nil {
		return false, "", err
	}
	if scenarioID <= 0 {
		return false, "", fmt.Errorf("scenario ID must be positive")
	}
	if attempt <= 0 {
		return false, "", fmt.Errorf("translation attempt must be positive")
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
			AND translation_attempts = $3`,
		scenarioStatusTableName(),
	)

	result, err := coreDBPool.ExecContext(ctx, query, scenarioID, ScenarioStateScheduled, attempt, finalState)
	if err != nil {
		return false, "", fmt.Errorf("mark scenario %d attempt %d failed: %w", scenarioID, attempt, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, "", fmt.Errorf("inspect failed-attempt update for scenario %d attempt %d: %w", scenarioID, attempt, err)
	}

	return rows > 0, finalState, nil
}

// MarkScenarioTranslationPublishFailed handles the narrower case where the
// transport publish failed before Scenario Manager recorded a publish marker.
//
// This helper is stricter than MarkScenarioTranslationAttemptFailed because it
// must not overwrite rows where publish confirmation may already have been
// recorded. The exact row guard includes a NULL publish marker so only the
// unpublished Scheduled claim is eligible for recovery.
func MarkScenarioTranslationPublishFailed(ctx context.Context, scenarioID int, attempt int, maxAttempts int) (bool, string, error) {
	if err := ensureCoreDBPool(); err != nil {
		return false, "", err
	}
	if scenarioID <= 0 {
		return false, "", fmt.Errorf("scenario ID must be positive")
	}
	if attempt <= 0 {
		return false, "", fmt.Errorf("translation attempt must be positive")
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
			AND translation_request_published_at IS NULL`,
		scenarioStatusTableName(),
	)

	result, err := coreDBPool.ExecContext(ctx, query, scenarioID, ScenarioStateScheduled, attempt, finalState)
	if err != nil {
		return false, "", fmt.Errorf("mark scenario %d attempt %d publish failed: %w", scenarioID, attempt, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, "", fmt.Errorf("inspect publish-failed update for scenario %d attempt %d: %w", scenarioID, attempt, err)
	}

	return rows > 0, finalState, nil
}

// MarkScenarioTranslatorReady applies a non-empty Translator ready message to
// the exact scenario row addressed by scenarioID.
//
// This helper owns the semantic DB-side idempotency rules for ready messages:
// missing rows become Poison, stale attempts become IgnoredStale, matching
// Scheduled attempts transition to StartingRunners, and already-applied
// StartingRunners rows classify as Duplicate or Conflict depending on the
// stored image. Errors are reserved for transient DB problems only.
func MarkScenarioTranslatorReady(ctx context.Context, scenarioID int, translationAttempt int, containerImage string) (TranslatorReadyResult, error) {
	if err := ensureCoreDBPool(); err != nil {
		return TranslatorReadyResult{}, err
	}
	if scenarioID <= 0 {
		return TranslatorReadyResult{}, fmt.Errorf("scenario ID must be positive")
	}
	if translationAttempt <= 0 {
		return TranslatorReadyResult{}, fmt.Errorf("translation attempt must be positive")
	}

	containerImage = strings.TrimSpace(containerImage)
	if containerImage == "" {
		return TranslatorReadyResult{}, fmt.Errorf("container image must not be empty")
	}

	tx, err := coreDBPool.BeginTx(ctx, nil)
	if err != nil {
		return TranslatorReadyResult{}, fmt.Errorf("begin translator ready update: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT state, translation_attempts, container_image
		FROM %s
		WHERE id = $1
		FOR UPDATE`,
		scenarioStatusTableName(),
	)

	var (
		currentState   string
		currentAttempt int
		currentImage   sql.NullString
	)
	err = tx.QueryRowContext(ctx, query, scenarioID).Scan(&currentState, &currentAttempt, &currentImage)
	if err != nil {
		_ = tx.Rollback()
		if err == sql.ErrNoRows {
			return TranslatorReadyResult{Status: TranslatorReadyPoison}, nil
		}
		return TranslatorReadyResult{}, fmt.Errorf("lock scenario %d for translator ready handling: %w", scenarioID, err)
	}

	if currentAttempt != translationAttempt {
		_ = tx.Rollback()
		return TranslatorReadyResult{
			Status:        TranslatorReadyIgnoredStale,
			PreviousState: currentState,
			CurrentState:  currentState,
		}, nil
	}

	currentImageValue := strings.TrimSpace(currentImage.String)
	switch currentState {
	case ScenarioStateScheduled:
		updateQuery := fmt.Sprintf(`
			UPDATE %s
			SET state = $2,
				container_image = $3,
				updated_at = NOW()
			WHERE id = $1`,
			scenarioStatusTableName(),
		)
		if _, err := tx.ExecContext(ctx, updateQuery, scenarioID, ScenarioStateStartingRunners, containerImage); err != nil {
			_ = tx.Rollback()
			return TranslatorReadyResult{}, fmt.Errorf("persist translator ready for scenario %d attempt %d: %w", scenarioID, translationAttempt, err)
		}
		if err := tx.Commit(); err != nil {
			return TranslatorReadyResult{}, fmt.Errorf("commit translator ready for scenario %d attempt %d: %w", scenarioID, translationAttempt, err)
		}
		return TranslatorReadyResult{
			Status:        TranslatorReadyApplied,
			PreviousState: currentState,
			CurrentState:  ScenarioStateStartingRunners,
		}, nil
	case ScenarioStateStartingRunners:
		_ = tx.Rollback()
		if currentImageValue == containerImage {
			return TranslatorReadyResult{
				Status:        TranslatorReadyDuplicate,
				PreviousState: currentState,
				CurrentState:  currentState,
			}, nil
		}
		return TranslatorReadyResult{
			Status:        TranslatorReadyConflict,
			PreviousState: currentState,
			CurrentState:  currentState,
		}, nil
	default:
		_ = tx.Rollback()
		return TranslatorReadyResult{
			Status:        TranslatorReadyIgnoredStale,
			PreviousState: currentState,
			CurrentState:  currentState,
		}, nil
	}
}

// RecoverUnpublishedTranslationClaim recovers exactly one unpublished Scheduled
// claim when higher-level orchestration decides to check that scenario ID and
// translation attempt.
//
// The update targets only rows still in Scheduled, still missing a publish
// marker, still on the discovered attempt, and older than claimedBefore. Zero
// affected rows mean newer work already superseded discovery, which is a normal
// no-op rather than a database failure.
func RecoverUnpublishedTranslationClaim(
	ctx context.Context,
	scenarioID int,
	expectedAttempt int,
	claimedBefore time.Time,
	maxAttempts int,
) (bool, string, error) {
	if ctx == nil {
		return false, "", fmt.Errorf("context must not be nil")
	}
	if scenarioID <= 0 {
		return false, "", fmt.Errorf("scenario ID must be positive")
	}
	if expectedAttempt <= 0 {
		return false, "", fmt.Errorf("expected translation attempt must be positive")
	}
	if claimedBefore.IsZero() {
		return false, "", fmt.Errorf("claimed-before threshold must be set")
	}
	if maxAttempts <= 0 {
		return false, "", fmt.Errorf("max attempts must be positive")
	}
	if err := ensureCoreDBPool(); err != nil {
		return false, "", fmt.Errorf("recover unpublished translation claim for scenario %d: %w", scenarioID, err)
	}

	query := fmt.Sprintf(`
		UPDATE %s
		SET state = CASE
				WHEN translation_attempts < $4 THEN $5
				ELSE $6
			END,
			updated_at = NOW()
		WHERE id = $1
			AND state = $2
			AND translation_attempts = $3
			AND translation_request_published_at IS NULL
			AND updated_at < $7
		RETURNING state`,
		scenarioStatusTableName(),
	)

	var finalState string
	err := coreDBPool.QueryRowContext(
		ctx,
		query,
		scenarioID,
		ScenarioStateScheduled,
		expectedAttempt,
		maxAttempts,
		ScenarioStateCreated,
		ScenarioStateFailed,
		claimedBefore,
	).Scan(&finalState)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, "", nil
		}
		return false, "", fmt.Errorf("recover unpublished translation claim for scenario %d: %w", scenarioID, err)
	}

	return true, finalState, nil
}

// MarkScenarioInProcessing claims runner startup before the selector invokes
// the runner placeholder. A false result means another worker changed the row
// after candidate discovery.
func MarkScenarioInProcessing(ctx context.Context, scenarioID int) (bool, error) {
	return markScenarioState(
		ctx,
		scenarioID,
		ScenarioStateStartingRunners,
		ScenarioStateInProcessing,
		"mark scenario in processing",
	)
}

// MarkScenarioFinished records the successful terminal result of the
// post-processing decision, but only while the row remains PostProcessing.
func MarkScenarioFinished(ctx context.Context, scenarioID int) (bool, error) {
	return markScenarioState(
		ctx,
		scenarioID,
		ScenarioStatePostProcessing,
		ScenarioStateFinished,
		"mark scenario finished",
	)
}

// MarkScenarioRequiresMoreRuns returns a PostProcessing row to runner startup
// without changing its repetition or confidence fields.
func MarkScenarioRequiresMoreRuns(ctx context.Context, scenarioID int) (bool, error) {
	return markScenarioState(
		ctx,
		scenarioID,
		ScenarioStatePostProcessing,
		ScenarioStateStartingRunners,
		"mark scenario as requiring more runs",
	)
}

// MarkScenarioFailedFrom applies a terminal failure only when the row still has
// the exact non-terminal state owned by the caller. The allowlist is deliberately
// case-sensitive; trimming or accepting future states could overwrite work that
// this version of Scenario Manager does not own.
func MarkScenarioFailedFrom(ctx context.Context, scenarioID int, expectedState string) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("context must not be nil")
	}
	if scenarioID <= 0 {
		return false, fmt.Errorf("scenario ID must be positive")
	}

	switch expectedState {
	case ScenarioStateCreated,
		ScenarioStateScheduled,
		ScenarioStateStartingRunners,
		ScenarioStateInProcessing,
		ScenarioStatePostProcessing:
		// These are the canonical non-terminal states that callers may own.
	default:
		return false, fmt.Errorf("expected state %q is not a supported non-terminal state", expectedState)
	}

	return markScenarioState(
		ctx,
		scenarioID,
		expectedState,
		ScenarioStateFailed,
		"mark scenario failed",
	)
}

// markScenarioState performs one guarded lifecycle transition. It intentionally
// uses no preliminary SELECT or transaction: PostgreSQL evaluates the ID and
// expected-state guard together with the update, so concurrent losers receive a
// harmless zero-row result.
func markScenarioState(
	ctx context.Context,
	scenarioID int,
	expectedState string,
	nextState string,
	operation string,
) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("context must not be nil")
	}
	if scenarioID <= 0 {
		return false, fmt.Errorf("scenario ID must be positive")
	}
	if err := ensureCoreDBPool(); err != nil {
		return false, fmt.Errorf(
			"%s for scenario %d from %s to %s: %w",
			operation,
			scenarioID,
			expectedState,
			nextState,
			err,
		)
	}

	query := fmt.Sprintf(`
		UPDATE %s
		SET state = $2,
			updated_at = NOW()
		WHERE id = $1
			AND state = $3`,
		scenarioStatusTableName(),
	)

	result, err := coreDBPool.ExecContext(ctx, query, scenarioID, nextState, expectedState)
	if err != nil {
		return false, fmt.Errorf(
			"%s for scenario %d from %s to %s: %w",
			operation,
			scenarioID,
			expectedState,
			nextState,
			err,
		)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf(
			"inspect %s result for scenario %d from %s to %s: %w",
			operation,
			scenarioID,
			expectedState,
			nextState,
			err,
		)
	}
	if rows > 1 {
		return false, fmt.Errorf(
			"inspect %s result for scenario %d: guarded update changed %d rows",
			operation,
			scenarioID,
			rows,
		)
	}

	return rows == 1, nil
}
