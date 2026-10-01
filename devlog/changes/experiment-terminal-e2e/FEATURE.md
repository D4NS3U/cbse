# FEATURE.md — Experiment-terminal-e2e: live proof of the `Error` and `Failed` terminal branches

**Foundation:** [notes/terminal-paths-and-triggers.md](notes/terminal-paths-and-triggers.md) — the code-verified route analysis (the two Error flavors, the readiness park contract, the three scenario-Failed writers, the cap semantics). That note is normative background for every worker dispatched under this spec. The terminal phase machine itself is landed and cluster-proven (experiment-phase-aggregation, merged `c993078`).
**Normative for every worker dispatched under it. Read-only for workers.** On contradiction between this spec and live code that repository inspection cannot resolve: stop and ask via the ask channel.

## 1. Title and mission

The landed terminal phase machine is proven live only for the `Finished` chain (the 7th smoke spec). This feature proves the remaining terminal branches live on the cluster, in the same harness discipline: **both `Error` flavors** (user ruling 2026-10-01) — the Validation-Error (pre-creation gate, zero children) and the Provisioning-Error (mid-sequence failure, partial children persist) — and, pending ruling F1, the **`Failed` chain** (a deterministically failing scenario → `stop_unmet` → verdict `Failed` → phase `Failed`). The operator's contracts under test are the ones the recon verified: the validation gate, the fixed provisioning order, the absorbing stickiness, the report-only persistence of partial children, and the GC cascade over them on user deletion.

## 2. Scope

- **In scope:** (a) e2e specs creating red experiments in the smoke namespace (spec-created CRs via the suite's `k8sClient`; the operator is multi-experiment by design); (b) pending F1: the `PostProcessingSpec.MaxReplications` CR field and the PPS `-max-replications` arg wiring (API surface + CRD regen + validation + envtest); (c) pending F1/M: the eds-mock red-batch capability (project-name-keyed, env-driven).
- **Out of scope:** any change to the landed terminal phase machine (the derive, the stickiness, the aggregate pass); any readiness-deadline feature (open ruling E2 — park-forever stays the contract unless the user rules otherwise); the green experiment's profile (the 2×2 batch, fleet sizes, ε values stay exactly as landed); the harness's cluster/registry machinery.
- **Breaking changes / compatibility:** the (pending) `MaxReplications` field is additive and optional (absent = the PPS flag default, 10000) — old objects are unaffected; the CRD regen is additive; the mock change is test-infrastructure only.

## 3. Workflow decisions (concern → decision)

| # | Concern | Decision for this feature |
|---|---|---|
| D1 | The Error coverage | **Three flavors are tested (user rulings 2026-10-01).** Flavor 1 — Validation-Error: a red experiment with a non-digest `translator.image` → `Error` before any component exists; assert phase+message, stickiness, and **zero children**. Flavor 2 — Provisioning-Error, creation variant: a blocker Service holds a NodePort, the red experiment's PPS requests it → API-server rejection mid-sequence → `Error` with the databases + translator + their Services/Secrets already present; assert phase+message, stickiness, **the partial children persist** (report-only semantics), and the GC cascade over the partial set on deletion. Flavor 3 — Provisioning-Error, readiness variant (E2 amendment): a well-formed, digest-pinned, nonexistent PPS image → every component creates, the pull fails observably (`ImagePullBackOff`) → the amended detection (T2) moves the experiment to `Error` with the full child set present but not ready; assert phase+message, stickiness, and the not-ready inventory. |
| D2 | Red experiments' placement | Spec-created CRs in the smoke namespace alongside the green experiment — the multi-experiment SM (cluster-wide listing, per-project subjects) already serves this shape; no harness changes to the green profile. |
| D3 | The readiness contract (E2-amended) | **Provisioning does not go on indefinitely (user ruling 2026-10-01).** An *observed* provisioning failure → `Error` immediately, without retry: creation failures (already the code's behavior) and Kubernetes-reported component failures consumed by the amended `checkReadiness` (pod container failures: `ErrImagePull`/`ImagePullBackOff`, `CrashLoopBackOff`, `Unschedulable`; Deployment `ProgressDeadlineExceeded` as the backstop). A healthy-but-slow rollout — no failure condition — keeps waiting; no manager-side timers, only observed conditions. Scenario-level response deadlines remain absent (the exception is scoped to the experiment phase level). Implemented as slice T2. |
| D4 | The Failed coverage | **Both routes are tested (user ruling 2026-10-01).** Route 1 — **F-crash (the operational path, zero platform change):** the red batch carries a recipe whose simulation exits nonzero → the runner Job exhausts its backoff (`globalBackoffLimit = 4`, ≈3–4 min) → `JobFailed` → `ObservationFailed` → the guarded `InProcessing → Failed` → verdict Failed (fail-fast) → phase Failed. Route 2 — **F1 (the scientific path):** `PostProcessingSpec.MaxReplications` (optional, min 1, absent = PPS default 10000) → the operator passes `-max-replications <n>` as the PPS container args (the CR as the user surface of the PPS's "user-defined maximum"); the red profile `maxReplications: 2` with one loop-style scenario (1 initial rep, ε = 0.92 — existing sensible values; Q6: determinism via cap arithmetic, never ε manipulation) → two rounds → `stop_unmet` → Failed → verdict Failed → phase Failed. Together they cover every per-experiment triggerable writer of scenario-`Failed` (the design note's fault model: observed failures route to Failed; silent faults stall by design — no deadlines). |
| D5 | The red profile's richness | **Open ruling S.** One red scenario (minimal chain) or two (a failing one plus a still-running sibling that would meet — live-proving fail-fast *and* absorbing: the verdict lands while the sibling is in flight; the experiment stays Failed after the sibling finishes). |
| D6 | The red batch source | **Open ruling M.** The eds-mock serves a red batch keyed by the project-name convention (a licensed-mock extension, env-driven, zero operator changes) — versus env plumbing through the EDS spec (more machinery for a mock). |
| D7 | Observability | Each spec persists its terminal evidence (phase, message, verdict, the partial-children inventory) to the artifact directory per the suite's triage discipline. |

## 4. Design patterns (Gang of Four)

| # | Concern | Pattern | Concrete application | Why the simpler composition is not enough |
|---|---|---|---|---|
| 1 | Red experiments in the shared namespace | **Builder** | Each spec assembles its red CR from the green experiment's spec (same databases/translator/PPS contracts) with exactly the one red delta (image, nodePort, or cap) | Hand-written full CRs would drift from the real spec shape and test configuration accidents rather than the intended failure |
| 2 | The NodePort blocker | **Fixture setup** (test-side) | The spec creates the blocker Service before the red CR and removes it in teardown | The conflict must pre-exist the provisioning attempt to be deterministic; the operator's own machinery stays untouched |

*Deliberately not used:* no mocking of the operator or SM (the point is the live behavior); no test doubles for the API server (envtest already covers unit-level; the cluster's real rejection semantics are the subject).

## 5. Global constraints (per worker, verbatim discipline)

- **Obey `AGENTS.md`.** `make test-fast` after every Go/CRD change; the consolidated settlement smoke is the manager's gate.
- The design note is normative background; this spec's decisions (§3) are the binding application.
- **Scope discipline:** exactly the slice's ownership partition; the green experiment's profile and every landed contract stay untouched; discovered gaps stop-and-ask.
- **Test integrity:** never weaken, delete, skip, or rewrite a test to conceal a failure; the green specs stay byte-identical.
- **Branch discipline:** implementation lands on the dedicated feature branch (Orca-managed worktree off `main`); workers commit only to that branch with the `[orchestration: task <id> dispatch <id>]` trailer; the merge to `main` is one user-approved event at settlement. Independent review lane (`glm`) per `MANAGER.md`.
- **Secrets hygiene; verification honesty; runtime attestation** (`printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"`, expected `ai.forge/qwen3.8-27b-nvfp4` for implementers, `ai.forge/glm` for reviewers).

## 6. Slices (task specs are authored per slice at its dispatch wave)

| Slice | Content | Dependency | Tier gate |
|---|---|---|---|
| **T1 — Error flavors spec** | The two red experiments (Validation-Error: non-digest image, zero children; Provisioning-Error: NodePort conflict, partial children persist + GC cascade on deletion) as e2e spec(s); diagnostics artifacts; all green specs byte-identical | — | `make test-fast` + the settlement smoke |
| **T2 — readiness-failure detection (the E2 amendment)** | The operator change: `checkReadiness` consumes the Kubernetes-observed failure signals (pod container failures: `ErrImagePull`/`ImagePullBackOff`, `CrashLoopBackOff`, `Unschedulable`; Deployment `ProgressDeadlineExceeded`, with `progressDeadlineSeconds` set explicitly on owned Deployments) → `setErrorStatus` with a message naming the component; no retry. Envtest: a failing-condition component → `Error`; a not-ready-but-healthy component → keeps requeueing. Plus the flavor-3 red spec (the nonexistent PPS image → `Error` with the full not-ready child set) | — | `make test-fast` + the settlement smoke |
| **T3 — maxReplications wiring** | `PostProcessingSpec.MaxReplications *int` (optional, min 1, absent = PPS default) + validation + CRD regen + the operator passing `-max-replications <n>` as the PPS container args + envtest | — | `make test-fast` |
| **T4 — the red batch (mock capability)** | The eds-mock's red batch, project-name-keyed (ruling M): the crashing recipe (a simulation that exits nonzero) for the F-crash experiment, and the loop-style scenario profile for the F1 experiment — licensed-mock extension, env-driven | — | `make test-fast` |
| **T5 — the two Failed-chain specs** | The F-crash experiment (crashing scenario + the S-ruled sibling → fail-fast and absorbing live proofs) and the F1 experiment (`maxReplications: 2` + the loop scenario → `stop_unmet`); the live chain assertions (scenario Failed → verdict Failed → phase Failed + stickiness); diagnostics artifacts | T3, T4 | `make test-fast` + the settlement smoke |

Wave plan: serialized single workers (W1 = [T1]; W2 = [T2]; W3 = [T3]; W4 = [T4]; W5 = [T5]), each with the independent review lane; the consolidated settlement smoke runs after W5.

## 7. Verification gates

- Per-tier rule as in §5; T1/T3 are live-behavior specs — their tier gate includes the settlement smoke (the specs are compile-proven by `make test-fast`, live-proven by the manager's consolidated gate).
- Feature-level acceptance: on the cluster — both Error flavors observed (phase, message, children inventory matching the flavor's contract) and, pending F1, the Failed chain observed end-to-end with the ruled richness assertions. Artifacts under `artifacts/test/<run-id>/`.
- **Forbidden shortcuts:** weakening any green spec; asserting an Error flavor with the wrong trigger (e.g., a nonexistent image for Validation-Error); letting the red experiments' setup touch the green experiment's resources; prose-only evidence.

## 8. Completion and handoff

State vocabulary per slice: `complete` / `incomplete` / `verification-blocked`, recorded with evidence in `IMPLEMENTATION_HANDOFF.md` after manager-verified settlement; the merge to `main` is the user's single approval at settlement.

## Rulings record

- ~~**E1** — the Error coverage~~ **Answered (2026-10-01):** both flavors are tested — the Validation-Error (pre-creation gate, zero children) and the Provisioning-Error (mid-sequence failure, partial children persist) (recorded in D1).
- ~~**E2** — the watchdog/deadline family~~ **Answered (2026-10-01, with one user exception):** (1) no scenario-level response deadlines — silent component faults stall the scenario by design; (2) **exception — Provisioning does not go on indefinitely**: an observed provisioning failure moves the experiment to `Error` immediately without retry (code-verified: creation failures already behaved exactly so — `provisionComponents` → `Error`, sticky, no retry; readiness failures requeued indefinitely *by documented design*, so the exception is a real behavior change implemented as slice T2 — consuming Kubernetes-observed failure conditions, never a manager-side timer); (3) no TTL, auto-deletion, or auto-cleanup of experiments in any phase — an `Error` experiment persists with its children until the user deletes it; (4) the inverse stop-signal stays a recorded backlog item. Stalls outside Provisioning are correct, observable behavior; recovery is user-driven (delete + recreate).
- ~~**F** — the Failed coverage~~ **Answered (2026-10-01):** both routes are tested — F-crash (the operational path: the crashing recipe → `JobFailed` → `InProcessing → Failed`) and F1 (the scientific path: the `MaxReplications` CR wiring → `stop_unmet`) (recorded in D4).
- **S** — the red profile's richness: the F-crash experiment carries one crashing scenario plus a **loop-style sibling** (a met-path sibling finishes before the ≈3–4 min backoff exhausts; only a multi-round sibling is still in flight when the verdict lands) → live-proving fail-fast (the verdict lands while the sibling runs) and absorbing (the experiment stays Failed after the sibling finishes); the F1 experiment stays minimal (one scenario — its subject is the criterion, not the timing) — *pending user confirmation.*
- **M** — the red batch source: the eds-mock project-name-keyed red batch — *pending user ruling.*
- **R** — the readiness-failure signal set (E2 amendment, slice T2): pod container failures (`ErrImagePull`/`ImagePullBackOff`, `CrashLoopBackOff`, `Unschedulable`) + Deployment `ProgressDeadlineExceeded` with `progressDeadlineSeconds` set explicitly on owned Deployments — *pending user confirmation; recommendation: exactly this set, no manager-side timers.*
