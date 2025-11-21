package nats

import (
	"os"
	"testing"
)

func TestConnectEstablishesSharedConnection(t *testing.T) {
	if os.Getenv(natsURLEnv) == "" {
		t.Skipf("Environment variable %s is not set; skipping NATS integration test", natsURLEnv)
	}

	if !Connect() {
		t.Fatalf("Connect returned false; verify the NATS broker is reachable at %s", os.Getenv(natsURLEnv))
	}

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
		t.Fatalf("Connection returned %v; expected an active NATS client", conn)
	}
}
