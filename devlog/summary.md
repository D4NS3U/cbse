# CBSE Codebase Summary

Reviewed on: 2026-05-15

This document summarizes what is currently present in the CBSE repository, how the major pieces fit together, what appears implemented, what appears scaffolded or incomplete, and what I would prioritize next.

## Executive Summary

The repository is a Go workspace for a Kubernetes-native, container-based simulation environment. It currently contains two Go modules plus a Kubernetes test environment:

- `experiment-operator`: a Kubebuilder/controller-runtime operator that defines and reconciles the `SimulationExperiment` custom resource.
- `scenario-manager`: a separate runtime service that watches `SimulationExperiment` resources, stores project/scenario state in PostgreSQL, and accepts Experimental Design Service scenario batches through NATS JetStream.
- `test/`: shared test infrastructure. The current smoke suite is in `test/e2e`, reusable harness scripts are in `test/harness`, mocks are in `test/mocks`, and the former EDS integration fixtures are retained under `test/compat/eds-sm`.

The top-level `README.md` presents the repository as an early research prototype and says the operator is the only implemented piece. The codebase has moved beyond that description: the Scenario Manager now has real connectivity, persistence, informer, and EDS ingestion code. That said, functional simulation execution is still not present. The system can provision experiment-adjacent Kubernetes resources and ingest scenario metadata, but it does not yet schedule, execute, monitor, or complete simulation replications.

The most complete path today is:

1. Install/apply the `SimulationExperiment` CRD.
2. Run the Scenario Manager with PostgreSQL and NATS JetStream.
3. Create a `SimulationExperiment`.
4. Scenario Manager observes the CR and persists a project row.
5. EDS mock announces available scenarios over NATS request/reply.
6. Scenario Manager returns a project-specific JetStream subject.
7. EDS mock publishes scenario batches.
8. Scenario Manager consumes batches and inserts `scenario_status` rows.

The operator path is also implemented, but at a provisioning/lifecycle level:

1. It accepts `SimulationExperiment` resources under `experiment.cbse.terministic.de/alpha2`.
2. It moves status through `Pending`, `Provisioning`, and `InProgress`.
3. It creates owned Kubernetes resources for databases, translator, post-processing, and experimental design service components.
4. It checks readiness for those components.

## Repository Layout

Top-level files and directories:

- `README.md`: project overview, research status, publication list, Apache 2.0 license note.
- `LICENSE`: Apache License 2.0.
- `go.work`: workspace tying together `experiment-operator` and `scenario-manager`.
- `go.work.sum`: workspace dependency checksum file.
- `cbse.code-workspace`: editor workspace file.
- `experiment-operator/`: Kubernetes operator module.
- `scenario-manager/`: Scenario Manager service module.
- `test/`: shared testing infrastructure and compatibility fixtures.
- `.DS_Store`: currently untracked local filesystem artifact. I did not modify it.

## Go Workspace

The root `go.work` declares:

- Go version: `1.24.0`
- Workspace modules:
  - `./experiment-operator`
  - `./scenario-manager`

This matters because `scenario-manager` depends on `experiment-operator` for the generated `SimulationExperiment` API types. Its `go.mod` uses a local replace directive:

```text
replace github.com/D4NS3U/cbse/experiment-operator => ../experiment-operator
```

The Scenario Manager Dockerfile also builds from the monorepo root because of this local module dependency.

## Experiment Operator

Module path:

- `github.com/D4NS3U/cbse/experiment-operator`

Main technologies:

- Go 1.24
- Kubebuilder
- controller-runtime v0.21.0
- Kubernetes libraries v0.33.0
- Ginkgo/Gomega for tests
- Kustomize/controller-gen/tooling via the generated Makefile

### Operator Purpose

The operator owns the `SimulationExperiment` CRD and reconciles a simulation experiment into a set of Kubernetes resources. Its current responsibility is resource provisioning and status progression. It does not run simulation logic itself.

The main entry point is:

- `experiment-operator/cmd/main.go`

The reconciler is:

- `experiment-operator/internal/controller/simulationexperiment_controller.go`

The API type is:

- `experiment-operator/api/alpha2/simulationexperiment_types.go`

### SimulationExperiment API

The currently generated CRD serves only:

- API group: `experiment.cbse.terministic.de`
- Version: `alpha2`
- Kind: `SimulationExperiment`
- Resource: `simulationexperiments`
- Short name: `simexp`
- Scope: namespaced
- Status subresource: enabled

The `alpha2` spec contains these top-level sections:

- `detailDatabase`
- `resultDatabase`
- `translator`
- `postProcessingService`
- `experimentalDesignService`
- `defaultServiceType`

The status contains:

- `phase`
- `message`
- `metrics.scenarioCount`

Allowed status phases are:

- `Pending`
- `Provisioning`
- `InProgress`
- `Completed`
- `Failed`
- `Error`

Today, the operator itself only advances resources into `InProgress` or `Error`. It does not appear to set `Completed` or `Failed`; those are reserved for later workflow/execution behavior or external actors.

### Database Spec

`DatabaseSpec` supports either an image-based database or a host-based database:

- `image`
- `host`
- `dbname`
- `user`
- `password`
- `serviceType`
- `nodePort`
- `port`
- `command`
- `args`

The code comments say either `Image` or `Host` must be specified and that the controller enforces this. The current controller does not fully enforce that invariant. If neither is set, it will attempt to create a Deployment with an empty image. If `host` is set, it smoke-tests TCP connectivity and returns early.

Important current behavior: for host-based DBs, `reconcileDatabase` returns after the TCP check and does not create the connection Secret that image-based DBs get. If downstream components are supposed to consume DB connection Secrets uniformly, that is a gap.

### Translator Spec

`TranslatorSpec` includes:

- `image`
- `repository`
- `baseimage`
- `serviceType`
- `nodePort`
- `port`
- `command`
- `args`

The controller creates:

- ConfigMap named `<experiment>-translator-cfg`
- Deployment named `<experiment>-translator`
- Service named `<experiment>-translator-svc`

The ConfigMap stores:

- `REPOSITORY`
- `BASEIMAGE`

The Deployment exposes these ConfigMap values as environment variables.

### Post-Processing Spec

`PostProcessingSpec` includes:

- `image`
- `serviceType`
- `nodePort`
- `port`
- `command`
- `args`

The controller creates:

- Deployment named `<experiment>-postproc`
- Service named `<experiment>-postproc-svc`

### Experimental Design Service Spec

`ExperimentalDesignServiceSpec` includes:

- `design`
- `image`
- `command`
- `args`
- `serviceType`
- `nodePort`
- `port`

The controller currently creates:

- ConfigMap named `<experiment>-design` when `design` is provided.
- Pod named `<experiment>-design`.
- Service named `<experiment>-design-svc`.

This differs from the other components because it creates a raw Pod rather than a Deployment. That can be okay for one-shot behavior, but it has operational consequences:

- Pod specs are mostly immutable after creation.
- Updates to image/command/args may fail or require delete/recreate handling.
- There is no Deployment controller to restart the Pod.
- The operator's `Owns(...)` declarations do not include Pods, although the code does register a field index for Pods and sets the controller reference.

The CRD makes `experimentalDesignService` required as an object, but the fields inside it are mostly optional. The controller creates a Pod even when `image` is empty, so additional validation or conditional creation would make this safer.

### Operator Reconciliation Flow

The reconciler does this:

1. Fetches the `SimulationExperiment`.
2. Adds a finalizer named `experiment.cbse.terministic.de/finalizer`.
3. On deletion, removes the finalizer and lets Kubernetes garbage collection handle owned resources.
4. If status phase is empty, patches it to:
   - phase: `Pending`
   - message: `Waiting to provision dependencies`
5. If phase is `Pending`, patches it to:
   - phase: `Provisioning`
   - message: `Provisioning components`
6. Reconciles:
   - detail database
   - result database
   - translator
   - post-processing service
   - experimental design service
7. If phase is `Provisioning`, checks readiness.
8. Once all checks pass, patches status to:
   - phase: `InProgress`
   - message: `All components provisioned; validating spec`

The reconciliation loop is intentionally sequential and simple. Comments acknowledge that production-grade operator behavior would likely use controller decomposition.

### Operator Readiness Logic

`allComponentsReady` checks:

- Deployments for:
  - `<experiment>-detaildb`
  - `<experiment>-resultdb`
  - `<experiment>-translator`
  - `<experiment>-postproc`
- ConfigMaps for:
  - `<experiment>-translator`
  - `<experiment>-design` when a design is configured
- Pod readiness for:
  - `<experiment>-design` when an experimental design service image is specified

For host-based databases, Deployment readiness checks are skipped.

### Operator-Owned Kubernetes Resources

The controller creates or updates:

- Deployments
- Services
- Secrets
- ConfigMaps
- Pods

RBAC markers grant permissions for:

- `apps/deployments`
- core `secrets`
- core `services`
- core `configmaps`
- `simulationexperiments`
- `simulationexperiments/status`
- `simulationexperiments/finalizers`

There is an implementation mismatch worth noting: the controller creates Pods for the experimental design service, but the RBAC markers shown in the reconciler do not explicitly grant Pod create/update/delete permissions. Generated RBAC may therefore be incomplete for that path unless another manifest grants it.

### Operator Runtime

`cmd/main.go` is mostly Kubebuilder scaffold plus the `SimulationExperimentReconciler`. It supports:

- metrics endpoint configuration
- secure metrics by default
- health and readiness probes
- leader election option
- HTTP/2 disable-by-default TLS hardening
- optional cert watchers for metrics and webhook certs
- webhook server construction

However, actual webhook setup is commented out.

### Operator Config And Deployment Assets

The operator has a standard Kubebuilder config tree:

- `config/crd`: generated CRD and kustomization.
- `config/rbac`: service account, roles, role bindings, generated resource roles.
- `config/manager`: manager Deployment.
- `config/default`: default deployment kustomization and patches.
- `config/certmanager`: cert-manager certificate/issuer resources.
- `config/prometheus`: ServiceMonitor and TLS patch.
- `config/network-policy`: network policies for metrics/webhook traffic.
- `config/samples`: sample `SimulationExperiment` resources.

The generated `Makefile` supports:

- `make manifests`
- `make generate`
- `make fmt`
- `make vet`
- `make test`
- `make test-e2e`
- `make docker-build`
- `make docker-push`
- `make docker-buildx`
- `make install`
- `make uninstall`
- `make deploy`
- `make undeploy`
- `make build-installer`

### Operator Dockerfile

`experiment-operator/Dockerfile` uses a two-stage build:

- Builder: `golang:1.24`
- Runtime: `gcr.io/distroless/static:nonroot`
- Binary: `/manager`
- User: `65532:65532`

This is a typical secure/minimal controller image pattern.

### Operator Tests

There are three test areas:

- `internal/controller/*_test.go`: envtest-based controller tests.
- `test/e2e/*`: Kind/kubectl based e2e tests.
- `test/utils/utils.go`: test helpers for kubectl, Kind image loading, cert-manager, etc.

Current test quality is mixed:

- The controller test is still mostly scaffolded and only asserts that reconciliation does not error for a very sparse CR.
- The e2e suite is also Kubebuilder-derived and checks manager startup, metrics, cert-manager certificate existence, and CA injection for a conversion webhook.
- The e2e suite does not yet validate the actual experiment provisioning behavior.

### Operator Gaps And Risks

Key current gaps:

- No actual simulation execution is implemented.
- No replication scheduling/execution lifecycle exists.
- `Completed` and `Failed` phases are defined but not meaningfully driven by the operator.
- `defaultServiceType` is defined but does not appear to be used as a fallback by the controller.
- The controller comment says it enforces `Image` or `Host` for databases, but the code does not fully enforce that.
- Host-based DBs do not get connection Secrets.
- Experimental design service uses a raw Pod, which makes update semantics awkward.
- Pod RBAC may be missing for the experimental design Pod path.
- Webhook scaffolding exists, but actual webhook setup is commented out.
- The generated `PROJECT` file still references an `alpha1` API path, but `experiment-operator/api/alpha1` is not present.
- `config/samples` includes `alpha1` sample resources, but the generated CRD currently serves only `alpha2`.
- The e2e tests expect conversion webhook behavior, but the webhook is not implemented/enabled in the running manager.

## Scenario Manager

Module path:

- `github.com/D4NS3U/cbse/scenario-manager`

Main technologies:

- Go 1.24
- controller-runtime/client-go for Kubernetes access and informers
- PostgreSQL through `database/sql` and `github.com/jackc/pgx/v5/stdlib`
- NATS through `github.com/nats-io/nats.go`
- The `SimulationExperiment` API types imported from `experiment-operator`

### Scenario Manager Purpose

The Scenario Manager is the bridge between Kubernetes experiment resources, the Core DB, and the Experimental Design Service. It currently:

- Validates startup dependencies.
- Connects to the Kubernetes API from inside the cluster.
- Watches `SimulationExperiment` resources.
- Persists project metadata to PostgreSQL.
- Connects to NATS.
- Sets up NATS request/reply and JetStream subscriptions for EDS scenario intake.
- Converts EDS scenario payloads into rows in `scenario_status`.

It does not yet:

- Schedule scenarios onto workers.
- Build or run simulation containers.
- Track live execution.
- Update computed replications.
- Calculate completion criteria.
- Feed status back into the `SimulationExperiment` CR.

### Scenario Manager Startup

Entrypoint:

- `scenario-manager/cmd/main.go`

Startup sequence:

1. Create a root context cancelled by `SIGINT` or `SIGTERM`.
2. Run `core.CheckingDependencies(rootCtx)`.
3. Run `core.RunScenarioManager(rootCtx)`.

`CheckingDependencies` currently validates:

- Kubernetes in-cluster client.
- NATS connectivity.
- Core DB connectivity.
- Core DB schema/table availability.

`RunScenarioManager` then starts:

- The `SimulationExperiment` informer.
- EDS communication over NATS/JetStream.

The service then blocks on context cancellation.

### Kubernetes Connectivity

Kubernetes helpers live in:

- `scenario-manager/internal/kube/kubeconnect.go`
- `scenario-manager/internal/kube/simulationexperiments.go`

The client is built from `rest.InClusterConfig()`, registers core Kubernetes types and the `alpha2` `SimulationExperiment` scheme, then stores a shared controller-runtime client.

Important issue: `KubeConnect` calls `log.Fatalf` if `rest.InClusterConfig()` fails. That exits the process immediately. The surrounding code in `CheckingDependencies` appears to have a local-development branch for "no in-cluster configuration was detected", but that branch is unreachable because `log.Fatalf` exits before `KubeConnect` can return `false`.

This also affects the `kubeconnect_test.go` integration test, which appears intended to skip outside a cluster but would terminate before skipping.

### SimulationExperiment Informer

Informer code lives in:

- `scenario-manager/internal/core/simexp_informer.go`

It starts a cluster-wide controller-runtime cache for `SimulationExperiment` objects and registers handlers for:

- Add events.
- Update events.
- Delete events.

Events emitted to the callback include:

- `SimulationExperimentCreated`
- `SimulationExperimentDeleted`
- `SimulationExperimentInProgress`
- `SimulationExperimentCompleted`
- `SimulationExperimentFailed`
- `SimulationExperimentErrored`

The informer persists experiment/project state into the Core DB:

- On add: creates a project row.
- On update: updates changed project fields, currently component count and status.
- On delete: deletes the project row.

Component count is derived by checking whether each spec section appears configured:

- detail database
- result database
- translator
- post-processing service
- experimental design service

### Core DB

Core DB code lives in:

- `scenario-manager/internal/coredb/coredbconnect.go`
- `scenario-manager/internal/coredb/schema.go`
- `scenario-manager/internal/coredb/project.go`
- `scenario-manager/internal/coredb/scenario_status.go`

Connection environment variables:

- `SCENARIO_MANAGER_CORE_DB_DSN`
- `SCENARIO_MANAGER_CORE_DB_USER`
- `SCENARIO_MANAGER_CORE_DB_PASSWORD`

Table override environment variables:

- `SCENARIO_MANAGER_CORE_DB_SCENARIO_STATUS_TABLE`
- `SCENARIO_MANAGER_CORE_DB_PROJECT_TABLE`

Default tables:

- `project`
- `scenario_status`

The code creates schema automatically with `CREATE TABLE IF NOT EXISTS` and includes lightweight migrations for older table versions:

- Adds `project.number_of_components` if missing.
- Adds `project.status` if missing.
- Adds `scenario_status.project_id` if missing.
- Adds a foreign key from `scenario_status.project_id` to `project.id` if missing.

`project` table currently contains:

- `id`
- `project_name`
- `number_of_components`
- `status`

`scenario_status` table currently contains:

- `id`
- `project_id`
- `state`
- `priority`
- `number_of_reps`
- `number_of_computed_reps`
- `recipe_info`
- `container_image`
- `confidence_metric`

Scenario inserts happen in a single transaction. If any row in a batch fails, the transaction rolls back and no partial batch is committed.

### NATS Connectivity

NATS connection code lives in:

- `scenario-manager/internal/nats/natsconnect.go`

Environment variables:

- `SCENARIO_MANAGER_NATS_URL`
- `SCENARIO_MANAGER_NATS_USER`
- `SCENARIO_MANAGER_NATS_PASSWORD`

The connection is stored globally behind a mutex. `normalizeEndpoint` accepts:

- Full URLs like `nats://host:4222`
- Host and port like `host:4222`
- Host only like `host`, defaulting to `nats://host:4222`

### EDS Communication Protocol

EDS communication code lives in:

- `scenario-manager/internal/nats/eds_com.go`

It defines:

- Availability request payload: `EDSAvailabilityNotice`
- Ready response payload: `EDSReadyResponse`
- Scenario batch payload: `ScenarioBatch`
- Scenario row payload: `ScenarioStatusPayload`
- Batch acknowledgement payload: `ScenarioBatchAck`

Configuration environment variables:

- `SCENARIO_MANAGER_EDS_AVAILABLE_SUBJECT`
- `SCENARIO_MANAGER_EDS_BATCH_SUBJECT_TEMPLATE`
- `SCENARIO_MANAGER_EDS_BATCH_SUBJECT`
- `SCENARIO_MANAGER_EDS_QUEUE_GROUP`
- `SCENARIO_MANAGER_EDS_MAX_BATCH`
- `SCENARIO_MANAGER_EDS_STREAM`
- `SCENARIO_MANAGER_EDS_CONSUMER`
- `SCENARIO_MANAGER_EDS_ACK_WAIT`
- `SCENARIO_MANAGER_EDS_MAX_ACK_PENDING`

Defaults:

- Availability subject: `cbse.eds.scenarios.available`
- Batch subject template: `cbse.{project}.eds.scenarios`
- Legacy fixed batch subject: `cbse.eds.scenarios.batch`
- Queue group: `scenario-manager-eds`
- Max batch size: `1000`
- Stream: `cbse_eds_scenarios`
- Durable consumer: `scenario-manager-eds-consumer`
- ACK wait: `2m`
- Max ACK pending: `1024`

Protocol flow:

1. EDS sends an availability request to the availability subject.
2. Scenario Manager parses the request.
3. Scenario Manager derives a batch subject from the project name.
4. Scenario Manager responds with `status=ready` and the batch subject.
5. EDS publishes a `ScenarioBatch` to JetStream.
6. Scenario Manager validates the batch.
7. Scenario Manager resolves the project row by project name.
8. Scenario Manager inserts scenario status records.
9. Scenario Manager ACKs JetStream only after successful persistence.
10. On persistence errors, it NAKs so the message can be redelivered.

Invalid payload handling:

- Empty batch payloads are ACKed with an error response.
- Invalid JSON is ACKed with an error response.
- Missing project is ACKed with an error response.
- Oversized batches are ACKed with an error response.
- Insert failures are NAKed for redelivery.

The subject template supports `{project}` and `%s` placeholders. Project names are normalized to lowercase NATS subject tokens with non-alphanumeric characters mapped to `-`, while `-` and `_` are preserved.

### Sequential EDS Processing

`scenario-manager/internal/core/scenario_manager.go` wraps EDS processing with a mutex:

- NATS may invoke handlers concurrently.
- The custom processor serializes batch conversion and insertion in-process.
- This avoids concurrent inserts within one Scenario Manager instance.

This does not serialize across multiple Scenario Manager replicas. The durable queue consumer and database constraints would need to provide cross-replica safety.

### Scenario Manager Dockerfiles

`scenario-manager/Dockerfile`:

- Builds from monorepo root.
- Copies `go.work`, `experiment-operator`, and `scenario-manager`.
- Builds a static Linux AMD64 binary.
- Runs on `alpine:3.21`.
- Creates a non-root `app` user.
- Entrypoint is `/usr/local/bin/scenario-manager`.

`scenario-manager/test_sm.Dockerfile`:

- Builds an image that runs `go test -v -count=1 ./...`.
- Can accept a context with both modules or fetch `experiment-operator` from GitHub if missing.
- Intended for in-cluster or containerized Scenario Manager tests.

### Scenario Manager Tests

Tests currently present:

- `internal/nats/natsconnect_test.go`
- `internal/coredb/coredbconnect_test.go`
- `internal/kube/kubeconnect_test.go`

These are mostly integration-style tests:

- NATS test skips if `SCENARIO_MANAGER_NATS_URL` is unset.
- Core DB test skips if DB env vars are unset.
- Kubernetes test appears intended to skip outside a cluster but currently cannot do so cleanly because `KubeConnect` uses `log.Fatalf` on missing in-cluster config.

There are no focused unit tests yet for:

- EDS subject generation.
- EDS batch validation.
- JetStream stream config behavior.
- DB insert mapping.
- Project create/update/delete behavior using a test database.
- Informer event mapping with fake objects.

## Historical Test Environment

The former `test-env` directory is now consolidated under `test/`; its compatibility fixtures live in `test/compat/eds-sm`.

Important files:

- `test/compat/eds-sm/README.md`: overview of the compatibility fixtures.
- `test/compat/eds-sm/EDS_SM_TEST_STEPS.md`: archived runbook and migration note.
- `test/compat/eds-sm/run_eds_e2e.sh`: compatibility wrapper for the smoke suite.
- `test/compat/eds-sm/postgres-core-db.yaml`: legacy PostgreSQL Deployment and Service.
- `test/compat/eds-sm/nats-jetstream.yaml`: legacy NATS Deployment and Service with JetStream enabled.
- `test/compat/eds-sm/manifests.yaml`: legacy Scenario Manager ConfigMap, ServiceAccount, RBAC, and Deployment.
- `test/compat/eds-sm/simulationexperiment-sm-eds-e2e.yaml`: legacy sample `SimulationExperiment`.
- `test/compat/eds-sm/eds-mock.yaml`: legacy Kubernetes Deployment for the EDS mock.
- `test/mocks/eds/eds_mock.py`: Python EDS mock implementation.
- `test/mocks/eds/Dockerfile`: EDS mock image definition.
- `test/mocks/eds/requirements.txt`: Python dependency list.

### EDS Mock

The Python mock EDS:

- Connects to NATS with retries.
- Sends availability requests to Scenario Manager.
- Reads the returned batch subject.
- Publishes deterministic scenario batches to JetStream.
- Can keep running after publishing for log inspection.

Default behavior:

- Project: `sm-eds-e2e`
- Total batches: `2`
- Scenarios per batch: `2`
- Expected scenario rows: `4`

### EDS End-To-End Script

`run_eds_e2e.sh` performs these steps:

1. Apply Core DB and NATS resources.
2. Apply Scenario Manager resources.
3. Scale/recreate Scenario Manager and set the image.
4. Wait for PostgreSQL, NATS, and Scenario Manager rollouts.
5. Verify NATS has ready endpoints.
6. Clean up prior EDS mock deployment and old project rows.
7. Apply legacy-schema guard migrations.
8. Apply the `SimulationExperiment` CRD.
9. Apply the test `SimulationExperiment`.
10. Wait for the project row in Core DB.
11. Deploy the EDS mock.
12. Set EDS mock environment variables.
13. Wait for expected scenario rows.
14. Print expected and actual counts.

This is a useful integration proof for the EDS-to-SM intake path.

## Current Implemented Capabilities

The codebase currently has:

- A Kubernetes CRD for `SimulationExperiment` under `alpha2`.
- Generated Go API types and DeepCopy methods.
- A controller-runtime operator process with metrics, health checks, RBAC scaffolding, and reconciliation.
- Resource provisioning for databases, translator, post-processing, and experimental design service components.
- Status progression from empty to `Pending` to `Provisioning` to `InProgress`.
- A Scenario Manager process with startup dependency checks.
- Kubernetes informer code watching `SimulationExperiment` resources.
- Project persistence into PostgreSQL.
- Schema creation and light migrations for Core DB tables.
- NATS connection management.
- JetStream stream setup and durable queue consumption.
- EDS request/reply handshake.
- Scenario batch validation and insertion into `scenario_status`.
- Dockerfiles for operator, Scenario Manager, Scenario Manager test runner, and EDS mock.
- Kustomize/Kubebuilder deployment assets for the operator.
- Kubernetes manifests for a Scenario Manager/EDS integration environment.
- A shell-driven e2e harness for EDS batch ingestion.

## Not Yet Implemented

The repository does not yet contain:

- A simulation worker component.
- A scheduler for scenarios or replications.
- Execution of model containers.
- Translation from scenario metadata into runnable model artifacts.
- Post-processing orchestration beyond provisioning a post-processing service container.
- Replication progress updates.
- Completion detection.
- Result aggregation.
- Failure recovery semantics for running simulations.
- Bidirectional updates from Scenario Manager back to `SimulationExperiment.status.metrics.scenarioCount`.
- A complete operator webhook implementation.
- Conversion logic between `alpha1` and `alpha2`.
- Production-grade secrets handling.
- Persistent volumes for the test PostgreSQL/NATS resources.
- CI configuration for the full workspace, beyond generated/scaffolded assets.

## Important Inconsistencies

These are the items most likely to confuse future development:

1. Top-level docs understate current Scenario Manager implementation.

   The top-level README says only the Experiment Operator is implemented. The Scenario Manager now has meaningful code and an e2e harness.

2. `alpha1` references remain, but no `api/alpha1` package exists.

   The Kubebuilder `PROJECT` file references `api/alpha1`, and `config/samples` includes `alpha1` samples. The current generated CRD serves only `alpha2`.

3. Webhook scaffolding and tests are ahead of implementation.

   `cmd/main.go` creates a webhook server and config has webhook/cert-manager scaffolding, but webhook registration is commented out and there is no visible webhook package. E2E tests expect conversion webhook CA injection, which likely does not match the current runtime.

4. Local Scenario Manager Kubernetes behavior is inconsistent.

   `CheckingDependencies` appears to tolerate no in-cluster config during local development, but `KubeConnect` exits immediately with `log.Fatalf`, so that tolerant path is unreachable.

5. Operator creates Pods but RBAC markers focus on Deployments, Secrets, Services, and ConfigMaps.

   The experimental design service path creates a raw Pod. Generated RBAC should be checked to confirm Pod permissions are present.

6. Host-based databases and image-based databases behave differently.

   Image-based DBs get Deployments, Services, and Secrets. Host-based DBs only get a TCP smoke test and then return. That may be intentional, but it is not obvious from the spec.

7. `defaultServiceType` is not applied by controller code.

   The field is present in the API but individual service types are used directly. If admission defaulting does not populate a nested service type, this can create invalid or surprising Service specs.

8. Tests are mostly integration tests.

   There are few fast unit tests around pure logic, which makes it harder to iterate on NATS subject handling, DB mapping, and informer event behavior.

## Verification Attempt

I attempted to run a small compile/test subset:

- `go test ./scenario-manager/internal/nats ./scenario-manager/internal/coredb ./scenario-manager/internal/core`
- `go test ./experiment-operator/api/...`
- `go test ./scenario-manager/cmd`

All three failed immediately because the current shell environment does not have `go` available:

```text
zsh:1: command not found: go
```

So this summary is based on static source review only. I did not modify or execute cluster resources.

## Suggested Next Steps

Highest-value next steps:

1. Update documentation.

   Bring the top-level `README.md` in line with current reality: the Scenario Manager has EDS intake and Core DB persistence, while simulation execution remains unimplemented.

2. Decide what to do with `alpha1`.

   Either restore/implement `alpha1` and conversion, or remove stale `alpha1` references from `PROJECT` and samples.

3. Fix Scenario Manager local/test startup behavior.

   Change `KubeConnect` to return `false` on missing in-cluster config instead of calling `log.Fatalf`. Let `CheckingDependencies` decide whether that is fatal.

4. Add focused unit tests.

   Start with pure logic:

   - NATS endpoint normalization.
   - EDS subject template expansion.
   - EDS project token sanitization.
   - EDS batch validation decisions.
   - Core DB DSN construction.
   - Component count calculation.

5. Clarify experimental design service lifecycle.

   Decide whether it is a long-running Deployment, a one-shot Job, or a raw Pod. The current raw Pod implementation will be limiting.

6. Validate generated RBAC.

   Confirm the operator can create/update/delete every resource it actually reconciles, especially Pods.

7. Add spec validation.

   Enforce required runtime invariants, especially:

   - Database must have either `image` or `host`.
   - Host-based database should probably still produce connection data.
   - Experimental design service should not create a Pod with an empty image.
   - NodePort should only be set when service type is `NodePort`.

8. Wire Scenario Manager back to CR status.

   Once scenarios are inserted, update `SimulationExperiment.status.metrics.scenarioCount` so `kubectl get simexp` reflects Scenario Manager intake progress.

9. Define the execution model.

   The next architectural gap is the component that turns `scenario_status` rows into running simulation work and updates progress.

## Short Version

You currently have a research prototype with a real Kubernetes operator and an increasingly real Scenario Manager. The operator provisions experiment dependencies and moves CR status into `InProgress`. The Scenario Manager watches experiment CRs, persists projects, accepts EDS scenario batches over NATS JetStream, and writes scenario rows into PostgreSQL. The strongest working story is EDS-to-Scenario-Manager ingestion. The main missing story is actual simulation execution.
