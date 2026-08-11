#!/usr/bin/env bash
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
