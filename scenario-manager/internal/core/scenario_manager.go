package core

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/D4NS3U/cbse/scenario-manager/internal/coredb"
	"github.com/D4NS3U/cbse/scenario-manager/internal/nats"
)

// RunScenarioManager starts Scenario Manager's context-owned integrations and
// its one joinable Basic Scenario Selection Logic (BSSL) worker, then waits for
// the shared context to be cancelled.
//
// Startup order:
//  1. Start the SimulationExperiment informer, which tracks project lifecycle
//     events used by Scenario Manager.
//  2. Start EDS communication over NATS/JetStream with a custom batch processor
//     that enforces in-process sequential handling of incoming scenario batches.
//  3. Start Translator communication, including its ready-message consumer.
//  4. Unless SCENARIO_MANAGER_SELECTOR_ENABLED=false, reuse that Translator
//     adapter as the publisher for one BSSL worker.
//  5. Report readiness, wait for shared-context cancellation, join BSSL, and
//     only then report final shutdown.
//
// Every startup step is a required dependency. Any initialization
// failure is treated as fatal and terminates the process immediately.
func RunScenarioManager(ctx context.Context) {
	if err := StartSimulationExperimentInformer(ctx, handleSimulationExperimentEvent); err != nil {
		log.Fatalf("Failed to start SimulationExperiment informer: %v", err)
	}

	if err := nats.StartEDSCommsWithProcessor(ctx, newSequentialEDSBatchProcessor()); err != nil {
		log.Fatalf("Failed to start EDS communication: %v", err)
	}

	translatorComms, err := nats.StartTranslatorComms(ctx, HandleTranslatorReady)
	if err != nil {
		log.Fatalf("Failed to start Translator communication: %v", err)
	}

	selectorEnabled := !strings.EqualFold(strings.TrimSpace(os.Getenv("SCENARIO_MANAGER_SELECTOR_ENABLED")), "false")
	var selectorDone <-chan struct{}
	if selectorEnabled {
		selectorDone, err = StartBasicScenarioSelector(ctx, translatorComms)
		if err != nil {
			log.Fatalf("Failed to start Basic Scenario Selection Logic: %v", err)
		}
		log.Println("Scenario Manager is ready; Basic Scenario Selection Logic worker started.")
	} else {
		log.Println("Scenario Manager is ready; Basic Scenario Selection Logic is disabled by configuration.")
	}

	<-ctx.Done()

	// The informer and NATS consumers retain their existing context-driven
	// lifecycle. BSSL supplies a join handle because final shutdown must not be
	// reported while its active context-aware operation is still unwinding.
	if selectorDone != nil {
		<-selectorDone
	}
	log.Printf("Scenario Manager shutdown complete: %v", ctx.Err())
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
