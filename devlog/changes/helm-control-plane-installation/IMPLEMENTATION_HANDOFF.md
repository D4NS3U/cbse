# Implementation handoff — Helm control-plane installation

This is the durable routing ledger for [FEATURE.md](FEATURE.md). Slice states use `complete` / `incomplete` / `verification-blocked`. Specifications remain normative and read-only to workers.

## Current state

| Slice | State | Landing / evidence | Next owner |
|---|---|---|---|
| H1 — chart skeleton and contract tests | **complete** | Landed on feature branch `D4NS3U/helm-control-plane-installation` via merge commit `bcc6d87` (reviewed implementation HEAD `2ac80718fa87208c562b7d8d49dd2987806c5af0`; commits `82a64ef`, `2ac8071`) | none |
| H2 — Operator and Scenario Manager workloads | **complete** | Landed on feature branch via merge commit `6969513` (reviewed implementation HEAD `c0d06668a1bf984a23906e75123c24f6ae6e1036`; commits `eeb592a`, `c0d0666`) | none |
| H3 — internal NATS and Core DB | **complete** | Landed on feature branch via merge commit `e465775` (reviewed implementation HEAD `60b9a3efdf000df6ea8f4b0e25cdf39fa593573a`); F-R1 closed | none |
| H4 — lifecycle safety | incomplete | H2+H3 and W2 merged-feature gates satisfied | manager/user decision to start H4 |
| H5 — Helm smoke integration | incomplete | waits for H4 | — |
| H6 — documentation | incomplete | waits for H5 | — |

## H1 settlement (2026-11-02)

- **Run:** `run_019967646d6d`
- **Implementation:** task `task_863c88586e8e`, dispatch `ctx_5e875c8442d6`, runtime attestation `ai.forge/qwen3.8-27b-nvfp4`.
- **Independent review:** task `task_538e39970502`, dispatch `ctx_f364b232c6d2`, runtime attestation `ai.forge/glm`, verdict **approve** on exact HEAD `2ac80718fa87208c562b7d8d49dd2987806c5af0`.
- **Landing:** feature-branch merge commit `bcc6d87`; the H1 child worktree and slice branch were removed after evidence preservation. `main` remains untouched until final settlement and explicit user approval.
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

## H2 settlement (2026-11-03)

- **Run:** `run_0439c86c2523`
- **Implementation:** task `task_51a6a19ef90d`, dispatch `ctx_c8db95681449`, runtime attestation `ai.forge/qwen3.8-27b-nvfp4`.
- **Independent review:** task `task_3f447409283b`, dispatch `ctx_5075b3aa1cd6`, runtime attestation `ai.forge/glm`, verdict **approve** on exact HEAD `c0d06668a1bf984a23906e75123c24f6ae6e1036`.
- **Landing:** feature-branch merge commit `6969513`; `main` was not touched.
- **Reports (untracked, feature checkout):** `artifacts/orchestration/helm-H2-implementation-report.md`, `artifacts/orchestration/helm-H2-review.md`.

### Verified gates

- Scenario Manager targeted race tests and full vet: rc=0, covering health, config, namespace-scoped RBAC, namespace-scoped informer cache, and core startup ordering.
- Experiment Operator controller envtest: rc=0; named tests prove exact `cbse-registry-auth` pull Secrets on EDS/PPS workloads.
- Scenario Manager image/chart identity: numeric UID/GID 1000; no root chart exception.
- `make test-helm-chart`: rc=0 with exact namespaced control-plane resources, RBAC, env, probes, security, digest images, and no ClusterRole/ClusterRoleBinding.
- `make test-fast`: rc=0 from both implementation and fresh independent review.
- API/generated/private-infrastructure/test-integrity/scope checks: clean.
- W2 cluster smoke remains pending until H3 lands.

### Manager-licensed correctness amendments

1. The H1 chart harness now counts version entries only inside the rendered CRD document; valid RoleBinding subject names no longer cause a false failure.
2. Scenario Manager now requires and validates `SCENARIO_MANAGER_WATCH_NAMESPACE`, scopes every startup SelfSubjectAccessReview and its SimulationExperiment informer cache to that namespace, and has no cluster-wide fallback. This makes the namespaced Helm Role operationally consistent.

### Mandatory H3/W2 consolidation item

H2 review finding **F-R1** is non-blocking for the H2-only render but blocking for H3/W2 settlement: `test/helm/assert-control-plane.sh` currently pins full-render document and Secret-reference counts to the H2-only chart. H3 must receive a manager-licensed, narrowly scoped amendment so those assertions select/count only H2-owned resources while remaining strict; otherwise adding the valid H3 resources will make `make test-fast` fail deterministically.

## H3 and W2 settlement (2026-11-04)

- **Run:** `run_0fc1d77cedef`
- **Implementation:** task `task_7542d62a9b39`, dispatch `ctx_f073ca1b2b80`, runtime attestation `ai.forge/qwen3.8-27b-nvfp4`.
- **Independent review:** task `task_ebc4071edf5a`, dispatch `ctx_d058ce75be58`, runtime attestation `ai.forge/glm`, verdict **approve** on exact HEAD `60b9a3efdf000df6ea8f4b0e25cdf39fa593573a`.
- **Landing:** feature-branch merge commit `e465775`; `main` was not touched.
- **Reports (untracked, feature checkout):** `artifacts/orchestration/helm-H3-implementation-report.md`, `artifacts/orchestration/helm-H3-review.md`.

### Verified H3 gates

- `bash -n` over H2/H3 chart assertion scripts: rc=0.
- `make helm-lint`, `make helm-render`, and `make test-helm-chart`: rc=0.
- Persistent, ephemeral, explicit-storage-class, `retain=false`, and NetworkPolicy-disabled render branches independently verified.
- Core DB Secret exact keys, generate/reuse/fail/retention lifecycle, and non-Secret credential hygiene verified without recording generated password bytes.
- Authentication-free NATS/JetStream config, probes, ports, security, persistence, and NetworkPolicy semantics verified.
- F-R1 closed by selecting only H2-owned documents while preserving strict H2 counts and global security invariants.
- `make test-fast`: rc=0 from implementation, independent review, and the merged feature branch.
- Scope/private-infrastructure/test-integrity/API/generated/CRD checks: clean.

### W2 consolidated manifest smoke

- Fresh immutable Operator and Scenario Manager images were built and published from the merged H2/H3 feature tree; the six unchanged component digests came from the latest proven eight-image set.
- The first image-build attempt ended in a local Docker BuildKit EOF and produced no digest; Docker Desktop was restarted and the clean retry succeeded.
- Smoke run `w2-20261104073014-16f4`: **15/15 passed**, rc=0, 522 seconds.
- Evidence: `artifacts/test/w2-20261104073014-16f4/` (untracked), including `summary.json` result `passed`, JUnit `tests=15 failures=0`, rendered manifests, diagnostics, and the combined immutable image set.
- Teardown verified: run namespace absent, smoke lease absent, zero remaining `SimulationExperiment` resources.

The W2 run proves manifest-path compatibility of the H2 application/image changes. It does **not** claim Helm-installed runtime proof for H3's NATS read-only-root or Core DB `PGDATA` assumptions; those remain explicit H5 Helm-smoke obligations.

### H3 reviewer observations (non-blocking, routed to H5)

1. NATS `readOnlyRootFilesystem: true` is source-supported but still needs live Helm-installed proof.
2. Core DB `PGDATA=/var/lib/postgresql/data/pgdata` is the intended PVC-compatible path but first receives live chart proof in H5.
3. `js-enabled-only=true` verifies runtime JetStream readiness when the chart enables JetStream; it is not a generic assertion that config enabled JetStream.
4. F-R1 deliberately scopes H2 Secret-reference counts; equivalent strict H3-side assertions preserve combined coverage.

## Next wave

H4 — lifecycle safety — is unblocked:

- implement the pre-delete experiment guard, hook bootstrap RBAC/ServiceAccount, retained credential/PVC behavior, and CRs-first cleanup correction;
- receive independent review and land only on this feature branch;
- keep H5 responsible for both manifest and Helm 15/15 smoke modes, including the two live chart-runtime observations above.
