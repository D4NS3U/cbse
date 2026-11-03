// Copyright 2025-2026 Daniel Seufferth
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package main bootstraps the Scenario Manager binary.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/D4NS3U/cbse/scenario-manager/internal/core"
	"github.com/D4NS3U/cbse/scenario-manager/internal/health"
)

// main bootstraps the Scenario Manager. It constructs the explicit-dependency
// readiness state and the health server (listening on
// SCENARIO_MANAGER_HEALTH_ADDRESS, default :8080; a malformed address or a
// server start failure is fatal), serves /healthz and /readyz on that
// address, and runs core.RunScenarioManager, which performs all startup
// validation (runner-start worker count, messaging templates/stream names,
// Core DB schema, Kubernetes authorization, and the authentication-free NATS
// connection) in the SM startup failure-class order before starting any
// informer, consumer, selector, or scheduler; any startup configuration
// failure is fatal and exits the process before the control loop starts.
// RunScenarioManager flips the readiness state to ready at exactly the point
// immediately before the existing "Scenario Manager is ready" log and back to
// not ready before the shutdown join.
func main() {
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	readiness := health.NewReadiness()
	server, err := health.NewServer(readiness)
	if err != nil {
		log.Fatalf("Scenario Manager health server: %v", err)
	}
	go func() {
		if err := server.Start(rootCtx, health.Address(os.Getenv(health.AddressEnv))); err != nil {
			log.Fatalf("Scenario Manager health server: %v", err)
		}
	}()

	core.RunScenarioManager(rootCtx, readiness)
}
