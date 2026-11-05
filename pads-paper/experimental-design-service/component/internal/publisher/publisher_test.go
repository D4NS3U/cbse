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

package publisher

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/D4NS3U/cbse/component-templates/experimental-design-service/internal/wire"
)

var testIdentity = wire.Identity{
	ExperimentUID: "aabbccdd-1122-3344-5566-77889900aabb",
	Namespace:     "ns",
	Project:       "exp",
}

const readyReply = `{"status":"ready","batch_subject":"cbse.ns.exp.eds.scenarios","experiment_type":"standard"}`

// fakeConn is the fake-NATS transport the publisher tests drive (the
// sibling modules' fake pattern): scripted request failures, a canned
// reply, and captured publishes.
type fakeConn struct {
	mu            sync.Mutex
	requestFails  int // the first N requests fail (timeout/transient)
	reply         []byte
	publishErr    error
	closed        bool
	publishedData []string
	publishedSubj []string
	requested     [][]byte
}

func (f *fakeConn) Request(ctx context.Context, subject string, data []byte, timeout time.Duration) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requested = append(f.requested, append([]byte(nil), data...))
	if f.requestFails > 0 {
		f.requestFails--
		return nil, errors.New("fake: request timeout")
	}
	return f.reply, nil
}

func (f *fakeConn) Publish(ctx context.Context, subject string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publishErr != nil {
		return f.publishErr
	}
	f.publishedData = append(f.publishedData, string(data))
	f.publishedSubj = append(f.publishedSubj, subject)
	return nil
}

func (f *fakeConn) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}

func (f *fakeConn) lastPublish() (subject, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.publishedData) == 0 {
		return "", ""
	}
	return f.publishedSubj[len(f.publishedSubj)-1], f.publishedData[len(f.publishedData)-1]
}

func (f *fakeConn) lastRequest() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requested) == 0 {
		return ""
	}
	return string(f.requested[len(f.requested)-1])
}

// newTestPublisher wires a Publisher over the fake transport with fast,
// bounded skeleton knobs (3 attempts, 1ms delay, 10ms request timeout).
func newTestPublisher(t *testing.T, conn *fakeConn, dialFails int) *Publisher {
	t.Helper()
	var mu sync.Mutex
	var dials int
	dial := func(ctx context.Context) (Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		dials++
		if dials <= dialFails {
			return nil, errors.New("fake: dial refused")
		}
		return conn, nil
	}
	p, err := New(testIdentity, Config{
		AvailabilitySubject: "cbse.ns.exp.eds.scenarios.available",
		ConnectAttempts:     3,
		RequestAttempts:     3,
		RetryDelay:          time.Millisecond,
		RequestTimeout:      10 * time.Millisecond,
	}, dial)
	if err != nil {
		t.Fatalf("new publisher: %v", err)
	}
	return p
}

func testRecords() []wire.ScenarioRecord {
	return []wire.ScenarioRecord{{
		Priority: 1, NumberOfReps: 20, RecipeInfo: wire.RecipeInfo{ParametersetID: 5}, ConfidenceMetric: 0.91,
	}}
}

// TestNewValidation covers the constructor's fail-fast validation.
func TestNewValidation(t *testing.T) {
	dial := func(ctx context.Context) (Conn, error) { return &fakeConn{reply: []byte(readyReply)}, nil }
	if _, err := New(testIdentity, Config{AvailabilitySubject: ""}, dial); err == nil {
		t.Fatalf("missing subject must fail")
	}
	if _, err := New(testIdentity, Config{AvailabilitySubject: "cbse.ns.exp.eds.scenarios.available"}, nil); err == nil {
		t.Fatalf("nil dialer must fail")
	}
	if _, err := New(wire.Identity{Project: ""}, Config{AvailabilitySubject: "cbse.ns.exp.eds.scenarios.available"}, dial); err == nil {
		t.Fatalf("empty identity project must fail")
	}
}

// TestHandshakeSuccess is the happy path: one availability request, the
// ready reply with the batch subject and the echo, the PubAck-gated
// publish to the reply's batch subject.
func TestHandshakeSuccess(t *testing.T) {
	conn := &fakeConn{reply: []byte(readyReply)}
	p := newTestPublisher(t, conn, 0)
	res, err := p.Start(context.Background(), testRecords())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.BatchSubject != "cbse.ns.exp.eds.scenarios" || res.Reply == nil || res.Reply.Status != "ready" {
		t.Fatalf("result = %+v", res)
	}
	subject, data := conn.lastPublish()
	if subject != "cbse.ns.exp.eds.scenarios" {
		t.Fatalf("published to %q, want the reply's batch subject", subject)
	}
	batch, err := wire.DecodeScenarioBatch([]byte(data))
	if err != nil {
		t.Fatalf("published payload is not a scenario batch: %v", err)
	}
	if batch.Final || len(batch.Scenarios) != 1 || batch.Scenarios[0].RecipeInfo.ParametersetID != 5 {
		t.Fatalf("published batch = %+v", batch)
	}
	if err := wire.ValidateBatch(batch, testIdentity); err != nil {
		t.Fatalf("published batch fails validation: %v", err)
	}
}

// TestRequestRetryOnTimeout covers the request skeleton: a timeout (transient)
// is retried with the delay, and the action succeeds once the SM replies.
func TestRequestRetryOnTimeout(t *testing.T) {
	conn := &fakeConn{reply: []byte(readyReply), requestFails: 2}
	p := newTestPublisher(t, conn, 0)
	if _, err := p.Start(context.Background(), testRecords()); err != nil {
		t.Fatalf("start after retries: %v", err)
	}
	if conn.requestFails != 0 {
		t.Fatalf("expected both scripted timeouts consumed, %d left", conn.requestFails)
	}
	if len(conn.publishedData) != 1 {
		t.Fatalf("exactly one publish expected, got %d", len(conn.publishedData))
	}
}

// TestRequestRetryExhausted covers the request skeleton's bounded end: after
// all attempts the action fails with a hard handshake error naming the
// attempts.
func TestRequestRetryExhausted(t *testing.T) {
	failing := &fakeConn{}
	failing.requestFails = 100
	p := newTestPublisher(t, failing, 0)
	p.cfg.RequestAttempts = 2
	_, err := p.Start(context.Background(), testRecords())
	if err == nil {
		t.Fatalf("exhausted retries must fail")
	}
	var pr *ErrProtocol
	if !errors.As(err, &pr) {
		t.Fatalf("exhausted retries must be a protocol error, got %v", err)
	}
	if !strings.Contains(pr.Reason, "2 attempts") {
		t.Fatalf("reason must name the attempts: %q", pr.Reason)
	}
}

// TestHandshakeHardErrorStatusNotReady is the hard-error test: a status
// "error" reply from the SM is not retried; the reason is surfaced cleanly.
func TestHandshakeHardErrorStatusNotReady(t *testing.T) {
	conn := &fakeConn{reply: []byte(`{"status":"error","reason":"experiment Pending"}`)}
	p := newTestPublisher(t, conn, 0)
	_, err := p.Start(context.Background(), testRecords())
	if err == nil {
		t.Fatalf("status=error must fail")
	}
	var unav *ErrUnavailable
	if !errors.As(err, &unav) {
		t.Fatalf("clean SM refusal expected, got %v", err)
	}
	if unav.Reason != "experiment Pending" {
		t.Fatalf("reason = %q, want the SM's reason verbatim", unav.Reason)
	}
	// No retry: exactly one request was made (the scripted fail-count is
	// untouched, and no publish happened).
	if len(conn.publishedData) != 0 {
		t.Fatalf("no publish may follow a hard handshake error")
	}
}

// TestHandshakeHardErrorMissingBatchSubject is the hard-error test for a
// ready reply without a batch subject.
func TestHandshakeHardErrorMissingBatchSubject(t *testing.T) {
	conn := &fakeConn{reply: []byte(`{"status":"ready","experiment_type":"standard"}`)}
	p := newTestPublisher(t, conn, 0)
	_, err := p.Start(context.Background(), testRecords())
	var pr *ErrProtocol
	if !errors.As(err, &pr) {
		t.Fatalf("protocol error expected, got %v", err)
	}
	if !strings.Contains(pr.Reason, "batch_subject") {
		t.Fatalf("reason must name the missing batch_subject: %q", pr.Reason)
	}
}

// TestHandshakeEchoCheck is the echo-check test: a missing or mismatched
// experiment_type echo on a ready reply is a hard protocol error.
func TestHandshakeEchoCheck(t *testing.T) {
	for name, reply := range map[string]string{
		"missing echo":    `{"status":"ready","batch_subject":"cbse.ns.exp.eds.scenarios"}`,
		"mismatched echo": `{"status":"ready","batch_subject":"cbse.ns.exp.eds.scenarios","experiment_type":"optimization"}`,
	} {
		conn := &fakeConn{reply: []byte(reply)}
		p := newTestPublisher(t, conn, 0)
		_, err := p.Start(context.Background(), testRecords())
		if err == nil {
			t.Fatalf("%s: must fail", name)
		}
		var pr *ErrProtocol
		if !errors.As(err, &pr) {
			t.Fatalf("%s: protocol error expected, got %v", name, err)
		}
		if !strings.Contains(pr.Reason, "experiment_type") {
			t.Fatalf("%s: reason must name the echo: %q", name, pr.Reason)
		}
	}
}

// TestConnectRetrySkeleton covers the connect skeleton: dial failures are
// retried with the delay, and the action succeeds once the server accepts
// the connection.
func TestConnectRetrySkeleton(t *testing.T) {
	conn := &fakeConn{reply: []byte(readyReply)}
	p := newTestPublisher(t, conn, 2)
	if _, err := p.Start(context.Background(), testRecords()); err != nil {
		t.Fatalf("start after connect retries: %v", err)
	}
}

// TestConnectExhausted covers the connect skeleton's bounded end.
func TestConnectExhausted(t *testing.T) {
	conn := &fakeConn{reply: []byte(readyReply)}
	p := newTestPublisher(t, conn, 3) // all 3 dials fail
	_, err := p.Start(context.Background(), testRecords())
	if err == nil {
		t.Fatalf("exhausted connect must fail")
	}
	if !strings.Contains(err.Error(), "3 attempts") {
		t.Fatalf("error must name the attempts: %v", err)
	}
}

// TestPublishPubAckGated covers the PubAck gate: a publish the server does
// not confirm fails the action with an ErrPublish wrapping the transport
// error, after the handshake already succeeded.
func TestPublishPubAckGated(t *testing.T) {
	conn := &fakeConn{reply: []byte(readyReply)}
	conn.publishErr = errors.New("fake: no puback")
	p := newTestPublisher(t, conn, 0)
	_, err := p.Start(context.Background(), testRecords())
	var ep *ErrPublish
	if !errors.As(err, &ep) {
		t.Fatalf("ErrPublish expected, got %v", err)
	}
	if ep.Err == nil || !strings.Contains(ep.Err.Error(), "no puback") {
		t.Fatalf("wrapped transport error expected: %v", ep.Err)
	}
}

// TestFinalizationPublish is the finalization test: Finish publishes the
// finalization batch — scenarios [] present, final true — to the reply's
// batch subject, byte-pinned.
func TestFinalizationPublish(t *testing.T) {
	conn := &fakeConn{reply: []byte(readyReply)}
	p := newTestPublisher(t, conn, 0)
	res, err := p.Finish(context.Background())
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if !res.Final {
		t.Fatalf("result must mark the finalization batch")
	}
	_, data := conn.lastPublish()
	batch, err := wire.DecodeScenarioBatch([]byte(data))
	if err != nil {
		t.Fatalf("published payload is not a scenario batch: %v", err)
	}
	if !batch.Final || batch.Scenarios == nil || len(batch.Scenarios) != 0 {
		t.Fatalf("finalization batch = %+v (want scenarios [] + final true)", batch)
	}
	if !strings.Contains(data, `"scenarios":[]`) || !strings.Contains(data, `"final":true`) {
		t.Fatalf("finalization payload must carry scenarios [] and final true: %s", data)
	}
	if strings.Contains(data, `"project":"exp"`) == false {
		t.Fatalf("finalization payload must carry the identity project: %s", data)
	}
	// The finalization handshake declares scenario_count 0 and the standard
	// experiment type.
	req := conn.lastRequest()
	if !strings.Contains(req, `"scenario_count":0`) || !strings.Contains(req, `"experiment_type":"standard"`) {
		t.Fatalf("finalization availability request = %s (want scenario_count 0, experiment_type standard)", req)
	}
}

// TestStartEmptyDeltaRefused is the empty-delta refusal: a Start with no
// records is refused before any transport work (no dial, no request).
func TestStartEmptyDeltaRefused(t *testing.T) {
	conn := &fakeConn{reply: []byte(readyReply)}
	p := newTestPublisher(t, conn, 0)
	_, err := p.Start(context.Background(), nil)
	if err == nil {
		t.Fatalf("empty delta must be refused")
	}
	if !strings.Contains(err.Error(), "no new scenarios to publish") {
		t.Fatalf("refusal must be descriptive: %v", err)
	}
	if len(conn.publishedData) != 0 {
		t.Fatalf("no publish may follow an empty-delta refusal")
	}
}

// TestConnectionReuseAndReconnect covers the connection lifecycle: the
// healthy connection is reused across actions (one dial), and a transport
// loss on publish drops it so the next action re-enters the connect
// skeleton (a second dial).
func TestConnectionReuseAndReconnect(t *testing.T) {
	conn := &fakeConn{reply: []byte(readyReply)}
	p := newTestPublisher(t, conn, 0)
	if _, err := p.Start(context.Background(), testRecords()); err != nil {
		t.Fatalf("first start: %v", err)
	}
	// A healthy second action reuses the connection (no new dial).
	if _, err := p.Start(context.Background(), testRecords()); err != nil {
		t.Fatalf("second start: %v", err)
	}
	// A lost connection (publish without PubAck) is dropped.
	conn.publishErr = errors.New("fake: conn loss")
	if _, err := p.Start(context.Background(), testRecords()); err == nil {
		t.Fatalf("publish loss must fail")
	}
	if !conn.closed {
		t.Fatalf("the lost connection must be closed")
	}
	// The next action dials again.
	conn.publishErr = nil
	dial := func(ctx context.Context) (Conn, error) { return conn, nil }
	p.dial = dial
	if _, err := p.Start(context.Background(), testRecords()); err != nil {
		t.Fatalf("reconnect start: %v", err)
	}
	if len(conn.publishedData) != 3 {
		t.Fatalf("three successful publishes expected, got %d", len(conn.publishedData))
	}
}

// TestSetAckPoison covers the ack seam: an ack payload whose batch_id does
// not match the published batch is poison; a matching ack is recorded.
func TestSetAckPoison(t *testing.T) {
	res := Result{BatchID: "eds-exp-1"}
	if err := SetAck(&res, []byte(`{"status":"inserted","batch_id":"eds-exp-1","received":1,"inserted":1,"failed":0}`)); err != nil {
		t.Fatalf("matching ack must be recorded: %v", err)
	}
	if res.Ack == nil || res.Ack.Status != wire.BatchAckStatusInserted {
		t.Fatalf("ack not recorded: %+v", res.Ack)
	}
	if err := SetAck(&res, []byte(`{"status":"inserted","batch_id":"other-batch"}`)); err == nil {
		t.Fatalf("mismatched ack batch_id must be poison")
	}
}

// TestStartRejectsInvalidRecords is the wire-level guard inside the action:
// out-of-domain scenario records never leave the process.
func TestStartRejectsInvalidRecords(t *testing.T) {
	conn := &fakeConn{reply: []byte(readyReply)}
	p := newTestPublisher(t, conn, 0)
	bad := testRecords()
	bad[0].NumberOfReps = wire.MaxReps + 1
	if _, err := p.Start(context.Background(), bad); err == nil {
		t.Fatalf("out-of-range reps must be refused before publication")
	}
	if len(conn.publishedData) != 0 {
		t.Fatalf("no publish may follow a wire refusal")
	}
}
