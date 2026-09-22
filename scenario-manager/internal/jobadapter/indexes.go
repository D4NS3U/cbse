package jobadapter

import (
	"fmt"
	"strconv"
	"strings"
)

// parseCompletedIndexes parses the Kubernetes compressed-index syntax used in
// job.status.completedIndexes and returns the count of represented indexes.
// It validates that every interval is a non-negative decimal, that ranges are
// ordered (start <= end), that intervals are strictly increasing and
// non-overlapping (each start is strictly greater than the previous end), and
// that every index is within [0, maxIndex]. It counts the represented indexes
// by summing interval lengths without expanding them into a slice, so a
// 100000-repetition Job with completedIndexes "0-99999" counts in O(intervals).
//
// An empty string is a valid zero-count result. A malformed or out-of-range
// string is invalid Job status and the caller treats it as a scenario failure.
func parseCompletedIndexes(completedIndexes string, maxIndex int) (int, error) {
	if completedIndexes == "" {
		return 0, nil
	}
	if maxIndex < 0 {
		return 0, fmt.Errorf("maxIndex %d is negative", maxIndex)
	}
	var (
		count   int
		lastEnd = -1
	)
	for i, part := range strings.Split(completedIndexes, ",") {
		if part == "" {
			return 0, fmt.Errorf("completedIndexes interval %d is empty", i)
		}
		lo, hi, err := parseInterval(part)
		if err != nil {
			return 0, fmt.Errorf("completedIndexes interval %d (%q): %w", i, part, err)
		}
		if hi > maxIndex {
			return 0, fmt.Errorf("completedIndexes interval %d (%q) exceeds max index %d", i, part, maxIndex)
		}
		if lo <= lastEnd {
			return 0, fmt.Errorf("completedIndexes interval %d (%q) overlaps or precedes the previous end %d", i, part, lastEnd)
		}
		count += hi - lo + 1
		lastEnd = hi
	}
	return count, nil
}

// parseInterval parses one comma-separated item, either "N" or "N-M", and
// returns the inclusive [lo, hi] bounds.
func parseInterval(part string) (lo, hi int, err error) {
	if strings.Contains(part, "-") {
		dash := strings.IndexByte(part, '-')
		lo, err = parseIndex(part[:dash])
		if err != nil {
			return 0, 0, err
		}
		hi, err = parseIndex(part[dash+1:])
		if err != nil {
			return 0, 0, err
		}
		if lo > hi {
			return 0, 0, fmt.Errorf("range %d-%d has start greater than end", lo, hi)
		}
		return lo, hi, nil
	}
	lo, err = parseIndex(part)
	if err != nil {
		return 0, 0, err
	}
	return lo, lo, nil
}

// parseIndex parses a non-negative decimal integer with no sign or whitespace.
func parseIndex(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty index")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("index %q is not a non-negative decimal", s)
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("index %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("index %q is negative", s)
	}
	return n, nil
}
