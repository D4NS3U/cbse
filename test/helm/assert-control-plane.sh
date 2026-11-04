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

# assert-control-plane.sh — H2 control-plane contract assertions.
#
# Proves, against charts/cbse with the pinned Helm binary and the public
# example.invalid fixture values, the normative H2 resources (slice
# devlog/changes/helm-control-plane-installation/slices/H2-control-plane-workloads.md):
#   * exact resource names: experiment-operator, scenario-manager,
#     scenario-manager-config, and the fixed Secret references
#     cbse-registry-auth / core-db-credentials;
#   * namespace scope: namespaced Role/RoleBinding for both workloads and no
#     ClusterRole/ClusterRoleBinding in the rendered control plane;
#   * exact RBAC rules (verbatim rule blocks) including the named-Secret
#     resourceNames constraint;
#   * digest-only images <repository>@<digest> for both workloads and the
#     global imagePullPolicy;
#   * exact ServiceAccount imagePullSecrets [cbse-registry-auth];
#   * probes: HTTP /readyz readiness + /healthz liveness on the named health
#     port with the proven timings;
#   * exact ConfigMap env values (Core DB DSN without credentials, table
#     names, authentication-free NATS URL with explicitly empty legacy
#     user/password, runner-start workers, health address);
#   * watch namespace from the Pod namespace via the downward API;
#   * stable selectors (app label + H1 selector labels) and full label sets;
#   * restricted security contexts: runAsNonRoot, seccomp RuntimeDefault,
#     drop ALL capabilities, no privilege escalation, read-only root
#     filesystem, and the SM numeric UID/GID 1000 with no root UID anywhere;
#   * F-R1 consolidation: the full-render document and Secret-reference
#     counts are scoped to the H2-owned documents (fixed names
#     experiment-operator, scenario-manager, scenario-manager-config) so the
#     H3 data plane can render alongside without changing the exact H2
#     counts.
#
# No cluster mutation and no network access; everything renders locally.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=test/helm/assert-helpers.sh
source "${root}/test/helm/assert-helpers.sh"
cbse_helm_resolve

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

# Fixture image references (test/helm/values-render.yaml, public and
# non-runnable).
operator_image="example.invalid/cbse/experiment-operator@sha256:0000000000000000000000000000000000000000000000000000000000000000"
sm_image="example.invalid/cbse/scenario-manager@sha256:1111111111111111111111111111111111111111111111111111111111111111"

# render_one TEMPLATE OUTPUT — render exactly one chart template file.
render_one() {
  local template="$1" output="$2"
  "${CBSE_HELM_BIN}" template cbse "${CBSE_HELM_CHART}" \
    --namespace cbse-system -f "${CBSE_HELM_VALUES}" \
    --show-only "templates/${template}" >"${output}"
}

# rules_block FILE — print the rendered Role's rules block (top-level
# "rules:" line through end of the single-document file).
rules_block() {
  awk '/^rules:/{f=1} f' "$1"
}

# selector_labels FILE — print the Deployment matchLabels block: the 6-space
# indented label lines between "matchLabels:" and the next shallower key.
selector_labels() {
  awk '
    /^[[:space:]]*matchLabels:[[:space:]]*$/ { f = 1; next }
    f && /^      [^[:space:]]/ { print; next }
    f { f = 0 }
  ' "$1"
}

# --- 1. Operator resources ----------------------------------------------------
op_sa="${tmp}/op-sa.yaml"
render_one operator/serviceaccount.yaml "${op_sa}"
grep -Fqx 'kind: ServiceAccount' "${op_sa}"
grep -Fqx '  name: experiment-operator' "${op_sa}"
grep -Fqx 'imagePullSecrets:' "${op_sa}"
cbse_helm_assert_line_count "operator ServiceAccount pull Secret entries" \
  "$(grep -cF '  - name: cbse-registry-auth' "${op_sa}")" 1

op_role="${tmp}/op-role.yaml"
render_one operator/role.yaml "${op_role}"
grep -Fqx 'kind: Role' "${op_role}"
grep -Fqx '  name: experiment-operator' "${op_role}"
cbse_helm_assert_eq "operator Role rules" "$(rules_block "${op_role}")" "$(cat <<'EOF'
rules:
  - apiGroups: [""]
    resources: ["configmaps", "secrets", "serviceaccounts", "services", "pods"]
    verbs: ["create", "delete", "get", "list", "patch", "update", "watch"]
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["create", "patch"]
  - apiGroups: [""]
    resources: ["configmaps/finalizers", "secrets/finalizers", "serviceaccounts/finalizers", "services/finalizers"]
    verbs: ["update"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["create", "delete", "get", "list", "patch", "update", "watch"]
  - apiGroups: ["experiment.cbse.terministic.de"]
    resources: ["simulationexperiments", "simulationexperiments/finalizers", "simulationexperiments/status"]
    verbs: ["create", "delete", "get", "list", "patch", "update", "watch"]
EOF
)"

op_rb="${tmp}/op-rolebinding.yaml"
render_one operator/rolebinding.yaml "${op_rb}"
grep -Fqx 'kind: RoleBinding' "${op_rb}"
grep -Fqx '  name: experiment-operator' "${op_rb}"
grep -Fqx '  kind: Role' "${op_rb}"
grep -Fqx '  name: experiment-operator' "${op_rb}"
grep -Fqx '    name: experiment-operator' "${op_rb}"
grep -Fqx '    namespace: cbse-system' "${op_rb}"

op_dep="${tmp}/op-deployment.yaml"
render_one operator/deployment.yaml "${op_dep}"
grep -Fqx 'kind: Deployment' "${op_dep}"
grep -Fqx '  name: experiment-operator' "${op_dep}"
grep -Fqx '  replicas: 1' "${op_dep}"
# Stable selector: the app label plus the H1 selector labels, nothing else.
cbse_helm_assert_eq "operator selector labels" \
  "$(selector_labels "${op_dep}" | tr -d ' \t')" \
  "$(printf 'app:experiment-operator\napp.kubernetes.io/name:cbse\napp.kubernetes.io/instance:cbse')"
# Pod template labels carry the full H1 label set.
for line in \
  '        app: experiment-operator' \
  '        app.kubernetes.io/name: cbse' \
  '        app.kubernetes.io/instance: cbse' \
  '        app.kubernetes.io/version: 0.1.0' \
  '        app.kubernetes.io/managed-by: Helm' \
  '        app.kubernetes.io/part-of: cbse'; do
  grep -Fqx "${line}" "${op_dep}" || cbse_helm_fail "operator pod template labels missing: ${line}"
done
grep -Fqx '      serviceAccountName: experiment-operator' "${op_dep}"
grep -Fqx "          image: ${operator_image}" "${op_dep}"
grep -Fqx '          imagePullPolicy: IfNotPresent' "${op_dep}"
grep -Fqx '            - --health-probe-bind-address=:8081' "${op_dep}"
# Watch namespace from the Pod namespace via the downward API.
grep -Fqx '            - name: EXPERIMENT_OPERATOR_WATCH_NAMESPACE' "${op_dep}"
grep -Fqx '                  fieldPath: metadata.namespace' "${op_dep}"
# Named health port and HTTP probes with the proven timings.
grep -Fqx '            - name: health' "${op_dep}"
grep -Fqx '              containerPort: 8081' "${op_dep}"
grep -Fqx '              path: /readyz' "${op_dep}"
grep -Fqx '              port: health' "${op_dep}"
grep -Fqx '            initialDelaySeconds: 2' "${op_dep}"
grep -Fqx '            periodSeconds: 3' "${op_dep}"
grep -Fqx '              path: /healthz' "${op_dep}"
grep -Fqx '            initialDelaySeconds: 10' "${op_dep}"
grep -Fqx '            periodSeconds: 10' "${op_dep}"
# Probe shape is httpGet (exec and tcp probes must not appear).
cbse_helm_assert_line_count "operator httpGet probes" \
  "$(grep -cF 'httpGet:' "${op_dep}")" 2
# Resource values from values.
grep -Fqx '              cpu: 10m' "${op_dep}"
grep -Fqx '              memory: 64Mi' "${op_dep}"
grep -Fqx '              cpu: 500m' "${op_dep}"
grep -Fqx '              memory: 192Mi' "${op_dep}"
# Restricted security profile.
grep -Fqx '        runAsNonRoot: true' "${op_dep}"
grep -Fqx '          type: RuntimeDefault' "${op_dep}"
grep -Fqx '            allowPrivilegeEscalation: false' "${op_dep}"
grep -Fqx '              drop: ["ALL"]' "${op_dep}"
grep -Fqx '            readOnlyRootFilesystem: true' "${op_dep}"
# No leader election and no metrics/webhook/Service objects in the operator
# workload; the Deployment args carry only the probe bind address.
cbse_helm_assert_line_count "operator args (probe bind address only)" \
  "$(grep -c '^            - --' "${op_dep}")" 1

# --- 2. Scenario Manager resources ---------------------------------------------
sm_sa="${tmp}/sm-sa.yaml"
render_one scenario-manager/serviceaccount.yaml "${sm_sa}"
grep -Fqx 'kind: ServiceAccount' "${sm_sa}"
grep -Fqx '  name: scenario-manager' "${sm_sa}"
cbse_helm_assert_line_count "scenario-manager ServiceAccount pull Secret entries" \
  "$(grep -cF '  - name: cbse-registry-auth' "${sm_sa}")" 1
grep -Fqx 'imagePullSecrets:' "${sm_sa}"

sm_role="${tmp}/sm-role.yaml"
render_one scenario-manager/role.yaml "${sm_role}"
grep -Fqx 'kind: Role' "${sm_role}"
grep -Fqx '  name: scenario-manager' "${sm_role}"
cbse_helm_assert_eq "scenario-manager Role rules" "$(rules_block "${sm_role}")" "$(cat <<'EOF'
rules:
  - apiGroups: ["experiment.cbse.terministic.de"]
    resources: ["simulationexperiments"]
    verbs: ["get", "list", "watch", "patch"]
  - apiGroups: ["experiment.cbse.terministic.de"]
    resources: ["simulationexperiments/status"]
    verbs: ["patch"]
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["create", "delete", "get", "list", "watch"]
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: ["cbse-registry-auth"]
    verbs: ["get"]
  - apiGroups: [""]
    resources: ["serviceaccounts"]
    verbs: ["get"]
EOF
)"

sm_rb="${tmp}/sm-rolebinding.yaml"
render_one scenario-manager/rolebinding.yaml "${sm_rb}"
grep -Fqx 'kind: RoleBinding' "${sm_rb}"
grep -Fqx '  name: scenario-manager' "${sm_rb}"
grep -Fqx '  kind: Role' "${sm_rb}"
grep -Fqx '    name: scenario-manager' "${sm_rb}"
grep -Fqx '    namespace: cbse-system' "${sm_rb}"

sm_cm="${tmp}/sm-configmap.yaml"
render_one scenario-manager/configmap.yaml "${sm_cm}"
grep -Fqx 'kind: ConfigMap' "${sm_cm}"
grep -Fqx '  name: scenario-manager-config' "${sm_cm}"
grep -Fqx '  SCENARIO_MANAGER_CORE_DB_DSN: postgres://core-db:5432/scenarios?sslmode=disable' "${sm_cm}"
grep -Fqx '  SCENARIO_MANAGER_CORE_DB_SCENARIO_STATUS_TABLE: scenario_status' "${sm_cm}"
grep -Fqx '  SCENARIO_MANAGER_CORE_DB_PROJECT_TABLE: project' "${sm_cm}"
grep -Fqx '  SCENARIO_MANAGER_NATS_URL: nats://sm-eds-nats:4222' "${sm_cm}"
# Authentication-free NATS: legacy credentials explicitly empty.
grep -Fqx '  SCENARIO_MANAGER_NATS_USER: ""' "${sm_cm}"
grep -Fqx '  SCENARIO_MANAGER_NATS_PASSWORD: ""' "${sm_cm}"
grep -Fqx '  SCENARIO_MANAGER_RUNNER_START_WORKERS: "4"' "${sm_cm}"
grep -Fqx '  SCENARIO_MANAGER_HEALTH_ADDRESS: ":8080"' "${sm_cm}"
cbse_helm_assert_line_count "scenario-manager ConfigMap entries" \
  "$(grep -c '^  SCENARIO_MANAGER_' "${sm_cm}")" 8

sm_dep="${tmp}/sm-deployment.yaml"
render_one scenario-manager/deployment.yaml "${sm_dep}"
grep -Fqx 'kind: Deployment' "${sm_dep}"
grep -Fqx '  name: scenario-manager' "${sm_dep}"
grep -Fqx '  replicas: 1' "${sm_dep}"
cbse_helm_assert_eq "scenario-manager selector labels" \
  "$(selector_labels "${sm_dep}" | tr -d ' \t')" \
  "$(printf 'app:scenario-manager\napp.kubernetes.io/name:cbse\napp.kubernetes.io/instance:cbse')"
for line in \
  '        app: scenario-manager' \
  '        app.kubernetes.io/name: cbse' \
  '        app.kubernetes.io/instance: cbse' \
  '        app.kubernetes.io/version: 0.1.0' \
  '        app.kubernetes.io/managed-by: Helm' \
  '        app.kubernetes.io/part-of: cbse'; do
  grep -Fqx "${line}" "${sm_dep}" || cbse_helm_fail "scenario-manager pod template labels missing: ${line}"
done
grep -Fqx '      serviceAccountName: scenario-manager' "${sm_dep}"
grep -Fqx "          image: ${sm_image}" "${sm_dep}"
grep -Fqx '          imagePullPolicy: IfNotPresent' "${sm_dep}"
# Secret + ConfigMap env injection: the ConfigMap data and the Helm-managed
# Core DB credentials Secret, nothing else.
cbse_helm_assert_eq "scenario-manager envFrom entries" \
  "$(awk '
     /^          envFrom:$/ { f = 1; next }
     f && /^          [a-z]/ { f = 0 }
     f && /^            - / { n++ }
     END { print n + 0 }
   ' "${sm_dep}")" 2
grep -Fqx '                name: scenario-manager-config' "${sm_dep}"
grep -Fqx '                name: core-db-credentials' "${sm_dep}"
# Watch namespace from the Pod namespace via the downward API.
grep -Fqx '            - name: SCENARIO_MANAGER_WATCH_NAMESPACE' "${sm_dep}"
grep -Fqx '                  fieldPath: metadata.namespace' "${sm_dep}"
# Named health port and HTTP probes on the values port.
grep -Fqx '            - name: health' "${sm_dep}"
grep -Fqx '              containerPort: 8080' "${sm_dep}"
grep -Fqx '              path: /readyz' "${sm_dep}"
grep -Fqx '              port: health' "${sm_dep}"
grep -Fqx '              path: /healthz' "${sm_dep}"
grep -Fqx '            initialDelaySeconds: 2' "${sm_dep}"
grep -Fqx '            periodSeconds: 3' "${sm_dep}"
grep -Fqx '            initialDelaySeconds: 10' "${sm_dep}"
grep -Fqx '            periodSeconds: 10' "${sm_dep}"
cbse_helm_assert_line_count "scenario-manager httpGet probes" \
  "$(grep -cF 'httpGet:' "${sm_dep}")" 2
# Resource values from values.
grep -Fqx '              cpu: 10m' "${sm_dep}"
grep -Fqx '              memory: 48Mi' "${sm_dep}"
grep -Fqx '              cpu: 500m' "${sm_dep}"
grep -Fqx '              memory: 192Mi' "${sm_dep}"
# Restricted security profile with the numeric UID/GID 1000 runtime user; the
# smoke stack root exception must not enter the chart.
grep -Fqx '        runAsNonRoot: true' "${sm_dep}"
grep -Fqx '        runAsUser: 1000' "${sm_dep}"
grep -Fqx '        runAsGroup: 1000' "${sm_dep}"
grep -Fqx '          type: RuntimeDefault' "${sm_dep}"
grep -Fqx '            allowPrivilegeEscalation: false' "${sm_dep}"
grep -Fqx '              drop: ["ALL"]' "${sm_dep}"
grep -Fqx '            readOnlyRootFilesystem: true' "${sm_dep}"

# --- 3. Control-plane-wide absence invariants ----------------------------------
# Render every template document (no CRDs: the CRD schema text is not a
# control-plane object).
full_render="${tmp}/full.yaml"
"${CBSE_HELM_BIN}" template cbse "${CBSE_HELM_CHART}" \
  --namespace cbse-system -f "${CBSE_HELM_VALUES}" >"${full_render}"

# F-R1 scoped consolidation: the H3 data plane now renders alongside the
# H2 control plane in the same chart. h2_docs selects exactly the H2-owned
# documents (the fixed names experiment-operator, scenario-manager, and
# scenario-manager-config) from the full render; the H2-owned counts below
# stay exact and strict, so an H3 document can never mask a changed H2
# count. The focused per-template inspections above remain the H2 proof of
# the operator/SM resources.
h2_docs() {
  awk '
    function flush() { if (keep) printf "%s", buf; buf = ""; keep = 0 }
    /^---$/ { flush(); next }
    {
      buf = buf $0 "\n"
      if ($0 ~ /^  name: (experiment-operator|scenario-manager|scenario-manager-config)$/) keep = 1
    }
    END { flush() }
  ' "$1"
}

cbse_helm_assert_eq "rendered H2 control-plane document count" \
  "$(h2_docs "${full_render}" | grep -c '^kind: ' || true)" 9
cbse_helm_assert_line_count "no ClusterRole in the rendered control plane" \
  "$(grep -c '^kind: ClusterRole' "${full_render}" || true)" 0
cbse_helm_assert_line_count "no ClusterRoleBinding in the rendered control plane" \
  "$(grep -c '^kind: ClusterRoleBinding' "${full_render}" || true)" 0
# No root UID anywhere in the control plane (the SM root exception is gone).
cbse_helm_assert_line_count "no root runAsUser in the rendered control plane" \
  "$(grep -c 'runAsUser: 0$' "${full_render}" || true)" 0
cbse_helm_assert_line_count "no root runAsGroup in the rendered control plane" \
  "$(grep -c 'runAsGroup: 0$' "${full_render}" || true)" 0
# Both workloads render exactly one digest-pinned image reference.
cbse_helm_assert_line_count "operator image rendered once" \
  "$(grep -cF "image: ${operator_image}" "${full_render}")" 1
cbse_helm_assert_line_count "scenario-manager image rendered once" \
  "$(grep -cF "image: ${sm_image}" "${full_render}")" 1
# No tag-based image reference anywhere in the rendered control plane.
if grep -E '^[[:space:]]*image:[[:space:]]' "${full_render}" |
  grep -Ev '^[[:space:]]*image: [^ ]+@sha256:[a-f0-9]{64}$' >/dev/null; then
  cbse_helm_fail "rendered control plane contains a tag-based image reference"
fi
# The registry pull Secret reference appears exactly once per H2
# ServiceAccount (twice in the H2-owned documents) and exactly one RBAC
# resourceNames constraint binds the SM to the named Secret within the H2
# control plane (scoped like F-R1: the H4 hook Role carries its own
# named-Secret get rule, asserted by assert-lifecycle.sh). The
# Helm-managed Core DB credentials Secret is referenced exactly once by
# the scenario-manager envFrom; the counts are scoped to the H2-owned
# documents (F-R1) so H3/H4 additions cannot change them.
cbse_helm_assert_line_count "cbse-registry-auth pull Secret references in the H2 control plane" \
  "$(h2_docs "${full_render}" | grep -cF '  - name: cbse-registry-auth' || true)" 2
cbse_helm_assert_line_count "cbse-registry-auth named-Secret RBAC constraint in the H2 control plane" \
  "$(h2_docs "${full_render}" | grep -cF 'resourceNames: ["cbse-registry-auth"]' || true)" 1
cbse_helm_assert_line_count "core-db-credentials Secret references in the H2 control plane" \
  "$(h2_docs "${full_render}" | grep -cF '                name: core-db-credentials' || true)" 1

echo "assert-control-plane: all H2 control-plane contract assertions passed."
