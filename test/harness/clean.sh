#!/usr/bin/env bash
# clean.sh — tear down one smoke run's namespace and release its Lease.
#
# Deletes the ephemeral namespace cbse-e2e-<RUN_ID> and, if this run still holds
# it, the cbse-smoke-lock Lease in cbse-test-system. The namespace is only
# deleted when its cbse.terministic.de/managed-run ownership label equals RUN_ID,
# so a stale or foreign namespace can never be removed by mistake. Both the
# namespace and the Lease deletion are idempotent: an already-absent resource is
# a no-op rather than an error.
#
# Inputs / environment:
#   KUBECTL    (required) path to the pinned kubectl binary.
#   KUBECONFIG (required) dedicated test-cluster kubeconfig.
#   RUN_ID     (required) 1-30 lowercase DNS-label chars; selects the namespace
#              and is checked against the namespace ownership label.
#
# Exit codes:
#   0  Namespace deleted (or absent) and Lease released if held.
#   2  RUN_ID is not a valid DNS label, or the namespace ownership label does
#      not match RUN_ID (refused deletion).
#   Other non-zero from kubectl via set -e.
#
# Side effects:
#   Deletes namespace cbse-e2e-<RUN_ID> (with --wait --timeout=180s) and the
#   cbse-smoke-lock Lease (--wait=false). Read-only otherwise.
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
