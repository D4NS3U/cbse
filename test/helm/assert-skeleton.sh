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

# assert-skeleton.sh — H1 chart contract assertions.
#
# Proves, against charts/cbse with the pinned Helm binary:
#   * chart metadata (v2, name, type, version, appVersion, no dependencies);
#   * the exact value-surface keys of values.yaml (top-level set and the
#     complete indent/key shape of every owned component);
#   * the fixed compatibility-name helpers and the standard label set via a
#     temporary helper-probe chart (the repository chart is untouched);
#   * digest-only image composition (<repository>@<digest>) for every image
#     object in the fixture;
#   * invalid digest rejection (schema level and helper level), tag-bearing
#     repository rejection, and empty-image rejection through Helm `required`;
#   * unknown-key rejection at every owned object boundary (schema level);
#   * numeric range rejection (runnerStartWorkers, healthPort, storage sizes,
#     imagePullPolicy);
#   * no registry/server/storage-class defaults in the raw source values.
#
# No cluster mutation and no network access; everything renders locally.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=test/helm/assert-helpers.sh
source "${root}/test/helm/assert-helpers.sh"
cbse_helm_resolve

values="${CBSE_HELM_CHART}/values.yaml"
chart_yaml="${CBSE_HELM_CHART}/Chart.yaml"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

# --- 1. Chart metadata -------------------------------------------------------
chart_meta="$("${CBSE_HELM_BIN}" show chart "${CBSE_HELM_CHART}")"
cbse_helm_assert_contains "chart apiVersion" "${chart_meta}" "apiVersion: v2"
cbse_helm_assert_contains "chart name" "${chart_meta}" "name: cbse"
cbse_helm_assert_contains "chart type" "${chart_meta}" "type: application"
cbse_helm_assert_contains "chart version" "${chart_meta}" "version: 0.1.0"
cbse_helm_assert_contains "chart appVersion" "${chart_meta}" "appVersion: 0.1.0"
cbse_helm_assert_contains "chart description names the alpha4 CRD" "${chart_meta}" "SimulationExperiment alpha4 CRD"
[[ ! "${chart_meta}" == *"dependencies"* ]] || cbse_helm_fail "chart must declare no dependencies"
grep -Fqx 'home: https://github.com/D4NS3U/cbse' "${chart_yaml}"
cbse_helm_assert_line_count "Chart.yaml public source entries" \
  "$(grep -cF '  - https://github.com/D4NS3U/cbse' "${chart_yaml}")" 1
cbse_helm_assert_line_count "Chart.yaml http links (home + one source)" \
  "$(grep -c 'http' "${chart_yaml}")" 2

# --- 2. Exact value-surface keys ---------------------------------------------
# Top-level key set, in file order.
top_keys="$(grep -E '^[a-zA-Z]+:' "${values}" | cut -d: -f1)"
cbse_helm_assert_eq "values.yaml top-level keys" "${top_keys}" \
  "$(printf 'global\noperator\nscenarioManager\nnats\ncoreDatabase\nhooks\nnetworkPolicies')"

# Print "indent key" for every scalar/list-start entry inside one top-level
# component block; comments and blank lines are skipped.
block_entries() {
  local file="$1" top="$2"
  awk -v top="${top}" '
    $0 == top ":" { inblock=1; next }
    inblock && /^[^ \t]/ { inblock=0 }
    inblock && /^[ \t]+[A-Za-z_]/ {
      pos = index($0, ":")
      if (pos > 0) {
        n = 0
        while (substr($0, n + 1, 1) == " ") n++
        key = substr($0, 1, pos - 1)
        sub(/^ */, "", key)
        printf "%d %s\n", n, key
      }
    }
  ' "${file}"
}

cbse_helm_assert_eq "global value keys" "$(block_entries "${values}" global)" \
  "2 imagePullPolicy"
cbse_helm_assert_eq "operator value keys" "$(block_entries "${values}" operator)" \
  "$(printf '2 image\n4 repository\n4 digest\n2 resources\n4 requests\n6 cpu\n6 memory\n4 limits\n6 cpu\n6 memory')"
cbse_helm_assert_eq "scenarioManager value keys" "$(block_entries "${values}" scenarioManager)" \
  "$(printf '2 image\n4 repository\n4 digest\n2 runnerStartWorkers\n2 healthPort\n2 resources\n4 requests\n6 cpu\n6 memory\n4 limits\n6 cpu\n6 memory')"
cbse_helm_assert_eq "nats value keys" "$(block_entries "${values}" nats)" \
  "$(printf '2 image\n4 repository\n4 digest\n2 jetstream\n4 maxMemoryStore\n4 maxFileStore\n2 persistence\n4 enabled\n4 size\n4 storageClass\n4 retain\n2 resources\n4 requests\n6 cpu\n6 memory\n4 limits\n6 cpu\n6 memory')"
cbse_helm_assert_eq "coreDatabase value keys" "$(block_entries "${values}" coreDatabase)" \
  "$(printf '2 image\n4 repository\n4 digest\n2 database\n2 persistence\n4 enabled\n4 size\n4 storageClass\n4 retain\n2 resources\n4 requests\n6 cpu\n6 memory\n4 limits\n6 cpu\n6 memory')"
cbse_helm_assert_eq "hooks value keys" "$(block_entries "${values}" hooks)" \
  "$(printf '2 kubectlImage\n4 repository\n4 digest')"
cbse_helm_assert_eq "networkPolicies value keys" "$(block_entries "${values}" networkPolicies)" \
  "2 enabled"

# --- 2b. NOTES.txt content contract ------------------------------------------
# helm template does not render NOTES.txt, so the bounded message contract is
# asserted on the source file: it must name the pre-release status, the
# pre-existing cbse-registry-auth, the release-namespace experiment scope, and
# the H6 guide; it must contain no command or credential material.
notes="${CBSE_HELM_CHART}/templates/NOTES.txt"
cbse_helm_assert_contains "NOTES pre-release status" "$(cat "${notes}")" "pre-release"
cbse_helm_assert_contains "NOTES registry Secret prerequisite" "$(cat "${notes}")" "cbse-registry-auth"
cbse_helm_assert_contains "NOTES release namespace scope" "$(cat "${notes}")" "SimulationExperiment resources belong in the release namespace"
cbse_helm_assert_contains "NOTES H6 full guide" "$(cat "${notes}")" "H6 documentation slice"
if grep -nE '^(kubectl|helm) |dG[V]zdDp0Z[X]N0|pass[w]ord[[:space:]]*:' "${notes}"; then
  cbse_helm_fail "NOTES.txt contains a command or credential material"
fi

# --- 3. Fixed-name helpers and labels via a temporary probe chart -------------
probe="${tmp}/probe-chart"
cp -R "${CBSE_HELM_CHART}" "${probe}"
cat >"${probe}/templates/h1-helper-probe.yaml" <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: cbse-h1-helper-probe
  labels: {{- include "cbse.labels" . | nindent 4 }}
data:
  selectorLabels: |
{{ include "cbse.selectorLabels" . | indent 4 }}
  operatorImage: {{ include "cbse.image" .Values.operator.image }}
  scenarioManagerImage: {{ include "cbse.image" .Values.scenarioManager.image }}
  natsImage: {{ include "cbse.image" .Values.nats.image }}
  coreDatabaseImage: {{ include "cbse.image" .Values.coreDatabase.image }}
  kubectlImage: {{ include "cbse.image" .Values.hooks.kubectlImage }}
  imagePullSecrets: |
{{ include "cbse.serviceAccountImagePullSecrets" . | indent 4 }}
  operatorName: {{ include "cbse.name.experimentOperator" . }}
  scenarioManagerName: {{ include "cbse.name.scenarioManager" . }}
  natsServiceName: {{ include "cbse.name.natsService" . }}
  coreDatabaseServiceName: {{ include "cbse.name.coreDatabaseService" . }}
  credentialsName: {{ include "cbse.name.coreDatabaseCredentials" . }}
EOF
probe_render="${tmp}/probe.yaml"
"${CBSE_HELM_BIN}" template cbse "${probe}" --namespace cbse-system \
  -f "${CBSE_HELM_VALUES}" >"${probe_render}"

# Digest-only image composition for every image object in the fixture.
grep -Fqx '  operatorImage: example.invalid/cbse/experiment-operator@sha256:0000000000000000000000000000000000000000000000000000000000000000' "${probe_render}"
grep -Fqx '  scenarioManagerImage: example.invalid/cbse/scenario-manager@sha256:1111111111111111111111111111111111111111111111111111111111111111' "${probe_render}"
grep -Fqx '  natsImage: example.invalid/nats@sha256:2222222222222222222222222222222222222222222222222222222222222222' "${probe_render}"
grep -Fqx '  coreDatabaseImage: example.invalid/postgres@sha256:3333333333333333333333333333333333333333333333333333333333333333' "${probe_render}"
grep -Fqx '  kubectlImage: example.invalid/kubectl@sha256:4444444444444444444444444444444444444444444444444444444444444444' "${probe_render}"

# Fixed compatibility names, exactly and without release prefixing.
grep -Fqx '  operatorName: experiment-operator' "${probe_render}"
grep -Fqx '  scenarioManagerName: scenario-manager' "${probe_render}"
grep -Fqx '  natsServiceName: sm-eds-nats' "${probe_render}"
grep -Fqx '  coreDatabaseServiceName: core-db' "${probe_render}"
grep -Fqx '  credentialsName: core-db-credentials' "${probe_render}"

# The fixed registry pull Secret name is emitted exactly once.
cbse_helm_assert_line_count "pull Secret name" \
  "$(grep -cF '    - name: cbse-registry-auth' "${probe_render}")" 1

# Standard label set on chart-owned objects.
grep -Fqx '    app.kubernetes.io/name: cbse' "${probe_render}"
grep -Fqx '    app.kubernetes.io/instance: cbse' "${probe_render}"
grep -Fqx '    app.kubernetes.io/version: 0.1.0' "${probe_render}"
grep -Fqx '    app.kubernetes.io/managed-by: Helm' "${probe_render}"
grep -Fqx '    app.kubernetes.io/part-of: cbse' "${probe_render}"

# Selectors contain only the stable keys: name + instance, never version.
selector_labels="$(awk '/selectorLabels: \|/{f=1;next} f && /^  [a-z]/{f=0} f' "${probe_render}")"
cbse_helm_assert_eq "selector label set" "${selector_labels}" \
  "$(printf '    app.kubernetes.io/name: cbse\n    app.kubernetes.io/instance: cbse')"

# --- 4. Negative: invalid digests, tags, and empty image fields ---------------
probe_try() {
  # Render the probe chart with an extra values file into $1; return helm rc.
  local output="$1" extra_values="$2"
  "${CBSE_HELM_BIN}" template cbse "${probe}" --namespace cbse-system \
    -f "${CBSE_HELM_VALUES}" -f "${extra_values}" >"${output}" 2>&1
  return $?
}

# Helper level: an empty digest must fail through cbse.image `required`.
printf 'nats:\n  image:\n    digest: ""\n' >"${tmp}/empty-digest.yaml"
out="${tmp}/empty-digest.out"
if probe_try "${out}" "${tmp}/empty-digest.yaml"; then
  cbse_helm_fail "empty digest was accepted by cbse.image"
fi
cbse_helm_assert_contains "empty digest message" "$(cat "${out}")" "image.digest is required"

# Helper level: an empty repository must fail through cbse.image `required`.
printf 'operator:\n  image:\n    repository: ""\n' >"${tmp}/empty-repo.yaml"
out="${tmp}/empty-repo.out"
if probe_try "${out}" "${tmp}/empty-repo.yaml"; then
  cbse_helm_fail "empty repository was accepted by cbse.image"
fi
cbse_helm_assert_contains "empty repository message" "$(cat "${out}")" "image.repository is required"

# Helper level: a tag-bearing repository must be rejected (no tag support).
printf 'operator:\n  image:\n    repository: "example.invalid/cbse/experiment-operator:latest"\n' >"${tmp}/tagged-repo.yaml"
out="${tmp}/tagged-repo.out"
if probe_try "${out}" "${tmp}/tagged-repo.yaml"; then
  cbse_helm_fail "tag-bearing repository was accepted by cbse.image"
fi
cbse_helm_assert_contains "tag rejection message" "$(cat "${out}")" "must not carry a tag"

# Schema level: malformed digests are rejected before any template runs.
for bad in 'sha256:000000000000000000000000000000000000000000000000000000000000000G' \
           'sha256:ABCDEF0000000000000000000000000000000000000000000000000000000000' \
           'sha256:abc' \
           'not-a-digest' \
           'latest'; do
  printf 'coreDatabase:\n  image:\n    digest: "%s"\n' "${bad}" >"${tmp}/bad-digest.yaml"
  out="${tmp}/bad-digest.out"
  if "${CBSE_HELM_BIN}" template cbse "${CBSE_HELM_CHART}" --namespace cbse-system \
      --include-crds -f "${CBSE_HELM_VALUES}" -f "${tmp}/bad-digest.yaml" \
      >"${out}" 2>&1; then
    cbse_helm_fail "invalid digest ${bad} passed the chart schema"
  fi
  cbse_helm_assert_contains "invalid digest ${bad} reported" "$(cat "${out}")" "digest"
done

# --- 5. Negative: unknown keys at every owned object boundary ------------------
unknown_key_case() {
  local path="$1"
  local extra="${tmp}/unknown-key.yaml" out="${tmp}/unknown-key.out"
  if [[ "${path}" == *"."* ]]; then
    local parent leaf
    parent="${path%.*}"
    leaf="${path##*.}"
    printf '%s:\n  %s: true\n' "${parent}" "${leaf}" >"${extra}"
  else
    printf '%s: true\n' "${path}" >"${extra}"
  fi
  if "${CBSE_HELM_BIN}" template cbse "${CBSE_HELM_CHART}" --namespace cbse-system \
      --include-crds -f "${CBSE_HELM_VALUES}" -f "${extra}" >"${out}" 2>&1; then
    cbse_helm_fail "unknown key ${path} was accepted by the chart schema"
  fi
  local leaf
  leaf="${path##*.}"
  cbse_helm_assert_contains "unknown key ${path} reported" "$(cat "${out}")" "Additional property ${leaf} is not allowed"
}

for path in bogusTopLevel global.bogusKey operator.bogusKey scenarioManager.bogusKey \
            nats.bogusKey coreDatabase.bogusKey hooks.bogusKey networkPolicies.bogusKey; do
  unknown_key_case "${path}"
done

# --- 6. Negative: numeric and type ranges ---------------------------------------
range_case() {
  local path="$1" value="$2"
  local extra="${tmp}/range.yaml" out="${tmp}/range.out"
  local leaf
  leaf="${path##*.}"
  local IFS='.'
  local -a parts=(${path})
  unset IFS
  local indent=0 i
  {
    for ((i = 0; i < ${#parts[@]}; i++)); do
      printf '%*s%s: ' "$indent" '' "${parts[i]}"
      if (( i == ${#parts[@]} - 1 )); then
        printf '%s\n' "${value}"
      else
        printf '\n'
      fi
      indent=$((indent + 2))
    done
  } >"${extra}"
  if "${CBSE_HELM_BIN}" template cbse "${CBSE_HELM_CHART}" --namespace cbse-system \
      --include-crds -f "${CBSE_HELM_VALUES}" -f "${extra}" >"${out}" 2>&1; then
    cbse_helm_fail "out-of-range value ${value} for ${path} was accepted"
  fi
  cbse_helm_assert_contains "range ${path}=${value} reported" "$(cat "${out}")" "${leaf}"
}

range_case scenarioManager.runnerStartWorkers 0
range_case scenarioManager.runnerStartWorkers 65
range_case scenarioManager.runnerStartWorkers '"4"'
range_case scenarioManager.healthPort 0
range_case scenarioManager.healthPort 65536
range_case nats.persistence.size '"0Gi"'
range_case nats.persistence.size '""'
range_case global.imagePullPolicy '"Sometimes"'

# --- 7. No private infrastructure defaults in raw source values ------------------
# Every image repository/digest in the source chart is empty.
cbse_helm_assert_line_count "empty repository defaults" \
  "$(grep -cE '^ +repository: ""$' "${values}")" 5
cbse_helm_assert_line_count "empty digest defaults" \
  "$(grep -cE '^ +digest: ""$' "${values}")" 5
# No storage-class defaults.
cbse_helm_assert_line_count "empty storageClass defaults" \
  "$(grep -cE '^ +storageClass: ""$' "${values}")" 2
# No registry hosts, endpoints, or server URLs anywhere in the source values.
if grep -nE 'registry\.|https?://|:[0-9]{2,5}[^0-9]' "${values}"; then
  cbse_helm_fail "values.yaml contains a registry/server/endpoint default"
fi

# The render fixture is public and non-runnable: example.invalid repositories
# and syntactically valid 64-hex digests only.
fixture="${CBSE_HELM_VALUES}"
cbse_helm_assert_line_count "fixture example.invalid repositories" \
  "$(grep -cE '^ +repository: example\.invalid/' "${fixture}")" 5
cbse_helm_assert_line_count "fixture valid 64-hex digests" \
  "$(grep -cE '^ +digest: "sha256:[a-f0-9]{64}"$' "${fixture}")" 5
if grep -nE 'registry\.|/[a-z]+\.kubeconfig' "${fixture}"; then
  cbse_helm_fail "render fixture references private infrastructure"
fi

echo "assert-skeleton: all H1 chart contract assertions passed."
