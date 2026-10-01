# Design note — The experiment's terminal paths and their live triggers

**Feature:** experiment-terminal-e2e — live proof of the `SimulationExperiment`'s terminal branches.
**Foundation:** the landed [experiment-phase-aggregation](../../experiment-phase-aggregation/FEATURE.md) feature (merged `c993078`) built the terminal phase machine; this feature proves its paths live on the cluster. All findings below are code-verified against the merged `main` (2026-10-01 recon).

## 1. The two Error flavors (user ruling 2026-10-01: both are tested)

The operator's `Error` is written by exactly two writers, and they are behaviorally disjoint — different machinery exercised, different cluster states left behind. A test that proves only one proves half the contract.

### Flavor 1 — Validation-Error (the pre-creation gate)

`validateExperiment` (`simulationexperiment_alpha4_controller.go:259`) rejects the experiment **before any component is created**; the reconciler writes `Error` via `setErrorStatus` → `patchPhase` (the status subresource). The validated surface includes the immutable-digest discipline: `ValidateDigestImage` (`images.go:32`) requires the exact form `name@sha256:<64 lowercase hex>` for `translator.image`, `baseImage`, `builderImage`, and `postProcessingService.image` — the repo's "use immutable image digests" rule is *actively enforced* at the operator's gate, not just prose in `AGENTS.md`.

**Live trigger:** a red experiment whose `translator.image` carries a tag but no digest (e.g. `registry.example/foo:v1`). Deterministic, instant, and — critically — it leaves **zero children**: the assert-set includes the absence of every owned resource.

**Proves:** the validation gate, the fast-fail, the `Error` phase write + message, stickiness (a follow-up reconcile parks), and the no-children guarantee.

### Flavor 2 — Provisioning-Error (the mid-sequence failure, with partial children)

`provisionComponents` (`:391-401`) reconciles in a fixed order: **detaildb → resultdb → translator → PPS → runner ServiceAccount**. A creation failure mid-sequence writes `Error` with everything before the failure already existing — the canonical "provisioning failed with partial children" state.

**Live trigger:** the NodePort conflict. The PPS spec's `nodePort` flows into the Service (`applyServiceSpec`, `:840-841`: applied when `serviceType` is NodePort); a blocker Service created by the e2e spec first holds the port, then the red experiment requests it → the API server **hard-rejects** the PPS Service creation ("provided port is already allocated" — a permanent rejection, not retryable) → `reconcilePPS` fails → `Error` **with the two databases, the translator, and their Services/Secrets already present**. Pure spec-side setup; no platform change; passes every validation gate and fails exactly where a real cluster-level provisioning failure would.

**Proves:** the mid-sequence failure path, the `Error` write, **partial children persisting** (the report-only semantics ruled for terminals — no auto-teardown, children stay for inspection), stickiness, and — as the closing assertion — the GC cascade over the partial set when the user deletes the red experiment.

### The readiness contract (why "nonexistent image" is not an Error)

A well-formed, digest-pinned image that simply does not exist in the registry produces components that create fine and pods that sit in `ImagePullBackOff`. The operator's designed behavior there is **park in `Provisioning` forever**: `checkReadiness`'s own doc ("requeues without becoming Error and without performing application database work") — provisioning problems requeue indefinitely; `Error` is reserved for deterministic failures. This is a *contract*, not a test gap: making a bad image produce `Error` would be a new platform feature (a readiness deadline), ruled out of this feature's scope unless the user wants it designed (open ruling E2).

## 2. The Failed route (open ruling F1 — options presented)

The SM has exactly three writers of scenario-`Failed` (code-verified), and only the first is per-experiment triggerable through the CR:

1. **`stop_unmet`** (`verdict.go:31` → `MarkScenarioFailedFrom(PostProcessing)`) — the PPS's cap criterion. The stop decision is `n >= MaxReplications` on the **pooled** count (`evaluation.go:267`) — there is no fast-fail on `n_req` alone. `MaxReplications` is a PPS **command-line flag** (default 10000; `config.go`), and the operator's `ppsEnvVars` (`:750`) passes **env only, no args** — so today the cap is unreachable per-experiment through the CR. At the smoke's +30/round clamp, the default cap means ~333 rounds (hours): not smoke-viable as-is.
2. **Translation-publish attempts exhausted** (`attempt_failed.go:49`) — NATS-level publish failures; not triggerable per-experiment in a healthy smoke stack.
3. **Runner-start `ProjectionInvalid`** (`scheduler.go:81-86` → `StartingRunners → Failed`) — permanent projection validation failures (runner digest/repository, registry Secret, ServiceAccount, effective-Job construction). All are shared-infrastructure or translator-behavior conditions, not reachable through the CR without tampering with the green experiment's dependencies.

**The F1 proposal:** wire the cap through the CR — `PostProcessingSpec.MaxReplications` (optional, minimum 1; absent = the PPS default) → the operator passes `-max-replications <n>` as the PPS container args. The PPS's own documentation calls the cap "the user-defined maximum total number of replications"; the CR *is* the user surface, and today that knob has no per-experiment path — this wiring is a genuinely missing piece of the provisioning contract (the same class as the wave-cap wiring landed in S4R). With it, a red experiment (`maxReplications: 2`, one loop-style scenario: 1 initial replication, ε = 0.92 — the existing sensible values, no ε manipulation) runs exactly two rounds: n=1 unmet → top-up → n=2 unmet → cap reached → `stop_unmet` → scenario `Failed` → verdict `Failed` (fail-fast) → phase `Failed`. Deterministic by profile arithmetic; Q6-compliant (determinism via the cap and fleet size, ε keeps real values).

**The richness option (open ruling S):** a second red scenario that *would* meet, still running when the first fails, live-proves the two rulings that only the live path can show: the verdict lands **while the sibling is in flight** (fail-fast), and the experiment stays `Failed` even after the sibling finishes (absorbing).

## 3. The red-profile mechanics (open ruling M)

Scenarios reach the SM through the EDS: each experiment owns its EDS deployment (the spec's `experimentalDesignService`), and the smoke's eds-mock is already env-driven (`PROJECT_NAME`-keyed, deterministic payload generation). A red batch served when the project name matches a convention is a licensed-mock extension — zero operator changes. The second experiment is a full but minimal per-experiment stack (its own databases, translator, PPS, EDS), exactly what the multi-experiment SM design (`ListSimulationExperiments` cluster-wide, per-project subjects) already serves.
