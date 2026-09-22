// cmd/translator is the reference Translator framework entrypoint. It loads the
// validated configuration, connects the BuildKit sidecar and NATS JetStream,
// constructs the registry client and example generator, and runs the
// orchestrator with graceful shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	nats "github.com/nats-io/nats.go"

	"github.com/D4NS3U/cbse/component-templates/translator/internal/buildkit"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/config"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/generator"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/messaging"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/registry"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/registryauth"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/translator"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/workspace"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.LUTC)
	if err := run(); err != nil {
		log.Fatalf("translator: %v", err)
	}
}

func run() error {
	cfg, err := config.Load(config.Mounts{
		DetailDBDir:  config.DefaultDetailDBDir,
		ResultDBDir:  config.DefaultResultDBDir,
		RegistryAuth: config.DefaultRegistryAuth,
	})
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	// Re-load the validated registry Docker config for the BuildKit auth provider
	// and the registry client (config.Load has already validated its contents).
	authConfig, err := registryauth.LoadFile(cfg.Mounts.RegistryAuth)
	if err != nil {
		return fmt.Errorf("registry auth: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// BuildKit sidecar client and seams.
	bkc, err := buildkit.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("buildkit client: %w", err)
	}
	defer bkc.Close()

	// Registry digest/annotation verification client.
	regClient := registry.NewClient(authConfig, &http.Client{Timeout: 30 * time.Second})

	// Pod-local attempt workspace.
	ws := workspace.New("/workspace")

	// Reference SimPy generator with the real pgx Detail DB connector and the
	// process default DNS resolver.
	gen := generator.NewExampleGenerator()

	// NATS JetStream connection (no credentials in alpha4).
	nc, err := nats.Connect(cfg.NATSURL, nats.Name("cbse-translator-"+cfg.ExperimentUID))
	if err != nil {
		return fmt.Errorf("nats connect: %w", err)
	}
	defer nc.Close()

	// Per-experiment request consumer / ready publisher / consumer manager.
	msgClient, err := messaging.NewClient(nc, cfg.Stream, cfg.ExperimentUID, cfg.Namespace, cfg.Project, cfg.RequestSubject)
	if err != nil {
		return fmt.Errorf("messaging: %w", err)
	}

	tr := translator.New(translator.Deps{
		Config:      cfg,
		Workspace:   ws,
		Generator:   gen,
		Registry:    regClient,
		Solve:       buildkit.SolveFuncFor(bkc, authConfig),
		ListWorkers: buildkit.ListWorkersFuncFor(bkc),
		Consumer:    msgClient,
		Publisher:   msgClient,
		Manager:     msgClient,
	})

	log.Printf("translator: starting for experiment %s namespace=%s project=%s", cfg.ExperimentUID, cfg.Namespace, cfg.Project)
	if err := tr.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Printf("translator: shutdown complete")
	return nil
}
