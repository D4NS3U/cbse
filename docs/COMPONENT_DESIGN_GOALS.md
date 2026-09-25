# Designing Custom CBSE Components

This is the starting point for developers who want to provide their own Experimental Design Service (EDS), Translator, or PostProcessingService image for CBSE. It explains what CBSE owns, what a component must own, and which integration contracts exist in the current `alpha4` API.

CBSE is still evolving. Treat the subjects and payloads below as the current `alpha4` contract, not as a promise of long-term API stability. The only served and stored API version is `alpha4`; the `alpha2` and `alpha3` versions are retired — they are not served or stored, and no conversion webhook exists for them. In `alpha4`, EDS intake, the Translator handoff, runner-Job orchestration, and observation are implemented, while the PostProcessingService communication contract is not.

The alpha4 reference component images are built from the repository: the reference Translator (`component-templates/translator/`), its runner base (`component-templates/translator/runner-base/`), and the Scenario Detail Database (`component-templates/scenario-detail-database/`). They share a common set of component design goals: each is built from a locked, digest-pinned source image recorded in `test/e2e/images.lock.env` with no environment override; each runs as a numeric non-root user (`1000:1000`); each is published in the nested repository layout (`${CBSE_REGISTRY}/<component>:<version>`) with a canonical tag plus an immutable provenance tag and referenced in production by its pushed digest; and none bakes credentials into the image. See the per-component README files for the exact build, communication, and cleanup contracts.

## Start with the current boundary

The `SimulationExperiment` custom resource describes one experiment. The Experiment Operator turns that description into Kubernetes resources, while the Scenario Manager owns scenario state in PostgreSQL and coordinates EDS and Translator messages through NATS and JetStream.

```text
SimulationExperiment (alpha4)
        |
        v
Experiment Operator -----> detail/result database connection Secrets (always)
        |                  detail/result database Deployments + Services (image form)
        |                  Translator Deployment (translator container +
        |                  rootless BuildKit sidecar) + Service + ConfigMap
        |                  runner ServiceAccount (simrunner-<UID-prefix>)
        |
        +-----------------------------------------------+

EDS (installed by the installation) -- availability request --> Scenario Manager
EDS == scenario batch ========> Scenario Manager --> Core PostgreSQL DB
                                      |
                                      +== translation request ==> Translator
                                      <== ready message =========+
                                      |
                                      +--> runner Job (simrun-<UID-prefix>-s<id>-a<attempt>)
                                      +--> observation --> PostProcessing or Failed

PostProcessingService: spec field present, no provisioned workload, no call contract yet.
```

The arrows marked `==>` are persistent JetStream messages. The EDS availability handshake uses ordinary NATS request/reply. PostgreSQL is the authority for workflow ownership and progress; messages and Kubernetes workloads are effects of that state.

### Integration status

| Component | Provisioned by Operator | Usable integration today | Current limitation |
| --- | --- | --- | --- |
| EDS | None (the `experimentalDesignService` spec field is configuration only) | Availability request/reply and JetStream scenario-batch ingestion | The installation must run the EDS container itself; the Operator creates no EDS workload |
| Translator | One two-container Deployment (translator + rootless BuildKit sidecar), one Service, and one configuration ConfigMap | JetStream translation requests and ready messages, digest and repository verification, and runner-Job start | The generated runner image is executed as a runner Job by the Scenario Manager, not by the Operator |
| PostProcessingService | None (the `postProcessingService` spec field is configuration only) | No service-call or message contract | `PostProcessing` is a scenario state entered after runner completion; no component consumes it yet |

The Operator waits for its provisioned workloads to be ready before moving the experiment to `InProgress`: both database endpoints must answer an availability probe and the Translator Deployment must have a ready replica.

## What every custom image receives

The current API is `experiment.cbse.terministic.de/alpha4`. Each component is selected in the corresponding part of `spec`:

```yaml
apiVersion: experiment.cbse.terministic.de/alpha4
kind: SimulationExperiment
metadata:
  name: example-experiment
  namespace: simulations
spec:
  defaultServiceType: ClusterIP
  # Exactly one of image or host must be set per database.
  detailDatabase:
    image: registry.example/scenario-detail-database@sha256:<digest>
    dbname: simulation_db
    user: dbuser
    password: dbpassword
    port: 5432
  resultDatabase:
    host: postgres.example
    dbname: result_db
    user: dbuser
    password: dbpassword
    port: 5432
  translator:
    image: registry.example/translator@sha256:<digest>
    repository: registry.example/generated-runners
    baseimage: registry.example/runner-base@sha256:<digest>
    builderImage: registry.example/buildkit@sha256:<digest>
    registryAuthSecretRef:
      name: cbse-registry-auth
    port: 8080
  postProcessingService:
    image: registry.example/post-processing@sha256:<digest>
    port: 8080
  experimentalDesignService:
    design: '{"method":"full-factorial"}'
    image: registry.example/eds@sha256:<digest>
    port: 8080
  # Optional: a full batch/v1 JobTemplateSpec for the runner Job.
  # The Operator validates it; the Scenario Manager builds the effective Job.
  # runner:
  #   jobTemplate: { ... }
```

`spec.translator.registryAuthSecretRef.name` must equal `cbse-registry-auth`, the namespace-local Docker configuration Secret the Builder uses to pull and push. Most `spec` fields are immutable after creation (enforced by CEL validations); the intended update path is to delete and recreate the `SimulationExperiment`.

The Operator injects the following into the Translator container it provisions:

| Variable | Source |
| --- | --- |
| `SIMULATIONPROJECTNAMESPACE` | Pod namespace (downward API) |
| `SIMULATIONPROJECTNAME` | `metadata.name` from the `SimulationExperiment` (downward API) |
| `SIMULATIONEXPERIMENTUID` | `metadata.uid` from the `SimulationExperiment` (downward API) |
| `REPOSITORY` | `spec.translator.repository` (via the Translator ConfigMap) |
| `BASEIMAGE` | `spec.translator.baseimage` (via the Translator ConfigMap) |
| `NATS_URL`, `TRANSLATOR_STREAM`, `TRANSLATOR_REQUEST_SUBJECT`, `TRANSLATOR_READY_SUBJECT_TEMPLATE`, `TRANSLATOR_CONSUMER` | fixed `alpha4` values derived from the experiment's namespace, name, and UID |

Image-based database containers additionally receive `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, and `SIMULATIONPROJECTNAME`. The `experimentalDesignService` and `postProcessingService` workloads are not provisioned by the Operator, so nothing is injected for them; an EDS must bring its own configuration (NATS URL, subjects) from wherever the installation deploys it.

`command` and `args` can override an image's entrypoint for the Translator and the image-based databases; `port`, `serviceType`, and `nodePort` configure the provisioned Services; `spec.translator.builderResources` configures the BuildKit sidecar; and `spec.runner.jobTemplate` customizes the runner Job template. The API does not provide arbitrary environment variables or Secret mounts beyond the fixed set above. Do not put credentials into an image, `command`, `args`, the `design` string, or other non-Secret fields.

## How CBSE identifies an experiment

The raw experiment name is stored in PostgreSQL, labeled onto owned workloads, and injected as `SIMULATIONPROJECTNAME`. The CRD requires `metadata.name` to be a lowercase DNS label of at most 63 characters, and the `alpha4` subject grammar never normalizes identifiers: the namespace and the project name appear in subjects exactly as written.

Every NATS subject has the fixed form `cbse.<namespace>.<project>.<domain>.<event>`, where `<namespace>` and `<project>` are each a single lowercase DNS label (no dots, 1–63 characters) and the remaining tokens are reserved names:

| Subject | Transport | Purpose |
| --- | --- | --- |
| `cbse.<namespace>.<project>.eds.scenarios.available` | Core NATS request/reply | EDS availability handshake |
| `cbse.<namespace>.<project>.eds.scenarios` | JetStream (stream `cbse_eds_scenarios`) | EDS scenario batches |
| `cbse.<namespace>.<project>.trans.request` | JetStream (stream `cbse_translator`) | Scenario Manager translation requests to the Translator |
| `cbse.<namespace>.<project>.trans.<scenario-id>.ready` | JetStream (stream `cbse_translator`) | Translator ready messages per scenario |

For example, the experiment `example-experiment` in namespace `simulations` uses `cbse.simulations.example-experiment.trans.request`. Because the namespace is part of every subject, the same experiment name can exist in two namespaces without a routing collision.

Payload fields named `project` carry the raw `SimulationExperiment` name. The wire payload retains that field for fixture compatibility, and a batch whose `project` does not exactly match the subject's project token is permanent poison. Use the subject's identity tokens, never a re-normalized version of the payload field.

## Designing an Experimental Design Service

An EDS owns domain-specific experiment design: it turns the configured design into scenarios and submits those scenarios to CBSE. Scenario Manager owns persistence, initial state, scenario IDs, and later lifecycle transitions.

The EDS should:

1. Read and validate `SIMULATIONPROJECTNAME` and any configuration that is actually available to the process before doing external work.
2. Connect to the installation's NATS server with bounded retries.
3. Announce each pending batch with the availability handshake.
4. Publish the complete batch to the subject returned by Scenario Manager and wait for a JetStream publish acknowledgement.
5. Make retries safe and remain alive until Kubernetes terminates the Pod.

The repository's [EDS mock](../test/mocks/eds/eds_mock.py) is executable protocol documentation. It is a test fixture, not a production template; its subject and stream settings are environment-configurable, and the smoke harness configures it for the `alpha4` subjects.

### 1. Announce availability

Send a NATS request to the experiment's availability subject:

```text
cbse.<namespace>.<project>.eds.scenarios.available
```

Request body:

```json
{
  "batch_id": "design-001",
  "project": "example-experiment",
  "scenario_count": 2
}
```

`batch_id` and `scenario_count` are informational in the current implementation. `project` must equal the subject's project token.

For an admitted (live, non-deleting, `InProgress`) experiment the response is:

```json
{
  "status": "ready",
  "batch_subject": "cbse.<namespace>.<project>.eds.scenarios"
}
```

On failure, `status` is `error` and `reason` describes the rejection; no `batch_subject` is returned. Do not construct the batch subject independently; use the returned value so the EDS follows Scenario Manager configuration.

### 2. Publish a scenario batch

Publish the batch through JetStream to `batch_subject` (stream `cbse_eds_scenarios`):

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
| `batch_id` | EDS-defined trace identifier; not a database idempotency key |
| `project` | Exact, raw `SimulationExperiment` name; must match the subject's project token |
| `scenarios` | Zero or more scenario definitions |
| `priority` | Stored with the scenario; the selector does not schedule by priority |
| `number_of_reps` | Requested repetitions; must be in `1..100000` or the whole batch is permanent poison, and is executed as the runner Job's completion count |
| `recipe_info` | Arbitrary JSON consumed later by the user-defined Translator |
| `confidence_metric` | Optional numeric target for future post-processing |

Scenario Manager assigns each inserted row its database ID, initializes it in `Created`, and inserts the whole batch in one transaction. A database failure rolls back the whole batch and negatively acknowledges the delivery so JetStream can redeliver it. Malformed JSON, an invalid subject or identity, a subject/payload mismatch, or an out-of-range `number_of_reps` is permanent poison: the delivery is acknowledged and no rows are inserted. A terminal experiment is acknowledged and discarded; an unavailable experiment or transient dependency failure is negatively acknowledged for redelivery.

JetStream delivery is at least once. The database schema does not deduplicate `batch_id`, so an EDS must not assume that a retry can never create duplicate scenarios. Use deterministic recipes and retain a stable batch ID for observability; if duplicates are unacceptable, wait for or contribute an explicit idempotency contract rather than querying or modifying the Core DB directly.

## Designing a Translator

A Translator owns the domain-specific conversion from `recipe_info` into an executable simulation-runner image. Scenario Manager owns scenario selection, the durable translation claim, attempt numbers, retries, and state transitions.

An `alpha4` Translator should be a long-running, per-experiment consumer. It should:

1. Read and validate `SIMULATIONPROJECTNAME`, `SIMULATIONPROJECTNAMESPACE`, `SIMULATIONEXPERIMENTUID`, `REPOSITORY`, `BASEIMAGE`, broker settings, and credentials at startup.
2. Subscribe to the exact experiment request subject with the UID-specific durable consumer name the Operator injects.
3. Strictly validate each request and treat `id` plus `translation_attempt` as the work identity.
4. Generate or locate the runner image idempotently.
5. Publish the ready message through JetStream and wait for its publish acknowledgement.
6. Acknowledge the request only after the ready message is durably accepted.

The repository's [reference Translator](../component-templates/translator/README.md) implements this framework for `alpha4`, including the BuildKit sidecar build+push seam. The repository's [Translator mock](../test/mocks/translator/translator_mock.py) is a test fixture that demonstrates the request/ready handshake with synthetic image names; it is not a secure image-building implementation.

### Request subject and payload

Subscribe to the exact request subject the Operator injects (`TRANSLATOR_REQUEST_SUBJECT`):

```text
cbse.<namespace>.<project>.trans.request
```

Scenario Manager publishes on stream `cbse_translator`:

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

The durable consumer is fixed by the Operator: `translator-<12-char-UID-prefix>` (injected as `TRANSLATOR_CONSUMER`), on stream `cbse_translator`, with explicit ACK policy, a two-minute `AckWait`, and a single in-flight delivery per Translator. Do not rename or reconfigure it; an existing durable with a mismatched configuration is an identity collision that the reference framework rejects without adopting it.

### Ready subject and payload

After successful translation, publish to the subject for the scenario (the Operator injects the template `cbse.{namespace}.{project}.trans.{scenario_id}.ready`):

```text
cbse.<namespace>.<project>.trans.<scenario-id>.ready
```

with exactly this JSON shape:

```json
{
  "translation_attempt": 1,
  "container_image": "registry.example/generated-runners@sha256:<digest>"
}
```

The ready decoder rejects unknown fields, trailing JSON, non-positive attempts, malformed subjects, and non-positive scenario IDs. `container_image` must be non-empty. Scenario Manager verifies that the image is an immutable digest reference and that its repository exactly matches the live experiment's `spec.translator.repository`; a digest from another repository is permanent poison (acknowledged, not persisted, no Job).

Scenario Manager applies a ready message only to the matching current attempt: the guarded `Scheduled -> StartingRunners` transition stores the accepted digest and starts the runner-Job path. An older attempt, a duplicate, or a message for a missing scenario is terminally acknowledged without overwriting newer state. A transient database error is negatively acknowledged for redelivery. An empty image consumes the attempt through the recovery path: the scenario returns to `Created` until the shared attempt limit is exhausted, then to `Failed`.

### Make translation retry-safe

JetStream and process crashes can deliver a request more than once. Key workspaces, build tags, caches, and status records by both scenario ID and translation attempt. Reprocessing the same pair should produce the same semantic result. Never reuse partial output from another attempt.

Publish-before-ack ordering is essential:

```text
receive request
  -> validate
  -> create/resolve runner image
  -> retain the pushed tag and resolved digest
  -> publish ready message
  -> wait for JetStream PubAck
  -> ACK request
```

Once an image push succeeds, that pushed image is the durable outcome for the scenario ID and translation attempt. If ready publication fails, retry publication with the same digest. Do not regenerate the model, rebuild the image, or push another image for that attempt. Keep a credential-free local outcome marker while the request is unacknowledged, and use the deterministic registry tag as the recovery source if the Translator process or Pod restarts. The ready-message handoff is incomplete until JetStream confirms publication, but a broker failure does not erase the completed registry effect.

For malformed requests that cannot become valid through redelivery, log a credential-free reason and ACK them. For transient broker, registry, or build failures, leave the request unacknowledged or NAK it according to the client's retry policy.

## Designing a PostProcessingService

You can provide a `postProcessingService` image in the `alpha4` spec, but the Operator does not provision a PostProcessingService workload and Scenario Manager does not call one. `PostProcessing` is a scenario state entered after the runner Job completes successfully; it is a boundary, not a service integration.

There is no interoperable PostProcessingService API yet. Scenario Manager does not call a PostProcessingService, does not send it a NATS message, and does not supply result-database connection details for it.

Consequently, a custom PostProcessingService can be made deployment-compatible now (by the installation), but not CBSE-workflow-compatible. Do not invent an HTTP route or NATS subject and describe it as a CBSE contract. Until an explicit contract is added, design the domain calculation behind a narrow internal function so that its future transport adapter can be replaced without rewriting the calculation.

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

The implemented `alpha4` happy path runs a scenario from batch intake through executed repetitions:

```text
EDS batch
   |
   v
Created --claim and publish--> Scheduled --matching Translator ready--> StartingRunners
                                                                           |
                                                    runnerstart creates/confirms
                                                    the deterministic runner Job
                                                                           v
                                                                  InProcessing --Job Complete--> PostProcessing
                                                                  InProcessing --Job Failed/Collision/Forbidden--> Failed
```

The Basic Scenario Selection Logic (BSSL) is one serial worker per Scenario Manager process. It checks immediately and then waits five seconds between iterations. It selects the globally lowest positive scenario ID in `Created`; the stored `priority` does not currently affect selection. Each iteration is recovery-first: it reclaims stale unpublished translation claims before discovering the next `Created` scenario.

The runner-start scheduler discovers `StartingRunners` scenarios in ascending ID order on a bounded, ordered worker pool and creates the deterministic runner Job `simrun-<UID-prefix>-s<scenario-id>-a<attempt>` (or confirms an existing one) from the Operator-validated runner template, the accepted runner digest, and the deterministic `simrunner-<UID-prefix>` runner ServiceAccount.

The observation scheduler discovers `InProcessing` scenarios on a fixed five-second interval and observes the deterministic runner Job. On completion it records the computed repetition count in the scenario row and applies the guarded `InProcessing -> PostProcessing` transition; on Job failure, collision, or forbidden access it applies `InProcessing -> Failed`.

The full state vocabulary also contains `Finished`, but the current product has no implemented transition into it. `PostProcessing` is a boundary: no component consumes it yet, and the selection loop treats it as a no-op.

The durable state transitions are guarded so repeated workers, stale messages, and restarts do not blindly overwrite newer work. Component implementations should preserve that model: claim state before an external effect, use deterministic external identity, and verify ownership before adopting an existing object.

When the experiment is deleted, the Scenario Manager's deletion cleanup deletes ownership-verified runner Jobs, deletes the per-experiment Translator consumer after ownership verification, and purges the experiment's Translator ready subjects; when an experiment enters a terminal failure, the terminal action moves its unfinished scenarios to `Failed`.

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

Pass Secrets only to the component that needs them. Never place Secret values in logs, Kubernetes status, NATS messages, image metadata, or test artifacts. A missing configuration boundary in `alpha4` is a product limitation, not permission to bake credentials into an image.

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

When one durable external effect succeeds and the following handoff fails, resume from the completed effect. In particular, a Translator that has pushed a runner image must recover and republish that exact digest rather than repeating model generation or image creation.

Each lifecycle transition, cleanup action, validation step, and failure class needs one clear owner. Infrastructure outages should remain retryable infrastructure errors rather than being forced into a domain state such as `Failed`.

### Observable and testable behavior

Logs should identify the component, operation, project, scenario ID, and attempt when available. Health should distinguish startup failure, dependency outage, and readiness for new work. Long operations need progress that is visible without exposing payload Secrets.

Keep the default log volume proportional to scenarios and failures, not repetitions or simulation events. Log scenario-level creation and terminal outcomes. A runner-controlled failure should emit one timestamped, sanitized record with the scenario ID, Pod hostname, failure stage, and reason. Use Kubernetes Job conditions, Pod status, and events for infrastructure-level detail instead of duplicating them continuously. Log retention and deletion are installation concerns unless a feature explicitly owns them.

Test normal behavior as well as every required environment variable, mounted Secret key and path, subject template, component identity, image or repository reference, and writable workspace named by the active component contract. Also test malformed payloads, duplicate delivery, stale attempts, publish-after-effect crashes, dependency outages, shutdown, and cleanup. Protocol tests should run against NATS/JetStream rather than replacing acknowledgement behavior with mocks alone.

When contributing component, Go, CRD, Dockerfile, or test-harness changes to this repository, follow the root [test contract](../AGENTS.md) and [testing guide](CBSE_TESTING_GUIDE.md).

## Licensing your components

Contributed component sources — custom EDS, Translator, and PostProcessing images and their source files — carry the same Apache-2.0 short-form license header as the repository's own sources. The copyright line uses the form `Copyright <years> <holder>` (plain ASCII hyphen). Add `SPDX-License-Identifier: Apache-2.0` alongside the header where the file format supports it. The header is written in the file's comment style (`//` for Go, `#` for shell and Python) and placed at the top of the file:

```text
// Copyright <years> <holder>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
```

## Implementation checklist

Before treating a custom component image as ready:

- It has one clear responsibility and a self-contained entrypoint.
- It validates its complete configuration before external work.
- It uses `SIMULATIONPROJECTNAMESPACE` and `SIMULATIONPROJECTNAME` consistently and avoids subject collisions.
- Its image is pinned by digest and supports restricted, non-root execution.
- It never logs credentials and embeds them only when an active feature specification explicitly requires and documents a trusted-prototype exception.
- Its external effects are safe under retry and process restart.
- It distinguishes malformed work from transient failure.
- It acknowledges messages only at the contract's durable completion point.
- Its logs include project, scenario, and attempt identifiers where applicable.
- Its tests cover duplicate, stale, partial-success, and shutdown paths.
- Its documentation states current limitations instead of presenting future interfaces as implemented.

## Source-level references

- `alpha2`/`alpha3` component fields (removed with the retired trees; alpha4 is the only served and stored version)
- [Operator component provisioning](../experiment-operator/internal/controller/simulationexperiment_alpha4_controller.go)
- [EDS wire types and acknowledgement behavior](../scenario-manager/internal/communication/communication.go)
- [Translator wire types and acknowledgement behavior](../scenario-manager/internal/communication/communication.go)
- [Namespace-aware subject grammar](../scenario-manager/internal/subject/subject.go)
- [Translator durable state transitions](../scenario-manager/internal/ready/ready.go)
- [Current lifecycle selector](../scenario-manager/internal/selection/selection.go)
