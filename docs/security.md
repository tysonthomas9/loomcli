# Loom Security Guide

> **Status:** Current · *audited 2026-08-03 against source*

Contributor-facing catalogue of Loom's security posture. This file owns
**auth-mode policy**; `docs/api.md` owns per-endpoint auth requirements.
Terminology (`fleet` vs `fleet mode`, `backend`, `local mode`) is defined in
[loom-glossary.md](loom-glossary.md).

## Credential Storage

### Redis Passwords

Loom connects to Redis for terminal-state persistence and, when fleet mode is enabled, fleet coordination and stale detection. The desktop local runtime can also connect embedded FleetDB to an external Redis backing store. **Never store Redis passwords in committed config files.**

Redis is configured via `loom serve` flags and environment variables:

| Flag | Env Var | Description |
|------|---------|-------------|
| `--redis-addr` | `LOOM_REDIS_ADDR` | "Redis address for fleet coordination (enables stale detector)" (`internal/cli/serve/serve.go:170`). When set, terminal state also lands in this Redis instead of the in-process miniredis. |
| `--redis-password` | `LOOM_REDIS_PASSWORD` | Redis password (prefer env var to avoid leaking in process list) (`internal/cli/serve/serve.go:171`) |
| `--fleet-mode` | `LOOM_FLEET_MODE=true` | Enable fleet coordination (task claims, stale detector, fleet routes, JWT signing). Off by default (`internal/cli/serve/serve.go:172`). |

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
| `~/.loom/local-settings.json` or app data equivalent | Desktop-local config | Optional external Redis settings for embedded FleetDB (may contain a Redis password) **plus sealed Daytona/GitHub runtime credentials** under the `runtime_credentials` key (`internal/localsettings/settings.go:45,67-77`). Written `0o600` via tmp+rename into a `0o700` directory (`settings.go:170-187`) |
| `runtime-credentials.key` (sibling of `local-settings.json`) | Desktop-local key material | 32 bytes from `crypto/rand`, base64-encoded, generated on first use. This is the symmetric key that unseals the `runtime_credentials` ciphertext above. Written `0o600` via tmp+rename into a `0o700` directory (`internal/localsettings/settings.go:353-383`) |
| `.loom/` | User config directory | Runtime state, daemon PID files |

The Daytona/GitHub runtime credentials in `local-settings.json` are AEAD
ciphertext with per-provider additional data (`runtimeCredentialAAD`,
`internal/localsettings/settings.go:387-389`), so *those* credentials are not
plaintext — but the file is not secret-free: the external-Redis password is
persisted in the clear (`RedisConfig.Password`,
`internal/localsettings/settings.go:52`, written by `Save` via
`json.MarshalIndent` at `:174`). And `runtime-credentials.key` sits in the same
directory, defeating the sealing on its own. Harden the directory, not just the
file.

The miniredis snapshot may include an embedded fleet JWT signing key (when fleet mode is on); the writer chmods it to `0o600` in that case. Plain UI snapshots stay `0o644`.

`.loom/` is listed in the project `.gitignore`. The user-level `~/.loom/` directory lives outside any repo by design.

In cloud mode (`LOOM_FLEET_DB_URL` set), workspace/repo/agent topology lives on
the fleet-db server and only the **embedded fleet-db snapshot**
(`~/.loom/fleet-db/redis-snapshot.json`) drops out — `OpenStore` dispatches to
`openCloudStore` and never starts the embedded server
(`internal/bootstrap/openstore.go:100-105`). The other local files still apply
in both modes: `~/.loom/state.json` is read on every `LoadConfig` to overlay the
machine-local checkout paths fleet-db cannot know
(`internal/cli/config/config.go:180`, `internal/bootstrap/statecache.go:19-32`)
and is written back through `bootstrap.MutateStateCache` with no mode gate;
`local-settings.json` plus `runtime-credentials.key` remain desktop-local; and
`.loom/` still holds daemon PID files. Auth is via `LOOM_FLEET_DB_API_KEY`
(production) or `LOOM_FLEET_DB_ACTOR` (dev mode).

### Redis Production Configuration

For production Redis deployments:

1. **Enable `requirepass`** in `redis.conf` to require password authentication.
2. **Bind to localhost** or a private network interface — avoid exposing Redis on `0.0.0.0`.
3. **Use TLS** when Redis is accessed over a network. The desktop embedded FleetDB Redis setting supports TLS; `loom serve --redis-addr` terminal-state Redis does not currently expose a TLS flag.

### State File Write Hardening

There is no runtime config file to harden — `~/.loom/config.yaml` was removed as
a runtime source, and `LoadConfig` (`internal/cli/config/config.go:120-134`) is
now a **pure reader**: it opens the fleet-db store via `bootstrap.OpenStore` and
projects workspaces through `loadConfigFromStore` (`config.go:175-207`), writing
nothing. No non-test writer of `config.yaml` remains
(`docs/design/distributed-control-plane.md:844`). The hardening below applies to
the user-state files listed above.

- **Atomic writes**: state files go through `atomicfile.WriteFile`
  (tmp file, chmod, rename), so a reader never sees a half-written file. The
  bootstrap state cache writes `~/.loom/state.json` this way at mode `0600`
  (`saveStateCacheLocked`, `internal/bootstrap/statecache.go:108`);
  `local-settings.json` and `runtime-credentials.key` do their own tmp+rename
  (`internal/localsettings/settings.go:178-187,374-383`).
- **File locking**: flock-based locking (`internal/configlock`) prevents
  concurrent write corruption from parallel agents. It now guards the state
  cache (`internal/bootstrap/statecache.go:123`), the stack store
  (`internal/stackstore/stackstore.go:131`), and epic reconcile
  (`internal/cli/epic/epic_reconcile.go:59`) — not a config file.
- **Parse failure protection**: `bootstrap.MutateStateCache`
  (`internal/bootstrap/statecache.go:133-147`) takes the lock, calls
  `LoadStateCache()`, and returns early on any read or `json.Unmarshal` error,
  so a corrupted `state.json` is never overwritten with partial data.
  `LoadStateCache` returns an empty cache only when the file does not exist
  (`statecache.go:60-83`).
- **Session file permissions**: Session audit trail files are created with
  `0o600` permissions and session directories with `0o700`
  (`internal/sessions/store.go:21-22`).

### Secret Rotation

Redis configuration changes require restarting `loom serve` — there is no hot-reload mechanism. Plan maintenance windows accordingly.

### Process Security

- Environment variables are visible to the same user via `/proc/<pid>/environ` but not to other users (requires root or same UID).
- When binding `loom serve` to a non-localhost address (`--bind 0.0.0.0`), a warning is logged. Ensure this is intentional and that appropriate network controls are in place.
- Use `--fleet-api-key` (or `LOOM_FLEET_API_KEY` env var) to authenticate fleet worker registration (`internal/cli/serve/serve.go:173`).
- API key auth has been removed. Authentication is handled exclusively via external OIDC (`--auth-url`) or open mode.

### Auth modes

`loom serve` has exactly two user-auth postures, selected by whether
`--auth-url` / `LOOM_AUTH_URL` is set (`internal/cli/serve/serve.go:180`):

- **Open mode (the default).** No `--auth-url` means no user-JWT middleware at
  all — `ExtAuthURL` empty is documented as "open mode"
  (`internal/webui/server_config.go:77`) and the middleware field is left nil
  (`internal/webui/app/server.go:96`). Every protected route is reachable
  without a bearer token. The only guard is a log line when the server is also
  bound off-loopback (`internal/webui/app/server_app.go:93-94`) — it is a
  warning, not a refusal. **Always set `--auth-url` in production.**
- **External-auth mode.** `--auth-url` enables RS256 JWT bearer validation
  against the external auth service's JWKS. `--auth-issuer` overrides a default
  that *is* derived from `--auth-url` (it defaults to the URL verbatim);
  `--auth-audience` overrides a default that is **not** — it is the literal
  string `loom`, unrelated to the auth URL. Both defaults are applied only when
  `--auth-url` is set (`internal/cli/serve/serve.go:181-182`, defaults in
  `applyAuthDefaults` at `serve.go:557-568`).

`registerServeAuthFlags` binds a fourth flag that modifies external-auth mode
without adding a third posture:

- **`--auth-allow-insecure`** (`internal/cli/serve/serve.go:183`) — flag only,
  with no env-var fallback, unlike the three above. It does two separate things:
  1. Permits an `http://` `--auth-url` whose host is not loopback, which is
     otherwise a startup `log.Fatalf`
     (`internal/cli/serve/serve_auth.go:19-21`).
  2. Builds the JWKS HTTP client with `webui.SafeDialContext(true)`, which
     returns the bare dialer and skips the private-IP rejection entirely
     (`internal/webui/app/server_app_helpers.go:18-22`,
     `internal/webui/safe_dial.go:28-36`).

  Effect (2) makes the flag effectively mandatory for **any** auth service on a
  private network, including over `https://`. Startup validation only fatals on
  `http://` + non-loopback, so `--auth-url https://auth.internal` starts
  cleanly and then fails every JWKS fetch at dial time with
  `blocked: <host> resolves to private IP` (`safe_dial.go:54-56`). The JWKS URL
  is operator-supplied rather than attacker-supplied, so this dialer is a
  private-network policy gate, not the SSRF boundary described under
  [Workspace Clone Security](#workspace-clone-security).

### Routes that authenticate themselves

Six route families bypass the user-JWT middleware because their handlers
enforce a different scheme (`internal/webui/server/middleware/auth_routes.go:66-88`):

| Prefix | Scheme |
|---|---|
| `/api/fleet/` | `--fleet-api-key` for register; fleet JWT for claim/done/heartbeat |
| `/api/internal/workers/` | `LOOM_WORKER_TOKEN` bearer |
| `/api/webhooks/` | Per-binding signature (e.g. GitHub `X-Hub-Signature-256`) |
| `/api/driver/` | Run-scoped `X-Loom-Driver-*` credentials + fenced heartbeat |
| `/api/task-run/` | Per-task-run lease-token bearer, verified through the store's fenced checks |
| `/api/auth/` | Reverse-proxied to the BetterAuth service, which handles its own auth |

Additionally unauthenticated by design: `GET /health`, `GET /api/health`,
`GET /metrics`, `GET /api/config` (auth discovery for bootstrap),
`GET /api/workspaces/{ws}/events` (own SSE token exchange), the terminal
WebSockets (own one-time token), `POST /api/client-errors`,
`POST /api/sessions/notify` (own bearer, below), and all non-`/api/` paths
(frontend static files and SPA routes) —
`internal/webui/server/middleware/auth_routes.go:12-63`. Workspace-scoped
paths are normalized by stripping `/api/workspaces/{ws}/` before matching
(`auth_routes.go:16`), so the same rules cover both forms.

## Agent IPC Security

The daemon runs **two** Unix domain sockets. Neither is reachable off-host.

**Agent IPC socket** — `<daemon-dir>/agent-ipc.sock`
(`internal/cli/daemon/daemon_ipc.go:461-464`), chmod `0600` immediately after
listen (`daemon_ipc.go:70`). Its path is injected into agent subprocesses as
`LOOM_DAEMON_SOCKET` (`internal/cli/daemon/supervisor/spawn.go:446-447`).

The operation surface is **mutations only** — `claim`, `update`, `complete`,
`heartbeat`, `release_lock`, `release_claim`
(`internal/cli/daemon/daemon_ipc.go:43-48`, dispatched at `:166-189`). Reads
never traverse it: the subprocess-side decorator overrides exactly five issue
mutations — `Update`, `ClaimIssue`, `ReleaseIssueLock`, `Close`, `ReleaseClaim`
(`internal/cli/ipc_issue_backend.go:50,55,61,66,74`) — and serves
`Get`/`List`/`Ready`/`Stats`/… directly from the underlying issue backend
(`ipc_issue_backend.go:30-32,80-171`). The sixth operation, `heartbeat`,
reaches the socket from the agent supervision loop, not through this decorator.
Routing mutations through the daemon is what keeps them behind the lease fence
(`ipc_issue_backend.go:3-8`).

**Control socket** — `<daemon-dir>/daemon.sock`
(`internal/cli/daemon/daemon_control.go:416-419`), exposing five
agent-lifecycle operations (`agent_stop`, `agent_start`, `agent_restart`,
`agent_list`, `agent_yield` — `daemon_control.go:42-48`). This socket path is
never injected into agent subprocesses.

> **Known gap — the control socket is not permission-hardened.**
> `startControlServer` (`daemon_control.go:53-64`) calls `net.Listen("unix",
> …)` and never chmods the result, so it lands at the process umask (typically
> `0755`), unlike the agent IPC socket's explicit `0600`. `os.Chmod` appears
> exactly once in `internal/cli/daemon/` — `daemon_ipc.go:70`. Nor is the
> parent directory hardened: `rpc.EnsureSocketDir`, which does the
> non-symlink / same-uid / force-`0700` checks
> (`internal/rpc/socket_path.go:74-130`), has **no non-test callers** and in
> any case only applies to `/tmp/loom-*` directories (`socket_path.go:79`),
> while `daemon.sock` sits next to the daemon PID file. It is protected only
> by filesystem permissions on whatever directory the PID file lives in.
> Verified 2026-07-23; an earlier revision of this file claimed
> `EnsureSocketDir` protected this socket — it does not.

Stale socket files from a previous crash are removed on startup — safe because
the daemon lockfile prevents concurrent startup
(`internal/cli/daemon/daemon_ipc.go:59-61`).

> The operation constants in `internal/rpc/protocol.go:8-56` (~50 ops including
> reads, `import`, `delete`, `shutdown`) belong to the **RPC client** in
> `internal/rpc/client_ops.go`, which talks to an external issue daemon. No
> in-tree server dispatches them, and they are not exposed on either socket
> above.

## Input Sanitization

### Markdown Rendering (XSS Prevention)

All user-supplied markdown rendered in the frontend passes through DOMPurify sanitization. This prevents stored XSS via issue descriptions, comments, and design fields.

### Subprocess Environment Allowlist

`envfilter.FilterEnv()` is an **allowlist**, not a `GIT_*` blocklist: a variable
reaches an agent subprocess only if its name matches an exact allowlist entry
or an allowed prefix; everything else is dropped, as are malformed entries with
no `=` (`internal/cli/envfilter/envfilter.go:106-138`). `internal/cli/exec.go:87,91-93`
is a thin re-export. Use `envfilter.FilteredEnv()` instead of `os.Environ()`
when building a subprocess environment.

- **Allowed exactly** (`envfilter.go:15-47`): system/locale/XDG basics, proxy
  vars, color vars, `SSH_AUTH_SOCK`, the AI-backend keys (`ANTHROPIC_API_KEY`,
  `OPENAI_API_KEY`, `GEMINI_API_KEY`, `GOOGLE_API_KEY`, `CURSOR_API_KEY`,
  `CODEX_HOME`, `CLAUDE_CODE_OAUTH_TOKEN`), `GITHUB_TOKEN(_FILE)`, and
  `EDITOR` / `VISUAL`.
- **Allowed by prefix** (`envfilter.go:90-95`): `LOOM_`, `DAYTONA_`.
- **Some `GIT_*` vars are deliberately kept** (`envfilter.go:24-27`):
  `GIT_SSH_COMMAND`, `GIT_TERMINAL_PROMPT`, `GIT_AUTHOR_NAME`/`_EMAIL`,
  `GIT_COMMITTER_NAME`/`_EMAIL`. Agents need them to commit and push.
- **Blocklisted regardless of the allowlist** (`envfilter.go:52-78`, precedence
  enforced at `:109-118`): the git-redirection vars (`GIT_DIR`,
  `GIT_WORK_TREE`, `GIT_INDEX_FILE`, `GIT_OBJECT_DIRECTORY`,
  `GIT_ALTERNATE_OBJECT_DIRECTORIES`, `GIT_CEILING_DIRECTORIES`,
  `GIT_COMMON_DIR`), the code-execution vars (`GIT_EXEC_PATH`,
  `GIT_TEMPLATE_DIR`, `GIT_ASKPASS`, `GIT_HOOKS_PATH`), the config-injection
  vars (`GIT_CONFIG`, `GIT_CONFIG_GLOBAL`, `GIT_CONFIG_SYSTEM`,
  `GIT_CONFIG_COUNT`, plus the `envBlocklistPrefixes` entries `GIT_CONFIG_KEY_`
  / `GIT_CONFIG_VALUE_` at `:83-86`), and
  `DAYTONA_API_KEY` / `DAYTONA_SDK_IMPORT` — the Daytona key
  travels via `DAYTONA_CREDENTIAL_FILE` / the runtime-credential API instead,
  and the SDK import path is a dynamic `import()` target.

### Workflow / Task-Runner Environment Allowlist

`envfilter` above governs **agent** subprocesses. Workflow bundles and task
runners go through a **second, stricter** filter in `internal/driver/env.go` —
they are not the same list and neither one implies the other.

- `scopedSubprocessBaseEnv` starts from a small exact allowlist
  (`env.go:5-27`: `PATH`, `HOME`, `PWD`, `TMPDIR`, `TERM`, `USER`, `TZ`, `LANG`,
  `LOOM_CONFIG_DIR`, `LOOM_FLUE_AGENT_MODEL`, the task-runner command vars) plus
  the `LC_` prefix (`env.go:29-31`).
- Everything matching the sensitive lists is denied even if it would otherwise
  pass: exact names (`env.go:33-56`), the prefixes `AWS_`/`AZURE_`/`GCP_`/
  `GOOGLE_`/`FLEET_`/`GIT_CONFIG_` (`env.go:57-65`), and the substrings
  `SECRET`/`TOKEN`/`PASSWORD`/`PRIVATE_KEY`/`ACCESS_KEY`/`API_KEY`
  (`env.go:66-74`).
- **fleet-db credentials never reach a workflow runtime.** Both the
  `LOOM_FLEET_DB_*` and the serve-side `LOOM_DRIVER_FLEET_DB_*` namespaces are
  denied, and `TestFlueRuntimeEnvCarriesNoFleetDBCredentials` fails the build if either
  prefix appears in the runtime env (`internal/driver/env_test.go:94`).
  `LOOM_DRIVER_FLEET_DB_ACTOR` is read by **serve**, not by the workflow: it is
  one of the fallbacks for the executor's owner-actor identity
  (`internal/driver/executor.go:513`). Workflows authenticate with
  `LOOM_RUN_TOKEN` instead — see `docs/design/driver-op-http-api.md`.
- One deliberate widening: `localTaskRunnerBaseEnv` (`env.go:111-123`) re-admits
  the provider-credential allowlist (`trustedLocalProviderCredentials`,
  `env.go:82-100` — `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`,
  `OPENAI_API_KEY`, `CODEX_API_KEY`, `CODEX_HOME`, `GEMINI_API_KEY`,
  `GOOGLE_API_KEY`, `GOOGLE_APPLICATION_CREDENTIALS`, `CURSOR_API_KEY`,
  `GITHUB_TOKEN`, `GH_TOKEN`) so the **local** task runner's backend CLI
  authenticates like local tooling. It applies to the local-task-runner
  entrypoint only; Daytona/remote runners keep the strict filter.

### Log Path Sanitization

Role and worktree names used in daemon log file paths are reduced with
`filepath.Base`, which strips directory components and so prevents traversal out
of the log directory. It does **not** replace or reject any other character — a
role containing spaces or shell metacharacters reaches the filename unchanged.
Only the *role* substitution is logged (guarded on `safeRole != role`,
`internal/cli/daemon/supervisor/spawn.go:333-337`,
`internal/cli/daemon/daemon_logs_cmd.go:171-173`); a rewritten *worktree* name
is swallowed silently (`spawn.go:332`, `daemon_logs_cmd.go:169`).

No character-class filtering is applied — there is no `[a-zA-Z0-9_-]` sanitizer
on this path. A role such as `foo bar;rm` or `foo$(x)` reaches the log filename
verbatim and does not even trigger the warning, since `filepath.Base` leaves it
unchanged. That is acceptable only because the result goes to `os.OpenFile` and
is never handed to a shell.

## SSE and WebSocket Security

### SSE Token Workspace Binding

SSE tokens are bound to a specific workspace at issuance. `TokenStore.Validate` checks the signature, expiry, workspace binding, and single-use nonce (`internal/webui/server/realtime/sse_token.go:89-92`); the workspace check runs **before** nonce consumption so a mismatch does not burn the nonce (`sse_token.go:135-137`).

### Terminal WebSocket Auth

Terminal WebSocket connections use a signed, time-limited, single-use token. The token binds session, workspace, and user id (`internal/webui/server/realtime/terminal_auth.go:28-30,61`), and `ValidateToken` re-checks all three plus single-use (`terminal_auth.go:89-91`). In open mode the embedded user id is empty — it is present for audit logging, not authorization (`terminal_auth.go:60`).

### Session Notification Endpoint

`POST /api/sessions/notify` uses bearer token authentication instead of the previous loopback IP check, hardening against request forgery from local processes. The comparison is constant-time (`internal/webui/handlers/misc/sessions.go:191-197`).

## Editor Launch Security

`POST /api/editors/open` is restricted to loopback-only connections — the handler splits `r.RemoteAddr` and rejects any non-loopback IP (`internal/webui/handlers/misc/editors.go:174-183`). This prevents remote attackers from triggering arbitrary editor launches on the server host.

## Workspace Clone Security

Loom's workspace creation accepts git clone URLs via the API. If the API is exposed to untrusted users, an attacker with API access could trigger the server to make outbound `git clone` connections to internal services (SSRF).

### Mitigations in place

- **Protocol restriction**: only `https://` and `git@` URL schemes are allowed (prefix check rejects `http://`, `ftp://`, `file://`, etc.)
- **Control character filtering**: null bytes, newlines, and carriage returns are rejected
- **Git flag injection prevention**: path segments starting with `-` are rejected
- **SSRF hostname blocklist** (`isBlockedCloneHost`, `internal/webui/service/workspace_validate.go:200-218`; the IP test is the return at `:216-217`, with the CGNAT CIDR in the `cgnatBlock` var at `:194-198`): loopback addresses (127.0.0.0/8, ::1), private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16), CGNAT shared addresses (100.64.0.0/10, RFC 6598), link-local addresses (169.254.0.0/16, fe80::/10), unspecified addresses (0.0.0.0, ::), and cloud metadata IPs (169.254.169.254, matched as link-local) are rejected
- **SSRF known-hostname blocklist** (`internal/webui/service/workspace_validate.go:206-210`): `localhost`, `localhost.localdomain`, `metadata.google.internal`, and `metadata.internal` are rejected
- **Path traversal prevention**: cloned repos are confined to `~/.loom/workspaces/`
- **Request timeout**: 60-second deadline on clone operations

### Limitations

- **DNS rebinding**: a public hostname that resolves to an internal IP at clone time is not blocked, because `git clone` uses its own network stack and we cannot inject Go's dialer. Mitigate with egress firewall rules or DNS pinning at the network level.
- **Internal hostnames**: custom internal git hosts (e.g., `git.corp.example.com`) are not blocked by the hostname blocklist. Mitigate with network-level egress controls or a future admin-configurable allowlist.
- **Credential forwarding**: git may send stored credentials (from credential helpers) to the target host. This is standard git behavior and is not blocked.

### Recommendation

When exposing the Loom API to untrusted users, use `--auth-url` for authentication and configure network egress rules to restrict outbound git connections to approved hosts.

## Related

- [loom-glossary.md](loom-glossary.md) — `fleet` vs `fleet mode`, the three
  senses of `backend`, local vs cloud mode
- [api.md](api.md) — per-endpoint reference. **Generated — never hand-edit it**
  (`docs/api.md:1`, `docs/README.md:48`): `scripts/openapi-to-md` builds it from
  `api/openapi.yaml` plus `api.preamble.md` / `api.appendix.md` via
  `make gen-api-docs` (`Makefile:336`). Edit those sources, not `api.md`.
  `api/openapi.yaml` is the machine-checked contract.
- [observability/tracing-contract.md](observability/tracing-contract.md) §6 —
  the PII/redaction policy for span attributes
- [agents/issue-tracker.md](agents/issue-tracker.md) — which fleet-db a
  `loom data` command reaches
