# CBSE Reference Scenario Detail Database

This directory holds the repository-owned reference **Scenario Detail
Database** image for CBSE alpha4: the reference seed for the EDS
design-layer schema, reworked by the experimental-design-service feature
(the ruled R2 six-object schema replaces the former single seeded table).

## Contents

- `10-simulation-parameters.sql` — copied into `/docker-entrypoint-initdb.d/`
  by the image `Dockerfile` and executed once by the official PostgreSQL
  first-start initialization, as the configured `POSTGRES_USER` account. It
  creates the design-layer schema — the four per-parameter entry tables
  (`arrival_rate`, `service_rate`, `run_duration`, `seed_policy`), the
  `recipes` locator table, and the `simulation_parameters` **view** over
  their join — documents the required access through explicit grants to the
  owning account, and inserts the four immutable baseline rows (ids 1–4) in
  all five tables.
- `Dockerfile` — builds the reference image from the locked `POSTGRES_IMAGE`
  (passed via the `POSTGRES_IMAGE` build arg) and copies the initialization
  SQL into the official first-start initdb directory.

## Schema

The SQL block below mirrors the DDL of `10-simulation-parameters.sql`
verbatim:

```sql
CREATE TABLE IF NOT EXISTS public.arrival_rate (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value > 0));
CREATE TABLE IF NOT EXISTS public.service_rate (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value > 0));
CREATE TABLE IF NOT EXISTS public.run_duration (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value > 0));
CREATE TABLE IF NOT EXISTS public.seed_policy  (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value >= 0));
CREATE TABLE IF NOT EXISTS public.recipes (
    parameterset_id INTEGER PRIMARY KEY CHECK (parameterset_id > 0),
    arrival_rate_id INTEGER NOT NULL REFERENCES public.arrival_rate(id),
    service_rate_id INTEGER NOT NULL REFERENCES public.service_rate(id),
    run_duration_id INTEGER NOT NULL REFERENCES public.run_duration(id),
    seed_policy_id  INTEGER NOT NULL REFERENCES public.seed_policy(id)
);
CREATE OR REPLACE VIEW public.simulation_parameters AS
    SELECT r.parameterset_id,
           a.value AS arrival_rate,
           s.value AS service_rate,
           d.value AS run_duration,
           p.value AS seed_policy
    FROM public.recipes r
    JOIN public.arrival_rate a ON a.id = r.arrival_rate_id
    JOIN public.service_rate  s ON s.id = r.service_rate_id
    JOIN public.run_duration  d ON d.id = r.run_duration_id
    JOIN public.seed_policy   p ON p.id = r.seed_policy_id;
```

- The four entry tables each carry one `value` column: `arrival_rate`,
  `service_rate`, and `run_duration` are positive integers; `seed_policy` is
  an integer `>= 0`.
- `recipes` is the locator table: one row per `parameterset_id`, one foreign
  key per parameter, pointing at the entry row the scenario resolves.
- `simulation_parameters` is a **view** over `recipes` joined with the four
  entry tables, projecting the exact legacy columns (`parameterset_id`,
  `arrival_rate`, `service_rate`, `run_duration`, `seed_policy`). The view
  is created in the `CREATE OR REPLACE VIEW` form — the only valid
  idempotent view form in PostgreSQL (`CREATE VIEW IF NOT EXISTS` does not
  exist) — a semantic no-op against a matching pre-created view and
  identical on a fresh data directory.

### Baseline rows (ids 1–4, read-only)

The seed inserts one immutable row per id 1–4 in every table, so the view
computes the legacy single-table projection exactly:

| `parameterset_id` | `arrival_rate` | `service_rate` | `run_duration` | `seed_policy` |
| --- | --- | --- | --- | --- |
| 1 | 2 | 4 | 100 | 1001 |
| 2 | 3 | 5 | 120 | 1002 |
| 3 | 4 | 7 | 150 | 1003 |
| 4 | 5 | 8 | 180 | 1004 |

The rows are read-only: no CBSE component updates or deletes them, and the
Experimental Design Service's id-space starts at 5 in every table (a write
targeting an id 1–4 is refused). The reference image seeds no EDS rows
(id >= 5). The values are fixed, reproducible image inputs; they are never
regenerated during image build, container startup, or a smoke run.

### Grants

```sql
GRANT USAGE ON SCHEMA public TO CURRENT_USER;
GRANT SELECT ON TABLE public.simulation_parameters TO CURRENT_USER;
GRANT SELECT, INSERT, UPDATE ON TABLE public.arrival_rate TO CURRENT_USER;
GRANT SELECT, INSERT, UPDATE ON TABLE public.service_rate TO CURRENT_USER;
GRANT SELECT, INSERT, UPDATE ON TABLE public.run_duration TO CURRENT_USER;
GRANT SELECT, INSERT, UPDATE ON TABLE public.seed_policy TO CURRENT_USER;
GRANT SELECT, INSERT, UPDATE ON TABLE public.recipes TO CURRENT_USER;
```

The Translator receives `SELECT` on the view only (read-only by
construction); the write surface is the entry tables and `recipes`
(`SELECT, INSERT, UPDATE`).

## The EDS write surface

The Experimental Design Service's Detail DB write surface is exactly the
entry tables and `recipes` — ids >= 5 only, never the view, never any other
schema or table. A design save runs in one transaction: new entry ids are
INSERTed and the values of ids the EDS assigned earlier are UPDATEd (live
locators — a saved design's resolved values track its entries until the
next save); new `parameterset_id`s are INSERTed into `recipes` and the four
FK columns of recipe ids the EDS assigned are UPDATEd.

## The Translator's view-transparent lookup

The example generator (in `../translator/internal/generator`) reads this
schema through the single predefined parameterized lookup owned by the
Translator's `detaildb` package (`../translator/internal/detaildb`,
`SELECT arrival_rate, service_rate, run_duration, seed_policy FROM
public.simulation_parameters WHERE parameterset_id = $1`) and never executes
SQL text received in a message. The rework to the six-object schema is
view-transparent: the lookup contract (the query text, the `parameterset_id`
key, the four-value projection) is unchanged by construction.

## Lockstep with the repository-owned SQL

The `sqldoc` lockstep test
(`component-templates/translator/internal/sqldoc/sqldoc_test.go`) asserts
this seed against the repository-owned SQL: the six objects with their
exact DDL lines (including the `CREATE OR REPLACE VIEW` form), the explicit
grants, and the four baseline rows in each of the five tables (every row id
1–4; no EDS row seeded). Seed and contract drifting apart fails
`make test-fast`.

## Build and image contract

The reference Detail Database is built by `test/harness/build-images.sh`
using the **exact component token `scenario-detail-database`** and the
**nested** repository layout (all component images use it):

- canonical repository: `${CBSE_REGISTRY}/scenario-detail-database`
- canonical tag: `${CBSE_REGISTRY}/scenario-detail-database:${VERSION}`
- immutable provenance tag:
  `${CBSE_REGISTRY}/scenario-detail-database:${VERSION}.sha-${commit}-${sourceHash}-${runId}`
- digest output in `images.env`:
  `DETAIL_DB_IMAGE=${CBSE_REGISTRY}/scenario-detail-database@sha256:<64hex>` (the
  digest output **keeps** the nested repository path)

The Dockerfile requires the locked `POSTGRES_IMAGE` build argument and uses
`FROM ${POSTGRES_IMAGE}`; a missing argument fails the build. The build is
the only place `POSTGRES_IMAGE` is consumed; the Result DB does **not** use
this derived image — `spec.resultDatabase.image` receives the locked official
`POSTGRES_IMAGE` digest directly.

### `YY.M.D` version validation

`TEST_IMAGE_VERSION` uses a non-normalizing `YY.M.D` format (for example
`26.7.16`). The harness validates it against `^[0-9]{2}\.[0-9]{1,2}\.[0-9]{1,2}$`
**without** normalizing single-digit month/day values: `26.7.16` stays
`26.7.16` and is not rewritten to `26.07.16`. Single-digit month and day are
accepted as-is; the regex permits 1-2 digit month/day, so callers may optionally
zero-pad, but the harness never rewrites the supplied string. The same version
is the canonical tag for the Detail Database image.

### `DETAIL_DB_IMAGE` output and skip-build input contract

When the harness builds the image, it records `DETAIL_DB_IMAGE` in `images.env`
as the immutable digest reference. When `SKIP_BUILD=1` is set, the smoke harness
does not build any image and instead requires `DETAIL_DB_IMAGE` to be supplied
already as an immutable digest reference (`<repo>@sha256:<64hex>`); a mutable
(floating-tag) `DETAIL_DB_IMAGE` is rejected by preflight.

### Immutable-digest rendering

All references to the Detail Database image in CRs, manifests, and smoke
assertions use the immutable digest form
`${CBSE_REGISTRY}/scenario-detail-database@sha256:<64hex>`, never a floating
tag. The digest is the immutable reference; the canonical and provenance tags
are publish-time artifacts.

### Cleanup prohibition

Generated-runner cleanup targets **only** the `${CBSE_REGISTRY}/cbse-test-runner`
repository. The reference Detail Database repository
`${CBSE_REGISTRY}/scenario-detail-database` must **never** be deleted or pruned
by generated-runner cleanup. The cleanup adapter is restricted to
`cbse-test-runner`; it must not touch `cbse-test`,
`cbse-test/scenario-detail-database`, or any other shared reference repository.

## Ownership and status

This reference image form is deployed by the Experiment Operator, which also
supplies `POSTGRES_USER`, `POSTGRES_PASSWORD`, and `POSTGRES_DB` from the
experiment's `spec.detailDatabase`. This file documents the image contents
(the reference seed of the EDS design-layer schema) and the build/cleanup
contracts wired by the image harness.
