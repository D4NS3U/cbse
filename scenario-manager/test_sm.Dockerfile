# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.24
FROM golang:${GO_VERSION} AS runtime

ARG CBSE_REPO="https://github.com/D4NS3U/cbse.git"
ARG EXPERIMENT_OPERATOR_REF="main"

WORKDIR /workspace

# Copy the incoming build context into an intermediate location; depending on
# whether the caller pointed Docker at the monorepo root or the scenario-manager
# directory we reorganise the files so that /workspace/scenario-manager and
# /workspace/experiment-operator always exist.
COPY . ./context

RUN set -eux; \
    if [ -d context/scenario-manager ]; then \
        mv context/scenario-manager ./scenario-manager; \
        if [ -d context/experiment-operator ]; then \
            mv context/experiment-operator ./experiment-operator; \
        fi; \
        if [ -f context/go.work ]; then mv context/go.work ./; fi; \
        if [ -f context/go.work.sum ]; then mv context/go.work.sum ./; fi; \
    else \
        mv context ./scenario-manager; \
    fi; \
    rm -rf context

# If the experiment-operator sources were not provided in the build context,
# fetch them from the authoritative repository so the replace directive resolves.
RUN if [ ! -d /workspace/experiment-operator ]; then \
        git clone --filter=blob:none --depth 1 \
        --branch "${EXPERIMENT_OPERATOR_REF}" \
        "${CBSE_REPO}" /tmp/cbse && \
        mv /tmp/cbse/experiment-operator /workspace/experiment-operator && \
        rm -rf /tmp/cbse; \
    fi

WORKDIR /workspace/scenario-manager

# Pre-download modules to speed up the eventual test invocation.
RUN go mod download

# Allow callers to override env vars (DB DSN, NATS URL, kube config, etc.)
# when running the container inside Kubernetes.
ENV CGO_ENABLED=0

ENTRYPOINT ["go", "test", "-v", "-count=1", "./..."]
