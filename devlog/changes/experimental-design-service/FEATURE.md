# FEATURE.md — The Experimental Design Service reference component (draft)

**Status: DRAFT — design pass complete (2026-10-02): all rulings R1–R6 answered (see the rulings record); Tier-2 sub-decisions open: B1 (record "resume a finished experiment" as a future increment) and B2 (the interactive acceptance at settlement: optional-but-expected vs required); dispatch pending the Tier-2 close.**
**Foundation:** the recon of 2026-10-01 (all claims below are code-verified with anchors). The EDS concept gap was found and ruled during the [experiment-terminal-e2e](../experiment-terminal-e2e/notes/terminal-paths-and-triggers.md) design pass: the alpha4 API **requires** `spec.experimentalDesignService` with a full deployment shape (`design`, `image`, `command`, `args`, `serviceType`, `nodePort`, `port` — `simulationexperiment_types.go:215+`), but the operator never reads the field — it is required-but-inert. **This feature builds the reference component and the `reconcileEDS` provisioning that finally honors it.**

## 1. Title and mission

The **Experimental Design Service (EDS)** becomes what the CR already declares it to be: a per-experiment component, provisioned by the operator from `spec.experimentalDesignService`, that lets the experimenter **design the experiment interactively** and delivers the design into the running system:

- a **small web UI** (served on the CR's `port`, fronted by the CR's Service) where the user **designs the experiment**: **parameter entries** (values of the simulation's four varyable parameters — `arrival_rate`, `service_rate`, `run_duration`, `seed_policy`; the SimPy model's complete varyable space — server count, process distributions, queue, and KPI are fixed by the reference simulation) and the **scenarios** (each with `priority`, `number_of_reps`, `confidence_metric`, and a **recipe** — the explicit composition locating one entry row per parameter);
- the EDS **persists the design into the experiment's Scenario Detail Database** (the connection the operator already provisions — the `<exp>-detaildb-sct` Secret; the default is the reference PostgreSQL image, a host-based `detailDatabase` is honored identically through the same Secret) under the **ruled design-layer schema** (ruling R2): the entries in the per-parameter tables, the recipes in the `recipes` table (the locator rows), and `public.simulation_parameters` as a **view** over the join — so the **Translator's existing lookup keeps working unchanged** (`detaildb.go:83`: the single predefined parameterized query, view-transparent);
- an explicit **"Save Experiment" action** (user-ruled: the persistence is a *step in the flow*, not an automatic side effect) — the entries and the recipes, one transaction;
- an explicit **"Start Experiment" action** (the UI's button): the pre-flight (every batch-referenced `parameterset_id` must resolve, else a clean UI-visible refusal), then the landed availability handshake — the request `{"batch_id","project","scenario_count"}` on the per-project availability subject, the SM's reply `{"status":"ready","batch_subject":...}` (only for `InProgress` experiments) — and the **scenario batch publish** (`{"batch_id","project","scenarios":[{priority, number_of_reps, recipe_info:{parameterset_id}, confidence_metric}]}`) to the SM-provided subject, populating `scenario_status` through the existing intake.

**Out of the feature's scope:** any change to the SM's intake or the availability protocol (landed and cluster-proven); any change to the Translator's recipe lookup; the PPS; the scenario state machine. The EDS is a new *producer* at the existing contract's edge.

## 2. Scope

- **In scope:** (a) the reference EDS component (`component-templates/experimental-design-service/` — a Go module in the PPS's architectural tradition: minimal dependencies, `nats.go` + `pgx` + `net/http`, `USER 1000:1000`, digest-pinned, licensed Makefile lines, go.work member); (b) the operator's `reconcileEDS` — the Deployment + Service from the existing CR field, the env contract, the Detail DB connection mount, the image validation, and the readiness-gate membership; (c) the harness/e2e integration (the image set grows 7→8; the smoke's EDS flow per ruling R1); (d) the documentation (`COMPONENT_DESIGN_GOALS.md`: the EDS contract section, the architecture diagram's "EDS (installed by the installation)" line becomes per-experiment), and the human-interactive acceptance CR (Appendix A).
- **Out of scope:** authentication/authorization on the UI (the EDS serves a ClusterIP Service inside the experiment's namespace by default; NodePort exposure is the user's explicit choice — the UI trusts the cluster boundary, recorded as a deliberate boundary); multi-user concurrency control (a single experimenter per experiment is the model); changing the seeded reference recipes' values.
- **Breaking changes / compatibility:** none to the API (the field exists; `reconcileEDS` honors it). The Detail DB's reference seed is **reworked by the ruled R2**: the per-parameter entry tables + the `recipes` locator table + the `simulation_parameters` view replace the single seeded table; the lookup contract (the query text, the `parameterset_id` key, the four-value projection) is unchanged; the seeded baseline generalizes to **ids 1–4 read-only in all five tables**; the reference image is re-pinned in E3 (the image set still grows by exactly one: the EDS).

## 3. Workflow decisions (concern → decision)

| # | Concern | Decision for this feature |
|---|---|---|
| D1 | The component shape | **Go, mirroring the PPS reference module**: `config` (env validation, fail-fast at startup like the PPS), `wire` (the availability request/reply + the batch payload types — golden-tested against the landed SM contract), `recipestore` (the pgx design-layer writer: the entry CRUD + the recipe locator rows, one transaction, EDS-assigned ids ≥ 5, ids 1–4 refused), `server` (the `net/http` API + the embedded static UI page), `publisher` (the handshake + the publish, the mock's retry semantics as the reference). The UI is a thin layer: every button maps to a JSON API endpoint, and the e2e drives the **same API** — the smoke's determinism is the API's determinism. |
| D2 | The env contract (the operator → EDS) | The PPS precedent (`ppsEnvVars`): `NATS_URL`; the derived per-experiment **availability subject** (the operator computes it from the identity — mirroring the PPS subject envs); the downward-API identity envs (`SIMULATIONPROJECTNAMESPACE`, `SIMULATIONPROJECTNAME`, `SIMULATIONEXPERIMENTUID`); the batch subject comes from the SM's reply at runtime (never baked). The Detail DB connection arrives as the mounted `<exp>-detaildb-sct` Secret (the same five-field endpoint contract `dbEndpointFromSecret` builds), mounted **read-write**. |
| D3 | The design-to-storage path (ruled R2, 2026-10-02) | **(1) Design** (the GUI's in-memory state: the parameter entries + the scenarios with their recipes) → **(2) "Save Experiment"** (an explicit user action; one transaction into the Detail DB via the mounted Secret: the entries into the per-parameter tables `arrival_rate` / `service_rate` / `run_duration` / `seed_policy`, the recipes into the `recipes` table keyed by `parameterset_id` — EDS-assigned ids ≥ 5; ids 1–4 read-only in all five tables) → **(3) "Start Experiment"** (the pre-flight: every `recipe_info.parameterset_id` the batch references must resolve, else a clean UI-visible refusal — the one failure mode the flow could otherwise create: a scenario referencing an unresolvable recipe reaches the Translator's `RecipeError` path, a *silent stall* (not a Failed), the exact stall class the terminal-e2e feature documented; then the handshake + the publish). **Execution layer:** `public.simulation_parameters` is a **view** over `recipes ⋈ the four entry tables` (projection `parameterset_id, arrival_rate, service_rate, run_duration, seed_policy`) — the Translator's landed lookup (`detaildb.go:83`), the batch schema, the mock, and the e2e assertions are unchanged by construction. **Live locators:** a saved design's resolved values track its entries until the next save. Fresh host-based DBs: ruling R3 (self-provision the six objects). **Scope note (ruled):** this feature's write surface is exactly the entries and the recipes in the Detail DB; additional Detail DB write capabilities are a recorded future increment, not built. |
| D4 | The start semantics | One "Start Experiment" = one availability handshake + one batch publish (the SM accepts multiple batches over time — the mock published `TOTAL_BATCHES`; the UI may re-arm: re-design + re-save + re-start). The handshake fails cleanly (status=error from the SM) while the experiment is not `InProgress` — the UI surfaces the reason. |
| D5 | The readiness gate | The EDS Deployment joins the gate (`ReadyReplicas >= 1`, like the translator and the G-ruled PPS) — completing the coverage: the watchdog's inventory names it; a broken EDS becomes a bounded, diagnosed `Error` instead of the zero-scenario idle (ruled R5: yes). |
| D6 | The EDS image validation | `ValidateDigestImage` on `spec.experimentalDesignService.image` (the immutable-digest discipline, like the translator/PPS fields) — ruled R4: yes. |
| D7 | The UI's accessibility | ClusterIP by default (`kubectl port-forward` for the browser); the CR's `serviceType: NodePort` + `nodePort` fields expose it directly — the user's explicit choice, the existing validated surface. |
| D8 | The KPI | Fixed to `mean_wait_time` (the reference Translator's KPI) — the UI configures `confidence_metric` per scenario; the KPI field stays out of the surface until a second KPI exists (ruled R6: confirmed). |
| D9 | The mock's fate | Ruled R1 (a): the eds-mock is **retired from the smoke** (the stack-level `eds-mock` Deployment and the one-shot Pod fixture leave); it remains for the unit/integration tiers as the protocol-faithful publisher and the publisher's retry-semantics reference. |

## 4. Design patterns (Gang of Four)

| # | Concern | Pattern | Concrete application | Why the simpler composition is not enough |
|---|---|---|---|---|
| 1 | The UI as an API layer | **Facade** | The embedded HTML page is a thin Facade over the JSON API (`GET/POST /recipes`, `POST /batch`, `POST /switch-on`); the e2e and the human drive one surface | A bespoke UI with private logic would be untestable in the smoke; the API is the contract, the page is a client |
| 2 | The recipe store | **Adapter** | `recipestore` wraps `pgx` behind a small interface (list/write recipes, next id), with the connection derived from the mounted Secret — the same Adapter discipline the SM's persistence layer uses | The Detail DB is pluggable (reference image or user's host DB); the writer must not know which |
| 3 | The publish flow | **Template Method** | The publisher follows the mock's proven skeleton (connect → handshake-with-retry → publish → confirm), parameterized by the batch built from the API state | A bespoke one-off flow would drift from the proven protocol discipline |
| 4 | The flow's state | **State** | The three-step flow's state machine (designed → saved → started) reflected in the UI — derived from the configuration store + the Detail DB + the last publish result, never a second source of truth | The UI must render which step is possible next (save before start; the pre-flight validation before the publish), not a stale hope |

## 5. Global constraints (per worker, verbatim discipline)

- **Obey `AGENTS.md`.** `make test-fast` after every Go change; the consolidated settlement smoke is the manager's gate.
- **The reference component discipline** (the PPS/S4 precedent): locked digest-pinned source images recorded in the lock file, `USER 1000:1000`, no credentials baked, canonical + provenance tags, the licensed Makefile lines, go.work membership; `make test-fast` covers the new module (vet + race).
- **The operator discipline** (the PPS/S3 precedent): the env contract is fully operator-injected and validated at the component's startup (fail-fast with descriptive errors); ownership refs on every child; `verify-generated` stays byte-stable (no API change).
- **Test integrity, secrets hygiene, verification honesty, runtime attestation, branch discipline, the serialized review lane** — all per the standing rules (the FEATURE.md conventions of the previous features apply verbatim).

## 6. Slices (task specs are authored per slice at its dispatch wave)

| Slice | Content | Dependency | Tier gate |
|---|---|---|---|
| **E1 — the reference EDS module** | The Go module: `config`/`wire` (the golden-tested protocol types), `recipestore` (the design-layer writer: the entry CRUD + the recipe locator rows under the model's constraints, ids 1–4 refused; the six-object schema self-provision, ruled R3 — `CREATE TABLE IF NOT EXISTS` / `CREATE VIEW IF NOT EXISTS`, DDL mirroring the reference seed SQL), `server` (the API + the embedded UI), `publisher` (the handshake + publish with the mock's retry semantics), the module tests, the Dockerfile, go.work + Makefile wiring | — | `make test-fast` |
| **E2 — the operator's `reconcileEDS`** | The Deployment + Service from the existing CR field (image, command, args, port, serviceType, nodePort), the env contract (D2), the detaildb Secret mount, the image validation (D6), the readiness-gate membership (D5), envtest (the provisioning contract, the gate, the ownership) | E1 (the env contract it injects) | `make test-fast` + the settlement smoke |
| **E3 — the harness/e2e integration** | The image build/lock (the set 7→8 with the EDS; the Detail DB reference image re-pinned with the ruled R2 seed rework — the entry tables, `recipes`, the view, the baseline rows; the `sqldoc` lockstep test updated in the same slice; the lock pairs, the loader keys, the self-test truths), the smoke's EDS flow per R1, the Appendix A acceptance CR checked in at `devlog/changes/experimental-design-service/acceptance/simulationexperiment-manual.yaml` (kept in sync with the spec's appendix), the green specs' EDS-driving setup, the red specs' terminal inventories re-baselined to the new provisioning sequence (the EDS step; the gate membership per R5), the e2e assertions (the entries + recipes in the Detail DB, the view's projection, the batch intake, the full chain through the real component) | E1, E2 | `make test-fast` + the settlement smoke |
| **E4 — the documentation** | `COMPONENT_DESIGN_GOALS.md`: the EDS reference-component section (the build, the API, the DB-write, the publish contracts), the architecture diagram update (per-experiment EDS), the reference-image README | E1–E3 | `make test-fast` |

Wave plan: serialized single workers (W1 = [E1]; W2 = [E2]; W3 = [E3]; W4 = [E4] — E4 may fold into E3's wave), each with the independent glm review lane; the consolidated settlement smoke after the final wave; the merge to `main` is one user-approved event.

## 7. Verification gates

- Per-tier rule as in §5; E1's wire types are golden-tested against the SM's landed structs (the PPS wire-test precedent); E2's envtest covers the full provisioning contract; E3's smoke proves the **real interactive flow** end-to-end: the e2e drives the EDS API (recipes → the Detail DB → the batch → the SM intake → the scenarios run with the user's recipes → the Translator resolves them → … → the terminal phase).
- **Forbidden shortcuts:** weakening any green spec; driving the smoke's batch through the mock while claiming the reference EDS is proven; prose-only evidence.

## 8. Completion and handoff

State vocabulary per slice: `complete` / `incomplete` / `verification-blocked`, recorded with evidence in `IMPLEMENTATION_HANDOFF.md` after manager-verified settlement; the merge to `main` is the user's single approval at settlement.

## Rulings record (open)

- ~~**R1 — the smoke's EDS flow**~~ **Answered (2026-10-02, user ruling):** **(a)** — the smoke's EDS is the **reference component**, driven by the e2e through its **JSON API** (the `server` package's `net/http` endpoints, JSON payloads; the embedded UI page is a thin client over the same contract — the Facade row). The mock-EDS is **retired from the smoke** (the stack-level `eds-mock` Deployment and the `launchEdsMockOneShot` fixture leave); it remains for the unit/integration tiers (D9). **Two client modes on one contract (ruled):** the **non-interactive mode** — the automated smoke (CI): the harness port-forwards the EDS Service and drives the API scripted (design → Save → assert the DB → Start → assert the intake), fully unattended per the `AGENTS.md` harness contract; and the **interactive mode** — a human in the browser (the embedded UI issues the same API calls; `kubectl port-forward` per D7, or NodePort as the user's explicit choice): no harness involved, the human clicks Save/Start. The interactive flow is human validation (optionally a manual acceptance step at settlement — the retained stack or the dev cluster plus **one fresh experiment CR, the well-defined `SimulationExperiment` of Appendix A**; the human port-forwards its EDS and drives the full flow in the UI while the experiment is `InProgress`, optionally re-arming mid-run; the smoke's own green experiment stays fully automated — a `Finished` experiment cannot be re-started: the SM's `ready` reply is `InProgress`-only and terminal phases are sticky), **not a harness spec**: the suite remains fully non-interactive. The green specs' batch step becomes the API-driven setup; the red F-crash spec's batch goes through the same helper, its delta staying in the CR. |
- ~~**R2 — the design-to-storage flow**~~ **Answered (2026-10-02, user ruling, approved as drafted):** the three-step GUI flow — **(1) Design** (the parameter entries of the simulation's four varyable parameters + the scenarios with their recipes, the explicit compositions); **(2) "Save Experiment"** (an explicit user action; one transaction into the Detail DB: the entries into the per-parameter tables `arrival_rate`, `service_rate`, `run_duration`, `seed_policy`, the recipes into the `recipes` table keyed by `parameterset_id`, EDS-assigned ids ≥ 5); **(3) "Start Experiment"** (the pre-flight: every batch-referenced `parameterset_id` must resolve, else a clean UI-visible refusal — preventing the silent `RecipeError` stall; then the established handshake + the batch publish). **Storage (ruled):** per-variable entry tables (the user's literal names); `recipes` = the locator table ("where to find each parameter", one FK per parameter); `public.simulation_parameters` = a **view** over `recipes ⋈ the four entry tables` — the Translator's landed lookup (`detaildb.go:83`, view-transparent), the batch schema, the mock, and the e2e assertions are unchanged by construction. **Semantics (ruled):** recipes are live locators — a saved design's resolved values track its entries until the next save (the single-experimenter model owns mid-run edits as the user's explicit action; reproducibility holds for unchanged designs via the `seed_policy` derivation). **Baseline (ruled, the former sub-point):** ids 1–4 read-only in all five tables — the seeded rows 1–4 remain the reference baseline, the EDS's id-space starts at 5 in every table. **Scope note (ruled):** this feature's write surface is exactly the entries and the recipes in the Detail DB; additional Detail DB write capabilities are a recorded future increment, not built.
- ~~**R3 — the fresh-DB schema**~~ **Answered (2026-10-02, user ruling): self-provision, set in stone** — the EDS dynamically self-provisions all six objects of the R2 schema (the four entry tables, `recipes`, the `simulation_parameters` view) via `CREATE TABLE IF NOT EXISTS` / `CREATE VIEW IF NOT EXISTS`, the DDL mirroring the reference seed SQL, at startup. Both schema-delivery paths are covered: the **pre-defined** branch — the image-form detail DB (the reference image seeds the schema at first-start initdb; re-pinned in E3) — and the **dynamic** branch — the host-based form (the EDS creates the schema; the host-based form works without a manual DB step). The `IF NOT EXISTS` form additionally honors a host-based DB on which the user pre-created the schema (the EDS's DDL is a no-op against matching existing objects). The result DB is untouched: its schema is the runner's concern (the per-run self-provisioned result tables, the established precedent); the EDS connects to the detail DB only.
- ~~**R4 — the EDS image validation**~~ **Answered (2026-10-02, user ruling): yes** — `ValidateDigestImage` on `spec.experimentalDesignService.image`, the immutable-digest discipline like the translator/PPS fields (`images.go:32`, the controller's validation path).
- ~~**R5 — the readiness gate**~~ **Answered (2026-10-02, user ruling): yes** — the EDS joins the gate (`ReadyReplicas >= 1`), completing the G ruling: the `InProgress` message "All components provisioned and ready" becomes true for the full set, and the watchdog covers the EDS (a broken EDS becomes a bounded, diagnosed `Error` instead of the zero-scenario idle).
- ~~**R6 — the UI's field scope**~~ **Answered (2026-10-02, user ruling): confirmed** — per-scenario `priority`, `number_of_reps`, `confidence_metric`, and the recipe assignment are configurable; the **recipe fields are the four varyable parameters** (`arrival_rate`, `service_rate`, `run_duration`, **`seed_policy`**) — seed variation is in scope at the recipe/entry level (the user's explicit confirmation); the KPI stays fixed to `mean_wait_time` (D8); `batch_id`/`project` are not UI fields (identity-derived from the operator-injected env contract).

## Appendix A — the human-interactive acceptance CR (ruled R1, 2026-10-02)

The well-defined `SimulationExperiment` the user applies to the cluster to exercise the **human-driven (interactive) client mode**: the reference EDS provisioned from this CR is driven by a person in the browser, never by the harness. E3 checks it in at `devlog/changes/experimental-design-service/acceptance/simulationexperiment-manual.yaml` and keeps it in sync with this appendix.

**Prerequisites** (a retained smoke namespace, or a dev cluster):

- the shared stack running in the namespace (the Scenario Manager, `sm-eds-nats`, the experiment operator, `core-db` — a retained smoke run leaves them; `make test-e2e-retained`); a finished green experiment from the same run coexists harmlessly (a distinct project; per-project subjects);
- the namespace-local `cbse-registry-auth` Docker-config Secret (the harness creates it in smoke namespaces; on a dev cluster the user creates it);
- the reference EDS image built and pushed — its digest taken from the image lock / `artifacts/test/<run-id>/images.env` (immutable digests, environment-provided registry, per `AGENTS.md`).

```yaml
apiVersion: experiment.cbse.terministic.de/alpha4
kind: SimulationExperiment
metadata:
  name: manual-acceptance      # the project name - the subjects' key; unique cluster-wide
  namespace: <namespace>       # e.g. cbse-e2e-<run-id> (retained) or a dev namespace
spec:
  defaultServiceType: ClusterIP
  detailDatabase:
    image: <registry>/scenario-detail-database@sha256:<digest>   # re-pinned by the ruled R2 seed rework (E3)
    dbname: simulation_db
    user: <user>
    password: <password>
    port: 5432
    serviceType: ClusterIP
  resultDatabase:
    image: <registry>/postgres@sha256:<digest>
    dbname: result_db
    user: <user>
    password: <password>
    port: 5432
    serviceType: ClusterIP
  translator:
    image: <registry>/translator@sha256:<digest>
    repository: <runner-repository-url>
    baseimage: <registry>/runner-base@sha256:<digest>
    builderImage: <registry>/buildkit-builder@sha256:<digest>
    registryAuthSecretRef:
      name: cbse-registry-auth
    port: 8080
    serviceType: ClusterIP
  postProcessingService:
    image: <registry>/post-processing-service@sha256:<digest>
    port: 8081
    serviceType: ClusterIP
    args: ["-max-runners-per-round", "30"]
  experimentalDesignService:
    design: "human-interactive design (manual acceptance pass)"
    image: <registry>/experimental-design-service@sha256:<digest>   # the reference EDS - not the mock
    port: 8082
    serviceType: ClusterIP
```

The field shape mirrors the smoke's green experiment (`test/e2e/manifests/experiment.yaml`) — the same required fields, ports, service types, and the PPS wave-cap arg — with exactly two deltas: `experimentalDesignService.image` is the **reference EDS** (not the harness's mock), and the images are digest-pinned placeholders filled from the lock. `runner` is omitted (the controller's default Job template applies).

**The pass** (every step is a human action — no harness):

1. `kubectl apply -f simulationexperiment-manual.yaml`.
2. Watch the phase: `Pending → Provisioning → InProgress` (`kubectl get simulationexperiment manual-acceptance -w`); the `InProgress` message "All components provisioned and ready" — after R5 — includes the EDS being Ready. The experiment now idles `InProgress` with zero scenarios: the open-ended interactive window.
3. `kubectl port-forward svc/<the operator-named EDS Service> 8082` (the name follows the operator's per-component convention — E2 defines it; `kubectl get svc`) and open `http://localhost:8082`.
4. **Design:** the parameter entries (values of the four varyable parameters) and the scenarios (each with `priority`, `number_of_reps`, `confidence_metric`, and its recipe composition).
5. **Save Experiment** — then verify the Detail DB rows (port-forward the detail DB: the entry tables, `recipes`, the `simulation_parameters` view).
6. **Start Experiment** — the pre-flight passes, the handshake completes, the batch publishes; the scenarios appear and run (runner Jobs, PPS evaluation) and the experiment lands **`Finished`** (sticky).
7. Optional: **re-arm mid-run** — design further scenarios, Save, Start; the SM accepts the second batch into the same `InProgress` experiment.
8. Cleanup: `kubectl delete simulationexperiment manual-acceptance` (the GC cascade removes the children); a retained smoke namespace is removed with `make test-clean RUN_ID=<run-id>` per `AGENTS.md`.

## Future increments (ruled as out of scope for this feature)

- **No "resume" of finished experiments (B1, ruled 2026-10-02).** The sticky-terminal ruling stands: a `Finished` experiment cannot take new scenarios (the SM's `ready` reply is `InProgress`-only; the verdict stays stable). The dynamic path is *forward*, not backward — the experiment must not finish *early* while the EDS still has scenarios to generate (the next item).
- **Optimization-mode SM↔EDS protocol (direction ruled 2026-10-02; design iterating — a separate future feature).** Simulation-based optimization EDSs append scenarios iteratively; under the landed verdict semantics the `Finished` verdict fires when the last *known* scenario is terminally verified, so an optimizing EDS that is still generating would be silently cut off (`status=error` from the availability request, no diagnostic on the EDS side). **Ruled direction (user):** the EDS declares `experiment_type` (`standard` | `optimization`) in the availability request (first contact); in `optimization` mode the SM withholds the `Finished` verdict until the EDS publishes a finalization batch (`ScenarioBatch` with `final: true`, empty `scenarios`) — "no further scenarios will be generated"; the verdict then requires all scenarios terminally verified **and** finalized; `Failed` remains fail-fast in both modes; finalization is irrevocable (later batches rejected, the landed ack's `reason` carries why); non-finalization keeps the experiment `InProgress` (convergence is the EDS's job; no SM-side timer). **Open iteration:** the availability reply echoing the mode (an optimization EDS must detect a pre-increment SM and surface it cleanly instead of being silently cut off), field naming, no-un-finalization, docs-only operator surface. **Out of scope for this feature** (scope boundary: no change to the SM's intake or the availability protocol — landed and cluster-proven); the reference EDS component stays `standard`-mode.
