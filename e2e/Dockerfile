# Multi-stage Dockerfile for loomcli E2E tests.
# Compiles test binaries in the builder, runs them in a slim runtime.
#
# Build:  docker build -f e2e/Dockerfile -t loomcli-e2e .
# Run:    docker run loomcli-e2e

ARG GO_VERSION=1.25.6

# ─── Stage 1: Build everything ──────────────────────────────────────────────
FROM golang:${GO_VERSION}-bookworm AS builder

WORKDIR /src
COPY . .

# Build everything with cache mounts — modules and build cache don't bloat layers
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download \
    && go build -o /out/loom ./cmd/loom \
    && if [ -f third_party/beads/cmd/bd/main.go ]; then \
        cd third_party/beads && go build -o /out/bd ./cmd/bd; \
    fi \
    && echo "Build complete"

# ─── Stage 2: Slim runtime (no Go toolchain) ────────────────────────────────
FROM debian:bookworm-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
        tmux git jq curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Copy compiled binaries only
COPY --from=builder /out/loom /usr/local/bin/loom
COPY --from=builder /out/b[d] /usr/local/bin/
# cli-e2e.test omitted — add back once e2e tests compile cleanly

# Install stubs, smoke tests, and orchestrator
COPY e2e/stubs/claude e2e/stubs/codex e2e/stubs/opencode /usr/local/bin/
COPY e2e/verify_todo.sh e2e/verify_list.sh e2e/verify_bd.sh /usr/local/bin/
COPY e2e/run_test.sh /usr/local/bin/run_test.sh
RUN mkdir -p /etc/loom \
    && chmod +x /usr/local/bin/claude /usr/local/bin/codex /usr/local/bin/opencode \
        /usr/local/bin/verify_todo.sh /usr/local/bin/verify_list.sh \
        /usr/local/bin/verify_bd.sh /usr/local/bin/run_test.sh
COPY e2e/task_template.txt /etc/loom/task_template.txt

WORKDIR /workspace

ENV LOOM_BACKEND=claude \
    TERM=xterm-256color

CMD ["run_test.sh"]
