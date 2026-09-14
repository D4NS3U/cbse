// Package app is the alpha4 Scenario Manager composition entry point.
// RunScenarioManager performs startup validation in the exact order and
// classification required by the SM startup failure class, connects to NATS and
// JetStream, reconciles streams and consumers, constructs the experiment
// informer, the four NATS adapters, the selection loop, and the runner-start
// and observation schedulers, starts them, emits the existing
// "Scenario Manager is ready" log only after every component has started,
// blocks on the context, and joins every started component on shutdown.
//
// It owns no domain logic; it only constructs and starts the components owned by
// the informer, natsadapter, selection, ready, runnerstart, and observation
// packages, consuming the 04-06 alpha4 library as-is.
//
// This package is a temporary alpha4 placement. Slice 07 moves it to its final
// internal home (merging into internal/core); it is NOT imported by cmd/main.go
// in this slice. The active alpha3 binary, CRD, schemes, manifests, and smoke
// path are unchanged.
package core

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"sync"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/config"
	"github.com/D4NS3U/cbse/scenario-manager/internal/informer"
	"github.com/D4NS3U/cbse/scenario-manager/internal/jobadapter"
	"github.com/D4NS3U/cbse/scenario-manager/internal/nats"
	"github.com/D4NS3U/cbse/scenario-manager/internal/observation"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
	"github.com/D4NS3U/cbse/scenario-manager/internal/rbac"
	"github.com/D4NS3U/cbse/scenario-manager/internal/ready"
	"github.com/D4NS3U/cbse/scenario-manager/internal/runnerstart"
	"github.com/D4NS3U/cbse/scenario-manager/internal/selection"
	"github.com/D4NS3U/cbse/scenario-manager/internal/translatorconfig"
	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver for database/sql
	natsgo "github.com/nats-io/nats.go"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	clientcmd "k8s.io/client-go/tools/clientcmd" //nolint:staticcheck // kubeconfig path fallback is intentional for local dev
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Env var names for the Core DB and NATS, shared with the alpha3 wiring.
const (
	coreDBDSNEnv      = "SCENARIO_MANAGER_CORE_DB_DSN"
	coreDBUserEnv     = "SCENARIO_MANAGER_CORE_DB_USER"
	coreDBPasswordEnv = "SCENARIO_MANAGER_CORE_DB_PASSWORD"
	natsURLEnv        = "SCENARIO_MANAGER_NATS_URL"
	natsUserEnv       = "SCENARIO_MANAGER_NATS_USER"
	natsPasswordEnv   = "SCENARIO_MANAGER_NATS_PASSWORD"
	// kubeconfigEnv optionally overrides the in-cluster REST config for local
	// development; production uses the in-cluster service account.
	kubeconfigEnv = "SCENARIO_MANAGER_KUBECONFIG"
)

// startupConfig carries the validated pure-configuration values that
// RunScenarioManager uses to construct components.
type startupConfig struct {
	workers                int
	natsURL                string
	maxAttempts            int
	publishRecoveryTimeout time.Duration
}

// RunScenarioManager is the alpha4 Scenario Manager composition entry point. It
// mirrors internal/core.RunScenarioManager for alpha4: startup validation in
// the SM startup failure-class order, NATS/JetStream connect + reconcile,
// construct + start the informer, four NATS adapters, selection loop, and
// runner-start and observation schedulers, emit the ready log, block on ctx,
// and join on shutdown. Any startup configuration failure is fatal and
// terminates the process before any informer, consumer, selector, or scheduler
// starts.
func RunScenarioManager(ctx context.Context) {
	cfg, err := validatePureConfig(os.Getenv)
	if err != nil {
		log.Fatalf("Scenario Manager startup configuration error: %v", err)
	}
	cfg.maxAttempts = translatorconfig.LoadMaxAttempts()
	cfg.publishRecoveryTimeout = translatorconfig.LoadPublishRecoveryTimeout()

	// Core DB: open from the DSN, then validate the schema before any business
	// processing starts. A malformed DSN or an incompatible schema is a fatal
	// startup error.
	store, db, err := openCoreDB(ctx)
	if err != nil {
		log.Fatalf("Scenario Manager startup: core DB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.EnsureSchema(ctx, store); err != nil {
		log.Fatalf("Scenario Manager startup: core DB schema: %v", err)
	}

	// Kubernetes: build the in-cluster REST config, the controller-runtime
	// client, and the client-go clientset. rbac.Verify rejects a denied
	// required cluster-wide authorization check before any component starts.
	restCfg, err := loadRESTConfig()
	if err != nil {
		log.Fatalf("Scenario Manager startup: kubernetes config: %v", err)
	}
	k8sScheme, err := buildK8sScheme()
	if err != nil {
		log.Fatalf("Scenario Manager startup: kubernetes scheme: %v", err)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: k8sScheme})
	if err != nil {
		log.Fatalf("Scenario Manager startup: kubernetes client: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		log.Fatalf("Scenario Manager startup: kubernetes clientset: %v", err)
	}
	if _, err := rbac.Verify(ctx, clientset); err != nil {
		log.Fatalf("Scenario Manager startup: authorization: %v", err)
	}

	// NATS: validate the URL and legacy credentials, then connect with no
	// credentials and reconcile the two streams and two SM-owned consumers.
	natsURL, err := validateNATSConfig(os.Getenv)
	if err != nil {
		log.Fatalf("Scenario Manager startup: NATS config: %v", err)
	}
	cfg.natsURL = natsURL
	nc, js, err := connectNATS(cfg.natsURL)
	if err != nil {
		log.Fatalf("Scenario Manager startup: NATS: %v", err)
	}
	defer nc.Close()
	if err := nats.ReconcileStreamsAndConsumers(js); err != nil {
		log.Fatalf("Scenario Manager startup: reconcile streams and consumers: %v", err)
	}

	// Construct the components.
	msgCleaner := nats.NewNATSDeletionClient(js)
	dispatch := informer.NewProductionDispatcher(k8sClient, store, msgCleaner)
	inf, err := informer.NewInformer(informer.Config{RESTConfig: restCfg, Scheme: k8sScheme}, dispatch)
	if err != nil {
		log.Fatalf("Scenario Manager startup: informer: %v", err)
	}

	readyHandler := ready.NewHandler(k8sClient, store, cfg.maxAttempts)
	adapters := nats.NewAdapters(nc, js, k8sClient, db, readyHandler.Handle)
	publisher := nats.NewTranslationRequestPublisher(js)
	availability := nats.NewEDSAvailabilityResponder(adapters)
	edsBatch := nats.NewEDSBatchConsumer(adapters)
	translatorReady := nats.NewTranslatorReadyConsumer(adapters)

	selDeps, err := selection.NewProductionDependencies(selection.ProductionDeps{
		K8s:         k8sClient,
		Store:       store,
		Publisher:   publisher,
		MaxAttempts: cfg.maxAttempts,
	})
	if err != nil {
		log.Fatalf("Scenario Manager startup: selection dependencies: %v", err)
	}
	selector, err := selection.NewSelector(publisher, cfg.publishRecoveryTimeout, selDeps)
	if err != nil {
		log.Fatalf("Scenario Manager startup: selection: %v", err)
	}

	adapter := jobadapter.NewControllerRuntimeAdapter(k8sClient)
	runnerStartScheduler, err := runnerstart.NewScheduler(
		&runnerstart.PersistenceStore{DB: store},
		adapter,
		runnerstart.Config{Workers: cfg.workers},
	)
	if err != nil {
		log.Fatalf("Scenario Manager startup: runner-start scheduler: %v", err)
	}
	observationScheduler, err := observation.NewScheduler(
		&observation.PersistenceStore{DB: store},
		adapter,
		observation.Config{Workers: 4},
	)
	if err != nil {
		log.Fatalf("Scenario Manager startup: observation scheduler: %v", err)
	}

	// Start the components in dependency order: the informer registers projects
	// and installs finalizers; the NATS consumers then consume on the
	// reconciled durables; the selection loop and schedulers then discover DB
	// rows. Only after every component has started is the ready log emitted.
	if err := inf.Start(ctx); err != nil {
		log.Fatalf("Scenario Manager startup: informer start: %v", err)
	}
	if err := availability.Start(ctx); err != nil {
		log.Fatalf("Scenario Manager startup: EDS availability: %v", err)
	}
	if err := edsBatch.Start(ctx); err != nil {
		log.Fatalf("Scenario Manager startup: EDS batch consumer: %v", err)
	}
	if err := translatorReady.StartTranslatorReadyConsumer(ctx, readyHandler.Handle); err != nil {
		log.Fatalf("Scenario Manager startup: translator-ready consumer: %v", err)
	}
	selectorDone, err := selector.Start(ctx)
	if err != nil {
		log.Fatalf("Scenario Manager startup: selection loop: %v", err)
	}
	runnerStartScheduler.Start()
	observationScheduler.Start()

	log.Println("Scenario Manager is ready")

	<-ctx.Done()

	// Join every started component that owns a join handle, mirroring
	// internal/core.RunScenarioManager. The NATS consumers stop when the
	// connection closes; the informer and schedulers have explicit shutdown
	// joins.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); inf.Shutdown() }()
	go func() { defer wg.Done(); _ = runnerStartScheduler.Shutdown(shutdownCtx) }()
	go func() { defer wg.Done(); _ = observationScheduler.Shutdown(shutdownCtx) }()
	if selectorDone != nil {
		<-selectorDone
	}
	wg.Wait()
	log.Printf("Scenario Manager shutdown complete: %v", ctx.Err())
}

// validatePureConfig performs the startup configuration checks that need no
// external dependency: the runner-start worker count and the messaging subject
// template / stream name canonicality. It is factored out so the configuration
// failure classes can be tested in isolation without a DB, Kubernetes, or NATS.
func validatePureConfig(getenv func(string) string) (startupConfig, error) {
	workers, err := config.ParseRunnerStartWorkers(getenv(config.RunnerStartWorkersEnv))
	if err != nil {
		return startupConfig{}, err
	}
	if err := nats.ValidateTemplates(getenv); err != nil {
		return startupConfig{}, err
	}
	if err := nats.ValidateStreamNames(getenv); err != nil {
		return startupConfig{}, err
	}
	return startupConfig{workers: workers}, nil
}

// validateNATSConfig validates the NATS URL and legacy credential env vars. The
// alpha4 NATS contract is authentication-free: the URL must not contain user
// information, and the legacy SCENARIO_MANAGER_NATS_USER/PASSWORD env vars must
// be empty. A non-empty legacy credential or a credential-bearing URL is a
// fatal startup configuration error.
func validateNATSConfig(getenv func(string) string) (string, error) {
	natsURL := getenv(natsURLEnv)
	if natsURL == "" {
		return "", fmt.Errorf("%s is not set", natsURLEnv)
	}
	if u, err := url.Parse(natsURL); err != nil {
		return "", fmt.Errorf("%s is malformed: %w", natsURLEnv, err)
	} else if u.User != nil {
		return "", fmt.Errorf("%s must not contain user information", natsURLEnv)
	}
	if getenv(natsUserEnv) != "" {
		return "", fmt.Errorf("legacy %s must be empty; alpha4 NATS is authentication-free", natsUserEnv)
	}
	if getenv(natsPasswordEnv) != "" {
		return "", fmt.Errorf("legacy %s must be empty; alpha4 NATS is authentication-free", natsPasswordEnv)
	}
	return natsURL, nil
}

// openCoreDB opens the Core DB from the DSN, user, and password env vars and
// returns a persistence.Store over the pool plus the raw *sql.DB (for shutdown).
// A missing or malformed DSN, user, or password is a fatal startup error.
func openCoreDB(ctx context.Context) (persistence.Store, *sql.DB, error) {
	baseDSN := os.Getenv(coreDBDSNEnv)
	if baseDSN == "" {
		return nil, nil, fmt.Errorf("%s is not set", coreDBDSNEnv)
	}
	user := os.Getenv(coreDBUserEnv)
	if user == "" {
		return nil, nil, fmt.Errorf("%s is not set", coreDBUserEnv)
	}
	password := os.Getenv(coreDBPasswordEnv)
	if password == "" {
		return nil, nil, fmt.Errorf("%s is not set", coreDBPasswordEnv)
	}
	finalDSN, err := buildCoreDBConnString(baseDSN, user, password)
	if err != nil {
		return nil, nil, fmt.Errorf("malformed core DB DSN: %w", err)
	}
	db, err := sql.Open("pgx", finalDSN)
	if err != nil {
		return nil, nil, fmt.Errorf("open core DB: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("ping core DB: %w", err)
	}
	return persistence.NewStore(db), db, nil
}

// buildCoreDBConnString merges the base DSN with the provided credentials so
// the pgx driver receives a fully-qualified connection string.
func buildCoreDBConnString(base, username, password string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	parsed.User = url.UserPassword(username, password)
	return parsed.String(), nil
}

// loadRESTConfig returns the in-cluster REST config, or a kubeconfig-based
// config when SCENARIO_MANAGER_KUBECONFIG is set (local development). A
// syntactically valid but unavailable API server is a retryable dependency
// failure surfaced by the caller.
func loadRESTConfig() (*rest.Config, error) {
	if kubeconfig := os.Getenv(kubeconfigEnv); kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

// buildK8sScheme returns a scheme that knows the alpha4 SimulationExperiment
// and the built-in Kubernetes types so the informer cache and the
// controller-runtime client can decode objects.
func buildK8sScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := experimentalpha4.AddToScheme(s); err != nil {
		return nil, err
	}
	return s, nil
}

// connectNATS connects to the NATS broker with no credentials and returns the
// connection and a JetStream context. A syntactically valid but unavailable
// NATS server is a retryable dependency failure surfaced to the caller.
func connectNATS(natsURL string) (*natsgo.Conn, natsgo.JetStreamContext, error) {
	nc, err := natsgo.Connect(natsURL, natsgo.Name("Scenario Manager"))
	if err != nil {
		return nil, nil, fmt.Errorf("connect NATS %s: %w", natsURL, err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("JetStream context: %w", err)
	}
	return nc, js, nil
}
