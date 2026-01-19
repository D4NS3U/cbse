// Package main bootstraps the Scenario Manager binary.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/D4NS3U/cbse/scenario-manager/internal/core"
)

// main bootstraps the Scenario Manager by ensuring all critical dependencies are reachable.
// Dependency failures are treated as fatal, so the process exits before the control loop starts.
func main() {
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	core.CheckingDependencies(rootCtx)
	core.RunScenarioManager(rootCtx)
}
