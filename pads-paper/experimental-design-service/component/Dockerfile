# syntax=docker/dockerfile:1.7
# Reference ExperimentalDesignService Go image for CBSE alpha4.
#
# This image is a repository-built static Go runtime. The Dockerfile requires
# the locked EDS_GO_BUILDER_IMAGE (Go toolchain, linux/amd64 manifest digest)
# as its only build stage. A missing argument fails the build. It compiles
# ./cmd/experimental-design-service into a static, stripped, non-root binary
# and copies only that binary into a scratch final stage.
#
# Runtime configuration (NATS_URL, the per-project availability subject, the
# downward-API identity, the listen address, and the Detail DB connection
# Secret) is injected by the Operator at run time. None of it is copied into
# this image. The service never receives Kubernetes credentials; its identity
# comes exclusively from the downward-API environment. The image contains no
# database driver binaries beyond the linked Go driver (pure Go pgx), no
# Python, and no secrets.
#
# Unlike the Translator image, no CA certificate bundle is carried: the EDS
# speaks plain NATS (nats://) and PostgreSQL with sslmode=disable, and
# performs no HTTPS verification of its own.

ARG EDS_GO_BUILDER_IMAGE
FROM ${EDS_GO_BUILDER_IMAGE} AS builder

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
    -o /experimental-design-service \
    ./cmd/experimental-design-service

FROM scratch

# The static binary is the only content of the final image.
COPY --from=builder /experimental-design-service /experimental-design-service

# The Operator's restricted security context requires a non-root runtime;
# the static binary runs unprivileged as UID/GID 1000.
USER 1000:1000

ENTRYPOINT ["/experimental-design-service"]
