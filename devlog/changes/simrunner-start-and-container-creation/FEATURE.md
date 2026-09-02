# Specification for Alpha4 Simulation Runner Startup and Translator On-Demand Image Creation

This root document defines the cross-cutting contracts for the feature. Detailed implementation requirements and acceptance tests are owned by the ordered [feature slices](#slice-dependency-map).

## Mission Statement

CBSE is a Kubernetes-native framework for executing simulation experiments: a `SimulationExperiment` custom resource defines an experiment, the Experiment Operator provisions its supporting services, and the Scenario Manager coordinates the scenario workflow, persists project and scenario state in the Core Database Service, and starts Simulation Runners from scenario-specific images produced by the Translator. Conceptually, an experiment progresses from `Pending` through `Provisioning` and `InProgress` to `Completed`, with provisioning errors leading to `Error` and execution failures to `Failed`; each scenario progresses from `Created` through `Scheduled`, `StartingRunners`, `InProcessing`, and `PostProcessing`, repeats runner execution until the required confidence is reached, and terminates in `Finished` or `Failed`. This feature implements the scenario lifecycle only through `PostProcessing`; confidence evaluation, additional execution rounds, `Finished`, and Scenario Manager-owned experiment completion or failure remain future work.

This branch turns the Scenario Manager (SM) from a component that only advances a scenario to `InProcessing` into the component that starts and observes the scenario's Simulation Runner workload on Kubernetes.

Translator remains an always-on, per-experiment service, provisioned and managed by the Experiment Operator. This branch does not replace it with a Job or change its NATS/JetStream request-consumer model. “On-demand container creation” means that Translator creates the executable simulation model and builds its Simulation Runner image only after it receives a request for a claimed scenario. It then returns the immutable runner image reference through the existing ready-message workflow, from which SM starts the runner on demand.

CBSE cannot provide one universal production Translator because translating a scenario into an executable model is user-defined. Instead, this branch provides a production-quality reference Translator template in `component-templates/translator/`. The template owns the stable NATS/JetStream, BuildKit, registry, digest, acknowledgement, and extension contracts. Its included SimPy generator is an intentionally small example and replacement point, not a production simulation model.

The implementation must make Kubernetes runner Jobs a projection of durable scenario state, not a second source of truth. Database state and guarded transitions decide ownership; workloads perform external work. The coding agent should favor small, explicit, idempotent behavior that is safe across SM restarts and retries while preserving the existing Translator handoff boundary.

The coding agent should favor clarity over cleverness: write small, explicit, modular code that follows nearby patterns. Each component should have one obvious responsibility, explicit dependencies, descriptive errors, and no hidden package-global state. Understand the existing lifecycle, ownership boundaries, and tests first; reuse suitable abstractions, and introduce new ones only to clarify a boundary or enable independent testing.

Work incrementally and stay within scope. Preserve the Translator request and ready-message contracts, durable Scenario Manager state machine, and Kubernetes ownership patterns unless this specification changes them. Avoid redesigns, speculative compatibility layers, and unrelated cleanup. A successful Kubernetes Job `CREATE` is authoritative and its returned object is not validated or compared. Make retries, restarts, and `AlreadyExists` results deliberate and inspectable: confirm the exact Job ownership identity before using an existing or later-retrieved resource; never silently adopt an unrelated Job. Validate image references, Secret types, namespaces, lifecycle state, and the ownership of `AlreadyExists`, observed, or ordinarily deleted resources at their boundaries. Do not weaken security implicitly, hide privileged requirements, or treat a created Job or pushed image as proof of a successful simulation.

Write clear documentation alongside the code. Explain the lifecycle, ownership, configuration, security and prototype assumptions, operational diagnosis, and deferred hardening. Tests must cover normal behavior, the exact validation groups defined below, stale claims, cancellation, identity collisions, repeated reconciliation, partial external success, and cleanup.

## Scope

This change introduces the only supported `SimulationExperiment` API version: `experiment.cbse.terministic.de/alpha4`. It defines the Translator image-build architecture, starts Simulation Runner Jobs, and observes their terminal result through the `PostProcessing` boundary.

`alpha4` is introduced by this feature as the only served, storage, and reconciled version. It is the active API, not a deprecated or transitional version. Only `alpha2` and `alpha3` are retired. There is no legacy reconciler, conversion webhook, compatibility mode, or automatic migration.

### Kubernetes compatibility

This feature supports conformant Kubernetes 1.x API servers at version 1.30 or any later minor release. Version 1.30 is the minimum; there is no feature-defined upper minor bound. The versions of the `k8s.io/*` Go modules selected by the repository are build dependencies, not the runtime support definition, an API-server version pin, or the boundary of the alpha4 Job-template API. Implementation code must use only Kubernetes APIs and required behavior available in 1.30, while remaining compatible with later 1.x servers. Distribution suffixes in `gitVersion`, such as K3s build metadata, do not change the major/minor decision.

The alpha4 Job-template surface remains the explicit allow-list below. A field exposed by a newer build-time Kubernetes Go struct is not automatically user-supported: it remains prohibited until this specification adds it. Conversely, compiling against a newer Go client does not permit rejecting an otherwise compatible Kubernetes 1.30 API server. The smoke harness must read the server version during preflight, require major version `1` and minor version at least `30`, impose no maximum-minor check, and fail before any cluster or registry mutation when the minimum is not met. Installation and testing documentation must state the same `>=1.30` contract.

### Upgrade from alpha2 or alpha3

This is a breaking upgrade. Before installing the alpha4-only CRD, operators must export the desired old resources, transform each manifest to alpha4 by supplying every new required Translator registry and Builder field, delete all old `SimulationExperiment` resources, and delete the old CRD. They then install the alpha4 CRD and create the transformed resources. The Operator and SM must never perform these destructive steps.

The shared smoke cluster follows the same alpha4-only contract but does not perform the breaking upgrade. The smoke harness must never delete or migrate the shared CRD. Its preflight requires the installed CRD to serve and store only alpha4. It fails before creating a test namespace when it finds alpha2, alpha3, another storage version, or incompatible `status.storedVersions`. A cluster administrator performs the one-time breaking upgrade outside the test harness. Fast envtest coverage installs the generated alpha4-only CRD in an isolated control plane and verifies that alpha2 and alpha3 are not served.

### In Scope

Detailed subsystem scope is owned by the seven slices. The following constraints apply across those boundaries:

   - `translator.image`, `baseimage`, `builderImage`, and a ready-message runner image must be OCI digest references in the exact form `name@sha256:<64 lowercase hexadecimal characters>`. Tags alone are invalid.
   - Namespace and project name are the routing identity for EDS and Translator communication. Alpha4 experiment names must be lowercase DNS labels of 1 to 63 characters matching `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`; dots are rejected even though Kubernetes otherwise permits them in names. Namespace and project are inserted into NATS subjects without lossy normalization.

### Global terminology

- **Experiment identity:** the namespace/name pair used for durable project identity and routing; the live Kubernetes UID distinguishes an incarnation.
- **Translator:** the always-on per-experiment service; [Slice 5](slices/05-reference-translator-runtime.md) owns its runtime contract.
- **Runner:** the scenario-specific executable image and its indexed Kubernetes Job; [Slice 6](slices/06-runner-job-orchestration.md) owns orchestration.
- **Successful CREATE:** the authoritative Kubernetes Job-create outcome defined under [Successful Kubernetes Job CREATE](#successful-kubernetes-job-create).

### Workflow Decisions

| Concern | Decision for this branch |
| --- | --- |
| Active API | Alpha4 only; alpha2 and alpha3 are retired through a manual breaking upgrade. |
| Messaging identity | Namespace and project are separate NATS subject tokens and resolve one exact project row. |
| Messaging payloads | Existing EDS and Translator JSON payloads remain unchanged. |
| Project incarnation | Namespace/name in Core DB; SM cleanup finalizer prevents same-name recreation until old durable state is deleted. |
| Translator lifetime | Existing always-on Deployment and Service per experiment. |
| Translator implementation | Go reference framework in `component-templates/translator/`; user-defined model generation is plugged into it. |
| Translator image | Repository-built static Go image from `component-templates/translator/Dockerfile`; the pushed digest is emitted as `TRANS_IMAGE` and used as `spec.translator.image`. |
| Translator configuration lifetime | Translator image, command, arguments, service type, node port, and port are immutable after alpha4 object admission; changing any of them requires deleting and recreating the experiment. |
| Example model | Scenario-specific single-server queue whose generator reads a positive integer `parameterset_id` lookup key from `recipe_info`, retrieves four integer parameters through the fixed Scenario Detail Database query, and writes them into the generated model. |
| Example PRNG | Each runner uses a local PRNG seeded from the Detail DB `seed_policy` plus its runtime Pod hostname, diversifying repetitions and retry Pods without a Core DB or payload change. |
| Build engine | One serial, rootless BuildKit sidecar per Translator Pod. |
| BuildKit image | Locked `linux/amd64` `moby/buildkit:v0.32.2-rootless` manifest digest from `BUILDER_IMAGE`; copied directly to `spec.translator.builderImage`. |
| Build handoff | Shared `/workspace` and `/run/buildkit` `emptyDir` volumes, UID/GID `1000`, `fsGroup: 1000`, and a Unix socket. |
| BuildKit startup gate | BuildKit must pass `buildctl ... debug workers`; Translator waits for the socket plus `ListWorkers` before attaching its request consumer, retrying `250ms`, `500ms`, `1s`, then every `2s`. |
| BuildKit resources | Optional `corev1.ResourceRequirements`; CPU and memory limits default independently to `1` CPU and `2Gi`, while requests are user-configurable and have no defaults. |
| Build result | Deterministic attempt tag, then immutable digest in the existing ready message. |
| Ready publish retry | Reuse the successful pushed digest; never regenerate, rebuild, or repush that scenario attempt. |
| Empty translation outcome | Persist and publish an empty ready; retry from `Created` until `SCENARIO_MANAGER_TRANS_MAX_ATTEMPTS` (default `3`), then fail the scenario. |
| Translation publish boundary | Persist publish-start before invoking NATS; refund only an exact claim for which publication definitely never began. |
| NATS security | Authentication-free in this branch, matching the current smoke deployment; exact subjects and ownership metadata are correctness checks, not broker-enforced ACLs. |
| Database configuration | Complete Detail and Result `DatabaseSpec` objects are immutable after alpha4 object admission; changing endpoints, credentials, or deployment settings requires deleting and recreating the experiment. |
| Database endpoints | One DNS/IPv4/IPv6 contract is implemented by the Operator availability probe, Translator Detail DB client, and generated runner Result DB client; only the latter two perform application work. |
| Credential lifetime | Registry, Scenario Detail DB, and Result DB credentials are loaded at Translator startup; live rotation and reload are out of scope. |
| Reference Detail DB image | Full smoke selects `scenario-detail-database`, publishes the unnormalized date tag from `TEST_IMAGE_VERSION`, and hands the resolved digest to rendering only as `DETAIL_DB_IMAGE`. |
| Runner base image | Repository-built from the locked Python 3.14.6 source with hashed SimPy 4.1.2 and Psycopg 3.3.4 packages; the pushed digest is emitted as `RUNNER_BASE_IMAGE` and used as `spec.translator.baseimage`. |
| Result DB image | Locked official PostgreSQL 18.6 `linux/amd64` manifest digest from `POSTGRES_IMAGE`; copied directly to `spec.resultDatabase.image`. |
| Runner unit | One customizable, indexed Job per scenario and translation attempt; one completion index per required repetition. |
| Runner Pod parallelism | `parallelism = completions = number_of_reps`. |
| Runner Pod parallelism limit | `number_of_reps <= 100000`, the Kubernetes Indexed Job limit; no smaller feature-specific cap. |
| Runner retry | One global Kubernetes Job retry budget with `backoffLimit: 4`; `backoffLimitPerIndex` is absent. |
| Runner-start reconciliation | SM discovers all `StartingRunners` rows immediately and every five seconds, de-duplicates them by positive scenario-status ID within the process, and dispatches currently eligible IDs in ascending order. This workflow is separate from the serial BSSL actionable query. |
| Runner-start concurrency | `SCENARIO_MANAGER_RUNNER_START_WORKERS` bounds simultaneous SM validation and Kubernetes Job `GET`/`CREATE` workflows only; default `4`, valid range `1..64`. It does not cap active Jobs or Pods. |
| Runner-start API retry | Transient Kubernetes get or create failures make the scenario eligible again after five seconds without a persisted counter or scenario-failure limit. The delay occupies no reconciler. |
| Runner observation cadence | One immediate discovery runs at SM startup, then all non-terminal `InProcessing` observations run in deduplicated five-second slices. Observation has no per-scenario exponential backoff. |
| Runner completion | Job success moves the scenario to `PostProcessing`; Job failure moves it to `Failed`. |
| Post-processing boundary | `PostProcessing` is reached but not processed in this branch. |
| Experiment terminal phases | Only `InProgress` admits messaging work. `Error` and `Failed` close the lifecycle gate, delete verified runner Jobs, and mark unfinished scenarios `Failed` while retaining diagnostic state. `Completed` closes only the gate and retains Jobs and durable state. Deletion closes the gate, deletes verified runner Jobs, removes project-scoped messaging artifacts, deletes the project row, and then removes the SM finalizer. Shared SM subscriptions remain active for other projects. |
| Example result sink | Each successful generated runner appends one JSONB result to a PostgreSQL table dedicated to its scenario. Writes are at-least-once; completion indexes are not persisted as result identities. |
| Scenario logging | Log scenario-level creation and terminal outcome plus one runner-controlled failure record per failed Pod attempt; do not log each successful repetition. |
| Durable authority | PostgreSQL guarded transitions; Jobs are idempotent external effects. |

### Registry Credential Boundary

The test pipeline publishes component images, including SM, EDS, and Translator, and shared base images to `registry.unibw.de/i31bdase/cbse-test`; it publishes the reference Detail DB image to `registry.unibw.de/i31bdase/cbse-test/scenario-detail-database` and generated smoke runner images to `registry.unibw.de/i31bdase/cbse-test-runner`. On a fresh registry, Harbor creates each repository record implicitly on its first push; an already-existing repository is equally valid. The harness does not create, delete, or otherwise manage repositories. It may push a canonical shared-image tag that already exists, allowing the registry's ordinary tag replacement behavior without a preliminary delete. Registry authentication is external operational state. The Harbor robot-account identifier, secret, source credential file, complete Docker configuration, and login procedure must never be committed, hard-coded, logged, rendered into test artifacts, or copied into this specification. The repository records only non-secret integration identifiers and requirements: the three registry repositories, Kubernetes Secret name and type, runtime input names, and required registry operations.

`cbse-registry-auth` is the sole registry-credential Secret name. It is a namespace-scoped `kubernetes.io/dockerconfigjson` Secret with a `.dockerconfigjson` key. Outside the smoke profile, the deployment owner supplies that Secret independently with credentials appropriate for the configured base and target repositories; this specification fixes its name and validation, not a production account. In the smoke profile, it is provisioned outside this repository from the protected Harbor robot-account credentials. The credential file and its source location are not repository inputs and must not be copied below the repository, including into an ignored directory. The smoke credential must authorize Repository Pull and Push and Artifact List, Read, and Delete in the Harbor project `i31bdase`. Harbor scopes the robot account's Artifact Delete capability to the project rather than to one repository: after that permission is enabled, the credential can delete artifacts from `cbse-test`, `cbse-test/scenario-detail-database`, and `cbse-test-runner`, as well as from any other repository in `i31bdase`. Keeping generated runners in `cbse-test-runner` is an organizational and cleanup-target boundary, not a registry authorization boundary. This project-wide artifact-deletion capability is an explicitly accepted smoke-account risk. Feature code may exercise it only through the annotation-verified `cbse-test-runner` cleanup path and must never delete an artifact from `cbse-test` or `cbse-test/scenario-detail-database`; no feature code uses project, project-metadata, whole-repository deletion, or update operations. Kubernetes uses the Secret as an image pull Secret for component and runner image pulls and for every Operator-created image-based Detail or Result DB Pod. Each database Deployment Pod template contains the exact entry `imagePullSecrets: [{name: cbse-registry-auth}]` once, including when the database image is public; a host-based database has no corresponding Pod template. On database Pods the Secret is available only to the kubelet for pulling the image and is never mounted or injected into PostgreSQL. Kubernetes does not push images: the Translator and its rootless BuildKit sidecar mount the Secret file read-only and use its credentials for authenticated base-image pulls and generated-runner pushes. “Read-only” describes the volume mount, not the credential's registry permissions. Because the same smoke credential also supports cleanup, those trusted smoke containers technically receive project-wide artifact-delete-capable credentials; this accepted smoke-only boundary must not be generalized into a production credential requirement. Translator and BuildKit must never invoke deletion or expose a deletion interface. Every `SimulationExperiment` must set `spec.translator.registryAuthSecretRef.name` to `cbse-registry-auth`, and the Secret must exist in that experiment's namespace before provisioning begins.

The smoke source Secret resides in `cbse-test-system`; the harness validates its type, `.dockerconfigjson` key, Docker JSON syntax, and `registry.unibw.de` basic-auth entry before copying it only into the ephemeral `cbse-e2e-<run-id>` namespace. The only filesystem credential input is the runtime environment variable `CBSE_REGISTRY_AUTH_FILE`, which must identify a protected Docker `config.json` supplied by CI or the operator and located outside the repository. The source Secret and `CBSE_REGISTRY_AUTH_FILE` must represent the same Harbor robot account. Before any registry or cluster mutation, the harness resolves the `registry.unibw.de` basic credentials from both Docker configurations with the canonical Docker resolver and requires the resolved username and password to match exactly; differing JSON serialization or unrelated registry entries do not constitute a mismatch. Missing, unsupported, or unequal resolved credentials fail preflight without logging which field differed or either credential value. `CBSE_REGISTRY` must equal `registry.unibw.de/i31bdase/cbse-test`, `CBSE_PULL_SECRET_NAME` must equal `cbse-registry-auth`, and `CBSE_PULL_SECRET_NAMESPACE` must equal `cbse-test-system` when those legacy variables are present; a different value fails preflight. The runner repository has no override variable. CI provides the protected Docker configuration only for the duration of a run; it authenticates component publication and generated-runner cleanup without being converted into a repository file. The harness must not print the credential-file path or contents. No registry or robot-account credential, registry Secret payload, runtime registry-credential file, or decoded registry credential may be committed, logged, included in manifests or artifacts, or added to `FEATURE.md`. This restriction is specific to third-party credentials and does not prohibit literal CBSE database credentials in the CR or smoke configuration. If a Kubernetes namespace is retained for debugging, the harness deletes its copied `cbse-registry-auth` Secret before returning and reports only that the credential was removed; the shared source Secret remains untouched.

### Out of Scope

This branch must not add:

- alpha2 or alpha3 serving, reconciliation, conversion, or migration automation;
- a per-scenario Translator Job or a change to Translator lifetime;
- in-place rollout or reconciliation of a changed Translator image, command, arguments, service type, node port, or port;
- new EDS or Translator JSON payload fields, or a new JetStream acknowledgement model beyond the namespace-aware subject change defined here;
- NATS authentication, per-client NATS identities, account or subject ACL provisioning, credential Secrets, tokens, NKeys, JWTs, credentials files, or credential rotation;
- a universal production model generator or production simulation semantics;
- runner result ingestion, confidence calculation, PostProcessingService invocation, or any ordinary scenario transition out of `PostProcessing`; the experiment-level `Error`/`Failed` bulk transition to scenario `Failed` is the sole exception;
- aggregation of scenario states into `SimulationExperiment.status.phase`, including SM-owned transitions to experiment phase `Failed` or `Completed`;
- a later execution/run identity for a repeated scenario after post-processing;
- PostProcessingService-driven `n+m` repetitions or any increase of `number_of_reps` after the original EDS batch;
- workload replacement, cancellation recovery, or self-healing beyond the global Kubernetes Job retry budget defined here;
- a second scheduler implementation or alpha4 scheduler-provider selection field;
- concurrent Translator builds, object-storage/PVC build handoff, or remote BuildKit TCP access;
- automatic registry, Scenario Detail DB, or Result DB credential rotation, file watching, or live credential reload;
- production lifecycle deletion or garbage collection of generated runner images;
- Translator-consumer deletion or subject purging for an `Error`, `Failed`, or `Completed` experiment before CR deletion, periodic orphan-consumer sweeping, and automatic recovery of consumers orphaned before the SM finalizer was installed;
- centralized scenario-log storage, retention limits, or automatic deletion of past logs;
- privileged containers, host networking, host paths, container-runtime socket mounts, insecure registries, or registry TLS-verification bypasses;
- PostgreSQL transport encryption, certificate or hostname verification, CA or client-certificate distribution, or database TLS configuration fields; alpha4's reference PostgreSQL clients use `sslmode=disable` on the trusted prototype network;
- mutation of Scenario Detail DB parameter rows, parameter snapshots or versions, and reload of a translated parameter set after image generation;
- untrusted Dockerfile isolation or build egress controls;
- an EDS template, EDS-side repetition policy, runner parallelism cap below Kubernetes' Indexed Job limit, or SM-side runner throttling;
- CPU or memory pressure prediction, Metrics Server integration, SM capacity admission, SM-side Pod placement, or any other SM-side cluster-compute scheduling decision;
- SM HTTP health endpoints, Kubernetes readiness or liveness probes, and external monitoring integration; or
- deployment of test workloads in `default` or `kube-system`.

## Slice dependency map

| Slice | Depends on |
| --- | --- |
| [01 — Alpha4 API and CRD](slices/01-alpha4-api-and-crd.md) | None |
| [02 — Job-template policy](slices/02-job-template-policy.md) | Slice 01 |
| [03 — Operator provisioning](slices/03-operator-provisioning.md) | Slices 01 and 02 |
| [04 — SM messaging and lifecycle](slices/04-sm-messaging-and-lifecycle.md) | Slice 01 |
| [05 — Reference Translator runtime](slices/05-reference-translator-runtime.md) | Slices 03 and 04 |
| [06 — Runner Job orchestration](slices/06-runner-job-orchestration.md) | Slices 01, 02, and 04 |
| [07 — Images, smoke, and documentation](slices/07-images-smoke-and-documentation.md) | Slices 01 through 06 |

## Alpha4 cutover checkpoint

Slices may prepare additive alpha4 work before the integration checkpoint. Active schemes, CRD serving and storage, Scenario Manager imports, fixtures, and smoke manifests switch together in Slice 07. The integrated result has no mixed-version or compatibility mode.

## Change Location

Component and file ownership is recorded in each slice's **Files and components owned** section.

## Logic Description

The normative behavior is partitioned across the seven slices above. Cross-slice consumers link to the owning contract instead of copying it.

### Configuration validation and failure classes

The implementation must not use “invalid configuration” as an undifferentiated error. Validation is grouped and classified as follows:

1. **Alpha4 experiment admission and provisioning.** The Kubernetes API server rejects an update that adds, removes, or changes any immutable alpha4 field listed above, including the Translator image, command, arguments, service type, node port, and port; the rejected update is never persisted, never starts an in-place Translator rollout, and therefore does not cause an Operator-owned phase transition. For an admitted new object, the Operator rejects an invalid namespace or experiment name; an invalid Detail or Result `DatabaseSpec`; a non-digest Translator, Builder, or base image; a tagged or digested `translator.repository`; a `registryAuthSecretRef.name` other than `cbse-registry-auth`; a referenced Secret that does not yet exist in the experiment namespace; a wrong-type or malformed Docker configuration Secret; a missing or invalid basic-auth entry for a required registry host; invalid Builder request or limit quantities; or a runner Job template containing an unlisted field, an invalid allowed value, a protected-field override, or a non-root violation. The Operator reports the exact field or referenced object and moves provisioning to `Error` before creating experiment components.
2. **Translator startup.** Translator refuses readiness for a missing or malformed NATS URL, a NATS URL containing user information, stream, request subject, ready-subject template, consumer name, experiment identity, repository, or base image; missing or malformed registry, Scenario Detail DB, or Result DB Secret files; a non-numeric or out-of-range database port; or an unwritable workspace. The reference template accepts no NATS credential setting or mount. A valid but not-yet-ready BuildKit socket or worker is a retryable startup dependency: Translator waits under the mandatory admission gate without attaching its request consumer. The error names the setting or mounted path without printing its value when it may contain credentials.
3. **Scenario Manager startup.** `SCENARIO_MANAGER_RUNNER_START_WORKERS` is optional; an absent value resolves to `4`, while a present value must be a base-10 integer in `1..64`. A malformed, zero, negative, or greater-than-64 value is a fatal startup configuration error. SM also fails startup for a malformed Core DB DSN, incompatible required table schema, an invalid or credential-bearing NATS URL, a non-empty legacy NATS username or password, invalid NATS subject template, invalid configured stream or consumer name, or a denied required cluster-wide Kubernetes authorization check. It reports the exact setting, schema check, or denied Kubernetes verb/resource before starting informers, consumers, scenario selection, runner-start discovery, or runner observation. After all startup dependencies, informers, consumers, selection, runner-start reconcilers, and observation workers start successfully, SM emits its existing `Scenario Manager is ready` log. The smoke harness continues to use this log as its application-start signal.
4. **Runtime dependencies.** A syntactically valid but unavailable NATS server or Kubernetes API is a retryable dependency failure. A database endpoint resolution or connection failure follows the policy of the client using the common endpoint contract: Operator keeps provisioning and repeats its availability probe, Translator applies its bounded Detail DB lookup retry and confirmed empty-image workflow, and runner exits unsuccessfully so the Result DB failure consumes the shared Job retry budget. Before Translator accepts a request, unavailable BuildKit is the retryable startup dependency governed by the admission gate; after it accepts a request, a registry, BuildKit, generator, push, or digest-resolution failure follows the confirmed empty-image workflow. Except for cleanup operations governed by their own retry contracts, a scenario-specific Kubernetes `Forbidden` or terminal Job failure transitions only that scenario to `Failed`; SM continues other work. None of these failures is relabeled as a startup configuration error.
5. **Messages and scenario work.** Invalid subject shape, payload shape, attempt identity, repository, or scenario-specific generator input follows the poison-message or guarded scenario-failure rules defined by the owning workflow. It is not a process startup configuration failure. A lifecycle rejection is classified separately: pre-`InProgress` work is transient and retryable, while `Error`, `Failed`, `Completed`, and deletion are permanent for message handling and use ACK-and-discard without message-driven scenario mutation. The independent `Error`/`Failed` terminal action may bulk-transition unfinished scenarios to `Failed` as lifecycle control.

### State boundary

This branch implements this prefix of the scenario diagram:

```text
Created -> Scheduled -> StartingRunners -> InProcessing
                          | startup failure       | Job success
                          v                       v
                       Failed             PostProcessing

InProcessing --Job failure--> Failed
```

`Scheduled -> StartingRunners` remains owned by accepted Translator ready handling. `StartingRunners -> InProcessing` is owned by SM after a matching Job exists. SM then observes that exact Job. Job success owns `InProcessing -> PostProcessing`; Job failure owns `InProcessing -> Failed`. `PostProcessing` is a normal-execution boundary state in this branch. SM does not invoke the PostProcessingService or move a scenario from `PostProcessing` to `StartingRunners` or `Finished`; only the experiment-level `Error`/`Failed` terminal action may move it to `Failed`.

The experiment diagram remains unchanged. The Operator moves an alpha4 experiment through `Pending`, `Provisioning`, and `InProgress` when components are ready, and may set `Error` for a provisioning problem. The intended future owner of execution-level `Failed` and `Completed` transitions is SM. This branch does not implement those two experiment transitions and never writes `SimulationExperiment.status`. On `Error` or `Failed`, it closes the lifecycle gate, deletes verified runner Jobs, and marks unfinished scenarios `Failed`; on `Completed`, it only closes the gate and retains diagnostic state. None of those three phases deletes or purges NATS artifacts. Deletion closes the gate, deletes verified Jobs, and then runs the destructive messaging and project cleanup defined above. No NATS or JetStream request is expected in `Completed`, and SM performs no scenario-work consumption in `Error`, `Failed`, `Completed`, or during deletion; terminal cleanup is lifecycle control rather than scenario-work consumption.

### Identity and lifecycle invariants

Namespace/name is the durable project and routing identity. The live experiment UID, exact owner reference, reserved workload labels, and the Scenario Manager cleanup finalizer protect incarnation-specific Kubernetes effects. Detailed messaging and deletion behavior is owned by [Slice 04](slices/04-sm-messaging-and-lifecycle.md).

### Successful Kubernetes Job CREATE

After a successful Kubernetes create, SM treats the result as authoritative and immediately calls the guarded `StartingRunners -> InProcessing` update. It does not validate or compare any returned field, does not invoke the ownership verifier, and retains only the returned UID process-locally for possible create/gate-closure cleanup. After `AlreadyExists`, it first applies the ownership check defined above. If the guarded update affects zero rows, SM logs a stale result and does not change the Job unless the lifecycle gate has closed as described below. This is expected when another worker confirmed the same Job and won the transition.

### Verification and shutdown

Slice-specific acceptance tests are colocated with their owning contracts. Repository-wide implementation acceptance uses the repository-root entry points:

```bash
make test-fast

make test-smoke \
  KUBECONFIG=/home/d4ns3u/.kube/config \
  TEST_IMAGE_VERSION=26.7.16 \
  CBSE_REGISTRY_AUTH_FILE=<protected-docker-config>
```

After implementation, run `make test-fast`, then run the mandatory `make test-smoke` with an explicit `KUBECONFIG`, the checked-in immutable source-image lock, and the protected registry-auth runtime input. The normal path builds and resolves all repository-owned output digests; a skip-build invocation must supply the complete validated output set. Smoke preflight must require the already-installed alpha4-only CRD and must not delete, migrate, or downgrade the shared CRD. Full feature acceptance requires the smoke suite to pass. Do not add test artifacts to commits.
