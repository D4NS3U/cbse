// Package ready implements the alpha4 Translator-ready workflow as a
// communication.TranslatorReadyHandler. The workflow owns the semantic decision
// for one Translator ready message after the transport adapter has validated
// subject shape and JSON payload shape and applied the lifecycle gate.
//
// For a non-empty container image it validates the digest with
// registry.ValidateDigestImage, resolves the repository with
// registry.RepositoryFromDigest, and requires an exact match against the live
// experiment's spec.translator.repository; a digest from another repository is
// permanent poison (ACK, no persistence, no Job). On a match it applies the
// guarded persistence.MarkScenarioStartingRunners transition.
//
// For an empty container image it applies the attempt-consuming recovery path
// through persistence.MarkScenarioTranslationAttemptFailed (Created retry
// below the limit, Failed at the limit) and never refunds the attempt.
//
// A stale translation attempt (the row is no longer Scheduled for the exact
// attempt) or an identity mismatch is permanent poison: the workflow returns
// TranslatorReadyHandled so the adapter ACKs without a database mutation or
// Job creation. A transient database or Kubernetes lookup failure returns
// TranslatorReadyRetry so the adapter NAKs for redelivery.
//
// This package is a temporary alpha4 placement. Slice 07 moves it to its final
// internal home; it consumes only the exported 04-06 library surfaces so the
// move is a mechanical rename.
package ready

import (
	"context"
	"fmt"
	"strings"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
	"github.com/D4NS3U/cbse/scenario-manager/internal/registry"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Handler is the alpha4 Translator-ready workflow. It fetches the live
// experiment for the repository match, validates the ready image, and applies
// the guarded persistence transition. It satisfies the
// communication.TranslatorReadyHandler func type via its Handle method.
type Handler struct {
	k8s         client.Client
	db          persistence.DB
	maxAttempts int
}

// NewHandler returns a ready-workflow handler. k8s must have a scheme that knows
// the alpha4 SimulationExperiment. maxAttempts is the shared
// SCENARIO_MANAGER_TRANS_MAX_ATTEMPTS policy; the caller loads it once via
// translatorconfig.LoadMaxAttempts so the handler does not re-read the
// environment per message.
func NewHandler(k8s client.Client, db persistence.DB, maxAttempts int) *Handler {
	return &Handler{k8s: k8s, db: db, maxAttempts: maxAttempts}
}

// Handle applies the semantic Translator-ready workflow to one validated ready
// message. It returns TranslatorReadyHandled for every terminal semantic
// outcome (transition, duplicate, stale, or poison) and TranslatorReadyRetry
// only for a transient dependency failure.
func (h *Handler) Handle(ctx context.Context, ready communication.TranslatorReadyMessage) communication.TranslatorReadyHandlingResult {
	if h == nil {
		return communication.TranslatorReadyHandlingResult{Status: communication.TranslatorReadyRetry, Reason: "ready handler is nil"}
	}
	image := strings.TrimSpace(ready.ContainerImage)

	// Fetch the live experiment for spec.translator.repository. The transport
	// adapter already applied lifecycle.AdmitExperiment and only invokes the
	// handler for an admitted (live, non-deleting, InProgress) experiment; a
	// NotFound here is a race where the experiment disappeared between the
	// adapter gate and this fetch, which is permanent poison (ACK, no
	// mutation). A transient lookup error is retryable (NAK).
	exp := &experimentalpha4.SimulationExperiment{}
	if err := h.k8s.Get(ctx, types.NamespacedName{Namespace: ready.ProjectNamespace, Name: ready.ProjectName}, exp); err != nil {
		if apierrors.IsNotFound(err) {
			return communication.TranslatorReadyHandlingResult{Status: communication.TranslatorReadyHandled, Reason: "experiment gone: poison ready"}
		}
		return communication.TranslatorReadyHandlingResult{Status: communication.TranslatorReadyRetry, Reason: fmt.Sprintf("fetch experiment: %v", err)}
	}

	if image == "" {
		return h.handleEmptyImage(ctx, ready)
	}
	return h.handleImage(ctx, ready, image, exp)
}

// handleEmptyImage applies the attempt-consuming recovery path for a Translator
// that returned no usable image. It never refunds the attempt. A false result
// is a stale no-op (the row was no longer Scheduled for the exact attempt),
// which is still a terminal Handled outcome.
func (h *Handler) handleEmptyImage(ctx context.Context, ready communication.TranslatorReadyMessage) communication.TranslatorReadyHandlingResult {
	changed, finalState, err := persistence.MarkScenarioTranslationAttemptFailed(ctx, h.db, ready.ScenarioID, ready.TranslationAttempt, h.maxAttempts)
	if err != nil {
		return communication.TranslatorReadyHandlingResult{Status: communication.TranslatorReadyRetry, Reason: fmt.Sprintf("db recovery failed for empty image: %v", err)}
	}
	reason := "empty container image classified as poison ready"
	if changed {
		reason = fmt.Sprintf("empty container image consumed attempt; scenario -> %s", finalState)
	} else {
		reason = "empty container image stale or already handled"
	}
	return communication.TranslatorReadyHandlingResult{Status: communication.TranslatorReadyHandled, Reason: reason}
}

// handleImage validates the digest and repository match, then applies the
// guarded Scheduled -> StartingRunners transition. An invalid digest or a
// repository mismatch is permanent poison (Handled, ACK, no persistence, no
// Job). A false transition is a stale attempt (Handled, ACK, no mutation). A
// transient DB error is retryable (Retry, NAK).
func (h *Handler) handleImage(ctx context.Context, ready communication.TranslatorReadyMessage, image string, exp *experimentalpha4.SimulationExperiment) communication.TranslatorReadyHandlingResult {
	if err := registry.ValidateDigestImage(image); err != nil {
		return communication.TranslatorReadyHandlingResult{Status: communication.TranslatorReadyHandled, Reason: fmt.Sprintf("invalid digest: poison: %v", err)}
	}
	repo, err := registry.RepositoryFromDigest(image)
	if err != nil {
		return communication.TranslatorReadyHandlingResult{Status: communication.TranslatorReadyHandled, Reason: fmt.Sprintf("repository from digest: poison: %v", err)}
	}
	if repo != exp.Spec.Translator.Repository {
		return communication.TranslatorReadyHandlingResult{Status: communication.TranslatorReadyHandled, Reason: fmt.Sprintf("repository %q != spec.translator.repository %q: poison", repo, exp.Spec.Translator.Repository)}
	}

	ok, err := persistence.MarkScenarioStartingRunners(ctx, h.db, ready.ScenarioID, image)
	if err != nil {
		return communication.TranslatorReadyHandlingResult{Status: communication.TranslatorReadyRetry, Reason: fmt.Sprintf("db update failed: %v", err)}
	}
	if !ok {
		return communication.TranslatorReadyHandlingResult{Status: communication.TranslatorReadyHandled, Reason: "stale translation attempt: poison"}
	}
	return communication.TranslatorReadyHandlingResult{Status: communication.TranslatorReadyHandled, Reason: "scenario -> StartingRunners"}
}
