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

package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TranslationCandidate is the small projection returned by Created-scenario
// discovery for the alpha4 selection loop. It carries the positive scenario id
// and the static (namespace, name) project identity so the loop can fetch the
// live experiment and re-apply the lifecycle gate before publishing. The
// selection loop claims the exact id with ClaimScenarioForTranslation, which
// performs the guarded Created -> Scheduled transition and returns the
// publishing projection with the new attempt; discovery itself never locks or
// mutates the row.
type TranslationCandidate struct {
	ID                 int
	ProjectNamespace   string
	ProjectName        string
	TranslationAttempt int
}

// StaleTranslationClaim identifies the exact Scheduled translation attempt
// found by stale-claim discovery. Carrying the attempt with the id lets the
// later recovery update reject a row that was reclaimed between discovery and
// use.
type StaleTranslationClaim struct {
	ID                 int
	TranslationAttempt int
}

// NextCreatedScenarioForTranslation returns the globally lowest positive
// Created scenario for the translation-handoff selection loop, with its static
// (namespace, name) project identity and current translation attempt. Discovery
// only observes the row; the caller claims the exact id with
// ClaimScenarioForTranslation, which performs the guarded Created -> Scheduled
// transition. A nil result with nil error means no Created scenario exists.
//
// The selection loop owns Created -> Scheduled only. It must not discover
// StartingRunners (owned by the runnerstart scheduler) or PostProcessing (a
// boundary no-op in this branch), so this query filters on state = Created
// exclusively.
func NextCreatedScenarioForTranslation(ctx context.Context, db DB) (*TranslationCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("db must not be nil")
	}
	query := fmt.Sprintf(`
		SELECT s.id, p.project_namespace, p.project_name, s.translation_attempts
		FROM %s s
		JOIN %s p ON p.id = s.project_id
		WHERE s.id > 0 AND s.state = $1
		ORDER BY s.id ASC
		LIMIT 1`,
		ScenarioStatusTable(), ProjectTable())
	var c TranslationCandidate
	err := db.QueryRowContext(ctx, query, ScenarioStateCreated).Scan(
		&c.ID, &c.ProjectNamespace, &c.ProjectName, &c.TranslationAttempt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("find next created scenario for translation: %w", err)
	}
	return &c, nil
}

// NextStaleUnpublishedTranslationClaim returns the globally lowest positive
// Scheduled row whose publish confirmation is missing
// (translation_request_published_at IS NULL) and whose durable claim is
// strictly older than claimedBefore. Discovery does not modify or lock the row;
// the caller applies RecoverUnpublishedTranslationClaim with the exact id and
// attempt so a row reclaimed between discovery and recovery is rejected.
//
// A row with translation_publish_started_at set but
// translation_request_published_at null is an ambiguous publication: it is
// discovered here and RecoverUnpublishedTranslationClaim consumes the attempt
// without refunding it. A confirmed request (translation_request_published_at
// non-null) is excluded and never discovered.
func NextStaleUnpublishedTranslationClaim(ctx context.Context, db DB, claimedBefore time.Time) (*StaleTranslationClaim, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("db must not be nil")
	}
	if claimedBefore.IsZero() {
		return nil, fmt.Errorf("claimed-before threshold must be set")
	}
	query := fmt.Sprintf(`
		SELECT id, translation_attempts
		FROM %s
		WHERE id > 0
			AND state = $1
			AND translation_request_published_at IS NULL
			AND updated_at < $2
		ORDER BY id ASC
		LIMIT 1`,
		ScenarioStatusTable())
	var c StaleTranslationClaim
	err := db.QueryRowContext(ctx, query, ScenarioStateScheduled, claimedBefore).Scan(
		&c.ID, &c.TranslationAttempt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("find next stale unpublished translation claim: %w", err)
	}
	return &c, nil
}
