package messaging

import (
	"errors"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
)

func TestConsumerConfig(t *testing.T) {
	cfg := ConsumerConfig("a1b2c3d4-e5f6-7890-abcd-ef1234567890", "ns", "proj", "cbse.ns.proj.trans.request")
	if cfg.Durable != "translator-a1b2c3d4e5f6" {
		t.Fatalf("durable = %q", cfg.Durable)
	}
	if cfg.FilterSubject != "cbse.ns.proj.trans.request" {
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
		"experiment.cbse.terministic.de/managed-by":     "translator",
		"experiment.cbse.terministic.de/experiment-uid": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		"experiment.cbse.terministic.de/namespace":      "ns",
		"experiment.cbse.terministic.de/project":        "proj",
	}
	for k, v := range wantMeta {
		if cfg.Metadata[k] != v {
			t.Fatalf("metadata %s = %q, want %q", k, cfg.Metadata[k], v)
		}
	}
}

func TestCompareConsumerMatch(t *testing.T) {
	want := ConsumerConfig("uid-1", "ns", "proj", "cbse.ns.proj.trans.request")
	got := &nats.ConsumerInfo{Config: *want}
	if err := CompareConsumer(got, want); err != nil {
		t.Fatalf("matching consumer rejected: %v", err)
	}
}

func TestCompareConsumerMismatches(t *testing.T) {
	want := ConsumerConfig("uid-1", "ns", "proj", "cbse.ns.proj.trans.request")
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
