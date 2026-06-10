# syntax=docker/dockerfile:1.6
#
# Local-mode dogfood image.
#
# Builds a static loom binary, then runs a small deterministic backend so the
# local supervised-agent product loop can be tested without external AI auth.

ARG GO_VERSION=1.25.6
ARG INSTALL_CODEX=false
ARG CODEX_CLI_VERSION=0.128.0

FROM golang:${GO_VERSION}-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/loom ./cmd/loom

FROM debian:bookworm-slim
ARG INSTALL_CODEX=false
ARG CODEX_CLI_VERSION=0.128.0
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash ca-certificates git jq make ripgrep tmux wget \
    && if [ "$INSTALL_CODEX" = "true" ]; then \
        apt-get install -y --no-install-recommends nodejs npm \
        && npm install -g "@openai/codex@${CODEX_CLI_VERSION}"; \
    fi \
    && rm -rf /var/lib/apt/lists/*
ENV SHELL=/bin/bash

COPY --from=builder /out/loom /usr/local/bin/loom
COPY test/local-mode/local-mode-entrypoint /usr/local/bin/local-mode-entrypoint
COPY test/local-mode/loom-backend-localdogfood /usr/local/bin/loom-backend-localdogfood
RUN chmod +x /usr/local/bin/local-mode-entrypoint /usr/local/bin/loom-backend-localdogfood \
    && ln -sf /usr/local/bin/loom-backend-localdogfood /usr/local/bin/localdogfood

WORKDIR /workspace
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/local-mode-entrypoint"]
