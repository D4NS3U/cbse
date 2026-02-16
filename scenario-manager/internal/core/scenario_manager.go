package core

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/D4NS3U/cbse/scenario-manager/internal/coredb"
	"github.com/D4NS3U/cbse/scenario-manager/internal/nats"
)

// RunScenarioManager starts the SimulationExperiment informer and then waits
// for the shared context to be cancelled.
//
// Startup order:
//  1. Start the SimulationExperiment informer, which tracks project lifecycle
//     events used by Scenario Manager.
//  2. Start EDS communication over NATS/JetStream with a custom batch processor
//     that enforces in-process sequential handling of incoming scenario batches.
//
// Both startup steps are considered required dependencies. Any initialization
// failure is treated as fatal and terminates the process immediately.
func RunScenarioManager(ctx context.Context) {
	if err := StartSimulationExperimentInformer(ctx, handleSimulationExperimentEvent); err != nil {
		log.Fatalf("Failed to start SimulationExperiment informer: %v", err)
	}

	if err := nats.StartEDSCommsWithProcessor(ctx, newSequentialEDSBatchProcessor()); err != nil {
		log.Fatalf("Failed to start EDS communication: %v", err)
	}

	// The component currently has no explicit work loop beyond informer callbacks
	// and NATS subscriptions, so it blocks until global shutdown.
	log.Println("Scenario Manager is ready; awaiting future work loop.")
	<-ctx.Done()
	log.Printf("Scenario Manager shutting down: %v", ctx.Err())
}

// handleSimulationExperimentEvent is the current callback used by the
// SimulationExperiment informer.
//
// The handler is intentionally lightweight and currently logs event metadata
// only. It is designed as a stable hook point for future behavior that reacts
// to project phase transitions.
func handleSimulationExperimentEvent(event SimulationExperimentEvent) {
	// name is set to a sentinel value when no experiment payload is attached.
	name := "<nil>"
	if event.Experiment != nil {
		name = event.Experiment.Name
	}
	log.Printf("SimulationExperiment event: %s (name=%s, old=%s, new=%s)", event.Type, name, event.OldPhase, event.NewPhase)
}

// newSequentialEDSBatchProcessor returns a NATS batch processor that guarantees
// sequential in-process handling of EDS scenario batches.
//
// Concurrency model:
//  1. NATS may invoke the processor concurrently.
//  2. A shared mutex serializes execution so only one batch is converted and
//     inserted at a time within this Scenario Manager process.
//
// Data flow:
//  1. Convert transport payload entries (nats.ScenarioBatch) into
//     persistence records (coredb.ScenarioStatusRecord).
//  2. Persist the entire converted slice via coredb.InsertScenarioStatusBatch.
//
// Reliability and timeout behavior are still governed by the caller in the NATS
// package (ACK/NAK semantics and context timeout handling).
func newSequentialEDSBatchProcessor() nats.EDSBatchProcessor {
	// mu serializes processor execution across concurrent callback invocations.
	var mu sync.Mutex

	return func(ctx context.Context, batch nats.ScenarioBatch) (int, error) {
		mu.Lock()
		defer mu.Unlock()

		if batch.Project == "" {
			return 0, fmt.Errorf("scenario batch project must not be empty")
		}

		projectID, err := coredb.ProjectIDByName(ctx, batch.Project)
		if err != nil {
			return 0, fmt.Errorf("resolve project %q: %w", batch.Project, err)
		}

		// records is pre-sized to avoid repeated reallocations while mapping the
		// incoming batch payload to DB insert records.
		records := make([]coredb.ScenarioStatusRecord, 0, len(batch.Scenarios))
		for _, scenario := range batch.Scenarios {
			records = append(records, coredb.ScenarioStatusRecord{
				ProjectID:            projectID,
				State:                coredb.DefaultScenarioState,
				Priority:             scenario.Priority,
				NumberOfReps:         scenario.NumberOfReps,
				NumberOfComputedReps: coredb.DefaultScenarioComputedReps,
				RecipeInfo:           scenario.RecipeInfo,
				ContainerImage:       coredb.DefaultScenarioContainerImage,
				ConfidenceMetric:     scenario.ConfidenceMetric,
			})
		}

		return coredb.InsertScenarioStatusBatch(ctx, records)
	}
}
