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

// cmd/experimental-design-service is the reference ExperimentalDesignService
// entrypoint. It loads the validated configuration (environment + the
// mounted Detail DB connection Secret), self-provisions the canonical
// six-object Detail DB schema at startup (ruled R3 — a no-op against a
// pre-created matching schema), wires the design-layer store, the NATS
// publisher (the mock's bounded-retry skeleton), and the HTTP server (the
// JSON API + the embedded UI), and runs with graceful shutdown on
// SIGINT/SIGTERM. The EDS owns no Kubernetes credentials: its identity
// comes exclusively from the downward-API environment.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/D4NS3U/cbse/component-templates/experimental-design-service/internal/config"
	"github.com/D4NS3U/cbse/component-templates/experimental-design-service/internal/publisher"
	"github.com/D4NS3U/cbse/component-templates/experimental-design-service/internal/recipestore"
	"github.com/D4NS3U/cbse/component-templates/experimental-design-service/internal/server"
	"github.com/D4NS3U/cbse/component-templates/experimental-design-service/internal/wire"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.LUTC)
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("experimental-design-service: %v", err)
	}
}

func run(args []string) error {
	// The env contract is fully operator-injected and validated fail-fast at
	// startup; the only container arg is the listen address.
	cfg, err := config.Load(args, config.Mounts{DetailDBDir: config.DefaultDetailDBDir})
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Design-layer store over the mounted Detail DB connection Secret.
	store := recipestore.New(
		cfg.DetailDB.Host, int32(cfg.DetailDB.Port),
		cfg.DetailDB.User, cfg.DetailDB.Password, cfg.DetailDB.DBName,
		recipestore.PgxConnector{})

	// Startup self-provision (ruled R3): the canonical six-object DDL, run
	// before serving; a no-op against a pre-created matching schema.
	if err := store.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("detail database self-provision: %w", err)
	}
	log.Printf("experimental-design-service: detail DB schema self-provisioned (project %s namespace %s)", cfg.Project, cfg.Namespace)

	// The NATS publisher: the mock's bounded-retry skeleton (connect
	// 60 x 2s; availability request 60 x 2s, per-request timeout 5s;
	// PubAck-gated publish). The batch subject comes from the SM's
	// availability reply at runtime — never baked.
	pub, err := publisher.New(
		wire.Identity{
			ExperimentUID: cfg.ExperimentUID,
			Namespace:     cfg.Namespace,
			Project:       cfg.Project,
		},
		publisher.Config{AvailabilitySubject: cfg.AvailabilitySubject},
		publisher.NatsDialer(cfg.NATSURL))
	if err != nil {
		return fmt.Errorf("publisher: %w", err)
	}

	// The HTTP server: the JSON API + the embedded UI (one surface — the
	// e2e drives the same API).
	app := server.New(server.Deps{
		Identity:  wire.Identity{ExperimentUID: cfg.ExperimentUID, Namespace: cfg.Namespace, Project: cfg.Project},
		Store:     store,
		Publisher: pub,
	})
	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           app,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("experimental-design-service: serving on %s for experiment %s namespace=%s project=%s",
			cfg.Listen, cfg.ExperimentUID, cfg.Namespace, cfg.Project)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Printf("experimental-design-service: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	log.Printf("experimental-design-service: shutdown complete")
	return nil
}
