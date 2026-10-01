# T1 — The Error flavors spec: Validation-Error and Provisioning-Error (creation variant)

**Status: complete** (Wave 1, settled 2026-10-01, approved on the first review round).

- **Task/Dispatch:** `task_f642f9edcc92` / `ctx_c496c5570ebd` (implementer, attested `ai.forge/qwen3.8-27b-nvfp4`); review `task_11956b120c0d` / `ctx_d88fe9124be1` (reviewer `ai.forge/glm`, verdict **approve**).
- **Branch commit:** `7cac516` on `D4NS3U/experiment-terminal-e2e` — one file, `test/e2e/smoke_test.go`, +378/-0 pure insertions; every pre-existing spec byte-identical.
- **Landed:** the two red-experiment specs, each built by deep-copying the live green experiment's spec with exactly one delta — the Validation-Error spec (`<project>-errval`, a tag-form `translator.image`: `Error` + the exact digest-form message, the complete zero-children inventory, annotation-triggered stickiness, deletion cleanup) and the Provisioning-Error spec (`<project>-errprov`, a blocker Service holding NodePort 32700 vs the red PPS's request: `Error` at the PPS Service create with the partial inventory matching `provisionComponents`' real order — the PPS Deployment present, the PPS Service rejected "provided port is already allocated", the runner SA never created — stickiness, the GC cascade over the full partial set, and the fixture teardown). Both persist terminal evidence via the suite's artifact discipline.
- **Evidence:** `make test-fast` rc=0 (re-run independently by the reviewer; e2e compile gates clean); receipts in `artifacts/orchestration/task_f642f9edcc92-report.md` and `task_11956b120c0d-review.md` (worktree-local).
- **Live proof:** pending the consolidated settlement smoke (the manager's gate after the final wave).
