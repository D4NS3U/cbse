# Slice H5 — Existing full smoke through the Helm installer

Normative and self-contained. Read order: [../FEATURE.md](../FEATURE.md) §3 D14, §5–§7; settled H1–H4 slices and chart; root `AGENTS.md`; then this slice. Specs are read-only. This is serial wave W4.

## Target

Sole ownership of:

- `test/harness/smoke.sh`
- `test/harness/preflight.sh` — installer/tool validation only
- `test/harness/diagnose.sh`
- `test/harness/clean.sh` — Helm-release-aware cleanup layered on H4's CRs-first order
- `test/harness/test-harness.sh`
- `test/e2e/smoke_test.go` — only shared-stack workload lookup abstractions required by Deployment vs StatefulSet
- `test/e2e/README.md` — test-mode description only (product install docs belong H6)
- root `Makefile` — `test-helm-smoke` convenience target/help/phony only

Consume without changing: chart, image-values generator, lifecycle hooks, image locks, experiment manifest, application code. No `devlog/**`.

## Installer selection contract

New environment variable:

- `CBSE_STACK_INSTALLER=manifest|helm`
- default `manifest` preserves the current path and behavior;
- any other value fails before cluster mutation;
- `make test-helm-smoke` invokes the existing `test-smoke` recipe with `CBSE_STACK_INSTALLER=helm`;
- `make test-e2e-retained` honors the caller's installer selection, so `CBSE_STACK_INSTALLER=helm make test-e2e-retained ...` retains a Helm-installed interactive stack.

Both modes share one flow after stack readiness: same namespace/project, same digest images, same rendered `SimulationExperiment`, same Ginkgo binary/specs, same assertions, same artifacts/summary, same ownership and Lease.

## Helm installation path

After preflight, Lease, image preparation, namespace creation/labels, and installation-owner `cbse-registry-auth` copy:

1. Do **not** create `core-db-credentials`; chart owns it.
2. Do **not** patch the namespace default ServiceAccount; H2 made every required pull reference explicit.
3. Generate `${artifact_dir}/helm-values.yaml` using H4's generator from this run's `images.env` plus locked NATS/Postgres/hook images.
4. Preserve the existing CRD safety check:
   - existing CRD must store/serve alpha4 only and satisfy the existing ownership/upgrade rule;
   - if absent, Helm's `crds/` installs it;
   - Helm mode never separately applies the CRD before install;
   - manifest mode keeps the existing server-side apply path.
5. `helm upgrade --install cbse charts/cbse --namespace <run-ns> --values <artifact-values> --wait --atomic --timeout 5m` using pinned Helm.
6. Verify release status `deployed`, Operator and SM Deployments Ready, NATS/Core DB StatefulSets Ready, and SM `/readyz` reflected by Pod readiness. No log-grep readiness in Helm mode.
7. Write safe artifacts: `stack-installer.txt`, `helm-values.yaml`, `helm-status.txt`, `helm-history.txt`, object inventory. Never write `helm get manifest` or Secret output.

Manifest mode remains byte-/behavior-compatible except removal of the default-SA patch and use of explicit pull Secrets from H2. It retains `stack.yaml` rendering and the existing readiness path (it may adopt SM `/readyz`, but must not be weakened).

## E2E shared-stack abstraction

The Ginkgo suite currently execs Core DB as `deployment/core-db`. Replace this installer-specific assumption with a deterministic helper that resolves exactly one Ready Pod by stable label `app=core-db`, fails on zero/multiple Ready Pods, and execs the selected Pod. Do not alter SQL, scenarios, timings, or assertions.

Search and remove every remaining assertion/helper that assumes Core DB or NATS is a Deployment. Per-experiment DBs remain Deployments and are unchanged.

## Teardown and retained runs

- Non-retained Helm run: EXIT trap diagnoses; deletes all SimulationExperiments with wait while SM is alive; runs `helm uninstall cbse --wait --timeout 3m`; confirms release absent; deletes namespace; releases Lease.
- Retained Helm run: leaves release, namespace, experiments, PVCs, and Lease behavior exactly per retained-mode contract; prints run ID and installer.
- Failure retention follows `CBSE_KEEP_ON_FAILURE`.
- `test-clean RUN_ID=<id>`:
  1. ownership-label check;
  2. delete SimulationExperiments and wait;
  3. if release `cbse` exists in the namespace, uninstall it;
  4. delete namespace;
  5. release Lease.
- The chart's pre-delete guard should pass because CRs are already gone; a failure is surfaced, never ignored.

## Diagnostics

For Helm runs, add without credential leakage:

- `helm status cbse -n <ns> --show-resources`;
- `helm history cbse -n <ns>`;
- StatefulSet/PVC inventory and Pod describe/logs already gathered generically;
- no Secret YAML/data, no Helm manifest, no decoded release Secret.

Manifest diagnostics stay unchanged.

## Change

1. Add installer validation/selection and Make convenience target.
2. Refactor shared setup only enough to select manifest apply vs Helm install; preserve all common behavior.
3. Remove harness creation of `core-db-credentials` and default-SA patch **only in Helm mode** initially; after proving H2 explicit pulls, remove default-SA patch from both modes. Manifest mode continues to create Core DB credentials because its stack manifest references them.
4. Make Core DB e2e lookup installer-independent.
5. Add Helm-safe diagnosis, CRs-first Helm uninstall, retained cleanup.
6. Extend fake-tool harness self-tests for both paths, expected command order, invalid installer refusal, no Secret/manifest artifact, and failure propagation.
7. Update e2e README with the two installer modes and exact command surface; no private default additions.

## Constraints

- One suite, not a fork. `smoke_test.go` coverage and 15-spec count remain intact.
- No weakened/skipped/reordered Ginkgo behavior except existing retained GC skip by design.
- Manifest remains default until a later explicit cutover ruling.
- No image build/registry fallback changes; all chart images digest-pinned.
- No private strings added. Existing C1-deferred strings are not this slice's cleanup vehicle.
- No deployment to `default`/`kube-system`, no CRD deletion, no Secret payload artifacts.
- Harness/manifests require `make test-fast` plus both manifest and Helm smoke receipts. If cluster lock permits only serial runs, run manifest then Helm; neither substitutes for the other.
- Branch/commit/attestation constraints apply.

## Ownership

Only Target paths. Any chart/application defect found by Helm smoke is a report line for a fixes task in the owning slice; do not cross partitions silently.

## Observable acceptance

1. Attestation → mandated model.
2. Containment: Target paths only.
3. `bash -n` for all changed harness scripts → rc=0.
4. `make test-fast` → rc=0; fake harness proves both installer command flows and teardown order.
5. E2E compile/vet → rc=0; spec count remains 15; only shared Core DB lookup changed.
6. `make test-smoke ...` (default manifest) → 15/15, rc=0, clean teardown.
7. `make test-helm-smoke ... HELM_IMAGES_ENV=<digest source if required by final Make contract>` → 15/15, rc=0, release uninstalled, namespace/Lease/simexps clean.
8. Artifact receipt from Helm run: safe files present; no Helm manifest or Secret data artifact.
9. Retained dry/control receipt: fake self-test or live retained run proves Helm release remains and `test-clean` performs CR delete → Helm uninstall → namespace delete.
10. Private added-line scan empty; commit HEAD/trailers and run IDs.

Completion requires one `worker_done` with explicit outcome, IDs, attestation, all evidence, files, report. Both cluster runs are mandatory; unavailable cluster verification is `verification-blocked`.
