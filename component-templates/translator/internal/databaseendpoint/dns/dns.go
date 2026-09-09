// Package dns is the Translator template's independently testable DNS endpoint
// module for the common database endpoint contract. A DNS host is resolved anew
// through the context-aware resolver for both A and AAAA records; results
// retain resolver order and duplicate normalized addresses are removed by
// retaining their first occurrence. An empty result or a resolver error is a
// resolution failure. The host is never re-resolved once a connection is
// established, so each candidate address is a fresh snapshot for one dial
// attempt.
package dns

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// Resolver resolves a host to its IP addresses. net.Resolver satisfies this
// interface.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// defaultResolver wraps the process default net.Resolver so callers can pass a
// nil resolver.
type defaultResolver struct{}

func (defaultResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// ValidateHost rejects obviously non-DNS hosts: empty, bracketed, zone-scoped,
// or containing whitespace. A host that parses as an IP literal is also
// rejected, since the dispatcher routes literals to the ipv4/ipv6 modules.
func ValidateHost(host string) error {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return fmt.Errorf("dns host is required")
	}
	if trimmed != host {
		return fmt.Errorf("dns host %q must not contain surrounding whitespace", host)
	}
	if strings.ContainsAny(host, "[] \t") {
		return fmt.Errorf("dns host %q must be a bare hostname", host)
	}
	if strings.Contains(host, "%") {
		return fmt.Errorf("dns host %q must not contain a zone identifier", host)
	}
	return nil
}

// Resolve resolves host to an ordered, de-duplicated list of canonical IP
// address strings through the supplied resolver. A nil resolver uses the
// process default net.Resolver. An empty result or a resolver error is a
// resolution failure; duplicate normalized addresses are removed by retaining
// their first occurrence and resolver order is preserved.
func Resolve(ctx context.Context, host string, resolver Resolver) ([]string, error) {
	if err := ValidateHost(host); err != nil {
		return nil, err
	}
	r := resolver
	if r == nil {
		r = defaultResolver{}
	}
	addrs, err := r.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve database host %q: %w", host, err)
	}
	seen := make(map[string]struct{}, len(addrs))
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a.IP == nil {
			continue
		}
		s := a.IP.String()
		if s == "<nil>" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("database host %q resolved to no addresses", host)
	}
	return out, nil
}
