-- Reference Scenario Detail Database initialization for CBSE alpha4.
--
-- This SQL is copied into /docker-entrypoint-initdb.d by the reference image
-- Dockerfile and executed once by the official PostgreSQL first-start
-- initialization, as the configured POSTGRES_USER account. It creates the
-- ruled R2 design-layer schema - the four per-parameter entry tables
-- (arrival_rate, service_rate, run_duration, seed_policy), the recipes
-- locator table (one foreign key per parameter), and the
-- simulation_parameters view over their join - documents the required
-- access through explicit grants to the owning account, and inserts the
-- four immutable baseline rows. The values are checked in as fixed,
-- reproducible image inputs; they are never regenerated during image build,
-- container startup, or a smoke run.
--
-- The Experimental Design Service's write surface is exactly the entry
-- tables and recipes: it assigns new ids >= 5 and upserts its own rows
-- (live locators) in one transaction. Baseline ids 1-4 are read-only in all
-- five tables; they remain the reference baseline.
--
-- The Translator reads public.simulation_parameters through the single
-- predefined parameterized query owned by the example generator. The view
-- is transparent to that lookup: it projects the exact legacy columns
-- (parameterset_id, arrival_rate, service_rate, run_duration, seed_policy)
-- over recipes joined with the four entry tables, so the landed lookup
-- contract (the query text, the parameterset_id key, the four-value
-- projection) is unchanged by the rework from the former single seeded
-- table.

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

GRANT USAGE ON SCHEMA public TO CURRENT_USER;
GRANT SELECT ON TABLE public.simulation_parameters TO CURRENT_USER;
GRANT SELECT, INSERT, UPDATE ON TABLE public.arrival_rate TO CURRENT_USER;
GRANT SELECT, INSERT, UPDATE ON TABLE public.service_rate TO CURRENT_USER;
GRANT SELECT, INSERT, UPDATE ON TABLE public.run_duration TO CURRENT_USER;
GRANT SELECT, INSERT, UPDATE ON TABLE public.seed_policy TO CURRENT_USER;
GRANT SELECT, INSERT, UPDATE ON TABLE public.recipes TO CURRENT_USER;

-- The immutable baseline (ids 1-4, read-only in all five tables; the EDS
-- id-space starts at 5). The view computes the legacy rows exactly:
-- (1,2,4,100,1001) (2,3,5,120,1002) (3,4,7,150,1003) (4,5,8,180,1004).
INSERT INTO public.arrival_rate (id, value) VALUES
    (1, 2),
    (2, 3),
    (3, 4),
    (4, 5);
INSERT INTO public.service_rate (id, value) VALUES
    (1, 4),
    (2, 5),
    (3, 7),
    (4, 8);
INSERT INTO public.run_duration (id, value) VALUES
    (1, 100),
    (2, 120),
    (3, 150),
    (4, 180);
INSERT INTO public.seed_policy (id, value) VALUES
    (1, 1001),
    (2, 1002),
    (3, 1003),
    (4, 1004);
INSERT INTO public.recipes
    (parameterset_id, arrival_rate_id, service_rate_id, run_duration_id, seed_policy_id)
VALUES
    (1, 1, 1, 1, 1),
    (2, 2, 2, 2, 2),
    (3, 3, 3, 3, 3),
    (4, 4, 4, 4, 4);
