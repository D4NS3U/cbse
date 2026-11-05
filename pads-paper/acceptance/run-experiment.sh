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

# run-experiment.sh — PADS paper end-to-end acceptance driver (P5-R3).
#
# Drives ONE complete live experiment on the dedicated test cluster with the
# paper-local Translator: preflight; create and label its own
# cbse-pads-<suffix> namespace; copy the cbse-registry-auth pull Secret from
# cbse-test-system (read-only) and patch the default ServiceAccount; render
# test/e2e/manifests/base via kustomize and test/e2e/manifests/experiment.yaml
# and apply them; wait for the support stack, the Scenario Manager ready log
# line, and the experiment to reach InProgress; drive the EDS JSON API over a
# kubectl port-forward (entries, recipes, scenarios, save, start, state); poll
# the Core DB until the scenario reaches the terminal Finished verdict state
# (40m timeout); export the Result DB rows, the Core DB scenario record, the
# experiment CR status, and the EDS final state; run assert-results.py and
# replay-seeds.py (SimPy 4.1.2 required); write sanitized evidence under
# pads-paper/acceptance/evidence/<run-id>/ (P5-R4) and raw exports to the
# untracked artifact dir artifacts/pads/<run-id>/; and tear down via an EXIT
# trap.
#
# The driver NEVER mutates the cluster while its environment is incomplete:
# every required environment variable, the kube context, and the image
# digest-pinning are validated before any cluster mutation (the first
# mutation is the namespace creation). It never touches cbse-test-system,
# the shared CRD, or any resource it did not create.
#
# Inputs / environment (all required unless defaulted):
#   KUBECONFIG            dedicated test-cluster kubeconfig (must be readable).
#   CBSE_EXPECTED_CONTEXT required current kube context; a mismatch refuses.
#   KUBECTL               (default: kubectl) kubectl binary on PATH or path.
#   CBSE_REGISTRY         registry prefix (no default; environment-provided).
#   OPERATOR_IMAGE        digest-pinned image for the support stack operator.
#   SM_IMAGE              digest-pinned image for the Scenario Manager.
#   EDS_REFERENCE_IMAGE   digest-pinned reference EDS image.
#   PPS_IMAGE             digest-pinned PostProcessingService image.
#   DETAIL_DB_IMAGE       digest-pinned scenario Detail Database image.
#   PADS_TRANS_IMAGE      digest-pinned paper-local Translator image.
#   RUNNER_BASE_IMAGE     digest-pinned runner base image.
#   CBSE_RUNNER_REPOSITORY (default ${CBSE_REGISTRY}/cbse-test-runner)
#   CBSE_PULL_SECRET_NAME (default cbse-registry-auth),
#   CBSE_PULL_SECRET_NAMESPACE (default cbse-test-system).
#   PADS_SMOKE_RECEIPT        the unchanged 15-spec smoke receipt (run id and
#              15/15 result) from the same settlement, recorded verbatim in
#              run.md (P5-R4).
#   PADS_PYTHON           (default python3) interpreter with SimPy 4.1.2.
#   CBSE_KEEP_ON_FAILURE=1 skip teardown for active debugging.
#   PADS_EDS_LOCAL_PORT   (default 18082) local port for the EDS port-forward.
#
# Exit codes:
#   0  preflight, deploy, EDS design, export, assertions, and replay passed.
#   1  a run step failed (deploy/rollout/readiness, EDS API, Finished
#      polling, export, assertion, or replay).
#   2  preflight refusal (incomplete environment, wrong context, bad digest).
#   Other non-zero propagated from set -e.
#
# Side effects:
#   Creates the namespace cbse-pads-<suffix> and applies the support stack
#   and experiment into it; port-forwards the EDS service; executes psql in
#   the run's own database pods for the export. The EXIT trap deletes the
#   SimulationExperiment CRs (waiting for the Scenario Manager finalizer) and
#   then the namespace unless retention is requested. Writes raw exports and
#   diagnostics under artifacts/pads/<run-id> and sanitized evidence under
#   pads-paper/acceptance/evidence/<run-id>.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
acceptance_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- Preflight (before ANY cluster mutation) --------------------------------

require_env() {
  local name="$1"
  local value="${!name:-}"
  [[ -n "${value}" ]] || { echo "${name} is required" >&2; exit 2; }
}
require_env KUBECONFIG
require_env CBSE_EXPECTED_CONTEXT
require_env CBSE_REGISTRY
require_env OPERATOR_IMAGE
require_env SM_IMAGE
require_env EDS_REFERENCE_IMAGE
require_env PPS_IMAGE
require_env DETAIL_DB_IMAGE
require_env PADS_TRANS_IMAGE
require_env RUNNER_BASE_IMAGE
require_env PADS_SMOKE_RECEIPT
kubeconfig="${KUBECONFIG:?KUBECONFIG is required}"
[[ -r "${kubeconfig}" ]] || { echo "Kubeconfig is not readable: ${kubeconfig}" >&2; exit 2; }

kubectl_bin="${KUBECTL:-kubectl}"
if [[ "${kubectl_bin}" == */* ]]; then
  [[ -x "${kubectl_bin}" ]] || { echo "KUBECTL is not executable: ${kubectl_bin}" >&2; exit 2; }
else
  command -v "${kubectl_bin}" >/dev/null || { echo "KUBECTL not found on PATH: ${kubectl_bin}" >&2; exit 2; }
fi

command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
command -v curl >/dev/null || { echo "curl is required" >&2; exit 2; }
command -v openssl >/dev/null || { echo "openssl is required" >&2; exit 2; }
command -v git >/dev/null || { echo "git is required (branch commit provenance for run.md)" >&2; exit 2; }
branch_commit="$(git -C "${root}" rev-parse HEAD 2>/dev/null)" || {
  echo "The repository root is not a git checkout; run.md requires the branch commit" >&2
  exit 2
}

python_bin="${PADS_PYTHON:-python3}"
command -v "${python_bin}" >/dev/null || { echo "Python interpreter ${python_bin} is required" >&2; exit 2; }
# The live replay contract requires SimPy 4.1.2; validate before any
# cluster mutation.
simpy_version="$("${python_bin}" - <<'PY'
import importlib.util
import importlib.metadata as metadata

if importlib.util.find_spec("simpy") is None:
    print("missing")
else:
    try:
        print(metadata.version("simpy"))
    except Exception:
        import simpy
        print(getattr(simpy, "__version__", "unknown"))
PY
)"
[[ "${simpy_version}" == "4.1.2" ]] || {
  echo "SimPy 4.1.2 is required for the live replay; found ${simpy_version}" >&2
  exit 2
}

# Every image reference must be an immutable digest (no tag-only references).
for image_var in OPERATOR_IMAGE SM_IMAGE EDS_REFERENCE_IMAGE PPS_IMAGE DETAIL_DB_IMAGE PADS_TRANS_IMAGE RUNNER_BASE_IMAGE; do
  image_value="${!image_var}"
  [[ "${image_value}" =~ ^[A-Za-z0-9._/-]+@sha256:[0-9a-f]{64}$ ]] || {
    echo "${image_var} must be an immutable digest reference (<repo>@sha256:<64-hex>), got: ${image_value}" >&2
    exit 2
  }
done

runner_repository="${CBSE_RUNNER_REPOSITORY:-${CBSE_REGISTRY}/cbse-test-runner}"
pull_secret_name="${CBSE_PULL_SECRET_NAME:-cbse-registry-auth}"
pull_secret_namespace="${CBSE_PULL_SECRET_NAMESPACE:-cbse-test-system}"

# Locked source images (no environment override) for the experiment render.
# shellcheck disable=SC1091
source "${root}/test/harness/image-lock.sh"
load_image_lock "${root}/test/e2e/images.lock.env"

# The current kube context must equal the expected one (refuse otherwise).
current_context="$("${kubectl_bin}" --kubeconfig "${kubeconfig}" config current-context 2>/dev/null)" || {
  echo "Unable to read the current kube context from ${kubeconfig}" >&2
  exit 2
}
[[ "${current_context}" == "${CBSE_EXPECTED_CONTEXT}" ]] || {
  echo "Refusing cluster mutation: context ${current_context} does not match ${CBSE_EXPECTED_CONTEXT}" >&2
  exit 2
}

# The pull Secret must exist with the right type (read-only inspection).
secret_type="$("${kubectl_bin}" --kubeconfig "${kubeconfig}" get secret "${pull_secret_name}" \
  -n "${pull_secret_namespace}" -o jsonpath='{.type}' 2>/dev/null || true)"
[[ "${secret_type}" == "kubernetes.io/dockerconfigjson" ]] || {
  echo "Required pull Secret ${pull_secret_namespace}/${pull_secret_name} is missing or has the wrong type" >&2
  exit 2
}

# --- Run identity and artifact layout ----------------------------------------
run_id="pads-$(date -u +%Y%m%d%H%M%S)-$(openssl rand -hex 3)"
[[ "${run_id}" =~ ^pads-[0-9]{14}-[0-9a-f]{6}$ ]]
suffix="${run_id#pads-}"
namespace="cbse-pads-${suffix}"
project="${run_id}"
artifact_dir="${root}/artifacts/pads/${run_id}"
evidence_dir="${acceptance_dir}/evidence/${run_id}"
mkdir -p "${artifact_dir}"
tmp="$(mktemp -d)"

# --- Teardown trap ------------------------------------------------------------
namespace_created=0
port_forward_pid=""
finish() {
  status=$?
  set +e
  if [[ -n "${port_forward_pid}" ]]; then
    kill "${port_forward_pid}" >/dev/null 2>&1 || true
    wait "${port_forward_pid}" >/dev/null 2>&1 || true
  fi
  if (( namespace_created == 1 )); then
    if [[ "${CBSE_KEEP_ON_FAILURE:-0}" == "1" ]]; then
      echo "CBSE_KEEP_ON_FAILURE=1: skipped teardown for active debugging (namespace ${namespace}, run ${run_id})" >&2
    else
      # Delete the experiment CRs first and wait for their Scenario-Manager
      # cleanup finalizer to clear while the Scenario Manager is still alive;
      # the namespace deletion below cascades the SM Deployment and the CRs
      # together, and a dead SM cannot release its own finalizer.
      "${kubectl_bin}" --kubeconfig "${kubeconfig}" delete simulationexperiment --all -n "${namespace}" --wait=true --timeout=120s >/dev/null 2>&1
      "${kubectl_bin}" --kubeconfig "${kubeconfig}" delete namespace "${namespace}" --wait=true --timeout=180s >/dev/null 2>&1
    fi
  fi
  rm -rf "${tmp}" >/dev/null 2>&1 || true
  exit "${status}"
}
trap finish EXIT
trap 'exit 130' INT TERM

started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
{
  printf 'run_id=%s\n' "${run_id}"
  printf 'namespace=%s\n' "${namespace}"
  printf 'project=%s\n' "${project}"
  printf 'context=%s\n' "${current_context}"
  printf 'started_at=%s\n' "${started_at}"
  printf 'runner_repository=%s\n' "${runner_repository}"
} >"${artifact_dir}/preflight.txt"

# --- Deployment ---------------------------------------------------------------
# The first cluster mutation: create and label our own namespace.
"${kubectl_bin}" --kubeconfig "${kubeconfig}" create namespace "${namespace}" >/dev/null
namespace_created=1
"${kubectl_bin}" --kubeconfig "${kubeconfig}" label namespace "${namespace}" \
  "cbse.terministic.de/managed-run=${run_id}" \
  app.kubernetes.io/part-of=cbse-pads \
  pod-security.kubernetes.io/enforce=privileged \
  pod-security.kubernetes.io/audit=restricted \
  pod-security.kubernetes.io/warn=restricted --overwrite >/dev/null

core_db_password="$(openssl rand -hex 24)"
experiment_db_password="$(openssl rand -hex 24)"

# Read-only copy of the existing pull Secret into our namespace, exactly as
# the smoke does, plus the default-ServiceAccount patch so runner and builder
# pods can pull.
"${kubectl_bin}" --kubeconfig "${kubeconfig}" create secret generic core-db-credentials -n "${namespace}" \
  --from-literal=SCENARIO_MANAGER_CORE_DB_USER=cbse_test \
  --from-literal="SCENARIO_MANAGER_CORE_DB_PASSWORD=${core_db_password}" \
  --dry-run=client -o yaml | "${kubectl_bin}" --kubeconfig "${kubeconfig}" apply -f - >/dev/null
"${kubectl_bin}" --kubeconfig "${kubeconfig}" get secret "${pull_secret_name}" -n "${pull_secret_namespace}" -o json \
  | jq 'del(.metadata.namespace, .metadata.resourceVersion, .metadata.uid, .metadata.creationTimestamp, .metadata.managedFields, .metadata.ownerReferences)' \
  | "${kubectl_bin}" --kubeconfig "${kubeconfig}" apply -n "${namespace}" -f - >/dev/null
"${kubectl_bin}" --kubeconfig "${kubeconfig}" patch serviceaccount default -n "${namespace}" \
  --type=merge -p "{\"imagePullSecrets\":[{\"name\":\"${pull_secret_name}\"}]}" >/dev/null

# Render the support stack with the same substitutions the smoke uses and
# wait for every Deployment to roll out plus the Scenario Manager ready log.
"${kubectl_bin}" kustomize "${root}/test/e2e/manifests/base" >"${tmp}/stack.raw.yaml"
sed \
  -e "s|CBSE_NAMESPACE|${namespace}|g" \
  -e "s|CBSE_RUN_ID|${run_id}|g" \
  -e "s|CBSE_OPERATOR_IMAGE|${OPERATOR_IMAGE}|g" \
  -e "s|CBSE_SM_IMAGE|${SM_IMAGE}|g" \
  -e "s|CBSE_PROJECT|${project}|g" \
  "${tmp}/stack.raw.yaml" >"${artifact_dir}/stack.yaml"
"${kubectl_bin}" --kubeconfig "${kubeconfig}" apply -n "${namespace}" -f "${artifact_dir}/stack.yaml" >/dev/null

for deployment in core-db sm-eds-nats experiment-operator scenario-manager; do
  "${kubectl_bin}" --kubeconfig "${kubeconfig}" rollout status deployment/"${deployment}" -n "${namespace}" --timeout=240s
done

scenario_manager_ready_deadline=$((SECONDS + 120))
until "${kubectl_bin}" --kubeconfig "${kubeconfig}" logs deployment/scenario-manager -n "${namespace}" --all-containers 2>/dev/null \
  | grep -q 'Scenario Manager is ready'; do
  if (( SECONDS >= scenario_manager_ready_deadline )); then
    echo "Scenario Manager did not report application readiness within 120 seconds" >&2
    exit 1
  fi
  sleep 2
done

# Render the experiment with the paper-local Translator and the locked
# Postgres image, the runner repository, and a generated database password.
sed \
  -e "s|CBSE_NAMESPACE|${namespace}|g" \
  -e "s|CBSE_PROJECT|${project}|g" \
  -e "s|CBSE_RUN_ID|${run_id}|g" \
  -e "s|CBSE_SUPPORT_IMAGE|${EDS_REFERENCE_IMAGE}|g" \
  -e "s|CBSE_EDS_REFERENCE_IMAGE|${EDS_REFERENCE_IMAGE}|g" \
  -e "s|CBSE_PPS_IMAGE|${PPS_IMAGE}|g" \
  -e "s|CBSE_TRANS_IMAGE|${PADS_TRANS_IMAGE}|g" \
  -e "s|CBSE_BUILDER_IMAGE|${BUILDER_IMAGE}|g" \
  -e "s|CBSE_RUNNER_BASE_IMAGE|${RUNNER_BASE_IMAGE}|g" \
  -e "s|CBSE_RUNNER_REPOSITORY|${runner_repository}|g" \
  -e "s|CBSE_DETAIL_DB_IMAGE|${DETAIL_DB_IMAGE}|g" \
  -e "s|CBSE_POSTGRES_IMAGE|${POSTGRES_IMAGE}|g" \
  -e "s|password: smoke|password: ${experiment_db_password}|g" \
  "${root}/test/e2e/manifests/experiment.yaml" >"${artifact_dir}/experiment.yaml"
"${kubectl_bin}" --kubeconfig "${kubeconfig}" apply -f "${artifact_dir}/experiment.yaml" >/dev/null

# Wait for the experiment to reach InProgress.
in_progress_deadline=$((SECONDS + 300))
phase=""
until [[ "${phase}" == "InProgress" ]]; do
  phase="$("${kubectl_bin}" --kubeconfig "${kubeconfig}" get simulationexperiment "${project}" -n "${namespace}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  if [[ "${phase}" == "Error" || "${phase}" == "Failed" ]]; then
    message="$("${kubectl_bin}" --kubeconfig "${kubeconfig}" get simulationexperiment "${project}" -n "${namespace}" -o jsonpath='{.status.message}' 2>/dev/null || true)"
    echo "Experiment ${project} entered ${phase}: ${message}" >&2
    exit 1
  fi
  if (( SECONDS >= in_progress_deadline )); then
    echo "Experiment did not reach InProgress within 300 seconds (last phase: ${phase:-none})" >&2
    exit 1
  fi
  sleep 5
done

# --- EDS-driven design ---------------------------------------------------------
# Ruled design values (P5-R3): one design, one recipe, one scenario.
design_arrival_rate=4
design_service_rate=4
design_run_duration=500
design_seed_policy=2027
scenario_priority=5
scenario_number_of_reps=5
scenario_confidence_metric=0.05

"${kubectl_bin}" --kubeconfig "${kubeconfig}" rollout status deployment/"${project}-eds" -n "${namespace}" --timeout=240s

eds_local_port="${PADS_EDS_LOCAL_PORT:-18082}"
eds_port=8082
"${kubectl_bin}" --kubeconfig "${kubeconfig}" port-forward -n "${namespace}" "svc/${project}-eds-svc" "${eds_local_port}:${eds_port}" \
  >"${artifact_dir}/eds-port-forward.log" 2>&1 &
port_forward_pid=$!
eds_deadline=$((SECONDS + 90))
until (echo > "/dev/tcp/127.0.0.1/${eds_local_port}") 2>/dev/null; do
  if (( SECONDS >= eds_deadline )); then
    echo "EDS port-forward did not accept connections within 90 seconds" >&2
    exit 1
  fi
  sleep 1
done
eds_base="http://127.0.0.1:${eds_local_port}"

# eds_do METHOD PATH [PAYLOAD] sets EDS_CODE and EDS_BODY (the EDS JSON API,
# driven exactly as implemented in the reference server).
eds_do() {
  local method="$1" path="$2" payload="${3:-}"
  local -a args=(--silent --show-error --request "${method}" --write-out $'\n%{http_code}')
  if [[ -n "${payload}" ]]; then
    args+=(--header 'Content-Type: application/json' --data "${payload}")
  fi
  local out
  if ! out="$(curl "${args[@]}" "${eds_base}${path}" 2>>"${artifact_dir}/eds-errors.log")"; then
    echo "EDS API transport failure: ${method} ${path}" >&2
    exit 1
  fi
  EDS_CODE="${out##*$'\n'}"
  EDS_BODY="${out%$'\n'*}"
}
eds_expect_ok() {
  local name="$1"
  [[ "${EDS_CODE}" == "200" ]] || { echo "EDS ${name} refused (HTTP ${EDS_CODE}): ${EDS_BODY}" >&2; exit 1; }
}

eds_do POST /api/entries "{\"parameter\":\"arrival_rate\",\"value\":${design_arrival_rate}}"
eds_expect_ok "POST /api/entries (arrival_rate=${design_arrival_rate})"
arrival_rate_id="$(printf '%s' "${EDS_BODY}" | jq -r '.id')"

eds_do POST /api/entries "{\"parameter\":\"service_rate\",\"value\":${design_service_rate}}"
eds_expect_ok "POST /api/entries (service_rate=${design_service_rate})"
service_rate_id="$(printf '%s' "${EDS_BODY}" | jq -r '.id')"

eds_do POST /api/entries "{\"parameter\":\"run_duration\",\"value\":${design_run_duration}}"
eds_expect_ok "POST /api/entries (run_duration=${design_run_duration})"
run_duration_id="$(printf '%s' "${EDS_BODY}" | jq -r '.id')"

eds_do POST /api/entries "{\"parameter\":\"seed_policy\",\"value\":${design_seed_policy}}"
eds_expect_ok "POST /api/entries (seed_policy=${design_seed_policy})"
seed_policy_id="$(printf '%s' "${EDS_BODY}" | jq -r '.id')"

eds_do POST /api/recipes "{\"arrival_rate_id\":${arrival_rate_id},\"service_rate_id\":${service_rate_id},\"run_duration_id\":${run_duration_id},\"seed_policy_id\":${seed_policy_id}}"
eds_expect_ok "POST /api/recipes"
parameterset_id="$(printf '%s' "${EDS_BODY}" | jq -r '.parameterset_id')"

eds_do POST /api/scenarios "{\"priority\":${scenario_priority},\"number_of_reps\":${scenario_number_of_reps},\"confidence_metric\":${scenario_confidence_metric},\"parameterset_id\":${parameterset_id}}"
eds_expect_ok "POST /api/scenarios (priority=${scenario_priority})"

eds_do POST /api/save
eds_expect_ok "POST /api/save"

eds_do POST /api/start
eds_expect_ok "POST /api/start"
published="$(printf '%s' "${EDS_BODY}" | jq -r '.published')"
[[ "${published}" == "1" ]] || { echo "EDS start did not publish exactly one scenario (published=${published})" >&2; exit 1; }

eds_do GET /api/state
eds_expect_ok "GET /api/state (post-start)"
printf '%s\n' "${EDS_BODY}" >"${artifact_dir}/eds-state-post-start.json"

# Record the design values for the assertion context.
jq -n \
  --argjson ps "${parameterset_id}" \
  --argjson ar "${design_arrival_rate}" \
  --argjson sr "${design_service_rate}" \
  --argjson rd "${design_run_duration}" \
  --argjson sp "${design_seed_policy}" \
  --argjson nr "${scenario_number_of_reps}" \
  --argjson cm "${scenario_confidence_metric}" \
  '{parameterset_id:$ps, arrival_rate:$ar, service_rate:$sr, run_duration:$rd, seed_policy:$sp, number_of_reps:$nr, confidence_metric:$cm}' \
  >"${artifact_dir}/design-values.json"

# --- Completion and export ------------------------------------------------------
# Poll the Core DB until the scenario reaches the terminal Finished verdict
# state (40m timeout). A Failed state fails the run.
psql_core() {
  local sql="$1" pod
  pod="$("${kubectl_bin}" --kubeconfig "${kubeconfig}" get pod -n "${namespace}" -l app=core-db -o jsonpath='{.items[0].metadata.name}')"
  "${kubectl_bin}" --kubeconfig "${kubeconfig}" exec -n "${namespace}" "${pod}" -- \
    env PGPASSWORD="${core_db_password}" psql -U cbse_test -d scenarios -t -A -v ON_ERROR_STOP=1 -c "${sql}"
}
scenario_sql="SELECT s.state FROM scenario_status s JOIN project p ON p.id = s.project_id WHERE p.project_namespace = '${namespace}' AND p.project_name = '${project}'"
finished_deadline=$((SECONDS + 2400))
state=""
until [[ "${state}" == "Finished" ]]; do
  state="$(psql_core "${scenario_sql}" || true)"
  if [[ "${state}" == "Failed" ]]; then
    echo "Scenario reached the Failed state (not Finished); the run fails" >&2
    exit 1
  fi
  if (( SECONDS >= finished_deadline )); then
    echo "Scenario did not reach the terminal Finished state within 40 minutes (last state: ${state:-none})" >&2
    exit 1
  fi
  sleep 15
done

# Export: Core DB scenario record (all 20 columns, computed-replication and
# evaluation fields as the schema defines them).
scenario_export_sql="SELECT json_agg(row_to_json(s) ORDER BY s.id) FROM scenario_status s JOIN project p ON p.id = s.project_id WHERE p.project_namespace = '${namespace}' AND p.project_name = '${project}'"
scenario_rows="$(psql_core "${scenario_export_sql}")"
printf '%s\n' "${scenario_rows}" >"${artifact_dir}/scenario-status.json"
scenario_count="$(printf '%s' "${scenario_rows}" | jq 'length')"
[[ "${scenario_count}" == "1" ]] || { echo "Expected exactly one Core DB scenario row, found ${scenario_count}" >&2; exit 1; }
scenario_id="$(printf '%s' "${scenario_rows}" | jq -r '.[0].id')"
container_image="$(printf '%s' "${scenario_rows}" | jq -r '.[0].container_image')"
number_of_computed_reps="$(printf '%s' "${scenario_rows}" | jq -r '.[0].number_of_computed_reps')"
runner_round="$(printf '%s' "${scenario_rows}" | jq -r '.[0].runner_round')"
[[ -n "${container_image}" && "${container_image}" != "null" ]] || {
  echo "The chain reported no generated runner image (scenario_status.container_image is empty)" >&2
  exit 1
}
printf '%s\n' "${container_image}" >"${artifact_dir}/runner-image.txt"

# Export: every Result DB row of the scenario.
resultdb_pod="$("${kubectl_bin}" --kubeconfig "${kubeconfig}" get pod -n "${namespace}" -l "app=${project}-resultdb" -o jsonpath='{.items[0].metadata.name}')"
result_rows="$("${kubectl_bin}" --kubeconfig "${kubeconfig}" exec -n "${namespace}" "${resultdb_pod}" -- \
  env PGPASSWORD="${experiment_db_password}" psql -U smoke -d result_db -t -A -v ON_ERROR_STOP=1 \
  -c "SELECT json_agg(result ORDER BY id) FROM scenario_${scenario_id}_results")"
printf '%s\n' "${result_rows}" >"${artifact_dir}/result-rows.json"
result_count="$(printf '%s' "${result_rows}" | jq 'length' 2>/dev/null || echo 0)"
[[ "${result_count}" -ge 1 ]] 2>/dev/null || { echo "No Result DB rows exported for scenario ${scenario_id}" >&2; exit 1; }

# Export: experiment CR status and EDS final state.
"${kubectl_bin}" --kubeconfig "${kubeconfig}" get simulationexperiment "${project}" -n "${namespace}" -o json \
  >"${artifact_dir}/simulationexperiment.json"
eds_do GET /api/state
eds_expect_ok "GET /api/state (final)"
printf '%s\n' "${EDS_BODY}" >"${artifact_dir}/eds-state-final.json"

# --- Assertions and replay -------------------------------------------------------
# The assertion context adds the Core DB computed-replication count.
jq --argjson ncr "${number_of_computed_reps}" '. + {number_of_computed_reps:$ncr}' \
  "${artifact_dir}/design-values.json" >"${artifact_dir}/assert-context.json"

if ! "${python_bin}" "${acceptance_dir}/assert-results.py" \
    --rows "${artifact_dir}/result-rows.json" \
    --context "${artifact_dir}/assert-context.json" \
    --summary-out "${artifact_dir}/assert-summary.json" \
    >"${artifact_dir}/assertions.txt" 2>&1; then
  echo "assert-results.py failed:" >&2
  cat "${artifact_dir}/assertions.txt" >&2
  exit 1
fi
if ! "${python_bin}" "${acceptance_dir}/replay-seeds.py" \
    --rows "${artifact_dir}/result-rows.json" \
    --report "${artifact_dir}/replay-report.json" \
    >"${artifact_dir}/replay-stdout.txt" 2>&1; then
  echo "replay-seeds.py failed:" >&2
  cat "${artifact_dir}/replay-report.json" >&2
  exit 1
fi

# --- Sanitized evidence (P5-R4) ---------------------------------------------------
# Image references are recorded as <repository-name>@<digest> without the
# registry host (scrubbed per P5-R5). scrub_image_ref strips the exact
# CBSE_REGISTRY prefix, then any remaining registry host (the first path
# segment containing a dot or colon).
scrub_image_ref() {
  local ref="$1" path rest host
  if [[ "${ref}" == *@* ]]; then
    path="${ref%@*}"
    rest="@${ref##*@}"
  else
    path="${ref}"
    rest=""
  fi
  if [[ "${path}" == "${CBSE_REGISTRY}"/* ]]; then
    path="${path#"${CBSE_REGISTRY}"/}"
  elif [[ "${path}" == "${CBSE_REGISTRY}" ]]; then
    path=""
  fi
  if [[ "${path}" == */* ]]; then
    host="${path%%/*}"
    if [[ "${host}" == *.* || "${host}" == *:* ]]; then
      path="${path#*/}"
    fi
  fi
  printf '%s%s' "${path}" "${rest}"
}

sample_mean="$(jq -r '.sample_mean' "${artifact_dir}/assert-summary.json")"
half_width="$(jq -r '.half_width' "${artifact_dir}/assert-summary.json")"
recomputed_verdict="$(jq -r '.recomputed_verdict' "${artifact_dir}/assert-summary.json")"
rows_for_summary="$(jq -r '.rows' "${artifact_dir}/assert-summary.json")"
replay_rows_total="$(jq -r '.rows_total' "${artifact_dir}/replay-report.json")"
dirty_count="$(git -C "${root}" status --porcelain | wc -l | tr -d '[:space:]')"
if [[ "${dirty_count}" == "0" ]]; then
  dirty_state="clean"
else
  dirty_state="dirty (${dirty_count} uncommitted path entries)"
fi
finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

mkdir -p "${evidence_dir}"
cat >"${evidence_dir}/run.md" <<EOF
# PADS end-to-end acceptance run ${run_id}

- **Run ID:** ${run_id}
- **UTC date:** ${finished_at}
- **Branch commit:** ${branch_commit} (${dirty_state})
- **Cluster context:** ${CBSE_EXPECTED_CONTEXT}
- **Namespace:** ${namespace}
- **Project (SimulationExperiment):** ${project}

## Images (scrubbed per P5-R4: repository-name@digest, no registry host)

| Component | Reference |
|---|---|
| Paper-local Translator | $(scrub_image_ref "${PADS_TRANS_IMAGE}") |
| Experiment Operator | $(scrub_image_ref "${OPERATOR_IMAGE}") |
| Scenario Manager | $(scrub_image_ref "${SM_IMAGE}") |
| Reference EDS | $(scrub_image_ref "${EDS_REFERENCE_IMAGE}") |
| PostProcessingService | $(scrub_image_ref "${PPS_IMAGE}") |
| Scenario Detail Database | $(scrub_image_ref "${DETAIL_DB_IMAGE}") |
| Runner base | $(scrub_image_ref "${RUNNER_BASE_IMAGE}") |
| BuildKit builder (locked) | $(scrub_image_ref "${BUILDER_IMAGE}") |
| Result DB Postgres (locked) | $(scrub_image_ref "${POSTGRES_IMAGE}") |
| Generated runner (reported by the chain) | $(scrub_image_ref "${container_image}") |

## Scenario settings

| Field | Value |
|---|---|
| arrival_rate | ${design_arrival_rate} |
| service_rate | ${design_service_rate} |
| run_duration | ${design_run_duration} |
| seed_policy | ${design_seed_policy} |
| priority | ${scenario_priority} |
| number_of_reps | ${scenario_number_of_reps} |
| confidence_metric | ${scenario_confidence_metric} |
| PPS evaluation policy | statistical (default) |

## Verdict summary

- Final sample mean (mean_wait_time): ${sample_mean}
- Half-width (95% CI, Student-t): ${half_width}
- Verdict: met (scenario state Finished; recomputed verdict: ${recomputed_verdict})
- Computed replications: ${number_of_computed_reps}
- Additional-runner rounds: $((runner_round - 1)) (runner_round ${runner_round})

## Assertion and replay outcomes

- assert-results: PASS — ${rows_for_summary} rows, exact 15-field contract, distinct effective seeds, count matches computed replications
- replay-seeds: PASS — ${replay_rows_total} of ${replay_rows_total} rows replayed exactly (SimPy ${simpy_version})

## Unchanged smoke receipt (same settlement)

${PADS_SMOKE_RECEIPT}
EOF

# Copy the raw exports to the evidence directory and sanitize every file in
# place (deterministic redaction; the raw unsanitized exports stay untracked
# under artifacts/pads/).
cp "${artifact_dir}/result-rows.json" "${evidence_dir}/result-rows.json"
cp "${artifact_dir}/scenario-status.json" "${evidence_dir}/scenario-status.json"
cp "${artifact_dir}/eds-state-final.json" "${evidence_dir}/eds-state.json"
cp "${artifact_dir}/replay-report.json" "${evidence_dir}/replay-report.json"
cp "${artifact_dir}/assertions.txt" "${evidence_dir}/assertions.txt"

sanitize_evidence_file() {
  local file="$1"
  "${python_bin}" - "${file}" "${CBSE_REGISTRY}" "${kubeconfig}" <<'PY'
import re
import sys

path, registry, kubeconfig = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path, "r", encoding="utf-8") as fh:
    text = fh.read()
# R1: strip the exact CBSE_REGISTRY prefix (host + project path).
if registry:
    text = text.replace(registry + "/", "").replace(registry, "")
# R2: redact the kubeconfig path.
if kubeconfig:
    text = text.replace(kubeconfig, "<redacted-kubeconfig>")
# R3: redact URLs.
text = re.sub(r"https?://[^\s\"'<>]+", "<redacted-url>", text)
# R4: redact IPv4 endpoints.
text = re.sub(r"\b\d{1,3}(?:\.\d{1,3}){3}(?::\d{1,5})?\b", "<redacted-ip>", text)
# R5: strip a registry host from a host-qualified image reference, keeping
# the repository path and digest. The host's last label must contain a
# letter so version decimals (4.1.2, 0.05) are untouched.
text = re.sub(
    r"\b[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z0-9-]*[A-Za-z][A-Za-z0-9-]*(?::\d{1,5})?/([A-Za-z0-9._/-]+)(@sha256:[0-9a-f]{64})?",
    r"\1\2",
    text,
)
# R6: redact any remaining bare FQDN host token (same alphabetic-label rule).
text = re.sub(
    r"\b[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z0-9-]*[A-Za-z][A-Za-z0-9-]+(?::\d{1,5})?\b",
    "<redacted-host>",
    text
)
with open(path, "w", encoding="utf-8") as fh:
    fh.write(text)
PY
}
for evidence_file in "${evidence_dir}/run.md" "${evidence_dir}/result-rows.json" \
  "${evidence_dir}/scenario-status.json" "${evidence_dir}/eds-state.json" \
  "${evidence_dir}/replay-report.json" "${evidence_dir}/assertions.txt"; do
  sanitize_evidence_file "${evidence_file}"
done

# Verify the sanitized evidence is free of operational infrastructure.
sanitize_violation=0
for evidence_file in "${evidence_dir}"/run.md "${evidence_dir}"/result-rows.json \
  "${evidence_dir}"/scenario-status.json "${evidence_dir}"/eds-state.json \
  "${evidence_dir}"/replay-report.json "${evidence_dir}"/assertions.txt; do
  if grep -qF -- "${CBSE_REGISTRY}" "${evidence_file}"; then
    echo "Sanitization violation: registry value present in ${evidence_file}" >&2
    sanitize_violation=1
  fi
  if [[ -n "${kubeconfig}" ]] && grep -qF -- "${kubeconfig}" "${evidence_file}"; then
    echo "Sanitization violation: kubeconfig path present in ${evidence_file}" >&2
    sanitize_violation=1
  fi
  if grep -qE 'https?://' "${evidence_file}"; then
    echo "Sanitization violation: URL present in ${evidence_file}" >&2
    sanitize_violation=1
  fi
done
if (( sanitize_violation == 1 )); then
  exit 1
fi

echo "PADS acceptance run ${run_id} completed; sanitized evidence: ${evidence_dir}"
