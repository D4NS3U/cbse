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

# generate-helm-values.sh — generate the installation-owner private Helm
# image-values file ruled by H6.
#
# Maps full digest-pinned image references into the chart's repository/digest
# value pairs for exactly five components:
#   operator.image, scenarioManager.image, nats.image, coreDatabase.image,
#   hooks.kubectlImage
# OPERATOR_IMAGE and SM_IMAGE come from a readable digest-pinned images.env
# file (the smoke images.env format; extra keys are ignored). NATS_IMAGE,
# POSTGRES_IMAGE, and HELM_HOOK_KUBECTL_IMAGE come from the repository image
# lock. Every reference must be <non-empty-repository>@sha256:<64 lowercase
# hex>; tags, empty values, duplicate '@', and malformed digests fail before
# anything is written.
#
# The file is written atomically (same-directory temp file + rename) with
# mode 0600. It contains no credentials, but repositories may be
# operational/private, so this script never prints full private repository
# values to stdout; success prints only the output path and the component
# count. There is no registry default or fallback anywhere in this script.
#
# Inputs / environment:
#   IMAGES_ENV (required) readable file with digest-pinned full references
#              for OPERATOR_IMAGE and SM_IMAGE (extra keys ignored).
#   OUTPUT     (required) destination path. If it lies inside the tracked
#              repository it must be under the git-ignored artifacts/
#              directory; outside the repository any writable path is
#              accepted.
#
# Exit codes:
#   0  values file written atomically with mode 0600 (5 components).
#   2  any input invalid: missing/unreadable inputs, missing OPERATOR_IMAGE
#      or SM_IMAGE, tag/empty/duplicate-@/malformed-digest reference, or
#      OUTPUT inside a tracked repository path outside artifacts/.
#
# Side effects:
#   Writes OUTPUT (mode 0600) via a temp file in the same directory. Prints
#   no repository values.
set -euo pipefail

images_env="${IMAGES_ENV:?IMAGES_ENV must point to a readable digest-pinned images.env file}"
output="${OUTPUT:?OUTPUT is the required destination path for the generated values file}"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# --- OUTPUT location guard ---------------------------------------------------
# The generated file is private: if the caller places it inside this tracked
# repository it must live under the git-ignored artifacts/ directory. Relative
# paths are resolved against the caller's working directory.
fail_output() {
  echo "OUTPUT is not acceptable: $*" >&2
  exit 2
}
out_dir="$(cd "$(dirname "${output}")" 2>/dev/null && pwd)" ||
  fail_output "parent directory of OUTPUT is missing or not a directory"
if [[ "${out_dir}" == "${root}" || "${out_dir}" == "${root}"/* ]]; then
  [[ "${out_dir}" == "${root}/artifacts" || "${out_dir}" == "${root}/artifacts"/* ]] ||
    fail_output "a generated values file inside the tracked repository must live under the ignored artifacts/ directory"
fi

# --- reference validation -----------------------------------------------------
# A digest-pinned reference is <non-empty-repository>@sha256:<64 lowercase
# hex> with no tag. Validation errors name the variable and the shape
# violation only; they never echo the reference itself (it may be a private
# operational value).
validate_image_ref() {
  local name="$1" ref="$2"
  [[ -n "${ref}" ]] || {
    echo "${name} is empty; supply a digest-pinned <repository>@sha256:<64 lowercase hex> reference" >&2
    exit 2
  }
  [[ "${ref}" =~ ^[^@]+@sha256:[0-9a-f]{64}$ ]] || {
    echo "${name} is not a digest-pinned <repository>@sha256:<64 lowercase hex> reference (tags, empty values, duplicate @, and malformed digests are rejected)" >&2
    exit 2
  }
  local repo="${ref%%@*}"
  local last="${repo##*/}"
  [[ -n "${repo}" && -n "${last}" ]] || {
    echo "${name} has an empty repository component" >&2
    exit 2
  }
  case "${last}" in
    *:*)
      echo "${name} carries a tag; only digest-pinned references are accepted" >&2
      exit 2
      ;;
  esac
  case "${repo}" in
    *[[:space:]]*)
      echo "${name} repository contains whitespace" >&2
      exit 2
      ;;
  esac
}

# --- images.env input ---------------------------------------------------------
# Parse the digest-pinned images.env verbatim (KEY=VALUE lines, no shell
# evaluation). The smoke format carries the eight component references; any
# extra keys are ignored. The file may be private, so parse errors name the
# variable only and never echo a reference value.
[[ -r "${images_env}" ]] || {
  echo "IMAGES_ENV is not readable: ${images_env}" >&2
  exit 2
}
operator_image=""
sm_image=""
while IFS= read -r line || [[ -n "${line}" ]]; do
  [[ -z "${line}" || "${line}" == \#* ]] && continue
  [[ "${line}" == *=* ]] || {
    echo "IMAGES_ENV contains a malformed line (expected KEY=VALUE)" >&2
    exit 2
  }
  case "${line%%=*}" in
    OPERATOR_IMAGE) operator_image="${line#*=}" ;;
    SM_IMAGE) sm_image="${line#*=}" ;;
    *) : ;;
  esac
done <"${images_env}"
validate_image_ref "OPERATOR_IMAGE (from IMAGES_ENV)" "${operator_image}"
validate_image_ref "SM_IMAGE (from IMAGES_ENV)" "${sm_image}"

# --- repository image lock -----------------------------------------------------
# The lock loader already rejects duplicates, unknown keys, environment
# overrides, and malformed digests for all seven locked source images; the
# re-validation below keeps every component on one uniform code path.
# shellcheck source=test/harness/image-lock.sh
source "${root}/test/harness/image-lock.sh"
load_image_lock "${root}/test/e2e/images.lock.env"
validate_image_ref "NATS_IMAGE (image lock)" "${NATS_IMAGE}"
validate_image_ref "POSTGRES_IMAGE (image lock)" "${POSTGRES_IMAGE}"
validate_image_ref "HELM_HOOK_KUBECTL_IMAGE (image lock)" "${HELM_HOOK_KUBECTL_IMAGE}"

# --- atomic 0600 write ---------------------------------------------------------
component_count=5
write_component() {
  local component="$1" subkey="$2" ref="$3"
  local repo="${ref%%@*}"
  local digest="${ref##*@}"
  printf '%s:\n  %s:\n    repository: "%s"\n    digest: "%s"\n' "${component}" "${subkey}" "${repo}" "${digest}"
}
tmp="$(mktemp "${out_dir}/.cbse-helm-image-values.XXXXXX")"
trap 'rm -f "${tmp}"' EXIT
{
  echo "# CBSE installation image values — generated by test/harness/generate-helm-values.sh"
  echo "# Private and untracked: repositories may be operational. Never commit, share,"
  echo "# or print this file. It contains no credentials."
  write_component "operator" "image" "${operator_image}"
  write_component "scenarioManager" "image" "${sm_image}"
  write_component "nats" "image" "${NATS_IMAGE}"
  write_component "coreDatabase" "image" "${POSTGRES_IMAGE}"
  write_component "hooks" "kubectlImage" "${HELM_HOOK_KUBECTL_IMAGE}"
} >"${tmp}"
chmod 0600 "${tmp}"
mv "${tmp}" "${output}"
trap - EXIT
echo "helm image values written: ${output} (${component_count} components)"
