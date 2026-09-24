# Contributing to CBSE

Thank you for your interest in the **Container-Based Simulation Environment (CBSE)**! This document describes how to participate: prerequisites, the test contract, development setup, where the extension points live, and the pull-request flow.

> CBSE is a research prototype in early development (see [README.md](README.md)). Interfaces and behavior may change without notice. This guidance is written to stay truthful for the next run — if the repository has moved past this document, the repository itself is the source of truth.

## Prerequisites

- A public Go toolchain of the 1.26 era — the workspace (`go.work`) pins `go 1.26.3`. No private or internal toolchains, mirrors, or build services are required or assumed.
- `make`
- No Kubernetes cluster, container registry, or credentials are needed for the hermetic development loop (`make test-fast`).

## Getting and verifying generated artifacts

Generated assets (the `SimulationExperiment` CRD, the RBAC role, and DeepCopy code) live in the repository and are checked by the test contract. Regenerate and verify them with:

```bash
make verify-generated
```

This runs `controller-gen` for the `experiment-operator` module and then diffs the regenerated CRD, RBAC role, and `zz_generated.deepcopy.go` files against the checked-in copies. If your change touches API types, run this target before proposing it: generated files must be committed together with the source change, and the check fails on any drift.

## Test contract

The repository exposes one test contract (the normative statement lives in [AGENTS.md](AGENTS.md); the full guide is [Testing guide](docs/CBSE_TESTING_GUIDE.md)):

| Tier | Command | When |
|---|---|---|
| Module tier (hermetic) | `make test-fast` | **Mandatory** for any Go, CRD, Dockerfile, or test-harness change — and a good default for any code change |
| Cluster tier (smoke) | `make test-smoke` | Exists but is **environment-gated**: it requires a user-provided Kubernetes cluster and registry via `KUBECONFIG`, `CBSE_REGISTRY`, and `CBSE_REGISTRY_AUTH_FILE` (no repository defaults). Use it for changes to the operator reconciliation path, API/CRD, Scenario Manager Kubernetes/NATS/database integration, container images, or Kubernetes manifests |
| None | — | Documentation-only changes need no test tier |

Two working rules:

- Never weaken, delete, skip, or rewrite a test to conceal a failure. Diagnose at the source, and flag suspected real bugs instead of papering over them.
- `make test-fast` must pass before you propose any code change.

## Development setup

The repository is a Go workspace (`go.work`) with four members: `experiment-operator`, `scenario-manager`, `component-templates/translator`, and `test/e2e`.

```bash
# Full hermetic verification: unit tests, vet, format check, generated-artifact verification
make test-fast

# Per-module loops
cd scenario-manager && go test ./...
cd experiment-operator && go test ./...
cd component-templates/translator && go test ./...
```

## Extension points

The framework's pluggable boundaries are the Experimental Design Service (EDS), the Translator, and the PostProcessingService. Reference implementations live in `component-templates/`:

- `component-templates/translator/` — the reference Translator (SimPy-based runner generation, rootless BuildKit build)
- `component-templates/scenario-detail-database/` — the reference Scenario Detail Database image

To implement your own component, start with [Designing Custom CBSE Components](docs/COMPONENT_DESIGN_GOALS.md); the cluster your components run on must meet the [Cluster requirements](docs/CLUSTER_REQUIREMENTS.md) (Kubernetes >= 1.30, `UserNamespacesSupport` feature gate enabled).

## Pull request process

- Use the standard GitHub flow: fork (if needed), branch, and propose a pull request against `main`.
- Keep changes small and focused; one concern per pull request.
- Pull requests are expected to come with a passing `make test-fast` — and with the cluster tier run when the table above says it applies.
- Include a short description of what changed, why, and how it was verified.

> **Disclosure / collaboration contact:** the maintainers are still finalizing the project's public disclosure and collaboration contact. Until it is published, see [SECURITY.md](SECURITY.md) for the disclosure channel and its status. Do not assume an email address or social handle exists.

## Coding agents

This repository is developed with coding agents. If you use one, point it at [AGENTS.md](AGENTS.md) — the repository's test contract and cluster-safety rules that agents must obey.
