#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
registry="${CBSE_REGISTRY:-docker.io/d4ns3u/cbse-testing}"
version="${TEST_IMAGE_VERSION:-26.7.16}"
run_id="${RUN_ID:-$(date -u +%Y%m%d%H%M%S)-$(openssl rand -hex 3)}"
artifact_dir="${CBSE_IMAGE_ARTIFACT_DIR:-${root}/artifacts/test-images/${version}/${run_id}}"
auth_file="${CBSE_REGISTRY_AUTH_FILE:-}"

[[ "${registry}" != */ ]] || registry="${registry%/}"
[[ "${version}" =~ ^[0-9]{2}\.[0-9]{1,2}\.[0-9]{1,2}$ ]] || {
  echo "TEST_IMAGE_VERSION must use YY.M.D format (for example 26.7.16)" >&2
  exit 2
}
command -v docker >/dev/null || { echo "docker is required" >&2; exit 2; }
docker buildx version >/dev/null
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

commit="$(git -C "${root}" rev-parse --short=12 HEAD 2>/dev/null || echo no-git)"
source_hash="$(
  git -C "${root}" ls-files -co --exclude-standard | LC_ALL=C sort |
    while IFS= read -r file; do git -C "${root}" hash-object "${file}"; done |
    git hash-object --stdin | cut -c1-12
)"
immutable_suffix="${version}.sha-${commit}-${source_hash}-${run_id}"
mkdir -p "${artifact_dir}"

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

build_image() {
  local name="$1" image_var="$2" dockerfile="$3" context="$4" title="$5"
  local canonical="${registry}:${name}.test.${version}"
  local immutable="${registry}:${name}.test.${immutable_suffix}"
  local metadata="${artifact_dir}/${name}.metadata.json"
  "${docker_config[@]}" docker buildx build --platform linux/amd64 --pull --push \
    --progress=plain --file "${dockerfile}" \
    --tag "${canonical}" --tag "${immutable}" \
    --build-arg "IMAGE_VERSION=${version}" --build-arg "VCS_REF=${commit}" \
    --label "org.opencontainers.image.title=${title}" \
    --metadata-file "${metadata}" "${context}" \
    2>&1 | tee "${artifact_dir}/build-${name}.log" >&2
  local digest
  digest="$(jq -r '.["containerimage.digest"] // empty' "${metadata}")"
  [[ "${digest}" == sha256:* ]] || { echo "Build did not report a digest for ${name}" >&2; return 1; }
  printf '%s=%s@%s\n' "${image_var}" "${registry}:${name}.test.${version}" "${digest}" >>"${artifact_dir}/images.env"
  printf '%s canonical=%s immutable=%s digest=%s\n' "${name}" "${canonical}" "${immutable}" "${digest}" >>"${artifact_dir}/summary.txt"
}

: >"${artifact_dir}/images.env"
build_image exop OPERATOR_IMAGE "${root}/experiment-operator/Dockerfile" "${root}/experiment-operator" "CBSE Experiment Operator"
build_image sm SM_IMAGE "${root}/scenario-manager/Dockerfile" "${root}" "CBSE Scenario Manager"
build_image eds-mock EDS_IMAGE "${root}/test-env/eds-mock/Dockerfile" "${root}" "CBSE EDS Mock"
printf 'REGISTRY=%s\nVERSION=%s\nCOMMIT=%s\nSOURCE_HASH=%s\nRUN_ID=%s\n' \
  "${registry}" "${version}" "${commit}" "${source_hash}" "${run_id}" >"${artifact_dir}/build-info.env"
echo "Published test images; metadata: ${artifact_dir}"
