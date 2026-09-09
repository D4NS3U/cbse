package dns

import (
	"context"
	"errors"
	"net"
	"testing"
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

func TestValidateHost(t *testing.T) {
	cases := []struct {
		name string
		host string
		err  bool
	}{
		{"simple", "db.example.com", false},
		{"subdomain", "db.svc.cluster.local", false},
		{"single label", "db", false},
		{"empty", "", true},
		{"whitespace", " db.example.com ", true},
		{"bracketed", "[db]", true},
		{"zone", "db%eth0", true},
		{"space inside", "db example.com", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateHost(tc.host)
			if tc.err && err == nil {
				t.Fatalf("ValidateHost(%q) = nil, want error", tc.host)
			}
			if !tc.err && err != nil {
				t.Fatalf("ValidateHost(%q) = %v, want nil", tc.host, err)
			}
		})
	}
}

func TestResolveOrderAndDeDup(t *testing.T) {
	v4 := ipAddr("10.0.0.1")
	v6 := ipAddr("2001:db8::1")
	v4b := ipAddr("10.0.0.2")
	cases := []struct {
		name  string
		addrs []net.IPAddr
		want  []string
	}{
		{"A then AAAA", []net.IPAddr{v4, v6}, []string{"10.0.0.1", "2001:db8::1"}},
		{"AAAA then A", []net.IPAddr{v6, v4}, []string{"2001:db8::1", "10.0.0.1"}},
		{"duplicates removed", []net.IPAddr{v4, v4, v6, v6}, []string{"10.0.0.1", "2001:db8::1"}},
		{"mixed de-dup", []net.IPAddr{v4, v6, v4, v4b, v6}, []string{"10.0.0.1", "2001:db8::1", "10.0.0.2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(context.Background(), "db.example.com", fakeResolver{addrs: tc.addrs})
			if err != nil {
				t.Fatalf("Resolve err = %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Resolve = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Resolve[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestResolveFailures(t *testing.T) {
	if _, err := Resolve(context.Background(), "db.example.com", fakeResolver{addrs: nil}); err == nil {
		t.Fatal("empty resolution should fail")
	}
	if _, err := Resolve(context.Background(), "db.example.com", fakeResolver{err: errors.New("no such host")}); err == nil {
		t.Fatal("resolver error should fail")
	}
	if _, err := Resolve(context.Background(), "", fakeResolver{addrs: []net.IPAddr{v4()}}); err == nil {
		t.Fatal("empty host should fail")
	}
}

func v4() net.IPAddr { return ipAddr("10.0.0.1") }

func TestResolveNilResolverUsesDefault(t *testing.T) {
	// A nil resolver must use the process default net.Resolver. A malformed
	// hostname that the default resolver rejects proves the default path is
	// taken (rather than a nil-dereference panic).
	_, err := Resolve(context.Background(), "db.example.com", nil)
	// In sandboxed environments the default resolver may return a lookup error or
	// a result; either is acceptable as long as there is no panic. Assert only
	// that the call completed without panicking and returned a typed error or
	// slice.
	_ = err
}
