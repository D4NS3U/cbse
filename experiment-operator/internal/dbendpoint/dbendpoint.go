// Package dbendpoint owns the common database endpoint-resolution contract
// shared by the Experiment Operator availability probe, the Translator Detail
// DB client, and the generated runner Result DB client.
//
// Only the Operator availability probe lives in this slice. It resolves an
// alpha4 DatabaseSpec host to a single address, opens one connection, runs
// SELECT 1 exactly once, closes the connection, and retains no pool. The
// resolution and connection seams are interfaces so callers (and tests) can
// inject a resolver and connector without a live DNS server or PostgreSQL.
package dbendpoint

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// HostKind classifies a normalized database host.
type HostKind int

const (
	// HostDNS is a lowercase DNS subdomain host.
	HostDNS HostKind = iota
	// HostIPv4 is a literal IPv4 address.
	HostIPv4
	// HostIPv6 is a literal IPv6 address without a zone identifier.
	HostIPv6
)

// String renders the kind for diagnostic messages.
func (k HostKind) String() string {
	switch k {
	case HostIPv4:
		return "ipv4"
	case HostIPv6:
		return "ipv6"
	default:
		return "dns"
	}
}

// Classified holds a normalized database host and its kind.
type Classified struct {
	Kind       HostKind
	Normalized string
}

// ClassifyHost trims and classifies a database host per the alpha4 common
// endpoint contract.
//
// An address accepted by netip.ParseAddr without a zone selects the IPv4 or
// IPv6 path according to Addr.Is4 or Addr.Is6 and is normalized with
// Addr.String. Any other non-empty value must be accepted by
// validation.IsDNS1123Subdomain and is preserved verbatim. Empty, bracketed,
// zone-scoped, URL-scheme, path, query, fragment, embedded-port, and
// Unix-socket hosts are rejected.
func ClassifyHost(host string) (Classified, error) {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return Classified{}, errors.New("database host is required")
	}
	if strings.HasPrefix(trimmed, "[") || strings.HasSuffix(trimmed, "]") {
		return Classified{}, fmt.Errorf("database host %q must be an unbracketed address", trimmed)
	}
	if strings.ContainsAny(trimmed, "/?#") {
		return Classified{}, fmt.Errorf("database host %q must not contain a scheme, path, query, or fragment", trimmed)
	}
	if strings.Contains(trimmed, "%") {
		return Classified{}, fmt.Errorf("database host %q must not contain an IPv6 zone identifier", trimmed)
	}
	if addr, err := netip.ParseAddr(trimmed); err == nil {
		// ParseAddr accepts a trailing zone for IPv6 (for example fe80::1%eth0);
		// the leading check above rejects any "%", but defend in depth.
		if addr.Zone() != "" {
			return Classified{}, fmt.Errorf("database host %q must not contain an IPv6 zone identifier", trimmed)
		}
		return Classified{Kind: addrKind(addr), Normalized: addr.String()}, nil
	}
	// Not a literal address. A DNS subdomain contains no colon, so a colon here
	// can only be an embedded port or an unbracketed IPv6 literal that
	// netip.ParseAddr already rejected.
	if strings.Contains(trimmed, ":") {
		return Classified{}, fmt.Errorf("database host %q must not contain an embedded port", trimmed)
	}
	if msgs := validation.IsDNS1123Subdomain(trimmed); len(msgs) != 0 {
		return Classified{}, fmt.Errorf("database host %q is not a valid lowercase DNS subdomain: %s", trimmed, strings.Join(msgs, ", "))
	}
	return Classified{Kind: HostDNS, Normalized: trimmed}, nil
}

func addrKind(addr netip.Addr) HostKind {
	if addr.Is4() {
		return HostIPv4
	}
	return HostIPv6
}

// Resolver resolves a DNS name to IP addresses. A nil Resolver uses the
// process default net.Resolver.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type defaultResolver struct{}

func (defaultResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// Conn is a single database connection used by the availability probe. It
// exposes only the SELECT 1 ping and a close, so the probe can perform no
// other SQL.
type Conn interface {
	// Ping runs SELECT 1 and returns nil only on a successful result.
	Ping(ctx context.Context) error
	// Close releases the single connection.
	Close(ctx context.Context) error
}

// Connector opens a single database connection to a resolved host. It must
// not maintain a pool: each Connect returns a connection the caller closes.
type Connector interface {
	Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error)
}

// Endpoint is a database endpoint for an availability probe.
type Endpoint struct {
	Host     string
	Port     int32
	User     string
	Password string
	DBName   string
}

// Probe resolves the endpoint, opens a single connection, runs SELECT 1, and
// closes the connection.
//
// It performs exactly one connect, one ping, and one close, performs no other
// SQL, and retains no pool. A DNS host that resolves to no address, or a
// resolver error, is a probe failure: the probe does not connect. IP hosts
// skip DNS resolution and connect directly to the normalized address.
func Probe(ctx context.Context, ep Endpoint, resolver Resolver, connector Connector) error {
	if connector == nil {
		return errors.New("database availability probe requires a connector")
	}
	classified, err := ClassifyHost(ep.Host)
	if err != nil {
		return err
	}
	host := classified.Normalized
	if classified.Kind == HostDNS {
		r := Resolver(resolver)
		if r == nil {
			r = defaultResolver{}
		}
		addrs, err := r.LookupIPAddr(ctx, host)
		if err != nil {
			return fmt.Errorf("resolve database host %q: %w", host, err)
		}
		if len(addrs) == 0 {
			return fmt.Errorf("database host %q resolved to no addresses", host)
		}
		// Use the first resolved address; duplicates and alternates are ignored
		// for an availability-only probe.
		host = addrs[0].IP.String()
	}
	conn, err := connector.Connect(ctx, host, ep.Port, ep.User, ep.Password, ep.DBName)
	if err != nil {
		return fmt.Errorf("connect database %s:%d: %w", host, ep.Port, err)
	}
	pingErr := conn.Ping(ctx)
	closeErr := conn.Close(ctx)
	if pingErr != nil {
		return fmt.Errorf("probe database %s:%d: %w", host, ep.Port, pingErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close database probe %s:%d: %w", host, ep.Port, closeErr)
	}
	return nil
}
