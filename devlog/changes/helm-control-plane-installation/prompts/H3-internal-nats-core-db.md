# Dispatch prompt — H3: Direct internal NATS/JetStream and Core DB

**Task title:** H3 — Render direct single-node NATS/JetStream and PostgreSQL Core DB StatefulSets: persistent-by-default/ephemeral-dev branches, retained PVCs, Helm-only stable `core-db-credentials`, fixed Services, and namespace-local NetworkPolicies

**Read order (mandatory/read-only):** 1) Helm feature `FEATURE.md` (§2, D2/D4/D5/D7/D9/D12/D15, Secret inventory, §4–§6, rulings); 2) `slices/H3-internal-nats-core-db.md`; 3) settled H1 chart contracts; 4) root `AGENTS.md`; 5) smoke `stack.yaml` only as the proven behavioral baseline. Missing shared value/helper → ask; do not alter H1.

**Target summary:** own only `charts/cbse/templates/{nats,core-database,network-policies}/` and `test/helm/assert-data-plane.sh`. Render fixed client Services `sm-eds-nats`/`core-db`, headless governing Services, one-replica StatefulSets, digest images, direct authentication-free JetStream config, exact probes/security/resources, retained claim templates by default, `emptyDir` development branch, and enabled-by-default ingress policies. Helm exclusively creates fixed `core-db-credentials`: exact two keys, user `cbse`, 48-char generated password, `lookup` reuse, fail on malformed existing Secret, keep with Core DB PVC; no password values/existingSecret branch. No NATS Secret/auth/TLS.

**Parallel wave:** H2 runs in a different child worktree and owns control-plane templates/application code. Do not edit H1 contracts or H2 paths.

**Acceptance summary:** persistent/ephemeral/storage-class/retention renders all asserted; Secret keys/lifecycle/no leakage; NATS auth absent; exact NetworkPolicies; digest-only images; `make helm-lint`, `make helm-render`, `make test-helm-chart`, `make test-fast` rc=0. W2 consolidated smoke is manager-owned after merge.

**Branch/protocol:** dedicated child branch, never `main`; every commit body carries `[orchestration: task <task-id> dispatch <dispatch-id>]`. First checkpoint runs `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` and must print `ai.forge/qwen3.8-27b-nvfp4`; mismatch stops with failed outcome. Specs/devlog read-only, bounded outputs, ten evidence blocks collected incrementally. Check coordinator messages at checkpoints and immediately before one `worker_done` carrying summary, IDs, outcome, attestation, evidence, files, report path, and W2 smoke report line.
