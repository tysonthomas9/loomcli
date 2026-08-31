# Loom Security Guide

## Execution Isolation

Loom does not sandbox the process that runs the LLM and edits code. The
container sandbox (`LOOM_DRIVER_SANDBOX=container`) contains the DriverRun
workflow bundle only; TaskRun leaves and daemon-supervised agents run as host
processes, and the backend CLIs are launched with their own sandboxes disabled.
`read_only` and the tool lists are backend tool/approval policy, not a
boundary. A Daytona leaf is the only real remote isolation available today and
needs a network-reachable git URL.

Read `docs/design/execution-isolation.md` before relying on any of these as a
security control. The rest of this guide covers credential storage and the
network-facing API surface, which are separate concerns.

## Credential Storage

### Redis Passwords

Loom connects to Redis for terminal-state persistence and, when fleet mode is enabled, fleet coordination and stale detection. The desktop local runtime can also connect embedded FleetDB to an external Redis backing store. **Never store Redis passwords in committed config files.**

Redis is configured via `loom serve` flags and environment variables:

| Flag | Env Var | Description |
|------|---------|-------------|
| `--redis-addr` | `LOOM_REDIS_ADDR` | Redis address for terminal-state storage (and fleet coordination when `--fleet-mode` is set) |
| `--redis-password` | `LOOM_REDIS_PASSWORD` | Redis password (prefer env var to avoid leaking in process list) |
| `--fleet-mode` | `LOOM_FLEET_MODE=true` | Enable fleet coordination (task claims, stale detector, fleet worker API, JWT signing). Off by default. |

The desktop app's Settings page can configure external Redis for embedded
FleetDB. That setting is stored outside the repo in the local runtime data
directory as `local-settings.json` with `0o600` permissions. The UI accepts
`redis://`, `rediss://`, or `redis-cli --tls -u ...` values, stores the
password locally, and never returns the password to the browser after saving.

When `--redis-addr` is empty, terminal state persists to an in-process
miniredis and is dumped to `~/.loom/terminal-state/snapshot.json`. When
fleet mode is on without an external Redis, the snapshot also contains
the JWT signing key and is written with `0o600` permissions. Plain
UI-only snapshots use `0o644`.

### State File Security

| File | Scope | Contents |
|------|-------|----------|
| `~/.loom/state.json` | User-level cache | Local checkout paths and last selected workspace hint (regenerable, not config) |
| `~/.loom/fleet-db/redis-snapshot.json` | Local-mode storage | Embedded fleet-db's miniredis snapshot - workspaces, repos, agents, roles, daemon profiles, issues |
| `~/.loom/local-settings.json` or app data equivalent | Desktop-local config | Optional external Redis settings for embedded FleetDB; may contain a Redis password |
| `.loom/` | User config directory | Runtime state, daemon PID files |

The miniredis snapshot may include an embedded fleet JWT signing key (when fleet mode is on); the writer chmods it to `0o600` in that case. Plain UI snapshots stay `0o644`.

`.loom/` is listed in the project `.gitignore`. The user-level `~/.loom/` directory lives outside any repo by design.

In cloud mode (`LOOM_FLEET_DB_URL` set), state lives on the fleet-db server; the local files above are not used. Auth is via `LOOM_FLEET_DB_API_KEY` (production) or `LOOM_FLEET_DB_ACTOR` (dev mode).

### Redis Production Configuration

For production Redis deployments:

1. **Enable `requirepass`** in `redis.conf` to require password authentication.
2. **Bind to localhost** or a private network interface — avoid exposing Redis on `0.0.0.0`.
3. **Use TLS** when Redis is accessed over a network. The desktop embedded FleetDB Redis setting supports TLS; `loom serve --redis-addr` terminal-state Redis does not currently expose a TLS flag.

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

### Repo-local Credential Helper

Loom installs a git credential helper into the `.git/config` of every loom-managed clone whose `origin` is an `https://` remote (`localworkspace.EnsureCredentialHelper`). The helper is a shell snippet that answers `get` with `x-access-token` and the value of `GITHUB_TOKEN` (falling back to `GH_TOKEN`) read from the environment at call time — **no token is ever written to disk**, only a reference to an environment variable.

It exists because agents run under a daemon process with no working directory service available: `osxkeychain`, the global `gh` credential helper, and `ssh` all fail before they read any configuration, so https plus a token in the environment is the only credential path that still works. `GIT_ASKPASS` and `GIT_CONFIG_*` remain blocked by `FilterEnv()`; this deliberately routes around them via repo configuration rather than re-opening them.

Two consequences worth knowing:

- The helper list is written as an empty entry followed by loom's snippet. The empty entry **resets** helpers inherited from system and global git config for this repo, so a keychain helper cannot answer first. A hand-written `credential.helper` in a loom-managed clone will be replaced.
- It applies only to loom-managed clones with https remotes. `git@`, `file://`, and local-path remotes are left untouched, and linked worktrees inherit the setting from the clone's shared `.git/config`.

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

## Operator Attribution on Board Writes

Issue writes originating in the web UI carry a per-request *operator identity*
in the `X-Actor` header, so the board's audit trail names the person who
clicked rather than the harness process. The identity is:

1. the verified session's email (or subject) when authentication is enabled, else
2. `LOOM_OPERATOR_ACTOR`, else
3. `operator@local`.

### The actor must hold a role in the issue backend's ACL

fleet-db honors `X-Actor` but then requires that actor to hold an ACL role in
the workspace. An issue store provisioned before operator attribution existed
knows only the harness actor, so the operator identity is rejected with
`403 forbidden: workspace access denied`.

That rejection is **advisory, never fatal**. The fleet client retries the
rejected request exactly once as the configured process actor, so the write
lands. What is lost is attribution, not the board: the audit trail records the
process actor instead of the operator. A warning naming the actor, the
workspace, and the remediation is logged once per actor per ten minutes, and
`loom doctor` reports the same condition as a `operator_actor_role` warning.

Only the "actor has no role at all" rejection falls back. `403 insufficient
permissions` — the actor *has* a role that lacks the permission — is reported
honestly and never retried, because escalating it to the process actor would be
privilege escalation rather than a fallback. `401` credential failures are
likewise never retried.

To restore attribution, grant the actor a role:

```
redis-cli SET 'fleet-db:acl:global-roles:operator@local' maintainer
```

The denial is re-probed within ten minutes, so a role granted while `loom
serve` is running takes effect without a restart. Alternatively point
`LOOM_OPERATOR_ACTOR` at an actor that already holds a role.

### Attribution requires header-based identity

fleet-db resolves identity in the order **API key → header**, first success
wins, and then strips `X-Actor`. So when loom is configured with
`LOOM_FLEET_DB_API_KEY`, every write is attributed to the *key's* actor and the
operator identity is ignored entirely — operator attribution is only in effect
when loom sends no API key and fleet-db runs with `auth.dev_mode`. `loom
doctor` skips the `operator_actor_role` check under an API key for that reason:
there is nothing to diagnose.
