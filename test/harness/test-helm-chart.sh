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

# test-helm-chart.sh — static CBSE Helm chart contract tests.
#
# Runs, with the pinned Helm binary (HELM environment, set by the Makefile;
# falling back to the single repository bin/helm-* install) and the public
# example.invalid fixture values, entirely without cluster mutation or
# network access:
#   * helm lint with the fixture values;
#   * helm template --include-crds into a temp file;
#   * balanced YAML document / basic parse checks on the rendered output;
#   * rendered CRD is the alpha4-only CRD;
#   * digest scan: no rendered image reference is tag-based;
#   * source tag scan: no chart or fixture file carries a tag-style image;
#   * CRD lockstep: the chart CRD copy is byte-identical to the generated
#     alpha4 CRD;
#   * private-string scan over the H1-owned chart and test files;
#   * discovery and dispatch of every test/helm/assert-*.sh contract script
#     (JSON-schema negative cases included) via assert-helpers.sh.
#
# Inputs / environment:
#   HELM (optional) pinned helm binary; defaults to the repository bin/helm-*.
#
# Exit codes:
#   0  all chart tests passed (prints "Helm chart tests passed.").
#   1  a test assertion failed.
#   Other non-zero propagated from helm/diff via set -e.
#
# Side effects:
#   Read-only against the repository; rendered output and scratch files live
#   in a temp directory removed on EXIT.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
chart="${root}/charts/cbse"
values="${root}/test/helm/values-render.yaml"

# Pinned helm binary: honor the Makefile-provided HELM; otherwise use the
# single repository install under bin/.
pinned_helm="${HELM:-}"
if [[ -z "${pinned_helm}" ]]; then
  candidates=()
  for candidate in "${root}"/bin/helm-*; do
    [[ -x "${candidate}" ]] || continue
    candidates+=("${candidate}")
  done
  if [[ "${#candidates[@]}" -eq 1 ]]; then
    pinned_helm="${candidates[0]}"
  elif [[ "${#candidates[@]}" -eq 0 ]] && command -v helm >/dev/null 2>&1; then
    pinned_helm="$(command -v helm)"
  else
    echo "no pinned helm binary found; run 'make helm-tools' or set HELM" >&2
    exit 1
  fi
fi
echo "Helm chart tests: ${pinned_helm} ($("${pinned_helm}" version --short))"

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
fail() { echo "helm chart test failed: $*" >&2; exit 1; }

# --- lint --------------------------------------------------------------------
echo "  lint: ${pinned_helm} lint ${chart} -f ${values}"
"${pinned_helm}" lint "${chart}" -f "${values}"

# --- render --------------------------------------------------------------------
rendered="${tmp}/rendered.yaml"
echo "  render: helm template cbse ${chart} --namespace cbse-system --include-crds"
"${pinned_helm}" template cbse "${chart}" \
  --namespace cbse-system --include-crds -f "${values}" >"${rendered}"

# Balanced YAML documents / basic parse check: split the rendered output on
# top-level --- separators; every non-empty (non-comment) document must carry
# exactly one top-level kind: and one top-level apiVersion: line. helm has
# already parsed every template; this catches any document that lost its
# structure in concatenation (helm template does not render NOTES.txt).
if ! doc_count="$(awk '
  /^---$/ { finish(); buf = ""; next }
  { buf = buf $0 "\n" }
  END { finish() }
  function finish(   lines, n, content, kinds, apis, i, l) {
    kinds = 0; apis = 0
    n = split(buf, lines, "\n")
    for (i = 1; i <= n; i++) {
      l = lines[i]
      if (l ~ /^[[:space:]]*(#.*)?$/) continue
      if (l ~ /^kind: /) kinds++
      if (l ~ /^apiVersion: /) apis++
    }
    if (kinds == 0 && apis == 0) return
    total++
    if (kinds != 1 || apis != 1) {
      bad = 1
      printf "unbalanced rendered document (kind=%d apiVersion=%d)\n", kinds, apis > "/dev/stderr"
    }
  }
  END {
    if (bad) exit 1
    if (total < 1) exit 1
    print total
  }
' "${rendered}")"; then
  fail "rendered output contains unbalanced YAML documents"
fi

# Rendered CRD is the alpha4-only CRD.
grep -q '^kind: CustomResourceDefinition$' "${rendered}" || fail "rendered output missing the CRD"
[[ "$(grep -c '^kind: CustomResourceDefinition$' "${rendered}")" -eq 1 ]] || fail "rendered output must contain exactly one CRD"
grep -q '^  name: simulationexperiments.experiment.cbse.terministic.de$' "${rendered}" || fail "rendered CRD name mismatch"
cbse_alpha4_versions="$(grep -c '^    name: alpha4$' "${rendered}")"
[[ "${cbse_alpha4_versions}" -eq 1 ]] || fail "rendered CRD must serve exactly one version (alpha4), found ${cbse_alpha4_versions}"
# Count version entries inside the CRD document only: workload templates
# render their own 4-space indented name: list items (e.g. RoleBinding
# subjects), so the count must be scoped to the CustomResourceDefinition
# document, not the full multi-document render.
cbse_crd_version_entries="$(awk '
  /^---$/ { in_crd = 0 }
  /^kind: CustomResourceDefinition$/ { in_crd = 1 }
  in_crd && /^    name: / { n++ }
  END { print n + 0 }
' "${rendered}")"
[[ "${cbse_crd_version_entries}" -eq 1 ]] || fail "rendered CRD must define a single version entry"
grep -q 'served: true' "${rendered}" || fail "rendered CRD must mark alpha4 served"
grep -q 'storage: true' "${rendered}" || fail "rendered CRD must mark alpha4 storage"

# Digest scan: no rendered image reference may be tag-based. (H1 renders no
# workload images; the invariant binds every later slice that does.)
if grep -E '^[[:space:]]*image:[[:space:]]' "${rendered}" |
  grep -Ev '^[[:space:]]*image: [^ ]+@sha256:[a-f0-9]{64}$' >"${tmp}/tagged-images.txt"; then
  cat "${tmp}/tagged-images.txt" >&2
  fail "rendered output contains a tag-based image reference"
fi

# Source tag scan: chart and fixture files must never carry image: repo:tag.
if grep -RnE '^[[:space:]]*image:[[:space:]]*[^ ]+:[^ ]+' "${chart}" "${root}/test/helm" >"${tmp}/source-tags.txt"; then
  cat "${tmp}/source-tags.txt" >&2
  fail "chart or fixture source contains a tag-based image reference"
fi

# CRD lockstep: the chart CRD copy is byte-identical to the generated CRD.
generated_crd="${root}/experiment-operator/config/crd/bases/experiment.cbse.terministic.de_simulationexperiments.yaml"
chart_crd="${chart}/crds/experiment.cbse.terministic.de_simulationexperiments.yaml"
diff -u "${generated_crd}" "${chart_crd}" >/dev/null || fail "chart CRD copy is not byte-identical to the generated alpha4 CRD"
echo "  crd lockstep: chart copy byte-identical to generated alpha4 CRD"

# Private-string scan over the H1-owned chart and test files: no private
# registry, cluster endpoint, credential material, or local filesystem path.
scan_files=()
while IFS= read -r f; do
  scan_files+=("${f}")
done < <(find "${chart}" "${root}/test/helm" -type f)
scan_files+=("${root}/test/harness/install-helm.sh" "${root}/test/harness/test-helm-chart.sh" "${root}/Makefile")
private_hits="$(
  grep -RnE \
    -e 'registry\.unibw\.de' \
    -e '192\.168\.' \
    -e '(^|[^[:alnum:]])(10|172\.(1[6-9]|2[0-9]|3[0-1]))\.[0-9]+\.[0-9]+' \
    -e '/Users/[a-z]' \
    -e '/home/[a-z]' \
    -e 'dG[V]zdDp0Z[X]N0' \
    -e 'pass[w]ord:[[:space:]]*[^ ]' \
    "${scan_files[@]}" || true
)"
if [[ -n "${private_hits}" ]]; then
  printf '%s\n' "${private_hits}" >&2
  fail "private infrastructure strings found in H1-owned files"
fi

# Contract scripts (metadata, value keys, helpers, schema negative cases).
# shellcheck source=test/helm/assert-helpers.sh
source "${root}/test/helm/assert-helpers.sh"
HELM="${pinned_helm}" cbse_helm_run_discovered

echo "Helm chart tests passed."
