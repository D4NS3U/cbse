# PADS paper — simulation model container

Paper-local home for the runner image contract that hosts one replication of
the `erlang_a_v1` model: the shared runner-base image, the standalone
application image, and the generated scenario-specific runner Dockerfile.

## Baseline

`component-templates/translator/runner-base/` and the generated-runner
Dockerfile contract produced by the Translator generator. P3A imported the
baseline `runner-base/` tree byte-identically into [`base/`](base/); see the
[template baseline provenance](../../TEMPLATE_BASELINE.md).

## Template-reuse ruling (mandatory)

> Reuse the already existing CBSE templates and extend them only with the parts
> necessary for the impatient-customer multi-server queue (M/M/c+M, Erlang-A)
> model. Do not replace a template with a clean rewrite, parallel framework, or
> speculative abstraction.

## Role in Experiments A–C

For **Experiments A and B**
([initial-results plan](../../../devlog/changes/pads-paper/INITIAL_RESULTS_PLAN.md)),
one scenario-specific container image hosts exactly one replication: the baked
scenario parameters, the generated model/result modules, and the Result DB
connection configuration, executed as a numeric non-root user. **Experiment C**
replays recorded rows offline outside Kubernetes and uses no container.

## Preserved baseline contracts (must survive template import)

- **Immutable base-image input.** The base image is built from a locked
  official Python platform manifest digest supplied as the `PYTHON_BASE_IMAGE`
  build argument; a missing argument fails the build — the base is never
  inferred from a floating tag.
- **Hash-locked dependencies.** `requirements.lock` pins exactly SimPy 4.1.2
  and Psycopg 3.3.4 (with its matching `psycopg-binary`) by version and
  content hash, installed with `pip --require-hashes`; the build imports both
  and verifies the exact versions before the layer is committed.
- **No secrets in the image.** The base contains no model, no scenario
  parameters, no database configuration, and no credentials; the generated
  runner adds only the generated modules and the baked connection
  configuration.
- **Numeric non-root identity.** The container runs as numeric UID/GID
  1000:1000; the generated Dockerfile keeps `FROM <digest-pinned base image>`,
  the runner-file copy with numeric ownership, `USER 1000:1000`, and the
  `python3 /runner/main.py` entrypoint.
- **No dependency reinstallation.** The generated Dockerfile installs no
  packages; it inherits the base's locked dependency closure.

## Expected Erlang-A-specific extension area

None for the container contract itself. The paper model enters as the
generated model/result modules copied into the runner build context; the base
image, the hash lock, and the generated Dockerfile shape remain unchanged.

## Preserved interfaces (must not change in this directory)

- The `runner-base` Dockerfile and `requirements.lock` contract;
- The digest-pinned `FROM` input of the generated-runner Dockerfile;
- The numeric non-root user, entrypoint, and build-context layout;
- The standalone application Dockerfile contract: the defaultless
  `RUNNER_BASE_IMAGE` argument, the exact two-module copy into `/runner/`, the
  explicit `USER 1000:1000`, and the no-`CMD` CLI entrypoint.

## Standalone application image (P3B2)

[`app/Dockerfile`](app/Dockerfile) packages the accepted P3B1
credential-free CLI as a paper-local application image built on the
paper-local runner-base. One invocation runs one replication and writes
exactly one compact, key-sorted JSON result object to stdout — no database
access, no credentials, and nothing else on stdout.

### Two-stage local build chain

Only repository-locked inputs are used. Stage 1 builds the paper-local
runner-base from the locked `PYTHON_BASE_IMAGE` digest in
[`test/e2e/images.lock.env`](../../../test/e2e/images.lock.env); stage 2
builds the application image on top of that locally built runner-base.
`<local-runner-base-tag>` and `<local-model-app-tag>` are placeholder local
tags for build/run verification only: arbitrary local names, with no
registry involved. **No registry push happens in this slice**; publication
of paper images is deferred to the experiment-flow integration (P4 and
later), which wires them into an executed experiment flow.

```sh
set -a
source test/e2e/images.lock.env   # provides the locked PYTHON_BASE_IMAGE digest
set +a

# Stage 1 - paper-local runner-base from the locked digest input
docker buildx build --platform linux/amd64 --pull --load \
    --file pads-paper/simulation-model/container/base/Dockerfile \
    --build-arg "PYTHON_BASE_IMAGE=${PYTHON_BASE_IMAGE}" \
    --tag <local-runner-base-tag> \
    pads-paper/simulation-model/container/base

# Stage 2 - standalone application image on the local runner-base
docker buildx build --platform linux/amd64 --load \
    --file pads-paper/simulation-model/container/app/Dockerfile \
    --build-arg "RUNNER_BASE_IMAGE=<local-runner-base-tag>" \
    --tag <local-model-app-tag> \
    pads-paper/simulation-model
```

The `RUNNER_BASE_IMAGE` build argument has no default: a build without it
fails (`base name (${RUNNER_BASE_IMAGE}) should not be blank`), so the base
is never inferred from a floating tag. The stage-2 build context is the
`pads-paper/simulation-model` directory; the Dockerfile copies exactly the
canonical `src/model.py` and `src/cli.py` into `/runner/` with numeric
ownership `1000:1000`. It copies no `result.py`, `seed.py`, tests, scenario
files, database configuration, or credentials, and installs no packages.

### In-container run

All four CLI flags are required (the entrypoint defines no `CMD`); the
values below are placeholder flags:

```sh
docker run --rm <local-model-app-tag> \
    --arrival-rate <positive-integer> \
    --service-rate <positive-integer> \
    --run-duration <positive-integer> \
    --seed <non-negative-integer>
```

With identical flags the run is deterministic: two runs produce
byte-identical stdout — one compact, key-sorted JSON object with exactly
the 13 P3B1 fields plus a single newline, all metric values finite and
`abandonment_probability`/`server_utilization` in `[0, 1]`. An invalid
invocation (missing flag, non-positive rate, negative seed) exits non-zero
with stderr diagnostics and empty stdout. The container executes as numeric
UID/GID 1000:1000, and `/runner` contains exactly `model.py` and `cli.py`.

### Distinction from the production runner

This standalone image is **not** the Translator-generated scenario runner.
It accepts the scenario parameters plus the effective seed as CLI flags and
performs no database access at all. The future production runner image
(P4) is generated by the paper-local Translator per scenario with baked
scenario parameters, a hostname-derived effective seed, and the Result DB
connection configuration, and inserts the single JSONB result row into
PostgreSQL.

## Status

**P3A baseline import complete; P3B2 standalone application image
implemented — behavior unchanged.** [`base/`](base/) contains the
byte-identical baseline `runner-base/` tree (Dockerfile and
`requirements.lock`) recorded in the [template baseline
provenance](../../TEMPLATE_BASELINE.md). In P3B2 the paper-local
runner-base is built locally from that Dockerfile with the locked
`PYTHON_BASE_IMAGE` digest, and [`app/`](app/) provides the standalone
application Dockerfile on top of it (two-stage local chain; nothing is
pushed to any registry in this slice). The Translator-generated
scenario-specific runner image is not part of this directory and belongs
to P4.
