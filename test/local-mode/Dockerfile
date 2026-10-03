# syntax=docker/dockerfile:1.6
#
# Local-mode dogfood image.
#
# Builds a static loom binary, then runs a small deterministic backend so the
# local supervised-agent product loop can be tested without external AI auth.
#
# Targets: `local-mode` is the default image (make local-mode-up and the
# codex/claude/daytona variants); `agents` is the Agent API image (make
# local-mode-agents-up). `agents` builds FROM the `local-mode` stage in the
# same build, never from an image tagged by an earlier step, so an image prune
# between two make steps cannot remove its base.

ARG GO_VERSION=1.27.1
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

FROM debian:bookworm-slim AS local-mode
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

# Agent API variant (target `agents`, make local-mode-agents-up).
#
# Adds, on top of the local-mode stage, the pinned OpenCode build (R22: 2.0.19,
# sst/opencode v2 at b30c4d0, built as in aft.yml) and the 2.0d scripted fake
# model, so a Lead can run without real provider auth. The workspace's source
# repo is the 2.0f Slack-clone fixture. It also carries the R21 pinned codex
# and Claude Code CLIs for real mode (2.0q2); their auth is only ever a
# read-only runtime mount, never part of the image.

FROM docker.io/oven/bun:1.4.2 AS opencode
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
# One RUN that deletes the multi-GB source tree, so the layer holds only the binary.
RUN git init -q && git remote add origin https://github.com/sst/opencode \
    && git fetch -q --depth 1 origin b30c4d00d15ed20d15a19e5c07534514bc2ee0fe \
    && git checkout -q FETCH_HEAD \
    && bun install --frozen-lockfile \
    && cd packages/cli \
    && OPENCODE_CHANNEL=latest OPENCODE_VERSION=2.0.19 bun run script/build.ts --single --skip-install \
    && cp dist/cli-linux-*/bin/opencode /opencode \
    && /opencode --version | grep -F 2.0.19 \
    && cd / && rm -rf /src /root/.bun/install/cache

FROM docker.io/library/node:24-bookworm-slim AS harnesses
RUN npm install -g --prefix /opt/harnesses @openai/codex@0.157.1 @anthropic-ai/claude-code@2.1.285 \
    && /opt/harnesses/bin/codex --version | grep -F 0.157.1 \
    && /opt/harnesses/bin/claude --version | grep -F 2.1.285 \
    && rm -rf /root/.npm

FROM local-mode AS agents
COPY --from=docker.io/library/node:24-bookworm-slim /usr/local/bin/node /usr/local/bin/node
COPY --from=harnesses /opt/harnesses /opt/harnesses
# claude is a wrapper: in real mode a `claude setup-token` token, mounted
# read-only at /run/secrets/claude-token, reaches only the claude process.
RUN ln -s /opt/harnesses/bin/codex /usr/local/bin/codex \
    && printf '%s\n' '#!/bin/sh' \
       '[ -s /run/secrets/claude-token ] && CLAUDE_CODE_OAUTH_TOKEN=$(cat /run/secrets/claude-token) && export CLAUDE_CODE_OAUTH_TOKEN' \
       'exec /opt/harnesses/bin/claude "$@"' > /usr/local/bin/claude \
    && chmod +x /usr/local/bin/claude
COPY --from=opencode /opencode /usr/local/bin/opencode
COPY tests/aft/fixtures/fake-model/server.mjs /opt/fake-model/server.mjs
COPY scripts/seed-slack-clone.sh /opt/fixtures/scripts/seed-slack-clone.sh
COPY tests/fixtures/slack-clone /opt/fixtures/tests/fixtures/slack-clone
COPY test/local-mode/local-mode-agents-entrypoint /usr/local/bin/local-mode-agents-entrypoint
ENV LOOM_OPENCODE_BIN=/usr/local/bin/opencode \
    OPENCODE_DISABLE_MODELS_FETCH=1 \
    FAKE_MODEL_PORT=4010
ENTRYPOINT ["/usr/local/bin/local-mode-agents-entrypoint"]

# Default target: the plain local-mode image. A build without --target stops
# here and skips the agents stages.
FROM local-mode
