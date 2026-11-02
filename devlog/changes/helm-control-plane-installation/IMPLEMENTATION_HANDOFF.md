# Implementation handoff — Helm control-plane installation

This is the durable routing ledger for [FEATURE.md](FEATURE.md). Slice states use `complete` / `incomplete` / `verification-blocked`. Specifications remain normative and read-only to workers.

## Current state

| Slice | State | Landing / evidence | Next owner |
|---|---|---|---|
| H1 — chart skeleton and contract tests | **complete** | Merged locally to `main` as `bcc6d87` (reviewed implementation HEAD `2ac80718fa87208c562b7d8d49dd2987806c5af0`; commits `82a64ef`, `2ac8071`) | none |
| H2 — Operator and Scenario Manager workloads | incomplete | dependency H1 satisfied; not dispatched | manager/user decision to start W2 |
| H3 — internal NATS and Core DB | incomplete | dependency H1 satisfied; not dispatched | manager/user decision to start W2 |
| H4 — lifecycle safety | incomplete | waits for H2+H3 settlement | — |
| H5 — Helm smoke integration | incomplete | waits for H4 | — |
| H6 — documentation | incomplete | waits for H5 | — |

## H1 settlement (2026-11-02)

- **Run:** `run_019967646d6d`
- **Implementation:** task `task_863c88586e8e`, dispatch `ctx_5e875c8442d6`, runtime attestation `ai.forge/qwen3.8-27b-nvfp4`.
- **Independent review:** task `task_538e39970502`, dispatch `ctx_f364b232c6d2`, runtime attestation `ai.forge/glm`, verdict **approve** on exact HEAD `2ac80718fa87208c562b7d8d49dd2987806c5af0`.
- **Merge:** local `main` merge commit `bcc6d87`; the child worktree and merged branch were removed after evidence preservation.
- **Reports (untracked, main repo):** `artifacts/orchestration/helm-H1-implementation-report.md`, `artifacts/orchestration/helm-H1-review.md`.

### Verified gates

- `bash -n` over the four H1 scripts: rc=0.
- `make helm-lint`: rc=0 with pinned Helm v3.18.4.
- `make helm-render`: rc=0; rendered CRD alpha4-only.
- `make test-helm-chart`: rc=0, including bad-digest, tag, empty-image, unknown-key, and range negative cases.
- `make verify-generated`: rc=0; operator/chart CRD SHA256 identical (`10071348fbc60c9d1c33797bbf632d05a045d9bc494e5c219e510e7cd449fbcc`).
- `make test-fast`: rc=0 from both implementation and independent review; reviewer used a fresh environment shell.
- Scope/private-infrastructure/test-integrity checks: clean.
- No smoke required for H1: it intentionally renders no workloads.

### Settlement amendment

H1 additionally owns one `.gitignore` line: `/bin/helm-*`, directly mirroring the existing `/bin/kubectl-*`. The pinned Helm installer writes below repository `bin/`; without this line, every `helm-tools`/`test-fast` run would dirty the checkout. The manager licensed this exact one-line addition before independent review; the reviewer verified it was the only scope extension.

### Reviewer observations (non-blocking)

1. The installer stages its final `mv` from `$TMPDIR`; atomicity assumes source and destination share a filesystem. Checksum verification and safe failure remain correct.
2. The positive storage-quantity regex intentionally fails closed and rejects uncommon fractional forms such as `0.05Gi`; the documented normal values pass.

## Next wave

**W2 = H2 ∥ H3** in separate Orca child worktrees after explicit user/manager start:

- H2 owns control-plane templates plus SM readiness/numeric-user and Operator pull-secret corrections.
- H3 owns NATS/Core DB/storage/networking templates.
- Both consume H1 values/schema/helpers without editing them.
- W2 settlement requires both independent reviews, merge, `make test-fast`, and the consolidated smoke gate specified by their slices.
