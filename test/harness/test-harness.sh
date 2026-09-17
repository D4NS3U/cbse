#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

touch "${tmp}/kubeconfig"
cat >"${tmp}/auth.json" <<'EOF'
{"auths":{"registry.example.test":{"auth":"dGVzdDp0ZXN0"}}}
EOF

# Valid node JSON files avoid heredoc-escaping pitfalls for the preflight
# amd64-Node check. The default is a single qualifying linux/amd64 Node.
cat >"${tmp}/node_amd64.json" <<'EOF'
{"items":[{"metadata":{"name":"n1","labels":{"kubernetes.io/arch":"amd64"}},"spec":{},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}
EOF
cat >"${tmp}/node_arm64.json" <<'EOF'
{"items":[{"metadata":{"name":"n1","labels":{"kubernetes.io/arch":"arm64"}},"spec":{},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}
EOF
cat >"${tmp}/node_old.txt" <<'EOF'
{"serverVersion":{"gitVersion":"v1.29.4+k3s1"}}
EOF

cat >"${tmp}/kubectl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
args="$*"
case "${args}" in
  *"config current-context"*) echo default ;;
  *"config view --minify"*) printf '%s' "${FAKE_SERVER:-https://192.168.101.245:6443}" ;;
  *"version -o json"*)
    if [[ -n "${FAKE_VERSION:-}" ]]; then printf '%s' "{\"serverVersion\":{\"gitVersion\":\"${FAKE_VERSION}\"}}";
    else printf '%s' '{"serverVersion":{"gitVersion":"v1.32.5+k3s1"}}'; fi ;;
  *"get nodes"*) cat "${FAKE_NODES_FILE:?FAKE_NODES_FILE is required}" ;;
  *"get secret cbse-registry-auth -n cbse-test-system"*) echo kubernetes.io/dockerconfigjson ;;
  *"get secret"*) echo "unexpected registry Secret lookup: ${args}" >&2; exit 9 ;;
  *"auth can-i"*) echo yes ;;
  *"get namespace"*) exit 1 ;;
  *) echo "unexpected fake kubectl call: ${args}" >&2; exit 9 ;;
esac
EOF
chmod +x "${tmp}/kubectl"

common=(
  KUBECTL="${tmp}/kubectl"
  KUBECONFIG="${tmp}/kubeconfig"
  SKIP_BUILD=1
  FAKE_NODES_FILE="${tmp}/node_amd64.json"
  OPERATOR_IMAGE=registry.example.test/operator@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  SM_IMAGE=registry.example.test/sm@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  EDS_IMAGE=registry.example.test/eds@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
  TRANS_IMAGE=registry.example.test/trans@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
  RUNNER_BASE_IMAGE=registry.example.test/runner-base@sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
  DETAIL_DB_IMAGE=registry.example.test/scenario-detail-database@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
  CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json"
)

# Preflight must pass against a qualifying cluster.
env "${common[@]}" "${root}/test/harness/preflight.sh" >/dev/null

# Preflight must reject a partial skip-build set: the alpha4 smoke requires all
# six images (operator, sm, eds, translator, runner base, and Detail Database).
# partial_common omits RUNNER_BASE_IMAGE and DETAIL_DB_IMAGE, so preflight must fail.
partial_common=(
  KUBECTL="${tmp}/kubectl"
  KUBECONFIG="${tmp}/kubeconfig"
  SKIP_BUILD=1
  FAKE_NODES_FILE="${tmp}/node_amd64.json"
  OPERATOR_IMAGE=registry.example.test/operator@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  SM_IMAGE=registry.example.test/sm@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  EDS_IMAGE=registry.example.test/eds@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
  TRANS_IMAGE=registry.example.test/trans@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
  CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json"
)
if env "${partial_common[@]}" "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a partial skip-build image set missing the alpha4 outputs" >&2; exit 1
fi
# When an alpha4 output image is supplied it must still be an immutable digest.
if env "${common[@]}" RUNNER_BASE_IMAGE=registry.example.test/runner-base:latest "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a mutable alpha4 output image" >&2; exit 1
fi

# Repository-structure invariants.
grep -Fqx 'CBSE_REGISTRY ?= registry.unibw.de/i31bdase/cbse-test' "${root}/Makefile"
grep -Fqx 'CBSE_IMAGE_COMPONENTS ?= exop,sm,eds-mock,translator,runner-base,scenario-detail-database' "${root}/Makefile"
grep -Fqx '  local immutable="${repository}:${immutable_suffix}"' "${root}/test/harness/build-images.sh"
# The flat layout (cbse-test:<component>.test.<version>) is retired; every
# component uses the nested layout cbse-test/<component>:<version>.
if grep -Fq '.test.${version}' "${root}/test/harness/build-images.sh"; then
  echo "build-images.sh still uses the flat .test. tag layout" >&2; exit 1
fi
grep -Fqx '  local repository="${registry}/${name}"' "${root}/test/harness/build-images.sh"
grep -Fqx '  local canonical="${repository}:${version}"' "${root}/test/harness/build-images.sh"
grep -Fq 'load_image_lock "${lock_file}"' "${root}/test/harness/preflight.sh"
grep -Fq 'source "${root}/test/harness/image-lock.sh"' "${root}/test/harness/preflight.sh"
grep -Fqx 'pull_secret_name="${CBSE_PULL_SECRET_NAME:-cbse-registry-auth}"' "${root}/test/harness/preflight.sh"
grep -Fqx 'pull_secret_namespace="${CBSE_PULL_SECRET_NAMESPACE:-cbse-test-system}"' "${root}/test/harness/preflight.sh"
grep -Fq 'name: cbse-registry-auth' "${root}/test/e2e/manifests/base/stack.yaml"
grep -Fq 'registry.unibw.de/i31bdase/cbse-test' "${root}/test/e2e/README.md"
grep -Fq 'registry-cleanup.sh' "${root}/test/harness/preflight.sh"
grep -Fq 'experiment.cbse.terministic.de/experiment-uid' "${root}/test/harness/registry-cleanup.sh"

# Preflight must reject unsafe or non-conforming inputs.
if env "${common[@]}" FAKE_SERVER=https://wrong.example.test:6443 "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted the wrong API server" >&2; exit 1
fi
if env "${common[@]}" FAKE_VERSION=v1.29.4+k3s1 "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted an older-than-1.30 Kubernetes server" >&2; exit 1
fi
if env "${common[@]}" FAKE_NODES_FILE="${tmp}/node_arm64.json" "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a cluster with no amd64 Node" >&2; exit 1
fi
if env "${common[@]}" OPERATOR_IMAGE=registry.example.test/operator:latest "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a mutable image" >&2; exit 1
fi
if env "${common[@]}" KUBECONFIG="${tmp}/missing" "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a missing kubeconfig" >&2; exit 1
fi
if env "${common[@]}" BUILDER_IMAGE=override "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a locked source-image environment override" >&2; exit 1
fi

# build-images.sh: flat shared components, nested Detail DB, locked build args.
mkdir -p "${tmp}/fake-bin" "${tmp}/docker-source" "${tmp}/build-artifacts"
cp "${tmp}/auth.json" "${tmp}/docker-source/config.json"
cat >"${tmp}/fake-bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == "buildx version" ]]; then exit 0; fi
[[ "$1" == "buildx" && "$2" == "build" ]] || exit 9
shift 2
metadata=""
while (( $# > 0 )); do
  case "$1" in
    --tag) printf '%s\n' "$2" >>"${FAKE_DOCKER_LOG}"; shift 2 ;;
    --build-arg) printf 'ARG %s\n' "$2" >>"${FAKE_DOCKER_LOG}"; shift 2 ;;
    --metadata-file) metadata="$2"; shift 2 ;;
    *) shift ;;
  esac
done
[[ -n "${metadata}" ]] || exit 9
printf '%s\n' '{"containerimage.digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}' >"${metadata}"
EOF
chmod +x "${tmp}/fake-bin/docker"
PATH="${tmp}/fake-bin:${PATH}" DOCKER_CONFIG="${tmp}/docker-source" \
  FAKE_DOCKER_LOG="${tmp}/docker-tags.txt" \
  CBSE_REGISTRY=registry.unibw.de/i31bdase/cbse-test \
  TEST_IMAGE_VERSION=26.9.7 RUN_ID=alpha4-repository-test \
  CBSE_IMAGE_COMPONENTS=exop,sm,eds-mock,translator,runner-base,scenario-detail-database \
  CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json" \
  CBSE_IMAGE_ARTIFACT_DIR="${tmp}/build-artifacts" \
  "${root}/test/harness/build-images.sh" >/dev/null
for image in exop sm eds-mock translator runner-base scenario-detail-database; do
  grep -Fqx "registry.unibw.de/i31bdase/cbse-test/${image}:26.9.7" "${tmp}/docker-tags.txt"
  grep -Eq "^registry\.unibw\.de/i31bdase/cbse-test/${image}:26\.9\.7\.sha-" "${tmp}/docker-tags.txt"
done
grep -Fqx 'ARG TRANSLATOR_GO_BUILDER_IMAGE=docker.io/library/golang@sha256:3bf5b04541eb4a37fe62aa1bc9c98a1dec09db9d2e79c1d2eb54e3c9d08dbca9' "${tmp}/docker-tags.txt"
grep -Fqx 'ARG PYTHON_BASE_IMAGE=docker.io/library/python@sha256:b921fe7e7522f828d45197a47656ec465a9b15689b27fa8e1fba2864fca5b967' "${tmp}/docker-tags.txt"
grep -Fqx 'ARG POSTGRES_IMAGE=docker.io/library/postgres@sha256:7341002d2b8c7c5bdd7542a671a95b36196c0b5b888daf454ae4fc33ba5346d7' "${tmp}/docker-tags.txt"
grep -Eq '^OPERATOR_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/exop@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^SM_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/sm@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^EDS_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/eds-mock@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^TRANS_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/translator@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^RUNNER_BASE_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/runner-base@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^DETAIL_DB_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/scenario-detail-database@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"

# build-images.sh default (alpha4): the cutover build default is the real
# translator 6-component set, so the default build produces the real Translator
# and runner base, and the reference Detail Database, not the synthetic
# translator mock.
PATH="${tmp}/fake-bin:${PATH}" DOCKER_CONFIG="${tmp}/docker-source" \
  FAKE_DOCKER_LOG="${tmp}/docker-tags-default.txt" \
  CBSE_REGISTRY=registry.unibw.de/i31bdase/cbse-test \
  TEST_IMAGE_VERSION=26.9.7 RUN_ID=alpha4-default \
  CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json" \
  CBSE_IMAGE_ARTIFACT_DIR="${tmp}/build-artifacts-default" \
  "${root}/test/harness/build-images.sh" >/dev/null
grep -Fqx "registry.unibw.de/i31bdase/cbse-test/translator:26.9.7" "${tmp}/docker-tags-default.txt"
grep -Fqx "registry.unibw.de/i31bdase/cbse-test/runner-base:26.9.7" "${tmp}/docker-tags-default.txt"
grep -Fqx "registry.unibw.de/i31bdase/cbse-test/scenario-detail-database:26.9.7" "${tmp}/docker-tags-default.txt"
grep -Eq '^OPERATOR_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/exop@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts-default/images.env"
grep -Eq '^TRANS_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/translator@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts-default/images.env"
grep -Eq '^RUNNER_BASE_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/runner-base@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts-default/images.env"
grep -Eq '^DETAIL_DB_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/scenario-detail-database@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts-default/images.env"
if grep -Fq 'trans-mock' "${tmp}/docker-tags-default.txt"; then
  echo "alpha4 default build produced the synthetic translator mock" >&2; exit 1
fi

# build-images.sh must reject unknown, empty, and duplicate component tokens
# and locked source-image environment overrides before any build.
if PATH="${tmp}/fake-bin:${PATH}" DOCKER_CONFIG="${tmp}/docker-source" \
  CBSE_REGISTRY=registry.unibw.de/i31bdase/cbse-test TEST_IMAGE_VERSION=26.9.7 \
  CBSE_IMAGE_COMPONENTS=exop,bogus CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json" \
  CBSE_IMAGE_ARTIFACT_DIR="${tmp}/bad-artifacts" \
  "${root}/test/harness/build-images.sh" >/dev/null 2>&1; then
  echo "build-images accepted an unknown component token" >&2; exit 1
fi
if PATH="${tmp}/fake-bin:${PATH}" DOCKER_CONFIG="${tmp}/docker-source" \
  CBSE_REGISTRY=registry.unibw.de/i31bdase/cbse-test TEST_IMAGE_VERSION=26.9.7 \
  CBSE_IMAGE_COMPONENTS=exop,exop CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json" \
  CBSE_IMAGE_ARTIFACT_DIR="${tmp}/bad-artifacts" \
  "${root}/test/harness/build-images.sh" >/dev/null 2>&1; then
  echo "build-images accepted a duplicate component token" >&2; exit 1
fi
if BUILDER_IMAGE=override PATH="${tmp}/fake-bin:${PATH}" DOCKER_CONFIG="${tmp}/docker-source" \
  CBSE_REGISTRY=registry.unibw.de/i31bdase/cbse-test TEST_IMAGE_VERSION=26.9.7 \
  CBSE_IMAGE_COMPONENTS=exop CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json" \
  CBSE_IMAGE_ARTIFACT_DIR="${tmp}/bad-artifacts" \
  "${root}/test/harness/build-images.sh" >/dev/null 2>&1; then
  echo "build-images accepted a locked source-image environment override" >&2; exit 1
fi

# registry-cleanup.sh: annotation-verified generated-runner cleanup. The
# adapter targets only cbse-test-runner, deletes an artifact only after its
# three framework OCI identity annotations match the experiment UID, scenario
# ID, and attempt encoded by its exact deterministic runner tag and its
# complete tag set contains only that tag, treats absence as idempotent
# success, refuses a mismatched candidate without deleting it, and never
# targets cbse-test or the Detail DB repository.
mkdir -p "${tmp}/harbor-state"
cat >"${tmp}/fake-bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
method="GET"; out=""; url=""
while (( $# > 0 )); do
  case "$1" in
    -X) method="$2"; shift 2 ;;
    -o) out="$2"; shift 2 ;;
    -w) shift 2 ;;
    -H|--header) shift 2 ;;
    -sS|-s|--silent|--show-error) shift ;;
    *) url="$1"; shift ;;
  esac
done
uid="11111111-2222-3333-4444-555555555555"
prefix="111111112222"
safe_tag="runner-${prefix}-s1-a1"
unsafe_tag="runner-${prefix}-s2-a1"
safe_digest="sha256:$(printf 'a1%.0s' {1..32})"
unsafe_digest="sha256:$(printf 'b2%.0s' {1..32})"
state_dir="${FAKE_HARBOR_STATE:?}"
AU="experiment.cbse.terministic.de/experiment-uid"
AS="experiment.cbse.terministic.de/scenario-id"
AT="experiment.cbse.terministic.de/translation-attempt"
respond() {
  if [[ -n "${out}" && "${out}" != "/dev/null" ]]; then printf '%s' "$1" >"${out}"; fi
  printf '%s' "$2"
}
artifact_json() {
  printf '{"digest":"%s","tags":[{"name":"%s"}],"annotations":{"%s":"%s","%s":"%s","%s":"%s"}}' \
    "$1" "$2" "${AU}" "$3" "${AS}" "$4" "${AT}" "$5"
}
rel="${url#*repositories/cbse-test-runner/artifacts}"
ref="${rel%%\?*}"; ref="${ref#/}"
if [[ -z "${ref}" ]]; then
  if [[ -e "${state_dir}/del-${safe_digest/:/_}" ]]; then
    respond '{"items":[]}' "200"
  else
    body="$(artifact_json "${safe_digest}" "${safe_tag}" "${uid}" "1" "1")"
    respond "{\"items\":[${body}]}" "200"
  fi
  exit 0
fi
if [[ "${method}" == "DELETE" ]]; then
  if [[ "${ref}" == "${safe_digest}" ]]; then touch "${state_dir}/del-${safe_digest/:/_}"; respond "" "200"; else respond "" "404"; fi
  exit 0
fi
if [[ "${ref}" == "${safe_tag}" || "${ref}" == "${safe_digest}" ]]; then
  if [[ -e "${state_dir}/del-${safe_digest/:/_}" ]]; then respond "" "404"; else
    respond "$(artifact_json "${safe_digest}" "${safe_tag}" "${uid}" "1" "1")" "200"
  fi
  exit 0
fi
if [[ "${ref}" == "${unsafe_tag}" || "${ref}" == "${unsafe_digest}" ]]; then
  if [[ -e "${state_dir}/del-${unsafe_digest/:/_}" ]]; then respond "" "404"; else
    respond "$(artifact_json "${unsafe_digest}" "${unsafe_tag}" "wrong-uid" "2" "1")" "200"
  fi
  exit 0
fi
respond "" "404"
EOF
chmod +x "${tmp}/fake-bin/curl"
printf '%s\n%s\n%s\n' \
  "runner-111111112222-s1-a1" \
  "runner-111111112222-s2-a1" \
  "runner-111111112222-s3-a1" >"${tmp}/runner-records.txt"
cat >"${tmp}/harbor-auth.json" <<'EOF'
{"auths":{"registry.unibw.de":{"auth":"dGVzdDp0ZXN0"}}}
EOF
cleanup_rc=0
PATH="${tmp}/fake-bin:${PATH}" FAKE_HARBOR_STATE="${tmp}/harbor-state" \
  CBSE_REGISTRY_AUTH_FILE="${tmp}/harbor-auth.json" \
  CBSE_EXPERIMENT_UID="11111111-2222-3333-4444-555555555555" \
  CBSE_RUNNER_RECORDS="${tmp}/runner-records.txt" \
  "${root}/test/harness/registry-cleanup.sh" >"${tmp}/cleanup.out" 2>"${tmp}/cleanup.err" || cleanup_rc=$?
[[ "${cleanup_rc}" -ne 0 ]] || { echo "registry-cleanup accepted a mismatched-annotation candidate" >&2; exit 1; }
[[ -e "${tmp}/harbor-state/del-sha256_$(printf 'a1%.0s' {1..32})" ]] || { echo "registry-cleanup did not delete the verified safe artifact" >&2; exit 1; }
[[ ! -e "${tmp}/harbor-state/del-sha256_$(printf 'b2%.0s' {1..32})" ]] || { echo "registry-cleanup deleted an unverified artifact" >&2; exit 1; }
grep -Fq 'experiment-uid mismatch' "${tmp}/cleanup.err" || { echo "cleanup failure log missing mismatch report" >&2; exit 1; }
if PATH="${tmp}/fake-bin:${PATH}" FAKE_HARBOR_STATE="${tmp}/harbor-state" \
  CBSE_REGISTRY_AUTH_FILE="${tmp}/harbor-auth.json" \
  CBSE_EXPERIMENT_UID="11111111-2222-3333-4444-555555555555" \
  CBSE_RUNNER_REPO="cbse-test" \
  "${root}/test/harness/registry-cleanup.sh" >/dev/null 2>&1; then
  echo "registry-cleanup accepted an unsafe target repository" >&2; exit 1
fi

KUBECTL="${tmp}/kubectl" KUBECONFIG="${tmp}/kubeconfig" RUN_ID=unit \
  CBSE_ARTIFACT_DIR="${tmp}/artifacts" "${root}/test/harness/diagnose.sh"
grep -q 'does not exist' "${tmp}/artifacts/cluster-state.txt"

cat >"${tmp}/kubectl-lock" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == *" create -f "* ]]; then
  if [[ -e "${FAKE_LOCK_STATE}" ]]; then exit 1; fi
  touch "${FAKE_LOCK_STATE}"
  exit 0
fi
if [[ "$*" == *" get lease "* ]]; then
  printf '%s' first-run
  exit 0
fi
exit 9
EOF
chmod +x "${tmp}/kubectl-lock"
KUBECTL="${tmp}/kubectl-lock" KUBECONFIG="${tmp}/kubeconfig" RUN_ID=first-run \
  FAKE_LOCK_STATE="${tmp}/lease-state" "${root}/test/harness/acquire-lock.sh"
if KUBECTL="${tmp}/kubectl-lock" KUBECONFIG="${tmp}/kubeconfig" RUN_ID=second-run \
  FAKE_LOCK_STATE="${tmp}/lease-state" "${root}/test/harness/acquire-lock.sh" >/dev/null 2>&1; then
  echo "a second run bypassed the lease" >&2; exit 1
fi

KUBECTL="${tmp}/kubectl" KUBECONFIG="${tmp}/kubeconfig" RUN_ID=unit "${root}/test/harness/clean.sh"
KUBECTL="${tmp}/kubectl" KUBECONFIG="${tmp}/kubeconfig" RUN_ID=unit "${root}/test/harness/clean.sh"

echo "Harness self-tests passed."
