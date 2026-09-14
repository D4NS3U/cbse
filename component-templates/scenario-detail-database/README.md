# CBSE Reference Scenario Detail Database

This directory holds the repository-owned reference **Scenario Detail Database**
image for CBSE alpha4 (Slice 05).

## Contents

- `10-simulation-parameters.sql` — copied into `/docker-entrypoint-initdb.d/`
  by the image `Dockerfile` and executed once by the official PostgreSQL
  first-start initialization, as the configured `POSTGRES_USER` account. It
  creates the schema-qualified reference table `public.simulation_parameters`,
  documents the required access through explicit grants to the owning account,
  and inserts the four immutable parameter rows.
- `Dockerfile` — builds the reference image from the locked `POSTGRES_IMAGE`
  (passed via the `POSTGRES_IMAGE` build arg) and copies the initialization SQL
  into the official first-start initdb directory.

## Schema

```sql
CREATE TABLE public.simulation_parameters (
    parameterset_id INTEGER PRIMARY KEY CHECK (parameterset_id > 0),
    arrival_rate    INTEGER NOT NULL CHECK (arrival_rate > 0),
    service_rate    INTEGER NOT NULL CHECK (service_rate > 0),
    run_duration    INTEGER NOT NULL CHECK (run_duration > 0),
    seed_policy     INTEGER NOT NULL CHECK (seed_policy >= 0)
);

GRANT USAGE ON SCHEMA public TO CURRENT_USER;
GRANT SELECT ON TABLE public.simulation_parameters TO CURRENT_USER;
```

All five values are PostgreSQL `INTEGER`s. The four checked-in rows are fixed,
reproducible image inputs; they are never regenerated during image build,
container startup, or a smoke run.

The example generator (in `../translator/internal/generator`) reads this table
through the single predefined parameterized query
(`SELECT ... FROM public.simulation_parameters WHERE parameterset_id = $1`) and
never executes SQL text received in a message. Detail DB rows are immutable for
this feature: no CBSE component updates or deletes them.

## Build and image contract (Slice 07)

The reference Detail Database is built by `test/harness/build-images.sh` using
the **exact component token `scenario-detail-database`** and the **nested**
repository layout (unlike the five flat shared components):

- canonical repository: `${CBSE_REGISTRY}/scenario-detail-database`
- canonical tag: `${CBSE_REGISTRY}/scenario-detail-database:${VERSION}`
- immutable provenance tag:
  `${CBSE_REGISTRY}/scenario-detail-database:${VERSION}.sha-${commit}-${sourceHash}-${runId}`
- digest output in `images.env`:
  `DETAIL_DB_IMAGE=${CBSE_REGISTRY}/scenario-detail-database@sha256:<64hex>` (the
  digest output **keeps** the nested repository path)

The Dockerfile requires the locked `POSTGRES_IMAGE` build argument and uses
`FROM ${POSTGRES_IMAGE}`; a missing argument fails the build. The build is the
only place `POSTGRES_IMAGE` is consumed; the Result DB does **not** use this
derived image — `spec.resultDatabase.image` receives the locked official
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
repository (the `cbse-test-runner` namespace for generated runner images). The
reference Detail Database repository `${CBSE_REGISTRY}/scenario-detail-database`
must **never** be deleted or pruned by generated-runner cleanup. The cleanup
adapter is restricted to `cbse-test-runner`; it must not touch `cbse-test`,
`cbse-test/scenario-detail-database`, or any other shared reference repository.

## Ownership and status

This reference image form is deployed by the Experiment Operator, which also
supplies `POSTGRES_USER`, `POSTGRES_PASSWORD`, and `POSTGRES_DB` from the
experiment's `spec.detailDatabase`. The image build and digest-output contract
are wired by Slice 07; this file documents the image contents owned by Slice 05
and the build/cleanup contracts owned by Slice 07.
