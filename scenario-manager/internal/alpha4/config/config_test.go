package config

import (
	"os"
	"strconv"
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
