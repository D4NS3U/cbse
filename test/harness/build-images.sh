#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
registry="${CBSE_REGISTRY:-registry.unibw.de/i31bdase/cbse-test}"
version="${TEST_IMAGE_VERSION:-26.7.16}"
run_id="${RUN_ID:-$(date -u +%Y%m%d%H%M%S)-$(openssl rand -hex 3)}"
artifact_dir="${CBSE_IMAGE_ARTIFACT_DIR:-${root}/artifacts/test-images/${version}/${run_id}}"
auth_file="${CBSE_REGISTRY_AUTH_FILE:-}"
components="${CBSE_IMAGE_COMPONENTS:-exop,sm,eds-mock,trans-mock}"
lock_file="${root}/test/e2e/images.lock.env"

[[ "${registry}" != */ ]] || registry="${registry%/}"
[[ "${version}" =~ ^[0-9]{2}\.[0-9]{1,2}\.[0-9]{1,2}$ ]] || {
  echo "TEST_IMAGE_VERSION must use YY.M.D format (for example 26.7.16)" >&2
  exit 2
}
command -v docker >/dev/null || { echo "docker is required" >&2; exit 2; }
docker buildx version >/dev/null
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

# --- Source-image lock (immutable build-only inputs) -------------------------
# The four locked source images and their provenance versions live in
# test/e2e/images.lock.env. They are repository inputs with no environment
# override; the shared loader validates them before any build or registry
# mutation and sets the lock variables in this shell.
# shellcheck disable=SC1091
source "${root}/test/harness/image-lock.sh"
load_image_lock "${lock_file}"

# --- Component selection -----------------------------------------------------
validate_components() {
  [[ -n "${components}" ]] || { echo "CBSE_IMAGE_COMPONENTS is empty" >&2; return 1; }
  local IFS=','
  local -a tokens=(${components})
  local t s
  local -a seen=()
  for t in "${tokens[@]}"; do
    [[ -n "${t}" ]] || { echo "empty component token in CBSE_IMAGE_COMPONENTS" >&2; return 1; }
    case "${t}" in
      exop|sm|eds-mock|trans-mock|translator|runner-base|scenario-detail-database) ;;
      *) echo "unknown component token: ${t}" >&2; return 1 ;;
    esac
    for s in ${seen[@]+"${seen[@]}"}; do
      [[ "${s}" != "${t}" ]] || { echo "duplicate component token: ${t}" >&2; return 1; }
    done
    seen+=("${t}")
  done
}
validate_components

component_enabled() {
  [[ ",${components}," == *",$1,"* ]]
}

# --- Provenance --------------------------------------------------------------
commit="$(git -C "${root}" rev-parse --short=12 HEAD 2>/dev/null || echo no-git)"
source_hash="$(
  git -C "${root}" ls-files -co --exclude-standard | LC_ALL=C sort |
    while IFS= read -r file; do git -C "${root}" hash-object "${file}"; done |
    git hash-object --stdin | cut -c1-12
)"
immutable_suffix="${version}.sha-${commit}-${source_hash}-${run_id}"
mkdir -p "${artifact_dir}"

# --- Registry authentication -------------------------------------------------
docker_config=()
if [[ -n "${auth_file}" ]]; then
  [[ -r "${auth_file}" ]] || { echo "Registry auth file is not readable: ${auth_file}" >&2; exit 2; }
  auth_dir="$(mktemp -d)"
  # Preserve Docker Desktop's contexts, builders, and CLI plugins while
  # replacing only the credential file with the dedicated auth config.
  source_docker_config="${DOCKER_CONFIG:-${HOME}/.docker}"
  cp -R "${source_docker_config}/." "${auth_dir}/"
  cp "${auth_file}" "${auth_dir}/config.json"
  docker_config=(env "DOCKER_CONFIG=${auth_dir}")
  trap 'rm -rf "${auth_dir}"' EXIT
fi

# --- Build helpers -----------------------------------------------------------
# Shared component images use the FLAT repository layout: a single registry
# prefix (${CBSE_REGISTRY}) carries one tag per component, e.g.
# ${CBSE_REGISTRY}:translator.test.${version}. The digest output omits any
# repository path: TRANS_IMAGE=${CBSE_REGISTRY}@sha256:<hex>.
build_flat() {
  local name="$1" image_var="$2" dockerfile="$3" context="$4" title="$5"
  shift 5
  local canonical="${registry}:${name}.test.${version}"
  local immutable="${registry}:${name}.test.${immutable_suffix}"
  _build "${name}" "${image_var}" "${registry}" "${canonical}" "${immutable}" \
    "${dockerfile}" "${context}" "${title}" "$@"
}

# The reference Scenario Detail Database uses the NESTED layout: a dedicated
# repository below the prefix, ${CBSE_REGISTRY}/scenario-detail-database, with
# the date version as its tag. Its digest output keeps the path:
# DETAIL_DB_IMAGE=${CBSE_REGISTRY}/scenario-detail-database@sha256:<hex>.
build_nested() {
  local name="$1" image_var="$2" dockerfile="$3" context="$4" title="$5"
  shift 5
  local repository="${registry}/${name}"
  local canonical="${repository}:${version}"
  local immutable="${repository}:${immutable_suffix}"
  _build "${name}" "${image_var}" "${repository}" "${canonical}" "${immutable}" \
    "${dockerfile}" "${context}" "${title}" "$@"
}

_build() {
  local name="$1" image_var="$2" out_repo="$3" canonical="$4" immutable="$5" dockerfile="$6" context="$7" title="$8"
  shift 8
  local metadata="${artifact_dir}/${name}.metadata.json"
  "${docker_config[@]}" docker buildx build --platform linux/amd64 --pull --push \
    --progress=plain --file "${dockerfile}" \
    --tag "${canonical}" --tag "${immutable}" \
    --build-arg "IMAGE_VERSION=${version}" --build-arg "VCS_REF=${commit}" \
    "$@" \
    --label "org.opencontainers.image.title=${title}" \
    --metadata-file "${metadata}" "${context}" \
    2>&1 | tee "${artifact_dir}/build-${name}.log" >&2
  local digest
  digest="$(jq -r '.["containerimage.digest"] // empty' "${metadata}")"
  [[ "${digest}" == sha256:* ]] || { echo "Build did not report a digest for ${name}" >&2; return 1; }
  printf '%s=%s@%s\n' "${image_var}" "${out_repo}" "${digest}" >>"${artifact_dir}/images.env"
  printf '%s canonical=%s immutable=%s digest=%s\n' "${name}" "${canonical}" "${immutable}" "${digest}" >>"${artifact_dir}/summary.txt"
}

: >"${artifact_dir}/images.env"
component_enabled exop && build_flat exop OPERATOR_IMAGE "${root}/experiment-operator/Dockerfile" "${root}/experiment-operator" "CBSE Experiment Operator"
component_enabled sm && build_flat sm SM_IMAGE "${root}/scenario-manager/Dockerfile" "${root}" "CBSE Scenario Manager"
component_enabled eds-mock && build_flat eds-mock EDS_IMAGE "${root}/test/mocks/eds/Dockerfile" "${root}" "CBSE EDS Mock"
component_enabled trans-mock && build_flat trans-mock TRANS_IMAGE "${root}/test/mocks/translator/Dockerfile" "${root}" "CBSE Translator Mock"
component_enabled translator && build_flat translator TRANS_IMAGE "${root}/component-templates/translator/Dockerfile" "${root}/component-templates/translator" "CBSE Translator" --build-arg "TRANSLATOR_GO_BUILDER_IMAGE=${TRANSLATOR_GO_BUILDER_IMAGE}"
component_enabled runner-base && build_flat runner-base RUNNER_BASE_IMAGE "${root}/component-templates/translator/runner-base/Dockerfile" "${root}/component-templates/translator/runner-base" "CBSE Runner Base" --build-arg "PYTHON_BASE_IMAGE=${PYTHON_BASE_IMAGE}"
component_enabled scenario-detail-database && build_nested scenario-detail-database DETAIL_DB_IMAGE "${root}/component-templates/scenario-detail-database/Dockerfile" "${root}/component-templates/scenario-detail-database" "CBSE Scenario Detail Database" --build-arg "POSTGRES_IMAGE=${POSTGRES_IMAGE}"
printf 'REGISTRY=%s\nVERSION=%s\nCOMMIT=%s\nSOURCE_HASH=%s\nRUN_ID=%s\n' \
  "${registry}" "${version}" "${commit}" "${source_hash}" "${run_id}" >"${artifact_dir}/build-info.env"
echo "Published test images; metadata: ${artifact_dir}"
