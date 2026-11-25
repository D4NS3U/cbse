# Deterministic Replication Seeds in CBSE

## Human-readable thesis background and design explanation

> **Purpose:** This document explains the statistical motivation, current CBSE
> behavior, and proposed deterministic replication-seed architecture in a
> narrative form intended for thesis writing and human review. The normative
> engineering draft is [`FEATURE.md`](FEATURE.md).
>
> **Maintenance rule:** Whenever the accepted API, seed derivation, runtime
> interface, retry behavior, persistence model, or replay semantics change,
> this document and `FEATURE.md` must be updated together. Current behavior and
> proposed behavior must remain clearly separated.

## 1. The underlying statistical problem

A stochastic simulation uses a pseudo-random number generator to sample values
such as interarrival times, service durations, customer patience, component
failures, routing decisions, or repair times. A pseudo-random generator is a
deterministic algorithm: once initialized with the same state, it produces the
same sequence.

This makes seed management part of the scientific design rather than a minor
implementation detail.

A simulation study normally executes several independent replications of one
scenario. Those observations are used to estimate a mean, variance, confidence
interval, or another statistic. If every replication initializes the same RNG
with the same seed, each isolated process may produce exactly the same sequence
and result. Thirty identical observations do not provide thirty independent
samples. They may even create an estimated variance of zero and cause a
precision controller to report a misleadingly narrow confidence interval.

Separate containers do not solve this by themselves. The following creates
three physically separate RNG objects but gives all three the same sequence:

```python
# Executed independently in three containers.
rng = random.Random(12345)
```

Asynchronous start times also do not help when time is not part of the RNG
state. The requirement is not merely “a separate RNG object per container.” The
requirement is:

> Every logical replication must receive a distinct random stream, and that
> stream must be deterministically reconstructible when the replication is
> retried or replayed.

## 2. Current CBSE execution flow

CBSE executes one scenario as one or more waves of Kubernetes runner Pods:

1. The **Experimental Design Service (EDS)** describes the scenario, its model
   recipe, and its initial replication count.
2. The **Translator** resolves the recipe and generates an immutable
   scenario-specific runner image.
3. The **Scenario Manager (SM)** creates a Kubernetes Indexed Job.
4. Kubernetes assigns one completion index to each logical Job completion.
5. Each runner Pod executes one model replication and writes one result.
6. The **Post-Processing Service (PPS)** evaluates the accumulated observations.
7. If precision is insufficient, PPS requests an additional runner wave and SM
   creates another round-specific Indexed Job.

The second wave contains only additional replications. If the first wave
contains thirty and PPS asks for forty more, CBSE executes a new forty-completion
Job. It does not rerun a cumulative forty containing the first thirty.

## 3. Current seed behavior

The current reference runner does not use system time. It reads the runtime
hostname and derives:

```text
effective_seed = first 63 bits of SHA-256(seed_policy + ":" + hostname)
```

It then constructs a local Python generator:

```python
rng = random.Random(effective_seed)
```

The result includes both `seed_policy` and `effective_seed`. This is already a
valuable provenance property: after a successful replication, the seed actually
used is known.

### 3.1 Indexed Job identities

CBSE uses Kubernetes Indexed Jobs. A Job with thirty completions assigns
round-local indices `0–29`. Kubernetes exposes the index through:

```text
metadata.annotations['batch.kubernetes.io/job-completion-index']
```

For an Indexed Job, the Pod name includes a random suffix, while the documented
Pod hostname has the form:

```text
$(job-name)-$(index)
```

This distinction matters. If the Pod for index 7 fails, `restartPolicy: Never`
means Kubernetes creates a replacement Pod rather than restarting the same Pod.
The replacement has another full Pod name, but it retains index 7 and normally
the same indexed hostname. The current hostname-derived reference seed is
therefore already stable for a replacement of the same index within the same
Job.

### 3.2 Different runner rounds

Scenario Manager gives every later wave a round-specific Job name:

```text
round 1: simrun-<experiment-uid>-s<scenario>-a<attempt>
round 2: simrun-<experiment-uid>-s<scenario>-a<attempt>-r2
round 3: simrun-<experiment-uid>-s<scenario>-a<attempt>-r3
```

Because the Job-name component changes, the indexed hostname changes. The
current mechanism therefore normally produces new seeds in later waves despite
indices restarting at zero.

## 4. What remains wrong with the current mechanism

The current mechanism creates practical variation, but its scientific identity
is still derived from Kubernetes infrastructure.

If the experiment is recreated, it receives a new Kubernetes object UID and new
Job names. The same model design and `seed_policy` then produce different
hostnames and streams. The old effective seeds are visible afterward, but the
normal production path cannot request those seeds for the recreated experiment.

The current result therefore answers:

```text
Which seed did this Pod happen to use?
```

The desired design should answer:

```text
Which stream belongs to this experiment, scenario, and replication?
```

This is the central architectural change: statistical identity should not be an
accidental consequence of infrastructure identity.

## 5. The proposed hierarchy

The proposed stream assignment uses three scientific coordinates:

```text
experiment root seed
        + scenario seed
        + global replication index
        -> effective seed
```

### 5.1 Experiment root seed

The root seed identifies the randomization of the complete experiment. It lives
in the `SimulationExperiment` CR, conceptually:

```yaml
spec:
  randomness:
    rootSeed: 123456789
```

The final API still needs rulings for integer range, required versus generated
behavior, and whether updates are forbidden after execution begins. Regardless
of the final shape, the value must be durable, exportable, and stable throughout
the experiment.

The root seed is scientific provenance, not a password.

### 5.2 Scenario seed

The scenario seed separates the random streams of different scenarios. It lives
in EDS recipe information and is resolved by the Translator when it generates
the runner image.

The current reference recipe already has an integer called `seed_policy`. One
open design question is whether this existing value should formally become the
scenario seed or whether a new `scenario_seed` field should be introduced.

The scenario seed must not be inferred from a generated Core DB row ID if a
recreated experiment is expected to reconstruct the same streams. It must be
part of the persistent experimental design.

### 5.3 Global replication index

Every logical replication of one scenario receives a stable ordinal:

```text
0, 1, 2, 3, ...
```

This index continues across PPS waves. It does not restart at zero from the
statistical point of view even though each Kubernetes Job has local indices
starting at zero.

## 6. How the global replication index is obtained

Scenario Manager already persists two pieces of accounting:

- the cumulative successful count, `number_of_computed_reps`;
- the current round's requested and completed counts.

Before a new runner round starts, the cumulative count is the offset for that
round:

```text
replication_offset = number_of_computed_reps before the round
```

Kubernetes supplies the current Pod's local completion index:

```text
completion_index = Indexed Job annotation value
```

The runner base image calculates:

```text
global_replication_index = replication_offset + completion_index
```

Example:

| Runner round | Completed before round | Local Job indices | Global replication indices |
|---:|---:|---:|---:|
| 1 | 0 | 0–29 | 0–29 |
| 2 | 30 | 0–39 | 30–69 |
| 3 | 70 | 0–9 | 70–79 |

This provides wave-partition invariance. If a fixed study eventually executes
seventy replications, indices `0–69` are assigned whether Kubernetes executes
all seventy in one wave or PPS requests thirty and then forty.

## 7. How the values reach each Pod

Scenario Manager constructs the Kubernetes Job and therefore controls the Pod
template. It can inject shared values as ordinary environment variables and the
per-Pod completion index through the Kubernetes Downward API.

Conceptually:

```yaml
env:
  - name: CBSE_ROOT_SEED
    value: "123456789"
  - name: CBSE_REPLICATION_OFFSET
    value: "30"
  - name: CBSE_COMPLETION_INDEX
    valueFrom:
      fieldRef:
        fieldPath: metadata.annotations['batch.kubernetes.io/job-completion-index']
```

Every Pod in the round receives the same root seed and offset. Kubernetes
resolves a different completion index for each logical Job completion.

The Translator-generated image additionally supplies the scenario seed, for
example through:

```text
CBSE_SCENARIO_SEED=7001
```

or through a generated scenario configuration read by the base-image library.
The exact representation remains open, but Scenario Manager should not parse
model-specific recipe JSON to discover it.

The environment names are framework-owned. Custom Job templates must not be
able to override them.

## 8. Why the base image derives the seed

The digest-pinned runner base image is the best home for the derivation
implementation.

Scenario Manager knows which logical replication is running, but it should not
contain model-runtime seed code. The Translator knows the model, but it does not
know future PPS wave sizes or cumulative offsets when the image is translated.
The base image executes inside every runner Pod and can combine both sources of
information.

The base facility conceptually performs:

```python
root_seed = read_root_seed()
scenario_seed = read_scenario_seed()
offset = read_replication_offset()
completion_index = read_completion_index()

replication_index = offset + completion_index
effective_seed = derive(root_seed, scenario_seed, replication_index)
```

It then makes the effective seed available to the generated model runner.

The base facility must fail if required values are missing or malformed. It must
never silently return to hostname- or time-based seeding.

## 9. Why there is no selectable derivation version in the CR

An earlier design considered a field such as:

```yaml
derivationVersion: cbse-v1
```

This was rejected.

If a base image implements only one algorithm, the field gives the user no real
choice. If several values are accepted, every base image must implement every
historical algorithm or fail on unsupported combinations. That makes the API
and artifact compatibility unnecessarily complicated.

Instead, the derivation implementation is part of the immutable base image. A
new algorithm is released in a new image digest:

```text
base image digest A -> old derivation
base image digest B -> new derivation
```

An old experiment retains its generated runner/base-image digest and therefore
its original algorithm. A base image may emit a readable constant such as
`cbse-seed-v1` in the result, but this is provenance produced by the artifact,
not user-selectable configuration.

The image digest remains the authoritative record of the executable behavior.

## 10. Effective-seed derivation

The exact algorithm is not yet settled. One candidate is a byte-exact
HMAC-SHA-256 construction:

```text
effective_seed = first 63 bits of HMAC-SHA-256(
    key     = encoded experiment root seed,
    message = domain separator
              || encoded scenario seed
              || encoded global replication index
)
```

A complete scientific contract must specify integer widths, signedness, byte
order, domain-separator bytes, digest truncation, masking, error behavior, and
test vectors. Simply writing “HMAC-SHA-256” is not enough for independent
implementations to agree.

A truncated hash has a theoretical collision probability. It is negligible for
ordinary experiment sizes but not mathematically zero. The final design should
state the accepted risk and whether duplicate effective seeds are checked.

## 11. Retry behavior under the proposed design

Assume round 2 has offset 30 and Kubernetes executes completion index 7:

```text
global replication index = 30 + 7 = 37
```

If that Pod fails, Kubernetes creates a replacement for index 7. The replacement
receives the same offset and completion index and therefore reconstructs:

```text
(root seed, scenario seed, replication index 37)
```

It derives the same effective seed. This is desirable: an infrastructure retry
should repeat the same statistical replication, not substitute an unplanned new
observation.

## 12. Duplicate result risk

A Pod might successfully insert its result and then fail before Kubernetes
records completion. A replacement Pod can rerun the same logical replication
and attempt another insert.

The deterministic seed design therefore exposes a related storage requirement:
results must be idempotent by logical replication identity. A candidate key is:

```text
(experiment identity, scenario identity, global replication index)
```

Without this protection, PPS could count two attempts of the same replication
as two independent observations. The exact Result DB schema and duplicate-write
behavior remain to be designed.

## 13. Local RNG and user flexibility

The base image should derive the replication's effective seed, but the generated
runner remains responsible for selecting and using the model's RNG.

The reference Python model may use:

```python
rng = random.Random(effective_seed)
```

Another Translator may use:

```python
rng = numpy.random.Generator(
    numpy.random.PCG64DXSM(effective_seed)
)
```

or a counter-based generator such as Philox.

This separation preserves the user-provided Translator as the model-integration
boundary. CBSE standardizes logical replication identity and deterministic seed
assignment; it does not require every simulation language to expose the same
RNG object.

Distinct derived seeds do not prove that every possible downstream RNG has
formally non-overlapping subsequences. Stronger guarantees depend on the chosen
RNG and may require explicit splitting, jump-ahead, or counter-based semantics.

## 14. Why a central RNG service is not used

A service distributing random values to all runner Pods would make results
depend on concurrent request order. Network retries could duplicate or skip
values. The service would become a bottleneck and an additional availability
dependency, and replaying the precise ordering of concurrent requests would be
difficult.

Independent local RNGs are therefore retained. The platform distributes stable
identity coordinates, not random values.

## 15. Result provenance

A result should record enough information to audit and reconstruct the stream:

```json
{
  "root_seed": 123456789,
  "scenario_seed": 7001,
  "replication_index": 37,
  "effective_seed": 8072139487123,
  "seed_derivation": "cbse-seed-v1",
  "rng_algorithm": "python-random-mt19937"
}
```

The result bundle must also retain the generated runner image digest, model and
dependency versions, scenario parameters, and experimental design.

The effective seed could theoretically be recomputed from the inputs and image,
but recording it remains useful. It proves what the runner actually used,
simplifies single-replication replay, and makes derivation defects easier to
detect.

## 16. Reproducibility terminology

Terminology differs between research fields. A useful thesis distinction is:

- **computational repeatability:** the same image and stream identity produce
  the same computational result;
- **computational reproducibility:** an exported experiment definition can
  reconstruct the same model, parameters, images, and random streams in a new
  execution;
- **statistical replication:** a new experiment intentionally uses a new root
  seed and is expected to support the same scientific conclusion without
  reproducing identical observations.

The deterministic replication-seed feature primarily supports the first two. It
must still allow a researcher to choose a different root seed for an independent
statistical replication of the study.

The repository directory retains the historical working name
`experiment-replicatability`; thesis prose should define and use the chosen
terms consistently.

## 17. What the feature would guarantee

If fully implemented and validated, the feature should establish that:

1. every logical replication receives a distinct deterministic stream identity;
2. a recreation with the same root seed, scenario seeds, replication indices,
   and image digests reconstructs the same effective seeds;
3. Pod names, nodes, scheduling order, and wall-clock time do not affect seed
   assignment;
4. a replacement Pod for the same completion index repeats the same stream;
5. additional PPS rounds continue from the cumulative replication count;
6. one large wave and several smaller waves assign the same first N streams;
7. missing stream inputs fail closed;
8. result provenance exposes the actual stream identity and software artifact;
9. duplicate execution attempts cannot inflate the statistical sample size.

## 18. What it would not guarantee automatically

The feature alone cannot guarantee that:

- arbitrary RNG algorithms have formally disjoint subsequences;
- different model code produces the same result from the same seed;
- floating-point results are identical across all architectures;
- internal model parallelism is deterministic;
- external files or services remain unchanged;
- a common-random-number design is statistically analyzed correctly;
- deterministic seeds compensate for a biased or invalid simulation model.

These limitations belong in the final reproducibility documentation and thesis
methodology.

## 19. Proposed implementation sequence

A practical implementation order is:

1. add and validate the experiment root seed in `SimulationExperiment`;
2. settle the scenario-seed recipe contract;
3. carry Scenario Manager's cumulative offset into effective-Job construction;
4. project the Kubernetes completion index into each runner;
5. reserve the randomness environment interface;
6. implement and test the base-image derivation facility;
7. update the reference Translator and runner result contract;
8. make result insertion idempotent by logical replication identity;
9. prove retry stability, wave-partition invariance, and experiment recreation;
10. validate through both manifest and Helm installation modes;
11. publish the final reproducibility and thesis documentation.

## 20. Thesis-oriented summary

The current CBSE reference implementation derives each replication seed from a
scenario seed policy and an Indexed Job hostname. This produces distinct streams
in normal operation and already preserves the effective seed in the result.
However, the random stream remains tied to Kubernetes Job identity and cannot be
reconstructed through explicit experimental coordinates after an experiment is
recreated.

The proposed architecture assigns streams hierarchically. The
`SimulationExperiment` provides an experiment root seed, EDS provides a
scenario seed, and Scenario Manager plus Kubernetes provide a global replication
index that continues across adaptive PPS waves. A digest-pinned runner base
image combines those values into an effective seed, while the user-provided
Translator initializes an appropriate local RNG for the model. The algorithm is
part of the immutable base-image artifact rather than a selectable CR field.

The central principle is:

> Kubernetes identifies an execution attempt; CBSE identifies the statistical
> replication that attempt is responsible for computing.
