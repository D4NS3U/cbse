# Slice D1 — Root-seed API and scenario-seed contract

Normative and self-contained for its worker. Read order:
[../FEATURE.md](../FEATURE.md) §§1–7, especially D1/D2 and §§7.1–7.2;
repository-root `AGENTS.md`; then this slice. Specifications are read-only. On a
contradiction that repository inspection cannot resolve, stop and ask through
the orchestration ask channel.

This is wave W1 and may run in parallel with D3. D2 cannot start until D1 is
reviewed and landed because D2 consumes the new API field.

## Target

Sole ownership of:

- `experiment-operator/api/alpha4/simulationexperiment_types.go`;
- generated alpha4 deepcopy and CRD artifacts required by that API change;
- the byte-identical Helm CRD copy under `charts/cbse/crds/`;
- alpha4 API/CRD/controller tests whose fixtures construct a
  `SimulationExperiment`;
- `test/e2e/manifests/experiment.yaml` only to add the canonical root seed;
- reference EDS `seed_policy` value-domain implementation/tests;
- Translator Detail DB `seed_policy` validation/tests;
- generated-verification tests needed to preserve CRD/chart lockstep.

No Scenario Manager, effective Job, runner-base, generated runner, Result DB,
PPS, harness flow, chart template, or product-documentation changes. No
`devlog/**` edits.

## Change

### D1.1 Root-seed API

Add required `RandomnessSpec` to `SimulationExperimentSpec`:

```go
type RandomnessSpec struct {
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:Minimum=0
    // +kubebuilder:validation:Maximum=9223372036854775807
    RootSeed *int64 `json:"rootSeed"`
}
```

Add required:

```go
Randomness RandomnessSpec `json:"randomness"`
```

The pointer is deliberate: runtime code can distinguish absent legacy data from
explicit zero. Add a spec-level CEL rule making `randomness` immutable after
creation with the established delete-and-recreate message style.

Do not add defaults, generated-seed mode, nullable behavior, a string form, a
`derivationVersion`, or status copy of the seed.

### D1.2 CRD and fixtures

Regenerate the operator artifacts using the repository's existing generation
entry point. The chart CRD copy remains byte-identical. The OpenAPI schema must:

- require `spec.randomness`;
- require `spec.randomness.rootSeed`;
- encode integer/int64 with minimum zero and maximum MaxInt64;
- include the immutability CEL rule;
- contain no derivation-version field.

Update every owned test fixture to use explicit values. Use `123456789` in the
canonical e2e experiment. Unit fixtures may use zero to prove zero is valid.

Add admission/schema tests proving missing randomness, missing rootSeed,
negative values, values above MaxInt64, and mutation are rejected while zero
and MaxInt64 are accepted.

### D1.3 Existing `seed_policy` becomes scenario seed

Preserve all public JSON, SQL, and Go field names. Tighten and document the
reference value domain as `0..2147483647`:

- EDS rejects values outside the range at its API/store validation seam;
- Translator Detail DB lookup rejects a value outside the range defensively;
- PostgreSQL `INTEGER` remains the storage type;
- baseline values `1001..1004` remain unchanged;
- recipe shape remains `seed_policy_id` and view column remains `seed_policy`.

Add boundary tests for zero, MaxInt32, negative, and overflow. Do not add a
parallel `scenario_seed` table/column/recipe key and do not impose uniqueness:
reusing a scenario seed is the intentional common-random-number mechanism.

### D1.4 Compatibility behavior

New objects without the field are rejected by the CRD. An old stored object may
be decoded with `RootSeed == nil`; D1 does not invent runtime behavior for it.
D2 must fail effective-Job construction closed for nil. Do not silently map nil
to zero.

## Constraints

- API is alpha4-only; do not create alpha5 or conversion webhooks.
- Generated CRD files are generated, never manually diverged.
- Chart CRD copy is exact.
- Do not change model parameters, baseline rows, recipe locator keys, or PPS.
- Do not add root seed to labels, annotations, logs, status, Secrets, or database
  credentials. It is CR spec provenance only in this slice.
- No private infrastructure strings.
- `make test-fast` and manifest smoke are mandatory under `AGENTS.md` because
  API/CRD and experiment manifest change.
- Commit only on the dispatched child branch with orchestration trailers.
- FEATURE.md §6 applies verbatim.

## Ownership

D1 owns the API and scenario-seed domain only. D2 owns runtime injection; D3
owns derivation; D4 owns result provenance. If repository generation requires a
file outside Target, stop and ask before editing it.

## Observable acceptance

1. First checkpoint attestation:
   `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` prints
   `ai.forge/qwen3.8-27b-nvfp4`.
2. `git status --short` contains only Target paths.
3. API unit tests prove requiredness, range, zero/MaxInt64 acceptance, and
   immutability.
4. EDS/Translator targeted tests prove exact `seed_policy` boundaries and
   unchanged recipe/view names.
5. Repository generation command succeeds; generated diff is stable on a
   second run.
6. Operator and chart CRD files are byte-identical.
7. `rg -n 'derivationVersion|scenario_seed'` over API/CRD/EDS schema paths has
   no new field/schema result.
8. `make test-fast` succeeds.
9. `make test-smoke KUBECONFIG=... CBSE_EXPECTED_CONTEXT=k3s-test ...` succeeds
   15/15 with immutable images and clean teardown; record run ID.
10. Added-line private-infrastructure/credential scan is empty.
11. Commit receipt names branch HEAD and orchestration trailers.

Completion requires one `worker_done` with explicit outcome, task/dispatch IDs,
attestation, evidence 1–11, modified files, smoke run ID, and report path.
Unavailable mandatory smoke is `verification-blocked`.
