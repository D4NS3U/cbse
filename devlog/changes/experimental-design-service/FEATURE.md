# FEATURE.md — The Experimental Design Service reference component (draft)

**Status: DRAFT — the design pass is in progress; the rulings below (R1–R6) are open.**
**Foundation:** the recon of 2026-10-01 (all claims below are code-verified with anchors). The EDS concept gap was found and ruled during the [experiment-terminal-e2e](../experiment-terminal-e2e/notes/terminal-paths-and-triggers.md) design pass: the alpha4 API **requires** `spec.experimentalDesignService` with a full deployment shape (`design`, `image`, `command`, `args`, `serviceType`, `nodePort`, `port` — `simulationexperiment_types.go:215+`), but the operator never reads the field — it is required-but-inert. **This feature builds the reference component and the `reconcileEDS` provisioning that finally honors it.**

## 1. Title and mission

The **Experimental Design Service (EDS)** becomes what the CR already declares it to be: a per-experiment component, provisioned by the operator from `spec.experimentalDesignService`, that lets the experimenter **design the experiment interactively** and delivers the design into the running system:

- a **small web UI** (served on the CR's `port`, fronted by the CR's Service) where the user configures **recipes** (the simulation parameter sets: `arrival_rate`, `service_rate`, `run_duration`, `seed_policy`) and the **scenario batch** (the scenario count, each scenario's priority, replication fleet, `confidence_metric`, and recipe assignment);
- the EDS **writes the recipes into the experiment's Scenario Detail Database** (the connection the operator already provisions — the `<exp>-detaildb-sct` Secret; the default is the reference PostgreSQL image seeded with `public.simulation_parameters`, a host-based `detailDatabase` is honored identically through the same Secret) — so the **Translator's existing lookup keeps working unchanged** (`detaildb.go:83`: the single predefined parameterized query keyed by `parameterset_id`);
- a **switch-on action** (the UI's button): the EDS performs the landed availability handshake — the request `{"batch_id","project","scenario_count"}` on the per-project availability subject, the SM's reply `{"status":"ready","batch_subject":...}` (only for `InProgress` experiments) — and **publishes the scenario batch** (`{"batch_id","project","scenarios":[{priority, number_of_reps, recipe_info:{parameterset_id}, confidence_metric}]}`) to the SM-provided subject, populating `scenario_status` through the existing intake.

**Out of the feature's scope:** any change to the SM's intake or the availability protocol (landed and cluster-proven); any change to the Translator's recipe lookup; the PPS; the scenario state machine. The EDS is a new *producer* at the existing contract's edge.

## 2. Scope

- **In scope:** (a) the reference EDS component (`component-templates/experimental-design-service/` — a Go module in the PPS's architectural tradition: minimal dependencies, `nats.go` + `pgx` + `net/http`, `USER 1000:1000`, digest-pinned, licensed Makefile lines, go.work member); (b) the operator's `reconcileEDS` — the Deployment + Service from the existing CR field, the env contract, the Detail DB connection mount, the image validation, and the readiness-gate membership; (c) the harness/e2e integration (the image set grows 7→8; the smoke's EDS flow per ruling R1); (d) the documentation (`COMPONENT_DESIGN_GOALS.md`: the EDS contract section, the architecture diagram's "EDS (installed by the installation)" line becomes per-experiment).
- **Out of scope:** authentication/authorization on the UI (the EDS serves a ClusterIP Service inside the experiment's namespace by default; NodePort exposure is the user's explicit choice — the UI trusts the cluster boundary, recorded as a deliberate boundary); multi-user concurrency control (a single experimenter per experiment is the model); changing the seeded reference recipes' values.
- **Breaking changes / compatibility:** none to the API (the field exists; `reconcileEDS` honors it). The Detail DB's "rows are immutable" reference comment (the seeded SQL) is superseded by the EDS's write contract (ruling R2) — the seeded four rows remain as the reference baseline; the schema itself (`simulation_parameters`, the CHECK constraints) is unchanged.

## 3. Workflow decisions (concern → decision)

| # | Concern | Decision for this feature |
|---|---|---|
| D1 | The component shape | **Go, mirroring the PPS reference module**: `config` (env validation, fail-fast at startup like the PPS), `wire` (the availability request/reply + the batch payload types — golden-tested against the landed SM contract), `recipestore` (the pgx Detail DB writer/reader), `server` (the `net/http` API + the embedded static UI page), `publisher` (the handshake + the publish, the mock's retry semantics as the reference). The UI is a thin layer: every button maps to a JSON API endpoint, and the e2e drives the **same API** — the smoke's determinism is the API's determinism. |
| D2 | The env contract (the operator → EDS) | The PPS precedent (`ppsEnvVars`): `NATS_URL`; the derived per-experiment **availability subject** (the operator computes it from the identity — mirroring the PPS subject envs); the downward-API identity envs (`SIMULATIONPROJECTNAMESPACE`, `SIMULATIONPROJECTNAME`, `SIMULATIONEXPERIMENTUID`); the batch subject comes from the SM's reply at runtime (never baked). The Detail DB connection arrives as the mounted `<exp>-detaildb-sct` Secret (the same five-field endpoint contract `dbEndpointFromSecret` builds), mounted **read-write**. |
| D3 | The recipe-write path | The EDS connects via the mounted Secret and writes recipes to `public.simulation_parameters` **under the existing CHECK constraints** (positivity enforced by the schema). The seeded rows 1–4 are the immutable reference baseline; the EDS-created rows are the user's design surface (ruling R2: append-only vs edit-capable). On a user's fresh host-based DB, the EDS self-provisions the table (ruling R3: `CREATE TABLE IF NOT EXISTS`, the runner's result-table precedent) or requires the schema. |
| D4 | The switch-on semantics | One switch-on = one availability handshake + one batch publish (the SM accepts multiple batches over time — the mock published `TOTAL_BATCHES`; the UI may re-arm: reconfigure + switch again). The handshake fails cleanly (status=error from the SM) while the experiment is not `InProgress` — the UI surfaces the reason. |
| D5 | The readiness gate | The EDS Deployment joins the gate (`ReadyReplicas >= 1`, like the translator and the G-ruled PPS) — completing the coverage: the watchdog's inventory names it; a broken EDS becomes a bounded, diagnosed `Error` instead of the zero-scenario idle (ruling R5 — recommendation: yes). |
| D6 | The EDS image validation | `ValidateDigestImage` on `spec.experimentalDesignService.image` (the immutable-digest discipline, like the translator/PPS fields) — ruling R4 (recommendation: yes). |
| D7 | The UI's accessibility | ClusterIP by default (`kubectl port-forward` for the browser); the CR's `serviceType: NodePort` + `nodePort` fields expose it directly — the user's explicit choice, the existing validated surface. |
| D8 | The KPI | Fixed to `mean_wait_time` (the reference Translator's KPI) — the UI configures `confidence_metric` per scenario; the KPI field stays out of the surface until a second KPI exists (ruling R6). |
| D9 | The mock's fate | The eds-mock remains for the harness's unit/integration tiers; the smoke's EDS flow is ruling R1. |

## 4. Design patterns (Gang of Four)

| # | Concern | Pattern | Concrete application | Why the simpler composition is not enough |
|---|---|---|---|---|
| 1 | The UI as an API layer | **Facade** | The embedded HTML page is a thin Facade over the JSON API (`GET/POST /recipes`, `POST /batch`, `POST /switch-on`); the e2e and the human drive one surface | A bespoke UI with private logic would be untestable in the smoke; the API is the contract, the page is a client |
| 2 | The recipe store | **Adapter** | `recipestore` wraps `pgx` behind a small interface (list/write recipes, next id), with the connection derived from the mounted Secret — the same Adapter discipline the SM's persistence layer uses | The Detail DB is pluggable (reference image or user's host DB); the writer must not know which |
| 3 | The publish flow | **Template Method** | The publisher follows the mock's proven skeleton (connect → handshake-with-retry → publish → confirm), parameterized by the batch built from the API state | A bespoke one-off flow would drift from the proven protocol discipline |
| 4 | The switch-on state | **State** | The EDS holds a small in-memory state machine (unconfigured → recipes-written → armed/published) reflected in the UI — the state is derived from the store + the last publish result, never a second source of truth | The UI must render what the system will do, not a stale hope |

## 5. Global constraints (per worker, verbatim discipline)

- **Obey `AGENTS.md`.** `make test-fast` after every Go change; the consolidated settlement smoke is the manager's gate.
- **The reference component discipline** (the PPS/S4 precedent): locked digest-pinned source images recorded in the lock file, `USER 1000:1000`, no credentials baked, canonical + provenance tags, the licensed Makefile lines, go.work membership; `make test-fast` covers the new module (vet + race).
- **The operator discipline** (the PPS/S3 precedent): the env contract is fully operator-injected and validated at the component's startup (fail-fast with descriptive errors); ownership refs on every child; `verify-generated` stays byte-stable (no API change).
- **Test integrity, secrets hygiene, verification honesty, runtime attestation, branch discipline, the serialized review lane** — all per the standing rules (the FEATURE.md conventions of the previous features apply verbatim).

## 6. Slices (task specs are authored per slice at its dispatch wave)

| Slice | Content | Dependency | Tier gate |
|---|---|---|---|
| **E1 — the reference EDS module** | The Go module: `config`/`wire` (the golden-tested protocol types), `recipestore` (the Detail DB writer under the CHECK constraints), `server` (the API + the embedded UI), `publisher` (the handshake + publish with the mock's retry semantics), the module tests, the Dockerfile, go.work + Makefile wiring | — | `make test-fast` |
| **E2 — the operator's `reconcileEDS`** | The Deployment + Service from the existing CR field (image, command, args, port, serviceType, nodePort), the env contract (D2), the detaildb Secret mount, the image validation (D6), the readiness-gate membership (D5), envtest (the provisioning contract, the gate, the ownership) | E1 (the env contract it injects) | `make test-fast` + the settlement smoke |
| **E3 — the harness/e2e integration** | The image build/lock (the set 7→8: the build, the lock pairs, the loader keys, the self-test truths), the smoke's EDS flow per R1, the green specs' EDS-driving setup, the e2e assertions (the recipes in the Detail DB, the batch intake, the full chain through the real component) | E1, E2 | `make test-fast` + the settlement smoke |
| **E4 — the documentation** | `COMPONENT_DESIGN_GOALS.md`: the EDS reference-component section (the build, the API, the DB-write, the publish contracts), the architecture diagram update (per-experiment EDS), the reference-image README | E1–E3 | `make test-fast` |

Wave plan: serialized single workers (W1 = [E1]; W2 = [E2]; W3 = [E3]; W4 = [E4] — E4 may fold into E3's wave), each with the independent glm review lane; the consolidated settlement smoke after the final wave; the merge to `main` is one user-approved event.

## 7. Verification gates

- Per-tier rule as in §5; E1's wire types are golden-tested against the SM's landed structs (the PPS wire-test precedent); E2's envtest covers the full provisioning contract; E3's smoke proves the **real interactive flow** end-to-end: the e2e drives the EDS API (recipes → the Detail DB → the batch → the SM intake → the scenarios run with the user's recipes → the Translator resolves them → … → the terminal phase).
- **Forbidden shortcuts:** weakening any green spec; driving the smoke's batch through the mock while claiming the reference EDS is proven; prose-only evidence.

## 8. Completion and handoff

State vocabulary per slice: `complete` / `incomplete` / `verification-blocked`, recorded with evidence in `IMPLEMENTATION_HANDOFF.md` after manager-verified settlement; the merge to `main` is the user's single approval at settlement.

## Rulings record (open)

- **R1 — the smoke's EDS flow:** (a) the green experiment's EDS is the **reference component** and the e2e drives its API (the honest end-state; the EDS-dependent green specs are reworked to the API-driven setup) — *the manager's recommendation: this is the feature's point*; (b) the mock as the provisioned image (minimal rework; the mock adapted to the operator's env contract); (c) staged: (b) first, (a) as a follow-up.
- **R2 — the recipe-write semantics:** append-only (the EDS creates new `parameterset_id`s; the seeded 1–4 stay immutable) vs edit-capable (the EDS's rows are updatable/deletable through the UI; the seeded rows stay untouched). *Recommendation: append-only first — the write surface matches the user's "set the recipes" while keeping the reference baseline pristine; edit arrives as a follow-up if wanted.*
- **R3 — the fresh-DB schema:** the EDS self-provisions `public.simulation_parameters` (`CREATE TABLE IF NOT EXISTS`, the runner's result-table precedent) on a user's host-based DB vs requiring the schema to exist. *Recommendation: self-provision — the host-based form must work without a manual DB step.*
- **R4 — the EDS image validation:** `ValidateDigestImage` like the translator/PPS. *Recommendation: yes (the immutable-digest discipline).*
- **R5 — the readiness gate:** the EDS joins the gate (D5). *Recommendation: yes — completing the G ruling; the watchdog converts the zero-scenario idle into a bounded, diagnosed `Error`.*
- **R6 — the UI's field scope:** per-scenario `priority`, `number_of_reps`, `confidence_metric`, and the recipe assignment are configurable; the KPI stays fixed to `mean_wait_time` (D8); the recipe fields are the four `simulation_parameters` columns. *Confirm or extend.*
