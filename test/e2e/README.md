# CBSE full-stack smoke test

This suite deploys the current Experiment Operator and Scenario Manager with real PostgreSQL and NATS/JetStream dependencies. An EDS support image supplies two deterministic batches and acts as a keep-alive mock for components that are not implemented yet.

## Prerequisites

- Go 1.24 or newer, Docker with Buildx, `curl`, `jq`, and OpenSSL.
- A readable kubeconfig. Linux agents normally use `/home/d4ns3u/.kube/config`; set another explicit path when needed.
- Access to the K3s API server at `https://192.168.101.245:6443`.
- Access to the University Harbor repository `registry.unibw.de/i31bdase/cbse-test` from both the agent and K3s node.
- A dedicated Docker `config.json` provided through `CBSE_REGISTRY_AUTH_FILE`.
- A `kubernetes.io/dockerconfigjson` Secret named `cbse-registry-auth` in `cbse-test-system`; the harness copies it only to its ephemeral test namespace.

The harness downloads kubectl v1.32.5 into the ignored root `bin/` directory.

## Run

```bash
make test-smoke \
  KUBECONFIG=/home/d4ns3u/.kube/config \
  TEST_IMAGE_VERSION=26.7.16 \
  CBSE_REGISTRY_AUTH_FILE=<protected-docker-config>
```

An authorized administrator provisions the dedicated Docker configuration and the shared Kubernetes Secret outside this repository. Do not use a credential helper for the configuration and do not commit it.

To reuse already published images, every reference must include a digest:

```bash
SKIP_BUILD=1 \
OPERATOR_IMAGE=registry.unibw.de/i31bdase/cbse-test:exop.test.26.7.16@sha256:... \
SM_IMAGE=registry.unibw.de/i31bdase/cbse-test:sm.test.26.7.16@sha256:... \
EDS_IMAGE=registry.unibw.de/i31bdase/cbse-test:eds-mock.test.26.7.16@sha256:... \
TRANS_IMAGE=registry.unibw.de/i31bdase/cbse-test:trans-mock.test.26.7.16@sha256:... \
CBSE_REGISTRY_AUTH_FILE=<protected-docker-config> \
make test-smoke KUBECONFIG=/home/d4ns3u/.kube/config
```

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

## Safety and artifacts

The runner rejects unexpected contexts, API servers, non-K3s clusters, missing permissions, mutable image references, invalid registry credentials, and unowned incompatible CRDs before proceeding. Runs are serialized through `cbse-test-system/cbse-smoke-lock` and never use `default`.

Each run writes JUnit XML, a JSON summary, image digests, sanitized rendered manifests, events, pod descriptions, workload state, database assertions, and logs to `artifacts/test/<run-id>/`. Kubernetes Secret objects and their payloads are never collected.
