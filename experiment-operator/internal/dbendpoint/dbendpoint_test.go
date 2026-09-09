package dbendpoint

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func TestClassifyHost(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		want    Classified
		wantErr bool
	}{
		{"dns lowercase", "db.example.com", Classified{HostDNS, "db.example.com"}, false},
		{"dns subdomain trailing dot rejected", "db.example.com.", Classified{}, true},
		{"dns uppercase rejected", "DB.Example.com", Classified{}, true},
		{"dns invalid char rejected", "db_-bad", Classified{}, true},
		{"ipv4 normalized", "10.0.0.1", Classified{HostIPv4, "10.0.0.1"}, false},
		{"ipv4 with embedded port rejected", "10.0.0.1:5432", Classified{}, true},
		{"ipv6 normalized", "2001:db8::1", Classified{HostIPv6, "2001:db8::1"}, false},
		{"ipv6 uppercase hex normalized lowercase", "2001:DB8::1", Classified{HostIPv6, "2001:db8::1"}, false},
		{"ipv6 bracketed rejected", "[2001:db8::1]", Classified{}, true},
		{"ipv6 zone rejected", "fe80::1%eth0", Classified{}, true},
		{"empty rejected", "  ", Classified{}, true},
		{"url rejected", "https://db.example.com", Classified{}, true},
		{"path rejected", "db.example.com/path", Classified{}, true},
		{"query rejected", "db.example.com?q=1", Classified{}, true},
		{"fragment rejected", "db.example.com#frag", Classified{}, true},
		{"embedded port dns rejected", "db.example.com:5432", Classified{}, true},
		{"unix socket rejected", "/var/run/pg/.s.PGSQL.5432", Classified{}, true},
		{"trimmed spaces", "  db.example.com  ", Classified{HostDNS, "db.example.com"}, false},
		{"trimmed ipv4", "  10.0.0.1  ", Classified{HostIPv4, "10.0.0.1"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ClassifyHost(tc.host)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ClassifyHost(%q) err = %v, wantErr = %v", tc.host, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got != tc.want {
				t.Fatalf("ClassifyHost(%q) = %+v, want %+v", tc.host, got, tc.want)
			}
		})
	}
}

type fakeConn struct {
	pingErr   error
	closeErr  error
	pings     int
	closes    int
	otherSQLs int
}

func (c *fakeConn) Ping(ctx context.Context) error {
	c.pings++
	return c.pingErr
}
func (c *fakeConn) Close(ctx context.Context) error {
	c.closes++
	return c.closeErr
}

type fakeConnector struct {
	conn  Conn
	err   error
	opens int
}

func (c *fakeConnector) Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error) {
	c.opens++
	if c.err != nil {
		return nil, c.err
	}
	return c.conn, nil
}

type fakeResolver struct {
	addrs []net.IPAddr
	err   error
	calls int
}

func (r *fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	r.calls++
	return r.addrs, r.err
}

func ipAddr(s string) net.IPAddr {
	return net.IPAddr{IP: net.ParseIP(s)}
}

func TestProbeDNSResolutionClasses(t *testing.T) {
	v4 := ipAddr("10.0.0.1")
	v6 := ipAddr("2001:db8::1")
	successConn := &fakeConn{}
	tests := []struct {
		name       string
		host       string
		resolver   Resolver
		wantErr    bool
		wantResolv bool
	}{
		{"dns only A", "db.example.com", &fakeResolver{addrs: []net.IPAddr{v4}}, false, true},
		{"dns only AAAA", "db.example.com", &fakeResolver{addrs: []net.IPAddr{v6}}, false, true},
		{"dns mixed A AAAA", "db.example.com", &fakeResolver{addrs: []net.IPAddr{v4, v6}}, false, true},
		{"dns duplicate", "db.example.com", &fakeResolver{addrs: []net.IPAddr{v4, v4}}, false, true},
		{"dns empty resolution", "db.example.com", &fakeResolver{addrs: nil}, true, true},
		{"dns resolver failure", "db.example.com", &fakeResolver{err: errors.New("no such host")}, true, true},
		{"ipv4 literal no resolution", "10.0.0.1", &fakeResolver{}, false, false},
		{"ipv6 literal no resolution", "2001:db8::1", &fakeResolver{}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conn := &fakeConn{}
			connector := &fakeConnector{conn: conn}
			resolver, _ := tc.resolver.(*fakeResolver)
			err := Probe(context.Background(), Endpoint{Host: tc.host, Port: 5432, User: "u", Password: "p", DBName: "d"}, resolver, connector)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Probe err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantResolv && resolver == nil {
				t.Fatalf("expected resolver usage")
			}
			if tc.wantErr {
				// On resolution failure, no connect/ping/close happens.
				if connector.opens != 0 {
					t.Fatalf("resolution failure opened %d connections, want 0", connector.opens)
				}
				if conn.pings != 0 || conn.closes != 0 {
					t.Fatalf("resolution failure pinged/closed: pings=%d closes=%d", conn.pings, conn.closes)
				}
				return
			}
			// Exactly one connect, one ping, one close. No pool, no other SQL.
			if connector.opens != 1 {
				t.Fatalf("opens = %d, want 1 (no pool, single connection)", connector.opens)
			}
			if conn.pings != 1 {
				t.Fatalf("pings = %d, want exactly one SELECT 1", conn.pings)
			}
			if conn.closes != 1 {
				t.Fatalf("closes = %d, want exactly one close (no retained pool)", conn.closes)
			}
			if conn.otherSQLs != 0 {
				t.Fatalf("otherSQLs = %d, want no other SQL", conn.otherSQLs)
			}
		})
	}
	_ = successConn
}

func TestProbeClosesConnectionOnPingFailure(t *testing.T) {
	conn := &fakeConn{pingErr: errors.New("boom")}
	connector := &fakeConnector{conn: conn}
	err := Probe(context.Background(), Endpoint{Host: "10.0.0.1", Port: 5432}, nil, connector)
	if err == nil {
		t.Fatal("expected probe failure")
	}
	if !strings.Contains(err.Error(), "probe database") {
		t.Fatalf("error %q should report probe failure", err)
	}
	if connector.opens != 1 {
		t.Fatalf("opens = %d, want 1", connector.opens)
	}
	if conn.pings != 1 {
		t.Fatalf("pings = %d, want 1", conn.pings)
	}
	if conn.closes != 1 {
		t.Fatalf("probe must close the connection even on ping failure; closes = %d, want 1", conn.closes)
	}
}

func TestProbeRequiresConnector(t *testing.T) {
	if err := Probe(context.Background(), Endpoint{Host: "10.0.0.1"}, nil, nil); err == nil {
		t.Fatal("expected error for nil connector")
	}
}
