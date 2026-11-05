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

// nats.go is the real NATS adapter implementing the publisher.Conn seam
// with the official nats.go client. The publisher depends on the seam; this
// adapter is wired in main and exercised by the smoke suite.
package publisher

import (
	"context"
	"fmt"
	"time"

	nats "github.com/nats-io/nats.go"
)

// NatsConn is the real NATS transport: the Core-NATS availability
// request/reply (bounded by the per-request timeout) and the JetStream
// PubAck-gated batch publish.
type NatsConn struct {
	nc *nats.Conn
	js nats.JetStreamContext
}

// NatsDialer is the real Dialer: it connects to the NATS server (no
// credentials in the alpha4 URL) and opens the JetStream context.
func NatsDialer(url string) Dialer {
	return func(ctx context.Context) (Conn, error) {
		nc, err := nats.Connect(url, nats.Name("cbse-eds"))
		if err != nil {
			return nil, fmt.Errorf("nats connect: %w", err)
		}
		js, err := nc.JetStream()
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("nats jetstream: %w", err)
		}
		return &NatsConn{nc: nc, js: js}, nil
	}
}

// Request publishes data on the availability subject and waits for the
// single reply, bounded by timeout (the per-request timeout of the request
// skeleton). A timeout (no reply) or transport error is returned for the
// publisher to classify as transient.
func (c *NatsConn) Request(ctx context.Context, subject string, data []byte, timeout time.Duration) ([]byte, error) {
	msg, err := c.nc.RequestMsg(&nats.Msg{Subject: subject, Data: data}, timeout)
	if err != nil {
		return nil, err
	}
	return msg.Data, nil
}

// Publish publishes the batch on the SM-provided subject via JetStream and
// blocks until the server confirms publication (PubAck-gated).
func (c *NatsConn) Publish(ctx context.Context, subject string, data []byte) error {
	if _, err := c.js.Publish(subject, data); err != nil {
		return fmt.Errorf("jetstream publish %q: %w", subject, err)
	}
	return nil
}

// Close drains and closes the connection.
func (c *NatsConn) Close() {
	c.nc.Close()
}
