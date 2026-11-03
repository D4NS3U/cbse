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

// Package health implements the Scenario Manager's explicit-dependency
// health and readiness endpoints.
//
// The server serves two unauthenticated, Pod-local HTTP endpoints (no Service
// exposes them):
//
//	GET /healthz — 200 while the process and server are alive; no dependency
//	probing on every request.
//	GET /readyz — 503 until the Scenario Manager's readiness state is ready,
//	200 afterwards.
//
// The readiness state is an explicit dependency, not package-global mutable
// state: cmd/main.go constructs the Readiness, passes it to the Server, and
// passes it to core.RunScenarioManager, which calls Ready() at the exact
// point immediately before the existing "Scenario Manager is ready" log and
// NotReady() before the shutdown join. Handler responses contain only the
// fixed bodies "ok" and "not ready" — never configuration or credentials.
package health

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// AddressEnv names the environment variable that configures the health
// server listen address.
const AddressEnv = "SCENARIO_MANAGER_HEALTH_ADDRESS"

// DefaultAddress is the listen address when AddressEnv is absent or empty.
const DefaultAddress = ":8080"

// shutdownTimeout bounds the server shutdown after the root context is done
// so Start always returns within a finite window.
const shutdownTimeout = 10 * time.Second

// Address resolves the health server listen address: an empty value (absent
// or empty environment variable) resolves to DefaultAddress.
func Address(value string) string {
	if value == "" {
		return DefaultAddress
	}
	return value
}

// Readiness is the explicit-dependency readiness state the Scenario Manager
// reports through the /readyz endpoint. It carries no configuration and no
// credentials — only the 503/200 transition itself.
type Readiness struct {
	ready atomic.Bool
}

// NewReadiness returns a Readiness in the not-ready state.
func NewReadiness() *Readiness {
	return &Readiness{}
}

// Ready flips the state to ready. It is idempotent and safe for concurrent
// use.
func (r *Readiness) Ready() {
	r.ready.Store(true)
}

// NotReady flips the state to not ready. It is idempotent, safe for
// concurrent use, and safe before Ready was ever called.
func (r *Readiness) NotReady() {
	r.ready.Store(false)
}

// IsReady reports whether the Scenario Manager is ready.
func (r *Readiness) IsReady() bool {
	return r.ready.Load()
}

// ReadinessState is the readiness-reporting surface the health server
// consumes. *Readiness satisfies it.
type ReadinessState interface {
	IsReady() bool
}

// Server serves the health endpoints over the given readiness state.
type Server struct {
	state ReadinessState
}

// NewServer constructs a Server over the given readiness state. A nil state
// is a wiring error.
func NewServer(state ReadinessState) (*Server, error) {
	if state == nil {
		return nil, errors.New("health: readiness state must not be nil")
	}
	return &Server{state: state}, nil
}

// Handler returns the HTTP handler for the health endpoints. Unknown paths
// answer 404 with an empty body, keeping handler responses within the fixed
// ok / not ready vocabulary.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleLive)
	mux.HandleFunc("/readyz", s.handleReady)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			mux.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
}

// handleLive answers GET /healthz with 200 while the server is alive. It
// performs no dependency probing.
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeBody(w, http.StatusOK, "ok")
}

// handleReady answers GET /readyz with 503 until the readiness state is
// ready and 200 afterwards.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if !s.state.IsReady() {
		writeBody(w, http.StatusServiceUnavailable, "not ready")
		return
	}
	writeBody(w, http.StatusOK, "ok")
}

// writeBody writes the fixed one-line response body as text. The body is
// always exactly "ok" or "not ready"; handler responses never carry
// configuration or credentials.
func writeBody(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintln(w, body)
}

// methodNotAllowed answers non-GET requests with 405, the Allow header, and
// an empty body so handler responses stay within the fixed ok / not ready
// vocabulary.
func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", http.MethodGet)
	w.WriteHeader(http.StatusMethodNotAllowed)
}

// Start listens on address (an empty address resolves to DefaultAddress) and
// serves until ctx is done. A malformed address or a serve failure before
// ctx is done returns an error; the caller treats it as fatal. Once ctx is
// done the server shuts down within a bounded timeout and Start returns, so
// no server goroutine outlives the caller.
func (s *Server) Start(ctx context.Context, address string) error {
	listenAddr := Address(address)
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("health: listen on %q: %w", listenAddr, err)
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(ln)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		err := srv.Shutdown(shutdownCtx)
		cancel()
		// If the bounded shutdown could not drain, hard-close so the serve
		// goroutine is guaranteed to return within the bound.
		if err != nil {
			_ = srv.Close()
		}
		if serveErr := <-serveErr; serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && err == nil {
			return fmt.Errorf("health: serve: %w", serveErr)
		}
		if err != nil {
			return fmt.Errorf("health: shutdown: %w", err)
		}
		return nil
	case err := <-serveErr:
		_ = ln.Close()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("health: serve: %w", err)
		}
		return nil
	}
}
