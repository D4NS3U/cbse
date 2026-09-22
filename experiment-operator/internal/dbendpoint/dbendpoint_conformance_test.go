package dbendpoint

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// hostConnector connects or fails based on the candidate host. It records the
// ordered list of hosts it was asked to connect to so fallback and de-dup
// behavior is observable.
type hostConnector struct {
	fail    map[string]error
	success map[string]Conn
	tried   []string
}

func (c *hostConnector) Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error) {
	c.tried = append(c.tried, host)
	if err, ok := c.fail[host]; ok {
		return nil, err
	}
	if conn, ok := c.success[host]; ok {
		return conn, nil
	}
	// Default: succeed with a fresh fake conn.
	return &fakeConn{}, nil
}

// blockingConnector waits on the context until it is cancelled, then returns the
// context error, so the shared deadline can be observed.
type blockingConnector struct{ tried []string }

func (c *blockingConnector) Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error) {
	c.tried = append(c.tried, host)
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestProbeFallbackTriesAddressesInOrder verifies the common endpoint contract:
// the probe tries resolved addresses in resolver order and the first
// successful connection owns the operation; remaining addresses are not
// contacted.
func TestProbeFallbackTriesAddressesInOrder(t *testing.T) {
	v4 := ipAddr("10.0.0.1")
	v6 := ipAddr("2001:db8::1")
	conn := &fakeConn{}
	connector := &hostConnector{
		fail:    map[string]error{"10.0.0.1": errors.New("connection refused")},
		success: map[string]Conn{"2001:db8::1": conn},
	}
	resolver := &fakeResolver{addrs: []net.IPAddr{v4, v6}}
	err := Probe(context.Background(), Endpoint{Host: "db.example.com", Port: 5432}, resolver, connector)
	if err != nil {
		t.Fatalf("Probe err = %v, want nil (fallback to second address)", err)
	}
	if connector.tried[0] != "10.0.0.1" || connector.tried[1] != "2001:db8::1" {
		t.Fatalf("tried = %v, want [10.0.0.1 2001:db8::1]", connector.tried)
	}
	if len(connector.tried) != 2 {
		t.Fatalf("tried %d addresses, want 2", len(connector.tried))
	}
	if conn.pings != 1 || conn.closes != 1 {
		t.Fatalf("pings=%d closes=%d, want 1/1", conn.pings, conn.closes)
	}
}

// TestProbeDeDuplication verifies duplicate resolved addresses are removed by
// retaining their first occurrence, so the probe contacts each address at most
// once.
func TestProbeDeDuplication(t *testing.T) {
	v4 := ipAddr("10.0.0.1")
	connector := &hostConnector{}
	resolver := &fakeResolver{addrs: []net.IPAddr{v4, v4, v4}}
	err := Probe(context.Background(), Endpoint{Host: "db.example.com", Port: 5432}, resolver, connector)
	if err != nil {
		t.Fatalf("Probe err = %v, want nil", err)
	}
	if len(connector.tried) != 1 || connector.tried[0] != "10.0.0.1" {
		t.Fatalf("tried = %v, want [10.0.0.1] (de-duplicated)", connector.tried)
	}
}

// TestProbeAddressExhaustion verifies that when every resolved address fails to
// connect, the probe fails and performs no ping.
func TestProbeAddressExhaustion(t *testing.T) {
	v4 := ipAddr("10.0.0.1")
	v6 := ipAddr("2001:db8::1")
	connector := &hostConnector{
		fail: map[string]error{
			"10.0.0.1":    errors.New("connection refused"),
			"2001:db8::1": errors.New("connection refused"),
		},
	}
	resolver := &fakeResolver{addrs: []net.IPAddr{v4, v6}}
	err := Probe(context.Background(), Endpoint{Host: "db.example.com", Port: 5432}, resolver, connector)
	if err == nil {
		t.Fatal("Probe err = nil, want exhaustion failure")
	}
	if !strings.Contains(err.Error(), "connect database") {
		t.Fatalf("error %q should report connect failure", err)
	}
	if len(connector.tried) != 2 {
		t.Fatalf("tried %d addresses, want 2", len(connector.tried))
	}
}

// TestProbeSharedDeadline verifies DNS resolution and candidate connection
// attempts share one deadline: a connector that blocks until the context is
// cancelled fails with the deadline, not an unbounded wait. The probe wraps its
// caller's context with the 10-second resolutionDeadline, so a shorter caller
// deadline bounds the wait; this test uses a short caller deadline to verify
// the shared-deadline behavior quickly.
func TestProbeSharedDeadline(t *testing.T) {
	if resolutionDeadline != 10*time.Second {
		t.Fatalf("resolutionDeadline = %v, want 10s per the common endpoint contract", resolutionDeadline)
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	connector := &blockingConnector{}
	err := Probe(ctx, Endpoint{Host: "10.0.0.1", Port: 5432}, &fakeResolver{}, connector)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Probe err = nil, want deadline exceeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Probe err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Fatalf("Probe waited %v, want the short shared deadline to bound the wait", elapsed)
	}
}

// TestResolveAddressesOrderAndDeDup verifies the resolver helper retains
// resolver order and removes duplicates by first occurrence for mixed A/AAAA
// results.
func TestResolveAddressesOrderAndDeDup(t *testing.T) {
	v4 := ipAddr("10.0.0.1")
	v6 := ipAddr("2001:db8::1")
	v4b := ipAddr("10.0.0.2")
	cases := []struct {
		name  string
		kind  HostKind
		host  string
		addrs []net.IPAddr
		want  []string
	}{
		{"ipv4 literal", HostIPv4, "10.0.0.1", nil, []string{"10.0.0.1"}},
		{"ipv6 literal", HostIPv6, "2001:db8::1", nil, []string{"2001:db8::1"}},
		{"dns A then AAAA", HostDNS, "db.example.com", []net.IPAddr{v4, v6}, []string{"10.0.0.1", "2001:db8::1"}},
		{"dns AAAA then A", HostDNS, "db.example.com", []net.IPAddr{v6, v4}, []string{"2001:db8::1", "10.0.0.1"}},
		{"dns duplicates removed", HostDNS, "db.example.com", []net.IPAddr{v4, v4, v6, v6}, []string{"10.0.0.1", "2001:db8::1"}},
		{"dns triple mixed de-dup", HostDNS, "db.example.com", []net.IPAddr{v4, v6, v4, v4b, v6}, []string{"10.0.0.1", "2001:db8::1", "10.0.0.2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &fakeResolver{addrs: tc.addrs}
			got, err := ResolveAddresses(context.Background(), Classified{Kind: tc.kind, Normalized: tc.host}, resolver)
			if err != nil {
				t.Fatalf("ResolveAddresses err = %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ResolveAddresses = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ResolveAddresses[%d] = %q, want %q (full: %v want %v)", i, got[i], tc.want[i], got, tc.want)
				}
			}
		})
	}
}

// TestResolveAddressesEmptyAndFailure verifies an empty result and a resolver
// error are resolution failures.
func TestResolveAddressesEmptyAndFailure(t *testing.T) {
	if _, err := ResolveAddresses(context.Background(), Classified{Kind: HostDNS, Normalized: "db.example.com"}, &fakeResolver{addrs: nil}); err == nil {
		t.Fatal("empty resolution should fail")
	}
	if _, err := ResolveAddresses(context.Background(), Classified{Kind: HostDNS, Normalized: "db.example.com"}, &fakeResolver{err: errors.New("no such host")}); err == nil {
		t.Fatal("resolver error should fail")
	}
}
