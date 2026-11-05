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

// cmd/post-processing-service is the reference PostProcessingService
// entrypoint. It loads the validated configuration (environment, container
// args, mounted Result DB connection Secret), connects the NATS JetStream
// client, constructs the Result DB connector, and runs the AckExplicit
// processing loop with graceful shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	nats "github.com/nats-io/nats.go"

	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/config"
	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/evaluation"
	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/messaging"
	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/resultdb"
	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/wire"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.LUTC)
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("post-processing-service: %v", err)
	}
}

func run(args []string) error {
	cfg, err := config.Load(args, config.Mounts{ResultDBDir: config.DefaultResultDBDir})
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// NATS JetStream connection (no credentials in alpha4).
	nc, err := nats.Connect(cfg.NATSURL, nats.Name("cbse-pps-"+cfg.ExperimentUID))
	if err != nil {
		return fmt.Errorf("nats connect: %w", err)
	}
	defer nc.Close()

	// Per-experiment request consumer binding / verdict publisher.
	msgClient, err := messaging.NewClient(nc, cfg.Stream, cfg.ExperimentUID, cfg.Namespace, cfg.Project, cfg.RequestSubject)
	if err != nil {
		return fmt.Errorf("messaging: %w", err)
	}

	// Read-only Result DB connector (the real pgx adapter).
	proc := messaging.New(messaging.Deps{
		Identity: wire.Identity{
			ExperimentUID: cfg.ExperimentUID,
			Namespace:     cfg.Namespace,
			Project:       cfg.Project,
		},
		EvaluationTemplate: cfg.EvalTemplate,
		ResultDB:           cfg.ResultDB,
		Params: evaluation.Params{
			Policy:                         cfg.Policy,
			DeterministicAdditionalRunners: cfg.DetAddRunners,
			MaxReplications:                cfg.MaxReplications,
			MaxRunnersPerRound:             cfg.MaxRunnersRound,
		},
		Connector: resultdb.PgxConnector{},
		Consumer:  msgClient,
		Publisher: msgClient,
		Manager:   msgClient,
	})

	log.Printf("post-processing-service: starting for experiment %s namespace=%s project=%s policy=%s",
		cfg.ExperimentUID, cfg.Namespace, cfg.Project, cfg.Policy)
	if err := proc.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Printf("post-processing-service: shutdown complete")
	return nil
}
