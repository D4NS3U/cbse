# Reference PostProcessingService Go image for CBSE alpha4.
#
# This image is a repository-built static Go runtime. The Dockerfile requires
# the locked PPS_GO_BUILDER_IMAGE (Go toolchain, linux/amd64 manifest digest)
# as its only build stage. A missing argument fails the build. It compiles
# ./cmd/post-processing-service into a static, stripped, non-root binary and
# copies only that binary into a scratch final stage.
#
# Runtime configuration (NATS, experiment identity, stream, subject
# templates, consumer name, evaluation policy and loop-bound args) and the
# Result DB connection Secret file are injected by the Operator at run time.
# None of them are copied into this image. The service never receives
# Kubernetes credentials; its identity comes exclusively from the
# downward-API environment. The image contains no database driver binaries
# beyond the linked Go driver (pure Go pgx), no Python, and no secrets.
#
# Unlike the Translator image, no CA certificate bundle is carried: the PPS
# speaks plain NATS (nats://) and PostgreSQL with sslmode=disable, and
# performs no HTTPS verification of its own.

# syntax=docker/dockerfile:1.7
ARG PPS_GO_BUILDER_IMAGE
FROM ${PPS_GO_BUILDER_IMAGE} AS builder

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
    -o /post-processing-service \
    ./cmd/post-processing-service

FROM scratch

# The static binary is the only content of the final image.
COPY --from=builder /post-processing-service /post-processing-service

# The Operator's restricted security context requires a non-root runtime;
# the static binary runs unprivileged as UID/GID 1000.
USER 1000:1000

ENTRYPOINT ["/post-processing-service"]
