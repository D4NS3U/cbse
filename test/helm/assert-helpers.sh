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

# assert-helpers.sh — shared shell assertion helpers for the CBSE Helm chart
# contract tests under test/helm/, plus automatic discovery/dispatch of every
# test/helm/assert-*.sh script added by later slices (this file is excluded
# from discovery).
#
# This file is sourced, never executed. test/harness/test-helm-chart.sh
# sources it once and calls cbse_helm_run_discovered; each individual
# assert-*.sh script sources it for the helpers. Nothing here performs
# cluster mutation or network access; rendering uses the pinned Helm binary
# against local files only.
#
# Environment (resolved lazily by cbse_helm_resolve):
#   CBSE_HELM_ROOT    repository root (auto-derived from this file's path).
#   CBSE_HELM_CHART   chart directory (defaults to charts/cbse).
#   CBSE_HELM_VALUES  fixture values (defaults to test/helm/values-render.yaml).
#   CBSE_HELM_BIN     pinned helm binary (HELM env when set, else the single
#                     repository bin/helm-* install, else helm on PATH).
#
# Exit codes (cbse_helm_fail):
#   1  an assertion failed.
set -o pipefail

cbse_helm_fail() {
  echo "helm contract assertion failed: $*" >&2
  exit 1
}

cbse_helm_assert_eq() {
  local description="$1" actual="$2" expected="$3"
  [[ "${actual}" == "${expected}" ]] ||
    cbse_helm_fail "${description}: expected ${expected}, got ${actual}"
}

cbse_helm_assert_matches() {
  local description="$1" actual="$2" pattern="$3"
  [[ "${actual}" =~ ${pattern} ]] ||
    cbse_helm_fail "${description}: expected match against ${pattern}, got ${actual}"
}

cbse_helm_assert_not_matches() {
  local description="$1" actual="$2" pattern="$3"
  [[ ! "${actual}" =~ ${pattern} ]] ||
    cbse_helm_fail "${description}: must not match ${pattern}, got ${actual}"
}

cbse_helm_assert_contains() {
  local description="$1" haystack="$2" needle="$3"
  [[ "${haystack}" == *"${needle}"* ]] ||
    cbse_helm_fail "${description}: expected to contain ${needle}"
}

cbse_helm_assert_line_count() {
  local description="$1" actual="$2" expected="$3"
  [[ "${actual}" -eq "${expected}" ]] ||
    cbse_helm_fail "${description}: expected ${expected} matching line(s), found ${actual}"
}

# cbse_helm_assert_fails DESCRIPTION COMMAND...
# COMMAND must exit non-zero; its combined output is reported on failure.
cbse_helm_assert_fails() {
  local description="$1"
  shift
  local output rc=0
  output="$("$@" 2>&1)" || rc=$?
  [[ "${rc}" -ne 0 ]] ||
    cbse_helm_fail "${description}: command unexpectedly succeeded: $(printf '%s' "${output}" | head -n 3)"
}

# cbse_helm_resolve — derive the chart, fixture values, and pinned helm
# binary; idempotent.
cbse_helm_resolve() {
  if [[ -z "${CBSE_HELM_ROOT:-}" ]]; then
    CBSE_HELM_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
    export CBSE_HELM_ROOT
  fi
  CBSE_HELM_CHART="${CBSE_HELM_CHART:-${CBSE_HELM_ROOT}/charts/cbse}"
  export CBSE_HELM_CHART
  CBSE_HELM_VALUES="${CBSE_HELM_VALUES:-${CBSE_HELM_ROOT}/test/helm/values-render.yaml}"
  export CBSE_HELM_VALUES
  if [[ -z "${CBSE_HELM_BIN:-}" ]]; then
    if [[ -n "${HELM:-}" ]]; then
      CBSE_HELM_BIN="${HELM}"
    else
      local candidate candidates=()
      for candidate in "${CBSE_HELM_ROOT}"/bin/helm-*; do
        [[ -x "${candidate}" ]] || continue
        candidates+=("${candidate}")
      done
      if [[ "${#candidates[@]}" -eq 1 ]]; then
        CBSE_HELM_BIN="${candidates[0]}"
      elif command -v helm >/dev/null 2>&1; then
        CBSE_HELM_BIN="$(command -v helm)"
      else
        cbse_helm_fail "no pinned helm binary found; run 'make helm-tools' or set HELM"
      fi
    fi
    export CBSE_HELM_BIN
  fi
}

# cbse_helm_render OUTPUT — render the chart with the fixture values into
# OUTPUT (stdout of helm is redirected; errors propagate).
cbse_helm_render() {
  local output="$1"
  "${CBSE_HELM_BIN}" template cbse "${CBSE_HELM_CHART}" \
    --namespace cbse-system --include-crds -f "${CBSE_HELM_VALUES}" >"${output}"
}

# cbse_helm_render_try OUTPUT EXTRA_VALUES — render with an extra values file
# and write combined stdout/stderr to OUTPUT; returns the helm exit code
# without failing (for negative schema/helper cases).
cbse_helm_render_try() {
  local output="$1" extra_values="$2"
  "${CBSE_HELM_BIN}" template cbse "${CBSE_HELM_CHART}" \
    --namespace cbse-system --include-crds -f "${CBSE_HELM_VALUES}" -f "${extra_values}" \
    >"${output}" 2>&1
  return $?
}

# cbse_helm_run_discovered — execute every test/helm/assert-*.sh contract
# script (this helper file excluded) in filename order. Each script runs in
# its own process so an assert failure cannot leak state.
cbse_helm_run_discovered() {
  local dir script found=0
  dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  for script in "${dir}"/assert-*.sh; do
    [[ -e "${script}" ]] || continue
    [[ "$(basename "${script}")" == "assert-helpers.sh" ]] && continue
    echo "  helm contract script: $(basename "${script}")"
    bash "${script}"
    found=1
  done
  [[ "${found}" -eq 1 ]] || cbse_helm_fail "no test/helm/assert-*.sh contract scripts discovered"
}
