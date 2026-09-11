// Package runnerstart implements the bounded ordered runner-start scheduler
// (Slice 06 S06-M4). It discovers every StartingRunners scenario from the Core
// DB in ascending positive-ID order, de-duplicates the process-local ready,
// delayed, and in-flight sets by scenario-status ID, dispatches the lowest
// currently eligible ID to a free reconciler, and applies the guarded database
// transition dictated by the resource-neutral scheduler adapter outcome. A
// transient failure records a process-local nextEligibleAt five seconds after
// the attempt completes without occupying a reconciler; when the delay expires
// the ID rejoins the eligible set and regains its ascending-ID position. The
// scheduler is process-local: a restart loses the in-memory state and the
// immediate discovery reconstructs it from the authoritative database rows.
package runnerstart

import (
	"context"
	"fmt"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/persistence"
)

// Projection is the runner-start DB projection for one StartingRunners scenario.
// It is a self-contained copy of persistence.RunnerStartProjection so the
// scheduler contract does not depend on the persistence struct layout.
type Projection struct {
	ID                   int
	TranslationAttempt   int
	NumberOfReps         int
	NumberOfComputedReps int
	ContainerImage       string
	ProjectNamespace     string
	ProjectName          string
}

// Store is the Core DB surface the runner-start scheduler needs. The guarded
// MarkInProcessing and MarkFailed transitions are single-row UPDATEs that return
// false when the row was no longer in the expected source state (stale success
// or lifecycle-gate closure). A nil projection from LoadProjection means the
// row is absent or no longer StartingRunners (stale): the scheduler removes the
// ID without a state transition.
type Store interface {
	ListStartingRunners(ctx context.Context) ([]int, error)
	LoadProjection(ctx context.Context, scenarioID int) (*Projection, error)
	MarkInProcessing(ctx context.Context, scenarioID int) (bool, error)
	MarkFailed(ctx context.Context, scenarioID int) (bool, error)
}

// PersistenceStore adapts a persistence.DB to the runnerstart.Store interface.
// It is the production Core DB surface; tests use a fake Store.
type PersistenceStore struct {
	DB persistence.DB
}

// ListStartingRunners returns every StartingRunners scenario ID in ascending
// positive-ID order.
func (s *PersistenceStore) ListStartingRunners(ctx context.Context) ([]int, error) {
	return persistence.ListStartingRunnersScenarios(ctx, s.DB)
}

// LoadProjection loads the runner-start projection for one StartingRunners
// scenario. A nil result with nil error means the row is absent or no longer
// StartingRunners (stale).
func (s *PersistenceStore) LoadProjection(ctx context.Context, scenarioID int) (*Projection, error) {
	p, err := persistence.LoadRunnerStartProjection(ctx, s.DB, scenarioID)
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
		ContainerImage:       p.ContainerImage,
		ProjectNamespace:     p.ProjectNamespace,
		ProjectName:          p.ProjectName,
	}, nil
}

// MarkInProcessing is the guarded StartingRunners -> InProcessing transition.
// A false result means the row was no longer StartingRunners.
func (s *PersistenceStore) MarkInProcessing(ctx context.Context, scenarioID int) (bool, error) {
	return persistence.MarkScenarioInProcessing(ctx, s.DB, scenarioID)
}

// MarkFailed is the guarded StartingRunners -> Failed transition for a
// permanent startup failure. A false result means the row was no longer
// StartingRunners (stale no-op).
func (s *PersistenceStore) MarkFailed(ctx context.Context, scenarioID int) (bool, error) {
	return persistence.MarkScenarioFailedFrom(ctx, s.DB, scenarioID, persistence.ScenarioStateStartingRunners)
}

// Config fixes the runner-start scheduler behavior for the lifetime of the SM
// process. Workers is the number of reconciler goroutines (1..64). The discovery
// interval and transient delay default to five seconds; tests may shorten them
// to assert ordering without waiting.
type Config struct {
	Workers           int
	DiscoveryInterval time.Duration
	TransientDelay    time.Duration
}

func (c Config) withDefaults() Config {
	out := c
	if out.DiscoveryInterval <= 0 {
		out.DiscoveryInterval = 5 * time.Second
	}
	if out.TransientDelay <= 0 {
		out.TransientDelay = 5 * time.Second
	}
	return out
}

// validateConfig returns a programmer error if Workers is outside 1..64. The
// startup configuration package performs the env-parse and SM-startup fatal
// check; this guard defends direct construction (tests and wiring).
func validateWorkers(workers int) error {
	if workers < 1 || workers > 64 {
		return fmt.Errorf("runner-start workers %d out of range [1,64]", workers)
	}
	return nil
}
