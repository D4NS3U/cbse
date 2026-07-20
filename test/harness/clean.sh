#!/usr/bin/env bash
set -euo pipefail

kubectl_bin="${KUBECTL:?KUBECTL is required}"
kubeconfig="${KUBECONFIG:?KUBECONFIG is required}"
run_id="${RUN_ID:?RUN_ID is required}"
[[ "${run_id}" =~ ^[a-z0-9]([-a-z0-9]{0,28}[a-z0-9])?$ ]] || {
  echo "RUN_ID must be 1-30 lowercase DNS-label characters" >&2
  exit 2
}
namespace="cbse-e2e-${run_id}"

if "${kubectl_bin}" --kubeconfig "${kubeconfig}" get namespace "${namespace}" >/dev/null 2>&1; then
  managed="$(${kubectl_bin} --kubeconfig "${kubeconfig}" get namespace "${namespace}" -o jsonpath='{.metadata.labels.cbse\.terministic\.de/managed-run}')"
  [[ "${managed}" == "${run_id}" ]] || {
    echo "Refusing to delete namespace ${namespace}: ownership label does not match ${run_id}" >&2
    exit 2
  }
  "${kubectl_bin}" --kubeconfig "${kubeconfig}" delete namespace "${namespace}" --wait=true --timeout=180s
fi

holder="$(${kubectl_bin} --kubeconfig "${kubeconfig}" get lease cbse-smoke-lock -n cbse-test-system -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)"
if [[ "${holder}" == "${run_id}" ]]; then
  "${kubectl_bin}" --kubeconfig "${kubeconfig}" delete lease cbse-smoke-lock -n cbse-test-system --wait=false
fi
