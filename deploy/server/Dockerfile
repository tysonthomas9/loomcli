# Production Dockerfile for the loom API server.
#
# Pure Go — no Node, no frontend dist. Proves the Go binary is
# self-contained post-Phase 5. Build context is the repo root.
#
# Build: docker build -f deploy/server/Dockerfile -t loom-server .
# Run:   docker run --rm -p 8080:8080 loom-server
#
# ─── Two runtime targets ─────────────────────────────────────────────────────
#
#   runtime           (DEFAULT) distroless/static, nonroot. The production
#                     reference image: minimal attack surface, no shell, no
#                     package manager. API-only — see the caveat below.
#   runtime-terminal  debian:bookworm-slim + bash/git/procps/wget, nonroot.
#                     Used by docker-compose.dev.yml to smoke-test the full
#                     stack including the Web UI terminal.
#
# Why two: the terminal feature spawns a PTY running $SHELL, falling back to
# /bin/bash (internal/webui/terminal/pty_manager.go), and container health
# probes of the CMD-SHELL form need a shell too. distroless has neither, so on
# the `runtime` image the terminal cannot start and an in-container healthcheck
# cannot run. Rather than downgrade the production image's hardening to serve a
# dev smoke test, the shell-bearing variant is a separate, explicitly selected
# target. The tradeoff is size and attack surface: `runtime-terminal` carries a
# full Debian userland (~30x the base layer) and a package manager, which is
# exactly what you do not want facing a network.
#
# `runtime` is deliberately the LAST stage in this file, so a bare
# `docker build -f deploy/server/Dockerfile .` (as deploy/README.md Shape 2
# instructs) still produces the hardened image. Select the other explicitly:
# `docker build --target runtime-terminal …`.

ARG GO_VERSION=1.25.6

# ─── Stage 1: Build ──────────────────────────────────────────────────────────
FROM golang:${GO_VERSION}-bookworm AS builder

WORKDIR /src
# Copy the full source once; this image favors a reliable repo-root build over
# a split dependency-cache stage.
COPY . .

# NOTE: `.git/` is excluded from the build context via .dockerignore, so
# the `git rev-parse` fallback always resolves to "docker". This is
# intentional — release builds inject the real commit via goreleaser,
# not this Dockerfile. If you need accurate build provenance for a local
# docker build, pass --build-arg BUILD_COMMIT=$(git rev-parse --short HEAD)
# and wire it into the ldflags line below.
RUN set -eux; \
    export GOMODCACHE=/tmp/go-mod-cache GOCACHE=/tmp/go-build-cache; \
    go mod download; \
    CGO_ENABLED=0 go build \
        -ldflags="-s -w -X github.com/tysonthomas9/loomcli/internal/cli.Build=docker" \
        -o /out/loom ./cmd/loom; \
    rm -rf "$GOMODCACHE" "$GOCACHE" /root/.cache/go-build /go/pkg/mod

# ─── Stage 2: Runtime (terminal-capable) ─────────────────────────────────────
#
# Not the default — see the header. Selected by docker-compose.dev.yml via
# `build.target: runtime-terminal`.
FROM debian:bookworm-slim AS runtime-terminal

# bash: the PTY's shell (pty_manager falls back to /bin/bash, not /bin/sh).
# git:  the workspace seed creates a fixture repo and git worktrees.
# procps: `pgrep`, used by scripts/compose-smoke.sh to assert a live PTY child.
# wget: the container healthcheck.
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash ca-certificates git procps wget \
    && rm -rf /var/lib/apt/lists/*

ENV SHELL=/bin/bash

# uid/gid 65532 matches distroless' `nonroot`, so the two targets write the
# same ownership onto a shared volume.
#
# HOME and LOOM_CONFIG_DIR both point at /var/lib/loom so a single mounted
# volume holds everything that must survive a container recreate: the terminal
# snapshot (terminal-state/snapshot.json), the per-machine state cache
# (state.json), and the workspace checkouts (workspaces/<NAME>/…).
#
# The directory MUST exist in the image before USER: Docker seeds a fresh named
# volume from the image's contents at that path, ownership included. If the
# path is absent the volume is created root-owned and the nonroot process gets
# EACCES writing the snapshot — the server still starts, so the symptom is
# tabs silently vanishing rather than an obvious failure.
RUN groupadd -g 65532 nonroot \
    && useradd -u 65532 -g 65532 -M -d /var/lib/loom -s /bin/bash nonroot \
    && install -d -m 0755 -o 65532 -g 65532 /var/lib/loom
ENV HOME=/var/lib/loom

COPY --from=builder /out/loom /usr/local/bin/loom

EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/loom"]
CMD ["serve", "--port", "8080", "--bind", "0.0.0.0"]

# ─── Stage 3: Runtime (default, hardened) ────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=builder /out/loom /usr/local/bin/loom

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/loom"]
# Frontend CORS origins are supplied via the LOOM_FRONTEND_URL environment
# variable (comma-separated). The compose file sets a default; operators
# deploying cross-origin should override it at `docker run` / kube-env time.
#
# --bind 0.0.0.0 is required in a container: the default 127.0.0.1 binds the
# container's own loopback, so a published port answers nothing. The flag beats
# LOOM_BIND_ADDR (flag > env > default) and is immune to a `docker run` that
# overrides the environment.
CMD ["serve", "--port", "8080", "--bind", "0.0.0.0"]
