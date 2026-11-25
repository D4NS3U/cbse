# CBSE Helm chart (`cbse`)

The `cbse` chart installs the **CBSE shared control plane** into one dedicated
Kubernetes namespace:

- the alpha4-only `SimulationExperiment` CRD (cluster-scoped, shared, from
  `crds/`);
- the **Experiment Operator** (Deployment, namespaced RBAC) — provisions every
  experiment-scoped workload;
- the **Scenario Manager** (Deployment, ConfigMap, namespaced RBAC, HTTP
  `/readyz` readiness);
- a directly rendered **single-node NATS/JetStream** server (StatefulSet,
  ConfigMap, Services, NetworkPolicy);
- a directly rendered **single-node PostgreSQL Core DB** (StatefulSet,
  Services, Helm-managed credentials Secret, NetworkPolicy);
- lifecycle hooks: a pre-install/pre-upgrade registry-Secret validation Job
  and a read-only pre-delete experiment-guard Job.

It does **not** install any experiment-scoped workload (EDS, Translator, PPS,
per-experiment databases, runner ServiceAccount, runner Jobs); those remain
dynamically provisioned by the Operator and Scenario Manager per
`SimulationExperiment`.

Canonical installation and operations guide:
[`docs/HELM_INSTALLATION.md`](../../docs/HELM_INSTALLATION.md). Cluster
prerequisites: [`docs/CLUSTER_REQUIREMENTS.md`](../../docs/CLUSTER_REQUIREMENTS.md).
Normative feature contract:
[`devlog/changes/helm-control-plane-installation/FEATURE.md`](../../devlog/changes/helm-control-plane-installation/FEATURE.md).

## Quick install

The chart source ships **no private defaults** (all image repositories and
digests default to empty and fail rendering with `required` until supplied),
so installation needs the generated private image-values file:

```bash
# Prerequisites: cluster per docs/CLUSTER_REQUIREMENTS.md, the namespace's
# Pod Security labels, and the installation-owner cbse-registry-auth Secret
# created from a local Docker config file (see the installation guide).
make helm-image-values IMAGES_ENV=<digest-pinned-images.env> OUTPUT=<private-values-images.yaml>

helm upgrade --install cbse charts/cbse \
  --namespace <release-namespace> \
  --values <private-values-images.yaml> \
  --wait --atomic
```

Step-by-step (namespace, Secret creation, first experiment, observation,
upgrade, guarded uninstall, troubleshooting):
[`docs/HELM_INSTALLATION.md`](../../docs/HELM_INSTALLATION.md).

## Values

Complete v0.1 value surface. Unknown top-level and component keys are
rejected by `values.schema.json`.

| Value | Default | Description |
|---|---|---|
| `global.imagePullPolicy` | `IfNotPresent` | Pull policy applied to every chart-owned workload (`Always` / `IfNotPresent` / `Never`). |
| `operator.image.repository` | `""` | Experiment Operator image repository; **required at render**; immutable digest mandatory. |
| `operator.image.digest` | `""` | Operator image digest, `sha256:<64 lowercase hex>`; **required at render**; tag-based references are rejected. |
| `operator.resources.requests.cpu` / `.memory` | `10m` / `64Mi` | Operator CPU/memory requests. |
| `operator.resources.limits.cpu` / `.memory` | `500m` / `192Mi` | Operator CPU/memory limits. |
| `scenarioManager.image.repository` | `""` | Scenario Manager image repository; **required at render**; digest mandatory. |
| `scenarioManager.image.digest` | `""` | Scenario Manager image digest; **required at render**. |
| `scenarioManager.runnerStartWorkers` | `4` | Concurrent runner-start workers (integer, 1–64). |
| `scenarioManager.healthPort` | `8080` | Port of the SM readiness/health endpoint (1–65535); serves `/healthz` and `/readyz`. |
| `scenarioManager.resources.requests.cpu` / `.memory` | `10m` / `48Mi` | SM CPU/memory requests. |
| `scenarioManager.resources.limits.cpu` / `.memory` | `500m` / `192Mi` | SM CPU/memory limits. |
| `nats.image.repository` | `""` | NATS image repository; **required at render**; digest mandatory. |
| `nats.image.digest` | `""` | NATS image digest; **required at render**. |
| `nats.jetstream.maxMemoryStore` | `128MB` | JetStream maximum memory store (positive quantity). |
| `nats.jetstream.maxFileStore` | `512MB` | JetStream maximum file store (positive quantity). |
| `nats.persistence.enabled` | `true` | Persistent JetStream data (PVC claim template). `false` is the explicit development-only `emptyDir` branch. |
| `nats.persistence.size` | `2Gi` | JetStream PVC requested size (positive quantity). |
| `nats.persistence.storageClass` | `""` | Storage class for the JetStream PVC; empty = cluster default StorageClass decides. |
| `nats.persistence.retain` | `true` | Retain JetStream data on uninstall (StatefulSet PVC retention policy `Retain`). |
| `nats.resources.requests.cpu` / `.memory` | `10m` / `32Mi` | NATS CPU/memory requests. |
| `nats.resources.limits.cpu` / `.memory` | `500m` / `256Mi` | NATS CPU/memory limits. |
| `coreDatabase.image.repository` | `""` | PostgreSQL Core DB image repository; **required at render**; digest mandatory. |
| `coreDatabase.image.digest` | `""` | Core DB image digest; **required at render**. |
| `coreDatabase.database` | `scenarios` | Core DB database name (non-empty string). |
| `coreDatabase.persistence.enabled` | `true` | Persistent Core DB data (PVC claim template). `false` is the explicit development-only `emptyDir` branch. |
| `coreDatabase.persistence.size` | `5Gi` | Core DB PVC requested size (positive quantity). |
| `coreDatabase.persistence.storageClass` | `""` | Storage class for the Core DB PVC; empty = cluster default StorageClass decides. |
| `coreDatabase.persistence.retain` | `true` | Retain Core DB data **and** the generated credentials Secret on uninstall. |
| `coreDatabase.resources.requests.cpu` / `.memory` | `25m` / `96Mi` | Core DB CPU/memory requests. |
| `coreDatabase.resources.limits.cpu` / `.memory` | `750m` / `384Mi` | Core DB CPU/memory limits. |
| `hooks.kubectlImage.repository` | `""` | Read-only hook Job image repository; **required at render**; digest mandatory. |
| `hooks.kubectlImage.digest` | `""` | Hook Job image digest; **required at render**. |
| `networkPolicies.enabled` | `true` | Render the internal NATS and Core DB ingress NetworkPolicies. |

Deliberate absences (enforced by schema and templates): no release/watch
namespace value (`.Release.Namespace` is authoritative), no configurable
resource names, no registry-Secret value, no Core DB username/password or
existing-Secret value, no NATS username/password/TLS value, no
external-service branch, no image tags.

### Immutable digest requirement

Every chart image is rendered as `<repository>@sha256:<64 lowercase hex>` by
the `cbse.image` helper: an empty repository or digest fails with an
actionable `required` message, a malformed digest fails the schema, and a
tag-bearing repository fails at render time. The release process generates
the private, untracked installation-owner `values-images-<version>.yaml`
(repositories + resolved digests for the five components) via
`make helm-image-values`; see the
[installation guide](../../docs/HELM_INSTALLATION.md#image-release-values).

## Fixed resource names and constraints

Fixed compatibility names are code-level contracts, never release-prefixed,
and arbitrary Helm release names must not alter them:

| Name | Kind / role |
|---|---|
| `sm-eds-nats` | NATS client Service + StatefulSet (operator-injected endpoint `nats://sm-eds-nats:4222`) |
| `sm-eds-nats-headless` | NATS headless Service (StatefulSet `serviceName`) |
| `core-db` | Core DB client Service + StatefulSet (SM DSN endpoint) |
| `core-db-headless` | Core DB headless Service |
| `core-db-credentials` | Helm-managed Core DB credentials Secret |
| `experiment-operator` | Operator Deployment / ServiceAccount / Role / RoleBinding |
| `scenario-manager` | SM Deployment / ServiceAccount / Role / RoleBinding |
| `scenario-manager-config` | SM ConfigMap (non-secret configuration only) |
| `nats-config` | NATS ConfigMap (authentication-free) |
| `cbse-registry-auth` | installation-owner registry pull Secret (pre-existing; chart validates and references it only) |
| `cbse-hook-registry-validation` | pre-install/pre-upgrade registry-validation hook Job |
| `cbse-hook-experiment-guard` | pre-delete experiment-guard hook Job |
| `cbse-helm-hooks` | hook ServiceAccount + read-only Role/RoleBinding (no `imagePullSecrets`) |
| `sm-eds-nats-ingress` / `core-db-ingress` | internal NetworkPolicies (when enabled) |
| `data-sm-eds-nats-0` / `data-core-db-0` | persistent-mode PVCs (claim template `data`) |

One chart release per namespace is the v0.1 model: fixed service/Secret
names are namespace-local, and the Operator/SM watch exactly the release
namespace with namespaced RBAC. Selectors contain only stable keys (never the
chart version), so upgrades do not churn workload identity.

## Secret semantics

| Secret | Owner | Lifecycle |
|---|---|---|
| `cbse-registry-auth` | **installation owner only** (exactly one per release namespace) | Must pre-exist; type `kubernetes.io/dockerconfigjson` with a non-empty `.dockerconfigjson` key. A pre-install/pre-upgrade hook validates it read-only and fails fast otherwise. Helm references it in ServiceAccount `imagePullSecrets` and in operator-created workload pull references, but never creates, adopts, mutates, rotates, or deletes it. There is no `helm.sh/resource-policy: keep` on it and no registry value in the chart. |
| `core-db-credentials` | **Helm only** (release namespace) | Generated once on first install (username `cbse`, 48-character random password); every upgrade reuses both existing encoded values byte-for-byte through `lookup`; an existing Secret missing either exact key (`SCENARIO_MANAGER_CORE_DB_USER`, `SCENARIO_MANAGER_CORE_DB_PASSWORD`) fails the render. No values-supplied credential, no existing-Secret branch. Carries `helm.sh/resource-policy: keep` exactly when `coreDatabase.persistence.retain` is true, so the credentials are retained together with the Core DB data. |

Per-experiment endpoint Secrets (`<experiment>-detaildb-sct`,
`<experiment>-resultdb-sct`) are Operator-generated and
garbage-collected with the experiment; they are not chart resources.

## Persistence semantics

Both stateful components are single-replica StatefulSets with a `data` claim
template mounted at `/data/jetstream` (NATS) and `/var/lib/postgresql/data`
(Core DB). Persistence is **enabled by default**; `persistence.enabled=false`
is an explicit development-only branch rendering `emptyDir` at the same mount
(no claim template, no PVC). Empty `storageClass` omits
`storageClassName` entirely (cluster default decides); an explicit class is
emitted exactly.

Persistent does **not** mean highly available and is not a backup: a PVC
survives Pod replacement, not node loss. `retain=true` (default) sets the
StatefulSet `persistentVolumeClaimRetentionPolicy` to `Retain` on delete and
scale, so uninstall leaves the PVCs behind for a reinstall to reconnect to;
`retain=false` permits the PVCs to be deleted with the release and drops the
credential-Secret keep policy at the same time, so data and credentials never
drift apart.

## Hook semantics

Both hooks run from a digest-pinned public kubectl-plus-shell image
(`hooks.kubectlImage`) as UID/GID 1000 with a read-only root filesystem,
dropped capabilities, and no privilege escalation, under a dedicated
read-only ServiceAccount/Role/RoleBinding. The hook ServiceAccount
deliberately carries **no** `imagePullSecrets`, so the hooks boot even while
`cbse-registry-auth` is absent.

- **Registry validation** (`pre-install,pre-upgrade`, weight 0): reads only
  `cbse-registry-auth` in the release namespace; fails with one actionable
  line when the Secret is absent, has the wrong type, or has a missing/empty
  `.dockerconfigjson` key. It never decodes, prints, or persists the payload
  and never touches the Secret.
- **Experiment guard** (`pre-delete`, weight 0): lists
  `simulationexperiments.experiment.cbse.terministic.de` in the release
  namespace while the Scenario Manager is still alive. Zero objects → success;
  one or more → non-zero exit that blocks uninstall with the exact recovery
  command (delete all experiments with `--wait`, then rerun `helm uninstall`).
  It never deletes or patches experiments, finalizers, PVCs, Secrets, or the
  CRD, and it prints a count, never experiment names.

Failed hook Jobs remain in place for diagnosis (`backoffLimit: 0`,
`restartPolicy: Never`) until the next Helm attempt replaces them
(`before-hook-creation,hook-succeeded` delete policy).

## CRD semantics

The alpha4-only CRD ships in `crds/`, byte-identical to the operator's
generated CRD (enforced by `make verify-generated`). Helm installs it at new
release install only; it is never upgraded, never migrated, and never deleted
by `helm upgrade` or `helm uninstall`. The CRD is cluster-scoped and shared by
every release; a non-alpha4 served/stored CRD is a deliberate cluster-admin
breaking-upgrade concern that the chart documents but never performs.

## Uninstall semantics

`helm uninstall` is guarded by the pre-delete experiment guard: it proceeds
only when no `SimulationExperiment` remains in the release namespace. The
supported workflow is CRs-first — delete all experiments with `--wait` while
the live Scenario Manager releases its finalizers, then rerun `helm
uninstall`. With default retention the PVCs (`data-sm-eds-nats-0`,
`data-core-db-0`) and the `core-db-credentials` Secret survive uninstall so a
reinstall reconnects to the retained data; deleting them is an explicit,
irreversible administrator action. The installation-owner `cbse-registry-auth`
Secret is never chart-owned and is never deleted by the chart. The CRD is
preserved (see CRD semantics).

## Verification

Static, hermetic (no cluster, no network beyond the pinned Helm binary, which
`make helm-tools` installs under the ignored root `bin/` directory):

```bash
make helm-lint        # helm lint charts/cbse with the public fixture values
make helm-render      # helm template --include-crds into a temp file
make test-helm-chart  # lint + render + schema/naming/CRD/hygiene contract tests
```

Cluster tiers (dedicated test cluster, explicit `KUBECONFIG`, environment-
provided registry — placeholders only):

```bash
# install -> upgrade -> guarded-uninstall lifecycle acceptance
make test-helm-lifecycle KUBECONFIG=/path/to/config \
  HELM_IMAGES_ENV=/path/to/digest-images.env CBSE_EXPECTED_CONTEXT=<context>

# the unchanged 15-spec smoke suite against a chart-installed stack
make test-helm-smoke KUBECONFIG=/path/to/config \
  CBSE_REGISTRY=<your-registry> CBSE_REGISTRY_AUTH_FILE=<protected-docker-config>
```

See the [testing guide](../../docs/CBSE_TESTING_GUIDE.md) for artifacts and
cleanup (`make test-clean RUN_ID=<id>` is CRs-first and Helm-release-aware).

## Versioning

- **Chart version** (`Chart.yaml` `version`, currently `0.1.0`): versions the
  packaged control-plane surface — templates, values contract, hooks.
- **Application/image release:** the chart carries no image defaults; the
  release process generates the private `values-images-<version>.yaml`
  (repositories + resolved immutable digests) that an installation consumes.
  Chart version and image release are independent axes.
- **alpha4 API:** the CRD copy in `crds/` is locked to the generated CRD; an
  API change updates the generated CRD and the chart copy together
  (`make verify-generated`), and the served/stored version boundary
  (alpha2/alpha3 → alpha4) is a documented, deliberate cluster-admin
  operation, never an automatic chart upgrade.
