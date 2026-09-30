# FEATURE.md — Experiment-phase aggregation: `InProgress → Finished | Failed` via field-level ownership

**Foundation:** [notes/component-reports-and-field-ownership.md](notes/component-reports-and-field-ownership.md) — the architectural pattern this feature implements (per-field object ownership; the Status subresource as the component-report channel; Node/Pod/Service precedents). That note is normative background for every worker dispatched under this spec. The scenario-level machinery this builds on (terminal scenario states, verdict application, the evaluation loop) is landed and cluster-proven per the [post-processing-service feature record](../post-processing-service/IMPLEMENTATION_HANDOFF.md).
**Normative for every worker dispatched under it. Read-only for workers.** On contradiction between this spec and live code that repository inspection cannot resolve: stop and ask via the ask channel.

## 1. Title and mission

The `SimulationExperiment` custom resource gains its terminal phases. Today the Experiment Operator writes exactly one phase, `InProgress` (controller:831 `patchPhase`; the single literal `alpha4PhaseInProgress`), and the experiment never terminates — "terminality" exists only as the Scenario Manager's deletion-cleanup action. This feature closes the loop's last level, using exactly the methodology of the design note: **the SimulationExperiment becomes a multi-writer object in the sense of the Node** —

- the **Scenario Manager** — the only component that knows scenario outcomes (its Core Database, which no other component reads) — owns and writes one new status slice: `status.scenarioManagerVerdict` (verdict, counts, `observedGeneration`);
- the **Experiment Operator** — the CR's owning controller — reads that report, derives, and exclusively owns `status.phase`, transitioning the experiment to **`Finished`** when every scenario finished, or **`Failed`** when any scenario failed, with terminal stickiness (a terminal phase never regresses to `InProgress`).

Judgment is delegated (the SM reports the ground truth); authority is retained (the operator alone writes its phase) — the same discipline the PPS verdict flow established one level down. The user's first-step priority (the Finished branch) lands first; the Failed branch rides the identical machinery (only the report value differs) and is in scope.

## 2. Scope

- **In scope:** (a) the alpha4 API surface — an additive, optional `ScenarioAggregate` status struct (`verdict`, `finished`, `failed`, `total`, `observedGeneration`) with documented field ownership; (b) the SM's aggregation pass — a scheduler-cadence pass mirroring the existing SM passes: per admitted experiment, an SQL aggregation over `scenario_status`; when all scenarios are terminal and the count is non-zero, an idempotent status patch of the aggregate; plus the RBAC delta (`simulationexperiments/status` `patch` in the stack Role and the `rbac.Verify` startup checks); (c) the operator's derive branch — validation, the `observedGeneration` staleness gate, `patchPhase` to the terminal literals, stickiness, and a Kubernetes Event on the transition; (d) an e2e smoke spec proving the live chain: all scenarios `Finished` → `status.scenarioManagerVerdict` present and correct → experiment phase `Finished`.
- **Out of scope:** any change to the landed scenario-level machinery (states, rounds, verdict application, the evaluation loop); any NATS surface (none — the report channel is the CR itself); the annotation/child-CR/NATS alternatives (rejected in the design note §6); experiment deletion or retry semantics; spec-update/mid-run experiment mutation handling (the generation gate merely guards staleness); any new component image (the set stays seven).
- **Breaking changes / compatibility:** the status field is additive and optional (pointer with `omitempty`) — served alpha4, no conversion, old objects lack it harmlessly; the CRD schema regeneration is additive; `zz_generated.deepcopy.go` and the CRD yaml regenerate via the repo's `controller-gen` flow (`make verify-generated` proves byte-stable generation). The smoke's `InProgress` spec is unaffected. Field ownership (SM owns `scenarioManagerVerdict`; operator owns `phase`/`message`) is enforced by convention + RBAC + API comments, per the design note §4. **Optionality semantics (ruling 2026-09-30):** `omitempty` here is purely temporal-schema: the field is **absent until the SM writes its first verdict** — absence means *not yet reported* (or, for zero-scenario experiments under D3, never), **never** an opt-out or an alternative phase-transition path. Status fields are never user input (the enabled status subresource discards user-supplied status on create/update); the report is **the sole mechanism** of the terminal phase transition (D1).
- **Compatibility boundaries:** Kubernetes >= 1.30; alpha4-only API; merge-patch disjointness makes the two writers conflict-free by construction (the operator's patch carries only `phase`/`message`; the SM's only `scenarioManagerVerdict`).

## 3. Workflow decisions (concern → decision)

| # | Concern | Decision for this feature |
|---|---|---|
| D1 | The report channel | **The Status subresource, per the design note** — a dedicated, SM-owned `status.scenarioManagerVerdict` field; the operator derives `phase` from it. Ruled by the user's methodology choice ("this exact phase transition methodology"). The annotation handshake is documented as the pragmatic cousin and is NOT used. |
| D2 | Report shape | **A bare typed struct** (`ScenarioManagerVerdictStatus{verdict, finished, failed, total, observedGeneration}`) rather than a `metav1.Condition` list: disjoint-field merge patches are conflict-free by construction (the note §4.5), and the shape matches this codebase's minimalism. The struct's absence is the *pre-verdict* temporal state (never a user choice or an alternative mechanism — see §2). *(Open question Q1 — a Condition list brings standardized `kubectl describe` rendering at the cost of array-merge mechanics between writers.)* |
| D3 | Aggregation rule | For each admitted experiment: count scenarios by state. If **total > 0** and **no scenario is non-terminal**, aggregate: verdict `Finished` iff zero scenarios are `Failed`, else `Failed`. An experiment with zero scenarios **never aggregates** (stays `InProgress` — nothing to report). |
| D4 | Staleness contract | The SM stamps `observedGeneration` with the experiment's `metadata.generation` as read from its informer cache; the operator trusts a report **only if** `observedGeneration ==` the live generation (and validates: verdict ∈ {Finished, Failed}; `finished + failed == total > 0`). Invalid or stale reports are ignored — the next pass re-reports. |
| D5 | Drive mechanism (SM) | **A scheduler-cadence aggregation pass** mirroring the existing SM passes (runner-start, observation, evaluation publication): per admitted experiment, one SQL aggregation; if all-terminal and the cached experiment's `scenarioManagerVerdict` is absent or mismatched, patch it. No post-transition hooks — the verdict handler stays untouched; the pass is idempotent and self-healing; latency is one cadence tick. The absorbing condition (nothing leaves `Finished`/`Failed`) makes at-least-once + idempotent writes provably sufficient. |
| D6 | Derive + stickiness (ExOp) | The reconcile, after the existing provisioning, reads the report: valid + fresh (D4) → desired phase = verdict → `patchPhase` via the existing path, **once** (idempotent patch). New literals `alpha4PhaseFinished`/`alpha4PhaseFailed`. **Terminal stickiness:** once `phase` ∈ {Finished, Failed}, the reconcile never re-asserts `InProgress`, regardless of report state. |
| D7 | Observability | The operator emits a Kubernetes **Event** on the terminal transition (`kubectl describe` visibility — the design note's "garnish"). The SM logs its report writes in its established `operation=` style. |
| D8 | RBAC | The SM's stack ClusterRole (`test/e2e/manifests/base/stack.yaml`) and the `rbac.Verify` startup checks (`scenario-manager/internal/rbac/rbac.go:83-86`) gain `simulationexperiments/status` `patch`. A denied check remains a fatal startup error (existing semantics). |
| D9 | Ownership partition | Documented in the API type comments: `scenarioManagerVerdict` — *"written by the Scenario Manager; consumed by the Experiment Operator"*; `phase`/`message` — owned by the operator. The two writers' merge patches are field-disjoint by construction. |
| D10 | e2e scope | The smoke proves the **Finished** path live (all four scenarios Finished → aggregate report → experiment `Finished`). The **Failed** branch is proven at the operator's envtest level (a synthetic `verdict: Failed` report → phase `Failed` + stickiness). *(Open question Q2: a full Failed e2e profile — a second experiment with unreachable precision — is possible but doubles the smoke; recommended: envtest only.)* |

## 4. Design patterns (Gang of Four)

| # | Concern | Pattern | Concrete application | Why the simpler composition is not enough |
|---|---|---|---|---|
| 1 | SM reports; operator consumes | **Observer** | The CR's Status is the blackboard: the SM writes its slice; the operator's existing watch delivers the report as a reconcile trigger — no direct coupling, no new transport | A direct call would couple the SM to the operator's liveness and bypass the owner's reconcile; the report must survive component gaps and trigger exactly the owner's existing machinery |
| 2 | The experiment's phase machine gains terminal states | **State** | `InProgress → Finished \| Failed` as absorbing states with stickiness (D6); `patchPhase` is the only transition edge | Two writers on one object require absorbing terminal semantics — without stickiness, the operator's periodic reconcile would fight the terminal phase |
| 3 | The SM's aggregation pass | **Template Method** | The new pass reuses the established scheduler-pass skeleton (per-tick scan → gate → guarded idempotent action → no-op), like runner-start/observation/evaluation publication | A bespoke one-off loop would drift from the proven pass discipline (bounded cadence, no hot loops, idempotent no-ops) and lose its test shape |

*Deliberately not used:* Mediator (the CR itself mediates — field-level ownership makes the shared blackboard safe), Strategy (the aggregation rule is fixed policy, not an interchangeable algorithm), any speculative pattern without a requirement row.

## 5. Global constraints (per worker, verbatim discipline)

- **Obey `AGENTS.md`.** `make test-fast` after every Go/CRD change; `make test-smoke` additionally after operator-reconciliation, API/CRD, or SM integration changes (the settlement smoke is the manager's consolidated gate per the standing serialized-worker policy).
- **The design note is normative background** (`notes/component-reports-and-field-ownership.md`); this spec's decisions (§3) are the binding application.
- **Scope discipline:** exactly the slice's ownership partition; no unrelated cleanup; discovered gaps stop-and-ask.
- **Test integrity:** never weaken, delete, skip, or rewrite a test to conceal a failure; the e2e suite grows a spec, none is modified beyond what a slice names.
- **Field ownership is the contract:** the SM never writes `phase`/`message`; the operator never writes `scenarioManagerVerdict` — enforced per D9.
- **Secrets hygiene; verification honesty; runtime attestation** (`printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` at the first checkpoint, expected `ai.forge/qwen3.8-27b-nvfp4`, mismatch → stop and report failed) **; no commits; read-only specs.**
- **Serialization policy:** one dispatched worker at a time (standing provider constraint — the LAPI/CrowdSec incidents).

## 6. Slices (ordered; five-part task specs are authored per slice at its dispatch wave)

| Slice | Content | Dependency | Tier gate |
|---|---|---|---|
| **P1 — API surface** | `SimulationExperimentStatus.ScenarioAggregate *ScenarioManagerVerdictStatus` (verdict/finished/failed/total/observedGeneration, all `omitempty`-clean) in `api/alpha4/simulationexperiment_types.go` with the D9 ownership comments; regenerate `zz_generated.deepcopy.go` + `config/crd/bases/experiment.cbse.terministic.de_simulationexperiments.yaml` via the repo's `controller-gen` flow (`make verify-generated` byte-stable) | — | `make test-fast` |
| **P2 — SM aggregation pass** | New pass package (e.g. `internal/aggregate`) mirroring the scheduler-pass idiom: per admitted experiment (informer cache), SQL aggregation over `scenario_status`; D3 rule; D4 stamp; idempotent status patch via the SM's kube client; wiring in `internal/core/app.go`; RBAC delta (`stack.yaml` Role + `rbac.Verify` checks + tests); failure/no-op taxonomy; unit tests | P1 | `make test-fast` |
| **P3 — Operator derive** | Phase literals + the reconcile derive branch (D4 validation, generation gate, `patchPhase` once, D6 stickiness), the D7 Event on transition, envtest coverage: Finished derive, Failed derive (synthetic report), stickiness/no-regression, invalid report ignored, stale `observedGeneration` ignored, idempotent re-reconcile, no `scenarioManagerVerdict` writes by the operator | P1 | `make test-fast` |
| **P4 — e2e spec** | One new smoke spec after the convergence spec: all four scenarios `Finished` → `status.scenarioManagerVerdict` present and exact (verdict `Finished`, finished=4, failed=0, total=4, observedGeneration == live generation) → experiment `status.phase == "Finished"` (and no regression under a follow-up reconcile); diagnostics artifact; all other specs byte-identical | P2, P3 | `make test-fast` |

Wave plan: W1 = [P1]; W2 = [P2]; W3 = [P3]; W4 = [P4] — **each wave a single serialized worker** (the standing provider policy; P2 ∥ P3 would be ownership-disjoint but serialization stands). The manager re-runs every acceptance recipe before settlement; the consolidated settlement gate (`make test-fast` + `make test-smoke`, 7 e2e specs) runs after P4.

## 7. Verification gates

- Per-tier rule as in §5; P2's pass tests cover the absorbing-condition idempotence (re-report no-ops) and the empty-set guard; P3's envtest covers both verdicts and every rejection path; P4 proves the live chain.
- **Feature-level acceptance (settlement smoke):** on the cluster — scenarios converge `Finished` (existing spec) → the aggregate report appears → the experiment's phase is `Finished` and sticky. Artifacts under `artifacts/test/<run-id>/`.
- **Forbidden shortcuts:** prose-only evidence; weakening any e2e spec; letting either component write the other's field; skipping the attestation check.

## 8. Completion and handoff

State vocabulary per slice: `complete` / `incomplete` / `verification-blocked`, recorded with evidence in `IMPLEMENTATION_HANDOFF.md` after manager-verified settlement; every dispatch prompt embeds attestation + read order + no-commit + the global constraints; after P4 the handoff records the settlement smoke and this feature's `devlog/changes/README.md` row turns `implemented`.

## Open questions for the user (block slice authoring where marked)

- **Q1** — report shape: bare typed struct *(my recommendation — conflict-free disjoint patches, matches the codebase's minimalism)* vs. `metav1.Condition` list (standardized `kubectl describe` rendering; array-merge mechanics between writers).
- **Q2** — e2e scope: Finished-path live + Failed-branch envtest *(my recommendation)* vs. a full Failed e2e profile (a second experiment with unreachable precision — doubles the smoke's runtime).
- **Q3** — confirm the aggregation rule (D3): any `Failed` scenario → experiment `Failed`; zero scenarios → never aggregates (stays `InProgress`).
- **Q4** — the transition Event (D7): include *(my recommendation — one-line `kubectl describe` visibility, matches the note's garnish)* or skip.
