package core

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/coredb"
	"github.com/D4NS3U/cbse/scenario-manager/internal/translatorconfig"
)

// ProcessScenarioTrans executes the translator handoff for exactly one supplied
// scenario id.
//
// This function belongs to the core orchestration layer. It does not discover
// which scenario to process, initialize broker adapters, or start consumers.
// Instead it claims only the caller-provided row, publishes only after the DB
// claim commits, marks publish confirmation only after the publisher reports
// success, and uses exact-attempt recovery helpers when publish fails. A nil
// return after a nil claim means the row was skipped because it was missing or
// no longer in Created.
func ProcessScenarioTrans(ctx context.Context, scenarioID int, publisher communication.TranslationRequestPublisher) error {
	if publisher == nil {
		return fmt.Errorf("translation request publisher must not be nil")
	}

	maxAttempts := translatorconfig.LoadMaxAttempts()
	claimed, err := coredb.ClaimScenarioForTranslation(ctx, scenarioID)
	if err != nil {
		return err
	}
	if claimed == nil {
		log.Printf("Skipping translator handoff for scenario %d because it is missing or no longer in %s.", scenarioID, coredb.ScenarioStateCreated)
		return nil
	}

	log.Printf("Claimed scenario %d for translation handoff (project=%q old=%s new=%s attempt=%d).", claimed.ID, claimed.Project, coredb.ScenarioStateCreated, coredb.ScenarioStateScheduled, claimed.TranslationAttempt)

	if err := publisher.PublishTranslationRequest(ctx, *claimed); err != nil {
		log.Printf("Translator publish failure for scenario %d attempt %d (project=%q class=publish): %v", claimed.ID, claimed.TranslationAttempt, claimed.Project, err)
		stateChanged, finalState, recoveryErr := coredb.MarkScenarioTranslationPublishFailed(ctx, claimed.ID, claimed.TranslationAttempt, maxAttempts)
		if recoveryErr != nil {
			return recoveryErr
		}
		if stateChanged {
			log.Printf("Recovered publish failure for scenario %d (project=%q old=%s new=%s attempt=%d).", claimed.ID, claimed.Project, coredb.ScenarioStateScheduled, finalState, claimed.TranslationAttempt)
		} else {
			log.Printf("Publish failure recovery for scenario %d attempt %d was stale or already handled.", claimed.ID, claimed.TranslationAttempt)
		}
		return err
	}

	published, err := coredb.MarkScenarioTranslationRequestPublished(ctx, claimed.ID, claimed.TranslationAttempt)
	if err != nil {
		log.Printf("Translator publish marker update failed for scenario %d attempt %d after confirmed publish (project=%q class=DB): %v", claimed.ID, claimed.TranslationAttempt, claimed.Project, err)
		return err
	}
	if !published {
		log.Printf("Translator publish marker update for scenario %d attempt %d was stale after confirmed publish.", claimed.ID, claimed.TranslationAttempt)
		return nil
	}

	log.Printf("Recorded translation request publish for scenario %d (project=%q old=%s new=%s attempt=%d).", claimed.ID, claimed.Project, coredb.ScenarioStateScheduled, coredb.ScenarioStateScheduled, claimed.TranslationAttempt)
	return nil
}

// RecoverUnpublishedTranslationClaim applies recovery to the exact Scheduled
// attempt discovered by BSSL. The caller supplies the cutoff as well as the
// attempt so the guarded update uses precisely the same eligibility snapshot as
// discovery; this helper never reads the clock or reloads the recovery timeout.
func RecoverUnpublishedTranslationClaim(
	ctx context.Context,
	scenarioID int,
	expectedAttempt int,
	claimedBefore time.Time,
) (bool, string, error) {
	if ctx == nil {
		return false, "", fmt.Errorf("unpublished-claim recovery context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return false, "", err
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

	return coredb.RecoverUnpublishedTranslationClaim(
		ctx,
		scenarioID,
		expectedAttempt,
		claimedBefore,
		translatorconfig.LoadMaxAttempts(),
	)
}

// HandleTranslatorReady applies the semantic Translator ready workflow after a
// transport adapter has already validated subject shape and JSON shape.
//
// The function trims the container image, treats an empty image as a poison
// Translator response for the current attempt, and delegates all durable state
// changes to coredb. Terminal classified outcomes return Handled so adapters
// ACK them; transient persistence failures return Retry so adapters can request
// redelivery.
func HandleTranslatorReady(ctx context.Context, ready communication.TranslatorReadyMessage) communication.TranslatorReadyHandlingResult {
	maxAttempts := translatorconfig.LoadMaxAttempts()
	ready.ContainerImage = strings.TrimSpace(ready.ContainerImage)

	if ready.ContainerImage == "" {
		stateChanged, finalState, err := coredb.MarkScenarioTranslationAttemptFailed(ctx, ready.ScenarioID, ready.TranslationAttempt, maxAttempts)
		if err != nil {
			log.Printf("Translator ready recovery failed for scenario %d attempt %d (project=%q class=DB): %v", ready.ScenarioID, ready.TranslationAttempt, ready.Project, err)
			return communication.TranslatorReadyHandlingResult{
				Status: communication.TranslatorReadyRetry,
				Reason: "db recovery failed for empty container image",
			}
		}
		if stateChanged {
			log.Printf("Translator ready reported empty container image for scenario %d (project=%q old=%s new=%s attempt=%d class=poison ready).", ready.ScenarioID, ready.Project, coredb.ScenarioStateScheduled, finalState, ready.TranslationAttempt)
		} else {
			log.Printf("Translator ready with empty container image for scenario %d attempt %d was stale or already handled.", ready.ScenarioID, ready.TranslationAttempt)
		}
		return communication.TranslatorReadyHandlingResult{
			Status: communication.TranslatorReadyHandled,
			Reason: "empty container image classified as poison ready",
		}
	}

	result, err := coredb.MarkScenarioTranslatorReady(ctx, ready.ScenarioID, ready.TranslationAttempt, ready.ContainerImage)
	if err != nil {
		log.Printf("Translator ready persistence failed for scenario %d attempt %d (project=%q class=DB): %v", ready.ScenarioID, ready.TranslationAttempt, ready.Project, err)
		return communication.TranslatorReadyHandlingResult{
			Status: communication.TranslatorReadyRetry,
			Reason: "db update failed",
		}
	}

	switch result.Status {
	case coredb.TranslatorReadyApplied:
		log.Printf("Applied translator ready for scenario %d (project=%q old=%s new=%s attempt=%d).", ready.ScenarioID, ready.Project, result.PreviousState, result.CurrentState, ready.TranslationAttempt)
	case coredb.TranslatorReadyDuplicate:
		log.Printf("Duplicate translator ready for scenario %d attempt %d acknowledged without state change.", ready.ScenarioID, ready.TranslationAttempt)
	case coredb.TranslatorReadyConflict:
		log.Printf("Conflicting translator ready for scenario %d attempt %d acknowledged as terminal inconsistency.", ready.ScenarioID, ready.TranslationAttempt)
	case coredb.TranslatorReadyIgnoredStale:
		log.Printf("Stale translator ready for scenario %d attempt %d acknowledged without state change.", ready.ScenarioID, ready.TranslationAttempt)
	case coredb.TranslatorReadyPoison:
		log.Printf("Translator ready for missing scenario %d acknowledged as poison.", ready.ScenarioID)
	}

	return communication.TranslatorReadyHandlingResult{
		Status: communication.TranslatorReadyHandled,
		Reason: string(result.Status),
	}
}
