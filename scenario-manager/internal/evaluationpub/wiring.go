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

package evaluationpub

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/nats"
	"github.com/D4NS3U/cbse/scenario-manager/internal/persistence"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ProductionDeps is the concrete surface the production wiring binds into the
// transport-neutral Dependencies: a controller-runtime client to fetch the
// live experiment for the lifecycle gate, a persistence.Store (which also
// satisfies persistence.DB) for the guarded transitions and the round-scoped
// discovery queries, a JetStream context for the per-experiment PPS consumer
// reconciliation, and the EvaluationRequestPublisher.
type ProductionDeps struct {
	K8s       client.Client
	Store     persistence.Store
	JS        natsgoJetStream
	Publisher communication.EvaluationRequestPublisher
}

// natsgoJetStream is the JetStream surface the production wiring needs for the
// per-experiment PPS consumer reconciliation. natsgo.JetStreamContext
// satisfies it; naming it locally keeps this file's imports explicit.
type natsgoJetStream = nats.JetStreamDeletion

// NewProductionDependencies binds the concrete production surfaces to the
// transport-neutral Dependencies the publisher consumes. It is the only place
// the evaluationpub package touches controller-runtime, persistence, and NATS
// directly; the loop logic itself depends only on the function fields.
func NewProductionDependencies(deps ProductionDeps) (Dependencies, error) {
	if deps.K8s == nil {
		return Dependencies{}, fmt.Errorf("kubernetes client must not be nil")
	}
	if deps.Store == nil {
		return Dependencies{}, fmt.Errorf("persistence store must not be nil")
	}
	if deps.JS == nil {
		return Dependencies{}, fmt.Errorf("jetstream context must not be nil")
	}
	if deps.Publisher == nil {
		return Dependencies{}, fmt.Errorf("evaluation request publisher must not be nil")
	}
	return Dependencies{
		now:  time.Now,
		wait: waitDelay,
		nextPostProcessingScenario: func(ctx context.Context) (*PostProcessingCandidate, error) {
			return nextPostProcessingScenario(ctx, deps.Store)
		},
		claimScenarioForEvaluation: func(ctx context.Context, id int) (int, bool, error) {
			return persistence.ClaimScenarioForEvaluation(ctx, deps.Store, id)
		},
		loadEvaluationProjection: func(ctx context.Context, id int) (*EvaluationProjection, error) {
			return loadEvaluationProjection(ctx, deps.Store, id)
		},
		getExperiment: func(ctx context.Context, namespace, name string) (*experimentalpha4.SimulationExperiment, error) {
			exp := &experimentalpha4.SimulationExperiment{}
			if err := deps.K8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, exp); err != nil {
				return nil, err
			}
			return exp, nil
		},
		ensurePPSConsumer: func(ctx context.Context, uid, namespace, project string) error {
			_ = ctx
			return nats.EnsurePPSConsumer(deps.JS, nats.PPSStreamName, uid, namespace, project)
		},
		markEvaluationPublishStarted: func(ctx context.Context, id, attempt int) (bool, error) {
			return persistence.MarkEvaluationPublishStarted(ctx, deps.Store, id, attempt)
		},
		publish: func(ctx context.Context, s communication.ScenarioForEvaluation) error {
			return deps.Publisher.PublishEvaluationRequest(ctx, s)
		},
		markEvaluationRequestPublished: func(ctx context.Context, id, attempt int) (bool, error) {
			return persistence.MarkEvaluationRequestPublished(ctx, deps.Store, id, attempt)
		},
	}, nil
}

// nextPostProcessingScenario returns the globally lowest positive PostProcessing
// scenario, with its static (namespace, name) project identity. Discovery only
// observes the row; the caller claims the exact id with
// persistence.ClaimScenarioForEvaluation, which performs the guarded claim. A
// nil result with nil error means no PostProcessing scenario exists.
func nextPostProcessingScenario(ctx context.Context, db persistence.DB) (*PostProcessingCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("db must not be nil")
	}
	query := fmt.Sprintf(`
		SELECT s.id, p.project_namespace, p.project_name
		FROM %s s
		JOIN %s p ON p.id = s.project_id
		WHERE s.id > 0 AND s.state = $1
		ORDER BY s.id ASC
		LIMIT 1`, persistence.ScenarioStatusTable(), persistence.ProjectTable())
	var c PostProcessingCandidate
	err := db.QueryRowContext(ctx, query, persistence.ScenarioStatePostProcessing).Scan(
		&c.ID, &c.ProjectNamespace, &c.ProjectName,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("find next post-processing scenario for evaluation: %w", err)
	}
	return &c, nil
}

// loadEvaluationProjection loads the round-scoped projection the evaluation
// request payload needs for one claimed scenario: the current runner_round,
// the cross-round computed total (the replications so far), the nullable
// per-scenario confidence_metric, and the static project identity. A nil
// result with nil error means the row is absent (stale claim): the caller
// skips the publish without a mutation.
func loadEvaluationProjection(ctx context.Context, db persistence.DB, scenarioID int) (*EvaluationProjection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("db must not be nil")
	}
	query := fmt.Sprintf(`
		SELECT s.id, p.project_namespace, p.project_name, s.runner_round, s.number_of_computed_reps, s.confidence_metric
		FROM %s s
		JOIN %s p ON p.id = s.project_id
		WHERE s.id = $1`, persistence.ScenarioStatusTable(), persistence.ProjectTable())
	var p EvaluationProjection
	err := db.QueryRowContext(ctx, query, scenarioID).Scan(
		&p.ScenarioID, &p.ProjectNamespace, &p.ProjectName, &p.RunnerRound, &p.NumberOfReps, &p.ConfidenceMetric,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load evaluation projection for scenario %d: %w", scenarioID, err)
	}
	return &p, nil
}
