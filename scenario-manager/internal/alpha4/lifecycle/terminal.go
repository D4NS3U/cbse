package lifecycle

import (
	"context"
	"errors"
	"fmt"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/persistence"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MessagingCleaner performs the NATS-side deletion-time cleanup for one
// experiment. It is injected so unit tests exercise the cleanup ordering with
// fakes; the real adapter wraps a JetStream context.
type MessagingCleaner interface {
	// DeleteTranslatorConsumer deletes the per-experiment Translator consumer
	// after ownership verification. A missing consumer is success (nil). An
	// ownership collision returns an error so the caller retains the finalizer
	// and retries.
	DeleteTranslatorConsumer(ctx context.Context, uid, namespace, project string) error
	// PurgeSubject purges messages matching subject from the named stream. A
	// missing stream or a stream with no matching messages is success (nil).
	PurgeSubject(ctx context.Context, stream, subject string) error
}

// RunTerminalAction applies the idempotent Error/Failed terminal action for a
// non-deleting experiment in one pass:
//
//  1. Delete and confirm absence of all ownership-verified runner Jobs (pass 1).
//  2. In one Core DB transaction, move the project's scenarios in Created,
//     Scheduled, StartingRunners, InProcessing, or PostProcessing to Failed.
//  3. List and delete verified runner Jobs again (pass 2) to catch a Job create
//     that crossed the gate-close event.
//
// An absent project row, zero matching unfinished rows, and missing Jobs are
// success. An ownership collision or a Job still present after deletion fails
// the attempt; the caller retries on the fixed cadence. The terminal action
// does not delete the project row, scenario rows, the Translator consumer, or
// retained messages: those remain for diagnosis until CR deletion.
func RunTerminalAction(ctx context.Context, k8s client.Client, store ProjectStore, exp *experimentalpha4.SimulationExperiment) error {
	if exp == nil {
		return fmt.Errorf("terminal action: experiment must not be nil")
	}
	// Pass 1: delete verified Jobs and confirm absence.
	pass1, err := DeleteVerifiedRunnerJobs(ctx, k8s, exp)
	if err != nil {
		return fmt.Errorf("terminal action pass 1: %w", err)
	}
	if err := ConfirmRunnerJobsAbsent(ctx, k8s, exp.Namespace, pass1); err != nil {
		return fmt.Errorf("terminal action pass 1 absence: %w", err)
	}

	// Bulk update: move non-terminal scenarios to Failed. An absent project is
	// success.
	projectID, err := store.ProjectIDByNamespaceAndName(ctx, exp.Namespace, exp.Name)
	if err != nil && !errors.Is(err, persistence.ErrProjectNotFound) {
		return fmt.Errorf("terminal action resolve project: %w", err)
	}
	if err == nil {
		if _, err := store.MarkScenariosFailedForProject(ctx, projectID); err != nil {
			return fmt.Errorf("terminal action bulk update: %w", err)
		}
	}

	// Pass 2: delete verified Jobs again to catch a crossing create.
	if _, err := DeleteVerifiedRunnerJobs(ctx, k8s, exp); err != nil {
		return fmt.Errorf("terminal action pass 2: %w", err)
	}
	return nil
}

// RunCompletedAction closes only the lifecycle gate for a Completed, non-deleting
// experiment. It does not delete Jobs, change scenario rows, delete the
// Translator consumer, or purge subjects. The gate closure itself is the
// caller's responsibility (the informer event handler); this function is a
// documented no-op so the dispatch table is explicit.
func RunCompletedAction(ctx context.Context, k8s client.Client, store ProjectStore, exp *experimentalpha4.SimulationExperiment) error {
	_ = ctx
	_ = k8s
	_ = store
	_ = exp
	return nil
}
