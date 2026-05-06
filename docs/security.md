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

### State File Security

| File | Scope | Contents |
|------|-------|----------|
| `~/.loom/state.json` | User-level cache | Local checkout paths and last selected workspace hint (regenerable, not config) |
| `~/.loom/fleet-db/redis-snapshot.json` | Local-mode storage | Embedded fleet-db's miniredis snapshot — workspaces, repos, agents, roles, daemon profiles, issues |
| `.loom/` | User config directory | Runtime state, daemon PID files |

The miniredis snapshot may include an embedded fleet JWT signing key (when fleet mode is on); the writer chmods it to `0o600` in that case. Plain UI snapshots stay `0o644`.

`.loom/` is listed in the project `.gitignore`. The user-level `~/.loom/` directory lives outside any repo by design.

In cloud mode (`LOOM_FLEET_DB_URL` set), state lives on the fleet-db server; the local files above are not used. Auth is via `LOOM_FLEET_DB_API_KEY` (production) or `LOOM_FLEET_DB_ACTOR` (dev mode).

### Redis Production Configuration

For production Redis deployments:

1. **Enable `requirepass`** in `redis.conf` to require password authentication.
2. **Bind to localhost** or a private network interface — avoid exposing Redis on `0.0.0.0`.
3. **Use a TLS-terminating proxy** (e.g., stunnel, HAProxy) if Redis is accessed over a network. Loom does not natively configure TLS on Redis connections.

### Config File Security Hardening

- **Atomic writes**: `SaveConfig` uses tmp+rename to prevent partial writes on crash.
- **File locking**: flock-based config file locking (`internal/configlock`) prevents concurrent write corruption from parallel agents.
- **Parse failure protection**: `LoadConfig` refuses to overwrite config after parse/read failures, preventing data loss from corrupted reads.
- **Session file permissions**: Session audit trail files are created with `0o600` permissions. Session directories use `0o700`.

### Secret Rotation

Redis configuration changes require restarting `loom serve` — there is no hot-reload mechanism. Plan maintenance windows accordingly.

### Process Security

- Environment variables are visible to the same user via `/proc/<pid>/environ` but not to other users (requires root or same UID).
- When binding `loom serve` to a non-localhost address (`--bind 0.0.0.0`), a warning is logged. Ensure this is intentional and that appropriate network controls are in place.
- Use `--auth-url` to point at an external auth service for JWT-based authentication. Always enable external auth in production.
- Use `--fleet-api-key` (or `LOOM_FLEET_API_KEY` env var) to authenticate fleet worker registration.
- API key auth has been removed. Authentication is handled exclusively via external OIDC (`--auth-url`) or open mode.

## Agent IPC Security

The daemon IPC Unix socket is created with strict owner-only permissions (`0600`). Only the user running the daemon can connect. Stale socket files from previous crashes are removed on startup. The daemon lockfile prevents concurrent startup.

IPC operations are limited to three mutations (claim, update, complete) — no read operations are exposed over IPC. The `LOOM_DAEMON_SOCKET` environment variable is injected into agent subprocesses automatically.

## Input Sanitization

### Markdown Rendering (XSS Prevention)

All user-supplied markdown rendered in the frontend passes through DOMPurify sanitization. This prevents stored XSS via issue descriptions, comments, and design fields.

### Git Environment Variable Blocklist

`FilterEnv()` strips `GIT_*` environment variables from agent subprocess environments as defense-in-depth. This prevents agents from inheriting git credential helpers, custom hooks, or configuration that could leak credentials or alter git behavior.

### Log Path Sanitization

Role names used in daemon log file paths are sanitized to prevent path traversal. Characters outside `[a-zA-Z0-9_-]` are rejected.

## SSE and WebSocket Security

### SSE Token Workspace Binding

SSE tokens are bound to a specific workspace ID at issuance. The server enforces that a token can only subscribe to mutations for its bound workspace, preventing cross-workspace data leakage via SSE.

### Terminal WebSocket Auth

Terminal WebSocket connections use HMAC-SHA256 token exchange. Tokens bind to both user identity and workspace, preventing cross-workspace terminal access.

### Session Notification Endpoint

`POST /api/sessions/notify` uses bearer token authentication instead of the previous loopback IP check, hardening against request forgery from local processes.

## Editor Launch Security

`POST /api/editors/open` is restricted to loopback-only connections. This prevents remote attackers from triggering arbitrary editor launches on the server host.

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
