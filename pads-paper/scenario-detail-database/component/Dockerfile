# Reference Scenario Detail Database image for CBSE alpha4.
#
# This image derives from the locked official PostgreSQL manifest digest supplied
# through the POSTGRES_IMAGE build argument. A missing argument fails the build:
# the base image is never inferred from a floating tag. The standard PostgreSQL
# first-start initialization in /docker-entrypoint-initdb.d creates the
# six-object design-layer schema (the four per-parameter entry tables, the
# recipes locator table, and the simulation_parameters view over their join),
# applies the explicit grants, and inserts the four immutable baseline rows
# before the server reports readiness.
#
# The image and initialization SQL contain no database credentials. The Operator
# supplies POSTGRES_USER, POSTGRES_PASSWORD, and POSTGRES_DB from
# spec.detailDatabase at runtime; the initialization SQL runs as that account,
# which owns the schema objects. The Experimental Design Service writes the
# entry tables and recipes (new ids >= 5, baseline ids 1-4 read-only) with the
# same account; the Translator later connects with the same account to read
# the simulation_parameters view. This is the active alpha4 Scenario Detail
# Database image; the baseline rows are the immutable inputs the reference
# Translator's example generator looks up by parameterset_id.

# syntax=docker/dockerfile:1.7
ARG POSTGRES_IMAGE
FROM ${POSTGRES_IMAGE}

# The init SQL is executed by the official PostgreSQL entrypoint on the first
# initialization of the data directory, as the configured POSTGRES_USER.
COPY 10-simulation-parameters.sql /docker-entrypoint-initdb.d/10-simulation-parameters.sql
