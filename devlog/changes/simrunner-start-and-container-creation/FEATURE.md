# Specification for Alpha4 Simulation Runner Startup and Translator On-Demand Image Creation

This document has the following structure:

1. Mission Statement -- explains the intent and attitude of the work.
2. Scope -- explains the boundaries, goals, and explicitly chosen workflow.
3. Change Location -- identifies the code, API, and manifest areas expected to change.
4. Logic Description -- describes the implementation behavior and ownership boundaries.

## Mission Statement

This branch turns the Scenario Manager (SM) from a component that only advances a scenario to `InProcessing` into the component that actually starts the scenario's Simulation Runner workload on Kubernetes.

Translator remains an always-on, per-experiment service, provisioned and managed by the Experiment Operator. This branch does not replace it with a Job or change its NATS/JetStream request-consumer model. “On-demand container creation” means that Translator creates the executable simulation model and builds its Simulation Runner image only after it receives a request for a claimed scenario. It then returns the immutable runner image reference through the existing ready-message workflow, from which SM starts the runner on demand.

The implementation must make Kubernetes runner Jobs a projection of durable scenario state, not a second source of truth. Database state and guarded transitions decide ownership; workloads perform external work. The coding agent should favor small, explicit, idempotent behavior that is safe across SM restarts and retries while preserving the existing Translator handoff boundary.

The coding agent should favor clarity over cleverness: write small, explicit, modular code that follows nearby patterns. Each component should have one obvious responsibility, explicit dependencies, descriptive errors, and no hidden package-global state. Understand the existing lifecycle, ownership boundaries, and tests first; reuse suitable abstractions, and introduce new ones only to clarify a boundary or enable independent testing.

Work incrementally and stay within scope. Preserve the Translator request and ready-message contracts, durable Scenario Manager state machine, and Kubernetes ownership patterns unless this specification changes them. Avoid redesigns, speculative compatibility layers, and unrelated cleanup. Make retries, restarts, and `AlreadyExists` results deliberate and inspectable: confirm the exact expected resource or return a useful conflict; never silently adopt it. Validate image references, Secret types, namespaces, ownership, and lifecycle state at boundaries. Do not weaken security implicitly, hide privileged requirements, or treat a created Job or pushed image as proof of a successful simulation.

Write easily followable, high-verbosity documentation alongside the code. Explain the lifecycle, ownership, configuration, security and prototype assumptions, operational diagnosis, and deferred hardening so another engineer can safely operate and extend the change. Tests should explain behavior as well as cover it, including the normal path and the state-corruption or duplicate-work risks: stale claims, cancellation, malformed configuration, identity collisions, repeated reconciliation, partial external success, and cleanup.

## Scope

This change introduces the active `experiment.cbse.terministic.de/alpha4` `SimulationExperiment` API, defines the Translator image-build architecture, and implements Simulation Runner Job startup.

`alpha4` is the only reconciled and storage API version. The CRD continues to serve `alpha2` and `alpha3` only for controlled migration. A legacy-version reconciler must set `status.phase = Error` with a version-specific message instructing users to recreate the resource as alpha4. It must not add finalizers or create, update, or delete child workloads for alpha2 or alpha3.

### In Scope

1. **Alpha4 API and registry authentication.**
   - Carry the alpha3 user-facing schema forward into `api/alpha4`.
   - Add required `spec.translator.registryAuthSecretRef`, a same-namespace Secret name, and required `spec.translator.builderImage`, the rootless BuildKit sidecar image. The referenced Secret must be type `kubernetes.io/dockerconfigjson`.
   - The one referenced Secret supplies credentials for Translator/BuildKit image pushes and for Simulation Runner image pulls. It is mounted read-only only where required and referenced as the runner Job’s `imagePullSecret`.
   - Require digest references for `spec.translator.baseimage`, `spec.translator.builderImage`, and every runner image returned to SM. Registry access uses verified TLS; insecure registries and TLS-verification bypasses are prohibited.

2. **Existing Translator handoff and on-demand image creation.**
   - Retain the Experiment Operator's long-lived, per-experiment Translator Deployment, Service, ConfigMap, and readiness behavior.
   - Retain the guarded `Created -> Scheduled` claim, the existing NATS/JetStream translation request, request-publish confirmation, ready message, and existing attempt recovery logic.
   - For each received scenario/translation attempt, Translator creates the executable model, writes its generated source and Dockerfile into an isolated attempt workspace, builds and pushes the runner image, resolves it to a digest, and publishes the existing ready message with that digest.
   - A terminal build or registry-push failure is reported as the existing ready message with an empty image. SM therefore retains its existing exact-attempt recovery and maximum-attempt behavior without a new subject or payload field.

3. **Mandatory rootless BuildKit sidecar.**
   - Each alpha4 Translator Pod has exactly two mandatory containers: Translator and a rootless BuildKit sidecar. Translator owns request consumption, model generation, build submission, digest resolution, and ready-message publication. BuildKit is only the local OCI-image build engine.
   - The sidecar image is defined per alpha4 `SimulationExperiment` by required `spec.translator.builderImage`. The Operator validates that it is a non-empty, digest-pinned image reference before reconciling the Translator Deployment. This makes the Builder implementation part of the experiment contract while still rejecting mutable image tags.
   - Translator and BuildKit share an `emptyDir` build workspace and an `emptyDir` Unix-socket directory. Translator’s embedded compatible BuildKit client submits the attempt workspace to the local daemon over the shared socket; no TCP BuildKit endpoint is exposed.
   - A Translator Deployment performs one build at a time. It pushes to a deterministic project/scenario/translation-attempt tag, then resolves and publishes only the resulting immutable digest reference.
   - The BuildKit sidecar runs rootless and non-privileged, but receives the deliberately scoped compatibility exception required by the selected engine: `Unconfined` seccomp and AppArmor plus `--oci-worker-no-process-sandbox`. It must not use privilege escalation, privileged mode, host networking, host paths, or host container-runtime sockets. Translator retains the normal restricted security profile.
   - Generated source, Dockerfile, and build commands are trusted prototype input. Egress isolation and an untrusted-build sandbox are deferred to a later hardening feature.

4. **Simulation Runner startup.**
   - When BSSL selects `StartingRunners`, SM creates or confirms one deterministic `batch/v1` Simulation Runner Job for the project, scenario, and repetition, using the digest persisted by accepted Translator ready handling.
   - SM rejects a missing, deleting, or non-`InProgress` alpha4 experiment; an empty or non-digest runner image; and an existing Job whose labels, identity, image, or command contract does not match. It must not adopt collisions.
   - SM creates or confirms the runner Job before issuing the guarded `StartingRunners -> InProcessing` transition. A successful Job creation means only that work exists; completion/reporting remains out of scope and leaves the row `InProcessing`.
   - Runner Jobs are owned by the alpha4 `SimulationExperiment`, run in its namespace with a minimal service account, and use non-root execution where their image permits it, `RuntimeDefault` seccomp, disabled privilege escalation, and dropped capabilities.

### Workflow Decisions

| Concern | Decision for this branch |
| --- | --- |
| Active API | `alpha4`; alpha2 and alpha3 remain served legacy versions that receive only migration-required error status. |
| Translator lifetime | Existing always-on, per-experiment Deployment and Service. |
| Translator dispatch | Existing NATS/JetStream request publisher and shared Translator request consumer. |
| Build engine | One mandatory, rootless BuildKit sidecar per Translator Pod. |
| Sidecar control | Required digest from `spec.translator.builderImage`; the experiment selects the compatible rootless BuildKit sidecar image. |
| Build handoff | Shared `emptyDir` workspace and Unix socket; Translator uses its embedded BuildKit client. |
| Registry auth | Required `translator.registryAuthSecretRef` Docker-config Secret, used for image push and runner pull. |
| Build ordering | One serial build per Translator Deployment. |
| Image identity | Deterministic project/scenario/attempt tag for push; resolved immutable digest only in the ready message. |
| Build failure | Existing empty-image ready message and current attempt-recovery policy. |
| Runner dispatch | SM creates or confirms the Job, then makes the guarded transition to `InProcessing`. |
| Durable authority | PostgreSQL guarded transitions; Jobs are idempotent external effects. |

### Out of Scope

This branch must not add:

- active reconciliation or child-workload provisioning for alpha2 or alpha3;
- a per-scenario Translator Job or any other change to Translator’s process lifetime;
- a new NATS subject, ready-message payload, or JetStream acknowledgement model;
- runner result ingestion, repetition accounting, confidence calculation, or `InProcessing -> PostProcessing` completion logic;
- automatic retry, timeout recovery, cancellation, or failure classification for a created runner Job;
- concurrent Translator builds, object-storage/PVC build handoff, or remote BuildKit TCP access;
- privileged containers, host networking, host paths, container-runtime socket mounts, or insecure image registry/TLS exceptions;
- untrusted Dockerfile isolation or build egress controls; and
- deployment of test workloads in `default` or `kube-system`.

## Change Location

1. `experiment-operator/api/alpha4/`, generated CRD manifests, scheme registration, samples, and compatibility tests -- define alpha4, continue serving alpha2/alpha3 as legacy error versions, and add the registry Secret and Builder-sidecar image references.
2. `experiment-operator/internal/controller/` -- validate the experiment-defined Builder image; inject the Translator sidecar, workspace/socket volumes, security profiles, registry Secret mount, and image-pull Secret.
3. Translator implementation and tests -- generate attempt-scoped context, submit serial builds locally, push and resolve digest results, and signal terminal build/push failure with the unchanged empty-image ready message.
4. `scenario-manager/internal/core/selector.go`, `scenario-manager/internal/kube/`, and `scenario-manager/internal/coredb/` -- replace the runner placeholder with create-or-confirm Job behavior while preserving Translator attempt/publish guards.
5. RBAC and `test/e2e/manifests/` -- grant minimal runner-Job permissions and add digest-pinned Builder, Translator, and runner smoke images. The harness continues to create and remove only its `cbse-e2e-<run-id>` namespace.
6. `docs/project-status.md` and relevant developer documentation -- accurately describe Alpha4, the BuildKit sidecar, and runner startup once implemented.

## Logic Description

### API compatibility

Alpha4 is the Operator’s sole reconciliation authority and CRD storage version. Alpha2 and alpha3 remain served only so clients receive an explicit migration result. Their legacy reconcilers may update only status to `Error`; they never add finalizers or manage owned resources. This keeps no hidden conversion or mutation policy for existing user objects.

### Translator image-build workflow

```text
Created
  | existing guarded DB claim and NATS request publish
  v
Scheduled
  | always-on Translator receives request
  | creates model + attempt-scoped Docker context
  | local rootless BuildKit builds and pushes
  v
existing Translator ready message (immutable image digest)
  | existing guarded ready handling
  v
StartingRunners
```

1. BSSL claims one `Created` scenario and publishes the existing translation request for exact attempt `N`.
2. Translator serializes build handling, generates the simulation model and Docker context beneath its project/scenario/attempt workspace, and submits it through the shared Unix socket.
3. BuildKit uses the required digest-pinned base image and registry credentials, pushes to the deterministic attempt tag, and Translator resolves that tag to an immutable digest.
4. Translator publishes the current ready message containing project, scenario ID, attempt, and digest. Existing ready handling advances the exact `Scheduled` attempt to `StartingRunners`.
5. A terminal build or push failure emits the current empty-image ready message. Existing SM behavior returns the scenario to `Created` or marks it `Failed` at the configured attempt limit.
6. Existing request-publish confirmation and stale-unpublished recovery remain unchanged. This feature does not repurpose `translation_request_published_at`.

### Simulation Runner Job workflow

```text
StartingRunners
  | SM validates image and creates/confirms Job
  v
Simulation Runner Job exists
  | guarded DB transition
  v
InProcessing
```

SM derives a DNS-safe, deterministic Job name from the alpha4 experiment, scenario ID, and repetition. Before accepting `AlreadyExists`, it checks labels/annotations and the full expected workload identity. Only after create-or-confirm succeeds does it make the guarded state transition; a zero-row transition is a normal stale result and does not alter another scenario or Job.

### Verification and shutdown

Add alpha4 API/controller/CRD compatibility coverage; verify alpha2 and alpha3 legacy error-only behavior; test Builder image and Secret-reference validation; and verify sidecar security/volume shape, serial build behavior, digest publication, and empty-image failure signaling. Test runner Job idempotency, image validation, collision refusal, guarded ordering, and cancellation behavior.

After implementation, run `make test-fast`. Because this changes Operator reconciliation, API/CRD, container images, Scenario Manager orchestration, and Kubernetes manifests, also run `make test-smoke` with an explicit `KUBECONFIG`, immutable test image digests, and the configured registry-auth file. Do not add test artifacts to commits.
