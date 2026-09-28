# IMPLEMENTATION_HANDOFF — PostProcessingService reference component and flow integration

Umbrella: [FEATURE.md](FEATURE.md) (rulings Q1–Q5 recorded 2026-09-25). Orchestration: run `run_9174202d965c`.

## S1 — SM state machine & persistence — **complete** (settled 2026-09-28)

- **Task/Dispatch:** `task_15803ce33940`; first dispatch `ctx_0529a5510cae` crashed mid-evidence-gathering on a repeated provider `400 (no body)` after completing the full implementation (turn unrecoverable in that session; `worker-stop` + replacement). Replacement dispatch `ctx_d2fdee852073` (`--retry-of` the crashed one) settled verification-only: no re-implementation, zero code changes. Both workers attested `ai.forge/qwen3.8-27b-nvfp4`.
- **Manager-licensed boundary exception:** `scenario-manager/internal/scheduler/scheduler.go` — additive `Round int` on `RunnerStartRequest`/`ObservationRequest` (the slice's Target list had omitted the boundary package; it is the only channel between runnerstart/observation and jobadapter/effectivejob). Licensed via ask-channel reply to the first worker.
- **Scope landed (24 files, six packages + the boundary file):** `Finished` terminal state + guarded `MarkScenarioFinished`; six additive columns (`runner_round`, `round_reps`, `round_computed_reps`, `evaluation_attempts`, `evaluation_publish_started_at`, `evaluation_request_published_at`) with `ErrSchemaIncompatible` validation; evaluation publication guard trio (`ClaimScenarioForEvaluation`/`MarkEvaluationPublishStarted`/`MarkEvaluationRequestPublished`, exact-attempt guards); `ClaimScenarioForEvaluationRound` (`PostProcessing -> StartingRunners`, `ErrInvalidAdditionalRunners`); `UpdateScenarioComputedRepsForRound` (per-round clamp + cross-round total); effectivejob round support (round-1 Job names byte-identical, `-r<round>` suffix from round 2, `experiment.cbse.terministic.de/runner-round` label); jobadapter round parse/create/observe; `round_reps`-driven runnerstart; current-round observation; lifecycle doc corrections (`PostProcessing` = evaluation boundary, no longer the success-terminal); eventlog records.
- **Manager re-verification (all independently re-run, all rc=0):** containment (exactly the 24 files); `go build ./... && go vet ./...`; `make test-fast`; targeted six-package `go test -count=1`; greps — `ScenarioStateFinished` (db.go:146), six DDL columns (schema.go:95–100), `nonTerminalFailureStates` still exactly 5 members (no `Finished`); pin tests present — `TestBuildRoundOneNameByteIdentical` (effectivejob_test.go:178), `TestEnsureSchemaRejectsMissingRoundColumns` (persistence_integration_test.go:635), `TestComputedRepsSingleRoundEqualityPin` (persistence_integration_test.go:754).
- **Frozen invariants proven:** round-1 Job name byte-equality; single-round `number_of_computed_reps == number_of_reps`; `PostProcessing` remains failable (stop-unmet verdict path for D7); `Finished` never failable.

## Wave status

- W1 [S1] — **complete** (this record).
- W2 [S2 ∥ S3 ∥ S4] — next: SM messaging & wire contract; operator PPS provisioning; reference PPS module. Ownership-disjoint per FEATURE.md §6.
- W3 [S5], W4 [S6] — after W2.
