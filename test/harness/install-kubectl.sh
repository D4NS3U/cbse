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

# install-kubectl.sh — download and verify a pinned kubectl binary.
#
# Fetches a specific kubectl release for the host OS and architecture from
# dl.k8s.io, verifies it against the official SHA256 checksum, installs it to
# OUTPUT with mode 0755, and prints the client version. The smoke harness uses
# this to obtain a kubectl matching the test cluster's expected version skew
# rather than relying on a host-installed binary. Architecture names are
# normalized (x86_64/amd64 -> amd64, arm64/aarch64 -> arm64); the OS is taken
# from `uname -s` lowercased.
#
# Inputs / environment:
#   KUBECTL_VERSION (default v1.32.5) kubectl release tag to download.
#   OUTPUT          (required) destination path for the kubectl binary.
#
# Exit codes:
#   0  kubectl downloaded, checksum-verified, installed, and version printed.
#   1  checksum verification failed (downloaded digest != official checksum).
#   2  unsupported architecture, or OUTPUT not writable.
#
# Side effects:
#   Writes the executable kubectl binary to OUTPUT (0755). Downloads to a temp
#   file first, verifies in place, then atomically moves it over OUTPUT; the
#   temp file and checksum file are removed on EXIT.
set -euo pipefail

version="${KUBECTL_VERSION:-v1.32.5}"
output="${OUTPUT:?OUTPUT is required}"
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "${arch}" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "Unsupported architecture: ${arch}" >&2; exit 2 ;;
esac

mkdir -p "$(dirname "${output}")"
tmp="$(mktemp "${output}.tmp.XXXXXX")"
checksum_file="${tmp}.sha256"
trap 'rm -f "${tmp}" "${checksum_file}"' EXIT
curl --fail --location --silent --show-error \
  "https://dl.k8s.io/release/${version}/bin/${os}/${arch}/kubectl" \
  --output "${tmp}"
curl --fail --location --silent --show-error \
  "https://dl.k8s.io/release/${version}/bin/${os}/${arch}/kubectl.sha256" \
  --output "${checksum_file}"
expected_checksum="$(tr -d '[:space:]' <"${checksum_file}")"
if command -v sha256sum >/dev/null 2>&1; then
  actual_checksum="$(sha256sum "${tmp}" | awk '{print $1}')"
else
  actual_checksum="$(shasum -a 256 "${tmp}" | awk '{print $1}')"
fi
[[ "${actual_checksum}" == "${expected_checksum}" ]] || {
  echo "kubectl checksum verification failed" >&2
  exit 1
}
chmod 0755 "${tmp}"
mv "${tmp}" "${output}"
"${output}" version --client
