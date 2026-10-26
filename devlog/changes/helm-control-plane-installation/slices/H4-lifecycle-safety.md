# Slice H4 — Helm lifecycle safety, release values, and cluster acceptance

Normative and self-contained. Read order: [../FEATURE.md](../FEATURE.md) §3 D5–D8/D10–D13, §5, §6 H4, rulings H2/H4/H5/H6, Secret inventory; settled H1–H3 slices and live chart; root `AGENTS.md`; then this slice. Specs are read-only.

This is serial wave W3 after H2+H3 are reviewed and landed.

## Target

Sole ownership of:

- `charts/cbse/templates/hooks/` — pre-install/pre-upgrade registry-Secret validation and pre-delete experiment guard, including hook ServiceAccount/RBAC
- `test/harness/generate-helm-values.sh`
- `test/harness/helm-lifecycle.sh`
- `test/harness/clean.sh` — CRs-first correction only
- `test/harness/image-lock.sh`, `test/e2e/images.lock.env`, `test/harness/preflight.sh` — NATS and hook-kubectl source-image locks/validation only
- `test/harness/test-harness.sh` — lifecycle/value-generator/cleaner self-test truths
- `test/helm/assert-lifecycle.sh`
- root `Makefile` — `helm-image-values` and `test-helm-lifecycle` targets/help/phony entries only

No application Go/controller changes, no H1 values/schema/helper edits, no H2/H3 templates, no main smoke installer switch (H5), no docs, no `devlog/**`.

## Locked auxiliary images

Extend `test/e2e/images.lock.env` and `image-lock.sh` with:

- `NATS_IMAGE` + `NATS_VERSION`, resolving the existing supported source `docker.io/library/nats:2.11-alpine` to an immutable manifest digest;
- `HELM_HOOK_KUBECTL_IMAGE` + `HELM_HOOK_KUBECTL_VERSION`, resolving `registry.k8s.io/kubectl:v1.32.5` (matching repository `KUBECTL_VERSION`) to an immutable manifest digest.

The worker records the exact resolved digests and platform/manifest evidence. No floating tag enters rendered release values. Preflight verifies both lock pairs exactly like existing image/version pairs.

## Release image-values generator

`generate-helm-values.sh` produces the private/untracked installation file ruled by H6.

Inputs:

- `IMAGES_ENV` — readable file containing full digest-pinned `OPERATOR_IMAGE` and `SM_IMAGE` (the smoke `images.env` format; extra keys ignored);
- `OUTPUT` — destination path, must not be inside a tracked repository path unless the caller explicitly chooses an ignored artifact directory;
- repository image lock supplies `NATS_IMAGE`, `POSTGRES_IMAGE`, `HELM_HOOK_KUBECTL_IMAGE`.

Output YAML maps full image references into repository/digest pairs for exactly:

- `operator.image`
- `scenarioManager.image`
- `nats.image`
- `coreDatabase.image`
- `hooks.kubectlImage`

Rules:

- Every input must be `<non-empty-repository>@sha256:<64 lowercase hex>`; tags, empty values, duplicate `@`, or malformed digests fail before writing.
- Write atomically with mode 0600. It contains no credentials, but repositories may be operational/private.
- Never print full private repository values to stdout; success prints only output path and component count.
- No registry default or fallback.
- `make helm-image-values IMAGES_ENV=<path> OUTPUT=<path>` is the supported entry point.

## Hook contract

Use the pinned kubectl image and dedicated hook ServiceAccount/Role/RoleBinding. Hook resources carry deterministic weights so RBAC exists before Jobs and use `before-hook-creation,hook-succeeded`; failed Jobs remain for diagnosis until the next attempt.

### Registry validation (`pre-install,pre-upgrade`)

- Read only Secret `cbse-registry-auth` in `.Release.Namespace`.
- Fail with one actionable line when absent.
- Require `type == kubernetes.io/dockerconfigjson` and non-empty `.data[".dockerconfigjson"]`; do not decode or print it.
- Never label, annotate, adopt, patch, or delete the Secret.
- The hook ServiceAccount deliberately has **no** `imagePullSecrets`: the validator must be able to run when `cbse-registry-auth` is absent. Therefore the locked hook image must be pullable without that Secret (the official public source in v0.1); air-gapped/private-mirror hook bootstrapping is future work.

### Experiment guard (`pre-delete`)

- Read-only list of `simulationexperiments.experiment.cbse.terministic.de` in the release namespace.
- Zero objects → success.
- One or more → non-zero exit and exact recovery guidance: delete all SimulationExperiments in the release namespace with `--wait`, then rerun `helm uninstall`.
- Never deletes/patches CRs, finalizers, controller resources, PVCs, Secrets, or CRD.
- RBAC: named registry Secret get; SimulationExperiments list only (get is optional only if the script needs names). No wildcard verbs/resources.

## Helm lifecycle harness

`helm-lifecycle.sh` follows the repository cluster-safety contract:

- explicit `KUBECONFIG`, pinned `KUBECTL`, pinned `HELM`, dedicated-cluster preflight, smoke Lease, generated unique `cbse-e2e-<run-id>` namespace, ownership labels, privileged Pod Security label, artifacts under `artifacts/test/<run-id>/`;
- copies `cbse-registry-auth` from the configured helper namespace exactly as smoke does, without logging payload;
- uses `generate-helm-values.sh` from a digest-pinned `images.env` supplied by `HELM_IMAGES_ENV`; no builds in this slice;
- test sequence:
  1. attempt install before registry Secret exists → expected hook failure and no running release;
  2. create/copy Secret, install `cbse` with `--wait --atomic` and release values;
  3. record Core DB credential Secret data hashes (never contents), PVC identities, StatefulSet revisions;
  4. create a marker table/row in Core DB without echoing credentials; delete NATS/Core DB Pods; wait Ready; prove marker survives;
  5. `helm upgrade --install` with identical values; prove credential hashes and PVC identities unchanged;
  6. render/apply one valid digest-pinned `SimulationExperiment` fixture from existing smoke images; immediately attempt uninstall → expected pre-delete guard failure while shared stack remains Ready;
  7. delete the CR with `--wait` while SM is alive; rerun uninstall → success;
  8. assert CRD remains; chart workloads/RBAC/ConfigMaps/Services/hooks are gone; ruled retained PVCs and `core-db-credentials` existed after Helm uninstall;
  9. delete the harness-owned namespace (which removes retained test data), release Lease.
- EXIT trap performs CRs-first deletion, then best-effort Helm uninstall, then namespace deletion, then Lease release. `CBSE_KEEP_ON_FAILURE=1` retention follows existing semantics.
- Artifacts contain Helm status/history, object inventories, hashes, and command rc—not `helm get manifest` (it contains Secret data), decoded Secrets, or credential-bearing environment.

## `clean.sh` correction

Before deleting a harness-owned namespace, delete all `SimulationExperiment` resources in it and wait up to 120s for SM finalizers, then delete the namespace. Preserve ownership-label refusal and Lease release. This closes the known CRs-first gap already fixed in `smoke.sh`.

## Change

1. Lock NATS and hook-kubectl source images and extend lock/preflight tests.
2. Implement atomic digest-only Helm values generation.
3. Render hook RBAC/Jobs exactly above.
4. Implement lifecycle harness and Make targets.
5. Correct `clean.sh` teardown ordering.
6. Add static/self-tests and `assert-lifecycle.sh` for hook annotations, weights, least privilege, locked digest images, Secret non-ownership, and no destructive command in guard scripts.

## Constraints

- Hooks never expose Secret payloads or take ownership of `cbse-registry-auth`.
- A failed pre-delete guard must leave SM/Operator/NATS/Core DB running.
- No `helm.sh/resource-policy: keep` on installation-owner `cbse-registry-auth`; only Helm Core DB Secret/data policy.
- Lifecycle script may mutate only its harness-owned namespace plus smoke Lease/helper namespace read/copy operations already authorized by the harness.
- No `default`/`kube-system` deployment and no CRD deletion.
- `make test-fast` and `make test-helm-lifecycle` are mandatory; chart/Kubernetes/harness changes also require a manager/reviewer smoke receipt per `AGENTS.md` (current manifest smoke is acceptable for regression here; H5 later proves Helm full smoke).
- Branch commits/trailers and global constraints apply.

## Ownership

Only Target paths. H5 will consume, not rewrite, the value generator and lifecycle contracts.

## Observable acceptance

1. Attestation → mandated implementation model.
2. Containment: Target paths only.
3. Shell syntax for all changed scripts → rc=0.
4. `make test-helm-chart` and `make test-fast` → rc=0.
5. Generator positive receipt plus negative tests for tags/malformed/missing refs; output mode 0600; private added-line scan empty.
6. Hook render receipt: annotations/weights, exact RBAC, digest kubectl image, no delete/patch verbs, no Secret payload decoding/output.
7. `make test-helm-lifecycle KUBECONFIG=<dedicated> HELM_IMAGES_ENV=<digest-file> ...` → rc=0 with evidence for expected failed first install, persistence, stable credentials, expected blocked uninstall, successful final uninstall, CRD retention.
8. `make test-smoke ...` regression receipt → 15/15, or explicit manager-owned settlement reference if serialization requires it.
9. `clean.sh` self-test proves CR delete occurs before namespace delete and Lease still releases.
10. Commit HEAD/trailers and artifact run ID/report path.

Completion: one `worker_done` with explicit outcome, IDs, attestation, evidence, files, report; mandatory unavailable cluster gates mean `verification-blocked`, not succeeded.
