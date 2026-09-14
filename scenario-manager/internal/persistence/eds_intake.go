package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ScenarioIntakeRecord is the per-scenario subset the EDS batch handler maps
// from a transport-neutral communication.ScenarioRecord before insertion. It
// is the persistence-layer analogue of the alpha3 coredb.ScenarioStatusRecord
// intake shape, restricted to the columns the EDS supplies; the remaining
// scenario_status columns take their schema defaults (state=Created,
// number_of_computed_reps=0, translation_attempts=0, container_image=NULL,
// translation_*_at=NULL).
type ScenarioIntakeRecord struct {
	Priority         int
	NumberOfReps     int
	RecipeInfo       json.RawMessage
	ConfidenceMetric *float64
}

// InsertScenarioBatch registers the (namespace, project) pair idempotently and
// inserts every intake record as a Created scenario_status row in a single
// statement. It returns the number of rows inserted.
//
// The project is registered with RegisterProject so the namespace/name pair
// exists before the FK-bearing scenario rows are inserted. A nil recipe_info or
// confidence_metric is inserted as NULL. number_of_reps is stored verbatim; the
// communication-layer rep-range guard (MinReps..MaxReps) has already rejected
// out-of-range batches as permanent poison before this function is called.
func InsertScenarioBatch(ctx context.Context, db DB, namespace, project string, records []ScenarioIntakeRecord) (int, error) {
	namespace = strings.TrimSpace(namespace)
	project = strings.TrimSpace(project)
	if namespace == "" || project == "" {
		return 0, fmt.Errorf("eds batch namespace and project must not be empty")
	}
	if len(records) == 0 {
		return 0, nil
	}
	if db == nil {
		return 0, fmt.Errorf("db must not be nil")
	}

	projectID, err := RegisterProject(ctx, db, namespace, project)
	if err != nil {
		return 0, fmt.Errorf("register project %s/%s: %w", namespace, project, err)
	}

	table := ScenarioStatusTable()
	// A single multi-row INSERT keeps the batch atomic. The schema defaults
	// supply state, number_of_computed_reps, translation_attempts, the two
	// publication timestamps, and container_image.
	values := make([]string, 0, len(records))
	args := make([]interface{}, 0, len(records)*6)
	placeholder := 1
	for _, r := range records {
		values = append(values, fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d)", placeholder, placeholder+1, placeholder+2, placeholder+3, placeholder+4, placeholder+5))
		args = append(args, projectID, ScenarioStateCreated, r.Priority, r.NumberOfReps, r.RecipeInfo, r.ConfidenceMetric)
		placeholder += 6
	}
	query := fmt.Sprintf(`
		INSERT INTO %s (project_id, state, priority, number_of_reps, recipe_info, confidence_metric)
		VALUES %s`,
		table, strings.Join(values, ", "))

	res, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("insert scenario batch for project %s/%s: %w", namespace, project, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected for scenario batch insert: %w", err)
	}
	return int(affected), nil
}

// MarkScenarioStartingRunners applies the guarded Scheduled -> StartingRunners
// transition and records the Translator-produced runner image digest. It is
// the persistence backing for the Translator-ready handler: only a row that is
// currently Scheduled AND has a non-null translation_request_published_at (the
// request was durably accepted by the transport) may advance. A false result
// means the row was no longer an eligible Scheduled attempt (already advanced,
// cancelled back to Created, failed, or stale): the caller treats the ready
// message as handled without a transition.
//
// containerImage must be non-empty; the communication-layer adapter validates
// the digest shape before this call. The transition sets updated_at = NOW()
// and leaves the two publication timestamps intact for audit.
func MarkScenarioStartingRunners(ctx context.Context, db DB, scenarioID int, containerImage string) (bool, error) {
	if scenarioID <= 0 {
		return false, errPositiveID
	}
	if strings.TrimSpace(containerImage) == "" {
		return false, fmt.Errorf("container image must not be empty")
	}
	if db == nil {
		return false, fmt.Errorf("db must not be nil")
	}

	table := ScenarioStatusTable()
	query := fmt.Sprintf(`
		UPDATE %s
		SET state = $2,
			container_image = $3,
			updated_at = NOW()
		WHERE id = $1
			AND state = $4
			AND translation_request_published_at IS NOT NULL`,
		table)
	res, err := db.ExecContext(ctx, query, scenarioID, ScenarioStateStartingRunners, containerImage, ScenarioStateScheduled)
	if err != nil {
		return false, fmt.Errorf("mark scenario %d starting runners: %w", scenarioID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected for starting runners transition: %w", err)
	}
	return affected > 0, nil
}
