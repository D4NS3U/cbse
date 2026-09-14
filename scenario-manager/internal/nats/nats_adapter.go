package nats

import (
	"context"
	"fmt"

	natsgo "github.com/nats-io/nats.go"
)

// JetStreamDeletion is the subset of natsgo.JetStreamContext required for
// deletion-time cleanup: consumer lookup/delete and subject-filtered purge.
// natsgo.JetStreamContext satisfies it, and the method set also structurally
// satisfies this package's unexported consumerManager and streamPurger
// interfaces, so the adapter can delegate to DeleteTranslatorConsumer
// and PurgeSubject.
type JetStreamDeletion interface {
	ConsumerInfo(stream, name string, opts ...natsgo.JSOpt) (*natsgo.ConsumerInfo, error)
	AddConsumer(stream string, cfg *natsgo.ConsumerConfig, opts ...natsgo.JSOpt) (*natsgo.ConsumerInfo, error)
	UpdateConsumer(stream string, cfg *natsgo.ConsumerConfig, opts ...natsgo.JSOpt) (*natsgo.ConsumerInfo, error)
	DeleteConsumer(stream, consumer string, opts ...natsgo.JSOpt) error
	PurgeStream(stream string, opts ...natsgo.JSOpt) error
}

// NATSDeletionClient adapts a JetStream context to the lifecycle.MessagingCleaner
// interface by delegating to this package's ownership-verified consumer deletion
// and subject-filtered purge helpers. It is constructed in the SM wiring
// (internal/core) and injected into lifecycle.RunDeletionCleanup.
type NATSDeletionClient struct {
	js JetStreamDeletion
}

// NewNATSDeletionClient returns a lifecycle.MessagingCleaner backed by the
// given JetStream context.
func NewNATSDeletionClient(js JetStreamDeletion) *NATSDeletionClient {
	return &NATSDeletionClient{js: js}
}

// DeleteTranslatorConsumer deletes the per-experiment Translator consumer from
// stream cbse_translator after ownership verification. A missing consumer is
// success; a collision returns an error so the caller retains the finalizer.
func (c *NATSDeletionClient) DeleteTranslatorConsumer(ctx context.Context, uid, namespace, project string) error {
	_ = ctx
	if err := DeleteTranslatorConsumer(c.js, TranslatorStreamName, uid, namespace, project); err != nil {
		return fmt.Errorf("delete translator consumer: %w", err)
	}
	return nil
}

// PurgeSubject purges messages matching subject from the named stream. A
// missing stream is success.
func (c *NATSDeletionClient) PurgeSubject(ctx context.Context, stream, subject string) error {
	_ = ctx
	if err := PurgeSubject(c.js, stream, subject); err != nil {
		return fmt.Errorf("purge subject: %w", err)
	}
	return nil
}
