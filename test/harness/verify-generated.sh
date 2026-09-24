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

# verify-generated.sh — guard generated Experiment Operator artifacts.
#
# Regenerates the operator's CRDs, RBAC role, and DeepCopy code with
# controller-gen in an isolated temp copy of the experiment-operator module,
# then diffs the result against the checked-in files. It fails on any drift,
# enforcing that generated artifacts stay in sync with the alpha4 API
# definitions so a commit never ships stale generated code. Read-only against
# the repository: only a temp directory is written.
#
# Inputs / environment:
#   CONTROLLER_GEN (required) path to the controller-gen binary.
#   GOCACHE        (optional) override for the isolated build cache.
#
# Exit codes:
#   0  all generated artifacts match the checked-in files.
#   1  a diff was found (generated code is stale) — returned by diff.
#   Other non-zero from controller-gen or copy failures via set -e.
#
# Side effects:
#   Copies the experiment-operator module (api, cmd, config, hack, internal,
#   go.mod, go.sum, PROJECT) into a temp dir with an isolated GOCACHE, runs
#   controller-gen for RBAC, CRD (alpha4 paths), and object (DeepCopy), then
#   diffs config/crd/bases, config/rbac/role.yaml, and every
#   zz_generated.deepcopy.go against the repo. The temp dir is removed on EXIT.
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
GOCACHE="${GOCACHE:-${tmp}/gocache}" "${controller_gen}" rbac:roleName=manager-role webhook paths="./..."
GOCACHE="${GOCACHE:-${tmp}/gocache}" "${controller_gen}" crd:generateEmbeddedObjectMeta=true paths="./api/alpha4/..." output:crd:artifacts:config=config/crd/bases
GOCACHE="${GOCACHE:-${tmp}/gocache}" "${controller_gen}" object:headerFile="hack/boilerplate.go.txt" paths="./..."

diff -ru "${root}/experiment-operator/config/crd/bases" config/crd/bases
diff -u "${root}/experiment-operator/config/rbac/role.yaml" config/rbac/role.yaml
while IFS= read -r generated; do
  relative="${generated#./}"
  diff -u "${root}/experiment-operator/${relative}" "${tmp}/experiment-operator/${relative}"
done < <(find . -name zz_generated.deepcopy.go -type f | sort)
