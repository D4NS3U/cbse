SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
TEST_DIR := $(ROOT)/test
GOCACHE ?= $(ROOT)/.cache/go-build
export GOCACHE
KUBECTL_VERSION ?= v1.32.5
KUBECTL ?= $(ROOT)/bin/kubectl-$(KUBECTL_VERSION)
HELM_VERSION ?= v3.18.4
HELM ?= $(ROOT)/bin/helm-$(HELM_VERSION)
RUN_ID ?=
# CBSE_REGISTRY is required for the image-build and cluster targets. It is
# environment-provided and has no default in this repository; see
# docs/CBSE_TESTING_GUIDE.md. The consuming targets (publish-test-images,
# test-smoke, test-e2e-retained) fail fast when it is unset.
CBSE_REGISTRY ?=
TEST_IMAGE_VERSION ?= $(shell date -u +%-y.%-m.%-d)
CBSE_IMAGE_COMPONENTS ?= exop,sm,eds-mock,eds,translator,runner-base,scenario-detail-database,pps

.PHONY: help test-fast test-smoke test-e2e-retained publish-test-images test-diagnose test-clean test-tools helm-tools helm-lint helm-render test-helm-chart test-helm-lifecycle helm-image-values verify-generated

help:
	@echo "CBSE test commands:"
	@echo "  make test-fast"
	@echo "  make publish-test-images TEST_IMAGE_VERSION=$(TEST_IMAGE_VERSION)"
	@echo "  make test-smoke KUBECONFIG=/path/to/config CBSE_REGISTRY=<your-registry>"
	@echo "  make test-e2e-retained KUBECONFIG=/path/to/config CBSE_REGISTRY=<your-registry>"
	@echo "  make test-diagnose RUN_ID=<run-id> KUBECONFIG=/path/to/config"
	@echo "  make test-clean RUN_ID=<run-id> KUBECONFIG=/path/to/config"
	@echo "  make helm-lint"
	@echo "  make helm-render"
	@echo "  make test-helm-chart"
	@echo "  make test-helm-lifecycle KUBECONFIG=/path/to/config HELM_IMAGES_ENV=/path/to/digest-images.env CBSE_EXPECTED_CONTEXT=<context>"
	@echo "  make helm-image-values IMAGES_ENV=/path/to/digest-images.env OUTPUT=/path/to/private-values.yaml"

test-fast: verify-generated
	$(TEST_DIR)/harness/test-harness.sh
	$(MAKE) test-helm-chart
	@unformatted="$$(gofmt -l $$(find experiment-operator scenario-manager component-templates/translator component-templates/post-processing-service component-templates/experimental-design-service $(TEST_DIR)/e2e -name '*.go' -type f))"; \
	if [[ -n "$${unformatted}" ]]; then echo "Unformatted Go files:"; echo "$${unformatted}"; exit 1; fi
	cd experiment-operator && go vet ./...
	cd scenario-manager && go vet ./...
	cd component-templates/translator && go vet ./...
	cd component-templates/post-processing-service && go vet ./...
	cd component-templates/experimental-design-service && go vet ./...
	cd scenario-manager && go test -tags=integration -run '^$$' ./...
	cd experiment-operator && go test -tags=e2e -run '^$$' ./test/e2e
	cd $(TEST_DIR)/e2e && go vet -tags=e2e ./...
	cd $(TEST_DIR)/e2e && go test -tags=e2e -run '^$$' ./...
	cd scenario-manager && go test -race ./...
	cd component-templates/translator && go test -race -timeout 30m ./...
	cd component-templates/post-processing-service && go test -race -timeout 30m ./...
	cd component-templates/experimental-design-service && go test -race -timeout 30m ./...
	$(MAKE) -C experiment-operator setup-envtest
	cd experiment-operator && \
	KUBEBUILDER_ASSETS="$$(./bin/setup-envtest use $$(go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' k8s.io/api | awk -F'[v.]' '{printf "1.%d", $$3}') --bin-dir "$(ROOT)/experiment-operator/bin" -p path)" \
	go test ./...

verify-generated:
	$(MAKE) -C experiment-operator controller-gen
	CONTROLLER_GEN="$(ROOT)/experiment-operator/bin/controller-gen" $(TEST_DIR)/harness/verify-generated.sh

test-tools: $(KUBECTL)

$(KUBECTL):
	KUBECTL_VERSION=$(KUBECTL_VERSION) OUTPUT=$@ $(TEST_DIR)/harness/install-kubectl.sh

helm-tools: $(HELM)

$(HELM):
	HELM_VERSION=$(HELM_VERSION) OUTPUT=$@ $(TEST_DIR)/harness/install-helm.sh

helm-lint: helm-tools
	$(HELM) lint charts/cbse -f $(TEST_DIR)/helm/values-render.yaml

helm-render: helm-tools
	@tmp="$$(mktemp)"; \
	$(HELM) template cbse charts/cbse --namespace cbse-system --include-crds -f $(TEST_DIR)/helm/values-render.yaml >"$$tmp"; \
	echo "helm-render: $$(grep -c '^kind: ' "$$tmp") rendered Kubernetes document(s)"; \
	rm -f "$$tmp"

test-helm-chart: helm-tools
	HELM="$(HELM)" $(TEST_DIR)/harness/test-helm-chart.sh

# Helm lifecycle safety acceptance (H4): expected failed pre-install hook,
# install with copied registry Secret, credential/PVC/StatefulSet stability
# across restart and upgrade, blocked pre-delete guard with a live CR,
# successful uninstall, CRD retention, and CRs-first teardown. Requires a
# digest-pinned images.env (HELM_IMAGES_ENV) and the dedicated cluster.
test-helm-lifecycle: test-tools helm-tools
	KUBECTL="$(KUBECTL)" HELM="$(HELM)" $(TEST_DIR)/harness/helm-lifecycle.sh

# Generate the private installation-owner image-values file (H6) from a
# digest-pinned images.env plus the repository image lock. OUTPUT must be
# outside the tracked repository or under the ignored artifacts/ directory.
helm-image-values:
	@test -n "$(IMAGES_ENV)" || { echo "IMAGES_ENV is required (digest-pinned images.env)" >&2; exit 2; }
	@test -n "$(OUTPUT)" || { echo "OUTPUT is required (private values file destination)" >&2; exit 2; }
	IMAGES_ENV="$(IMAGES_ENV)" OUTPUT="$(OUTPUT)" $(TEST_DIR)/harness/generate-helm-values.sh

publish-test-images:
	@test -n "$(CBSE_REGISTRY)" || { echo "CBSE_REGISTRY is required (environment-provided; see docs/CBSE_TESTING_GUIDE.md)" >&2; exit 2; }
	CBSE_REGISTRY="$(CBSE_REGISTRY)" TEST_IMAGE_VERSION="$(TEST_IMAGE_VERSION)" \
	  CBSE_IMAGE_COMPONENTS="$(CBSE_IMAGE_COMPONENTS)" \
	  CBSE_REGISTRY_AUTH_FILE="$(CBSE_REGISTRY_AUTH_FILE)" \
	  $(TEST_DIR)/harness/build-images.sh

test-smoke: test-tools
	@test -n "$(CBSE_REGISTRY)" || { echo "CBSE_REGISTRY is required (environment-provided; see docs/CBSE_TESTING_GUIDE.md)" >&2; exit 2; }
	KUBECTL="$(KUBECTL)" CBSE_REGISTRY="$(CBSE_REGISTRY)" \
	  TEST_IMAGE_VERSION="$(TEST_IMAGE_VERSION)" $(TEST_DIR)/harness/smoke.sh

test-e2e-retained: test-tools
	@test -n "$(CBSE_REGISTRY)" || { echo "CBSE_REGISTRY is required (environment-provided; see docs/CBSE_TESTING_GUIDE.md)" >&2; exit 2; }
	CBSE_KEEP_NAMESPACE=1 CBSE_RETAIN_RESOURCES=1 CBSE_SELECTOR_ENABLED=1 \
	  KUBECTL="$(KUBECTL)" CBSE_REGISTRY="$(CBSE_REGISTRY)" \
	  TEST_IMAGE_VERSION="$(TEST_IMAGE_VERSION)" $(TEST_DIR)/harness/smoke.sh

test-diagnose: test-tools
	@test -n "$(RUN_ID)" || { echo "RUN_ID is required" >&2; exit 2; }
	KUBECTL="$(KUBECTL)" RUN_ID="$(RUN_ID)" $(TEST_DIR)/harness/diagnose.sh

test-clean: test-tools
	@test -n "$(RUN_ID)" || { echo "RUN_ID is required" >&2; exit 2; }
	KUBECTL="$(KUBECTL)" RUN_ID="$(RUN_ID)" $(TEST_DIR)/harness/clean.sh
