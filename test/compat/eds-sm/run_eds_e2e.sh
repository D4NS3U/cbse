#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
echo "test/compat/eds-sm/run_eds_e2e.sh is deprecated; delegating to the full-stack smoke pipeline." >&2
echo "Use 'make test-smoke KUBECONFIG=/path/to/config CBSE_REGISTRY=registry/project' directly." >&2
exec make -C "${root}" test-smoke
