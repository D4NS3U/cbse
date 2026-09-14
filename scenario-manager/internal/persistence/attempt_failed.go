package persistence

import (
	"context"
	"fmt"
)

// MarkScenarioTranslationAttemptFailed consumes the current translation attempt
// for a Scheduled scenario when the Translator returns an empty (failure)
// ready image. It guards only on state = Scheduled and the exact attempt, so it
// matches a row whose translation_request_published_at is already set (the
// normal state when a Translator ready arrives after a durably accepted
// request). It restores Created below the attempt limit and moves to Failed at
// the limit, and never refunds the attempt (translation_attempts is unchanged).
// A false result means the row was no longer Scheduled for the exact attempt
// (stale): the caller treats the ready as handled without a transition.
//
// This function is the alpha4 analogue of the alpha3
// coredb.MarkScenarioTranslationAttemptFailed used by the Translator-ready
// workflow. It is distinct from MarkScenarioTranslationPublishFailed, which
// targets only the pre-publish failure case (both publication timestamps null)
// owned by the selection loop. The Slice 06.5 ready workflow owns the
// empty-image path; this function is the minimal additive persistence
// transition that path requires (see the slice's resolution guidance for a
// missing domain function that is genuinely part of this slice's owned
// deliverables).
func MarkScenarioTranslationAttemptFailed(ctx context.Context, db DB, scenarioID, attempt, maxAttempts int) (bool, string, error) {
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
			AND translation_attempts = $3`,
		ScenarioStatusTable())
	res, err := db.ExecContext(ctx, query, scenarioID, ScenarioStateScheduled, attempt, finalState)
	if err != nil {
		return false, "", fmt.Errorf("mark scenario %d attempt %d failed: %w", scenarioID, attempt, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, "", fmt.Errorf("inspect failed-attempt update for scenario %d attempt %d: %w", scenarioID, attempt, err)
	}
	return rows > 0, finalState, nil
}
