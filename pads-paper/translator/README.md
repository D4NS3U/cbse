# PADS paper — Translator

Paper-local home for the Translator framework that converts a scenario batch
into a generated, digest-pinned runner image executing the `erlang_a_v1`
model.

## Baseline

`component-templates/translator/`. P3A imported the baseline tree
byte-identically into [`component/`](component/); see the [template baseline
provenance](../TEMPLATE_BASELINE.md).

## Template-reuse ruling (mandatory)

> Reuse the already existing CBSE templates and extend them only with the parts
> necessary for the impatient-customer multi-server queue (M/M/c+M, Erlang-A)
> model. Do not replace a template with a clean rewrite, parallel framework, or
> speculative abstraction.

## Role in Experiments A–C

For each scenario in **Experiments A and B**
([initial-results plan](../../devlog/changes/pads-paper/INITIAL_RESULTS_PLAN.md)),
the Translator looks up the scenario's four parameters, generates the
paper-local model and result modules, builds and publishes the
scenario-specific runner image through the BuildKit sidecar, and makes the
digest-pinned image available for the runner. **Experiment C** consumes
already-persisted result rows and does not invoke the Translator.

## Preserved baseline contracts (must survive template import)

- **Detail DB lookup.** The `simulation_parameters` projection is read
  through the single predefined parameterized query keyed on
  `parameterset_id`; the query text, key, and four-value projection are
  unchanged.
- **BuildKit and registry security.** The rootless BuildKit sidecar admission
  gate, the session-mounted registry configuration, the deterministic tag,
  and the registry-push path remain exactly as the template implements them.
- **Generated-image digest verification.** The pushed digest is taken from
  the exporter response and recorded as the immutable reference (the
  `images.env` digest outputs); the runner base reference is digest-pinned and
  never a floating tag.
- **Result DB connection handling.** The baked Result DB connection
  configuration file contract (host, port, dbname, user, password with
  non-root file permissions) is unchanged.
- **Attempt semantics.** One translation attempt produces one complete,
  validated build context; partial output is never published.

## Expected Erlang-A-specific extension area

The generator's model/result module emission: instead of the single-server
queue modules it emits the `erlang_a_v1` model and the additive result
contract, and the generated runner build context carries those paper-local
modules. The generation boundary, its security checks, and the digest
handling stay as they are.

## Preserved interfaces (must not change in this directory)

- The scenario request wire and Detail DB lookup contract;
- The BuildKit sidecar admission-gate and push contract;
- The `images.env` digest output contract;
- The generated Dockerfile structure and runner build-context layout.

## Status

**P3A baseline import complete — behavior unchanged.** [`component/`](component/)
contains the byte-identical baseline Translator tree recorded in the
[template baseline provenance](../TEMPLATE_BASELINE.md). The imported module
is a dormant source snapshot: it is not registered in `go.work` and no
harness or manifest builds or selects it; later slices extend it in place.
