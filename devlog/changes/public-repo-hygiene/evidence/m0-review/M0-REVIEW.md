# M0 — External-reviewer fresh-clone dry-run report

- **Reviewer role:** independent external reviewer, no prior context; executed the acceptance checklist exactly as written.
- **Clone:** `git clone https://github.com/D4NS3U/cbse.git` into `/tmp/cbse-m0-review/cbse` (fresh; pre-existing scratch dir removed first).
- **Clone head:** `3b5dc63` ("docs: re-anchor surviving public links after the D2 relocation"), branch `main`.
- **Reviewer host toolchain:** Go 1.27.1 (`darwin/amd64`; workspace pins `go 1.26.3`), GNU-make at `/usr/bin/make`, python3 present (needed by nothing in the fast tier; noted for the embedded runner conformance suite, which skips gracefully per `component-templates/translator/README.md`).
- **Runtime attestation:** `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → `ai.forge/qwen3.8-27b-nvfp4` (matches the required target; review proceeded).
- **Discipline:** reviewer-only — no code edits, no commits, no pushes, no kubectl/KUBECONFIG, no docker/registry operations, no cluster contact. Only this untracked file was created in the clone.

---

## Item 1 — clone, build, test on public prerequisites (`make test-fast`, cold)

Command: `make test-fast` in the fresh clone (cold: local `GOCACHE` under the repo, module downloads and envtest control-plane binaries from public sources).

**Toolchain check (public prerequisites only):** host Go 1.27.1 auto-satisfies the `go 1.26.3` workspace pin; `make` present; no cluster, registry, or credentials touched. Everything the suite fetched was publicly obtainable: `sigs.k8s.io/controller-tools/cmd/controller-gen@v0.18.0`, `sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.21`, and the `k8s/1.33.0-darwin-amd64` envtest control-plane binaries. Python was present but not required by anything the fast tier actually ran (the translator's conformance/SQL-doc tests passed without it — `sqldoc` package `ok`).

**Run 1 (cold, first documented run):**

- **rc = 2** (verbatim: `make: *** [test-fast] Error 1`)
- **Wall time: 26m36s**
- All tiers up to the translator race tier passed: `verify-generated` (controller-gen diff clean), harness self-test (`Harness self-tests passed.`), `gofmt` check, `go vet` for operator/scenario-manager/translator, tag-compilation runs, and the full `scenario-manager` `go test -race ./...` suite (all `ok`).
- The `component-templates/translator` `go test -race ./...` tier then failed: **11 of 12 test binaries hung and were killed at the test timeout** (~660s each), while 6 fast packages passed (`buildkit` 2.3s, `databaseendpoint` 2.9s, `ipv4` 5.2s, `detaildb` 6.6s, `registry` 6.0s, `sqldoc` 3.7s, `subject` 4.6s). Verbatim tail of the run-1 log:

```
ok   	github.com/D4NS3U/cbse/component-templates/translator/internal/buildkit	2.304s
*** Test killed with quit: ran too long (11m0s).
signal: quit
FAIL	github.com/D4NS3U/cbse/component-templates/translator/internal/config	659.997s
ok   	github.com/D4NS3U/cbse/component-templates/translator/internal/databaseendpoint	2.956s
*** Test killed with quit: ran too long (11m0s).
signal: quit
FAIL	github.com/D4NS3U/cbse/component-templates/translator/internal/databaseendpoint/dns	659.998s
ok   	github.com/D4NS3U/cbse/component-templates/translator/internal/databaseendpoint/ipv4	5.184s
*** Test killed with quit: ran too long (11m0s).
signal: quit
FAIL	github.com/D4NS3U/cbse/component-templates/translator/internal/databaseendpoint/ipv6	659.998s
?    	github.com/D4NS3U/cbse/component-templates/translator/internal/dbconfig	[no test files]
ok   	github.com/D4NS3U/cbse/component-templates/translator/internal/detaildb	6.655s
*** Test killed with quit: ran too long (11m0s).
signal: quit
FAIL	github.com/D4NS3U/cbse/component-templates/translator/internal/generator	659.999s
*** Test killed with quit: ran too long (11m0s).
signal: quit
FAIL	github.com/D4NS3U/cbse/component-templates/translator/internal/imageref	659.999s
*** Test killed with quit: ran too long (11m0s).
signal: quit
FAIL	github.com/D4NS3U/cbse/component-templates/translator/internal/messaging	659.999s
ok   	github.com/D4NS3U/cbse/component-templates/translator/internal/registry	5.972s
*** Test killed with quit: ran too long (11m0s).
signal: quit
FAIL	github.com/D4NS3U/cbse/component-templates/translator/internal/registryauth	659.999s
ok   	github.com/D4NS3U/cbse/component-templates/translator/internal/sqldoc	3.722s
ok   	github.com/D4NS3U/cbse/component-templates/translator/internal/subject	4.563s
*** Test killed with quit: ran too long (11m0s).
signal: quit
FAIL	github.com/D4NS3U/cbse/component-templates/translator/internal/translator	660.000s
*** Test killed with quit: ran too long (11m0s).
signal: quit
FAIL	github.com/D4NS3U/cbse/component-templates/translator/internal/wire	660.000s
*** Test killed with quit: ran too long (11m0s).
signal: quit
FAIL	github.com/D4NS3U/cbse/component-templates/translator/internal/workspace	660.001s
FAIL
make: *** [test-fast] Error 1
```

Because `make` runs the tiers sequentially, the final envtest-based operator tier (the suite's last step) **never ran** in run 1.

**Diagnosis (reviewer observations, no fixes applied):**

- The hung binaries were observed in `S` state with near-zero cumulative CPU time — blocked, not computing — during the cold, fully-parallel `-race` fan-out on this host (16 cores, macOS darwin/amd64).
- Each failing package passes in isolation: e.g. `go test -race -v -timeout 60s ./internal/imageref` → all tests PASS in 1.5s.
- A warm-cache full run of the same tier passes: `GOWORK=off go test -race -timeout 240s ./...` in `component-templates/translator` → **rc=0, all packages ok** (`workspace` 2.3s, `translator` 12.4s, …).
- **Run 2 (full `make test-fast`, warm):** **rc = 0**, wall time **3m27s**, **60 `ok` lines, 0 `FAIL`**, including the envtest operator suite: `ok github.com/D4NS3U/cbse/experiment-operator/internal/controller/alpha4 14.779s`, `ok .../internal/controller 1.706s`, `ok .../api/alpha4 14.613s`, `ok .../internal/dbendpoint 4.459s`, `ok .../internal/jobtemplate 5.207s`. Final `ok`-lines tail (run 2):

```
ok   	github.com/D4NS3U/cbse/experiment-operator/api/alpha4	14.613s
?    	github.com/D4NS3U/cbse/experiment-operator/cmd	[no test files]
ok   	github.com/D4NS3U/cbse/experiment-operator/internal/controller	1.706s
ok   	github.com/D4NS3U/cbse/experiment-operator/internal/controller/alpha4	14.779s
ok   	github.com/D4NS3U/cbse/experiment-operator/internal/dbendpoint	4.459s
ok   	github.com/D4NS3U/cbse/experiment-operator/internal/jobtemplate	5.207s
?    	github.com/D4NS3U/cbse/experiment-operator/test/utils	[no test files]
```

**Classification:** the cold-run failure is consistent with a host-environment stall (cold-cache/parallel-binary-launch contention on this reviewer host), not a deterministic suite defect and not a missing host tool — the identical contract passes end-to-end once warm, every package passes in isolation, and only publicly obtainable tooling was used. It is recorded verbatim because the first cold `make test-fast` on a fresh clone returned rc=2 on this host.

Logs: `/tmp/cbse-m0-review/test-fast.log` (run 1), `/tmp/cbse-m0-review/test-fast-run2.log` (run 2), `/tmp/cbse-m0-review/translator-race-repro.log` (warm `-race` repro).

---

## Item 2 — private-infrastructure grep audit

Command run over **tracked files** of the clone:

```bash
git grep -nE '192\.168\.|registry\.unibw\.de|i31bdase|cbse-k3s|/home/[^ )`]*\.kube|ai\.forge|self-hosted'
```

Result: **86 hit lines** across **13 tracked files** (full verbatim capture: `/tmp/cbse-m0-review/item2-hits.txt`). No hits for `cbse-k3s` or `self-hosted`. None of the hits is pure attribution (no LICENSE/copyright/paper content matched); every hit is therefore a finding. Classification per file (credential-like values are redacted per reviewer policy):

### 2.1 `.pi/settings.json` (1 hit) — tracked coding-agent settings

| File:line | Hit | Classification |
|---|---|---|
| `.pi/settings.json:2` | `"defaultProvider": "ai.forge",` | Internal AI-API provider name (plus `defaultModel: "qwen3.8-27b-nvfp4"` on line 3) shipped in tracked agent settings — operational infrastructure, not attribution. |

### 2.2 `docs/CLUSTER_REQUIREMENTS.md` (1 hit) — public cluster-requirements doc

| File:line | Hit | Classification |
|---|---|---|
| `docs/CLUSTER_REQUIREMENTS.md:86` | "A reachable `registry.unibw.de` Docker configuration (`CBSE_REGISTRY_AUTH_FILE`) authorizing pull/push and artifact list/read/delete in the Harbor project `i31bdase`…" | Specific private registry + Harbor project named in a public doc. Directly contradicts the README/CONTRIBUTING/Makefile/test-guide statement that `CBSE_REGISTRY` is "required, environment-provided, never defaulted in this repository" and that "no repository default exists for any of them". |

### 2.3 `experiment-operator/config/samples/experiment_alpha1_simulationexperiment_hostbased_db.yaml` (2 hits) — tracked sample CR

| File:line | Hit | Classification |
|---|---|---|
| `…hostbased_db.yaml:13` | `host: 192.168.101.248` | Private RFC-1918 IP address of a database host in a committed sample manifest. |
| `…hostbased_db.yaml:19` | `host: 192.168.101.248` | Same private IP, second occurrence in the same sample. |

### 2.4 `experiment-operator/internal/controller/alpha4/alpha4_controller_test.go` (10 hits) — test fixtures

| File:line | Hit | Classification |
|---|---|---|
| `alpha4_controller_test.go:139` | `Image: "registry.unibw.de/i31bdase/cbse-test/detaildb@sha256:" + shaA,` | Private registry/repo path baked into test fixture data. |
| `alpha4_controller_test.go:153` | `Image: "registry.unibw.de/i31bdase/cbse-test/translator@sha256:" + shaA,` | Same — private registry fixture. |
| `alpha4_controller_test.go:154` | `Repository: "registry.unibw.de/i31bdase/cbse-test-runner",` | Same — private registry fixture. |
| `alpha4_controller_test.go:155` | `BaseImage: "registry.unibw.de/i31bdase/cbse-test/base@sha256:" + shaA,` | Same. |
| `alpha4_controller_test.go:156` | `BuilderImage: "registry.unibw.de/i31bdase/cbse-test/buildkit@sha256:" + shaA,` | Same. |
| `alpha4_controller_test.go:165` | `// registry.unibw.de.` | Private registry named in a test comment. |
| `alpha4_controller_test.go:167` | `return dockerConfigSecret(map[string]string{"registry.unibw.de": "***REDACTED***"}, nil, "")` | **Credential-shaped value (user:pass test fixture) — REDACTED.** Test-fixture docker-config auth entry for the private registry. |
| `alpha4_controller_test.go:570` | `if cm.Data["REPOSITORY"] != "registry.unibw.de/i31bdase/cbse-test-runner" {` | Hard-coded assertion against the private registry path. |
| `alpha4_controller_test.go:573` | `if !strings.HasPrefix(cm.Data["BASEIMAGE"], "registry.unibw.de/") {` | Hard-coded prefix assertion against the private registry. |
| `alpha4_controller_test.go:667` | `…map[string]any{"registry.unibw.de": map[string]any{"identitytoken": "***REDACTED***"}}…` | **Token-shaped value (test fixture) — REDACTED.** |
| `alpha4_controller_test.go:674` | `{"helper only", dockerConfigSecret(nil, map[string]string{"registry.unibw.de": "desktop"}, "")},` | Registry named in a fixture label. |
| `alpha4_controller_test.go:834` | `inst.Spec.Translator.Image = "registry.unibw.de/i31bdase/cbse-test/translator@sha256:" + …` | Same — private registry fixture. |

### 2.5 `experiment-operator/internal/controller/helpers_test.go` (19 hits) — test fixtures

All 19 hits are `registry.unibw.de[...]/i31bdase/cbse-test[-runner]` strings inside unit-test fixtures/assertions (lines 30, 35, 36, 37, 38, 49, 53, 54, 67, 68, 93, 101, 102, 126, 127, 132, 138, 148, 255). Classification: private registry + Harbor project hard-coded in unit tests instead of neutral test-domain values (e.g. `registry.example`). No credential literal on these lines (the auth strings on lines 93/148 are constructed at runtime via `b64Auth("u","p")` / `b64Auth("dock","hub")` from visible fake inputs).

### 2.6 `experiment-operator/internal/controller/images.go` (1 hit) — production code comment

| File:line | Hit | Classification |
|---|---|---|
| `images.go:41` | `// registry.unibw.de/i31bdase/cbse-test-runner; a trailing :tag or @digest is a` | Private registry named in a doc comment of non-test production source. |

### 2.7 `scenario-manager/internal/registry/registry_test.go` (1 hit) — test fixture

| File:line | Hit | Classification |
|---|---|---|
| `registry_test.go:53` | `"registry.unibw.de/i31bdase/cbse-test-runner",` | Private registry fixture in unit test. |

### 2.8 `test/e2e/README.md` (10 hits) — public smoke-test README

| File:line | Hit | Classification |
|---|---|---|
| `test/e2e/README.md:17` | "…Linux agents normally use `/home/d4ns3u/.kube/config`" | Personal home-directory kubeconfig path (username-scoped) in public docs. |
| `test/e2e/README.md:19` | "Access to the K3s API server at `https://192.168.101.245:6443`." | Private cluster API-server endpoint (RFC-1918 IP) in public docs. |
| `test/e2e/README.md:21` | "`registry.unibw.de/i31bdase/cbse-test` from both the agent and the K3s node." | Private registry prerequisite stated as fact in public docs (contradicts the env-provided contract). |
| `test/e2e/README.md:42` | `KUBECONFIG=/home/d4ns3u/.kube/config \` | Personal kubeconfig path in an example command. |
| `test/e2e/README.md:64–69` | `OPERATOR_IMAGE=registry.unibw.de/i31bdase/cbse-test@sha256:...` (×5 vars) + `DETAIL_DB_IMAGE=registry.unibw.de/i31bdase/cbse-test/scenario-detail-database@sha256:...` | Six example `*_IMAGE` references hard-coded to the private registry. |
| `test/e2e/README.md:71` | `make test-smoke KUBECONFIG=/home/d4ns3u/.kube/config` | Personal kubeconfig path in an example command. |

### 2.9 `test/e2e/smoke_test.go` (1 hit) — smoke assertion

| File:line | Hit | Classification |
|---|---|---|
| `smoke_test.go:269` | `Expect(runnerImage).To(HavePrefix("registry.unibw.de/i31bdase/cbse-test-runner@sha256:"),` | Smoke-suite assertion hard-coded to the private registry instead of the run's `CBSE_REGISTRY`. |

### 2.10 `test/harness/build-images.sh` (2 hits)

| File:line | Hit | Classification |
|---|---|---|
| `build-images.sh:28` | `#   CBSE_REGISTRY (default registry.unibw.de/i31bdase/cbse-test) registry` | Comment asserting a repository-embedded default — contradicts the Makefile/README/CONTRIBUTING "no default" contract. |
| `build-images.sh:54` | `registry="${CBSE_REGISTRY:-registry.unibw.de/i31bdase/cbse-test}"` | Embedded private-registry fallback default in a tracked harness script. |

### 2.11 `test/harness/preflight.sh` (5 hits)

| File:line | Hit | Classification |
|---|---|---|
| `preflight.sh:33` | `#   CBSE_EXPECTED_APISERVER (default https://192.168.101.245:6443) required` | Private cluster endpoint as documented default. |
| `preflight.sh:38` | `#   CBSE_REGISTRY          (default registry.unibw.de/i31bdase/cbse-test).` | Private registry as documented default. |
| `preflight.sh:61` | `expected_server="${CBSE_EXPECTED_APISERVER:-https://192.168.101.245:6443}"` | Embedded private cluster endpoint as the default the harness "identifies the API server … it expects". |
| `preflight.sh:167` | `registry="${CBSE_REGISTRY:-registry.unibw.de/i31bdase/cbse-test}"` | Embedded private-registry fallback. |
| `preflight.sh:222` | `# targets only the generated-runner repository (i31bdase/cbse-test-runner) and` | Private Harbor project named in comment. |
| `preflight.sh:238` | `harbor_project="${CBSE_HARBOR_PROJECT:-i31bdase}"` | Embedded private Harbor project default. |

### 2.12 `test/harness/registry-cleanup.sh` (3 hits)

| File:line | Hit | Classification |
|---|---|---|
| `registry-cleanup.sh:18` | `# Targets ONLY the generated-runner repository (i31bdase/cbse-test-runner). It` | Private Harbor project named in comment. |
| `registry-cleanup.sh:42` | `api_base="${CBSE_HARBOR_API:-https://registry.unibw.de/api/v2.0}"` | Embedded private registry API endpoint default. |
| `registry-cleanup.sh:43` | `project="${CBSE_HARBOR_PROJECT:-i31bdase}"` | Embedded private Harbor project default. |

### 2.13 `test/harness/smoke.sh` (3 hits)

| File:line | Hit | Classification |
|---|---|---|
| `smoke.sh:32` | `#   CBSE_REGISTRY (default registry.unibw.de/i31bdase/cbse-test) image prefix.` | Comment asserting an embedded default (contradicts the public contract). |
| `smoke.sh:154` | `CBSE_REGISTRY="${CBSE_REGISTRY:-registry.unibw.de/i31bdase/cbse-test}" \` | Embedded private-registry fallback in the smoke entry point. |
| `smoke.sh:247` | `-e "s\|CBSE_RUNNER_REPOSITORY\|${CBSE_RUNNER_REPOSITORY:-registry.unibw.de/i31bdase/cbse-test-runner}\|g"` | Embedded private runner-repository fallback. |

### 2.14 `test/harness/test-harness.sh` (19 hits) — harness self-test

| File:line | Hit | Classification |
|---|---|---|
| `test-harness.sh:74` | `printf '%s' "${FAKE_SERVER:-https://192.168.101.245:6443}" ;;` | Private cluster endpoint as the default fake API server in the self-test. |
| `test-harness.sh:151` | `grep -Fq 'registry.unibw.de/i31bdase/cbse-test' "${root}/test/e2e/README.md"` | Self-test that *asserts* the private registry string is present in the public README — i.e., the scrub is codified into the hermetic tier. |
| `test-harness.sh:205, 231, 250, 257, 264` | `CBSE_REGISTRY=registry.unibw.de/i31bdase/cbse-test …` (×5) | Self-test invokes the build harness with the private registry hard-coded. |
| `test-harness.sh:212–223` | `grep -Fqx "registry.unibw.de/i31bdase/cbse-test/${image}:26.9.7" …` (×8) | Self-test assertions hard-coded to the private registry + a fixed version. |
| `test-harness.sh:236–242` | `grep -Fqx "registry.unibw.de/i31bdase/cbse-test/translator:26.9.7" …` (×5) | Same, "default" build self-test assertions. |
| `test-harness.sh:346` | `{"auths":{"registry.unibw.de":{"auth":"***REDACTED***"}}}` | **Credential-shaped base64 value in the self-test (decodes to a fake test pair) — REDACTED.** |

**Item 2 verdict:** FAIL-level findings — 86 hits, 13 files. The private registry (`registry.unibw.de`), Harbor project (`i31bdase`), two cluster IPs (`192.168.101.245` API server, `192.168.101.248` DB host), a personal kubeconfig path (`/home/d4ns3u/.kube/config`), and an internal AI provider (`ai.forge`) all appear in tracked files — documentation, production-code comments, test fixtures, and harness scripts — and two of the harness self-tests positively assert that the private registry string remains in public docs. The AGENTS.md rule "operational infrastructure … must not appear in working contracts, documentation, or tracked defaults" is therefore violated by the clone as published. Nothing was edited.

---

## Item 3 — contributor path (CONTRIBUTING.md alone)

Executed per CONTRIBUTING.md, up to (excluding) anything requiring a cluster or external credentials. CONTRIBUTING.md ("Development setup", "Getting and verifying generated artifacts", "Test contract") directs a contributor to the following entry points, all of which I executed from the fresh clone:

| # | Entry point (as documented) | Result |
|---|---|---|
| 1 | `make help` (root README Quickstart; target exists per Makefile) | **rc=0** — lists the six documented commands (`test-fast`, `publish-test-images`, `test-smoke`, `test-e2e-retained`, `test-diagnose`, `test-clean`). |
| 2 | `make test-fast` (mandatory module tier) | See Item 1: cold run rc=2 on this host (host-environment stall); warm re-run **rc=0**, all 60 `ok`. The document's own caveat ("this guidance is written to stay truthful for the next run — if the repository has moved past this document, the repository itself is the source of truth") applies; the command itself is correct and complete. |
| 3 | `make verify-generated` ("Regenerate and verify them with") | **rc=0** — controller-gen regenerated; `verify-generated.sh` diffed CRD/RBAC/DeepCopy against checked-in copies, clean. |
| 4 | `cd scenario-manager && go test ./...` (per-module loop) | **rc=0** — all packages `ok` (e.g. `internal/ready 8.916s`, `internal/selection 6.349s`, `internal/subject 7.400s`). |
| 5 | `cd experiment-operator && go test ./...` (per-module loop) | **rc=0** — all packages `ok`, including the envtest-based `internal/controller/alpha4 10.391s` with **no `KUBEBUILDER_ASSETS` set** (the tests locate the envtest binaries themselves after the `setup-envtest` step of Item 2/`test-fast` has populated `experiment-operator/bin`). |
| 6 | `cd component-templates/translator && go test ./...` (per-module loop) | **rc=0** — all packages `ok` (e.g. `internal/translator 18.222s`, `internal/generator 6.859s`, `internal/workspace 9.995s`). |

I did **not** run any cluster smoke tier (`test-smoke`/`test-e2e-retained`/`publish-test-images` were out of scope by instruction; they are environment-gated per the docs). I needed **no outside knowledge** at any point: prerequisites (Go 1.26-era toolchain, make) were satisfied by the host; every command, target, and expectation I needed was stated in CONTRIBUTING.md, the root README, the Makefile `help` output, or the testing guide. No stall, no missing instruction.

**Item 3 verdict: PASS** — the contributor path is fully self-sufficient at the module tier (the sole hiccup was the host-environment cold-run stall documented in Item 1, which is a host fact, not a documentation gap).

---

## Item 4 — expert readability (README.md, docs/, component-templates/ only)

Answers in the reviewer's own words, from the cloned documents alone (no outside knowledge).

**(a) What is CBSE?**
CBSE is a Kubernetes-native research framework for running simulation experiments. An Experiment Operator provisions an experiment's supporting services from a `SimulationExperiment` custom resource; a Scenario Manager receives scenario batches from an Experimental Design Service (EDS) over NATS/JetStream, persists them in PostgreSQL, and coordinates translation and execution; a Translator generates and builds the simulation-runner image; Kubernetes Jobs run the repetitions; results persist in PostgreSQL for post-processing. It is explicitly framed as a research prototype in early development ("Interfaces and behavior may change without notice"), with four related publications listed.

**(b) How is it installed today, and what is stated about the install path's future?**
Today the only path is **from source**: clone the public GitHub repository, then build and run it (`make test-fast` for hermetic verification; per-module `go test` loops for development). The README Quickstart states that "Installation by Helm chart is a planned future capability — a designated forward reference for Package B (release pipeline) of the repository professionalization program — and no chart exists yet." So: source-only today, Helm chart planned but absent.

**(c) What must a custom EDS / Translator / PostProcessingService fulfil to work with it?**
- **EDS:** connect to the installation's NATS server (bounded retries); announce each pending batch via the availability handshake (default subject `cbse.eds.scenarios.available`) and use the batch subject the Scenario Manager returns (do not construct it independently); publish complete scenario batches through JetStream and wait for the publish acknowledgement; make retries safe; stay alive (long-running) after publishing batches. Batches are at-least-once delivered and the schema does not deduplicate `batch_id`, so the EDS must not assume retries cannot create duplicates.
- **Translator:** long-running per-experiment consumer; read/validate `SIMULATIONPROJECTNAME`, `REPOSITORY`, `BASEIMAGE` at startup; subscribe to the exact experiment request subject with a durable consumer; treat `(id, translation_attempt)` as work identity; generate/build the runner image idempotently; publish the ready message (strict JSON shape; immutable OCI digest reference) and wait for its PubAck **before** acknowledging the request; make all work retry/restart-safe, recovering the already-pushed digest rather than rebuilding; never bake credentials into images. The reference Translator's replaceable boundary is the `generator.Generator` interface (model-specific work only; no NATS/BuildKit/push/ack calls from the replacement).
- **PostProcessingService:** **no interoperable contract exists yet** — it can be deployment-compatible (the Operator runs it as a Deployment, injects `SIMULATIONPROJECTNAME`, exposes its port), but the Scenario Manager does not call it; the documents warn not to invent an HTTP/NATS contract and to design behind a narrow internal function.
- Cross-cutting design goals (apply to all): self-contained digest-pinned images, numeric non-root user, no privilege escalation, `RuntimeDefault` seccomp, explicit configuration validation before external work, Secret boundaries, idempotent external effects, observable/testable behavior, Apache-2.0 licensing of contributed sources.
- **Gap/finding:** `docs/COMPONENT_DESIGN_GOALS.md` describes the contract as "the current `alpha3` contract" (with `experiment.cbse.terministic.de/alpha3` CR examples and a subject scheme without namespace), while README/CHANGELOG/testing-guide state that **`alpha4` is the only served/stored version** and the reference components are alpha4. The design doc does note that alpha4 will replace the alpha3 sections "when it is implemented", but as written an expert cannot tell from the doc alone whether the documented subjects/payloads are the live contract.
- **Gap/finding (layout contradiction):** `docs/CBSE_TESTING_GUIDE.md` states all six component images use the **nested** layout ("there is no flat form"), while `component-templates/translator/README.md` states the Translator and runner-base are published with the **flat** layout (`${CBSE_REGISTRY}:translator.test.${VERSION}`) and `test/e2e/README.md` likewise describes five flat + one nested. An integrator cannot reconcile the three.

**(d) What must a cluster admin enable/provide on the target cluster?**
From `docs/CLUSTER_REQUIREMENTS.md`: Kubernetes >= 1.30 (conformant; no upper minor bound); the `UserNamespacesSupport` feature gate enabled on kube-apiserver, kube-controller-manager, and kubelet (one-time cluster-admin operation; K3s enablement steps given), with kernel user namespaces available and kubelet CPU manager policy `none`; experiment namespaces permitting the rootless BuildKit sidecar's unconfined seccomp/AppArmor profiles (Pod Security `enforce=privileged`); a `cbse-registry-auth` dockerconfigjson Secret in each experiment namespace; the checked-in CRD serves/stores only alpha4 (alpha2/alpha3 must not be served — a one-time breaking upgrade the chart must document); for 1.30–1.32, the `SidecarContainers` gate confirmed on if distributions disable beta gates. Smoke-only additions: one `Ready` `linux/amd64` node and registry pull/push rights (see finding in item 2 — the doc names the specific private registry/ Harbor project here).

**Item 4 verdict:** all four questions answerable from the three document families, but with two internal contradictions (alpha3-vs-alpha4 framing in the component design doc; flat-vs-nested image layout across three documents) and one stale artifact: `experiment-operator/README.md` is an unedited kubebuilder scaffold (`// TODO(user)` placeholders, "go v1.24.0+", "Kubernetes v1.11.3+", "docker 17.03+") that contradicts the root docs — although it is outside the item's strict reading set, the README's "most useful follow-up references" points readers at it.

---

## Item 5 — surface integrity

**(a) Delete-nothing link check.**

Ran the specified loop over the checklist's file list (relative markdown links only; http/mailto/`#` skipped). Result:

```
MISSING FILE: scenario-manager/README.md
link-check-done rc=0
```

- **No `BROKEN:` lines** — every relative link in the 12 existing files resolves.
- **Finding:** `scenario-manager/README.md` does not exist in the clone, although the checklist (and the repository's own testing guide, which references "per-package documentation for the operator, Scenario Manager, and reference Translator") expects it. The root README's follow-up list does not link it, so no broken link results, but the named file is absent.

**(b) CHANGELOG seeded history.**

- `CHANGELOG.md` exists at the clone root. ✓
- Keep-a-Changelog markers present: "The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)" and explicit `Added / Changed / Removed` subsection semantics. ✓
- `## [Unreleased]` section present with populated Added/Changed/Removed entries. ✓
- Anchor for the `v0.1-jos-paper` tag present: `## [v0.1-jos-paper] - 2026-02-16`; the git tag `v0.1-jos-paper` also exists in the clone (`git tag -l` → `v0.1-jos-paper`). ✓
- Pre-tag history section present ("Early foundations", "alpha2 → alpha3 API evolution"). ✓

**(c) Settled-slice handoffs naming evidence.**

Finding: **no development-log / handoff material is present in the public clone.** A tracked-file search for `devlog|handoff|slice|program|status` matches only Go source files (`runner_status.go`, `scenario_status.go`), not process documents. The clone's commit history shows the material was deliberately relocated out of the public repository (e.g. `D2: relocate devlog history`, `D2: relocate internal project status`, `D2: relocate agent docs`, `ci: remove the private self-hosted cluster-smoke workflow (relocated; preserved copy staged for cbse-labs)`), and `component-templates/scenario-detail-database/README.md` still references internal slice IDs ("Slice 05", "Slice 07") as status markers. So the intended checklist question ("does every settled slice's handoff name its evidence?") is **unanswerable from the public repository: the handoffs are absent**; the slice references that do survive point at documents that are not in the clone. (No wider-filesystem search was performed, per instructions.)

---

## Verdict table

| Item | Verdict | One line |
|---|---|---|
| 1 — clone, build, test (public prerequisites) | **PASS-with-findings** | Hermetic contract runs on public tooling alone and fully passes warm (rc=0, 60 ok, incl. envtest operator suite), but the first cold `make test-fast` on this host returned rc=2 after 26m36s when 11 translator `-race` test binaries stalled at the ~660s test timeout; all failed packages pass in isolation and on warm re-run, classifying the cold failure as a host-environment stall. |
| 2 — private-infrastructure grep audit | **PASS-with-findings** | Executed exactly as written: 86 hits across 13 tracked files (private registry `registry.unibw.de`, Harbor project `i31bdase`, cluster IPs `192.168.101.245`/`.248`, personal kubeconfig path `/home/d4ns3u/.kube/config`, internal AI provider `ai.forge`) — none is pure attribution, so every hit is a finding, including two harness self-tests that positively assert the private registry string stays in public docs. |
| 3 — contributor path | **PASS** | `make help`, `make test-fast` (warm), `make verify-generated`, and all three per-module `go test ./...` loops executed from CONTRIBUTING.md alone; all rc=0 with no outside knowledge required; no cluster tier run. |
| 4 — expert readability | **PASS-with-findings** | All four questions (what/installed/custom-components/cluster-admin) are answerable from README+docs+component-templates, but the docs carry two contradictions (alpha3-vs-alpha4 contract framing in `COMPONENT_DESIGN_GOALS.md`; flat-vs-nested image layout across testing-guide/translator-README/e2e-README) plus a stale kubebuilder-scaffold `experiment-operator/README.md` the root README points readers at. |
| 5 — surface integrity | **PASS-with-findings** | No broken relative links in the 12 existing files and `scenario-manager/README.md` is absent from the clone; `CHANGELOG.md` is properly seeded (Keep-a-Changelog markers, `[Unreleased]`, `v0.1-jos-paper` anchor + matching git tag); development-log/slice-handoff material is **absent** from the public repository (commit history shows it was deliberately relocated out), so the "every settled slice's handoff names its evidence" question is unanswerable from the clone. |

**Overall:** the review executed completely and honestly on a fresh clone of `3b5dc63` with no modifications to the repository. The public surface is well-organized and its hermetic test contract is genuinely runnable with public tooling, but the clone as published violates its own scrub contract (Item 2) in 13 tracked files, and its documentation carries residual internal-process references (slice IDs, relocated handoffs) and two cross-document contradictions.
