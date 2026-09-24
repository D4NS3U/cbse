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

package lifecycle

import (
	"context"

	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
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
// Scenario Manager composition (internal/core) and injected into the
// lifecycle actions.
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
