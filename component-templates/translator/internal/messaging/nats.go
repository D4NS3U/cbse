// nats.go is the real NATS JetStream adapter implementing the messaging.Consumer,
// messaging.Publisher, and messaging.Manager seams with the official nats.go
// client. The orchestrator depends on the seams; this adapter is wired in main
// and exercised by the smoke suite.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	nats "github.com/nats-io/nats.go"
)

// Client is the real NATS JetStream client. It holds one pull subscription
// created lazily on the first Fetch.
type Client struct {
	js     nats.JetStreamContext
	stream string
	cfg    *nats.ConsumerConfig
	sub    *nats.Subscription
	subErr error
}

// NewClient connects to NATS at url, builds the per-experiment consumer config,
// and returns a Client. It does not create the consumer; call EnsureConsumer.
func NewClient(nc *nats.Conn, stream, uid, namespace, project, requestSubject string) (*Client, error) {
	if nc == nil {
		return nil, errors.New("messaging: nil nats connection")
	}
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("messaging: jetstream: %w", err)
	}
	return &Client{
		js:     js,
		stream: stream,
		cfg:    ConsumerConfig(uid, namespace, project, requestSubject),
	}, nil
}

// Config returns the consumer configuration (for diagnostics and ownership
// comparison).
func (c *Client) Config() *nats.ConsumerConfig { return c.cfg }

// EnsureConsumer creates the per-experiment durable consumer if absent, or
// attaches to an existing one whose identity exactly matches. A name collision
// with mismatched settings is rejected without modifying the consumer.
func (c *Client) EnsureConsumer() error {
	info, err := c.js.ConsumerInfo(c.stream, c.cfg.Durable)
	if err != nil {
		if !errors.Is(err, nats.ErrConsumerNotFound) && !errors.Is(err, nats.ErrStreamNotFound) {
			return fmt.Errorf("messaging: consumer info: %w", err)
		}
		if _, err := c.js.AddConsumer(c.stream, c.cfg); err != nil {
			return fmt.Errorf("messaging: create consumer: %w", err)
		}
		return nil
	}
	if err := CompareConsumer(info, c.cfg); err != nil {
		return err
	}
	return nil
}

// Fetch blocks until one request is available or ctx is cancelled. It creates
// the pull subscription lazily and polls with a bounded MaxWait so ctx
// cancellation is honored promptly.
func (c *Client) Fetch(ctx context.Context) (Message, error) {
	if err := c.ensureSub(); err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		msgs, err := c.sub.Fetch(1, nats.MaxWait(time.Second))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				continue
			}
			return nil, fmt.Errorf("messaging: fetch: %w", err)
		}
		if len(msgs) == 0 {
			continue
		}
		return &msgWrapper{msg: msgs[0]}, nil
	}
}

func (c *Client) ensureSub() error {
	if c.sub != nil || c.subErr != nil {
		return c.subErr
	}
	sub, err := c.js.PullSubscribe(c.cfg.FilterSubject, c.cfg.Durable, nats.BindStream(c.stream))
	if err != nil {
		c.subErr = fmt.Errorf("messaging: pull subscribe: %w", err)
		return c.subErr
	}
	c.sub = sub
	return nil
}

// Publish publishes a ready message and blocks until the server confirms
// publication (PubAck).
func (c *Client) Publish(subject string, data []byte) error {
	if _, err := c.js.Publish(subject, data); err != nil {
		return fmt.Errorf("messaging: publish ready: %w", err)
	}
	return nil
}

// msgWrapper adapts *nats.Msg to the messaging.Message interface.
type msgWrapper struct{ msg *nats.Msg }

func (m *msgWrapper) Data() []byte      { return m.msg.Data }
func (m *msgWrapper) Subject() string   { return m.msg.Subject }
func (m *msgWrapper) Ack() error        { return m.msg.Ack() }
func (m *msgWrapper) Nak() error        { return m.msg.Nak() }
func (m *msgWrapper) InProgress() error { return m.msg.InProgress() }
