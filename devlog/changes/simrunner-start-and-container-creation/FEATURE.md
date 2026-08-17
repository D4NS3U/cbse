# Specification for Alpha4 Simulation Runner Startup and Translator On-Demand Image Creation

This document has the following structure:

1. Mission Statement -- explains the intent and attitude of the work.
2. Scope -- explains the boundaries, goals, and explicitly chosen workflow.
3. Change Location -- identifies the code, API, and manifest areas expected to change.
4. Logic Description -- describes the implementation behavior and ownership boundaries.

## Mission Statement

This branch turns the Scenario Manager (SM) from a component that only advances a scenario to `InProcessing` into the component that starts and observes the scenario's Simulation Runner workload on Kubernetes.

Translator remains an always-on, per-experiment service, provisioned and managed by the Experiment Operator. This branch does not replace it with a Job or change its NATS/JetStream request-consumer model. “On-demand container creation” means that Translator creates the executable simulation model and builds its Simulation Runner image only after it receives a request for a claimed scenario. It then returns the immutable runner image reference through the existing ready-message workflow, from which SM starts the runner on demand.

CBSE cannot provide one universal production Translator because translating a scenario into an executable model is user-defined. Instead, this branch provides a production-quality reference Translator template in `component-templates/translator/`. The template owns the stable NATS/JetStream, BuildKit, registry, digest, acknowledgement, and extension contracts. Its included SimPy generator is an intentionally small example and replacement point, not a production simulation model.

The implementation must make Kubernetes runner Jobs a projection of durable scenario state, not a second source of truth. Database state and guarded transitions decide ownership; workloads perform external work. The coding agent should favor small, explicit, idempotent behavior that is safe across SM restarts and retries while preserving the existing Translator handoff boundary.

The coding agent should favor clarity over cleverness: write small, explicit, modular code that follows nearby patterns. Each component should have one obvious responsibility, explicit dependencies, descriptive errors, and no hidden package-global state. Understand the existing lifecycle, ownership boundaries, and tests first; reuse suitable abstractions, and introduce new ones only to clarify a boundary or enable independent testing.

Work incrementally and stay within scope. Preserve the Translator request and ready-message contracts, durable Scenario Manager state machine, and Kubernetes ownership patterns unless this specification changes them. Avoid redesigns, speculative compatibility layers, and unrelated cleanup. Make retries, restarts, and `AlreadyExists` results deliberate and inspectable: confirm the exact expected resource or return a useful conflict; never silently adopt it. Validate image references, Secret types, namespaces, ownership, and lifecycle state at boundaries. Do not weaken security implicitly, hide privileged requirements, or treat a created Job or pushed image as proof of a successful simulation.

Write clear documentation alongside the code. Explain the lifecycle, ownership, configuration, security and prototype assumptions, operational diagnosis, and deferred hardening. Tests must cover normal behavior, the exact validation groups defined below, stale claims, cancellation, identity collisions, repeated reconciliation, partial external success, and cleanup.

## Scope

This change introduces the only supported `SimulationExperiment` API version: `experiment.cbse.terministic.de/alpha4`. It defines the Translator image-build architecture, starts Simulation Runner Jobs, and observes their terminal result through the `PostProcessing` boundary.

`alpha4` is introduced by this feature as the only served, storage, and reconciled version. It is the active API, not a deprecated or transitional version. Only `alpha2` and `alpha3` are retired. There is no legacy reconciler, conversion webhook, compatibility mode, or automatic migration.

### Upgrade from alpha2 or alpha3

This is a breaking upgrade. Before installing the alpha4-only CRD, operators must export the desired old resources, delete all old `SimulationExperiment` resources, and delete the old CRD. They then install the alpha4 CRD and recreate the exported resources as alpha4. The Operator and SM must never perform these destructive steps.

The shared smoke cluster follows the same alpha4-only contract but does not perform the breaking upgrade. The smoke harness must never delete or migrate the shared CRD. Its preflight requires the installed CRD to serve and store only alpha4. It fails before creating a test namespace when it finds alpha2, alpha3, another storage version, or incompatible `status.storedVersions`. A cluster administrator performs the one-time breaking upgrade outside the test harness. Fast envtest coverage installs the generated alpha4-only CRD in an isolated control plane and verifies that alpha2 and alpha3 are not served.

### In Scope

1. **Alpha4 API, identity, and registry authentication.**
   - Carry the alpha3 user-facing schema into `api/alpha4` and add required `spec.translator.registryAuthSecretRef` and `spec.translator.builderImage`. `spec.translator.image` continues to select the Translator container. `builderImage` independently selects its rootless BuildKit sidecar.
   - Add optional `spec.translator.builderResources.limits.cpu` and `.memory`. When either limit is omitted, default it to `1` CPU or `2Gi` memory respectively. The feature exposes limits only; it does not define resource requests.
   - Add optional `spec.runner.jobTemplate` as a typed Kubernetes `batch/v1.JobTemplateSpec`. When absent, SM uses the minimal built-in runner Job. When present, it provides the permitted Job and Pod customization described below.
   - `registryAuthSecretRef` is the name of a same-namespace Secret of type `kubernetes.io/dockerconfigjson` with the required `.dockerconfigjson` key. It may contain credentials for more than one registry host. It supplies the Translator and BuildKit push session, pulls for the Translator and Builder images when required, and the runner Job image pull secret.
   - `translator.image`, `baseimage`, `builderImage`, and a ready-message runner image must be OCI digest references in the exact form `name@sha256:<64 lowercase hexadecimal characters>`. Tags alone are invalid.
   - `translator.repository` is the user-provided OCI repository where generated runner images are pushed. It is a repository name without a tag or digest. This feature and its tests must not hard-code a production registry. Registry access uses verified TLS. Insecure registries and TLS-verification bypasses are prohibited.
   - Persist each project with `project_namespace`, `project_name`, and `experiment_uid`. The `project_namespace`/`project_name` pair is unique. Existing Core DB tables are validation-only; this schema change requires a recreated development or test database and must not use automatic `ALTER TABLE` repair.
   - Keep experiment identity in the project table. Do not add namespace, project-name, or experiment-UID columns to `scenario_status`; its existing `project_id` foreign key remains the only project identity stored on a scenario row.
   - Namespace and project name are the routing identity for EDS and Translator communication. Alpha4 experiment names must be lowercase DNS labels matching `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`; dots are rejected even though Kubernetes otherwise permits them in names. Namespace and project are inserted into NATS subjects without lossy normalization.

2. **Namespace-aware EDS and Translator communication.**
   - Change the alpha4 subjects to carry namespace and project as separate tokens:
     - EDS availability: `cbse.<namespace>.<project>.eds.scenarios.available`
     - EDS scenario batch: `cbse.<namespace>.<project>.eds.scenarios`
     - Translator request: `cbse.<namespace>.<project>.trans.request`
     - Translator ready: `cbse.<namespace>.<project>.trans.<scenario-id>.ready`
   - Preserve the existing EDS availability, scenario-batch, Translator request, and Translator ready JSON payloads. Do not add namespace or UID fields to those payloads. Preserve publish confirmation, ACK/NAK behavior, guarded `Created -> Scheduled`, accepted-ready handling, and exact-attempt recovery.
   - SM parses namespace and project from every EDS and Translator subject and resolves the exact `(project_namespace, project_name)` database row. A payload project field, where the existing payload has one, must exactly equal the project subject token. A subject/payload or subject/database identity mismatch is a permanent poison message and must not mutate scenario state.
   - Before accepting a non-empty Translator ready image, SM gets the matching alpha4 experiment and validates the image as a digest reference. It normalizes the image repository and `spec.translator.repository` with the same OCI reference parser and requires an exact match. A digest from another repository is a permanent ready-message rejection: SM ACKs it as poison, does not persist the image, and does not create a runner Job.
   - A cluster-wide SM subscribes to `cbse.*.*.eds.scenarios.available`, `cbse.*.*.eds.scenarios`, and `cbse.*.*.trans.*.ready`. When `SCENARIO_MANAGER_WATCH_NAMESPACE` is set, replace the namespace wildcard with that exact validated namespace token. Never subscribe to or accept another namespace in namespace-scoped mode.
   - The Operator injects `SIMULATIONPROJECTNAMESPACE`, `SIMULATIONPROJECTNAME`, and `SIMULATIONEXPERIMENTUID` into the per-experiment EDS and Translator through the downward API. Each component publishes or subscribes only to subjects for that exact namespace/name pair.
   - SM removes the experiment's NATS and JetStream artifacts when it observes the experiment in `Error`, `Failed`, or `Completed`. Cleanup deletes the UID-specific Translator durable consumer and purges remaining EDS-batch, Translator-request, and Translator-ready messages for the exact namespace/project subjects. It never deletes a shared stream or an SM consumer.

3. **Reference Translator template and on-demand image creation.**
   - Add a Go reference implementation at `component-templates/translator/`. It is the supported integration template for user-defined Translators and replaces `test/mocks/translator` in the full smoke path.
   - Separate the implementation into three explicit layers: the CBSE-maintained reference framework, a replaceable model-generator module, and the generated scenario-specific runner image. Production-quality guarantees apply to the framework and its integration contracts, not to the example model's semantics.
   - The Operator configures Translator through the same environment-based process used by the existing mock: `NATS_URL`, Translator stream, exact request subject, ready-subject template, and UID-specific durable-consumer name. It also injects `SIMULATIONPROJECTNAMESPACE`, `SIMULATIONPROJECTNAME`, `SIMULATIONEXPERIMENTUID`, `REPOSITORY`, and `BASEIMAGE`. The template validates all values before connecting to NATS.
   - `BASEIMAGE` is sourced only from `spec.translator.baseimage`. EDS payloads, Scenario Status, and existing Translator request and ready payloads do not carry or persist base-image information.
   - The Operator mounts the existing `<experiment-name>-resultdb-sct` Secret read-only at `/resultdb-connection` for Translator. It contains `host`, `port`, `dbname`, `user`, and `password`.
   - The template strictly validates the established request payload and handles one request at a time. Its workspace is `/workspace/scenario-<scenario-id>/attempt-<attempt>`. Before generation it removes and recreates that exact attempt directory so redelivery cannot reuse stale build input.
   - The template uses the official BuildKit Go client over `unix:///run/buildkit/buildkitd.sock`. It submits the generated attempt directory as both the Dockerfile and build context, supplies registry credentials from `/registry-auth/config.json` through the BuildKit session, and requests a registry push to the deterministic tag.
   - The pushed tag is `<repository>:runner-<12-char-UID-prefix>-s<scenario-id>-a<attempt>`. The UID prefix is the first 12 lowercase hexadecimal characters of the UID after removing hyphens. Translator resolves this tag and publishes only the digest reference.
   - Translator publishes the ready message and waits for publish confirmation before acknowledging the request. A build, push, or digest-resolution failure publishes the existing ready message with an empty image and is acknowledged only after that publish succeeds.
   - A successful push is a durable external effect. If ready publication then fails, Translator retains the resolved digest and retries publication of the same ready message. It must not regenerate, rebuild, or push another image for that scenario and attempt.
   - After a terminal acknowledged outcome, the template removes the attempt workspace. Before a build on redelivery, it checks the retained outcome for that scenario and attempt and resolves the deterministic registry tag. If the image was already pushed, it reconstructs the same digest ready message and resumes publication without invoking the generator or BuildKit. It recreates build input only when no successful pushed outcome exists.

4. **Mandatory rootless BuildKit sidecar.**
   - Each Translator Pod has exactly two containers: `translator` and rootless `buildkit`.
   - Both containers run as UID and GID `1000`. The Pod sets `fsGroup: 1000`. They share `emptyDir` volumes mounted at `/workspace` and `/run/buildkit`, so generated build input and the BuildKit socket are writable by both containers. BuildKit listens only on `/run/buildkit/buildkitd.sock`; no TCP endpoint is exposed.
   - BuildKit receives `spec.translator.builderResources.limits`, with default limits of `1` CPU and `2Gi` memory. A user may override either limit through the alpha4 API.
   - Both containers receive the registry Secret read-only at `/registry-auth/config.json`. The Translator also receives the result-database Secret read-only.
   - BuildKit runs rootless and non-privileged with the required compatibility exception: `Unconfined` seccomp and AppArmor plus `--oci-worker-no-process-sandbox`. It must not use privilege escalation, privileged mode, host networking, host paths, or host container-runtime sockets. Translator keeps the normal restricted security profile.
   - A namespace that enforces the Kubernetes Baseline or Restricted Pod Security Standard rejects this required BuildKit exception. The smoke harness therefore labels its isolated `cbse-e2e-<run-id>` namespace with `pod-security.kubernetes.io/enforce=privileged`. This admission label does not permit the implementation to set `privileged: true` or weaken any other container restriction. The harness keeps `audit` and `warn` at `restricted` so the exception remains visible.
   - Kubernetes 1.30 or newer is required so the BuildKit container can set `securityContext.appArmorProfile.type: Unconfined`. Cluster installation documentation must call out the namespace admission requirement and the AppArmor prerequisite.
   - Generated source, Dockerfile, and build commands are trusted prototype input. Egress isolation and an untrusted-build sandbox are deferred.

5. **Simulation Runner startup and terminal observation.**
   - For one accepted Translator image, SM creates or confirms one `batch/v1` Job for the scenario and translation attempt. The Job name is `simrun-<12-char-UID-prefix>-s<scenario-id>-a<attempt>`, using the UID-prefix rule above.
   - The default Job has one container named `runner`. A custom template must contain exactly one `runner` container and may add sidecars, init containers, volumes, mounted ConfigMaps or Secrets, resource configuration, annotations, and Kubernetes scheduling constraints. SM replaces the `runner` image with the accepted Translator image.
   - The Job is non-indexed. `spec.completions` and `spec.parallelism` both equal `scenario_status.number_of_reps`. Each successful runner Pod performs one homogeneous repetition. Pods and repetitions require no stable identifier. All repetitions may run concurrently; a user-defined model and durable result sink must support that concurrency.
   - This branch deliberately adds no lower operational cap below `math.MaxInt32`, no separate maximum parallelism, and no batching or throttling. A valid EDS message can therefore request a very large Job. The trusted-input prototype accepts that risk. A future EDS template will own policy that limits generated repetition counts.
   - Set `restartPolicy: Never`, `backoffLimit: 0`, and no TTL-after-finished field. Completed and failed Jobs remain visible.
   - The Job has the exact alpha4 experiment owner reference, runs in the experiment namespace, and uses the deterministic ServiceAccount `simrunner-<12-char-UID-prefix>`. The Operator creates this ServiceAccount, gives it no RoleBinding, and SM sets `automountServiceAccountToken: false` on the Job Pod.
   - Runner, sidecar, and init-container images must support non-root execution. The effective Pod template uses `RuntimeDefault` seccomp, `runAsNonRoot: true`, disabled privilege escalation, and dropped capabilities. The Operator validates the template before moving the experiment to `InProgress`; SM validates it again before creating a Job.
   - SM observes Jobs for scenarios in `InProcessing` and monotonically records successful Pod completions in `scenario_status.number_of_computed_reps`. A successful Job records all requested repetitions before the guarded `InProcessing -> PostProcessing` transition. A failed Job preserves any partial successful count before the guarded `InProcessing -> Failed` transition. This branch does not execute post-processing or move a scenario out of `PostProcessing`.

### Workflow Decisions

| Concern | Decision for this branch |
| --- | --- |
| Active API | Alpha4 only; alpha2 and alpha3 are retired through a manual breaking upgrade. |
| Messaging identity | Namespace and project are separate NATS subject tokens and resolve one exact project row. |
| Messaging payloads | Existing EDS and Translator JSON payloads remain unchanged. |
| Translator lifetime | Existing always-on Deployment and Service per experiment. |
| Translator implementation | Go reference framework in `component-templates/translator/`; user-defined model generation is plugged into it. |
| Example model | Scenario-specific SimPy runner generated from validated `recipe_info`; illustrative rather than production simulation logic. |
| Build engine | One serial, rootless BuildKit sidecar per Translator Pod. |
| Build handoff | Shared `/workspace` and `/run/buildkit` `emptyDir` volumes, UID/GID `1000`, `fsGroup: 1000`, and a Unix socket. |
| BuildKit resources | User-configurable CPU and memory limits; defaults are `1` CPU and `2Gi` memory. No requests are defined. |
| Build result | Deterministic attempt tag, then immutable digest in the existing ready message. |
| Ready publish retry | Reuse the successful pushed digest; never regenerate, rebuild, or repush that scenario attempt. |
| Credential lifetime | Registry and Result DB credentials are loaded at Translator startup; live rotation and reload are out of scope. |
| Runner unit | One customizable, non-indexed Job per scenario and translation attempt; one successful runner Pod per required repetition. |
| Runner concurrency | `parallelism = completions = number_of_reps`. |
| Runner concurrency limit | No feature-specific limit below `math.MaxInt32`; trusted EDS input is an explicit prototype assumption. |
| Runner retry | No Kubernetes retry: `backoffLimit: 0`. |
| Runner completion | Job success moves the scenario to `PostProcessing`; Job failure moves it to `Failed`. |
| Post-processing boundary | `PostProcessing` is reached but not processed in this branch. |
| Experiment terminal phases | SM observes `Error`, `Failed`, and `Completed` for cleanup, but does not produce those experiment phase transitions in this branch. |
| Example result sink | Each successful generated runner appends one JSONB result to a PostgreSQL table dedicated to its scenario. Repetitions have no identity. |
| Scenario logging | Log scenario-level creation and terminal outcome plus one runner-controlled failure record; do not log each successful repetition. |
| Durable authority | PostgreSQL guarded transitions; Jobs are idempotent external effects. |

### Out of Scope

This branch must not add:

- alpha2 or alpha3 serving, reconciliation, conversion, or migration automation;
- a per-scenario Translator Job or a change to Translator lifetime;
- new EDS or Translator JSON payload fields, or a new JetStream acknowledgement model beyond the namespace-aware subject change defined here;
- a universal production model generator or production simulation semantics;
- runner result ingestion, confidence calculation, PostProcessingService invocation, or any scenario transition out of `PostProcessing`;
- aggregation of scenario states into `SimulationExperiment.status.phase`, including SM-owned transitions to experiment phase `Failed` or `Completed`;
- a later execution/run identity for a repeated scenario after post-processing;
- PostProcessingService-driven `n+m` repetitions or any increase of `number_of_reps` after the original EDS batch;
- Kubernetes runner retries, timeout recovery, cancellation, or self-healing;
- a second scheduler implementation or alpha4 scheduler-provider selection field;
- concurrent Translator builds, object-storage/PVC build handoff, or remote BuildKit TCP access;
- automatic registry or Result DB credential rotation, file watching, or live credential reload;
- production lifecycle deletion or garbage collection of generated runner images;
- centralized scenario-log storage, retention limits, or automatic deletion of past logs;
- privileged containers, host networking, host paths, container-runtime socket mounts, or insecure registry/TLS settings;
- untrusted Dockerfile isolation or build egress controls;
- an EDS template, EDS-side repetition policy, runner parallelism cap, or SM-side runner throttling; or
- deployment of test workloads in `default` or `kube-system`.

## Change Location

1. `experiment-operator/api/alpha4/`, CRD manifests, schemes, samples, and tests -- define alpha4 only, the registry fields, configurable BuildKit resource limits with defaults, optional typed runner Job template, the UID environment value, the BuildKit sidecar, and the runner ServiceAccount.
2. `experiment-operator/internal/controller/` -- validate images, Secrets, and runner Job templates; reconcile the Translator, its mock-style runtime configuration, Result DB Secret mount, BuildKit limits and workspace permissions, and the runner ServiceAccount with their exact volume, security, and ownership settings.
3. `scenario-manager/internal/nats/`, communication types, and EDS integration tests -- implement the namespace-aware EDS and Translator subject grammar, parsing, scoped wildcard subscriptions, exact namespace/name persistence lookups, and terminal experiment artifact cleanup without changing payload schemas.
4. `component-templates/translator/` and its tests -- implement the Go reference framework, namespace-aware request/ready subjects, per-experiment consumer, replaceable SimPy generator, attempt workspaces, BuildKit client, authenticated push, durable pushed-outcome recovery, digest resolution, and ready-message acknowledgement ordering. Retire `test/mocks/translator` from the full smoke path.
5. `scenario-manager/internal/core/`, `internal/coredb/`, and `internal/kube/` -- persist experiment identity, expose the scheduler boundary, implement the basic Kubernetes Job adapter, validate and merge runner templates, create or confirm Jobs, monotonically record successful repetitions, and apply terminal Job results.
6. The repository `Makefile` and test harness -- include the new Translator Go module in formatting, vet, unit, race, and compile checks run by `make test-fast`; replace alpha3 schemes, fixtures, manifests, preflight, and assertions with alpha4; and unconditionally clean generated smoke runner images.
7. SM RBAC and `test/e2e/manifests/` -- grant only `get`, `list`, and `watch` for alpha4 experiments plus `create`, `get`, `list`, and `watch` for Jobs; provide digest-pinned Builder, Translator, and runner smoke images.
8. `component-templates/translator/README.md`, `COMPONENT_DESIGN_GOALS.md`, `README.md`, `docs/CBSE_TESTING_GUIDE.md`, and developer documentation -- provide the Translator integration guide, common component design goals, alpha4-only public and test documentation, and links to those contracts.

## Logic Description

### Namespace-aware messaging contract

Alpha4 replaces project-only routing with a single canonical subject grammar. Namespace and project occupy separate subject segments and are copied directly from `SimulationExperiment.metadata.namespace` and `.metadata.name`. Code must validate both tokens before constructing or accepting a subject. It must not pass identity through `subject.NormalizeToken` or any other lossy conversion. Invalid identity is a permanent configuration or message error.

| Flow | Concrete subject | SM subscription or stream subject |
| --- | --- | --- |
| EDS availability request/reply | `cbse.<namespace>.<project>.eds.scenarios.available` | `cbse.*.*.eds.scenarios.available`, or `cbse.<watch-namespace>.*.eds.scenarios.available` |
| EDS scenario batch | `cbse.<namespace>.<project>.eds.scenarios` | `cbse.*.*.eds.scenarios`, or `cbse.<watch-namespace>.*.eds.scenarios` |
| Translator request | `cbse.<namespace>.<project>.trans.request` | JetStream stream subject `cbse.*.*.trans.request`; each Translator consumes its exact subject |
| Translator ready | `cbse.<namespace>.<project>.trans.<scenario-id>.ready` | `cbse.*.*.trans.*.ready`, or `cbse.<watch-namespace>.*.trans.*.ready` |

EDS continues its request/reply handshake, but publishes availability to its exact namespace/project subject. SM parses the subject, validates the existing availability payload's `project` against it, and resolves the exact project row before replying. A successful reply returns the exact namespace-aware batch subject. When the subsequent JetStream batch arrives, SM parses and validates its subject again, requires `ScenarioBatch.project` to match, and associates all inserted scenarios with `ProjectIDByNamespaceAndName(namespace, project)`.

SM's translation claim projection carries raw project namespace and name. The Translator request publisher uses both to construct the exact request subject. The per-experiment Translator template validates its injected namespace and project at startup, subscribes only to that subject, and publishes ready messages only on the corresponding namespace/project ready subject. SM parses both tokens from a ready subject and requires them to match the scenario's persisted project row before applying empty-image recovery or a non-empty ready result. For a non-empty image, SM also gets the UID-matching alpha4 experiment, validates the digest, and requires its normalized repository to equal `spec.translator.repository`. A wrong namespace, project, UID, digest, or repository is ACKed as poison and cannot consume an attempt or alter the row.

Transport-neutral and persistence projections carry the validated raw identity explicitly. Extend `ScenarioForTranslation` and `TranslatorReadyMessage` with `ProjectNamespace` and `ProjectName`; do not retain a single ambiguous `Project` field in new alpha4 code. EDS may keep the wire field `project`, but after subject parsing its internal `ScenarioBatch` also carries `ProjectNamespace` and `ProjectName` fields excluded from JSON. Core orchestration and database functions receive this explicit identity and never reconstruct a namespace from process configuration or a normalized subject token.

Malformed subject shape, invalid tokens, subject/payload disagreement, and a batch or ready message that identifies no matching project are permanent poison deliveries: respond with an error where request/reply permits it, ACK JetStream messages, and perform no database mutation. A temporary database or NATS failure is retryable: return an error for availability so EDS retries its request, or NAK a JetStream delivery. Availability for a valid identity that has not yet been registered returns `status=error` with no `batch_subject`; it does not create a project row. The EDS retries availability and publishes no batch until it receives `status=ready`.

The alpha4 defaults and configuration validation are:

- `SCENARIO_MANAGER_EDS_AVAILABLE_SUBJECT_TEMPLATE=cbse.{namespace}.{project}.eds.scenarios.available`
- `SCENARIO_MANAGER_EDS_BATCH_SUBJECT_TEMPLATE=cbse.{namespace}.{project}.eds.scenarios`
- `SCENARIO_MANAGER_TRANS_REQUEST_SUBJECT_TEMPLATE=cbse.{namespace}.{project}.trans.request`
- `SCENARIO_MANAGER_TRANS_READY_SUBJECT_TEMPLATE=cbse.{namespace}.{project}.trans.{scenario_id}.ready`

Each template must contain every named placeholder exactly once and each placeholder must occupy an entire dot-delimited segment. Alpha4 removes support for `%s`, fixed project-independent subjects, and templates missing `{namespace}` or `{project}`. SM derives stream and subscription wildcards only by replacing named identity placeholders with `*`; it never accepts a caller-supplied wildcard that covers a namespace outside `SCENARIO_MANAGER_WATCH_NAMESPACE`.

The EDS scenario stream remains `cbse_eds_scenarios` with `WorkQueuePolicy` and includes `cbse.*.*.eds.scenarios`. The Translator stream remains `cbse_translator` with `WorkQueuePolicy` and includes both `cbse.*.*.trans.request` and `cbse.*.*.trans.*.ready`. Existing SM durable and queue-group behavior remains shared across SM replicas. If SM is namespace-scoped, its EDS and Translator-ready durable and queue-group names must include the validated namespace token so independent namespace-scoped SM installations sharing NATS do not join the same consumer group.

Each Translator uses a durable consumer named `translator-<12-char-UID-prefix>` with its exact request subject as the filter. The UID prefix follows the existing lowercase, hyphen-stripped rule. This keeps consumers distinct across namespaces and across deletion/recreation of an experiment. Translator request handling permits one unacknowledged request at a time with `MaxAckPending: 1`; long generation or BuildKit operations send JetStream in-progress acknowledgements often enough not to exceed the configured `AckWait`.

### UID-safe project persistence

The alpha4 project table has this logical schema:

```text
id                    SERIAL PRIMARY KEY
project_namespace     TEXT NOT NULL
project_name          TEXT NOT NULL
experiment_uid        TEXT NOT NULL
number_of_components  INTEGER NOT NULL DEFAULT 0
status                TEXT NOT NULL DEFAULT ''
UNIQUE (project_namespace, project_name)
```

`scenario_status.project_id` remains a foreign key to `project.id` with `ON DELETE CASCADE`. No experiment identity is duplicated in `scenario_status`. Queries obtain namespace, name, and UID by joining the project table.

Project registration is idempotent for the exact namespace, name, and UID. Mutable project fields may be refreshed for that row. If SM observes the same namespace and name with a different UID, it treats the stored row as a deleted earlier incarnation. In one transaction it deletes that old project row, lets the existing foreign key cascade remove its stale scenarios, and inserts the new project row. This replacement is logged with both UIDs.

Project updates and deletes include namespace, name, and UID in their `WHERE` clause. A late update or delete event for an earlier UID is a stale no-op. It cannot update or delete the replacement row. This keeps informer reordering and missed deletion events from blocking the new experiment or attaching old scenarios to it.

When the project table does not exist, SM creates it with the alpha4 schema. When it already exists, SM validates it without issuing `ALTER TABLE`. A legacy or incompatible project table makes startup fail with recreation guidance before the informer, NATS consumers, or selector starts.

### Terminal experiment artifact cleanup

SM requests cleanup when the SimulationExperiment informer adds an experiment already in `Error`, `Failed`, or `Completed`, or observes a transition into one of those phases. Handling terminal objects on informer add makes cleanup retry after an SM restart. `Error` is included even though an experiment that fails during provisioning will normally have no per-experiment messaging artifacts.

Cleanup uses the event's namespace, name, and UID. Before purging namespace/name subjects, SM gets the current alpha4 experiment. If an experiment with the same namespace and name now has a different UID, the cleanup event is stale and SM performs no cleanup. This prevents a late terminal event from purging messages for a recreated experiment.

For an accepted terminal event, SM performs these idempotent operations:

1. Delete durable consumer `translator-<12-char-UID-prefix>` from stream `cbse_translator`.
2. Purge messages matching `cbse.<namespace>.<project>.eds.scenarios` from stream `cbse_eds_scenarios`.
3. Purge messages matching `cbse.<namespace>.<project>.trans.request` from stream `cbse_translator`.
4. Purge messages matching `cbse.<namespace>.<project>.trans.*.ready` from stream `cbse_translator`.

The availability subject uses Core NATS request/reply and has no durable server artifact to remove. Cleanup never deletes or recreates the shared EDS or Translator streams. It never deletes SM durable consumers or queue groups. A missing stream, consumer, or matching message is successful cleanup.

SM replicas may request the same cleanup. The operations remain safe under repetition. A temporary NATS or JetStream failure is retried until success or shutdown. The delay between attempts is bounded and context-aware. Cleanup failure does not change the terminal experiment phase and does not block handling for other experiments.

This branch reacts to terminal experiment phases but does not decide them. A future SM lifecycle feature will own scenario aggregation and the experiment transitions from `InProgress` to `Failed` or `Completed`. The Operator continues to own `Pending -> Provisioning -> InProgress` and provisioning failure to `Error`.

### Translator template boundary

The reference Translator is production-quality integration code, but it is not a universal production Translator. The stable framework owns request consumption and validation, serial delivery, workspace lifecycle, BuildKit sessions, registry authentication, push and digest verification, ready publication, and request acknowledgement. It must not contain model-specific decisions outside the example generator.

The Operator provides the framework configuration as environment variables, following the existing mock Translator process. `NATS_URL` identifies the NATS server. `TRANSLATOR_STREAM`, `TRANSLATOR_REQUEST_SUBJECT`, `TRANSLATOR_READY_SUBJECT_TEMPLATE`, and `TRANSLATOR_CONSUMER` identify the exact alpha4 JetStream stream, request filter, ready-subject template, and UID-specific durable consumer. `SIMULATIONPROJECTNAMESPACE`, `SIMULATIONPROJECTNAME`, and `SIMULATIONEXPERIMENTUID` identify the owning experiment. `REPOSITORY` is the configured target repository and `BASEIMAGE` is the digest-pinned `spec.translator.baseimage` value. The template fails startup with a descriptive configuration error when any required value is missing, malformed, or inconsistent with the alpha4 subject grammar. It creates neither shared stream nor Scenario Manager consumer.

The framework derives the ready subject from the configured template and the validated request scenario ID. It must publish only within its injected namespace and project. The stream, request subject, ready-subject template, and consumer naming are configuration, not model-generator responsibilities.

The replaceable Go generator boundary has this conceptual shape; the implementation may place these types in an internal package, but it must preserve this dependency direction:

```go
type Generator interface {
	Generate(context.Context, GenerationInput) error
}

type GenerationInput struct {
	ScenarioID         int
	TranslationAttempt int
	RecipeInfo         json.RawMessage
	ConfidenceMetric   *float64
	BaseImage           string
	Workspace           string
	ResultDatabase      ResultDatabaseConfig
}

type ResultDatabaseConfig struct {
	Host     string
	Port     int
	DBName   string
	User     string
	Password string
}
```

`Generate` writes a complete Docker build context into `Workspace`: generated model files, scenario configuration, dependencies, an entrypoint, and a Dockerfile. It does not consume NATS messages, call BuildKit, push images, publish ready messages, or acknowledge deliveries. Those remain framework responsibilities so a user can replace model generation without reimplementing the handoff protocol.

The included example generator uses the smoke EDS recipe shape already established in this repository. It requires `recipe_info` to be a JSON object with a non-empty string `scenario` and a non-negative integer `seed`; unknown fields are ignored so the example remains usable with richer user recipes. Missing fields, wrong types, an empty `scenario`, or a negative `seed` are generation failures and follow the empty-image ready workflow.

The example generator writes the validated values to `scenario.json` in the build context and packages a small, checked-in SimPy model from the template source. That model initializes its pseudo-random input from `seed`, executes five deterministic timeout events, and reports the scenario name, seed, event count, and final simulated time. The generated Dockerfile uses the required digest-pinned `baseimage`, installs the pinned SimPy dependency, copies only the generated runner files and connection configuration, sets a numeric non-root user, and defines the model launcher as its entrypoint. The generated image therefore starts the baked scenario without command, arguments, or environment variables from SM.

At startup, the template reads exactly `host`, `port`, `dbname`, `user`, and `password` from the files mounted at `/resultdb-connection` and reads the registry configuration mounted at `/registry-auth/config.json`. It rejects missing or malformed values before handling requests and passes a validated `ResultDatabaseConfig` only to the generator. It does not watch, reload, or rotate either credential set while running. Secret updates after startup have no defined effect until the Translator Pod restarts.

The example generator bakes the connection configuration and a replaceable result-sink interface into the runner image. This retains the trusted prototype assumption that the generated image contains credentials: neither the framework, generator, BuildKit progress handling, nor runner may print those values or expose them in image labels or annotations. Credentials must not be added to NATS messages, Scenario Status, image labels, or annotations.

The example runner connects to PostgreSQL and creates one minimal, user-replaceable table for its scenario before writing its result. The table name is derived only from the already validated positive integer scenario ID:

```sql
CREATE TABLE IF NOT EXISTS scenario_<scenario-id>_results (
    id BIGSERIAL PRIMARY KEY,
    result JSONB NOT NULL
);
```

After a successful simulation, each runner appends one JSONB `result` containing exactly `scenario`, `seed`, `events_processed`, and `simulated_time`. PostgreSQL's sequence orders concurrent inserts. Repetitions have no identifier or uniqueness constraint. A simulation failure performs no result insert. A Result DB connection, table-creation, or insert failure makes the runner exit unsuccessfully so the Job becomes `Failed`. A user-defined generator may replace the per-scenario schema and sink independently, but it must preserve the generated image's ownership of runner startup and result handling.

The namespace-aware subjects above replace the project-only subject names; the existing request and ready JSON payloads remain unchanged. The template uses the established JetStream publish-confirmation and poison-message rules. It allows at most one active request and build. A successful digest ready message, or a confirmed empty-image failure ready message, is published with JetStream confirmation before the request is acknowledged.

After BuildKit confirms a successful push and Translator resolves the digest, Translator atomically writes a credential-free `ready-outcome.json` in the attempt workspace containing scenario ID, translation attempt, deterministic tag, digest, and ready subject. It retains that workspace until the ready publish is confirmed and the request is acknowledged. A publish retry reuses this exact outcome. On redelivery or process restart, Translator first reads the marker. If the marker was lost because the Pod was replaced, it resolves the deterministic tag from the registry. An existing valid tag is treated as the successful build outcome and is published without generator or BuildKit execution. Only an attempt with neither a valid marker nor a resolvable pushed tag may generate and build again.

### Required Translator template documentation

`component-templates/translator/README.md` is a mandatory, detailed integration guide. It is part of the template's supported interface, not optional explanatory material. It must give a user enough information to replace the example generator without changing the framework-owned communication or build protocol.

The guide must document:

- every required environment variable, its source, expected format, and startup validation; the mounted registry and Result DB Secret paths; and the exact required keys in each Secret;
- the namespace-aware request and ready subjects, durable-consumer name, unchanged request and ready JSON payloads, serial delivery, publish-confirmation-before-acknowledgement rule, and permanent poison-message handling;
- the framework-versus-generator ownership boundary, the `Generator` and `GenerationInput` contract, and how a replacement generator produces a complete build context without consuming NATS, invoking BuildKit, or publishing a ready message;
- the `/workspace` attempt layout, cleanup and redelivery behavior, `/run/buildkit/buildkitd.sock`, UID/GID and `fsGroup` requirements, registry authentication, deterministic tag, push, and digest-resolution process;
- pushed-outcome persistence and recovery, including the rule that a ready-publish retry always reuses the already-pushed digest and never rebuilds that attempt;
- the self-contained runner-image contract: the image owns its executable, dependencies, entrypoint, simulation behavior, and result handling even when no custom Job template is supplied;
- how optional sidecars, init containers, ConfigMap and Secret volumes, and other permitted Job-template fields complement rather than replace the runner image's capabilities;
- the non-root execution contract for the runner and all auxiliary containers;
- how the example generator reads the Result DB configuration, embeds it in the generated runner, creates `scenario_<scenario-id>_results`, and appends one JSONB row after each successful repetition;
- how users replace the example generator and result schema while preserving the framework's required runtime configuration, acknowledgement protocol, and immutable-digest handoff; and
- the explicit trusted-prototype limitation that generated images contain PostgreSQL credentials, plus deferred hardening such as secret-at-runtime delivery, untrusted-build isolation, and build egress controls.

### Runner Job customization contract

`spec.runner.jobTemplate` is optional. It is a typed `batch/v1.JobTemplateSpec`, not raw YAML and not a ConfigMap containing a Job. The built-in template is used when the field is absent. A supplied template must contain exactly one container named `runner`; it may add sidecars, init containers, runner environment and resource settings, volume mounts, ConfigMap or Secret volumes, metadata annotations, node selectors, tolerations, affinity, topology constraints, and other ordinary Kubernetes scheduling settings.

SM constructs an effective Job by copying permitted template fields and then enforcing the fields owned by CBSE. Users cannot override the Job name, namespace, owner reference, identity labels, non-indexed completion mode, completions, parallelism, `backoffLimit: 0`, Pod `restartPolicy: Never`, runner image, image pull Secret, runner ServiceAccount, disabled service-account token mounting, or required security settings. The Operator rejects a template that conflicts with these protected fields and moves the experiment to provisioning `Error`; SM repeats the same validation before any Job create call and treats a conflict that escapes provisioning as a permanent scenario startup failure.

Every runner, sidecar, and init container must declare or inherit non-root-compatible settings. The effective Pod uses `runAsNonRoot: true` and `RuntimeDefault` seccomp. Every container disables privilege escalation and drops all Linux capabilities. Privileged containers, host namespaces, host paths, and service-account token mounting remain prohibited. The accepted runner digest always replaces the custom template's `runner` image. The image must work with the built-in template; user customization supplies optional integration and scheduling concerns, not missing executable behavior.

SM computes a deterministic hash of the validated effective Job contract and stores it in an annotation. An `AlreadyExists` Job is accepted only when its experiment owner UID, identity labels, protected fields, and template hash match. SM never adopts or rewrites a mismatched Job.

### Scheduler boundary and RBAC

SM places Job creation and observation behind a small internal scheduler interface. The interface ensures one workload from a validated runner projection and reports successful repetition count, active state, and terminal success or failure. This branch provides only the `batch/v1` Kubernetes Job adapter. Alpha4 has no scheduler-provider selection field. A future scheduler adapter must add its own explicit configuration, resources, and additive RBAC without broadening the base permissions speculatively.

The default SM ServiceAccount receives only these workload permissions:

- `get`, `list`, and `watch` on alpha4 `simulationexperiments`; and
- `create`, `get`, `list`, and `watch` on `batch/jobs`.

A namespace-scoped SM uses a Role and RoleBinding in its watched namespace. A cluster-wide SM uses a ClusterRole and ClusterRoleBinding with the same resources and verbs. SM receives no Pod, ConfigMap, Secret, Job update, Job patch, Job delete, or RBAC-management permission. ConfigMap and Secret volumes are resolved by Kubernetes and do not require SM to read those objects. Per-experiment runner ServiceAccounts have no RoleBinding and set `automountServiceAccountToken: false`.

Kubernetes HTTP `403 Forbidden` means the authenticated SM ServiceAccount is not authorized for the attempted verb, resource, or namespace. It is an operational installation or RBAC error, not evidence of scenario failure. SM logs those three dimensions, leaves scenario state and experiment phase unchanged, marks readiness unhealthy, and retries with context-aware exponential backoff starting at one second and capped at 30 seconds. A later successful Kubernetes operation clears this RBAC readiness failure. SM does not translate `Forbidden` into `Failed` or `Error`.

### Configuration validation and failure classes

The implementation must not use “invalid configuration” as an undifferentiated error. Validation is grouped and classified as follows:

1. **Alpha4 experiment provisioning.** The Operator rejects an invalid namespace or experiment name; a non-digest Translator, Builder, or base image; a tagged or digested `translator.repository`; a missing registry Secret reference; a missing, wrong-type, or malformed Docker configuration Secret; invalid Builder resource quantities; or a runner Job template that violates its protected fields or non-root contract. The Operator reports the exact field and moves provisioning to `Error`.
2. **Translator startup.** Translator refuses readiness for a missing or malformed NATS URL, stream, request subject, ready-subject template, consumer name, experiment identity, repository, or base image; missing or malformed registry or Result DB Secret files; a non-numeric or out-of-range Result DB port; or an unwritable workspace. The error names the setting or mounted path without printing its value when it may contain credentials.
3. **Scenario Manager startup.** SM fails startup for a malformed Core DB DSN, incompatible required table schema, invalid watch namespace, invalid NATS subject template, or invalid configured stream or consumer name. It reports the exact setting or schema check before starting informers, consumers, or scenario selection.
4. **Runtime dependencies.** A syntactically valid but unavailable NATS server, registry, Result DB, Kubernetes API, or BuildKit socket is a retryable dependency failure. Kubernetes `Forbidden` uses the operational RBAC behavior above. These failures are not relabeled as configuration errors.
5. **Messages and scenario work.** Invalid subject shape, payload shape, attempt identity, repository, or scenario-specific generator input follows the poison-message or guarded scenario-failure rules defined by the owning workflow. It is not a process startup configuration failure.

### Lightweight scenario observability

Scenario execution uses concise, credential-free logs and Kubernetes' existing Job and Pod diagnostics. SM logs Job creation or adoption and one terminal scenario outcome. Those records include experiment namespace and name, scenario ID, translation attempt, Job name, requested repetitions, computed repetitions, outcome, and a short reason. SM does not log every successful repetition or every unchanged observation poll.

The basic generated runner emits no framework debug stream and no per-simulation-event log. On a runner-controlled failure, it emits one timestamped terminal record containing scenario ID, Pod hostname, failure stage, and a sanitized reason, then exits non-zero without writing a result row. The Pod hostname is an infrastructure diagnostic identity only; it does not create a repetition identity in the Core DB or Result DB. Failures before the runner starts remain visible through standard Job conditions, Pod status, and Kubernetes events. SM does not gain Pod-read RBAC for logging.

Automatic collection, centralized storage, size limits, retention periods, and deletion of old scenario logs are future operational work. This branch keeps completed and failed Jobs available for inspection and does not implement log garbage collection.

### State boundary

This branch implements this prefix of the scenario diagram:

```text
Created -> Scheduled -> StartingRunners -> InProcessing
                                               | Job success
                                               v
                                        PostProcessing

InProcessing --Job failure--> Failed
```

`Scheduled -> StartingRunners` remains owned by accepted Translator ready handling. `StartingRunners -> InProcessing` is owned by SM after a matching Job exists. SM then observes that exact Job. Job success owns `InProcessing -> PostProcessing`; Job failure owns `InProcessing -> Failed`. `PostProcessing` is a boundary state in this branch. SM does not invoke the PostProcessingService or move a scenario from `PostProcessing` to `StartingRunners` or `Finished`.

The experiment diagram remains unchanged. The Operator moves an alpha4 experiment through `Pending`, `Provisioning`, and `InProgress` when components are ready, and may set `Error` for a provisioning problem. The intended future owner of execution-level `Failed` and `Completed` transitions is SM. This branch does not implement those two experiment transitions. It only observes `Error`, `Failed`, and `Completed` to run terminal artifact cleanup.

### Runner-start projection and validation

SM reads one runner-start projection from the Core DB: scenario ID, `number_of_reps`, `number_of_computed_reps`, persisted runner digest, translation attempt, and project namespace, name, and UID. It then gets that alpha4 experiment from Kubernetes and requires the same UID and phase `InProgress`. It revalidates the runner digest and requires its normalized repository to equal `spec.translator.repository` before creating a Job.

`number_of_reps` must be between `1` and `math.MaxInt32`, and `number_of_computed_reps` must initially be zero for this execution. The runner digest, registry Secret, runner template, and ServiceAccount must be valid before Job creation. The Job uses the experiment's registry Secret as `imagePullSecrets`.

The Job and its Pod template must carry these labels:

- `experiment.cbse.terministic.de/project=<experiment-name>`
- `experiment.cbse.terministic.de/experiment-uid=<full-UID>`
- `experiment.cbse.terministic.de/scenario-id=<decimal-scenario-id>`
- `experiment.cbse.terministic.de/translation-attempt=<decimal-attempt>`

Before accepting `AlreadyExists`, SM compares the owner UID, these labels, runner image, non-indexed completion mode, completions, parallelism, restart policy, backoff limit, ServiceAccount, image pull secret, token setting, pod security settings, and effective-template hash. A mismatch is a collision and is never adopted.

### Startup result handling

SM creates or confirms the Job before calling the guarded `StartingRunners -> InProcessing` update. If the update affects zero rows, SM logs a stale result and does not change the Job. This is expected when another worker confirmed the same Job and won the transition.

The following are permanent startup failures. SM calls the guarded `StartingRunners -> Failed` transition: invalid repetition state, image, image repository, or runner template; missing or wrong-type pull Secret; missing, deleting, terminal-phase, or UID-mismatched experiment; and a Job collision.

Cancellation, deadlines, Kubernetes transport errors other than `Forbidden`, and an experiment in an empty, `Pending`, or `Provisioning` phase are retryable. SM returns an error and leaves the scenario in `StartingRunners`. `Forbidden` follows the operational RBAC behavior above and cannot cause a domain-state transition.

### Runner terminal observation

Runner observation is independent of selection and translation. A small process-local monitor discovers `InProcessing` rows and reconciles their deterministic Jobs. It must not make `InProcessing` part of the ordinary lowest-ID actionable query because a running Job must not block later scenarios from translation or runner startup.

The observation projection contains the scenario ID, requested and computed repetition counts, translation attempt, project namespace, project name, experiment UID, and the fields needed to verify the deterministic Job identity. The scheduler adapter gets the exact Job and confirms that it still matches the projection before applying progress or a terminal result. SM does not watch Pods.

The result rules are:

- For every observation, SM atomically sets `number_of_computed_reps` to `GREATEST(current_value, LEAST(job.status.succeeded, number_of_reps))` while guarding scenario ID and state `InProcessing`. The value never decreases or exceeds `number_of_reps`, so informer redelivery and SM restart cannot double-count a Pod.
- A failed Pod does not increment the counter. A Job with Kubernetes `Failed=True` first preserves the observed partial successful count, then causes a guarded `InProcessing -> Failed` update.
- A Job with Kubernetes `Complete=True` first records `number_of_computed_reps = number_of_reps`, then causes a guarded `InProcessing -> PostProcessing` update.
- A Job without either terminal condition remains `InProcessing` without error.
- A missing Job, cancellation, deadline, or Kubernetes transport failure other than `Forbidden` is retryable and leaves the row `InProcessing`. This branch does not recreate a Job after the scenario entered `InProcessing`.
- `Forbidden` leaves the row unchanged and activates the operational readiness and backoff behavior above.
- A Job that exists under the deterministic name but does not match the observation projection is a collision. SM applies the guarded `InProcessing -> Failed` transition and does not adopt the Job.
- A stale projection or zero-row guarded update is a successful no-op.

If an invalid Job reports both terminal conditions, SM treats `Failed=True` as failure and does not move the scenario to `PostProcessing`. `number_of_computed_reps` records Kubernetes-confirmed successful Pods; SM does not derive it from Result DB rows. Completed and failed Jobs remain present for inspection.

### Smoke registry cleanup

The smoke registry and repository are supplied by test configuration and are not fixed by this feature. The rendered alpha4 experiment sets `spec.translator.repository`, `spec.translator.image`, `spec.translator.builderImage`, and `spec.translator.registryAuthSecretRef` from that configuration. The referenced Secret contains the credentials used by Translator and BuildKit. The full smoke path uses the reference Translator's basic SimPy generator and rootless Builder sidecar; it does not use the synthetic Translator mock.

The harness records the experiment UID and uses the deterministic `runner-<UID-prefix>-s<scenario-id>-a<attempt>` tag prefix to identify only runner images created by that smoke run. An unconditional exit trap invokes a registry-specific cleanup adapter with the same registry authentication and deletes every matching runner tag or manifest whether the smoke test succeeds, fails, or retains Kubernetes resources for diagnosis. It never deletes shared Operator, SM, Builder, base, EDS, or Translator images.

Smoke preflight requires the configured registry cleanup adapter and readable credentials before mutation. Cleanup attempts every recorded image even when one deletion fails. Cleanup failure is written to the run artifacts and fails an otherwise successful smoke run; on an already-failed run it is reported in addition to the original failure. Selection of the concrete registry and its deletion implementation is deferred until the test registry is supplied, but unconditional scoped cleanup is an acceptance requirement. Before implementation handoff, this section must name the selected registry, supported tag or manifest deletion API, authentication method, and cleanup tool or client. The coding agent must not guess those provider-specific details.

### Verification and shutdown

Test alpha4-only CRD guidance; namespace/name/UID project identity; same-UID registration; different-UID replacement; stale UID update and deletion; DNS-label project-name validation; and the required Core DB schema failure for old tables. Verify that `scenario_status` receives no new identity columns. Test each named provisioning, Translator-startup, SM-startup, runtime-dependency, message, and scenario-work validation class above and assert its exact failure classification. Test Builder resource-limit defaults and valid user overrides. Test the absent default runner template and custom typed templates with sidecars, init containers, ConfigMap volumes, runner mounts, resources, and scheduling constraints. Reject protected-field overrides, missing or duplicate `runner` containers, and non-root violations.

Test the exact EDS and Translator subject grammar, strict placeholder validation, cluster-wide and namespace-scoped wildcard derivation, rejection of lossy or dotted identity, EDS subject/payload mismatch, exact namespace/name project lookup, Translator-ready identity mismatch, per-experiment durable naming, and isolation of same-named projects in different namespaces. Existing payload fixtures must continue to decode without namespace or UID fields.

Test cleanup on informer add for an already-terminal experiment and on transitions to `Error`, `Failed`, and `Completed`. Test exact consumer deletion and subject-filtered purges, repeated cleanup, missing artifacts, transient retry, and stale-UID refusal. Confirm that shared streams, SM consumers, and another experiment's messages remain unchanged.

Unit-test the template's runtime configuration, request and Result DB validation, generator invocation, serial processing, deterministic workspace and tag construction, BuildKit calls, registry authentication, digest validation, empty-image failure publication, and request acknowledgement ordering. After a successful push, force ready publication to fail and verify that retries and redelivery reuse the retained or registry-resolved digest with zero additional generator, build, or push calls. Repeat after a Translator process restart and after loss of the Pod-local marker. Cover each exact Translator startup setting and mounted path listed above. Verify that Secret changes are not reloaded during the process lifetime. Test valid and invalid example `recipe_info`; inspect the generated SimPy build context for its baked scenario and Result DB configuration, non-root entrypoint, per-scenario PostgreSQL result sink, and absence of logged credentials. Verify concurrent append-only inserts produce the expected row count and that a failed simulation writes no row. Verify that Translator and BuildKit can write their shared workspace as UID/GID `1000` with `fsGroup: 1000`.

Replace the synthetic Translator mock in the mandatory full smoke path with the reference template. The smoke path must demonstrate request consumption, example SimPy context generation, a rootless sidecar build selected by alpha4, authenticated registry push through the alpha4 registry Secret, immutable digest publication, runner Job creation, non-root model execution, PostgreSQL result persistence, and the scenario transition through `InProcessing` to `PostProcessing`. The smoke Result DB is PostgreSQL. Configure at least two repetitions, verify they run through one non-indexed Job, query exactly that many rows from `scenario_<scenario-id>_results`, and verify `number_of_computed_reps = number_of_reps`.

Test the exact Translator sidecar, default and overridden resource limits, volumes, UID/GID `1000` workspace access, socket, serial request handling, digest publication, empty-image failure handling, repository-mismatch rejection, and request acknowledgement ordering. Verify that Baseline and Restricted admission reject the required BuildKit profiles, that the smoke namespace uses the explicit admission exception, and that all runner-workload containers remain non-root and non-privileged. Test Job naming, labels, owner reference, non-indexed manifest shape, template hash, `N` parallel completions, zero retry limit, matching `AlreadyExists`, collision refusal, and Job-before-state ordering. Test active, partial-success, successful, failed, missing, stale, and contradictory Job observations; monotonic repetition updates; redelivery and restart safety; and preservation of partial counts on failure. Confirm that runner observation cannot block ordinary scenario selection. Include a maximum-valid `math.MaxInt32` repetition manifest unit test without creating Pods.

Test the internal scheduler interface with a fake adapter and the built-in Kubernetes Job adapter. Assert the exact namespace-scoped and cluster-wide RBAC resources and verbs. Simulate Kubernetes `Forbidden` for Job create and observation, verify that no scenario or experiment transition occurs, verify one-to-30-second bounded retry behavior and unhealthy readiness, then verify readiness recovery after a successful API operation.

Test lightweight scenario observability without requiring a logging backend: SM emits one creation or adoption record and one terminal record with the required scenario fields, the basic runner emits one sanitized failure record and no result row on controlled failure, success does not emit one framework log per repetition, and no credential or full recipe payload appears. Keep Pod-level startup failures available through standard Kubernetes diagnostics. Log retention and automatic deletion receive no implementation test in this branch.

The repository test pipeline must import only alpha4 experiment types in active Operator, SM, envtest, compatibility, and smoke paths. Generated CRDs, samples, fixtures, schemes, RBAC, preflight storage-version checks, and smoke assertions use alpha4. Tests assert alpha2 and alpha3 are not served or reconciled and never treat alpha4 as deprecated or transitional. Image publication builds the reference Translator instead of `trans-mock` and supplies the configured Builder digest and registry Secret. An unconditional cleanup test proves that runner images are removed after both a successful smoke run and a forced failure without deleting shared component images.

`make test-fast` must format, vet, compile, and test `component-templates/translator/` in addition to the existing Go modules. Run its unit tests with the race detector. A failure in the Translator module fails the repository-root target.

After implementation, run `make test-fast`. Because this changes Operator reconciliation, API/CRD, container images, Scenario Manager orchestration, and Kubernetes manifests, also run `make test-smoke` with an explicit `KUBECONFIG`, immutable test image digests, and the configured registry-auth file. Smoke preflight must require the already-installed alpha4-only CRD and must not delete, migrate, or downgrade the shared CRD. Do not add test artifacts to commits.
