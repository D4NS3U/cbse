# Slice H6 — Helm installation, operations, and architecture documentation

Normative and self-contained. Read order: [../FEATURE.md](../FEATURE.md), settled H1–H5 slices and their live CLI/value contracts, root `AGENTS.md`, then this slice. All feature/slice specifications are read-only. This is final wave W5; document only behavior proven by H1–H5.

## Target

Sole ownership of documentation surfaces:

- `charts/cbse/README.md` — replace H1 skeleton with complete chart guide
- `charts/cbse/templates/NOTES.txt` — concise post-install operational next steps
- root `README.md`
- `docs/CLUSTER_REQUIREMENTS.md`
- `docs/CBSE_TESTING_GUIDE.md`
- new `docs/HELM_INSTALLATION.md`
- `docs/wiki/README.md`, `docs/wiki/architecture.md`, `docs/wiki/getting-started.md` — links/installation topology only; do not regenerate or rewrite module references
- `test/e2e/README.md` only if H5 left a documentation TODO explicitly assigned here

No chart logic/values/schema, code, manifests, harness behavior, Makefile, CRD, or `devlog/**` changes.

## Required documentation contract

### `docs/HELM_INSTALLATION.md` (canonical user guide)

1. **What is installed:** alpha4 CRD; Operator; SM; internal single-node NATS/JetStream; internal single-node Core DB. Explicit list of experiment-scoped resources that remain dynamically provisioned.
2. **Prerequisites:** Kubernetes >=1.30; `UserNamespacesSupport`; experiment namespace Pod Security exception; Helm pinned/supported major; default StorageClass or explicit storage values; registry access; architecture/image support; one release per namespace.
3. **Secret inventory:** exact concise table from FEATURE §3.1, with ownership and lifecycle. Highlight:
   - exactly one installation-owner-only `cbse-registry-auth`, fixed name/type, never Helm-owned;
   - Helm-only generated/retained `core-db-credentials`, never supplied in values;
   - operator-generated per-experiment endpoint Secrets;
   - no NATS/TLS Secret in v0.1.
4. **Secure creation prerequisite:** show only a payload-safe pattern such as creating `cbse-registry-auth` from a local `.dockerconfigjson` file in the target namespace; never put username/password/token in command arguments or examples. Verification commands inspect only name/type, never data.
5. **Image release values:** explain public chart source vs optional packaged publication vs private images; use `make helm-image-values IMAGES_ENV=<digest-file> OUTPUT=<private-file>`; generated file is private/untracked and digest-only.
6. **Install:** create namespace and required Pod Security labels; create registry Secret; run `helm upgrade --install ... --wait --atomic -f <private-image-values>`, using placeholders only.
7. **First experiment:** point to a public example/acceptance CR with digest placeholders and explain same-namespace requirement; link EDS port-forward workflow.
8. **Observe:** Helm status, Pods/StatefulSets/PVCs, CR phase, SM readiness—no Secret inspection commands.
9. **Upgrade/rollback:** stable credentials/PVCs; one-replica/no-zero-downtime limitation; `helm upgrade`; rollback caveat for CRD changes; never assume Helm upgrades CRDs.
10. **Uninstall:** explain pre-delete guard; exact CRs-first workflow; retained PVC/Core DB Secret behavior; explicit optional destructive cleanup commands naming only chart-owned PVC/Secret and warning that data is irreversible; CRD is preserved/shared and deleted only by deliberate cluster-admin action after all releases/CRs are gone.
11. **Troubleshooting:** missing/wrong registry Secret, no default StorageClass/Pending PVC, `UserNamespacesSupport`/BuildKit failure, Pod Security rejection, SM not Ready (Core DB/NATS/RBAC), hook Job logs, retained failed smoke.
12. **Limitations/roadmap:** single namespace, single-node persistence not HA/backup, authentication-free internal NATS, literal DB credentials in current CR API, external Core DB before secure external NATS, no automatic Secret distribution.

### Chart README

A compact chart-specific reference:

- installation command linking canonical guide;
- complete values table (every H1 value; defaults; immutable-digest requirement);
- fixed resource names and one-release-per-namespace constraint;
- Secret, persistence, hook, CRD, and uninstall semantics;
- supported tests (`helm lint`, `helm template`, lifecycle, full smoke);
- versioning: chart version vs application/image release file vs alpha4 API.

### `NOTES.txt`

Rendered, concise, no credentials/private values:

- release/namespace and readiness summary;
- verify Pods and create `SimulationExperiment` in same namespace;
- registry Secret was validated but remains installation-owner managed;
- retained-data/uninstall guard reminder;
- link/reference to chart README (no external operational hostname).

### Existing docs

- Root README: Helm is the recommended installation path once v0.1 is present; source smoke remains development validation; link install guide and chart.
- Cluster requirements: replace “future Helm chart” language with implemented chart prerequisites, keeping all technical feature-gate facts intact.
- Testing guide: exact `helm-lint`, `helm-render`, `test-helm-lifecycle`, `test-helm-smoke`, retained Helm e2e, artifacts, and cleanup commands; explicit KUBECONFIG and registry env placeholders.
- Code wiki: architecture says Helm owns the shared control plane and the Operator/SM own experiment resources; getting-started links installation guide. Do not claim the code wiki was regenerated at a new source SHA.

## Change

1. Write the canonical install/operations guide.
2. Complete chart README and NOTES.
3. Update root/requirements/testing/wiki entry points.
4. Cross-link without circular dead ends: root → install guide → chart README/testing/requirements; chart README → install guide; wiki → install guide.
5. Run examples against `helm template`/live command help where possible; names/values must match landed code exactly.

## Constraints

- No private registry, API server, hostname, kubeconfig path, storage class, credential path, or payload.
- Never show password/token command-line arguments. `.dockerconfigjson` file import example may use a relative placeholder file only.
- Do not call PVC persistence HA or backup.
- Do not promise external services, multi-namespace, secure NATS, public chart publication, automatic CRD upgrades, or zero downtime.
- Institution attribution, LICENSE headers, and paper references remain intact.
- Documentation-only: no cluster smoke required by AGENTS, but chart render/link/example checks are mandatory.
- Child-branch commits/trailers; specs read-only.

## Ownership

Only Target docs. Technical discrepancy → ask/report; do not fix implementation in this slice.

## Observable acceptance

1. Attestation → mandated implementation model.
2. Containment: docs-only Target paths.
3. `make helm-lint`, `make helm-render`, and `make test-helm-chart` → rc=0 (examples/value names remain valid).
4. Markdown relative-link checker over all changed files → zero broken links.
5. Command/name audit: every Make/Helm/value/resource name exists in landed files.
6. Secret-hygiene scan: no credentials, private infra, absolute local path, decoded Secret command, or password/token CLI argument.
7. Content receipts: headings covering prerequisites, every Secret, installation, first experiment, upgrade, uninstall guard, retained data, CRD lifecycle, troubleshooting, limitations, external roadmap.
8. Diff confirms no implementation file changed.
9. Commit HEAD/trailers and report path.

Complete with one `worker_done`, explicit outcome, lifecycle IDs, attestation, evidence 1–9, files modified, and report. Check coordinator messages before completion.
