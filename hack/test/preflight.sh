#!/usr/bin/env bash
set -euo pipefail

kubectl_bin="${KUBECTL:?KUBECTL is required}"
kubeconfig="${KUBECONFIG:?KUBECONFIG must point to the dedicated test-cluster config}"
expected_server="${CBSE_EXPECTED_APISERVER:-https://192.168.101.245:6443}"
expected_context="${CBSE_EXPECTED_CONTEXT:-default}"

command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

[[ -r "${kubeconfig}" ]] || { echo "Kubeconfig is not readable: ${kubeconfig}" >&2; exit 2; }
[[ -x "${kubectl_bin}" ]] || { echo "Pinned kubectl is not executable: ${kubectl_bin}" >&2; exit 2; }

actual_context="$(${kubectl_bin} --kubeconfig "${kubeconfig}" config current-context)"
actual_server="$(${kubectl_bin} --kubeconfig "${kubeconfig}" config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
[[ "${actual_context}" == "${expected_context}" ]] || {
  echo "Refusing cluster mutation: context ${actual_context} does not match ${expected_context}" >&2
  exit 2
}
[[ "${actual_server}" == "${expected_server}" ]] || {
  echo "Refusing cluster mutation: API server ${actual_server} does not match ${expected_server}" >&2
  exit 2
}

server_version="$(${kubectl_bin} --kubeconfig "${kubeconfig}" version -o json | jq -r '.serverVersion.gitVersion')"
[[ "${server_version}" == *k3s* ]] || {
  echo "Refusing cluster mutation: expected K3s, got ${server_version}" >&2
  exit 2
}

for check in \
  "create namespaces" \
  "delete namespaces" \
  "create leases.coordination.k8s.io --namespace cbse-test-system" \
  "create roles.rbac.authorization.k8s.io --namespace cbse-test-system" \
  "create rolebindings.rbac.authorization.k8s.io --namespace cbse-test-system" \
  "create serviceaccounts --namespace cbse-test-system" \
  "create secrets --namespace cbse-test-system" \
  "create configmaps --namespace cbse-test-system" \
  "create services --namespace cbse-test-system" \
  "create deployments.apps --namespace cbse-test-system" \
  "create customresourcedefinitions.apiextensions.k8s.io" \
  "patch customresourcedefinitions.apiextensions.k8s.io"; do
  read -r verb resource scope <<<"${check}"
  args=(auth can-i "${verb}" "${resource}")
  if [[ -n "${scope:-}" ]]; then args+=( ${scope} ); fi
  allowed="$(${kubectl_bin} --kubeconfig "${kubeconfig}" "${args[@]}")"
  [[ "${allowed}" == "yes" ]] || { echo "Missing Kubernetes permission: ${check}" >&2; exit 2; }
done

if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  command -v docker >/dev/null || { echo "docker is required to build current-source images" >&2; exit 2; }
  docker info >/dev/null || { echo "Docker daemon is not available" >&2; exit 2; }
  docker buildx version >/dev/null || { echo "Docker Buildx is required" >&2; exit 2; }
  registry="${CBSE_REGISTRY:-docker.io/d4ns3u/cbse-testing}"
  registry_host="${registry%%/*}"
  probe_host="${registry_host}"
  [[ "${probe_host}" == "docker.io" ]] && probe_host="registry-1.docker.io"
  status="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' "https://${probe_host}/v2/" || true)"
  [[ "${status}" == "200" || "${status}" == "401" ]] || {
    echo "Registry TLS/connectivity preflight failed for ${probe_host}; insecure TLS is not supported" >&2
    exit 2
  }
else
  for variable in OPERATOR_IMAGE SM_IMAGE EDS_IMAGE; do
    value="${!variable:-}"
    [[ "${value}" == *@sha256:* ]] || {
      echo "${variable} must be an immutable digest reference when SKIP_BUILD=1" >&2
      exit 2
    }
  done
fi

registry_auth_file="${CBSE_REGISTRY_AUTH_FILE:?CBSE_REGISTRY_AUTH_FILE must point to a dedicated Docker config.json}"
[[ -r "${registry_auth_file}" ]] || { echo "Registry auth file is not readable: ${registry_auth_file}" >&2; exit 2; }
jq -e '.auths | type == "object" and length > 0' "${registry_auth_file}" >/dev/null || {
  echo "CBSE_REGISTRY_AUTH_FILE is not a valid non-empty Docker config" >&2
  exit 2
}
if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  jq -e --arg host "${registry_host}" \
    '.auths | has($host) or has("https://" + $host) or has("https://" + $host + "/v1/") or ($host == "docker.io" and (has("https://index.docker.io/v1/") or has("https://registry-1.docker.io")))' \
    "${registry_auth_file}" >/dev/null || {
    echo "Registry auth file has no credentials for ${registry_host}" >&2
    exit 2
  }
fi

echo "Preflight passed: context=${actual_context} server=${actual_server} version=${server_version}"
