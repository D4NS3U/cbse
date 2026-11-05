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

# publish-translator-image.sh — build and push the paper-local Translator image
# (P5 acceptance, P5-R2).
#
# Mirrors the repository build contract (test/harness/build-images.sh) for the
# paper-local Translator: it builds pads-paper/translator/component with its
# inherited Dockerfile, pushes both the canonical <CBSE_REGISTRY>/translator:
# <version> tag and the immutable provenance tag
# <version>.sha-<commit>-<source-hash>-<run-id>, and writes the single
# digest-pinned PADS_TRANS_IMAGE line to the caller-supplied artifact file
# (default artifacts/pads/<run-id>/pads-translator.env, untracked).
#
# Inputs / environment:
#   CBSE_REGISTRY (required) registry prefix; no default registry value is
#              embedded. The pushed repository is <CBSE_REGISTRY>/translator.
#   CBSE_REGISTRY_AUTH_FILE (required) dedicated Docker config.json for push
#              credentials; copied into an isolated DOCKER_CONFIG and never
#              printed.
#   TEST_IMAGE_VERSION (required) canonical tag; must use YY.M.D form.
#   RUN_ID (default timestamp+random) part of the immutable provenance tag.
#   PADS_IMAGE_ARTIFACT_FILE (default <root>/artifacts/pads/<run-id>/
#              pads-translator.env) output file for the single
#              PADS_TRANS_IMAGE=<repo>@<digest> line.
#   DOCKER_CONFIG (default ${HOME}/.docker) host Docker config preserved in
#              the isolated config (contexts/builders), with only the
#              credential file replaced by CBSE_REGISTRY_AUTH_FILE.
#
# The locked source image is the TRANSLATOR_GO_BUILDER_IMAGE digest from
# test/e2e/images.lock.env, with no environment override (loaded through the
# shared image-lock.sh loader, which rejects any override).
#
# Exit codes:
#   0  image built and pushed; digest-pinned line written.
#   1  missing required environment (the three :? refusals), a locked
#      source image override or lock-load failure, a failed build, or the
#      build did not report a containerimage.digest.
#   2  bad TEST_IMAGE_VERSION, missing docker/jq, unreadable auth file,
#      or missing Dockerfile.
#
# Side effects:
#   Pushes the paper-local Translator to the registry (canonical and
#   immutable provenance tags) with buildx --push --platform linux/amd64.
#   Writes the digest line, the build log, and the buildx metadata under the
#   artifact dir (all untracked). No cluster access. The isolated
#   DOCKER_CONFIG temp dir is removed on EXIT.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
registry="${CBSE_REGISTRY:?CBSE_REGISTRY is required (environment-provided; no default registry value is embedded)}"
version="${TEST_IMAGE_VERSION:?TEST_IMAGE_VERSION is required in YY.M.D form (for example 26.11.5)}"
auth_file="${CBSE_REGISTRY_AUTH_FILE:?CBSE_REGISTRY_AUTH_FILE is required (dedicated Docker config.json)}"
run_id="${RUN_ID:-$(date -u +%Y%m%d%H%M%S)-$(openssl rand -hex 3)}"
artifact_file="${PADS_IMAGE_ARTIFACT_FILE:-${root}/artifacts/pads/${run_id}/pads-translator.env}"

[[ "${registry}" != */ ]] || registry="${registry%/}"
[[ "${version}" =~ ^[0-9]{2}\.[0-9]{1,2}\.[0-9]{1,2}$ ]] || {
  echo "TEST_IMAGE_VERSION must use YY.M.D format (for example 26.7.16)" >&2
  exit 2
}
command -v docker >/dev/null || { echo "docker is required" >&2; exit 2; }
docker buildx version >/dev/null
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
[[ -r "${auth_file}" ]] || { echo "Registry auth file is not readable: ${auth_file}" >&2; exit 2; }

# --- Source-image lock (immutable build-only input, no override) -----------
# The locked TRANSLATOR_GO_BUILDER_IMAGE digest is the only builder stage of
# the paper-local Translator Dockerfile; the shared loader validates it
# before any registry mutation and rejects environment overrides.
# shellcheck disable=SC1091
source "${root}/test/harness/image-lock.sh"
load_image_lock "${root}/test/e2e/images.lock.env"

context="${root}/pads-paper/translator/component"
dockerfile="${context}/Dockerfile"
[[ -f "${dockerfile}" ]] || { echo "Paper-local Translator Dockerfile is missing: ${dockerfile}" >&2; exit 2; }

# --- Provenance (harness convention) ---------------------------------------
commit="$(git -C "${root}" rev-parse --short=12 HEAD 2>/dev/null || echo no-git)"
source_hash="$(
  git -C "${root}" ls-files -co --exclude-standard | LC_ALL=C sort |
    while IFS= read -r file; do git -C "${root}" hash-object "${file}"; done |
    git hash-object --stdin | cut -c1-12
)"
immutable_suffix="${version}.sha-${commit}-${source_hash}-${run_id}"

repository="${registry}/translator"
canonical="${repository}:${version}"
immutable="${repository}:${immutable_suffix}"

artifact_dir="$(dirname "${artifact_file}")"
mkdir -p "${artifact_dir}"
metadata="${artifact_dir}/pads-translator.metadata.json"
build_log="${artifact_dir}/pads-translator-build.log"

# --- Registry authentication (isolated DOCKER_CONFIG, never printed) -------
# Preserve the host Docker contexts/builders/CLI plugins while replacing only
# the credential file with the dedicated auth config.
auth_dir="$(mktemp -d)"
trap 'rm -rf "${auth_dir}"' EXIT
source_docker_config="${DOCKER_CONFIG:-${HOME:-/nonexistent}/.docker}"
if [[ -d "${source_docker_config}" ]]; then
  cp -R "${source_docker_config}/." "${auth_dir}/"
fi
cp "${auth_file}" "${auth_dir}/config.json"
export DOCKER_CONFIG="${auth_dir}"

# --- Build and push ----------------------------------------------------------
# Nested repository layout: the dedicated <CBSE_REGISTRY>/translator
# repository carries the date-versioned canonical tag and the immutable
# provenance tag. The digest output keeps the repository path.
echo "Building paper-local translator: ${canonical} / ${immutable}" >&2
if ! docker buildx build --platform linux/amd64 --pull --push \
    --progress=plain --file "${dockerfile}" \
    --tag "${canonical}" --tag "${immutable}" \
    --build-arg "IMAGE_VERSION=${version}" --build-arg "VCS_REF=${commit}" \
    --build-arg "TRANSLATOR_GO_BUILDER_IMAGE=${TRANSLATOR_GO_BUILDER_IMAGE}" \
    --label "org.opencontainers.image.title=CBSE Paper-Local Translator" \
    --metadata-file "${metadata}" "${context}" >"${build_log}" 2>&1; then
  tail -n 50 "${build_log}" >&2
  echo "Paper-local translator build failed; log: ${build_log}" >&2
  exit 1
fi

digest="$(jq -r '.["containerimage.digest"] // empty' "${metadata}")"
[[ "${digest}" == sha256:* ]] || {
  echo "Build did not report a digest for the paper-local translator" >&2
  exit 1
}

printf 'PADS_TRANS_IMAGE=%s@%s\n' "${repository}" "${digest}" >"${artifact_file}"
printf '%s canonical=%s immutable=%s digest=%s\n' "pads-translator" "${canonical}" "${immutable}" "${digest}" \
  >"${artifact_dir}/pads-translator-summary.txt"
echo "Published paper-local translator; digest line: ${artifact_file}"
