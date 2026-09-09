# Reference Scenario Detail Database image for CBSE alpha4.
#
# This image derives from the locked official PostgreSQL manifest digest supplied
# through the POSTGRES_IMAGE build argument. A missing argument fails the build:
# the base image is never inferred from a floating tag. The standard PostgreSQL
# first-start initialization in /docker-entrypoint-initdb.d creates the
# schema-qualified reference table, applies the explicit grants, and inserts the
# four immutable parameter rows before the server reports readiness.
#
# The image and initialization SQL contain no database credentials. The Operator
# supplies POSTGRES_USER, POSTGRES_PASSWORD, and POSTGRES_DB from
# spec.detailDatabase at runtime; the initialization SQL runs as that account,
# which owns the reference table. Translator later connects with the same
# POSTGRES_USER account to read the four rows. See slice 05 of the
# simrunner-start-and-container-creation change for the contract.

# syntax=docker/dockerfile:1.7
ARG POSTGRES_IMAGE
FROM ${POSTGRES_IMAGE}

# The init SQL is executed by the official PostgreSQL entrypoint on the first
# initialization of the data directory, as the configured POSTGRES_USER.
COPY 10-simulation-parameters.sql /docker-entrypoint-initdb.d/10-simulation-parameters.sql
