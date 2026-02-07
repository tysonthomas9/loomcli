# Loom Security Guide

## Credential Storage

### Redis Passwords

Loom connects to Redis for fleet coordination and stale detection. **Never store Redis passwords in committed config files.**

Fleet Redis is configured via `loom serve` flags and environment variables:

| Flag | Env Var | Description |
|------|---------|-------------|
| `--redis-addr` | `LOOM_REDIS_ADDR` | Redis address for fleet coordination and stale detection |

Redis password authentication is supported at the library level (`internal/kv` and `internal/webui/fleet`) but is not yet exposed via CLI flags or environment variables. If your Redis instance requires password authentication, use network-level access controls (localhost binding, firewall rules) until CLI password support is added.

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
- Use `--api-key` (or `LOOM_WEBUI_API_KEY` env var) to authenticate WebUI API access. Do not run with `--no-auth` in production.
- Use `--fleet-api-key` (or `LOOM_FLEET_API_KEY` env var) to authenticate fleet worker registration.
