# Slice S5 — Harness & stack integration: the reference PPS image in the mandatory component set, the real-PPS smoke profile

Normative and self-contained for its worker. Read order and global constraints: [../FEATURE.md](../FEATURE.md) §5 (verbatim discipline), §3 (esp. D1, D6, D7 and the rulings record), §6 S5 row **including its 2026-09-28 erratum** (no stack-level PPS Deployment — S3 provisions it per-experiment; SKIP_BUILD env is `PPS_IMAGE`; `CBSE_PPS_IMAGE` is only the manifest placeholder); then repository-root `AGENTS.md`. S1–S4 are landed (commits `5b3efa4`…`498a17f`): the SM messaging, the operator provisioning, and the reference module at `component-templates/post-processing-service/` (its Dockerfile already requires the locked `PPS_GO_BUILDER_IMAGE` build arg — your lock extension feeds it). This slice extends, never overrides, the umbrella. All three documents are read-only for you. On any contradiction between this slice and live code that inspection cannot resolve: stop and ask via your ask channel.

## Target

Exactly these files (the complete list — everything else is out of bounds):

- `test/e2e/images.lock.env` — add the `PPS_GO_BUILDER_IMAGE` / `PPS_GO_VERSION` pair (same locked Go toolchain as the translator's: `docker.io/library/golang@sha256:3bf5b04541eb4a37fe62aa1bc9c98a1dec09db9d2e79c1d2eb54e3c9d08dbca9` / `1.26.3-bookworm`).
- `test/harness/image-lock.sh` — extend the lock loader from eight to ten keys (the new pair read verbatim, env-override rejected, same digest/version format validation as the existing pairs; doc comment updated).
- `test/harness/build-images.sh` — the default component list and doc comment gain `pps`; add the build line: `component_enabled pps && build_nested pps PPS_IMAGE "${root}/component-templates/post-processing-service/Dockerfile" "${root}/component-templates/post-processing-service" "CBSE Post Processing Service" --build-arg "PPS_GO_BUILDER_IMAGE=${PPS_GO_BUILDER_IMAGE}"` (mirroring the translator line's shape; the component token is **`pps`** → image path `<registry>/pps`, env `PPS_IMAGE`).
- `Makefile` — line 17: append `,pps` to `CBSE_IMAGE_COMPONENTS` (the self-test pins the exact line; keep it in lockstep).
- `test/harness/test-harness.sh` — every component-set truth 6→7 in lockstep: the `grep -Fqx` Makefile pin (line ~137), the fake-docker build fixture's component list (~207) and its per-image greps (canonical + immutable tags for `pps`), the build-arg pin (add `ARG PPS_GO_BUILDER_IMAGE=docker.io/library/golang@sha256:3bf5...`), the `images.env` grep (add the `^PPS_IMAGE=registry\.unibw\.de/i31bdase/cbse-test/pps@sha256:[a-f0-9]{64}$` line), the default-build fixture's expectations, and the lock-loader tests for the two new keys. Extend, never weaken: the unknown/duplicate-token rejection tests stay (they use `bogus`/duplicates — unaffected by the new valid token).
- `test/harness/preflight.sh` — `PPS_IMAGE` joins the SKIP_BUILD immutable-digest validation (either existing loop) and the doc comment's env list.
- `test/harness/smoke.sh` — doc comment env lists; the SKIP_BUILD block requires and sources `PPS_IMAGE`; the export line; the `images.env` printf gains the `PPS_IMAGE` line; the experiment-manifest sed gains `-e "s|CBSE_PPS_IMAGE|${PPS_IMAGE}|g"` (the existing `CBSE_SUPPORT_IMAGE`→`${EDS_IMAGE}` line stays — it now covers only the EDS section).
- `test/e2e/manifests/experiment.yaml` — `postProcessingService.image: CBSE_SUPPORT_IMAGE` → `postProcessingService.image: CBSE_PPS_IMAGE` (the `experimentalDesignService` keeps `CBSE_SUPPORT_IMAGE`); the `postProcessingService` section additionally gains `args: ["-max-runners-per-round", "30"]` (ruling 2026-09-28 — bounds the low-fleet scenarios' wave sizes against small-sample estimator noise; the clamp is convergence-preserving, see `notes/additional-wave-estimation.md` §3–5; the component default stays 1000 per ruling Q4).
- `test/mocks/eds/eds_mock.py` — the batch's per-scenario `number_of_reps` becomes the determinism lever (ruling Q6, extended 2026-09-28): scenario idx 1 (priority 1) carries the met-path fleet **`30`**; scenarios idx 2–4 (priorities 2–4) each carry the loop-path fleet **`1`** (deterministically not-met on wave 1 via the degenerate n<2 rule — each naturally tops up; S6's loop spec targets all three). **`confidence_metric` stays exactly as it is** (`round(0.90 + (idx * 0.01), 3)` — real, sensible precision demands, untouched per ruling Q6). Update the fleet comment accordingly.
- `test/e2e/smoke_test.go` — **manager-licensed e2e truth update (exactly four line-contacts, mirroring the S3 precedent):** the chain spec (1) the `It(...)` description at line ~166 ("…through the full reference Translator chain to PostProcessing and persists results" → "…to **Finished** and persists results"); (2) its leading comment lines describing the PostProcessing wait (now: the chain runs into the real PPS evaluation and the met verdict lands the scenario in the terminal `Finished` state); (3) the state query at line ~174: `ss.state='PostProcessing'` → `ss.state='Finished'`; (4) the same query gains the met-path target filter `AND ss.priority = 1` (the high-fleet scenario — without the filter, the first-to-PostProcessing catch could grab the low-fleet loop scenario, which deterministically waves and would break the pin below). **Every other line of `smoke_test.go` is out of bounds** — in particular the completed-Job assert, the `number_of_computed_reps == number_of_reps` pin (stays true for the met-path scenario: met-on-wave-1 means no additional runners), the results/seed asserts, the idempotence count, and the GC cascade.

Everything else is untouchable — in particular `scenario-manager/**`, `experiment-operator/**`, `component-templates/**` (all landed S1–S4 work), `test/e2e/smoke_test.go` beyond the three licensed contacts, `api/**`, `docs/**`, `devlog/**`, `go.work`, module `go.mod`/`go.sum` files.

## Change

1. **Lock pair.** `images.lock.env` + `image-lock.sh`: the PPS pair uses the identical Go toolchain digest as `TRANSLATOR_GO_BUILDER_IMAGE` (the PPS module builds with the same locked toolchain; the separate key keeps per-component lock hygiene and matches S4's already-landed `ARG PPS_GO_BUILDER_IMAGE`).
2. **Build path.** `build-images.sh` + `Makefile`: token `pps` in the default set (appended last), nested image `<registry>/pps:<version>` (+ immutable provenance tag), locked build-arg forwarded; missing lock key → build fails fast (existing loader behavior).
3. **Self-test.** All enumerated truths extended to the 7-component reality (the pin, fixtures, greps, default-build expectations, lock tests). The self-test runs inside `make test-fast` — its green run is your primary evidence.
4. **Preflight.** `PPS_IMAGE` validated as an immutable digest when `SKIP_BUILD=1`, exactly like the other six.
5. **smoke.sh plumbing.** Build path unchanged (the Makefile's component set now includes `pps`); SKIP_BUILD requires `PPS_IMAGE` (old run dirs without it fail — correct, they cannot run the new chain); exports + `images.env` carry it; the manifest substitution injects the real PPS image into `postProcessingService.image`.
6. **Smoke profile (fleet-size determinism, ruling Q6 + extension).** ε keeps its real values; the batch's fleet sizes carry the determinism: the priority-1 scenario (30 reps) evaluates to `met` on wave 1 **with a margin** (the manager's settlement run verifies the achieved margin — if ε/h < 2, raise the met-path fleet size and re-run; the fleet is the honest tuning lever, ε never moves), and the priority-2–4 scenarios (1 rep each) are deterministically not-met via the degenerate rule (S4: n<2 → not-met with the min-batch), each naturally topping up — three independent loop subjects for S6's spec. The experiment's `-max-runners-per-round 30` bounds every wave so small-sample estimator noise cannot become an unbounded pod burst (waves converge over more rounds instead; the clamp is convergence-preserving). The chain spec catches the met-path scenario's terminal `Finished` state — race-free. The `number_of_computed_reps == number_of_reps` pin holds for the met-path scenario.
7. **Docs-in-harness only.** Update the harness files' own doc comments to the 7-component truth; do not touch repository docs (`docs/**` — a stale component-set mention there, if any, is a report line, not yours to fix).

## Constraints

- **Extend, never weaken** the harness self-tests (P1): every 6-component truth becomes the 7-component truth in the same run; no assertion is deleted, skipped, or loosened.
- The e2e edit is exactly the four licensed contacts; the mock edit is the per-scenario `number_of_reps` values + comment (`confidence_metric` untouched); no other test/e2e or mock changes.
- The private values already present in the harness (registry, cluster endpoint) stay as-is — no new private values anywhere, no secrets in commands/logs (P3/P5).
- No cluster operations, no image builds/pushes, no `make test-smoke` — **the cluster smoke is the settlement gate, run by the manager** after your `worker_done` (single serialized worker per the LAPI policy; the manager's run uses the standard recipe with the user's environment).
- `make test-fast` rc=0 is mandatory (it contains the extended self-test).
- Global constraints of FEATURE.md §5 apply verbatim (attestation, no commits, read-only specs, scope discipline, verification honesty).

## Ownership

Sole owner of the eleven named files in this solo wave. Discovered gaps → report lines, do not fix.

## Observable acceptance

Run and echo all of these in `worker_done` (the manager re-runs each independently, then runs `make test-smoke` as the settlement gate):

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat; mismatch → stop, `--outcome failed`).
2. Containment: `git status --short` shows exactly your eleven files.
3. `make test-fast` rc=0 final receipt (mandatory — the extended self-test runs inside it).
4. Self-test receipts: echo the passing pin/grep lines for the 7-component set (the `grep -Fqx` Makefile pin, the `pps` canonical+immutable tag greps, the `ARG PPS_GO_BUILDER_IMAGE` pin, the `images.env` `PPS_IMAGE` line) — quote them from the test-fast output or re-run the self-test verbosely.
5. grep receipts: the `PPS_GO_BUILDER_IMAGE`/`PPS_GO_VERSION` lock lines; the `image-lock.sh` 10-key list; the `build-images.sh` pps line + default list; the `Makefile` line; the `preflight.sh` SKIP_BUILD list; the `smoke.sh` substitution + export + images.env lines; the `experiment.yaml` placeholder + args line; the mock's fleet-size lines (30/1/1/1, `confidence_metric` untouched); the four licensed `smoke_test.go` contacts.
6. Lock-loader proof: the self-test's lock tests for the new keys (names/lines + PASS).

Completion protocol: `worker_done` with a three-sentence executive summary, both lifecycle IDs (task + dispatch from your preamble), explicit `--outcome succeeded|failed`, the verbatim attestation line, the six evidence blocks, `--files-modified`.

**Session hygiene (binding, learned from the S1/LAPI crashes):** keep tool outputs small (`head`/`tail`/`grep -n`, never whole-file cats); gather evidence incrementally as you complete each requirement; never batch everything into one giant end-session run.
