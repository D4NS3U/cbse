# Slice S4 — Reference PostProcessingService module (`component-templates/post-processing-service/`)

Normative and self-contained for its worker. Read order and global constraints: [../FEATURE.md](../FEATURE.md) §5 (verbatim discipline), §3 (esp. D1, D2, D3, D5, D6, D7 and the rulings record — Q1's paper-exact criterion and Q4/Q5's knobs), §6 S4 row; then repository-root `AGENTS.md`. Your component-template archetype is `component-templates/translator/` (module layout, `config`/`wire`/`subject`/`messaging`/`dbconfig`-style packages, Dockerfile, README, test discipline). S1 is landed (SM persistence) and S2 lands the SM-side wire in parallel — Go's internal-package rules forbid importing `scenario-manager/internal/**`, so your module defines its own wire types pinned to the normative contract below (field-for-field; golden-tested). This slice extends, never overrides, the umbrella. All three documents are read-only for you. On any contradiction between this slice and live code that inspection cannot resolve: stop and ask via your ask channel.

## Target

Exactly these surfaces — all new files unless named otherwise:

- `component-templates/post-processing-service/` — a new Go module `github.com/D4NS3U/cbse/component-templates/post-processing-service` with: `go.mod`/`go.sum`; `cmd/post-processing-service/main.go`; `internal/config` (env + flag parsing/validation, fail-fast at startup); `internal/subject` (the two subject templates + validation, mirroring the translator's); `internal/wire` (both payloads per the contract below, strict unmarshal validation); `internal/messaging` (NATS connect, per-experiment durable consumer attach, AckExplicit processing loop, verdict JetStream publisher — mirroring the translator's messaging taxonomy); `internal/resultdb` (pgx read-only client: parse the mounted connection Secret, query a scenario's result rows); `internal/evaluation` (the paper-exact strategy + the deterministic policy + the additional-rep estimator + the caps); `Dockerfile` (static scratch binary mirroring the translator's Dockerfile discipline, `USER` 1000:1000 to satisfy the operator's restricted security context); `README.md` (component doc: purpose, contract, config, evaluation policy, flags); Apache-2.0 headers everywhere (`Copyright 2025-2026 Daniel Seufferth`).
- `go.work` — add the module to the `use` list; keep the replace block intact; add the needed `go.work.sum` entries (`go work sync`); no version churn in the other modules (use the same nats.go/pgx versions scenario-manager pins; `gonum.org/v1/gonum` is your one new dependency).
- **Manager-licensed Makefile addition (exactly three line-contacts):** the root `Makefile` `test-fast` recipe enumerates modules; add `component-templates/post-processing-service` to (a) the `gofmt` `find` path list (line ~32), (b) a `cd component-templates/post-processing-service && go vet ./...` line beside the translator's (~36), (c) a `cd component-templates/post-processing-service && go test -race -timeout 30m ./...` line beside the translator's (~42). Nothing else in the Makefile is yours (`test/harness/**` stays untouched — the harness self-test inside test-fast must keep passing unmodified).

Everything else is untouchable — in particular `scenario-manager/**` (S2's parallel wave), `experiment-operator/**` (S3's), `test/harness/**`, `api/**`, `docs/**`, `devlog/**`, the harness image lock (S5 wires the image into the component set — you do not).

## Normative wire contract (shared verbatim with S2; you own the PPS-side implementation)

**Evaluation request** (you consume), on `cbse.<namespace>.<project>.pps.request` — all fields required: `experiment_uid` (string), `namespace` (string), `project` (string), `scenario_id` (int64 > 0), `runner_round` (int >= 1), `number_of_reps` (int >= 1), `confidence_metric` (finite float64 > 0). Identity fields must equal your pod's downward-API identity env — a mismatch is permanent poison (ACK + log, mirroring the translator's taxonomy).

**Evaluation verdict** (you publish), on `cbse.<namespace>.<project>.pps.<scenario_id>.evaluation` (the `%s` in `PPS_EVALUATION_SUBJECT_TEMPLATE`): `experiment_uid`, `namespace`, `project`, `scenario_id` (int64 > 0), `runner_round` (int >= 1), `metric` ("mean_wait_time"), `verdict` ("met" | "additional_runners" | "stop_unmet"), `sample_mean` (finite float64), `half_width` (finite float64), `replications` (int >= 0), `confidence_metric` (finite > 0), `additional_runners` (int >= 1 iff verdict == "additional_runners", else 0), `max_replications` (int > 0, your configured cap, echoed for observability).

**Env you parse** (injected by the operator, S3): `NATS_URL`, `PPS_STREAM` ("cbse_pps"), `PPS_REQUEST_SUBJECT`, `PPS_EVALUATION_SUBJECT_TEMPLATE`, `PPS_CONSUMER` ("pps-<12char>"), `SIMULATIONPROJECTNAMESPACE`, `SIMULATIONPROJECTNAME`, `SIMULATIONEXPERIMENTUID`, plus the mounted read-only connection Secret (keys `host`, `port`, `dbname`, `user`, `password`; sslmode disable; no sslmode key — mirror the translator's `dbconfig`).

**Flags** (from `spec.postProcessingService.args`, passed through as container args; defaults in parentheses): `-evaluation-policy` (`statistical`; or `deterministic-first-round-not-met`), `-deterministic-additional-runners` (`2`), `-max-replications` (`10000`), `-max-runners-per-round` (`1000`). Fail fast on unknown flags/policies or non-positive numbers.

## Change

1. **Result rows → observations**: `internal/resultdb` reads, per request, all rows of `scenario_<scenario_id>_results` (`SELECT result FROM ...`; each `result` is the runner's JSONB record) and extracts the `mean_wait_time` values (finite floats only; a row with a missing/non-finite metric is skipped and counted as malformed — if any malformed rows exist, include them in no computation but log the count). Connection via the mounted Secret; 30-second statement timeout (mirror the runner's discipline).
2. **Statistical evaluation (paper-exact, FEATURE.md D6)**: over the pooled `mean_wait_time` observations (n >= 1): sample mean X̄ and sample variance s² (two-pass or Welford — your choice, documented); half-width **h = t₀.₉₇₅,ₙ₋₁ · s / √n** (Student-t quantile via `gonum.org/v1/gonum/stat/distuv`; for n == 1, h is undefined → treat as not-met per item 4). Met iff **h ≤ ε** where ε = the request's `confidence_metric` (absolute, metric units).
3. **Additional-rep estimator**: **n_req = ⌈(t₀.₉₇₅,ₙ₋₁ · s / ε)²⌉**; additional = max(min-batch, n_req − n) with min-batch = max(1, min(2, max-runners-per-round)); clamp additional to `max-runners-per-round`.
4. **Stop policy (D7, ruling Q4)**: if not met and n >= `max-replications` → verdict `stop_unmet` (additional_runners 0); if not met and n < `max-replications`, clamp additional to `max-replications − n`; if the clamped additional is 0 → `stop_unmet`.
5. **Deterministic policy (ruling Q5)**: with `-evaluation-policy deterministic-first-round-not-met`: `runner_round == 1` → verdict `additional_runners` with `additional_runners` = `-deterministic-additional-runners` (clamped to max-runners-per-round, at least 1) and the request's `number_of_reps` echoed as `replications` with `sample_mean`/`half_width` 0.0; `runner_round >= 2` → verdict `met` (same zero-value echoes). This decouples the e2e loop test from model randomness (the statistical math itself is proven by your unit vectors).
6. **Processing loop**: attach the per-experiment durable `PPS_CONSUMER` (AckExplicit; the consumer is ensured by the SM — mirror the translator's ownership invariants: bind, do not create-and-orphan; redelivery per the consumer's AckWait), read request → validate (poison: ACK + log) → query Result DB (failure: NAK for redelivery) → evaluate per policy → publish the verdict on the evaluation subject (PubAck-gated; failure: NAK) → ACK. Startup validates every env/flag/identity (fail-fast, translator style).
7. **Dockerfile**: static linux binary, scratch base, `USER 1000:1000`, no secrets baked; mirror the translator Dockerfile's layering discipline.
8. **README**: component documentation (purpose, the wire contract tables, env/flags, the evaluation math with the paper's formulas, the deterministic-policy demo knob, the S5/S6 smoke wiring note "image joins the harness component set in a later slice").
9. **go.work + licensed Makefile lines**: per Target. `go build ./...` from the repo root must compile all four modules; `make test-fast` must run your module's vet + race suite.
10. **Tests**: golden-JSON wire tests for both payloads (field-for-field per the contract, including every poison case: wrong identity, non-positive ids, non-finite floats, invalid verdict enum, additional_runners inconsistency); evaluation-math unit tests with **hand-computed vectors** (fixed s/n/ε → expected h, verdict, n_req — include the paper's example numbers ε=0.5, and cases: met, not-met, cap-clamp, stop-unmet, n==1, s==0-with-n>=2 → met via h=0); deterministic-policy tests (round 1/2, clamping); config/flag/env validation tests; subject template tests; resultdb tests against a fake; messaging tests against the fake NATS pattern the translator uses (or tagged integration if the translator's suite does — mirror its discipline); README/doc-echo not required in tests.

## Constraints

- Mirror the translator's structural and testing discipline; no `scenario-manager` imports (Go internal rules — duplicate the wire shapes per the normative contract, golden-tested).
- **Read-only on the Result DB**: SELECT only; the PPS never writes the Result DB and never touches the Core DB.
- The PPS owns no Kubernetes credentials: no client-go, no in-cluster config; identity comes from env only.
- No harness edits; no cluster operations. `make test-fast` rc=0 is mandatory (with your licensed Makefile lines in place); **the cluster smoke is the wave gate**, run by the manager after S2+S3+S4 settle; you do not run `make test-smoke`.
- go.work discipline: no requirement bumps in the other three modules' `go.mod` files (your `go.work`/`go.work.sum` edits must leave their builds byte-stable — prove with `cd scenario-manager && go build ./...` rc=0).
- Parallel wave-mates: S2 owns `scenario-manager/**` messaging packages; S3 owns `experiment-operator/internal/controller/**` (+ one licensed line pair in `test/e2e/smoke_test.go`). Their uncommitted files will appear in `git status` — expected co-residency, never touch them; containment evidence lists your files and flags only files outside the three partitions.
- Global constraints of FEATURE.md §5 apply verbatim (attestation, no commits, read-only specs, scope discipline, verification honesty, licensing headers).

## Ownership

Sole owner of the new module + `go.work` + the three licensed Makefile line-contacts. Discovered gaps → report lines, do not fix.

## Observable acceptance

Run and echo all of these in `worker_done` (the manager re-runs each independently):

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat; mismatch → stop, `--outcome failed`).
2. Containment: `git status --short` — your new module (untracked dir), `go.work`/`go.work.sum`, the Makefile, plus expected co-resident wave-mate files; zero files outside the three partitions.
3. `cd component-templates/post-processing-service && go build ./... && go vet ./...` → rc=0.
4. `cd scenario-manager && go build ./...` → rc=0 (proof your go.work edits left the SM byte-stable) — also echo `git diff --stat go.work` and the three Makefile diff lines.
5. `make test-fast` rc=0 final receipt (mandatory — includes your vet + race lines).
6. Targeted: `cd component-templates/post-processing-service && go test -race -count=1 ./...` → rc=0.
7. Math-proof receipts: echo two named hand-computed vector tests (one met, one not-met-with-n_req) with their expected values in the assertion lines.
8. Wire-proof receipts: echo the two golden-JSON test names (request + verdict) and the identity-mismatch poison test name.

Completion protocol: `worker_done` with a three-sentence executive summary, both lifecycle IDs (task + dispatch from your preamble), explicit `--outcome succeeded|failed`, the verbatim attestation line, the eight evidence blocks, `--files-modified`.

**Session hygiene (binding, learned from the S1 crash):** keep tool outputs small (`head`/`tail`/`grep -n`, never whole-file cats of large files); gather evidence incrementally as you complete each requirement rather than batching everything into one giant end-session run.
