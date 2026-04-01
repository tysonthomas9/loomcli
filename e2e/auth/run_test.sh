#!/usr/bin/env bash
# e2e/auth/run_test.sh — Full-stack auth E2E smoke test.
#
# Starts the auth service via docker-compose, builds and starts loom serve
# with --auth-url, and verifies the integration works end-to-end.
#
# Prerequisites: docker, docker compose, go
# Usage: bash e2e/auth/run_test.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
AUTH_DIR="$REPO_ROOT/services/auth"
COMPOSE_FILE="$AUTH_DIR/docker-compose.test.yml"
AUTH_PORT="${AUTH_TEST_PORT:-3099}"
LOOM_PORT="${LOOM_TEST_PORT:-8099}"
LOOM_BIN="$REPO_ROOT/tmp/loom-e2e"
LOOM_PID=""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

pass() { echo -e "${GREEN}✓ $1${NC}"; }
fail() { echo -e "${RED}✗ $1${NC}"; FAILURES=$((FAILURES + 1)); }
info() { echo -e "${YELLOW}→ $1${NC}"; }

FAILURES=0

# ── Cleanup ──
cleanup() {
    info "Cleaning up..."
    if [ -n "$LOOM_PID" ] && kill -0 "$LOOM_PID" 2>/dev/null; then
        kill "$LOOM_PID" 2>/dev/null || true
        wait "$LOOM_PID" 2>/dev/null || true
    fi
    docker compose -f "$COMPOSE_FILE" down -v 2>/dev/null || true
    rm -f "$LOOM_BIN"
}
trap cleanup EXIT INT TERM

# ── Step 1: Build auth service Docker image ──
info "Building auth service Docker image..."
docker compose -f "$COMPOSE_FILE" build --quiet

# ── Step 2: Start auth service ──
info "Starting auth service on port $AUTH_PORT..."
docker compose -f "$COMPOSE_FILE" up -d

# ── Step 3: Wait for auth service health ──
info "Waiting for auth service healthcheck..."
for i in $(seq 1 30); do
    if curl -sf "http://localhost:$AUTH_PORT/health" > /dev/null 2>&1; then
        pass "Auth service healthy"
        break
    fi
    if [ "$i" -eq 30 ]; then
        fail "Auth service failed to start within 30 seconds"
        docker compose -f "$COMPOSE_FILE" logs auth
        exit 1
    fi
    sleep 1
done

# ── Step 4: Build loom binary ──
info "Building loom binary..."
mkdir -p "$REPO_ROOT/tmp"
(cd "$REPO_ROOT" && go build -o "$LOOM_BIN" ./cmd/loom)

# ── Step 5: Start loom serve ──
info "Starting loom serve on port $LOOM_PORT with --auth-url..."
"$LOOM_BIN" serve \
    --port "$LOOM_PORT" \
    --auth-url "http://localhost:$AUTH_PORT" \
    --no-open \
    > /dev/null 2>&1 &
LOOM_PID=$!

# ── Step 6: Wait for loom server health ──
info "Waiting for loom server healthcheck..."
for i in $(seq 1 15); do
    if curl -sf "http://localhost:$LOOM_PORT/api/health" > /dev/null 2>&1; then
        pass "Loom server healthy"
        break
    fi
    if [ "$i" -eq 15 ]; then
        fail "Loom server failed to start within 15 seconds"
        exit 1
    fi
    sleep 1
done

# ── Step 7: Smoke tests ──
info "Running smoke tests..."

# 7a: /api/config returns external mode
CONFIG=$(curl -sf "http://localhost:$LOOM_PORT/api/config" 2>/dev/null || echo "")
if echo "$CONFIG" | grep -q '"mode":"external"'; then
    pass "/api/config returns mode=external"
else
    fail "/api/config did not return mode=external: $CONFIG"
fi

if echo "$CONFIG" | grep -q "\"auth_url\":\"http://localhost:$AUTH_PORT\""; then
    pass "/api/config contains correct auth_url"
else
    fail "/api/config missing auth_url: $CONFIG"
fi

# 7b: Auth service JWKS endpoint is reachable
JWKS=$(curl -sf "http://localhost:$AUTH_PORT/api/auth/jwks" 2>/dev/null || echo "")
if echo "$JWKS" | grep -q '"keys"'; then
    pass "Auth service JWKS endpoint returns keys array"
else
    fail "Auth service JWKS endpoint not returning keys: $JWKS"
fi

# 7c: Protected endpoint returns 401 without auth
STATUS=$(curl -sf -o /dev/null -w "%{http_code}" "http://localhost:$LOOM_PORT/api/workspaces/test/issues" 2>/dev/null || echo "000")
if [ "$STATUS" = "401" ]; then
    pass "Protected endpoint returns 401 without auth"
else
    fail "Protected endpoint returned $STATUS, expected 401"
fi

# 7d: Public health endpoint returns 200
STATUS=$(curl -sf -o /dev/null -w "%{http_code}" "http://localhost:$LOOM_PORT/api/health" 2>/dev/null || echo "000")
if [ "$STATUS" = "200" ]; then
    pass "Public /api/health returns 200"
else
    fail "Public /api/health returned $STATUS, expected 200"
fi

# ── Results ──
echo ""
if [ "$FAILURES" -eq 0 ]; then
    echo -e "${GREEN}All smoke tests passed!${NC}"
    exit 0
else
    echo -e "${RED}$FAILURES smoke test(s) failed${NC}"
    exit 1
fi
