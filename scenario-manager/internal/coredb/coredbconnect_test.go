package coredb

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestCoreDBConnectAndEnsureTables(t *testing.T) {
	requireEnv(t, coreDBDSNEnv, coreDBUserEnv, coreDBPasswordEnv)

	t.Log("Starting Core DB integration test...")

	var errs []string
	if !CoreDBConnect() {
		errs = append(errs, "CoreDBConnect returned false; ensure the database is reachable and credentials are valid")
	} else {
		t.Cleanup(func() {
			if coreDBPool != nil {
				_ = coreDBPool.Close()
				coreDBPool = nil
			}
		})

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if !EnsureTablesAvailable(ctx) {
			errs = append(errs, "EnsureTablesAvailable reported missing schema despite a reachable database")
		}
	}

	if len(errs) > 0 {
			for _, err := range errs {
				t.Logf("Core DB integration error: %s", err)
			}
			t.Fatalf("Core DB integration test failed with %d error(s)", len(errs))
		}

		t.Log("Core DB integration test succeeded.")
}

func requireEnv(t *testing.T, keys ...string) {
	t.Helper()

	for _, key := range keys {
		if os.Getenv(key) == "" {
			t.Skipf("Environment variable %s is not set; skipping Core DB integration test", key)
		}
	}
}
