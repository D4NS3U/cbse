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

// translatorHandoffConfig groups the small set of shared settings that core
// needs for per-scenario translator handoff and unpublished-claim recovery.
type translatorHandoffConfig struct {
	MaxAttempts            int
	PublishRecoveryTimeout time.Duration
}

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

	cfg := loadTranslatorHandoffConfig()
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
		stateChanged, finalState, recoveryErr := coredb.MarkScenarioTranslationPublishFailed(ctx, claimed.ID, claimed.TranslationAttempt, cfg.MaxAttempts)
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

// RecoverUnpublishedTranslationClaim checks whether the supplied scenario id is
// still stuck in the short unpublished Scheduled window that Scenario Manager
// owns before publish confirmation is recorded.
//
// This helper is intentionally callable and scenario-scoped so a future higher
// level loop can decide which ids to inspect. It never scans for other rows.
// When no exact row matches the recovery guards, the function returns
// stateChanged=false without treating that as an error.
func RecoverUnpublishedTranslationClaim(ctx context.Context, scenarioID int) (bool, string, error) {
	cfg := loadTranslatorHandoffConfig()
	claimedBefore := time.Now().Add(-cfg.PublishRecoveryTimeout)

	stateChanged, finalState, err := coredb.RecoverUnpublishedTranslationClaim(ctx, scenarioID, claimedBefore, cfg.MaxAttempts)
	if err != nil {
		log.Printf("Unpublished-claim recovery failed for scenario %d (class=unpublished-claim recovery): %v", scenarioID, err)
		return false, "", err
	}
	if stateChanged {
		log.Printf("Recovered unpublished translation claim for scenario %d (old=%s new=%s).", scenarioID, coredb.ScenarioStateScheduled, finalState)
	}
	return stateChanged, finalState, nil
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
	cfg := loadTranslatorHandoffConfig()
	ready.ContainerImage = strings.TrimSpace(ready.ContainerImage)

	if ready.ContainerImage == "" {
		stateChanged, finalState, err := coredb.MarkScenarioTranslationAttemptFailed(ctx, ready.ScenarioID, ready.TranslationAttempt, cfg.MaxAttempts)
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

// loadTranslatorHandoffConfig reads the shared translator retry and unpublished
// claim recovery settings needed by core handoff helpers.
//
// The values are intentionally sourced from translatorconfig so core and the
// transport adapter both use the same max-attempt and recovery-timeout policy.
func loadTranslatorHandoffConfig() translatorHandoffConfig {
	return translatorHandoffConfig{
		MaxAttempts:            translatorconfig.LoadMaxAttempts(),
		PublishRecoveryTimeout: translatorconfig.LoadPublishRecoveryTimeout(),
	}
}
