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

// Package ipv4 is the Translator template's independently testable IPv4 address
// module for the common database endpoint contract. A literal IPv4 host is
// normalized through netip to its canonical form and yields exactly one
// candidate address with no DNS resolution.
package ipv4

import (
	"fmt"
	"net/netip"
	"strings"
)

// Normalize validates that host is a literal IPv4 address without a zone and
// returns its canonical netip string form. Bracketed, zone-scoped, or
// non-IPv4 values are rejected.
func Normalize(host string) (string, error) {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return "", fmt.Errorf("ipv4 host is required")
	}
	if strings.HasPrefix(trimmed, "[") || strings.HasSuffix(trimmed, "]") {
		return "", fmt.Errorf("ipv4 host %q must be an unbracketed address", trimmed)
	}
	if strings.Contains(trimmed, "%") {
		return "", fmt.Errorf("ipv4 host %q must not contain a zone identifier", trimmed)
	}
	addr, err := netip.ParseAddr(trimmed)
	if err != nil {
		return "", fmt.Errorf("ipv4 host %q is not a literal IPv4 address: %w", trimmed, err)
	}
	if !addr.Is4() {
		return "", fmt.Errorf("ipv4 host %q is not an IPv4 address", trimmed)
	}
	return addr.String(), nil
}

// Addresses returns the single normalized candidate address for a literal IPv4
// host. It is a convenience over Normalize for the dispatcher.
func Addresses(host string) ([]string, error) {
	normalized, err := Normalize(host)
	if err != nil {
		return nil, err
	}
	return []string{normalized}, nil
}
