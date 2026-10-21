# Getting Started

## Prerequisites

- **Go** (workspace mode — the repo is a `go.work` multi-module workspace) and **Python 3** (Translator templates and the generated runner are Python).
- **kubectl** (the harness pins its own binary under `bin/`) and a reachable Kubernetes cluster ≥ 1.30 via an explicit `KUBECONFIG`.
- A container registry accessible from the cluster: `CBSE_REGISTRY` (required, environment-provided — the repo embeds no default) and, if auth-protected, `CBSE_REGISTRY_AUTH_FILE` (a Docker `config.json`).
- **Docker / BuildKit** for building the component images (the Translator's image build runs rootless BuildKit as a sidecar).

## Installation

```bash
git clone https://github.com/D4NS3U/cbse.git
cd cbse
make test-tools   # pins the harness kubectl under bin/
```

## First Run

No cluster needed for the fast tier — unit and integration tests with the embedded fake docker:

```bash
make test-fast
```

The full chain against a real cluster (the smoke harness creates and removes its own `cbse-e2e-<run-id>` namespace and refuses the wrong cluster identity):

```bash
make publish-test-images CBSE_REGISTRY=<your-registry> CBSE_REGISTRY_AUTH_FILE=<docker-config>
make test-smoke KUBECONFIG=<path-to-test-cluster-config> CBSE_REGISTRY=<your-registry> CBSE_REGISTRY_AUTH_FILE=<docker-config>
```

`TEST_IMAGE_VERSION` defaults to a date-stamped version; images are referenced by digest, never by floating tag. Diagnostics and JUnit output land under `artifacts/test/<run-id>/` (never committed).

## Common Workflows

### Run one experiment by hand
Apply a `SimulationExperiment` CR (see the harness manifests for the shape) — the operator provisions the component set; drive the design and Finish through the EDS's embedded UI (served at the EDS service port).

### Diagnose a smoke run
```bash
make test-diagnose RUN_ID=<run-id>
```

### Tear down a retained run
Delete the experiment CRs first (the live Scenario Manager must release its cleanup finalizers), then:
```bash
make test-clean RUN_ID=<run-id> KUBECONFIG=<path-to-test-cluster-config>
```

## Configuration

- `KUBECONFIG` — **always set explicitly** for cluster work; the harness refuses unexpected cluster identities.
- `CBSE_REGISTRY` — the test image registry; required, environment-provided.
- `CBSE_REGISTRY_AUTH_FILE` — protected-registry credentials for push/pull.
- `TEST_IMAGE_VERSION` — image tag; defaults to a UTC date-stamped version.
- `SKIP_BUILD=1` — smoke with already-published digest-pinned images (bypasses flaky external registries).

## Where to Go Next

- Architecture: [architecture.md](architecture.md)
- The interface reference: [modules/communication-interfaces.md](modules/communication-interfaces.md)
- Module map: [README.md#module-map](README.md#module-map)
