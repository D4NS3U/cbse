package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// This file owns the Slice 06 runner-start and observation persistence
// surface: the guarded StartingRunners/InProcessing transitions, the
// monotonic computed-repetitions update, the runner-start and observation
// projections, and the discovery queries that list StartingRunners and
// InProcessing scenario IDs in ascending positive-ID order.
//
// The serial BSL selector no longer owns StartingRunners (see
// actionableScenarioPredicate); the bounded ordered runner-start scheduler
// (scenario-manager/internal/alpha4/runnerstart) discovers StartingRunners
// rows directly and the per-scenario observation queue
// (scenario-manager/internal/alpha4/observation) discovers InProcessing rows
// directly. Both use the deterministic Job name and guarded database
// transitions so multiple SM replicas converge.

// nonTerminalFailureStates lists the non-terminal scenario states from which a
// guarded single-row failure transition is permitted. It mirrors the
// terminal-action bulk set so a runtime Forbidden, ownership collision, or
// invalid projection can fail an individual scenario from any non-terminal
// state the runner-start or observation workflows encounter.
var nonTerminalFailureStates = []string{
	ScenarioStateCreated,
	ScenarioStateScheduled,
	ScenarioStateStartingRunners,
	ScenarioStateInProcessing,
	ScenarioStatePostProcessing,
}

// failureAllowList lets MarkScenarioFailedFrom reject an unexpected from-state
// without a SQL round-trip.
var failureAllowList = func() map[string]struct{} {
	m := make(map[string]struct{}, len(nonTerminalFailureStates))
	for _, s := range nonTerminalFailureStates {
		m[s] = struct{}{}
	}
	return m
}()

// MarkScenarioInProcessing transitions a scenario from StartingRunners to
// InProcessing. It is the guarded runner-start success transition applied after
// a matching Job exists (created or ownership-verified AlreadyExists). A false
// result means the row was no longer StartingRunners: either another replica
// won the transition (stale success), or the lifecycle gate closed and the
// terminal action already moved the row to Failed. The caller treats a
// zero-row result as stale success unless it created the Job, in which case it
// follows the create/gate-closure cleanup contract.
func MarkScenarioInProcessing(ctx context.Context, db DB, scenarioID int) (bool, error) {
	if scenarioID <= 0 {
		return false, errPositiveID
	}
	query := fmt.Sprintf(`
		UPDATE %s
		SET state = $2, updated_at = NOW()
		WHERE id = $1 AND state = $3`,
		ScenarioStatusTable())
	res, err := db.ExecContext(ctx, query, scenarioID, ScenarioStateInProcessing, ScenarioStateStartingRunners)
	if err != nil {
		return false, fmt.Errorf("mark scenario %d in-processing: %w", scenarioID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect in-processing update for scenario %d: %w", scenarioID, err)
	}
	return rows > 0, nil
}

// MarkScenarioPostProcessing transitions a scenario from InProcessing to
// PostProcessing. It is applied after a Complete Job recorded all requested
// repetitions. A false result means the row was no longer InProcessing (stale
// success or terminal action). This branch does not execute post-processing;
// PostProcessing is a normal-execution boundary state.
func MarkScenarioPostProcessing(ctx context.Context, db DB, scenarioID int) (bool, error) {
	if scenarioID <= 0 {
		return false, errPositiveID
	}
	query := fmt.Sprintf(`
		UPDATE %s
		SET state = $2, updated_at = NOW()
		WHERE id = $1 AND state = $3`,
		ScenarioStatusTable())
	res, err := db.ExecContext(ctx, query, scenarioID, ScenarioStatePostProcessing, ScenarioStateInProcessing)
	if err != nil {
		return false, fmt.Errorf("mark scenario %d post-processing: %w", scenarioID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect post-processing update for scenario %d: %w", scenarioID, err)
	}
	return rows > 0, nil
}

// MarkScenarioFailedFrom transitions a scenario to Failed only when its current
// state is exactly fromState, preserving computed repetitions, attempts,
// timestamps, the runner image, and diagnostics. It is the guarded single-row
// failure transition for runner-start startup failures
// (StartingRunners -> Failed) and observation failures
// (InProcessing -> Failed). A false result is a stale no-op. An unsupported
// fromState is a programmer error and returns an error without a SQL round-trip.
func MarkScenarioFailedFrom(ctx context.Context, db DB, scenarioID int, fromState string) (bool, error) {
	if scenarioID <= 0 {
		return false, errPositiveID
	}
	if _, ok := failureAllowList[fromState]; !ok {
		return false, fmt.Errorf("unsupported failure source state %q", fromState)
	}
	query := fmt.Sprintf(`
		UPDATE %s
		SET state = $2, updated_at = NOW()
		WHERE id = $1 AND state = $3`,
		ScenarioStatusTable())
	res, err := db.ExecContext(ctx, query, scenarioID, ScenarioStateFailed, fromState)
	if err != nil {
		return false, fmt.Errorf("mark scenario %d failed from %q: %w", scenarioID, fromState, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect failure update for scenario %d: %w", scenarioID, err)
	}
	return rows > 0, nil
}

// UpdateScenarioComputedRepsMonotonic atomically sets number_of_computed_reps
// to LEAST(number_of_reps, GREATEST(number_of_computed_reps, count)) while
// guarding scenario ID and state InProcessing. The LEAST clamp guarantees the
// value never exceeds number_of_reps even if a caller supplies a count at the
// boundary; the adapter validates completedIndexes against 0..number_of_reps-1
// before calling. It returns the resulting computed-reps value and whether a
// row matched. A false match means the row was no longer InProcessing (stale
// success or terminal action); the caller does not apply a further transition.
func UpdateScenarioComputedRepsMonotonic(ctx context.Context, db DB, scenarioID, count int) (int, bool, error) {
	if scenarioID <= 0 {
		return 0, false, errPositiveID
	}
	if count < 0 {
		return 0, false, fmt.Errorf("computed reps count must not be negative")
	}
	query := fmt.Sprintf(`
		UPDATE %s
		SET number_of_computed_reps = LEAST(number_of_reps, GREATEST(number_of_computed_reps, $2))
		WHERE id = $1 AND state = $3
		RETURNING number_of_computed_reps`,
		ScenarioStatusTable())
	var updated int
	err := db.QueryRowContext(ctx, query, scenarioID, count, ScenarioStateInProcessing).Scan(&updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("update computed reps for scenario %d: %w", scenarioID, err)
	}
	return updated, true, nil
}

// RunnerStartProjection is the DB projection the runner-start reconciler loads
// for one StartingRunners scenario. It carries the persisted runner digest
// (container_image), the requested and computed repetition counts, the
// translation attempt, and the static project identity needed to read the live
// experiment from Kubernetes.
type RunnerStartProjection struct {
	ID                   int
	TranslationAttempt   int
	NumberOfReps         int
	NumberOfComputedReps int
	ContainerImage       string
	ProjectNamespace     string
	ProjectName          string
}

// LoadRunnerStartProjection loads the runner-start projection for one scenario
// that is currently in StartingRunners. A nil result with nil error means the
// row is absent or no longer StartingRunners (stale): the reconciler removes
// the ID without a state transition.
func LoadRunnerStartProjection(ctx context.Context, db DB, scenarioID int) (*RunnerStartProjection, error) {
	if scenarioID <= 0 {
		return nil, errPositiveID
	}
	query := fmt.Sprintf(`
		SELECT s.id, s.translation_attempts, s.number_of_reps, s.number_of_computed_reps,
			COALESCE(s.container_image, ''), p.project_namespace, p.project_name
		FROM %s s
		JOIN %s p ON p.id = s.project_id
		WHERE s.id = $1 AND s.state = $2`,
		ScenarioStatusTable(), ProjectTable())
	var pr RunnerStartProjection
	var containerImage sql.NullString
	err := db.QueryRowContext(ctx, query, scenarioID, ScenarioStateStartingRunners).Scan(
		&pr.ID, &pr.TranslationAttempt, &pr.NumberOfReps, &pr.NumberOfComputedReps,
		&containerImage, &pr.ProjectNamespace, &pr.ProjectName,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load runner-start projection for scenario %d: %w", scenarioID, err)
	}
	pr.ContainerImage = containerImage.String
	return &pr, nil
}

// ObservationProjection is the DB projection the observation worker loads for
// one InProcessing scenario. It carries the requested and computed repetition
// counts, the translation attempt, and the static project identity needed to
// read the live experiment and the deterministic Job from Kubernetes.
type ObservationProjection struct {
	ID                   int
	TranslationAttempt   int
	NumberOfReps         int
	NumberOfComputedReps int
	ProjectNamespace     string
	ProjectName          string
}

// LoadObservationProjection loads the observation projection for one scenario
// that is currently in InProcessing. A nil result with nil error means the row
// is absent or no longer InProcessing (stale): the observation is a successful
// no-op.
func LoadObservationProjection(ctx context.Context, db DB, scenarioID int) (*ObservationProjection, error) {
	if scenarioID <= 0 {
		return nil, errPositiveID
	}
	query := fmt.Sprintf(`
		SELECT s.id, s.translation_attempts, s.number_of_reps, s.number_of_computed_reps,
			p.project_namespace, p.project_name
		FROM %s s
		JOIN %s p ON p.id = s.project_id
		WHERE s.id = $1 AND s.state = $2`,
		ScenarioStatusTable(), ProjectTable())
	var pr ObservationProjection
	err := db.QueryRowContext(ctx, query, scenarioID, ScenarioStateInProcessing).Scan(
		&pr.ID, &pr.TranslationAttempt, &pr.NumberOfReps, &pr.NumberOfComputedReps,
		&pr.ProjectNamespace, &pr.ProjectName,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load observation projection for scenario %d: %w", scenarioID, err)
	}
	return &pr, nil
}

// ListStartingRunnersScenarios returns every scenario ID currently in
// StartingRunners with a positive id, ordered by ascending id. The runner-start
// scheduler uses the positive scenario-status id as its process-local
// de-duplication key.
func ListStartingRunnersScenarios(ctx context.Context, db DB) ([]int, error) {
	return listScenariosByState(ctx, db, ScenarioStateStartingRunners)
}

// ListInProcessingScenarios returns every scenario ID currently in
// InProcessing with a positive id, ordered by ascending id. The observation
// queue adds these keys to its de-duplicating work set each discovery tick.
func ListInProcessingScenarios(ctx context.Context, db DB) ([]int, error) {
	return listScenariosByState(ctx, db, ScenarioStateInProcessing)
}

func listScenariosByState(ctx context.Context, db DB, state string) ([]int, error) {
	query := fmt.Sprintf(`
		SELECT id FROM %s
		WHERE id > 0 AND state = $1
		ORDER BY id ASC`,
		ScenarioStatusTable())
	rows, err := db.QueryContext(ctx, query, state)
	if err != nil {
		return nil, fmt.Errorf("list %s scenarios: %w", state, err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan %s scenario id: %w", state, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s scenario ids: %w", state, err)
	}
	return ids, nil
}
