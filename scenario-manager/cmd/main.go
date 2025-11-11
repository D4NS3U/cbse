package main

import (
	"context"
	"log"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/coredb"
	"github.com/D4NS3U/cbse/scenario-manager/internal/kube"
)

const (
	maxCoreDBAttempts = 6
	coreDBRetryDelay  = 10 * time.Second
)

// main bootstraps the Scenario Manager by ensuring all critical dependencies are reachable.
func main() {
	checkingDependencies()
	runScenarioManager()
}

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

	if kube.KubeConnect() {
		log.Println("Kubernetes client initialized.")
		return
	}

	if kube.RunningInCluster() {
		log.Fatalf("Running inside a Kubernetes cluster but failed to initialize the Kubernetes client.")
	}

	log.Println("Kubernetes client not initialized because no in-cluster configuration was detected.")
}

func runScenarioManager() {
	log.Println("Scenario Manager is ready; awaiting future work loop.")
	select {}
}
