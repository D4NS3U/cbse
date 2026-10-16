# IMPLEMENTATION_HANDOFF — the Experimental Design Service reference component

**Settled 2026-10-16.** The feature is complete: merged to `main` as the single
user-approved event after both settlement gates passed. Branch:
`D4NS3U/experimental-design-service` (base `655711a`, merge head the branch
tip; all commits trailed per the orchestration discipline).

## Slice states

| Slice | State | Evidence |
|---|---|---|
| E1 — the reference EDS module | **complete** | `component-templates/experimental-design-service` (module, API, embedded UI incl. Finish, recipestore self-provision, publisher skeleton, Dockerfile); glm review approve; standalone + workspace test-fast green; the smoke + the acceptance pass exercised it live |
| E2 — the operator's `reconcileEDS` | **complete** | `<exp>-eds` + `<exp>-eds-svc` from the CR, D2 env contract, digest validation, readiness-gate membership; envtest green; glm review approve; live in every smoke run |
| E3 — the SM finalization protocol | **complete** | SBO wire additions, the durable `experiment_finalization` marker, the finalization-aware verdict, the post-final rejection, the InProgress diagnostic; glm review approve; live: the marker row + the terminal `Finished` in both gates |
| E4 — the harness/e2e integration | **complete** | The 8-image set, the six-object seed + `sqldoc` lockstep, the API-driven green flow incl. Finish, the fcrash helper, the inventories re-baselined, the mock's finalization + handshake, the acceptance CR; glm review approve |
| E5 — the documentation | **complete** | CDG (the per-experiment EDS architecture, the SBO wire, the reference-EDS section), the detail-DB README rework; glm review request-changes → resolved at settlement |

All five waves ran under the ruled parallel discipline (W1 = E1 ∥ E2 ∥ E3),
each implementer on `ai.forge/qwen3.8-27b-nvfp4` with an independent
`ai.forge/glm` review lane (lane separation attested per report; reports under
`artifacts/orchestration/`).

## The settlement gates

1. **The consolidated settlement smoke: GREEN** — run `20261016070724-fe6d9a`,
   **15/15 specs, rc=0**, clean teardown. The campaign (ten attempts) found six
   real defects, every one fixed on the branch and proven by the final run:
   the EDS build context (the manager's pre-flight ruling), E1's
   standalone-incomplete `go.sum`, the invalid `CREATE VIEW IF NOT EXISTS`
   (erratum 4 — live-verified against the locked PostgreSQL), the pending
   design-id collision (erratum 6 — the canonical multi-recipe flow), the
   harness teardown's SM-finalizer deadlock + lease leak (CRs-first deletion
   + the lease-release retry), and the fcrash InProgress race.
2. **The B2-C human-interactive acceptance pass: PASSED** — recorded in
   [`acceptance/manual-pass-2026-10-16.md`](acceptance/manual-pass-2026-10-16.md)
   (the 9 steps against the harvested cluster evidence; the durable
   finalization marker, the 15-scenario intake, the live top-up, the terminal
   `Finished`).

## Errata ledger (on `main`)

1. The batch-ack delivery completed on the wire (reply-subject-gated;
   e2e assertions target scenario rows, never an ack payload).
2. The `scenario_count:0` Finish-handshake corner — a harmless producer-side
   encoding asymmetry (no round-trip).
3. The final-with-scenarios redelivery window — a documented limitation of
   the spec-mandated insert-then-mark order.
4. The view DDL form is `CREATE OR REPLACE VIEW` (`CREATE VIEW IF NOT EXISTS`
   is invalid PostgreSQL — live-verified).
5. The detaildb Secret is mounted read-only (the D2 prose's "read-write" was
   imprecise; the code is correct).
6. The server's issued-id ceiling (the store's NextID sees only persisted
   rows; pending designs advance a monotonic per-process ceiling).
   Operational: `go work sync` is the churn generator (workspace sum
   maintenance is `go mod download` in workspace mode).

## Known leftovers (tracked, not blockers)

- The C1-deferred private-string class (harness scripts, test fixtures,
  docs) — the public-repo-hygiene C1 package is the scheduled vehicle.
- Harness observation: the self-test's fake docker cannot see build contexts
  (a future truth for `test-harness.sh`).
- The pre-existing PPS-era staleness in `COMPONENT_DESIGN_GOALS.md` (outside
  this feature's doc slices) — flagged by the E5 review lane.
