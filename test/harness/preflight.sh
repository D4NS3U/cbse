#!/usr/bin/env bash
set -euo pipefail

kubectl_bin="${KUBECTL:?KUBECTL is required}"
kubeconfig="${KUBECONFIG:?KUBECONFIG must point to the dedicated test-cluster config}"
expected_server="${CBSE_EXPECTED_APISERVER:-https://192.168.101.245:6443}"
expected_context="${CBSE_EXPECTED_CONTEXT:-default}"
pull_secret_name="${CBSE_PULL_SECRET_NAME:-cbse-registry-auth}"
pull_secret_namespace="${CBSE_PULL_SECRET_NAMESPACE:-cbse-test-system}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
lock_file="${root}/test/e2e/images.lock.env"

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

# Kubernetes 1.30 minimum compatibility: accept any 1.x server whose minor
# version is at least 30, including distribution suffixes such as K3s. Impose
# no upper minor bound. Reject 1.29 or older, a non-1 major, or unparsable
# version data before any cluster or registry mutation.
server_version="$(${kubectl_bin} --kubeconfig "${kubeconfig}" version -o json | jq -r '.serverVersion.gitVersion')"
if [[ "${server_version}" =~ ^v?([0-9]+)\.([0-9]+)\. ]]; then
  k8s_major="${BASH_REMATCH[1]}"
  k8s_minor="${BASH_REMATCH[2]}"
else
  echo "Unable to parse Kubernetes server version: ${server_version}" >&2
  exit 2
fi
[[ "${k8s_major}" == "1" ]] || { echo "Unsupported Kubernetes major version ${k8s_major}; expected 1" >&2; exit 2; }
[[ "${k8s_minor}" -ge 30 ]] || { echo "Kubernetes ${server_version} is older than the required 1.30 minimum" >&2; exit 2; }

# The alpha4 smoke profile is linux/amd64 only. Require at least one Node
# whose Ready condition is True, that is not cordoned (spec.unschedulable
# absent or false), and whose kubernetes.io/arch label is exactly amd64.
# Other Nodes neither qualify nor cause failure. This check adds no Node
# permission to the application ServiceAccounts; preflight runs with the
# dedicated admin kubeconfig and inspects no taints, capacity, or pressure.
nodes_json="$(${kubectl_bin} --kubeconfig "${kubeconfig}" get nodes -o json)"
amd64_ready="$(printf '%s' "${nodes_json}" | jq -r '
  [.items[] | select(
    ((.status.conditions // []) | any(.type == "Ready" and .status == "True"))
    and ((.spec.unschedulable // false) == false)
    and ((.metadata.labels // {}) | .["kubernetes.io/arch"] == "amd64")
  )] | length')"
[[ "${amd64_ready}" -ge 1 ]] || {
  echo "No qualifying linux/amd64 Ready schedulable Node found" >&2
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

"${kubectl_bin}" --kubeconfig "${kubeconfig}" get secret "${pull_secret_name}" \
  -n "${pull_secret_namespace}" -o jsonpath='{.type}' | grep -qx 'kubernetes.io/dockerconfigjson' || {
  echo "Required pull Secret ${pull_secret_namespace}/${pull_secret_name} is missing or has the wrong type" >&2
  exit 2
}

# Source-image lock: load and validate the four locked source images and their
# provenance versions before any registry or cluster mutation. Rejects
# duplicates, unknown keys, environment overrides, and malformed digests.
# shellcheck disable=SC1091
source "${root}/test/harness/image-lock.sh"
load_image_lock "${lock_file}"

registry="${CBSE_REGISTRY:-registry.unibw.de/i31bdase/cbse-test}"
registry_host="${registry%%/*}"

if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  command -v docker >/dev/null || { echo "docker is required to build current-source images" >&2; exit 2; }
  docker info >/dev/null || { echo "Docker daemon is not available" >&2; exit 2; }
  docker buildx version >/dev/null || { echo "Docker Buildx is required" >&2; exit 2; }
  probe_host="${registry_host}"
  [[ "${probe_host}" == "docker.io" ]] && probe_host="registry-1.docker.io"
  status="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' "https://${probe_host}/v2/" || true)"
  [[ "${status}" == "200" || "${status}" == "401" ]] || {
    echo "Registry TLS/connectivity preflight failed for ${probe_host}; insecure TLS is not supported" >&2
    exit 2
  }
  # A smoke build builds the component set requested via CBSE_IMAGE_COMPONENTS
  # (defaulting to the alpha3 mock set). The alpha4 reference build (real
  # Translator, runner base, and Detail Database) is selected by passing the
  # six-component set explicitly; it is validated and exercised by the harness
  # self-tests and becomes the default when the manifests and smoke switch to
  # alpha4 together.
else
  for variable in OPERATOR_IMAGE SM_IMAGE EDS_IMAGE TRANS_IMAGE; do
    value="${!variable:-}"
    [[ "${value}" == *@sha256:* ]] || {
      echo "${variable} must be an immutable digest reference when SKIP_BUILD=1" >&2
      exit 2
    }
  done
  # The alpha4 reference outputs (runner base and Detail Database) are not yet
  # required by the active smoke path; when they are supplied they must still be
  # immutable digest references. They become required when the smoke switches to
  # alpha4 together with the manifests and build default.
  for variable in RUNNER_BASE_IMAGE DETAIL_DB_IMAGE; do
    value="${!variable:-}"
    [[ -z "${value}" || "${value}" == *@sha256:* ]] || {
      echo "${variable} must be an immutable digest reference when SKIP_BUILD=1" >&2
      exit 2
    }
  done
fi

if [[ "${SKIP_BUILD:-0}" != "1" || -n "${CBSE_REGISTRY_AUTH_FILE:-}" ]]; then
  registry_auth_file="${CBSE_REGISTRY_AUTH_FILE:?CBSE_REGISTRY_AUTH_FILE must point to a dedicated Docker config.json when building images}"
  [[ -r "${registry_auth_file}" ]] || { echo "Registry auth file is not readable: ${registry_auth_file}" >&2; exit 2; }
  jq -e '.auths | type == "object" and length > 0' "${registry_auth_file}" >/dev/null || {
    echo "CBSE_REGISTRY_AUTH_FILE is not a valid non-empty Docker config" >&2
    exit 2
  }
fi
if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  jq -e --arg host "${registry_host}" \
    '.auths | has($host) or has("https://" + $host) or has("https://" + $host + "/v1/") or ($host == "docker.io" and (has("https://index.docker.io/v1/") or has("https://registry-1.docker.io")))' \
    "${registry_auth_file}" >/dev/null || {
    echo "Registry auth file has no credentials for ${registry_host}" >&2
    exit 2
  }
fi

# Built-in registry cleanup adapter: must exist and be executable. The adapter
# targets only the generated-runner repository (i31bdase/cbse-test-runner) and
# never the shared (cbse-test) or Detail DB (cbse-test/scenario-detail-database)
# repositories.
cleanup_adapter="${root}/test/harness/registry-cleanup.sh"
[[ -x "${cleanup_adapter}" ]] || {
  echo "Built-in registry cleanup adapter is missing or not executable: ${cleanup_adapter}" >&2
  exit 2
}

# Harbor artifact-list preflight for the generated-runner repository. Accept
# HTTP 200 (repository exists) or 404 (repository absent before the first
# generated-runner push; cbse-test-runner is intentionally not pre-provisioned).
# Reject 401/403 or any other result. Run only when credentials for the
# registry host are available in the auth file; otherwise skip (e.g. a
# synthetic harness self-test with unrelated credentials).
harbor_api="${CBSE_HARBOR_API:-https://${registry_host}/api/v2.0}"
harbor_project="${CBSE_HARBOR_PROJECT:-i31bdase}"
runner_repo="${CBSE_RUNNER_REPO:-cbse-test-runner}"
if [[ -n "${CBSE_REGISTRY_AUTH_FILE:-}" && -r "${CBSE_REGISTRY_AUTH_FILE}" ]]; then
  harbor_auth="$(jq -r --arg h "${registry_host}" '.auths[$h].auth // empty' "${CBSE_REGISTRY_AUTH_FILE}" 2>/dev/null || true)"
  if [[ -n "${harbor_auth}" ]]; then
    list_status="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
      -H "Authorization: Basic ${harbor_auth}" \
      "${harbor_api}/projects/${harbor_project}/repositories/${runner_repo}/artifacts" || true)"
    [[ "${list_status}" == "200" || "${list_status}" == "404" ]] || {
      echo "Harbor artifact-list preflight for ${harbor_project}/${runner_repo} returned HTTP ${list_status}; 200 or 404 required" >&2
      exit 2
    }
  fi
fi

echo "Preflight passed: context=${actual_context} server=${actual_server} version=${server_version}"
