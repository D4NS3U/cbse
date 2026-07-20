# CBSE testing

All shared test infrastructure is kept below this directory.

- `e2e/`: digest-pinned Kubernetes smoke suite, manifests, and Go assertions.
- `harness/`: cluster preflight, image publishing, locking, diagnostics, cleanup, and harness self-tests.
- `mocks/`: source and Dockerfiles for the EDS and translator test doubles.
- `compat/eds-sm/`: deprecated compatibility fixtures and wrapper retained for existing users.

Component-local Go tests remain with their components (`experiment-operator/` and `scenario-manager/`), which keeps normal Go package tests discoverable. Use the repository-root commands rather than invoking harness scripts directly:

```bash
make test-fast
make test-smoke KUBECONFIG=/path/to/config
```
