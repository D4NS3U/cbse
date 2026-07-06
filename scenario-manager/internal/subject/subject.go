// Package subject contains shared helpers for broker subject construction and
// validation.
//
// These helpers intentionally normalize only transport-facing tokens. Database
// records keep raw project names exactly as Kubernetes provided them, while
// broker subjects use a safe restricted alphabet that matches the existing EDS
// behavior.
package subject

import "strings"

// NormalizeToken converts arbitrary input into the restricted token form used
// in broker subjects.
//
// The normalization rules intentionally mirror the long-standing EDS behavior:
// the token is lowercased, ASCII letters and digits are preserved, '-' and '_'
// remain unchanged, and every other rune is replaced with '-'. Empty input
// returns an empty string so callers can still detect missing project values.
func NormalizeToken(value string) string {
	if value == "" {
		return ""
	}

	normalized := strings.ToLower(value)
	out := make([]rune, 0, len(normalized))
	for _, r := range normalized {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, r)
		case r >= '0' && r <= '9':
			out = append(out, r)
		case r == '-' || r == '_':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}

	return string(out)
}
