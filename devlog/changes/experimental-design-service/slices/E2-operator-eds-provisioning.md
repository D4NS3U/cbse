# Slice E2 — The operator's `reconcileEDS` (`experiment-operator/`)

Normative and self-contained for its worker. Read order and global constraints: [../FEATURE.md](../FEATURE.md) §5 (verbatim discipline + the parallelism contract pin — the env contract), §3 (esp. D2, D5, D6, D7 and the rulings record R4/R5; the SBO ruling + its diagnostic resolution — which confirms **E2 is unchanged by the SBO protocol: no DB reads, no verdict-semantics change**), §6 E2 row; then repository-root `AGENTS.md`. Your archetype is the landed PPS/Translator provisioning in `experiment-operator/internal/controller/simulationexperiment_alpha4_controller.go` — the per-component Deployment+Service build, the `ppsEnvVars`/`translatorEnvVars` pattern, the volume builders, the restricted security context, the `checkReadiness` gate loop, `ValidateDigestImage`. This slice is part of a parallel wave: E1 owns `component-templates/experimental-design-service/**` (+ `go.work` + three Makefile line-contacts) and E3 owns `scenario-manager/**` in the same worktree — their uncommitted files are expected co-residency, never yours to touch. All three documents are read-only for you. On any contradiction between this slice and live code that inspection cannot resolve: stop and ask via your ask channel.

## Target

Exactly these surfaces — all in `experiment-operator/internal/controller/` (plus its tests) unless named otherwise:

- **The EDS provisioning** — a `reconcileEDS` mirroring the landed `reconcilePPS` (same placement in the per-experiment reconciliation sequence): the Deployment `<exp>-eds` from `spec.experimentalDesignService` (image, command, args, port — the spec's `Port` field, default 8080; no `builderImage`/registry-auth surface — the EDS pulls nothing), the env contract (below), the detaildb Secret volume + mount, the restricted security context (mirror the landed one), ownership refs on every child (the existing pattern).
- **The EDS Service** — from `serviceType` (default ClusterIP) / `nodePort` / `port`, mirroring the landed PPS/Translator Service build.
- **`edsEnvVars(instance)`** — the D2 env set (below), mirroring `ppsEnvVars` structurally (a new derivation helper for the availability subject beside `ppsRequestSubject`/`translatorRequestSubject`, reusing the existing `translatorNATSURL` constant and the three downward-API env helpers).
- **Image validation** — `ValidateDigestImage` on `spec.experimentalDesignService.image` (D6, ruled R4: yes), beside the landed PPS/Translator checks in the validation path.
- **Readiness-gate membership** — add `"eds"` to the component suffix loop in `checkReadiness` (currently `["translator", "pps"]`), so the gate covers the full provisioned set and "All components provisioned and ready" is true only with the EDS Ready (ruled R5: yes).
- **The envtest** — the provisioning contract cases (below), mirroring the landed PPS envtest style.

Everything else is untouchable — in particular `experiment-operator/api/**` (no API change), `scenario-manager/**` (E3), `component-templates/**` (E1), `test/**` (E4), `docs/**`, `devlog/**`, and the generated CRD manifests (`verify-generated` stays byte-stable — the CRD field `spec.experimentalDesignService` already exists, required-but-inert; you make it honored, you do not add fields).

## Normative env contract (the EDS module — E1 — validates every value fail-fast; byte-pinned by FEATURE.md §5)

| Env | Value |
|---|---|
| `NATS_URL` | the existing `translatorNATSURL` constant (same value the Translator and PPS get) |
| `EDS_AVAILABILITY_SUBJECT` | `cbse.<instance.Namespace>.<instance.Name>.eds.scenarios.available` — the SM's landed availability subject, derived exactly as the sibling subject helpers derive theirs |
| `SIMULATIONPROJECTNAMESPACE` / `SIMULATIONPROJECTNAME` / `SIMULATIONEXPERIMENTUID` | the three existing downward-API env helpers, unchanged |

Plus the mounted Secret `<exp>-detaildb-sct` (the existing detail-DB connection Secret the operator already provisions for the Translator): a standard read-only Secret volume (name `detaildb-connection`, mirroring `translatorVolumes`) mounted at `/detaildb-connection`. The EDS uses the connection for read-write Detail DB access; the Secret volume itself stays read-only like the landed mounts. The batch subject is never baked — it arrives from the SM's availability reply at runtime.

## Change

1. **Validation:** `ValidateDigestImage` on the EDS image in the validation path (the CRD makes the field required, so an empty image cannot reach the controller — validate the digest shape of whatever is present, mirroring the PPS check).
2. **The Deployment + Service build** per Target (naming `<exp>-eds`; the container port is the spec's `Port`; the security context mirrors the landed restricted profile so the `USER 1000:1000` image runs).
3. **`edsEnvVars` + the subject derivation helper** per the contract above.
4. **The gate membership** — `"eds"` in the `checkReadiness` suffix loop (the not-ready inventory names it; the watchdog bounds the wait — the landed machinery, no new state).
5. **The envtest cases** (mirror the landed PPS envtest; the new cases):
   - the provisioning contract: the Deployment exists with the spec's image, command, args, port; the env set is exactly the contract (echo the exact `EDS_AVAILABILITY_SUBJECT` string for a fixed experiment name/namespace); the Secret volume is mounted at `/detaildb-connection`; the ownership ref points at the CR.
   - the Service: default ClusterIP; the `nodePort` + `serviceType: NodePort` path renders the nodePort.
   - the image validation: a non-digest EDS image is refused (the reconciliation reports the validation failure, no child created).
   - the gate: with the EDS Deployment not ready, the experiment does not reach InProgress; with it ready (and the sibling components ready), it does.
   - the existing suite stays green unchanged.

## Constraints

- **No API change:** `make verify-generated` byte-stable; the `api/` package and the generated CRD manifests are untouched.
- The field-ownership contract stays intact (the operator writes phase/message; the SM writes the verdict — and now, per the ruled diagnostic resolution, the SM writes `status.message` in exactly one InProgress diagnostic case; you change none of that — your readiness/terminal message writes are unchanged).
- No cluster operations; no image builds. `make test-fast` rc=0 is mandatory (it runs the envtest tier); **the cluster smoke is the settlement gate**, run by the manager — you do not run `make test-smoke`.
- **Parallel wave-mates:** E1 owns `component-templates/experimental-design-service/**` + `go.work`/`go.work.sum` + three Makefile line-contacts; E3 owns `scenario-manager/**`. Their uncommitted files will appear in `git status` — expected co-residency, never touch them. **Stage only your own partition** (explicit `git add <paths>` — never bare `git add .`), because your commits interleave with the wave-mates' on the shared feature branch.
- **Commits:** your work lands on the feature branch (the worktree's branch, `D4NS3U/experimental-design-service`) — never `main`, never another worker's branch. Every commit carries the traceability trailer `[orchestration: task <task-id> dispatch <dispatch-id>]` (the IDs from your injected preamble). Commit at natural milestones; leave the branch in a test-green state at `worker_done`.
- Global constraints of FEATURE.md §5 apply verbatim (attestation, read-only specs, scope discipline, verification honesty, licensing headers).

## Ownership

Sole owner of `experiment-operator/internal/controller/**` (the EDS provisioning additions + the envtest). Discovered gaps → report lines in `worker_done`, do not fix.

## Observable acceptance

Run and echo all of these in `worker_done` (the manager re-runs each independently):

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat; mismatch → stop, `--outcome failed`).
2. Containment: `git status --short` — your controller files only, plus expected co-resident wave-mate files; zero files outside `experiment-operator/internal/controller/**`.
3. `cd experiment-operator && go build ./... && go vet ./...` → rc=0.
4. `make test-fast` rc=0 final receipt (mandatory — includes the envtest tier and the byte-stable `verify-generated`).
5. API-untouched receipt: `git status --short experiment-operator/api experiment-operator/config` → empty (no API/generated change).
6. Envtest receipts: echo the new EDS envtest case names (the provisioning contract incl. the exact availability-subject string; the Secret mount at `/detaildb-connection`; the port; the NodePort path; the image-validation refusal; the gate — not-ready EDS blocks InProgress, ready EDS reaches it; the ownership ref).
7. Commit receipts: the branch HEAD, and `git log --format=%b -3 | grep 'orchestration:'` showing the trailer on your commits.

Completion protocol: `worker_done` exactly once, with a three-sentence executive summary, both lifecycle IDs (task + dispatch from your preamble), explicit `--outcome succeeded|failed`, the verbatim attestation line, the seven evidence blocks, and `--files-modified`. Read coordinator follow-ups at each natural checkpoint via `orca orchestration check --terminal <your_handle> --json`, and once more immediately before `worker_done`.

**Session hygiene (binding, learned from the PPS S1 crash):** keep tool outputs small (`head`/`tail`/`grep -n`, never whole-file cats of large files); gather evidence incrementally as you complete each requirement rather than batching everything into one giant end-session run.
