# Manual acceptance pass — 2026-10-16 (B2-C, ruling Option C)

**Outcome: PASS.** Performed by the user (D. Seufferth) in the browser against the
reference Experimental Design Service, on the retained settlement-smoke stack
(run `20261016072020-5099e4`, namespace `cbse-e2e-20261016072020-5099e4`; the
retained suite went 14 passed / 0 failed / 1 skipped-by-design before the pass).
The harness was not involved in the pass (the interactive mode: the human drives
the EDS's embedded UI, which issues the same JSON API calls the e2e suite
drives). All evidence below was harvested by the manager, read-only, from the
retained cluster state after the user reported completion.

## The 9 steps (Appendix A) — as evidenced

1. **Apply**: `SimulationExperiment/manual-acceptance` created (~07:31 UTC); the
   operator provisioned the full per-experiment set from the CR — the
   detail-DB, result-DB, translator (+ BuildKit sidecar), PPS, and the
   reference EDS (`manual-acceptance-eds` Deployment +
   `manual-acceptance-eds-svc` Service, port 8082), digest-pinned images from
   the retained run's `images.env`.
2. **Watch to InProgress**: the readiness gate held the experiment until every
   component — the EDS included (R5) — was ready; the EDS's own log shows the
   intended startup behavior: its first container fail-fasted with
   `connect: connection refused` while its detail DB was still coming up
   (07:32:59Z), the Deployment restarted it, and the second container logged
   `detail DB schema self-provisioned` (07:33:14Z) — the R3 six-object
   self-provision against a real PostgreSQL — then `serving on :8082`.
3. **Port-forward + open the UI**: `kubectl port-forward
   svc/manual-acceptance-eds-svc 8082:8082` → the embedded GUI at
   `http://localhost:8082`.
4. **Design**: the Detail DB shows the user's design persisted — 8
   `arrival_rate` entries, 8 `service_rate`, 5 `run_duration`, 5 `seed_policy`
   (ids 5+, the read-only baseline rows 1–4 coexisting), and **19 recipes**
   (ids 5–19).
5. **Save Experiment**: the single-transaction save upserted the entries and
   the recipe locator rows; the `simulation_parameters` view projects all 19
   recipes over the join (verified via the live DB).
6. **Start Experiment**: the pre-flight passed, the availability handshake
   completed (the `experiment_type` declaration + echo), and the batch(es)
   published; the Core DB holds **15 scenarios** for the project (ids 7–21,
   priorities 1–11, recipes 5–19, all eventually state `Finished`). The
   scenarios ran through the reference chain: the Result DB carries one
   per-scenario table with full-JSON rows echoing the resolved recipe values
   (the live-locator model working: `parameterset_id`, `arrival_rate`,
   `service_rate`, `run_duration`, `seed_policy`), the derived
   `effective_seed`, `completed_customers`, and `mean_wait_time` per
   replication — e.g. scenario 7: 15 rows, mean wait ≈ 25.3; **scenario 21: 42
   completed replications** — the PPS's statistical top-up visibly running
   (the sequential fixed-width policy requesting additional waves until the
   95%-CI half-width met the user's per-scenario precision target).
7. *(Optional re-arm — the user's choice; the 15-scenario set is consistent
   with multiple design/start rounds.)*
8. **Finish Experiment**: the durable marker row in `experiment_finalization`
   records the irrevocable finalization at **2026-10-16 08:59:56 UTC**; with
   all 15 scenarios terminally verified, the SM's finalization-aware verdict
   fired — the CR's phase transitioned to **`Finished`** (the operator's
   event: `experiment phase transitioned to Finished`; status message
   "All scenarios finished (scenarioManagerVerdict report)").
9. **Cleanup**: pending (the CR and children remain in the retained namespace
   for inspection at the time of this record; the retained stack is removed
   with `make test-clean RUN_ID=20261016072020-5099e4`).

## Verdict

The human-interactive acceptance pass **passed**: the interactive window
worked end-to-end in the browser (design → Save → Start → the scenarios
running with the live top-up policy → Finish → the experiment's terminal
`Finished` derived from the finalization-aware verdict), with the persisted
state at every layer matching the ruled contracts. This record plus the
green consolidated settlement smoke (`20261016070724-fe6d9a`, 15/15, rc=0)
are the settlement gates ahead of the merge approval.
