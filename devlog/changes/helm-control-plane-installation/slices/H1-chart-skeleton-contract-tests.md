# Slice H1 — Helm chart skeleton and contract-test foundation

Normative and self-contained for its worker. Read order: [../FEATURE.md](../FEATURE.md), especially §2, §3 D1–D10/D14, §4 (Builder/Facade/installer Strategy), §5, §6 H1, Appendix A, and rulings H1–H8; repository-root `AGENTS.md`; then this slice. All specifications are read-only. On a contradiction that repository inspection cannot resolve, stop and ask via the orchestration ask channel.

This is wave W1. It establishes every cross-slice chart value, name, helper, tool, and test seam. H2 and H3 may not invent new values or edit the shared chart contracts after this slice settles; a missing contract is an escalation to the manager.

## Target

Sole ownership of:

- `charts/cbse/Chart.yaml`
- `charts/cbse/values.yaml`
- `charts/cbse/values.schema.json`
- `charts/cbse/crds/experiment.cbse.terministic.de_simulationexperiments.yaml` — byte-identical copy of the generated alpha4 CRD
- `charts/cbse/templates/_helpers.tpl`
- `charts/cbse/templates/NOTES.txt` — initial bounded message; H6 completes user documentation
- `charts/cbse/.helmignore`
- `charts/cbse/README.md` — skeleton identifying status and linking the normative feature; H6 completes it
- `test/helm/values-render.yaml` — public, non-runnable `example.invalid` fixture with syntactically valid 64-hex digests
- `test/helm/assert-helpers.sh` — shared shell assertion helpers and automatic discovery/dispatch of `test/helm/assert-*.sh` files added by later slices
- `test/helm/assert-skeleton.sh`
- `test/harness/install-helm.sh`
- `test/harness/test-helm-chart.sh`
- `test/harness/verify-generated.sh` — only the chart-CRD lockstep assertion
- `test/harness/test-harness.sh` — only self-tests for the new Helm installer/chart-test truths
- root `Makefile` — Helm tool variables/targets and `test-fast` chart-test wiring

Everything else is out of scope: no component workload templates, no application Go/Dockerfile changes, no smoke/cluster mutation, no docs beyond the two chart skeleton files, no image-lock changes, no `devlog/**` edits.

**Settlement amendment (2026-11-02, manager-licensed before independent review):** H1 additionally owns exactly one `.gitignore` line, `/bin/helm-*`, mirroring the existing `/bin/kubectl-*`; the pinned installer otherwise leaves every tested checkout dirty. No other scope extension is licensed.

## Pinned chart structure and metadata

`Chart.yaml`:

- `apiVersion: v2`
- `name: cbse`
- `type: application`
- initial chart `version: 0.1.0`
- initial `appVersion: "0.1.0"`
- concise description naming the alpha4 CRD, Operator, SM, NATS/JetStream, and Core DB
- home/source links may use only the public repository URL
- no chart dependencies

The chart must be valid with Helm 3. H1 pins repository tooling to **Helm v3.18.4** via `HELM_VERSION ?= v3.18.4`, `HELM ?= $(ROOT)/bin/helm-$(HELM_VERSION)`, and a checksum-verifying installer mirroring `install-kubectl.sh` (host `darwin|linux`, architecture `amd64|arm64`; official `get.helm.sh` tarball and checksum; atomic install; no `sudo`).

## Pinned value surface

H1 declares the complete v0.1 schema. Defaults contain no private infrastructure. Image repository/digest defaults are empty; templates added later use `required` with actionable messages. `test/helm/values-render.yaml` supplies public fake values only for static rendering.

```yaml
global:
  imagePullPolicy: IfNotPresent

operator:
  image: {repository: "", digest: ""}
  resources:
    requests: {cpu: 10m, memory: 64Mi}
    limits: {cpu: 500m, memory: 192Mi}

scenarioManager:
  image: {repository: "", digest: ""}
  runnerStartWorkers: 4
  healthPort: 8080
  resources:
    requests: {cpu: 10m, memory: 48Mi}
    limits: {cpu: 500m, memory: 192Mi}

nats:
  image: {repository: "", digest: ""}
  jetstream:
    maxMemoryStore: 128MB
    maxFileStore: 512MB
  persistence:
    enabled: true
    size: 2Gi
    storageClass: ""
    retain: true
  resources:
    requests: {cpu: 10m, memory: 32Mi}
    limits: {cpu: 500m, memory: 256Mi}

coreDatabase:
  image: {repository: "", digest: ""}
  database: scenarios
  persistence:
    enabled: true
    size: 5Gi
    storageClass: ""
    retain: true
  resources:
    requests: {cpu: 25m, memory: 96Mi}
    limits: {cpu: 750m, memory: 384Mi}

hooks:
  kubectlImage: {repository: "", digest: ""}

networkPolicies:
  enabled: true
```

There is deliberately:

- no configurable release/watch namespace (Helm `.Release.Namespace` is authoritative);
- no configurable resource names;
- no registry Secret value (the name is fixed `cbse-registry-auth`);
- no Core DB username/password or existing-Secret value;
- no NATS username/password/TLS value;
- no external-service branch;
- no image tags.

`values.schema.json` validates types, resource quantities as non-empty strings where applicable, `runnerStartWorkers` integer 1..64, `healthPort` 1..65535, positive storage sizes, and every non-empty digest against `^sha256:[a-f0-9]{64}$`. It must reject unknown top-level and component keys (`additionalProperties: false` at every owned object boundary). Empty image fields are allowed at schema level so the source chart can exist without private defaults; workload templates fail with Helm `required` when rendered without release values.

## Pinned helpers

`_helpers.tpl` provides only these helpers:

- `cbse.labels` / `cbse.selectorLabels` — standard `app.kubernetes.io/{name,instance,version,managed-by,part-of}` labels; selectors contain only stable keys and never chart version.
- `cbse.image` — accepts one image object, requires repository and digest, emits `<repository>@<digest>`; never accepts a tag.
- `cbse.serviceAccountImagePullSecrets` — emits exactly `cbse-registry-auth`.
- stable compatibility-name helpers returning exactly: `experiment-operator`, `scenario-manager`, `sm-eds-nats`, `core-db`, `core-db-credentials`.
- no generic abstraction framework and no release-prefixed variant for fixed names.

## Change

1. Create the chart skeleton and complete value/schema/helper contract above.
2. Copy the generated CRD byte-for-byte into `charts/cbse/crds/`. Extend `verify-generated.sh` so generation verification also diffs the generated alpha4 CRD against the chart copy; a CRD/API change cannot leave the chart stale.
3. Add the pinned Helm installer and Makefile tooling:
   - `helm-tools` (or fold into `test-tools`) ensures the pinned binary;
   - `helm-lint` runs `helm lint charts/cbse -f test/helm/values-render.yaml`;
   - `helm-render` runs `helm template cbse charts/cbse --namespace cbse-system --include-crds -f test/helm/values-render.yaml` into a temp file;
   - `test-helm-chart` runs `test/harness/test-helm-chart.sh`;
   - `test-fast` invokes `test-helm-chart` after generated verification and before Go suites.
4. `test-helm-chart.sh` runs lint, render, JSON-schema negative cases, CRD lockstep, private-string scan, digest scan, balanced YAML-document/basic parse checks, and discovers every `test/helm/assert-*.sh` except its helper. It does no cluster mutation.
5. `assert-skeleton.sh` proves metadata, exact value keys, fixed-name helpers, digest-only image composition, invalid digest rejection, unknown-key rejection, and that the raw source values contain no registry/server/storage-class defaults.
6. Extend the harness self-test using the existing fake-tool style: Helm download/checksum failure is caught; chart tests invoke the pinned `HELM`; no real network/cluster operation in the self-test.
7. Chart `NOTES.txt` says only: chart is pre-release; `cbse-registry-auth` must pre-exist; experiments belong in the release namespace; H6 supplies the full guide. It contains no command with credential material.

## Constraints

- H1 renders no shared-stack workload except the CRD in `crds/`; H2/H3 own component templates.
- The CRD chart copy is exact, not hand-edited. `make verify-generated` must fail if it drifts.
- No private registry, API server, kubeconfig path, storage class, credentials, or local filesystem path in tracked files.
- Fake fixture digests are exactly 64 lowercase hex characters and clearly non-runnable (`example.invalid`).
- Do not add Helm plugins, chart dependencies, Node/Python package managers, or external schema tools. Tests use Helm, Bash, grep/jq already required by the harness, and repository scripts.
- `make test-fast` is mandatory because Makefile/test-harness/generated-verification paths change. No smoke run: this slice has no installable workloads.
- Commit only in the dispatched child branch; every commit includes `[orchestration: task <task-id> dispatch <dispatch-id>]`. Never edit the normative feature/slice.
- FEATURE.md §5 applies verbatim, including secrets hygiene, immutable images, scope discipline, verification honesty, and no `go work sync`.

## Ownership

Sole owner of the Target paths. H2/H3 add their own templates and `test/helm/assert-*.sh` files after this slice settles; they must not change H1's value/helper/schema contract. Discovered missing values or contradictory live-code requirements are report lines and manager questions, not silent edits.

## Observable acceptance

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → `ai.forge/qwen3.8-27b-nvfp4`.
2. `git status --short` contains only Target paths.
3. `bash -n test/harness/install-helm.sh test/harness/test-helm-chart.sh test/helm/assert-helpers.sh test/helm/assert-skeleton.sh` → rc=0.
4. `make helm-lint` → rc=0 using the pinned Helm binary.
5. `make helm-render` → rc=0; rendered CRD is alpha4-only.
6. `make test-helm-chart` → rc=0, including bad digest and unknown-key negative tests.
7. `make verify-generated` → rc=0 and the output/diff receipt proves the chart CRD lockstep.
8. `make test-fast` → rc=0.
9. Hygiene receipt: added-line scan for private infrastructure is empty; `rg -n 'image:.*:' charts/cbse` shows no tag-based rendered image path; helper test proves `repository@sha256` output.
10. Commit receipt: branch HEAD plus orchestration trailers.

Completion: `worker_done` exactly once with explicit outcome, task/dispatch IDs, attestation, evidence 1–10, files modified, and report path. Check coordinator messages before each new file group, after tests, and immediately before completion.
