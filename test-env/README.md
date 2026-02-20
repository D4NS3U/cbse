# EDS Integration Test (Scenario Manager)

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
  - default in `manifests.yaml`: `scenario-manager-test:eds-e2e`
  - override at runtime with `SM_IMAGE=<your-image>`
- EDS mock image available to the cluster
  - default in `eds-mock.yaml`: `eds-mock:sm-e2e`
  - override at runtime with `EDS_IMAGE=<your-image>`

Build the Scenario Manager image from repo root:

```bash
docker build -f scenario-manager/Dockerfile -t scenario-manager-test:eds-e2e .
```

Build the EDS mock image from repo root:

```bash
docker build -f test-env/eds-mock/Dockerfile -t eds-mock:sm-e2e .
```

## Run

From repo root:

```bash
./test-env/run_eds_e2e.sh
```

Optional overrides:

```bash
SM_IMAGE=<registry/scenario-manager:tag> \
EDS_IMAGE=<registry/eds-mock:tag> \
PROJECT_NAME=sm-eds-e2e \
EXPECTED_SCENARIOS=4 \
./test-env/run_eds_e2e.sh
```
