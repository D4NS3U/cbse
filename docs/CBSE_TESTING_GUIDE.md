# Understanding CBSE and its test pipeline

This guide is the human-facing entry point for the current CBSE implementation and its Kubernetes test system. It is intentionally practical: it explains what works today, where the important code lives, and how to interpret a smoke-test run.

> **Cluster requirements**: CBSE needs Kubernetes >= 1.30 with the `UserNamespacesSupport` feature gate enabled (for the reference Translator's rootless BuildKit sidecar) and experiment namespaces that permit the sidecar's unconfined seccomp/AppArmor profiles (Pod Security enforce `privileged`). See [`CLUSTER_REQUIREMENTS.md`](CLUSTER_REQUIREMENTS.md) for the exact enablement steps, prerequisites, and verification; it is the source a future Helm chart's prerequisites must reference.

## 1. What CBSE currently does

CBSE is a Kubernetes-native research prototype for preparing simulation experiments. The tested path is:

```text
SimulationExperiment (alpha3 custom resource)
                 |
                 v
Experiment Operator -----> Kubernetes workloads for the experiment
                 |
                 v
Scenario Manager <----> PostgreSQL project/scenario state
                 |
                 +----> NATS / JetStream <----> EDS mock
                 |
                 +----> NATS / JetStream <----> translator mock
```

In the smoke profile, the Experiment Operator creates the experiment's supporting Deployments, Services, Secrets, ConfigMaps, and design Pod. The Scenario Manager records one project and consumes deterministic EDS batches into four `scenario_status` rows. The EDS and translator are deliberately lightweight mocks; PostgreSQL and NATS/JetStream are real services.

### Implemented and validated

- The active `SimulationExperiment` API is `experiment.cbse.terministic.de/alpha3`.
- The operator provisions and removes experiment-owned Kubernetes resources.
- Scenario Manager watches experiment lifecycle changes, persists projects and scenarios in PostgreSQL, and handles EDS and translator messaging through NATS/JetStream.
- The smoke suite proves provisioning, persistence, idempotent reconciliation, and deletion cleanup together on K3s.

### Still deliberately incomplete

- The detail database, result database, and post-processing component use keep-alive mocks in the smoke profile. The experiment-design component uses the active deterministic EDS mock.
- The translator mock implements only the NATS/JetStream handshake and returns generated `trans.test:<number>` image names after a delay.
- Full simulation execution, replication scheduling, and production hardening are not yet the scope of the validated path.
- Scenario Manager is temporarily run as UID 0 in the smoke manifest because its image uses a symbolic user. Converting that image to a numeric non-root user is a follow-up hardening task.

## 2. Where to start reading

| Question | Start here | Then read |
| --- | --- | --- |
| What commands are available? | [`Makefile`](../Makefile) | [`AGENTS.md`](../AGENTS.md) |
| What is deployed in a smoke run? | [`test/e2e/manifests/base/stack.yaml`](../test/e2e/manifests/base/stack.yaml) | [`test/e2e/manifests/experiment.yaml`](../test/e2e/manifests/experiment.yaml) |
| What does the smoke suite prove? | [`test/e2e/smoke_test.go`](../test/e2e/smoke_test.go) | [`test/e2e/README.md`](../test/e2e/README.md) |
| How is cluster safety enforced? | [`test/harness/preflight.sh`](../test/harness/preflight.sh) | [`test/harness/smoke.sh`](../test/harness/smoke.sh) |
| How does the operator reconcile an experiment? | [`experiment-operator/internal/controller/simulationexperiment_controller.go`](../experiment-operator/internal/controller/simulationexperiment_controller.go) | [`experiment-operator/cmd/main.go`](../experiment-operator/cmd/main.go) |
| How does Scenario Manager start? | [`scenario-manager/internal/core/scenario_manager.go`](../scenario-manager/internal/core/scenario_manager.go) | [`scenario-manager/cmd/main.go`](../scenario-manager/cmd/main.go) |
| How does EDS ingestion work? | [`scenario-manager/internal/nats/eds_com.go`](../scenario-manager/internal/nats/eds_com.go) | [`test/mocks/eds/eds_mock.py`](../test/mocks/eds/eds_mock.py) |
| How does translation messaging work? | [`scenario-manager/internal/nats/trans_com.go`](../scenario-manager/internal/nats/trans_com.go) | [`test/mocks/translator/translator_mock.py`](../test/mocks/translator/translator_mock.py) |

## 3. Test directory map

All shared test infrastructure is contained below [`test/`](../test/):

```text
test/
├── harness/       Shell orchestration: preflight, lock, build, diagnose, cleanup
├── e2e/           Kustomize manifests and Go/Ginkgo smoke assertions, images.lock.env
├── mocks/          EDS mock source plus Dockerfile (alpha3 translator mock retained for the smoke profile)
└── compat/eds-sm/ Deprecated fixtures retained only for compatibility
```

The alpha4 reference components live under `component-templates/`:
`component-templates/translator/` (the reference Translator Go module, its
Dockerfile, and the `runner-base/` Python/SimPy/Psycopg base) and
`component-templates/scenario-detail-database/` (the reference Detail Database
image and initialization SQL). See those directories' README files for the
component contracts. The shared source-image lock is `test/e2e/images.lock.env`;
`test/harness/image-lock.sh` loads and validates it for both `build-images.sh`
and `preflight.sh`.

Component-local Go tests remain under `experiment-operator/test/` because that is the conventional Go package layout. They are still run by the root commands below.

## 4. Which command to run

| Command | Use it when | What it does not do |
| --- | --- | --- |
| `make test-fast` | Any Go, CRD, Dockerfile, manifest, or test-harness change | Does not contact K3s or build/push images |
| `make publish-test-images` | You need new University Harbor test images | Does not deploy to K3s |
| `make test-smoke` | Operator, Scenario Manager, mock, image, or Kubernetes integration change | Does not retain a namespace unless asked |
| `make test-diagnose RUN_ID=<id>` | A retained failure needs fresh diagnostics | Does not change workloads |
| `make test-clean RUN_ID=<id>` | `CBSE_KEEP_ON_FAILURE=1` retained a failed namespace | Does not delete the shared CRD or `cbse-test-system` |

Set `CBSE_KEEP_NAMESPACE=1` on `make test-smoke` to retain a successful run's namespace for manual inspection. Clean it with the ownership-checked `make test-clean RUN_ID=<id>` command when finished.

The fast suite checks generated code, formatting, harness self-tests, vetting
(operator, Scenario Manager, and the isolated Translator module), Scenario
Manager and Translator race tests, and the operator's `envtest` suite. The
Translator module is vetted and tested with `GOWORK=off` so its buildkit/docker
dependencies do not affect the operator or Scenario Manager workspace.

The smoke suite uses a pinned Kubernetes 1.32 `kubectl`, verifies the expected
K3s API server and permissions, requires Kubernetes 1.30 or newer with a
`linux/amd64` `Ready` schedulable Node, acquires a Kubernetes Lease, creates a
unique `cbse-e2e-<run-id>` namespace, and deploys images by digest. Test images
are published by `test/harness/build-images.sh` from the locked source images in
`test/e2e/images.lock.env`. The five shared components (`exop`, `sm`, `eds-mock`,
`translator`, `runner-base`) use the flat layout
(`${CBSE_REGISTRY}:<component>.test.<version>`, digest
`${CBSE_REGISTRY}@sha256:<hex>`); the reference Scenario Detail Database uses the
nested layout (`${CBSE_REGISTRY}/scenario-detail-database:<version>`, digest
`${CBSE_REGISTRY}/scenario-detail-database@sha256:<hex>`). Generated runner
images are published to `${CBSE_REGISTRY}/cbse-test-runner`. It never deploys
test resources to `default` or `kube-system`.

### Component build and image contract

All six component images use the **nested** repository layout
`${CBSE_REGISTRY}/<component>:<version>` (for example
`${CBSE_REGISTRY}/sm:26.9.16`); there is no flat `${CBSE_REGISTRY}:<component>.test.<version>`
form. The version tag is the build date in `YY.M.D` form (no leading zeros;
overridable via `TEST_IMAGE_VERSION`). The reference Scenario Detail Database uses
the same nested layout under `${CBSE_REGISTRY}/scenario-detail-database:<version>`.
Generated runner images are published to `${CBSE_REGISTRY}/cbse-test-runner`.
It never deploys test resources to `default` or `kube-system`.

The exact component tokens and their repository layouts are:

| Token | Layout | Canonical tag | Digest output |
| --- | --- | --- | --- |
| `exop`, `sm`, `eds-mock`, `translator`, `runner-base`, `scenario-detail-database` | nested | `${CBSE_REGISTRY}/<token>:<version>` | `${CBSE_REGISTRY}/<token>@sha256:<hex>` |

`TEST_IMAGE_VERSION` defaults to the build date (`$(date -u +%-y.%-m.%-d)`) when
unset, and uses a non-normalizing `YY.M.D` format validated against
`^[0-9]{2}\.[0-9]{1,2}\.[0-9]{1,2}$`; single-digit month/day are accepted
as-is and never rewritten (optional zero-padding is permitted by the regex, but
the harness does not normalize). `build-images.sh` records `DETAIL_DB_IMAGE` in
`images.env` as the immutable nested digest reference; with `SKIP_BUILD=1`,
`DETAIL_DB_IMAGE` must be supplied already as an immutable digest reference and a
mutable (floating-tag) value is rejected by preflight. All CR, manifest, and
smoke references use the immutable digest form, never a floating tag.
Generated-runner cleanup targets only `${CBSE_REGISTRY}/cbse-test-runner`; it must
never delete or prune the reference Detail Database repository
(`${CBSE_REGISTRY}/scenario-detail-database`) or any other shared reference
repository.

## 5. How to read a smoke-test result

Every run creates `artifacts/test/<run-id>/`. The first place to look is always `summary.json`:

```bash
RUN_ID=$(basename "$(ls -1dt artifacts/test/* | head -n 1)")
cat "artifacts/test/${RUN_ID}/summary.json"
```

Read artifacts in this order:

1. `summary.json` — run ID, namespace, project name, result, and timing.
2. `junit.xml` — exact test/spec outcome for CI or an IDE.
3. `preflight.txt` — selected context, API server, K3s version, and safety checks.
4. `images.env` — the exact digest-pinned images used by that run.
5. `database.txt` — captured scenario rows before the final deletion assertion. A successful smoke run should show four `Created` rows with deterministic seeds.
6. `simulationexperiments.yaml`, `stack.yaml`, and `experiment.yaml` — sanitized resources used in the run; Secrets are intentionally excluded.
7. `events.txt`, `cluster-state.txt`, and `pod-descriptions.txt` — the fastest way to locate scheduling, readiness, or image-pull failures.
8. `logs/` — component logs, including Scenario Manager, operator, NATS, PostgreSQL, and experiment-owned workload logs.

On a successful run, the final assertion deletes the experiment and verifies that its owned Kubernetes resources and PostgreSQL rows are gone. Therefore, use `database.txt` rather than querying the deleted namespace when reviewing the persisted rows.

## 6. Understanding a failure

Run the smoke suite with retention only while diagnosing a real failure:

```bash
CBSE_KEEP_ON_FAILURE=1 make test-smoke KUBECONFIG=/path/to/config
```

The command prints the run ID. Then inspect and clean it explicitly:

```bash
make test-diagnose RUN_ID=<run-id> KUBECONFIG=/path/to/config
make test-clean RUN_ID=<run-id> KUBECONFIG=/path/to/config
```

Common starting points:

| Symptom | First files to inspect | Likely layer |
| --- | --- | --- |
| Preflight failure | `preflight.txt` | kubeconfig, API server, permissions, registry authentication |
| `ImagePullBackOff` | `events.txt`, `pod-descriptions.txt` | digest/reference or the `cbse-registry-auth` pull Secret |
| `CrashLoopBackOff` | `pod-descriptions.txt`, affected `logs/*.log` | application startup, permissions, database, or NATS configuration |
| CR phase is `Error` | `simulationexperiments.yaml`, operator log | reconciliation or experiment specification |
| Database assertion fails | `database.txt`, Scenario Manager and EDS logs | NATS subject, JetStream processing, or persistence |
| Cleanup assertion fails | operator log, `cluster-state.txt` | owner references, finalizer, or Scenario Manager delete handling |

## 7. Suggested learning path

1. Read the root [`README.md`](../README.md) and this guide.
2. Read [`test/e2e/smoke_test.go`](../test/e2e/smoke_test.go) before changing integration behavior; it is the executable acceptance criterion.
3. Follow one test run from [`test/harness/smoke.sh`](../test/harness/smoke.sh) to the rendered manifests and then the Ginkgo assertions.
4. Study the operator reconciler to understand Kubernetes resource ownership and cleanup.
5. Study Scenario Manager's EDS and translator adapters to understand the message flow and persistence boundary.
6. When adding a real service to replace a mock, first update the smoke manifest and assertions, then retain the mock only where it still provides useful deterministic coverage.

## 8. Current operational baseline

The latest successful smoke run can be found by listing `artifacts/test/` by modification time. It passed all four full-stack assertions: resource provisioning, project/scenario persistence, idempotent reconciliation, and cleanup. The run artifacts are local diagnostics, are ignored by Git, and should never contain Secret payloads.
