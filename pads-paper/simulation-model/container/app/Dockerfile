# syntax=docker/dockerfile:1.7
#
# Standalone paper model application image (P3B2) for the erlang_a_v1
# credential-free CLI.
#
# The application image builds on the paper-local runner-base image, which
# the caller must build first from
# pads-paper/simulation-model/container/base/Dockerfile using the locked
# PYTHON_BASE_IMAGE digest from test/e2e/images.lock.env. That locally built
# runner-base image is supplied as the RUNNER_BASE_IMAGE build argument. The
# argument has no default: a missing argument fails the build, so the base is
# never inferred from a floating tag.
#
# The image copies exactly the canonical model.py and cli.py from
# pads-paper/simulation-model/src/ (relative to the build context, the
# pads-paper/simulation-model directory) into /runner/. It copies no
# result.py, seed.py, tests, scenario files, database configuration, or
# credentials, and it installs no packages. The inherited runner-base
# non-root numeric user is kept explicitly: the container executes as
# USER 1000:1000. The entrypoint runs the accepted CLI; all four CLI flags
# are required, so no CMD is defined. No EXPOSE, volume, network, or secret
# construct is added.
#
# One invocation runs one replication and writes exactly one compact,
# key-sorted JSON result object to stdout.

ARG RUNNER_BASE_IMAGE
FROM ${RUNNER_BASE_IMAGE}

COPY --chown=1000:1000 src/model.py src/cli.py /runner/

USER 1000:1000

ENTRYPOINT ["python3", "/runner/cli.py"]
