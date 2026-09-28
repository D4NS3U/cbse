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

package messaging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"

	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/evaluation"
	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/resultdb"
	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/wire"
)

const (
	testUID  = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	testNS   = "default"
	testProj = "proj"
)

func testIdentity() wire.Identity {
	return wire.Identity{ExperimentUID: testUID, Namespace: testNS, Project: testProj}
}

func validRequestJSON(t *testing.T, scenarioID, round, reps int, eps float64) []byte {
	t.Helper()
	return []byte(fmt.Sprintf(
		`{"experiment_uid":"%s","namespace":"%s","project":"%s","scenario_id":%d,"runner_round":%d,"number_of_reps":%d,"confidence_metric":%v}`,
		testUID, testNS, testProj, scenarioID, round, reps, eps,
	))
}

// metObservations returns the 41 pooled observations of the hand-computed
// met vector (twenty -0.5, twenty 0.5, one 0.0): mean 0, s 0.5,
// h = t(0.975,40)*0.5/sqrt(41) = 0.157821 <= 0.5.
func metObservations() []float64 {
	obs := make([]float64, 41)
	for i := 0; i < 20; i++ {
		obs[i] = -0.5
		obs[i+20] = 0.5
	}
	return obs
}

// --- fakes -----------------------------------------------------------------

// fakeManager is a Manager stub with a per-call error sequence.
type fakeManager struct {
	mu    sync.Mutex
	calls int
	errs  []error // errs[i] is the error of call i+1; nil entries succeed
}

func (m *fakeManager) EnsureConsumer() error {
	m.mu.Lock()
	i := m.calls
	m.calls++
	m.mu.Unlock()
	if i < len(m.errs) {
		return m.errs[i]
	}
	return nil
}

// fakeMessage is a Message stub recording acknowledgements.
type fakeMessage struct {
	data []byte
	subj string

	mu                 sync.Mutex
	ackCount, nakCount int
	inProgress         int
}

func (m *fakeMessage) Data() []byte    { return m.data }
func (m *fakeMessage) Subject() string { return m.subj }

func (m *fakeMessage) Ack() error {
	m.mu.Lock()
	m.ackCount++
	m.mu.Unlock()
	return nil
}

func (m *fakeMessage) Nak() error {
	m.mu.Lock()
	m.nakCount++
	m.mu.Unlock()
	return nil
}

func (m *fakeMessage) InProgress() error {
	m.mu.Lock()
	m.inProgress++
	m.mu.Unlock()
	return nil
}

func (m *fakeMessage) acks() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ackCount
}

func (m *fakeMessage) naks() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nakCount
}

func (m *fakeMessage) inprog() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inProgress
}

// fakePublisher records published verdicts.
type fakePublisher struct {
	mu        sync.Mutex
	published []publishedMsg
	err       error
}

type publishedMsg struct {
	subject string
	data    []byte
}

func (p *fakePublisher) Publish(subject string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.published = append(p.published, publishedMsg{subject: subject, data: append([]byte(nil), data...)})
	return nil
}

func (p *fakePublisher) all() []publishedMsg {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]publishedMsg, len(p.published))
	copy(out, p.published)
	return out
}

// fakeConsumer serves queued messages, then blocks until ctx is done.
type fakeConsumer struct {
	mu      sync.Mutex
	msgs    []Message
	fetched int
}

func (c *fakeConsumer) Fetch(ctx context.Context) (Message, error) {
	for {
		c.mu.Lock()
		var msg Message
		if len(c.msgs) > 0 {
			msg = c.msgs[0]
			c.msgs = c.msgs[1:]
			c.fetched++
		}
		c.mu.Unlock()
		if msg != nil {
			return msg, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
}

// fakeResultConn serves the JSONB rows the fake connector built from its
// configured FetchResult.
type fakeResultConn struct {
	rows [][]byte
}

func (c *fakeResultConn) Exec(context.Context, string, ...any) error { return nil }

func (c *fakeResultConn) Query(context.Context, string, ...any) (resultdb.Rows, error) {
	return &fakeResultRows{rows: c.rows}, nil
}

func (c *fakeResultConn) Close(context.Context) error { return nil }

type fakeResultRows struct {
	rows [][]byte
	idx  int
}

func (r *fakeResultRows) Next() bool {
	if r.idx < len(r.rows) {
		r.idx++
		return true
	}
	return false
}

func (r *fakeResultRows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.rows) {
		return errors.New("scan past end")
	}
	for _, d := range dest {
		if p, ok := d.(*[]byte); ok {
			*p = r.rows[r.idx-1]
		} else {
			return errors.New("unsupported scan dest")
		}
	}
	return nil
}

func (r *fakeResultRows) Close() error { return nil }
func (r *fakeResultRows) Err() error   { return nil }

// fakeConnector is a resultdb.Connector stub. Connect records the call,
// applies an optional delay (for the in-progress ack test), and serves JSONB
// rows that re-extract exactly the configured FetchResult (one
// {"mean_wait_time": v} row per value, plus one empty-object row per
// malformed count).
type fakeConnector struct {
	mu    sync.Mutex
	calls int
	res   resultdb.FetchResult
	err   error
	delay time.Duration
}

func (c *fakeConnector) Connect(ctx context.Context, _ string, _ int32, _, _, _ string) (resultdb.Conn, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	rows := make([][]byte, 0, len(c.res.Values)+c.res.Malformed)
	for _, v := range c.res.Values {
		rows = append(rows, []byte(fmt.Sprintf(`{"mean_wait_time":%v}`, v)))
	}
	for i := 0; i < c.res.Malformed; i++ {
		rows = append(rows, []byte(`{}`))
	}
	return &fakeResultConn{rows: rows}, nil
}

func (c *fakeConnector) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// --- consumer config / comparison tests -------------------------------------

func TestConsumerName(t *testing.T) {
	if got := ConsumerName(testUID); got != "pps-a1b2c3d4e5f6" {
		t.Fatalf("consumer name = %q, want pps-a1b2c3d4e5f6", got)
	}
	if got := UIDPrefix("A1B2C3D4-E5F6-7890-ABCD-EF1234567890"); got != "a1b2c3d4e5f6" {
		t.Fatalf("uid prefix = %q, want a1b2c3d4e5f6 (lowercased)", got)
	}
}

func TestConsumerConfig(t *testing.T) {
	cfg := ConsumerConfig(testUID, testNS, testProj, "cbse.default.proj.pps.request")
	if cfg.Durable != "pps-a1b2c3d4e5f6" {
		t.Fatalf("durable = %q", cfg.Durable)
	}
	if cfg.FilterSubject != "cbse.default.proj.pps.request" {
		t.Fatalf("filter = %q", cfg.FilterSubject)
	}
	if cfg.AckPolicy != nats.AckExplicitPolicy {
		t.Fatalf("ack policy = %v", cfg.AckPolicy)
	}
	if cfg.DeliverPolicy != nats.DeliverAllPolicy {
		t.Fatalf("deliver policy = %v", cfg.DeliverPolicy)
	}
	if cfg.AckWait != 2*time.Minute {
		t.Fatalf("ack wait = %v", cfg.AckWait)
	}
	if cfg.MaxAckPending != 1 {
		t.Fatalf("max ack pending = %d", cfg.MaxAckPending)
	}
	if cfg.MaxDeliver != -1 {
		t.Fatalf("max deliver = %d", cfg.MaxDeliver)
	}
	wantMeta := map[string]string{
		"experiment.cbse.terministic.de/managed-by":     "pps",
		"experiment.cbse.terministic.de/experiment-uid": testUID,
		"experiment.cbse.terministic.de/namespace":      testNS,
		"experiment.cbse.terministic.de/project":        testProj,
	}
	for k, v := range wantMeta {
		if cfg.Metadata[k] != v {
			t.Fatalf("metadata %s = %q, want %q", k, cfg.Metadata[k], v)
		}
	}
}

func TestCompareConsumerMatch(t *testing.T) {
	want := ConsumerConfig(testUID, testNS, testProj, "cbse.default.proj.pps.request")
	got := &nats.ConsumerInfo{Config: *want}
	if err := CompareConsumer(got, want); err != nil {
		t.Fatalf("matching consumer rejected: %v", err)
	}
}

func TestCompareConsumerMismatches(t *testing.T) {
	want := ConsumerConfig(testUID, testNS, testProj, "cbse.default.proj.pps.request")
	cases := []struct {
		name string
		mut  func(*nats.ConsumerConfig)
	}{
		{"durable", func(c *nats.ConsumerConfig) { c.Durable = "other" }},
		{"filter", func(c *nats.ConsumerConfig) { c.FilterSubject = "other" }},
		{"ackpolicy", func(c *nats.ConsumerConfig) { c.AckPolicy = nats.AckNonePolicy }},
		{"deliverpolicy", func(c *nats.ConsumerConfig) { c.DeliverPolicy = nats.DeliverLastPolicy }},
		{"ackwait", func(c *nats.ConsumerConfig) { c.AckWait = time.Second }},
		{"maxackpending", func(c *nats.ConsumerConfig) { c.MaxAckPending = 2 }},
		{"maxdeliver", func(c *nats.ConsumerConfig) { c.MaxDeliver = 5 }},
		{"metadata", func(c *nats.ConsumerConfig) {
			c.Metadata = map[string]string{"experiment.cbse.terministic.de/project": "other"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := *want
			tc.mut(&g)
			got := &nats.ConsumerInfo{Config: g}
			err := CompareConsumer(got, want)
			if err == nil {
				t.Fatalf("mismatch %s must be rejected", tc.name)
			}
			if !errors.Is(err, ErrOwnershipCollision) {
				t.Fatalf("mismatch %s err = %v, want ErrOwnershipCollision", tc.name, err)
			}
		})
	}
}

// --- processor protocol tests ------------------------------------------------

// procDeps builds processor deps with fakes for handle-level tests.
func procDeps(t *testing.T, policy evaluation.Policy, conn *fakeConnector, pub *fakePublisher) Deps {
	t.Helper()
	return Deps{
		Identity:           testIdentity(),
		EvaluationTemplate: "cbse.default.proj.pps.%s.evaluation",
		ResultDB:           resultdb.DatabaseConfig{Host: "db", Port: 5432, DBName: "results", User: "u", Password: "p"},
		Params: evaluation.Params{
			Policy:                         policy,
			DeterministicAdditionalRunners: 2,
			MaxReplications:                10000,
			MaxRunnersPerRound:             1000,
		},
		Connector: conn,
		Consumer:  &fakeConsumer{},
		Publisher: pub,
		Manager:   &fakeManager{},
		Logger:    log.New(io.Discard, "", 0),
	}
}

func TestProcessStatisticalMet(t *testing.T) {
	conn := &fakeConnector{res: resultdb.FetchResult{Values: metObservations(), Rows: 41}}
	pub := &fakePublisher{}
	p := New(procDeps(t, evaluation.PolicyStatistical, conn, pub))

	msg := &fakeMessage{data: validRequestJSON(t, 42, 1, 41, 0.5), subj: "cbse.default.proj.pps.request"}
	if err := p.handle(context.Background(), msg); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if msg.acks() != 1 || msg.naks() != 0 {
		t.Fatalf("ack/nak = %d/%d, want 1/0", msg.acks(), msg.naks())
	}
	published := pub.all()
	if len(published) != 1 {
		t.Fatalf("published %d verdicts, want 1", len(published))
	}
	if published[0].subject != "cbse.default.proj.pps.42.evaluation" {
		t.Fatalf("subject = %q, want cbse.default.proj.pps.42.evaluation", published[0].subject)
	}
	v, err := wire.DecodeVerdict(published[0].data)
	if err != nil {
		t.Fatalf("decode published verdict: %v", err)
	}
	if v.Verdict != wire.VerdictMet || v.AdditionalRunners != 0 || v.Replications != 41 {
		t.Fatalf("verdict = %q additional=%d reps=%d, want met/0/41", v.Verdict, v.AdditionalRunners, v.Replications)
	}
	if v.ScenarioID != 42 || v.RunnerRound != 1 || v.Metric != wire.MetricMeanWaitTime {
		t.Fatalf("identity fields = scenario %d round %d metric %q", v.ScenarioID, v.RunnerRound, v.Metric)
	}
	if v.ConfidenceMetric != 0.5 || v.MaxReplications != 10000 {
		t.Fatalf("bounds = %v/%d, want 0.5/10000", v.ConfidenceMetric, v.MaxReplications)
	}
	if v.ExperimentUID != testUID || v.Namespace != testNS || v.Project != testProj {
		t.Fatalf("payload identity = %q/%q/%q", v.ExperimentUID, v.Namespace, v.Project)
	}
	if v.SampleMean != 0 {
		t.Fatalf("sample mean = %v, want 0", v.SampleMean)
	}
	if v.HalfWidth < 0.1578 || v.HalfWidth > 0.1579 {
		t.Fatalf("half width = %v, want ~0.157821", v.HalfWidth)
	}
}

func TestProcessPoisonDecode(t *testing.T) {
	conn := &fakeConnector{}
	pub := &fakePublisher{}
	p := New(procDeps(t, evaluation.PolicyStatistical, conn, pub))

	msg := &fakeMessage{data: []byte(`{not json`)}
	if err := p.handle(context.Background(), msg); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if msg.acks() != 1 || msg.naks() != 0 {
		t.Fatalf("ack/nak = %d/%d, want 1/0 (raw poison ACKed)", msg.acks(), msg.naks())
	}
	if len(pub.all()) != 0 {
		t.Fatalf("poison produced %d publications, want 0", len(pub.all()))
	}
	if conn.callCount() != 0 {
		t.Fatalf("poison queried the Result DB %d times", conn.callCount())
	}
}

func TestProcessPoisonIdentityMismatch(t *testing.T) {
	conn := &fakeConnector{}
	pub := &fakePublisher{}
	p := New(procDeps(t, evaluation.PolicyStatistical, conn, pub))

	data := []byte(fmt.Sprintf(
		`{"experiment_uid":"00000000-1111-2222-3333-444444444444","namespace":"%s","project":"%s","scenario_id":42,"runner_round":1,"number_of_reps":40,"confidence_metric":0.5}`,
		testNS, testProj))
	msg := &fakeMessage{data: data}
	if err := p.handle(context.Background(), msg); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if msg.acks() != 1 || msg.naks() != 0 {
		t.Fatalf("ack/nak = %d/%d, want 1/0 (identity poison ACKed)", msg.acks(), msg.naks())
	}
	if len(pub.all()) != 0 || conn.callCount() != 0 {
		t.Fatalf("identity poison: published=%d dbCalls=%d, want 0/0", len(pub.all()), conn.callCount())
	}
}

func TestProcessPoisonNonPositiveScenarioID(t *testing.T) {
	conn := &fakeConnector{}
	pub := &fakePublisher{}
	p := New(procDeps(t, evaluation.PolicyStatistical, conn, pub))
	for _, id := range []int64{0, -1} {
		msg := &fakeMessage{data: validRequestJSON(t, int(id), 1, 40, 0.5)}
		if err := p.handle(context.Background(), msg); err != nil {
			t.Fatalf("handle: %v", err)
		}
		if msg.acks() != 1 {
			t.Fatalf("scenario %d: acks = %d, want 1 (poison ACKed)", id, msg.acks())
		}
	}
	if conn.callCount() != 0 || len(pub.all()) != 0 {
		t.Fatalf("poison side effects: db=%d pub=%d, want 0/0", conn.callCount(), len(pub.all()))
	}
}

func TestProcessDBFailureLeavesUnacked(t *testing.T) {
	conn := &fakeConnector{err: errors.New("connection refused")}
	pub := &fakePublisher{}
	p := New(procDeps(t, evaluation.PolicyStatistical, conn, pub))

	msg := &fakeMessage{data: validRequestJSON(t, 42, 1, 41, 0.5)}
	err := p.handle(context.Background(), msg)
	if err == nil {
		t.Fatal("DB failure must return an error (NAK path)")
	}
	if msg.acks() != 0 || msg.naks() != 0 {
		t.Fatalf("ack/nak = %d/%d, want 0/0 (NAK performed by the Run loop)", msg.acks(), msg.naks())
	}
	if len(pub.all()) != 0 {
		t.Fatalf("DB failure produced %d publications, want 0", len(pub.all()))
	}
}

func TestProcessPublishFailureLeavesUnacked(t *testing.T) {
	conn := &fakeConnector{res: resultdb.FetchResult{Values: metObservations(), Rows: 41}}
	pub := &fakePublisher{err: errors.New("puback timeout")}
	p := New(procDeps(t, evaluation.PolicyStatistical, conn, pub))

	msg := &fakeMessage{data: validRequestJSON(t, 42, 1, 41, 0.5)}
	err := p.handle(context.Background(), msg)
	if err == nil {
		t.Fatal("publish failure must return an error (NAK path)")
	}
	if msg.acks() != 0 {
		t.Fatalf("acks = %d, want 0 (verdict not confirmed)", msg.acks())
	}
}

func TestProcessDeterministicSkipsResultDB(t *testing.T) {
	conn := &fakeConnector{}
	pub := &fakePublisher{}
	p := New(procDeps(t, evaluation.PolicyDeterministicFirstRoundNotMet, conn, pub))

	msg := &fakeMessage{data: validRequestJSON(t, 42, 1, 40, 0.5)}
	if err := p.handle(context.Background(), msg); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if conn.callCount() != 0 {
		t.Fatalf("deterministic policy queried the Result DB %d times, want 0", conn.callCount())
	}
	published := pub.all()
	if len(published) != 1 {
		t.Fatalf("published %d verdicts, want 1", len(published))
	}
	v, err := wire.DecodeVerdict(published[0].data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v.Verdict != wire.VerdictAdditionalRunners || v.AdditionalRunners != 2 {
		t.Fatalf("round 1 = %q/%d, want additional_runners/2", v.Verdict, v.AdditionalRunners)
	}
	if v.Replications != 40 || v.SampleMean != 0 || v.HalfWidth != 0 {
		t.Fatalf("round 1 echoes = reps %d mean %v hw %v, want 40/0/0", v.Replications, v.SampleMean, v.HalfWidth)
	}

	// Round 2 answers met.
	pub2 := &fakePublisher{}
	p2 := New(procDeps(t, evaluation.PolicyDeterministicFirstRoundNotMet, conn, pub2))
	msg2 := &fakeMessage{data: validRequestJSON(t, 42, 2, 42, 0.5)}
	if err := p2.handle(context.Background(), msg2); err != nil {
		t.Fatalf("handle round 2: %v", err)
	}
	v2, err := wire.DecodeVerdict(pub2.all()[0].data)
	if err != nil {
		t.Fatalf("decode round 2: %v", err)
	}
	if v2.Verdict != wire.VerdictMet || v2.AdditionalRunners != 0 || v2.Replications != 42 {
		t.Fatalf("round 2 = %q/%d/%d, want met/0/42", v2.Verdict, v2.AdditionalRunners, v2.Replications)
	}
}

func TestProcessMalformedRowsLogged(t *testing.T) {
	var buf bytes.Buffer
	conn := &fakeConnector{res: resultdb.FetchResult{
		Values:    []float64{5},
		Malformed: 3,
		Rows:      4,
	}}
	pub := &fakePublisher{}
	deps := procDeps(t, evaluation.PolicyStatistical, conn, pub)
	deps.Logger = log.New(&buf, "", 0)
	p := New(deps)

	msg := &fakeMessage{data: validRequestJSON(t, 42, 1, 4, 0.5)}
	if err := p.handle(context.Background(), msg); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if got := buf.String(); !bytes.Contains([]byte(got), []byte("3 malformed")) {
		t.Fatalf("log %q missing the malformed-row count", got)
	}
	v, err := wire.DecodeVerdict(pub.all()[0].data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// n = 1 (only the finite value): not-met with the minimum batch 2.
	if v.Verdict != wire.VerdictAdditionalRunners || v.AdditionalRunners != 2 || v.Replications != 1 {
		t.Fatalf("verdict = %q/%d/%d, want additional_runners/2/1", v.Verdict, v.AdditionalRunners, v.Replications)
	}
}

// --- Run loop tests -----------------------------------------------------------

func TestRunBindRetryThenProcess(t *testing.T) {
	conn := &fakeConnector{res: resultdb.FetchResult{Values: metObservations(), Rows: 41}}
	pub := &fakePublisher{}
	deps := procDeps(t, evaluation.PolicyStatistical, conn, pub)
	cons := &fakeConsumer{msgs: []Message{&fakeMessage{data: validRequestJSON(t, 42, 1, 41, 0.5)}}}
	deps.Consumer = cons
	deps.Manager = &fakeManager{errs: []error{ErrConsumerNotReady, ErrConsumerNotReady, nil}}
	deps.BindRetryInterval = time.Millisecond
	p := New(deps)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	// Wait for the verdict, then shut down.
	deadline := time.After(5 * time.Second)
	for {
		if len(pub.all()) == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("verdict not published within 5s")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if v, derr := wire.DecodeVerdict(pub.all()[0].data); derr != nil {
		t.Fatalf("decode: %v", derr)
	} else if v.Verdict != wire.VerdictMet {
		t.Fatalf("verdict = %q, want met", v.Verdict)
	}
}

func TestRunBindCollisionFails(t *testing.T) {
	conn := &fakeConnector{}
	pub := &fakePublisher{}
	deps := procDeps(t, evaluation.PolicyStatistical, conn, pub)
	deps.Manager = &fakeManager{errs: []error{fmt.Errorf("%w: durable %q != %q", ErrOwnershipCollision, "other", "pps-a1b2c3d4e5f6")}}
	p := New(deps)

	err := p.Run(context.Background())
	if err == nil {
		t.Fatal("ownership collision must fail startup")
	}
	if !errors.Is(err, ErrOwnershipCollision) {
		t.Fatalf("Run = %v, want ErrOwnershipCollision", err)
	}
	if conn.callCount() != 0 || len(pub.all()) != 0 {
		t.Fatalf("collision side effects: db=%d pub=%d, want 0/0", conn.callCount(), len(pub.all()))
	}
}

func TestRunNAKsOnDBFailure(t *testing.T) {
	conn := &fakeConnector{err: errors.New("connection refused")}
	pub := &fakePublisher{}
	deps := procDeps(t, evaluation.PolicyStatistical, conn, pub)
	msg := &fakeMessage{data: validRequestJSON(t, 42, 1, 41, 0.5)}
	deps.Consumer = &fakeConsumer{msgs: []Message{msg}}
	p := New(deps)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	deadline := time.After(5 * time.Second)
	for {
		if msg.naks() >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("request not NAKed within 5s")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if len(pub.all()) != 0 {
		t.Fatalf("DB failure produced %d publications, want 0", len(pub.all()))
	}
}

func TestRunInProgressDuringLongQuery(t *testing.T) {
	// The fake connector is replaced by a query-delay stub so the 20ms
	// in-progress cadence fires at least once during the fetch.
	conn := &fakeConnector{
		res:   resultdb.FetchResult{Values: metObservations(), Rows: 41},
		delay: 60 * time.Millisecond,
	}
	pub := &fakePublisher{}
	deps := procDeps(t, evaluation.PolicyStatistical, conn, pub)
	msg := &fakeMessage{data: validRequestJSON(t, 42, 1, 41, 0.5)}
	deps.Consumer = &fakeConsumer{msgs: []Message{msg}}
	deps.InProgressInterval = 20 * time.Millisecond
	p := New(deps)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	deadline := time.After(5 * time.Second)
	for {
		if len(pub.all()) == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("verdict not published within 5s")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if msg.inprog() < 1 {
		t.Fatalf("in-progress acks = %d, want >= 1 during the long query", msg.inprog())
	}
}
