// Package main bootstraps the Scenario Manager binary and ensures all dependencies
// are reachable before entering its long-running control loop.
package main

import (
	"context"
	"log"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/coredb"
	"github.com/D4NS3U/cbse/scenario-manager/internal/kube"
	"github.com/D4NS3U/cbse/scenario-manager/internal/nats"
)

const (
	maxCoreDBAttempts = 6
	coreDBRetryDelay  = 10 * time.Second
	maxNATSAttempts   = 6
	natsRetryDelay    = 5 * time.Second
)

// main bootstraps the Scenario Manager by ensuring all critical dependencies are reachable.
func main() {
	checkingDependencies()
	runScenarioManager()
}

// checkingDependencies sequentially validates connectivity to the Core DB,
// applies schema checks, confirms the NATS broker is reachable, and finally
// initializes the Kubernetes client used to coordinate SimulationExperiments.
func checkingDependencies() {
	ctx := context.Background()
	for attempt := 1; attempt <= maxCoreDBAttempts; attempt++ {
		if coredb.CoreDBConnect() {
			log.Printf("Core DB connected on attempt %d.", attempt)
			break
		}

		if attempt == maxCoreDBAttempts {
			log.Fatalf("Core DB connection failed after %d attempts.", maxCoreDBAttempts)
		}

		log.Printf("Core DB connection attempt %d/%d failed. Retrying in %s...", attempt, maxCoreDBAttempts, coreDBRetryDelay)
		time.Sleep(coreDBRetryDelay)
	}

	if !coredb.EnsureTablesAvailable(ctx) {
		log.Fatalf("Required Core DB tables are unavailable.")
	}

	for attempt := 1; attempt <= maxNATSAttempts; attempt++ {
		if nats.Connect() {
			log.Printf("NATS broker connected on attempt %d.", attempt)
			break
		}

		if attempt == maxNATSAttempts {
			log.Fatalf("NATS connection failed after %d attempts.", maxNATSAttempts)
		}

		log.Printf("NATS connection attempt %d/%d failed. Retrying in %s...", attempt, maxNATSAttempts, natsRetryDelay)
		time.Sleep(natsRetryDelay)
	}

	if kube.KubeConnect() {
		log.Println("Kubernetes client initialized.")
		return
	}

	if kube.RunningInCluster() {
		log.Fatalf("Running inside a Kubernetes cluster but failed to initialize the Kubernetes client.")
	}

	log.Println("Kubernetes client not initialized because no in-cluster configuration was detected.")
}

// runScenarioManager represents the long-running control loop placeholder.
// Once the dependency validation passes the process idles until future work
// (e.g., reconcilers or schedulers) is implemented.
func runScenarioManager() {
	log.Println("Scenario Manager is ready; awaiting future work loop.")
	select {}
}
