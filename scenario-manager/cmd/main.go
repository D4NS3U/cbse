// Package main bootstraps the Scenario Manager binary.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/D4NS3U/cbse/scenario-manager/internal/core"
)

// main bootstraps the Scenario Manager. RunScenarioManager performs all startup
// validation (runner-start worker count, messaging templates/stream names, Core DB
// schema, Kubernetes authorization, and the authentication-free NATS connection) in
// the SM startup failure-class order before starting any informer, consumer,
// selector, or scheduler; any startup configuration failure is fatal and exits the
// process before the control loop starts.
func main() {
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	core.RunScenarioManager(rootCtx)
}
