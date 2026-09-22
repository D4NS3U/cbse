#!/usr/bin/env bash
# diagnose.sh — collect post-run cluster diagnostics into the artifact dir.
#
# Gathers the run namespace's resources, events, pod descriptions,
# SimulationExperiment YAML, per-pod container images, and per-pod logs
# (current and previous container instances) so a failed smoke run can be
# triaged from the artifacts alone. It is read-only against the cluster and
# idempotent: if the target namespace does not exist it writes a note to
# cluster-state.txt and exits 0. Typically invoked from smoke.sh's finish
# trap after a run, success or failure.
#
# Inputs / environment:
#   KUBECTL    (required) path to the pinned kubectl binary.
#   KUBECONFIG (required) dedicated test-cluster kubeconfig.
#   RUN_ID     (required) 1-30 lowercase DNS-label chars.
#   CBSE_TEST_NAMESPACE (default cbse-e2e-<RUN_ID>) namespace to inspect.
#   CBSE_ARTIFACT_DIR   (default <root>/artifacts/test/<RUN_ID>) output dir.
#
# Exit codes:
#   0  diagnostics written, or the namespace is absent.
#   2  RUN_ID is not a valid DNS label.
#
# Side effects:
#   Creates <artifact_dir>/logs/ and writes cluster-state.txt, events.txt,
#   pod-descriptions.txt, simulationexperiments.yaml, images.txt, and
#   logs/<pod>.log + logs/<pod>-previous.log. No cluster mutation.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
kubectl_bin="${KUBECTL:?KUBECTL is required}"
kubeconfig="${KUBECONFIG:?KUBECONFIG is required}"
run_id="${RUN_ID:?RUN_ID is required}"
[[ "${run_id}" =~ ^[a-z0-9]([-a-z0-9]{0,28}[a-z0-9])?$ ]] || {
  echo "RUN_ID must be 1-30 lowercase DNS-label characters" >&2
  exit 2
}
namespace="${CBSE_TEST_NAMESPACE:-cbse-e2e-${run_id}}"
artifact_dir="${CBSE_ARTIFACT_DIR:-${root}/artifacts/test/${run_id}}"
mkdir -p "${artifact_dir}/logs"

if ! "${kubectl_bin}" --kubeconfig "${kubeconfig}" get namespace "${namespace}" >/dev/null 2>&1; then
  echo "Namespace ${namespace} does not exist" >"${artifact_dir}/cluster-state.txt"
  exit 0
fi

"${kubectl_bin}" --kubeconfig "${kubeconfig}" get all,configmaps,serviceaccounts,roles,rolebindings,simulationexperiments \
  -n "${namespace}" -o wide >"${artifact_dir}/cluster-state.txt" 2>&1 || true
"${kubectl_bin}" --kubeconfig "${kubeconfig}" get events -n "${namespace}" --sort-by=.lastTimestamp \
  >"${artifact_dir}/events.txt" 2>&1 || true
"${kubectl_bin}" --kubeconfig "${kubeconfig}" describe pods -n "${namespace}" \
  >"${artifact_dir}/pod-descriptions.txt" 2>&1 || true
"${kubectl_bin}" --kubeconfig "${kubeconfig}" get simulationexperiments -n "${namespace}" -o yaml \
  >"${artifact_dir}/simulationexperiments.yaml" 2>&1 || true
"${kubectl_bin}" --kubeconfig "${kubeconfig}" get pods -n "${namespace}" \
  -o custom-columns='NAME:.metadata.name,IMAGE:.spec.containers[*].image,PHASE:.status.phase' \
  >"${artifact_dir}/images.txt" 2>&1 || true

while IFS= read -r pod; do
  [[ -n "${pod}" ]] || continue
  safe_name="${pod#pod/}"
  "${kubectl_bin}" --kubeconfig "${kubeconfig}" logs -n "${namespace}" "${pod}" --all-containers --timestamps \
    >"${artifact_dir}/logs/${safe_name}.log" 2>&1 || true
  "${kubectl_bin}" --kubeconfig "${kubeconfig}" logs -n "${namespace}" "${pod}" --all-containers --previous --timestamps \
    >"${artifact_dir}/logs/${safe_name}-previous.log" 2>&1 || true
done < <("${kubectl_bin}" --kubeconfig "${kubeconfig}" get pods -n "${namespace}" -o name 2>/dev/null || true)
