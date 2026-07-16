# Coding-agent test contract

Use the repository-root test entry points. Do not deploy test resources directly into the `default` or `kube-system` namespaces.

## Required test tier

- Run `make test-fast` after every Go, CRD, Dockerfile, or test-harness change.
- Also run `make test-smoke` after changes to the operator reconciliation path, API/CRD, Scenario Manager Kubernetes/NATS/database integration, container images, or Kubernetes manifests.
- Documentation-only changes do not require the cluster smoke suite.

## Cluster safety

- Set `KUBECONFIG` explicitly. The normal Linux agent path is `/home/d4ns3u/.kube/config`.
- The smoke harness must identify `https://192.168.101.245:6443` and the expected K3s context before it mutates anything.
- Use immutable image digests. Never add insecure-registry or TLS-verification bypasses.
- Let the harness create and remove its `cbse-e2e-<run-id>` namespace. Use `CBSE_KEEP_ON_FAILURE=1` only for active debugging, followed by `make test-clean RUN_ID=<run-id>`.
- Do not delete the shared CRD or the `cbse-test-system` namespace.

## Commands

```bash
make test-fast

make test-smoke \
  KUBECONFIG=/home/d4ns3u/.kube/config \
  TEST_IMAGE_VERSION=26.7.16 \
  CBSE_REGISTRY_AUTH_FILE=/secure/dockerhub-config.json
```

Diagnostics and JUnit output are written below `artifacts/test/<run-id>/`; never add that directory to commits.
