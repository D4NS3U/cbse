package databaseendpoint

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

type fakeResolver struct {
	addrs []net.IPAddr
	err   error
}

func (f fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.addrs, nil
}

func ipAddr(s string) net.IPAddr { return net.IPAddr{IP: net.ParseIP(s)} }

type fakeConn struct {
	pings  int
	closes int
}

func (c *fakeConn) Ping(ctx context.Context) error  { c.pings++; return nil }
func (c *fakeConn) Close(ctx context.Context) error { c.closes++; return nil }

// hostConnector connects or fails based on the candidate host and records the
// ordered list of hosts it was asked to connect to.
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
	return &fakeConn{}, nil
}

// blockingConnector waits on the context until it is cancelled.
type blockingConnector struct{ tried []string }

func (c *blockingConnector) Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error) {
	c.tried = append(c.tried, host)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestClassifyHost(t *testing.T) {
	cases := []struct {
		name string
		host string
		kind HostKind
		norm string
		err  bool
	}{
		{"ipv4 literal", "10.0.0.1", HostIPv4, "10.0.0.1", false},
		{"ipv4 trimmed", "  10.0.0.1  ", HostIPv4, "10.0.0.1", false},
		{"ipv6 literal", "2001:db8::1", HostIPv6, "2001:db8::1", false},
		{"ipv6 full form normalized", "2001:0db8:0000:0000:0000:0000:0000:0001", HostIPv6, "2001:db8::1", false},
		{"dns subdomain", "db.svc.cluster.local", HostDNS, "db.svc.cluster.local", false},
		{"dns single label", "db", HostDNS, "db", false},
		{"dns uppercase rejected", "DB.example.com", HostDNS, "", true},
		{"empty", "", HostDNS, "", true},
		{"bracketed", "[10.0.0.1]", HostDNS, "", true},
		{"embedded port", "db:5432", HostDNS, "", true},
		{"zone", "fe80::1%eth0", HostDNS, "", true},
		{"scheme", "tcp://db", HostDNS, "", true},
		{"path", "db/path", HostDNS, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ClassifyHost(tc.host)
			if tc.err {
				if err == nil {
					t.Fatalf("ClassifyHost(%q) = %+v, want error", tc.host, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ClassifyHost(%q) err = %v", tc.host, err)
			}
			if got.Kind != tc.kind || got.Normalized != tc.norm {
				t.Fatalf("ClassifyHost(%q) = %+v, want kind=%v norm=%q", tc.host, got, tc.kind, tc.norm)
			}
		})
	}
}

func TestResolveAddresses(t *testing.T) {
	v4 := ipAddr("10.0.0.1")
	v6 := ipAddr("2001:db8::1")
	v4b := ipAddr("10.0.0.2")
	cases := []struct {
		name string
		kind HostKind
		host string
		addr []net.IPAddr
		want []string
	}{
		{"ipv4 literal", HostIPv4, "10.0.0.1", nil, []string{"10.0.0.1"}},
		{"ipv6 literal", HostIPv6, "2001:db8::1", nil, []string{"2001:db8::1"}},
		{"dns A then AAAA", HostDNS, "db.example.com", []net.IPAddr{v4, v6}, []string{"10.0.0.1", "2001:db8::1"}},
		{"dns AAAA then A", HostDNS, "db.example.com", []net.IPAddr{v6, v4}, []string{"2001:db8::1", "10.0.0.1"}},
		{"dns de-dup", HostDNS, "db.example.com", []net.IPAddr{v4, v4, v6, v6}, []string{"10.0.0.1", "2001:db8::1"}},
		{"dns mixed de-dup", HostDNS, "db.example.com", []net.IPAddr{v4, v6, v4, v4b, v6}, []string{"10.0.0.1", "2001:db8::1", "10.0.0.2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveAddresses(context.Background(), Classified{Kind: tc.kind, Normalized: tc.host}, fakeResolver{addrs: tc.addr})
			if err != nil {
				t.Fatalf("ResolveAddresses err = %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ResolveAddresses = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ResolveAddresses[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestResolveAddressesFailures(t *testing.T) {
	if _, err := ResolveAddresses(context.Background(), Classified{Kind: HostDNS, Normalized: "db.example.com"}, fakeResolver{addrs: nil}); err == nil {
		t.Fatal("empty resolution should fail")
	}
	if _, err := ResolveAddresses(context.Background(), Classified{Kind: HostDNS, Normalized: "db.example.com"}, fakeResolver{err: errors.New("no such host")}); err == nil {
		t.Fatal("resolver error should fail")
	}
}

// TestDialFallback verifies the dispatcher tries resolved addresses in resolver
// order and the first successful connection owns the dial; remaining addresses
// are not contacted.
func TestDialFallback(t *testing.T) {
	v4 := ipAddr("10.0.0.1")
	v6 := ipAddr("2001:db8::1")
	conn := &fakeConn{}
	connector := &hostConnector{
		fail:    map[string]error{"10.0.0.1": errors.New("connection refused")},
		success: map[string]Conn{"2001:db8::1": conn},
	}
	resolver := fakeResolver{addrs: []net.IPAddr{v4, v6}}
	res, err := Dial(context.Background(), Endpoint{Host: "db.example.com", Port: 5432}, resolver, connector)
	if err != nil {
		t.Fatalf("Dial err = %v", err)
	}
	if res.Host != "2001:db8::1" {
		t.Fatalf("Dial host = %q, want 2001:db8::1 (first successful)", res.Host)
	}
	if res.Kind != HostDNS {
		t.Fatalf("Dial kind = %v, want HostDNS", res.Kind)
	}
	if len(connector.tried) != 2 || connector.tried[0] != "10.0.0.1" || connector.tried[1] != "2001:db8::1" {
		t.Fatalf("tried = %v, want [10.0.0.1 2001:db8::1]", connector.tried)
	}
	if res.Conn != conn {
		t.Fatal("Dial returned the wrong connection")
	}
}

// TestDialDeDuplication verifies duplicate resolved addresses are removed so the
// dispatcher contacts each address at most once.
func TestDialDeDuplication(t *testing.T) {
	v4 := ipAddr("10.0.0.1")
	connector := &hostConnector{}
	resolver := fakeResolver{addrs: []net.IPAddr{v4, v4, v4}}
	res, err := Dial(context.Background(), Endpoint{Host: "db.example.com", Port: 5432}, resolver, connector)
	if err != nil {
		t.Fatalf("Dial err = %v", err)
	}
	if len(connector.tried) != 1 || connector.tried[0] != "10.0.0.1" {
		t.Fatalf("tried = %v, want [10.0.0.1] (de-duplicated)", connector.tried)
	}
	if res.Host != "10.0.0.1" {
		t.Fatalf("Dial host = %q, want 10.0.0.1", res.Host)
	}
}

// TestDialExhaustion verifies that when every resolved address fails, Dial fails
// and returns no connection.
func TestDialExhaustion(t *testing.T) {
	v4 := ipAddr("10.0.0.1")
	v6 := ipAddr("2001:db8::1")
	connector := &hostConnector{
		fail: map[string]error{
			"10.0.0.1":    errors.New("connection refused"),
			"2001:db8::1": errors.New("connection refused"),
		},
	}
	resolver := fakeResolver{addrs: []net.IPAddr{v4, v6}}
	res, err := Dial(context.Background(), Endpoint{Host: "db.example.com", Port: 5432}, resolver, connector)
	if err == nil {
		t.Fatal("Dial err = nil, want exhaustion failure")
	}
	if !strings.Contains(err.Error(), "connect database") {
		t.Fatalf("error %q should report connect failure", err)
	}
	if res.Conn != nil {
		t.Fatal("Dial should return no connection on exhaustion")
	}
}

// TestDialSharedDeadline verifies DNS resolution and candidate connection
// attempts share one deadline: a connector that blocks until the context is
// cancelled fails with the deadline, not an unbounded wait.
func TestDialSharedDeadline(t *testing.T) {
	if resolutionDeadline != 10*time.Second {
		t.Fatalf("resolutionDeadline = %v, want 10s per the common endpoint contract", resolutionDeadline)
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	connector := &blockingConnector{}
	_, err := Dial(ctx, Endpoint{Host: "10.0.0.1", Port: 5432}, fakeResolver{}, connector)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Dial err = nil, want deadline exceeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Fatalf("Dial waited %v, want the short shared deadline to bound the wait", elapsed)
	}
}

// TestDialRequiresConnector verifies Dial fails fast without a connector.
func TestDialRequiresConnector(t *testing.T) {
	if _, err := Dial(context.Background(), Endpoint{Host: "10.0.0.1", Port: 5432}, fakeResolver{}, nil); err == nil {
		t.Fatal("Dial with nil connector should fail")
	}
}

// TestDialSeparateHostPort verifies the dispatcher forwards host and port as
// separate driver values.
func TestDialSeparateHostPort(t *testing.T) {
	connector := &sepConnector{}
	res, err := Dial(context.Background(), Endpoint{Host: "10.0.0.1", Port: 6543, User: "u", Password: "p", DBName: "d"}, fakeResolver{}, connector)
	if err != nil {
		t.Fatalf("Dial err = %v", err)
	}
	if res.Host != "10.0.0.1" || connector.host != "10.0.0.1" || connector.port != 6543 || connector.user != "u" || connector.password != "p" || connector.dbname != "d" {
		t.Fatalf("driver values = host=%q port=%d user=%q pw=%q db=%q, want separate host=10.0.0.1 port=6543 user=u pw=p db=d", connector.host, connector.port, connector.user, connector.password, connector.dbname)
	}
}

type sepConnector struct {
	host     string
	port     int32
	user     string
	password string
	dbname   string
}

func (c *sepConnector) Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error) {
	c.host = host
	c.port = port
	c.user = user
	c.password = password
	c.dbname = dbname
	return &fakeConn{}, nil
}

// TestDialLiteralSkipsDNS verifies IP literals are contacted directly without
// DNS, regardless of the resolver state.
func TestDialLiteralSkipsDNS(t *testing.T) {
	connector := &hostConnector{}
	// A resolver that returns an error would fail a DNS path, proving literals
	// bypass DNS.
	resolver := fakeResolver{err: errors.New("resolver should not be called")}
	res, err := Dial(context.Background(), Endpoint{Host: "10.0.0.1", Port: 5432}, resolver, connector)
	if err != nil {
		t.Fatalf("Dial err = %v", err)
	}
	if res.Host != "10.0.0.1" || res.Kind != HostIPv4 {
		t.Fatalf("Dial = %+v, want host=10.0.0.1 kind=HostIPv4", res)
	}
	if len(connector.tried) != 1 || connector.tried[0] != "10.0.0.1" {
		t.Fatalf("tried = %v, want [10.0.0.1]", connector.tried)
	}
}
