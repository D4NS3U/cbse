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

Write clear documentation alongside the code. Explain the lifecycle, ownership, configuration, security and prototype assumptions, operational diagnosis, and deferred hardening. Tests must cover normal behavior and duplicate-work risks: stale claims, cancellation, malformed configuration, identity collisions, repeated reconciliation, partial external success, and cleanup.

## Scope

This change introduces the only supported `SimulationExperiment` API version: `experiment.cbse.terministic.de/alpha4`. It defines the Translator image-build architecture, starts Simulation Runner Jobs, and observes their terminal result through the `PostProcessing` boundary.

`alpha4` is the only served, storage, and reconciled version. `alpha2` and `alpha3` are retired. There is no legacy reconciler, conversion webhook, compatibility mode, or automatic migration.

### Upgrade from alpha2 or alpha3

This is a breaking upgrade. Before installing the alpha4-only CRD, operators must export the desired old resources, delete all old `SimulationExperiment` resources, and delete the old CRD. They then install the alpha4 CRD and recreate the exported resources as alpha4. The Operator and SM must never perform these destructive steps.

The shared smoke cluster follows the same alpha4-only contract but does not perform the breaking upgrade. The smoke harness must never delete or migrate the shared CRD. Its preflight requires the installed CRD to serve and store only alpha4. It fails before creating a test namespace when it finds alpha2, alpha3, another storage version, or incompatible `status.storedVersions`. A cluster administrator performs the one-time breaking upgrade outside the test harness. Fast envtest coverage installs the generated alpha4-only CRD in an isolated control plane and verifies that alpha2 and alpha3 are not served.

### In Scope

1. **Alpha4 API, identity, and registry authentication.**
   - Carry the alpha3 user-facing schema into `api/alpha4` and add required `spec.translator.registryAuthSecretRef` and `spec.translator.builderImage`.
   - `registryAuthSecretRef` is the name of a same-namespace Secret of type `kubernetes.io/dockerconfigjson`. It supplies Translator build credentials and the runner Job image pull secret.
   - `baseimage`, `builderImage`, and a ready-message runner image must be OCI digest references in the exact form `name@sha256:<64 lowercase hexadecimal characters>`. Tags alone are invalid.
   - `translator.repository` is an OCI repository name without a tag or digest. Registry access uses verified TLS. Insecure registries and TLS-verification bypasses are prohibited.
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
   - A cluster-wide SM subscribes to `cbse.*.*.eds.scenarios.available`, `cbse.*.*.eds.scenarios`, and `cbse.*.*.trans.*.ready`. When `SCENARIO_MANAGER_WATCH_NAMESPACE` is set, replace the namespace wildcard with that exact validated namespace token. Never subscribe to or accept another namespace in namespace-scoped mode.
   - The Operator injects `SIMULATIONPROJECTNAMESPACE`, `SIMULATIONPROJECTNAME`, and `SIMULATIONEXPERIMENTUID` into the per-experiment EDS and Translator through the downward API. Each component publishes or subscribes only to subjects for that exact namespace/name pair.
   - SM removes the experiment's NATS and JetStream artifacts when it observes the experiment in `Error`, `Failed`, or `Completed`. Cleanup deletes the UID-specific Translator durable consumer and purges remaining EDS-batch, Translator-request, and Translator-ready messages for the exact namespace/project subjects. It never deletes a shared stream or an SM consumer.

3. **Reference Translator template and on-demand image creation.**
   - Add a Go reference implementation at `component-templates/translator/`. It is the supported integration template for user-defined Translators and replaces `test/mocks/translator` in the full smoke path.
   - Separate the implementation into three explicit layers: the CBSE-maintained reference framework, a replaceable model-generator module, and the generated scenario-specific runner image. Production-quality guarantees apply to the framework and its integration contracts, not to the example model's semantics.
   - The Operator mounts the existing `<experiment-name>-resultdb-sct` Secret read-only at `/resultdb-connection` for Translator.
   - The template strictly validates the established request payload and handles one request at a time. Its workspace is `/workspace/scenario-<scenario-id>/attempt-<attempt>`. Before generation it removes and recreates that exact attempt directory so redelivery cannot reuse stale build input.
   - The template uses the official BuildKit Go client over `unix:///run/buildkit/buildkitd.sock`. It submits the generated attempt directory as both the Dockerfile and build context, supplies registry credentials from `/registry-auth/config.json` through the BuildKit session, and requests a registry push to the deterministic tag.
   - The pushed tag is `<repository>:runner-<12-char-UID-prefix>-s<scenario-id>-a<attempt>`. The UID prefix is the first 12 lowercase hexadecimal characters of the UID after removing hyphens. Translator resolves this tag and publishes only the digest reference.
   - Translator publishes the ready message and waits for publish confirmation before acknowledging the request. A build, push, or digest-resolution failure publishes the existing ready message with an empty image and is acknowledged only after that publish succeeds. A ready-message publish failure leaves the request unacknowledged for redelivery.
   - After a terminal acknowledged outcome, the template removes the attempt workspace. If processing is interrupted or the ready publish is unconfirmed, redelivery safely regenerates the same attempt from a clean directory.

4. **Mandatory rootless BuildKit sidecar.**
   - Each Translator Pod has exactly two containers: `translator` and rootless `buildkit`.
   - They share `emptyDir` volumes mounted at `/workspace` and `/run/buildkit`. BuildKit listens only on `/run/buildkit/buildkitd.sock`; no TCP endpoint is exposed.
   - Both containers receive the registry Secret read-only at `/registry-auth/config.json`. The Translator also receives the result-database Secret read-only.
   - BuildKit runs rootless and non-privileged with the required compatibility exception: `Unconfined` seccomp and AppArmor plus `--oci-worker-no-process-sandbox`. It must not use privilege escalation, privileged mode, host networking, host paths, or host container-runtime sockets. Translator keeps the normal restricted security profile.
   - A namespace that enforces the Kubernetes Baseline or Restricted Pod Security Standard rejects this required BuildKit exception. The smoke harness therefore labels its isolated `cbse-e2e-<run-id>` namespace with `pod-security.kubernetes.io/enforce=privileged`. This admission label does not permit the implementation to set `privileged: true` or weaken any other container restriction. The harness keeps `audit` and `warn` at `restricted` so the exception remains visible.
   - Kubernetes 1.30 or newer is required so the BuildKit container can set `securityContext.appArmorProfile.type: Unconfined`. Cluster installation documentation must call out the namespace admission requirement and the AppArmor prerequisite.
   - Generated source, Dockerfile, and build commands are trusted prototype input. Egress isolation and an untrusted-build sandbox are deferred.

5. **Simulation Runner startup and terminal observation.**
   - For one accepted Translator image, SM creates or confirms one `batch/v1` Job for the scenario and translation attempt. The Job name is `simrun-<12-char-UID-prefix>-s<scenario-id>-a<attempt>`, using the UID-prefix rule above.
   - A Job has exactly one `runner` container. SM sets only its image; it does not set command, args, or runtime configuration. The generated image owns its entrypoint and result handling.
   - `spec.completions` and `spec.parallelism` both equal `scenario_status.number_of_reps`. Each Pod performs one repetition. All repetitions may run concurrently; a user-defined model and any durable result sink must support that concurrency.
   - This branch deliberately adds no lower operational cap below `math.MaxInt32`, no separate maximum parallelism, and no batching or throttling. A valid EDS message can therefore request a very large Job. The trusted-input prototype accepts that risk. A future EDS template will own policy that limits generated repetition counts.
   - Set `restartPolicy: Never`, `backoffLimit: 0`, and no TTL-after-finished field. Completed and failed Jobs remain visible.
   - The Job has the exact alpha4 experiment owner reference, runs in the experiment namespace, and uses the deterministic ServiceAccount `simrunner-<12-char-UID-prefix>`. The Operator creates this ServiceAccount, gives it no RoleBinding, and SM sets `automountServiceAccountToken: false` on the Job Pod.
   - The runner Pod uses `RuntimeDefault` seccomp, disabled privilege escalation, dropped capabilities, and non-root execution where its image permits it.
   - SM observes Jobs for scenarios in `InProcessing`. A successful Job causes the guarded transition `InProcessing -> PostProcessing`. A failed Job causes the guarded transition `InProcessing -> Failed`. This branch does not execute post-processing or move a scenario out of `PostProcessing`.

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
| Build handoff | Shared `/workspace` and `/run/buildkit` `emptyDir` volumes and a Unix socket. |
| Build result | Deterministic attempt tag, then immutable digest in the existing ready message. |
| Runner unit | One Job per scenario and translation attempt; one Pod completion per required repetition. |
| Runner concurrency | `parallelism = completions = number_of_reps`. |
| Runner concurrency limit | No feature-specific limit below `math.MaxInt32`; trusted EDS input is an explicit prototype assumption. |
| Runner retry | No Kubernetes retry: `backoffLimit: 0`. |
| Runner completion | Job success moves the scenario to `PostProcessing`; Job failure moves it to `Failed`. |
| Post-processing boundary | `PostProcessing` is reached but not processed in this branch. |
| Experiment terminal phases | SM observes `Error`, `Failed`, and `Completed` for cleanup, but does not produce those experiment phase transitions in this branch. |
| Example result sink | Structured JSON to standard output; PostgreSQL persistence is deferred until its schema and repetition identity are defined. |
| Durable authority | PostgreSQL guarded transitions; Jobs are idempotent external effects. |

### Out of Scope

This branch must not add:

- alpha2 or alpha3 serving, reconciliation, conversion, or migration automation;
- a per-scenario Translator Job or a change to Translator lifetime;
- new EDS or Translator JSON payload fields, or a new JetStream acknowledgement model beyond the namespace-aware subject change defined here;
- a universal production model generator or production simulation semantics;
- a durable Result DB schema or PostgreSQL result persistence for the example runner;
- runner result ingestion, confidence calculation, PostProcessingService invocation, or any scenario transition out of `PostProcessing`;
- aggregation of scenario states into `SimulationExperiment.status.phase`, including SM-owned transitions to experiment phase `Failed` or `Completed`;
- a later execution/run identity for a repeated scenario after post-processing;
- Kubernetes runner retries, timeout recovery, cancellation, or self-healing;
- concurrent Translator builds, object-storage/PVC build handoff, or remote BuildKit TCP access;
- privileged containers, host networking, host paths, container-runtime socket mounts, or insecure registry/TLS settings;
- untrusted Dockerfile isolation or build egress controls;
- an EDS template, EDS-side repetition policy, runner parallelism cap, or SM-side runner throttling; or
- deployment of test workloads in `default` or `kube-system`.

## Change Location

1. `experiment-operator/api/alpha4/`, CRD manifests, schemes, samples, and tests -- define alpha4 only, the registry fields, the UID environment value, the BuildKit sidecar, and the runner ServiceAccount.
2. `experiment-operator/internal/controller/` -- validate images and Secrets; reconcile the Translator and runner ServiceAccount with their exact volume, security, and ownership settings.
3. `scenario-manager/internal/nats/`, communication types, and EDS integration tests -- implement the namespace-aware EDS and Translator subject grammar, parsing, scoped wildcard subscriptions, exact namespace/name persistence lookups, and terminal experiment artifact cleanup without changing payload schemas.
4. `component-templates/translator/` and its tests -- implement the Go reference framework, namespace-aware request/ready subjects, per-experiment consumer, replaceable generator boundary, example SimPy generator, attempt workspaces, BuildKit client, authenticated push, digest resolution, and ready-message acknowledgement ordering. Retire `test/mocks/translator` from the full smoke path.
5. `scenario-manager/internal/core/`, `internal/coredb/`, and `internal/kube/` -- persist experiment identity, select the full runner-start and runner-observation projections, validate them, create or confirm Jobs before changing state, and apply terminal Job results.
6. The repository `Makefile` and test harness -- include the new Translator Go module in formatting, vet, unit, race, and compile checks run by `make test-fast`.
7. SM RBAC and `test/e2e/manifests/` -- grant only required alpha4 and Job permissions and provide digest-pinned Builder, Translator, and runner smoke images.
8. `docs/project-status.md` and developer documentation -- describe alpha4, namespace-aware messaging, the manual upgrade, BuildKit, runner startup, and the prototype credential assumption.

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

SM's translation claim projection carries raw project namespace and name. The Translator request publisher uses both to construct the exact request subject. The per-experiment Translator template validates its injected namespace and project at startup, subscribes only to that subject, and publishes ready messages only on the corresponding namespace/project ready subject. SM parses both tokens from a ready subject and requires them to match the scenario's persisted project row before applying empty-image recovery or a non-empty ready result. A wrong namespace or project is ACKed as poison and cannot consume an attempt or alter the row.

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

The template reads exactly `host`, `port`, `dbname`, `user`, and `password` from the files mounted at `/resultdb-connection`, rejects missing or malformed values before building, and passes a validated `ResultDatabaseConfig` only to the generator. The example generator bakes the connection configuration and a replaceable result-sink interface into the runner image. This retains the trusted prototype assumption that the generated image contains credentials: neither the framework, generator, BuildKit progress handling, nor runner may print those values or expose them in image labels or annotations.

For this branch, the example result sink emits one JSON object to standard output containing exactly `scenario`, `seed`, `events_processed`, and `simulated_time`, then exits successfully. It does not connect to PostgreSQL. Durable Result DB writes are deferred until a result schema and per-repetition identity are specified. A user-defined generator may replace the sink independently, but the mandatory template tests must use the provided logging sink and must not imply that logged output is durable simulation completion.

The namespace-aware subjects above replace the project-only subject names; the existing request and ready JSON payloads remain unchanged. The template uses the established JetStream publish-confirmation and poison-message rules. It allows at most one active request and build. A successful digest ready message, or a confirmed empty-image failure ready message, is published with JetStream confirmation before the request is acknowledged. If ready publication is not confirmed, the request remains unacknowledged and the next delivery recreates the same deterministic workspace and tag.

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

SM reads one runner-start projection from the Core DB: scenario ID, `number_of_reps`, persisted runner digest, translation attempt, and project namespace, name, and UID. It then gets that alpha4 experiment from Kubernetes and requires the same UID and phase `InProgress`.

`number_of_reps` must be between `1` and `math.MaxInt32`. The runner digest, registry Secret, and ServiceAccount must be valid before Job creation. The Job uses the experiment's registry Secret as `imagePullSecrets`.

The Job and its Pod template must carry these labels:

- `experiment.cbse.terministic.de/project=<experiment-name>`
- `experiment.cbse.terministic.de/experiment-uid=<full-UID>`
- `experiment.cbse.terministic.de/scenario-id=<decimal-scenario-id>`
- `experiment.cbse.terministic.de/translation-attempt=<decimal-attempt>`

Before accepting `AlreadyExists`, SM compares the owner UID, these labels, runner image, completions, parallelism, restart policy, backoff limit, ServiceAccount, image pull secret, token setting, and pod security settings. A mismatch is a collision and is never adopted.

### Startup result handling

SM creates or confirms the Job before calling the guarded `StartingRunners -> InProcessing` update. If the update affects zero rows, SM logs a stale result and does not change the Job. This is expected when another worker confirmed the same Job and won the transition.

The following are permanent startup failures. SM calls the guarded `StartingRunners -> Failed` transition: invalid repetitions or image; missing or wrong-type pull Secret; missing, deleting, terminal-phase, or UID-mismatched experiment; and a Job collision.

Cancellation, deadlines, Kubernetes transport errors, and an experiment in an empty, `Pending`, or `Provisioning` phase are retryable. SM returns an error and leaves the scenario in `StartingRunners`.

### Runner terminal observation

Runner observation is independent of selection and translation. A small process-local monitor discovers `InProcessing` rows and reconciles their deterministic Jobs. It must not make `InProcessing` part of the ordinary lowest-ID actionable query because a running Job must not block later scenarios from translation or runner startup.

The observation projection contains the scenario ID, translation attempt, project namespace, project name, experiment UID, and the fields needed to reconstruct and verify the deterministic Job identity. The monitor gets the exact Job and confirms that it still matches the projection before applying a result.

The result rules are:

- A Job with Kubernetes `Complete=True` causes a guarded `InProcessing -> PostProcessing` update.
- A Job with Kubernetes `Failed=True` causes a guarded `InProcessing -> Failed` update.
- A Job without either terminal condition remains `InProcessing` without error.
- A missing Job, cancellation, deadline, or Kubernetes transport failure is retryable and leaves the row `InProcessing`. This branch does not recreate a Job after the scenario entered `InProcessing`.
- A Job that exists under the deterministic name but does not match the observation projection is a collision. SM applies the guarded `InProcessing -> Failed` transition and does not adopt the Job.
- A stale projection or zero-row guarded update is a successful no-op.

If an invalid Job reports both terminal conditions, SM treats `Failed=True` as failure and does not move the scenario to `PostProcessing`. Job success does not imply durable result ingestion or successful post-processing. Completed and failed Jobs remain present for inspection.

### Verification and shutdown

Test alpha4-only CRD guidance; namespace/name/UID project identity; same-UID registration; different-UID replacement; stale UID update and deletion; DNS-label project-name validation; and the required Core DB schema failure for old tables. Verify that `scenario_status` receives no new identity columns. Test digest, Secret, and repetition validation; UID mismatch; and the permanent versus retryable startup paths.

Test the exact EDS and Translator subject grammar, strict placeholder validation, cluster-wide and namespace-scoped wildcard derivation, rejection of lossy or dotted identity, EDS subject/payload mismatch, exact namespace/name project lookup, Translator-ready identity mismatch, per-experiment durable naming, and isolation of same-named projects in different namespaces. Existing payload fixtures must continue to decode without namespace or UID fields.

Test cleanup on informer add for an already-terminal experiment and on transitions to `Error`, `Failed`, and `Completed`. Test exact consumer deletion and subject-filtered purges, repeated cleanup, missing artifacts, transient retry, and stale-UID refusal. Confirm that shared streams, SM consumers, and another experiment's messages remain unchanged.

Unit-test the template's request and Result DB validation, generator invocation, serial processing, deterministic workspace and tag construction, clean redelivery, BuildKit calls, registry authentication, digest validation, empty-image failure publication, and request acknowledgement ordering. Test valid and invalid example `recipe_info` and inspect the generated SimPy build context, including its baked scenario configuration, entrypoint, result-sink boundary, and absence of logged credentials.

Replace the synthetic Translator mock in the mandatory full smoke path with the reference template. The smoke path must demonstrate request consumption, example SimPy context generation, a rootless sidecar build, authenticated registry push, immutable digest publication, runner Job creation, model execution with structured JSON output, and the scenario transition through `InProcessing` to `PostProcessing`. It must not require or claim PostgreSQL result persistence.

Test the exact Translator sidecar, volumes, socket, serial request handling, digest publication, empty-image failure handling, and request acknowledgement ordering. Verify that Baseline and Restricted admission reject the required BuildKit profiles, that the smoke namespace uses the explicit admission exception, and that the containers remain non-privileged. Test Job naming, labels, owner reference, manifest shape, `N` parallel completions, zero retry limit, matching `AlreadyExists`, collision refusal, and Job-before-state ordering. Test active, successful, failed, missing, stale, and contradictory terminal Job observations. Confirm that runner observation cannot block ordinary scenario selection. Include a maximum-valid `math.MaxInt32` repetition manifest unit test without creating Pods.

`make test-fast` must format, vet, compile, and test `component-templates/translator/` in addition to the existing Go modules. Run its unit tests with the race detector. A failure in the Translator module fails the repository-root target.

After implementation, run `make test-fast`. Because this changes Operator reconciliation, API/CRD, container images, Scenario Manager orchestration, and Kubernetes manifests, also run `make test-smoke` with an explicit `KUBECONFIG`, immutable test image digests, and the configured registry-auth file. Smoke preflight must require the already-installed alpha4-only CRD and must not delete, migrate, or downgrade the shared CRD. Do not add test artifacts to commits.
