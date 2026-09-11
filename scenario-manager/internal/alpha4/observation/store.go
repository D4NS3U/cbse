package observation

import (
	"context"
	"fmt"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/persistence"
)

// Projection is the self-contained observation projection the coordinator hands
// to a worker. It is a copy of the persistence row so the worker does not hold
// DB state across the Kubernetes call.
type Projection struct {
	ID                   int
	TranslationAttempt   int
	NumberOfReps         int
	NumberOfComputedReps int
	ProjectNamespace     string
	ProjectName          string
}

// Store is the persistence surface the observation scheduler uses. It is the
// resource-neutral subset of the Core DB the coordinator and workers need; the
// concrete adapter wraps a *persistence.DB.
type Store interface {
	// ListInProcessing returns every scenario ID currently in InProcessing,
	// ordered by ascending positive id.
	ListInProcessing(ctx context.Context) ([]int, error)
	// LoadProjection returns the observation projection for one InProcessing
	// scenario, or nil if the row is absent or no longer InProcessing (stale).
	LoadProjection(ctx context.Context, scenarioID int) (*Projection, error)
	// UpdateComputedRepsMonotonic sets number_of_computed_reps to
	// LEAST(number_of_reps, GREATEST(current, count)) while guarding state
	// InProcessing. It returns the resulting value and whether a row matched
	// (false = stale: the row is no longer InProcessing).
	UpdateComputedRepsMonotonic(ctx context.Context, scenarioID, count int) (int, bool, error)
	// MarkPostProcessing applies the guarded InProcessing -> PostProcessing
	// transition. A false result is stale success or a terminal-action move.
	MarkPostProcessing(ctx context.Context, scenarioID int) (bool, error)
	// MarkFailedFrom applies the guarded InProcessing -> Failed transition.
	// A false result is stale.
	MarkFailedFrom(ctx context.Context, scenarioID int) (bool, error)
}

// PersistenceStore adapts a persistence.DB to the observation Store interface.
type PersistenceStore struct {
	DB persistence.DB
}

func (s *PersistenceStore) ListInProcessing(ctx context.Context) ([]int, error) {
	return persistence.ListInProcessingScenarios(ctx, s.DB)
}

func (s *PersistenceStore) LoadProjection(ctx context.Context, scenarioID int) (*Projection, error) {
	p, err := persistence.LoadObservationProjection(ctx, s.DB, scenarioID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}
	return &Projection{
		ID:                   p.ID,
		TranslationAttempt:   p.TranslationAttempt,
		NumberOfReps:         p.NumberOfReps,
		NumberOfComputedReps: p.NumberOfComputedReps,
		ProjectNamespace:     p.ProjectNamespace,
		ProjectName:          p.ProjectName,
	}, nil
}

func (s *PersistenceStore) UpdateComputedRepsMonotonic(ctx context.Context, scenarioID, count int) (int, bool, error) {
	return persistence.UpdateScenarioComputedRepsMonotonic(ctx, s.DB, scenarioID, count)
}

func (s *PersistenceStore) MarkPostProcessing(ctx context.Context, scenarioID int) (bool, error) {
	return persistence.MarkScenarioPostProcessing(ctx, s.DB, scenarioID)
}

func (s *PersistenceStore) MarkFailedFrom(ctx context.Context, scenarioID int) (bool, error) {
	return persistence.MarkScenarioFailedFrom(ctx, s.DB, scenarioID, persistence.ScenarioStateInProcessing)
}

// Config is the observation scheduler configuration. Defaults are applied via
// withDefaults; the startup configuration package validates Workers from the
// SCENARIO_MANAGER_RUNNER_START_WORKERS environment (observation shares the
// runner-start worker count contract: 1..64, default 4).
type Config struct {
	Workers           int
	DiscoveryInterval time.Duration
}

func (c Config) withDefaults() Config {
	if c.Workers < 1 {
		c.Workers = 4
	}
	if c.Workers > 64 {
		c.Workers = 64
	}
	if c.DiscoveryInterval <= 0 {
		c.DiscoveryInterval = 5 * time.Second
	}
	return c
}

func validateWorkers(n int) error {
	if n < 1 || n > 64 {
		return fmt.Errorf("observation workers must be in [1,64], got %d", n)
	}
	return nil
}
