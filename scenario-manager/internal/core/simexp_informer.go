package core

import (
	"context"
	"fmt"
	"log"

	experimentalpha2 "github.com/D4NS3U/cbse/experiment-operator/api/alpha2"
	"github.com/D4NS3U/cbse/scenario-manager/internal/coredb"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	kcache "k8s.io/client-go/tools/cache"
	crcache "sigs.k8s.io/controller-runtime/pkg/cache"
)

// SimulationExperimentEventType captures the set of informer notifications the
// informer can emit. Each value maps to a concrete lifecycle event that
// consumers can use to trigger downstream behaviour (publishing, logging,
// orchestration, etc.).
type SimulationExperimentEventType string

const (
	SimulationExperimentCreated    SimulationExperimentEventType = "SimulationExperimentCreated"
	SimulationExperimentDeleted    SimulationExperimentEventType = "SimulationExperimentDeleted"
	SimulationExperimentInProgress SimulationExperimentEventType = "SimulationExperimentInProgress"
	SimulationExperimentCompleted  SimulationExperimentEventType = "SimulationExperimentCompleted"
	SimulationExperimentFailed     SimulationExperimentEventType = "SimulationExperimentFailed"
	SimulationExperimentErrored    SimulationExperimentEventType = "SimulationExperimentErrored"

	// Internal phase markers used to decide which transitions merit an event.
	phaseInProgress = "InProgress"
	phaseCompleted  = "Completed"
	phaseFailed     = "Failed"
	phaseError      = "Error"
)

// SimulationExperimentEvent describes a notable change to a
// SimulationExperiment resource. Handlers receive a deep copy of the object so
// they can safely read or modify it without affecting the cache. OldPhase/NewPhase
// are populated only for updates that include a phase transition.
type SimulationExperimentEvent struct {
	Type       SimulationExperimentEventType
	Experiment *experimentalpha2.SimulationExperiment
	OldPhase   string
	NewPhase   string
}

// StartSimulationExperimentInformer initializes and runs a cluster-wide informer
// for SimulationExperiment resources.
//
// Behaviour:
//   - Builds an in-cluster REST config, registers core and SimulationExperiment
//     schemes, and spins up a controller-runtime cache.
//   - Attaches event handlers that emit SimulationExperimentEvent values for:
//       * creation (SimulationExperimentCreated)
//       * deletion (SimulationExperimentDeleted)
//       * phase transitions into InProgress, Completed, Failed, or Error
//   - Sends a deep copy of the object to the caller-provided handler so user
//     code does not mutate the cached object.
//   - Persists a summary of each SimulationExperiment into the core DB project
//     table so the informer cache state survives process restarts.
//   - Runs the informer until ctx is cancelled, returning an error if setup
//     fails or the cache fails to sync.
//
// Expected usage: call this during service startup, provide a handler that
// forwards events to your messaging or orchestration layer, and cancel the
// context on shutdown.
func StartSimulationExperimentInformer(ctx context.Context, handler func(SimulationExperimentEvent)) error {
	// Validate caller provided a handler; the informer cannot dispatch without it.
	if handler == nil {
		return fmt.Errorf("event handler must not be nil")
	}

	// Build an in-cluster REST config (uses service account token/CA mounted in the pod).
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("build in-cluster config: %w", err)
	}

	// Register built-in Kubernetes types and the SimulationExperiment CRD types
	// so the cache/informer can decode objects.
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add core scheme: %w", err)
	}
	if err := experimentalpha2.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add simulation experiment scheme: %w", err)
	}

	// Create a controller-runtime cache backed by the in-cluster config.
	cache, err := crcache.New(cfg, crcache.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("create informer cache: %w", err)
	}

	// Ask the cache for an informer that watches SimulationExperiment objects.
	informer, err := cache.GetInformer(ctx, &experimentalpha2.SimulationExperiment{})
	if err != nil {
		return fmt.Errorf("get SimulationExperiment informer: %w", err)
	}

	informer.AddEventHandler(kcache.ResourceEventHandlerFuncs{
		// AddFunc reports new SimulationExperiment objects.
		AddFunc: func(obj interface{}) {
			se, ok := obj.(*experimentalpha2.SimulationExperiment)
			if !ok {
				log.Printf("unexpected add object type: %T", obj)
				return
			}

			if err := persistSimulationExperiment(ctx, se); err != nil {
				log.Printf("failed to persist SimulationExperiment %q: %v", se.Name, err)
			}

			// Creation event: pass the new object to the handler with its initial phase.
			handler(SimulationExperimentEvent{
				Type:       SimulationExperimentCreated,
				Experiment: se.DeepCopy(),
				NewPhase:   se.Status.Phase,
			})
		},
		// UpdateFunc only reacts to phase changes, emitting a specific event when
		// the SimulationExperiment enters a notable phase.
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldSE, okOld := oldObj.(*experimentalpha2.SimulationExperiment)
			newSE, okNew := newObj.(*experimentalpha2.SimulationExperiment)
			if !okOld || !okNew {
				log.Printf("unexpected update object types: %T -> %T", oldObj, newObj)
				return
			}

			oldPhase := oldSE.Status.Phase
			newPhase := newSE.Status.Phase

			if err := persistSimulationExperiment(ctx, newSE); err != nil {
				log.Printf("failed to persist SimulationExperiment %q: %v", newSE.Name, err)
			}

			if oldPhase == newPhase {
				// Ignore updates that do not change phase.
				return
			}

			// Emit a typed event for the phase entered; copy the object to isolate
			// handler code from cache mutations.
			switch newPhase {
			case phaseInProgress:
				handler(SimulationExperimentEvent{
					Type:       SimulationExperimentInProgress,
					Experiment: newSE.DeepCopy(),
					OldPhase:   oldPhase,
					NewPhase:   newPhase,
				})
			case phaseCompleted:
				handler(SimulationExperimentEvent{
					Type:       SimulationExperimentCompleted,
					Experiment: newSE.DeepCopy(),
					OldPhase:   oldPhase,
					NewPhase:   newPhase,
				})
			case phaseFailed:
				handler(SimulationExperimentEvent{
					Type:       SimulationExperimentFailed,
					Experiment: newSE.DeepCopy(),
					OldPhase:   oldPhase,
					NewPhase:   newPhase,
				})
			case phaseError:
				handler(SimulationExperimentEvent{
					Type:       SimulationExperimentErrored,
					Experiment: newSE.DeepCopy(),
					OldPhase:   oldPhase,
					NewPhase:   newPhase,
				})
			default:
				// No-op for other phase changes; only specific transitions emit events.
			}
		},
		// DeleteFunc reports deleted SimulationExperiments, handling both standard
		// delete notifications and tombstones delivered after missed updates.
		DeleteFunc: func(obj interface{}) {
			se, ok := obj.(*experimentalpha2.SimulationExperiment)
			if !ok {
				tombstone, ok := obj.(kcache.DeletedFinalStateUnknown)
				if !ok {
					log.Printf("unexpected delete object type: %T", obj)
					return
				}
				se, ok = tombstone.Obj.(*experimentalpha2.SimulationExperiment)
				if !ok {
					log.Printf("unexpected tombstone object type: %T", tombstone.Obj)
					return
				}
			}

			if err := coredb.DeleteProject(ctx, se.Name); err != nil {
				log.Printf("failed to delete SimulationExperiment %q from core DB: %v", se.Name, err)
			}

			handler(SimulationExperimentEvent{
				Type:       SimulationExperimentDeleted,
				Experiment: se.DeepCopy(),
				OldPhase:   se.Status.Phase,
			})
		},
	})

	// Start the informer cache in the background; it stops when ctx is cancelled.
	go func() {
		if err := cache.Start(ctx); err != nil {
			log.Printf("SimulationExperiment informer stopped: %v", err)
		}
	}()

	// Block until the informer has synced with the API server; ensures handlers
	// see a consistent initial state before proceeding.
	if !cache.WaitForCacheSync(ctx) {
		return fmt.Errorf("SimulationExperiment informer cache failed to sync")
	}

	return nil
}

// persistSimulationExperiment writes a summary of the SimulationExperiment into
// the core DB so the Scenario Manager can recover state after restarts.
func persistSimulationExperiment(ctx context.Context, se *experimentalpha2.SimulationExperiment) error {
	if se == nil {
		return fmt.Errorf("SimulationExperiment is nil")
	}

	project := coredb.ProjectRecord{
		Name:               se.Name,
		NumberOfComponents: countExperimentComponents(se.Spec),
		Status:             se.Status.Phase,
	}

	return coredb.UpsertProject(ctx, project)
}

// countExperimentComponents returns how many optional components are configured
// in the SimulationExperiment spec.
func countExperimentComponents(spec experimentalpha2.SimulationExperimentSpec) int {
	count := 0

	if databaseSpecDefined(spec.DetailDatabase) {
		count++
	}
	if databaseSpecDefined(spec.ResultDatabase) {
		count++
	}
	if translatorSpecDefined(spec.Translator) {
		count++
	}
	if postProcessingSpecDefined(spec.PostProcessingService) {
		count++
	}
	if experimentalDesignServiceDefined(spec.ExperimentalDesignService) {
		count++
	}

	return count
}

// databaseSpecDefined reports whether a database spec has any configuration set.
func databaseSpecDefined(spec experimentalpha2.DatabaseSpec) bool {
	return spec.Image != "" ||
		spec.Host != "" ||
		spec.DBName != "" ||
		spec.User != "" ||
		spec.Password != "" ||
		spec.Port != 0 ||
		len(spec.Command) > 0 ||
		len(spec.Args) > 0 ||
		spec.NodePort != nil ||
		spec.ServiceType != ""
}

// translatorSpecDefined reports whether a translator spec has any configuration set.
func translatorSpecDefined(spec experimentalpha2.TranslatorSpec) bool {
	return spec.Image != "" ||
		spec.Repository != "" ||
		spec.BaseImage != "" ||
		spec.Port != 0 ||
		len(spec.Command) > 0 ||
		len(spec.Args) > 0 ||
		spec.NodePort != nil ||
		spec.ServiceType != ""
}

// postProcessingSpecDefined reports whether a post-processing spec has any configuration set.
func postProcessingSpecDefined(spec experimentalpha2.PostProcessingSpec) bool {
	return spec.Image != "" ||
		spec.Port != 0 ||
		len(spec.Command) > 0 ||
		len(spec.Args) > 0 ||
		spec.NodePort != nil ||
		spec.ServiceType != ""
}

// experimentalDesignServiceDefined reports whether an experimental design service spec has any configuration set.
func experimentalDesignServiceDefined(spec experimentalpha2.ExperimentalDesignServiceSpec) bool {
	return spec.Design != "" ||
		spec.Image != "" ||
		spec.Port != 0 ||
		len(spec.Command) > 0 ||
		len(spec.Args) > 0 ||
		spec.NodePort != nil ||
		spec.ServiceType != ""
}
