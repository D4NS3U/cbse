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

## Ownership and status

This reference image form is deployed by the Experiment Operator, which also
supplies `POSTGRES_USER`, `POSTGRES_PASSWORD`, and `POSTGRES_DB` from the
experiment's `spec.detailDatabase`. Slice 07 wires the image build and the
operator deployment details; this file documents only the contents owned by
Slice 05.
