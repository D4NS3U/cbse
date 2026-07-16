#!/usr/bin/env bash
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
  acquireTime: "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
EOF

if ! "${kubectl_bin}" --kubeconfig "${kubeconfig}" create -f "${tmp}" >/dev/null 2>&1; then
  holder="$(${kubectl_bin} --kubeconfig "${kubeconfig}" get lease cbse-smoke-lock -n cbse-test-system -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)"
  echo "Another smoke run holds the cluster lease: ${holder:-unknown}" >&2
  exit 3
fi
