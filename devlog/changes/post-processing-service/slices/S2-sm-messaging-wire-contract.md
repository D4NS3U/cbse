# Slice S2 — SM messaging & wire contract: subjects, `cbse_pps` stream, publication pipeline, verdict handler

Normative and self-contained for its worker. Read order and global constraints: [../FEATURE.md](../FEATURE.md) §5 (verbatim discipline), §3 (esp. D2, D3, D4, D6, D8, D10 and the rulings record), §6 S2 row; then repository-root `AGENTS.md`. S1 is landed (commit `5b3efa4`): the guarded persistence primitives you wire against already exist in `scenario-manager/internal/persistence/scenario_status.go` (`ClaimScenarioForEvaluation`, `MarkEvaluationPublishStarted`, `MarkEvaluationRequestPublished`, `ClaimScenarioForEvaluationRound`, `MarkScenarioFinished`) and `runner_status.go` (`MarkScenarioFailedFrom`). This slice extends, never overrides, the umbrella. All three documents are read-only for you. On any contradiction between this slice and live code that inspection cannot resolve: stop and ask via your ask channel.

## Target

Exactly these surfaces, all under `scenario-manager/`:

- `internal/subject/subject.go` (+ test) — PPS grammar: `PPSRequestSubject(namespace, project)`, `PPSEvaluationSubject(namespace, project, scenarioID)`, `PPSEvaluationWildcardSubject(namespace, project)`, stream-subject constants, and `Parse` rules for both new forms (mirror the `trans.*` token-count and identifier validation).
- `internal/nats/` (+ tests) — `config.go`: `PPSStreamName = "cbse_pps"` (canonical, env-override validation mirroring the translator's), `PPSConsumerName(uid)` = `pps-` + 12-char UID prefix, stream config (mirror the translator stream's retention/limits policy exactly), `consumers.go`: `EnsurePPSConsumer` / `DeletePPSConsumer` (mirror `EnsureTranslatorConsumer`/`DeleteTranslatorConsumer`: durable, filter subject = the experiment's `pps.request` subject, explicit ACK, DeliverAll, AckWait, MaxAckPending, MaxDeliver — copy the translator consumer's exact settings), a verdict consumer mirroring `readyconsumer.go` (SM-anchored durable, filter `cbse.*.*.pps.*.evaluation`, explicit ACK, routes each delivery to the handler), and a JetStream evaluation-request publisher (PubAck-gated, mirroring `publisher.go`).
- `internal/communication/communication.go` (+ test) — the wire types per the normative contract below: `ScenarioEvaluationRequest`, `PPSEvaluationVerdict`, handling status/result types, validation functions, and the interfaces (`EvaluationRequestPublisher`, `PPSEvaluationConsumer`) mirroring the translation request/ready shapes.
- A new evaluation-publication package (name your choice, mirroring `internal/selection/` — e.g. `internal/evaluationpub/`): claim `PostProcessing` scenarios for evaluation, `MarkEvaluationPublishStarted`, publish, `MarkScenarioTranslationRequestPublished`'s analog `MarkEvaluationRequestPublished` on PubAck — driven by the existing scheduler cadence exactly as selection/wiring.go drives translation.
- A new verdict-handler package (mirroring `internal/ready/` — e.g. `internal/verdict/`): validate identity → gate → apply the guarded transitions (S1 primitives) → handling result with the poison/stale taxonomy.
- `internal/lifecycle/cleanup.go` (+ test) — deletion-cleanup extension: purge `cbse.<ns>.<proj>.pps.request` and `cbse.<ns>.<proj>.pps.*.evaluation` from the PPS stream and delete the per-experiment PPS consumer (steps appended per the existing numbered-step convention).
- `cmd/main.go` — wire the publisher, scheduler pass, verdict consumer, and per-experiment PPS consumer reconciliation (creation on experiment admission / deletion on cleanup) mirroring the translation-flow wiring.

Everything else is untouchable — in particular `internal/{persistence,lifecycle(non-cleanup),effectivejob,jobadapter,runnerstart,observation,scheduler}` (S1, landed), `experiment-operator/**` (S3's parallel wave), `component-templates/**` (S4's), `test/**`, root `Makefile`, `go.mod`/`go.sum` (no new SM dependencies — you use the existing nats.go/pgx stack), `docs/**`, `devlog/**`.

## Normative wire contract (shared verbatim with S4; you own the SM-side types)

**Evaluation request** (SM → PPS), JetStream-published on `cbse.<namespace>.<project>.pps.request`, JSON payload — all fields required:

```json
{"experiment_uid": "<uid>", "namespace": "<ns>", "project": "<proj>", "scenario_id": 42, "runner_round": 1, "number_of_reps": 40, "confidence_metric": 0.5}
```

Validation on both sides: `scenario_id` > 0; `runner_round` >= 1; `number_of_reps` >= 1; `confidence_metric` finite and > 0; identity fields must equal the transport identity (`subject.ParseIdentity` pattern — a subject/payload mismatch is permanent poison).

**Evaluation verdict** (PPS → SM), JetStream-published on `cbse.<namespace>.<project>.pps.<scenario_id>.evaluation`, JSON payload — all fields required:

```json
{"experiment_uid": "<uid>", "namespace": "<ns>", "project": "<proj>", "scenario_id": 42, "runner_round": 1, "metric": "mean_wait_time", "verdict": "additional_runners", "sample_mean": 10.1, "half_width": 0.9, "replications": 40, "confidence_metric": 0.5, "additional_runners": 45, "max_replications": 10000}
```

`verdict` ∈ `{"met", "additional_runners", "stop_unmet"}`; `additional_runners` >= 1 iff `verdict == "additional_runners"` (absent-or-zero otherwise); all floats finite; `replications` >= 0. The SM-side handler applies: `met` → `MarkScenarioFinished`; `additional_runners` → `ClaimScenarioForEvaluationRound(N)`; `stop_unmet` → `MarkScenarioFailedFrom(PostProcessing)` — each guarded, stale-state no-ops, poison ACKed with an eventlog record (translator-ready taxonomy).

## Change

1. **Subject grammar** (`internal/subject/`): the three new template funcs + stream-subject constants + `Parse` cases, with the exact identifier validation the `trans.*` forms use. The evaluation subject's `<scenario-id>` token validates as a positive integer.
2. **Stream + consumers** (`internal/nats/`): `cbse_pps` ensured at startup alongside the existing streams (mirror the stream-config policy of `cbse_translator`); `EnsurePPSConsumer`/`DeletePPSConsumer` with the per-experiment durable `pps-<12char>`; the SM-anchored verdict durable consuming `cbse.*.*.pps.*.evaluation`; the PubAck-gated request publisher. Config validation stays canonical-env-override-only, like the translator stream names.
3. **Wire types** (`internal/communication/`): both payloads as Go types with strict `UnmarshalJSON` validation per the contract above; handling-status and handling-result types mirroring the translator-ready pattern; the two interfaces.
4. **Publication pipeline** (new package): on each scheduler pass, claim eligible `PostProcessing` scenarios (`ClaimScenarioForEvaluation`), mark publish-started for the exact attempt, publish on the experiment's request subject, mark published on PubAck. A publish failure or lost PubAck leaves the row claimable again on the next pass (exact-attempt guards make retries safe) — the selection package's semantics, mirrored.
5. **Verdict handler + consumer** (new package + `internal/nats/`): identity check → payload validation → gate (experiment live, scenario exists, round matches the scenario's current `runner_round`) → guarded transition per verdict → handling result. Permanent poison (malformed JSON, invalid subject, identity mismatch, invalid enum) is ACKed with an eventlog record; transient dependency failure NAKs.
6. **Deletion cleanup** (`internal/lifecycle/cleanup.go`): append the two purges + the PPS-consumer deletion as new numbered steps (do not renumber existing steps).
7. **Main wiring** (`cmd/main.go`): wire all of the above mirroring the translation flow's construction sites (publisher, scheduler pass, consumers, per-experiment reconciliation).
8. **Tests**: subject grammar (valid/invalid forms), stream/consumer config, wire-type validation (golden JSON in/out per the contract — field-for-field, including the poison cases), publication pipeline (claim/mark/publish/mark exactly-once per round incl. PubAck-loss retry), verdict handler (all three verdicts + stale-state no-op + poison taxonomy), cleanup steps, and NATS integration tests (tags=integration, local NATS container, the documented env pattern from `messaging_integration_test.go`) proving request-publish → consumer delivery and verdict-publish → SM consumer delivery.

## Constraints

- Mirror, never invent: the translator flow (`internal/selection/`, `internal/nats/{publisher,consumers,readyconsumer}.go`, `internal/ready/`) is the normative archetype for every new surface; deviate only where the contract above requires.
- The SM stays Result-DB-free (ruling Q2): no result-database access anywhere in scenario-manager.
- No new SM dependencies; no harness edits; no cluster operations. `make test-fast` rc=0 is mandatory (includes the SM race suite); the SM NATS integration tests must pass against the local NATS container (documented env pattern).
- **Cluster smoke is the wave gate**, run by the manager after S2+S3+S4 settle (co-resident parallel changes make per-worker cluster runs unsound) — you do not run `make test-smoke`.
- Parallel wave-mates: S3 owns `experiment-operator/internal/controller/**` (+ one licensed line pair in `test/e2e/smoke_test.go`), S4 owns `component-templates/post-processing-service/**` (+ `go.work`, + licensed `Makefile` test-fast lines). Their uncommitted files will appear in `git status` — expected co-residency, never touch them; containment evidence lists your files and flags only files outside the three partitions.
- Global constraints of FEATURE.md §5 apply verbatim (attestation, no commits, read-only specs, scope discipline, verification honesty).

## Ownership

Sole owner of the SM messaging surface named in Target. Discovered gaps → report lines, do not fix.

## Observable acceptance

Run and echo all of these in `worker_done` (the manager re-runs each independently):

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat; mismatch → stop, `--outcome failed`).
2. Containment: `git status --short` — your files plus the expected co-resident wave-mate files; zero files outside the three partitions.
3. `cd scenario-manager && go build ./... && go vet ./...` → rc=0.
4. `make test-fast` rc=0 final receipt (mandatory).
5. Targeted: `cd scenario-manager && go test -count=1 ./internal/subject/... ./internal/nats/... ./internal/communication/... ./internal/lifecycle/... <your two new packages>` → rc=0.
6. NATS integration receipts (documented env pattern): the request-publish→delivery test and the verdict→SM-consumer test, test names + PASS lines.
7. Golden-JSON proof: echo one golden test name per payload (request + verdict) proving field-for-field wire equality.
8. grep receipts: `PPSStreamName = "cbse_pps"`, `pps-` consumer-name func, the two purge subjects in `cleanup.go`, the three verdict applications (`MarkScenarioFinished`, `ClaimScenarioForEvaluationRound`, `MarkScenarioFailedFrom`) in the handler.

Completion protocol: `worker_done` with a three-sentence executive summary, both lifecycle IDs (task + dispatch from your preamble), explicit `--outcome succeeded|failed`, the verbatim attestation line, the eight evidence blocks, `--files-modified`.

**Session hygiene (binding, learned from the S1 crash):** keep tool outputs small (`head`/`tail`/`grep -n`, never whole-file cats of large files); gather evidence incrementally as you complete each requirement rather than batching everything into one giant end-session run.
