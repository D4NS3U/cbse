# Slice D4 — Reference runner integration and idempotent results

Normative and self-contained. Read order: [../FEATURE.md](../FEATURE.md) §§7.2,
7.4–7.7; settled D2 and D3 implementations; root `AGENTS.md`; then this slice.
Specs are read-only. Stop and ask on unresolved contradiction.

This is wave W3 and depends on D2 and D3.

## Target

Sole ownership of:

- `component-templates/translator/internal/generator/runnermod/` runner source
  and tests;
- `component-templates/translator/internal/generator/` generation/conformance
  tests and only the generator code necessary to preserve scenario JSON and
  build-context behavior;
- narrow Translator test wiring needed to run generated-runner tests against
  the D3 base module;
- `component-templates/post-processing-service/internal/resultdb/` query and
  tests only for deterministic observation ordering;
- no other component.

Delete the obsolete generated `seed.py` and its hostname-based tests if no
longer referenced. No operator, Scenario Manager, EDS, runner-base, PPS, e2e,
harness, manifest, chart, docs, or `devlog/**` edits.

## Change

### D4.1 Consume the base Facade

Replace hostname-derived seeding in generated `main.py` with:

```python
stream = cbse_random.derive_stream(scenario["seed_policy"])
rng = random.Random(stream.effective_seed)
```

Remove `socket.gethostname()` and all local hash derivation. The generated build
context must not contain a private copy of `cbse_random`; import comes from the
immutable base image.

Keep the reference model's simulation parameters and `random.Random` use
otherwise unchanged.

### D4.2 Result provenance

Extend `result.build_result` and `RESULT_FIELDS` with exact fields:

```text
root_seed
scenario_seed
replication_index
effective_seed
seed_derivation
rng_algorithm
```

Preserve existing fields including `seed_policy`. Assert
`scenario_seed == seed_policy`. Set:

```text
seed_derivation = stream.seed_derivation
rng_algorithm   = python-random-mt19937
```

Use integer JSON values, not strings. Do not include hostname, Pod/Job name,
offset, completion index, database configuration, environment dump, or Secret
material. Offset and completion index are execution inputs; the global
replication index is the scientific identity persisted in results.

### D4.3 New table contract

Change the dynamic Result DB table to:

```sql
CREATE TABLE IF NOT EXISTS scenario_<id>_results (
    id BIGSERIAL PRIMARY KEY,
    replication_index BIGINT NOT NULL UNIQUE CHECK (replication_index >= 0),
    result JSONB NOT NULL
)
```

Continue using the existing validated table-name construction,
`statement_timeout`, scenario advisory transaction lock, endpoint dispatcher,
and one transaction.

After create, verify that an existing table exposes:

- `replication_index` as non-null `BIGINT`;
- `result` as non-null `JSONB`;
- a uniqueness constraint/index that enforces one row per replication index.

A legacy or incompatible table causes an actionable error and no insert. Do not
run `ALTER TABLE`, backfill legacy rows, drop/recreate, or silently continue.

### D4.4 Idempotent insert

Use `stream.replication_index` as the explicit insert key. In the same locked
transaction:

1. attempt `INSERT ... ON CONFLICT (replication_index) DO NOTHING RETURNING ...`;
2. successful insert returns normally;
3. conflict reads the existing JSONB row for that index;
4. PostgreSQL JSONB equality with the proposed result determines identity;
5. equal returns normally as an idempotent retry;
6. unequal raises an error, leaves the original row unchanged, and makes the
   runner Pod fail.

Do not compare only `effective_seed` or KPI. Do not overwrite a conflicting
row. Do not rely on PPS-side `DISTINCT`.

### D4.5 Deterministic PPS read order

Change the PPS Result DB query from unordered:

```sql
SELECT result FROM public.scenario_<id>_results
```

to:

```sql
SELECT result FROM public.scenario_<id>_results
ORDER BY replication_index ASC
```

Do not change KPI extraction, malformed-row behavior, confidence calculations,
or wave-size policy. Tests must feed rows in a deliberately different insertion
order and prove the values returned to evaluation follow replication index.
This is necessary because floating-point aggregation order must not depend on
concurrent Pod completion order.

### D4.6 Unit and conformance tests

Tests must prove:

- main passes the exact effective seed to `random.Random`;
- hostname is neither imported nor consulted;
- all provenance fields and types are exact;
- scenario seed mismatch is rejected defensively;
- first insert creates one row;
- identical same-index retry leaves one row and succeeds;
- different same-index retry leaves the original row and fails;
- different indices create distinct rows;
- negative index is rejected;
- legacy/incompatible table fails without alteration;
- transaction rollback preserves the original row on conflict/error;
- PPS query includes exact ascending replication-index ordering and preserves
  all extraction/malformed semantics;
- generated Dockerfile still inherits the supplied digest base and does not
  copy/reinstall the derivation module;
- generated source imports the D3 base module in tests through `PYTHONPATH`, not
  a duplicate fixture implementation.

## Constraints

- Preserve `scenario.json` key `seed_policy` and all existing model fields.
- Preserve generated image non-root behavior and digest-pinned base.
- Preserve Result DB credential handling; do not print/bake new credentials.
- No inner database retry loop. Kubernetes replacement is the execution retry.
- Change only PPS row ordering; do not change KPI extraction, malformed-row
  handling, confidence calculations, or wave-size policy.
- Do not add a second derivation algorithm or compatibility hostname branch.
- Translator/generated-image and Result DB integration changes require
  `make test-fast` and manifest smoke with newly built/published Translator and
  runner-base-derived runner images.
- Commit/attestation/hygiene constraints apply.

## Ownership

D4 owns the reference adaptation, Result DB Proxy, and deterministic PPS read
order. D2 owns coordinates and D3 owns their derivation. D5 owns full-system
assertions and evidence; D4 may add only local unit/conformance coverage.

## Observable acceptance

1. Runtime attestation is `ai.forge/qwen3.8-27b-nvfp4`.
2. Diff containment is Target paths only, including intentional deletion of old
   seed files.
3. All generated-runner Python unit tests pass with D3 module imported from the
   runner-base source path.
4. Generator Go/conformance tests prove no duplicated derivation source and
   exact result fields.
5. Result DB fake/integration tests prove first/identical/conflicting/different
   index and legacy-schema behavior; PPS tests prove ascending-index read order.
6. `rg -n 'gethostname|socket|seed_policy.*hostname|derive_seed'` finds no live
   generated-runner production path.
7. `make test-fast` succeeds.
8. Newly built generated runner image imports `cbse_random` as UID 1000 and runs
   a golden-vector dry invocation with supplied coordinate env.
9. Manifest smoke with new immutable Translator/base/generated-runner images
   passes 15/15 and clean teardown; record run ID and relevant digests.
10. Smoke Result DB inspection shows one row per replication index and all new
    provenance fields, without relying on D5's stronger cross-wave assertions.
11. Added-line private/credential scan empty; commit HEAD/trailers reported.

Completion requires one `worker_done` with evidence 1–11, files, report, smoke
ID, and image digests. Mandatory smoke unavailable means
`verification-blocked`.
