# Slice H2 — Experiment Operator and Scenario Manager chart workloads

Normative and self-contained. Read order: [../FEATURE.md](../FEATURE.md) §2, §3 D1/D3/D4/D6/D8/D11/D12, §4, §5, §6 H2, the closed rulings; [H1](H1-chart-skeleton-contract-tests.md) and its landed chart contract; root `AGENTS.md`; then this slice. Specifications are read-only. Missing values/helpers are contradictions—ask; do not modify H1-owned contracts.

This is one half of parallel wave W2. H3 owns NATS/Core DB/storage/networking templates. Work occurs in a dedicated child worktree/branch; no shared uncommitted tree is assumed.

## Target

Sole ownership of:

- `charts/cbse/templates/operator/` — ServiceAccount, Role, RoleBinding, Deployment
- `charts/cbse/templates/scenario-manager/` — ServiceAccount, Role, RoleBinding, ConfigMap, Deployment
- `test/helm/assert-control-plane.sh`
- Scenario Manager readiness implementation and tests under:
  - `scenario-manager/cmd/main.go`
  - `scenario-manager/internal/core/app.go` and its tests
  - new `scenario-manager/internal/health/`
  - `scenario-manager/Dockerfile`
- Experiment Operator pull-secret correction and tests in `experiment-operator/internal/controller/` only

No edits to H1 chart contracts (`values.yaml`, schema, helpers, Chart.yaml, CRD, shared tests), H3 templates, harness smoke scripts, image lock, docs, or `devlog/**`.

## Normative control-plane resources

### Experiment Operator

- Resource names exactly `experiment-operator`.
- ServiceAccount carries `imagePullSecrets: [{name: cbse-registry-auth}]`.
- Namespaced Role/RoleBinding, adapted from the smoke stack, permits only the current controller operations in the release namespace:
  - core: ConfigMaps, Secrets, ServiceAccounts, Services, Pods; matching finalizer subresources; Events create/patch;
  - apps: Deployments;
  - alpha4: SimulationExperiments, finalizers, status.
- Deployment:
  - one replica; stable selector `app: experiment-operator` plus H1 common labels;
  - image from `operator.image` through `cbse.image`; pull policy from global values;
  - `--health-probe-bind-address=:8081`;
  - `EXPERIMENT_OPERATOR_WATCH_NAMESPACE` from `metadata.namespace`;
  - `/readyz` and `/healthz` probes exactly on named port `health`, preserving proven timings unless tests justify a bounded adjustment;
  - ServiceAccount, resource values, read-only root filesystem, drop ALL capabilities, no privilege escalation, RuntimeDefault seccomp, non-root.
- No leader election in v0.1 (one replica); no Service/metrics/webhook/cert-manager objects.

### Scenario Manager

- Resource names exactly `scenario-manager`.
- ServiceAccount carries `imagePullSecrets: [{name: cbse-registry-auth}]`.
- **Namespaced Role/RoleBinding**, not ClusterRole, because v0.1 watches one namespace:
  - alpha4 SimulationExperiments get/list/watch/patch and status patch;
  - batch Jobs create/delete/get/list/watch;
  - named Secret `cbse-registry-auth` get only;
  - ServiceAccounts get.
- ConfigMap `scenario-manager-config`:
  - `SCENARIO_MANAGER_CORE_DB_DSN=postgres://core-db:5432/<coreDatabase.database>?sslmode=disable` (no credentials);
  - table names `scenario_status` and `project`;
  - `SCENARIO_MANAGER_NATS_URL=nats://sm-eds-nats:4222`;
  - legacy user/password explicitly empty;
  - `SCENARIO_MANAGER_RUNNER_START_WORKERS` from values;
  - `SCENARIO_MANAGER_HEALTH_ADDRESS=:<scenarioManager.healthPort>`.
- Deployment:
  - one replica and stable selector;
  - image from values; Secret + ConfigMap env injection;
  - `SCENARIO_MANAGER_WATCH_NAMESPACE` from Pod namespace;
  - health port and HTTP `/healthz` liveness + `/readyz` readiness probes;
  - ServiceAccount, values resources, RuntimeDefault, read-only root, drop ALL, no privilege escalation;
  - `runAsNonRoot: true`, numeric UID/GID 1000. The smoke stack's root exception must not enter the chart.

## Scenario Manager health contract

The live SM has no health endpoint; log polling is not a production readiness contract. Add a minimal explicit-dependency health server:

- New `internal/health.Server` constructed in `cmd/main.go` with address from `SCENARIO_MANAGER_HEALTH_ADDRESS` (default `:8080`; malformed address/start failure is fatal).
- `GET /healthz`: HTTP 200 while the process/server is alive; no dependency probing on every request.
- `GET /readyz`: HTTP 503 until all Core DB schema, Kubernetes authorization, NATS/JetStream reconciliation, informer, consumers, loops, and schedulers have started; HTTP 200 only at the same point immediately before/with the existing `Scenario Manager is ready` log.
- `core.RunScenarioManager` receives the readiness reporter explicitly (no package-global mutable state), calls `Ready()` at that point, and calls `NotReady()` before shutdown joins.
- Server shutdown is joined with the root context and bounded; no goroutine leak.
- Handler responses contain only `ok` / `not ready`, never configuration or credentials.
- Unit tests cover initial 503, transition to 200, liveness, method behavior, and shutdown. Existing startup-order tests pin the ready transition after the last component start.

## Scenario Manager image correction

Replace the symbolic-user/root-exception path:

- Alpine runtime creates group/user at numeric 1000 and declares `USER 1000:1000`.
- Chart security context matches 1000.
- Existing runtime behavior and entrypoint stay unchanged.
- The smoke manifest root exception is removed later by H5; H2 changes only the image and chart.

## Operator-created workload pull-secret correction

The chart requires one private-registry Secret, but current EDS/PPS Pods rely on the namespace default ServiceAccount patch. Make controller ownership self-contained:

- `reconcilePPS` and `reconcileEDS` set Pod `imagePullSecrets` to exactly `[{name: cbse-registry-auth}]`, matching the image-form DB and Translator discipline.
- Update envtests that currently expect none.
- No default-ServiceAccount mutation and no API change.
- This is solely kubelet image-pull authorization; it does not mount Docker credentials into EDS/PPS containers.

## Change

1. Render Operator resources exactly above.
2. Render SM resources exactly above.
3. Add the SM health server/explicit readiness dependency and numeric runtime user.
4. Add EDS/PPS explicit image pull Secrets.
5. Add `assert-control-plane.sh` using H1 helpers to assert names, namespace scope, RBAC verbs/resourceNames, images as `repository@digest`, probes, env values, Secret references, selectors, security contexts, and absence of ClusterRole/ClusterRoleBinding/root UID.
6. Add targeted Go/envtest/Dockerfile tests without weakening existing tests.

## Constraints

- No CRD/API change and no subject/database schema change.
- No chart value/helper/schema edits. No release-prefixed fixed names.
- Operator and SM remain single replica. Do not introduce leader-election or HA values.
- The health endpoint is unauthenticated and Pod-local (no Service exposes it); it carries no sensitive data.
- No use of the namespace default ServiceAccount for image-pull policy.
- `make test-fast` is mandatory for Go/Dockerfile/controller changes. Because this changes reconciliation and images, full smoke is a W2 settlement gate after H2+H3 land; report local mandatory smoke as wave-owned, not silently passed.
- Child-branch commits only with orchestration trailers. FEATURE/H1/slice files are read-only.

## Ownership

Own only Target paths. H3 may add `test/helm/assert-data-plane.sh` concurrently. H1's dispatcher automatically runs both. Any required shared-contract amendment is escalated to the manager.

## Observable acceptance

1. Runtime attestation → `ai.forge/qwen3.8-27b-nvfp4`.
2. Containment: only Target paths changed.
3. `cd scenario-manager && go test -race ./internal/health ./internal/core/... && go vet ./...` → rc=0.
4. `cd experiment-operator && go test ./internal/controller/...` with envtest assets → rc=0; named cases prove EDS/PPS pull Secrets.
5. Dockerfile receipt: numeric `USER 1000:1000`; no root chart security context.
6. `make test-helm-chart` → rc=0; `assert-control-plane.sh` proves all normative resources.
7. `make test-fast` → rc=0.
8. API/generated receipt: no diff under `experiment-operator/api` or generated CRD.
9. Test-name receipts for health 503→200/shutdown, ready-after-start ordering, namespaced RBAC, and EDS/PPS pull Secrets.
10. Commit HEAD/trailers and explicit report line: W2 consolidated smoke remains the manager's post-wave gate.

Completion uses one `worker_done` with explicit outcome, lifecycle IDs, attestation, evidence 1–10, files modified, and report path; check coordinator messages at natural checkpoints and immediately before completion.
