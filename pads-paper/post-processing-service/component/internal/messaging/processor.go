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

// processor.go is the reference PPS AckExplicit processing loop. It binds to
// the Scenario Manager's per-experiment durable consumer, then consumes one
// evaluation request at a time:
//
//	read request -> validate (poison: ACK + log) -> query Result DB
//	(failure: NAK for redelivery) -> evaluate per policy -> publish the
//	verdict on the evaluation subject (PubAck-gated; failure: NAK) -> ACK.
//
// The processor depends on narrow seams (messaging.Consumer/Publisher/
// Manager, a resultdb.Connector) so the full request protocol is
// unit-testable with fakes and no real NATS or PostgreSQL.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/evaluation"
	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/resultdb"
	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/subject"
	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/wire"
)

// Deps holds the replaceable dependencies. Production wires the real
// adapters; tests inject fakes.
type Deps struct {
	// Identity is the pod's downward-API identity.
	Identity wire.Identity
	// EvaluationTemplate is the validated PPS_EVALUATION_SUBJECT_TEMPLATE.
	EvaluationTemplate string
	// ResultDB is the mounted Result DB connection configuration.
	ResultDB resultdb.DatabaseConfig
	// Params carries the policy and loop bounds.
	Params evaluation.Params
	// Connector opens Result DB connections.
	Connector resultdb.Connector
	Consumer  Consumer
	Publisher Publisher
	Manager   Manager
	Logger    *log.Logger
	// BindRetryInterval overrides the not-ready consumer retry cadence for
	// tests. Zero defaults to BindRetryInterval.
	BindRetryInterval time.Duration
	// InProgressInterval overrides the 30-second in-progress ack cadence for
	// tests. Zero defaults to InProgressInterval.
	InProgressInterval time.Duration
}

// Processor is the reference PPS processing loop.
type Processor struct {
	deps Deps
	log  *log.Logger
}

// New returns a Processor wired to deps.
func New(deps Deps) *Processor {
	logger := deps.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Processor{deps: deps, log: logger}
}

// Run binds to the per-experiment consumer (retrying while the Scenario
// Manager has not created it yet) and consumes one request at a time until
// ctx is cancelled. An ownership collision or any non-retryable bind failure
// fails startup.
func (p *Processor) Run(ctx context.Context) error {
	if err := p.bind(ctx); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		msg, err := p.deps.Consumer.Fetch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			p.log.Printf("pps: fetch error: %v", err)
			continue
		}
		if err := p.handle(ctx, msg); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if nackErr := msg.Nak(); nackErr != nil {
				p.log.Printf("pps: nak failed: %v", nackErr)
			}
			p.log.Printf("pps: request left for redelivery: %v", err)
		}
	}
}

// bind attaches to the Scenario Manager's per-experiment consumer, retrying
// at the bind interval while it is not created yet. It returns nil once
// bound, ctx's error on cancellation, or the bind failure (e.g. an
// ownership collision).
func (p *Processor) bind(ctx context.Context) error {
	interval := p.deps.BindRetryInterval
	if interval <= 0 {
		interval = BindRetryInterval
	}
	for {
		err := p.deps.Manager.EnsureConsumer()
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrConsumerNotReady) {
			p.log.Printf("pps: consumer not ready yet (Scenario Manager ensures it); retrying in %s", interval)
			select {
			case <-time.After(interval):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return fmt.Errorf("pps: ensure consumer: %w", err)
	}
}

// handle processes one request delivery through the full protocol and
// performs the server ack itself. It returns nil after a terminal
// acknowledged outcome (success or poison). A non-nil return means the
// request was NAKed for JetStream redelivery (Result DB or
// verdict-publication failure) or the context was cancelled while the
// request was still in flight.
func (p *Processor) handle(ctx context.Context, msg Message) error {
	req, err := wire.DecodeRequest(msg.Data())
	if err != nil {
		// Raw poison: ACK without a Result DB query or a verdict.
		p.log.Printf("pps: acknowledging raw poison: decode=%v", err)
		return msg.Ack()
	}
	if err := req.ValidateRequest(p.deps.Identity); err != nil {
		// Identity/domain poison: ACK without a Result DB query or a
		// verdict, mirroring the translator's permanent-poison taxonomy.
		p.log.Printf("pps: acknowledging poison request scenario=%d round=%d: %v", req.ScenarioID, req.RunnerRound, err)
		return msg.Ack()
	}
	if err := p.runWithInProgress(ctx, msg, func(opCtx context.Context) error {
		return p.process(opCtx, req)
	}); err != nil {
		if errors.Is(err, context.Canceled) {
			return err // cancellation: leave unacked for redelivery
		}
		return err // Result DB or publication failure: caller NAKs
	}
	return msg.Ack()
}

// process queries the Result DB (statistical policy only), evaluates per
// policy, and publishes the verdict on the evaluation subject with
// JetStream confirmation.
func (p *Processor) process(ctx context.Context, req *wire.Request) error {
	var observations []float64
	if p.deps.Params.Policy == evaluation.PolicyStatistical {
		res, err := resultdb.Fetch(ctx, p.deps.ResultDB, req.ScenarioID, p.deps.Connector)
		if err != nil {
			return fmt.Errorf("result db fetch: %w", err)
		}
		if res.Malformed > 0 {
			p.log.Printf("pps: scenario=%d: %d malformed result rows skipped (rows=%d, usable=%d)",
				req.ScenarioID, res.Malformed, res.Rows, len(res.Values))
		}
		observations = res.Values
	}

	out, err := evaluation.Evaluate(evaluation.Input{
		RunnerRound:      req.RunnerRound,
		NumberOfReps:     req.NumberOfReps,
		ConfidenceMetric: req.ConfidenceMetric,
		Observations:     observations,
		Params:           p.deps.Params,
	})
	if err != nil {
		return fmt.Errorf("evaluation: %w", err)
	}

	verdict := &wire.Verdict{
		ExperimentUID:     p.deps.Identity.ExperimentUID,
		Namespace:         p.deps.Identity.Namespace,
		Project:           p.deps.Identity.Project,
		ScenarioID:        req.ScenarioID,
		RunnerRound:       req.RunnerRound,
		Metric:            wire.MetricMeanWaitTime,
		Verdict:           out.Verdict,
		SampleMean:        out.SampleMean,
		HalfWidth:         out.HalfWidth,
		Replications:      out.Replications,
		ConfidenceMetric:  req.ConfidenceMetric,
		AdditionalRunners: out.AdditionalRunners,
		MaxReplications:   p.deps.Params.MaxReplications,
	}
	data, err := wire.EncodeVerdict(verdict)
	if err != nil {
		return fmt.Errorf("encode verdict: %w", err)
	}
	evalSubject, err := subject.EvaluationSubject(p.deps.EvaluationTemplate, req.ScenarioID)
	if err != nil {
		return fmt.Errorf("evaluation subject: %w", err)
	}
	if err := p.deps.Publisher.Publish(evalSubject, data); err != nil {
		return fmt.Errorf("publish verdict: %w", err)
	}
	p.log.Printf("pps: scenario=%d round=%d verdict=%s replications=%d additional=%d",
		req.ScenarioID, req.RunnerRound, out.Verdict, out.Replications, out.AdditionalRunners)
	return nil
}

// runWithInProgress runs op under a derived context, sending 30-second
// in-progress acknowledgements so the server does not redeliver under the
// two-minute AckWait. If an in-progress acknowledgement fails, the operation
// is cancelled and the error is returned (the caller leaves the request
// unacknowledged for redelivery).
func (p *Processor) runWithInProgress(ctx context.Context, msg Message, op func(context.Context) error) error {
	opCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	interval := p.deps.InProgressInterval
	if interval <= 0 {
		interval = InProgressInterval
	}
	done := make(chan error, 1)
	go func() { done <- op(opCtx) }()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			if err := msg.InProgress(); err != nil {
				cancel()
				return <-done
			}
		}
	}
}
