// Package buildkit owns the BuildKit admission gate and the image build+push
// seam for the reference Translator.
//
// Before creating or attaching its JetStream request consumer, Translator
// waits for the BuildKit socket and a successful ListWorkers call reporting at
// least one worker. The gate retries at 250ms, 500ms, 1s, then every 2s,
// honoring shutdown cancellation. Until the gate opens, Translator creates no
// consumer, accepts no delivery, writes no marker, and consumes no attempt.
// A BuildKit failure that occurs only after a request was accepted follows the
// ordinary confirmed empty-image workflow; the startup gate does not suppress
// request-time failure handling.
//
// The build+push seam is a function closure so the orchestrator can be unit
// tested without a real buildkitd. The real adapter in client.go wraps the
// official moby/buildkit Go client, supplies registry credentials from the
// mounted Docker configuration through the BuildKit session, submits the
// generated attempt directory as both Dockerfile and build context, requests a
// registry push to the deterministic tag with the framework-owned OCI manifest
// annotations, and returns the pushed digest.
package buildkit

import (
	"context"
	"errors"
	"time"
)

// gateBackoff is the canonical admission-gate retry schedule: 250ms, 500ms,
// 1s, then every 2s.
var gateBackoff = []time.Duration{
	250 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
}

// gateEvery is the steady-state retry interval after the initial schedule.
const gateEvery = 2 * time.Second

// ListWorkersFunc reports the number of available BuildKit workers. A non-nil
// error or a zero count means the gate is not yet open.
type ListWorkersFunc func(ctx context.Context) (int, error)

// AdmissionGate waits for listWorkers to report at least one worker, retrying
// with the canonical backoff (250ms, 500ms, 1s, then every 2s) and honoring ctx
// cancellation. It returns nil when the gate opens and ctx.Err() if ctx is
// cancelled while waiting.
func AdmissionGate(ctx context.Context, listWorkers ListWorkersFunc) error {
	if listWorkers == nil {
		return errors.New("buildkit: nil listWorkers")
	}
	attempt := 0
	for {
		n, err := listWorkers(ctx)
		if err == nil && n >= 1 {
			return nil
		}
		sleep := gateEvery
		if attempt < len(gateBackoff) {
			sleep = gateBackoff[attempt]
		}
		attempt++
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
	}
}

// SolveOptions carries the build+push inputs. Annotations are the
// framework-owned OCI manifest identity annotations the exporter adds to the
// pushed manifest. AuthConfigPath is the mounted Docker configuration path.
type SolveOptions struct {
	BuildDir       string            // the generated attempt directory (Dockerfile + build context)
	Tag            string            // the deterministic push tag <repository>:<tag>
	Annotations    map[string]string // framework-owned manifest annotations
	AuthConfigPath string            // path to mounted Docker config (registry creds)
}

// SolveFunc builds and pushes the image described by opt and returns the
// pushed digest (sha256:...) from the exporter response. A non-nil error is a
// build or push failure that the orchestrator treats as an empty-image
// outcome.
type SolveFunc func(ctx context.Context, opt SolveOptions, status chan<- Status) (string, error)

// Status is a minimal build progress event the orchestrator may observe. The
// real adapter converts moby/buildkit client.SolveStatus; tests use a no-op.
type Status struct {
	Vertex string
}

// Build invokes solve with opt and a background status channel. It returns the
// pushed digest. The status channel is drained and closed by the adapter.
func Build(ctx context.Context, solve SolveFunc, opt SolveOptions) (string, error) {
	if solve == nil {
		return "", errors.New("buildkit: nil solve")
	}
	status := make(chan Status, 16)
	done := make(chan struct{})
	go func() {
		for range status {
		}
		close(done)
	}()
	digest, err := solve(ctx, opt, status)
	close(status)
	<-done
	return digest, err
}
