# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.24

FROM golang:${GO_VERSION} AS builder

WORKDIR /workspace

# Build from monorepo root because scenario-manager/go.mod depends on the local
# experiment-operator module via replace directive.
COPY go.work go.work.sum ./
COPY experiment-operator ./experiment-operator
COPY scenario-manager ./scenario-manager

WORKDIR /workspace/scenario-manager

RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/scenario-manager ./cmd

FROM alpine:3.21

RUN addgroup -S app && adduser -S -G app app

COPY --from=builder /out/scenario-manager /usr/local/bin/scenario-manager

USER app

ENTRYPOINT ["/usr/local/bin/scenario-manager"]
