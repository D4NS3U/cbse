# Slice H3 — Direct internal NATS/JetStream and PostgreSQL Core DB

Normative and self-contained. Read order: [../FEATURE.md](../FEATURE.md) §2, §3 D2/D4/D5/D7/D9/D12/D15, §4 Strategy/Builder, §5, §6 H3, Secret inventory, closed rulings; [H1](H1-chart-skeleton-contract-tests.md) and its landed contracts; root `AGENTS.md`; then this slice. Specs are read-only. Missing shared values/helpers are manager questions.

This is the data-plane half of parallel wave W2. H2 owns Operator/SM templates and application changes. Work in a dedicated child branch.

## Target

Sole ownership of:

- `charts/cbse/templates/nats/` — ConfigMap, client Service, headless Service, StatefulSet
- `charts/cbse/templates/core-database/` — Secret, client Service, headless Service, StatefulSet
- `charts/cbse/templates/network-policies/` — internal NATS and Core DB ingress policies
- `test/helm/assert-data-plane.sh`

No changes to chart metadata/values/schema/helpers/CRD, control-plane templates, Go code, harness smoke, image lock, docs, or `devlog/**`.

## Normative NATS resources

- Fixed client Service/StatefulSet name: `sm-eds-nats`.
- Additional governing headless Service: `sm-eds-nats-headless` (`clusterIP: None`, publish-not-ready addresses not enabled).
- One-replica StatefulSet; stable selector `app: sm-eds-nats`; `serviceName: sm-eds-nats-headless`.
- Image from `nats.image` via `cbse.image`; global pull policy. NATS is an official external image and does **not** use `cbse-registry-auth` unless the installation-owner release values point it at a private mirror; Pod imagePullSecrets may still reference the fixed Secret uniformly.
- Authentication-free configuration, matching alpha4:
  - client port 4222;
  - monitor HTTP port 8222;
  - JetStream store `/data/jetstream`;
  - `max_mem_store` and `max_file_store` from H1 values;
  - no user/password/token/TLS/account blocks.
- ConfigMap `nats-config`, mounted read-only.
- Readiness uses HTTP `GET /healthz?js-enabled-only=true` on monitor port if the pinned NATS version supports it; otherwise the proven TCP client probe is permitted only with a report line and targeted live proof. Liveness uses the monitor health endpoint or bounded TCP fallback.
- Runtime security: UID/GID/fsGroup 1000, non-root, RuntimeDefault, read-only root filesystem where compatible, drop ALL, no privilege escalation.
- Persistent mode: `volumeClaimTemplates` named `data`, requested size/storage class from values, mount `/data/jetstream`, StatefulSet `persistentVolumeClaimRetentionPolicy` retains on delete/scale when `retain=true`.
- Ephemeral mode: no claim template; `emptyDir` named `data`. The StatefulSet remains the same kind/name.

## Normative Core DB resources

- Fixed client Service/StatefulSet name: `core-db`.
- Governing headless Service: `core-db-headless`.
- One-replica StatefulSet; selector `app: core-db`; `serviceName: core-db-headless`.
- Image from `coreDatabase.image` via `cbse.image`.
- Database from `coreDatabase.database`; `PGDATA=/var/lib/postgresql/data/pgdata` for PVC compatibility.
- Helm-managed fixed Secret `core-db-credentials`:
  - `type: Opaque`;
  - exact data keys `SCENARIO_MANAGER_CORE_DB_USER`, `SCENARIO_MANAGER_CORE_DB_PASSWORD`;
  - first render against a cluster with no Secret uses username `cbse` and `randAlphaNum 48` password;
  - later upgrade uses `lookup` to preserve both existing encoded values byte-for-byte;
  - an existing Secret missing either key triggers `fail`, never silent rotation;
  - no `stringData`, no values-supplied credential;
  - annotation `helm.sh/resource-policy: keep` when retention is true.
- PostgreSQL env maps the exact Secret keys to `POSTGRES_USER` / `POSTGRES_PASSWORD`; no credential appears in a ConfigMap, command, probe, note, or rendered non-Secret object.
- `pg_isready` readiness/liveness using env-expanded user/database.
- Official PostgreSQL entrypoint retains the minimum startup permissions it requires; do not force the application-container restricted context onto it. Pod RuntimeDefault applies; no privileged/host settings.
- Persistent mode: claim template `data`, values size/class, mount `/var/lib/postgresql/data`, retention policy consistent with values.
- Ephemeral mode: `emptyDir` at the same mount.

## NetworkPolicies

Rendered only when `networkPolicies.enabled=true`:

1. `sm-eds-nats-ingress` selects `app: sm-eds-nats` and allows:
   - TCP 4222 from any Pod in the same namespace (`podSelector: {}`), because the SM, EDS, Translator, and PPS all use NATS;
   - TCP 8222 only from Pods carrying `app.kubernetes.io/part-of: cbse` (plus node/kubelet probe behavior is not modeled as an ingress peer).
2. `core-db-ingress` selects `app: core-db` and allows TCP 5432 only from `app: scenario-manager` Pods.

No egress policy and no namespaceSelector/cross-namespace allowance in v0.1. Policies contain no cluster CIDRs.

## Change

1. Render NATS resources and persistent/ephemeral branches exactly above.
2. Render Core DB resources and chart-generated credential lifecycle exactly above.
3. Render enabled-by-default NetworkPolicies.
4. Add `assert-data-plane.sh` to prove:
   - fixed + headless names, StatefulSets, one replica;
   - digest image refs and no NATS auth fields;
   - persistent defaults create claim templates and Retain policies;
   - ephemeral render uses `emptyDir` and no PVC template;
   - storage class omission does not emit an empty `storageClassName`; explicit class emits exactly the value;
   - Secret exact keys, keep annotation, lookup branch text, no credential value in non-Secret output;
   - probes, ports, mounts, selectors, and NetworkPolicy peers/ports.
5. Tests render twice where useful but never compare generated password bytes; they assert structure and key reuse logic statically.

## Constraints

- Direct templates only: no chart dependencies, operators, or external-service values.
- No NATS credentials Secret. Alpha4 remains authentication-free.
- No HA claim: one replica for both stateful services.
- Persistent PVC retention and Secret retention must agree; `retain=false` may permit deletion but must not silently leave one without the other.
- No hard-coded storage class, node selector, hostname, registry, or endpoint outside fixed in-namespace Services.
- No chart shared-contract edits. H2 may land concurrently.
- `make test-fast` is required for chart/test additions. W2 consolidated cluster smoke is manager-owned after H2+H3 settlement.
- Child-branch commits with orchestration trailers; specs read-only.

## Ownership

Only Target paths. Report any NATS health-endpoint incompatibility with the selected locked image; do not weaken readiness without evidence.

## Observable acceptance

1. Attestation → `ai.forge/qwen3.8-27b-nvfp4`.
2. Containment: Target paths only.
3. `make helm-lint` and `make helm-render` → rc=0.
4. `make test-helm-chart` → rc=0; data-plane assertion script runs.
5. Persistent render receipts: claim templates, sizes, omitted default storageClass, Retain policies, Secret keep annotation.
6. Ephemeral render receipt: zero PVC templates, two `emptyDir` data volumes.
7. Secret hygiene receipt: only the Secret contains credential keys/data; no literal generated/user password; no NATS credential material.
8. NetworkPolicy receipt: exact selectors/ports/peers; disabled render emits none.
9. `make test-fast` → rc=0.
10. Commit HEAD/trailers and report line for W2 consolidated smoke.

Complete with one `worker_done`, explicit outcome, IDs, attestation, evidence, modified files, and report path; check coordinator messages before completion.
