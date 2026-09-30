# P1 — API surface: `scenarioManagerVerdict` + the `Finished` phase vocabulary

**Status: complete** (Wave 1, settled 2026-09-30).

- **Task/Dispatch:** `task_c135b5893dd6` / `ctx_7144a7b07c0d` (implementer, attested `ai.forge/qwen3.8-27b-nvfp4`); review `task_ae96fa1c635a` / `ctx_34ed81bcf947` (independent reviewer, attested `ai.forge/glm`, verdict **approve**).
- **Branch:** `D4NS3U/experiment-phase-aggregation`, commit `ceedfbb` — exactly 2 files, +49/-11: `experiment-operator/api/alpha4/simulationexperiment_types.go` (the `ScenarioManagerVerdict string` field with `+kubebuilder:validation:Enum=Finished;Failed`, the D9 field-ownership doc comment, `Finished` appended to the `Phase` enum per D12 with `Completed` retained) and the regenerated CRD yaml (`status.properties.scenarioManagerVerdict` with enum; extended `phase` enum; updated vocabulary doc comment).
- **Adjudicated deviation:** `zz_generated.deepcopy.go` is byte-identical — the new field is a value-type string covered by the struct-level `*out = *in` shallow copy (`Metrics` remains the only pointer field with a per-field deep-copy block). Upheld by the reviewer as legitimate controller-gen behavior.
- **Evidence:** `make verify-generated` rc=0 byte-stable; `make test-fast` rc=0 (both re-run independently by the reviewer at the reported HEAD, plus an uncached `go test -count=1 ./api/alpha4/...` ok); grep receipts for field, markers, enum entries, and diff scope in `artifacts/orchestration/task_c135b5893dd6-report.md` and `artifacts/orchestration/task_ae96fa1c635a-review.md` (worktree-local).
- **Merge:** held by design — the regenerated CRD manifest is a protected path and the branch-isolation ruling makes the merge to `main` one user-approved event at settlement.
