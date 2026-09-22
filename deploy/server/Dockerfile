# Production Dockerfile for the loom API server.
#
# Pure Go — no Node, no frontend dist. Proves the Go binary is
# self-contained post-Phase 5. Build context is the repo root.
#
# Build: docker build -f deploy/server/Dockerfile -t loom-server .
# Run:   docker run --rm -p 8080:8080 loom-server

ARG GO_VERSION=1.26.0

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

# ─── Stage 2: Runtime ────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=builder /out/loom /usr/local/bin/loom

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/loom"]
# Frontend CORS origins are supplied via the LOOM_FRONTEND_URL environment
# variable (comma-separated). The compose file sets a default; operators
# deploying cross-origin should override it at `docker run` / kube-env time.
CMD ["serve", "--port", "8080"]
