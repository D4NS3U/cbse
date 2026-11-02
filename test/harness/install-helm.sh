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

# install-helm.sh — download and verify a pinned Helm binary.
#
# Fetches a specific Helm release for the host OS (darwin|linux) and
# architecture (amd64|arm64) from the official get.helm.sh tarball and
# checksum URLs, verifies the tarball against the official SHA256 checksum,
# extracts the helm binary, and installs it to OUTPUT with mode 0755. The
# chart test harness uses this to obtain the exact pinned Helm release rather
# than relying on a host-installed binary. Architecture names are normalized
# (x86_64/amd64 -> amd64, arm64/aarch64 -> arm64); the OS is taken from
# `uname -s` lowercased.
#
# Inputs / environment:
#   HELM_VERSION (default v3.18.4) Helm release tag to download.
#   OUTPUT          (required) destination path for the helm binary.
#
# Exit codes:
#   0  helm downloaded, checksum-verified, installed, and version printed.
#   1  checksum verification failed (downloaded digest != official checksum).
#   2  unsupported OS/architecture, or OUTPUT not writable.
#
# Side effects:
#   Writes the executable helm binary to OUTPUT (0755). Downloads to a temp
#   file first, verifies in place, extracts into a temp directory, then
#   atomically moves the binary over OUTPUT; the temp files are removed on
#   EXIT. No sudo and no host-tool mutation.
set -euo pipefail

version="${HELM_VERSION:-v3.18.4}"
output="${OUTPUT:?OUTPUT is required}"
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "${os}" in
  darwin|linux) ;;
  *) echo "Unsupported OS: ${os}" >&2; exit 2 ;;
esac
arch="$(uname -m)"
case "${arch}" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "Unsupported architecture: ${arch}" >&2; exit 2 ;;
esac

bundle="helm-${version}-${os}-${arch}"
# The release tarball extracts into an inner <os>-<arch> directory.
bundle_dir="${os}-${arch}"
mkdir -p "$(dirname "${output}")"
tmp="$(mktemp "${output}.tmp.XXXXXX")"
checksum_file="${tmp}.sha256"
extract_dir="$(mktemp -d)"
trap 'rm -rf "${tmp}" "${checksum_file}" "${extract_dir}"' EXIT
curl --fail --location --silent --show-error \
  "https://get.helm.sh/${bundle}.tar.gz" \
  --output "${tmp}"
curl --fail --location --silent --show-error \
  "https://get.helm.sh/${bundle}.tar.gz.sha256" \
  --output "${checksum_file}"
expected_checksum="$(tr -d '[:space:]' <"${checksum_file}")"
if command -v sha256sum >/dev/null 2>&1; then
  actual_checksum="$(sha256sum "${tmp}" | awk '{print $1}')"
else
  actual_checksum="$(shasum -a 256 "${tmp}" | awk '{print $1}')"
fi
[[ "${actual_checksum}" == "${expected_checksum}" ]] || {
  echo "helm checksum verification failed" >&2
  exit 1
}
tar -xzf "${tmp}" -C "${extract_dir}"
chmod 0755 "${extract_dir}/${bundle_dir}/helm"
mv "${extract_dir}/${bundle_dir}/helm" "${output}"
"${output}" version --short
