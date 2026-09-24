#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# env-provider.template.sh — TEMPLATE ONLY.
#
# Fill values inside cbse-labs, never in this repository.
#
# Purpose
#   Shows how the private environment provider (charter: the "env-value
#   provider" of the cbse-labs skeleton, PROBLEM.md §5) supplies the
#   smoke-tier variables of the public repository's test contract. The
#   public Makefile takes these as required, environment-provided values
#   with no repository defaults; see the public repository's
#   docs/CBSE_TESTING_GUIDE.md for the tier semantics and
#   docs/CLUSTER_REQUIREMENTS.md for what the cluster itself must provide.
#
# Usage
#   1. Copy this file into cbse-labs (suggested: cbse-labs/env/provider.sh).
#   2. Replace every <placeholder> below with the private value.
#   3. In the private environment, before invoking the smoke tier:
#        source cbse-labs/env/provider.sh
#        make test-smoke        # from the public repository checkout
#
# Safety notes
#   - This template is deliberately NOT under `set -eu` and contains no
#     private values: it is safe to copy, review, and edit. Real values are
#     never written back into this repository (P5).
#   - The guard below fails loudly if any placeholder is left untouched, so
#     an unfilled copy cannot silently export the placeholder strings.
#   - Never echo or log the contents of the auth file referenced by
#     CBSE_REGISTRY_AUTH_FILE; it is credential material.
# ---------------------------------------------------------------------------

# --- Fill these inside cbse-labs (placeholders — zero private values here) ---

# Container registry (and repository prefix) where the test image set is
# published and from which the smoke run pulls.
_CBSE_REGISTRY_TPL="<your-registry>"

# Path to a Docker configuration file with pull/push rights for the above
# registry (the smoke harness consumes it via CBSE_REGISTRY_AUTH_FILE).
_CBSE_AUTH_FILE_TPL="<path-to-docker-config>"

# Kubeconfig of the smoke cluster (Kubernetes >= 1.30,
# UserNamespacesSupport feature gate enabled — see the public
# docs/CLUSTER_REQUIREMENTS.md).
_CBSE_KUBECONFIG_TPL="<path-to-kubeconfig>"

# --- Guard: refuse to export unfilled placeholders --------------------------

_cbse_provider_unfilled=""
[[ "${_CBSE_REGISTRY_TPL}" == "<your-registry>" ]] && _cbse_provider_unfilled="${_cbse_provider_unfilled} CBSE_REGISTRY"
[[ "${_CBSE_AUTH_FILE_TPL}" == "<path-to-docker-config>" ]] && _cbse_provider_unfilled="${_cbse_provider_unfilled} CBSE_REGISTRY_AUTH_FILE"
[[ "${_CBSE_KUBECONFIG_TPL}" == "<path-to-kubeconfig>" ]] && _cbse_provider_unfilled="${_cbse_provider_unfilled} KUBECONFIG"

if [[ -n "${_cbse_provider_unfilled}" ]]; then
  echo "env-provider.template.sh: unfilled placeholder(s)${_cbse_provider_unfilled}." >&2
  echo "Fill the values in the cbse-labs copy of this file; never in this repository." >&2
  return 1 2>/dev/null || exit 1
fi

# --- Export the smoke-tier variables ------------------------------------------

export CBSE_REGISTRY="${_CBSE_REGISTRY_TPL}"
export CBSE_REGISTRY_AUTH_FILE="${_CBSE_AUTH_FILE_TPL}"
export KUBECONFIG="${_CBSE_KUBECONFIG_TPL}"

# TEST_IMAGE_VERSION is optional: the public Makefile defaults it to the
# current UTC date when unset. The cbse-labs provider may pin it for
# reproducible runs:
# export TEST_IMAGE_VERSION="<pinned-image-version>"
