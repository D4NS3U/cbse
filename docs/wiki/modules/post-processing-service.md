# Reference Post Processing Service (PPS)

The per-experiment component that completes the scenario end-state of the
CBSE alpha4 flow. When the Scenario Manager (SM) has moved a scenario to
`PostProcessing`, it publishes one **evaluation request** per scenario to the
PPS over NATS JetStream; the PPS reads the scenario's result rows
**read-only** from the experiment's Result DB, evaluates the scenario's KPI
against the user-supplied precision threshold using the paper's criterion —
the half-width of a 95% confidence interval for `mean_wait_time` — and answers
with a **verdict**:

- `met` — precision met; the SM applies the guarded `PostProcessing → Finished`
  transition (no further runners, results preserved);
- `additional_runners` — precision not met; the SM starts a new runner round
  with the returned count (guarded `PostProcessing → StartingRunners`), and
  evaluation repeats after the round completes;
- `stop_unmet` — the user-defined maximum number of replications was reached
  with the criterion unmet; the scenario fails through the guarded
  `PostProcessing → Failed` path (`persistence.MarkScenarioFailedFrom`), all
  results preserved.

The loop is owned by the PPS's stop policy; the SM applies verdicts and never
self-limits. The wire contract is the normative interface: any custom PPS image
may implement any evaluation strategy as long as it answers the verdict
contract on the evaluation subject.

Source root: `component-templates/post-processing-service/`, a standalone Go
module (`github.com/D4NS3U/cbse/component-templates/post-processing-service`).
It never imports `scenario-manager/internal/**`; its `wire` package duplicates
the shared contract field-for-field and is golden-tested.

| Package | Responsibility |
|---|---|
| `cmd/post-processing-service/` | entrypoint: config → NATS connect → consumer binding → AckExplicit processing loop, graceful shutdown on SIGINT/SIGTERM |
| `internal/config/` | env + flag parsing/validation, fail-fast startup |
| `internal/subject/` | the canonical PPS subject grammar and validation |
| `internal/wire/` | request/verdict payloads, strict decode, domain validation |
| `internal/messaging/` | consumer binding/ownership, the processing loop, PubAck-gated verdict publisher (seams + real NATS adapter in `nats.go`) |
| `internal/resultdb/` | mounted-Secret parsing, read-only result-row fetch, `mean_wait_time` extraction |
| `internal/evaluation/` | the paper-exact statistical strategy, the deterministic policy, the additional-replication estimator, and the caps |

## Configuration

`config.Load`
([`internal/config/config.go`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/post-processing-service/internal/config/config.go))
rejects any unknown flag, missing, malformed, or inconsistent value with a
descriptive error before any NATS connection or request handling. The service
accepts no NATS credentials (username, password, token, NKey, JWT, or
credentials file) and does not watch, reload, or rotate credentials after
startup.

| Variable | Value / validation |
|---|---|
| `NATS_URL` | required; URL with a host; user information in the URL is refused |
| `PPS_STREAM` | required; JetStream stream `cbse_pps` (no whitespace/wildcards); the operator constant is `ppsStream = "cbse_pps"` |
| `PPS_REQUEST_SUBJECT` | required; must parse as the canonical `cbse.<namespace>.<project>.pps.request` — authoritative for the namespace/project tokens, which must also match the identity env |
| `PPS_EVALUATION_SUBJECT_TEMPLATE` | required; must match `cbse.<namespace>.<project>.pps.%s.evaluation` with the concrete tokens and exactly one `%s` scenario-id placeholder |
| `PPS_CONSUMER` | required; must equal `pps-<12-char-UID-prefix>` derived from `SIMULATIONEXPERIMENTUID` (a mismatch is an Operator injection error) |
| `SIMULATIONPROJECTNAMESPACE` | required; lowercase DNS label, 1–63 chars |
| `SIMULATIONPROJECTNAME` | required; DNS label |
| `SIMULATIONEXPERIMENTUID` | required; 8-4-4-4-12 lowercase hex |

Flags (passed through from `spec.postProcessingService.args`):

| Flag | Default | Meaning |
|---|---|---|
| `-evaluation-policy` | `statistical` | `statistical` (the paper-exact criterion) or `deterministic-first-round-not-met` (test/demo knob) |
| `-deterministic-additional-runners` | `2` | the deterministic policy's fixed additional-runner count (clamped to `-max-runners-per-round` while the cap is enabled, at least 1) |
| `-max-replications` | `10000` | user-defined maximum total replications across all rounds — the scientific stopping criterion, strictly positive-mandatory |
| `-max-runners-per-round` | `1000` | per-round safety clamp on any additional batch; **0 disables the per-wave cap** (ruling Q7, logged at startup: "per-wave cap disabled; waves sized by the estimate alone, bounded only by max-replications headroom"); negative is a configuration error |

The Result DB connection comes from the mounted
`<experiment-name>-resultdb-sct` Secret at `/resultdb-connection/` (the same
Secret the Translator mounts): keys `host`, `port`, `dbname`, `user`,
`password`; no `sslmode` key — the PPS always connects with `sslmode=disable`.

## NATS surface (`internal/messaging`, `internal/subject`)

Subjects (canonical grammar in
[`internal/subject/subject.go`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/post-processing-service/internal/subject/subject.go)):

- request (SM → PPS), fixed per experiment: `cbse.<namespace>.<project>.pps.request`;
- verdict (PPS → SM), derived per scenario at publish time:
  `cbse.<namespace>.<project>.pps.<scenario-id>.evaluation` (the decimal
  scenario id substitutes the template's single `%s`; the PPS accepts only
  canonical templates so it never publishes outside the injected namespace or
  project).

The per-experiment durable consumer is named
`pps-<12-char-UID-prefix>` (`ConsumerName`: the UID lowercased, hyphens
removed, first 12 hex characters) on the stream `cbse_pps`. The canonical
consumer settings are: explicit ACK (`AckExplicit`), `DeliverAll`,
`AckWait` 2 minutes, `MaxAckPending` 1 (one in-flight request per PPS),
`MaxDeliver` −1 (unlimited redelivery), filter subject the exact request
subject, and four ownership metadata entries
(`experiment.cbse.terministic.de/managed-by=pps`,
`.../experiment-uid`, `.../namespace`, `.../project`).

**Ownership:** the durable consumer is **created and deleted by the Scenario
Manager** with the experiment. The PPS only *binds* to it
(`Manager.EnsureConsumer` → `nats.go`'s `Client.EnsureConsumer`, which checks
`ConsumerInfo` and runs `CompareConsumer` over durable name, filter subject,
delivery policy, ACK policy, AckWait, MaxAckPending, MaxDeliver, and all four
metadata entries) and never creates, updates, deletes, or adopts it:

- a missing consumer (or stream) returns `ErrConsumerNotReady` — the
  processing loop retries the attachment at a 10-second cadence
  (`BindRetryInterval`) until the SM ensures it;
- an existing consumer with mismatched identity or settings is an ownership
  collision (`ErrOwnershipCollision`) and **fails startup without
  modification**.

The pull subscription is created lazily on the first `Fetch`
(`PullSubscribe` with `nats.BindStream`), then cached — a failed first
subscribe is not retried. `Fetch` polls with `MaxWait(1s)` so context
cancellation is honored promptly.

### The processing loop

`Processor.Run`
([`internal/messaging/processor.go`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/post-processing-service/internal/messaging/processor.go))
binds, then consumes one request at a time:

1. **decode** — `wire.DecodeRequest` (strict: unknown fields and trailing
   tokens rejected);
2. **validate** — `ValidateRequest` against the pod identity: the three
   identity fields must exactly equal the downward-API identity (a mismatch is
   poison), `scenario_id > 0`, `runner_round >= 1`, `number_of_reps >= 1`,
   `confidence_metric` finite and > 0. A request failing either step is
   **permanent poison**: it is ACKed and logged, with no Result DB query and
   no verdict;
3. **query** — `resultdb.Fetch` (statistical policy only; below);
4. **evaluate** — `evaluation.Evaluate` (below);
5. **publish** — the verdict on the evaluation subject, JetStream
   confirmation-gated (`Publish` blocks until the PubAck);
6. **ACK**.

Result DB fetch failures and verdict-publication failures **NAK** the request
for JetStream redelivery — they never manufacture a verdict, and there is no
built-in retry (the transport retry is the message redelivery). While a
request is in flight (the Result DB query included), the PPS sends
30-second in-progress acknowledgements (`InProgress()`,
`InProgressInterval`) so the server does not redeliver under the 2-minute
AckWait; a failed in-progress acknowledgement cancels the operation and leaves
the request unacknowledged.

## Wire payloads (`internal/wire`)

**Evaluation request** (SM → PPS), all seven fields required:

```json
{"experiment_uid":"<uid>","namespace":"<ns>","project":"<proj>",
 "scenario_id":42,"runner_round":1,"number_of_reps":40,"confidence_metric":0.5}
```

**Evaluation verdict** (PPS → SM), all thirteen fields required:

```json
{"experiment_uid":"<uid>","namespace":"<ns>","project":"<proj>",
 "scenario_id":42,"runner_round":1,"metric":"mean_wait_time",
 "verdict":"met","sample_mean":10.1,"half_width":0.9,"replications":40,
 "confidence_metric":0.5,"additional_runners":0,"max_replications":10000}
```

`wire.EncodeVerdict` validates before encoding (the single shared validation
also enforced by the golden conformance tests): valid identity fields,
`scenario_id > 0`, `runner_round >= 1`, `metric` exactly `mean_wait_time`
(`MetricMeanWaitTime` — carried so custom PPS images can evaluate other
metrics), `verdict` in the three-value enum, finite `sample_mean` and
`half_width`, `replications >= 0`, `confidence_metric` finite and > 0,
`max_replications > 0`, and `additional_runners >= 1` **iff** the verdict is
`additional_runners` (0 otherwise).

## The evaluation policy (`internal/evaluation`)

`Evaluate`
([`internal/evaluation/evaluation.go`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/post-processing-service/internal/evaluation/evaluation.go))
selects between two policies. The inputs are the request's `runner_round`,
`number_of_reps` (pooled replications so far, echoed as `replications` by the
deterministic policy), `confidence_metric`, the pooled `mean_wait_time`
observations (one per completed replication, pooled across rounds — from the
Result DB fetch), and the loop-bound `Params`.

### `confidence_metric` semantics — the statistical policy, exactly as coded

The request's `confidence_metric` is the desired precision threshold
**epsilon** ε (absolute, in the metric's units — SimPy time units for the
reference model). The statistical policy is the paper's criterion verbatim,
over the pooled observations with n ≥ 1:

1. Sample mean X̄ and sample standard deviation s by the **two-pass**
   algorithm (`SampleMeanStd`): the mean first, then
   s = √(Σ(xᵢ − X̄)² / (n − 1)). (n = 0 returns (0, 0); n = 1 returns
   (mean, 0) — the sample variance is undefined.)
2. Half-width of the **95% confidence interval**:
   **h = t(0.975, n−1) · s / √n** (`HalfWidth`), with the Student-t quantile
   from `gonum.org/v1/gonum/stat/distuv` —
   `distuv.StudentsT{Nu: df, Sigma: 1}.Quantile(0.975)` (the zero-value Sigma
   would scale the distribution to a point mass, so Sigma is set to 1).
3. **The criterion is met iff h ≤ ε.**

When not met, the **sequential fixed-width additional-replication estimate** is
`n_req = ⌈(t(0.975, n−1) · s / ε)²⌉` (`RequiredReplications`) — the total
replications needed for a half-width of at most ε — and the request arithmetic
for the top-up batch is:

```
additional = max(MinBatch(max-runners-per-round), n_req − n)
additional = min(additional, max-runners-per-round)      # skipped when the cap is 0 (disabled)
additional = min(additional, max-replications − n)        # always active
```

where `MinBatch(m) = max(1, min(2, m))` — the floor is 1 for `m = 1`, 2 for
`m = 2`, and 2 for `m = 0` (cap disabled) or `m > 2`. If the clamped
additional is ≤ 0 (total cap exhausted), or if `n >= max-replications` with
the criterion unmet, the verdict is `stop_unmet` with `additional_runners` 0.
Otherwise it is `additional_runners` with the clamped count.

**Degenerate rule** (the estimate cannot be formed): `n = 0` (no completed
replication yet) or `n = 1` counts as **not-met with the minimum batch**
(`additional_runners`, `MinBatch`; h is undefined and reported as 0.0);
`s = 0` with `n >= 2` counts as **met** with h = 0.

With `max-runners-per-round = 0` (ruling Q7) the per-wave cap is disabled: the
estimate flows verbatim, bounded only by the max-replications headroom; the
min-batch floor and the headroom stay active in all modes.

Verdict fields are filled from `Output`: `sample_mean` (X̄; 0.0 when undefined
or under the deterministic policy), `half_width` (h; 0.0 when undefined),
`replications` (n; the request's `number_of_reps` under the deterministic
policy), `additional_runners` (≥ 1 iff `additional_runners`, else 0), and
`max_replications` (the configured cap, echoed for observability).

### The deterministic policy (test/demo knob)

`-evaluation-policy deterministic-first-round-not-met` decouples the
loop-machinery (messaging, round Jobs, verdict application) from the model's
randomness:

- `runner_round == 1` → verdict `additional_runners` with
  `additional_runners` = `-deterministic-additional-runners` (clamped to
  `max-runners-per-round` while the cap is enabled, at least 1);
- `runner_round >= 2` → verdict `met`.

Both with `sample_mean`/`half_width` 0.0 and the request's `number_of_reps`
echoed as `replications`. The PPS does **not** query the Result DB under this
policy (observations are ignored). The statistical math itself is
conformance-tested with hand-computed vectors in `evaluation_test.go`.

## Result DB reads (`internal/resultdb`)

The PPS is the sole Result DB reader and only ever issues SELECTs: it never
writes the Result DB and never touches the Core DB.

`Fetch`
([`internal/resultdb/resultdb.go`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/post-processing-service/internal/resultdb/resultdb.go))
per evaluation request:

1. connects through the pgx connector with the mounted Secret's endpoint
   (`sslmode=disable`);
2. sets `SET statement_timeout = '30000'` (30 seconds, mirroring the
   runner's statement-timeout discipline);
3. runs `SELECT result FROM public.scenario_<scenario-id>_results` — one
   result table per scenario (`ResultTableName`);
4. extracts the finite-float `mean_wait_time` from each JSONB result record.

A row whose `mean_wait_time` is missing, JSON null, non-numeric, or non-finite
is skipped and counted as malformed (`FetchResult.Malformed`, logged); it
enters no computation. An empty table returns a zero-row result without
error. The result row shape is built by the runner's
`build_result` in
[`component-templates/translator/internal/generator/runnermod/result.py`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/translator/internal/generator/runnermod/result.py)
and carries exactly: `parameterset_id`, `arrival_rate`, `service_rate`,
`run_duration`, `seed_policy` (the baked scenario parameters),
`effective_seed`, `completed_customers`, `mean_wait_time` (the reference KPI
the PPS reads; `scenario_id` is excluded — it is the table/partition key).

Fetch failures are returned to the caller, which leaves the request
unacknowledged (NAK) for redelivery; there is no built-in retry, mirroring the
Translator's taxonomy.

## Verdict application (Scenario Manager side, for context)

The SM consumes the verdict on the per-scenario evaluation subject and applies
the matching guarded transition in
[`scenario-manager/internal/verdict/verdict.go`](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/verdict/verdict.go):
`met` → `MarkScenarioFinished` (only a row currently in `PostProcessing` may
finish; `Finished` is terminal); `additional_runners` →
`ClaimScenarioForEvaluationRound` (increments `runner_round`, stores the count
as the new round's `round_reps`, resets `round_computed_reps` to 0, and moves
the row `PostProcessing → StartingRunners`; rounds then flow through
`StartingRunners → InProcessing → PostProcessing`); `stop_unmet` →
`MarkScenarioFailedFrom(PostProcessing)` (the maximum-replications stop
criterion). Any other verdict value is permanent poison.

## Image and security

The
[Dockerfile](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/post-processing-service/Dockerfile)
requires the locked `PPS_GO_BUILDER_IMAGE` build arg (Go toolchain,
linux/amd64 manifest digest; a missing argument fails the build) and copies
only the static, stripped binary into a `scratch` final stage running as
`USER 1000:1000` (satisfying the operator's restricted security context). The
PPS owns no Kubernetes credentials (no client-go, no in-cluster config); its
identity comes exclusively from the downward-API environment. No secrets are
baked in.
