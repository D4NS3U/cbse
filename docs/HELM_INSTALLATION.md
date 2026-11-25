# Installing the CBSE control plane with Helm

This is the canonical installation and operations guide for the `cbse` chart
(`charts/cbse/`). It documents only the behavior proven by the settled chart
slices (H1–H5) and the cluster acceptance evidence recorded in
[`devlog/changes/helm-control-plane-installation/IMPLEMENTATION_HANDOFF.md`](../devlog/changes/helm-control-plane-installation/IMPLEMENTATION_HANDOFF.md).

The chart installs the **shared control plane** of CBSE into one dedicated
namespace: the `SimulationExperiment` alpha4 CRD, the Experiment Operator, the
Scenario Manager, a directly rendered single-node NATS/JetStream server, and
the Scenario Manager's PostgreSQL Core DB. It does **not** install any
experiment-scoped workload; those remain dynamically provisioned by the
Experiment Operator and the Scenario Manager per experiment, exactly as in the
manifest-based smoke path.

Companion references:

- [Chart README](../charts/cbse/README.md) — complete values table, fixed
  resource names, and lifecycle semantics.
- [Cluster requirements](CLUSTER_REQUIREMENTS.md) — the cluster feature-gate
  prerequisites this guide summarizes.
- [Testing guide](CBSE_TESTING_GUIDE.md) — the development smoke tiers,
  including the chart-installed smoke (`make test-helm-smoke`).

## What the chart installs

| Component | Rendered objects | Behavior |
|---|---|---|
| `SimulationExperiment` CRD | one cluster-scoped `CustomResourceDefinition` from `crds/` | alpha4-only; installed at chart install; never upgraded or deleted by Helm |
| Experiment Operator | Deployment `experiment-operator`, ServiceAccount, namespaced Role/RoleBinding | one replica; watches only the release namespace; `/readyz` and `/healthz` probes |
| Scenario Manager | Deployment `scenario-manager`, ServiceAccount, namespaced Role/RoleBinding, ConfigMap `scenario-manager-config` | one replica; watches only the release namespace; HTTP `/readyz` readiness probe |
| NATS/JetStream | StatefulSet `sm-eds-nats`, client Service `sm-eds-nats`, headless Service `sm-eds-nats-headless`, ConfigMap `nats-config`, NetworkPolicy `sm-eds-nats-ingress` | single-node, authentication-free in v0.1; client port 4222, monitor port 8222 |
| Core DB | StatefulSet `core-db`, client Service `core-db`, headless Service `core-db-headless`, Helm-managed Secret `core-db-credentials`, NetworkPolicy `core-db-ingress` | single-node PostgreSQL; database `scenarios` by default |
| Lifecycle hooks | pre-install/pre-upgrade registry-validation Job, pre-delete experiment-guard Job, hook ServiceAccount `cbse-helm-hooks` with read-only Role/RoleBinding | digest-pinned public kubectl image; read-only; never destructive |

Fixed compatibility names are code-level contracts and are **never
release-prefixed**: `sm-eds-nats` (the operator injects
`nats://sm-eds-nats:4222` into experiment components), `core-db`,
`core-db-credentials`, `cbse-registry-auth`. One chart release per namespace
is the v0.1 installation model; the release namespace is both the control-plane
namespace and the only experiment namespace that release watches.

### Experiment-scoped resources remain dynamically provisioned

The chart does not render any per-experiment workload. For each
`SimulationExperiment` the Experiment Operator still provisions, in the
release namespace:

- the Scenario Detail DB and Result DB (Deployment + Service + connection
  Secret each);
- the Translator Deployment with its rootless BuildKit sidecar and its
  ConfigMap;
- the PostProcessingService and the Experimental Design Service (Deployment +
  Service each, e.g. `<experiment-name>-eds-svc`);
- the runner ServiceAccount `simrunner-<uid12>`;
- the `simrun-*` runner Jobs created by the Scenario Manager.

Garbage collection of those resources, and the experiment finalizer cleanup
inside the Scenario Manager, are unchanged by the chart.

## Prerequisites

- **Kubernetes 1.30 or newer** (conformant; no upper minor bound).
- **`UserNamespacesSupport` feature gate enabled** on kube-apiserver,
  kube-controller-manager, and kubelet — required by the per-experiment
  Translator's rootless BuildKit sidecar. See
  [cluster requirements](CLUSTER_REQUIREMENTS.md) for enablement and
  verification steps.
- **Pod Security exception in the release/experiment namespace:** the
  unconfined-seccomp/unconfined-AppArmor BuildKit sidecar requires
  `pod-security.kubernetes.io/enforce=privileged` (audit/warn may stay
  `restricted`) on every namespace that will host `SimulationExperiment`
  resources.
- **Helm 3.** The repository pins Helm v3.18.4 for its own tooling;
  `make helm-tools` installs the pinned binary under the ignored root `bin/`
  directory. Any Helm 3 that renders v2 charts with `crds/` support can
  perform the installation itself.
- **Storage:** a cluster default StorageClass, or explicit
  `nats.persistence.storageClass` / `coreDatabase.persistence.storageClass`
  values. Empty `storageClass` means the cluster default provisioner decides.
  Persistence is enabled by default for both stateful components;
  `persistence.enabled=false` is an explicit development-only ephemeral
  (`emptyDir`) branch.
- **Registry access:** every cluster node that runs the chart's workloads must
  be able to pull the digest-pinned images. The proven pull boundary is
  per-workload: the Operator and Scenario Manager ServiceAccounts carry an
  explicit `imagePullSecrets: cbse-registry-auth`, and the operator-created
  experiment workloads (EDS, Translator, PPS, per-experiment databases,
  runner Jobs) reference the same fixed Secret. The NATS and Core DB
  StatefulSet Pods carry **no** `imagePullSecrets` and no dedicated
  ServiceAccount, so their images must be pullable without that Secret —
  use the official public images from the repository image lock, or mirrors
  already authorized at the node level. Pointing those two images at a
  private mirror is a future installation-owner concern, not something the
  chart authorizes. The lifecycle hook Jobs also carry no pull Secret and
  run a digest-pinned public image.
- **Image architecture support:** the released digest references must be
  pullable on your cluster's node architecture; the smoke image set is
  `linux/amd64` only, so a cluster with different architecture nodes needs
  matching per-architecture digests in the release values.
- **One release per namespace.** Fixed service and Secret names are
  namespace-local; a second release in the same namespace is unsupported.

## Secret inventory and ownership

Every Secret CBSE requires or creates, with its owner and lifecycle (the
consumer set follows the normative inventory in FEATURE.md §3.1):

| Secret | Owner | Required when | Type and keys | Consumers | Chart behavior |
|---|---|---|---|---|---|
| `cbse-registry-auth` | **Installation owner only.** Exactly one per v0.1 release/experiment namespace | Before chart installation (pre-install validation) and by the experiment provisioning path (Translator BuildKit push/pull, runner image verification, private image pulls) | `kubernetes.io/dockerconfigjson`; standard `.dockerconfigjson` key | Operator validation and operator-created DB/Translator workloads; Translator BuildKit mount; SM registry/digest verification; runner Jobs; Operator/SM Pods when their images are private | **Existing only.** A pre-install/pre-upgrade hook validates the fixed name, type, and non-empty key; Helm references it (ServiceAccount `imagePullSecrets`) but never creates, adopts, mutates, rotates, or deletes it. No registry Secret value exists in the chart, and raw Docker config is not accepted in values. |
| `core-db-credentials` | **Helm managed only** (release namespace) | Always for the internal Core DB | `Opaque`; exactly `SCENARIO_MANAGER_CORE_DB_USER` and `SCENARIO_MANAGER_CORE_DB_PASSWORD` | Internal PostgreSQL container and Scenario Manager | Generated once on first install (username `cbse`, 48-character random password); every later upgrade reuses both existing encoded values byte-for-byte through `lookup`. There is **no** existing-Secret branch and **no** database password value. Retained with the Core DB PVC so a reinstall reconnects to the retained data. |
| `<experiment>-detaildb-sct` | Experiment Operator (per experiment) | Every `SimulationExperiment` | `Opaque`; `host`, `port`, `dbname`, `user`, `password` | Operator readiness probe, Translator, reference EDS | **Not a Helm Secret.** Generated from `spec.detailDatabase`; garbage-collected with the experiment. |
| `<experiment>-resultdb-sct` | Experiment Operator (per experiment) | Every `SimulationExperiment` | `Opaque`; `host`, `port`, `dbname`, `user`, `password` | Operator readiness probe, Translator/generated runner as applicable, PPS | **Not a Helm Secret.** Generated from `spec.resultDatabase`; garbage-collected with the experiment. |
| ServiceAccount projected tokens | Kubernetes | Kubernetes API clients | projected token volume; no long-lived Secret object on modern Kubernetes | Operator and SM Kubernetes clients | No chart-created token Secret. |
| NATS credentials | — | **Never in v0.1** | none | — | Authentication-free by the current alpha4 runtime contract; no NATS credential Secret is rendered or expected. Secure NATS is a future application feature. |
| TLS certificate Secrets | — | Not applicable in v0.1 | none | — | No ingress/webhook/TLS metrics endpoint in the initial chart; cert-manager scaffold Secrets are not part of v0.1. |

Current API security debt, recorded but **not** expanded by the chart:
`detailDatabase.user`/`password` and `resultDatabase.user`/`password` are
literal fields in the `SimulationExperiment` CR, and the operator copies them
into the two generated endpoint Secrets. Moving those to a
`SecretKeyRef`-style reference is a future API feature; Helm cannot repair it
without an API change.

## Creating the registry Secret safely

The only Secret you must create is `cbse-registry-auth`, in the target
namespace, before installation. Create it from a **local file** so no
credential ever appears in a command argument, shell history, or rendered
value:

```bash
# <your-docker-config> is a local Docker config file (config.json) with pull
# rights for your registry. Only a relative placeholder path is shown on
# purpose; keep the file protected.
kubectl create secret generic cbse-registry-auth \
  --from-file=.dockerconfigjson=./<your-docker-config> \
  --type=kubernetes.io/dockerconfigjson \
  -n <release-namespace>
```

Verify the Secret **by name and type only** — never inspect or print its
data:

```bash
# Expect: cbse-registry-auth kubernetes.io/dockerconfigjson
kubectl get secret cbse-registry-auth -n <release-namespace> \
  -o jsonpath='{.metadata.name} {.type}'
```

The chart's pre-install hook performs exactly this read-only validation and
fails the install with one actionable line when the Secret is missing, has the
wrong type, or has an empty `.dockerconfigjson` key. The hook never decodes or
prints the payload.

## Image release values

Three distinct things are often conflated; they are separate:

1. **Public chart source.** This repository ships the chart source with **no
   private defaults**: every image `repository` and `digest` defaults to empty,
   and workload templates fail with an actionable `required` error when
   rendered without release values. Rendering the source chart alone is not an
   installation.
2. **Optional packaged-chart publication.** Publishing a packaged chart
   (Helm package/index) is an optional release decision, not a v0.1
   requirement and not promised here.
3. **Private images.** The component images (Operator, Scenario Manager,
   NATS, PostgreSQL, hook kubectl image) are referenced by repository +
   immutable `sha256:` digest. The private-repository requirement applies
   only where a chart workload actually carries a pull Secret: the Operator
   and Scenario Manager Pods (and, during experiment provisioning, the
   operator-created workloads). The NATS and Core DB Pods have no pull
   Secret, so release values should point them at images pullable without
   `cbse-registry-auth` (the official public images in the repository image
   lock, or node-level-authorized mirrors); a private mirror for those two
   is a future installation-owner concern, not chart-supported behavior.
   The release process generates one
   **private, untracked** installation-owner values file, conventionally
   `values-images-<version>.yaml`, containing the repositories and resolved
   digests for exactly five components: `operator.image`,
   `scenarioManager.image`, `nats.image`, `coreDatabase.image`, and
   `hooks.kubectlImage`. Installation consumes that one file instead of manual
   digest entry. The file contains no credentials, but its repositories may be
   operational/private — never commit, share, or print it.

Generate it with the repository entry point:

```bash
make helm-image-values \
  IMAGES_ENV=<path-to-digest-pinned-images.env> \
  OUTPUT=<path-to-private-values-images.yaml>
```

- `IMAGES_ENV` must be a readable digest-pinned `images.env` (the smoke
  `images.env` format) containing at least `OPERATOR_IMAGE` and `SM_IMAGE` as
  full `<repository>@sha256:<64 lowercase hex>` references; extra keys are
  ignored.
- `NATS_IMAGE`, `POSTGRES_IMAGE`, and `HELM_HOOK_KUBECTL_IMAGE` come from the
  repository image lock (`test/e2e/images.lock.env`); no environment override
  is accepted for locked values.
- Tags, empty values, duplicate `@`, and malformed digests are rejected before
  anything is written.
- The file is written atomically with mode `0600`; the generator prints only
  the output path and the component count, never the repository values.

The generated file has this shape (the operator and scenario-manager
repositories are the private release components; the NATS, PostgreSQL, and
hook repositories are the repository image-lock values; all digests are
resolved immutable references):

```yaml
operator:
  image:
    repository: "<your-private-registry>/exop"
    digest: "sha256:<64-hex-digest>"
scenarioManager:
  image:
    repository: "<your-private-registry>/sm"
    digest: "sha256:<64-hex-digest>"
nats:
  image:
    repository: "docker.io/library/nats"
    digest: "sha256:<64-hex-digest>"
coreDatabase:
  image:
    repository: "docker.io/library/postgres"
    digest: "sha256:<64-hex-digest>"
hooks:
  kubectlImage:
    repository: "docker.io/alpine/k8s"
    digest: "sha256:<64-hex-digest>"
```

`values.schema.json` enforces the digest shape (`^sha256:[a-f0-9]{64}$`) and
rejects unknown keys, so a release values file that drifts from the chart
contract fails validation before install.

## Installation

```bash
# 1. Create the release namespace with the Pod Security exception.
kubectl create namespace <release-namespace>
kubectl label namespace <release-namespace> \
  pod-security.kubernetes.io/enforce=privileged \
  pod-security.kubernetes.io/audit=restricted \
  pod-security.kubernetes.io/warn=restricted

# 2. Create the installation-owner registry Secret in it (previous section).

# 3. Install. The chart source contains no private defaults, so the generated
#    private image-values file is required.
helm upgrade --install cbse charts/cbse \
  --namespace <release-namespace> \
  --values <path-to-private-values-images.yaml> \
  --wait --atomic --timeout 10m
```

Notes:

- `--wait` proves Kubernetes readiness of every chart-owned workload; the
  Scenario Manager Pod becomes Ready only when its `/readyz` readiness probe
  passes (all Core DB, NATS/JetStream, Kubernetes, and consumer components
  started). `--atomic` rolls the release back if installation fails.
- The CRD is installed from `crds/` only when absent. If the cluster already
  has a `simulationexperiments.experiment.cbse.terministic.de` CRD, it is left
  untouched; an existing CRD that serves/stores a version other than alpha4 is
  a cluster-admin breaking-upgrade concern, not something the chart performs
  (see [CRD lifecycle](#crd-lifecycle)).
- `helm upgrade --install` is idempotent: rerunning it with the same values
  reuses the generated Core DB credentials and PVCs and keeps selectors
  stable.

## First experiment

Create `SimulationExperiment` resources **only in the release namespace**. The
operator and Scenario Manager watch exactly that namespace (namespaced RBAC,
no cluster-wide fallback), and the fixed service names `sm-eds-nats` and
`core-db` are namespace-local DNS contracts.

The harness acceptance fixture
[`test/e2e/manifests/experiment.yaml`](../test/e2e/manifests/experiment.yaml)
is the reference shape. A minimal user-facing example with digest placeholders:

```yaml
apiVersion: experiment.cbse.terministic.de/alpha4
kind: SimulationExperiment
metadata:
  name: <experiment-name>
  namespace: <release-namespace>
spec:
  defaultServiceType: ClusterIP
  detailDatabase:
    image: <your-registry>/scenario-detail-database@sha256:<64-hex-digest>
    dbname: <database-name>
    user: <database-user>
    password: <database-password>
    port: 5432
    serviceType: ClusterIP
  resultDatabase:
    image: <your-registry>/postgres@sha256:<64-hex-digest>
    dbname: <database-name>
    user: <database-user>
    password: <database-password>
    port: 5432
    serviceType: ClusterIP
  translator:
    image: <your-registry>/translator@sha256:<64-hex-digest>
    repository: <your-registry>/<runner-image-repository>
    baseimage: <your-registry>/runner-base@sha256:<64-hex-digest>
    builderImage: <your-registry>/buildkit@sha256:<64-hex-digest>
    registryAuthSecretRef:
      name: cbse-registry-auth
    port: 8080
    serviceType: ClusterIP
  postProcessingService:
    image: <your-registry>/pps@sha256:<64-hex-digest>
    port: 8081
    serviceType: ClusterIP
  experimentalDesignService:
    design: <free-text design description>
    image: <your-registry>/eds@sha256:<64-hex-digest>
    port: 8082
    serviceType: ClusterIP
```

Rules this example encodes:

- every image field is a digest-pinned reference (the operator rejects
  floating tags);
- `translator.registryAuthSecretRef.name` must equal `cbse-registry-auth`
  (the fixed name validated during provisioning);
- `translator.repository` is the repository the generated runner images are
  pushed to and must match what the Scenario Manager accepts.

Once the experiment reaches `InProgress`, drive the Experimental Design
Service through a local port-forward of the per-experiment EDS Service (the
embedded UI and the same JSON API are served on the CR's EDS port, 8082 in the
smoke profile):

```bash
kubectl port-forward -n <release-namespace> \
  svc/<experiment-name>-eds-svc 8082:8082
```

Then complete the Design → Save Experiment → Start Experiment → (Re-arm) →
Finish Experiment flow in the EDS UI/API. The full API-driven flow, including
the finalization batch that is required for the terminal `Finished` verdict,
is exercised by the smoke suite (see the [testing guide](CBSE_TESTING_GUIDE.md)
and [test/e2e/README.md](../test/e2e/README.md)) and documented in the EDS
module documentation ([wiki/modules/experimental-design-service.md](wiki/modules/experimental-design-service.md)).

## Observing the installation

- Release state and resources:

  ```bash
  helm status cbse -n <release-namespace> --show-resources
  helm history cbse -n <release-namespace>
  ```

- Workloads, claims, and events:

  ```bash
  kubectl get pods,statefulset,pvc -n <release-namespace>
  kubectl get events -n <release-namespace> --sort-by='.lastTimestamp'
  ```

- The two StatefulSets must be `1/1 Ready`; the PVCs `data-sm-eds-nats-0` and
  `data-core-db-0` must be bound when persistence is enabled.
- Experiment progress:

  ```bash
  kubectl get simulationexperiments -n <release-namespace>
  kubectl get simexp <experiment-name> -n <release-namespace> \
    -o jsonpath='{.status.phase}'
  ```

- Scenario Manager readiness is expressed by the Pod `READY` column (the
  chart's `/readyz` probe); do not rely on log grep and do not inspect Secret
  contents for any operational check.

## Upgrade and rollback

### Upgrades

```bash
helm upgrade --install cbse charts/cbse \
  --namespace <release-namespace> \
  --values <path-to-private-values-images.yaml> \
  --wait --atomic
```

Upgrades preserve the invariants that make them safe in v0.1:

- `core-db-credentials` is reused byte-for-byte through `lookup`; an existing
  Secret missing either exact key fails the render instead of rotating
  credentials.
- The Core DB and JetStream PVCs are preserved; selectors are stable, so no
  workload identity churn occurs.
- The pre-install/pre-upgrade registry validation runs again and fails fast
  if `cbse-registry-auth` has gone missing or changed type.

Limitation: every chart-owned workload is **single replica**. An upgrade
rollout briefly restarts each workload; there is no zero-downtime guarantee
and no leader-election/HA in v0.1. What the upgrade preserves (proven by the
lifecycle acceptance): the `core-db-credentials` values, the PVC identities,
and the stored Core DB data (a marker row survived the upgrade together with
the Pod restarts). The shared control plane is not available while a
workload restarts.

### Rollback caveats

`helm rollback cbse <revision> -n <release-namespace>` reverts the
**template-rendered** objects to the chosen revision. Two caveats:

- The CRD is not a release object: rollback never touches
  `simulationexperiments.experiment.cbse.terministic.de` (see below).
- Rollback does not recreate deleted data. If the upgrade you are rolling back
  from changed image digests or configuration, verify the target revision's
  values still satisfy the runtime (digest-pinned images, fixed Secret names)
  before relying on it.

### CRD lifecycle

- Helm installs `crds/` on a **new** release only and **never upgrades or
  deletes CRDs** on upgrade or uninstall. Never assume a chart upgrade updates
  the CRD.
- The chart copy is byte-locked to the operator's generated CRD
  (`make verify-generated` fails on drift), alpha4-only.
- The CRD is cluster-scoped and shared by every release and every namespace.
  The alpha2/alpha3 → alpha4 migration was a one-time **breaking** upgrade
  that a cluster administrator performs deliberately; the chart documents it
  and never performs it automatically. If your cluster serves/stores a
  non-alpha4 version, resolve that as a cluster-admin operation before
  installing.

## Uninstall (guarded, CRs-first)

### Pre-delete guard

A read-only pre-delete hook Job (`cbse-hook-experiment-guard`) lists
`SimulationExperiment` resources in the release namespace **while the Scenario
Manager is still alive**:

- zero objects → the uninstall proceeds;
- one or more → the Job exits non-zero and **blocks the uninstall** with the
  exact recovery guidance. The Scenario Manager, Operator, NATS, and Core DB
  keep running. The guard never deletes, patches, or otherwise touches
  experiments, finalizers, controller resources, PVCs, Secrets, or the CRD,
  and a failed guard Job remains in place for diagnosis until the next attempt
  replaces it.

### CRs-first workflow

```bash
# 1. Delete all experiments in the release namespace and wait. This lets the
#    live Scenario Manager run its finalizer cleanup (consumer/subject
#    cleanup, Job deletion, row purge) before anything is removed.
kubectl delete simulationexperiments --all -n <release-namespace> --wait

# 2. Rerun the uninstall once every finalizer has cleared.
helm uninstall cbse -n <release-namespace>
```

If the guard blocks you, the uninstall error names the namespace and the
recovery command; read the guard Job logs for the exact message:

```bash
kubectl logs job/cbse-hook-experiment-guard -n <release-namespace>
```

### Retained PVCs and the Core DB Secret

With the default `retain=true` values, uninstall leaves behind:

- the JetStream PVC `data-sm-eds-nats-0` (StatefulSet
  `persistentVolumeClaimRetentionPolicy: Retain`);
- the Core DB PVC `data-core-db-0` (same retention policy);
- the Helm-managed `core-db-credentials` Secret
  (`helm.sh/resource-policy: keep`).

This is intentional: a reinstall into the same namespace reconnects the Core
DB to the retained data with the retained credentials, and the JetStream
data remains available for the components that depend on it. `retain=false` (an explicit choice) permits
both the PVCs and the credential Secret to be deleted together on uninstall.

### Optional destructive cleanup

Deleting retained data is **irreversible**. Only do it when you have verified
you no longer need the stored scenarios, state rows, or JetStream streams.
These commands name only chart-owned objects; the installation-owner
`cbse-registry-auth` Secret is **not** chart-owned and is outside chart
cleanup:

```bash
kubectl delete pvc data-sm-eds-nats-0 data-core-db-0 -n <release-namespace>
kubectl delete secret core-db-credentials -n <release-namespace>
```

### The CRD is preserved

`helm uninstall` never removes the cluster-scoped CRD. Delete it only as a
deliberate cluster-admin action, **after** every chart release in the cluster
is uninstalled and no `SimulationExperiment` resources remain in any
namespace — stored CR objects survive only as long as the CRD does.

## Troubleshooting

| Symptom | First checks | Likely cause / action |
|---|---|---|
| Install fails with `CBSE registry validation failed ...` | `kubectl get secret cbse-registry-auth -n <ns> -o jsonpath='{.type}'`; hook Job logs: `kubectl logs job/cbse-hook-registry-validation -n <ns>` | `cbse-registry-auth` missing, wrong type, or empty `.dockerconfigjson`. Recreate it from a local Docker config file (payload-safe pattern above); Helm never owns this Secret. |
| PVCs stuck `Pending` | `kubectl get pvc -n <ns>`; `kubectl get sc` | No default StorageClass and no explicit `persistence.storageClass` value. Set an explicit class in the release values, or provision a default. |
| Per-experiment Translator Pod fails with `can't enable NoProcessSandbox without Rootless` | Feature-gate verification in [cluster requirements](CLUSTER_REQUIREMENTS.md) | `UserNamespacesSupport` gate off on apiserver/controller-manager/kubelet. Enable it cluster-wide; the chart never enables feature gates. |
| Pod Security admission rejection for experiment Pods | `kubectl get ns <ns> --show-labels` | The release/experiment namespace is missing `pod-security.kubernetes.io/enforce=privileged` (or equivalent exception) required by the unconfined BuildKit sidecar. |
| Scenario Manager not Ready | `kubectl get pods -n <ns>` (Core DB, NATS, SM); `kubectl describe pod` for events; `kubectl logs` for the SM Pod (no Secret inspection) | Core DB not accepting connections (`pg_isready` readiness), NATS not ready, or a namespaced RBAC gap. Fix the underlying dependency; the SM reports Ready only when every component has started. |
| Uninstall blocked | `kubectl get simulationexperiments -n <ns>`; `kubectl logs job/cbse-hook-experiment-guard -n <ns>` | Experiments still exist. Delete them with `--wait` while the SM is alive, wait for finalizers, rerun `helm uninstall`. |
| Hook Job stuck | `kubectl get jobs -n <ns>`; `kubectl logs job/<hook-name> -n <ns>` | Failed hook Jobs are kept for diagnosis (`backoffLimit: 0`, `restartPolicy: Never`); the next install/uninstall attempt replaces them. The hook images are digest-pinned public images; a pull failure means node-level registry access for that public image. |
| Retained failed smoke run | `make test-diagnose RUN_ID=<run-id>` | A failed cluster run was retained with `CBSE_KEEP_ON_FAILURE=1`. Read the artifacts under `artifacts/test/<run-id>/`, then clean with `make test-clean RUN_ID=<run-id>` (CRs-first, then Helm release, then namespace). See the [testing guide](CBSE_TESTING_GUIDE.md). |

General hygiene: chart and harness artifacts deliberately never contain Secret
payloads, `helm get manifest` output, or decoded release data. If you need
deeper diagnosis, inspect Pods and events — never Secret contents.

## v0.1 limitations

State these plainly when planning an installation:

- **Single namespace.** One chart release per namespace; the release namespace
  is the only experiment namespace that release watches. Multi-namespace
  watching, namespace selectors, and cluster-wide installations are future
  features requiring binary configuration and an explicit Secret-distribution
  model.
- **Single-node persistence is not HA and not a backup.** NATS/JetStream and
  the Core DB are one-replica StatefulSets. A persistent PVC survives Pod
  replacement; it does not survive a node loss, and nothing in v0.1 replicates
  data or takes backups. Do not describe PVC persistence as high availability.
- **Authentication-free internal NATS.** The alpha4 runtime contract rejects
  credential-bearing NATS URLs, so v0.1 NATS has no credentials and no TLS.
  Its boundary is namespace-local ClusterIP networking plus the
  enabled-by-default NetworkPolicies. Secure NATS requires a separate
  application-level contract accepted by all four NATS clients.
- **Literal database credentials in the CR API.** `detailDatabase` and
  `resultDatabase` user/password are literal CR fields today; the generated
  endpoint Secrets inherit them. This is recorded API debt, not a chart
  defect.
- **No automatic Secret distribution.** The chart never copies
  `cbse-registry-auth` (or any other Secret) across namespaces, and no
  external secret operator is integrated.
- **No zero-downtime upgrades** (single replica per workload).
- **No automatic CRD upgrades** (see [CRD lifecycle](#crd-lifecycle)).

## External-service roadmap

External NATS and PostgreSQL are required future installation modes,
specified but **not implemented** in v0.1; no `external` branch exists in
`values.schema.json` because the current binaries cannot honor one yet.

- **External Core DB (first compatible external mode).** The chart would omit
  its PostgreSQL StatefulSet/Service/PVC and point the Scenario Manager at an
  operator-supplied DSN plus an existing installation-owner credentials
  Secret with the same runtime keys (`SCENARIO_MANAGER_CORE_DB_USER`,
  `SCENARIO_MANAGER_CORE_DB_PASSWORD`).
- **External NATS/JetStream (later, security-gated).** The chart would omit
  its NATS StatefulSet/Service/PVC and point all CBSE components at an
  external endpoint. The first compatible external mode may be
  authentication-free; TLS and authenticated NATS require new
  credential/TLS inputs accepted by the Operator, Scenario Manager,
  Translator, and EDS before any values become public. The operator's NATS
  endpoint is also not yet configurable (it is fixed), so endpoint
  configurability is a prerequisite implementation step.

Until those slices land, the internal single-node NATS and Core DB rendered by
this chart are the only supported mode.
