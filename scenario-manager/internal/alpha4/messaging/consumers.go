package messaging

import (
	"errors"
	"fmt"

	natsgo "github.com/nats-io/nats.go"
)

// streamManager is the subset of natsgo.JetStreamContext used for stream
// reconciliation. It is satisfied by natsgo.JetStreamContext and by the in-memory
// fake used in unit tests.
type streamManager interface {
	StreamInfo(stream string, opts ...natsgo.JSOpt) (*natsgo.StreamInfo, error)
	AddStream(cfg *natsgo.StreamConfig, opts ...natsgo.JSOpt) (*natsgo.StreamInfo, error)
	UpdateStream(cfg *natsgo.StreamConfig, opts ...natsgo.JSOpt) (*natsgo.StreamInfo, error)
}

// streamPurger is the subset of natsgo.JetStreamContext used for the
// deletion-time subject-filtered purges. It is satisfied by
// natsgo.JetStreamContext and by the in-memory fake used in unit tests.
type streamPurger interface {
	PurgeStream(stream string, opts ...natsgo.JSOpt) error
}

// consumerManager is the subset of natsgo.JetStreamContext used for consumer
// reconciliation and deletion cleanup. It is satisfied by
// natsgo.JetStreamContext and by the in-memory fake used in unit tests.
type consumerManager interface {
	ConsumerInfo(stream, name string, opts ...natsgo.JSOpt) (*natsgo.ConsumerInfo, error)
	AddConsumer(stream string, cfg *natsgo.ConsumerConfig, opts ...natsgo.JSOpt) (*natsgo.ConsumerInfo, error)
	UpdateConsumer(stream string, cfg *natsgo.ConsumerConfig, opts ...natsgo.JSOpt) (*natsgo.ConsumerInfo, error)
	DeleteConsumer(stream, consumer string, opts ...natsgo.JSOpt) error
}

// jetStreamManager is the union of stream and consumer management used by the
// full SM startup reconciliation. natsgo.JetStreamContext satisfies it.
type jetStreamManager interface {
	streamManager
	consumerManager
}

// ErrStreamNotFound is wrapped by ReconcileStream when a stream lookup fails
// for a reason other than not-found; a true not-found triggers creation.
var ErrStreamNotFound = errors.New("stream not found")

// ReconcileStream ensures the named stream exists with all required alpha4
// subjects present. If the stream is missing it is created with cfg. If it
// already exists, ReconcileStream appends any missing required subjects to the
// existing subject list without removing unrelated existing subjects; other
// stream fields retain the existing stream's value. A matching stream with all
// required subjects already present is success.
func ReconcileStream(js streamManager, cfg *natsgo.StreamConfig) error {
	info, err := js.StreamInfo(cfg.Name)
	if err != nil {
		if !errors.Is(err, natsgo.ErrStreamNotFound) {
			return fmt.Errorf("lookup stream %q: %w", cfg.Name, err)
		}
		if _, err := js.AddStream(cfg); err != nil {
			return fmt.Errorf("create stream %q: %w", cfg.Name, err)
		}
		return nil
	}
	// Existing stream: ensure required subjects are present without removing
	// unrelated existing subjects.
	existing := info.Config
	merged, added := unionSubjects(existing.Subjects, cfg.Subjects)
	if !added {
		return nil
	}
	updated := existing
	updated.Subjects = merged
	if _, err := js.UpdateStream(&updated); err != nil {
		return fmt.Errorf("update stream %q subjects: %w", cfg.Name, err)
	}
	return nil
}

// unionSubjects returns the union of existing and required subjects, preserving
// the order of existing subjects and appending any required subject that is not
// already present. It reports whether at least one required subject was added.
func unionSubjects(existing, required []string) ([]string, bool) {
	have := make(map[string]bool, len(existing))
	for _, s := range existing {
		have[s] = true
	}
	out := make([]string, 0, len(existing)+len(required))
	out = append(out, existing...)
	added := false
	for _, s := range required {
		if !have[s] {
			out = append(out, s)
			have[s] = true
			added = true
		}
	}
	return out, added
}

// VerifySMConsumerOwnership reports whether an existing SM-owned consumer
// matches the canonical configuration exactly. It compares the durable name,
// filter subject, queue group, acknowledgement policy, delivery policy, ACK
// wait, max-ack-pending, and max-deliver. A mismatch means the consumer must
// be reconciled (updated or deleted and recreated).
func VerifySMConsumerOwnership(info *natsgo.ConsumerInfo, want *natsgo.ConsumerConfig) error {
	got := &info.Config
	if got.Durable != want.Durable {
		return fmt.Errorf("%w: durable %q != %q", ErrOwnershipCollision, got.Durable, want.Durable)
	}
	if got.FilterSubject != want.FilterSubject {
		return fmt.Errorf("%w: filter %q != %q", ErrOwnershipCollision, got.FilterSubject, want.FilterSubject)
	}
	if got.DeliverGroup != want.DeliverGroup {
		return fmt.Errorf("%w: queue group %q != %q", ErrOwnershipCollision, got.DeliverGroup, want.DeliverGroup)
	}
	if err := compareConsumerSettings(got, want); err != nil {
		return err
	}
	return nil
}

// VerifyTranslatorConsumerOwnership reports whether an existing per-experiment
// Translator consumer belongs to the given experiment. It requires the stream,
// durable name, exact filter subject, and all four ownership metadata entries
// to match the deleting CR's full UID, namespace, and project, plus the
// acknowledgement policy, delivery policy, AckWait, MaxAckPending, and
// MaxDeliver. Any mismatch is an identity collision: the caller must not
// delete, update, or adopt the consumer.
func VerifyTranslatorConsumerOwnership(info *natsgo.ConsumerInfo, stream, uid, namespace, project string) error {
	if info == nil {
		return errors.New("nil consumer info")
	}
	got := &info.Config
	if got.Durable != TranslatorConsumerName(uid) {
		return fmt.Errorf("%w: durable %q != %q", ErrOwnershipCollision, got.Durable, TranslatorConsumerName(uid))
	}
	wantFilter := fmt.Sprintf("cbse.%s.%s.trans.request", namespace, project)
	if got.FilterSubject != wantFilter {
		return fmt.Errorf("%w: filter %q != %q", ErrOwnershipCollision, got.FilterSubject, wantFilter)
	}
	want := TranslatorConsumerConfig(uid, namespace, project)
	if err := compareConsumerSettings(got, want); err != nil {
		return err
	}
	if err := compareMetadata(got.Metadata, want.Metadata); err != nil {
		return fmt.Errorf("%w: %v", ErrOwnershipCollision, err)
	}
	return nil
}

// compareConsumerSettings compares the acknowledgement policy, delivery policy,
// ACK wait, max-ack-pending, and max-deliver between two consumer configs.
// Durable name and filter subject are compared by the callers because their
// expected values differ between SM-owned and per-experiment consumers.
func compareConsumerSettings(got, want *natsgo.ConsumerConfig) error {
	if got.DeliverPolicy != want.DeliverPolicy {
		return fmt.Errorf("%w: deliver policy %v != %v", ErrOwnershipCollision, got.DeliverPolicy, want.DeliverPolicy)
	}
	if got.AckPolicy != want.AckPolicy {
		return fmt.Errorf("%w: ack policy %v != %v", ErrOwnershipCollision, got.AckPolicy, want.AckPolicy)
	}
	if got.AckWait != want.AckWait {
		return fmt.Errorf("%w: ack wait %v != %v", ErrOwnershipCollision, got.AckWait, want.AckWait)
	}
	if got.MaxAckPending != want.MaxAckPending {
		return fmt.Errorf("%w: max ack pending %d != %d", ErrOwnershipCollision, got.MaxAckPending, want.MaxAckPending)
	}
	if got.MaxDeliver != want.MaxDeliver {
		return fmt.Errorf("%w: max deliver %d != %d", ErrOwnershipCollision, got.MaxDeliver, want.MaxDeliver)
	}
	return nil
}

// compareMetadata reports whether got contains every entry in want with the
// same value. Extra entries in got are tolerated only when they do not
// overwrite a wanted key; alpha4 does not define any extra metadata, so a
// mismatch on a wanted key is a collision.
func compareMetadata(got, want map[string]string) error {
	if want == nil {
		want = map[string]string{}
	}
	if got == nil {
		got = map[string]string{}
	}
	for k, v := range want {
		if got[k] != v {
			return fmt.Errorf("metadata %q: got %q want %q", k, got[k], v)
		}
	}
	return nil
}

// ReconcileSMConsumer ensures the SM-owned durable consumer named in want exists
// on stream and matches the canonical configuration. If missing, it is created.
// If present and matching, success. If present but a mutable setting differs,
// it is updated in place. If an update cannot reconcile an incompatible filter
// or immutable setting (the server returns an error), the consumer is deleted
// and recreated with the canonical configuration.
//
// A durable consumer with a matching name but a different stream, or any
// setting that cannot be updated, is handled by delete-and-recreate: this
// applies only to the two SM-owned durables and never to a per-experiment
// Translator consumer.
func ReconcileSMConsumer(js consumerManager, stream string, want *natsgo.ConsumerConfig) error {
	info, err := js.ConsumerInfo(stream, want.Durable)
	if err != nil {
		if !errors.Is(err, natsgo.ErrConsumerNotFound) {
			return fmt.Errorf("lookup consumer %q on stream %q: %w", want.Durable, stream, err)
		}
		if _, err := js.AddConsumer(stream, want); err != nil {
			return fmt.Errorf("create consumer %q on stream %q: %w", want.Durable, stream, err)
		}
		return nil
	}
	if VerifySMConsumerOwnership(info, want) == nil {
		return nil
	}
	// Try an in-place update first; this reconciles mutable settings and, on
	// many server versions, the filter subject.
	if _, err := js.UpdateConsumer(stream, want); err == nil {
		return nil
	}
	// The server could not update an incompatible filter or immutable setting
	// in place: delete and recreate the named SM-owned durable.
	if err := js.DeleteConsumer(stream, want.Durable); err != nil && !errors.Is(err, natsgo.ErrConsumerNotFound) {
		return fmt.Errorf("delete incompatible consumer %q on stream %q: %w", want.Durable, stream, err)
	}
	if _, err := js.AddConsumer(stream, want); err != nil {
		return fmt.Errorf("recreate consumer %q on stream %q: %w", want.Durable, stream, err)
	}
	return nil
}

// ReconcileStreamsAndConsumers reconciles the two shared streams and the two
// SM-owned durable consumers to the canonical alpha4 configuration. It is the
// SM startup reconciliation entry point. A remaining mismatch that cannot be
// reconciled fails startup with the exact stream or consumer name.
func ReconcileStreamsAndConsumers(js jetStreamManager) error {
	if err := ReconcileStream(js, EDSStreamConfig()); err != nil {
		return err
	}
	if err := ReconcileStream(js, TranslatorStreamConfig()); err != nil {
		return err
	}
	for _, c := range SMConsumers() {
		if err := ReconcileSMConsumer(js, c.Stream, c.Config); err != nil {
			return err
		}
	}
	return nil
}

// DeleteTranslatorConsumer deletes a per-experiment Translator consumer from
// stream cbse_translator only after verifying it belongs to the given
// experiment. A missing consumer is success. An ownership mismatch is a
// collision: the consumer is not deleted and the error is returned so the
// caller retains the finalizer and retries.
func DeleteTranslatorConsumer(js consumerManager, stream, uid, namespace, project string) error {
	name := TranslatorConsumerName(uid)
	info, err := js.ConsumerInfo(stream, name)
	if err != nil {
		if errors.Is(err, natsgo.ErrConsumerNotFound) {
			return nil
		}
		return fmt.Errorf("lookup translator consumer %q on stream %q: %w", name, stream, err)
	}
	if err := VerifyTranslatorConsumerOwnership(info, stream, uid, namespace, project); err != nil {
		return err
	}
	if err := js.DeleteConsumer(stream, name); err != nil && !errors.Is(err, natsgo.ErrConsumerNotFound) {
		return fmt.Errorf("delete translator consumer %q on stream %q: %w", name, stream, err)
	}
	return nil
}

// PurgeSubject purges messages matching subject from the named stream. A
// missing stream is success (the corresponding cleanup is already satisfied).
// Any other broker error is returned so the caller retains the finalizer and
// retries on the fixed cleanup cadence.
func PurgeSubject(js streamPurger, stream, subject string) error {
	err := js.PurgeStream(stream, &natsgo.StreamPurgeRequest{Subject: subject})
	if err == nil {
		return nil
	}
	if errors.Is(err, natsgo.ErrStreamNotFound) {
		return nil
	}
	return fmt.Errorf("purge subject %q from stream %q: %w", subject, stream, err)
}
