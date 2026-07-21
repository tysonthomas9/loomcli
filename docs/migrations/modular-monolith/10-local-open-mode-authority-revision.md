# Local Open-Mode Authority Revision

- **Status:** Approved and implemented in the current Phase 4 branch; packaged
  Desktop and local-stack browser evidence remains to be recorded
- **Date:** 2026-07-20
- **Decision affected:** MM-2
- **Scope:** The bundled local/open-mode Loom UI and its management API clients
- **Out of scope:** OIDC/cloud authentication, FleetDB service authentication, and capability command invariants

## Problem

Loom already has two Web UI authentication modes:

- `oidc`, enabled by `loom serve --auth-url`, uses the existing same-origin
  BetterAuth proxy and RS256 JWT validation through JWKS; and
- `open`, the default local single-operator mode.

The modular-monolith authority work added a second authentication requirement
inside `open` mode for operator-only commands. Desktop hides that requirement
by minting a 30-second launch code and exchanging it for a workspace-bound
browser session. A raw browser opened against the local or Podman UI cannot
initiate that flow. Agent and trigger-management forms therefore appear usable
but fail at submission with `authentication required`; some workflow lifecycle
controls instead disable themselves and instruct the user to open Desktop.

This is inconsistent with the local product promise. A user who starts a
single-user local stack and opens its bundled UI should be able to manage
agents without generating, copying, or understanding an operator credential.
Cloud and shared deployments already have the stronger OIDC path and do not
need another authentication design.

## Revision to MM-2

Treat `open` and `oidc` as distinct trust models:

| Mode | Operator authority source | User experience |
|---|---|---|
| `open` | `loom serve` derives a typed local operator authority for the canonical workspace and requested registered action. All clients able to reach the loopback management API are inside the trusted local boundary. | The bundled UI and management CLI work immediately. There is no launch code, browser-session exchange, credential file, login prompt, or Desktop-only management path. |
| `oidc` | The existing external-auth resolver validates the JWT and derives authority from authenticated identity, workspace role, and registered action. | The existing cloud/shared login and authorization flow remains unchanged. |

Open mode does **not** remove typed authority from capability APIs. HTTP and CLI
adapters still request one exact `OperatorAuthority`; the server-side open-mode
resolver supplies it after deriving the canonical workspace and action.
Request DTOs still cannot choose their actor, workspace, authority class, or
action. Capability admission, business invariants, CAS behavior, and audit
records remain unchanged.

The open-mode audit subject should be a stable server-defined value such as
`local-open-operator`, never a caller-provided header or request field.

## Explicit security tradeoff

Open mode trusts reachability of the deployment's intentionally exposed
management endpoint. Any process that can reach that endpoint can perform
registered operator actions, including an agent process with unrestricted
access to it. Open mode therefore does not claim protection against a
malicious process inside the same local or container-network trust boundary.

That tradeoff must be stated plainly rather than obscured behind a browser-only
credential that local management CLIs can read. A deployment that needs to
separate users, agents, or machines must use `--auth-url` and OIDC, even when
the server itself runs on a developer workstation.

To keep the boundary honest without breaking container networking:

- native local startup defaults to loopback;
- the supported Podman and local-mode profiles may listen on `0.0.0.0` inside
  their private container network, but publish their management/UI ports only
  on host loopback;
- a non-loopback open-mode listener logs a security warning because the server
  cannot determine how a container port is published by its host;
- documentation must forbid exposing an open-mode listener to an untrusted
  LAN, reverse proxy, or port-forward and require OIDC for that deployment; and
- switching from `oidc` to `open` is an explicit deployment configuration
  change, not an authentication fallback.

## Required product behavior

In open mode:

1. Opening the bundled UI directly by URL enables agent, trigger-binding,
   workflow-run, and workflow-version management immediately.
2. The UI does not show Desktop-required copy, operator-session controls, or a
   late `authentication required` error for a registered operator action.
3. Page refresh, workspace navigation, and a new browser tab do not require a
   browser-session exchange.
4. Standalone management CLI commands call the same management API without
   reading a durable operator token.
5. The server continues to create action- and workspace-scoped typed authority
   and records the server-defined local operator subject in audit evidence.

In OIDC mode, the current external authentication, workspace-role resolution,
JWT validation, and failure behavior remain authoritative. Token presence alone
must not imply an operator role.

## Removal and retention scope

Approving this revision means removing the local credential and browser-launch
system, not retaining it as a hidden alternate path or changing only the UI
default.

Remove from local `open` mode:

- creation, persistence, permission validation, and reads of
  `.loom/operator/operator.token`;
- the local browser launch/session broker, its launch and session TTL state,
  and the `/operator-sessions/launch` and `/operator-sessions/exchange` routes;
- the hidden `loom local browser-session` command;
- Desktop launch-code minting, URL-fragment injection, recovery validation,
  and related native capability plumbing;
- frontend launch-fragment capture, exchange, in-memory lifecycle bearer,
  per-request bearer attachment, and Desktop-only authorization copy;
- management CLI code that reads the durable local operator token or sends it
  as a bearer; and
- tests, fixtures, packaged assets, and documentation whose only purpose is to
  enforce the removed launch-code protocol.

Retain and continue enforcing:

- typed `OperatorAuthority` at capability command boundaries;
- exact workspace/action admission, server-derived audit identity, capability
  invariants, CAS behavior, and negative authority-class tests;
- OIDC mode, including the same-origin BetterAuth proxy, RS256 JWT/JWKS
  validation, issuer/audience checks, workspace-role resolution, and external
  operator resolver; and
- FleetDB service credentials, execution/session/webhook/system authorities,
  and other credentials unrelated to the local browser operator flow.

Open-mode composition replaces the removed credential resolver with a narrow
server-owned resolver that issues `local-open-operator` authority only for the
canonical workspace and registered action requested by the owning handler. It
must not accept caller-selected identity, workspace, action, or authority
class.

The removal must land as one coordinated server, CLI, frontend, Desktop, and
test change. A mixed state in which the UI stops sending credentials while
handlers still require them would reproduce the current failure. Existing
operator token files from older versions are inert once no route or client
accepts them; migration may leave them untouched initially rather than perform
destructive filesystem cleanup. Historical evidence remains unchanged because
it describes the commits on which the launch-code system was active.

## Acceptance evidence

The revision is complete only when all of the following pass:

- a raw loopback browser creates, edits, enables/disables, runs, and deletes an
  agent or trigger binding without any launch-code step;
- the same browser approves, unapproves, and activates a workflow version when
  the command invariant permits it;
- direct API and standalone CLI journeys prove the same open-mode behavior;
- every command still receives the correct typed authority, canonical
  workspace, registered action, and audit subject;
- `oidc` tests prove unauthenticated, wrong-workspace, and insufficient-role
  callers remain denied while an authorized operator succeeds;
- native and shipped container profiles constrain open-mode exposure to their
  intended local boundary, and OIDC never falls back to open mode; and
- the packaged Desktop and Podman/browser journeys both work without exposing
  or manually handling operator credentials.

## Migration and provenance

This revision does not rewrite the immutable Phase 1 through Phase 4 evidence.
The existing launch-code and local credential tests remain accurate for the
commits named by those records.

The coordinated implementation updates:

1. the MM-2 capability graph and migration baseline;
2. open-mode authority composition and management CLI clients;
3. the frontend's operator gating and Desktop-only messaging;
4. listener validation and security documentation; and
5. architecture, route, frontend, CLI, OIDC, packaged Desktop, and raw-browser
   regression evidence.

Focused deterministic Go, frontend, Desktop TypeScript/Rust, E2E compilation,
shell-syntax, and compose-boundary checks are the implementation evidence in
this branch. The packaged Desktop and running Podman/raw-browser journeys remain
explicitly pending until they are rerun against the final built artifacts.

Current branch verification:

- focused Go authority, composition, server, management-client, handler,
  bootstrap, local CLI, and local-mode packages pass;
- the modified Phase 2 and Phase 3 E2E packages compile with the `e2e` build
  tag;
- the focused frontend unit suite passes `48/48`, frontend and Desktop
  TypeScript typechecks pass, and focused frontend lint passes;
- the local-open Playwright proof passes `2/2`, covering credential-free
  approve/activate plus refresh/workspace navigation with no operator-session
  exchange or authorization header;
- Desktop Rust passes `13/13`, the local-mode entrypoint passes shell syntax,
  and the compose contract proves the local API and UI are published on host
  loopback; and
- the aggregate Loom repository gate passes in `747.64s` at exact source commit
  `54b338d61005799493463acdc559bf8a82022b1e`, with `FLEET_DB_REPO` pinned to
  FleetDB `c73c69a04f27490b54999fc8784cbc992ccff66c` and `FLEET_DB_BIN` pinned to
  the binary built from that commit. The byte-identical paired OpenAPI checksum
  is `bf2935fdbc785deaa70c2ac933dd5f63e44f9deb7ec2a61f332af1b889c8088c`.
  This source-gate result is not a new immutable validation snapshot.

Running packaged Desktop and Podman/raw-browser product proofs have not been
claimed by this revision run and remain pending.

---

[Migration overview](README.md) · [Target architecture](02-target-architecture.md) · [Enforcement and gates](04-enforcement-and-gates.md)
