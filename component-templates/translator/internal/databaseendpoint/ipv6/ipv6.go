// Package ipv6 is the Translator template's independently testable IPv6 address
// module for the common database endpoint contract. A literal IPv6 host is
// normalized through netip to its canonical form and yields exactly one
// candidate address with no DNS resolution. Bracketed and zone-scoped forms
// are rejected so an established connection cannot be re-resolved to a
// different scope.
package ipv6

import (
	"fmt"
	"net/netip"
	"strings"
)

// Normalize validates that host is a literal IPv6 address without brackets or a
// zone identifier and returns its canonical netip string form.
func Normalize(host string) (string, error) {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return "", fmt.Errorf("ipv6 host is required")
	}
	if strings.HasPrefix(trimmed, "[") || strings.HasSuffix(trimmed, "]") {
		return "", fmt.Errorf("ipv6 host %q must be an unbracketed address", trimmed)
	}
	if strings.Contains(trimmed, "%") {
		return "", fmt.Errorf("ipv6 host %q must not contain a zone identifier", trimmed)
	}
	addr, err := netip.ParseAddr(trimmed)
	if err != nil {
		return "", fmt.Errorf("ipv6 host %q is not a literal IPv6 address: %w", trimmed, err)
	}
	if !addr.Is6() {
		return "", fmt.Errorf("ipv6 host %q is not an IPv6 address", trimmed)
	}
	if addr.Is4In6() {
		return "", fmt.Errorf("ipv6 host %q is an IPv4-mapped address", trimmed)
	}
	return addr.String(), nil
}

// Addresses returns the single normalized candidate address for a literal IPv6
// host.
func Addresses(host string) ([]string, error) {
	normalized, err := Normalize(host)
	if err != nil {
		return nil, err
	}
	return []string{normalized}, nil
}
