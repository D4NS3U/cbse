package coredb

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestCoreDBConnectAndEnsureTables(t *testing.T) {
	requireEnv(t, coreDBDSNEnv, coreDBUserEnv, coreDBPasswordEnv)

	if !CoreDBConnect() {
		t.Fatalf("CoreDBConnect returned false; ensure the database is reachable and credentials are valid")
	}

	t.Cleanup(func() {
		if coreDBPool != nil {
			_ = coreDBPool.Close()
			coreDBPool = nil
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if !EnsureTablesAvailable(ctx) {
		t.Fatalf("EnsureTablesAvailable reported missing schema despite a reachable database")
	}
}

func requireEnv(t *testing.T, keys ...string) {
	t.Helper()

	for _, key := range keys {
		if os.Getenv(key) == "" {
			t.Skipf("Environment variable %s is not set; skipping Core DB integration test", key)
		}
	}
}
