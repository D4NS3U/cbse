# EDS Integration Test (Scenario Manager)

> Deprecated: this directory now contains compatibility fixtures only. `run_eds_e2e.sh` delegates to the maintained full-stack suite documented in `test/e2e/README.md`.

This test environment validates the Scenario Manager EDS communication path:

1. Scenario Manager starts and subscribes to EDS subjects.
2. A `SimulationExperiment` CR is created so SM persists a `project` row.
3. A dedicated EDS mock container sends:
   - availability request (`cbse.eds.scenarios.available`)
   - two scenario batch publishes (`cbse.<project>.eds.scenarios`) via JetStream
4. Core DB is queried to assert scenario rows were inserted.

## Manifests

- `postgres-core-db.yaml`: Core DB deployment/service
- `nats-jetstream.yaml`: NATS server with JetStream enabled
- `manifests.yaml`: Scenario Manager deployment/RBAC/config
- `simulationexperiment-sm-eds-e2e.yaml`: test `SimulationExperiment` CR
- `eds-mock.yaml`: mock EDS deployment using the dedicated EDS image

## Prerequisites

- A reachable Kubernetes cluster context
- `kubectl` configured for that cluster
- Scenario Manager image available to the cluster
  - default in `manifests.yaml`: `docker.io/d4ns3u/cbse-testing:scenario-manager.test.26.7.16`
  - override at runtime with `SM_IMAGE=<your-image>`
- EDS mock image available to the cluster
  - default in `eds-mock.yaml`: `docker.io/d4ns3u/cbse-testing:eds-mock.test.26.7.16`
  - override at runtime with `EDS_IMAGE=<your-image>`

Build and push the Scenario Manager image from repo root:

```bash
make publish-test-images TEST_IMAGE_VERSION=26.7.16 \
  CBSE_REGISTRY_AUTH_FILE=/secure/dockerhub-config.json
```

Build and push the EDS mock image from repo root:

```bash
The same command publishes the EDS mock and Experiment Operator images.
```

## Run

From repo root:

```bash
./test-env/run_eds_e2e.sh
```

Optional overrides:

```bash
SM_IMAGE=docker.io/d4ns3u/cbse-testing:scenario-manager.test.26.7.16@sha256:... \
EDS_IMAGE=docker.io/d4ns3u/cbse-testing:eds-mock.test.26.7.16@sha256:... \
PROJECT_NAME=sm-eds-e2e \
EXPECTED_SCENARIOS=4 \
./test-env/run_eds_e2e.sh
```
