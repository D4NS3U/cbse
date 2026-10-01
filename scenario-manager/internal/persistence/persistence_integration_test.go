// Copyright 2025-2026 Daniel Seufferth
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build integration

package persistence

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
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
// round_reps mirrors number_of_reps (the round-1 value) because the column is
// NOT NULL without a default.
func insertScenario(ctx context.Context, db DB, projectID int, state string, recipe string) (int, error) {
	var recipeArg interface{}
	if recipe == "null" || recipe == "" {
		recipeArg = nil
	} else {
		recipeArg = json.RawMessage(recipe)
	}
	query := fmt.Sprintf(`INSERT INTO %s (project_id, state, priority, number_of_reps, round_reps, number_of_computed_reps, recipe_info) VALUES ($1, $2, 0, 1, 1, 0, $3) RETURNING id`, ScenarioStatusTable())
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

// legacyScenarioStatusDDL is the pre-round alpha4 scenario_status shape (no
// round bookkeeping or evaluation-publication-guard columns), used to prove
// the schema validation rejects tables missing the additive columns.
const legacyScenarioStatusDDL = `CREATE TABLE %s (
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
	)`

// TestEnsureSchemaRejectsMissingRoundColumns proves the schema policy: a table
// missing the additive round/evaluation columns (or mis-typing one) is
// incompatible; EnsureSchema never issues ALTER repair, it fails startup with
// ErrSchemaIncompatible. Per-experiment Core DBs are created fresh, so a
// legacy table is a hard incompatibility, not a migration target.
func TestEnsureSchemaRejectsMissingRoundColumns(t *testing.T) {
	db, schema := openTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE project (
		id SERIAL PRIMARY KEY,
		project_namespace TEXT NOT NULL,
		project_name TEXT NOT NULL,
		CONSTRAINT %s_namespace_name_key UNIQUE (project_namespace, project_name)
	)`, schema)); err != nil {
		t.Fatalf("create project: %v", err)
	}
	// Legacy scenario_status: no runner_round/round_reps/round_computed_reps,
	// no evaluation_attempts/evaluation_*_at columns.
	if _, err := db.Exec(fmt.Sprintf(legacyScenarioStatusDDL, ScenarioStatusTable(), "project")); err != nil {
		t.Fatalf("create legacy scenario_status: %v", err)
	}
	err := EnsureSchema(ctx, db)
	if err == nil {
		t.Fatal("EnsureSchema: want ErrSchemaIncompatible for a table missing the round/evaluation columns, got nil")
	}
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Fatalf("EnsureSchema err = %v; want ErrSchemaIncompatible", err)
	}

	// Drop the legacy table; re-create with the new columns present but
	// round_reps mis-typed (TEXT instead of INTEGER): still incompatible.
	if _, err := db.Exec(fmt.Sprintf(`DROP TABLE %s`, ScenarioStatusTable())); err != nil {
		t.Fatalf("drop legacy table: %v", err)
	}
	mismatched := strings.Replace(legacyScenarioStatusDDL,
		"confidence_metric DOUBLE PRECISION",
		"confidence_metric DOUBLE PRECISION,\n\t\trunner_round INTEGER NOT NULL DEFAULT 1,\n\t\tround_reps TEXT NOT NULL DEFAULT '1',\n\t\tround_computed_reps INTEGER NOT NULL DEFAULT 0,\n\t\tevaluation_attempts INTEGER NOT NULL DEFAULT 0,\n\t\tevaluation_publish_started_at TIMESTAMPTZ,\n\t\tevaluation_request_published_at TIMESTAMPTZ", 1)
	if _, err := db.Exec(fmt.Sprintf(mismatched, ScenarioStatusTable(), "project")); err != nil {
		t.Fatalf("create mis-typed scenario_status: %v", err)
	}
	err = EnsureSchema(ctx, db)
	if err == nil {
		t.Fatal("EnsureSchema: want ErrSchemaIncompatible for mis-typed round_reps, got nil")
	}
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Fatalf("EnsureSchema err = %v; want ErrSchemaIncompatible", err)
	}
}

// scenarioRoundColumns reads the round bookkeeping and evaluation-guard
// columns for assertions.
func scenarioRoundColumns(t *testing.T, ctx context.Context, db DB, id int) (runnerRound, roundReps, roundComputed, total, evalAttempts int, evalStarted, evalPublished bool) {
	t.Helper()
	err := db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT runner_round, round_reps, round_computed_reps, number_of_computed_reps,
			evaluation_attempts,
			evaluation_publish_started_at IS NOT NULL, evaluation_request_published_at IS NOT NULL
		FROM %s WHERE id = $1`, ScenarioStatusTable()), id).Scan(
		&runnerRound, &roundReps, &roundComputed, &total, &evalAttempts, &evalStarted, &evalPublished,
	)
	if err != nil {
		t.Fatalf("select round columns: %v", err)
	}
	return
}

// insertScenarioReps inserts one scenario row with a specific repetition count
// (number_of_reps and round_reps both set to reps, the round-1 invariant).
func insertScenarioReps(ctx context.Context, db DB, projectID int, state string, reps int) (int, error) {
	query := fmt.Sprintf(`INSERT INTO %s (project_id, state, priority, number_of_reps, round_reps, number_of_computed_reps) VALUES ($1, $2, 0, $3, $3, 0) RETURNING id`, ScenarioStatusTable())
	var id int
	err := db.QueryRowContext(ctx, query, projectID, state, reps).Scan(&id)
	return id, err
}

// TestIntakeSuppliesRoundOneValues proves the intake INSERT supplies the
// round-1 values: round_reps = number_of_reps with the schema defaults
// runner_round=1, round_computed_reps=0, evaluation_attempts=0 and null
// evaluation timestamps, keeping the single-statement batch insert.
func TestIntakeSuppliesRoundOneValues(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	pid, _ := RegisterProject(ctx, db, "default", "rounds")
	inserted, err := InsertScenarioBatch(ctx, db, "default", "rounds", []ScenarioIntakeRecord{
		{Priority: 0, NumberOfReps: 2},
		{Priority: 1, NumberOfReps: 4},
	})
	if err != nil || inserted != 2 {
		t.Fatalf("InsertScenarioBatch: inserted=%d err=%v", inserted, err)
	}
	var ids []int
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE project_id = $1 ORDER BY id`, ScenarioStatusTable()), pid)
	if err != nil {
		t.Fatalf("query ids: %v", err)
	}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan id: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	wantReps := []int{2, 4}
	for i, id := range ids {
		runnerRound, roundReps, roundComputed, total, evalAttempts, evalStarted, evalPublished := scenarioRoundColumns(t, ctx, db, id)
		if roundReps != wantReps[i] {
			t.Fatalf("scenario %d: round_reps = %d; want %d (number_of_reps)", id, roundReps, wantReps[i])
		}
		if runnerRound != 1 || roundComputed != 0 || total != 0 || evalAttempts != 0 || evalStarted || evalPublished {
			t.Fatalf("scenario %d: defaults round=%d roundComputed=%d total=%d evalAttempts=%d started=%v published=%v; want 1/0/0/0/false/false", id, runnerRound, roundComputed, total, evalAttempts, evalStarted, evalPublished)
		}
	}
}

// TestComputedRepsSingleRoundEqualityPin proves the generalized computed-reps
// update keeps the single-round equality pin: after round 1 completes,
// number_of_computed_reps == number_of_reps (the S07-A3 harness pin). It also
// covers per-round clamping, monotonicity, the stale-round guard, and the
// stale-state guard.
func TestComputedRepsSingleRoundEqualityPin(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	pid, _ := RegisterProject(ctx, db, "default", "pin")
	sID, _ := insertScenarioReps(ctx, db, pid, ScenarioStateInProcessing, 3)

	// Partial updates accumulate monotonically within round 1.
	total, ok, err := UpdateScenarioComputedRepsForRound(ctx, db, sID, 1, 1)
	if err != nil || !ok || total != 1 {
		t.Fatalf("partial 1: total=%d ok=%v err=%v; want 1/true", total, ok, err)
	}
	total, ok, err = UpdateScenarioComputedRepsForRound(ctx, db, sID, 1, 2)
	if err != nil || !ok || total != 2 {
		t.Fatalf("partial 2: total=%d ok=%v err=%v; want 2/true", total, ok, err)
	}
	// A lower redelivered count is a monotonic no-op on the total.
	total, ok, err = UpdateScenarioComputedRepsForRound(ctx, db, sID, 1, 2)
	if err != nil || !ok || total != 2 {
		t.Fatalf("redelivered 2: total=%d ok=%v err=%v; want 2/true (monotone)", total, ok, err)
	}
	// Round completion: the equality pin — total equals number_of_reps.
	total, ok, err = UpdateScenarioComputedRepsForRound(ctx, db, sID, 1, 3)
	if err != nil || !ok || total != 3 {
		t.Fatalf("round-1 completion: total=%d ok=%v err=%v; want 3/true (number_of_reps)", total, ok, err)
	}
	var reps int
	if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT number_of_reps FROM %s WHERE id = $1`, ScenarioStatusTable()), sID).Scan(&reps); err != nil {
		t.Fatalf("select number_of_reps: %v", err)
	}
	if total != reps {
		t.Fatalf("single-round equality pin: number_of_computed_reps=%d; want number_of_reps=%d", total, reps)
	}
	runnerRound, roundReps, roundComputed, _, _, _, _ := scenarioRoundColumns(t, ctx, db, sID)
	if runnerRound != 1 || roundReps != 3 || roundComputed != 3 {
		t.Fatalf("round columns: round=%d roundReps=%d roundComputed=%d; want 1/3/3", runnerRound, roundReps, roundComputed)
	}

	// Per-round clamp: a count beyond round_reps never overstates the round.
	total, ok, err = UpdateScenarioComputedRepsForRound(ctx, db, sID, 1, 10)
	if err != nil || !ok || total != 3 {
		t.Fatalf("clamped count: total=%d ok=%v err=%v; want 3/true (clamped to round_reps)", total, ok, err)
	}

	// Stale round: the row is in round 1; a round-2 update matches no row.
	total, ok, err = UpdateScenarioComputedRepsForRound(ctx, db, sID, 2, 1)
	if err != nil || ok || total != 0 {
		t.Fatalf("stale round: total=%d ok=%v err=%v; want 0/false/nil", total, ok, err)
	}

	// Stale state: PostProcessing rows are not updated by the InProcessing
	// guard.
	if ok, err := MarkScenarioPostProcessing(ctx, db, sID); err != nil || !ok {
		t.Fatalf("MarkScenarioPostProcessing: ok=%v err=%v", ok, err)
	}
	total, ok, err = UpdateScenarioComputedRepsForRound(ctx, db, sID, 1, 3)
	if err != nil || ok || total != 0 {
		t.Fatalf("stale state: total=%d ok=%v err=%v; want 0/false/nil", total, ok, err)
	}
}

// TestRoundClaimAndEvaluationGuards proves the full round loop at the
// persistence layer: the guarded PostProcessing -> StartingRunners round-claim
// (runner_round increment, round_reps set to the additional runners,
// round_computed_reps reset), cross-round total accumulation, the evaluation
// publication guard trio (claim, publish-started, request-published with exact
// attempt guards and stale-attempt no-ops), and the guarded
// PostProcessing -> Finished success terminal with its rejections.
func TestRoundClaimAndEvaluationGuards(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	store := NewStore(db)
	pid, _ := RegisterProject(ctx, db, "default", "loop")

	// Round 1: 2 reps.
	sID, _ := insertScenarioReps(ctx, db, pid, ScenarioStateInProcessing, 2)
	if total, ok, err := UpdateScenarioComputedRepsForRound(ctx, db, sID, 1, 2); err != nil || !ok || total != 2 {
		t.Fatalf("round-1 completion: total=%d ok=%v err=%v", total, ok, err)
	}
	if ok, err := MarkScenarioPostProcessing(ctx, db, sID); err != nil || !ok {
		t.Fatalf("round-1 PostProcessing: ok=%v err=%v", ok, err)
	}

	// Evaluation claim for round 1: attempt 1, clean publication guards.
	attempt, ok, err := ClaimScenarioForEvaluation(ctx, store, sID)
	if err != nil || !ok || attempt != 1 {
		t.Fatalf("claim evaluation: attempt=%d ok=%v err=%v; want 1/true", attempt, ok, err)
	}
	if ok, err := MarkEvaluationPublishStarted(ctx, db, sID, 1); err != nil || !ok {
		t.Fatalf("publish started: ok=%v err=%v", ok, err)
	}
	// A second publish-start on the same attempt is a no-op.
	if ok, err := MarkEvaluationPublishStarted(ctx, db, sID, 1); err != nil || ok {
		t.Fatalf("second publish started: ok=%v err=%v; want false", ok, err)
	}
	// A stale attempt (never claimed) cannot publish.
	if ok, err := MarkEvaluationRequestPublished(ctx, db, sID, 99); err != nil || ok {
		t.Fatalf("stale request published: ok=%v err=%v; want false", ok, err)
	}
	if ok, err := MarkEvaluationRequestPublished(ctx, db, sID, 1); err != nil || !ok {
		t.Fatalf("request published: ok=%v err=%v", ok, err)
	}

	// Verdict not-met: claim round 2 with 3 additional runners.
	nextRound, ok, err := ClaimScenarioForEvaluationRound(ctx, db, sID, 3)
	if err != nil || !ok || nextRound != 2 {
		t.Fatalf("round claim: nextRound=%d ok=%v err=%v; want 2/true", nextRound, ok, err)
	}
	if state := scenarioState(t, ctx, db, sID); state != ScenarioStateStartingRunners {
		t.Fatalf("after round claim state = %q; want StartingRunners", state)
	}
	runnerRound, roundReps, roundComputed, total, evalAttempts, evalStarted, evalPublished := scenarioRoundColumns(t, ctx, db, sID)
	// The round-claim transition preserves the round-1 publication markers
	// for audit (the same convention as MarkScenarioStartingRunners keeping
	// the translation timestamps); the round-2 claim clears them.
	if runnerRound != 2 || roundReps != 3 || roundComputed != 0 || total != 2 || evalAttempts != 1 || !evalStarted || !evalPublished {
		t.Fatalf("after round claim: round=%d roundReps=%d roundComputed=%d total=%d evalAttempts=%d started=%v published=%v; want 2/3/0/2/1/true/true", runnerRound, roundReps, roundComputed, total, evalAttempts, evalStarted, evalPublished)
	}
	// The round-claim edge is one-shot: the row is no longer PostProcessing.
	if nextRound, ok, err := ClaimScenarioForEvaluationRound(ctx, db, sID, 3); err != nil || ok || nextRound != 0 {
		t.Fatalf("second round claim: nextRound=%d ok=%v err=%v; want 0/false/nil", nextRound, ok, err)
	}

	// Round 2 flows through StartingRunners -> InProcessing.
	if ok, err := MarkScenarioInProcessing(ctx, db, sID); err != nil || !ok {
		t.Fatalf("round-2 InProcessing: ok=%v err=%v", ok, err)
	}
	// Cross-round accumulation: 1 of 3 new reps adds exactly 1 to the total.
	total, ok, err = UpdateScenarioComputedRepsForRound(ctx, db, sID, 2, 1)
	if err != nil || !ok || total != 3 {
		t.Fatalf("round-2 partial: total=%d ok=%v err=%v; want 3/true (2+1)", total, ok, err)
	}
	// Completion of round 2: the total is the sum across all rounds.
	total, ok, err = UpdateScenarioComputedRepsForRound(ctx, db, sID, 2, 3)
	if err != nil || !ok || total != 5 {
		t.Fatalf("round-2 completion: total=%d ok=%v err=%v; want 5/true (2+3)", total, ok, err)
	}
	// Clamped to round_reps: an over-large count does not move the total.
	total, ok, err = UpdateScenarioComputedRepsForRound(ctx, db, sID, 2, 99)
	if err != nil || !ok || total != 5 {
		t.Fatalf("round-2 clamped: total=%d ok=%v err=%v; want 5/true", total, ok, err)
	}
	if ok, err := MarkScenarioPostProcessing(ctx, db, sID); err != nil || !ok {
		t.Fatalf("round-2 PostProcessing: ok=%v err=%v", ok, err)
	}

	// Round-2 evaluation claim: attempt 2 with clean publication guards; the
	// stale attempt-1 markers cannot mark publication for the newer claim.
	attempt, ok, err = ClaimScenarioForEvaluation(ctx, store, sID)
	if err != nil || !ok || attempt != 2 {
		t.Fatalf("round-2 claim: attempt=%d ok=%v err=%v; want 2/true", attempt, ok, err)
	}
	_, _, _, _, _, evalStarted, evalPublished = scenarioRoundColumns(t, ctx, db, sID)
	if evalStarted || evalPublished {
		t.Fatalf("after round-2 claim: started=%v published=%v; want false/false (fresh claim clears the guards)", evalStarted, evalPublished)
	}
	if ok, err := MarkEvaluationPublishStarted(ctx, db, sID, 1); err != nil || ok {
		t.Fatalf("stale publish started: ok=%v err=%v; want false (superseded attempt)", ok, err)
	}
	if ok, err := MarkEvaluationRequestPublished(ctx, db, sID, 1); err != nil || ok {
		t.Fatalf("stale request published: ok=%v err=%v; want false (superseded attempt)", ok, err)
	}
	if ok, err := MarkEvaluationPublishStarted(ctx, db, sID, 2); err != nil || !ok {
		t.Fatalf("round-2 publish started: ok=%v err=%v", ok, err)
	}
	if ok, err := MarkEvaluationRequestPublished(ctx, db, sID, 2); err != nil || !ok {
		t.Fatalf("round-2 request published: ok=%v err=%v", ok, err)
	}

	// Verdict met: guarded PostProcessing -> Finished.
	if ok, err := MarkScenarioFinished(ctx, db, sID); err != nil || !ok {
		t.Fatalf("MarkScenarioFinished: ok=%v err=%v", ok, err)
	}
	if state := scenarioState(t, ctx, db, sID); state != ScenarioStateFinished {
		t.Fatalf("state = %q; want Finished", state)
	}
	// Finished is terminal: the transition is one-shot, the row is not
	// claimable for evaluation, and it is never failable.
	if ok, err := MarkScenarioFinished(ctx, db, sID); err != nil || ok {
		t.Fatalf("second MarkScenarioFinished: ok=%v err=%v; want false/nil", ok, err)
	}
	if attempt, ok, err := ClaimScenarioForEvaluation(ctx, store, sID); err != nil || ok || attempt != 0 {
		t.Fatalf("claim Finished row: attempt=%d ok=%v err=%v; want 0/false/nil", attempt, ok, err)
	}
	if nextRound, ok, err := ClaimScenarioForEvaluationRound(ctx, db, sID, 2); err != nil || ok || nextRound != 0 {
		t.Fatalf("round claim Finished row: nextRound=%d ok=%v err=%v; want 0/false/nil", nextRound, ok, err)
	}
	if _, err := MarkScenarioFailedFrom(ctx, db, sID, ScenarioStateFinished); err == nil {
		t.Fatal("MarkScenarioFailedFrom(Finished): want the allow-list error, got nil")
	}
	if _, _, _, total, _, _, _ := scenarioRoundColumns(t, ctx, db, sID); total != 5 {
		t.Fatalf("finished row total = %d; want 5 (results preserved)", total)
	}

	// Wrong-from-state rejections: a non-PostProcessing row cannot finish.
	sInProc, _ := insertScenarioReps(ctx, db, pid, ScenarioStateInProcessing, 1)
	if ok, err := MarkScenarioFinished(ctx, db, sInProc); err != nil || ok {
		t.Fatalf("MarkScenarioFinished from InProcessing: ok=%v err=%v; want false/nil", ok, err)
	}
	sCreated, _ := insertScenario(ctx, db, pid, ScenarioStateCreated, `null`)
	if attempt, ok, err := ClaimScenarioForEvaluation(ctx, store, sCreated); err != nil || ok || attempt != 0 {
		t.Fatalf("claim Created row: attempt=%d ok=%v err=%v; want 0/false/nil", attempt, ok, err)
	}
	if attempt, ok, err := ClaimScenarioForEvaluation(ctx, store, 999999); err != nil || ok || attempt != 0 {
		t.Fatalf("claim missing row: attempt=%d ok=%v err=%v; want 0/false/nil", attempt, ok, err)
	}
}

func TestScenarioStateCountsByProject(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if _, err := RegisterProject(ctx, db, "aggns", "aggproj"); err != nil {
		t.Fatalf("register project: %v", err)
	}
	pid, err := ProjectIDByNamespaceAndName(ctx, db, "aggns", "aggproj")
	if err != nil || pid <= 0 {
		t.Fatalf("lookup project: id=%d err=%v", pid, err)
	}

	mustSetState := func(id int, state string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET state = $2 WHERE id = $1`, ScenarioStatusTable()), id, state); err != nil {
			t.Fatalf("set state %s for row %d: %v", state, id, err)
		}
	}

	id1, err := insertScenario(ctx, db, pid, ScenarioStateCreated, `{"k":"v"}`)
	if err != nil {
		t.Fatalf("insert scenario: %v", err)
	}
	id2, err := insertScenario(ctx, db, pid, ScenarioStateCreated, `{"k":"v"}`)
	if err != nil {
		t.Fatalf("insert scenario: %v", err)
	}
	id3, err := insertScenario(ctx, db, pid, ScenarioStateCreated, `{"k":"v"}`)
	if err != nil {
		t.Fatalf("insert scenario: %v", err)
	}

	// Nothing terminal yet: the total alone is non-zero.
	if c, err := ScenarioStateCountsByProject(ctx, db, pid); err != nil || c.Total != 3 || c.Finished != 0 || c.Failed != 0 {
		t.Fatalf("initial counts = %+v err=%v; want {3 0 0}", c, err)
	}

	// Every scenario finishes: the all-Finished aggregation input.
	mustSetState(id1, ScenarioStateFinished)
	mustSetState(id2, ScenarioStateFinished)
	mustSetState(id3, ScenarioStateFinished)
	if c, err := ScenarioStateCountsByProject(ctx, db, pid); err != nil || c.Total != 3 || c.Finished != 3 || c.Failed != 0 {
		t.Fatalf("all-finished counts = %+v err=%v; want {3 3 0}", c, err)
	}

	// A further scenario fails: the fail-fast aggregation input.
	id4, err := insertScenario(ctx, db, pid, ScenarioStateCreated, `{"k":"v"}`)
	if err != nil {
		t.Fatalf("insert scenario: %v", err)
	}
	mustSetState(id4, ScenarioStateFailed)
	if c, err := ScenarioStateCountsByProject(ctx, db, pid); err != nil || c.Total != 4 || c.Finished != 3 || c.Failed != 1 {
		t.Fatalf("fail-fast counts = %+v err=%v; want {4 3 1}", c, err)
	}
}
