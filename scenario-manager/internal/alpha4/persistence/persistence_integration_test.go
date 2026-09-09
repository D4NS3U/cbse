//go:build integration

package persistence

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	itDSNEnv      = "SCENARIO_MANAGER_CORE_DB_DSN"
	itUserEnv     = "SCENARIO_MANAGER_CORE_DB_USER"
	itPasswordEnv = "SCENARIO_MANAGER_CORE_DB_PASSWORD"
)

func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	if os.Getenv(itDSNEnv) == "" || os.Getenv(itUserEnv) == "" || os.Getenv(itPasswordEnv) == "" {
		t.Skipf("Environment variables %s, %s, %s are not all set; skipping alpha4 persistence integration test",
			itDSNEnv, itUserEnv, itPasswordEnv)
	}
	dsn := os.Getenv(itDSNEnv)
	user := os.Getenv(itUserEnv)
	pass := os.Getenv(itPasswordEnv)
	// Inject credentials into the DSN via the user@host form expected by pgx.
	// The base DSN is a URL; merge credentials like the alpha3 connect helper.
	connStr, err := mergeCredentials(dsn, user, pass)
	if err != nil {
		t.Fatalf("merge credentials: %v", err)
	}
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatalf("ping db: %v", err)
	}

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "alpha4_it_" + hex.EncodeToString(b)
	if _, err := db.Exec(fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		_ = db.Close()
		t.Fatalf("create schema %s: %v", schema, err)
	}
	// Force the search_path to the isolated schema so the unqualified table
	// names resolve there, avoiding collision with any alpha3 tables.
	if _, err := db.Exec(fmt.Sprintf(`SET search_path TO %s`, schema)); err != nil {
		_ = db.Close()
		t.Fatalf("set search_path: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
		_ = db.Close()
	})
	return db, schema
}

func mergeCredentials(base, user, pass string) (string, error) {
	// Reuse the alpha3 approach: parse the base DSN URL and set the user info.
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	parsed.User = url.UserPassword(user, pass)
	return parsed.String(), nil
}

func TestEnsureSchemaCreatesValidatesAndPersists(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatalf("EnsureSchema (create): %v", err)
	}
	// Running again on the now-existing tables must succeed (idempotent
	// validation).
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatalf("EnsureSchema (validate): %v", err)
	}

	// Register a project idempotently.
	id1, err := RegisterProject(ctx, db, "default", "smoke")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if id1 <= 0 {
		t.Fatalf("id1 = %d", id1)
	}
	id2, err := RegisterProject(ctx, db, "default", "smoke")
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("idempotent register returned %d then %d", id1, id2)
	}
	// Isolation: a same-named project in a different namespace is distinct.
	id3, err := RegisterProject(ctx, db, "other-ns", "smoke")
	if err != nil {
		t.Fatalf("register other ns: %v", err)
	}
	if id3 == id1 {
		t.Fatal("same-named project in different namespaces must be distinct")
	}
	// Exact namespace/name lookup.
	got, err := ProjectIDByNamespaceAndName(ctx, db, "default", "smoke")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got != id1 {
		t.Fatalf("lookup = %d; want %d", got, id1)
	}
	// Missing lookup.
	if _, err := ProjectIDByNamespaceAndName(ctx, db, "default", "missing"); err != ErrProjectNotFound {
		t.Fatalf("missing lookup err = %v; want ErrProjectNotFound", err)
	}

	// Insert scenarios for the project to exercise the publication boundary
	// and the bulk terminal update.
	scenarioID, err := insertScenario(ctx, db, id1, ScenarioStateCreated, `{"k":"v"}`)
	if err != nil {
		t.Fatalf("insert scenario: %v", err)
	}

	// Claim: Created -> Scheduled, attempt 1, both publish timestamps null.
	claimed, err := ClaimScenarioForTranslation(ctx, NewStore(db), scenarioID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed == nil || claimed.TranslationAttempt != 1 || claimed.ProjectNamespace != "default" || claimed.ProjectName != "smoke" {
		t.Fatalf("claimed = %+v", claimed)
	}
	// Re-claim of a Scheduled row is not claimable.
	if again, err := ClaimScenarioForTranslation(ctx, NewStore(db), scenarioID); err != nil || again != nil {
		t.Fatalf("re-claim = %+v err=%v; want nil", again, err)
	}

	// Mark publish started: requires exact Scheduled attempt, both null.
	ok, err := MarkTranslationPublishStarted(ctx, db, scenarioID, 1)
	if err != nil || !ok {
		t.Fatalf("mark publish started: ok=%v err=%v", ok, err)
	}
	// A second start on the same attempt is a no-op (started already set).
	ok, err = MarkTranslationPublishStarted(ctx, db, scenarioID, 1)
	if err != nil || ok {
		t.Fatalf("second start: ok=%v err=%v; want false", ok, err)
	}
	// Mark confirmed: requires non-null started.
	ok, err = MarkScenarioTranslationRequestPublished(ctx, db, scenarioID, 1)
	if err != nil || !ok {
		t.Fatalf("mark confirmed: ok=%v err=%v", ok, err)
	}
	// A confirmed attempt is not recoverable.
	_, _, err = RecoverUnpublishedTranslationClaim(ctx, db, scenarioID, 1, time.Now().Add(time.Hour), 3)
	if err != nil {
		t.Fatalf("recover confirmed: %v", err)
	}

	// Terminal bulk update for a different project with non-terminal scenarios.
	projTerm, _ := RegisterProject(ctx, db, "default", "terminal")
	sCreated, _ := insertScenario(ctx, db, projTerm, ScenarioStateCreated, `null`)
	sSched, _ := insertScenario(ctx, db, projTerm, ScenarioStateScheduled, `null`)
	sFinished, _ := insertScenario(ctx, db, projTerm, ScenarioStateFinished, `null`)
	sFailed, _ := insertScenario(ctx, db, projTerm, ScenarioStateFailed, `null`)
	n, err := MarkScenariosFailedForProject(ctx, NewStore(db), projTerm)
	if err != nil {
		t.Fatalf("bulk failed: %v", err)
	}
	if n != 2 {
		t.Fatalf("bulk failed moved %d rows; want 2 (Created+Scheduled)", n)
	}
	// Existing Failed and Finished rows are unchanged.
	for _, id := range []int{sCreated, sSched} {
		if state := scenarioState(t, ctx, db, id); state != ScenarioStateFailed {
			t.Fatalf("scenario %d state = %q; want Failed", id, state)
		}
	}
	if state := scenarioState(t, ctx, db, sFinished); state != ScenarioStateFinished {
		t.Fatalf("Finished row changed to %q", state)
	}
	if state := scenarioState(t, ctx, db, sFailed); state != ScenarioStateFailed {
		t.Fatalf("Failed row changed to %q", state)
	}

	// Delete project cascades to its scenarios.
	if err := DeleteProjectByNamespaceAndName(ctx, db, "default", "smoke"); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	if _, err := ProjectIDByNamespaceAndName(ctx, db, "default", "smoke"); err != ErrProjectNotFound {
		t.Fatalf("after delete: err = %v; want ErrProjectNotFound", err)
	}
	// The scenario belonging to the deleted project must be gone (cascade).
	var count int
	if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE id = $1`, ScenarioStatusTable()), scenarioID).Scan(&count); err != nil {
		t.Fatalf("count orphaned scenario: %v", err)
	}
	if count != 0 {
		t.Fatalf("cascade left %d scenario rows", count)
	}
}

func TestCancelAndRecoverUnpublishedClaims(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	pid, _ := RegisterProject(ctx, db, "default", "cancel")
	sCancel, _ := insertScenario(ctx, db, pid, ScenarioStateCreated, `null`)
	sRecover, _ := insertScenario(ctx, db, pid, ScenarioStateCreated, `null`)

	// Claim then cancel before any publish-start: refunds the attempt back to
	// Created.
	if _, err := ClaimScenarioForTranslation(ctx, NewStore(db), sCancel); err != nil {
		t.Fatalf("claim: %v", err)
	}
	ok, err := CancelUnpublishedTranslationClaim(ctx, db, sCancel, 1)
	if err != nil || !ok {
		t.Fatalf("cancel: ok=%v err=%v", ok, err)
	}
	if state := scenarioState(t, ctx, db, sCancel); state != ScenarioStateCreated {
		t.Fatalf("after cancel state = %q; want Created", state)
	}
	// Attempt refunded to 0.
	if a := scenarioAttempts(t, ctx, db, sCancel); a != 0 {
		t.Fatalf("after cancel attempts = %d; want 0", a)
	}

	// Claim the recover scenario, mark publish-started, then recover: the
	// attempt is NOT refunded (publication ambiguous), and at the attempt limit
	// the row moves to Failed.
	if _, err := ClaimScenarioForTranslation(ctx, NewStore(db), sRecover); err != nil {
		t.Fatalf("claim recover: %v", err)
	}
	if _, err := MarkTranslationPublishStarted(ctx, db, sRecover, 1); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Use maxAttempts=1 so the recover moves the row to Failed without refund.
	ok, finalState, err := RecoverUnpublishedTranslationClaim(ctx, db, sRecover, 1, time.Now().Add(time.Hour), 1)
	if err != nil || !ok {
		t.Fatalf("recover: ok=%v err=%v", ok, err)
	}
	if finalState != ScenarioStateFailed {
		t.Fatalf("recover finalState = %q; want Failed", finalState)
	}
	if a := scenarioAttempts(t, ctx, db, sRecover); a != 1 {
		t.Fatalf("recover attempts = %d; want 1 (not refunded)", a)
	}
}

// insertScenario inserts one scenario row in the given state and returns its id.
func insertScenario(ctx context.Context, db DB, projectID int, state string, recipe string) (int, error) {
	var recipeArg interface{}
	if recipe == "null" || recipe == "" {
		recipeArg = nil
	} else {
		recipeArg = json.RawMessage(recipe)
	}
	query := fmt.Sprintf(`INSERT INTO %s (project_id, state, priority, number_of_reps, number_of_computed_reps, recipe_info) VALUES ($1, $2, 0, 1, 0, $3) RETURNING id`, ScenarioStatusTable())
	var id int
	err := db.QueryRowContext(ctx, query, projectID, state, recipeArg).Scan(&id)
	return id, err
}

func scenarioState(t *testing.T, ctx context.Context, db DB, id int) string {
	t.Helper()
	var state string
	if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT state FROM %s WHERE id = $1`, ScenarioStatusTable()), id).Scan(&state); err != nil {
		t.Fatalf("select state: %v", err)
	}
	return state
}

func scenarioAttempts(t *testing.T, ctx context.Context, db DB, id int) int {
	t.Helper()
	var a int
	if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT translation_attempts FROM %s WHERE id = $1`, ScenarioStatusTable()), id).Scan(&a); err != nil {
		t.Fatalf("select attempts: %v", err)
	}
	return a
}
