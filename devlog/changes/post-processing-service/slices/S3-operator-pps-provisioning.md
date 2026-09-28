# Slice S3 — Operator: PPS Deployment + Service provisioning from `spec.postProcessingService`

Normative and self-contained for its worker. Read order and global constraints: [../FEATURE.md](../FEATURE.md) §5 (verbatim discipline), §3 (esp. D2, D9, D10 and the rulings record), §6 S3 row; then repository-root `AGENTS.md`. The alpha4 API already carries `PostProcessingSpec` (`image`, `serviceType`, `nodePort`, `port`, `command`, `args`) — you provision it; you do NOT touch `api/**`. The S2 wave-mate lands the SM-side stream/consumer contract in parallel; your env injection must match the env contract below verbatim (it is shared normatively with S2/S4). This slice extends, never overrides, the umbrella. All three documents are read-only for you. On any contradiction between this slice and live code that inspection cannot resolve: stop and ask via your ask channel.

## Target

Exactly these surfaces:

- `experiment-operator/internal/controller/simulationexperiment_alpha4_controller.go` (+ its envtest in `internal/controller/alpha4/`) — PPS Deployment + Service creation in the alpha4 reconcile path, mirroring the Translator's.
- `experiment-operator/internal/controller/workload.go` (if helpers live better there — the translator helpers `translatorEnvVars`, `workloadLabels`, downward-API env funcs, `applyServiceSpec` are the archetypes).
- **Manager-licensed e2e truth update (exactly one assert + its comment):** `test/e2e/smoke_test.go` ~line 339–357 — the idempotence spec asserts the project-labeled Deployment count "stays at three" / `HaveLen(3)`. The PPS Deployment carries the project label, so the truth becomes **four**: update the comment's "three" and the `HaveLen(3)` → `HaveLen(4)`. Nothing else in `test/**` is yours — the harness scripts (`test/harness/**`) are absolutely out of bounds (run them, never edit them).

Everything else is untouchable — in particular `api/**` (the spec fields exist already), `scenario-manager/**` (S2's parallel wave), `component-templates/**` (S4's), `test/harness/**`, root `Makefile`, `go.mod`/`go.sum` (no new operator dependencies), `docs/**`, `devlog/**`.

## Normative env contract (shared verbatim with S2/S4; the reference PPS parses exactly these)

Injected into the PPS container, mirroring `translatorEnvVars`:

- `NATS_URL` = the same value the Translator gets (`translatorNATSURL` constant, workload.go).
- `PPS_STREAM` = `"cbse_pps"`.
- `PPS_REQUEST_SUBJECT` = `cbse.<namespace>.<project>.pps.request` (derived per experiment, mirroring `translatorRequestSubject`).
- `PPS_EVALUATION_SUBJECT_TEMPLATE` = `cbse.<namespace>.<project>.pps.%s.evaluation` (`%s` = scenario id; mirror `translatorReadySubjectTemplate`'s derivation).
- `PPS_CONSUMER` = `"pps-" + <12-char UID prefix>` (mirror the translator consumer-name line; the SM-side S2 wave-mate ensures this durable exists).
- Identity downward-API vars: `SIMULATIONPROJECTNAMESPACE`, `SIMULATIONPROJECTNAME`, `SIMULATIONEXPERIMENTUID` (the existing helper funcs).
- Mounted read-only Secret `<experiment-name>-resultdb-sct` (the same Secret the Translator volumes already mount; keys `host`, `port`, `dbname`, `user`, `password` — the PPS parses it exactly like the Translator's `dbconfig` package reads connection Secrets; no sslmode field).

## Change

1. **PPS Deployment**: built from `spec.postProcessingService` — container image (verbatim from the spec; the operator does not validate or mutate image references beyond what the Translator path does), `port`, `command`, `args` passed through verbatim. Single container (no BuildKit sidecar — that is Translator-specific). Volumes: only the `resultdb-connection` Secret mount (read-only, same items as the Translator's). Security context: the restricted profile (`restrictedSecurityContext` — RunAsNonRoot UID/GID 1000). Labels: the standard workload labels (`workloadLabels`) so the Deployment is project-labeled and owner-selected. Object name: `<experiment-name>-pps` (mirror the translator deployment's naming convention). Owner reference + reconcile/apply semantics identical to the Translator Deployment (create-or-update via the same apply helper; idempotent on metadata-only updates — the e2e idempotence spec you update proves exactly this).
2. **PPS Service**: `<experiment-name>-pps-svc` via `applyServiceSpec(ppsSpec.ServiceType, ppsSpec.Port, ppsSpec.NodePort, <experiment-name>+"-pps")` — mirroring the Translator Service exactly (ClusterIP default behavior, NodePort when set, selector app label).
3. **Reconcile wiring**: create both in the same reconcile site as the Translator Deployment/Service; the controller's `Owns(&appsv1.Deployment{})`/`Owns(&appsv1.Service{})` watches already cover owned objects — verify no additional RBAC/watch changes are needed and none are made.
4. **Envtest coverage** (`internal/controller/alpha4/`): the PPS Deployment exists with the exact env contract above (every var name/value, the mounted Secret name), correct image/port/command/args pass-through, workload labels, owner reference, restricted security context; the Service exists with the right type/port/selector; deletion of the experiment garbage-collects both (if the existing envtest covers GC for translator children, mirror that assertion); idempotent re-reconcile does not duplicate children.
5. **Licensed e2e truth update**: the one assert + comment per Target (three → four). Do not touch any other e2e line.

## Constraints

- Mirror, never invent: the Translator Deployment/Service path is the normative archetype; the PPS differs only per the contract above (no BuildKit, resultdb-only volume, pps env names).
- No `api/**` changes; no RBAC changes; no new dependencies; no harness edits; no cluster operations by you (`make test-fast` includes the operator envtest suite — that is your gate; **the cluster smoke is the wave gate**, run by the manager after S2+S3+S4 settle; you do not run `make test-smoke`).
- Parallel wave-mates: S2 owns `scenario-manager/**` messaging packages; S4 owns `component-templates/post-processing-service/**` (+ `go.work`, + licensed `Makefile` test-fast lines). Their uncommitted files will appear in `git status` — expected co-residency, never touch them; containment evidence lists your files and flags only files outside the three partitions.
- Global constraints of FEATURE.md §5 apply verbatim (attestation, no commits, read-only specs, scope discipline, verification honesty).

## Ownership

Sole owner of the operator controller surfaces + the one licensed e2e assert. Discovered gaps → report lines, do not fix.

## Observable acceptance

Run and echo all of these in `worker_done` (the manager re-runs each independently):

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat; mismatch → stop, `--outcome failed`).
2. Containment: `git status --short` — your files plus expected co-resident wave-mate files; zero files outside the three partitions.
3. `cd experiment-operator && go build ./... && go vet ./...` → rc=0.
4. `make test-fast` rc=0 final receipt (mandatory — includes the operator envtest suite and the e2e compile pass over your licensed smoke_test.go edit).
5. Targeted envtest receipt: the PPS envtest test names + PASS lines (deployment env contract, service, labels/ownership, GC, idempotence).
6. Licensed-edit proof: `git diff test/e2e/smoke_test.go` shows exactly the count + comment change (echo the diff).
7. grep receipts: every env var name from the contract in the PPS env builder; `resultdb-sct` volume mount; `pps-` consumer prefix; workload labels on the PPS Deployment; the pps Service name suffix.

Completion protocol: `worker_done` with a three-sentence executive summary, both lifecycle IDs (task + dispatch from your preamble), explicit `--outcome succeeded|failed`, the verbatim attestation line, the seven evidence blocks, `--files-modified`.

**Session hygiene (binding, learned from the S1 crash):** keep tool outputs small (`head`/`tail`/`grep -n`, never whole-file cats of large files); gather evidence incrementally as you complete each requirement rather than batching everything into one giant end-session run.
