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

// Package publisher implements the reference ExperimentalDesignService's
// NATS publish flow: the bounded-retry connection skeleton, the availability
// handshake (the per-project request/reply with the bounded-retry
// availability request and the per-request timeout), and the JetStream
// scenario-batch publish — one batch of exactly the not-yet-published
// scenarios on Start, and the finalization batch on Finish. The skeleton
// mirrors the mock's proven retry semantics (D9): connect retries
// 60 attempts x 2s delay; availability-request retries 60 x 2s, per-request
// timeout 5s; status != ready, a missing batch_subject, or a
// missing/mismatched experiment_type echo is a hard error (no retry — the
// reason is surfaced); success requires the JetStream PubAck.
//
// The publisher is a long-running service with no keep-alive loop beyond the
// HTTP service: each action re-enters the skeleton, reusing the connection
// while it is healthy and reconnecting with the retry skeleton on loss.
//
// The flow is transport-agnostic: the caller supplies a Dialer that returns
// a Conn so the package is independently testable without a real NATS
// server (the fake-NATS pattern the sibling modules use). The real nats.go
// adapter lives in nats.go in this package.
package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/D4NS3U/cbse/component-templates/experimental-design-service/internal/wire"
)

// Defaults mirror the mock's bounded-retry skeleton (D9).
const (
	DefaultConnectAttempts = 60
	DefaultRequestAttempts = 60
	DefaultRetryDelay      = 2 * time.Second
	DefaultRequestTimeout  = 5 * time.Second
	DefaultConnectTimeout  = 2 * time.Second
)

// ErrUnavailable is the clean Scenario Manager refusal: the SM answered the
// availability request with status "error" and a reason (the experiment is
// not InProgress). It is not retried; the reason is the SM's own wording,
// surfaced to the UI (a 4xx flow refusal on the API).
type ErrUnavailable struct {
	Reason string
}

func (e *ErrUnavailable) Error() string {
	if e.Reason == "" {
		return "availability handshake failed"
	}
	return "availability handshake failed: " + e.Reason
}

// ErrProtocol is a hard protocol/transport error of the handshake (no
// retry): a ready reply without batch_subject, a missing or mismatched
// experiment_type echo, a consumed-but-malformed reply, or an exhausted
// request retry loop. It indicates an SM without (or beyond) the
// finalization protocol, or a transport the retries could not recover;
// the reason is surfaced to the UI (a 5xx on the API).
type ErrProtocol struct {
	Reason string
}

func (e *ErrProtocol) Error() string {
	if e.Reason == "" {
		return "availability protocol error"
	}
	return "availability protocol error: " + e.Reason
}

// ErrPublish is a publish failure after a successful handshake: the JetStream
// PubAck was not confirmed (transport loss or stream rejection).
type ErrPublish struct {
	BatchID string
	Err     error
}

func (e *ErrPublish) Error() string {
	return fmt.Sprintf("publish batch %s: %v", e.BatchID, e.Err)
}

func (e *ErrPublish) Unwrap() error { return e.Err }

// Conn is the NATS transport seam the publisher drives: the availability
// request/reply and the JetStream PubAck-gated batch publish.
type Conn interface {
	// Request publishes data on subject and waits for the single reply,
	// bounded by the per-request timeout. A timeout or transient transport
	// error is a retriable failure; the caller classifies the reply.
	Request(ctx context.Context, subject string, data []byte, timeout time.Duration) (reply []byte, err error)
	// Publish publishes data on subject via JetStream and blocks until the
	// server confirms publication (PubAck-gated).
	Publish(ctx context.Context, subject string, data []byte) error
	// Close releases the connection.
	Close()
}

// Dialer opens a NATS transport connection. It is retried by the connect
// skeleton (bounded attempts x retry delay).
type Dialer func(ctx context.Context) (Conn, error)

// Config is the publisher configuration.
type Config struct {
	AvailabilitySubject string
	ConnectAttempts     int
	RequestAttempts     int
	RetryDelay          time.Duration
	RequestTimeout      time.Duration
}

// WithDefaults fills the zero fields with the mock's proven skeleton values.
func (c Config) WithDefaults() Config {
	if c.ConnectAttempts <= 0 {
		c.ConnectAttempts = DefaultConnectAttempts
	}
	if c.RequestAttempts <= 0 {
		c.RequestAttempts = DefaultRequestAttempts
	}
	if c.RetryDelay <= 0 {
		c.RetryDelay = DefaultRetryDelay
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
	return c
}

// Result is the outcome of one handshake + publish action.
type Result struct {
	// BatchID is the batch id the publisher assigned to this action.
	BatchID string
	// BatchSubject is the SM-provided subject the batch was published to.
	BatchSubject string
	// Reply is the consumed availability reply.
	Reply *wire.AvailabilityReply
	// Ack is the consumed batch ack, when one was observed for this batch.
	Ack *wire.ScenarioBatchAck
	// Final reports whether this was the finalization batch.
	Final bool
}

// Publisher drives the handshake + publish flow against one process
// identity, reusing a healthy connection across actions.
type Publisher struct {
	id   wire.Identity
	cfg  Config
	dial Dialer

	mu       sync.Mutex
	conn     Conn
	batchSeq int
}

// New returns a Publisher bound to the identity, the availability subject,
// and the dialer. A missing subject or dialer is a descriptive error (the
// availability subject is operator-injected; the batch subject never is).
func New(id wire.Identity, cfg Config, dial Dialer) (*Publisher, error) {
	if cfg.AvailabilitySubject == "" {
		return nil, errors.New("publisher: AvailabilitySubject is required")
	}
	if dial == nil {
		return nil, errors.New("publisher: nil dialer")
	}
	if id.Project == "" {
		return nil, errors.New("publisher: identity project is empty")
	}
	return &Publisher{id: id, cfg: cfg.WithDefaults(), dial: dial}, nil
}

// NextBatchID returns the next process-scoped batch id (eds-<project>-<n>).
func (p *Publisher) NextBatchID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.batchSeq++
	return fmt.Sprintf("eds-%s-%d", p.id.Project, p.batchSeq)
}

// ensureConn returns the healthy connection, dialing with the bounded-retry
// connect skeleton (60 attempts x 2s delay) when none is open.
func (p *Publisher) ensureConn(ctx context.Context) (Conn, error) {
	p.mu.Lock()
	if p.conn != nil {
		c := p.conn
		p.mu.Unlock()
		return c, nil
	}
	p.mu.Unlock()

	var lastErr error
	for attempt := 1; attempt <= p.cfg.ConnectAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c, err := p.dial(ctx)
		if err == nil {
			p.mu.Lock()
			if p.conn == nil {
				p.conn = c
			}
			p.mu.Unlock()
			return c, nil
		}
		lastErr = err
		if attempt < p.cfg.ConnectAttempts {
			if err := sleepCancellable(ctx, p.cfg.RetryDelay); err != nil {
				return nil, err
			}
		}
	}
	return nil, fmt.Errorf("connect to NATS failed after %d attempts: %w", p.cfg.ConnectAttempts, lastErr)
}

// dropConn closes the current connection (transport loss) so the next action
// re-enters the connect skeleton.
func (p *Publisher) dropConn() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
	}
}

// handshake runs the availability request with the bounded-retry request
// skeleton (60 x 2s, per-request timeout 5s): transient transport/timeout
// failures are retried; a consumed reply is classified once — status "error",
// a ready reply without batch_subject, and a missing/mismatched
// experiment_type echo are hard errors (no retry).
func (p *Publisher) handshake(ctx context.Context, req *wire.AvailabilityRequest) (*wire.AvailabilityReply, string, error) {
	data, err := marshalRequest(req)
	if err != nil {
		return nil, "", err
	}

	var lastErr error
	for attempt := 1; attempt <= p.cfg.RequestAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		conn, err := p.ensureConn(ctx)
		if err != nil {
			return nil, "", err
		}
		replyData, err := conn.Request(ctx, p.cfg.AvailabilitySubject, data, p.cfg.RequestTimeout)
		if err != nil {
			// Timeout or transient transport error: retry with the delay.
			lastErr = err
			p.dropConn()
			if attempt < p.cfg.RequestAttempts {
				if serr := sleepCancellable(ctx, p.cfg.RetryDelay); serr != nil {
					return nil, "", serr
				}
			}
			continue
		}
		reply, derr := wire.DecodeAvailabilityReply(replyData)
		if derr != nil {
			// A consumed-but-malformed reply is a hard protocol error: the
			// SM is reachable and its reply is broken — surfacing beats
			// retrying.
			return nil, "", &ErrProtocol{Reason: "availability reply is malformed: " + derr.Error()}
		}
		subject, reason, ok := wire.ReplyError(reply)
		if !ok {
			if reply.Status == wire.AvailabilityStatusError {
				// The clean SM refusal (the experiment is not InProgress):
				// not retried, the reason is the SM's own wording.
				return nil, "", &ErrUnavailable{Reason: reason}
			}
			return nil, "", &ErrProtocol{Reason: reason}
		}
		return reply, subject, nil
	}
	return nil, "", &ErrProtocol{Reason: fmt.Sprintf("availability request failed after %d attempts: %v", p.cfg.RequestAttempts, lastErr)}
}

// publishBatch runs one full action: handshake, then the JetStream
// PubAck-gated publish of the batch to the reply's batch subject. final=true
// publishes the finalization batch (scenarios [], final true).
func (p *Publisher) publishBatch(ctx context.Context, batchID string, records []wire.ScenarioRecord, final bool) (Result, error) {
	req := &wire.AvailabilityRequest{
		BatchID:        batchID,
		Project:        p.id.Project,
		ScenarioCount:  len(records),
		ExperimentType: wire.ExperimentTypeStandard,
	}
	if err := wire.ValidateAvailabilityRequest(req, p.id); err != nil {
		return Result{}, err
	}
	reply, subject, err := p.handshake(ctx, req)
	if err != nil {
		return Result{}, err
	}

	batch := &wire.ScenarioBatch{
		BatchID:   batchID,
		Project:   p.id.Project,
		Scenarios: records,
		Final:     final,
	}
	// A finalization batch must carry the present empty array.
	if final && len(batch.Scenarios) == 0 {
		batch.Scenarios = []wire.ScenarioRecord{}
	}
	if err := wire.ValidateBatch(batch, p.id); err != nil {
		return Result{}, err
	}
	data, err := marshalBatch(batch)
	if err != nil {
		return Result{}, err
	}

	conn, err := p.ensureConn(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := conn.Publish(ctx, subject, data); err != nil {
		p.dropConn()
		return Result{}, &ErrPublish{BatchID: batchID, Err: err}
	}
	return Result{BatchID: batchID, BatchSubject: subject, Reply: reply, Final: final}, nil
}

// Start publishes one scenario batch containing exactly the given (not
// yet published) scenarios. It refuses an empty delta (the server enforces
// the "no new scenarios to publish" refusal before calling).
func (p *Publisher) Start(ctx context.Context, records []wire.ScenarioRecord) (Result, error) {
	if len(records) == 0 {
		return Result{}, errors.New("start: no new scenarios to publish")
	}
	batchID := p.NextBatchID()
	return p.publishBatch(ctx, batchID, records, false)
}

// Finish publishes the finalization batch (scenarios [], final true) — the
// explicit close of the experiment's scenario lifecycle. Finalization is
// irrevocable; the server enforces the once-only rule.
func (p *Publisher) Finish(ctx context.Context) (Result, error) {
	batchID := p.NextBatchID()
	return p.publishBatch(ctx, batchID, nil, true)
}

// SetAck records a consumed batch ack payload against the batch, so the
// server can surface status + reason in the API response. It is a pure
// decode + validation seam: the landed wire contract defines the ack shape,
// and the Scenario Manager finalization protocol delivers the ack on the
// batch delivery's reply subject when one is present. The reference EDS
// publishes without a reply subject and gates on the PubAck, so the seam
// is currently exercised by the tests only.
func SetAck(r *Result, data []byte) error {
	ack, err := wire.DecodeBatchAck(data)
	if err != nil {
		return err
	}
	if ack.BatchID != "" && ack.BatchID != r.BatchID {
		return fmt.Errorf("batch ack batch_id %q does not match published batch %q (poison)", ack.BatchID, r.BatchID)
	}
	r.Ack = ack
	return nil
}

// marshalJSON encodes a wire payload.
func marshalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// sleepCancellable waits d unless ctx is cancelled.
func sleepCancellable(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// marshalRequest encodes the availability request (all fields always
// present on the wire).
func marshalRequest(req *wire.AvailabilityRequest) ([]byte, error) {
	data, err := marshalJSON(req)
	if err != nil {
		return nil, fmt.Errorf("marshal availability request: %w", err)
	}
	return data, nil
}

// marshalBatch encodes the scenario batch (final absent when false).
func marshalBatch(batch *wire.ScenarioBatch) ([]byte, error) {
	data, err := marshalJSON(batch)
	if err != nil {
		return nil, fmt.Errorf("marshal scenario batch: %w", err)
	}
	return data, nil
}
