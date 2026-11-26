# Deterministic replication streams

**Status: DRAFT IMPLEMENTATION PACKAGE — contracts and slices are specified, but no slice is approved for dispatch until the user accepts this draft.**

Companion thesis explanation:
[`THESIS_EXPLANATION.md`](THESIS_EXPLANATION.md). Any accepted change to the
randomness API, derivation bytes, ownership boundaries, retry semantics,
persistence identity, or replay behavior must update both documents together.

## 1. Mission

CBSE shall assign every logical replication index within a scenario a distinct,
deterministically reproducible random-number stream without depending on Pod
names, hostnames, wall-clock time, node identity, scheduling order, or a shared
random-number service. `SimulationExperiment` owns an explicit experiment root
seed, EDS recipe information owns the scenario seed, Scenario Manager and
Kubernetes identify the global replication index, the digest-pinned runner base
image derives the effective seed, and the generated model runner initializes
its local RNG. Named model substreams and a general replay API remain future
work.

## 2. Scope and compatibility

### 2.1 In scope

- an explicit immutable experiment root seed in the alpha4 CR;
- a formal definition of existing `seed_policy` as the scenario seed;
- a wave-invariant global replication index;
- framework-owned runtime-coordinate injection into Indexed Job Pods;
- one byte-exact seed derivation in the immutable runner base image;
- reference Translator/runner integration and expanded result provenance;
- retry-idempotent result storage by logical replication identity;
- deterministic PPS observation ordering by global replication index;
- deterministic-stream acceptance through manifest and Helm installers;
- user, operator, and thesis documentation.

### 2.2 Out of scope

- a central RNG or seed-allocation service;
- user-selectable seed-derivation algorithms;
- a `derivationVersion` CR field;
- changing PPS confidence-interval mathematics or wave-size policy;
- formal guarantees that arbitrary downstream RNG subsequences never overlap;
- mandatory named substreams inside a model;
- a public single-replication replay controller/API;
- support for alpha2/alpha3 APIs;
- changing Kubernetes feature gates or cluster configuration.

### 2.3 Breaking changes

- New alpha4 `SimulationExperiment` objects must supply
  `spec.randomness.rootSeed`.
- A generated runner expects three framework runtime variables. Running a new
  generated image outside CBSE without those variables fails closed.
- Newly created per-scenario result tables contain a unique
  `replication_index` column. Legacy append-only result tables are not silently
  migrated; a new runner encountering one fails with an actionable incompatible
  schema error.
- The hostname-derived effective-seed algorithm is retired for new generated
  runners. Historical results remain interpretable through their recorded
  effective seed and image digest.

### 2.4 Preserved boundaries

- Kubernetes >= 1.30 and alpha4-only API.
- Existing EDS/Detail DB field and column name `seed_policy` is preserved.
- Existing scenario parameters and PPS KPI `mean_wait_time` are preserved.
- Indexed Jobs remain the execution primitive; global `backoffLimit: 4` remains
  unchanged in this feature.
- The user-provided Translator remains the model/RNG integration boundary.
- The manifest and Helm installation modes run one unchanged smoke suite.
- Images remain digest-pinned; no insecure registry or TLS bypass is introduced.

## 3. Current behavior and defect

The reference generated runner currently calculates:

```text
effective_seed = first 63 bits of SHA-256(seed_policy + ":" + hostname)
```

and constructs `random.Random(effective_seed)`. The result persists
`seed_policy` and `effective_seed`.

For an Indexed Job the full Pod name has a random suffix, while the documented
Pod hostname is `$(job-name)-$(index)`. A replacement Pod for the same index in
the same Job therefore normally keeps the same current reference seed. The
principal defect is not same-Job retry instability. It is that statistical
identity remains coupled to Kubernetes Job identity, changes when an experiment
is recreated, and cannot be reconstructed from explicit scientific
coordinates.

Additional PPS waves create new Jobs whose local completion indices restart at
zero. Scenario Manager already persists `number_of_computed_reps`, the
cross-round successful total, and the runner-start projection already loads it,
but the current request drops it before effective-Job construction.

## 4. Workflow decisions

| # | Concern | Decision for this feature |
|---|---|---|
| D1 | Experiment identity | Required `spec.randomness.rootSeed`, integer `0..9223372036854775807`, immutable after creation. |
| D2 | Scenario identity | Preserve `seed_policy`; define its non-negative integer value as the scenario seed. Reuse is allowed and intentionally creates common random numbers across scenarios; no parallel `scenario_seed` schema is added. |
| D3 | Replication identity | `global_replication_index = number_of_computed_reps_before_round + batch.kubernetes.io/job-completion-index`. |
| D4 | Wave partitioning | The first N replications use indices `0..N-1` regardless of PPS wave boundaries. |
| D5 | Retry identity | Replacement Pods for one Indexed Job index reuse the same global index and effective seed. |
| D6 | Runtime ownership | SM injects root seed and offset; Kubernetes projects completion index; Translator supplies scenario seed from baked scenario data. |
| D7 | Derivation ownership | One algorithm lives in the digest-pinned runner base image. The CR has no derivation selector. |
| D8 | Derivation provenance | The base emits constant `cbse-hmac-sha256-u63-v1`; the runner image digest remains authoritative. |
| D9 | RNG ownership | Generated model runner chooses and initializes its local RNG from the effective seed. |
| D10 | Result identity | Unique logical key is `(scenario result table, global replication index)`; experiment/scenario identity is already represented by database/table placement. |
| D11 | Duplicate execution | Identical retry result is accepted idempotently; a different result for an existing replication index fails closed. |
| D12 | Existing tables | No implicit legacy-table migration in a runner Pod; missing required columns produce an incompatible-schema failure. |
| D13 | PPS observation order | PPS reads JSONB observations in ascending `replication_index`, so floating-point aggregation order is stable across concurrent insertion schedules. |
| D14 | Replay | v0.1 proves reconstruction from exported CR/recipe/image provenance; no new replay API. |
| D15 | Installation | The same deterministic-stream assertions pass through both manifest and Helm installers. |
| D16 | Security | Seeds are non-secret provenance; database/registry credentials remain secret and never enter result provenance. |

## 5. Design patterns (Gang of Four)

| # | Concern | Pattern | Concrete application | Why simpler composition is insufficient |
|---|---|---|---|---|
| 1 | Effective Job assembly must merge a validated custom template with framework-owned random-stream coordinates | **Builder** | Existing `scenario-manager/internal/effectivejob.Build` adds the three exact runtime variables to the single `runner` container after template validation | Scattered mutation in the scheduler/adapter would make ownership and default/custom-template parity difficult to test |
| 2 | Generated runners need one stable API over environment parsing, index calculation, encoding, and HMAC | **Facade** | Base-image module `cbse_random` exposes `derive_stream(scenario_seed, environ=None) -> StreamDescriptor` | Every Translator reimplementing parsing and derivation would destroy byte-level portability |
| 3 | Framework stream identity must feed model-specific RNG APIs | **Adapter** | Reference `main.py` adapts `StreamDescriptor.effective_seed` to `random.Random`; other Translators may adapt it to NumPy/R/Java/counter-based generators | CBSE cannot force one RNG object or language API on all user models |
| 4 | Result writes must preserve one observation while tolerating retries | **Proxy** | `resultdb.insert` mediates the dynamic PostgreSQL table, unique replication-index insert, and identical-result verification | Direct append-only inserts cannot distinguish an infrastructure retry from a new statistical observation |

Workers may not introduce additional named patterns without a manager amendment.

## 6. Global constraints

1. Obey repository-root `AGENTS.md` in every slice.
2. Specifications under `devlog/**` are normative and read-only to workers.
3. Implement exactly the assigned slice; no unrelated cleanup, speculative
   compatibility layer, or redesign of preserved contracts.
4. Never weaken, delete, skip, or rewrite tests to conceal a failure.
5. Every Go, CRD, Dockerfile, test-harness, or runner-image change runs
   `make test-fast`; reconciliation, API/CRD, image, manifest, or integration
   changes also run the slice's required smoke gate.
6. Cluster mutation requires explicit `KUBECONFIG` and
   `CBSE_EXPECTED_CONTEXT=k3s-test`; the harness owns its run namespace.
7. Never deploy test resources to `default` or `kube-system`, delete the shared
   CRD, or delete resources the harness did not create.
8. Images remain immutable digest references. Never add an insecure-registry or
   TLS-verification bypass.
9. Never expose credentials, registry hosts, cluster endpoints, kubeconfig
   paths, Secret payloads, tokens, or private infrastructure in tracked files,
   reports, or artifacts.
10. `go work sync` remains forbidden.
11. Framework-owned variable names cannot be supplied by custom runner
    templates.
12. Missing/malformed stream inputs fail closed; no hostname/time fallback.
13. All arithmetic and encodings are deterministic and locale-independent.
14. Result provenance contains no database credentials or Secret material.
15. Every implementation commit stays on its dispatched child branch and
    carries `[orchestration: task … dispatch …]` trailers.
16. Implementation uses `ai.forge/qwen3.8-27b-nvfp4`; independent review uses
    `ai.forge/glm`; both attest with `PI_PROVIDER`/`PI_MODEL`.

## 7. Normative contracts

### 7.1 Root-seed API

Add:

```yaml
spec:
  randomness:
    rootSeed: 123456789
```

Go shape:

```go
type RandomnessSpec struct {
    // Required; 0..math.MaxInt64.
    RootSeed *int64 `json:"rootSeed"`
}
```

`spec.randomness` and `rootSeed` are required for newly admitted objects.
`randomness` is immutable after creation through CEL. A pointer permits the
runtime to detect a legacy stored object whose field is absent and fail closed
rather than treating absence as explicit zero.

No server-generated mode and no derivation-version field ship in v0.1.

### 7.2 Scenario-seed contract

The current Detail DB/EDS/Translator value named `seed_policy` is the scenario
seed. Its accepted range becomes:

```text
0..2147483647
```

which matches PostgreSQL `INTEGER` while remaining encodable in the derivation's
unsigned 64-bit slot. EDS rejects values outside this range before database
write but does not require uniqueness. Reusing a value across scenarios yields
the same effective seed for the same replication index and is the explicit
common-random-number mechanism; different scenarios that require independent
streams must use different scenario seeds. Translator bakes the resolved value
in `scenario.json` under the existing key `seed_policy`; result provenance
includes both compatibility field `seed_policy` and explicit semantic alias
`scenario_seed` with equal values.

### 7.3 Runtime variables

The effective Job injects exactly these variables into the regular container
named `runner`:

```text
CBSE_ROOT_SEED
CBSE_REPLICATION_OFFSET
CBSE_COMPLETION_INDEX
```

- `CBSE_ROOT_SEED`: decimal root seed from the live CR.
- `CBSE_REPLICATION_OFFSET`: decimal `number_of_computed_reps` before the round.
- `CBSE_COMPLETION_INDEX`: Kubernetes Downward API projection of
  `metadata.annotations['batch.kubernetes.io/job-completion-index']` with API
  version `v1`.

Custom templates may not define these names in any regular or init container.
The Operator rejects conflicts before `InProgress`; SM injects them only into
the regular `runner` container from authoritative state after validation.

### 7.4 Base-image API

The runner base image installs importable module `cbse_random` with:

```python
@dataclass(frozen=True)
class StreamDescriptor:
    root_seed: int
    scenario_seed: int
    replication_index: int
    effective_seed: int
    seed_derivation: str


def derive_stream(scenario_seed, environ=None) -> StreamDescriptor:
    ...
```

`derive_stream` reads and strictly parses the three framework variables,
validates each integer in `0..9223372036854775807`, calculates
`replication_index = offset + completion_index` with overflow rejection, and
validates `scenario_seed` in the same signed-63-bit range. It does not construct
a model RNG.

### 7.5 Byte-exact derivation

Constants:

```text
derivation id: cbse-hmac-sha256-u63-v1
domain bytes:  63 62 73 65 2e 73 65 65 64 2e 76 31 00
               "cbse.seed.v1\0"
```

Encoding:

```text
key     = root_seed as unsigned 64-bit big-endian
message = domain bytes
          || scenario_seed as unsigned 64-bit big-endian
          || replication_index as unsigned 64-bit big-endian
digest  = HMAC-SHA-256(key, message)
raw     = first 8 digest bytes interpreted unsigned big-endian
effective_seed = raw AND 0x7fffffffffffffff
```

Golden vectors:

| Root | Scenario | Replication | Digest hex | Effective seed |
|---:|---:|---:|---|---:|
| 0 | 0 | 0 | `218115103773ecbf6557e26b5cb98de4d258200c2cbdbe7d5c143d0e1426525b` | 2414234034641300671 |
| 1 | 1 | 0 | `4d2d5a116c940811992f025d39330c8b3ecba39e72bfc9783f0a6db7ecf1233f` | 5561200145755015185 |
| 123456789 | 1001 | 0 | `023bc3faa65a2fbc07059b3e86506da5b29d5ba02407df6a11445af424edeb4f` | 160937693001953212 |
| 123456789 | 1001 | 1 | `d2347e8ffe6343f0bc2bff49c506aadc2307d396e7a60921e0279818db55d79d` | 5923498566812386288 |
| 123456789 | 1001 | 37 | `99d48bf613ad278aedeb9388b47bb94245fb936a081300452542178d0723ffc5` | 1861266435019188106 |
| 9223372036854775807 | 9223372036854775807 | 9223372036854775807 | `bfe72fcc7f7b3b982f02bc3d5140f7195d6e8007ad869cb9f48c8aeec26cf5fb` | 4604701699368237976 |

The 63-bit truncation has a theoretical collision probability. This feature
accepts that negligible risk for ordinary experiment sizes while testing all
assigned effective seeds for uniqueness in acceptance runs. Logical identity
remains the replication index, not the effective seed.

### 7.6 Reference RNG and result provenance

The reference runner uses:

```python
stream = cbse_random.derive_stream(scenario["seed_policy"])
rng = random.Random(stream.effective_seed)
```

It emits existing model fields plus:

```json
{
  "seed_policy": 1001,
  "root_seed": 123456789,
  "scenario_seed": 1001,
  "replication_index": 37,
  "effective_seed": 1861266435019188106,
  "seed_derivation": "cbse-hmac-sha256-u63-v1",
  "rng_algorithm": "python-random-mt19937"
}
```

`seed_policy == scenario_seed` is an asserted compatibility invariant.

### 7.7 Idempotent Result DB contract

New scenario result table shape:

```sql
CREATE TABLE scenario_<id>_results (
    id BIGSERIAL PRIMARY KEY,
    replication_index BIGINT NOT NULL UNIQUE CHECK (replication_index >= 0),
    result JSONB NOT NULL
)
```

Within the existing scenario advisory-lock transaction:

1. create the table if absent;
2. verify an existing table has the required column/constraint shape;
3. insert `(replication_index, result)` with `ON CONFLICT DO NOTHING`;
4. when a conflict occurs, compare the stored JSONB with the proposed JSONB;
5. identical means idempotent success; different means fail the Pod.

PPS reads `result` JSONB rows with `ORDER BY replication_index ASC`. The unique
column ensures one logical replication contributes at most one observation,
and the explicit order makes floating-point accumulation independent of Pod
completion/insertion order.

## 8. Implementation slices and DAG

| Slice | Wave | Dependencies | Purpose |
|---|---:|---|---|
| [D1](slices/D1-api-and-scenario-seed-contract.md) | W1 | none | Root-seed API/CRD, manifest fixtures, and formal `seed_policy` domain |
| [D2](slices/D2-scenario-manager-replication-coordinates.md) | W2 | D1 | Carry cumulative offset and inject framework runtime coordinates |
| [D3](slices/D3-runner-base-derivation-facade.md) | W1 | none | Base-image `cbse_random` implementation and golden-vector tests |
| [D4](slices/D4-reference-runner-and-idempotent-results.md) | W3 | D2, D3 | Translator/reference-runner integration, provenance, and retry-idempotent Result DB |
| [D5](slices/D5-dual-installer-reproducibility-acceptance.md) | W4 | D4 | Full manifest/Helm deterministic-stream and recreation evidence |
| [D6](slices/D6-documentation-and-thesis-settlement.md) | W5 | D5 | User/operator documentation and final thesis synchronization |

D1 and D3 may run in parallel because their target paths do not overlap. All
later dependencies are real integration dependencies; do not bypass them.

## 9. Verification gates

### 9.1 Fast gate

Every implementation and review slice reruns `make test-fast` from a fresh
shell. Targeted tests named in the slice are additional, never substitutes.

### 9.2 Smoke gate

- D1: manifest smoke because API/CRD and experiment manifest change.
- D2: manifest smoke because reconciliation/Job construction changes.
- D3: manifest smoke because the runner-base container image changes.
- D4: manifest smoke because generated runner/result persistence changes.
- D5: both manifest and Helm smoke, using one unchanged 15-spec suite, plus
  cross-run seed-manifest comparison.
- D6: documentation-only; no cluster smoke.

A required cluster gate that cannot run is `verification-blocked`, not success.

### 9.3 Forbidden shortcuts

- no hostname/time fallback;
- no hard-coded effective-seed list;
- no completion index inferred from Pod name;
- no offset inferred from `round * round_reps`;
- no duplicate row filtering only in PPS;
- no mutable/floating runner-base image;
- no CR derivation selector;
- no broadening custom-template Downward API permissions;
- no test-only stream path different from production.

## 10. Required observable properties

Automated evidence must prove:

1. Golden vectors are byte-exact.
2. Same root/scenario/index yields the same effective seed.
3. Different tested indices within one scenario yield different effective
   seeds; equal scenario seeds intentionally reproduce equal indexed streams
   across scenarios.
4. Pod/Job name, hostname, node, start time, and scheduling order are absent
   from the derivation.
5. Same-index replacement uses the same coordinates.
6. Round 2 begins at the previous cumulative successful count.
7. One wave and several waves totaling N assign indices `0..N-1` and the same
   effective seeds.
8. Recreated experiments with the same root, recipes, and images emit identical
   seed manifests despite different Kubernetes UID/Job names.
9. Missing/malformed/overflow inputs fail closed.
10. Result provenance records all contract fields.
11. Identical duplicate writes are idempotent.
12. Conflicting duplicate writes fail and do not alter the original row.
13. PPS sample size cannot be inflated by a duplicate attempt.
14. PPS observation order is ascending global replication index regardless of
    result insertion order.
15. Manifest and Helm smoke each pass all 15 specs with clean teardown.

## 11. Completion and handoff

Slice states are `complete`, `incomplete`, or `verification-blocked` in
[`IMPLEMENTATION_HANDOFF.md`](IMPLEMENTATION_HANDOFF.md).

A slice is complete only when:

- implementation worker reports the exact branch HEAD, runtime attestation,
  changed files, and all required receipts;
- independent `ai.forge/glm` review approves that exact HEAD after rerunning
  acceptance from a fresh shell;
- the merge lane mechanically validates scope, protected paths, tests, and
  commit trailers;
- approved code is landed on the feature branch, not directly on `main`;
- the handoff ledger records implementation, review, landing, and smoke IDs.

Workers never edit this feature, its slices, or the handoff ledger unless a
manager task explicitly assigns ledger settlement. Protected `devlog/**`, CRD,
and API paths require the prescribed manager/user gate. No feature branch merge
to `main` and no push to `origin/main` occurs without explicit user approval.
