package selection

import (
	"context"
	"fmt"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ProductionDeps is the concrete surface the production wiring binds into the
// transport-neutral Dependencies: a controller-runtime client to fetch the
// live experiment for the lifecycle gate, a persistence.Store (which also
// satisfies persistence.DB) for the guarded transitions, the
// TranslationRequestPublisher, and the shared max-attempts policy.
type ProductionDeps struct {
	K8s         client.Client
	Store       persistence.Store
	Publisher   communication.TranslationRequestPublisher
	MaxAttempts int
}

// NewProductionDependencies binds the concrete production surfaces to the
// transport-neutral Dependencies the selector consumes. It is the only place
// the selection package touches controller-runtime and persistence directly;
// the loop logic itself depends only on the function fields. The selector
// loads publishRecoveryTimeout and maxAttempts once at startup (in the app
// package) and passes them in, so this constructor does not re-read the
// environment per iteration.
func NewProductionDependencies(deps ProductionDeps) (Dependencies, error) {
	if deps.K8s == nil {
		return Dependencies{}, fmt.Errorf("kubernetes client must not be nil")
	}
	if deps.Store == nil {
		return Dependencies{}, fmt.Errorf("persistence store must not be nil")
	}
	if deps.Publisher == nil {
		return Dependencies{}, fmt.Errorf("translation request publisher must not be nil")
	}
	if deps.MaxAttempts <= 0 {
		return Dependencies{}, fmt.Errorf("max attempts must be positive")
	}
	return Dependencies{
		now:  time.Now,
		wait: waitDelay,
		nextStaleUnpublishedTranslationClaim: func(ctx context.Context, before time.Time) (*persistence.StaleTranslationClaim, error) {
			return persistence.NextStaleUnpublishedTranslationClaim(ctx, deps.Store, before)
		},
		recoverUnpublishedTranslationClaim: func(ctx context.Context, id, attempt int, before time.Time) (bool, string, error) {
			return persistence.RecoverUnpublishedTranslationClaim(ctx, deps.Store, id, attempt, before, deps.MaxAttempts)
		},
		nextCreatedScenario: func(ctx context.Context) (*persistence.TranslationCandidate, error) {
			return persistence.NextCreatedScenarioForTranslation(ctx, deps.Store)
		},
		claimScenario: func(ctx context.Context, id int) (*persistence.ScenarioForTranslation, error) {
			return persistence.ClaimScenarioForTranslation(ctx, deps.Store, id)
		},
		getExperiment: func(ctx context.Context, namespace, name string) (*experimentalpha4.SimulationExperiment, error) {
			exp := &experimentalpha4.SimulationExperiment{}
			if err := deps.K8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, exp); err != nil {
				return nil, err
			}
			return exp, nil
		},
		markTranslationPublishStarted: func(ctx context.Context, id, attempt int) (bool, error) {
			return persistence.MarkTranslationPublishStarted(ctx, deps.Store, id, attempt)
		},
		publish: func(ctx context.Context, s communication.ScenarioForTranslation) error {
			return deps.Publisher.PublishTranslationRequest(ctx, s)
		},
		markScenarioTranslationRequestPublished: func(ctx context.Context, id, attempt int) (bool, error) {
			return persistence.MarkScenarioTranslationRequestPublished(ctx, deps.Store, id, attempt)
		},
		markScenarioTranslationPublishFailed: func(ctx context.Context, id, attempt int) (bool, string, error) {
			return persistence.MarkScenarioTranslationPublishFailed(ctx, deps.Store, id, attempt, deps.MaxAttempts)
		},
		cancelUnpublishedTranslationClaim: func(ctx context.Context, id, attempt int) (bool, error) {
			return persistence.CancelUnpublishedTranslationClaim(ctx, deps.Store, id, attempt)
		},
	}, nil
}
