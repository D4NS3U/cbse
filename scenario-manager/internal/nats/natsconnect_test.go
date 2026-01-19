package nats

import (
	"fmt"
	"os"
	"testing"
)

func TestConnectEstablishesSharedConnection(t *testing.T) {
	if os.Getenv(natsURLEnv) == "" {
		t.Skipf("Environment variable %s is not set; skipping NATS integration test", natsURLEnv)
	}

	t.Log("Starting NATS integration test...")

	var errs []string
	if !Connect() {
		errs = append(errs, "Connect returned false; verify the NATS broker is reachable at "+os.Getenv(natsURLEnv))
	} else {
		t.Cleanup(func() {
			connMu.Lock()
			defer connMu.Unlock()
			if natsConn != nil {
				natsConn.Close()
				natsConn = nil
			}
		})

		conn := Connection()
		if conn == nil || !conn.IsConnected() {
			errs = append(errs, fmt.Sprintf("Connection returned %v; expected an active NATS client", conn))
		}
	}

	if len(errs) > 0 {
		for _, err := range errs {
			t.Logf("NATS integration error: %s", err)
		}
		t.Fatalf("NATS integration test failed with %d error(s)", len(errs))
	}

	t.Log("NATS integration test succeeded.")
}
