# Slice D6 — Reproducibility documentation

Normative and self-contained. Read order: [../FEATURE.md](../FEATURE.md), all
settled D1–D5 implementation/evidence, root `AGENTS.md`, then this slice. Specs
are read-only. Stop and ask on unresolved contradiction.

This is wave W5 and depends on D5. It is documentation-only. Final edits to
`FEATURE.md`, `THESIS_EXPLANATION.md`, and `IMPLEMENTATION_HANDOFF.md` remain a
manager settlement action after independent review; this worker supplies the
verified facts but does not edit `devlog/**`.

## Target

Sole ownership of:

- new canonical `docs/REPRODUCIBILITY.md`;
- root `README.md` only to link the canonical guide;
- `experiment-operator/README.md` randomness API section;
- `component-templates/experimental-design-service/README.md` scenario-seed
  semantics;
- `component-templates/translator/README.md` custom Translator/base-Facade and
  result-provenance contract;
- `docs/COMPONENT_DESIGN_GOALS.md` only for the component ownership boundary;
- `docs/HELM_INSTALLATION.md` only for root-seed installation/first-experiment
  guidance;
- bounded `docs/wiki/` links/summary updates generated or manually updated under
  the repository's established wiki policy.

No code, tests, API/CRD, manifests, chart templates, harness, image locks,
artifacts, or `devlog/**` changes.

## Change

### D6.1 Canonical reproducibility guide

Create `docs/REPRODUCIBILITY.md` covering:

1. terminology: computational repeatability, computational reproducibility,
   and independent statistical replication;
2. current hierarchy: root seed → `seed_policy` scenario seed → global
   replication index → effective seed → local model RNG;
3. exact ownership table for CR, EDS, SM, Kubernetes, base image, Translator,
   Result DB, and PPS;
4. required CR YAML with `spec.randomness.rootSeed` and range/immutability;
5. scenario seed domain, preservation of the `seed_policy` name, and explicit
   common-random-number meaning when a seed is reused across scenarios;
6. global index formula and adaptive-wave example (30 + 40 → 0..69);
7. retry behavior and duplicate-result idempotency;
8. exact derivation ID and a link/reference to normative test vectors without
   suggesting user selection;
9. result provenance fields and interpretation;
10. custom Translator responsibilities and the `cbse_random` Facade API;
11. recreation workflow using the same CR root, EDS design, and image digests;
12. how to choose a new root seed for an independent statistical replication;
13. failure modes for missing runtime variables or legacy result tables;
14. limitations: RNG subsequence guarantees, model/dependency/architecture
    changes, floating point, external inputs, internal model concurrency;
15. security statement: seeds are public provenance; credentials are not.

Clearly distinguish historical hostname-derived behavior from the landed
behavior. Do not imply old and new effective seeds are compatible.

### D6.2 Component documentation

- Operator README: required field, accepted range, immutability, no generated
  mode, no derivation selector.
- EDS README: `seed_policy` is a scenario seed, exact range, same scenario seed
  does not by itself give each runner a different stream.
- Translator README: base-image module contract, scenario-seed argument,
  framework variables, local RNG adaptation, required provenance, and no local
  reimplementation of derivation.
- Component design goals: concise ownership matrix and no centralized RNG.
- Helm guide: include root seed in first experiment and upgrade/recreation
  cautions; Helm itself does not choose seeds.

### D6.3 Evidence-backed claims only

Use D5 receipts for claims about:

- exact 15/15 manifest and Helm runs;
- contiguous adaptive-wave indices;
- cross-installer/recreated stream-manifest equality;
- idempotent retry storage;
- immutable image provenance.

Name run IDs only if the repository's normal documentation policy includes test
receipts; never include private registry names, cluster endpoints, kubeconfig
paths, or raw artifact paths intended to remain local. Prefer stable behavior
over transient operational details.

### D6.4 Manager settlement handoff

The implementation report includes a concise list of any final factual changes
needed in `THESIS_EXPLANATION.md`, especially actual file/module names, final
result table behavior, and D5 evidence. The worker does not modify that file.

## Constraints

- Documentation must match the landed code exactly; no aspirational future
  replay API or named-substream claim.
- No credentials, Secret payload commands, private infrastructure, or runnable
  private defaults.
- Examples use public placeholders such as `example.invalid` where an image is
  necessary and never use tags where digest references are required.
- Do not call `seed_policy` an algorithm selector.
- Do not call `seed_derivation` a user setting.
- Do not claim mathematical independence/non-overlap for arbitrary RNGs.
- Preserve institution attribution, license headers, and paper references.
- Documentation-only: no cluster smoke. `make test-fast` remains the independent
  review baseline.
- Commit/attestation/hygiene constraints apply.

## Ownership

D6 owns public documentation only. Manager owns normative devlog/thesis/handoff
settlement. Findings of code/document mismatch are escalated; do not fix code.

## Observable acceptance

1. Runtime attestation is `ai.forge/qwen3.8-27b-nvfp4`.
2. Diff contains only Target documentation paths.
3. All relative Markdown links resolve.
4. Search confirms every required concept in D6.1 is present and no forbidden
   derivation selector/generated-root claim appears.
5. YAML/JSON/code examples are syntactically valid and match landed field/env/
   result names.
6. Root/Operator/EDS/Translator/Helm/component-design docs all link to the
   canonical guide without circular ambiguity.
7. Added-line scans find no private registry, endpoint, kubeconfig path,
   credential, token, Secret payload, or mutable image tag.
8. `make test-fast` succeeds.
9. Report lists D5 evidence consumed and the manager-only thesis settlement
   facts.
10. Commit HEAD and orchestration trailers are reported.

Completion requires one `worker_done` with evidence 1–10, files, report, and
attestation. No smoke run is required.
