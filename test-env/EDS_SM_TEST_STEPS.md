# EDS <-> Scenario Manager Communication Test
## Detailed Step-by-Step Runbook

## 1) What This Test Does
This test validates the integration path between:

- Scenario Manager (SM)
- Experimental Design Service mock (EDS mock)
- NATS + JetStream
- Core Postgres database used by SM
- SimulationExperiment custom resource (CR)

Expected behavior:

1. SM starts, connects to NATS and Postgres, and subscribes to EDS subjects.
2. A `SimulationExperiment` CR is created so SM persists a project row.
3. EDS mock sends availability requests to SM over NATS request/reply.
4. SM responds with the target batch subject.
5. EDS mock publishes scenario batches to JetStream.
6. SM consumes those batches and inserts scenario rows into Postgres.
7. Test succeeds if scenario row count reaches expected value.

Current default expected count:

- 2 batches
- 2 scenarios per batch
- Total expected scenarios: 4

## 2) Files Used By This Test
- `test-env/run_eds_e2e.sh`  
  Main orchestration script for the full e2e flow.
- `test-env/postgres-core-db.yaml`  
  Postgres deployment/service for SM core DB.
- `test-env/nats-jetstream.yaml`  
  NATS deployment/service with JetStream enabled.
- `test-env/manifests.yaml`  
  Scenario Manager deployment + config + RBAC.
- `test-env/simulationexperiment-sm-eds-e2e.yaml`  
  SimulationExperiment CR definition for this test.
- `test-env/eds-mock/eds_mock.py`  
  Mock EDS implementation (availability handshake + batch publishing).
- `test-env/eds-mock/Dockerfile`  
  Docker image definition for the EDS mock.
- `test-env/eds-mock.yaml`  
  Kubernetes deployment for EDS mock image.

## 3) Prerequisites
You need:

1. A reachable Kubernetes cluster and valid kubeconfig context.
2. `kubectl` installed and configured.
3. Cluster permission to create:
   - Deployments
   - Services
   - ConfigMaps
   - RBAC objects
   - CustomResourceDefinitions (CRD)
   - Custom resources (`SimulationExperiment`)
4. Docker (or another image builder) to build container images.
5. Cluster ability to pull your images:
   - Either local image loading (`kind` / `minikube` / `k3d`)
   - Or pushed images from a reachable registry

## 4) Decide Namespace And Project Name First
Defaults:

- Namespace: `default`
- Project name (`SimulationExperiment` name): `sm-eds-e2e`

Runtime overrides:

- `NAMESPACE=<namespace>`
- `PROJECT_NAME=<name>`

Important: `PROJECT_NAME` is used in batch subject routing and DB verification.

## 5) Build Images
Run these commands from repository root:

```bash
docker build -f scenario-manager/Dockerfile -t logsimharbor.informatik.unibw-muenchen.de/cbse/scenario-manager-test:v0.2 .
docker build -f test-env/eds-mock/Dockerfile -t logsimharbor.informatik.unibw-muenchen.de/cbse/eds-test:v0.1 .
```

Verify images exist locally:

```bash
docker images | grep -E "scenario-manager-test|eds-mock"
```

## 6) Make Images Available To Your Cluster
Use one approach:

### Approach A: Cluster can pull local daemon images directly
- Works in some local setups.
- If unsure, use Approach B or C.

### Approach B: Load local images into local cluster runtime
For `kind`:

```bash
kind load docker-image logsimharbor.informatik.unibw-muenchen.de/cbse/scenario-manager-test:v0.2
kind load docker-image logsimharbor.informatik.unibw-muenchen.de/cbse/eds-test:v0.1
```

For `minikube`:

```bash
minikube image load logsimharbor.informatik.unibw-muenchen.de/cbse/scenario-manager-test:v0.2
minikube image load logsimharbor.informatik.unibw-muenchen.de/cbse/eds-test:v0.1
```

For `k3d`:

```bash
k3d image import logsimharbor.informatik.unibw-muenchen.de/cbse/scenario-manager-test:v0.2
k3d image import logsimharbor.informatik.unibw-muenchen.de/cbse/eds-test:v0.1
```

### Approach C: Push to registry and use fully-qualified image tags
Example:

```bash
docker push logsimharbor.informatik.unibw-muenchen.de/cbse/scenario-manager-test:v0.2
docker push logsimharbor.informatik.unibw-muenchen.de/cbse/eds-test:v0.1
```

Then pass:

- `SM_IMAGE=logsimharbor.informatik.unibw-muenchen.de/cbse/scenario-manager-test:v0.2`
- `EDS_IMAGE=logsimharbor.informatik.unibw-muenchen.de/cbse/eds-test:v0.1`

## 7) Run The Complete Test
Minimal run (uses default image tags in manifests):

```bash
./test-env/run_eds_e2e.sh
```

Run with explicit overrides:

```bash
NAMESPACE=default \
PROJECT_NAME=sm-eds-e2e \
EXPECTED_SCENARIOS=4 \
SM_IMAGE=logsimharbor.informatik.unibw-muenchen.de/cbse/scenario-manager-test:v0.2 \
EDS_IMAGE=logsimharbor.informatik.unibw-muenchen.de/cbse/eds-test:v0.1 \
./test-env/run_eds_e2e.sh
```

If you pushed images to a registry, set `SM_IMAGE` and `EDS_IMAGE` to those tags.

## 8) What `run_eds_e2e.sh` Does Internally
The script executes these phases:

1. Apply Postgres and NATS/JetStream manifests.
2. Apply Scenario Manager manifests.
3. Wait for rollout of:
   - `scenario-manager-postgres`
   - `nats`
   - `scenario-manager-test`
4. Cleanup previous test state:
   - Delete old `eds-mock` deployment (best effort)
   - Delete previous project row in Postgres by project name (best effort)
5. Apply `SimulationExperiment` CRD.
6. Apply `SimulationExperiment` test resource.
7. Poll Postgres until project row exists.
8. Deploy EDS mock container and set runtime env values:
   - `PROJECT_NAME`
   - `BATCH_ID_PREFIX`
   - Optional `EDS_IMAGE` override
9. Poll Postgres until scenario count reaches `EXPECTED_SCENARIOS`.
10. Print expected/actual count and fail or pass.

## 9) Validate Manually (Optional But Recommended)
Check pods:

```bash
kubectl get pods -n default
```

Check Scenario Manager logs:

```bash
kubectl logs -n default deployment/scenario-manager-test --tail=200
```

Check EDS mock logs:

```bash
kubectl logs -n default deployment/eds-mock --tail=200
```

You should see lines similar to:

- EDS connected to NATS
- Availability accepted with batch subject
- Batches published successfully

Check scenario rows directly:

```bash
kubectl exec -n default deploy/scenario-manager-postgres -- \
  psql -U scenariomanager -d scenarios -tAc \
  "SELECT COUNT(*) FROM scenario_status ss JOIN project p ON p.id = ss.project_id WHERE p.project_name='sm-eds-e2e'"
```

## 10) Typical Success Criteria
The test is successful when all are true:

1. `run_eds_e2e.sh` exits with code 0.
2. Script prints:
   - `Expected scenarios: 4`
   - `Actual scenarios: >= 4`
3. SM logs do not show persistent batch insert failures.
4. EDS logs confirm batch publish ACKs from JetStream.

## 11) Cleanup After The Test
Remove created resources (for a clean environment):

```bash
kubectl delete -f test-env/eds-mock.yaml --ignore-not-found
kubectl delete -f test-env/simulationexperiment-sm-eds-e2e.yaml --ignore-not-found
kubectl delete -f test-env/manifests.yaml --ignore-not-found
kubectl delete -f test-env/nats-jetstream.yaml --ignore-not-found
kubectl delete -f test-env/postgres-core-db.yaml --ignore-not-found
```

Optional: remove CRD if this environment is test-only:

```bash
kubectl delete -f experiment-operator/config/crd/bases/experiment.cbse.terministic.de_simulationexperiments.yaml --ignore-not-found
```

## 12) Troubleshooting Guide
### Problem: Scenario Manager pod is `CrashLoopBackOff`
Checks:

- Verify `SCENARIO_MANAGER_CORE_DB_*` config in `test-env/manifests.yaml`.
- Verify Postgres pod is healthy and reachable.
- Verify `SCENARIO_MANAGER_NATS_URL` points to `nats` service.
- Read SM logs for dependency check failures.

### Problem: EDS mock cannot connect to NATS
Checks:

- Verify `nats` deployment is `Running`.
- Verify service name is exactly `sm-eds-nats` in same namespace.
- Verify `NATS_URL` env in `test-env/eds-mock.yaml`.
- Check network policies if present.

### Problem: EDS publishes but scenario rows remain 0
Checks:

- Confirm project row exists in `project` table first.
- Confirm `PROJECT_NAME` in EDS env matches SimulationExperiment name.
- Confirm SM logs show successful batch processing and insert.
- Confirm subject templates match:
  - Availability: `cbse.eds.scenarios.available`
  - Batch: `cbse.<project>.eds.scenarios`

### Problem: `ImagePullBackOff`
Checks:

- If using local images, load them into cluster runtime.
- If using remote registry images, ensure tags exist.
- Configure image pull secrets if registry is private.

### Problem: Script times out waiting for expected rows
Checks:

- Increase `EXPECTED_SCENARIOS` only if EDS publishes more data.
- Inspect EDS and SM logs to find the first failing step.
- Re-run with same project name only after cleanup, or use a new `PROJECT_NAME`.

## 13) Recommended Command Sequence (Copy/Paste Template)
Run from repo root:

```bash
docker build -f scenario-manager/Dockerfile -t logsimharbor.informatik.unibw-muenchen.de/cbse/scenario-manager-test:v0.2 .
docker build -f test-env/eds-mock/Dockerfile -t logsimharbor.informatik.unibw-muenchen.de/cbse/eds-test:v0.1 .

# Only if needed for your local cluster:
kind load docker-image logsimharbor.informatik.unibw-muenchen.de/cbse/scenario-manager-test:v0.2
kind load docker-image logsimharbor.informatik.unibw-muenchen.de/cbse/eds-test:v0.1

NAMESPACE=default \
PROJECT_NAME=sm-eds-e2e \
EXPECTED_SCENARIOS=4 \
SM_IMAGE=logsimharbor.informatik.unibw-muenchen.de/cbse/scenario-manager-test:v0.2 \
EDS_IMAGE=logsimharbor.informatik.unibw-muenchen.de/cbse/eds-test:v0.1 \
./test-env/run_eds_e2e.sh

kubectl logs -n default deployment/eds-mock --tail=200
kubectl logs -n default deployment/scenario-manager-test --tail=200
```

## 14) Notes For Extension
If you want stronger coverage later:

1. Increase `TOTAL_BATCHES` and `SCENARIOS_PER_BATCH` in `test-env/eds-mock.yaml`.
2. Update `EXPECTED_SCENARIOS` accordingly when launching the script.
3. Add failure-case batches (invalid payloads) in `test-env/eds-mock/eds_mock.py`.
4. Add explicit assertions around redelivery/ACK behavior.
5. Add a dedicated CI step that executes this script in a disposable cluster.
