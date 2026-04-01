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
- Use `--auth-url` to point at an external auth service for JWT-based authentication. Always enable external auth in production.
- Use `--fleet-api-key` (or `LOOM_FLEET_API_KEY` env var) to authenticate fleet worker registration.

## Workspace Clone Security

Loom's workspace creation accepts git clone URLs via the API. If the API is exposed to untrusted users, an attacker with API access could trigger the server to make outbound `git clone` connections to internal services (SSRF).

### Mitigations in place

- **Protocol restriction**: only `https://` and `git@` URL schemes are allowed (prefix check rejects `http://`, `ftp://`, `file://`, etc.)
- **Control character filtering**: null bytes, newlines, and carriage returns are rejected
- **Git flag injection prevention**: path segments starting with `-` are rejected
- **SSRF hostname blocklist**: loopback addresses (127.0.0.0/8, ::1), private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16), CGNAT shared addresses (100.64.0.0/10, RFC 6598), link-local addresses (169.254.0.0/16, fe80::/10), unspecified addresses (0.0.0.0, ::), and cloud metadata IPs (169.254.169.254) are rejected
- **SSRF known-hostname blocklist**: `localhost`, `localhost.localdomain`, `metadata.google.internal`, and `metadata.internal` are rejected
- **Path traversal prevention**: cloned repos are confined to `~/.loom/workspaces/`
- **Request timeout**: 60-second deadline on clone operations

### Limitations

- **DNS rebinding**: a public hostname that resolves to an internal IP at clone time is not blocked, because `git clone` uses its own network stack and we cannot inject Go's dialer. Mitigate with egress firewall rules or DNS pinning at the network level.
- **Internal hostnames**: custom internal git hosts (e.g., `git.corp.example.com`) are not blocked by the hostname blocklist. Mitigate with network-level egress controls or a future admin-configurable allowlist.
- **Credential forwarding**: git may send stored credentials (from credential helpers) to the target host. This is standard git behavior and is not blocked.

### Recommendation

When exposing the Loom API to untrusted users, use `--auth-url` for authentication and configure network egress rules to restrict outbound git connections to approved hosts.
