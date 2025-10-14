package cmd

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"cbse.terministic.de/scenario-manager/internal/kube"
)

// main is the entrypoint of the Scenario Manager service.
// Right now it just idles indefinitely until a termination signal arrives.
func main() {
	// Create a cancellable context that listens for system signals (SIGINT, SIGTERM).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Channel to listen for OS signals.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Println("Scenario Manager started. Waiting for tasks...")

	experiments, err := kube.ListSimulationExperiments(ctx, "")
	if err != nil {
		log.Printf("Failed to list SimulationExperiments: %v", err)
	} else {
		log.Printf("Found %d SimulationExperiments in the cluster", len(experiments))
	}

	// Goroutine: Wait for termination signal and trigger cancellation.
	go func() {
		sig := <-sigCh
		log.Printf("Received signal: %s. Shutting down...", sig)
		cancel()
	}()

	// Idle loop: keeps the process alive until context is canceled.
	<-ctx.Done()

	// Cleanup logic (if needed later, e.g., closing DB connections).
	log.Println("Scenario Manager stopped gracefully.")
}
