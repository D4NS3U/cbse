# Implementation handoff — Helm control-plane installation

This is the durable routing ledger for [FEATURE.md](FEATURE.md). Slice states use `complete` / `incomplete` / `verification-blocked`. Specifications remain normative and read-only to workers. The current parked-branch summary and resume procedure are in [PAUSE_CHECKPOINT.md](PAUSE_CHECKPOINT.md).

## Current state

| Slice | State | Landing / evidence | Next owner |
|---|---|---|---|
| H1 — chart skeleton and contract tests | **complete** | Landed on feature branch `D4NS3U/helm-control-plane-installation` via merge commit `bcc6d87` (reviewed implementation HEAD `2ac80718fa87208c562b7d8d49dd2987806c5af0`; commits `82a64ef`, `2ac8071`) | none |
| H2 — Operator and Scenario Manager workloads | **complete** | Landed on feature branch via merge commit `6969513` (reviewed implementation HEAD `c0d06668a1bf984a23906e75123c24f6ae6e1036`; commits `eeb592a`, `c0d0666`) | none |
| H3 — internal NATS and Core DB | **complete** | Landed on feature branch via merge commit `e465775` (reviewed implementation HEAD `60b9a3efdf000df6ea8f4b0e25cdf39fa593573a`); F-R1 closed | none |
| H4 — lifecycle safety | **complete** | Landed on feature branch via merge commit `b453fff` (reviewed corrected HEAD `0d4800c2edc72554b5869e5d771e07b1778bb150`); fresh lifecycle and 15/15 smoke independently rerun | none |
| H5 — Helm smoke integration | **complete** | Landed via merge commit `8267460` (reviewed corrected HEAD `abe40ff`; implementation `f27eb5d` plus namespace-scope fix `abe40ff`); independent manifest and Helm smoke both 15/15 | none |
| H6 — documentation | **complete** | Landed via merge commit `6796c8a` (reviewed corrected HEAD `5c75b87`; implementation `32d2856` plus review fixes `5c75b87`) | none |

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

## H4 settlement (2026-11-04)

- **Run:** `run_027bd9caedc7`
- **Implementation:** task `task_10c941a7c8ae`, dispatch `ctx_c610b8a40a7c`, runtime attestation `ai.forge/qwen3.8-27b-nvfp4`; whitespace-only correction task `task_1bb82b4e9a5c`, dispatch `ctx_9dfd83e19d89`, corrected HEAD `0d4800c2edc72554b5869e5d771e07b1778bb150`.
- **Independent review:** task `task_d94fe39521f9`, dispatch `ctx_e30bf75255fd`, runtime attestation `ai.forge/glm`, verdict **approve** on exact corrected HEAD.
- **Landing:** feature-branch merge commit `b453fff`; `main` was not touched.
- **Reports (untracked, feature checkout):** `artifacts/orchestration/helm-H4-implementation-report.md`, `artifacts/orchestration/helm-H4-review.md`.

### Verified static and fast gates

- Shell syntax, harness self-tests, generator positive/negative matrix, cleaner ordering, image-lock/preflight validations: rc=0.
- `make test-helm-chart`: rc=0 with H1–H4 assertion suites and CRD lockstep.
- `make test-fast`: rc=0 from implementation and independent review.
- Generator output is atomic mode 0600, digest-only, five-component, rejects tracked destinations/malformed refs, and does not print repository values.
- Hooks have deterministic ordering, read-only least-privilege RBAC, payload-safe registry validation, exact guard recovery guidance, restricted numeric-1000 security, no pull Secret bootstrap dependency, and no destructive command.
- `clean.sh` deletes experiments with wait before namespace deletion while preserving ownership refusal and Lease release.

### Hook-image amendment

The ruled distroless source cannot execute the required validation and recovery logic. Manager ruling `msg_60f9e1779a93` replaces it with a single public, digest-pinned kubectl-plus-POSIX-shell image at Kubernetes v1.32.5; no third image was added. Independent review verified public pull, linux/amd64, exact kubectl version, `/bin/sh`, immutable digest, and UID 1000/read-only/drop-ALL/no-new-privileges execution. The hook ServiceAccount still has no `imagePullSecrets`.

### Fresh independent live gates

- **Lifecycle:** reviewer run `20261104093529-9ce91c`, rc=0. Expected missing-Secret install failure, successful install, marker survival across Core DB/NATS Pod restarts, stable credential hashes/PVC identities across upgrade, expected guarded uninstall failure with shared workloads Ready, finalizer-safe CR deletion, successful final uninstall, retained CRD/PVCs/Core DB credential Secret, and installation-owner registry Secret non-ownership all proven.
- **Manifest smoke:** reviewer run `20261104093746-238904`, **15/15 passed**, rc=0, 531 seconds.
- Implementation live runs `20261104090633-0258a2` (lifecycle) and `20261104091352-c5d95d` (15/15 smoke) also passed.
- All four evidence directories are preserved under `artifacts/test/` and remain untracked.
- Teardown verified after reviewer gates: namespaces absent, smoke Lease absent, zero `SimulationExperiment` resources, shared CRD intact.

### Reviewer observations (non-blocking)

1. The implementation report describes the registry Secret copy mechanism imprecisely; code correctly uses the established payload-safe `kubectl get ... | jq metadata scrub | kubectl apply` pipe and does not persist/log the payload.
2. Guard count word-splitting is safe because Kubernetes object names contain no whitespace; names are never printed.
3. The pre-existing preflight artifact includes the cluster API endpoint; H4 did not introduce or change that behavior. H5 documentation/artifact review should avoid reproducing operational endpoints.

## H5 settlement (2026-11-25)

- **Run:** `run_5f3031d1e91d`
- **Implementation:** task `task_9298dfd646fc`, dispatch `ctx_4f76fb314ca2`, runtime attestation `ai.forge/qwen3.8-27b-nvfp4`, commit `f27eb5d`.
- **Licensed blocker fix:** task `task_aefabc05d011`, dispatch `ctx_a1ec5ff5b407`, corrected HEAD `abe40ff`; the Scenario Manager aggregate pass now lists only the validated watch namespace rather than requiring forbidden cluster-wide access.
- **Independent review:** task `task_70e21d3bf134`, dispatch `ctx_8535acddab64`, runtime attestation `ai.forge/glm`, verdict **approve** on exact HEAD `abe40ff`.
- **Landing:** feature-branch merge commit `8267460`; `main` remains untouched.
- **Reports (untracked):** `artifacts/orchestration/helm-H5-implementation-report.md`, `artifacts/orchestration/helm-H5-review.md`.

### Verified H5 gates

- `CBSE_STACK_INSTALLER=manifest|helm` selects one shared harness flow; `manifest` remains the default and invalid values fail before cluster mutation.
- `make test-fast`, shell syntax, fake-tool installer/cleanup tests, targeted Scenario Manager race tests, and the unchanged 15-spec compile/count gates passed.
- Core DB access is installer-independent through an exact-one-Ready-Pod lookup by stable label; SQL, scenarios, timings, and assertions remain unchanged.
- Helm mode consumes H4's private digest-values generator, installs with pinned Helm, preserves chart-owned Core DB credentials, avoids the default-ServiceAccount patch, records only safe status/history/inventory artifacts, and tears down CRs before Helm release/namespace/Lease removal.
- Implementation manifest smoke `20261125095819-ffbd33`: **15/15 passed**, clean teardown.
- Implementation Helm smoke `20261125101028-96e5cb`: **15/15 passed**, clean teardown; this supplies the required live NATS read-only-root and Core DB `PGDATA` chart proof.
- Independent review manifest smoke `h5rev-manifest-02`: **15/15 passed** after one transient EDS port-forward EOF in the first attempt; independent Helm smoke `h5rev-helm-01`: **15/15 passed**. Release, namespace, Lease, and SimulationExperiment cleanup were verified; the shared CRD remained.
- Retained Helm run `20261125094418-d056a2` plus `test-clean` proved CR deletion → Helm uninstall → namespace deletion → Lease release.

### H5 blocker resolution and observations

The first Helm run exposed that the aggregate pass used `listNamespace = ""` while the chart intentionally grants namespaced RBAC. The accepted fix wires `SCENARIO_MANAGER_WATCH_NAMESPACE` into the aggregate Kubernetes adapter, rejects an empty namespace, and includes a two-namespace regression test. Chart RBAC remained namespaced; no ClusterRole or permission broadening was added.

Independent review recorded non-blocking observations for later maintenance: a cold-node Translator pull should remain under observation despite successful live runs without the default-SA patch; one transient EDS port-forward EOF belongs in the flake ledger; and the smoke EXIT trap's uninstall-error reporting differs from explicit `test-clean` failure propagation. None blocks H5.

## H6 settlement (2026-11-25)

- **Implementation:** task `task_61f0462a36de`, dispatch `ctx_e466eed45d70`, runtime attestation `ai.forge/qwen3.8-27b-nvfp4`, implementation commit `32d2856`.
- **Review-fix round:** task `task_ddc088b582a5`, dispatch `ctx_98423aaedeb2`, corrected HEAD `5c75b87`.
- **Independent review:** task `task_87955da87dbc`, dispatch `ctx_7f69a86795fd`, initial `request-fixes`; re-review task `task_e4621c06ac61`, dispatch `ctx_f0920b45296b`, runtime attestation `ai.forge/glm`, verdict **approve** on exact corrected HEAD `5c75b87`.
- **Landing:** feature-branch merge commit `6796c8a`; `main` remains untouched.
- **Reports (untracked):** `artifacts/orchestration/helm-H6-implementation-report.md`, `artifacts/orchestration/helm-H6-review.md`.

### Verified H6 gates

- Added canonical `docs/HELM_INSTALLATION.md` and completed chart README/NOTES, root README, cluster requirements, testing guide, and bounded wiki installation/topology links.
- Documentation accurately covers installed resources, same-namespace operation, Secret ownership/lifecycle, payload-safe registry Secret creation, generated immutable image values, install/observe/first experiment, upgrade/rollback, CRs-first guarded uninstall, retained data, CRD lifecycle, troubleshooting, limitations, and the external-service roadmap.
- Review fixes corrected the per-workload private-image pull boundary, aligned hook/NATS/PostgreSQL repositories with landed image locks, and reduced registry Secret verification to metadata name/type only.
- `make helm-lint`, `make helm-render`, and `make test-helm-chart`: rc=0 from implementation and independent re-review.
- Relative-link, command/value/resource-name, secret/private-infrastructure hygiene, required-content, and docs-only containment audits all passed.

## Feature completion

H1–H6 are complete and independently approved. The feature branch contains the chart, dual-installer smoke integration, lifecycle safety, and canonical documentation. Remaining work is administrative settlement only: push the synchronized feature branch and obtain explicit user approval before merging it into `main`.
