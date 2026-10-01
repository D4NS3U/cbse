# T1 — The Error flavors spec: Validation-Error and Provisioning-Error (creation variant)

**Dispatch record (Wave 1, experiment-terminal-e2e).** Read order: `devlog/changes/experiment-terminal-e2e/FEATURE.md` (§1, §3 D1–D3, §5) → `AGENTS.md` → this spec → code. On contradiction the FEATURE.md cannot resolve through repository inspection: stop and ask via your `ask` channel.

## Target

- `test/e2e/smoke_test.go` — exactly two new `It(...)` specs, inserted after the `It("reconciles an idempotent metadata update without duplicating children")` spec and before the `It("garbage-collects owned resources and cascades persisted state")` spec (the green experiment is still alive there — the specs read it), plus any small unexported helpers local to the file (the builder).

## Change

Two red experiments prove the operator's two `Error` writers live (FEATURE.md D1, flavors 1 and 2; flavor 3 — the readiness variant — is slice T2's, not yours):

1. **The Validation-Error spec** — `It(...)` proving the pre-creation gate: build a red experiment by deep-copying the **live green experiment's spec** (the builder: `k8sClient.Get` the green CR, `DeepCopy`, change the name to `<project>-errval`, and set exactly one delta: `translator.image` to a tag-form reference without a digest, e.g. `registry.example.invalid/translator:v1`); create it via `k8sClient`. Then assert:
   - `Eventually`: `status.phase == "Error"` and the message names the image validation (the digest-form error);
   - **zero children**: no Deployment, Service, Secret, or ConfigMap named `<project>-errval-…` exists (the validation failed before any component was created — enumerate the owned-suffix set the way the InProgress spec does);
   - stickiness: after an additional reconcile window (or an explicit annotation-triggered reconcile), the phase stays `Error` and still no children exist;
   - cleanup: delete the red experiment and assert the object goes away.

2. **The Provisioning-Error spec (creation variant)** — `It(...)` proving the mid-sequence failure with partial children: first create a **blocker Service** (a small ClusterIP/NodePort Service in the test namespace holding a NodePort in the valid 30000–32767 range — pick a port and keep it constant); then build the red experiment the same way (name `<project>-errprov`) with exactly one delta: `postProcessingService.serviceType = NodePort` and `postProcessingService.nodePort = <the blocker's port>`; create it. Then assert:
   - `Eventually`: `status.phase == "Error"` (the API server rejects the PPS Service creation — "provided port is already allocated" — and `reconcilePPS` fails mid-sequence);
   - **partial children persist**: the detaildb and resultdb Deployments + Services + Secrets, and the translator Deployment + Service + translator-cfg ConfigMap, all exist (everything provisioned before the failure point — verify against `provisionComponents`' order: detaildb → resultdb → translator → PPS → runner ServiceAccount);
   - stickiness: the phase stays `Error` and the partial set is untouched across a follow-up reconcile (report-only semantics — no auto-teardown);
   - **the GC cascade over the partial set**: delete the red experiment and assert every owned child disappears (`Eventually` on the enumerated set);
   - teardown the blocker Service (the spec's own fixture).

Both specs persist their terminal evidence (phase, message, the children inventory) to the artifact directory per the suite's triage discipline. Red names must satisfy the operator's DNS-label rule (≤63 lowercase chars — `<project>-errval`/`-errprov` inherit it).

## Constraints

- **All other specs byte-identical** — the diff is insertions in one file (`git diff` shows no other change).
- **The green experiment and its resources are never touched** — the red experiments are separate CRs with separate names; the blocker Service is your own fixture and is torn down.
- No platform changes: no operator/SM/PPS code, no API types, no manifests — this slice is test code only (T2 owns the watchdog; T3 the cap wiring).
- No cluster run by you: your tier gate is `make test-fast` (the e2e compile checks run inside it); the live run is the manager's consolidated settlement smoke.
- **No test weakening** anywhere.
- **Branch discipline:** commit only to this branch (never `main`), the commit carrying the trailer `[orchestration: task <task-id> dispatch <dispatch-id>]` with your actual IDs. `artifacts/` is never committed.

## Ownership

Exactly: `test/e2e/smoke_test.go` (two `It` blocks + local helpers). Any discovered gap outside this ownership stops work with an `ask`.

## Observable acceptance

1. At your **first checkpoint** run and record:
   ```bash
   printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
   ```
   Expected exactly `ai.forge/qwen3.8-27b-nvfp4`. On mismatch: stop, report `failed`.
2. `make test-fast` from the repo root: rc=0 (includes `go vet -tags=e2e` and the e2e compile for `test/e2e`).
3. Greps for the report: the two new `It(...)` titles present; `git diff --numstat` shows one file, insertions only; the builder deep-copies the green experiment's spec (no hand-written CR).
4. `worker_done` from the dispatched terminal, exactly once, with: a three-sentence executive summary, both lifecycle IDs, `--outcome succeeded|failed`, the branch HEAD SHA, a one-line diff summary, `--files-modified`, `--report-path` (`artifacts/orchestration/<task-id>-report.md`), and the attestation line. Then idle.
