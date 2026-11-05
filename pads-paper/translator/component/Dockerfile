# Reference Translator Go image for CBSE alpha4.
#
# This image is a repository-built static Go runtime, separate from the Python
# runner base. The Dockerfile requires the locked TRANSLATOR_GO_BUILDER_IMAGE
# (Go toolchain, linux/amd64 manifest digest) as its only build stage. A
# missing argument fails the build. It compiles ./cmd/translator into a static,
# stripped, non-root binary and copies only that binary and the CA certificate
# bundle into a scratch final stage.
#
# Runtime configuration (NATS, experiment identity, repository, base image),
# the workspace and BuildKit socket emptyDirs, and the registry, Scenario
# Detail DB, and Result DB Secret files are injected by the Operator at run
# time. None of them are copied into this image. The image contains neither
# Python nor SimPy; model generation writes Python source into a build context
# whose generated Dockerfile inherits the separate RUNNER_BASE_IMAGE. See
# slice 05 of the simrunner-start-and-container-creation change for the contract.

# syntax=docker/dockerfile:1.7
ARG TRANSLATOR_GO_BUILDER_IMAGE
FROM ${TRANSLATOR_GO_BUILDER_IMAGE} AS builder

WORKDIR /src

# Download dependencies first to leverage Docker layer caching. The module has
# no vendored dependencies; the build stage reaches the Go module proxy.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static, stripped, reproducible binary for linux/amd64. -trimpath removes the
# build host's file paths; -ldflags '-s -w' strips the symbol table and debug
# information; -buildid= removes the Go build ID.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags='-s -w -buildid=' \
    -o /translator \
    ./cmd/translator

FROM scratch

# The static binary and the Debian CA certificate bundle are the only content
# of the final image. The bundle lets the framework resolve registry tags over
# HTTPS for digest and OCI annotation verification.
COPY --from=builder /translator /translator
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

USER 1000:1000

ENTRYPOINT ["/translator"]
