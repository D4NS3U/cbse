# CBSE full-stack smoke test

This suite deploys the current Experiment Operator and Scenario Manager with real
PostgreSQL and NATS/JetStream dependencies. The per-experiment reference
Experimental Design Service (the EDS) is driven by the suite through its JSON
API (design -> Save Experiment -> Start Experiment -> re-arm -> Finish
Experiment); the EDS mock remains a built image for the unit/integration
tiers only (it is no longer part of the smoke stack). The alpha4 reference
Translator, runner base, Scenario Detail Database, and reference Post
Processing Service are built from `component-templates/`.

## Prerequisites

- Go 1.24 or newer (build-time only; the Go module version is independent of the
  Kubernetes runtime version), Docker with Buildx, `curl`, `jq`, and OpenSSL.
- A Kubernetes **1.30 or newer** cluster with at least one `linux/amd64` `Ready`
  schedulable Node. The alpha4 Translator Deployment uses a native sidecar
  BuildKit container, so the 1.30-through-1.32 native-sidecar feature gate must
  be enabled on the cluster (graduated to GA in 1.33).
- A readable kubeconfig. Linux agents normally use `/home/d4ns3u/.kube/config`;
  set another explicit path when needed.
- Access to the K3s API server at `https://192.168.101.245:6443`.
- Access to the University Harbor repository prefix
  `registry.unibw.de/i31bdase/cbse-test` from both the agent and the K3s node.
  All eight component images use the **nested** layout, one dedicated repository
  per component below the prefix
  (`${CBSE_REGISTRY}/<component>:<version>`), including the reference
  Scenario Detail Database
  (`${CBSE_REGISTRY}/scenario-detail-database:<version>`).
  Generated runner
  images are published to a separate `${CBSE_REGISTRY}/cbse-test-runner`
  repository.
- The checked-in source-image lock `test/e2e/images.lock.env` (four locked
  source images and four provenance versions). Locked inputs have no
  environment override.
- A dedicated Docker `config.json` provided through `CBSE_REGISTRY_AUTH_FILE`.
- A `kubernetes.io/dockerconfigjson` Secret named `cbse-registry-auth` in
  `cbse-test-system`; the harness copies it only to its ephemeral test namespace.

The harness downloads kubectl v1.32.5 into the ignored root `bin/` directory.

## Run

```bash
make test-smoke \
  KUBECONFIG=/home/d4ns3u/.kube/config \
  TEST_IMAGE_VERSION=26.7.16 \
  CBSE_REGISTRY_AUTH_FILE=<protected-docker-config>
```

A smoke build builds the default alpha4 reference set
(`exop,sm,eds-mock,eds,translator,runner-base,scenario-detail-database,pps`);
`build-images.sh`
publishes each component in the nested layout with a canonical tag
(`${CBSE_REGISTRY}/<component>:<version>`) and an immutable provenance tag.
The pushed digests are
recorded in
`images.env` (`OPERATOR_IMAGE`, `SM_IMAGE`, `EDS_IMAGE`, `EDS_REFERENCE_IMAGE`,
`TRANS_IMAGE`, `RUNNER_BASE_IMAGE`, `DETAIL_DB_IMAGE`, `PPS_IMAGE`); the
digest is the immutable reference.

An authorized administrator provisions the dedicated Docker configuration and the shared Kubernetes Secret outside this repository. Do not use a credential helper for the configuration and do not commit it.

To reuse already published images, every reference must include a digest:

```bash
SKIP_BUILD=1 \
OPERATOR_IMAGE=registry.unibw.de/i31bdase/cbse-test/exop@sha256:... \
SM_IMAGE=registry.unibw.de/i31bdase/cbse-test/sm@sha256:... \
EDS_IMAGE=registry.unibw.de/i31bdase/cbse-test/eds-mock@sha256:... \
EDS_REFERENCE_IMAGE=registry.unibw.de/i31bdase/cbse-test/eds@sha256:... \
TRANS_IMAGE=registry.unibw.de/i31bdase/cbse-test/translator@sha256:... \
RUNNER_BASE_IMAGE=registry.unibw.de/i31bdase/cbse-test/runner-base@sha256:... \
DETAIL_DB_IMAGE=registry.unibw.de/i31bdase/cbse-test/scenario-detail-database@sha256:... \
PPS_IMAGE=registry.unibw.de/i31bdase/cbse-test/pps@sha256:... \
CBSE_REGISTRY_AUTH_FILE=<protected-docker-config> \
make test-smoke KUBECONFIG=/home/d4ns3u/.kube/config
```

The eight `*_IMAGE` variables are all required when `SKIP_BUILD=1`; a mutable
(floating-tag) reference is rejected. Every component image uses the nested
digest form (`${CBSE_REGISTRY}/<component>@sha256:<hex>`), including the
Detail Database (`${CBSE_REGISTRY}/scenario-detail-database@sha256:<hex>`).

`CBSE_KEEP_ON_FAILURE=1` retains a failed namespace. Inspect it with `make test-diagnose RUN_ID=<id>` and remove it with `make test-clean RUN_ID=<id>`. Neither cleanup path removes the shared CRD.

To retain the namespace after a successful run for manual inspection, set `CBSE_KEEP_NAMESPACE=1`. This is opt-in; without it, successful runs are cleaned automatically:

```bash
CBSE_KEEP_NAMESPACE=1 make test-smoke KUBECONFIG=/path/to/config
```

The command prints the run ID. Inspect it with `make test-diagnose RUN_ID=<id>` and remove it with `make test-clean RUN_ID=<id>` when finished.

For a retained E2E environment that also leaves the `SimulationExperiment`, owned EDS/translator/design workloads, and database rows intact, with the Basic Scenario Selection Logic and translator handoff enabled, use the dedicated target:

```bash
make test-e2e-retained KUBECONFIG=/path/to/config
```

This intentionally skips only the final garbage-collection assertion. The preceding provisioning, persistence, and idempotence assertions still run. Clean the retained run with `make test-clean RUN_ID=<id> KUBECONFIG=/path/to/config`.

## The experiment flow (the API-driven EDS)

The green experiment provisions the **reference EDS** (not the mock) from
`spec.experimentalDesignService`. Once the experiment is `InProgress` (the
readiness gate includes the EDS), the suite port-forwards the EDS Service
(`svc/<project>-eds-svc`, port 8082) and drives the four-step flow through the
EDS's JSON API - the same contract the embedded UI page issues:

1. **Design:** fixed deterministic entries (ids >= 5; the baseline ids 1-4
   stay read-only) and two recipes, then two scenarios (the met-path shape
   `priority 1, number_of_reps 40` and the loop-path shape `priority 2,
   number_of_reps 1`).
2. **Save Experiment:** one transaction into the Scenario Detail Database;
   the suite asserts the entry rows, the `recipes` locator rows, the
   `simulation_parameters` view's projection, and the coexisting baseline
   rows 1-4.
3. **Start Experiment:** the availability handshake plus the first batch
   publish; the suite asserts the Core DB intake.
4. **Re-arm:** two more scenarios, Save, Start - the delta publish (only the
   not-yet-published scenarios leave the EDS).
5. **Finish Experiment:** after all four scenarios are terminally verified
   and the CR carries the `InProgress` message
   "awaiting EDS finalization", the suite publishes the finalization batch
   (`scenarios: []`, `final: true`) and asserts the terminal `Finished`
   phase (the finalization-aware verdict required the final).

The red `fcrash` spec delivers its batch through the same API helper against
its own reference EDS (Save + Start, never finishing - `Failed` is fail-fast
and never awaits `final`). The EDS mock (`test/mocks/eds/eds_mock.py`) is no
longer part of the smoke stack; it stays for the unit/integration tiers, now
with the finalization step (its availability request declares
`experiment_type: "standard"` and it verifies the SM's verbatim echo; after
its standard batches it publishes the finalization batch, gated by
`EDS_FINALIZE`, default `true`).

## Version validation

`TEST_IMAGE_VERSION` uses a non-normalizing `YY.M.D` format (for example
`26.7.16`). The harness validates it against `^[0-9]{2}\.[0-9]{1,2}\.[0-9]{1,2}$`
without rewriting single-digit month/day values; `26.7.16` stays `26.7.16` and is
not padded to `26.07.16`.

## Safety and artifacts

The runner rejects unexpected contexts, API servers older than 1.30, clusters
with no `linux/amd64` `Ready` schedulable Node, missing permissions, mutable
image references, locked source-image environment overrides, invalid or
incomplete image locks, invalid registry credentials, and unowned incompatible
CRDs before proceeding. Runs are serialized through
`cbse-test-system/cbse-smoke-lock` and never use `default`.

Generated-runner cleanup targets only the `${CBSE_REGISTRY}/cbse-test-runner`
repository; it never deletes or prunes the shared reference repositories
(the per-component nested repositories under `${CBSE_REGISTRY}` or
`${CBSE_REGISTRY}/scenario-detail-database`).

Each run writes JUnit XML, a JSON summary, image digests, sanitized rendered manifests, events, pod descriptions, workload state, database assertions, and logs to `artifacts/test/<run-id>/`. Kubernetes Secret objects and their payloads are never collected.
