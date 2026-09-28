# CBSE Reference PostProcessingService

The reference PostProcessingService (PPS) completes the scenario end-state of
the CBSE alpha4 flow. When a scenario's simulation-runner fleet completes and
the Scenario Manager (SM) has moved the scenario to `PostProcessing`, the SM
publishes one **evaluation request** per scenario to the PPS over NATS
JetStream (stream `cbse_pps`, namespace-aware subjects). The PPS reads the
scenario's result rows **read-only** from the experiment's Result DB,
evaluates the scenario's KPI against the user-supplied precision threshold
using the paper's criterion — the half-width of a 95% confidence interval for
the selected performance metric — and answers with a **verdict**:

- **`met`** — precision met; the scenario transitions to the success-terminal
  `Finished` state (no further runners, results preserved).
- **`additional_runners`** — precision not met; the PPS returns the number of
  additional runners needed, the scenario returns to `StartingRunners`, and a
  new round-identified runner Job executes, after which evaluation repeats.
- **`stop_unmet`** — the user-defined maximum number of replications was
  reached with the criterion still unmet; the scenario fails through the
  guarded `PostProcessing → Failed` path with all results preserved.

The loop is owned by the PPS's stop policy; the SM applies verdicts and never
self-limits. The wire contract is the normative interface: any custom PPS
image may implement any evaluation strategy as long as it answers the verdict
contract on the evaluation subject.

This module is a standalone Go module (`github.com/D4NS3U/cbse/component-templates/post-processing-service`)
modeled on the reference Translator archetype: `config` / `subject` / `wire` /
`messaging` / `resultdb`-style packages, a static scratch Dockerfile, and
fake-based unit tests. It never imports `scenario-manager/internal/**` (Go
internal-package rules); its `wire` types duplicate the shared contract
field-for-field and are golden-tested.

## Layout

```
cmd/post-processing-service/   entrypoint (env + args + Secret → run loop)
internal/config/               env + flag parsing/validation, fail-fast startup
internal/subject/              the two subject templates + validation
internal/wire/                 request/verdict payloads, strict unmarshal + domain validation
internal/messaging/            NATS connect, durable consumer bind, AckExplicit processing loop,
                               PubAck-gated verdict publisher
internal/resultdb/             mounted-Secret parsing, read-only pgx result-row fetch,
                               mean_wait_time extraction
internal/evaluation/           the paper-exact statistical strategy, the deterministic policy,
                               the additional-rep estimator, and the caps
```

## Runtime configuration

### Environment variables

Injected by the Operator (S3); the PPS validates every value at startup and
fails fast on any missing, malformed, or inconsistent value.

| Variable | Value | Meaning |
|---|---|---|
| `NATS_URL` | e.g. `nats://sm-eds-nats:4222` | JetStream endpoint (no credentials in the URL) |
| `PPS_STREAM` | `cbse_pps` | JetStream stream carrying evaluation traffic |
| `PPS_REQUEST_SUBJECT` | `cbse.<namespace>.<project>.pps.request` | the per-experiment request subject (authoritative for ns/proj) |
| `PPS_EVALUATION_SUBJECT_TEMPLATE` | `cbse.<namespace>.<project>.pps.%s.evaluation` | verdict subject template; the single `%s` is the scenario id |
| `PPS_CONSUMER` | `pps-<12-char UID prefix>` | the per-experiment durable consumer name (must equal the UID-derived name) |
| `SIMULATIONPROJECTNAMESPACE` | DNS label | downward-API pod identity (namespace) |
| `SIMULATIONPROJECTNAME` | DNS label | downward-API pod identity (project) |
| `SIMULATIONEXPERIMENTUID` | `8-4-4-4-12` hex UID | downward-API pod identity (experiment) |

Mounted read-only Secret `<experiment-name>-resultdb-sct` at
`/resultdb-connection` (same Secret the Translator mounts): keys `host`,
`port`, `dbname`, `user`, `password` — no `sslmode` key; the PPS always
connects with `sslmode=disable`.

### Flags (container args)

Passed through from `spec.postProcessingService.args`; unknown flags or
non-positive numbers fail startup.

| Flag | Default | Meaning |
|---|---|---|
| `-evaluation-policy` | `statistical` | `statistical` (the paper-exact criterion) or `deterministic-first-round-not-met` (test/demo knob) |
| `-deterministic-additional-runners` | `2` | the deterministic policy's fixed additional-runner count (clamped to `-max-runners-per-round`, at least 1) |
| `-max-replications` | `10000` | user-defined maximum total replications across all rounds (the additional stopping criterion) |
| `-max-runners-per-round` | `1000` | per-round safety clamp on any additional batch |

## Wire contract

### Evaluation request (SM → PPS)

Published on `cbse.<namespace>.<project>.pps.request`; all fields required.

| Field | Type | Constraint |
|---|---|---|
| `experiment_uid` | string | valid UID; must equal the pod's `SIMULATIONEXPERIMENTUID` |
| `namespace` | string | DNS label; must equal the pod's `SIMULATIONPROJECTNAMESPACE` |
| `project` | string | DNS label; must equal the pod's `SIMULATIONPROJECTNAME` |
| `scenario_id` | int64 | > 0 |
| `runner_round` | int | >= 1 |
| `number_of_reps` | int | >= 1 (pooled replications so far) |
| `confidence_metric` | float64 | finite, > 0 — the desired precision threshold ε in metric units |

A request that fails strict decoding (unknown fields, trailing tokens) or
identity/domain validation is **permanent poison**: it is ACKed and logged,
with no Result DB query and no verdict.

### Evaluation verdict (PPS → SM)

Published on `cbse.<namespace>.<project>.pps.<scenario_id>.evaluation`
(PubAck-gated); all fields required.

| Field | Type | Constraint |
|---|---|---|
| `experiment_uid` | string | valid UID |
| `namespace` | string | DNS label |
| `project` | string | DNS label |
| `scenario_id` | int64 | > 0 |
| `runner_round` | int | >= 1 |
| `metric` | string | exactly `mean_wait_time` (the reference KPI; carried so custom PPS images can evaluate other metrics) |
| `verdict` | string | `met` \| `additional_runners` \| `stop_unmet` |
| `sample_mean` | float64 | finite (X̄ over the pooled observations; 0.0 for the deterministic policy) |
| `half_width` | float64 | finite (h; 0.0 when undefined) |
| `replications` | int | >= 0 (pooled observation count n; the request's `number_of_reps` under the deterministic policy) |
| `confidence_metric` | float64 | finite, > 0 (echoed ε) |
| `additional_runners` | int | >= 1 iff `verdict == "additional_runners"`, else 0 |
| `max_replications` | int | > 0 (the configured cap, echoed for observability) |

## Evaluation policy

### Statistical (default, paper-exact)

Over the pooled `mean_wait_time` observations (one per completed replication,
pooled across rounds):

1. Sample mean X̄ and sample variance s² (two-pass: mean first, then
   s² = Σ(xᵢ − X̄)² / (n − 1)).
2. Half-width of the 95% confidence interval:
   **h = t₀.₉₇₅,ₙ₋₁ · s / √n**, with the Student-t quantile from
   `gonum.org/v1/gonum/stat/distuv`.
3. **Met iff h ≤ ε**, where ε = the request's `confidence_metric`
   (absolute, in the metric's units — e.g. ±0.5 minutes in the paper's
   example; the reference model's time unit is SimPy time).

Additional batch (when not met): the sequential fixed-width estimate
**n_req = ⌈(t₀.₉₇₅,ₙ₋₁ · s / ε)²⌉**, then

```
additional = max(min-batch, n_req − n)
additional = min(additional, max-runners-per-round)
additional = min(additional, max-replications − n)
```

with min-batch = max(1, min(2, max-runners-per-round)). If the clamped
additional is 0, or if n ≥ max-replications with h > ε, the verdict is
`stop_unmet` (additional_runners 0).

Degenerate rule (the estimate cannot be formed): n = 0 or n = 1 counts as
not-met with the minimum batch (h undefined, reported 0.0); s = 0 with n ≥ 2
counts as met with h = 0. Rows whose `mean_wait_time` is missing,
non-numeric, or non-finite are skipped and counted as malformed — they enter
no computation, and the count is logged.

### Deterministic (test/demo knob)

`-evaluation-policy deterministic-first-round-not-met` decouples the
loop-machinery test (SM round-trip, messaging, round-Job spawn, verdict
application) from the model's randomness:

- `runner_round == 1` → verdict `additional_runners` with
  `additional_runners` = `-deterministic-additional-runners` (clamped to
  max-runners-per-round, at least 1).
- `runner_round >= 2` → verdict `met`.

Both with `sample_mean`/`half_width` 0.0 and the request's `number_of_reps`
echoed as `replications`. The PPS does not query the Result DB under this
policy. The statistical math itself is conformance-tested off-cluster with
hand-computed vectors (see `internal/evaluation` tests).

## Processing loop and ownership

- Startup validates every env var, flag, identity, and the mounted Secret
  (fail-fast, translator style), then connects NATS.
- The per-experiment durable consumer `pps-<12-char-UID-prefix>` on
  `cbse_pps` is **created and deleted by the Scenario Manager** with the
  experiment. The PPS **binds** to it (explicit ACK, DeliverAll, AckWait 2m,
  MaxAckPending 1, MaxDeliver −1, the four ownership metadata entries) and
  never creates, updates, deletes, or adopts it: a missing consumer is
  retried at a 10-second cadence until the SM ensures it; a mismatched
  consumer is an ownership collision that fails startup.
- One request at a time (AckExplicit): read request → validate (poison:
  ACK + log) → query Result DB (failure: **NAK** for redelivery) → evaluate
  per policy → publish the verdict (PubAck-gated; failure: **NAK**) → ACK.
  During the Result DB query the PPS sends 30-second in-progress
  acknowledgements so the server does not redeliver under the 2-minute
  AckWait.
- Read-only Result DB: `SELECT result FROM public.scenario_<id>_results`
  with a 30-second statement timeout; the PPS never writes the Result DB and
  never touches the Core DB. No built-in retry — a transient DB failure NAKs
  the request and JetStream redelivery is the retry.

## Security

The PPS owns no Kubernetes credentials (no client-go, no in-cluster
config); identity comes exclusively from the downward-API environment. The
image is a static binary in a scratch base running as `USER 1000:1000` to
satisfy the Operator's restricted security context; no secrets are baked in.

## Building and testing

```sh
go build ./...
go vet ./...
go test -race ./...
```

The image is built from `Dockerfile` with a locked Go builder image
(`PPS_GO_BUILDER_IMAGE`, linux/amd64 manifest digest) as the only build
stage. The image joins the harness mandatory component set (image lock,
build, preflight, self-test, stack manifest) in a later slice (S5); its smoke
coverage lands in S6.
