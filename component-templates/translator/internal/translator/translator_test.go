package translator_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/D4NS3U/cbse/component-templates/translator/internal/buildkit"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/config"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/dbconfig"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/generator"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/imageref"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/messaging"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/registry"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/subject"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/translator"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/workspace"
)

// --- fakes ---

type fakeMessage struct {
	data      []byte
	subj      string
	ackErr    error
	inProgErr error
	acks      int
	inProgs   int
}

func (m *fakeMessage) Data() []byte      { return m.data }
func (m *fakeMessage) Subject() string   { return m.subj }
func (m *fakeMessage) Ack() error        { m.acks++; return m.ackErr }
func (m *fakeMessage) Nak() error        { return nil }
func (m *fakeMessage) InProgress() error { m.inProgs++; return m.inProgErr }

type fakeConsumer struct {
	msgs chan messaging.Message
}

func (c *fakeConsumer) Fetch(ctx context.Context) (messaging.Message, error) {
	select {
	case m := <-c.msgs:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type pubRecord struct {
	subject string
	data    []byte
}

type fakePublisher struct {
	mu   sync.Mutex
	pubs []pubRecord
	err  error
}

func (p *fakePublisher) Publish(s string, d []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pubs = append(p.pubs, pubRecord{s, d})
	return p.err
}

func (p *fakePublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pubs)
}

func (p *fakePublisher) records() []pubRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]pubRecord, len(p.pubs))
	copy(out, p.pubs)
	return out
}

type fakeManager struct {
	calls int
	err   error
}

func (m *fakeManager) EnsureConsumer() error { m.calls++; return m.err }

type fakeGenerator struct {
	calls int
	err   error
	genFn func(context.Context, generator.GenerationInput) error
}

func (g *fakeGenerator) Generate(ctx context.Context, in generator.GenerationInput) error {
	g.calls++
	if g.genFn != nil {
		return g.genFn(ctx, in)
	}
	return g.err
}

type fakeRegistry struct {
	calls  int
	verify func(ctx context.Context, tag, repo, uid string, sid, attempt int) (string, error)
}

func (r *fakeRegistry) VerifyAndResolve(ctx context.Context, tag, repo, uid string, sid, attempt int) (string, error) {
	r.calls++
	return r.verify(ctx, tag, repo, uid, sid, attempt)
}

// --- helpers ---

func encodeReq(t *testing.T, id, attempt int, recipe json.RawMessage) []byte {
	t.Helper()
	type reqJSON struct {
		ID                 int             `json:"id"`
		TranslationAttempt int             `json:"translation_attempt"`
		RecipeInfo         json.RawMessage `json:"recipe_info"`
		ConfidenceMetric   *float64        `json:"confidence_metric"`
	}
	cm := 1.0
	b, err := json.Marshal(reqJSON{id, attempt, recipe, &cm})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const (
	testRepo = "registry.example.com/test"
	testUID  = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	testNS   = "ns"
	testProj = "proj"
)

func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Repository:           testRepo,
		BaseImage:            "registry.example.com/runner-base:latest",
		ExperimentUID:        testUID,
		Namespace:            testNS,
		Project:              testProj,
		RequestSubject:       "cbse.ns.proj.trans.request",
		ReadySubjectTemplate: "cbse.{namespace}.{project}.trans.{scenario_id}.ready",
		Stream:               "TRANSLATOR",
		Consumer:             "translator-a1b2c3d4e5f6",
		NATSURL:              "nats://localhost:4222",
		DetailDB:             dbconfig.DatabaseConfig{Host: "h", Port: 5432, DBName: "d", User: "u", Password: "p"},
		ResultDB:             dbconfig.DatabaseConfig{Host: "h", Port: 5432, DBName: "d", User: "u", Password: "p"},
	}
}

type env struct {
	tr       *translator.Translator
	ws       *workspace.Workspace
	root     string
	consumer *fakeConsumer
	pub      *fakePublisher
	mgr      *fakeManager
	gen      *fakeGenerator
	reg      *fakeRegistry
	solve    buildkit.SolveFunc
	list     buildkit.ListWorkersFunc
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	ws := workspace.New(root)
	cfg := newTestConfig(t)
	cons := &fakeConsumer{msgs: make(chan messaging.Message, 8)}
	pub := &fakePublisher{}
	mgr := &fakeManager{}
	gen := &fakeGenerator{}
	// Default registry: tag not found (permits generation).
	reg := &fakeRegistry{verify: func(ctx context.Context, tag, repo, uid string, sid, attempt int) (string, error) {
		return "", registry.ErrNotFound
	}}
	solve := func(ctx context.Context, opt buildkit.SolveOptions, status chan<- buildkit.Status) (string, error) {
		return "sha256:solved-digest", nil
	}
	list := func(ctx context.Context) (int, error) { return 1, nil }
	tr := translator.New(translator.Deps{
		Config: cfg, Workspace: ws, Generator: gen, Registry: reg,
		Solve: solve, ListWorkers: list, Consumer: cons, Publisher: pub, Manager: mgr,
		InProgressInterval: 5 * time.Millisecond,
	})
	return &env{tr: tr, ws: ws, root: root, consumer: cons, pub: pub, mgr: mgr, gen: gen, reg: reg, solve: solve, list: list}
}

// runOne runs the translator until it has handled one message, then cancels.
func runOne(t *testing.T, e *env, msg messaging.Message) {
	t.Helper()
	e.consumer.msgs <- msg
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := e.tr.Run(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
}

func readySubject(id int) string {
	return subject.ReadySubject(testNS, testProj, id)
}

func attemptDir(root string, id, attempt int) string {
	return filepath.Join(root, fmt.Sprintf("scenario-%d", id), fmt.Sprintf("attempt-%d", attempt))
}

func decodeReady(t *testing.T, data []byte) (int, string) {
	t.Helper()
	var p struct {
		TranslationAttempt int    `json:"translation_attempt"`
		ContainerImage     string `json:"container_image"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	return p.TranslationAttempt, p.ContainerImage
}

// twoStepVerify returns ErrNotFound on the first call and digest on the second
// (tag recovery miss, then post-push verification).
func twoStepVerify(digest string) func(context.Context, string, string, string, int, int) (string, error) {
	first := true
	return func(ctx context.Context, tag, repo, uid string, sid, attempt int) (string, error) {
		if first {
			first = false
			return "", registry.ErrNotFound
		}
		return digest, nil
	}
}

// dirExists reports whether the attempt directory still exists.
func dirExists(root string, id, attempt int) bool {
	_, err := os.Stat(attemptDir(root, id, attempt))
	return err == nil
}

// --- tests: raw poison ---

func TestRawPoisonDecodeFailureAcksWithoutReady(t *testing.T) {
	e := newEnv(t)
	msg := &fakeMessage{data: []byte("not json")}
	runOne(t, e, msg)
	if msg.acks != 1 {
		t.Fatalf("acks = %d, want 1", msg.acks)
	}
	if e.pub.count() != 0 {
		t.Fatal("poison must not publish ready")
	}
	if dirExists(e.root, 42, 1) {
		t.Fatal("poison must not leave an attempt dir")
	}
}

func TestRawPoisonNonPositiveID(t *testing.T) {
	for _, id := range []int{0, -1} {
		t.Run(fmt.Sprintf("id-%d", id), func(t *testing.T) {
			e := newEnv(t)
			msg := &fakeMessage{data: encodeReq(t, id, 1, json.RawMessage(`{"parameterset_id":1}`))}
			runOne(t, e, msg)
			if msg.acks != 1 {
				t.Fatalf("acks = %d, want 1", msg.acks)
			}
			if e.pub.count() != 0 {
				t.Fatal("non-positive id poison must not publish ready")
			}
		})
	}
}

// --- tests: retained marker reuse ---

func TestRetainedSuccessMarkerReusesDigestReady(t *testing.T) {
	e := newEnv(t)
	dir := attemptDir(e.root, 7, 2)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:abc123"
	marker := workspace.Marker{
		Outcome: workspace.OutcomeSuccess, ScenarioID: 7, Attempt: 2,
		ReadySubject: readySubject(7), ExperimentUID: testUID,
		Tag: imageref.Tag(testRepo, imageref.UIDPrefix(testUID), 7, 2), Digest: digest,
	}
	if err := workspace.WriteMarker(dir, marker); err != nil {
		t.Fatal(err)
	}
	msg := &fakeMessage{data: encodeReq(t, 7, 2, json.RawMessage(`{"parameterset_id":1}`))}
	runOne(t, e, msg)
	if msg.acks != 1 {
		t.Fatalf("acks = %d", msg.acks)
	}
	if e.gen.calls != 0 || e.reg.calls != 0 {
		t.Fatal("must not generate or resolve on retained marker")
	}
	if e.pub.count() != 1 {
		t.Fatal("must republish ready")
	}
	_, img := decodeReady(t, e.pub.records()[0].data)
	if img != imageref.DigestRef(testRepo, digest) {
		t.Fatalf("ready image = %q, want digest ref", img)
	}
	if dirExists(e.root, 7, 2) {
		t.Fatal("attempt dir must be removed after terminal ack")
	}
}

func TestRetainedEmptyFailureMarkerReusesEmptyReady(t *testing.T) {
	e := newEnv(t)
	dir := attemptDir(e.root, 7, 2)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := workspace.Marker{
		Outcome: workspace.OutcomeEmptyFailure, ScenarioID: 7, Attempt: 2,
		ReadySubject: readySubject(7), ExperimentUID: testUID, EmptyImage: true, FailureClass: "generator",
	}
	if err := workspace.WriteMarker(dir, marker); err != nil {
		t.Fatal(err)
	}
	msg := &fakeMessage{data: encodeReq(t, 7, 2, json.RawMessage(`{"parameterset_id":1}`))}
	runOne(t, e, msg)
	if msg.acks != 1 {
		t.Fatalf("acks = %d", msg.acks)
	}
	if e.gen.calls != 0 || e.reg.calls != 0 {
		t.Fatal("must not generate or resolve on retained marker")
	}
	if e.pub.count() != 1 {
		t.Fatal("must republish empty ready")
	}
	_, img := decodeReady(t, e.pub.records()[0].data)
	if img != "" {
		t.Fatalf("ready image = %q, want empty", img)
	}
	if dirExists(e.root, 7, 2) {
		t.Fatal("attempt dir must be removed after terminal ack")
	}
}

func TestRetainedInvalidMarkerDiscardedThenTagRecovery(t *testing.T) {
	e := newEnv(t)
	dir := attemptDir(e.root, 7, 2)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := workspace.Marker{
		Outcome: workspace.OutcomeSuccess, ScenarioID: 7, Attempt: 2,
		ReadySubject: readySubject(7), ExperimentUID: "wrong-uid",
		Tag: "t", Digest: "sha256:x",
	}
	if err := workspace.WriteMarker(dir, bad); err != nil {
		t.Fatal(err)
	}
	e.reg.verify = func(ctx context.Context, tag, repo, uid string, sid, attempt int) (string, error) {
		return "sha256:recovered", nil
	}
	msg := &fakeMessage{data: encodeReq(t, 7, 2, json.RawMessage(`{"parameterset_id":1}`))}
	runOne(t, e, msg)
	if msg.acks != 1 {
		t.Fatalf("acks = %d", msg.acks)
	}
	if e.gen.calls != 0 {
		t.Fatal("must not generate when tag recovers")
	}
	if e.pub.count() != 1 {
		t.Fatal("must publish ready")
	}
	_, img := decodeReady(t, e.pub.records()[0].data)
	if img != imageref.DigestRef(testRepo, "sha256:recovered") {
		t.Fatalf("ready image = %q", img)
	}
}

// --- tests: tag recovery ---

func TestTagRecoverySuccess(t *testing.T) {
	e := newEnv(t)
	e.reg.verify = func(ctx context.Context, tag, repo, uid string, sid, attempt int) (string, error) {
		return "sha256:tagged", nil
	}
	msg := &fakeMessage{data: encodeReq(t, 9, 1, json.RawMessage(`{"parameterset_id":1}`))}
	runOne(t, e, msg)
	if msg.acks != 1 {
		t.Fatalf("acks = %d", msg.acks)
	}
	if e.gen.calls != 0 {
		t.Fatal("must not generate on tag recovery")
	}
	_, img := decodeReady(t, e.pub.records()[0].data)
	if img != imageref.DigestRef(testRepo, "sha256:tagged") {
		t.Fatalf("ready image = %q", img)
	}
	if dirExists(e.root, 9, 1) {
		t.Fatal("attempt dir must be removed after terminal ack")
	}
}

func TestTagRecoveryResolutionErrorEmptyFailure(t *testing.T) {
	e := newEnv(t)
	e.reg.verify = func(ctx context.Context, tag, repo, uid string, sid, attempt int) (string, error) {
		return "", errors.New("registry timeout")
	}
	msg := &fakeMessage{data: encodeReq(t, 9, 1, json.RawMessage(`{"parameterset_id":1}`))}
	runOne(t, e, msg)
	if msg.acks != 1 {
		t.Fatalf("acks = %d", msg.acks)
	}
	if e.gen.calls != 0 {
		t.Fatal("must not generate on resolution error")
	}
	if e.pub.count() != 1 {
		t.Fatal("must publish empty ready")
	}
	_, img := decodeReady(t, e.pub.records()[0].data)
	if img != "" {
		t.Fatalf("ready image = %q, want empty", img)
	}
	if dirExists(e.root, 9, 1) {
		t.Fatal("attempt dir must be removed after terminal ack")
	}
}

// --- tests: full generate+build+resolve ---

func TestFullGenerateBuildResolveSuccess(t *testing.T) {
	e := newEnv(t)
	e.reg.verify = twoStepVerify("sha256:verified")
	e.gen.genFn = func(ctx context.Context, in generator.GenerationInput) error {
		return os.WriteFile(filepath.Join(in.Workspace, "Dockerfile"), []byte("FROM base\n"), 0o644)
	}
	msg := &fakeMessage{data: encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`))}
	runOne(t, e, msg)
	if msg.acks != 1 {
		t.Fatalf("acks = %d", msg.acks)
	}
	if e.gen.calls != 1 {
		t.Fatal("must generate")
	}
	if e.reg.calls != 2 {
		t.Fatalf("registry calls = %d, want 2", e.reg.calls)
	}
	if e.pub.count() != 1 {
		t.Fatal("must publish digest ready")
	}
	_, img := decodeReady(t, e.pub.records()[0].data)
	if img != imageref.DigestRef(testRepo, "sha256:verified") {
		t.Fatalf("ready image = %q", img)
	}
	if dirExists(e.root, 11, 3) {
		t.Fatal("attempt dir must be removed after terminal ack")
	}
}

func TestGeneratorFailureEmptyFailure(t *testing.T) {
	e := newEnv(t)
	e.gen.err = errors.New("lookup failed")
	msg := &fakeMessage{data: encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`))}
	runOne(t, e, msg)
	if msg.acks != 1 {
		t.Fatalf("acks = %d", msg.acks)
	}
	if e.pub.count() != 1 {
		t.Fatal("must publish empty ready")
	}
	_, img := decodeReady(t, e.pub.records()[0].data)
	if img != "" {
		t.Fatalf("ready image = %q, want empty", img)
	}
	if dirExists(e.root, 11, 3) {
		t.Fatal("attempt dir must be removed after terminal ack")
	}
}

func TestBuildFailureEmptyFailure(t *testing.T) {
	e := newEnv(t)
	e.solve = func(ctx context.Context, opt buildkit.SolveOptions, status chan<- buildkit.Status) (string, error) {
		return "", errors.New("buildkit solve failed")
	}
	e.gen.genFn = func(ctx context.Context, in generator.GenerationInput) error { return nil }
	e.tr = translator.New(translator.Deps{
		Config: newTestConfig(t), Workspace: e.ws, Generator: e.gen, Registry: e.reg,
		Solve: e.solve, ListWorkers: e.list, Consumer: e.consumer, Publisher: e.pub, Manager: e.mgr,
		InProgressInterval: 5 * time.Millisecond,
	})
	msg := &fakeMessage{data: encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`))}
	runOne(t, e, msg)
	if msg.acks != 1 {
		t.Fatalf("acks = %d", msg.acks)
	}
	_, img := decodeReady(t, e.pub.records()[0].data)
	if img != "" {
		t.Fatalf("ready image = %q, want empty", img)
	}
	if dirExists(e.root, 11, 3) {
		t.Fatal("attempt dir must be removed after terminal ack")
	}
}

func TestPostPushDigestResolutionFailureEmptyFailure(t *testing.T) {
	e := newEnv(t)
	e.reg.verify = func(ctx context.Context, tag, repo, uid string, sid, attempt int) (string, error) {
		// First call (tag recovery) not found; second (post-push) resolution error.
		if e.reg.calls == 1 {
			return "", registry.ErrNotFound
		}
		return "", errors.New("post-push resolution failed")
	}
	e.gen.genFn = func(ctx context.Context, in generator.GenerationInput) error { return nil }
	msg := &fakeMessage{data: encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`))}
	runOne(t, e, msg)
	if msg.acks != 1 {
		t.Fatalf("acks = %d", msg.acks)
	}
	_, img := decodeReady(t, e.pub.records()[0].data)
	if img != "" {
		t.Fatalf("ready image = %q, want empty", img)
	}
	if dirExists(e.root, 11, 3) {
		t.Fatal("attempt dir must be removed after terminal ack")
	}
}

// --- tests: leaves-unacked (marker persisted) ---

func TestReadyPublishFailureSuccessLeavesUnacked(t *testing.T) {
	e := newEnv(t)
	e.pub.err = errors.New("nats down")
	e.reg.verify = twoStepVerify("sha256:verified")
	e.gen.genFn = func(ctx context.Context, in generator.GenerationInput) error { return nil }
	msg := &fakeMessage{data: encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`))}
	runOne(t, e, msg)
	if msg.acks != 0 {
		t.Fatalf("acks = %d, want 0 (unacked)", msg.acks)
	}
	if !dirExists(e.root, 11, 3) {
		t.Fatal("attempt dir must be retained when unacked")
	}
	m, ok, _ := workspace.ReadMarker(attemptDir(e.root, 11, 3))
	if !ok || m.Outcome != workspace.OutcomeSuccess || m.Digest != "sha256:verified" {
		t.Fatalf("success marker must be retained: %+v", m)
	}
}

func TestFinalAckFailureSuccessLeavesUnacked(t *testing.T) {
	e := newEnv(t)
	e.reg.verify = twoStepVerify("sha256:verified")
	e.gen.genFn = func(ctx context.Context, in generator.GenerationInput) error { return nil }
	msg := &fakeMessage{
		data:   encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`)),
		ackErr: errors.New("ack failed"),
	}
	runOne(t, e, msg)
	if msg.acks != 1 {
		t.Fatalf("acks = %d, want 1 attempt", msg.acks)
	}
	if e.pub.count() != 1 {
		t.Fatal("must have published ready")
	}
	if !dirExists(e.root, 11, 3) {
		t.Fatal("attempt dir must be retained when unacked")
	}
	m, ok, _ := workspace.ReadMarker(attemptDir(e.root, 11, 3))
	if !ok || m.Outcome != workspace.OutcomeSuccess || m.Digest != "sha256:verified" {
		t.Fatalf("success marker must be retained: %+v", m)
	}
}

func TestReadyPublishFailureEmptyOutcomeLeavesUnacked(t *testing.T) {
	e := newEnv(t)
	e.pub.err = errors.New("nats down")
	e.gen.err = errors.New("lookup failed") // empty-failure path
	msg := &fakeMessage{data: encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`))}
	runOne(t, e, msg)
	if msg.acks != 0 {
		t.Fatalf("acks = %d, want 0 (unacked)", msg.acks)
	}
	m, ok, _ := workspace.ReadMarker(attemptDir(e.root, 11, 3))
	if !ok || m.Outcome != workspace.OutcomeEmptyFailure || m.FailureClass != "generator" {
		t.Fatalf("empty-failure marker must be retained: %+v", m)
	}
}

func TestRedeliveryReusesSuccessMarkerNoRepeatedCalls(t *testing.T) {
	e := newEnv(t)
	e.reg.verify = twoStepVerify("sha256:verified")
	e.gen.genFn = func(ctx context.Context, in generator.GenerationInput) error { return nil }
	// First delivery: publish fails, leaving a success marker unacked.
	e.pub.err = errors.New("nats down")
	msg1 := &fakeMessage{data: encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`))}
	runOne(t, e, msg1)
	if e.gen.calls != 1 || e.reg.calls != 2 {
		t.Fatalf("first delivery: gen=%d reg=%d", e.gen.calls, e.reg.calls)
	}
	// Second delivery (redelivery): reuse marker, no repeated work.
	e.pub.err = nil
	msg2 := &fakeMessage{data: encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`))}
	runOne(t, e, msg2)
	if msg2.acks != 1 {
		t.Fatalf("acks = %d, want 1", msg2.acks)
	}
	if e.gen.calls != 1 || e.reg.calls != 2 {
		t.Fatalf("redelivery must not repeat work: gen=%d reg=%d", e.gen.calls, e.reg.calls)
	}
	if e.pub.count() != 2 {
		t.Fatalf("must republish ready (pubs=%d)", e.pub.count())
	}
	_, img := decodeReady(t, e.pub.records()[1].data)
	if img != imageref.DigestRef(testRepo, "sha256:verified") {
		t.Fatalf("redelivery ready image = %q", img)
	}
	if dirExists(e.root, 11, 3) {
		t.Fatal("attempt dir must be removed after terminal ack")
	}
}

// --- tests: in-progress acks ---

func TestInProgressAcksDuringLongOp(t *testing.T) {
	e := newEnv(t)
	e.reg.verify = twoStepVerify("sha256:verified")
	started := make(chan struct{})
	done := make(chan struct{})
	e.gen.genFn = func(ctx context.Context, in generator.GenerationInput) error {
		close(started)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return nil
		}
	}
	e.tr = translator.New(translator.Deps{
		Config: newTestConfig(t), Workspace: e.ws, Generator: e.gen, Registry: e.reg,
		Solve: e.solve, ListWorkers: e.list, Consumer: e.consumer, Publisher: e.pub, Manager: e.mgr,
		InProgressInterval: 2 * time.Millisecond,
	})
	msg := &fakeMessage{data: encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`))}
	e.consumer.msgs <- msg
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- e.tr.Run(ctx) }()
	<-started
	time.Sleep(20 * time.Millisecond)
	close(done)
	if err := <-runErr; err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
	if msg.inProgs < 3 {
		t.Fatalf("in-progress acks = %d, want >=3", msg.inProgs)
	}
	if msg.acks != 1 {
		t.Fatalf("acks = %d, want 1 after op completes", msg.acks)
	}
}

func TestInProgressAckFailureCancelsOpLeavesUnacked(t *testing.T) {
	e := newEnv(t)
	e.gen.genFn = func(ctx context.Context, in generator.GenerationInput) error {
		<-ctx.Done()
		return ctx.Err()
	}
	e.tr = translator.New(translator.Deps{
		Config: newTestConfig(t), Workspace: e.ws, Generator: e.gen, Registry: e.reg,
		Solve: e.solve, ListWorkers: e.list, Consumer: e.consumer, Publisher: e.pub, Manager: e.mgr,
		InProgressInterval: 2 * time.Millisecond,
	})
	msg := &fakeMessage{
		data:      encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`)),
		inProgErr: errors.New("progress ack failed"),
	}
	runOne(t, e, msg)
	if msg.acks != 0 {
		t.Fatalf("acks = %d, want 0 (unacked)", msg.acks)
	}
	if _, ok, _ := workspace.ReadMarker(attemptDir(e.root, 11, 3)); ok {
		t.Fatal("must not write marker on cancellation")
	}
	if e.pub.count() != 0 {
		t.Fatal("must not publish ready on cancellation")
	}
}

// --- tests: build input recreation ---

func TestBuildInputRecreatedBeforeGenerate(t *testing.T) {
	e := newEnv(t)
	staleDir := attemptDir(e.root, 11, 3)
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	staleFile := filepath.Join(staleDir, "Dockerfile")
	if err := os.WriteFile(staleFile, []byte("STALE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var observed string
	e.gen.genFn = func(ctx context.Context, in generator.GenerationInput) error {
		observed = in.Workspace
		// Write a fresh Dockerfile to prove generation ran after removal.
		return os.WriteFile(filepath.Join(in.Workspace, "Dockerfile"), []byte("FROM base\n"), 0o644)
	}
	e.reg.verify = twoStepVerify("sha256:verified")
	msg := &fakeMessage{data: encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`))}
	runOne(t, e, msg)
	if observed == "" {
		t.Fatal("generate not called")
	}
	if msg.acks != 1 {
		t.Fatalf("acks = %d", msg.acks)
	}
}

// --- tests: admission gate ---

func TestAdmissionGateZeroWorkersNoConsumer(t *testing.T) {
	e := newEnv(t)
	e.list = func(ctx context.Context) (int, error) { return 0, nil }
	e.tr = translator.New(translator.Deps{
		Config: newTestConfig(t), Workspace: e.ws, Generator: e.gen, Registry: e.reg,
		Solve: e.solve, ListWorkers: e.list, Consumer: e.consumer, Publisher: e.pub, Manager: e.mgr,
		InProgressInterval: 5 * time.Millisecond,
	})
	e.consumer.msgs <- &fakeMessage{data: encodeReq(t, 1, 1, json.RawMessage(`{"parameterset_id":1}`))}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := e.tr.Run(ctx)
	if err == nil {
		t.Fatal("Run must block at admission gate and return ctx error")
	}
	if e.mgr.calls != 0 {
		t.Fatal("must not create consumer before admission gate opens")
	}
	if e.pub.count() != 0 {
		t.Fatal("must not publish before admission gate opens")
	}
}

func TestEnsureConsumerFailureStopsRun(t *testing.T) {
	e := newEnv(t)
	e.mgr.err = errors.New("ownership collision")
	e.tr = translator.New(translator.Deps{
		Config: newTestConfig(t), Workspace: e.ws, Generator: e.gen, Registry: e.reg,
		Solve: e.solve, ListWorkers: e.list, Consumer: e.consumer, Publisher: e.pub, Manager: e.mgr,
		InProgressInterval: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := e.tr.Run(ctx); err == nil {
		t.Fatal("Run must fail when consumer cannot be ensured")
	}
	if e.pub.count() != 0 {
		t.Fatal("must not publish on consumer failure")
	}
}

// --- tests: tag-collision (mismatched / unannotated tag) ---

func TestTagRecoveryMismatchedAnnotationEmptyFailure(t *testing.T) {
	e := newEnv(t)
	// An existing tag whose identity annotations mismatch the request is a
	// permanent collision: registry returns a non-ErrNotFound verification error.
	e.reg.verify = func(ctx context.Context, tag, repo, uid string, sid, attempt int) (string, error) {
		return "", errors.New("registry: annotation experiment-uid=other != current")
	}
	msg := &fakeMessage{data: encodeReq(t, 9, 1, json.RawMessage(`{"parameterset_id":1}`))}
	runOne(t, e, msg)
	if msg.acks != 1 {
		t.Fatalf("acks = %d", msg.acks)
	}
	if e.gen.calls != 0 {
		t.Fatal("must not generate on a tag collision")
	}
	if e.pub.count() != 1 {
		t.Fatal("must publish empty ready")
	}
	if _, img := decodeReady(t, e.pub.records()[0].data); img != "" {
		t.Fatalf("ready image = %q, want empty", img)
	}
}

func TestTagRecoveryUnannotatedTagEmptyFailure(t *testing.T) {
	e := newEnv(t)
	e.reg.verify = func(ctx context.Context, tag, repo, uid string, sid, attempt int) (string, error) {
		return "", errors.New("registry: missing manifest annotation experiment-uid")
	}
	msg := &fakeMessage{data: encodeReq(t, 9, 1, json.RawMessage(`{"parameterset_id":1}`))}
	runOne(t, e, msg)
	if e.gen.calls != 0 {
		t.Fatal("must not rebuild or overwrite an unannotated tag")
	}
	if e.reg.calls != 1 {
		t.Fatalf("registry calls = %d, want 1 (resolve only)", e.reg.calls)
	}
}

// --- tests: empty-outcome ack-fail + redelivery reuse ---

func TestEmptyOutcomeAckFailureLeavesUnacked(t *testing.T) {
	e := newEnv(t)
	e.gen.err = errors.New("lookup failed") // empty-failure path
	msg := &fakeMessage{
		data:   encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`)),
		ackErr: errors.New("ack failed"),
	}
	runOne(t, e, msg)
	if msg.acks != 1 {
		t.Fatalf("acks = %d, want 1 attempt", msg.acks)
	}
	if e.pub.count() != 1 {
		t.Fatal("must have published empty ready")
	}
	if !dirExists(e.root, 11, 3) {
		t.Fatal("attempt dir must be retained when unacked")
	}
	m, ok, _ := workspace.ReadMarker(attemptDir(e.root, 11, 3))
	if !ok || m.Outcome != workspace.OutcomeEmptyFailure || m.FailureClass != "generator" {
		t.Fatalf("empty-failure marker must be retained: %+v", m)
	}
}

func TestRedeliveryReusesEmptyMarkerNoRepeatedCalls(t *testing.T) {
	e := newEnv(t)
	e.gen.err = errors.New("lookup failed")
	msg1 := &fakeMessage{
		data:   encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`)),
		ackErr: errors.New("ack failed"),
	}
	runOne(t, e, msg1)
	if e.gen.calls != 1 {
		t.Fatalf("first delivery gen calls = %d", e.gen.calls)
	}
	// Redelivery: reuse the empty-failure marker, no repeated generate.
	msg2 := &fakeMessage{data: encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`))}
	runOne(t, e, msg2)
	if msg2.acks != 1 {
		t.Fatalf("acks = %d, want 1", msg2.acks)
	}
	if e.gen.calls != 1 {
		t.Fatalf("redelivery must not repeat generate: gen=%d", e.gen.calls)
	}
	if e.pub.count() != 2 {
		t.Fatalf("must republish empty ready (pubs=%d)", e.pub.count())
	}
	if _, img := decodeReady(t, e.pub.records()[1].data); img != "" {
		t.Fatalf("redelivery ready image = %q, want empty", img)
	}
	if dirExists(e.root, 11, 3) {
		t.Fatal("attempt dir must be removed after terminal ack")
	}
}

// --- tests: shutdown cancellation ---

func TestShutdownCancellationLeavesUnackedNoEmptyOutcome(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	e.gen.genFn = func(ctx context.Context, in generator.GenerationInput) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}
	msg := &fakeMessage{data: encodeReq(t, 11, 3, json.RawMessage(`{"parameterset_id":5}`))}
	e.consumer.msgs <- msg
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- e.tr.Run(ctx) }()
	cancel() // shutdown mid-operation
	if err := <-runErr; err == nil {
		t.Fatal("Run must return on shutdown")
	}
	close(release)
	if msg.acks != 0 {
		t.Fatalf("acks = %d, want 0 (unacked)", msg.acks)
	}
	if e.pub.count() != 0 {
		t.Fatal("must not publish on shutdown cancellation")
	}
	if _, ok, _ := workspace.ReadMarker(attemptDir(e.root, 11, 3)); ok {
		t.Fatal("must not write a marker on shutdown cancellation")
	}
}
