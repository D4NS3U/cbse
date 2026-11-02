# CBSE chart — skeleton (pre-release)

**Status: H1 skeleton.** This chart currently defines the complete v0.1 value
surface, JSON schema, helper contract, and the alpha4-only
`SimulationExperiment` CRD copy. Component workload templates (Operator,
Scenario Manager, NATS/JetStream, Core DB) are added by the H2/H3 slices;
lifecycle safety by H4; the Helm smoke installer by H5; and the full
installation documentation by H6.

The normative specification lives in the repository feature devlog:

- Feature: [`devlog/changes/helm-control-plane-installation/FEATURE.md`](../../devlog/changes/helm-control-plane-installation/FEATURE.md)
- Skeleton contract: [`devlog/changes/helm-control-plane-installation/slices/H1-chart-skeleton-contract-tests.md`](../../devlog/changes/helm-control-plane-installation/slices/H1-chart-skeleton-contract-tests.md)

## Layout

| Path | Purpose |
|---|---|
| `Chart.yaml` | Chart metadata (v0.1.0, no dependencies) |
| `values.yaml` | Complete v0.1 value surface; no private infrastructure defaults |
| `values.schema.json` | JSON schema; rejects unknown keys, tag-based images, and malformed digests |
| `crds/` | Byte-identical copy of the generated alpha4 CRD (verified by `make verify-generated`) |
| `templates/_helpers.tpl` | Pinned helpers: labels, digest-only images, fixed names |

## Value contract (v0.1)

- One release namespace = the watched experiment namespace; the namespace is
  not a value (Helm `.Release.Namespace` is authoritative).
- Every CBSE image is `repository` + immutable `sha256:` digest. Repository
  and digest default to empty in source; the release process generates a
  private installation-owner values file, and templates fail with
  `required` when rendered without release values.
- Fixed compatibility names (never release-prefixed): `experiment-operator`,
  `scenario-manager`, `sm-eds-nats`, `core-db`, `core-db-credentials`.
- The registry pull Secret name is fixed to `cbse-registry-auth`
  (installation-owner managed, pre-existing); there is no registry Secret
  value.
- The Core DB credential Secret name is fixed to `core-db-credentials`
  (Helm-managed); there is no username/password or existing-Secret value.
- NATS is authentication-free in v0.1; no NATS credential/TLS values exist.
- There is no external-service branch in the v0.1 schema.

## Static verification

```
make helm-lint        # helm lint with the public fixture values
make helm-render      # helm template --include-crds into a temp file
make test-helm-chart  # lint + render + schema/CRD/hygiene contract tests
```

The fixture `test/helm/values-render.yaml` contains public, non-runnable
`example.invalid` repositories with syntactically valid digests only.
