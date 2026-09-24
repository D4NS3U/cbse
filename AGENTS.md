# Coding-agent test contract

Use the repository-root test entry points. Do not deploy test resources directly into the `default` or `kube-system` namespaces.

## Required test tier

- Run `make test-fast` after every Go, CRD, Dockerfile, or test-harness change.
- Also run `make test-smoke` after changes to the operator reconciliation path, API/CRD, Scenario Manager Kubernetes/NATS/database integration, container images, or Kubernetes manifests.
- Documentation-only changes do not require the cluster smoke suite.

## Cluster safety

- Set `KUBECONFIG` explicitly to the dedicated test-cluster config. The smoke harness identifies the API server and context it expects before it mutates anything and refuses to run against a different cluster.
- Use immutable image digests. Never add insecure-registry or TLS-verification bypasses.
- Let the harness create and remove its `cbse-e2e-<run-id>` namespace. Use `CBSE_KEEP_ON_FAILURE=1` only for active debugging, followed by `make test-clean RUN_ID=<run-id>`.
- Do not delete resources the harness does not create, including the shared CRD and the operating cluster's helper namespaces.

## Commands

```bash
make test-fast

make test-smoke \
  KUBECONFIG=/path/to/kubeconfig \
  TEST_IMAGE_VERSION=26.7.16 \
  CBSE_REGISTRY=<your-registry> \
  CBSE_REGISTRY_AUTH_FILE=<protected-docker-config>
```

`CBSE_REGISTRY` is required and environment-provided; the repository never embeds a default registry value. Related entry points: `make publish-test-images` (build and publish the component test images into `CBSE_REGISTRY`) and `make test-e2e-retained` (smoke run with unconditional retention for active debugging).

Diagnostics and JUnit output are written below `artifacts/test/<run-id>/`; never add that directory to commits.

## What stays and what must not appear

Institution attribution in `LICENSE`, copyright headers, and paper references is legitimate and must survive any scrub. Operational infrastructure — cluster IPs and endpoints, private registry names, CI runner labels, kubeconfig paths, hostnames — must not appear in working contracts, documentation, or tracked defaults; write placeholders and take the values from the environment instead.
