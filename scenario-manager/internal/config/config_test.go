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

package config

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestParseRunnerStartWorkersAbsent(t *testing.T) {
	if got, err := ParseRunnerStartWorkers(""); err != nil || got != DefaultRunnerStartWorkers {
		t.Fatalf("absent -> (%d, %v), want (%d, nil)", got, err, DefaultRunnerStartWorkers)
	}
}

func TestParseRunnerStartWorkersAccepted(t *testing.T) {
	for _, v := range []string{"1", "4", "64"} {
		got, err := ParseRunnerStartWorkers(v)
		if err != nil {
			t.Fatalf("value %q: unexpected error %v", v, err)
		}
		want, _ := strconv.Atoi(v)
		if got != want {
			t.Fatalf("value %q -> %d, want %d", v, got, want)
		}
	}
}

func TestParseRunnerStartWorkersRejected(t *testing.T) {
	cases := []string{
		"0",    // zero
		"-1",   // negative
		"-64",  // negative in range magnitude
		"65",   // above max
		"100",  // above max
		"abc",  // malformed text
		"1.5",  // float
		" 4",   // leading whitespace
		"4 ",   // trailing whitespace
		"4\n",  // trailing newline
		"+4",   // leading sign
		"0x10", // hex
		"1e1",  // exponent
		"四",    // non-ASCII digit
		"",     // empty handled as default, but double-check rejection path is empty -> default not reject
	}
	for _, v := range cases {
		_, err := ParseRunnerStartWorkers(v)
		// Empty is the default case, not a rejection.
		if v == "" {
			continue
		}
		if err == nil {
			t.Fatalf("value %q: expected error, got nil", v)
		}
	}
}

func TestParseRunnerStartWorkersZeroFails(t *testing.T) {
	if _, err := ParseRunnerStartWorkers("0"); err == nil {
		t.Fatal("0 must fail (below minimum)")
	}
}

func TestParseRunnerStartWorkersAboveMaxFails(t *testing.T) {
	if _, err := ParseRunnerStartWorkers("65"); err == nil {
		t.Fatal("65 must fail (above maximum)")
	}
}

func TestRunnerStartWorkersReadsEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		want int
		err  bool
	}{
		{"absent", "", 4, false},
		{"one", "1", 1, false},
		{"four", "4", 4, false},
		{"sixtyfour", "64", 64, false},
		{"zero", "0", 0, true},
		{"negative", "-1", 0, true},
		{"above", "65", 0, true},
		{"malformed", "oops", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prev, wasSet := os.LookupEnv(RunnerStartWorkersEnv)
			defer func() {
				if wasSet {
					os.Setenv(RunnerStartWorkersEnv, prev)
				} else {
					os.Unsetenv(RunnerStartWorkersEnv)
				}
			}()
			if tc.env == "" {
				os.Unsetenv(RunnerStartWorkersEnv)
			} else {
				os.Setenv(RunnerStartWorkersEnv, tc.env)
			}
			got, err := RunnerStartWorkers()
			if tc.err {
				if err == nil {
					t.Fatalf("RunnerStartWorkers() = %d, nil; want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("RunnerStartWorkers() = _, %v; want nil", err)
			}
			if got != tc.want {
				t.Fatalf("RunnerStartWorkers() = %d; want %d", got, tc.want)
			}
		})
	}
}

// TestParseWatchNamespaceAcceptsDNSLabel pins the accepted watch-namespace
// shapes: lowercase DNS labels of 1 to 63 characters.
func TestParseWatchNamespaceAcceptsDNSLabel(t *testing.T) {
	for _, want := range []string{"cbse-system", "a", "ns-2", "x9-abc", "a" + strings.Repeat("b", 62)} {
		got, err := ParseWatchNamespace(want)
		if err != nil {
			t.Fatalf("ParseWatchNamespace(%q) = _, %v; want ok", want, err)
		}
		if got != want {
			t.Fatalf("ParseWatchNamespace(%q) = %q; want unchanged", want, got)
		}
	}
}

// TestParseWatchNamespaceRejectsMalformed pins the fatal startup handling for
// every malformed watch-namespace shape: absent, empty, non-DNS-label, and
// over-length values.
func TestParseWatchNamespaceRejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"",                            // absent / empty
		"CBSE-SYSTEM",                 // uppercase
		"cbse_system",                 // underscore
		"-cbse",                       // leading hyphen
		"cbse-",                       // trailing hyphen
		"cbse system",                 // whitespace inside
		" cbse",                       // leading whitespace
		"cbse ",                       // trailing whitespace
		"a" + strings.Repeat("b", 63), // 64 characters
	} {
		if _, err := ParseWatchNamespace(bad); err == nil {
			t.Fatalf("ParseWatchNamespace(%q) = ok; want error", bad)
		}
	}
}

// TestWatchNamespaceReadsProcessEnv pins that WatchNamespace consumes
// SCENARIO_MANAGER_WATCH_NAMESPACE from the process environment.
func TestWatchNamespaceReadsProcessEnv(t *testing.T) {
	t.Setenv(WatchNamespaceEnv, "cbse-system")
	got, err := WatchNamespace()
	if err != nil {
		t.Fatalf("WatchNamespace() = _, %v; want ok", err)
	}
	if got != "cbse-system" {
		t.Fatalf("WatchNamespace() = %q; want cbse-system", got)
	}
}
