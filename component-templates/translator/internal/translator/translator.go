// Package translator is the reference Translator framework orchestrator. It
// owns request consumption and validation, serial delivery, workspace
// lifecycle, BuildKit admission, generation, build+push, registry digest and
// OCI annotation verification, ready publication, and request acknowledgement.
//
// The orchestrator depends on narrow seams (messaging.Consumer/Publisher/
// Manager, generator.Generator, a registry verifier, BuildKit solve/list
// functions, and a filesystem workspace) so the full request protocol is
// unit-testable with fakes and no real NATS, BuildKit, or registry.
package translator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/D4NS3U/cbse/component-templates/translator/internal/buildkit"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/config"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/generator"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/imageref"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/messaging"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/registry"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/subject"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/wire"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/workspace"
)

// registryClient is the registry seam the orchestrator needs: verify the tag,
// repository, and identity annotations and return the digest, distinguishing a
// not-found tag (permits generation) from other resolution failures.
type registryClient interface {
	VerifyAndResolve(ctx context.Context, tagRef, repo, uid string, scenarioID, attempt int) (string, error)
}

// Deps holds the replaceable dependencies. Production wires the real adapters;
// tests inject fakes.
type Deps struct {
	Config      *config.Config
	Workspace   *workspace.Workspace
	Generator   generator.Generator
	Registry    registryClient
	Solve       buildkit.SolveFunc
	ListWorkers buildkit.ListWorkersFunc
	Consumer    messaging.Consumer
	Publisher   messaging.Publisher
	Manager     messaging.Manager
	Logger      *log.Logger
	// InProgressInterval overrides the 30-second in-progress ack cadence for
	// tests. Zero defaults to messaging.InProgressInterval.
	InProgressInterval time.Duration
}

// Translator is the reference framework orchestrator.
type Translator struct {
	deps Deps
	log  *log.Logger
}

// New returns a Translator wired to deps.
func New(deps Deps) *Translator {
	logger := deps.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Translator{deps: deps, log: logger}
}

// Run applies the BuildKit admission gate, creates or attaches the per-experiment
// request consumer, and consumes one request at a time until ctx is cancelled.
func (t *Translator) Run(ctx context.Context) error {
	if err := buildkit.AdmissionGate(ctx, t.deps.ListWorkers); err != nil {
		return fmt.Errorf("buildkit admission gate: %w", err)
	}
	if err := t.deps.Manager.EnsureConsumer(); err != nil {
		return fmt.Errorf("ensure consumer: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		msg, err := t.deps.Consumer.Fetch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			t.log.Printf("translator: fetch error: %v", err)
			continue
		}
		if err := t.handle(ctx, msg); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			t.log.Printf("translator: request left unacknowledged for redelivery: %v", err)
		}
	}
}

// handle processes one request delivery through the full protocol and performs
// the server-ack itself. It returns nil after a terminal acknowledged outcome
// (success, empty-failure, or raw poison). A non-nil return means the request
// was left unacknowledged for JetStream redelivery (cancellation, marker write
// failure, ready-publication failure, or final-ack failure).
func (t *Translator) handle(ctx context.Context, msg messaging.Message) error {
	req, err := wire.DecodeRequest(msg.Data())
	if err != nil || !req.HasUsableIdentity() {
		// Raw poison: ACK without a ready message or outcome marker.
		t.log.Printf("translator: acknowledging raw poison: decode=%v", err)
		return msg.Ack()
	}
	scenarioID := req.ID
	attempt := req.TranslationAttempt
	readySubject := subject.ReadySubject(t.deps.Config.Namespace, t.deps.Config.Project, scenarioID)
	uid := t.deps.Config.ExperimentUID
	repo := t.deps.Config.Repository
	uidPrefix := imageref.UIDPrefix(uid)

	dir, err := t.deps.Workspace.EnsureAttemptDir(scenarioID, attempt)
	if err != nil {
		return err
	}

	// 1. Retained outcome marker: reuse the exact ready message.
	if m, ok, err := workspace.ReadMarker(dir); err != nil {
		return err
	} else if ok {
		if verr := workspace.ValidateMarker(m, uid, scenarioID, attempt, readySubject); verr != nil {
			t.log.Printf("translator: discarding invalid retained marker: %v", verr)
			_ = workspace.RemoveBuildInput(dir)
		} else {
			return t.reuseMarker(ctx, dir, m, repo, msg)
		}
	}

	// 2. No marker: attempt registry-tag recovery (success-marker-loss path).
	tag := imageref.Tag(repo, uidPrefix, scenarioID, attempt)
	digest, rerr := t.deps.Registry.VerifyAndResolve(ctx, tag, repo, uid, scenarioID, attempt)
	if rerr == nil {
		m := workspace.Marker{
			Outcome: workspace.OutcomeSuccess, ScenarioID: scenarioID, Attempt: attempt,
			ReadySubject: readySubject, ExperimentUID: uid, Tag: tag, Digest: digest,
		}
		return t.finishSuccess(ctx, dir, m, repo, msg)
	}
	if !errors.Is(rerr, registry.ErrNotFound) {
		// Resolution error (not a clean not-found): confirmed empty-image outcome.
		return t.emptyFailure(ctx, dir, scenarioID, attempt, readySubject, uid, "digest_resolution", msg)
	}

	// 3. No marker and no recoverable tag: recreate build input and generate.
	if workspace.BuildInputExists(dir) {
		if err := workspace.RemoveBuildInput(dir); err != nil {
			return err
		}
	}
	genInput := generator.GenerationInput{
		ScenarioID:         scenarioID,
		TranslationAttempt: attempt,
		RecipeInfo:         req.RecipeInfo,
		ConfidenceMetric:   req.ConfidenceMetric,
		BaseImage:          t.deps.Config.BaseImage,
		Workspace:          dir,
		DetailDatabase:     t.deps.Config.DetailDB,
		ResultDatabase:     t.deps.Config.ResultDB,
	}
	if err := t.runWithInProgress(ctx, msg, func(opCtx context.Context) error {
		return t.deps.Generator.Generate(opCtx, genInput)
	}); err != nil {
		if errors.Is(err, context.Canceled) {
			return err // cancellation: leave unacked, no marker
		}
		return t.emptyFailure(ctx, dir, scenarioID, attempt, readySubject, uid, "generator", msg)
	}

	// 4. Build and push.
	if err := t.runWithInProgress(ctx, msg, func(opCtx context.Context) error {
		_, err := buildkit.Build(opCtx, t.deps.Solve, buildkit.SolveOptions{
			BuildDir:    dir,
			Tag:         tag,
			Annotations: imageref.Annotations(uid, scenarioID, attempt),
		})
		return err
	}); err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return t.emptyFailure(ctx, dir, scenarioID, attempt, readySubject, uid, "build", msg)
	}

	// 5. Resolve the digest and verify the repository and identity annotations.
	digest, err = t.resolveWithInProgress(ctx, msg, tag, repo, uid, scenarioID, attempt)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return t.emptyFailure(ctx, dir, scenarioID, attempt, readySubject, uid, "digest_resolution", msg)
	}

	m := workspace.Marker{
		Outcome: workspace.OutcomeSuccess, ScenarioID: scenarioID, Attempt: attempt,
		ReadySubject: readySubject, ExperimentUID: uid, Tag: tag, Digest: digest,
	}
	return t.finishSuccess(ctx, dir, m, repo, msg)
}

// reuseMarker republishes the exact ready message from a valid retained marker,
// confirms publication, acknowledges the request, and removes the attempt
// workspace.
func (t *Translator) reuseMarker(ctx context.Context, dir string, m *workspace.Marker, repo string, msg messaging.Message) error {
	image := ""
	if m.Outcome == workspace.OutcomeSuccess {
		image = imageref.DigestRef(repo, m.Digest)
	}
	if err := t.publishReady(ctx, m.ReadySubject, m.Attempt, image); err != nil {
		return err
	}
	if err := msg.Ack(); err != nil {
		return err
	}
	_ = t.deps.Workspace.RemoveAttemptDir(m.ScenarioID, m.Attempt)
	return nil
}

// finishSuccess writes the success marker, publishes the digest ready message,
// confirms publication, acknowledges the request, and removes the attempt
// workspace.
func (t *Translator) finishSuccess(ctx context.Context, dir string, m workspace.Marker, repo string, msg messaging.Message) error {
	if err := workspace.WriteMarker(dir, m); err != nil {
		return err
	}
	if err := t.publishReady(ctx, m.ReadySubject, m.Attempt, imageref.DigestRef(repo, m.Digest)); err != nil {
		return err
	}
	if err := msg.Ack(); err != nil {
		return err
	}
	_ = t.deps.Workspace.RemoveAttemptDir(m.ScenarioID, m.Attempt)
	return nil
}

// emptyFailure atomically writes the empty-failure marker, publishes the
// empty-image ready message, confirms publication, acknowledges the request,
// and removes the attempt workspace.
func (t *Translator) emptyFailure(ctx context.Context, dir string, scenarioID, attempt int, readySubject, uid, class string, msg messaging.Message) error {
	m := workspace.Marker{
		Outcome: workspace.OutcomeEmptyFailure, ScenarioID: scenarioID, Attempt: attempt,
		ReadySubject: readySubject, ExperimentUID: uid, EmptyImage: true, FailureClass: class,
	}
	if err := workspace.WriteMarker(dir, m); err != nil {
		return err
	}
	if err := t.publishReady(ctx, readySubject, attempt, ""); err != nil {
		return err
	}
	if err := msg.Ack(); err != nil {
		return err
	}
	_ = t.deps.Workspace.RemoveAttemptDir(scenarioID, attempt)
	return nil
}

// publishReady encodes and publishes a ready message with JetStream
// confirmation.
func (t *Translator) publishReady(ctx context.Context, subject string, attempt int, image string) error {
	data, err := wire.EncodeReady(attempt, image)
	if err != nil {
		return err
	}
	return t.deps.Publisher.Publish(subject, data)
}

// runWithInProgress runs op under a derived context, sending 30-second
// in-progress acknowledgements so the server does not redeliver under the
// two-minute AckWait. If an in-progress acknowledgement fails, the operation is
// cancelled and the error is returned (the caller leaves the request
// unacknowledged for redelivery).
func (t *Translator) runWithInProgress(ctx context.Context, msg messaging.Message, op func(context.Context) error) error {
	opCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	interval := t.deps.InProgressInterval
	if interval <= 0 {
		interval = messaging.InProgressInterval
	}
	done := make(chan error, 1)
	go func() { done <- op(opCtx) }()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			if err := msg.InProgress(); err != nil {
				cancel()
				return <-done
			}
		}
	}
}

// resolveWithInProgress runs the registry verification under the in-progress
// ack loop and returns the verified digest.
func (t *Translator) resolveWithInProgress(ctx context.Context, msg messaging.Message, tag, repo, uid string, scenarioID, attempt int) (string, error) {
	var digest string
	err := t.runWithInProgress(ctx, msg, func(opCtx context.Context) error {
		var err error
		digest, err = t.deps.Registry.VerifyAndResolve(opCtx, tag, repo, uid, scenarioID, attempt)
		return err
	})
	return digest, err
}
