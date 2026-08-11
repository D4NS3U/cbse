package coredb

import (
	"os"
	"testing"
)

func requireEnv(t *testing.T, keys ...string) {
	t.Helper()

	for _, key := range keys {
		if os.Getenv(key) == "" {
			t.Skipf("Environment variable %s is not set; skipping Core DB integration test", key)
		}
	}
}
