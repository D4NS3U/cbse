# Designing Custom CBSE Components

This is the starting point for developers who want to provide their own Experimental Design Service (EDS), Translator, or PostProcessingService image for CBSE. It explains what CBSE owns, what a component must own, and which integration contracts exist in the current `alpha3` prototype.

CBSE is still evolving. Treat the subjects and payloads below as the current contract, not as a promise of long-term API stability. In particular, EDS and Translator messaging are implemented today, while PostProcessingService communication is not.

## Start with the current boundary

The `SimulationExperiment` custom resource describes one experiment. The Experiment Operator turns that description into Kubernetes resources, while the Scenario Manager owns scenario state in PostgreSQL and coordinates EDS and Translator messages through NATS and JetStream.

```text
SimulationExperiment (alpha3)
        |
        v
Experiment Operator -----> EDS Pod
        |                  Translator Deployment + Service
        |                  PostProcessingService Deployment + Service
        |                  database workloads, Services, Secrets, ConfigMaps
        |
        +-----------------------------------------------+

EDS -- availability request --> Scenario Manager
EDS == scenario batch ========> Scenario Manager --> Core PostgreSQL DB
                                      |
                                      +== translation request ==> Translator
                                      <== ready message =========+
                                      |
                                      +--> durable scenario-state change

PostProcessingService is deployed, but is not called by Scenario Manager yet.
```

The arrows marked `==>` are persistent JetStream messages. The EDS availability handshake uses ordinary NATS request/reply. PostgreSQL is the authority for workflow ownership and progress; messages and Kubernetes workloads are effects of that state.

### Integration status

| Component | Provisioned by Operator | Usable integration today | Current limitation |
| --- | --- | --- | --- |
| EDS | One Pod, one Service, and an optional design ConfigMap | Availability request/reply and transactional scenario-batch ingestion | The design ConfigMap is created but not mounted; broker settings are not injected by the `alpha3` API |
| Translator | One Deployment, one Service, and one configuration ConfigMap | JetStream translation requests and ready messages | CBSE accepts a returned image reference but does not start that image |
| PostProcessingService | One Deployment and one Service | No service-call or message contract | Scenario Manager currently uses a local placeholder instead of calling the workload |

The Operator waits for these workloads to be ready before moving the experiment to `InProgress`. An EDS image must therefore remain running after it publishes its initial batches; a one-shot process causes its Pod to stop being ready.

## What every custom image receives

The current API is `experiment.cbse.terministic.de/alpha3`. Each component image is selected in the corresponding part of `spec`:

```yaml
apiVersion: experiment.cbse.terministic.de/alpha3
kind: SimulationExperiment
metadata:
  name: example-experiment
  namespace: simulations
spec:
  # Database fields are omitted here; they are still required by alpha3.
  translator:
    image: registry.example/translator@sha256:<digest>
    repository: registry.example/generated-runners
    baseimage: registry.example/runner-base@sha256:<digest>
    port: 8080
    serviceType: ClusterIP
  postProcessingService:
    image: registry.example/post-processing@sha256:<digest>
    port: 8080
    serviceType: ClusterIP
  experimentalDesignService:
    image: registry.example/eds@sha256:<digest>
    design: '{"method":"full-factorial"}'
    port: 8080
    serviceType: ClusterIP
```

The Operator injects `SIMULATIONPROJECTNAME` into all three containers. Its value is `metadata.name` from the `SimulationExperiment`.

The Translator additionally receives:

| Variable | Source |
| --- | --- |
| `REPOSITORY` | `spec.translator.repository` |
| `BASEIMAGE` | `spec.translator.baseimage` |

`command` and `args` can override an image's normal entrypoint for each component. Prefer an image-defined entrypoint for production use; overrides are most useful for controlled tests and migration.

The current `alpha3` API does **not** provide arbitrary environment variables, Secret mounts, resource settings, health probes, or Pod-template customization for these three components. It also does not inject NATS connection details into EDS or Translator. A custom image must either work with installation-known settings or wait for a future explicit configuration boundary. Do not put credentials into an image, `command`, `args`, the `design` string, or other non-Secret fields.

Setting `spec.experimentalDesignService.design` creates a ConfigMap containing an `experimentalDesign` key, but the current EDS Pod does not mount that ConfigMap. An EDS therefore cannot read this field today. Non-sensitive prototype settings can be expressed through component `args`; a generally configurable EDS needs an Operator/API enhancement that mounts or injects the design explicitly.

The component Services expose the configured `port`, but the implemented EDS and Translator workflows communicate through NATS rather than those Services. PostProcessingService has a Service, but no HTTP or messaging API is defined yet.

## How CBSE identifies an experiment

The raw experiment name is stored in PostgreSQL and injected as `SIMULATIONPROJECTNAME`. For current NATS subjects, Scenario Manager converts the name to lowercase, keeps ASCII letters, digits, `-`, and `_`, and replaces every other character with `-`.

For example, `example.v1` becomes `example-v1` in a subject. Because `alpha3` subjects do not contain the Kubernetes namespace, use an experiment name that is unique across the whole CBSE installation and already consists of lowercase letters, digits, and hyphens. This avoids both cross-namespace routing collisions and lossy normalization collisions.

Payload fields named `project` use the raw `SimulationExperiment` name. Subjects use the normalized token.

## Designing an Experimental Design Service

An EDS owns domain-specific experiment design: it turns the configured design into scenarios and submits those scenarios to CBSE. Scenario Manager owns persistence, initial state, scenario IDs, and later lifecycle transitions.

The EDS should:

1. Read and validate `SIMULATIONPROJECTNAME` and any configuration that is actually available to the process before doing external work.
2. Connect to the installation's NATS server with bounded retries.
3. Announce each pending batch with the availability handshake.
4. Publish the complete batch to the subject returned by Scenario Manager and wait for a JetStream publish acknowledgement.
5. Make retries safe and remain alive until Kubernetes terminates the Pod.

The repository's [EDS mock](test/mocks/eds/eds_mock.py) is executable protocol documentation. It is a test fixture, not a production template.

### 1. Announce availability

Send a NATS request to the installation's availability subject. The Scenario Manager default is:

```text
cbse.eds.scenarios.available
```

Request body:

```json
{
  "batch_id": "design-001",
  "project": "example-experiment",
  "scenario_count": 2
}
```

`batch_id` and `scenario_count` are informational in the current implementation. `project` is required when the default project-specific batch routing is used.

A successful response is:

```json
{
  "status": "ready",
  "batch_subject": "cbse.example-experiment.eds.scenarios"
}
```

On failure, `status` is `error` and `reason` describes the rejection. Do not construct the batch subject independently; use the returned value so the EDS follows Scenario Manager configuration.

### 2. Publish a scenario batch

Publish the batch through JetStream to `batch_subject`:

```json
{
  "batch_id": "design-001",
  "project": "example-experiment",
  "scenarios": [
    {
      "priority": 10,
      "number_of_reps": 20,
      "recipe_info": {
        "arrival_rate": 4.2,
        "servers": 3,
        "seed": 1042
      },
      "confidence_metric": 0.95
    },
    {
      "priority": 20,
      "number_of_reps": 20,
      "recipe_info": {
        "arrival_rate": 5.0,
        "servers": 4,
        "seed": 1043
      },
      "confidence_metric": 0.95
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `batch_id` | EDS-defined trace identifier; currently not a database idempotency key |
| `project` | Exact, raw `SimulationExperiment` name used to find the project row |
| `scenarios` | Zero or more scenario definitions; the default maximum is 1,000 per message |
| `priority` | Stored with the scenario; the current selector does not schedule by priority |
| `number_of_reps` | Requested repetitions; stored now, not executed by the current runner placeholder |
| `recipe_info` | Arbitrary JSON consumed later by the user-defined Translator |
| `confidence_metric` | Optional numeric target for future post-processing |

Scenario Manager assigns each inserted row its database ID, initializes it in `Created`, and inserts the whole batch in one transaction. A database failure rolls back the whole batch and causes a negative acknowledgement so JetStream can redeliver it. Malformed or permanently invalid messages are acknowledged to prevent a poison-message loop.

JetStream delivery is at least once. The current database schema does not deduplicate `batch_id`, so an EDS must not assume that a retry can never create duplicate scenarios. Use deterministic recipes and retain a stable batch ID for observability; if duplicates are unacceptable, wait for or contribute an explicit idempotency contract rather than querying or modifying the Core DB directly.

If the batch message includes a NATS reply subject, Scenario Manager can return this processing summary:

```json
{
  "status": "accepted",
  "batch_id": "design-001",
  "received": 2,
  "inserted": 2,
  "failed": 0
}
```

The durable JetStream publish acknowledgement remains the transport-level confirmation that the broker accepted the message. A request/reply processing summary is optional and must be treated separately.

## Designing a Translator

A Translator owns the domain-specific conversion from `recipe_info` into an executable simulation-runner image. Scenario Manager owns scenario selection, the durable translation claim, attempt numbers, retries, and state transitions.

In the current prototype a Translator should be a long-running, per-experiment consumer. It should:

1. Read and validate `SIMULATIONPROJECTNAME`, `REPOSITORY`, `BASEIMAGE`, broker settings, and credentials at startup.
2. Subscribe to the exact experiment request subject with a durable consumer name unique to the experiment.
3. Strictly validate each request and treat `id` plus `translation_attempt` as the work identity.
4. Generate or locate the runner image idempotently.
5. Publish the ready message through JetStream and wait for its publish acknowledgement.
6. Acknowledge the request only after the ready message is durably accepted.

The repository's [Translator mock](test/mocks/translator/translator_mock.py) demonstrates the current handshake. It returns synthetic image names and is not a secure image-building implementation.

### Request subject and payload

Subscribe to:

```text
cbse.<normalized-project>.trans.request
```

Scenario Manager publishes:

```json
{
  "id": 42,
  "translation_attempt": 1,
  "recipe_info": {
    "arrival_rate": 4.2,
    "servers": 3,
    "seed": 1042
  },
  "confidence_metric": 0.95
}
```

`id` is the Scenario Manager's positive scenario ID. `translation_attempt` is a positive, monotonically increasing attempt for that scenario. `recipe_info` and `confidence_metric` are the values supplied by EDS.

The Scenario Manager defaults to the `cbse_translator` stream. Consumer names are not supplied by `alpha3`; choose a deterministic name containing the normalized project token so two Translator deployments do not share work accidentally.

### Ready subject and payload

After successful translation, publish to:

```text
cbse.<normalized-project>.trans.<scenario-id>.ready
```

with exactly this JSON shape:

```json
{
  "translation_attempt": 1,
  "container_image": "registry.example/generated-runners@sha256:<digest>"
}
```

The ready decoder rejects unknown fields, trailing JSON, non-positive attempts, malformed subjects, and non-positive scenario IDs. `container_image` must be non-empty. The current `alpha3` code does not yet verify the image format or repository, but custom Translators should return an immutable OCI digest from `REPOSITORY`; tags make retries and audit trails ambiguous.

Scenario Manager applies a ready message only to the matching current attempt. An older attempt, a duplicate with the same image, a conflict after an image was already stored, or a message for a missing scenario is terminally acknowledged without overwriting newer state. A transient database error is negatively acknowledged for redelivery. An empty image fails the current attempt and may return the scenario to `Created` until the attempt limit is exhausted.

### Make translation retry-safe

JetStream and process crashes can deliver a request more than once. Key workspaces, build tags, caches, and status records by both scenario ID and translation attempt. Reprocessing the same pair should produce the same semantic result. Never reuse partial output from another attempt.

Publish-before-ack ordering is essential:

```text
receive request
  -> validate
  -> create/resolve runner image
  -> publish ready message
  -> wait for JetStream PubAck
  -> ACK request
```

For malformed requests that cannot become valid through redelivery, log a credential-free reason and ACK them. For transient broker, registry, or build failures, leave the request unacknowledged or NAK it according to the client's retry policy.

## Designing a PostProcessingService

You can provide a PostProcessingService image in `alpha3`, and the Operator will run it as a Deployment, inject `SIMULATIONPROJECTNAME`, and expose its configured port through `<experiment-name>-postproc-svc`.

There is no interoperable PostProcessingService API yet. Scenario Manager does not call this Service, does not send it a NATS message, and does not supply result-database connection details. The current selector contains a local, side-effect-free placeholder that always reports confidence reached; the normal workflow cannot reach it because no real runner completion path exists.

Consequently, a custom PostProcessingService can be made deployment-compatible now, but not CBSE-workflow-compatible. Do not invent an HTTP route or NATS subject and describe it as a CBSE contract. Until an explicit contract is added, design the domain calculation behind a narrow internal function so that its future transport adapter can be replaced without rewriting the calculation.

A future contract needs to define at least:

- how a scenario and execution attempt are identified;
- how result data is located without exposing database credentials broadly;
- whether the operation is request/reply, asynchronous messaging, or another durable workflow;
- how the service reports confidence reached versus more repetitions required;
- ownership of repetition-count changes and guarded state transitions;
- idempotency, timeout, retry, stale-result, and cancellation behavior; and
- which failures are domain failures and which are retryable infrastructure failures.

Until those decisions are implemented, make the container self-contained, able to start and remain healthy on the configured port, non-root compatible, and explicit about any configuration it still requires.

## Understanding the current scenario lifecycle

The implemented happy path currently ends before simulation execution:

```text
EDS batch
   |
   v
Created --claim and publish--> Scheduled --matching Translator ready--> StartingRunners
                                                                           |
                                                                           v
                                                                  InProcessing
                                                                  (placeholder only;
                                                                   no Job is created)
```

The Basic Scenario Selection Logic (BSSL) is one serial worker per Scenario Manager process. By default it checks immediately and then waits five seconds between iterations. It selects the globally lowest positive scenario ID in an actionable state; the stored `priority` does not currently affect selection.

The full state vocabulary also contains `PostProcessing`, `Finished`, and `Failed`, but the current product has no runner completion path into `PostProcessing`. Its local post-processing placeholder would move a manually present `PostProcessing` row to `Finished`; it is scaffolding, not a service integration.

The durable state transitions are guarded so repeated workers, stale messages, and restarts do not blindly overwrite newer work. Component implementations should preserve that model: claim state before an external effect, use deterministic external identity, and verify ownership before adopting an existing object.

## Common design goals

These goals apply to all custom components and to future repository templates. A feature specification may make them stricter, but an implementation should not weaken them silently.

### Self-contained images

The primary image owns its executable behavior, runtime dependencies, entrypoint, and required internal assets. Kubernetes manifests should not assemble missing application logic from injected shell scripts. External configuration, credentials, and user data may be supplied through explicit environment variables, Secrets, ConfigMaps, or mounted storage when the API supports them.

Use immutable image digests in reproducible experiments. Log the application version and contract version at startup, but never log credentials.

### Secure execution by default

Build images that run as a numeric, non-root user, require no privilege escalation, work with all Linux capabilities dropped, and support the `RuntimeDefault` seccomp profile. Use a read-only root filesystem when possible and place temporary data in an explicit writable directory.

Do not require Kubernetes API access unless it is the component's declared responsibility. Workloads without that need should run without a mounted service-account token. Any prototype security exception must state why it exists, where it applies, and what hardening is deferred.

### Explicit configuration and Secret boundaries

Document every setting's owner, source, format, default, and validation point. Validate the entire startup configuration before connecting to external systems. Fail with a descriptive error when a required setting is absent or malformed.

Pass Secrets only to the component that needs them. Never place Secret values in logs, Kubernetes status, NATS messages, image metadata, or test artifacts. A missing configuration boundary in `alpha3` is a product limitation, not permission to bake credentials into an image.

### Stable extension points

Keep CBSE integration code separate from domain behavior:

```text
process lifecycle and configuration
        |
transport adapter (NATS/HTTP/future API)
        |
small typed domain interface
        |
user-specific design, translation, or confidence logic
```

The framework side should own connection lifecycle, payload validation, acknowledgements, retry classification, and observability. The replaceable module should own only domain decisions. Protect framework-owned identity, security, lifecycle, and consistency fields from user overrides.

### Idempotent effects and clear ownership

Assume messages can be redelivered and processes can stop after an external effect but before recording success. Use scenario ID, attempt, and experiment identity to make work deterministic. Confirm the expected owner and contract before accepting an existing image, record, or workload; never silently adopt a conflict.

Each lifecycle transition, cleanup action, validation step, and failure class needs one clear owner. Infrastructure outages should remain retryable infrastructure errors rather than being forced into a domain state such as `Failed`.

### Observable and testable behavior

Logs should identify the component, operation, project, scenario ID, and attempt when available. Health should distinguish startup failure, dependency outage, and readiness for new work. Long operations need progress that is visible without exposing payload Secrets.

Test normal behavior as well as invalid configuration, malformed payloads, duplicate delivery, stale attempts, publish-after-effect crashes, dependency outages, shutdown, and cleanup. Protocol tests should run against NATS/JetStream rather than replacing acknowledgement behavior with mocks alone.

When contributing component, Go, CRD, Dockerfile, or test-harness changes to this repository, follow the root [test contract](AGENTS.md) and [testing guide](docs/CBSE_TESTING_GUIDE.md).

## Implementation checklist

Before treating a custom component image as ready:

- It has one clear responsibility and a self-contained entrypoint.
- It validates its complete configuration before external work.
- It uses `SIMULATIONPROJECTNAME` consistently and avoids subject collisions.
- Its image is pinned by digest and supports restricted, non-root execution.
- It never embeds or logs credentials.
- Its external effects are safe under retry and process restart.
- It distinguishes malformed work from transient failure.
- It acknowledges messages only at the contract's durable completion point.
- Its logs include project, scenario, and attempt identifiers where applicable.
- Its tests cover duplicate, stale, partial-success, and shutdown paths.
- Its documentation states current limitations instead of presenting future interfaces as implemented.

## Source-level references

- [`alpha3` component fields](experiment-operator/api/alpha3/simulationexperiment_types.go)
- [Operator component provisioning](experiment-operator/internal/controller/simulationexperiment_controller.go)
- [EDS wire types and acknowledgement behavior](scenario-manager/internal/nats/eds_com.go)
- [Translator wire types and acknowledgement behavior](scenario-manager/internal/nats/trans_com.go)
- [Translator durable state transitions](scenario-manager/internal/core/translator_handoff.go)
- [Current lifecycle selector and placeholders](scenario-manager/internal/core/selector.go)
- [Current project status](docs/project-status.md)
