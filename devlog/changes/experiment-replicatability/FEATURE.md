# FEATURE.md — Deterministic replication seeds

**Status: DRAFT — architecture discussion in progress; no implementation slices are approved or dispatched.**

Companion human-readable thesis document:
[`THESIS_EXPLANATION.md`](THESIS_EXPLANATION.md). Any accepted change to the
randomness contract, ownership boundaries, retry semantics, persistence model,
or replay behavior must update both documents in the same change.

## 1. Mission

CBSE shall assign every logical simulation replication a distinct,
deterministically reproducible random-number stream without depending on Pod
names, wall-clock time, node identity, scheduling order, or a shared central
random-number service.

The feature replaces the reference runner's infrastructure-derived seed with a
hierarchical scientific identity:

```text
experiment root seed
        + scenario seed
        + global replication index
        -> effective replication seed
        -> runner-local random-number generator
```

The implementation must preserve container isolation and user-model
flexibility. Scenario Manager identifies the logical replication; the
digest-pinned runner base image derives its effective seed; the generated model
runner selects and initializes the model-specific RNG.

## 2. Current implementation

### 2.1 Current reference seed

The reference Translator runner currently reads the runtime hostname and derives
its local seed as:

```text
effective_seed = first 63 bits of SHA-256(seed_policy + ":" + hostname)
```

It then constructs:

```python
rng = random.Random(effective_seed)
```

The result record already persists both `seed_policy` and `effective_seed`.
Seeds are therefore observable after execution, but the normal production path
cannot request a future replication with a recorded effective seed.

### 2.2 Indexed Job behavior

Scenario Manager creates Kubernetes Jobs with:

```text
completionMode = Indexed
completions    = current round's requested replications
parallelism    = current round's requested replications
restartPolicy  = Never
```

Kubernetes assigns each logical completion a round-local index in:

```text
metadata.annotations['batch.kubernetes.io/job-completion-index']
```

For an Indexed Job, the Pod name contains a random suffix, but the documented
Pod hostname is `$(job-name)-$(index)`. A replacement Pod for the same index in
the same Job therefore normally receives the same hostname and, under the
current reference algorithm, the same seed. The principal weakness is not a
same-Job retry; it is that the seed remains coupled to Kubernetes Job identity,
changes when the experiment is recreated, and cannot be reconstructed from
explicit scientific coordinates.

### 2.3 Additional PPS rounds

Each PPS request for additional replications creates a new round-specific Job:

```text
round 1: simrun-<uid>-s<scenario>-a<attempt>
round 2: simrun-<uid>-s<scenario>-a<attempt>-r2
round 3: simrun-<uid>-s<scenario>-a<attempt>-r3
```

Indices restart at zero in every Job. Scenario Manager already persists:

- `number_of_computed_reps`: cumulative successful replications across rounds;
- `round_reps`: requested replications in the current round;
- `round_computed_reps`: successful replications in the current round;
- `runner_round`: current runner round.

The runner-start projection already loads `number_of_computed_reps`, but the
current `RunnerStartRequest` does not carry it into effective-Job construction.

## 3. Settled architecture rulings

### R1 — Every replication requires a distinct stream

A scenario-level seed alone is not sufficient if every isolated runner resets
the same RNG with it. Separate RNG objects initialized from the same seed
produce the same sequence and can create identical observations, zero estimated
variance, and a falsely narrow confidence interval.

Every logical replication must therefore receive a distinct deterministic
stream. The portable v0.1 mechanism is a replication-specific effective seed.
A user model with a splittable or counter-based generator may interpret the same
replication identity as a formal substream instead of a scalar seed.

### R2 — Experiment root seed lives in `SimulationExperiment`

The experiment-level root seed is durable scientific configuration owned by the
`SimulationExperiment` CR. The initial API direction is conceptually:

```yaml
spec:
  randomness:
    rootSeed: 123456789
```

The precise integer width, validation range, required/defaulted behavior, and
immutability rule remain API details to close before implementation.

The root seed is not a credential. It belongs in exported provenance and must be
stable for the lifetime of the experiment.

### R3 — Scenario seed comes from EDS recipe information

The Experimental Design Service owns the scenario-level seed as part of the
scenario's recipe information. The Translator resolves and validates it while
generating the scenario runner image.

The current reference design already carries an integer `seed_policy`. A later
ruling must decide whether v0.1:

1. formally defines the existing `seed_policy` value as the scenario seed; or
2. introduces a new explicit `scenario_seed` recipe field and migration.

Whatever name is chosen, the scenario seed must be deterministic, persisted,
exported, and available to the generated runner without Scenario Manager having
to understand model-specific recipe JSON.

### R4 — Scenario Manager owns replication coordinates

Scenario Manager owns only the orchestration coordinates needed to identify the
logical replication. For every runner Job it supplies:

```text
CBSE_ROOT_SEED
CBSE_REPLICATION_OFFSET
CBSE_COMPLETION_INDEX
```

where:

- `CBSE_ROOT_SEED` is read from the live `SimulationExperiment`;
- `CBSE_REPLICATION_OFFSET` is the scenario's `number_of_computed_reps` before
  the new round starts;
- `CBSE_COMPLETION_INDEX` is projected by Kubernetes from the Indexed Job Pod's
  `batch.kubernetes.io/job-completion-index` annotation.

The global replication index is:

```text
global_replication_index = replication_offset + completion_index
```

Scenario Manager does not construct an RNG and does not calculate the final
effective seed.

### R5 — Wave partitioning must not affect stream identity

For thirty initial and forty additional replications:

| Round | Offset | Job completion indices | Global replication indices |
|---:|---:|---:|---:|
| 1 | 0 | 0–29 | 0–29 |
| 2 | 30 | 0–39 | 30–69 |

Executing seventy replications in one wave or as thirty plus forty must assign
the same logical stream identities `0–69`. PPS precision decisions may change
when work is scheduled, but not which streams belong to the first N
replications.

### R6 — Seed derivation implementation lives in the runner base image

The digest-pinned runner base image implements exactly one seed-derivation
algorithm. It receives the runtime coordinates, obtains the Translator-provided
scenario seed, computes the global replication index, and derives the effective
seed locally in every runner Pod.

The `SimulationExperiment` CR does **not** expose a selectable
`derivationVersion`. A runtime selector would either be meaningless when the
base supports one algorithm or would require every base image to carry all
historical algorithms.

When the algorithm changes, the release publishes a new base-image digest. An
old experiment remains tied to the old generated-runner/base-image digest and
therefore to its original derivation implementation.

The base image may emit a constant human-readable identifier such as
`cbse-seed-v1` in result provenance and test vectors. That identifier describes
what the immutable artifact did; it is not user-selectable configuration. The
runner image digest remains the authoritative implementation identity.

### R7 — Translator supplies the scenario seed and RNG integration

The Translator-generated image supplies the scenario seed to the base-image
interface, preferably through a reserved environment value or generated
scenario configuration. The exact representation remains to be ruled.

The generated runner uses the base-image derivation facility, obtains the
resulting effective seed, and initializes the model-specific local RNG. The
Translator may select Python `random`, NumPy, R, Java, a counter-based generator,
or several named model substreams, but it must not redefine the framework's
logical replication index.

### R8 — No centralized RNG service

CBSE will not distribute random values from a shared service. A central service
would introduce request-order dependence, network retry ambiguity, contention,
a new availability dependency, and concurrency-sensitive replay.

Each runner continues to use its own local RNG. CBSE standardizes deterministic
stream assignment, not random-value transport.

## 4. Proposed runtime contract

### 4.1 Inputs owned by Scenario Manager/Kubernetes

Scenario Manager extends `RunnerStartRequest` and effective-Job construction
with the cumulative offset. It injects framework-owned environment variables
into the `runner` container after the user Job template has been validated:

```text
CBSE_ROOT_SEED=<experiment root seed>
CBSE_REPLICATION_OFFSET=<number_of_computed_reps before this round>
CBSE_COMPLETION_INDEX=<Downward API completion index>
```

Conceptual Pod-template projection:

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

The Operator's Job-template policy must reserve all framework-owned `CBSE_*`
randomness names so a user template cannot override them. The existing custom
template validator intentionally does not permit annotation field projection in
user environment values; this feature should keep the completion-index
projection framework-owned rather than broadening user control.

### 4.2 Input owned by Translator

The Translator provides:

```text
CBSE_SCENARIO_SEED=<scenario seed resolved from recipe information>
```

Possible representations to rule:

- a generated image `ENV` value;
- a field in `/runner/scenario.json` read by the base-image library;
- a generated non-secret randomness descriptor file.

The selected representation must be immutable within the generated image,
validated before model execution, and included in result provenance.

### 4.3 Base-image calculation

Conceptually:

```python
root_seed = read_root_seed()
scenario_seed = read_scenario_seed()
offset = read_non_negative_int("CBSE_REPLICATION_OFFSET")
completion_index = read_non_negative_int("CBSE_COMPLETION_INDEX")

replication_index = offset + completion_index
effective_seed = derive(root_seed, scenario_seed, replication_index)
```

The base facility must fail closed on missing/malformed values, overflow, an
unsupported runtime contract, or an invalid index. It must never silently fall
back to hostname- or time-derived randomness.

## 5. Seed-derivation contract

The exact first algorithm is not yet ruled. A candidate is a byte-exact
HMAC-SHA-256 construction:

```text
effective_seed = first 63 bits of HMAC-SHA-256(
    key     = encoded experiment root seed,
    message = domain separator || encoded scenario seed || encoded replication index
)
```

Before implementation, the normative specification must fix:

- accepted integer ranges;
- signedness and byte order;
- fixed-width versus length-prefixed encoding;
- domain-separator bytes;
- digest truncation and masking;
- effective-seed range;
- error behavior;
- cross-language test vectors.

A truncated hash has a theoretical collision probability. The contract should
state the accepted risk and whether manifests/result ingestion detect duplicate
effective seeds. Distinct logical replication identities remain mandatory even
if the implementation cannot provide a mathematical proof that all downstream
RNG subsequences are non-overlapping.

## 6. Retry and duplicate-execution semantics

A Kubernetes replacement Pod for the same Indexed Job completion index receives
the same offset and completion index, and therefore the same global replication
index and effective seed. This is the desired retry behavior: infrastructure
retries repeat the same statistical replication.

Kubernetes may create more than one Pod object for an index over the Job's
lifetime. A runner may also write a result and fail before Kubernetes records
successful completion. Result persistence must therefore be idempotent by
logical replication identity, not merely append-only.

A candidate uniqueness identity is:

```text
(experiment identity, scenario identity, global replication index)
```

The exact Result DB key/schema migration and how PPS excludes duplicate attempts
remain open design work.

## 7. Result and provenance contract

Every accepted replication result should expose at least:

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

`seed_derivation` is an implementation-emitted provenance constant, not a CR
input. Provenance also retains:

- generated runner image digest;
- base-image digest, directly or through the generated-image provenance;
- model and dependency versions;
- scenario parameters and recipe identity;
- experiment design and root seed;
- any named substream policy;
- retry/attempt identity if duplicate execution occurred.

Persisting the effective seed is technically redundant when all derivation
inputs and the immutable image are retained, but remains valuable for auditing,
single-replication replay, and implementation-error detection.

## 8. Statistical interpretation

### 8.1 Independence

The framework guarantees distinct deterministic stream assignments. It does not
claim that arbitrary RNG algorithms provide formally non-overlapping
subsequences. Stronger guarantees depend on the model's selected RNG and may
require stream splitting, jump-ahead, or counter-based construction.

### 8.2 Same seed does not guarantee same result across software changes

Exact replay additionally requires the same model code, RNG algorithm,
distribution implementation, dependency versions, relevant architecture and
floating-point behavior, and external inputs. The immutable runner image is
therefore part of the scientific record.

### 8.3 Optional named substreams

A later extension may derive named substreams for arrivals, service durations,
patience, routing, failures, or other stochastic processes. This can isolate
model changes and support intentional common-random-number designs. It is not
required for the first deterministic-replication implementation.

## 9. Proposed responsibility matrix

| Concern | Owner |
|---|---|
| Experiment root seed | `SimulationExperiment` API |
| Scenario seed | EDS recipe; resolved and embedded by Translator |
| Cumulative replication offset | Scenario Manager/Core DB |
| Round-local completion index | Kubernetes Indexed Job; projected by Scenario Manager |
| Global replication index calculation | Runner base-image library |
| Effective-seed derivation algorithm | Digest-pinned runner base image |
| Derivation identifier | Base-image constant emitted as provenance |
| Model RNG selection and use | Translator-generated runner/model |
| Effective seed and stream provenance | Runner result contract |
| Retry duplicate prevention | Result DB/result-ingestion contract |
| Precision decision and additional wave size | PPS; no seed ownership |

## 10. Preliminary implementation slices

These are a draft sequencing proposal, not yet worker-ready specifications.

1. **D1 — API and persisted identity**
   - add root seed to `SimulationExperiment`;
   - settle scenario-seed naming/recipe contract;
   - define result/provenance fields and idempotency identity.
2. **D2 — Scenario Manager replication coordinates**
   - carry `number_of_computed_reps` into `RunnerStartRequest`/`BuildRequest`;
   - inject root seed and offset;
   - project the Indexed Job completion annotation;
   - reserve framework environment names.
3. **D3 — Base-image derivation facility**
   - implement the byte-exact derivation and validation;
   - publish test vectors;
   - expose effective seed/global index to generated runners.
4. **D4 — Translator/reference-runner integration**
   - provide scenario seed through the ruled interface;
   - remove hostname-derived seeding;
   - instantiate local RNG and extend results.
5. **D5 — Idempotent result persistence and PPS compatibility**
   - prevent duplicate logical replications from being counted twice;
   - preserve existing KPI/statistical evaluation semantics.
6. **D6 — Harness and replay acceptance**
   - prove one-wave versus multi-wave stream equivalence;
   - prove same-index retry stability;
   - prove recreated experiment seed reconstruction;
   - run the unchanged functional suite through manifest and Helm installers.
7. **D7 — Documentation and thesis synchronization**
   - user/API/operations guide;
   - reproducibility manifest and limitations;
   - update this file and `THESIS_EXPLANATION.md` together.

## 11. Required acceptance properties

The feature is incomplete until automated evidence proves:

1. Same root seed, scenario seed, global replication index, and immutable
   derivation implementation produce the same effective seed.
2. Different tested replication indices produce different effective seeds.
3. Pod name, hostname, node, start time, and scheduling order do not affect the
   effective seed.
4. Replacement Pods for the same completion index receive the same global index
   and effective seed.
5. Round 2 starts at the previous cumulative completed count.
6. One wave of N replications and several waves totaling N assign the same
   global indices and effective seeds.
7. Recreating an experiment with the same CR root seed, EDS scenario seeds, and
   image digests reconstructs the same streams.
8. Missing or malformed runtime coordinates fail the runner rather than falling
   back to nondeterministic seeding.
9. Result provenance records the complete identity and actual effective seed.
10. Duplicate attempts of one logical replication cannot inflate PPS sample
    size.
11. User Translators remain free to select a model-appropriate local RNG while
    respecting framework replication identity.
12. Both manifest and Helm installation modes pass the same full smoke suite.

## 12. Open rulings before implementation

1. Exact `SimulationExperiment` field shape, integer width/range, required versus
   generated root seed, and update immutability.
2. Reuse/definition of `seed_policy` versus a new `scenario_seed` recipe field.
3. Exact Translator-to-base scenario-seed representation.
4. Exact derivation algorithm and byte encoding.
5. Whether the base facility is a Python package, language-neutral executable,
   entrypoint wrapper, or a combination.
6. Result DB uniqueness/schema migration and behavior for a duplicate attempt.
7. Replay surface: full experiment recreation only, explicit single-replication
   replay, or both.
8. Whether the first release includes named model substreams or defers them.

## 13. Thesis-level interpretation

The architectural distinction is between **orchestration identity** and
**statistical identity**. Kubernetes determines where and when a replication
executes. CBSE's experiment definition determines which statistical replication
it is.

Under the proposed design, the control plane supplies stable experiment and
replication coordinates, the immutable runner base image performs one audited
seed derivation, and the user model consumes the resulting local random stream.
This keeps Kubernetes replaceable, preserves model independence, and makes
computational replay and statistical provenance explicit.
