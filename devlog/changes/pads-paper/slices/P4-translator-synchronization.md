# P4 — Translator synchronization and component integration

## Target

Make the paper-local Translator generate `erlang_a_v1` production runners: synchronize the canonical model modules into its embedded `runnermod`, adapt the production entrypoint to the new metrics/result API, and add an automated parity test so the canonical source remains the single source of truth. This slice is code and tests only; live experiment proof with the paper-local Translator image is P5.

## Dependency

- P3B2 is independently approved and landed at `8d79b3baceed1402346db7cfe4487bd40efc8160` (settlement smoke `20261105122418-e0acf4`, 15/15).

## Requirements

### P4-R1 — Canonical model synchronization

Replace the imported baseline copies in `pads-paper/translator/component/internal/generator/runnermod/` with byte-identical copies of exactly these canonical files from `pads-paper/simulation-model/src/`:

- `model.py`
- `result.py`
- `seed.py`
- `test_model.py`
- `test_result.py`
- `test_seed.py`

These six files are the synchronized production model surface. `cli.py` and `test_cli.py` are standalone-CLI-only and must not enter `runnermod`. The synchronized copies must be byte-identical to the canonical files; the paper-local Translator never edits them by hand — drift is resolved by re-copying from canonical.

### P4-R2 — Production entrypoint adaptation

Update `pads-paper/translator/component/internal/generator/runnermod/main.py` (Translator-owned production glue, not a synchronized file) to the P3B1 API:

- `metrics = model.run(scenario["arrival_rate"], scenario["service_rate"], scenario["run_duration"], rng)` returns the exact `METRIC_FIELDS` dict;
- `result_record = result.build_result(scenario, metrics, effective_seed)`;
- seed derivation, hostname-based `effective_seed`, JSON loading, single-transaction Result DB insert, failure exit behavior, and the no-command/no-args/no-env execution contract remain unchanged.

The inserted JSONB record is the ruled additive contract: the five scenario fields, `effective_seed`, and the nine `erlang_a_v1` metric fields in `RESULT_FIELDS` order. The PPS continues reading only `mean_wait_time`; `completed_customers` remains the compatibility alias. No `scenario_id`, credentials, or event-level logging may enter the record or the module.

### P4-R3 — Automated canonical parity test

Add `pads-paper/translator/component/internal/generator/parity_test.go` that fails `go test` whenever the synchronized copies drift from canonical:

- locate `pads-paper/simulation-model/src/` via the module-relative path from the generator package (`../../../../simulation-model/src/`);
- for each of the six ruled files, compare the **embedded** `runnermod` bytes (through the existing `runnerFS` embed) against the canonical file bytes;
- fail with a message naming the canonical path as source of truth and the copy command that restores parity;
- fail (never skip) when the canonical directory or any ruled file is missing on either side.

The test must be robust to being run from any package directory inside the module and must not mutate any file.

### P4-R4 — Activate the paper-local Detail DB guard

Fix the imported `pads-paper/translator/component/internal/sqldoc/sqldoc_test.go` reference path so the schema guard is active in the paper-local layout: point it at `pads-paper/scenario-detail-database/component/10-simulation-parameters.sql` (module-relative `../../../../scenario-detail-database/component/10-simulation-parameters.sql`) instead of the baseline-only `../../../scenario-detail-database/10-simulation-parameters.sql` path that silently skips. No other change to that test’s assertions.

### P4-R5 — Translator README integration section

Extend `pads-paper/translator/component/README.md` (the paper-local copy is now licensed to diverge from the baseline template) with a PADS section documenting:

- the six synchronized canonical files and the parity test that enforces them;
- the `main.py` production glue adaptation and the exact result-record fields;
- that `scenario.json` parameterization, Detail DB lookup, BuildKit sidecar build, authenticated push, digest/identity verification, and ready publication are unchanged — the Translator parameterizes the synchronized model through the same baked scenario and builds scenario-specific runner images on the existing path;
- the repair of the imported `../../docs/CLUSTER_REQUIREMENTS.md` relative link to resolve in the paper-local layout.

Preserve the baseline README content otherwise; do not delete or weaken existing statements.

### P4-R6 — Baseline divergence ledger

Append a ruled-divergence section to `pads-paper/TEMPLATE_BASELINE.md` recording that P4 introduces the first licensed extensions after the P3A import, listing exactly: the six synchronized `runnermod` files, `runnermod/main.py`, `parity_test.go`, the `sqldoc_test.go` path activation, and the Translator README integration section (including the link repair). State that the translator component tree is expected to diverge from the baseline from P4 onward and that the P3A verification block remains valid as dated import evidence.

### P4-R7 — Scope and hygiene

- Do not modify: `pads-paper/simulation-model/src/**`, `container/**`, the paper-local EDS/PPS/Detail DB/Result DB trees, original `component-templates/**`, shared Operator/Scenario Manager code, CRD, manifests, root Makefile, harness scripts, or `go.work`.
- Do not rename the module path or any import path in the paper-local Translator module.
- Do not add credentials, registry names, kubeconfig paths, endpoints, image tags, or generated artifacts.
- No Kubernetes cluster is used in this slice.

## Observable acceptance

1. The six synchronized `runnermod` files are byte-identical to the canonical files (`cmp`/hash proof), and no `cli.py`/`test_cli.py` entered `runnermod`.
2. `cd pads-paper/translator/component && GOWORK=off go vet ./...` exits 0.
3. `cd pads-paper/translator/component && GOWORK=off go test -race -timeout 30m ./...` passes, including the new parity test and the embedded Python conformance suites (which now run the canonical `test_model`/`test_result`).
4. Drift detection proof: temporarily modify one synchronized copy, show the parity test fails naming the canonical source, then restore byte-exact state and prove a clean `git status` and a green re-run. No other file may change during this proof.
5. The `sqldoc` test now executes (not skips) against the paper-local Detail DB SQL.
6. `git diff --check` passes.
7. Repository-root `make test-fast` passes (shared code is untouched and must stay green).
8. The implementation commit edits only the ruled paths above and carries the assigned orchestration task/dispatch trailer.

## Completion

P4 is `complete` only after independent Pi/GLM review returns `approve` against the implementation HEAD and the approved child branch is landed on `PADS-paper`. No settlement smoke is required at P4 because no harness-built image, manifest, reconciliation path, or deployed integration changed; the unchanged 15-spec suite plus live paper-Translator proof belong to P5.
