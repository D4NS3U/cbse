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

# acquire-lock.sh — serialize smoke runs with a Kubernetes Lease.
#
# Creates the coordination Lease cbse-smoke-lock in the shared cbse-test-system
# namespace with RUN_ID as holderIdentity and a 3600s duration, so only one
# smoke run mutates the test cluster at a time. It never deletes the Lease: the
# smoke orchestrator (smoke.sh) releases it on completion, and clean.sh releases
# it on manual teardown. If the Lease already exists, the create fails and this
# script reports the current holder and exits non-zero so the caller backs off.
#
# Inputs / environment:
#   KUBECTL   (required) path to the pinned kubectl binary.
#   KUBECONFIG (required) dedicated test-cluster kubeconfig.
#   RUN_ID    (required) 1-30 lowercase DNS-label chars; becomes holderIdentity.
#
# Exit codes:
#   0  Lease acquired (created).
#   3  Lease already held by another run; the current holder is printed.
#   Other non-zero from kubectl (auth/connect failure) via set -e.
#
# Side effects:
#   Creates Lease cbse-smoke-lock in namespace cbse-test-system. Writes a temp
#   Lease manifest (cleaned up on EXIT). No other cluster mutation.
set -euo pipefail

kubectl_bin="${KUBECTL:?KUBECTL is required}"
kubeconfig="${KUBECONFIG:?KUBECONFIG is required}"
run_id="${RUN_ID:?RUN_ID is required}"
tmp="$(mktemp)"
trap 'rm -f "${tmp}"' EXIT

cat >"${tmp}" <<EOF
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: cbse-smoke-lock
  namespace: cbse-test-system
  labels:
    app.kubernetes.io/part-of: cbse
spec:
  holderIdentity: ${run_id}
  leaseDurationSeconds: 3600
  # Kubernetes Lease timestamps require microsecond precision on this API.
  acquireTime: "$(date -u +%Y-%m-%dT%H:%M:%S.000000Z)"
EOF

if ! "${kubectl_bin}" --kubeconfig "${kubeconfig}" create -f "${tmp}" >/dev/null 2>&1; then
  holder="$(${kubectl_bin} --kubeconfig "${kubeconfig}" get lease cbse-smoke-lock -n cbse-test-system -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)"
  echo "Another smoke run holds the cluster lease: ${holder:-unknown}" >&2
  exit 3
fi
