# Legacy EDS–Scenario Manager fixtures

This directory preserves the earlier manually-applied EDS/Scenario Manager manifests as compatibility fixtures. They are not a supported test path and must not be applied directly to `default` or `kube-system`.

The maintained, isolated test path is:

```bash
make test-fast
make test-smoke KUBECONFIG=/path/to/config
```

The smoke harness creates a unique `cbse-e2e-<run-id>` namespace, uses immutable image digests, collects diagnostics, and removes that namespace on completion. Its source, manifests, and assertions are in [`../../e2e`](../../e2e); reusable mocks are in [`../../mocks`](../../mocks).

`run_eds_e2e.sh` remains as a compatibility wrapper and delegates to `make test-smoke` from the repository root.
