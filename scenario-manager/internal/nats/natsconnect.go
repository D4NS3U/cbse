// Package nats encapsulates the Scenario Manager's connection handling and
// configuration normalization for communicating with the NATS message broker.
package nats

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	natsgo "github.com/nats-io/nats.go"
)

const (
	natsURLEnv      = "SCENARIO_MANAGER_NATS_URL"
	natsUserEnv     = "SCENARIO_MANAGER_NATS_USER"
	natsPasswordEnv = "SCENARIO_MANAGER_NATS_PASSWORD"
)

var (
	// connMu guards access to the shared NATS connection so callers can safely
	// reuse it across goroutines.
	connMu sync.RWMutex
	// natsConn stores the established broker connection, allowing other packages
	// to publish or subscribe without re-dialing.
	natsConn *natsgo.Conn
)

// Connect ensures a shared connection to the configured NATS broker, applying
// environment derived defaults suitable for in-cluster service endpoints.
// Scenario Manager startup treats a failure to connect as a critical error.
func Connect() bool {
	connMu.Lock()
	defer connMu.Unlock()

	if connectionHealthy() {
		return true
	}

	if natsConn != nil {
		natsConn.Close()
		natsConn = nil
	}

	rawEndpoint := os.Getenv(natsURLEnv)
	if rawEndpoint == "" {
		log.Printf("%s is not set", natsURLEnv)
		return false
	}
	endpoint := normalizeEndpoint(rawEndpoint)

	opts := []natsgo.Option{
		natsgo.Name("Scenario Manager"),
	}

	user := os.Getenv(natsUserEnv)
	password := os.Getenv(natsPasswordEnv)
	switch {
	case user != "" && password != "":
		opts = append(opts, natsgo.UserInfo(user, password))
	case user != "" && password == "":
		log.Printf("%s is set but %s is empty", natsUserEnv, natsPasswordEnv)
	case user == "" && password != "":
		log.Printf("%s is set but %s is empty", natsPasswordEnv, natsUserEnv)
	}

	conn, err := natsgo.Connect(endpoint, opts...)
	if err != nil {
		log.Printf("Failed to connect to NATS: %v", err)
		return false
	}

	natsConn = conn
	log.Println("NATS connection established.")
	return true
}

// Connection returns the shared NATS connection reference.
func Connection() *natsgo.Conn {
	connMu.RLock()
	defer connMu.RUnlock()
	return natsConn
}

// connectionHealthy reports whether the cached connection is both present and
// currently marked as connected by the NATS client.
func connectionHealthy() bool {
	return natsConn != nil && natsConn.IsConnected()
}

// normalizeEndpoint accepts raw environment input that may only contain a
// Kubernetes service name (optionally with port) and returns a fully-qualified
// NATS URL that the client library accepts.
func normalizeEndpoint(value string) string {
	if strings.Contains(value, "://") {
		return value
	}

	if strings.Contains(value, ":") {
		return fmt.Sprintf("nats://%s", value)
	}

	return fmt.Sprintf("nats://%s:4222", value)
}
