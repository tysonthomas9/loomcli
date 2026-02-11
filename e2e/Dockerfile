# Multi-stage Dockerfile for loomcli E2E tests.
# Provides Go, Node.js, tmux, git, bd, and stub backend CLIs.
#
# Build:  docker build -f e2e/Dockerfile -t loomcli-e2e .
# Run:    docker run loomcli-e2e

ARG GO_VERSION=1.25.6

# ─── Stage 1: Build Go binaries ──────────────────────────────────────────────
FROM golang:${GO_VERSION}-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /out/loom ./cmd/loom && \
    go build -o /out/logrouter ./cmd/logrouter

# Build bd CLI from vendored source (if available)
RUN if [ -f third_party/beads/cmd/bd/main.go ]; then \
        go build -o /out/bd ./third_party/beads/cmd/bd; \
    fi

# ─── Stage 2: Build frontend ─────────────────────────────────────────────────
FROM node:20-bookworm-slim AS frontend

WORKDIR /app/internal/webui/frontend
COPY internal/webui/frontend/package*.json ./
RUN npm ci
COPY internal/webui/frontend/ .
RUN npm run build

# ─── Stage 3: Runtime ────────────────────────────────────────────────────────
FROM golang:${GO_VERSION}-bookworm AS runtime

# System packages
RUN apt-get update && apt-get install -y --no-install-recommends \
        tmux git jq curl ca-certificates gnupg \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js 20
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && \
    apt-get install -y --no-install-recommends nodejs && \
    rm -rf /var/lib/apt/lists/*

# Install Playwright (chromium only)
RUN npx playwright install --with-deps chromium

# Copy compiled binaries
COPY --from=builder /out/loom /usr/local/bin/loom
COPY --from=builder /out/logrouter /usr/local/bin/logrouter
# Copy bd if it was built (ignore error if not)
COPY --from=builder /out/b[d] /usr/local/bin/

# Copy full source tree (needed for go test)
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Overlay fresh frontend build
COPY --from=frontend /app/internal/webui/frontend/dist/ ./internal/webui/frontend/dist/

# Install stub backend CLIs
COPY e2e/stubs/claude /usr/local/bin/claude
COPY e2e/stubs/codex /usr/local/bin/codex
COPY e2e/stubs/opencode /usr/local/bin/opencode
RUN chmod +x /usr/local/bin/claude /usr/local/bin/codex /usr/local/bin/opencode

# Install smoke test and task template
COPY e2e/verify_todo.sh /usr/local/bin/verify_todo.sh
RUN mkdir -p /etc/loom
COPY e2e/task_template.txt /etc/loom/task_template.txt
RUN chmod +x /usr/local/bin/verify_todo.sh

# Install test orchestrator
COPY e2e/run_test.sh /usr/local/bin/run_test.sh
RUN chmod +x /usr/local/bin/run_test.sh

ENV LOOM_BACKEND=claude \
    TERM=xterm-256color

CMD ["run_test.sh"]
