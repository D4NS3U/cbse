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

package health

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestReadinessInitialStateNotReady pins the contract that a freshly
// constructed Readiness reports not ready: the /readyz endpoint must answer
// 503 until the Scenario Manager explicitly reports ready.
func TestReadinessInitialStateNotReady(t *testing.T) {
	r := NewReadiness()
	if r.IsReady() {
		t.Fatal("fresh Readiness must be not ready")
	}
}

// TestReadinessTransitionAndReentry covers the 503→200 transition, the
// 200→503 shutdown transition (NotReady after Ready), and idempotence of
// both transitions, including NotReady before any Ready.
func TestReadinessTransitionAndReentry(t *testing.T) {
	r := NewReadiness()
	r.Ready()
	if !r.IsReady() {
		t.Fatal("Readiness must be ready after Ready()")
	}
	r.Ready()
	if !r.IsReady() {
		t.Fatal("Ready() must be idempotent")
	}
	r.NotReady()
	if r.IsReady() {
		t.Fatal("Readiness must be not ready after NotReady()")
	}
	r.NotReady()
	if r.IsReady() {
		t.Fatal("NotReady() must be idempotent")
	}
	// NotReady before any Ready is safe and leaves the state not ready.
	r2 := NewReadiness()
	r2.NotReady()
	if r2.IsReady() {
		t.Fatal("NotReady() before Ready() must leave the state not ready")
	}
}

// TestLivenessEndpoint200WithoutReadiness pins the liveness contract:
// /healthz answers 200 "ok" while the server is alive, independently of the
// readiness state.
func TestLivenessEndpoint200WithoutReadiness(t *testing.T) {
	srv, err := NewServer(NewReadiness())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200; body %q", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "ok\n" {
		t.Fatalf("GET /healthz body = %q, want only \"ok\"", body)
	}
}

// TestReadyzEndpoint503BeforeReady pins the readiness contract: /readyz
// answers 503 "not ready" until the readiness state is ready.
func TestReadyzEndpoint503BeforeReady(t *testing.T) {
	readiness := NewReadiness()
	srv, err := NewServer(readiness)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz before Ready status = %d, want 503; body %q", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "not ready\n" {
		t.Fatalf("GET /readyz before Ready body = %q, want only \"not ready\"", body)
	}
}

// TestReadyzEndpoint200AfterReady pins the 503→200 transition and the
// 200→503 shutdown transition on the endpoint itself.
func TestReadyzEndpoint200AfterReady(t *testing.T) {
	readiness := NewReadiness()
	srv, err := NewServer(readiness)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ready := func() int {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		return rec.Code
	}
	if code := ready(); code != http.StatusServiceUnavailable {
		t.Fatalf("initial /readyz = %d, want 503", code)
	}
	readiness.Ready()
	if code := ready(); code != http.StatusOK {
		t.Fatalf("/readyz after Ready() = %d, want 200", code)
	}
	readiness.NotReady()
	if code := ready(); code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz after NotReady() = %d, want 503", code)
	}
}

// TestHandlerMethodBehavior pins the method contract: only GET is allowed on
// the health endpoints; every other method answers 405 with the Allow header
// and an empty body (handler responses contain only ok / not ready).
func TestHandlerMethodBehavior(t *testing.T) {
	srv, err := NewServer(NewReadiness())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead,
	} {
		for _, path := range []string{"/healthz", "/readyz"} {
			req := httptest.NewRequest(method, path, nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s status = %d, want 405", method, path, rec.Code)
			}
			if allow := rec.Header().Get("Allow"); allow != http.MethodGet {
				t.Fatalf("%s %s Allow = %q, want GET", method, path, allow)
			}
			if body := rec.Body.String(); body != "" {
				t.Fatalf("%s %s body = %q, want empty", method, path, body)
			}
		}
	}
}

// TestHandlerUnknownPath404 pins that unknown paths answer 404 with an empty
// body.
func TestHandlerUnknownPath404(t *testing.T) {
	srv, err := NewServer(NewReadiness())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /nope status = %d, want 404", rec.Code)
	}
	if body := rec.Body.String(); body != "" {
		t.Fatalf("GET /nope body = %q, want empty", body)
	}
}

// TestServerRejectsNilReadinessState pins the wiring error for a nil
// readiness state.
func TestServerRejectsNilReadinessState(t *testing.T) {
	if _, err := NewServer(nil); err == nil {
		t.Fatal("NewServer(nil) must return an error")
	}
}

// TestAddressResolvesDefault pins the address contract: an empty environment
// value resolves to :8080, and an explicit value passes through unchanged.
func TestAddressResolvesDefault(t *testing.T) {
	if got := Address(""); got != DefaultAddress {
		t.Fatalf("Address(\"\") = %q, want %q", got, DefaultAddress)
	}
	if got := Address(":9090"); got != ":9090" {
		t.Fatalf("Address(\":9090\") = %q, want \":9090\"", got)
	}
}

// waitForServe polls the endpoint until it answers, bounding the test
// against a slow server start.
func waitForServe(t *testing.T, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server at %s did not answer with %d within the bound", url, want)
}

// TestStartShutsDownAndJoinsWhenContextDone covers the shutdown contract:
// Start serves until the context is done, then shuts down within the bound
// and returns, so no server goroutine outlives the caller. It also proves
// the live endpoint answers 200 and the readiness endpoint answers 503
// during the serve window.
func TestStartShutsDownAndJoinsWhenContextDone(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	readiness := NewReadiness()
	srv, err := NewServer(readiness)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- srv.Start(ctx, addr)
	}()

	base := "http://" + addr
	waitForServe(t, base+"/healthz", http.StatusOK)
	waitForServe(t, base+"/readyz", http.StatusServiceUnavailable)
	readiness.Ready()
	waitForServe(t, base+"/readyz", http.StatusOK)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start after context cancellation = %v, want nil", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Start did not return after context cancellation (server goroutine leak)")
	}
}

// TestStartReturnsErrorOnMalformedAddress pins the fatal-startup contract:
// a malformed listen address fails before serving.
func TestStartReturnsErrorOnMalformedAddress(t *testing.T) {
	srv, err := NewServer(NewReadiness())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.Start(context.Background(), "no-port-here"); err == nil {
		t.Fatal("Start with a malformed address must return an error")
	}
}
