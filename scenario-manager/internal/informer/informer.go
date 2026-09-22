package informer

import (
	"context"
	"fmt"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/lifecycle"
	"github.com/D4NS3U/cbse/scenario-manager/internal/nats"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	kcache "k8s.io/client-go/tools/cache"
	crcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Config fixes the informer's Kubernetes surface. The scheme must know the
// alpha4 SimulationExperiment and the built-in Kubernetes types. The REST
// config is in-cluster in production; tests inject an envtest config.
type Config struct {
	Scheme     *runtime.Scheme
	RESTConfig *rest.Config
}

// Informer is the cluster-wide alpha4 SimulationExperiment informer. It owns
// a controller-runtime cache and a Dispatcher. Start launches the cache,
// attaches add/update/delete handlers, waits for the initial sync, and then
// dispatches lifecycle events until ctx is cancelled. Shutdown joins the
// dispatcher's in-flight actions.
type Informer struct {
	cfg      Config
	dispatch *Dispatcher
	cache    crcache.Cache
}

// NewInformer constructs an informer from a pre-built Dispatcher and a cache
// config. The Dispatcher is built separately (NewProductionDispatcher or
// NewDispatcher) so the cache wiring is decoupled from the dispatch
// dependencies.
func NewInformer(cfg Config, dispatch *Dispatcher) (*Informer, error) {
	if cfg.RESTConfig == nil {
		return nil, fmt.Errorf("informer REST config must not be nil")
	}
	if dispatch == nil {
		return nil, fmt.Errorf("dispatcher must not be nil")
	}
	return &Informer{cfg: cfg, dispatch: dispatch}, nil
}

// Dispatcher returns the underlying Dispatcher so the caller can inspect
// in-flight actions (tests) or trigger shutdown.
func (i *Informer) Dispatcher() *Dispatcher { return i.dispatch }

// Start builds the cache, attaches handlers, starts it, and blocks until the
// cache has synced. It returns once the cache is running; the caller cancels
// ctx to stop. The dispatcher's root context is set to ctx so shutdown cancels
// every in-flight action.
func (i *Informer) Start(ctx context.Context) error {
	i.dispatch.SetRootContext(ctx)

	scheme := i.cfg.Scheme
	if scheme == nil {
		var err error
		scheme, err = buildScheme()
		if err != nil {
			return err
		}
	}
	cache, err := crcache.New(i.cfg.RESTConfig, crcache.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("create informer cache: %w", err)
	}
	i.cache = cache

	informer, err := cache.GetInformer(ctx, &experimentalpha4.SimulationExperiment{})
	if err != nil {
		return fmt.Errorf("get SimulationExperiment informer: %w", err)
	}
	informer.AddEventHandler(kcache.ResourceEventHandlerFuncs{
		AddFunc:    i.dispatch.HandleAdd,
		UpdateFunc: i.dispatch.HandleUpdate,
		DeleteFunc: i.dispatch.HandleDelete,
	})

	go func() {
		if err := cache.Start(ctx); err != nil {
			// controller-runtime returns ctx.Err() on graceful shutdown.
			_ = err
		}
	}()
	if !cache.WaitForCacheSync(ctx) {
		return fmt.Errorf("SimulationExperiment informer cache failed to sync")
	}
	return nil
}

// Shutdown cancels every in-flight action goroutine and waits for them to exit.
// It is idempotent.
func (i *Informer) Shutdown() {
	i.dispatch.Shutdown()
}

// buildScheme returns a scheme that knows the alpha4 SimulationExperiment and
// the built-in Kubernetes types so the cache can decode objects.
func buildScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		return nil, fmt.Errorf("add core scheme: %w", err)
	}
	if err := experimentalpha4.AddToScheme(s); err != nil {
		return nil, fmt.Errorf("add alpha4 scheme: %w", err)
	}
	return s, nil
}

// NewProductionDispatcher builds a Dispatcher with the production dependencies
// bound to the alpha4 library: a DBProjectStore over the persistence store, a
// registerProject closure over persistence.RegisterProject, and the two
// canonical JetStream stream names from the NATS package for deletion-cleanup
// purges. It is a convenience for the app wiring so the informer and the
// dispatcher share the same store and DB.
func NewProductionDispatcher(k8s client.Client, store persistence.Store, msg lifecycle.MessagingCleaner) *Dispatcher {
	projectStore := lifecycle.DBProjectStore{Store: store}
	d := NewDispatcher(k8s, projectStore, msg, func(ctx context.Context, namespace, project string) error {
		_, err := persistence.RegisterProject(ctx, store, namespace, project)
		return err
	})
	d.edsStreamName = nats.EDSStreamName
	d.translatorStreamName = nats.TranslatorStreamName
	return d
}
