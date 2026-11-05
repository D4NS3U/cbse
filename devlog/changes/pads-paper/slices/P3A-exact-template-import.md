# P3A — Exact template import

## Target

Populate the paper-local implementation directories from the settled CBSE reference templates without changing behavior. This is a provenance/import slice only; `erlang_a_v1` is implemented in P3B.

## Dependency

- P2 is independently approved and landed at `1d5ad7756f07a0a5c69f5fe6b2187e1d5b296cff`.
- User rulings approve the canonical source, standalone CLI container, existing-template reuse, and staged P3 sequencing.

## Requirements

### P3A-R1 — Full component imports

Copy every Git-tracked file byte-for-byte from baseline commit `1dfc7eb5fec358199cb9c28b1df2aaabb035c71a` into these destinations:

| Baseline source | Paper-local destination |
|---|---|
| `component-templates/experimental-design-service/` | `pads-paper/experimental-design-service/component/` |
| `component-templates/translator/` | `pads-paper/translator/component/` |
| `component-templates/post-processing-service/` | `pads-paper/post-processing-service/component/` |
| `component-templates/scenario-detail-database/` | `pads-paper/scenario-detail-database/component/` |

“Every Git-tracked file” excludes build outputs, caches, ignored files, local artifacts, and filesystem metadata. Preserve file bytes and executable modes. Do not rewrite module paths, imports, README text, Dockerfiles, tests, licenses, or copyright headers in P3A.

### P3A-R2 — Canonical model baseline import

Create `pads-paper/simulation-model/src/` with byte-identical baseline copies of exactly these files from `component-templates/translator/internal/generator/runnermod/` at the baseline commit:

- `model.py`
- `result.py`
- `seed.py`
- `test_model.py`
- `test_result.py`
- `test_seed.py`

These files establish the existing CBSE API, deterministic local-RNG, result, and test contracts that P3B extends. Do not import `main.py`, `resultdb.py`, database endpoint code, or PostgreSQL tests into the credential-free canonical model directory.

### P3A-R3 — Container baseline import

Create `pads-paper/simulation-model/container/base/` with byte-identical copies of every tracked file from `component-templates/translator/runner-base/` at the baseline commit. The existing `pads-paper/simulation-model/container/README.md` remains the paper-specific ownership document outside `base/`.

Do not add the standalone application Dockerfile in P3A; P3B owns it.

### P3A-R4 — Result Database exception

Do not invent or import Result Database source. `pads-paper/result-database/` remains README-only because the settled baseline is the locked official PostgreSQL image with no repository-owned initialization schema. Update its README only if needed to record that P3A performed no code import by design.

### P3A-R5 — Provenance record

Add `pads-paper/TEMPLATE_BASELINE.md` containing:

- the exact baseline commit;
- the source-to-destination mappings above;
- the six selected canonical model files;
- the Result Database exception;
- a statement that P3A is byte-identical import evidence, not a claim that future paper-local files remain identical after ruled extensions;
- reproducible verification commands that compare tracked file lists, file bytes, and executable modes against `git archive` or `git show` output from the baseline commit without modifying the worktree.

Update `pads-paper/README.md` and component ownership READMEs only enough to link the imported implementation directory and provenance record. Preserve the mandatory template-reuse/no-rewrite ruling and foundation history.

### P3A-R6 — No behavioral or integration change

- Do not implement Erlang-A logic.
- Do not add a standalone CLI or application image.
- Do not synchronize new code into the Translator beyond the exact baseline import.
- Do not edit the original `component-templates/**` tree.
- Do not edit the Experiment Operator, Scenario Manager, CRD, root Makefile, harness, manifests, or shared tests.
- Do not register paper-local modules in `go.work` or activate their Dockerfiles in any build/harness path.
- Do not add credentials, infrastructure values, image tags, endpoints, or generated artifacts.

The copied Dockerfiles are dormant source snapshots: no active image selection, manifest, harness, or deployment path changes in P3A. Cluster smoke begins when a paper-local image is built or selected in a later slice.

## Observable acceptance

1. A baseline extraction into a temporary directory proves the four full component destinations have identical tracked relative file lists, bytes, and executable modes.
2. The six canonical model files are byte-identical to their named baseline files, and no other files exist under `simulation-model/src/`.
3. `simulation-model/container/base/` is byte-identical to the baseline `runner-base/` tracked tree.
4. `result-database/` contains no implementation source or initialization SQL.
5. Original `component-templates/**` paths are unchanged by the implementation diff.
6. `git diff --check` passes.
7. Repository-root `make test-fast` passes. P3A does not require cluster smoke because no imported image is built, selected, or deployed.
8. The implementation commit edits only `pads-paper/**` and contains the required orchestration task/dispatch trailer.

## Completion

P3A is `complete` only after independent Pi/GLM review returns `approve` against the implementation HEAD and the approved child branch is landed on `PADS-paper`.
