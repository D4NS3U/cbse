package messaging

import (
	"errors"
	"fmt"
	"testing"

	natsgo "github.com/nats-io/nats.go"
)

// fakeJS is an in-memory JetStream manager that mirrors the subset of
// natsgo.JetStreamContext used by stream and consumer reconciliation. It lets
// unit tests exercise the create/append/update/delete-recreate decision logic
// without a live NATS broker.
type fakeJS struct {
	streams   map[string]*natsgo.StreamInfo
	consumers map[string]map[string]*natsgo.ConsumerInfo // stream -> durable -> info
	updateErr error                                      // if set, UpdateConsumer returns this
	addErr    error                                      // if set, AddConsumer returns this
}

func newFakeJS() *fakeJS {
	return &fakeJS{
		streams:   make(map[string]*natsgo.StreamInfo),
		consumers: make(map[string]map[string]*natsgo.ConsumerInfo),
	}
}

func (f *fakeJS) StreamInfo(stream string, _ ...natsgo.JSOpt) (*natsgo.StreamInfo, error) {
	s, ok := f.streams[stream]
	if !ok {
		return nil, natsgo.ErrStreamNotFound
	}
	return s, nil
}

func (f *fakeJS) AddStream(cfg *natsgo.StreamConfig, _ ...natsgo.JSOpt) (*natsgo.StreamInfo, error) {
	if _, ok := f.streams[cfg.Name]; ok {
		return nil, fmt.Errorf("stream %q already exists", cfg.Name)
	}
	f.streams[cfg.Name] = &natsgo.StreamInfo{Config: *cfg}
	return f.streams[cfg.Name], nil
}

func (f *fakeJS) UpdateStream(cfg *natsgo.StreamConfig, _ ...natsgo.JSOpt) (*natsgo.StreamInfo, error) {
	if _, ok := f.streams[cfg.Name]; !ok {
		return nil, natsgo.ErrStreamNotFound
	}
	f.streams[cfg.Name] = &natsgo.StreamInfo{Config: *cfg}
	return f.streams[cfg.Name], nil
}

func (f *fakeJS) ConsumerInfo(stream, name string, _ ...natsgo.JSOpt) (*natsgo.ConsumerInfo, error) {
	durables, ok := f.consumers[stream]
	if !ok {
		return nil, natsgo.ErrConsumerNotFound
	}
	info, ok := durables[name]
	if !ok {
		return nil, natsgo.ErrConsumerNotFound
	}
	return info, nil
}

func (f *fakeJS) AddConsumer(stream string, cfg *natsgo.ConsumerConfig, _ ...natsgo.JSOpt) (*natsgo.ConsumerInfo, error) {
	if f.addErr != nil {
		return nil, f.addErr
	}
	if _, ok := f.consumers[stream]; !ok {
		f.consumers[stream] = make(map[string]*natsgo.ConsumerInfo)
	}
	if _, exists := f.consumers[stream][cfg.Durable]; exists {
		return nil, fmt.Errorf("consumer %q already exists", cfg.Durable)
	}
	info := &natsgo.ConsumerInfo{Stream: stream, Name: cfg.Durable, Config: *cfg}
	f.consumers[stream][cfg.Durable] = info
	return info, nil
}

func (f *fakeJS) UpdateConsumer(stream string, cfg *natsgo.ConsumerConfig, _ ...natsgo.JSOpt) (*natsgo.ConsumerInfo, error) {
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	durables, ok := f.consumers[stream]
	if !ok {
		return nil, natsgo.ErrConsumerNotFound
	}
	info, ok := durables[cfg.Durable]
	if !ok {
		return nil, natsgo.ErrConsumerNotFound
	}
	info.Config = *cfg
	return info, nil
}

func (f *fakeJS) DeleteConsumer(stream, consumer string, _ ...natsgo.JSOpt) error {
	durables, ok := f.consumers[stream]
	if !ok {
		return natsgo.ErrConsumerNotFound
	}
	if _, ok := durables[consumer]; !ok {
		return natsgo.ErrConsumerNotFound
	}
	delete(durables, consumer)
	return nil
}

func TestUnionSubjects(t *testing.T) {
	merged, added := unionSubjects([]string{"a", "b"}, []string{"b", "c"})
	if !added || len(merged) != 3 || merged[2] != "c" {
		t.Fatalf("union = %v added=%v", merged, added)
	}
	if _, added := unionSubjects([]string{"a", "b"}, []string{"a", "b"}); added {
		t.Fatal("expected no add when all present")
	}
}

func TestReconcileStreamCreatesMissing(t *testing.T) {
	f := newFakeJS()
	if err := ReconcileStream(f, EDSStreamConfig()); err != nil {
		t.Fatal(err)
	}
	info, err := f.StreamInfo(EDSStreamName)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Config.Subjects) != 1 || info.Config.Subjects[0] != "cbse.*.*.eds.scenarios" {
		t.Fatalf("created subjects = %v", info.Config.Subjects)
	}
	if info.Config.Retention != natsgo.WorkQueuePolicy {
		t.Fatalf("created retention = %v", info.Config.Retention)
	}
}

func TestReconcileStreamAppendsRequiredSubjects(t *testing.T) {
	f := newFakeJS()
	// Pre-existing translator stream missing the ready subject, with an
	// unrelated subject and non-canonical (retained) storage fields.
	f.streams[TranslatorStreamName] = &natsgo.StreamInfo{Config: natsgo.StreamConfig{
		Name:     TranslatorStreamName,
		Subjects: []string{"cbse.*.*.trans.request", "unrelated.subject"},
		MaxAge:   999, // an unlisted field that reconciliation must retain
	}}
	if err := ReconcileStream(f, TranslatorStreamConfig()); err != nil {
		t.Fatal(err)
	}
	info, _ := f.StreamInfo(TranslatorStreamName)
	// ready subject appended; unrelated subject preserved; MaxAge retained.
	got := info.Config.Subjects
	if len(got) != 3 {
		t.Fatalf("subjects = %v", got)
	}
	if got[0] != "cbse.*.*.trans.request" || got[1] != "unrelated.subject" || got[2] != "cbse.*.*.trans.*.ready" {
		t.Fatalf("subjects order/preserve = %v", got)
	}
	if info.Config.MaxAge != 999 {
		t.Fatalf("unlisted field not retained: MaxAge = %v", info.Config.MaxAge)
	}
}

func TestReconcileStreamNoopWhenComplete(t *testing.T) {
	f := newFakeJS()
	f.streams[EDSStreamName] = &natsgo.StreamInfo{Config: *EDSStreamConfig()}
	before := f.streams[EDSStreamName]
	if err := ReconcileStream(f, EDSStreamConfig()); err != nil {
		t.Fatal(err)
	}
	// No update should have occurred; pointer identity preserved.
	if f.streams[EDSStreamName] != before {
		t.Fatal("reconcile mutated an already-complete stream")
	}
}

func TestReconcileSMConsumerCreatesMissing(t *testing.T) {
	f := newFakeJS()
	if err := ReconcileSMConsumer(f, EDSStreamName, EDSConsumerConfig()); err != nil {
		t.Fatal(err)
	}
	info, err := f.ConsumerInfo(EDSStreamName, EDSConsumerName)
	if err != nil {
		t.Fatal(err)
	}
	if info.Config.FilterSubject != "cbse.*.*.eds.scenarios" {
		t.Fatalf("filter = %q", info.Config.FilterSubject)
	}
}

func TestReconcileSMConsumerMatchesExisting(t *testing.T) {
	f := newFakeJS()
	f.AddConsumer(EDSStreamName, EDSConsumerConfig())
	if err := ReconcileSMConsumer(f, EDSStreamName, EDSConsumerConfig()); err != nil {
		t.Fatalf("matching consumer should be success: %v", err)
	}
}

func TestReconcileSMConsumerUpdatesMutableSetting(t *testing.T) {
	f := newFakeJS()
	// Existing consumer with a wrong MaxAckPending but correct durable/filter.
	existing := *EDSConsumerConfig()
	existing.MaxAckPending = 5
	f.AddConsumer(EDSStreamName, &existing)
	if err := ReconcileSMConsumer(f, EDSStreamName, EDSConsumerConfig()); err != nil {
		t.Fatal(err)
	}
	info, _ := f.ConsumerInfo(EDSStreamName, EDSConsumerName)
	if info.Config.MaxAckPending != 1024 {
		t.Fatalf("MaxAckPending = %d; want 1024 after update", info.Config.MaxAckPending)
	}
}

func TestReconcileSMConsumerDeleteRecreateOnImmutableMismatch(t *testing.T) {
	f := newFakeJS()
	// Existing consumer with a wrong filter subject (an incompatible setting
	// the server cannot update in place on this branch).
	existing := *EDSConsumerConfig()
	existing.FilterSubject = "cbse.legacy.eds.scenarios"
	f.AddConsumer(EDSStreamName, &existing)
	// Simulate a server that cannot update the filter in place.
	f.updateErr = errors.New("filter subject cannot be updated in place")
	if err := ReconcileSMConsumer(f, EDSStreamName, EDSConsumerConfig()); err != nil {
		t.Fatal(err)
	}
	info, _ := f.ConsumerInfo(EDSStreamName, EDSConsumerName)
	if info.Config.FilterSubject != "cbse.*.*.eds.scenarios" {
		t.Fatalf("filter after recreate = %q; want canonical", info.Config.FilterSubject)
	}
	if info.Config.MaxAckPending != 1024 {
		t.Fatalf("MaxAckPending after recreate = %d; want 1024", info.Config.MaxAckPending)
	}
}

func TestReconcileStreamsAndConsumers(t *testing.T) {
	f := newFakeJS()
	if err := ReconcileStreamsAndConsumers(f); err != nil {
		t.Fatal(err)
	}
	for _, c := range SMConsumers() {
		if _, err := f.ConsumerInfo(c.Stream, c.Config.Durable); err != nil {
			t.Fatalf("consumer %q/%q missing after reconcile: %v", c.Stream, c.Config.Durable, err)
		}
	}
	if _, err := f.StreamInfo(EDSStreamName); err != nil {
		t.Fatalf("eds stream missing: %v", err)
	}
	if _, err := f.StreamInfo(TranslatorStreamName); err != nil {
		t.Fatalf("translator stream missing: %v", err)
	}
}

func TestVerifyTranslatorConsumerOwnership(t *testing.T) {
	uid := "A1B2C3D4-E5F6-7890-ABCD-EF1234567890"
	cfg := TranslatorConsumerConfig(uid, "default", "smoke")
	info := &natsgo.ConsumerInfo{Stream: TranslatorStreamName, Name: cfg.Durable, Config: *cfg}
	if err := VerifyTranslatorConsumerOwnership(info, TranslatorStreamName, uid, "default", "smoke"); err != nil {
		t.Fatalf("matching: %v", err)
	}

	// Wrong namespace: collision.
	if err := VerifyTranslatorConsumerOwnership(info, TranslatorStreamName, uid, "other", "smoke"); !errors.Is(err, ErrOwnershipCollision) {
		t.Fatalf("wrong namespace: err = %v; want ErrOwnershipCollision", err)
	}
	// Wrong project: collision.
	if err := VerifyTranslatorConsumerOwnership(info, TranslatorStreamName, uid, "default", "other"); !errors.Is(err, ErrOwnershipCollision) {
		t.Fatalf("wrong project: err = %v; want ErrOwnershipCollision", err)
	}
	// Wrong UID: durable name prefix differs -> collision.
	if err := VerifyTranslatorConsumerOwnership(info, TranslatorStreamName, "deadbeef-dead-beef-dead-beefdeadbeef", "default", "smoke"); !errors.Is(err, ErrOwnershipCollision) {
		t.Fatalf("wrong uid: err = %v; want ErrOwnershipCollision", err)
	}
	// Tampered metadata: collision.
	tampered := *cfg
	tampered.Metadata = map[string]string{
		"experiment.cbse.terministic.de/managed-by":     "translator",
		"experiment.cbse.terministic.de/experiment-uid": "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF",
		"experiment.cbse.terministic.de/namespace":      "default",
		"experiment.cbse.terministic.de/project":        "smoke",
	}
	tamperedInfo := &natsgo.ConsumerInfo{Stream: TranslatorStreamName, Name: tampered.Durable, Config: tampered}
	if err := VerifyTranslatorConsumerOwnership(tamperedInfo, TranslatorStreamName, uid, "default", "smoke"); !errors.Is(err, ErrOwnershipCollision) {
		t.Fatalf("tampered metadata: err = %v; want ErrOwnershipCollision", err)
	}
	// Tampered filter: collision.
	badFilter := *cfg
	badFilter.FilterSubject = "cbse.default.other.trans.request"
	badFilterInfo := &natsgo.ConsumerInfo{Stream: TranslatorStreamName, Name: badFilter.Durable, Config: badFilter}
	if err := VerifyTranslatorConsumerOwnership(badFilterInfo, TranslatorStreamName, uid, "default", "smoke"); !errors.Is(err, ErrOwnershipCollision) {
		t.Fatalf("tampered filter: err = %v; want ErrOwnershipCollision", err)
	}
}

func TestDeleteTranslatorConsumer(t *testing.T) {
	uid := "A1B2C3D4-E5F6-7890-ABCD-EF1234567890"
	f := newFakeJS()
	f.AddConsumer(TranslatorStreamName, TranslatorConsumerConfig(uid, "default", "smoke"))
	if err := DeleteTranslatorConsumer(f, TranslatorStreamName, uid, "default", "smoke"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ConsumerInfo(TranslatorStreamName, TranslatorConsumerName(uid)); !errors.Is(err, natsgo.ErrConsumerNotFound) {
		t.Fatalf("consumer still present: %v", err)
	}
	// Missing consumer is success.
	if err := DeleteTranslatorConsumer(f, TranslatorStreamName, uid, "default", "smoke"); err != nil {
		t.Fatalf("missing consumer: %v", err)
	}
	// Ownership collision: not deleted.
	f.AddConsumer(TranslatorStreamName, TranslatorConsumerConfig(uid, "default", "smoke"))
	if err := DeleteTranslatorConsumer(f, TranslatorStreamName, uid, "other", "smoke"); !errors.Is(err, ErrOwnershipCollision) {
		t.Fatalf("collision: err = %v; want ErrOwnershipCollision", err)
	}
	if _, err := f.ConsumerInfo(TranslatorStreamName, TranslatorConsumerName(uid)); err != nil {
		t.Fatalf("collided consumer should remain: %v", err)
	}
}
