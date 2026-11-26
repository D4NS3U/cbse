# Slice D2 — Scenario Manager replication coordinates

Normative and self-contained. Read order: [../FEATURE.md](../FEATURE.md),
especially decisions D3–D7 and §§7.1/7.3; settled D1 implementation; root
`AGENTS.md`; then this slice. Specs are read-only. Stop and ask on unresolved
contradiction.

This is wave W2 and depends on D1. D3 may already be landed or may run in
parallel; D2 must not consume D3 code.

## Target

Sole ownership of:

- `experiment-operator/internal/jobtemplate/` environment-name policy and tests;
- `scenario-manager/internal/scheduler/` runner-start request contract/tests;
- `scenario-manager/internal/runnerstart/` request construction/tests;
- `scenario-manager/internal/effectivejob/` effective Job construction/tests;
- `scenario-manager/internal/jobadapter/` BuildRequest wiring/tests;
- narrowly required Scenario Manager package comments/tests affected by those
  signatures.

No persistence schema/query changes: `NumberOfComputedReps` already exists in
the runner-start projection. No CRD/API, EDS, base-image, generated-runner,
Result DB, PPS, e2e assertion, harness, chart, docs, or `devlog/**` changes.

## Change

### D2.1 Reserve framework variables

The Operator's custom runner-template validator rejects these exact environment
variable names in every regular and init container:

```text
CBSE_ROOT_SEED
CBSE_REPLICATION_OFFSET
CBSE_COMPLETION_INDEX
```

Return a field-specific forbidden validation error explaining that CBSE owns the
random-stream coordinate. Do not reject unrelated `CBSE_*` names and do not
broaden the accepted Downward API field list for users.

Auxiliary and init containers are not assigned framework stream coordinates and
may not claim the reserved names. Tests must prove only the regular `runner`
receives injected values.

### D2.2 Carry the cumulative offset

Add `ReplicationOffset int` to:

- `scheduler.RunnerStartRequest`;
- `effectivejob.BuildRequest`.

`runnerstart.Scheduler.reconcile` passes
`proj.NumberOfComputedReps` unchanged. `jobadapter.Start` passes it into
`effectivejob.Build`. Validate it is non-negative before any Kubernetes create.
No query or scenario-state mutation belongs here.

### D2.3 Read the root seed from the live CR

`effectivejob.Build` reads `req.Experiment.Spec.Randomness.RootSeed` after the
adapter fetches the live `SimulationExperiment`.

- nil is a permanent effective-Job construction error;
- negative is a defensive permanent error even though CRD admission forbids it;
- explicit zero is valid;
- the decimal environment form has no whitespace or sign prefix.

Do not copy the root seed through Core DB, NATS, labels, annotations, Job names,
or the runner-start projection.

### D2.4 Inject exact runtime variables

After selecting/deep-copying the default or validated custom template, locate
exactly one regular container named `runner`. Fail closed on missing/duplicate
runner or on an existing exact reserved variable.

Append these explicit env entries in this stable order:

1. `CBSE_ROOT_SEED` — decimal value;
2. `CBSE_REPLICATION_OFFSET` — decimal value;
3. `CBSE_COMPLETION_INDEX` — `ValueFrom.FieldRef` with API version `v1` and
   field path
   `metadata.annotations['batch.kubernetes.io/job-completion-index']`.

Preserve all user environment entries and `envFrom` entries. Do not inject into
init containers, native sidecars, or ordinary sidecars. The builder remains
non-mutating with respect to the CR's template.

### D2.5 Retry and wave tests

Unit tests must prove:

- offset 0/index 0 produces coordinate identity 0 at the interface boundary;
- a round-2 request with cumulative offset 30 carries 30, independent of
  `Round` and `NumberOfReps`;
- replacement construction for the same request is byte-identical;
- round/job name is not used as a seed-coordinate value;
- default and custom templates receive the same three variables;
- user env is preserved and conflicting reserved env is rejected;
- nil root/negative offset make no create call;
- values at MaxInt64 serialize correctly;
- only completion index uses Downward API projection.

Do not calculate an effective seed in Go.

## Constraints

- `global_replication_index` is not persisted or injected as a fourth variable;
  the base-image Facade calculates offset + completion index.
- Never infer offset from round number, requested reps, completed-index strings,
  Pod/Job names, or current Job status.
- Keep global `backoffLimit: 4` and Indexed Job shape unchanged.
- Do not add `backoffLimitPerIndex` in this feature.
- Do not expand Scenario Manager RBAC; it already fetches the live CR.
- Existing Job-template policy remains strict; no new user annotation field
  projection.
- `make test-fast` and manifest smoke are mandatory because operator validation,
  reconciliation, and Job manifests change.
- Commit/attestation/hygiene rules from FEATURE.md §6 apply.

## Ownership

D2 owns orchestration coordinates only. D3 owns parsing/HMAC. D4 owns scenario
seed consumption, model RNG, and results. Missing root seed or reserved-env
conflicts are D2 errors; malformed runtime string handling inside the Pod is D3.

## Observable acceptance

1. Runtime attestation is `ai.forge/qwen3.8-27b-nvfp4`.
2. Diff containment is Target paths only.
3. Operator targeted tests reject all three names in runner, sidecar, ordinary
   init, and native-sidecar containers while accepting unrelated names.
4. Runner-start tests prove `NumberOfComputedReps` reaches
   `RunnerStartRequest.ReplicationOffset` for round 1 and later rounds.
5. Effective-Job tests prove exact env order/values/fieldRef, default/custom
   parity, template non-mutation, and fail-closed cases.
6. Job-adapter tests prove no create call on invalid root/offset and successful
   create otherwise.
7. `go test -race` for all changed operator/SM packages succeeds.
8. `make test-fast` succeeds.
9. Manifest `make test-smoke ...` succeeds 15/15 with clean teardown; record run
   ID and verify at least one rendered runner Job contains the three variables
   without exposing Secret data.
10. Added-line private-infrastructure and credential scans are empty.
11. Commit HEAD and orchestration trailers are reported.

Completion requires one `worker_done` with evidence 1–11, files, report, and
smoke run ID. Missing mandatory cluster verification is
`verification-blocked`.
