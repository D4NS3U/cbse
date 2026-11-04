# FEATURE.md — The CBSE Helm control-plane installation (first sketch)

**Status: IMPLEMENTING — H1–H3 complete, independently approved, and landed on feature branch `D4NS3U/helm-control-plane-installation` (H3 merge `e465775`, 2026-11-04); merged-feature `test-fast` and W2 manifest smoke are green, and H4 is unblocked but not dispatched.** All rulings H1–H8 are closed and H1–H6 specifications/prompts are authored. v0.1 renders its own NATS/JetStream and PostgreSQL Core DB directly in the chart, alongside the Experiment Operator and Scenario Manager. External NATS and PostgreSQL are required future installation modes and their compatibility seams are specified here, but they do not ship as non-functional values in v0.1. See [IMPLEMENTATION_HANDOFF.md](IMPLEMENTATION_HANDOFF.md).

## 1. Title and mission

The **CBSE Helm control-plane installation** packages the smoke-proven shared stack as one installable release: the `SimulationExperiment` alpha4 CRD, Experiment Operator, Scenario Manager, NATS/JetStream, and the Scenario Manager's PostgreSQL Core DB. A cluster administrator installs one chart into a dedicated namespace; users then create `SimulationExperiment` resources in that same namespace, and the operator continues to provision the experiment-scoped EDS, Translator + BuildKit, PPS, Scenario Detail DB, Result DB, runner ServiceAccount, and runner Jobs exactly as it does today.

The first release is intentionally conservative: it is a **single-namespace, single-replica installation wrapper around contracts already proven by the smoke suite**, not a redesign of CBSE deployment or tenancy. v0.1 renders NATS and PostgreSQL directly rather than depending on third-party charts. Later releases add external NATS/Core DB support behind explicit, mutually exclusive configuration branches after the required runtime contracts are present.

## 2. Scope

### In scope for v0.1

- A chart at `charts/cbse/` containing:
  - the alpha4-only `SimulationExperiment` CRD;
  - namespaced RBAC and the Experiment Operator;
  - namespaced RBAC and the Scenario Manager;
  - a directly rendered, single-node NATS server with JetStream;
  - a directly rendered, single-node PostgreSQL Core DB;
  - Services, ConfigMaps, ServiceAccounts, Secrets/references, PVCs, probes, security contexts, resources, labels, and chart notes required by those components;
  - `values.yaml`, `values.schema.json`, `Chart.yaml`, `README.md`, and render/install tests.
- One chart release namespace is the complete v0.1 installation and experiment scope:
  - Operator `EXPERIMENT_OPERATOR_WATCH_NAMESPACE` = its Pod namespace;
  - Scenario Manager `SCENARIO_MANAGER_WATCH_NAMESPACE` = its Pod namespace;
  - `SimulationExperiment` objects for that installation live in the same namespace;
  - one release per namespace; the fixed internal Service name `sm-eds-nats` remains unchanged because the operator injects `nats://sm-eds-nats:4222` into experiment components.
- Persistent storage for the internal Core DB and JetStream, with an explicit development-only ephemeral option.
- A complete, documented Secret contract (see §3.1).
- Helm-aware smoke integration: the existing e2e suite proves the same 15-spec behavior against a chart-installed shared stack rather than a second test suite.
- Install, upgrade, rollback-safe configuration, and uninstall behavior, including the experiment-finalizer ordering constraint.
- Kubernetes >= 1.30 and the existing cluster prerequisites in `docs/CLUSTER_REQUIREMENTS.md`; the chart validates/documents them but never changes cluster feature gates.

### Future installation modes specified now, implemented later

- **External Core DB:** the chart omits its PostgreSQL StatefulSet/Service/PVC and points the Scenario Manager at an operator-supplied DSN plus an existing credentials Secret with the same runtime keys as the internal branch.
- **External NATS/JetStream:** the chart omits its NATS StatefulSet/Service/PVC and points all CBSE components at an external endpoint. The first compatible external mode may be authentication-free; TLS and authenticated NATS require a separate application-level contract because alpha4 currently rejects credential-bearing URLs and requires the legacy NATS user/password variables to be empty.
- Multiple experiment namespaces, namespace selectors, cluster-wide installations, HA NATS/PostgreSQL, external secret operators, automatic registry-secret distribution, ingress, and public OCI chart publication.

The future branches are architectural commitments, not v0.1 knobs: `values.schema.json` must not advertise values the current binaries cannot honor.

### Out of scope for v0.1

- Moving experiment-scoped components into Helm. The EDS, Translator, PPS, Detail DB, Result DB, runner ServiceAccount, and runner Jobs stay operator/Scenario-Manager owned.
- Changing the `SimulationExperiment` API or any NATS subject/payload/database schema.
- Multi-namespace watching or copying Secrets across namespaces.
- NATS authentication/TLS, PostgreSQL HA, NATS clustering, backup operators, ingress, cert-manager, or service-mesh integration.
- Automatically enabling `UserNamespacesSupport` or changing Pod Security admission policy.
- Embedding registry credentials, private registry defaults, cluster endpoints, storage-class names, or other operational infrastructure in the chart.
- Treating the smoke harness as production installation logic; the chart replaces only the shared-stack manifest application step.

### Compatibility and breaking boundaries

- Kubernetes **1.30 or later**, alpha4-only CRD, and the existing `UserNamespacesSupport` / experiment-namespace Pod Security requirements remain unchanged.
- No application API or wire breaking change is intended.
- v0.1 supports exactly one chart release in a namespace because fixed service/Secret contracts are namespace-local.
- The CRD is cluster-scoped and shared by every release. Helm installs it from `crds/`, does not delete it on uninstall, and does not silently migrate incompatible served/stored versions.
- Internal NATS and PostgreSQL are single-node in v0.1. Persistent does not mean highly available.

## 3. Workflow decisions (concern → decision)

| # | Concern | Decision for this feature |
|---|---|---|
| D1 | The chart boundary | The chart owns the **shared control plane only**: CRD, Operator, Scenario Manager, NATS/JetStream, and Core DB. The operator and SM keep ownership of every per-experiment resource. Helm labels/annotations identify only chart-owned objects; controller references identify experiment-owned objects. |
| D2 | v0.1 dependency strategy | **Ruled by the user:** render NATS and PostgreSQL directly. Do not begin with Bitnami/upstream subchart dependencies. Direct templates preserve parity with `test/e2e/manifests/base/stack.yaml`, keep the values surface bounded, and make the first smoke migration mechanically reviewable. |
| D3 | Namespace and tenancy | **Single namespace.** The release namespace is both the control-plane namespace and the only experiment namespace watched by that release. Namespaced Roles/RoleBindings are preferred wherever the current operations permit them. Multi-namespace/selector support is a later feature requiring binary configuration and an explicit Secret-distribution model. |
| D4 | Stable internal discovery | Preserve the runtime names that are code-level contracts: NATS Service `sm-eds-nats`; Core DB Service `core-db`; registry Secret `cbse-registry-auth`. Other resource names use chart helpers only where that cannot break injected endpoints or existing lookup logic. Arbitrary Helm release names must not alter these DNS names. Multiple releases in one namespace are unsupported and rejected/documented. |
| D5 | Storage | **Ruled:** one StatefulSet + one PVC each for PostgreSQL and NATS JetStream, persistence enabled by default. `persistence.enabled=false` is an explicit development-only branch using `emptyDir`, matching the smoke stack. Storage class is unset by default so the cluster's default provisioner decides. Both PVCs are retained on uninstall by default. The generated Core DB credential Secret is retained with the Core DB PVC so reinstallation can reconnect to the retained data. |
| D6 | Image identity | **Ruled:** every CBSE-owned image value is repository + immutable digest; third-party NATS/PostgreSQL references also render by digest in release values. No private registry/repository default enters the repository. `values.schema.json` rejects an image without a `sha256:` digest. The release workflow generates an installation-owner `values-images-<version>.yaml` containing the private repositories and resolved digests, so installation consumes one generated file rather than manual digest entry. The file is never committed. Image pull Secrets are references only. |
| D7 | Core DB credentials | **Ruled:** Helm manages the fixed-name `core-db-credentials` Secret exclusively. The chart generates it on first install and preserves/reuses its values across upgrades via Helm `lookup`; there is no user-supplied `existingSecret` branch and no database password in values. The Secret has exactly `SCENARIO_MANAGER_CORE_DB_USER` and `SCENARIO_MANAGER_CORE_DB_PASSWORD`, consumed by both PostgreSQL and the SM. It is retained together with the Core DB PVC. The Core DB DSN remains non-secret configuration. |
| D8 | Registry authentication | **Ruled:** exactly one `cbse-registry-auth` exists in the v0.1 release/experiment namespace and is owned exclusively by the installation owner. Its fixed name is part of the Translator/SM contract. It must exist before chart installation and be type `kubernetes.io/dockerconfigjson`; a pre-install validation fails clearly when it is absent or has the wrong type. Helm references it but never creates, adopts, mutates, rotates, or deletes it. No raw Docker config is accepted in values because Helm stores release values in-cluster. |
| D9 | NATS security in v0.1 | The current alpha4 contract is authentication-free: the SM fails startup if the URL contains user information or legacy NATS credentials are non-empty; Translator, EDS, and PPS also reject credential-bearing URLs. Therefore v0.1 NATS has **no credential Secret**. Its boundary is namespace-local ClusterIP networking plus an enabled-by-default NetworkPolicy. NATS authentication/TLS is a separate future application feature, not templating alone. |
| D10 | CRD lifecycle | Place the generated alpha4 CRD in `charts/cbse/crds/`. Installation establishes it before templates; uninstall preserves it. The README documents Helm's CRD upgrade limitation and the alpha2/alpha3 breaking-upgrade boundary. The chart never deletes a shared CRD or rewrites stored objects as a hook side effect. |
| D11 | Readiness and startup | Keep real probes: Operator `/readyz` and `/healthz`; PostgreSQL `pg_isready`; NATS readiness (prefer the monitor endpoint if it expresses JetStream readiness, otherwise the proven client-port probe); add a Scenario Manager readiness signal if one exists, or specify the minimum code change rather than declaring a process-running Pod ready. `helm install --wait` proves Kubernetes readiness; the chart smoke proves functional readiness. |
| D12 | Upgrade | v0.1 is single replica. Upgrades preserve Core DB/JetStream PVCs and generated credentials, render stable selectors, and avoid immutable-field churn. The acceptance path is install → reference experiment → chart upgrade → second reference experiment, with the first experiment's persisted state preserved. No zero-downtime claim. |
| D13 | Uninstall | **Ruled:** a read-only pre-delete guard Job runs while the SM is still alive. If any `SimulationExperiment` remains in the release namespace, it exits non-zero and blocks uninstall with the exact cleanup command; it never deletes experiments or user data. Users delete experiments first, wait for finalizers, then rerun uninstall. CRD and retained data follow the ruled retention policy. |
| D14 | Existing smoke suite | Add a shared-stack installer seam to the harness: `manifest` (existing path) or `helm`. The same Ginkgo suite, image set, ownership checks, diagnostics, and teardown assertions run in both modes. No weaker Helm-only suite and no claim based only on `helm template`. |
| D15 | External-service evolution | The internal and external branches are mutually exclusive Strategies. Future Core DB support can reuse the existing SM DSN + credentials contract. Future NATS support first needs endpoint configurability in the operator (today `translatorNATSURL` is fixed) and, for secure NATS, new credential/TLS inputs accepted by all four clients. Those prerequisites become their own implementation slices/features before external values become public. |

### 3.1 Secret inventory — every Secret CBSE requires or creates

| Secret | Scope / owner | Required when | Type and exact keys | Consumers | Chart behavior |
|---|---|---|---|---|---|
| `cbse-registry-auth` | Exactly one per v0.1 release/experiment namespace; installation-owner managed | Required by chart pre-installation and by the current experiment provisioning path, particularly Translator BuildKit push/pull and runner image verification; also used as an image pull Secret for private images | `kubernetes.io/dockerconfigjson`; standard `.dockerconfigjson` key | Operator validation and operator-created DB/Translator workloads; Translator BuildKit mount (`config.json`); SM registry/digest verification; runner Jobs; Operator/SM Pods when their images are private | **Existing only.** Helm validates the fixed name/type and references it, but never owns or changes it. Automatic namespace replication is future work. |
| `core-db-credentials` | Release namespace; Helm managed | Always for the internal Core DB | `Opaque`; `SCENARIO_MANAGER_CORE_DB_USER`, `SCENARIO_MANAGER_CORE_DB_PASSWORD` | Internal PostgreSQL container and Scenario Manager | Generated once by the chart with `lookup`, reused unchanged across upgrades, and retained with the Core DB PVC. No existing-Secret or password-in-values branch. Future external Core DB uses a separately designed installation-owner Secret. |
| `<experiment>-detaildb-sct` | Experiment namespace; generated and owner-referenced by the Experiment Operator | Every `SimulationExperiment` | `Opaque`; `host`, `port`, `dbname`, `user`, `password` | Operator readiness probe, Translator, reference EDS | **Not a Helm Secret.** Generated from `spec.detailDatabase`; garbage-collected with the experiment. |
| `<experiment>-resultdb-sct` | Experiment namespace; generated and owner-referenced by the Experiment Operator | Every `SimulationExperiment` | `Opaque`; `host`, `port`, `dbname`, `user`, `password` | Operator readiness probe, Translator/generated runner as applicable, PPS | **Not a Helm Secret.** Generated from `spec.resultDatabase`; garbage-collected with the experiment. |
| ServiceAccount projected tokens | Kubernetes-managed, per Pod | Kubernetes API clients | Projected token volume; no long-lived Secret object required on modern Kubernetes | Operator and SM Kubernetes clients | No chart-created token Secret. |
| NATS credentials | None in alpha4 | Never in v0.1 | None | — | Authentication-free by current runtime contract. Future secure NATS requires application changes before a Secret contract is defined. |
| TLS certificate Secrets | None in v0.1 | Not applicable: no ingress/webhook/TLS metrics endpoint in the initial chart | None | — | cert-manager scaffold Secrets (`webhook-server-cert`, `metrics-server-cert`) are not part of v0.1. |

**Current API security debt, recorded but not silently expanded into Helm:** `DatabaseSpec.user` and `DatabaseSpec.password` are currently literal fields in the `SimulationExperiment` CR. The operator copies them into the two generated endpoint Secrets. Moving those credentials to `SecretKeyRef`/`credentialsSecretRef` is a future alpha4-compatible-or-successor API feature; Helm cannot repair it without an API change.

**User-supplied runner Job-template Secrets:** `spec.runner.jobTemplate` may reference additional Secrets. They are application-specific and are not universally required by CBSE, so they are not chart-managed and do not appear in the mandatory inventory above.

## 4. Design patterns (Gang of Four)

| # | Concern | Pattern | Concrete application | Why the simpler composition is not enough |
|---|---|---|---|---|
| 1 | Internal vs future external NATS/Core DB | **Strategy** | Each dependency has one selected deployment strategy: `internal` renders workload/service/storage; future `external` renders only validated connection configuration and existing-Secret references. `values.schema.json` enforces exactly one branch. | Interleaving `if` fragments throughout unrelated templates makes invalid half-internal installations possible and obscures the support boundary. |
| 2 | Rendering a coherent shared stack from values | **Builder** | Named helper templates build stable names, common labels, image references, security contexts, and Secret references before component templates assemble Deployments/StatefulSets/Services. | The chart has coupled invariants—fixed DNS names, selectors, Secret keys, digests—that must be constructed consistently rather than repeated ad hoc. |
| 3 | One user-facing installation over several subsystems | **Facade** | `charts/cbse/` is the installation Facade over CRD, Operator, SM, NATS, and Core DB. The chart README and `NOTES.txt` expose one bounded install/upgrade/uninstall workflow. | Requiring users to apply `stack.yaml`, create credentials, patch ServiceAccounts, and order components manually is precisely the installation problem this feature solves. |
| 4 | Helm vs manifest installation in the same smoke suite | **Strategy** | A harness installer interface selects `manifest` or `helm`, while every subsequent e2e step consumes the same namespace, image set, CRs, and assertions. | Forking the suite would let the chart path drift and would weaken the evidence by testing different behavior. |
| 5 | Preventing unsafe uninstall | **Chain of Responsibility** | Pre-delete checks run in a fixed order: remaining experiments → finalizers/cleanup readiness → retention notice. The first unsafe condition stops uninstall with the exact recovery command. | A single opaque hook failure would not tell the administrator what must be cleaned while the SM is still alive. |

No additional GoF patterns are implied. Template helpers are not license to build a generic chart framework; each helper must serve one invariant named above.

## 5. Global constraints

- **Obey `AGENTS.md`.** Every chart, Kubernetes manifest, CRD-copy, or harness change requires `make test-fast`; chart installation/harness integration requires `make test-smoke` with explicit `KUBECONFIG` against the dedicated cluster.
- **No unsafe namespaces.** Tests never install into `default` or `kube-system`; the harness creates/removes its own `cbse-e2e-<run-id>` namespace. A documented production example uses a placeholder such as `cbse-system`, never an operational namespace from a developer cluster.
- **No private infrastructure in tracked defaults.** No private registry, API server, hostname, kubeconfig path, storage class, runner label, or credential path. All operational values come from the environment or explicit user values.
- **Immutable images.** Release and test values use digests. No floating CBSE tags, insecure registry, or TLS-verification bypass.
- **Secrets hygiene.** Never put decoded credentials, Docker configs, passwords, tokens, Secret payloads, or protected paths in values, logs, reports, test artifacts, or chart notes. Render tests assert Secret key names and references, never real payloads.
- **Stable runtime contracts.** Preserve `sm-eds-nats`, `core-db`, `cbse-registry-auth`, the Core DB env names, the six NATS subjects, the alpha4 CRD, and the namespace/project identity contract unless a separately ruled application feature changes them first.
- **Ownership.** Helm owns shared-stack objects. The Operator/SM own experiment-scoped objects. No object has competing Helm and controller ownership.
- **Idempotency.** `helm upgrade --install` reuses generated credentials and PVCs, keeps selectors stable, and renders the same semantic configuration from the same values.
- **Persistence honesty.** A single-node PVC is durable across Pod replacement, not HA and not a backup. The README states failure and recovery boundaries plainly.
- **Scope discipline.** No unrelated controller refactor, no third-party chart dependency, no speculative external-service values, and no attempt to solve the CR-embedded database-password debt inside a packaging slice.
- **Normative spec and verification honesty.** Workers read this file, `AGENTS.md`, and their slice first; contradictions are questions, not unilateral design choices. Mandatory but unavailable cluster verification is `verification-blocked`, never success.
- **`go work sync` remains banned.** If implementation touches Go for readiness/config seams, workspace maintenance uses `go mod download` in workspace mode.

## 6. Preliminary slices

Detailed task specs are authored only after H1–H8 close.

| Slice | Content | Dependency | Gate |
|---|---|---|---|
| **[H1 — chart skeleton and contract tests](slices/H1-chart-skeleton-contract-tests.md)** | `charts/cbse/` skeleton; Chart metadata; values + JSON schema; helper templates; static naming/image/label/Secret contracts; `helm lint`/`helm template` repository entry points; CRD copy integrity check | — | `make test-fast` + chart lint/render tests |
| **[H2 — Operator and Scenario Manager packaging](slices/H2-control-plane-workloads.md)** | ServiceAccounts, least-privilege namespaced RBAC, Deployments, probes, resources, security contexts, configuration, fixed watch namespace, image/Secret refs; SM HTTP readiness; numeric runtime user; explicit EDS/PPS pull Secrets | H1 | `make test-fast` + W2 consolidated smoke |
| **[H3 — direct NATS and Core DB](slices/H3-internal-nats-core-db.md)** | NATS and PostgreSQL StatefulSets/Services/PVCs, internal config, NetworkPolicies, Helm-generated fixed-name Core DB Secret, persistent + explicit ephemeral branches | H1 | `make test-fast` + W2 consolidated smoke |
| **[H4 — lifecycle safety](slices/H4-lifecycle-safety.md)** | Locked auxiliary images; generated private release values; registry/pre-delete hooks; upgrade invariants; credential/PVC retention; lifecycle cluster harness; CRs-first `clean.sh` | H2, H3 | install → restart → upgrade → guarded uninstall acceptance + smoke regression |
| **[H5 — smoke-harness Helm installer](slices/H5-helm-smoke-integration.md)** | Add `manifest|helm` shared-stack installer seam; run the existing 15-spec suite unchanged against Helm; safe diagnostics; retained-run and teardown behavior | H2–H4 | `make test-fast` + manifest smoke 15/15 + Helm smoke 15/15 |
| **[H6 — install/operator documentation](slices/H6-documentation.md)** | Canonical Helm guide; chart README/NOTES; Secret creation/reference guide; first experiment; upgrade/rollback/uninstall; persistence limitations; external-service roadmap; root/testing/requirements/wiki links | H5 | docs/link/chart-render checks |

Proposed wave plan after rulings: **W1 = H1**, **W2 = H2 ∥ H3** (ownership-disjoint after the chart contracts are pinned), **W3 = H4**, **W4 = H5**, **W5 = H6**, each implementation slice followed by an independent review lane. Settlement requires a consolidated Helm-installed smoke and an install/upgrade/uninstall acceptance record.

## 7. Verification gates

### Static/chart tier

- `helm lint charts/cbse`.
- `helm template` for at least:
  - persistent internal defaults;
  - explicit ephemeral development mode;
  - invalid/mutually exclusive values rejected by `values.schema.json`.
- Rendered-object assertions:
  - fixed Service/Secret names resolve;
  - selectors match Pod labels;
  - every CBSE image contains an immutable digest;
  - no Secret payload or private default appears;
  - CRD copy is byte-/semantic-locked to `experiment-operator/config/crd/bases/`;
  - namespace-scoped RBAC grants only the operations current code uses.

### Cluster install tier

- Install into a fresh harness-owned namespace with an existing `cbse-registry-auth` fixture.
- `helm install --wait` reaches Ready without manual patching.
- NATS JetStream and Core DB survive Pod deletion with persistence enabled.
- Helm-generated Core DB credentials are unchanged by `helm upgrade` and retained with the Core DB PVC.
- Upgrade the chart and create/run another experiment.
- Uninstall is blocked while an experiment exists; after CR deletion/finalizer completion, uninstall succeeds.
- No chart-owned workload, Role/Binding, Service, ConfigMap, or non-retained Secret remains; CRD and ruled retained data behavior match the documentation.

### Full CBSE tier

- The existing smoke suite runs against the Helm installer path: **15/15** with the same assertions and immutable image set.
- A retained interactive run can expose the reference EDS UI and complete Design → Save → Start → Finish against the chart-installed shared stack.

### Forbidden shortcuts

- Claiming success from `helm template` alone.
- Copying the smoke `stack.yaml` into a chart without persistence, Secret lifecycle, upgrade, and uninstall contracts.
- Shipping external-service values that no binary can consume.
- Putting passwords or Docker configs in `values.yaml`, `--set` examples, checked-in fixtures, or Helm notes.
- Weakening the existing smoke suite, skipping finalizer-aware teardown, or testing a different workflow for Helm.
- Installing third-party dependency charts and calling that direct rendering.

## 8. Completion and handoff

Per-slice state is `complete` / `incomplete` / `verification-blocked`, recorded in `IMPLEMENTATION_HANDOFF.md` with:

- implementation commit and orchestration Task/Dispatch IDs;
- changed-file inventory and the numbered requirements satisfied;
- `helm lint` / render / `make test-fast` receipts;
- cluster install/upgrade/uninstall and smoke run IDs where required;
- independent reviewer verdict tied to the exact commit;
- unresolved rulings or compatibility blockers.

The chart is not feature-complete until the Helm-installed shared stack passes the existing full smoke suite and the lifecycle acceptance. Publishing a chart artifact is a later release decision; merge approval remains one user-owned settlement event.

## 9. Rulings record (open)

- ~~**H1 — dependency strategy**~~ **Answered (2026-10-22, user):** v0.1 renders NATS and PostgreSQL directly. External services are a required later mode and must be anticipated in the design.
- ~~**H2 — persistence and uninstall retention**~~ **Answered (2026-10-22, user accepted recommendation):** persistence enabled by default; Core DB and JetStream PVCs retained on uninstall by default; Helm-generated `core-db-credentials` retained with the Core DB PVC so reinstallation can reconnect.
- ~~**H3 — v0.1 namespace model**~~ **Answered (2026-10-22, user accepted recommendation):** one release namespace = one watched experiment namespace. Multi-namespace support is deferred.
- ~~**H4 — Secret ownership and installation prerequisite**~~ **Answered (2026-10-22, user clarification):** exactly one fixed-name `cbse-registry-auth`, owned only by the installation owner; Helm validates it before installation and never owns it. `core-db-credentials` is the opposite: Helm-managed only, generated by the chart, stable across upgrades, retained with the Core DB PVC, and not configurable as an existing Secret.
- ~~**H5 — uninstall safety mechanism**~~ **Answered (2026-10-22, user):** accepted. A read-only pre-delete hook Job lists `SimulationExperiment` resources before Helm removes the SM. Remaining experiments block uninstall; the administrator deletes them normally, waits for SM finalizer cleanup, and reruns uninstall. The guard never deletes experiments or user data.
- ~~**H6 — image-reference and chart-distribution model**~~ **Answered (2026-10-22, user):** retain immutable digests. The public repository contains chart source but no private registry defaults; publishing a packaged chart is optional. The release process generates a private, installation-owner `values-images-<version>.yaml` with repositories and resolved digests, and installation consumes that one file rather than requiring manual digest entry.
- ~~**H7 — NATS/Core DB resource naming**~~ **Answered (2026-10-22, user accepted recommendation):** preserve fixed compatibility names (`sm-eds-nats`, `core-db`, `core-db-credentials`); one release per namespace is accepted for v0.1.
- ~~**H8 — external NATS security horizon**~~ **Answered (2026-10-22, user accepted recommendation):** external Core DB may arrive first; defer all external NATS support until secure transport/authentication is designed and accepted by all clients.

## Appendix A — proposed chart surface (illustrative, not yet ruled)

```yaml
global:
  imagePullSecrets:
    - name: cbse-registry-auth

operator:
  image:
    repository: ""
    digest: "sha256:<required>"
  resources: {}

scenarioManager:
  image:
    repository: ""
    digest: "sha256:<required>"
  runnerStartWorkers: 4
  resources: {}

nats:
  mode: internal
  image:
    repository: nats
    digest: "sha256:<required>"
  persistence:
    enabled: true
    size: 2Gi
    storageClass: ""
    retain: true
  resources: {}

coreDatabase:
  mode: internal
  image:
    repository: postgres
    digest: "sha256:<required>"
  # The chart generates and retains core-db-credentials; no password value.
  persistence:
    enabled: true
    size: 5Gi
    storageClass: ""
    retain: true
  resources: {}

networkPolicies:
  enabled: true
```

Future-only conceptual branch (not included in v0.1 schema until supported):

```yaml
nats:
  mode: external
  external:
    url: nats://nats.example.invalid:4222
    # TLS/auth Secret shape is deliberately not invented before client support.

coreDatabase:
  mode: external
  external:
    dsn: postgres://db.example.invalid:5432/scenarios?sslmode=require
    existingSecret: cbse-external-core-db
    # Secret keys remain SCENARIO_MANAGER_CORE_DB_USER/PASSWORD.
```
