#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
controller_gen="${CONTROLLER_GEN:?CONTROLLER_GEN is required}"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

mkdir -p "${tmp}/experiment-operator"
for path in api cmd config hack internal go.mod go.sum PROJECT; do
  cp -R "${root}/experiment-operator/${path}" "${tmp}/experiment-operator/${path}"
done
cd "${tmp}/experiment-operator"
GOCACHE="${GOCACHE:-${tmp}/gocache}" "${controller_gen}" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases
GOCACHE="${GOCACHE:-${tmp}/gocache}" "${controller_gen}" object:headerFile="hack/boilerplate.go.txt" paths="./..."

diff -ru "${root}/experiment-operator/config/crd/bases" config/crd/bases
diff -u "${root}/experiment-operator/config/rbac/role.yaml" config/rbac/role.yaml
while IFS= read -r generated; do
  relative="${generated#./}"
  diff -u "${root}/experiment-operator/${relative}" "${tmp}/experiment-operator/${relative}"
done < <(find . -name zz_generated.deepcopy.go -type f | sort)
