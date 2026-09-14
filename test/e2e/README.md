# CBSE full-stack smoke test

This suite deploys the current Experiment Operator and Scenario Manager with real
PostgreSQL and NATS/JetStream dependencies. An EDS support image supplies two
deterministic batches and acts as a keep-alive mock for components that are not
implemented yet. The alpha4 reference Translator, runner base, and Scenario
Detail Database are built from `component-templates/`.

## Prerequisites

- Go 1.24 or newer (build-time only; the Go module version is independent of the
  Kubernetes runtime version), Docker with Buildx, `curl`, `jq`, and OpenSSL.
- A Kubernetes **1.30 or newer** cluster with at least one `linux/amd64` `Ready`
  schedulable Node. The alpha4 Translator Deployment uses a native sidecar
  BuildKit container, so the 1.30-through-1.32 native-sidecar feature gate must
  be enabled on the cluster (graduated to GA in 1.33).
- A readable kubeconfig. Linux agents normally use `/home/d4ns3u/.kube/config`;
  set another explicit path when needed.
- Access to the K3s API server at `https://192.168.101.245:6443`.
- Access to the University Harbor repository prefix
  `registry.unibw.de/i31bdase/cbse-test` from both the agent and the K3s node.
  Shared components (`exop`, `sm`, `eds-mock`, `translator`, `runner-base`) use
  the **flat** layout (one tag per component on the prefix,
  `${CBSE_REGISTRY}:<component>.test.<version>`); the reference Scenario Detail
  Database uses the **nested** layout
  (`${CBSE_REGISTRY}/scenario-detail-database:<version>`). Generated runner
  images are published to a separate `${CBSE_REGISTRY}/cbse-test-runner`
  repository.
- The checked-in source-image lock `test/e2e/images.lock.env` (four locked
  source images and four provenance versions). Locked inputs have no
  environment override.
- A dedicated Docker `config.json` provided through `CBSE_REGISTRY_AUTH_FILE`.
- A `kubernetes.io/dockerconfigjson` Secret named `cbse-registry-auth` in
  `cbse-test-system`; the harness copies it only to its ephemeral test namespace.

The harness downloads kubectl v1.32.5 into the ignored root `bin/` directory.

## Run

```bash
make test-smoke \
  KUBECONFIG=/home/d4ns3u/.kube/config \
  TEST_IMAGE_VERSION=26.7.16 \
  CBSE_REGISTRY_AUTH_FILE=<protected-docker-config>
```

A smoke build builds the exact mandatory component set
(`exop,sm,eds-mock,translator,runner-base,scenario-detail-database`); a subset
or superset is rejected before any build or registry mutation. `build-images.sh`
publishes each flat component with a canonical tag
(`${CBSE_REGISTRY}:<component>.test.<version>`) and an immutable provenance tag,
and publishes the nested Detail Database with
`${CBSE_REGISTRY}/scenario-detail-database:<version>`. The pushed digests are
recorded in `images.env` (`OPERATOR_IMAGE`, `SM_IMAGE`, `EDS_IMAGE`,
`TRANS_IMAGE`, `RUNNER_BASE_IMAGE`, `DETAIL_DB_IMAGE`); the digest is the
immutable reference.

An authorized administrator provisions the dedicated Docker configuration and the shared Kubernetes Secret outside this repository. Do not use a credential helper for the configuration and do not commit it.

To reuse already published images, every reference must include a digest:

```bash
SKIP_BUILD=1 \
OPERATOR_IMAGE=registry.unibw.de/i31bdase/cbse-test@sha256:... \
SM_IMAGE=registry.unibw.de/i31bdase/cbse-test@sha256:... \
EDS_IMAGE=registry.unibw.de/i31bdase/cbse-test@sha256:... \
TRANS_IMAGE=registry.unibw.de/i31bdase/cbse-test@sha256:... \
RUNNER_BASE_IMAGE=registry.unibw.de/i31bdase/cbse-test@sha256:... \
DETAIL_DB_IMAGE=registry.unibw.de/i31bdase/cbse-test/scenario-detail-database@sha256:... \
CBSE_REGISTRY_AUTH_FILE=<protected-docker-config> \
make test-smoke KUBECONFIG=/home/d4ns3u/.kube/config
```

The six `*_IMAGE` variables are all required when `SKIP_BUILD=1`; a mutable
(floating-tag) reference is rejected. The shared components use the flat digest
form (`${CBSE_REGISTRY}@sha256:<hex>`); the Detail Database uses the nested
digest form (`${CBSE_REGISTRY}/scenario-detail-database@sha256:<hex>`).

`CBSE_KEEP_ON_FAILURE=1` retains a failed namespace. Inspect it with `make test-diagnose RUN_ID=<id>` and remove it with `make test-clean RUN_ID=<id>`. Neither cleanup path removes the shared CRD.

To retain the namespace after a successful run for manual inspection, set `CBSE_KEEP_NAMESPACE=1`. This is opt-in; without it, successful runs are cleaned automatically:

```bash
CBSE_KEEP_NAMESPACE=1 make test-smoke KUBECONFIG=/path/to/config
```

The command prints the run ID. Inspect it with `make test-diagnose RUN_ID=<id>` and remove it with `make test-clean RUN_ID=<id>` when finished.

For a retained E2E environment that also leaves the `SimulationExperiment`, owned EDS/translator/design workloads, and database rows intact, with the Basic Scenario Selection Logic and translator handoff enabled, use the dedicated target:

```bash
make test-e2e-retained KUBECONFIG=/path/to/config
```

This intentionally skips only the final garbage-collection assertion. The preceding provisioning, persistence, and idempotence assertions still run. Clean the retained run with `make test-clean RUN_ID=<id> KUBECONFIG=/path/to/config`.

## Version validation

`TEST_IMAGE_VERSION` uses a non-normalizing `YY.M.D` format (for example
`26.7.16`). The harness validates it against `^[0-9]{2}\.[0-9]{1,2}\.[0-9]{1,2}$`
without rewriting single-digit month/day values; `26.7.16` stays `26.7.16` and is
not padded to `26.07.16`.

## Safety and artifacts

The runner rejects unexpected contexts, API servers older than 1.30, clusters
with no `linux/amd64` `Ready` schedulable Node, missing permissions, mutable
image references, locked source-image environment overrides, invalid or
incomplete image locks, invalid registry credentials, and unowned incompatible
CRDs before proceeding. Runs are serialized through
`cbse-test-system/cbse-smoke-lock` and never use `default`.

Generated-runner cleanup targets only the `${CBSE_REGISTRY}/cbse-test-runner`
repository; it never deletes or prunes the shared reference repositories
(`${CBSE_REGISTRY}` flat components or
`${CBSE_REGISTRY}/scenario-detail-database`).

Each run writes JUnit XML, a JSON summary, image digests, sanitized rendered manifests, events, pod descriptions, workload state, database assertions, and logs to `artifacts/test/<run-id>/`. Kubernetes Secret objects and their payloads are never collected.
