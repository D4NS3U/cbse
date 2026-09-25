# CBSE Experiment Operator

The Experiment Operator is CBSE's provisioning component. It watches the
`SimulationExperiment` custom resource and materializes the Kubernetes
resources one experiment needs to run: the PostgreSQL database endpoints, the
Translator deployment, and the deterministic runner identity the Scenario
Manager's runner Jobs reference. It owns the provisioning phases
(`Pending`, `Provisioning`, `InProgress`, `Error`) and writes them to the
`status` subresource; scenario lifecycle beyond provisioning belongs to the
[Scenario Manager](../scenario-manager/README.md).

## API

The operator serves a single API version:

- group `experiment.cbse.terministic.de`, version `alpha4`
- kind `SimulationExperiment` (short name `simexp`), namespaced
- `alpha4` is the only served **and** stored version; the retired `alpha2` and
  `alpha3` versions are not served, not stored, and have no conversion webhook

The CRD is checked in under `config/crd/bases/`. `spec` fields are immutable
after creation where the CRD marks them with CEL validations (the database
specs, the translator's image/repository/baseimage/builderImage/service
settings, and `spec.runner.jobTemplate`); the intended update path is to
delete and recreate the `SimulationExperiment`. The object name must be a
lowercase DNS label of at most 63 characters, so it is safe to use as a label
value on owned resources.

Each database (`spec.detailDatabase`, `spec.resultDatabase`) accepts exactly
one form: `image` (a container image the operator deploys) or `host` (an
existing reachable database). `spec.translator` names the Translator runtime
image, the generated-runner repository, the digest-pinned runner base image,
the rootless BuildKit builder image, and the namespace-local
`cbse-registry-auth` Docker configuration Secret. `spec.runner.jobTemplate`
optionally overrides the default runner Job template; the operator validates
it, and the Scenario Manager builds the effective Job from it.

## What the reconciler provisions

`Alpha4SimulationExperimentReconciler` (the only registered reconciler)
validates the full configuration and, per `SimulationExperiment`, creates or
updates:

- **Database connection Secrets** `<name>-detaildb-sct` and
  `<name>-resultdb-sct` (always), carrying `host`, `port`, `dbname`, `user`,
  and `password`
- **Image-form database Deployments and Services** `<name>-detaildb` /
  `<name>-detaildb-svc` and `<name>-resultdb` / `<name>-resultdb-svc` (only
  when the database spec uses `image`; host-form databases get the Secret
  only), pulling with exactly the `cbse-registry-auth` pull Secret
- **Translator ConfigMap** `<name>-translator-cfg` with the `REPOSITORY` and
  `BASEIMAGE` keys
- **Translator Deployment** `<name>-translator` with two containers: the
  Translator runtime and the rootless BuildKit sidecar (per-Pod user
  namespace via `hostUsers: false`, `fsGroup: 1000`, shared `/workspace` and
  `/run/buildkit` emptyDirs, the registry-auth Secret, and the two database
  connection Secrets mounted read-only). See
  [docs/CLUSTER_REQUIREMENTS.md](../docs/CLUSTER_REQUIREMENTS.md) for the
  `UserNamespacesSupport` feature gate this requires
- **Translator Service** `<name>-translator-svc`
- **runner ServiceAccount** `simrunner-<12-char-UID-prefix>` — no permissions,
  no token automount. The Scenario Manager references it by exact name in
  runner Job pod templates and computes the same name from the same UID

The reconciler moves the experiment to `InProgress` only after both database
endpoints answer a `SELECT 1` availability probe (see
`internal/dbendpoint`) and the Translator Deployment has a ready replica.
Validation failures move the experiment to `Error` before any component is
created. The operator does not provision EDS or PostProcessingService
workloads and never creates runner Jobs; it injects the fixed `alpha4`
NATS/JetStream configuration and the downward-API identity variables
(`SIMULATIONPROJECTNAMESPACE`, `SIMULATIONPROJECTNAME`,
`SIMULATIONEXPERIMENTUID`) into the Translator container.

## Module layout

- `api/alpha4/` — the `SimulationExperiment` types for the
  `experiment.cbse.terministic.de/alpha4` group
- `cmd/` — the controller-manager entry point (metrics, health probes,
  leader election)
- `internal/controller/` — the `alpha4` reconciler, RBAC markers, image and
  registry-auth validation, and workload environment/label helpers
- `internal/controller/alpha4/` — the reconciler's envtest suite
- `internal/dbendpoint/` — database endpoint resolution and the `SELECT 1`
  availability probe contract
- `internal/jobtemplate/` — the runner Job-template policy that validates
  `spec.runner.jobTemplate`
- `config/` — the checked-in cluster configuration: `crd/`, `rbac/`,
  `manager/`, `default/`, and the `network-policy`, `prometheus`, and
  `certmanager` overlays
- `test/` — module-local Go test utilities and the e2e-compile-only harness
  package

## Development

The repository-root [`make test-fast`](../AGENTS.md) is the hermetic test
tier. It regenerates and verifies the generated artifacts, then runs `go vet`,
the race-enabled unit suites, and the operator's envtest suite (controller
tests run against a local kube-apiserver/etcd with the checked-in CRDs in
`config/crd/bases/`).

From the repository root:

```sh
make test-fast        # full hermetic tier, including this module's envtest suite
make verify-generated # regenerate CRD/RBAC/deepcopy with controller-gen and verify
```

Within the module:

```sh
cd experiment-operator
make setup-envtest    # download envtest binaries into bin/ when KUBEBUILDER_ASSETS is unset
go test ./...         # unit + envtest suite (assets from KUBEBUILDER_ASSETS or bin/k8s)
```

Changes to Go, CRD, Dockerfile, or test-harness files must pass
`make test-fast`; the cluster smoke suite (`make test-smoke`) is described in
the [testing guide](../docs/CBSE_TESTING_GUIDE.md). For the component contracts
this operator injects, see [Designing Custom CBSE Components](../docs/COMPONENT_DESIGN_GOALS.md)
and the [reference Translator](../component-templates/translator/README.md).

## License

Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
