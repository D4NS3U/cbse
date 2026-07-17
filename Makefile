SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
GOCACHE ?= $(ROOT)/.cache/go-build
export GOCACHE
KUBECTL_VERSION ?= v1.32.5
KUBECTL ?= $(ROOT)/bin/kubectl-$(KUBECTL_VERSION)
RUN_ID ?=
CBSE_REGISTRY ?= docker.io/d4ns3u/cbse-testing
TEST_IMAGE_VERSION ?= 26.7.16
CBSE_IMAGE_COMPONENTS ?= exop,sm,eds-mock,trans-mock

.PHONY: help test-fast test-smoke publish-test-images test-diagnose test-clean test-tools verify-generated

help:
	@echo "CBSE test commands:"
	@echo "  make test-fast"
	@echo "  make publish-test-images TEST_IMAGE_VERSION=$(TEST_IMAGE_VERSION)"
	@echo "  make test-smoke KUBECONFIG=/path/to/config CBSE_REGISTRY=$(CBSE_REGISTRY)"
	@echo "  make test-diagnose RUN_ID=<run-id> KUBECONFIG=/path/to/config"
	@echo "  make test-clean RUN_ID=<run-id> KUBECONFIG=/path/to/config"

test-fast: verify-generated
	./hack/test/test-harness.sh
	@unformatted="$$(gofmt -l $$(find experiment-operator scenario-manager test/e2e -name '*.go' -type f))"; \
	if [[ -n "$${unformatted}" ]]; then echo "Unformatted Go files:"; echo "$${unformatted}"; exit 1; fi
	cd experiment-operator && go vet ./...
	cd scenario-manager && go vet ./...
	cd scenario-manager && go test -tags=integration -run '^$$' ./...
	cd experiment-operator && go test -tags=e2e -run '^$$' ./test/e2e
	cd test/e2e && go vet -tags=e2e ./...
	cd test/e2e && go test -tags=e2e -run '^$$' ./...
	cd scenario-manager && go test -race ./...
	$(MAKE) -C experiment-operator setup-envtest
	cd experiment-operator && \
	KUBEBUILDER_ASSETS="$$(./bin/setup-envtest use $$(go list -m -f '{{.Version}}' k8s.io/api | awk -F'[v.]' '{printf "1.%d", $$3}') --bin-dir "$(ROOT)/experiment-operator/bin" -p path)" \
	go test ./...

verify-generated:
	$(MAKE) -C experiment-operator controller-gen
	CONTROLLER_GEN="$(ROOT)/experiment-operator/bin/controller-gen" ./hack/test/verify-generated.sh

test-tools: $(KUBECTL)

$(KUBECTL):
	KUBECTL_VERSION=$(KUBECTL_VERSION) OUTPUT=$@ ./hack/test/install-kubectl.sh

publish-test-images:
	CBSE_REGISTRY="$(CBSE_REGISTRY)" TEST_IMAGE_VERSION="$(TEST_IMAGE_VERSION)" \
	  CBSE_IMAGE_COMPONENTS="$(CBSE_IMAGE_COMPONENTS)" \
	  CBSE_REGISTRY_AUTH_FILE="$(CBSE_REGISTRY_AUTH_FILE)" \
	  ./hack/test/build-images.sh

test-smoke: test-tools
	KUBECTL="$(KUBECTL)" CBSE_REGISTRY="$(CBSE_REGISTRY)" \
	  TEST_IMAGE_VERSION="$(TEST_IMAGE_VERSION)" ./hack/test/smoke.sh

test-diagnose: test-tools
	@test -n "$(RUN_ID)" || { echo "RUN_ID is required" >&2; exit 2; }
	KUBECTL="$(KUBECTL)" RUN_ID="$(RUN_ID)" ./hack/test/diagnose.sh

test-clean: test-tools
	@test -n "$(RUN_ID)" || { echo "RUN_ID is required" >&2; exit 2; }
	KUBECTL="$(KUBECTL)" RUN_ID="$(RUN_ID)" ./hack/test/clean.sh
