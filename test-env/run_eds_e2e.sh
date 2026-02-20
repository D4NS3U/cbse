#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${NAMESPACE:-default}"
PROJECT_NAME="${PROJECT_NAME:-sm-eds-e2e}"
EXPECTED_SCENARIOS="${EXPECTED_SCENARIOS:-4}"
SM_IMAGE="${SM_IMAGE:-}"
EDS_IMAGE="${EDS_IMAGE:-}"
SCENARIO_TABLE="${SCENARIO_TABLE:-scenario_status}"
PROJECT_TABLE="${PROJECT_TABLE:-project}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SIMEXP_TEMPLATE="${ROOT_DIR}/test-env/simulationexperiment-sm-eds-e2e.yaml"
SIMEXP_MANIFEST="$(mktemp)"

trap 'rm -f "${SIMEXP_MANIFEST}"' EXIT

if [[ ! "${EXPECTED_SCENARIOS}" =~ ^[0-9]+$ ]]; then
  echo "EXPECTED_SCENARIOS must be an integer, got: ${EXPECTED_SCENARIOS}" >&2
  exit 1
fi

sed "s/name: sm-eds-e2e/name: ${PROJECT_NAME}/" "${SIMEXP_TEMPLATE}" > "${SIMEXP_MANIFEST}"

echo "[1/10] Applying Core DB and NATS+JetStream resources"
kubectl apply -n "${NAMESPACE}" -f "${ROOT_DIR}/test-env/postgres-core-db.yaml"
kubectl apply -n "${NAMESPACE}" -f "${ROOT_DIR}/test-env/nats-jetstream.yaml"

echo "[2/10] Applying Scenario Manager resources"
kubectl apply -n "${NAMESPACE}" -f "${ROOT_DIR}/test-env/manifests.yaml"

if [[ -n "${SM_IMAGE}" ]]; then
  echo "Updating Scenario Manager image to ${SM_IMAGE}"
  kubectl set image deployment/scenario-manager-test \
    -n "${NAMESPACE}" scenario-manager="${SM_IMAGE}"
fi

echo "[3/10] Waiting for foundational deployments"
kubectl rollout status deployment/scenario-manager-postgres -n "${NAMESPACE}" --timeout=120s
kubectl rollout status deployment/nats -n "${NAMESPACE}" --timeout=120s
kubectl rollout status deployment/scenario-manager-test -n "${NAMESPACE}" --timeout=180s

echo "[4/10] Cleaning up prior test rows (best effort)"
kubectl delete deployment -n "${NAMESPACE}" eds-mock --ignore-not-found >/dev/null 2>&1 || true
kubectl exec -n "${NAMESPACE}" deploy/scenario-manager-postgres -- \
  psql -U scenariomanager -d scenarios -tAc \
  "DELETE FROM ${PROJECT_TABLE} WHERE project_name='${PROJECT_NAME}'" >/dev/null 2>&1 || true

echo "[5/10] Ensuring SimulationExperiment CRD exists"
kubectl apply -f "${ROOT_DIR}/experiment-operator/config/crd/bases/experiment.cbse.terministic.de_simulationexperiments.yaml"

echo "[6/10] Creating test SimulationExperiment"
kubectl delete -f "${SIMEXP_MANIFEST}" --ignore-not-found >/dev/null 2>&1 || true
kubectl apply -f "${SIMEXP_MANIFEST}"

echo "[7/10] Waiting until project row is available in Core DB"
attempts=60
for ((i=1; i<=attempts; i++)); do
  project_count="$(
    kubectl exec -n "${NAMESPACE}" deploy/scenario-manager-postgres -- \
      psql -U scenariomanager -d scenarios -tAc \
      "SELECT COUNT(*) FROM ${PROJECT_TABLE} WHERE project_name='${PROJECT_NAME}'" \
      | tr -d '[:space:]'
  )"
  if [[ "${project_count}" =~ ^[0-9]+$ ]] && (( project_count >= 1 )); then
    break
  fi
  if (( i == attempts )); then
    echo "Project row for ${PROJECT_NAME} was not created in time." >&2
    exit 1
  fi
  sleep 2
done

echo "[8/10] Deploying mock EDS container"
kubectl apply -n "${NAMESPACE}" -f "${ROOT_DIR}/test-env/eds-mock.yaml"
kubectl set env deployment/eds-mock -n "${NAMESPACE}" \
  PROJECT_NAME="${PROJECT_NAME}" \
  BATCH_ID_PREFIX="batch-${PROJECT_NAME}"

if [[ -n "${EDS_IMAGE}" ]]; then
  echo "Updating EDS mock image to ${EDS_IMAGE}"
  kubectl set image deployment/eds-mock -n "${NAMESPACE}" eds-mock="${EDS_IMAGE}"
fi

kubectl rollout status deployment/eds-mock -n "${NAMESPACE}" --timeout=180s

echo "[9/10] Waiting for EDS batches to be persisted"
attempts=90
actual_count="0"
for ((i=1; i<=attempts; i++)); do
  actual_count="$(
    kubectl exec -n "${NAMESPACE}" deploy/scenario-manager-postgres -- \
      psql -U scenariomanager -d scenarios -tAc \
      "SELECT COUNT(*) FROM ${SCENARIO_TABLE} ss JOIN ${PROJECT_TABLE} p ON p.id = ss.project_id WHERE p.project_name='${PROJECT_NAME}'" \
      | tr -d '[:space:]'
  )"
  if [[ "${actual_count}" =~ ^[0-9]+$ ]] && (( actual_count >= EXPECTED_SCENARIOS )); then
    break
  fi
  if (( i == attempts )); then
    echo "Timed out waiting for expected scenario rows." >&2
    kubectl logs -n "${NAMESPACE}" deployment/eds-mock --tail=200 || true
    exit 1
  fi
  sleep 2
done

echo "[10/10] Validating inserted scenario rows in Core DB"

echo "Expected scenarios: ${EXPECTED_SCENARIOS}"
echo "Actual scenarios:   ${actual_count}"

if (( actual_count < EXPECTED_SCENARIOS )); then
  echo "EDS integration test failed: scenario row count lower than expected." >&2
  exit 1
fi

echo "EDS integration test passed."
