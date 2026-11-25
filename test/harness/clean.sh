#!/usr/bin/env bash
# Copyright 2025-2026 Daniel Seufferth
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# clean.sh — tear down one smoke run's namespace and release its Lease.
#
# Deletes the ephemeral namespace cbse-e2e-<RUN_ID> and, if this run still holds
# it, the cbse-smoke-lock Lease in cbse-test-system. The namespace is only
# deleted when its cbse.terministic.de/managed-run ownership label equals RUN_ID,
# so a stale or foreign namespace can never be removed by mistake. Before the
# namespace deletion, every SimulationExperiment in the namespace is deleted
# with --wait (up to 120s for the Scenario Manager cleanup finalizer to
# clear): deleting the namespace first would cascade the Scenario Manager
# Deployment and the CRs together, and a dead SM cannot release its own
# finalizer, so the CR (and with it the whole namespace) would wedge in
# Terminating forever. A CR deletion wait timeout is non-fatal: the deletions
# have been issued, and the namespace deletion proceeds best-effort after.
# When the run used the Helm installer, the release cbse is uninstalled after
# the CRs are gone and before the namespace deletion, so the chart's pre-delete
# experiment guard passes; an uninstall failure is surfaced (the script exits
# non-zero after the best-effort namespace/Lease cleanup) and never ignored.
# Both the namespace and the Lease deletion are idempotent: an already-absent
# resource is a no-op rather than an error.
#
# Inputs / environment:
#   KUBECTL    (required) path to the pinned kubectl binary.
#   KUBECONFIG (required) dedicated test-cluster kubeconfig.
#   RUN_ID     (required) 1-30 lowercase DNS-label chars; selects the namespace
#              and is checked against the namespace ownership label.
#   HELM       (optional) pinned helm binary; defaults to bin/helm-v3.18.4
#              under the repository root. Only consulted when the release
#              cbse exists in the run namespace (Helm-installed runs).
#
# Exit codes:
#   0  Namespace deleted (or absent), CRs deleted first (or absent), the Helm
#      release uninstalled (or absent), and Lease released if held.
#   1  The Helm release uninstall failed (surfaced; the namespace deletion and
#      Lease release still proceed best-effort).
#   2  RUN_ID is not a valid DNS label, or the namespace ownership label does
#      not match RUN_ID (refused deletion).
#   Other non-zero from kubectl via set -e.
#
# Side effects:
#   Deletes every SimulationExperiment in cbse-e2e-<RUN_ID> (--wait
#   --timeout=120s, only when the CRD exists), uninstalls the Helm release cbse
#   (--wait --timeout 3m, only when it exists), the namespace (--wait
#   --timeout=180s), and the cbse-smoke-lock Lease (--wait=false). Read-only
#   otherwise.
set -euo pipefail

kubectl_bin="${KUBECTL:?KUBECTL is required}"
kubeconfig="${KUBECONFIG:?KUBECONFIG is required}"
run_id="${RUN_ID:?RUN_ID is required}"
[[ "${run_id}" =~ ^[a-z0-9]([-a-z0-9]{0,28}[a-z0-9])?$ ]] || {
  echo "RUN_ID must be 1-30 lowercase DNS-label characters" >&2
  exit 2
}
namespace="cbse-e2e-${run_id}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
helm_bin="${HELM:-${root}/bin/helm-${HELM_VERSION:-v3.18.4}}"
uninstall_failed=0

crd_name="simulationexperiments.experiment.cbse.terministic.de"
if "${kubectl_bin}" --kubeconfig "${kubeconfig}" get namespace "${namespace}" >/dev/null 2>&1; then
  managed="$(${kubectl_bin} --kubeconfig "${kubeconfig}" get namespace "${namespace}" -o jsonpath='{.metadata.labels.cbse\.terministic\.de/managed-run}')"
  [[ "${managed}" == "${run_id}" ]] || {
    echo "Refusing to delete namespace ${namespace}: ownership label does not match ${run_id}" >&2
    exit 2
  }
  # CRs-first: delete every SimulationExperiment and wait for the Scenario
  # Manager cleanup finalizer to clear while the SM is still alive. If the
  # CRD is absent there can be no CRs; a wait timeout is non-fatal (the
  # deletions have been issued) so the namespace deletion still proceeds.
  if "${kubectl_bin}" --kubeconfig "${kubeconfig}" get crd "${crd_name}" >/dev/null 2>&1; then
    "${kubectl_bin}" --kubeconfig "${kubeconfig}" delete simulationexperiment --all \
      -n "${namespace}" --wait=true --timeout=120s ||
      echo "SimulationExperiment deletion wait timed out; proceeding with namespace deletion" >&2
  fi
  # Helm release (H5): with the CRs gone the chart pre-delete guard passes.
  # The uninstall only runs when the release cbse exists in the namespace
  # (Helm-installed runs); a failure is surfaced, never ignored - cleanup
  # proceeds best-effort and the script exits non-zero afterwards.
  if [[ -x "${helm_bin}" ]] && "${helm_bin}" status cbse -n "${namespace}" >/dev/null 2>&1; then
    "${helm_bin}" uninstall cbse -n "${namespace}" --wait --timeout 3m || {
      echo "Helm uninstall cbse failed; the pre-delete guard or cluster state requires attention (manual cleanup may be needed)" >&2
      uninstall_failed=1
    }
  fi
  "${kubectl_bin}" --kubeconfig "${kubeconfig}" delete namespace "${namespace}" --wait=true --timeout=180s
fi

holder="$(${kubectl_bin} --kubeconfig "${kubeconfig}" get lease cbse-smoke-lock -n cbse-test-system -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)"
if [[ "${holder}" == "${run_id}" ]]; then
  "${kubectl_bin}" --kubeconfig "${kubeconfig}" delete lease cbse-smoke-lock -n cbse-test-system --wait=false
fi

(( uninstall_failed == 0 )) || exit 1
