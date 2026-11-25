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

# assert-lifecycle.sh — H4 lifecycle-safety contract assertions.
#
# Proves, against charts/cbse with the pinned Helm binary and the public
# example.invalid fixture values, the normative H4 hook resources (slice
# devlog/changes/helm-control-plane-installation/slices/H4-lifecycle-safety.md):
#   * hook ServiceAccount/Role/RoleBinding are pre-install,pre-upgrade,
#     pre-delete hooks at weight -10 with the before-hook-creation,
#     hook-succeeded delete policy, and the registry-validation and
#     experiment-guard Jobs are the matching pre-install,pre-upgrade and
#     pre-delete hooks at weight 0 with the same policy (RBAC exists before
#     the Jobs; failed Jobs remain for diagnosis until the next attempt);
#   * exact least-privilege hook RBAC: get on the single named Secret
#     cbse-registry-auth, list on SimulationExperiments, no other verbs,
#     resources, or wildcards, and no delete/patch verb anywhere;
#   * the hook ServiceAccount carries no imagePullSecrets (the locked
#     public hook image must be pullable without cbse-registry-auth);
#   * both Jobs run the locked digest-pinned kubectl image
#     (hooks.kubectlImage via cbse.image), backoffLimit 0, restartPolicy
#     Never, the hook ServiceAccount, and the numeric-1000 restricted
#     security profile (non-root, RuntimeDefault, read-only root, drop ALL,
#     no privilege escalation);
#   * registry validation is read-only and payload-safe: only kubectl get
#     with scoped jsonpath (type and .dockerconfigjson presence) against the
#     named Secret, no decode/print/trace/persist of the payload, no
#     Secret ownership verbs, and constant actionable error lines for
#     missing Secret, wrong type, and missing/empty key;
#   * the pre-delete guard is a read-only list of SimulationExperiments:
#     zero objects succeed, blockers exit non-zero with the exact recovery
#     guidance (delete all SimulationExperiments with --wait, then rerun
#     helm uninstall), no destructive kubectl invocation outside the
#     guidance text, and no CR name or payload output.
#
# No cluster mutation and no network access; everything renders locally.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=test/helm/assert-helpers.sh
source "${root}/test/helm/assert-helpers.sh"
cbse_helm_resolve

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

# render_one TEMPLATE OUTPUT — render exactly one chart template file.
render_one() {
  local template="$1" output="$2"
  "${CBSE_HELM_BIN}" template cbse "${CBSE_HELM_CHART}" \
    --namespace cbse-system -f "${CBSE_HELM_VALUES}" \
    --show-only "templates/${template}" >"${output}"
}

sa="${tmp}/hook-sa.yaml"
role="${tmp}/hook-role.yaml"
rolebinding="${tmp}/hook-rolebinding.yaml"
validate_job="${tmp}/validate-job.yaml"
guard_job="${tmp}/guard-job.yaml"
render_one "hooks/hook-serviceaccount.yaml" "${sa}"
render_one "hooks/hook-role.yaml" "${role}"
render_one "hooks/hook-rolebinding.yaml" "${rolebinding}"
render_one "hooks/registry-validation-job.yaml" "${validate_job}"
render_one "hooks/experiment-guard-job.yaml" "${guard_job}"

# --- 1. hook annotations and deterministic weights ---------------------------
assert_hook_annotations() {
  local file="$1" description="$2" expected_hook="$3" expected_weight="$4"
  cbse_helm_assert_contains "${description} hook annotation" "$(cat "${file}")" \
    "helm.sh/hook: \"${expected_hook}\""
  cbse_helm_assert_contains "${description} hook weight" "$(cat "${file}")" \
    "helm.sh/hook-weight: \"${expected_weight}\""
  cbse_helm_assert_contains "${description} hook delete policy" "$(cat "${file}")" \
    'helm.sh/hook-delete-policy: "before-hook-creation,hook-succeeded"'
}
assert_hook_annotations "${sa}" "hook ServiceAccount" "pre-install,pre-upgrade,pre-delete" "-10"
assert_hook_annotations "${role}" "hook Role" "pre-install,pre-upgrade,pre-delete" "-10"
assert_hook_annotations "${rolebinding}" "hook RoleBinding" "pre-install,pre-upgrade,pre-delete" "-10"
assert_hook_annotations "${validate_job}" "registry validation Job" "pre-install,pre-upgrade" "0"
assert_hook_annotations "${guard_job}" "experiment guard Job" "pre-delete" "0"
# Weight ordering: RBAC (-10) is deterministically created before the Jobs (0).
cbse_helm_assert_not_matches "Job weight must not precede the RBAC weight" \
  "$(grep -h 'helm.sh/hook-weight' "${sa}" "${role}" "${rolebinding}" | head -n1)" '^.*"0".*$'

# --- 2. fixed names, SA binding, and no pull Secret on the hook SA -----------
grep -Fqx '  name: cbse-helm-hooks' "${sa}" || cbse_helm_fail "hook ServiceAccount name"
grep -Fqx '  name: cbse-helm-hooks' "${role}" || cbse_helm_fail "hook Role name"
grep -Fqx '  name: cbse-helm-hooks' "${rolebinding}" || cbse_helm_fail "hook RoleBinding name"
grep -Fqx '  name: cbse-hook-registry-validation' "${validate_job}" || cbse_helm_fail "validation Job name"
grep -Fqx '  name: cbse-hook-experiment-guard' "${guard_job}" || cbse_helm_fail "guard Job name"
grep -Fqx '  kind: Role' "${rolebinding}" || cbse_helm_fail "RoleBinding must reference the hook Role"
grep -Fqx '  name: cbse-helm-hooks' <(sed -n '/^roleRef:/,/^subjects:/p' "${rolebinding}") \
  || cbse_helm_fail "RoleBinding roleRef name"
grep -Fqx '    name: cbse-helm-hooks' <(sed -n '/^subjects:/,$p' "${rolebinding}") \
  || cbse_helm_fail "RoleBinding subject"
grep -Fqx '      serviceAccountName: cbse-helm-hooks' "${validate_job}" \
  || cbse_helm_fail "validation Job serviceAccountName"
grep -Fqx '      serviceAccountName: cbse-helm-hooks' "${guard_job}" \
  || cbse_helm_fail "guard Job serviceAccountName"
if grep -vE '^[[:space:]]*#' "${sa}" | grep -q 'imagePullSecrets'; then
  cbse_helm_fail "hook ServiceAccount must carry no imagePullSecrets (public hook bootstrap without cbse-registry-auth)"
fi

# --- 3. exact least-privilege RBAC --------------------------------------------
cbse_helm_assert_line_count "hook Role rule count" "$(grep -c '^  - apiGroups:' "${role}")" 2
role_text="$(cat "${role}")"
cbse_helm_assert_contains "named registry Secret get rule" "${role_text}" 'resourceNames: ["cbse-registry-auth"]'
cbse_helm_assert_contains "registry Secret get verb" "${role_text}" 'verbs: ["get"]'
cbse_helm_assert_contains "SimulationExperiments list rule" "${role_text}" 'resources: ["simulationexperiments"]'
cbse_helm_assert_contains "SimulationExperiments list verb" "${role_text}" 'verbs: ["list"]'
cbse_helm_assert_line_count "hook Secret resource scope" "$(grep -c 'resources: \["secrets"\]' "${role}")" 1
if grep -qE 'verbs: \[[^]]*(delete|patch|create|update|put)' "${role}"; then
  cbse_helm_fail "hook Role carries a destructive or mutating verb"
fi
if grep -qE '"\*"|^\s*verbs: \[\s*"\*"' "${role}"; then
  cbse_helm_fail "hook Role contains a wildcard verb or resource"
fi

# --- 4. locked digest kubectl image and restricted Job profiles ---------------
kubectl_image="example.invalid/kubectl@sha256:4444444444444444444444444444444444444444444444444444444444444444"
grep -Fqx "          image: ${kubectl_image}" "${validate_job}" \
  || cbse_helm_fail "validation Job must run the locked hooks.kubectlImage digest reference"
grep -Fqx "          image: ${kubectl_image}" "${guard_job}" \
  || cbse_helm_fail "guard Job must run the locked hooks.kubectlImage digest reference"
for job in "${validate_job}" "${guard_job}"; do
  job_text="$(cat "${job}")"
  grep -Fqx '  backoffLimit: 0' "${job}" || cbse_helm_fail "hook Job must set backoffLimit 0 (failed Jobs remain for diagnosis)"
  grep -Fqx '      restartPolicy: Never' "${job}" || cbse_helm_fail "hook Job restartPolicy"
  grep -Fqx '        runAsNonRoot: true' "${job}" || cbse_helm_fail "hook Job runAsNonRoot"
  grep -Fqx '        runAsUser: 1000' "${job}" || cbse_helm_fail "hook Job numeric UID 1000"
  grep -Fqx '        runAsGroup: 1000' "${job}" || cbse_helm_fail "hook Job numeric GID 1000"
  grep -Fqx '        fsGroup: 1000' "${job}" || cbse_helm_fail "hook Job fsGroup"
  grep -Fqx '          type: RuntimeDefault' "${job}" || cbse_helm_fail "hook Job seccomp RuntimeDefault"
  grep -Fqx '            allowPrivilegeEscalation: false' "${job}" || cbse_helm_fail "hook Job allowPrivilegeEscalation"
  grep -Fqx '              drop: ["ALL"]' "${job}" || cbse_helm_fail "hook Job drop ALL capabilities"
  grep -Fqx '            readOnlyRootFilesystem: true' "${job}" || cbse_helm_fail "hook Job read-only root filesystem"
  cbse_helm_assert_contains "hook Job namespace from downward API" "${job_text}" 'fieldPath: metadata.namespace'
  cbse_helm_assert_contains "hook Job runs sh -ec" "${job_text}" 'command: ["/bin/sh", "-ec"]'
done

# --- 5. registry validation: read-only, payload-safe, constant error lines ----
validate_args="$(sed -n '/^          args:/,/^          env:/p' "${validate_job}")"
cbse_helm_assert_contains "validation reads the named Secret by scoped jsonpath" \
  "${validate_args}" "kubectl get secret \"\${secret}\" -n \"\${ns}\" -o jsonpath='{.data.\\.dockerconfigjson}'"
cbse_helm_assert_contains "validation reads the Secret type by scoped jsonpath" \
  "${validate_args}" "kubectl get secret \"\${secret}\" -n \"\${ns}\" -o jsonpath='{.type}'"
if printf '%s' "${validate_args}" | grep -q 'secret="cbse-registry-auth"' || grep -q 'secret="cbse-registry-auth"' "${validate_job}"; then
  :
else
  cbse_helm_fail "validation must target exactly the fixed-name Secret cbse-registry-auth"
fi
if printf '%s' "${validate_args}" | grep -vE '^[[:space:]]*(echo|#)' | grep -qE 'kubectl (delete|patch|apply|replace|create|label|annotate|edit|apply) '; then
  cbse_helm_fail "registry validation must be read-only (no mutating kubectl verbs)"
fi
if printf '%s' "${validate_args}" | grep -qE '\-o (yaml|json|wide|name)([[:space:]]|$)|base64[[:space:]]+-d|--decode|set -x|xtrace'; then
  cbse_helm_fail "registry validation must not decode or print the Secret payload"
fi
# The captured .dockerconfigjson value may be tested for non-emptiness but is
# never emitted: exactly two lines in the script mention the value variable —
# the scoped-jsonpath capture assignment and the empty test — and no other
# line references it.
cbse_helm_assert_line_count "registry value variable used only by capture and empty test" \
  "$(printf '%s' "${validate_args}" | grep -c 'value' || true)" 2
cbse_helm_assert_contains "value captured from the scoped jsonpath only" \
  "${validate_args}" 'value="$(kubectl get secret "'
cbse_helm_assert_contains "validation missing-Secret error line" "${validate_args}" \
  "the installation-owner Secret cbse-registry-auth is missing from namespace"
cbse_helm_assert_contains "validation wrong-type error line" "${validate_args}" \
  'has type ${type}; the CBSE chart requires type kubernetes.io/dockerconfigjson'
cbse_helm_assert_contains "validation empty-key error line" "${validate_args}" \
  "has a missing or empty .dockerconfigjson key"
# No chart-rendered Secret object and no ownership annotation on the registry
# Secret anywhere in the rendered hooks.
if grep -q '^kind: Secret' "${sa}" "${role}" "${rolebinding}" "${validate_job}" "${guard_job}"; then
  cbse_helm_fail "hooks must never render or own a Secret"
fi

# --- 6. pre-delete guard: read-only list, suppression, exact guidance --------
guard_args="$(sed -n '/^          args:/,/^          env:/p' "${guard_job}")"
cbse_helm_assert_contains "guard lists the full SimulationExperiment plural" \
  "${guard_args}" "kubectl get simulationexperiments.experiment.cbse.terministic.de -n \"\${ns}\" -o jsonpath='{.items[*].metadata.name}'"
if printf '%s' "${guard_args}" | grep -vE '^[[:space:]]*(echo|#)' | grep -qE 'kubectl (delete|patch|apply|replace|create|label|annotate|edit|scale) '; then
  cbse_helm_fail "the experiment guard must never run destructive kubectl verbs"
fi
# The delete command appears only inside the recovery-guidance echo line.
delete_mentions="$(printf '%s\n' "${guard_args}" | grep -F 'delete simulationexperiments' || true)"
non_echo_delete="$(printf '%s\n' "${delete_mentions}" | grep -v 'echo' || true)"
if [[ -n "${non_echo_delete}" ]]; then
  cbse_helm_fail "the guard may name the recovery delete command only in the guidance echo, not as an invocation: ${non_echo_delete}"
fi
cbse_helm_assert_contains "guard recovery guidance" "${guard_args}" \
  'Recovery: delete all SimulationExperiments in namespace ${ns} with kubectl delete simulationexperiments --all -n ${ns} --wait, wait for their finalizers to clear, then rerun helm uninstall'
cbse_helm_assert_contains "guard non-zero exit on blockers" "${guard_args}" 'exit 1'
cbse_helm_assert_contains "guard count, not names, on stdout" "${guard_args}" \
  'CBSE pre-delete guard: ${count} SimulationExperiment resource(s) still exist'
# No experiment payload/name output: no -o yaml/json/wide anywhere in the guard.
if printf '%s' "${guard_args}" | grep -qE '\-o (yaml|json|wide|name)([[:space:]]|$)'; then
  cbse_helm_fail "the guard must not render CR payloads (only a name-suppressed count)"
fi

echo "assert-lifecycle: all H4 lifecycle contract assertions passed."
