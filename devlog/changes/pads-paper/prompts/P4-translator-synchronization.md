# Dispatch prompt — P4 Translator synchronization and component integration

Implement `devlog/changes/pads-paper/slices/P4-translator-synchronization.md` exactly.

## Read first

1. `devlog/changes/pads-paper/FEATURE.md`
2. `AGENTS.md`
3. `devlog/changes/pads-paper/IMPLEMENTATION_HANDOFF.md`
4. `devlog/changes/pads-paper/slices/P4-translator-synchronization.md`
5. `pads-paper/simulation-model/src/` (canonical), `pads-paper/translator/component/internal/generator/` (runnermod, embed, conformance), and `pads-paper/TEMPLATE_BASELINE.md`

The feature and slice are normative and read-only. Stop and ask through Orca if repository inspection reveals a contradiction.

## Ownership

You may edit only:

- `pads-paper/translator/component/internal/generator/runnermod/` (the six synchronized files and `main.py`)
- `pads-paper/translator/component/internal/generator/parity_test.go` (new)
- `pads-paper/translator/component/internal/sqldoc/sqldoc_test.go` (the reference-path activation only)
- `pads-paper/translator/component/README.md` (the PADS integration section and link repair)
- `pads-paper/TEMPLATE_BASELINE.md` (the ruled-divergence section)

Do not edit canonical `src/**`, containers, other paper-local components, devlog specifications, original templates, shared code, manifests, CRDs, Makefiles, harness scripts, `go.work`, or artifacts.

## Required result

Synchronize the six canonical files byte-for-byte, adapt `main.py` to the `model.run` metrics dict and `result.build_result(scenario, metrics, effective_seed)` API, add the embedded-vs-canonical parity test, activate the paper-local Detail DB schema guard, document the integration, and record the ruled divergence. Keep the module path, import paths, and all orchestration/BuildKit/registry contracts unchanged.

## Runtime attestation

At the first checkpoint run:

```bash
printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
```

Required implementation runtime: `ai.forge/qwen3.8-27b-nvfp4`. Repeat the exact output in `worker_done`; mismatch is failure.

## Verification

Run every observable acceptance check from the slice: byte-identity proofs for the six files, `GOWORK=off go vet ./...` and `GOWORK=off go test -race -timeout 30m ./...` inside `pads-paper/translator/component`, the temporary-drift parity proof with full restoration, the activated `sqldoc` guard, `git diff --check`, and repository-root `make test-fast`. No cluster access and no smoke run.

Commit only to the dedicated child branch with the assigned Task/Dispatch trailer:

```text
[orchestration: task <task-id> dispatch <dispatch-id>]
```

Report branch HEAD, files modified, parity and drift-proof receipts, exact test results, runtime attestation, and any limitation through `worker_done`.
