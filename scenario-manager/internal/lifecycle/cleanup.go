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

package lifecycle

import (
	"context"
	"fmt"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/subject"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RunDeletionCleanup performs the idempotent deleted-experiment cleanup in the
// required order. The caller has already closed the lifecycle gate and re-got
// the current object; exp is the still-present CR protected by the SM finalizer
// and supplies the authoritative namespace, name, and UID.
//
// The two canonical JetStream stream names (edsStreamName and
// translatorStreamName) are injected by the caller rather than imported from the
// NATS package so this transport-neutral package has no dependency on the
// transport; the composition (internal/core) supplies the nats package's
// canonical constants.
//
//  1. Delete and confirm absence of all ownership-verified runner Jobs.
//  2. Retrieve and ownership-verify the per-experiment Translator consumer; a
//     missing consumer is success, a collision fails the attempt.
//  3. Purge cbse.<ns>.<proj>.eds.scenarios from edsStreamName.
//  4. Purge cbse.<ns>.<proj>.trans.request from translatorStreamName.
//  5. Purge cbse.<ns>.<proj>.trans.*.ready from translatorStreamName.
//  6. Delete the (project_namespace, project_name) row; ON DELETE CASCADE
//     removes its scenarios.
//  7. Final verified Job absence check.
//  8. Remove the SM finalizer.
//
// A missing stream, consumer, project row, verified Job, or finalizer is
// success. Any failure returns an error so the caller retains the finalizer
// and retries on the fixed cadence.
func RunDeletionCleanup(ctx context.Context, k8s client.Client, store ProjectStore, msg MessagingCleaner, edsStreamName, translatorStreamName string, exp *experimentalpha4.SimulationExperiment) error {
	if exp == nil {
		return fmt.Errorf("deletion cleanup: experiment must not be nil")
	}
	nsIdent, err := subject.ValidateIdent(exp.Namespace)
	if err != nil {
		return fmt.Errorf("deletion cleanup namespace: %w", err)
	}
	projIdent, err := subject.ValidateIdent(exp.Name)
	if err != nil {
		return fmt.Errorf("deletion cleanup project: %w", err)
	}

	// 1. Delete and confirm absence of verified runner Jobs.
	deleted, err := DeleteVerifiedRunnerJobs(ctx, k8s, exp)
	if err != nil {
		return fmt.Errorf("deletion cleanup step 1 (delete jobs): %w", err)
	}
	if err := ConfirmRunnerJobsAbsent(ctx, k8s, exp.Namespace, deleted); err != nil {
		return fmt.Errorf("deletion cleanup step 1 (confirm jobs absent): %w", err)
	}

	// 2. Retrieve and ownership-verify the Translator consumer, then delete.
	if err := msg.DeleteTranslatorConsumer(ctx, string(exp.UID), exp.Namespace, exp.Name); err != nil {
		return fmt.Errorf("deletion cleanup step 2 (translator consumer): %w", err)
	}

	// 3-5. Subject-filtered purges on the two shared streams.
	purges := []struct {
		stream  string
		subject string
		step    int
	}{
		{edsStreamName, subject.EDSBatchSubject(nsIdent, projIdent), 3},
		{translatorStreamName, subject.TranslatorRequestSubject(nsIdent, projIdent), 4},
		{translatorStreamName, subject.TranslatorReadyWildcardSubject(nsIdent, projIdent), 5},
	}
	for _, p := range purges {
		if err := msg.PurgeSubject(ctx, p.stream, p.subject); err != nil {
			return fmt.Errorf("deletion cleanup step %d (purge %s from %s): %w", p.step, p.subject, p.stream, err)
		}
	}

	// 6. Delete the project row; cascade removes its scenarios. A missing row is
	// success.
	if err := store.DeleteProjectByNamespaceAndName(ctx, exp.Namespace, exp.Name); err != nil {
		return fmt.Errorf("deletion cleanup step 6 (delete project): %w", err)
	}

	// 7. Final verified Job absence check.
	if err := ConfirmAllRunnerJobsAbsent(ctx, k8s, exp); err != nil {
		return fmt.Errorf("deletion cleanup step 7 (final job absence): %w", err)
	}

	// 8. Remove the SM finalizer.
	if err := RemoveFinalizer(ctx, k8s, exp); err != nil {
		return fmt.Errorf("deletion cleanup step 8 (remove finalizer): %w", err)
	}
	return nil
}

// GateClosedForDeletion reports whether the lifecycle gate should be closed for
// deletion rather than a phase action: when the current object carries a
// DeletionTimestamp, deletion cleanup takes precedence over its phase.
func GateClosedForDeletion(exp *experimentalpha4.SimulationExperiment) bool {
	return exp != nil && !exp.DeletionTimestamp.IsZero()
}

// DispatchAction selects the action for an accepted informer event. When the
// current object has a DeletionTimestamp it queues only deletion cleanup; an
// Error or Failed phase without deletion queues the terminal action; a
// Completed phase without deletion queues the completed (gate-close) action.
// Any other phase returns nil (no action).
func DispatchAction(exp *experimentalpha4.SimulationExperiment) ActionKind {
	if exp == nil {
		return ActionNone
	}
	if GateClosedForDeletion(exp) {
		return ActionDeletionCleanup
	}
	switch exp.Status.Phase {
	case PhaseError, PhaseFailed:
		return ActionTerminal
	case PhaseCompleted:
		return ActionCompleted
	default:
		return ActionNone
	}
}

// ActionKind names the lifecycle action queued for an accepted event.
type ActionKind int

const (
	ActionNone ActionKind = iota
	ActionTerminal
	ActionCompleted
	ActionDeletionCleanup
)

// String returns a stable name for logs and tests.
func (a ActionKind) String() string {
	switch a {
	case ActionTerminal:
		return "terminal"
	case ActionCompleted:
		return "completed"
	case ActionDeletionCleanup:
		return "deletion-cleanup"
	default:
		return "none"
	}
}
