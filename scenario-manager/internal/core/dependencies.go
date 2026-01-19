package core

import (
	"context"
	"log"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/coredb"
	"github.com/D4NS3U/cbse/scenario-manager/internal/kube"
	"github.com/D4NS3U/cbse/scenario-manager/internal/nats"
)

const (
	maxCoreDBAttempts       = 6
	coreDBRetryDelay        = 10 * time.Second
	maxNATSAttempts         = 6
	natsRetryDelay          = 5 * time.Second
	// dependencyCheckTimeout caps overall startup checks to avoid hanging forever.
	dependencyCheckTimeout  = 2 * time.Minute
)

// CheckingDependencies sequentially validates connectivity to the Core DB,
// applies schema checks, confirms the NATS broker is reachable, and finally
// initializes the Kubernetes client used to coordinate SimulationExperiments.
// Any missing dependency results in a fatal log and immediate process exit.
func CheckingDependencies(rootCtx context.Context) {
	dependencyCtx, cancel := context.WithTimeout(rootCtx, dependencyCheckTimeout)
	defer cancel()

	if kube.KubeConnect() {
		log.Println("Kubernetes client initialized.")
	} else if kube.RunningInCluster() {
		log.Fatalf("Running inside a Kubernetes cluster but failed to initialize the Kubernetes client.")
	} else {
		log.Println("Kubernetes client not initialized because no in-cluster configuration was detected.")
	}

	for attempt := 1; attempt <= maxNATSAttempts; attempt++ {
		if err := dependencyCtx.Err(); err != nil {
			log.Fatalf("Dependency check canceled while connecting to NATS: %v", err)
		}

		if nats.Connect() {
			log.Printf("NATS broker connected on attempt %d.", attempt)
			break
		}

		if attempt == maxNATSAttempts {
			log.Fatalf("NATS connection failed after %d attempts.", maxNATSAttempts)
		}

		log.Printf("NATS connection attempt %d/%d failed. Retrying in %s...", attempt, maxNATSAttempts, natsRetryDelay)
		select {
		case <-dependencyCtx.Done():
			log.Fatalf("Dependency check canceled while waiting to retry NATS: %v", dependencyCtx.Err())
		case <-time.After(natsRetryDelay):
		}
	}

	for attempt := 1; attempt <= maxCoreDBAttempts; attempt++ {
		if err := dependencyCtx.Err(); err != nil {
			log.Fatalf("Dependency check canceled while connecting to Core DB: %v", err)
		}

		if coredb.CoreDBConnect() {
			log.Printf("Core DB connected on attempt %d.", attempt)
			break
		}

		if attempt == maxCoreDBAttempts {
			log.Fatalf("Core DB connection failed after %d attempts.", maxCoreDBAttempts)
		}

		log.Printf("Core DB connection attempt %d/%d failed. Retrying in %s...", attempt, maxCoreDBAttempts, coreDBRetryDelay)
		select {
		case <-dependencyCtx.Done():
			log.Fatalf("Dependency check canceled while waiting to retry Core DB: %v", dependencyCtx.Err())
		case <-time.After(coreDBRetryDelay):
		}
	}

	if !coredb.EnsureTablesAvailable(dependencyCtx) {
		log.Fatalf("Required Core DB tables are unavailable.")
	}
}
