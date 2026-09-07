#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

touch "${tmp}/kubeconfig"
cat >"${tmp}/auth.json" <<'EOF'
{"auths":{"registry.example.test":{"auth":"dGVzdDp0ZXN0"}}}
EOF
cat >"${tmp}/kubectl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
args="$*"
case "${args}" in
  *"config current-context"*) echo default ;;
  *"config view --minify"*) printf '%s' "${FAKE_SERVER:-https://192.168.101.245:6443}" ;;
  *"version -o json"*) printf '%s' '{"serverVersion":{"gitVersion":"v1.32.5+k3s1"}}' ;;
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
  OPERATOR_IMAGE=registry.example.test/operator@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  SM_IMAGE=registry.example.test/sm@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  EDS_IMAGE=registry.example.test/eds@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
  TRANS_IMAGE=registry.example.test/trans@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
  CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json"
)

env "${common[@]}" "${root}/test/harness/preflight.sh" >/dev/null

grep -Fqx 'CBSE_REGISTRY ?= registry.unibw.de/i31bdase/cbse-test' "${root}/Makefile"
grep -Fqx '  local repository="${registry}/${name}"' "${root}/test/harness/build-images.sh"
grep -Fqx '  local canonical="${repository}:${version}"' "${root}/test/harness/build-images.sh"
grep -Fqx '  local immutable="${repository}:${immutable_suffix}"' "${root}/test/harness/build-images.sh"
if grep -Fq '${registry}:${name}.test.' "${root}/test/harness/build-images.sh"; then
  echo "build-images still uses flat repository tags" >&2
  exit 1
fi
for image in exop sm eds-mock trans-mock; do
  grep -Fq "registry.unibw.de/i31bdase/cbse-test/${image}:26.7.16" "${root}/test/e2e/README.md"
done
grep -Fqx 'pull_secret_name="${CBSE_PULL_SECRET_NAME:-cbse-registry-auth}"' "${root}/test/harness/preflight.sh"
grep -Fqx 'pull_secret_namespace="${CBSE_PULL_SECRET_NAMESPACE:-cbse-test-system}"' "${root}/test/harness/preflight.sh"
grep -Fq 'name: cbse-registry-auth' "${root}/test/e2e/manifests/base/stack.yaml"

if env "${common[@]}" FAKE_SERVER=https://wrong.example.test:6443 "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted the wrong API server" >&2
  exit 1
fi

if env "${common[@]}" OPERATOR_IMAGE=registry.example.test/operator:latest "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a mutable image" >&2
  exit 1
fi

if env "${common[@]}" KUBECONFIG="${tmp}/missing" "${root}/test/harness/preflight.sh" >/dev/null 2>&1; then
  echo "preflight accepted a missing kubeconfig" >&2
  exit 1
fi

mkdir -p "${tmp}/fake-bin" "${tmp}/docker-source" "${tmp}/build-artifacts"
cp "${tmp}/auth.json" "${tmp}/docker-source/config.json"
cat >"${tmp}/fake-bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == "buildx version" ]]; then
  exit 0
fi
[[ "$1" == "buildx" && "$2" == "build" ]] || exit 9
shift 2
metadata=""
while (( $# > 0 )); do
  case "$1" in
    --tag)
      printf '%s\n' "$2" >>"${FAKE_DOCKER_LOG}"
      shift 2
      ;;
    --metadata-file)
      metadata="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
[[ -n "${metadata}" ]] || exit 9
printf '%s\n' '{"containerimage.digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}' >"${metadata}"
EOF
chmod +x "${tmp}/fake-bin/docker"
PATH="${tmp}/fake-bin:${PATH}" DOCKER_CONFIG="${tmp}/docker-source" \
  FAKE_DOCKER_LOG="${tmp}/docker-tags.txt" \
  CBSE_REGISTRY=registry.unibw.de/i31bdase/cbse-test \
  TEST_IMAGE_VERSION=26.9.7 RUN_ID=nested-repository-test \
  CBSE_IMAGE_COMPONENTS=exop,sm,eds-mock,trans-mock \
  CBSE_REGISTRY_AUTH_FILE="${tmp}/auth.json" \
  CBSE_IMAGE_ARTIFACT_DIR="${tmp}/build-artifacts" \
  "${root}/test/harness/build-images.sh" >/dev/null
for image in exop sm eds-mock trans-mock; do
  grep -Fqx "registry.unibw.de/i31bdase/cbse-test/${image}:26.9.7" "${tmp}/docker-tags.txt"
  grep -Eq "^registry\.unibw\.de/i31bdase/cbse-test/${image}:26\.9\.7\.sha-" "${tmp}/docker-tags.txt"
done
grep -Eq '^OPERATOR_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/exop:26\.9\.7@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^SM_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/sm:26\.9\.7@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^EDS_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/eds-mock:26\.9\.7@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"
grep -Eq '^TRANS_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/trans-mock:26\.9\.7@sha256:[a-f0-9]{64}$' "${tmp}/build-artifacts/images.env"

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
  echo "a second run bypassed the lease" >&2
  exit 1
fi

KUBECTL="${tmp}/kubectl" KUBECONFIG="${tmp}/kubeconfig" RUN_ID=unit "${root}/test/harness/clean.sh"
KUBECTL="${tmp}/kubectl" KUBECONFIG="${tmp}/kubeconfig" RUN_ID=unit "${root}/test/harness/clean.sh"

echo "Harness self-tests passed."
