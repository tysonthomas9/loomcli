# E2E test preflight

> **Status:** Current · *audited 2026-08-03*

Shared setup for [e2e-cli.md](e2e-cli.md) and [e2e-ui.md](e2e-ui.md). Run this
once per testing session.

This is the **manual** preflight. It is not what `make test-e2e-*` does — those
targets are self-contained and provision their own server
(`playwright.config.ts:65-92`). See
[../testing-terminology.md](../testing-terminology.md) §Axis 3 for the
distinction.

## Required binaries

Both repos are built from source. `$LOOMCLI` is this checkout; `$FLEET_DB_REPO`
is the fleet-db sibling checkout — the Makefile resolves it as
`../fleet-db` or `../../fleet-db` and exports `FLEET_DB_BIN` when it finds a
built binary there (`Makefile:475-481`).

```bash
LOOMCLI=$(git rev-parse --show-toplevel)
FLEET_DB_REPO=${FLEET_DB_REPO:-$(cd "$LOOMCLI/.." && pwd)/fleet-db}
```

| Binary | How to build | Confirm |
|---|---|---|
| `loom` (CLI) | `(cd "$LOOMCLI" && go build -o /tmp/loom-test ./cmd/loom)` | `/tmp/loom-test --version` |
| `fleet-db` | `(cd "$FLEET_DB_REPO" && go build -o /tmp/fleet-db ./cmd/fleet-db)` | `/tmp/fleet-db --help \| head -1` |
| `redis-server` (only for cloud-mode tests) | container image `redis:7-alpine` via podman | `podman --version` |

Use a Go toolchain matching `go.mod` (currently `go 1.25.6`, `go.mod:3`); CI
resolves it via `go-version-file` (`.github/workflows/ci.yml:27`).

## Cloud-mode infra (Phases A, B, D)

```bash
# Start a dedicated Redis on host port 6399 (regression stack uses :6379).
# Use the default :6379 inside the container; map host :6399 → container :6379.
podman run -d --rm --name loom-test-redis -p 6399:6379 redis:7-alpine

# Start fleet-db pointed at it on a free port. --rpc-socket points at
# a user-writable path (the default /var/run/fleet-db.sock requires
# root). RPC isn't used by loom but fleet-db rejects an empty value.
nohup env \
  FLEET_REDIS_ADDR=localhost:6399 \
  FLEET_SERVER_ADDR=127.0.0.1:18095 \
  /tmp/fleet-db \
    --redis-durability-profile=managed \
    --auth-dev-mode \
    --authz-enabled=false \
    --rpc-socket=/tmp/loom-fleet-db.sock \
  > /tmp/fleet-db.log 2>&1 &
echo $! > /tmp/loom-fdb.pid
disown

# Wait for healthy
until curl -sf http://127.0.0.1:18095/healthz; do sleep 0.1; done; echo " ready"
```

> **`--rpc-socket=/tmp/loom-fleet-db.sock`:** redirects fleet-db's Unix-socket RPC listener to a user-writable path. The default (`/var/run/fleet-db.sock`) requires root and emits a noisy bind-permission ERROR. An empty value is rejected by fleet-db.

> **Why `-p 6399:6379`:** the regression stack uses host `:6379`. Map an alternative host port to the container's standard `:6379`.

## Loom env (cloud mode)

```bash
export LOOM_FLEET_DB_URL=http://127.0.0.1:18095
export LOOM_FLEET_DB_ACTOR=tester
export LOOM_CONFIG_DIR=/tmp/loom-e2e
rm -rf "$LOOM_CONFIG_DIR" && mkdir -p "$LOOM_CONFIG_DIR"
LOOM=/tmp/loom-test
```

## Loom env (embedded mode, Phase C)

```bash
unset LOOM_FLEET_DB_URL
export FLEET_DB_BIN=/tmp/fleet-db   # required: bootstrap.DiscoverFleetDBBinary checks this first
export LOOM_CONFIG_DIR=/tmp/loom-e2e-embedded
rm -rf "$LOOM_CONFIG_DIR" && mkdir -p "$LOOM_CONFIG_DIR"
LOOM=/tmp/loom-test
```

## Test-runner conventions

This section is canonical for the manual E2E plans; [known-issues.md](known-issues.md)
links here rather than repeating it.

1. **Capture stdout and stderr separately** — `level=` log lines on stderr can leak into combined output and cause grep matches to silently pass on error paths. Note that a blanket `grep -vE "level="` filter strips real errors too. Pattern:

    ```bash
    out_stdout=$($CMD 2>/tmp/test.err)
    out_stderr=$(cat /tmp/test.err)
    # Assert no ERROR/WARN log lines on stderr beyond tolerable startup noise.
    if echo "$out_stderr" | grep -E 'level=ERROR|level=WARN' | grep -vE 'durability profile|workspace authorization is DISABLED|rpc listener'; then
        echo "FAIL — stderr has unexpected ERROR/WARN: $out_stderr"
    fi
    # Assert pattern on stdout only.
    echo "$out_stdout" | grep -qE "$EXPECTED" || echo "FAIL"
    ```

2. **Anchor success patterns** — `grep -qE "^Created workspace ACME$"` not `grep -q Created`.

3. **Cross-check writes via curl** — don't trust the CLI's own read path to verify a CLI write. `loom role show` reads through the same client path as `loom role set`, so a stale-cache or wrong-endpoint bug round-trips silently. Always confirm against fleet-db's HTTP API.

4. **Process-leak detection** — after each embedded-mode test, `pgrep fleet-db` must return zero. A non-zero count means a defer did not run (likely an `os.Exit` shortcut bypassing `cmdstore.WithStore`'s deferred `Close`). Same check as `e2e-cli.md` C6.

## Cleanup

```bash
kill "$(cat /tmp/loom-fdb.pid 2>/dev/null)" 2>/dev/null
podman stop loom-test-redis 2>/dev/null
pkill -f /tmp/fleet-db 2>/dev/null
rm -rf /tmp/loom-e2e /tmp/loom-e2e-embedded /tmp/loom-fdb.pid /tmp/loom-fleet-db.sock 2>/dev/null
# Sanity: no leaked subprocess fleet-db
pgrep -a fleet-db || echo "no leaks"
```

## Related

- [e2e-cli.md](e2e-cli.md) — phases A/B/C/D/F, the CLI + curl matrix
- [e2e-ui.md](e2e-ui.md) — phase E, the browser matrix
- [known-issues.md](known-issues.md) — tracked defects and their regression guards
- [../testing-terminology.md](../testing-terminology.md) — depth / realness / provisioning / polarity, and the trap words
