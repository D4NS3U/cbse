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
	// The base DSN is a URL; merge credentials by parsing it and setting the
	// user info.
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
	// names resolve there, avoiding collision with other tables.
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
	// Parse the base DSN URL and set the user info.
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

func scenarioContainerImage(t *testing.T, ctx context.Context, db DB, id int) string {
	t.Helper()
	var image sql.NullString
	if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT container_image FROM %s WHERE id = $1`, ScenarioStatusTable()), id).Scan(&image); err != nil {
		t.Fatalf("select container_image: %v", err)
	}
	return image.String
}

func TestInsertScenarioBatch(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	records := []ScenarioIntakeRecord{
		{Priority: 5, NumberOfReps: 2, RecipeInfo: json.RawMessage(`{"k":"v"}`)},
		{Priority: 1, NumberOfReps: 4}, // nil recipe_info and nil confidence_metric
	}
	inserted, err := InsertScenarioBatch(ctx, db, "default", "batch", records)
	if err != nil || inserted != 2 {
		t.Fatalf("InsertScenarioBatch: inserted=%d err=%v", inserted, err)
	}

	pid, err := ProjectIDByNamespaceAndName(ctx, db, "default", "batch")
	if err != nil {
		t.Fatalf("lookup project: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("project id = %d; want positive", pid)
	}

	var count int
	if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE project_id = $1`, ScenarioStatusTable()), pid).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("row count = %d; want 2", count)
	}

	var state string
	if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT state FROM %s WHERE project_id = $1 ORDER BY id LIMIT 1`, ScenarioStatusTable()), pid).Scan(&state); err != nil {
		t.Fatalf("select state: %v", err)
	}
	if state != ScenarioStateCreated {
		t.Fatalf("state = %q; want Created", state)
	}

	// Idempotent project registration: a second batch reuses the same project id.
	inserted2, err := InsertScenarioBatch(ctx, db, "default", "batch", []ScenarioIntakeRecord{{Priority: 0, NumberOfReps: 1}})
	if err != nil || inserted2 != 1 {
		t.Fatalf("second InsertScenarioBatch: inserted=%d err=%v", inserted2, err)
	}
	pid2, _ := ProjectIDByNamespaceAndName(ctx, db, "default", "batch")
	if pid2 != pid {
		t.Fatalf("project id changed: %d -> %d", pid, pid2)
	}
}

func TestMarkScenarioStartingRunners(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	pid, _ := RegisterProject(ctx, db, "default", "ready")
	sID, _ := insertScenario(ctx, db, pid, ScenarioStateCreated, `null`)

	// Drive the scenario through Created -> Scheduled -> published, the only
	// eligible path into StartingRunners.
	if _, err := ClaimScenarioForTranslation(ctx, NewStore(db), sID); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := MarkTranslationPublishStarted(ctx, db, sID, 1); err != nil {
		t.Fatalf("publish started: %v", err)
	}
	if _, err := MarkScenarioTranslationRequestPublished(ctx, db, sID, 1); err != nil {
		t.Fatalf("published: %v", err)
	}

	ok, err := MarkScenarioStartingRunners(ctx, db, sID, "registry.example.com/runner@sha256:abc")
	if err != nil || !ok {
		t.Fatalf("MarkScenarioStartingRunners: ok=%v err=%v", ok, err)
	}
	if state := scenarioState(t, ctx, db, sID); state != ScenarioStateStartingRunners {
		t.Fatalf("state = %q; want StartingRunners", state)
	}
	if got := scenarioContainerImage(t, ctx, db, sID); got != "registry.example.com/runner@sha256:abc" {
		t.Fatalf("container_image = %q; want the digest", got)
	}

	// A second ready for the same scenario is a stale no-op: the row is no
	// longer Scheduled, so the transition is a handled false.
	ok2, err := MarkScenarioStartingRunners(ctx, db, sID, "registry.example.com/runner@sha256:def")
	if err != nil || ok2 {
		t.Fatalf("second MarkScenarioStartingRunners: ok=%v err=%v; want false nil", ok2, err)
	}
	if got := scenarioContainerImage(t, ctx, db, sID); got != "registry.example.com/runner@sha256:abc" {
		t.Fatalf("container_image changed on stale ready: %q", got)
	}

	// A Scheduled-but-unpublished scenario is NOT eligible: the ready handler
	// must not advance a scenario whose request was not durably accepted.
	sUnpub, _ := insertScenario(ctx, db, pid, ScenarioStateCreated, `null`)
	if _, err := ClaimScenarioForTranslation(ctx, NewStore(db), sUnpub); err != nil {
		t.Fatalf("claim unpub: %v", err)
	}
	// Intentionally do NOT call MarkScenarioTranslationRequestPublished.
	ok3, err := MarkScenarioStartingRunners(ctx, db, sUnpub, "registry.example.com/runner@sha256:xyz")
	if err != nil || ok3 {
		t.Fatalf("unpublished MarkScenarioStartingRunners: ok=%v err=%v; want false nil", ok3, err)
	}
	if state := scenarioState(t, ctx, db, sUnpub); state != ScenarioStateScheduled {
		t.Fatalf("unpublished state = %q; want Scheduled", state)
	}
}

func TestNextCreatedScenarioForTranslationDiscovery(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	pid, _ := RegisterProject(ctx, db, "default", "discovery")
	// Two Created scenarios; discovery returns the lowest id.
	sLow, _ := insertScenario(ctx, db, pid, ScenarioStateCreated, `null`)
	sHigh, _ := insertScenario(ctx, db, pid, ScenarioStateCreated, `null`)
	if sLow >= sHigh {
		t.Fatalf("expected sLow < sHigh; got %d, %d", sLow, sHigh)
	}

	c, err := NextCreatedScenarioForTranslation(ctx, db)
	if err != nil || c == nil {
		t.Fatalf("NextCreatedScenarioForTranslation: c=%+v err=%v", c, err)
	}
	if c.ID != sLow {
		t.Fatalf("discovered id = %d; want lowest %d", c.ID, sLow)
	}
	if c.ProjectNamespace != "default" || c.ProjectName != "discovery" {
		t.Fatalf("discovered identity = %s/%s; want default/discovery", c.ProjectNamespace, c.ProjectName)
	}
	if c.TranslationAttempt != 0 {
		t.Fatalf("fresh Created attempt = %d; want 0", c.TranslationAttempt)
	}

	// A non-Created row is never discovered.
	if _, err := ClaimScenarioForTranslation(ctx, NewStore(db), sLow); err != nil {
		t.Fatalf("claim sLow: %v", err)
	}
	c2, err := NextCreatedScenarioForTranslation(ctx, db)
	if err != nil || c2 == nil {
		t.Fatalf("second discovery: c=%+v err=%v", c2, err)
	}
	if c2.ID != sHigh {
		t.Fatalf("second discovered id = %d; want %d (Scheduled excluded)", c2.ID, sHigh)
	}

	// When no Created scenario remains, discovery returns nil.
	if _, err := ClaimScenarioForTranslation(ctx, NewStore(db), sHigh); err != nil {
		t.Fatalf("claim sHigh: %v", err)
	}
	if c3, err := NextCreatedScenarioForTranslation(ctx, db); err != nil || c3 != nil {
		t.Fatalf("no Created left: c=%+v err=%v; want nil", c3, err)
	}
}

func TestNextStaleUnpublishedTranslationClaimDiscovery(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	pid, _ := RegisterProject(ctx, db, "default", "stale")
	sStale, _ := insertScenario(ctx, db, pid, ScenarioStateCreated, `null`)
	if _, err := ClaimScenarioForTranslation(ctx, NewStore(db), sStale); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Backdate updated_at so the row is older than the cutoff.
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET updated_at = NOW() - INTERVAL '10 minutes' WHERE id = $1`, ScenarioStatusTable()), sStale); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	cutoff := time.Now()
	claim, err := NextStaleUnpublishedTranslationClaim(ctx, db, cutoff)
	if err != nil || claim == nil {
		t.Fatalf("stale discovery: c=%+v err=%v", claim, err)
	}
	if claim.ID != sStale || claim.TranslationAttempt != 1 {
		t.Fatalf("stale claim = %+v; want id=%d attempt=1", claim, sStale)
	}

	// A fresh Scheduled row (updated_at after the cutoff) is not discovered.
	sFresh, _ := insertScenario(ctx, db, pid, ScenarioStateCreated, `null`)
	if _, err := ClaimScenarioForTranslation(ctx, NewStore(db), sFresh); err != nil {
		t.Fatalf("claim fresh: %v", err)
	}
	if c2, err := NextStaleUnpublishedTranslationClaim(ctx, db, cutoff); err != nil || c2 == nil || c2.ID != sStale {
		t.Fatalf("fresh excluded: c=%+v err=%v; want sStale", c2, err)
	}

	// A confirmed request (translation_request_published_at set) is never discovered.
	if _, err := MarkTranslationPublishStarted(ctx, db, sStale, 1); err != nil {
		t.Fatalf("start sStale: %v", err)
	}
	if _, err := MarkScenarioTranslationRequestPublished(ctx, db, sStale, 1); err != nil {
		t.Fatalf("published sStale: %v", err)
	}
	if c3, err := NextStaleUnpublishedTranslationClaim(ctx, db, cutoff); err != nil || c3 != nil {
		t.Fatalf("confirmed excluded: c=%+v err=%v; want nil", c3, err)
	}
}

func TestMarkScenarioTranslationAttemptFailedConsumesPublishedAttempt(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	pid, _ := RegisterProject(ctx, db, "default", "attemptfail")
	sID, _ := insertScenario(ctx, db, pid, ScenarioStateCreated, `null`)
	// Drive the scenario through Created -> Scheduled -> published, the normal
	// state when a Translator ready arrives. Both publication timestamps are
	// SET, so MarkScenarioTranslationPublishFailed (which requires both NULL)
	// would NOT match; the attempt-failed transition must match this row.
	if _, err := ClaimScenarioForTranslation(ctx, NewStore(db), sID); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := MarkTranslationPublishStarted(ctx, db, sID, 1); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := MarkScenarioTranslationRequestPublished(ctx, db, sID, 1); err != nil {
		t.Fatalf("published: %v", err)
	}

	// Empty-image ready below the limit: Scheduled -> Created, attempt consumed
	// (not refunded; translation_attempts stays 1).
	changed, finalState, err := MarkScenarioTranslationAttemptFailed(ctx, db, sID, 1, 3)
	if err != nil || !changed {
		t.Fatalf("attempt-failed below limit: changed=%v err=%v", changed, err)
	}
	if finalState != ScenarioStateCreated {
		t.Fatalf("finalState = %q; want Created", finalState)
	}
	if state := scenarioState(t, ctx, db, sID); state != ScenarioStateCreated {
		t.Fatalf("state = %q; want Created", state)
	}
	if a := scenarioAttempts(t, ctx, db, sID); a != 1 {
		t.Fatalf("attempts = %d; want 1 (not refunded)", a)
	}

	// Re-claim and re-publish to reach the limit, then consume at the limit:
	// Scheduled -> Failed, attempt still not refunded.
	if _, err := ClaimScenarioForTranslation(ctx, NewStore(db), sID); err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if a := scenarioAttempts(t, ctx, db, sID); a != 2 {
		t.Fatalf("attempts after reclaim = %d; want 2", a)
	}
	if _, err := MarkTranslationPublishStarted(ctx, db, sID, 2); err != nil {
		t.Fatalf("start 2: %v", err)
	}
	if _, err := MarkScenarioTranslationRequestPublished(ctx, db, sID, 2); err != nil {
		t.Fatalf("published 2: %v", err)
	}
	changed, finalState, err = MarkScenarioTranslationAttemptFailed(ctx, db, sID, 2, 2)
	if err != nil || !changed {
		t.Fatalf("attempt-failed at limit: changed=%v err=%v", changed, err)
	}
	if finalState != ScenarioStateFailed {
		t.Fatalf("finalState = %q; want Failed", finalState)
	}
	if state := scenarioState(t, ctx, db, sID); state != ScenarioStateFailed {
		t.Fatalf("state = %q; want Failed", state)
	}
	if a := scenarioAttempts(t, ctx, db, sID); a != 2 {
		t.Fatalf("attempts at limit = %d; want 2 (not refunded)", a)
	}

	// A stale attempt (wrong attempt number) is a no-op.
	stale, _ := insertScenario(ctx, db, pid, ScenarioStateCreated, `null`)
	if _, err := ClaimScenarioForTranslation(ctx, NewStore(db), stale); err != nil {
		t.Fatalf("claim stale: %v", err)
	}
	changed, _, err = MarkScenarioTranslationAttemptFailed(ctx, db, stale, 99, 3)
	if err != nil || changed {
		t.Fatalf("stale attempt: changed=%v err=%v; want false nil", changed, err)
	}
	if state := scenarioState(t, ctx, db, stale); state != ScenarioStateScheduled {
		t.Fatalf("stale state = %q; want unchanged Scheduled", state)
	}
}
