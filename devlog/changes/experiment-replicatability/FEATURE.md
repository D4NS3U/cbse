# Experiment Replicatability: Deterministic Random Streams

Status: design discussion; not yet approved for implementation

Companion human-readable thesis document:
[`THESIS_EXPLANATION.md`](THESIS_EXPLANATION.md). Any accepted change to the
randomness contract, ownership boundaries, retry semantics, persistence model,
or replay behavior must update both documents in the same change.

## 1. Motivation

CBSE executes scenario replications as isolated containers in Kubernetes Indexed
Jobs. Every simulation runner needs an independent pseudo-random number stream.
The current reference runner creates that stream by combining the scenario's
`seed_policy` with the runner Pod's hostname:

```text
effective_seed = first 63 bits of SHA-256(seed_policy + ":" + pod_hostname)
```

The runner then initializes a process-local generator:

```python
rng = random.Random(effective_seed)
```

The resulting `effective_seed` is persisted in the replication result. This
provides distinct streams in normal operation and allows a completed result to
be traced back to its effective seed. It does not, however, provide a stable
statistical identity for a logical replication: Pod names are infrastructure
identities and can change when an experiment is recreated or a Pod is retried.
The normal production path also has no mechanism for requesting a replay with a
previously recorded seed.

The objective of this feature is therefore not to share one random-number
generator between containers. Instead, CBSE should assign every logical
replication a deterministic stream descriptor from which an isolated runner can
construct its own local generator.

## 2. Current behavior and its limitations

### 2.1 Seeds are not time-based

The reference implementation does not derive seeds from system time. It uses a
stable hash of `seed_policy` and the runtime hostname. In Kubernetes, the
container hostname normally corresponds to the Pod name.

### 2.2 Different runner rounds do not normally repeat Pod-derived seeds

Scenario Manager gives every runner round a distinct Job name:

```text
round 1: simrun-<experiment-uid>-s<scenario>-a<attempt>
round 2: simrun-<experiment-uid>-s<scenario>-a<attempt>-r2
round 3: simrun-<experiment-uid>-s<scenario>-a<attempt>-r3
```

Although the completion indices of each Indexed Job start again at zero, the
round-specific Job-name component changes. The resulting Pod hostnames, and
therefore the hostname-derived seeds, differ across rounds.

For example, if round 1 computes 30 replications and PPS requests 40 additional
replications, round 2 executes 40 new replications. It does not execute a
cumulative 40-replication Job containing the original first 30.

### 2.3 Infrastructure identity is not statistical identity

The remaining weakness is that a logical replication is identified indirectly
through a transient Pod hostname. If Kubernetes replaces a Pod for the same
Indexed Job completion index, the replacement can receive a different Pod name
and therefore a different seed. Recreating an experiment also creates different
Job and Pod identities.

Consequently:

- completed effective seeds are observable and persisted;
- normal replications receive distinct streams in practice;
- a logical replication does not necessarily retain its stream across an
  infrastructure retry;
- an experiment cannot be recreated through the normal workflow with the same
  streams;
- wave boundaries and Kubernetes object identities are unnecessarily involved
  in the random-stream definition.

## 3. Design principle

The framework should distinguish three concerns:

1. **Statistical identity:** which experiment, scenario, and replication is
   being executed?
2. **Stream derivation:** which deterministic random stream belongs to that
   statistical identity?
3. **Model integration:** how does the user model instantiate and consume its
   preferred random-number generator?

No central RNG service should distribute random values to runner containers.
Such a service would introduce request-order effects, network contention, an
additional availability dependency, and concurrency-dependent replay behavior.
Every runner should continue using a local generator.

Instead, CBSE should provide a deterministic, versioned stream descriptor to
each runner.

## 4. Recommended ownership split

Neither Scenario Manager nor Translator should own the complete mechanism.
Their responsibilities are complementary.

### 4.1 Experiment API or experiment design

The experiment-facing API should own the durable root seed and replay policy.
A conceptual API is:

```yaml
spec:
  randomness:
    mode: fixed
    rootSeed: 123456789
    derivation: cbse-v1
```

A generated mode may also be supported:

```yaml
spec:
  randomness:
    mode: generated
    derivation: cbse-v1
```

In generated mode, the control plane must generate the root seed exactly once
and persist it before any runner starts. The seed is scientific provenance, not
a credential.

Whether this contract ultimately lives in `SimulationExperiment`, EDS input,
or a dedicated experiment-design object is an API decision. Its required
properties are durability, immutability after execution begins, exportability,
and replayability.

### 4.2 Experimental Design Service and Core DB

Each scenario should have a stable scientific stream identifier, for example:

```json
{
  "scenario_stream_id": "baseline-lambda-4-mu-5"
}
```

This identifier should not be a generated database primary key if experiments
must be reproducible after recreation. A new experiment may assign different
Core DB scenario IDs. The stream identifier may instead be supplied by the
user or generated deterministically from the experimental design, and it should
be persisted in Core DB.

The API must define how duplicate scenarios are handled. Two scenarios with the
same model parameters may legitimately require either independent streams or
common random numbers. An explicit stream identifier makes that choice visible
instead of accidental.

### 4.3 Scenario Manager

Scenario Manager owns runtime orchestration identity and should supply the
coordinates needed to identify a logical replication:

- the experiment's root seed or a durable reference to it;
- the scenario's stable stream identifier;
- the number of replications completed before the current runner round;
- the current Indexed Job completion index;
- the random-stream contract version.

Scenario Manager already reads `number_of_computed_reps` when starting a new
round. It can inject this value as a replication offset:

```text
CBSE_REPLICATION_OFFSET=<number of replications completed before this round>
```

It can expose Kubernetes' Indexed Job completion index through the Downward API:

```yaml
env:
  - name: CBSE_COMPLETION_INDEX
    valueFrom:
      fieldRef:
        fieldPath: metadata.annotations['batch.kubernetes.io/job-completion-index']
```

The logical global replication index is then:

```text
global_replication_index = replication_offset + completion_index
```

Example:

| Runner round | Completed before round | Job indices | Global replication indices |
|---:|---:|---:|---:|
| 1 | 0 | 0-29 | 0-29 |
| 2 | 30 | 0-39 | 30-69 |
| 3 | 70 | 0-9 | 70-79 |

This makes stream assignment independent of wave partitioning: computing 70
replications in one Job yields the same logical replication indices as
computing 30 and then 40.

### 4.4 Translator-generated runner

The Translator knows how the user model consumes randomness. Its generated
runner should consume the standardized CBSE stream descriptor, apply the
versioned derivation contract, and instantiate a model-local RNG.

The Translator must not independently invent orchestration coordinates because
it does not know future PPS rounds, cumulative offsets, or whether Kubernetes
is retrying an existing completion index.

Conversely, Scenario Manager should not attempt to construct the model's RNG.
It cannot know whether a model uses Python `random`, NumPy, R, Java, a
counter-based generator, or several domain-specific substreams.

The framework therefore standardizes the inputs and derivation semantics while
the user-controlled Translator integrates them into the model runtime.

## 5. Versioned stream derivation

A conceptual `cbse-v1` derivation is:

```text
effective_seed = first 63 bits of HMAC-SHA-256(
    key     = encoded root_seed,
    message = "cbse-v1:" + scenario_stream_id + ":" + global_replication_index
)
```

Equivalent pseudocode:

```python
effective_seed = derive_seed(
    root_seed=123456789,
    scenario_stream_id="baseline-lambda-4-mu-5",
    replication_index=37,
    derivation="cbse-v1",
)
```

The encoding of every input, separators, integer widths, byte order, hash
truncation, and accepted value range must be specified byte-for-byte and guarded
by cross-language test vectors. Merely naming HMAC-SHA-256 is insufficient for
an interoperable contract.

The derivation version must be immutable. A future algorithm should use a new
identifier such as `cbse-v2`; it must not silently change `cbse-v1`.

## 6. Retry semantics

Assume a Job round starts with offset 30 and Kubernetes retries completion index
7. Both the original and replacement Pod receive:

```text
replication_offset = 30
completion_index = 7
global_replication_index = 37
```

Both therefore derive:

```text
(root_seed, scenario_stream_id, 37) -> same effective_seed
```

A Kubernetes retry becomes a retry of the same statistical replication rather
than an unplanned new sample.

Result insertion still requires idempotency. The Result DB should be able to
identify a result by stable replication identity, for example:

```text
(scenario_stream_id, global_replication_index)
```

or by an equivalent framework-owned key. Retrying a successful insert must not
create a second observation for the same logical replication.

## 7. RNG selection and statistical considerations

The framework should define a deterministic stream descriptor rather than
mandating one universal RNG implementation for every user model.

The reference runner may initially continue with:

```python
rng = random.Random(effective_seed)
```

A user Translator could instead use a generator such as:

```python
numpy.random.Generator(numpy.random.PCG64DXSM(effective_seed))
```

or a counter-based generator such as Philox.

Distinct hashed seeds are operationally useful, but they are not by themselves
a mathematical proof of non-overlapping random-number subsequences. For models
with stronger requirements, the Translator should be able to use a generator
with documented stream-splitting, jumping, or counter-based semantics. The
result provenance must identify the RNG algorithm and runtime version.

Exact reproduction also depends on the immutable runner image, model code,
dependency versions, and distribution implementations. Equal seeds alone do not
guarantee equal output across different software environments.

## 8. Model-level substreams

A simulation may consume randomness for conceptually different processes:

- arrivals;
- service durations;
- patience or abandonment;
- routing decisions;
- failures and repairs.

The Translator should be able to derive named substreams from the replication
stream, for example:

```text
replication 37 / arrivals
replication 37 / service
replication 37 / patience
```

A conceptual derivation is:

```text
substream_seed = HMAC-SHA-256(
    key     = replication_seed,
    message = "cbse-v1-substream:" + substream_name
)
```

Named substreams prevent changes to one stochastic process from shifting all
random draws used by unrelated processes. They can also support variance
reduction techniques such as common random numbers when scenarios are compared.
The use of common streams must be explicit in the experiment design rather than
an accidental consequence of equal parameters.

## 9. Result and provenance contract

Every replication result should record enough information to reconstruct its
stream assignment:

```json
{
  "randomness_contract": "cbse-v1",
  "root_seed": 123456789,
  "scenario_stream_id": "baseline-lambda-4-mu-5",
  "replication_index": 37,
  "effective_seed": 8072139487123,
  "rng_algorithm": "python-random-mt19937"
}
```

The provenance bundle should additionally retain:

- the immutable runner image digest;
- model and dependency versions;
- the complete experiment design;
- the root-seed mode and value;
- stream-derivation test vectors or contract version;
- any named substream policy;
- the relationship between scenario identifiers and stream identifiers.

The root seed is generally not secret. If future domains treat seeds as
sensitive, that must be an explicit policy rather than an assumption in the
base contract.

## 10. Proposed responsibility matrix

| Concern | Recommended owner |
|---|---|
| Experiment root seed and replay mode | Experiment API / experimental design |
| Stable scenario stream identifier | EDS input and Core DB |
| Cumulative replication offset | Scenario Manager |
| Indexed Job completion index | Kubernetes, exposed by Scenario Manager |
| Versioned environment or file contract | CBSE framework |
| Seed-derivation implementation | Translator-generated runner, conforming to the CBSE contract |
| Model-specific RNG and named substreams | User Translator and model |
| Stream and RNG provenance | Runner result contract |
| Duplicate-result prevention for retries | Result storage contract |
| Precision evaluation and extra-wave decision | PPS; no seed ownership |

## 11. Suggested incremental implementation

### Phase 1: stable replication identity

1. Add an experiment root-seed contract with fixed and generated modes.
2. Generate and persist a generated seed before the first scenario starts.
3. Add and persist a stable `scenario_stream_id`.
4. Reserve framework-owned `CBSE_*` runner environment variables.
5. Have Scenario Manager inject the root seed, stream ID, derivation version,
   cumulative offset, and Indexed Job completion index.
6. Define the global replication index as offset plus completion index.

### Phase 2: reference-runner integration

1. Define the byte-exact `cbse-v1` derivation and test vectors.
2. Update the reference Translator runner to stop using the hostname.
3. Instantiate its local RNG from the derived effective seed.
4. Persist the complete stream descriptor and RNG algorithm in every result.
5. Add tests proving stability across Pod-name changes and retries.

### Phase 3: replay and storage safety

1. Add an experiment replay mode that accepts an exported root seed and design.
2. Make result writes idempotent by logical replication identity.
3. Prove that 70 replications in one wave use the same stream identities as
   30 plus 40 replications across two waves.
4. Prove that retrying one completion index reproduces the same result without
   adding a duplicate observation.
5. Export a machine-readable reproducibility manifest.

### Phase 4: advanced statistical streams

1. Add a standard named-substream convention.
2. Provide reference adapters for appropriate splittable or counter-based RNGs.
3. Document common-random-number experiment designs and their inference
   implications.
4. Validate exact replay across the supported runner images and language
   runtimes.

## 12. Required acceptance properties

The feature should not be considered complete until automated tests establish
all of the following:

1. Identical root seed, scenario stream ID, replication index, and contract
   version produce the same effective seed.
2. Pod name, hostname, node, scheduling order, and wall-clock time do not affect
   the effective seed.
3. Distinct global replication indices produce distinct effective seeds for the
   tested contract vectors.
4. A replacement Pod for the same Indexed Job completion index receives the
   same stream.
5. Additional PPS rounds continue from the next global replication index.
6. Wave partitioning does not affect stream assignment.
7. Recreating an experiment from the same reproducibility manifest reconstructs
   the same streams.
8. Results record the stream descriptor, effective seed, RNG algorithm, and
   immutable runner image.
9. Retried result insertion cannot count one logical replication twice.
10. User Translators can select a model-appropriate RNG without redefining the
    framework's replication identity.

## 13. Thesis-level interpretation

The central architectural distinction is between **orchestration identity** and
**statistical identity**. Kubernetes Jobs and Pods are execution mechanisms;
their names are unsuitable as the durable definition of a random stream. CBSE
should instead assign streams from explicit experimental coordinates and treat
Kubernetes as a replaceable execution substrate.

Under this design, the control plane determines *which logical replication is
running*, while the user-provided Translator determines *how that replication's
deterministic entropy is consumed by the model*. This preserves the framework's
model independence without sacrificing experiment reproducibility, retry
stability, or statistical traceability.
