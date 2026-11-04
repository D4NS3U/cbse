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

# test-harness.sh — self-test for the CBSE test harness scripts.
#
# Stubs kubectl, docker, curl, and Harbor in a temp directory, then exercises
# the harness end to end with fake state: preflight pass and reject cases
# (wrong server, old version, no amd64 node, UserNamespacesSupport off/missing,
# mutable image, missing kubeconfig, locked-image override, partial skip-build
# set); build-images nested layout, locked build args, default vs. custom
# components, and rejection of unknown/duplicate tokens and locked-image
# overrides; registry-cleanup annotation verification and unsafe-repo refusal;
# diagnose on an absent namespace; acquire-lock Lease contention; clean
# idempotency and the H4 CRs-first ordering (CR delete before namespace
# delete, Lease still released); generate-helm-values.sh positive receipt
# (mode 0600, exactly five components, locked auxiliary images, no private
# repository on stdout) plus tag/malformed/missing/OUTPUT-location negatives;
# and helm-lifecycle.sh required-input validation before any cluster access.
# It also grep-checks repo-structure invariants (Makefile defaults, nested tag
# layout, lock loading, pull-secret name). Run by the verification gate and
# `make test-fast` to keep the harness internally consistent.
#
# Inputs / environment:
#   (none required) all binaries are stubbed in a temp dir; PATH is extended
#   with that dir. The fake kubectl honors FAKE_SERVER, FAKE_VERSION,
#   FAKE_NODES_FILE, and FAKE_USERNS to drive preflight reject cases.
#
# Exit codes:
#   0  all self-tests passed (prints "Harness self-tests passed.").
#   1  an assertion failed (a stub did not behave as required).
#   Other non-zero propagated from a script under test via set -e.
#
# Side effects:
#   Read-only against the repository (reads Makefile, build-images.sh,
#   preflight.sh, manifests, README). All cluster/registry/docker interactions
#   are stubbed into a temp directory that is removed on EXIT.
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
  *"get --raw=/metrics"*)
    # Default: UserNamespacesSupport gate enabled. Set FAKE_USERNS=0 to stub a
    # cluster with the gate disabled, or FAKE_USERNS=missing for an absent metric.
    case "${FAKE_USERNS:-1}" in
      0) printf '%s\n' 'kubernetes_feature_enabled{name="UserNamespacesSupport",stage="BETA"} 0' ;;
      missing) : ;;
      *) printf '%s\n' 'kubernetes_feature_enabled{name="UserNamespacesSupport",stage="BETA"} 1' ;;
    esac ;;
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
  EDS_REFERENCE_IMAGE=registry.example.test/eds-reference@sha256:2222222222222222222222222222222222222222222222222222222222222222
  TRANS_IMAGE=registry.example.test/trans@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
  RUNNER_BASE_IMAGE=registry.example.test/runner-base@sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
  DETAIL_DB_IMAGE=registry.example.test/scenario-detail-database@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
  PPS_IMAGE=registry.example.test/pps@sha256:1111111111111111111111111111111111111111111111111111111111111111
  CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json"
)

# Preflight must pass against a qualifying cluster.
env "${common[@]}" "${root}/test/harness/preflight.sh" >/dev/null

# Preflight must reject a partial skip-build set: the alpha4 smoke requires all
# eight images (operator, sm, eds mock, the reference EDS, translator, runner
# base, Detail Database, and PPS). partial_common omits RUNNER_BASE_IMAGE,
# DETAIL_DB_IMAGE, and PPS_IMAGE, so preflight must fail.
partial_common=(
  KUBECTL="${tmp}/kubectl"
  KUBECONFIG="${tmp}/kubeconfig"
  SKIP_BUILD=1
  FAKE_NODES_FILE="${tmp}/node_amd64.json"
  OPERATOR_IMAGE=registry.example.test/operator@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  SM_IMAGE=registry.example.test/sm@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  EDS_IMAGE=registry.example.test/eds@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
  EDS_REFERENCE_IMAGE=registry.example.test/eds-reference@sha256:2222222222222222222222222222222222222222222222222222222222222222
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
grep -Fqx 'CBSE_REGISTRY ?=' "${root}/Makefile"
grep -Fqx 'CBSE_IMAGE_COMPONENTS ?= exop,sm,eds-mock,eds,translator,runner-base,scenario-detail-database,pps' "${root}/Makefile"
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
if env "${common[@]}" FAKE_USERNS=0 "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a cluster with UserNamespacesSupport disabled" >&2; exit 1
fi
if env "${common[@]}" FAKE_USERNS=missing "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a cluster missing the UserNamespacesSupport metric" >&2; exit 1
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
if env "${common[@]}" PPS_GO_BUILDER_IMAGE=override "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a PPS locked source-image environment override" >&2; exit 1
fi
if env "${common[@]}" PPS_GO_VERSION=override "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a PPS locked source-version environment override" >&2; exit 1
fi
if env "${common[@]}" NATS_IMAGE=override "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a NATS locked source-image environment override" >&2; exit 1
fi
if env "${common[@]}" NATS_VERSION=override "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a NATS locked source-version environment override" >&2; exit 1
fi
if env "${common[@]}" HELM_HOOK_KUBECTL_IMAGE=override "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a hook kubectl locked source-image environment override" >&2; exit 1
fi
if env "${common[@]}" HELM_HOOK_KUBECTL_VERSION=override "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a hook kubectl locked source-version environment override" >&2; exit 1
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
  CBSE_IMAGE_COMPONENTS=exop,sm,eds-mock,eds,translator,runner-base,scenario-detail-database,pps \
  CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json" \
  CBSE_IMAGE_ARTIFACT_DIR="${tmp}/build-artifacts" \
  "${root}/test/harness/build-images.sh" >/dev/null
for image in exop sm eds-mock eds translator runner-base scenario-detail-database pps; do
  grep -Fqx "registry.unibw.de/i31bdase/cbse-test/${image}:26.9.7" "${tmp}/docker-tags.txt"
  grep -Eq "^registry\.unibw\.de/i31bdase/cbse-test/${image}:26\.9\.7\.sha-" "${tmp}/docker-tags.txt"
done
grep -Fqx 'ARG TRANSLATOR_GO_BUILDER_IMAGE=docker.io/library/golang@sha256:3bf5b04541eb4a37fe62aa1bc9c98a1dec09db9d2e79c1d2eb54e3c9d08dbca9' "${tmp}/docker-tags.txt"
grep -Fqx 'ARG EDS_GO_BUILDER_IMAGE=docker.io/library/golang@sha256:3bf5b04541eb4a37fe62aa1bc9c98a1dec09db9d2e79c1d2eb54e3c9d08dbca9' "${tmp}/docker-tags.txt"
grep -Fqx 'ARG PYTHON_BASE_IMAGE=docker.io/library/python@sha256:b921fe7e7522f828d45197a47656ec465a9b15689b27fa8e1fba2864fca5b967' "${tmp}/docker-tags.txt"
grep -Fqx 'ARG POSTGRES_IMAGE=docker.io/library/postgres@sha256:7341002d2b8c7c5bdd7542a671a95b36196c0b5b888daf454ae4fc33ba5346d7' "${tmp}/docker-tags.txt"
grep -Fqx 'ARG PPS_GO_BUILDER_IMAGE=docker.io/library/golang@sha256:3bf5b04541eb4a37fe62aa1bc9c98a1dec09db9d2e79c1d2eb54e3c9d08dbca9' "${tmp}/docker-tags.txt"
grep -Eq '^OPERATOR_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/exop@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^SM_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/sm@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^EDS_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/eds-mock@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^EDS_REFERENCE_IMAGE=.*/eds@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^TRANS_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/translator@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^RUNNER_BASE_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/runner-base@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^DETAIL_DB_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/scenario-detail-database@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^PPS_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/pps@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"

# build-images.sh default (alpha4): the cutover build default is the real
# translator 7-component set, so the default build produces the real Translator
# and runner base, the reference Detail Database, and the reference PPS, not the
# synthetic translator mock.
PATH="${tmp}/fake-bin:${PATH}" DOCKER_CONFIG="${tmp}/docker-source" \
  FAKE_DOCKER_LOG="${tmp}/docker-tags-default.txt" \
  CBSE_REGISTRY=registry.unibw.de/i31bdase/cbse-test \
  TEST_IMAGE_VERSION=26.9.7 RUN_ID=alpha4-default \
  CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json" \
  CBSE_IMAGE_ARTIFACT_DIR="${tmp}/build-artifacts-default" \
  "${root}/test/harness/build-images.sh" >/dev/null
grep -Fqx "registry.unibw.de/i31bdase/cbse-test/translator:26.9.7" "${tmp}/docker-tags-default.txt"
grep -Eq '^.*/eds:26\.9\.7$' "${tmp}/docker-tags-default.txt"
grep -Fqx "registry.unibw.de/i31bdase/cbse-test/runner-base:26.9.7" "${tmp}/docker-tags-default.txt"
grep -Fqx "registry.unibw.de/i31bdase/cbse-test/scenario-detail-database:26.9.7" "${tmp}/docker-tags-default.txt"
grep -Fqx "registry.unibw.de/i31bdase/cbse-test/pps:26.9.7" "${tmp}/docker-tags-default.txt"
grep -Eq '^OPERATOR_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/exop@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts-default/images.env"
grep -Eq '^EDS_REFERENCE_IMAGE=.*/eds@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts-default/images.env"
grep -Eq '^TRANS_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/translator@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts-default/images.env"
grep -Eq '^RUNNER_BASE_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/runner-base@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts-default/images.env"
grep -Eq '^DETAIL_DB_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/scenario-detail-database@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts-default/images.env"
grep -Eq '^PPS_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/pps@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts-default/images.env"
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
if PPS_GO_BUILDER_IMAGE=override PATH="${tmp}/fake-bin:${PATH}" DOCKER_CONFIG="${tmp}/docker-source" \
  CBSE_REGISTRY=registry.unibw.de/i31bdase/cbse-test TEST_IMAGE_VERSION=26.9.7 \
  CBSE_IMAGE_COMPONENTS=exop CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json" \
  CBSE_IMAGE_ARTIFACT_DIR="${tmp}/bad-artifacts" \
  "${root}/test/harness/build-images.sh" >/dev/null 2>&1; then
  echo "build-images accepted a PPS locked source-image environment override" >&2; exit 1
fi
if PPS_GO_VERSION=override PATH="${tmp}/fake-bin:${PATH}" DOCKER_CONFIG="${tmp}/docker-source" \
  CBSE_REGISTRY=registry.unibw.de/i31bdase/cbse-test TEST_IMAGE_VERSION=26.9.7 \
  CBSE_IMAGE_COMPONENTS=exop CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json" \
  CBSE_IMAGE_ARTIFACT_DIR="${tmp}/bad-artifacts" \
  "${root}/test/harness/build-images.sh" >/dev/null 2>&1; then
  echo "build-images accepted a PPS locked source-version environment override" >&2; exit 1
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

# clean.sh CRs-first ordering (H4): with an owned namespace and the CRD
# present, the SimulationExperiment deletion must be issued before the
# namespace deletion, and the Lease must still be released afterwards.
clean_log="${tmp}/clean-calls.log"
rm -f "${clean_log}"
cat >"${tmp}/kubectl-clean-order" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${FAKE_CLEAN_LOG:?}"
case "$*" in
  *"get namespace cbse-e2e-clean-order -o jsonpath"*) printf '%s' clean-order ;;
  *"get namespace cbse-e2e-clean-order"*) exit 0 ;;
  *"get crd simulationexperiments"*) exit 0 ;;
  *"delete simulationexperiment --all -n cbse-e2e-clean-order --wait=true --timeout=120s"*) exit 0 ;;
  *"delete namespace cbse-e2e-clean-order --wait=true --timeout=180s"*) exit 0 ;;
  *"get lease cbse-smoke-lock"*) printf '%s' clean-order ;;
  *"delete lease cbse-smoke-lock"*) exit 0 ;;
  *) echo "unexpected clean-order fake kubectl call: $*" >&2; exit 9 ;;
esac
EOF
chmod +x "${tmp}/kubectl-clean-order"
KUBECTL="${tmp}/kubectl-clean-order" KUBECONFIG="${tmp}/kubeconfig" RUN_ID=clean-order \
  FAKE_CLEAN_LOG="${clean_log}" "${root}/test/harness/clean.sh"
cr_line="$(grep -n 'delete simulationexperiment' "${clean_log}" | head -n1 | cut -d: -f1)"
ns_line="$(grep -n 'delete namespace cbse-e2e-clean-order' "${clean_log}" | head -n1 | cut -d: -f1)"
[[ -n "${cr_line}" ]] || { echo "clean.sh did not delete the SimulationExperiments" >&2; exit 1; }
[[ -n "${ns_line}" ]] || { echo "clean.sh did not delete the namespace" >&2; exit 1; }
(( cr_line < ns_line )) || { echo "clean.sh deleted the namespace before the SimulationExperiments (CRs-first order violated)" >&2; exit 1; }
grep -Fq 'delete lease cbse-smoke-lock' "${clean_log}" || { echo "clean.sh did not release the smoke Lease" >&2; exit 1; }

# generate-helm-values.sh (H4): positive receipt, mode 0600, exact five
# components, locked auxiliary images, and stdout hygiene (no private
# repository values). The fake images.env uses a private-looking host to
# prove the generator never prints repositories on success.
gen_env="${tmp}/gen-images.env"
gen_values="${tmp}/gen-values.yaml"
gen_op_digest="sha256:$(printf '6%.0s' {1..64})"
gen_sm_digest="sha256:$(printf '7%.0s' {1..64})"
printf '%s\n%s\n%s\n' \
  "OPERATOR_IMAGE=registry.private.example.test/cbse/exop@${gen_op_digest}" \
  "SM_IMAGE=registry.private.example.test/cbse/sm@${gen_sm_digest}" \
  "EXTRA_KEY=ignored" >"${gen_env}"
gen_stdout="$(IMAGES_ENV="${gen_env}" OUTPUT="${gen_values}" "${root}/test/harness/generate-helm-values.sh")"
[[ -f "${gen_values}" ]] || { echo "generator did not write the values file" >&2; exit 1; }
gen_mode="$(stat -f '%Lp' "${gen_values}" 2>/dev/null || stat -c '%a' "${gen_values}")"
[[ "${gen_mode}" == "600" ]] || { echo "generator output is not mode 0600 (${gen_mode})" >&2; exit 1; }
[[ "${gen_stdout}" == "helm image values written: ${gen_values} (5 components)" ]] || {
  echo "generator success output must be only the path and component count: ${gen_stdout}" >&2
  exit 1
}
if printf '%s' "${gen_stdout}" | grep -Fq 'registry.private.example.test'; then
  echo "generator printed a private repository value to stdout" >&2; exit 1
fi
for expected in \
  'operator:' \
  '  image:' \
  "    repository: \"registry.private.example.test/cbse/exop\"" \
  "    digest: \"${gen_op_digest}\"" \
  '  kubectlImage:' \
  '    repository: "docker.io/alpine/k8s"' \
  '    repository: "docker.io/library/nats"' \
  '    repository: "docker.io/library/postgres"'; do
  grep -Fqx "${expected}" "${gen_values}" || { echo "generated values missing line: ${expected}" >&2; exit 1; }
done
[[ "$(grep -c '^    repository:' "${gen_values}")" -eq 5 ]] || { echo "generated values must map exactly five components" >&2; exit 1; }
[[ "$(grep -c '^    digest:' "${gen_values}")" -eq 5 ]] || { echo "generated values must carry exactly five digests" >&2; exit 1; }

# generate-helm-values.sh negatives: tags, malformed digests, duplicate @,
# empty values, missing keys, malformed lines, unreadable input, and OUTPUT
# inside a tracked repository path outside the ignored artifacts/ directory.
# Every negative must fail before writing the output file.
gen_neg() {
  local description="$1" file="$2" output="$3" rc=0
  IMAGES_ENV="${file}" OUTPUT="${output}" "${root}/test/harness/generate-helm-values.sh" >/dev/null 2>&1 || rc=$?
  [[ "${rc}" -ne 0 ]] || { echo "generator accepted ${description}" >&2; exit 1; }
  [[ ! -f "${output}" ]] || { echo "generator wrote output for ${description}" >&2; exit 1; }
}
printf '%s\n' "OPERATOR_IMAGE=registry.private.example.test/cbse/exop:latest@${gen_op_digest}" "SM_IMAGE=x@${gen_sm_digest}" >"${tmp}/gen-tagged.env"
gen_neg "a tag-bearing reference" "${tmp}/gen-tagged.env" "${tmp}/gen-neg.yaml"
printf '%s\n' "OPERATOR_IMAGE=registry.private.example.test/cbse/exop@sha256:$(printf '6%.0s' {1..63})" "SM_IMAGE=x@${gen_sm_digest}" >"${tmp}/gen-short.env"
gen_neg "a short digest" "${tmp}/gen-short.env" "${tmp}/gen-neg.yaml"
printf '%s\n' "OPERATOR_IMAGE=registry.private.example.test/cbse/exop@SHA256:$(printf '6%.0s' {1..64})" "SM_IMAGE=x@${gen_sm_digest}" >"${tmp}/gen-case.env"
gen_neg "an uppercase digest prefix" "${tmp}/gen-case.env" "${tmp}/gen-neg.yaml"
printf '%s\n' "OPERATOR_IMAGE=registry.private.example.test/cbse/exop@sha256:a@${gen_op_digest}" "SM_IMAGE=x@${gen_sm_digest}" >"${tmp}/gen-dup.env"
gen_neg "a duplicate @ reference" "${tmp}/gen-dup.env" "${tmp}/gen-neg.yaml"
printf '%s\n' "OPERATOR_IMAGE=${gen_op_digest}" "SM_IMAGE=x@${gen_sm_digest}" >"${tmp}/gen-noat.env"
gen_neg "a reference without a digest separator" "${tmp}/gen-noat.env" "${tmp}/gen-neg.yaml"
printf '%s\n' "OPERATOR_IMAGE=registry.private.example.test/cbse/exop@${gen_op_digest}" "SM_IMAGE=" >"${tmp}/gen-empty.env"
gen_neg "an empty reference value" "${tmp}/gen-empty.env" "${tmp}/gen-neg.yaml"
printf '%s\n' "OPERATOR_IMAGE=registry.private.example.test/cbse/exop@${gen_op_digest}" >"${tmp}/gen-missing.env"
gen_neg "a file missing SM_IMAGE" "${tmp}/gen-missing.env" "${tmp}/gen-neg.yaml"
printf '%s\n' "OPERATOR_IMAGE=registry.private.example.test/cbse/exop@${gen_op_digest}" "SM_IMAGE=x@${gen_sm_digest}" "not-a-pair" >"${tmp}/gen-malformed.env"
gen_neg "a malformed images.env line" "${tmp}/gen-malformed.env" "${tmp}/gen-neg.yaml"
gen_neg "an unreadable images.env" "${tmp}/does-not-exist.env" "${tmp}/gen-neg.yaml"
# OUTPUT inside the tracked repository is refused unless under artifacts/.
rc=0
IMAGES_ENV="${gen_env}" OUTPUT="${root}/private-values.yaml" "${root}/test/harness/generate-helm-values.sh" >/dev/null 2>&1 || rc=$?
[[ "${rc}" -ne 0 ]] || { echo "generator accepted OUTPUT inside a tracked repository path" >&2; exit 1; }
[[ ! -f "${root}/private-values.yaml" ]] || { echo "generator wrote into the tracked repository" >&2; exit 1; }
rc=0
IMAGES_ENV="${gen_env}" OUTPUT="${root}/test/harness/values-out.yaml" "${root}/test/harness/generate-helm-values.sh" >/dev/null 2>&1 || rc=$?
[[ "${rc}" -ne 0 ]] || { echo "generator accepted OUTPUT inside a tracked test path" >&2; exit 1; }
# The ignored artifacts/ directory is an accepted in-repo destination.
mkdir -p "${root}/artifacts/gen-selftest"
IMAGES_ENV="${gen_env}" OUTPUT="${root}/artifacts/gen-selftest/values.yaml" \
  "${root}/test/harness/generate-helm-values.sh" >/dev/null
rm -rf "${root}/artifacts/gen-selftest"

# helm-lifecycle.sh (H4): required inputs are validated before any cluster
# access or artifact side effect.
lifecycle_env_test() {
  local description="$1" expected_message="$2"
  shift 2
  local out rc=0
  # env -i: a controlled environment so ambient KUBECTL/HELM/KUBECONFIG/
  # RUN_ID/HELM_IMAGES_EXPORTS can never satisfy or leak into the harness.
  out="$(env -i PATH="${PATH}" HOME="${HOME}" "$@" "${root}/test/harness/helm-lifecycle.sh" 2>&1)" || rc=$?
  [[ "${rc}" -ne 0 ]] || { echo "lifecycle harness accepted ${description}" >&2; exit 1; }
  printf '%s' "${out}" | grep -Fq "${expected_message}" || {
    echo "lifecycle harness missing expected message for ${description}: ${out}" >&2
    exit 1
  }
}
lifecycle_env_test "a missing KUBECTL" "KUBECTL is required" \
  HELM="${tmp}/helm" KUBECONFIG="${tmp}/kubeconfig" HELM_IMAGES_ENV="${gen_env}"
lifecycle_env_test "a missing HELM" "HELM is required" \
  KUBECTL="${tmp}/kubectl" KUBECONFIG="${tmp}/kubeconfig" HELM_IMAGES_ENV="${gen_env}"
lifecycle_env_test "a missing KUBECONFIG" "KUBECONFIG must point to the dedicated test-cluster config" \
  KUBECTL="${tmp}/kubectl" HELM="${tmp}/helm" HELM_IMAGES_ENV="${gen_env}"
lifecycle_env_test "a missing HELM_IMAGES_ENV" "HELM_IMAGES_ENV must point to a readable digest-pinned images.env" \
  KUBECTL="${tmp}/kubectl" HELM="${tmp}/helm" KUBECONFIG="${tmp}/kubeconfig"
lifecycle_env_test "an invalid RUN_ID" "RUN_ID must be 1-30 lowercase DNS-label characters" \
  KUBECTL="${tmp}/kubectl" HELM="${tmp}/helm" KUBECONFIG="${tmp}/kubeconfig" HELM_IMAGES_ENV="${gen_env}" RUN_ID=Bad_ID
lifecycle_env_test "an unreadable HELM_IMAGES_ENV" "HELM_IMAGES_ENV is not readable" \
  KUBECTL="${tmp}/kubectl" HELM="${tmp}/helm" KUBECONFIG="${tmp}/kubeconfig" HELM_IMAGES_ENV="${tmp}/no-such-images.env"

# install-helm.sh: pinned Helm installer. A stub curl serves a real tarball
# bundle; the checksum-failure and download-failure paths are caught, and the
# success path installs only the checksum-verified binary. No real
# network/cluster operation: the stub refuses any unexpected URL.
helm_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
helm_arch="$(uname -m)"
case "${helm_arch}" in
  x86_64|amd64) helm_arch="amd64" ;;
  arm64|aarch64) helm_arch="arm64" ;;
esac
helm_bundle="helm-v3.18.4-${helm_os}-${helm_arch}"
# The official release tarball extracts into an inner <os>-<arch> directory.
helm_bundle_dir="${helm_os}-${helm_arch}"
mkdir -p "${tmp}/helm-src/${helm_bundle_dir}"
cat >"${tmp}/helm-src/${helm_bundle_dir}/helm" <<'EOF'
#!/usr/bin/env bash
echo "v3.18.4+h1-selftest"
EOF
chmod +x "${tmp}/helm-src/${helm_bundle_dir}/helm"
tar -C "${tmp}/helm-src" -czf "${tmp}/helm.tar.gz" "${helm_bundle_dir}"
if command -v sha256sum >/dev/null 2>&1; then
  helm_sha="$(sha256sum "${tmp}/helm.tar.gz" | awk '{print $1}')"
else
  helm_sha="$(shasum -a 256 "${tmp}/helm.tar.gz" | awk '{print $1}')"
fi
printf '%s\n' "${helm_sha}" >"${tmp}/helm-ok.sha256"
printf '%s\n' "0000000000000000000000000000000000000000000000000000000000000000" >"${tmp}/helm-bad.sha256"
cat >"${tmp}/fake-bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
out=""
url=""
while (( $# > 0 )); do
  case "$1" in
    --output|-o) out="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
case "${url}" in
  *.tar.gz) cp "${FAKE_HELM_TARBALL:?}" "${out}" ;;
  *.sha256) cp "${FAKE_HELM_CHECKSUM_FILE:?}" "${out}" ;;
  *) echo "unexpected fake curl url: ${url}" >&2; exit 9 ;;
esac
EOF
chmod +x "${tmp}/fake-bin/curl"
helm_install_out="$(PATH="${tmp}/fake-bin:${PATH}" FAKE_HELM_TARBALL="${tmp}/helm.tar.gz" \
  FAKE_HELM_CHECKSUM_FILE="${tmp}/helm-ok.sha256" \
  HELM_VERSION=v3.18.4 OUTPUT="${tmp}/helm-bin-ok" \
  "${root}/test/harness/install-helm.sh")"
[[ -x "${tmp}/helm-bin-ok" ]] || { echo "install-helm did not install the verified binary" >&2; exit 1; }
grep -Fq 'v3.18.4' <(printf '%s' "${helm_install_out}") || { echo "install-helm did not print the verified version" >&2; exit 1; }
helm_rc=0
PATH="${tmp}/fake-bin:${PATH}" FAKE_HELM_TARBALL="${tmp}/helm.tar.gz" \
  FAKE_HELM_CHECKSUM_FILE="${tmp}/helm-bad.sha256" \
  HELM_VERSION=v3.18.4 OUTPUT="${tmp}/helm-bin-bad" \
  "${root}/test/harness/install-helm.sh" >/dev/null 2>"${tmp}/helm-bad.err" || helm_rc=$?
[[ "${helm_rc}" -eq 1 ]] || { echo "install-helm checksum failure not caught (rc=${helm_rc})" >&2; exit 1; }
grep -Fq 'checksum verification failed' "${tmp}/helm-bad.err"
[[ ! -e "${tmp}/helm-bin-bad" ]] || { echo "install-helm installed a binary with a bad checksum" >&2; exit 1; }
helm_rc=0
PATH="${tmp}/fake-bin:${PATH}" FAKE_HELM_TARBALL="${tmp}/helm.tar.gz" \
  FAKE_HELM_CHECKSUM_FILE="${tmp}/does-not-exist.sha256" \
  HELM_VERSION=v3.18.4 OUTPUT="${tmp}/helm-bin-dl" \
  "${root}/test/harness/install-helm.sh" >/dev/null 2>&1 || helm_rc=$?
[[ "${helm_rc}" -ne 0 ]] || { echo "install-helm accepted a failed download" >&2; exit 1; }
[[ ! -e "${tmp}/helm-bin-dl" ]] || { echo "install-helm installed after a failed download" >&2; exit 1; }

# Pinned-Helm wiring: the Makefile pins the version and binary path, the chart
# tests honor the HELM environment, and test-fast runs the chart tests.
grep -Fqx 'HELM_VERSION ?= v3.18.4' "${root}/Makefile"
grep -Fqx 'HELM ?= $(ROOT)/bin/helm-$(HELM_VERSION)' "${root}/Makefile"
grep -Fq 'HELM_VERSION=$(HELM_VERSION) OUTPUT=$@ $(TEST_DIR)/harness/install-helm.sh' "${root}/Makefile"
grep -Fq 'HELM="$(HELM)" $(TEST_DIR)/harness/test-helm-chart.sh' "${root}/Makefile"
grep -Fq '$(MAKE) test-helm-chart' "${root}/Makefile"
grep -Fqx 'test-helm-lifecycle: test-tools helm-tools' "${root}/Makefile"
grep -Fq 'KUBECTL="$(KUBECTL)" HELM="$(HELM)" $(TEST_DIR)/harness/helm-lifecycle.sh' "${root}/Makefile"
grep -Fqx 'helm-image-values:' "${root}/Makefile"
grep -Fq 'IMAGES_ENV="$(IMAGES_ENV)" OUTPUT="$(OUTPUT)" $(TEST_DIR)/harness/generate-helm-values.sh' "${root}/Makefile"
grep -Fq 'test-helm-lifecycle' "${root}/Makefile"
grep -Fq 'test-helm-lifecycle KUBECONFIG' "${root}/Makefile"
grep -Fq 'helm-image-values IMAGES_ENV' "${root}/Makefile"
grep -Fq 'test-helm-lifecycle helm-image-values' "${root}/Makefile"
grep -Fq 'pinned_helm="${HELM:-}"' "${root}/test/harness/test-helm-chart.sh"
grep -Fq 'cbse_helm_run_discovered' "${root}/test/harness/test-helm-chart.sh"
grep -Fq 'assert-helpers.sh' "${root}/test/harness/test-helm-chart.sh"

echo "Harness self-tests passed."
