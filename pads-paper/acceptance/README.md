# PADS paper — end-to-end acceptance (P5) and campaign driver (P6)

Tooling for the paper-local end-to-end acceptance slice
(`devlog/changes/pads-paper/slices/P5-end-to-end-acceptance.md`) and,
as ruled by the P6 slice
(`devlog/changes/pads-paper/slices/P6-paper-result-bundle.md`), the
`--design` campaign mode of the same driver. This directory is the
complete acceptance tooling contract; the live acceptance run and the
live campaign are **owned by settlement**, not by the worker lane.

Nothing in this directory may be run against a cluster, registry, or
database without the full environment contract below. Every entry point
preflights first and refuses (non-zero exit, no mutation) when the
environment is incomplete.

## Ownership and boundaries

- **Slice scope (this directory, only):**
  - `publish-translator-image.sh` — build/push the paper-local
    Translator image from `pads-paper/translator/component/Dockerfile`
    (the shared `test/harness/build-images.sh` is unchanged).
  - `run-experiment.sh` — live end-to-end acceptance driver
    (preflight → deploy → design → export → teardown). The default
    invocation is the P5 single-scenario acceptance path (unchanged);
    `--design <design.json>` adds the P6 ruled multi-scenario campaign
    mode (see "Run the ruled campaign (design mode)").
  - `assert-results.py` — pure, offline validation of exported Result DB
    rows against the canonical `erlang_a_v1` contract.
  - `replay-seeds.py` — recorded-effective-seed replay against the
    canonical simulation model.
  - `test_acceptance.py` — offline self-tests for the Python tools, the
    driver refusal paths, and the single-scenario and campaign stub
    dry-runs.
  - `evidence/` — placeholder-only; sanitized evidence is populated at
    settlement time and never hand-fabricated.
- **Out of scope (must not be modified here):** the devlog specs, the
  shared `test/harness/*` suite, `test/e2e/*` manifests, component code,
  and `pads-paper/simulation-model/**` (read-only input to
  `replay-seeds.py` and `test_acceptance.py`).
- **Settlement owns the live run.** The scripts here are the contract
  the settlement operator executes; they must stay reproducible from
  immutable digests and a sanitized evidence bundle.

## File layout

| File | Role |
|---|---|
| `publish-translator-image.sh` | Paper-local Translator image build/publish (digest-pinned output). |
| `run-experiment.sh` | Live experiment driver: strict preflight, deploy, EDS-driven design, export, verification, teardown, sanitized evidence. |
| `assert-results.py` | Offline validator for exported result rows (exact `erlang_a_v1` 15-field contract). |
| `replay-seeds.py` | Offline exact-value replay of recorded effective seeds (canonical model, SimPy 4.1.2). |
| `test_acceptance.py` | Offline self-tests (`python3 -m unittest`): Python tools, driver refusal paths, single-scenario and campaign stub dry-runs. |
| `evidence/` | Sanitized per-run evidence directory (placeholder README only; campaign evidence is written to `pads-paper/results/evidence/`). |

## Environment contract

All values are **environment-provided**. The repository never embeds a
default registry value, cluster endpoint, kubeconfig path, or runner
label. Placeholders in prose; real values come from the environment.

Shared by both scripts:

| Variable | Meaning |
|---|---|
| `CBSE_REGISTRY` | Host + project path of the private test registry (e.g. `<registry-host>/<project>`). Never a default. |

`publish-translator-image.sh` additionally requires:

| Variable | Meaning |
|---|---|
| `CBSE_REGISTRY_AUTH_FILE` | Protected `docker config` JSON (`.dockerconfigjson`) used to log in to `CBSE_REGISTRY`. Must be readable; never committed. |
| `TEST_IMAGE_VERSION` | Canonical image tag; must use `YY.M.D` form (e.g. `26.7.16`). |
| `RUN_ID` (optional) | Run id that forms part of the immutable provenance tag; default is a UTC timestamp plus six random hex digits. |

`run-experiment.sh` additionally requires (each validated in preflight
before any cluster mutation):

| Variable | Meaning |
|---|---|
| `KUBECONFIG` | Path to the dedicated test-cluster kubeconfig. |
| `CBSE_EXPECTED_CONTEXT` | The kube context name the run is allowed to use; the live `current-context` must match exactly or the run refuses. |
| `OPERATOR_IMAGE`, `SM_IMAGE`, `EDS_REFERENCE_IMAGE`, `PPS_IMAGE`, `DETAIL_DB_IMAGE` | Immutable (`@sha256:`) digest references for the base stack, matching `test/e2e/manifests/base/stack.yaml`. |
| `PADS_TRANS_IMAGE` | Immutable digest reference of the paper-local Translator (output of `publish-translator-image.sh`). |
| `RUNNER_BASE_IMAGE` | Immutable digest reference of the runner base image (input to the generator). |
| `PADS_SMOKE_RECEIPT` | One-line receipt from the unchanged smoke suite over the same digest set (evidence the base settlement is intact). |
| `CBSE_RUNNER_REPOSITORY` (optional) | Runner repository pushed by the generated runners (default `${CBSE_REGISTRY}/cbse-test-runner`). |
| `KUBECTL` (optional) | kubectl binary path (default `kubectl`). |
| `PADS_PYTHON` (optional) | Python interpreter that must provide SimPy 4.1.2 (default `python3`). |
| `PADS_EDS_LOCAL_PORT` (optional) | Local port for the EDS port-forward (default `18082`). |
| `CBSE_KEEP_ON_FAILURE` (optional) | Set to `1` to skip the EXIT-trap teardown for active debugging (the run id is printed so the retained namespace can be cleaned manually). |

Design mode (`--design`, P6) adds one optional variable:

| Variable | Meaning |
|---|---|
| `PADS_CAMPAIGN_TIMEOUT` (optional) | Wall-clock seconds to wait for all campaign scenarios to reach a terminal state (default `14400`). Must be a positive integer when set. |

The run id is always generated by `run-experiment.sh`; there is no
environment override for it. Default mode: `pads-<UTCstamp>-<6hex>`.
Design mode: `pads-campaign-<UTCstamp>-<6hex>` (the prefix keeps the
campaign evidence directories separable from single-run evidence).

Locked source images are read from `test/e2e/images.lock.env` through
the shared `load_image_lock` loader and are **never** environment
inputs: the loader aborts if any lock variable is set in the
environment. The acceptance scripts use three of the lock keys —
`TRANSLATOR_GO_BUILDER_IMAGE` (the Go builder stage in
`publish-translator-image.sh`) and `BUILDER_IMAGE` plus `POSTGRES_IMAGE`
(the BuildKit builder and the Result DB Postgres in
`run-experiment.sh`); the lock file additionally pins the remaining
images and their provenance versions (`PYTHON_BASE_IMAGE`,
`PPS_GO_BUILDER_IMAGE`, and the matching `_VERSION` keys).

## Usage

### 1. Publish the paper-local Translator image

```bash
CBSE_REGISTRY=<your-registry> \
CBSE_REGISTRY_AUTH_FILE=<protected-docker-config> \
TEST_IMAGE_VERSION=<version> \
bash pads-paper/acceptance/publish-translator-image.sh
```

The script:
1. Preflights (registry/auth/version/builder/Dockerfile present and
   valid); on any miss it exits non-zero **without building or pushing**.
2. Builds the image from `pads-paper/translator/component/Dockerfile`
   (multi-stage Go, locked builder image).
3. Logs in to `CBSE_REGISTRY` using `CBSE_REGISTRY_AUTH_FILE` (no
   insecure-registry or TLS-bypass flags are ever used).
4. Pushes the image under **both** the canonical version tag
   (`.../translator:<TEST_IMAGE_VERSION>`) and the immutable provenance
   tag `<version>.sha-<commit>-<source-hash>-<run-id>` (the harness
   convention, not a digest-pushed reference).
5. Writes a single digest-pinned `PADS_TRANS_IMAGE=<repo>@sha256:<digest>`
   line to the caller-supplied artifact file (default
   `artifacts/pads/<run-id>/pads-translator.env`) that can be
   `source`d into the experiment driver environment.

### 2. Run the live end-to-end acceptance experiment

```bash
CBSE_REGISTRY=<your-registry> \
KUBECONFIG=<test-kubeconfig> \
CBSE_EXPECTED_CONTEXT=<context> \
OPERATOR_IMAGE=<digest-ref> \
SM_IMAGE=<digest-ref> \
EDS_REFERENCE_IMAGE=<digest-ref> \
PPS_IMAGE=<digest-ref> \
DETAIL_DB_IMAGE=<digest-ref> \
PADS_TRANS_IMAGE=<digest-ref> \
RUNNER_BASE_IMAGE=<digest-ref> \
PADS_SMOKE_RECEIPT="<one-line smoke receipt>" \
bash pads-paper/acceptance/run-experiment.sh
```

Sequence (strict; the run id is `pads-<UTCstamp>-<6hex>` and the
namespace is `cbse-pads-<run-id-suffix>`):

1. **Preflight** (no cluster mutation on any miss, exit 2):
   - All required environment values present; kubeconfig readable.
   - `KUBECTL`/`jq`/`curl`/`openssl`/`git` available.
   - `PADS_PYTHON` (or `python3`) imports SimPy **4.1.2** exactly.
   - Every image reference is an immutable digest reference
     (`<repo>@sha256:<64hex>`); locked build inputs match
     `images.lock.env` and are not environment-overridden.
   - The live kube `current-context` equals `CBSE_EXPECTED_CONTEXT`
     (refuses to mutate a different cluster).
   - The shared pull Secret in the operating helper namespace exists
     and is of type `kubernetes.io/dockerconfigjson` (read-only check).
2. **Deploy**: create the run namespace (labelled, privileged
   pod-security for the BuildKit builder), create the Core DB
   credentials Secret, copy the shared pull Secret into the namespace,
   and patch the namespace's default ServiceAccount to use it. Render
   the base stack from `test/e2e/manifests/base/` via `kubectl
   kustomize` with the digest image overrides applied, then apply it.
   Wait for each Deployment rollout and for the Scenario Manager to
   report ready.
3. **Design**: create the `SimulationExperiment` (project = run id),
   wait for `status.phase == InProgress`, then drive the reference EDS
   JSON API over `kubectl port-forward` (never `kubectl exec`/`port-forward`
   into a mutating shell): create the four parameter entries
   (arrival_rate, service_rate, run_duration, seed_policy), the recipe,
   the scenario (priority 5, number_of_reps 5, confidence_metric 0.05),
   save, and start the batch.
4. **Export**: poll the Scenario Manager Core DB (via `kubectl exec psql`
   into the Core DB pod, read-only SELECTs) until the scenario reaches
   the terminal `Finished` state. Export: Result DB rows (canonical
   `erlang_a_v1` rows), the scenario record, the
   `SimulationExperiment` status, and the EDS state. Write the raw
   bundle under `artifacts/pads/<run-id>/` (never committed).
5. **Verify** (offline, pure Python): run `assert-results.py` and
   `replay-seeds.py` over the exported rows; both must pass.
6. **Teardown**: delete the run's `SimulationExperiment` (wait), then
   delete the run namespace (wait). An `EXIT` trap guarantees teardown
   runs on any failure path (unless `CBSE_KEEP_ON_FAILURE=1`).
7. **Evidence**: write the sanitized evidence bundle to
   `pads-paper/acceptance/evidence/<run-id>/` (see below) and print the
   run id and evidence path.

### 3. Run the ruled campaign (design mode, P6)

```bash
CBSE_REGISTRY=<your-registry> \
KUBECONFIG=<test-kubeconfig> \
CBSE_EXPECTED_CONTEXT=<context> \
OPERATOR_IMAGE=<digest-ref> \
SM_IMAGE=<digest-ref> \
EDS_REFERENCE_IMAGE=<digest-ref> \
PPS_IMAGE=<digest-ref> \
DETAIL_DB_IMAGE=<digest-ref> \
PADS_TRANS_IMAGE=<digest-ref> \
RUNNER_BASE_IMAGE=<digest-ref> \
PADS_SMOKE_RECEIPT="<one-line smoke receipt>" \
bash pads-paper/acceptance/run-experiment.sh \
  --design pads-paper/results/campaign/design.json
```

Design mode is a ruled, minimal extension of the same driver — the
single-scenario default path is unchanged. Differences from default
mode:

1. **Preflight** additionally validates the design file through
   `pads-paper/results/analysis/campaign.py` (strict `pads-campaign-
   design-v1` grammar: unique entry values, unique recipe labels,
   unique scenario labels, well-formed recipe/scenario references,
   positive replications, epsilon in `(0,1]`, known constant fields).
   Any violation refuses the run before cluster mutation.
2. **Run id / namespace**: `pads-campaign-<UTCstamp>-<6hex>` (namespace
   `cbse-<that-id>`).
3. **EDS flow**: every design entry, recipe, and scenario is posted
   (the design's own order), then **one** `/api/save` and **one**
   `/api/start` for the whole campaign.
4. **Completion**: all scenarios must reach terminal `Finished` within
   `PADS_CAMPAIGN_TIMEOUT` (default 4h); any `Failed` scenario fails
   the campaign.
5. **Verification**: `assert-results.py` and `replay-seeds.py` run
   **per scenario** over its exported rows.
6. **Evidence**: sanitized campaign evidence is written to
   `pads-paper/results/evidence/<run-id>/` (not this directory's
   `evidence/`) with the ruled structure:

   ```
   pads-paper/results/evidence/<run-id>/
     run.md                     # campaign provenance + {{P6-ANALYSIS:*}} markers
     design.json                # the validated design, verbatim
     eds-state.json             # post-start EDS state
     scenario-status.json       # all Core DB scenario records
     scenarios/<label>/         # one directory per scenario (A1..A8, B1..B3)
       result-rows.json         # that scenario's Result DB rows, verbatim
       scenario-status.json     # that scenario's Core DB record
       assertions.txt           # that scenario's assert-results output
       replay-report.json       # that scenario's replay report
   ```

   `run.md` is a skeleton at write time; the `{{P6-ANALYSIS:EXPERIMENT-
   C}}`, `{{P6-ANALYSIS:ILLUSTRATION}}`, and `{{P6-ANALYSIS:ANALYSIS-
   ENV}}` markers are completed offline by
   `pads-paper/results/analysis/analyze.py` at settlement.
   Sanitization follows the identical P5-R4 rules below.

### 4. Validate and replay (offline, no cluster)

```bash
# exact contract validation of exported rows; --rows and --context are
# file paths (the design context file carries the five baked scenario
# fields plus number_of_reps, number_of_computed_reps, and
# confidence_metric)
python3 pads-paper/acceptance/assert-results.py \
  --rows <result-rows.json> \
  --context <assert-context.json> \
  --summary-out <verdict-summary.json>

# exact-value replay of the recorded effective seeds
python3 pads-paper/acceptance/replay-seeds.py \
  --rows <result-rows.json> \
  --report <replay-report.json>

# offline self-tests (no SimPy required; SimPy-gated tests skip)
python3 -m unittest -v   # run from this directory
```

`assert-results.py` validates the exact `erlang_a_v1` 15-field contract
against the design context (the five baked scenario fields plus
`number_of_reps`, `number_of_computed_reps`, and `confidence_metric`),
effective-seed distinctness, computed-replication count consistency, and
per-row internal consistency (integer fields, range bounds, and the count
identities). It prints a PPS-style informational verdict summary (sample
mean / half-width / met-not-met) and can write it as JSON via
`--summary-out`, but **never** mutates or rewrites rows.

`replay-seeds.py` re-runs the canonical model
(`pads-paper/simulation-model/src/model.py`) for each recorded
`effective_seed` and requires **exact** binary-float equality on every
result field and exact count equality. It reports per-row mismatches and
exits non-zero on any failure. SimPy 4.1.2 is required (the model's
contract); the import is lazy so `test_acceptance.py` can skip cleanly
when SimPy is absent.

## Evidence and sanitization policy

`run-experiment.sh` writes two bundles per run:

- **Raw** (never committed): `artifacts/pads/<run-id>/` — the exact
  exported rows, scenario record(s), CR status, EDS state,
  assert/replay outputs, the rendered manifests, and logs.
  `artifacts/` is gitignored.
- **Sanitized** (the only thing that may be committed):
  - default mode: `pads-paper/acceptance/evidence/<run-id>/` —
    `run.md` (a self-describing summary), `result-rows.json`,
    `scenario-status.json`, `eds-state.json`, `replay-report.json`,
    and `assertions.txt`;
  - design mode (P6): `pads-paper/results/evidence/<run-id>/` with the
    per-scenario structure documented above (P6-R2).

The sanitization is deterministic, applied by the script itself (not by
hand) to every committed evidence file. Rules, in order:

| # | Rule | Effect |
|---|---|---|
| R1 | Strip the exact `CBSE_REGISTRY` prefix | Registry host + project path removed from image references, leaving `<repo>@sha256:<digest>`. |
| R2 | Redact the kubeconfig path | The `KUBECONFIG` path value becomes `<redacted-kubeconfig>`. |
| R3 | Redact URLs | Any `http(s)://…` token becomes `<redacted-url>`. |
| R4 | Redact IPv4 endpoints | Any IPv4 address (optionally `:port`) becomes `<redacted-ip>`. |
| R5 | Strip registry hosts from image refs | A host-qualified reference `host/repo@sha256:…` becomes `repo@sha256:…` (the host's last label must contain a letter, so version decimals are untouched). |
| R6 | Redact remaining bare FQDN host tokens | Any remaining dotted host token becomes `<redacted-host>` (same alphabetic-label rule, so `4.1.2`, `0.05`, and float statistics are preserved). |

Consequences (all verified by the offline dry-run in this slice):
- Image references in committed evidence appear as
  `<repository-name>@<digest>` — **never** with a registry host.
- Cluster IPs, endpoints, registry names, and kubeconfig paths do not
  appear in committed evidence.
- Numeric result statistics, parameter values, and version strings are
  preserved verbatim (the sanitization targets infrastructure, not
  science).

**No fabricated run data.** Both evidence directories ship with
placeholder READMEs only. Evidence is only ever produced by a live run
of `run-experiment.sh` at settlement time (default mode or design
mode); no file in this slice may be hand-written to look like run
output.

## Verification (offline, no cluster, no image build, no smoke run)

All of the following run without cluster access:

```bash
bash -n pads-paper/acceptance/run-experiment.sh
bash -n pads-paper/acceptance/publish-translator-image.sh
# shellcheck, if installed (skipped when absent — never a hard gate):
command -v shellcheck >/dev/null && shellcheck pads-paper/acceptance/*.sh
cd pads-paper/acceptance && python3 -m unittest            # no SimPy: gated tests skip
cd ../results/analysis && python3 -m unittest              # no SimPy/matplotlib: gated tests skip
# with a SimPy 4.1.2 + pinned-matplotlib venv (proves the replay and
# figure byte-stability paths):
<venv>/bin/python -m unittest -v   # from each directory
<venv>/bin/python pads-paper/results/analysis/plot-figures.py --help
git diff --check
make test-fast
```

Refusal-path proofs are done by pointing `run-experiment.sh` at a
synthetic kubeconfig and a stub `kubectl` that only answers read-only
calls, and asserting:
- with any required environment value missing, the script exits non-zero
  **before** any mutating call;
- with the wrong `current-context`, it refuses to mutate a different
  cluster;
- with non-digest image references, it refuses;
- with SimPy absent or a non-4.1.2 version, it refuses;
- with a malformed `--design` file (duplicate entry values, unknown
  keys, unknown fields, bad scenario references, bad replications or
  epsilon), it refuses before any cluster mutation, and with
  `--design` combined with `--rows/--context/--out`, it refuses the
  argument conflict;
- with a non-integer or non-positive `PADS_CAMPAIGN_TIMEOUT`, it
  refuses;
- the full single-scenario dry-run (stub cluster) completes and produces
  correctly sanitized evidence, with teardown ordered (experiment
  before namespace);
- the full design-mode dry-run (stub cluster, over
  `pads-paper/results/campaign/design.json`) completes and produces the
  ruled per-scenario evidence structure under
  `pads-paper/results/evidence/` with the same sanitization, the
  analysis markers in `run.md`, and the same teardown ordering.

## Limitations (this slice)

- No live cluster run, image build, or smoke run is performed here; the
  live run and the live campaign are owned by settlement.
- `shellcheck` is not installed in the worker environment; the scripts
  are `bash -n` clean and the refusal paths are proven offline. This is
  a recorded limitation, not a passed receipt.
- The evidence bundles committed by this slice are placeholder-only
  (default mode: `evidence/` here; campaign: `pads-paper/results/
  evidence/`).
