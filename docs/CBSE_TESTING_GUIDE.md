# Understanding CBSE and its test pipeline

This guide is the human-facing entry point for the current CBSE implementation and its Kubernetes test system. It is intentionally practical: it explains what works today, where the important code lives, and how to interpret a smoke-test run.

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
├── e2e/           Kustomize manifests and Go/Ginkgo smoke assertions
├── mocks/          EDS and translator mock source plus Dockerfiles
└── compat/eds-sm/ Deprecated fixtures retained only for compatibility
```

Component-local Go tests remain under `experiment-operator/test/` because that is the conventional Go package layout. They are still run by the root commands below.

## 4. Which command to run

| Command | Use it when | What it does not do |
| --- | --- | --- |
| `make test-fast` | Any Go, CRD, Dockerfile, manifest, or test-harness change | Does not contact K3s or build/push images |
| `make publish-test-images` | You need new private Docker Hub test images | Does not deploy to K3s |
| `make test-smoke` | Operator, Scenario Manager, mock, image, or Kubernetes integration change | Does not retain a namespace unless asked |
| `make test-diagnose RUN_ID=<id>` | A retained failure needs fresh diagnostics | Does not change workloads |
| `make test-clean RUN_ID=<id>` | `CBSE_KEEP_ON_FAILURE=1` retained a failed namespace | Does not delete the shared CRD or `cbse-test-system` |

The fast suite checks generated code, formatting, harness self-tests, vetting, Scenario Manager race tests, and the operator's `envtest` suite.

The smoke suite uses a pinned Kubernetes 1.32 `kubectl`, verifies the expected K3s API server and permissions, acquires a Kubernetes Lease, creates a unique `cbse-e2e-<run-id>` namespace, and deploys images by digest. It never deploys test resources to `default` or `kube-system`.

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
| `ImagePullBackOff` | `events.txt`, `pod-descriptions.txt` | digest/reference or the `dockerhub-auth` pull Secret |
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
