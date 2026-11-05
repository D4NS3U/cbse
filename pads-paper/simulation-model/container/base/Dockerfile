# Hash-locked Python runner base image for CBSE alpha4 generated runners.
#
# This image is a repository-built shared base, separate from the reference
# Translator Go image. The generated scenario-specific runner image inherits
# this base and adds only the generated model and Result DB connection
# configuration. The base contains no model, scenario parameters, database
# configuration, or credentials.
#
# PYTHON_BASE_IMAGE is a locked official Python platform manifest digest
# (Python 3.14.6, linux/amd64) supplied by the repository source-image lock. A
# missing argument fails the build: the base image is never inferred from a
# floating tag. The requirements lock installs exactly SimPy 4.1.2 and Psycopg
# 3.3.4 with the matching psycopg-binary 3.3.4 distribution, pinned through
# pip --require-hashes. The build imports SimPy and Psycopg and verifies their
# exact versions before the layer is committed, then removes the installation
# input and pip caches. See slice 05 of the simrunner-start-and-container-creation
# change for the contract.

# syntax=docker/dockerfile:1.7
ARG PYTHON_BASE_IMAGE
FROM ${PYTHON_BASE_IMAGE}

COPY requirements.lock /tmp/runner-base/requirements.lock

RUN pip install --no-cache-dir --require-hashes -r /tmp/runner-base/requirements.lock \
    && python -c "import simpy, psycopg; assert simpy.__version__ == '4.1.2', simpy.__version__; assert psycopg.__version__ == '3.3.4', psycopg.__version__" \
    && rm -rf /tmp/runner-base /root/.cache

WORKDIR /runner

USER 1000:1000
