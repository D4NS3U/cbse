# P5 — End-to-end acceptance

## Target

Prove the paper-local `erlang_a_v1` chain live on the test cluster and persist the evidence in the repository: publish a fresh immutable paper-local Translator image, run one EDS-driven experiment whose generated runners produce `erlang_a_v1` result rows through the existing PPS adaptive loop, validate the richer result contract, replay recorded effective seeds against the canonical model, and commit sanitized evidence under `pads-paper/acceptance/evidence/<run-id>/`.

The PADS experiment driver is paper-local tooling; the shared harness (`test/harness/**`, `test/e2e/**`) is not modified, and the unchanged 15-spec smoke is re-run at settlement for the slice's own receipt.

## Dependency

- P4 is independently approved and landed at `462f1fc06a3acc26e4d9bcc079814fb473551ce6`.

## Requirements

### P5-R1 — Acceptance tooling location and ownership

Create the tracked directory `pads-paper/acceptance/` containing:

- `README.md` — ownership, usage, environment contract, evidence policy;
- `publish-translator-image.sh` — build and push the paper-local Translator image;
- `run-experiment.sh` — the live end-to-end driver;
- `assert-results.py` — pure validation of exported result rows;
- `replay-seeds.py` — recorded-effective-seed replay against the canonical model;
- `test_acceptance.py` — offline unit tests for the two Python tools;
- `evidence/` — committed, sanitized evidence directory (populated at settlement).

Scripts are executable (mode 100755); Python tools run under system `python3` without third-party dependencies except that `replay-seeds.py` needs SimPy 4.1.2 at live-replay time (its self-tests skip cleanly without it, mirroring the repository conformance convention).

### P5-R2 — Paper-local Translator image publication

`publish-translator-image.sh` mirrors the repository build contract for the paper-local Translator:

- required environment: `CBSE_REGISTRY`, `CBSE_REGISTRY_AUTH_FILE`, `TEST_IMAGE_VERSION` (validated `YY.M.D` grammar like the harness); no default registry value is embedded;
- source image: the locked `TRANSLATOR_GO_BUILDER_IMAGE` digest from `test/e2e/images.lock.env` (no override);
- build context: `pads-paper/translator/component` with its inherited Dockerfile; `--platform linux/amd64`; push both a canonical `<CBSE_REGISTRY>/translator:<version>` tag and an immutable provenance tag `<version>.sha-<commit>-<source-hash>-<run-id>` exactly in the harness convention;
- registry authentication through an isolated `DOCKER_CONFIG` seeded from `CBSE_REGISTRY_AUTH_FILE` (never printed);
- output: one `PADS_TRANS_IMAGE=<repo>@<digest>` line written to a caller-supplied artifact file (default `artifacts/pads/<run-id>/pads-translator.env`, untracked);
- no cluster access; no tag-only references in outputs.

### P5-R3 — Live experiment driver

`run-experiment.sh` drives one complete experiment with the paper-local Translator. It must:

**Preflight (before any cluster mutation):**

- require `KUBECONFIG`, `CBSE_EXPECTED_CONTEXT`, `CBSE_REGISTRY`, and the image environment `OPERATOR_IMAGE`, `SM_IMAGE`, `EDS_REFERENCE_IMAGE`, `PPS_IMAGE`, `DETAIL_DB_IMAGE`, `PADS_TRANS_IMAGE`, plus `CBSE_RUNNER_REPOSITORY` (default derived as `${CBSE_REGISTRY}/cbse-test-runner`); source the locked `BUILDER_IMAGE`/`POSTGRES_IMAGE` from `test/e2e/images.lock.env`;
- verify the current kube context equals `CBSE_EXPECTED_CONTEXT` and refuse otherwise;
- generate a run id (`pads-<timestamp>-<rand>`), namespace `cbse-pads-<suffix>`, and untracked artifact dir `artifacts/pads/<run-id>/`.

**Deployment:**

- create and label its own namespace (`cbse.terministic.de/managed-run=<run-id>`, `app.kubernetes.io/part-of=cbse-pads`, the same pod-security labels the smoke namespace uses — the BuildKit sidecar needs them);
- read the existing `cbse-registry-auth` pull Secret from `cbse-test-system` (read-only copy into the namespace, exactly as the smoke does) and patch the default ServiceAccount;
- render `test/e2e/manifests/base` via kustomize with the same substitutions the smoke uses (`CBSE_NAMESPACE`, `CBSE_RUN_ID`, operator/SM images, project), apply, and wait for the `core-db`, `sm-eds-nats`, `experiment-operator`, `scenario-manager` rollouts plus the Scenario Manager ready log line;
- render `test/e2e/manifests/experiment.yaml` with `CBSE_TRANS_IMAGE=$PADS_TRANS_IMAGE`, the EDS reference, PPS, Detail DB, and locked Postgres images, the runner repository, a generated database password, and apply; wait for the experiment to reach `InProgress`.

**EDS-driven design:**

- wait for the EDS deployment readiness, then drive the EDS JSON API exactly as implemented in `pads-paper/experimental-design-service/component/internal/server/server.go` (POST `/api/entries`, `/api/recipes`, `/api/scenarios`, `/api/save`, `/api/start`; GET `/api/state`) via `kubectl port-forward`;
- create one design with entries `arrival_rate=4`, `service_rate=4`, `run_duration=500`, `seed_policy=2027`, one recipe over those four entries, and one scenario: priority 5, `number_of_reps=5`, `confidence_metric=0.05`, statistical evaluation;
- record the post-start `/api/state` response.

**Completion and export:**

- poll until the scenario reaches its terminal `Finished` verdict state (timeout 40m), exporting at minimum: every Result DB row of the scenario, the Core DB scenario row including its computed-replication and evaluation fields exactly as the scenario-manager schema defines them, the experiment CR status, the generated runner image reference reported by the chain, and the EDS final state;
- run `assert-results.py` over the exported rows and `replay-seeds.py` (with SimPy 4.1.2 available) over every row; a failure fails the run;
- write sanitized evidence to `pads-paper/acceptance/evidence/<run-id>/` (P5-R4) and raw exports to the untracked artifact dir.

**Teardown:**

- an EXIT trap deletes the `SimulationExperiment` CRs with `--wait` while the Scenario Manager is still alive, then deletes the namespace; `CBSE_KEEP_ON_FAILURE=1` skips teardown for active debugging;
- never touch `cbse-test-system`, the shared CRD, or any resource the script did not create.

### P5-R4 — Persisted evidence contract

Evidence is committed on the branch (the user-ruled persistence requirement). `pads-paper/acceptance/evidence/<run-id>/` must contain:

- `run.md` — run id, UTC date, branch commit and dirty state, cluster context name, the exact image digests used (scrubbed per P5-R5), the scenario settings, the verdict summary (final sample mean, half-width, verdict, computed replications, additional-runner round count), assertion and replay outcomes, and the unchanged-smoke receipt (run id and 15/15 result) from the same settlement;
- `result-rows.json` — every exported Result DB row verbatim (they contain parameters, effective seeds, and metrics only);
- `scenario-status.json` — the exported Core DB scenario/evaluation record;
- `eds-state.json` — the final EDS `/api/state`;
- `replay-report.json` — per-row replay comparison results;
- `assertions.txt` — the assertion output.

Every evidence file must be free of credentials, registry hostnames, cluster endpoints/URLs, node/pod hostnames, and kubeconfig paths. Image references are recorded as `<repository-name>@<digest>` without the registry host. The sanitizer redacts deterministically (the `CBSE_REGISTRY` prefix plus generic host/URL patterns) and its rules are documented in the acceptance README. Raw unsanitized exports stay untracked under `artifacts/pads/`.

### P5-R5 — Result assertions

`assert-results.py` validates the exported rows against the ruled contract and exits non-zero on any violation:

- every row has exactly the 15 `RESULT_FIELDS` keys with the ruled types (scenario ints, `effective_seed` non-negative int, nine metrics: `model_id == "erlang_a_v1"`, non-negative int counts, finite floats, probability/utilization in `[0,1]`);
- `completed_customers == served_customers` and `served + abandoned == total`;
- `throughput == served / run_duration` and `abandonment_probability == abandoned / total` (zero-safe) within float tolerance;
- `mean_wait_time` finite and non-negative;
- every row's baked scenario parameters equal the experiment's design values;
- effective seeds are distinct across rows and their count matches the Core DB computed-replication count.

Its self-tests cover each violation class with synthetic rows.

### P5-R6 — Recorded-effective-seed replay

`replay-seeds.py` imports the canonical model from `pads-paper/simulation-model/src/model.py` (located relative to the script, no path configuration required), reconstructs `random.Random(effective_seed)` per row, reruns `model.run(arrival_rate, service_rate, run_duration, rng)`, and compares every metric in the row: exact parsed-value equality for counts and probability; float metrics equal within strict binary equality (same seed, same inputs, same code → identical floats). It reports per-row pass/fail and exits non-zero on any mismatch or malformed row. Self-tests generate synthetic rows from the canonical model itself, prove successful replay, and detect deliberately perturbed metrics (skipping gracefully when SimPy is unavailable).

### P5-R7 — Scope and hygiene

- Do not modify anything outside `pads-paper/acceptance/**`.
- Do not modify the shared harness, e2e manifests, EDS/PPS/Translator/Operator/SM code, or devlog specifications.
- The acceptance README must state that live runs are settlement-owned: workers verify the tooling offline; the coordinator executes the cluster runs with the user-confirmed environment and commits the sanitized evidence.
- No credentials, registry hostnames, kubeconfig paths, or cluster endpoints in any tracked file.

## Verification tiers

- **Worker (offline):** `bash -n` on both shell scripts (shellcheck if installed); refusal-path tests (unset required environment → non-zero exit before any cluster mutation); `python3 -m unittest` in `pads-paper/acceptance` (assert/replay self-tests; replay tests fully exercised through a temporary untracked SimPy 4.1.2 venv); repository-root `make test-fast`; `git diff --check`.
- **Settlement (coordinator, cluster):** publish the paper-local Translator image (`TEST_IMAGE_VERSION=26.11.5` or later); re-run the unchanged 15-spec smoke over the published digest set (`SKIP_BUILD=1`, 15/15 required); execute `run-experiment.sh` with the user-confirmed environment; commit the sanitized evidence; record all receipts in the ledger. A settlement with any failed receipt is `verification-blocked`, not landed.

## Observable acceptance (worker lane)

1. `pads-paper/acceptance/` contains exactly the ruled files with executable shell scripts.
2. `bash -n` passes; refusal paths exit non-zero pre-mutation; no cluster access occurs in any worker verification.
3. `python3 -m unittest` passes (with the SimPy venv proving the replay tests fully).
4. `git diff --check` passes; the diff touches only `pads-paper/acceptance/**`.
5. Repository-root `make test-fast` passes.
6. The implementation commit carries the assigned orchestration task/dispatch trailer.

## Completion

P5 is `complete` only after independent Pi/GLM review returns `approve` against the implementation HEAD **and** the settlement cluster runs pass (unchanged smoke 15/15 plus the live PADS experiment with green assertions, replay, and committed sanitized evidence). Only then is the approved child branch and the evidence landed on `PADS-paper`.
