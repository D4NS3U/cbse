package lifecycle

import (
	"context"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/persistence"
)

// ProjectStore is the high-level alpha4 project and scenario persistence
// surface used by the terminal and deletion-cleanup actions. It is satisfied by
// DBProjectStore over a persistence.Store and by test fakes, so the lifecycle
// coordination logic can be unit-tested without a live database.
type ProjectStore interface {
	// ProjectIDByNamespaceAndName resolves the project id for the exact
	// (namespace, name) pair. It returns persistence.ErrProjectNotFound when no
	// row matches.
	ProjectIDByNamespaceAndName(ctx context.Context, namespace, project string) (int, error)
	// MarkScenariosFailedForProject moves the project's non-terminal scenarios
	// to Failed in one transaction and returns the number of rows moved. An
	// absent project or zero matching rows is success.
	MarkScenariosFailedForProject(ctx context.Context, projectID int) (int64, error)
	// DeleteProjectByNamespaceAndName deletes the project row; its foreign key
	// cascades to the project's scenarios. A missing row is success.
	DeleteProjectByNamespaceAndName(ctx context.Context, namespace, project string) error
}

// DBProjectStore adapts a persistence.Store to the ProjectStore interface by
// delegating to the persistence package functions. It is constructed in the
// alpha4 wiring (Slice 07) and injected into the lifecycle actions.
type DBProjectStore struct{ Store persistence.Store }

// ProjectIDByNamespaceAndName resolves the project id for the exact pair.
func (s DBProjectStore) ProjectIDByNamespaceAndName(ctx context.Context, namespace, project string) (int, error) {
	return persistence.ProjectIDByNamespaceAndName(ctx, s.Store, namespace, project)
}

// MarkScenariosFailedForProject bulk-moves the project's non-terminal scenarios
// to Failed.
func (s DBProjectStore) MarkScenariosFailedForProject(ctx context.Context, projectID int) (int64, error) {
	return persistence.MarkScenariosFailedForProject(ctx, s.Store, projectID)
}

// DeleteProjectByNamespaceAndName deletes the project row; cascade removes its
// scenarios.
func (s DBProjectStore) DeleteProjectByNamespaceAndName(ctx context.Context, namespace, project string) error {
	return persistence.DeleteProjectByNamespaceAndName(ctx, s.Store, namespace, project)
}
