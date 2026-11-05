# PADS paper — template baseline provenance

This record documents the exact baseline from which slice P3A
([slice specification](../devlog/changes/pads-paper/slices/P3A-exact-template-import.md))
imported every paper-local implementation directory. The import is a
provenance slice only: no behavior was changed and the Erlang-A model is not
implemented yet.

## Baseline commit

`1dfc7eb5fec358199cb9c28b1df2aaabb035c71a`

Every import below is a byte-identical copy of the Git-tracked files at that
commit. Extraction was Git-aware (`git archive` / `git show`, plus mode
inspection of `git ls-tree`), so ignored caches, build outputs, and local
artifacts cannot enter the import. File bytes and executable modes are
preserved; module paths, imports, README text, Dockerfiles, tests, licenses,
and copyright headers were not rewritten.

## Source-to-destination mappings

| Baseline source (at the baseline commit) | Paper-local destination |
|---|---|
| `component-templates/experimental-design-service/` (18 tracked files) | [`experimental-design-service/component/`](experimental-design-service/component/) |
| `component-templates/translator/` (61 tracked files) | [`translator/component/`](translator/component/) |
| `component-templates/post-processing-service/` (20 tracked files) | [`post-processing-service/component/`](post-processing-service/component/) |
| `component-templates/scenario-detail-database/` (3 tracked files) | [`scenario-detail-database/component/`](scenario-detail-database/component/) |
| Six selected files from `component-templates/translator/internal/generator/runnermod/` | [`simulation-model/src/`](simulation-model/src/) |
| `component-templates/translator/runner-base/` (2 tracked files) | [`simulation-model/container/base/`](simulation-model/container/base/) |

The full Translator tree import therefore also carries the baseline
`runner-base/` and `runnermod/` trees inside
[`translator/component/`](translator/component/); the separate
`simulation-model/src/` and `simulation-model/container/base/` imports are
the canonical model and container baselines that later slices extend in
place.

## Selected canonical model files

Exactly six files from
`component-templates/translator/internal/generator/runnermod/` establish the
canonical model baseline in [`simulation-model/src/`](simulation-model/src/):

1. `model.py`
2. `result.py`
3. `seed.py`
4. `test_model.py`
5. `test_result.py`
6. `test_seed.py`

These files carry the existing CBSE model API, deterministic local-RNG seed
derivation, result-record construction, and test contracts that P3B extends.
Deliberately **not** imported into `simulation-model/src/`: `main.py`,
`resultdb.py`, `test_resultdb.py`, and the `database_endpoint/` package —
the canonical model directory is credential-free and carries no Result DB
endpoint code and no PostgreSQL tests.

## Result Database exception

[`result-database/`](result-database/) contains no imported source. The
settled baseline is the locked official PostgreSQL image with no
repository-owned initialization schema, so P3A performed no code import for
this component by design; the directory remains README-only.

## Scope of the claim

P3A is **byte-identical import evidence only**. It proves that the
paper-local directories above are exact copies of the named baseline files
as of the baseline commit. It does **not** claim that those files will
remain identical after later slices: P3B and beyond are licensed to extend
the paper-local copies with the Erlang-A-required changes ruled in the
[feature specification](../devlog/changes/pads-paper/FEATURE.md), and such
extensions will diverge from this baseline by design.

## Known import consequence

The imported `translator/component/README.md` carries the baseline's relative
link to `../../docs/CLUSTER_REQUIREMENTS.md`, which resolves from
`component-templates/translator/` but not from
`pads-paper/translator/component/`. P3A-R1 forbids rewriting README text, so
the link is preserved verbatim for byte parity; a later licensed slice may
repair the reference in the paper-local copy.

## Reproducible verification

The following commands verify tracked file lists, file bytes, and executable
modes against the baseline commit without modifying the worktree. Run from
the repository root on a checkout containing the baseline commit; every
command exits zero on parity, so a non-zero exit means the paper-local copy
has diverged from this record.

```sh
BASELINE=1dfc7eb5fec358199cb9c28b1df2aaabb035c71a
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# 1) Extract only Git-tracked baseline files (no caches, no local artifacts).
git archive "$BASELINE" component-templates | tar -x -C "$TMP"

# 2) Full component trees: relative file list + bytes.
diff -r "$TMP/component-templates/experimental-design-service" \
        pads-paper/experimental-design-service/component
diff -r "$TMP/component-templates/translator" \
        pads-paper/translator/component
diff -r "$TMP/component-templates/post-processing-service" \
        pads-paper/post-processing-service/component
diff -r "$TMP/component-templates/scenario-detail-database" \
        pads-paper/scenario-detail-database/component

# 3) Canonical model files: byte parity per named file, exactly six files.
for f in model.py result.py seed.py test_model.py test_result.py test_seed.py; do
  git show "$BASELINE:component-templates/translator/internal/generator/runnermod/$f" \
    | cmp - "pads-paper/simulation-model/src/$f"
done
[ "$(cd pads-paper/simulation-model/src && find . -type f | wc -l)" -eq 6 ]

# 4) Runner-base container tree: file list + bytes.
diff -r "$TMP/component-templates/translator/runner-base" \
        pads-paper/simulation-model/container/base

# 5) Executable-mode parity: the baseline carries no executable file, so no
# imported file may be executable either.
git ls-tree -r "$BASELINE" -- component-templates/ | awk '$1 != "100644"'
find pads-paper -type f -perm -111

# 6) Result Database stays README-only.
[ "$(cd pads-paper/result-database && find . -type f)" = "README.md" ]
```
