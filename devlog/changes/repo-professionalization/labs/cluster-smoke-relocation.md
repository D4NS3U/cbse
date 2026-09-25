# cluster-smoke.yml relocation runbook (D3 = relocate)

> **Superseded 2026-09-25:** the consolidation decision returned all relocated content to the public cbse repository and retired the private cbse-labs repository; this runbook is kept as the historical record of the D2 relocation it executed.

Staging was performed by this slice (A4 of [`public-repo-hygiene`](../../public-repo-hygiene/FEATURE.md)); the user executes the private-repository steps at D2/D3 and confirms preservation (P6). Umbrella gate snapshot: D3 = relocate — A4 stages the preserved copy; the public file is deleted only after the user confirms the preservation (a blocked sub-step of A3, executed by a follow-up dispatch).

**Deletion condition (explicit):** *the public `.github/workflows/cluster-smoke.yml` is NOT deleted by Package A until the user confirms this preservation.*

## Staging record (executed by A4)

| Property | Value |
|---|---|
| Source (public repo) | `.github/workflows/cluster-smoke.yml` |
| Preserved copy (this tree) | [`ci/cluster-smoke.yml`](ci/cluster-smoke.yml) |
| Source measurement at start | 63 lines / 1839 bytes |
| Source sha256 | `b1295cd3f93b421b5afcc90ae6ab0ae8d0684fcd` |
| Copy measurement | 63 lines / 1839 bytes |
| Copy sha256 | `b1295cd3f93b421b5afcc90ae6ab0ae8d0684fcd` |
| Byte identity | `cmp .github/workflows/cluster-smoke.yml devlog/changes/repo-professionalization/labs/ci/cluster-smoke.yml` → clean (no output, rc=0) |

The measurement at decomposition time was 63 lines / 1839 bytes; the fresh measurement at this slice's start matches, so the public file was untouched between decomposition and staging. The copy is a byte-identical preservation — it carries **no** "improvements", comments, or edits; content-editing it is forbidden because it is preserved evidence for the user move.

## What the workflow references (D2-bound citations)

The following values appear in the preserved workflow and are quoted **exactly as they appear in the file** so the private-repository setup below is unambiguous. They are private operational values (P3/P5): this runbook quotes them only here, only where unavoidable, and no further. Per the umbrella §7 gate, this file — together with the preserved copy itself — is the only A4 content where such values are legally present, and they are D2-bound content (the charter's fresh-clone grep-clean item completes only after D2 moves `devlog/**` wholesale).

Workflow identity: `name: K3s full-stack smoke`; triggers `workflow_dispatch`, a nightly `schedule` (cron `17 2 * * *`), and `push` to `main`; concurrency group `cbse-k3s-smoke` (no in-progress cancellation).

Runner and environment (as they appear in the file):

```yaml
    runs-on: [self-hosted, linux, x64, cbse-k3s]
    environment: cbse-k3s
```

Job environment (as it appears in the file):

```yaml
    env:
      KUBECONFIG: /home/d4ns3u/.kube/config
      CBSE_REGISTRY: registry.unibw.de/i31bdase/cbse-test
      TEST_IMAGE_VERSION: "26.7.16"
```

Credential material arrives through the repository secret `CBSE_REGISTRY_DOCKERCONFIG_JSON`, which the workflow materializes into a temporary `CBSE_REGISTRY_AUTH_FILE` and removes after the run — the secret value itself is not part of this copy and must never be written into any repository (P5).

## User steps at D2/D3

1. **Create the private `cbse-labs` repository** (external action — user, P6; gate D2) and clone it.
2. **Place the preserved copy** in that repository's `.github/workflows/cluster-smoke.yml` (byte-identical — after the move, re-run `shasum` on the moved file and confirm it still reads `b1295cd3f93b421b5afcc90ae6ab0ae8d0684fcd`).
3. **Register the self-hosted runner** on that private repository with the labels as they appear in the file — `self-hosted`, `linux`, `x64`, `cbse-k3s` — and ensure the GitHub environment `cbse-k3s` exists there (the workflow declares `environment: cbse-k3s`).
4. **Provision the private-repository inputs**: the secret `CBSE_REGISTRY_DOCKERCONFIG_JSON` and any environment values the relocated workflow needs (the job-level `env:` block above is part of the file; only the Docker-config secret lives outside it).
5. **Verify**: trigger the relocated workflow with `workflow_dispatch` and confirm a green run of `make test-smoke` against the private cluster tier.
6. **Confirm preservation** (user decision): acknowledge that the workflow is live in cbse-labs.

Only after step 6 does the follow-up dispatch (blocked sub-step of A3) delete the public `.github/workflows/cluster-smoke.yml` from this repository. Until then it remains tracked here, untouched by Package A.

See [move-list.md](move-list.md) for the companion D2 move runbook and [inventory.md](inventory.md) for the content inventory.
