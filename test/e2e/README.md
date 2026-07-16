# CBSE full-stack smoke test

This suite deploys the current Experiment Operator and Scenario Manager with real PostgreSQL and NATS/JetStream dependencies. An EDS support image supplies two deterministic batches and acts as a keep-alive mock for components that are not implemented yet.

## Prerequisites

- Go 1.24 or newer, Docker with Buildx, `curl`, `jq`, and OpenSSL.
- A readable kubeconfig. Linux agents normally use `/home/d4ns3u/.kube/config`; set another explicit path when needed.
- Access to the K3s API server at `https://192.168.101.245:6443`.
- Access to the private Docker Hub repository `d4ns3u/cbse-testing` from both the agent and K3s node.
- A dedicated Docker `config.json` containing a Docker Hub PAT in `CBSE_REGISTRY_AUTH_FILE`.

The harness downloads kubectl v1.32.5 into the ignored root `bin/` directory.

## Run

```bash
make test-smoke \
  KUBECONFIG=/home/d4ns3u/.kube/config \
  TEST_IMAGE_VERSION=26.7.16 \
  CBSE_REGISTRY_AUTH_FILE=/secure/dockerhub-config.json
```

To create the dedicated authentication file, log in with a Docker Hub PAT using an isolated `DOCKER_CONFIG` directory and pass its `config.json` path. Do not use a credential helper for this file and do not commit it.

To reuse already published images, every reference must include a digest:

```bash
SKIP_BUILD=1 \
OPERATOR_IMAGE=docker.io/d4ns3u/cbse-testing:experiment-operator.test.26.7.16@sha256:... \
SM_IMAGE=docker.io/d4ns3u/cbse-testing:scenario-manager.test.26.7.16@sha256:... \
EDS_IMAGE=docker.io/d4ns3u/cbse-testing:eds-mock.test.26.7.16@sha256:... \
CBSE_REGISTRY_AUTH_FILE=/secure/dockerhub-config.json \
make test-smoke KUBECONFIG=/home/d4ns3u/.kube/config
```

`CBSE_KEEP_ON_FAILURE=1` retains a failed namespace. Inspect it with `make test-diagnose RUN_ID=<id>` and remove it with `make test-clean RUN_ID=<id>`. Neither cleanup path removes the shared CRD.

## Safety and artifacts

The runner rejects unexpected contexts, API servers, non-K3s clusters, missing permissions, mutable image references, invalid registry credentials, and unowned incompatible CRDs before proceeding. Runs are serialized through `cbse-test-system/cbse-smoke-lock` and never use `default`.

Each run writes JUnit XML, a JSON summary, image digests, sanitized rendered manifests, events, pod descriptions, workload state, database assertions, and logs to `artifacts/test/<run-id>/`. Kubernetes Secret objects and their payloads are never collected.
