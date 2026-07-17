#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
kubectl_bin="${KUBECTL:?KUBECTL is required}"
kubeconfig="${KUBECONFIG:?KUBECONFIG is required}"
run_id="${RUN_ID:-$(date -u +%Y%m%d%H%M%S)-$(openssl rand -hex 3)}"
[[ "${run_id}" =~ ^[a-z0-9]([-a-z0-9]{0,28}[a-z0-9])?$ ]] || {
  echo "RUN_ID must be 1-30 lowercase DNS-label characters" >&2
  exit 2
}

namespace="cbse-e2e-${run_id}"
project="smoke-${run_id}"
artifact_dir="${root}/artifacts/test/${run_id}"
pull_secret_name="${CBSE_PULL_SECRET_NAME:-dockerhub-auth}"
pull_secret_namespace="${CBSE_PULL_SECRET_NAMESPACE:-default}"
tmp="$(mktemp -d)"
lock_acquired=0
namespace_created=0
started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
mkdir -p "${artifact_dir}"
cat >"${artifact_dir}/junit.xml" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<testsuites tests="0" failures="0"></testsuites>
EOF

export RUN_ID="${run_id}"
export CBSE_TEST_NAMESPACE="${namespace}"
export CBSE_TEST_PROJECT="${project}"
export CBSE_ARTIFACT_DIR="${artifact_dir}"

finish() {
  status=$?
  set +e

  if (( namespace_created == 1 )); then
    KUBECTL="${kubectl_bin}" KUBECONFIG="${kubeconfig}" RUN_ID="${run_id}" \
      CBSE_TEST_NAMESPACE="${namespace}" CBSE_ARTIFACT_DIR="${artifact_dir}" \
      "${root}/hack/test/diagnose.sh"
  fi

  result="passed"
  if (( status != 0 )); then result="failed"; fi
  finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  cat >"${artifact_dir}/summary.json" <<EOF
{"run_id":"${run_id}","namespace":"${namespace}","project":"${project}","result":"${result}","started_at":"${started_at}","finished_at":"${finished_at}"}
EOF

  if (( namespace_created == 1 )); then
    if (( status == 0 )) || [[ "${CBSE_KEEP_ON_FAILURE:-0}" != "1" ]]; then
      "${kubectl_bin}" --kubeconfig "${kubeconfig}" delete namespace "${namespace}" --wait=true --timeout=180s >/dev/null 2>&1
    else
      echo "Retained failed run namespace: ${namespace}" >&2
    fi
  fi

  if (( lock_acquired == 1 )); then
    holder="$(${kubectl_bin} --kubeconfig "${kubeconfig}" get lease cbse-smoke-lock -n cbse-test-system -o jsonpath='{.spec.holderIdentity}' 2>/dev/null)"
    if [[ "${holder}" == "${run_id}" ]]; then
      "${kubectl_bin}" --kubeconfig "${kubeconfig}" delete lease cbse-smoke-lock -n cbse-test-system --wait=false >/dev/null 2>&1
    fi
  fi
  rm -rf "${tmp}"
  exit "${status}"
}
trap finish EXIT
trap 'exit 130' INT TERM

KUBECTL="${kubectl_bin}" KUBECONFIG="${kubeconfig}" "${root}/hack/test/preflight.sh" | tee "${artifact_dir}/preflight.txt"

"${kubectl_bin}" --kubeconfig "${kubeconfig}" create namespace cbse-test-system --dry-run=client -o yaml \
  | "${kubectl_bin}" --kubeconfig "${kubeconfig}" apply -f - >/dev/null
"${kubectl_bin}" --kubeconfig "${kubeconfig}" label namespace cbse-test-system \
  app.kubernetes.io/part-of=cbse cbse.terministic.de/test-infrastructure=true --overwrite >/dev/null

KUBECTL="${kubectl_bin}" KUBECONFIG="${kubeconfig}" RUN_ID="${run_id}" \
  "${root}/hack/test/acquire-lock.sh"
lock_acquired=1

if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  CBSE_REGISTRY="${CBSE_REGISTRY:-docker.io/d4ns3u/cbse-testing}" \
    TEST_IMAGE_VERSION="${TEST_IMAGE_VERSION:-26.7.16}" RUN_ID="${run_id}" \
    CBSE_REGISTRY_AUTH_FILE="${CBSE_REGISTRY_AUTH_FILE}" CBSE_IMAGE_ARTIFACT_DIR="${artifact_dir}" \
    "${root}/hack/test/build-images.sh"
  # shellcheck disable=SC1091
  source "${artifact_dir}/images.env"
else
  OPERATOR_IMAGE="${OPERATOR_IMAGE}"
  SM_IMAGE="${SM_IMAGE}"
  EDS_IMAGE="${EDS_IMAGE}"
  TRANS_IMAGE="${TRANS_IMAGE}"
fi
export OPERATOR_IMAGE SM_IMAGE EDS_IMAGE TRANS_IMAGE
printf 'OPERATOR_IMAGE=%s\nSM_IMAGE=%s\nEDS_IMAGE=%s\nTRANS_IMAGE=%s\n' \
  "${OPERATOR_IMAGE}" "${SM_IMAGE}" "${EDS_IMAGE}" "${TRANS_IMAGE}" >"${artifact_dir}/images.env"

crd="${root}/experiment-operator/config/crd/bases/experiment.cbse.terministic.de_simulationexperiments.yaml"
crd_name="simulationexperiments.experiment.cbse.terministic.de"
if "${kubectl_bin}" --kubeconfig "${kubeconfig}" get crd "${crd_name}" >/dev/null 2>&1; then
  owner="$(${kubectl_bin} --kubeconfig "${kubeconfig}" get crd "${crd_name}" -o jsonpath='{.metadata.labels.app\.kubernetes\.io/part-of}')"
  storage="$(${kubectl_bin} --kubeconfig "${kubeconfig}" get crd "${crd_name}" -o jsonpath='{.spec.versions[?(@.storage==true)].name}')"
  [[ "${storage}" == "alpha3" ]] || { echo "Existing CRD storage version is ${storage}, expected alpha3" >&2; exit 4; }
  if [[ "${owner}" != "cbse" && "${CBSE_ALLOW_CRD_UPGRADE:-0}" != "1" ]]; then
    echo "Existing CRD is not pipeline-managed; set CBSE_ALLOW_CRD_UPGRADE=1 after reviewing the diff" >&2
    exit 4
  fi
fi
"${kubectl_bin}" --kubeconfig "${kubeconfig}" apply --server-side --dry-run=server --field-manager=cbse-smoke -f "${crd}" >/dev/null
"${kubectl_bin}" --kubeconfig "${kubeconfig}" apply --server-side --field-manager=cbse-smoke -f "${crd}" >/dev/null
"${kubectl_bin}" --kubeconfig "${kubeconfig}" label crd "${crd_name}" app.kubernetes.io/part-of=cbse --overwrite >/dev/null

"${kubectl_bin}" --kubeconfig "${kubeconfig}" create namespace "${namespace}" >/dev/null
namespace_created=1
"${kubectl_bin}" --kubeconfig "${kubeconfig}" label namespace "${namespace}" \
  "cbse.terministic.de/managed-run=${run_id}" \
  app.kubernetes.io/part-of=cbse-smoke \
  pod-security.kubernetes.io/enforce=baseline \
  pod-security.kubernetes.io/audit=restricted \
  pod-security.kubernetes.io/warn=restricted --overwrite >/dev/null

db_password="$(openssl rand -hex 24)"
"${kubectl_bin}" --kubeconfig "${kubeconfig}" create secret generic core-db-credentials -n "${namespace}" \
  --from-literal=SCENARIO_MANAGER_CORE_DB_USER=cbse_test \
  --from-literal="SCENARIO_MANAGER_CORE_DB_PASSWORD=${db_password}" \
  --dry-run=client -o yaml | "${kubectl_bin}" --kubeconfig "${kubeconfig}" apply -f - >/dev/null
"${kubectl_bin}" --kubeconfig "${kubeconfig}" get secret "${pull_secret_name}" -n "${pull_secret_namespace}" -o json \
  | jq 'del(.metadata.namespace, .metadata.resourceVersion, .metadata.uid, .metadata.creationTimestamp, .metadata.managedFields, .metadata.ownerReferences)' \
  | "${kubectl_bin}" --kubeconfig "${kubeconfig}" apply -n "${namespace}" -f - >/dev/null
"${kubectl_bin}" --kubeconfig "${kubeconfig}" patch serviceaccount default -n "${namespace}" \
  --type=merge -p "{\"imagePullSecrets\":[{\"name\":\"${pull_secret_name}\"}]}" >/dev/null

"${kubectl_bin}" kustomize "${root}/test/e2e/manifests/base" >"${tmp}/stack.raw.yaml"
sed \
  -e "s|CBSE_NAMESPACE|${namespace}|g" \
  -e "s|CBSE_RUN_ID|${run_id}|g" \
  -e "s|CBSE_OPERATOR_IMAGE|${OPERATOR_IMAGE}|g" \
  -e "s|CBSE_SM_IMAGE|${SM_IMAGE}|g" \
  "${tmp}/stack.raw.yaml" >"${artifact_dir}/stack.yaml"
"${kubectl_bin}" --kubeconfig "${kubeconfig}" apply -n "${namespace}" -f "${artifact_dir}/stack.yaml" >/dev/null

for deployment in core-db sm-eds-nats experiment-operator scenario-manager; do
  "${kubectl_bin}" --kubeconfig "${kubeconfig}" rollout status deployment/"${deployment}" -n "${namespace}" --timeout=240s
done

scenario_manager_ready_deadline=$((SECONDS + 120))
until "${kubectl_bin}" --kubeconfig "${kubeconfig}" logs deployment/scenario-manager -n "${namespace}" --all-containers 2>/dev/null \
  | grep -q 'Scenario Manager is ready'; do
  if (( SECONDS >= scenario_manager_ready_deadline )); then
    echo "Scenario Manager did not report application readiness within 120 seconds" >&2
    exit 1
  fi
  sleep 2
done

sed \
  -e "s|CBSE_NAMESPACE|${namespace}|g" \
  -e "s|CBSE_PROJECT|${project}|g" \
  -e "s|CBSE_RUN_ID|${run_id}|g" \
  -e "s|CBSE_SUPPORT_IMAGE|${EDS_IMAGE}|g" \
  -e "s|CBSE_TRANS_IMAGE|${TRANS_IMAGE}|g" \
  "${root}/test/e2e/manifests/experiment.yaml" >"${artifact_dir}/experiment.yaml"
"${kubectl_bin}" --kubeconfig "${kubeconfig}" apply -f "${artifact_dir}/experiment.yaml" >/dev/null

cd "${root}/test/e2e"
go test -tags=e2e -count=1 -v ./... \
  -ginkgo.v -ginkgo.junit-report="${artifact_dir}/junit.xml"
