package core

import (
	"context"
	"log"

	"github.com/D4NS3U/cbse/scenario-manager/internal/nats"
)

// RunScenarioManager starts the SimulationExperiment informer and then waits
// for the shared context to be cancelled.
func RunScenarioManager(ctx context.Context) {
	if err := StartSimulationExperimentInformer(ctx, handleSimulationExperimentEvent); err != nil {
		log.Fatalf("Failed to start SimulationExperiment informer: %v", err)
	}

	if err := nats.StartEDSComms(ctx); err != nil {
		log.Fatalf("Failed to start EDS communication: %v", err)
	}

	log.Println("Scenario Manager is ready; awaiting future work loop.")
	<-ctx.Done()
	log.Printf("Scenario Manager shutting down: %v", ctx.Err())
}

func handleSimulationExperimentEvent(event SimulationExperimentEvent) {
	name := "<nil>"
	if event.Experiment != nil {
		name = event.Experiment.Name
	}
	log.Printf("SimulationExperiment event: %s (name=%s, old=%s, new=%s)", event.Type, name, event.OldPhase, event.NewPhase)
}
