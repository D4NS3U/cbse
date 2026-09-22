-- Reference Scenario Detail Database initialization for CBSE alpha4.
--
-- This SQL is copied into /docker-entrypoint-initdb.d by the reference image
-- Dockerfile and executed once by the official PostgreSQL first-start
-- initialization, as the configured POSTGRES_USER account. It creates the
-- schema-qualified reference table, documents the required access through
-- explicit grants to the owning account, and inserts the four immutable
-- parameter rows. The values are checked in as fixed, reproducible image
-- inputs; they are never regenerated during image build, container startup,
-- or a smoke run.
--
-- Translator reads this table through the single predefined parameterized
-- query owned by the example generator. Detail DB rows are immutable for this
-- feature: no CBSE component updates or deletes them.

CREATE TABLE public.simulation_parameters (
    parameterset_id INTEGER PRIMARY KEY CHECK (parameterset_id > 0),
    arrival_rate    INTEGER NOT NULL CHECK (arrival_rate > 0),
    service_rate    INTEGER NOT NULL CHECK (service_rate > 0),
    run_duration    INTEGER NOT NULL CHECK (run_duration > 0),
    seed_policy     INTEGER NOT NULL CHECK (seed_policy >= 0)
);

GRANT USAGE ON SCHEMA public TO CURRENT_USER;
GRANT SELECT ON TABLE public.simulation_parameters TO CURRENT_USER;

INSERT INTO public.simulation_parameters
    (parameterset_id, arrival_rate, service_rate, run_duration, seed_policy)
VALUES
    (1, 2, 4, 100, 1001),
    (2, 3, 5, 120, 1002),
    (3, 4, 7, 150, 1003),
    (4, 5, 8, 180, 1004);
