# Multi-stage Dockerfile for loomcli E2E tests.
# Supports: smoke tests, Go unit/e2e tests, Playwright browser tests.
# Alpine-based runtime to minimize image size.
#
# Build:  podman build -f e2e/Dockerfile -t loomcli-e2e .
# Run:    podman run loomcli-e2e                              # all phases
#         podman run loomcli-e2e run_test.sh --phase smoke     # smoke only
#         podman run loomcli-e2e run_test.sh --phase playwright # playwright only

ARG GO_VERSION=1.25.6

# ─── Stage 1: Build Go binaries ─────────────────────────────────────────────
FROM golang:${GO_VERSION}-bookworm AS builder

WORKDIR /src
COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download \
    && CGO_ENABLED=0 go build -o /out/loom ./cmd/loom

# ─── Stage 2: Build frontend ────────────────────────────────────────────────
FROM node:20-alpine AS frontend

WORKDIR /app/internal/webui/frontend
COPY internal/webui/frontend/ .
RUN --mount=type=cache,target=/app/internal/webui/frontend/node_modules \
    --mount=type=cache,target=/root/.npm \
    npm ci && npm run build

# ─── Stage 3: Runtime ───────────────────────────────────────────────────────
FROM node:20-alpine AS runtime

# System packages
RUN apk add --no-cache \
    tmux git jq curl ca-certificates bash coreutils make chromium \
    nss freetype harfbuzz ttf-freefont

# Playwright: use system chromium instead of downloading
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 \
    PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/usr/bin/chromium-browser

# agent-browser CLI
RUN --mount=type=cache,target=/root/.npm \
    npm install -g agent-browser @playwright/test

# Copy compiled binaries
COPY --from=builder /out/loom /usr/local/bin/loom

# Source tree (for go test if Go is mounted)
WORKDIR /src
COPY . .

# Overlay frontend build + install Playwright test deps
COPY --from=frontend /app/internal/webui/frontend/dist/ ./internal/webui/frontend/dist/
RUN --mount=type=cache,target=/root/.npm \
    cd internal/webui/frontend && npm install --no-save @playwright/test 2>/dev/null \
    && rm -rf /tmp/*

# Install stubs, smoke tests, and orchestrator
COPY e2e/stubs/claude e2e/stubs/codex e2e/stubs/opencode /usr/local/bin/
COPY e2e/verify_todo.sh e2e/verify_list.sh /usr/local/bin/
COPY e2e/run_test.sh /usr/local/bin/run_test.sh
COPY e2e/epic_runner_codex.sh /usr/local/bin/epic_runner_codex.sh
RUN mkdir -p /etc/loom \
    && chmod +x /usr/local/bin/claude /usr/local/bin/codex /usr/local/bin/opencode \
        /usr/local/bin/verify_todo.sh /usr/local/bin/verify_list.sh \
        /usr/local/bin/run_test.sh /usr/local/bin/epic_runner_codex.sh
COPY e2e/task_template.txt /etc/loom/task_template.txt
COPY scripts/start-e2e-server.sh /usr/local/bin/start-e2e-server.sh
RUN chmod +x /usr/local/bin/start-e2e-server.sh

WORKDIR /src

ENV LOOM_BACKEND=claude \
    TERM=xterm-256color

CMD ["run_test.sh"]
