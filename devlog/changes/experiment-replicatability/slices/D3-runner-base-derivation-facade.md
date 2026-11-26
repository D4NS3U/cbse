# Slice D3 — Runner-base derivation Facade

Normative and self-contained. Read order: [../FEATURE.md](../FEATURE.md),
especially decisions D7–D9 and §§7.4–7.5; root `AGENTS.md`; then this slice.
Specifications are read-only. Stop and ask on unresolved contradiction.

This is wave W1 and may run in parallel with D1. It defines no CR or Job
contract and consumes the exact environment/encoding names already fixed by
FEATURE.md.

## Target

Sole ownership of:

- `component-templates/translator/runner-base/Dockerfile`;
- new runner-base Python module and direct unit tests under
  `component-templates/translator/runner-base/`;
- root `Makefile` only to wire those direct stdlib tests into `test-fast`;
- harness self-tests only if the Docker build-input list must be updated for the
  new base file.

No operator, Scenario Manager, EDS, generated runner, Result DB, PPS, e2e suite,
manifest, chart, product docs, or `devlog/**` changes.

## Change

### D3.1 Install the Facade in the immutable base

Add `cbse_random.py` to the runner-base build context. The Dockerfile copies it
to a fixed root-owned, world-readable directory such as `/opt/cbse/lib/` and
sets a fixed `PYTHONPATH` so a generated non-root runner can:

```python
import cbse_random
```

The build must import the module and execute at least one golden-vector check
before committing the layer. Do not install a package from the network and do
not add an unpinned dependency.

The base remains free of model code, scenario data, database configuration, and
credentials.

### D3.2 Public API

Implement exactly:

```python
DERIVATION_ID = "cbse-hmac-sha256-u63-v1"

@dataclass(frozen=True)
class StreamDescriptor:
    root_seed: int
    scenario_seed: int
    replication_index: int
    effective_seed: int
    seed_derivation: str


def derive_stream(scenario_seed, environ=None) -> StreamDescriptor:
    ...
```

A dedicated `StreamContractError(ValueError)` may be public for callers/tests.
No public algorithm selector, version argument, hostname argument, Job-name
argument, RNG constructor, or mutable global state.

### D3.3 Strict parsing

`derive_stream` reads exactly:

- `CBSE_ROOT_SEED`;
- `CBSE_REPLICATION_OFFSET`;
- `CBSE_COMPLETION_INDEX`.

When `environ is None`, read `os.environ`; otherwise use only the supplied
mapping. Values must be canonical unsigned decimal strings (`0` or a non-zero
digit followed by digits) and within `0..9223372036854775807`. Reject missing,
empty, sign-prefixed, whitespace-padded, fractional, hexadecimal, leading-zero,
boolean, and out-of-range values.

The explicit `scenario_seed` argument must be a Python `int` but not `bool`, in
the same signed-63-bit range. Calculate offset + completion with explicit
MaxInt64 overflow rejection.

Errors name the field and condition but never dump the entire environment.
There is no fallback.

### D3.4 Exact derivation

Implement FEATURE.md §7.5 byte-for-byte using only Python standard-library
`hmac`, `hashlib`, and integer byte conversion/`struct`. The effective seed is
always `0..MaxInt64`.

Unit tests include all six normative golden vectors and independent assertions
for key/message byte layout. Do not generate expected values using the
production function in the same assertion; expected digest/effective values are
literal fixtures from FEATURE.md.

### D3.5 Test-fast integration

Add a deterministic Makefile step that runs the direct runner-base unit tests
with repository Python, without requiring SimPy, Psycopg, Docker, network, or a
cluster. Existing Go/Python test ordering may change only enough to include this
step. A failure must fail `make test-fast`.

## Constraints

- One algorithm per base-image digest.
- `DERIVATION_ID` is provenance, not configuration.
- Do not copy the module into generated images from Translator source; it must
  come from the base layer.
- Do not use Python's built-in `hash()`, random module, hostname, time, UUID,
  process ID, or filesystem identity in derivation.
- Do not log environment mappings.
- Preserve numeric non-root runtime user and digest-pinned base source.
- Dockerfile/image change requires `make test-fast` and manifest smoke with a
  newly built/published runner-base image; `SKIP_BUILD=1` with an old base is
  not acceptance.
- Commit/attestation/hygiene constraints apply.

## Ownership

D3 owns only the portable derivation Facade. D2 injects values. D4 imports the
Facade and chooses `random.Random`. If D4 requires an API different from this
slice, that is a normative-spec question, not a D3 extension.

## Observable acceptance

1. Runtime attestation is `ai.forge/qwen3.8-27b-nvfp4`.
2. Diff is limited to Target paths.
3. Direct runner-base unit suite passes all six literal golden vectors.
4. Negative matrix covers every malformed category, missing fields, scenario
   type/range, and addition overflow.
5. Repeated and parallel calls with separate mappings are deterministic and do
   not share mutable state.
6. `rg` confirms no hostname/time/random/UUID derivation input and no
   `derivationVersion` selector.
7. Runner-base Docker build succeeds from the pinned source image and its build
   import/golden check passes.
8. Non-root container import check succeeds for `cbse_random`.
9. `make test-fast` succeeds and demonstrably executes the new direct tests.
10. Manifest smoke with newly published immutable runner-base digest passes
    15/15 and cleans up; record run ID and image digest.
11. Added-line private/secret scan is empty; commit HEAD/trailers reported.

Completion requires one `worker_done` with evidence 1–11, files, report, smoke
ID, and immutable runner-base digest. Mandatory smoke unavailable means
`verification-blocked`.
