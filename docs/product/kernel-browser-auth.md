# Durable Interactive-Agent Browsers: Authentication

Status: implemented for the POC (KERNEL-BROWSER-2). This version stores
browser identities only. No live page or Kernel session is launched, so every
new browser stays `starting`.

## Components

| Piece | Where | Role |
|---|---|---|
| Browser records | FleetDB (`/api/v1/{ws}/browsers`) | Canonical store; Redis and Postgres |
| Delegation verifier | FleetDB `FLEET_BROWSER_DELEGATION_*` | Accepts only Loom-signed EdDSA delegations |
| Delegation signer | Loom `internal/browserauth` | Mints a single-operation, ≤5 min delegation after authorizing the caller |
| Agent routes | Loom `/api/agent/browsers…` | `loom browser create/state` from an agent terminal |
| Operator routes | Loom `/api/workspaces/{ws}/agents/{name}/browsers…` | The Browser pane |
| Local bridge | `<dataDir>/run/browser-session.sock` + `loom local browser-session` | Issues local desktop operator sessions to the Tauri shell |

FleetDB's normal service credential identifies the Loom server. It never names a
browser owner. Only the `X-Browser-Delegation` header does that, and FleetDB
rejects browser calls that lack a valid delegation. If the verifier is not
configured, the routes answer 503.

## Principals

### Agent session

When the server spawns an interactive agent's PTY from its own tab launch spec
(`LOOM_AGENT_NAME`, `LOOM_AGENT_TERMINAL_ID` = session name,
`LOOM_ORCHESTRATOR_SESSION_ID`), Loom creates a random 256-bit bearer. The
bearer is:

- kept in memory only as a SHA-256 hash;
- injected into that one process as `LOOM_AGENT_BROWSER_SESSION`, along with
  `LOOM_AGENT_BROWSER_URL`;
- never written to tab metadata, which clients can read;
- revoked when that exact process ends (exit, kill, grace or idle reap, or
  shutdown).

A respawn under the same terminal gets a new bearer, and the old process's end
cannot revoke it. Clients cannot set a tab's kind or launch spec, so a browser
client cannot trigger a binding for an agent it names. The spawn env also
strips any inherited `LOOM_AGENT_BROWSER_*` values.

The owner is always taken from the binding. A body `owner_agent_id` or a
`?workspace=` that disagrees with it returns 403. `X-Actor`, `LOOM_AGENT_NAME`,
Host, Origin and FileAccess never authorize anything. Every request also
re-checks that the owner is still an interactive agent (`kind: interactive`, or
role `lead`/`orchestrator` without a kind). Otherwise the request returns 404.

FleetDB agent leases were rejected as the binding. Lease tokens are readable by
any FleetDB reader, and lead processes inherit the server's FleetDB credentials.

### Workspace operator: remote

The JWT middleware must have validated a `UserIdentity`, and the browser
permission resolver must allow the operation for the requested canonical
workspace and target. That resolver is built from `LOOM_BROWSER_OPERATOR_GRANTS`,
a JSON array of workspace-scoped grants:

```json
[
  {"workspace": "KERNEL-BROWSER", "subjects": ["user-123"]},
  {"workspace": "OTHER", "subjects": ["user-456"], "agents": ["pair-lead"]}
]
```

A grant covers only its own workspace. Workspace matching is exact.
Omitting `agents` covers every interactive agent in that workspace. Wildcards
are rejected. When the variable is unset, every remote request is denied. If
the grants are malformed, the server logs an error and also denies every
remote request. The older workspace-blind `LOOM_BROWSER_OPERATOR_SUBJECTS`
allowlist is no longer honored. File-browser roles do not grant browser
access. Operators can list, get and select, but never create.

### Workspace operator: local desktop

In local open mode, `loom local service` passes `LOOM_BROWSER_SESSION_SOCKET`
to `loom serve`, which listens on a Unix socket with these protections:

- The socket directory is 0700 and the socket file is 0600.
- Each connection's peer UID must equal the server UID. Peer credentials come
  from `LOCAL_PEERCRED` on macOS and `SO_PEERCRED` on Linux. Where neither is
  available, the socket is never created.
- The path must be ≤103 bytes.
- A stale socket is replaced only if it is ours. Any other file at that path is
  left alone and the bridge stays off.

The Tauri shell runs `loom local browser-session issue --workspace W` as a
sidecar and hands the bearer to its own WebView over IPC. Refresh and revoke
read the bearer from stdin, never from argv or env. The WebView sends it as
`X-Loom-Operator-Session`.

Sessions are bound to the UID and one workspace. They expire after 20 minutes
idle or 12 hours absolute, are revoked on logout or unload, and are all dropped
when the runtime restarts. The client refuses to dial a socket whose directory
or file is not private to this user.

A normal browser tab pointed at the local server has no operator session and
gets 401 `browser_operator_session_required`. That holds even with a forged
loopback Host or Origin.

## Accepted trust boundaries (POC)

1. **Same OS user = operator.** Any process running as the desktop user can
   connect to the socket and obtain an operator session. That includes an
   agent process, which can then list and select, but not create, browsers of
   any interactive agent in the workspace. Ticket comments accepted this for
   the POC. It is tested separately from agent-session isolation: agent
   sessions cannot act for another agent, but same-UID operator access is by
   design.
2. **Residual delegation window.** FleetDB cannot see Loom's in-memory
   revocations. A delegation that was already minted stays valid until its
   `exp`, which is ≤5 minutes (the default is 2 minutes, with 30 s of clock
   skew). Each delegation carries a single operation for a single owner, and
   Loom mints it only after authorization, just before the FleetDB call.
3. **Local signing key.** In local mode the key lives at
   `<dataDir>/browser-delegation/signing-key.json`, with a 0700 directory and a
   0600 file owned by the user. Symlinks and loose modes are refused. The
   embedded FleetDB gets only the public key. Same-UID processes can read the
   private key; this is the same boundary as item 1.

## Configuration

| Env | Side | Meaning |
|---|---|---|
| `LOOM_BROWSER_DELEGATION_SIGNING_KEY` | Loom | `kid=<base64url 32-byte seed>`; overrides the local key |
| `LOOM_BROWSER_DELEGATION_ISSUER` / `_AUDIENCE` | Loom | Default `loom` / `fleet-db` |
| `LOOM_BROWSER_OPERATOR_GRANTS` | Loom (remote) | JSON workspace-scoped operator grants; unset or invalid = deny |
| `LOOM_BROWSER_SESSION_SOCKET` | Loom (local) | Set by `loom local service` |
| `FLEET_BROWSER_DELEGATION_PUBLIC_KEYS` | FleetDB | `kid=<base64url pubkey>,…`; malformed = startup fails |
| `FLEET_BROWSER_DELEGATION_ISSUER` / `_AUDIENCE` | FleetDB | Must match Loom |

Local mode resolves both the signer and the embedded FleetDB verifier from
`LoomDir()`, which `loom local service` pins with `LOOM_CONFIG_DIR`. An
embedded FleetDB that a pre-browser loom binary started and that is then reused
has no verifier. Browser routes answer 503 until that FleetDB restarts.

In remote deployments, set the signing key explicitly and give FleetDB the
matching public key. Rotate by adding the new `kid` to FleetDB, switching
Loom, and then removing the old `kid`.

## Error codes (Loom)

| HTTP | Code | Meaning |
|---|---|---|
| 401 | `browser_agent_session_required` | Missing or inactive agent session |
| 401 | `browser_operator_session_required` / `_expired` | No live desktop session |
| 401 | `browser_operator_identity_required` | Remote mode without a user |
| 403 | `browser_forbidden` | Another owner, workspace or permission |
| 404 | `browser_agent_not_found` | Target is missing or not interactive |
| 404 | `browser_not_found` | No such browser for this owner |
| 409 | `browser_request_conflict` | `request_id` reused with a different payload |
| 503 | `browser_unavailable` | FleetDB down, or delegation not configured or rejected |
| 503 | `browser_operator_bridge_unavailable` | Local socket not running |

The UI shows the error states it gets. It never invents a local browser.
