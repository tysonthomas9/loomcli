# Loom Security Guide

## Credential Storage

### Redis Passwords

Loom connects to Redis for terminal-state persistence and — when fleet mode is enabled — fleet coordination and stale detection. **Never store Redis passwords in committed config files.**

Redis is configured via `loom serve` flags and environment variables:

| Flag | Env Var | Description |
|------|---------|-------------|
| `--redis-addr` | `LOOM_REDIS_ADDR` | Redis address for terminal-state storage (and fleet coordination when `--fleet-mode` is set) |
| `--redis-password` | `LOOM_REDIS_PASSWORD` | Redis password (prefer env var to avoid leaking in process list) |
| `--fleet-mode` | `LOOM_FLEET_MODE=true` | Enable fleet coordination (task claims, stale detector, fleet worker API, JWT signing). Off by default. |

When `--redis-addr` is empty, terminal state persists to an in-process
miniredis and is dumped to `~/.loom/terminal-state/snapshot.json`. When
fleet mode is on without an external Redis, the snapshot also contains
the JWT signing key and is written with `0o600` permissions. Plain
UI-only snapshots use `0o644`.

### Config File Security

| File | Scope | Contents |
|------|-------|----------|
| `~/.loom/config.yaml` | User-level | Workspace paths, backend selection, daemon settings |
| `loom.yaml` | Project-level | Agent config, daemon settings |
| `.loom/` | User config directory | Runtime state, daemon PID files |

Both `loom.yaml` and `.loom/` are listed in the project `.gitignore` to prevent accidental credential commits.

If you have previously committed a `loom.yaml` that contains credentials, remove it from tracking:

```
git rm --cached loom.yaml
git commit -m "Remove loom.yaml from tracking"
```

### Redis Production Configuration

For production Redis deployments:

1. **Enable `requirepass`** in `redis.conf` to require password authentication.
2. **Bind to localhost** or a private network interface — avoid exposing Redis on `0.0.0.0`.
3. **Use a TLS-terminating proxy** (e.g., stunnel, HAProxy) if Redis is accessed over a network. Loom does not natively configure TLS on Redis connections.

### Secret Rotation

Redis configuration changes require restarting `loom serve` — there is no hot-reload mechanism. Plan maintenance windows accordingly.

### Process Security

- Environment variables are visible to the same user via `/proc/<pid>/environ` but not to other users (requires root or same UID).
- When binding `loom serve` to a non-localhost address (`--bind 0.0.0.0`), a warning is logged. Ensure this is intentional and that appropriate network controls are in place.
- Use `--auth` with `--api-key` (or `LOOM_WEBUI_API_KEY` env var) to authenticate WebUI API access. Always enable `--auth` in production.
- Use `--fleet-api-key` (or `LOOM_FLEET_API_KEY` env var) to authenticate fleet worker registration.
