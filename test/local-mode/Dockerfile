# syntax=docker/dockerfile:1.6
#
# Local-mode dogfood image.
#
# Builds a static loom binary, then runs a small deterministic backend so the
# local supervised-agent product loop can be tested without external AI auth.

ARG GO_VERSION=1.26.0
ARG INSTALL_CODEX=false
ARG CODEX_CLI_VERSION=latest
ARG INSTALL_CLAUDE=false
ARG CLAUDE_CLI_VERSION=latest

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
ARG CODEX_CLI_VERSION=latest
ARG INSTALL_CLAUDE=false
ARG CLAUDE_CLI_VERSION=latest
# Node 24 (via NodeSource) is installed when any Node-based backend CLI is
# requested. The bundled TS leaf imports node:sqlite (flue durability), so the
# image needs Node >=22 regardless of which backend runs.
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash ca-certificates curl git jq lsof make ripgrep tmux wget \
    && if [ "$INSTALL_CODEX" = "true" ] || [ "$INSTALL_CLAUDE" = "true" ]; then \
        curl -fsSL https://deb.nodesource.com/setup_24.x | bash - \
        && apt-get install -y --no-install-recommends nodejs; \
    fi \
    && if [ "$INSTALL_CODEX" = "true" ]; then \
        npm install -g "@openai/codex@${CODEX_CLI_VERSION}"; \
    fi \
    && if [ "$INSTALL_CLAUDE" = "true" ]; then \
        npm install -g "@anthropic-ai/claude-code@${CLAUDE_CLI_VERSION}"; \
    fi \
    && rm -rf /var/lib/apt/lists/*
ENV SHELL=/bin/bash

COPY --from=builder /out/loom /usr/local/bin/loom
# Managed codex hook policy: the pre-turn `loom skill materialize` hook runs
# with managed provenance (no per-handler trust review), and unmanaged hooks —
# including .codex/hooks.json inside cloned task repos — are refused. Only
# codex reads /etc/codex, so the file is inert in non-codex image variants.
COPY test/local-mode/codex-requirements.toml /etc/codex/requirements.toml
COPY test/local-mode/local-mode-entrypoint /usr/local/bin/local-mode-entrypoint
# A TUI harness needs a terminal type and a UTF-8 locale, and this image had
# neither: TERM unset, LC_CTYPE=POSIX. Claude Code then renders nothing usable,
# and harness-wrapper — whose readiness and reply detection key off the "❯",
# "⏺" and "✻" glyphs — never sees a composer it can submit into. The run hangs
# at "Launching Claude agent (non-interactive)..." and the session records zero
# assistant turns and zero tokens, which reads as a silent no-op.
#
# The deterministic localdogfood backend is a shell script with no TUI, which is
# why this stack looked healthy until a real harness was selected.
ENV TERM=xterm-256color \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8

COPY test/local-mode/loom-backend-localdogfood /usr/local/bin/loom-backend-localdogfood
RUN chmod +x /usr/local/bin/local-mode-entrypoint /usr/local/bin/loom-backend-localdogfood \
    && ln -sf /usr/local/bin/loom-backend-localdogfood /usr/local/bin/localdogfood

WORKDIR /workspace
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/local-mode-entrypoint"]
