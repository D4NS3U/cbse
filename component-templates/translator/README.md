# CBSE Reference Translator

This directory holds the repository-owned reference **Translator** for CBSE
alpha4: a standalone Go module (`github.com/D4NS3U/cbse/component-templates/translator`)
that consumes one translation request at a time, looks up the model parameters
from the Scenario Detail Database, writes a complete Docker build context, and
builds and pushes a generated runner image through a BuildKit sidecar before
publishing a ready message and acknowledging the request.

The Translator is a **framework**: it owns request consumption, the BuildKit
build+push seam, registry verification, ready publication, and acknowledgement.
The model-specific work lives behind the replaceable `generator.Generator`
interface. A user may replace `internal/generator` without reimplementing the
handoff protocol, provided the replacement preserves the interface and the
dependency direction (see [Replaceable generator](#replaceable-generator)).

This README is part of the template's supported interface, not optional
explanatory material. It gives an integrator enough information to replace the
example generator without changing the framework-owned communication or build
protocol.

> **Slice status.** The Go module, Dockerfiles, source-image lock, and build
> harness contracts documented here are implemented and validated by
> `make test-fast`. The Operator deployment that injects the mounted Secrets,
> the BuildKit sidecar, and the NATS connection is wired by the alpha4
> Experiment Operator reconciler. The Translator binary itself contains no
> Python, no SimPy, no NATS credentials, no database credentials, and no model;
> all of those are injected at runtime or generated per attempt.

## Two repository-owned images

The Translator template produces two distinct repository-built images through
`test/harness/build-images.sh`. Neither is a floating tag; both are referenced
by immutable digest in `images.env`.

| Image | Dockerfile | Build arg | Output var | Layout | Contains |
| --- | --- | --- | --- | --- | --- |
| Translator runtime | `Dockerfile` | `TRANSLATOR_GO_BUILDER_IMAGE` | `TRANS_IMAGE` | nested | static Go binary + CA bundle only |
| Runner base | `runner-base/Dockerfile` | `PYTHON_BASE_IMAGE` | `RUNNER_BASE_IMAGE` | nested | Python + SimPy + Psycopg, no model |

### Translator runtime (`Dockerfile`)

`Dockerfile` builds the static non-root Translator runtime from the locked
`TRANSLATOR_GO_BUILDER_IMAGE`. It requires `TRANSLATOR_GO_BUILDER_IMAGE` as its
only build argument and uses it as the single build stage. A missing argument
fails the build. It compiles `./cmd/translator` with `CGO_ENABLED=0`,
`GOOS=linux`, `GOARCH=amd64`, `-trimpath`, and stripped symbol and build-ID
linker flags. The final stage is `scratch`; it copies only the static
`/translator` binary and the CA certificate bundle from the build stage, sets
numeric `USER 1000:1000`, and sets `ENTRYPOINT ["/translator"]`. Runtime
configuration, workspaces, sockets, and Secret files are injected by the
Operator and are not copied into the image. The Translator image contains
neither Python nor SimPy.

### Runner base (`runner-base/Dockerfile`)

`runner-base/Dockerfile` builds the separate Python/SimPy/Psycopg runner base
from the locked `PYTHON_BASE_IMAGE`. It requires `PYTHON_BASE_IMAGE` as its only
build argument and uses `FROM ${PYTHON_BASE_IMAGE}`; a missing argument fails
the build. The checked-in `runner-base/requirements.lock` installs exactly
`simpy==4.1.2`, `psycopg==3.3.4`, and the matching `psycopg-binary==3.3.4`
distribution with package hashes through `pip --require-hashes`. The build
imports SimPy and Psycopg and verifies their exact versions before push,
removes installation input and caches, sets `WORKDIR /runner`, and sets numeric
`USER 1000:1000`. It contains no model, no scenario parameters, no database
configuration, and no credentials. Generated runner images inherit these
packages and add only the generated model and connection configuration; they do
not contain a PostgreSQL server (Psycopg is the client used to reach the
experiment's Result DB).

### Canonical publish tags and digest outputs

`build-images.sh` publishes both nested images with two tags each and records the
pushed digest:

- canonical tag: `${CBSE_REGISTRY}/translator:${VERSION}` and
  `${CBSE_REGISTRY}/runner-base:${VERSION}`
- immutable provenance tag:
  `${CBSE_REGISTRY}/translator:${VERSION}.sha-${commit}-${sourceHash}-${runId}`
  (and the same for `runner-base`)
- digest output in `images.env`:
  `TRANS_IMAGE=${CBSE_REGISTRY}/translator@sha256:<64hex>` and
  `RUNNER_BASE_IMAGE=${CBSE_REGISTRY}/runner-base@sha256:<64hex>`
  (nested, keeping the repository path)

The digest is the immutable reference; `RUNNER_BASE_IMAGE` becomes
`spec.translator.baseimage` and `TRANS_IMAGE` becomes `spec.translator.image`.

### Source-image lock

The two locked build inputs (`TRANSLATOR_GO_BUILDER_IMAGE` and
`PYTHON_BASE_IMAGE`, with their provenance versions) live in
`test/e2e/images.lock.env`. They are repository inputs with no environment
override; `test/harness/image-lock.sh` validates the lock (all eight keys
present exactly once, no overrides, no unknown keys, `name@sha256:<64hex>`
format) before any build. No Dockerfile, CR, manifest, or test configuration
may use a floating tag, an unversioned dependency, or a multi-platform index
where the lock requires a platform manifest. Updating a locked version or digest
is a later explicit specification change that updates the paired version and
digest together after verifying `linux/amd64` platform identity.

## Runtime configuration

The framework reads its configuration from environment variables and three
mounted Secret paths at startup, before any NATS connection. It accepts **no
NATS credentials** — no username, password, token, NKey, JWT, credentials file,
or credential mount — and `NATS_URL` must not contain user information. After
startup it does not watch, reload, or rotate credentials.

### Environment variables

| Variable | Meaning |
| --- | --- |
| `NATS_URL` | NATS connection URL (no user info) |
| `TRANSLATOR_STREAM` | JetStream stream name |
| `TRANSLATOR_REQUEST_SUBJECT` | exact request subject `cbse.<namespace>.<project>.trans.request` |
| `TRANSLATOR_READY_SUBJECT_TEMPLATE` | ready-subject template |
| `TRANSLATOR_CONSUMER` | UID-specific durable name, must equal `translator-<12-char-UID-prefix>` |
| `SIMULATIONPROJECTNAMESPACE` | experiment namespace (DNS label, must match the request subject) |
| `SIMULATIONPROJECTNAME` | experiment project (DNS label, must match the request subject) |
| `SIMULATIONEXPERIMENTUID` | experiment UID (8-4-4-4-12 lowercase hex) |
| `REPOSITORY` | target runner repository (no tag, no digest) |
| `BASEIMAGE` | digest-pinned runner base image ending in `@sha256:<64hex>` |

### Mounted Secret paths

| Path | Contents |
| --- | --- |
| `/detaildb-connection` | Detail DB connection: `host`, `port`, `dbname`, `user`, `password` files |
| `/resultdb-connection` | Result DB connection: `host`, `port`, `dbname`, `user`, `password` files |
| `/registry-auth/config.json` | Docker `config.json` with basic credentials for the base-image and target-repository authorities |

The mounted registry Docker config must contain basic credentials for both the
base-image authority and the target-repository authority; the framework
validates this at startup. A missing credential falls back to anonymous and the
registry then rejects the push if it requires auth.

## BuildKit sidecar and admission gate

The Translator runs alongside a BuildKit sidecar (provisioned by the alpha4
Operator) that exposes its socket at `unix:///run/buildkit/buildkitd.sock`.

The sidecar is the **rootless** `moby/buildkit:v0.32.2-rootless` image. Rootless
`buildkitd` must run as the **mapped root inside a user namespace**: the Operator
sets `hostUsers: false` on the Translator Pod and runs the `buildkit` container
as `runAsUser: 0` (mapped root) with `--oci-worker-no-process-sandbox`, an
unconfined seccomp profile, and an unconfined AppArmor profile. This requires the
cluster to have the **`UserNamespacesSupport` feature gate enabled** (beta in
Kubernetes 1.30–1.32, disabled by default; GA in 1.33). On a cluster where the
gate is off, the API server strips `hostUsers: false` and `buildkitd` fails at
startup with `can't enable NoProcessSandbox without Rootless`. Enabling the gate
and the experiment-namespace Pod Security it needs is a one-time cluster-admin
operation; see [`docs/CLUSTER_REQUIREMENTS.md`](../../docs/CLUSTER_REQUIREMENTS.md)
for the exact K3s enablement steps, the kernel/containerd prerequisites, the
verification procedure, and the namespace Pod Security label. The sidecar uses
no privileged mode, host networking, host paths, host runtime sockets, or
privilege escalation.

Before creating or attaching its JetStream request consumer, the framework
opens an **admission gate**: it connects the BuildKit client at the canonical
socket and calls `ListWorkers` (the `buildctl debug workers` equivalent),
requiring at least one worker. The gate retries on the canonical schedule
**250ms, 500ms, 1s, then every 2s**, honoring shutdown cancellation. Until the
gate opens the framework creates no consumer, accepts no delivery, writes no
marker, and consumes no attempt. A BuildKit failure that occurs only after a
request was accepted follows the ordinary confirmed empty-image workflow; the
startup gate does not suppress request-time failure handling.

The build+push seam submits the generated attempt directory as both Dockerfile
and build context to BuildKit, requests a registry push to the deterministic tag
with the framework-owned OCI manifest annotations, authenticates through the
mounted Docker configuration via a BuildKit session, and returns the pushed
digest from the exporter response.

## Per-attempt workspace layout

The framework owns a pod-local workspace rooted at `/workspace`. Each attempt
lives at:

```
/workspace/scenario-<scenario-id>/attempt-<translation-attempt>/
├── runner/                  # generated model build context (main.py, scenario.json, resultdb.json)
├── Dockerfile               # generated: FROM <baseimage digest>; COPY runner/; USER 1000:1000; ENTRYPOINT
└── ready-outcome.json       # single credential-free durable outcome marker (see below)
```

The framework checks for a retained outcome marker before removing or
recreating build input: only an attempt with no retained outcome and no
recoverable registry tag has its `runner/` directory and `Dockerfile`
recreated. It retains the workspace until ready publication and server-confirmed
request acknowledgement both succeed, then removes the attempt workspace.

### `ready-outcome.json` — the durable outcome marker

The marker is the single credential-free durable outcome format. It contains
the outcome (`success` or `empty_failure`), scenario ID, translation attempt,
ready subject, and full experiment UID. A success additionally contains the
deterministic tag and digest; an empty failure contains an empty image and a
short non-sensitive failure class. The complete file is written to a temporary
file in the same directory and renamed over the marker before publishing ready,
so a crash never leaves a partial marker. A valid marker is authoritative for
that attempt.

## Messaging and acknowledgement contract

The framework consumes **one request at a time** (`MaxAckPending` 1). It
creates or attaches a UID-specific durable pull consumer with the exact alpha4
configuration:

- durable name: `translator-<12-char-UID-prefix>`
- filter subject: the exact request subject `cbse.<namespace>.<project>.trans.request`
- ack policy: explicit ACK
- deliver policy: `DeliverAll`
- `AckWait`: two minutes
- `MaxAckPending`: one
- `MaxDeliver`: unlimited
- four ownership metadata entries (`managed-by=translator`, `experiment-uid`,
  `namespace`, `project`)

An existing durable with a matching name but mismatched settings is an
**identity collision**: the framework rejects it without deleting, updating, or
adopting it.

The framework publishes the ready message with JetStream confirmation
(`PubAck`) **before** acknowledging the request. During long operations it sends
**30-second in-progress acknowledgements** so the server does not redeliver under
the two-minute `AckWait`. NATS transport or ready-publication failures leave the
request unacknowledged and retryable; they never manufacture an empty result.

## UID/GID and fsGroup

The Translator runtime and the runner base both run as numeric `USER 1000:1000`.
The `/workspace` attempt directory is created with `0755`; generated files are
`0644` except the baked Result DB connection (`resultdb.json`), which is `0600`.
The Operator-deployed Translator pod must set a `securityContext.fsGroup` that
allows UID `1000` to read and write the mounted workspace and the BuildKit
socket.

## Replaceable generator

The `internal/generator.Generator` interface is the replaceable boundary:

```go
type Generator interface {
    Generate(ctx context.Context, in GenerationInput) error
}
```

`GenerationInput` carries the scenario ID, translation attempt, `recipe_info`,
the digest-pinned `BaseImage`, the attempt `Workspace` path, and both database
configs. The generator performs the predefined Scenario Detail Database
parameter lookup using `DetailDatabase` and bakes only `ResultDatabase` into the
generated runner image; it must **not** bake `DetailDatabase` credentials into
the image.

A replacement generator must:

- preserve the `Generator` interface and the dependency direction — it must
  never consume NATS messages, call BuildKit, push images, publish ready
  messages, or acknowledge deliveries;
- write a complete Docker build context into `in.Workspace` (a `runner/`
  directory plus a `Dockerfile` whose `FROM` is the digest-pinned `BaseImage`);
- perform only model-specific decisions (recipe validation, parameter lookup,
  model emission).

The reference `ExampleGenerator` is a small parameterizable SimPy single-server
queue whose parameters are looked up from the Scenario Detail Database by a
positive integer `parameterset_id` carried in `recipe_info`. `recipe_info` must
be exactly one JSON object containing one positive integer `parameterset_id` and
no other fields; any deviation is a permanent `RecipeError` that the framework
treats as a confirmed empty-image outcome.

## Deterministic tag and OCI identity annotations

The generated runner image is pushed to a deterministic tag
`<repository>:<tag>` derived from the experiment UID. The BuildKit exporter adds
the framework-owned OCI manifest identity annotations supplied by the
orchestrator; the framework verifies the pushed digest and annotations against
the registry after push. Human-readable source tags appear only as the
provenance version entries in `images.lock.env` and documentation; the digest is
the immutable reference.

## Building and testing

The Translator is built and tested in isolation from the monorepo Go
workspace (its buildkit/docker dependencies require newer `k8s.io/*` modules
than the Experiment Operator's controller-runtime). `make test-fast` runs:

```
cd component-templates/translator && GOWORK=off go vet ./...
cd component-templates/translator && GOWORK=off go test -race ./...
```

`GOWORK=off` makes the translator resolve against its own `go.mod`, so its
dependency versions never affect the operator or scenario-manager workspace.
The unit tests are offline-safe: the BuildKit seam is a function closure
injected with fakes, the messaging tests use struct literals (no live NATS),
and the conformance/SQL-doc tests skip gracefully when their inputs are absent.
