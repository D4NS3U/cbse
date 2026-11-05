# P3B2 — Standalone model container

## Target

Package the accepted P3B1 credential-free CLI as a paper-local application image under `pads-paper/simulation-model/container/app/`, built on the repository's locked Python base through the existing runner-base contract, and prove deterministic in-container behavior. This slice does not modify the paper-local Translator or any Kubernetes manifest; scenario-specific generated runner images remain P4.

## Dependency

- P3B1 is independently approved and landed at `dbadcd8b7352d237bce31e3edee0cd387269c008`.

## Requirements

### P3B2-R1 — Application image

Create `pads-paper/simulation-model/container/app/Dockerfile` that:

- starts with `# syntax=docker/dockerfile:1.7`;
- declares `ARG RUNNER_BASE_IMAGE` with no default, and `FROM ${RUNNER_BASE_IMAGE}` — a missing argument must fail the build (no floating tags);
- copies exactly the canonical `model.py` and `cli.py` from `pads-paper/simulation-model/src/` into `/runner/` — no `result.py`, `seed.py`, tests, scenario files, database configuration, or credentials;
- keeps the inherited non-root `USER 1000:1000` explicitly;
- sets `ENTRYPOINT ["python3", "/runner/cli.py"]` with no `CMD` (the four CLI flags are required);
- adds no `EXPOSE`, volume, network, or secret constructs.

The image is credential-free and single-purpose: one replication, one JSON object on stdout.

### P3B2-R2 — Locked base chain

The build chain uses only repository-locked inputs:

1. `test/e2e/images.lock.env` `PYTHON_BASE_IMAGE` (immutable digest) builds the paper-local runner-base from `pads-paper/simulation-model/container/base/Dockerfile` (the P3A byte-identical import; do not modify it).
2. That locally built runner-base image is passed as `RUNNER_BASE_IMAGE` to the application build.

No tag references, no environment-invented bases, no insecure-registry or TLS bypasses. Nothing is pushed to any registry in P3B2; registry publication of paper images happens only when a later slice wires them into an executed experiment flow.

### P3B2-R3 — Local build and in-container verification

From a fresh shell with Docker available, prove:

- both builds succeed with the locked inputs;
- two in-container runs with identical flags produce byte-identical stdout (the exact P3B1 13-field compact key-sorted JSON, finite/bounded);
- an in-container invalid invocation exits non-zero with stderr diagnostics and empty stdout;
- the container executes as UID/GID 1000 (no root);
- `/runner` inside the image contains exactly the expected module set (no tests, no database/credential files) and `result.py`/`seed.py` are absent from the image;
- the application image's copied modules are byte-identical to the canonical `pads-paper/simulation-model/src/` files.

### P3B2-R4 — Documentation

Update `pads-paper/simulation-model/container/README.md` (and the simulation-model README only if needed) with:

- the two-stage build chain and exact commands using the locked `PYTHON_BASE_IMAGE` digest reference and a placeholder local tag for the runner-base and application images;
- in-container run examples with placeholder flags;
- the credential-free, single-replication, stdout-only contract;
- the statement that no registry push happens in this slice and publication is deferred to the experiment-flow integration;
- the distinction between this standalone image and the future Translator-generated scenario runner.

Preserve the mandatory template-reuse ruling and baseline provenance text.

### P3B2-R5 — Scope and hygiene

- Do not modify `pads-paper/simulation-model/src/**` (the canonical sources are settled by P3B1), `container/base/**`, paper-local components, original `component-templates/**`, shared runtime code, manifests, CRDs, root Makefile, harness scripts, tests, or `go.work`.
- Do not add infrastructure values, credentials, registry names, kubeconfig paths, image tags, or generated artifacts to tracked files.
- Do not build, select, or deploy any image through the repository harness in this slice.

## Verification tiers

- `make test-fast` is mandatory for implementation and review lanes (a Dockerfile under the repository changed).
- The cluster smoke tier is the **settlement/landing gate for this slice**: after review approval, the coordinator runs `make test-smoke` with the user-provided `KUBECONFIG`, `CBSE_REGISTRY`, `CBSE_REGISTRY_AUTH_FILE`, `CBSE_EXPECTED_CONTEXT=k3s-test`, and an already-published `TEST_IMAGE_VERSION`, and records the run id and 15/15 result before landing. Implementation and review workers do not run smoke; the harness lock and cluster credentials remain with the user.

## Observable acceptance (worker lanes)

1. Both images build from the locked digest inputs; a build without `RUNNER_BASE_IMAGE` fails.
2. In-container deterministic replay, invalid-input behavior, non-root execution, and exact `/runner` content checks pass as specified in P3B2-R3.
3. The implementation diff edits only `pads-paper/simulation-model/container/**` (new `app/` tree plus README updates).
4. `git diff --check` passes.
5. Repository-root `make test-fast` passes.
6. The implementation commit carries the assigned orchestration task/dispatch trailer.

## Completion

P3B2 is `complete` only after independent Pi/GLM review returns `approve` against the implementation HEAD **and** the settlement smoke run passes 15/15 with the recorded run id; only then is the approved child branch landed on `PADS-paper`. Until the smoke receipt exists, the slice is `verification-blocked`.
