# Slice D5 — Dual-installer reproducibility acceptance

Normative and self-contained. Read order: [../FEATURE.md](../FEATURE.md) §§8–10;
settled D1–D4 code and evidence; root `AGENTS.md`; then this slice. Specs are
read-only. Stop and ask on unresolved contradiction.

This is wave W4 and depends on D4.

## Target

Sole ownership of:

- `test/e2e/smoke_test.go`, adding assertions to existing specs only;
- `test/harness/smoke.sh` only for safe deterministic-seed artifact plumbing;
- new seed-manifest comparison/validation scripts under `test/harness/`;
- `test/harness/test-harness.sh` fake/self-tests for those scripts/artifacts;
- root `Makefile` only for a convenience comparison target if needed;
- `test/e2e/README.md` only for test-mode/artifact descriptions, not product
  guidance.

Consume without changing: API/CRD, application code, runner-base, Translator,
chart, image locks, experiment manifest, statistical thresholds, EDS scenario
flow, and the 15-spec suite shape. No `devlog/**`.

## Change

### D5.1 Independent expected-seed calculation

Add a small test-only Go helper in `smoke_test.go` using Go standard-library
`crypto/hmac`, `crypto/sha256`, and `encoding/binary` to calculate the exact
FEATURE.md §7.5 expected seed. It must not invoke Python production code or copy
production output fixtures at runtime.

Pin the canonical experiment root seed from the rendered CR (`123456789`) and
read each scenario seed from the Detail DB `seed_policy` view column.

### D5.2 Strengthen existing result assertions

Without adding/removing/reordering Ginkgo specs, extend the existing result and
convergence assertions to prove for every final scenario:

- row count equals `number_of_computed_reps`;
- table `replication_index` values are exactly contiguous `0..N-1`;
- JSON `replication_index` equals the table column;
- `root_seed` equals the CR root seed;
- `scenario_seed == seed_policy` and equals the Detail DB value;
- `seed_derivation == cbse-hmac-sha256-u63-v1`;
- `rng_algorithm == python-random-mt19937`;
- every effective seed equals the independent Go calculation;
- all effective seeds for different indices within one scenario are distinct;
- scenarios that reuse the same recipe/scenario seed have equal effective seeds
  at equal replication indices, proving explicit common-random-number behavior;
- adaptive loop-path scenarios remain contiguous across runner rounds, proving
  the offset continued instead of resetting.

Preserve all existing model/KPI/statistical assertions and timeouts unless a
real defect is separately escalated. Do not replace exact seed checks with mere
non-empty/distinct checks.

### D5.3 Seed manifest artifact

At the terminal successful point, write
`${CBSE_ARTIFACT_DIR}/seed-manifest.json` with stable JSON ordering and only
non-secret scientific data:

```json
{
  "root_seed": 123456789,
  "seed_derivation": "cbse-hmac-sha256-u63-v1",
  "scenarios": [
    {
      "ordinal": 0,
      "priority": 1,
      "parameterset_id": 5,
      "scenario_seed": 7001,
      "replications": [
        {"replication_index": 0, "effective_seed": 123}
      ]
    }
  ]
}
```

Normalize away namespace, experiment name/UID, Scenario Manager row ID, Job/Pod
names, timestamps, database endpoints, image repository names, and credentials.
Scenario ordinal is deterministic Core DB insertion order within the test's
fixed EDS sequence. Sort replications by index.

The artifact is diagnostic output under ignored `artifacts/`, never committed.

### D5.4 Wave-partition acceptance

Add a cluster-free acceptance helper/test that calls the actual D3
`cbse_random` Facade with equivalent partitions, for example:

```text
one wave:       offset 0, completion indices 0..69
partitioned:    offset 0, indices 0..29; offset 30, indices 0..39
```

The normalized `(replication_index, effective_seed)` sequences must be
byte-identical. Include same-index retry reconstruction with identical inputs
and missing/malformed fail-closed checks. This supplements, not replaces, the
live adaptive-round assertions.

### D5.5 Recreation comparison

Add a strict comparison script that accepts two `seed-manifest.json` paths,
validates their schema/types/order/contiguity, and exits zero only when their
normalized scientific stream assignments are identical. It must fail on any
root, derivation, scenario seed, ordinal, replication index, or effective-seed
difference and print only a credential-free structural diff.

Harness self-tests cover equal, malformed, missing, and each meaningful mismatch
case.

### D5.6 Live dual-installer proof

Publish one fresh immutable image set from the exact reviewed feature tree. Run
both installers with that same resolved digest set and `SKIP_BUILD=1`:

1. manifest smoke;
2. Helm smoke.

Use different run IDs so Kubernetes namespace, CR UID, Job names, Pod names, and
hostnames differ. Both runs use the same root seed, EDS design, and image
digests. Compare their seed manifests with the new script. Equality proves
infrastructure recreation does not change stream assignment.

Both runs must use the unchanged 15-spec suite and perform clean teardown.

## Constraints

- Do not fork the smoke suite or create a special deterministic-seed deployment.
- Spec count remains exactly 15.
- Do not change EDS scenarios, confidence metrics, PPS cap, or model parameters
  merely to force desired statistics.
- Do not commit raw artifacts or seed manifests.
- Seed artifacts must contain no private registry, cluster endpoint, kubeconfig
  path, database endpoint, Secret, or image repository.
- Use a fresh test image version; never repush an existing immutable test tag.
- Explicit `KUBECONFIG` and expected context are mandatory.
- Do not delete shared CRD/helper namespaces or bypass harness cleanup.
- `make test-fast`, manifest smoke, Helm smoke, and manifest comparison are all
  mandatory.
- Commit/attestation/hygiene rules apply.

## Ownership

D5 owns acceptance only. Any application defect routes to a fixes task in D1–D4
ownership; do not silently fix application code in this slice.

## Observable acceptance

1. Runtime attestation is `ai.forge/qwen3.8-27b-nvfp4`.
2. Diff containment is Target paths only; suite remains 15 specs.
3. Targeted e2e compile/vet and new test-only derivation helper tests succeed.
4. Harness self-tests prove seed-manifest writing/comparison positive and
   negative cases, with no sensitive output.
5. Actual-base wave-partition/retry/fail-closed acceptance passes.
6. `make test-fast` succeeds.
7. One fresh immutable eight-image set is published; record version and digests
   without exposing registry credentials.
8. Manifest smoke passes 15/15 with clean teardown and a valid seed manifest;
   record run ID.
9. Helm smoke passes 15/15 with clean teardown and a valid seed manifest;
   record run ID.
10. Both run artifacts prove different namespaces/experiment UIDs while using
    the same root seed and image digests.
11. Comparison script reports the two normalized seed manifests identical.
12. Each live manifest proves contiguous indices, exact independent seeds, and
    multi-round offset continuation for loop-path scenarios.
13. No Secret/endpoint/private-infrastructure data appears in the new artifacts
    or added lines.
14. Commit HEAD and orchestration trailers are reported.

Completion requires one `worker_done` with evidence 1–14, files, report, image
version/digests, both run IDs, and comparison receipt. Either unavailable smoke
or absent comparison is `verification-blocked`.
