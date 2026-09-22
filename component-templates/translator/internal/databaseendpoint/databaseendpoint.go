// Package databaseendpoint is the Translator template's common database
// endpoint contract dispatcher. It classifies a database host as a DNS name or
// an IPv4/IPv6 literal, resolves DNS hosts to an ordered, de-duplicated address
// list through the dns module, and dials candidate addresses in resolver order
// under a shared 10-second deadline; the first successful PostgreSQL
// connection owns the operation and remaining addresses are not contacted.
//
// The contract is shared verbatim by the Experiment Operator availability
// probe and the generated Python runner Result DB client so that all three
// clients classify, order, de-duplicate, time-bound, and fall back identically.
// This package is pgx-free: the connection transport is supplied by the caller
// through the Connector interface.
package databaseendpoint

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/D4NS3U/cbse/component-templates/translator/internal/databaseendpoint/dns"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/databaseendpoint/ipv4"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/databaseendpoint/ipv6"
)

// HostKind identifies how a database host is resolved.
type HostKind int

const (
	// HostDNS is a DNS hostname resolved through A and AAAA records.
	HostDNS HostKind = iota
	// HostIPv4 is a literal IPv4 address contacted directly.
	HostIPv4
	// HostIPv6 is a literal IPv6 address contacted directly.
	HostIPv6
)

// Classified holds a normalized database host and its kind.
type Classified struct {
	Kind       HostKind
	Normalized string
}

// Endpoint is a database connection target with separate host and port driver
// values and PostgreSQL credentials.
type Endpoint struct {
	Host     string
	Port     int32
	User     string
	Password string
	DBName   string
}

// Conn is an established PostgreSQL connection used for a single query set and
// then closed.
type Conn interface {
	Ping(ctx context.Context) error
	Close(ctx context.Context) error
}

// Connector opens a PostgreSQL connection to a resolved candidate address.
// Implementations must honor the supplied context so the shared deadline
// aborts a blocked dial.
type Connector interface {
	Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error)
}

// Resolver resolves a DNS name to IP addresses. net.Resolver satisfies this
// interface.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// resolutionDeadline is the shared 10-second deadline covering DNS resolution
// and all candidate connection attempts for one dial, as required by the
// alpha4 common endpoint contract.
const resolutionDeadline = 10 * time.Second

// ClassifyHost trims and classifies a database host per the alpha4 common
// endpoint contract.
//
// An address accepted by netip.ParseAddr without a zone selects the IPv4 or
// IPv6 path and is normalized with Addr.String. Any other non-empty value must
// be a lowercase RFC 1123 DNS subdomain and is preserved verbatim. Empty,
// bracketed, zone-scoped, URL-scheme, path, query, fragment, embedded-port, and
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
	if !isDNS1123Subdomain(trimmed) {
		return Classified{}, fmt.Errorf("database host %q is not a valid lowercase DNS subdomain", trimmed)
	}
	return Classified{Kind: HostDNS, Normalized: trimmed}, nil
}

func addrKind(addr netip.Addr) HostKind {
	if addr.Is4() {
		return HostIPv4
	}
	return HostIPv6
}

// isDNS1123Subdomain reports whether s is a lowercase RFC 1123 DNS subdomain:
// one to 253 characters total, with one or more '.'-separated labels of 1 to 63
// characters, each matching ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$. This mirrors the
// Kubernetes validation rule used by the Experiment Operator probe so all three
// clients accept the same hostname grammar.
func isDNS1123Subdomain(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '.' {
			label := s[start:i]
			if !isDNS1123Label(label) {
				return false
			}
			start = i + 1
		}
	}
	return true
}

// isDNS1123Label reports whether label matches ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
// and is 1 to 63 characters long.
func isDNS1123Label(label string) bool {
	if len(label) == 0 || len(label) > 63 {
		return false
	}
	if !isAlphaNum(label[0]) {
		return false
	}
	if len(label) == 1 {
		return true
	}
	if !isAlphaNum(label[len(label)-1]) {
		return false
	}
	for i := 1; i < len(label)-1; i++ {
		c := label[i]
		if !(isAlphaNum(c) || c == '-') {
			return false
		}
	}
	return true
}

func isAlphaNum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

// ResolveAddresses resolves a classified database host to an ordered,
// de-duplicated list of canonical IP address strings. A literal IPv4 or IPv6
// address yields a single normalized address without DNS. A DNS host is
// resolved anew through the context-aware resolver for both A and AAAA records
// by the dns module; results retain resolver order and duplicate normalized
// addresses are removed by retaining their first occurrence. An empty result or
// a resolver error is a resolution failure. A nil resolver uses the process
// default net.Resolver.
func ResolveAddresses(ctx context.Context, classified Classified, resolver Resolver) ([]string, error) {
	switch classified.Kind {
	case HostIPv4:
		return ipv4.Addresses(classified.Normalized)
	case HostIPv6:
		return ipv6.Addresses(classified.Normalized)
	default:
		return dns.Resolve(ctx, classified.Normalized, resolver)
	}
}

// DialResult is the outcome of a successful Dial: the established connection
// and the candidate address that owns it. The address is returned so callers
// can pin a connection to one resolved address and never re-resolve an
// established connection.
type DialResult struct {
	Conn Conn
	Host string
	Kind HostKind
}

// Dial classifies the endpoint, resolves it to an ordered, de-duplicated
// address list, and tries addresses in resolver order under a shared 10-second
// resolution-and-connection deadline. The first successful PostgreSQL
// connection owns the dial; remaining addresses are not contacted. A DNS host
// that resolves to no address, a resolver error, or the exhaustion of every
// returned address is a dial failure. The connection is NOT closed by Dial: the
// caller owns Ping and Close.
func Dial(ctx context.Context, ep Endpoint, resolver Resolver, connector Connector) (DialResult, error) {
	if connector == nil {
		return DialResult{}, errors.New("database dial requires a connector")
	}
	conn, host, kind, err := DialConn[Conn](ctx, ep, resolver, connector.Connect)
	if err != nil {
		return DialResult{}, err
	}
	return DialResult{Conn: conn, Host: host, Kind: kind}, nil
}

// DialConn is the generic, transport-agnostic form of the common endpoint
// contract dial loop. It classifies the endpoint, resolves it to an ordered,
// de-duplicated address list, and calls connect for each address in resolver
// order under a shared 10-second deadline; the first successful connection
// owns the dial and remaining addresses are not contacted. The connect
// callback must honor its context so the shared deadline aborts a blocked
// dial. It returns the connection, the owning candidate address, and the
// classified host kind. The caller owns the connection's lifecycle; DialConn
// does not close it.
func DialConn[T any](ctx context.Context, ep Endpoint, resolver Resolver, connect func(ctx context.Context, host string, port int32, user, password, dbname string) (T, error)) (T, string, HostKind, error) {
	var zero T
	classified, err := ClassifyHost(ep.Host)
	if err != nil {
		return zero, "", HostDNS, err
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, resolutionDeadline)
	defer cancel()
	addresses, err := ResolveAddresses(deadlineCtx, classified, resolver)
	if err != nil {
		return zero, "", classified.Kind, err
	}
	dl, _ := deadlineCtx.Deadline()
	var connectErr error
	for _, host := range addresses {
		conn, err := connect(deadlineCtx, host, ep.Port, ep.User, ep.Password, ep.DBName)
		if err != nil {
			connectErr = err
			if !dl.IsZero() && time.Now().After(dl) {
				break
			}
			continue
		}
		return conn, host, classified.Kind, nil
	}
	return zero, "", classified.Kind, fmt.Errorf("connect database %s:%d: %w", classified.Normalized, ep.Port, connectErr)
}
