# PADS paper — simulation model container

Paper-local home for the runner image contract that hosts one replication of
the `erlang_a_v1` model: the shared runner-base image and the generated
scenario-specific runner Dockerfile.

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
- The numeric non-root user, entrypoint, and build-context layout.

## Status

**P3A baseline import complete — behavior unchanged.** [`base/`](base/) contains
the byte-identical baseline `runner-base/` tree (Dockerfile and
`requirements.lock`) recorded in the [template baseline
provenance](../../TEMPLATE_BASELINE.md). The imported Dockerfile is a dormant
source snapshot: no paper-local image is built, selected, or deployed in P3A,
and the standalone paper application Dockerfile belongs to P3B.
