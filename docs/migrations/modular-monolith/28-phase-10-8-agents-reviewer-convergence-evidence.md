# Phase 10.8 Agents Reviewer Convergence Evidence

- **Status:** Paired FleetDB command, Agents/PR Review/Interaction boundary,
  full repository gates, packaged-Desktop lifecycle proof, live Codex
  transcript, and fail-closed conflict journey green
- **Stack:** 10.8 Agents-owned reviewer convergence
- **Loom branch:** `modular-monolith-phase10-08-agents-reviewer-convergence`
- **Loom base:** stack 10.7 at `bc42d008b`
- **Loom implementation heads:** `7189aeedd` and `daa16149c`
- **FleetDB branch:** `modular-monolith-phase10-08-agents-reviewer-convergence`
- **FleetDB head:** `91c99d628`
- **FleetDB review:** [BrowserOperator/fleet-db#186](https://github.com/BrowserOperator/fleet-db/pull/186)

## Implemented boundary

PR Review owns one complete, versioned `pr-review-checkout` preset. The preset
specifies the shared `pr-reviewer` Role, interactive runtime kind, builtin
checkout prompt, checkout-specific Agent shape, desired state, maximum
instances, and runtime metadata. PR Review does not receive generic Role or
Agent issuers.

Agents owns `ConvergeReviewerIdentity`, one purpose-scoped command that
validates and atomically converges the shared Role and deterministic
checkout-specific Agent. Composition derives only the system authority needed
for that command. The paired FleetDB capability performs the durable Role and
Agent transition under one transaction boundary and reports stable invalid,
conflict, unavailable, and persisted-state results.

The deterministic Agent identity includes a canonical owner/repository hash
and PR number. For the proof repository the identities were:

- `review-octocat-phase10-proof-f1f96cf2-pr-7`; and
- `review-octocat-phase10-proof-f1f96cf2-pr-8`.

Closing a discussion archives only the checkout-specific Agent. The shared
Role remains. Reopening converges the same deterministic Agent back to active
instead of creating a second identity.

`internal/app/prreviewer`, its sequential Role/Agent persistence interfaces,
its partial `RoleCommitted` result, and its constants are deleted. Architecture
ratchets reject the retired package, retired seam names, sequential Fleet
Role/Agent calls in the reviewer transport, and more than one managed-reviewer
route.

## Session retirement correction found by product proof

The first packaged journey exposed a real lifecycle defect after the identity
implementation and repository gates were green: **Close discussion** archived
the Agent but left its Interaction-owned PTY alive. Reopening therefore found
the same terminal and conversation. A manually relaunched Codex child could
heartbeat while the persisted conversation remained `starting`, because the
new child did not own the original Interaction session's endpoint and thread
metadata.

The final implementation adds a narrow PR Review consumer port,
`StopReviewerSession`. Composition projects the existing process-local
interactive runtime into that purpose-specific port. The archive route holds
Interaction's exact Agent lifecycle lock across both operations:

1. Agents first validates ownership and atomically archives the managed
   identity. An unmanaged identity therefore conflicts before any runtime is
   touched.
2. Interaction then stops only PTYs proven by server-owned tab metadata to
   belong to that exact workspace and Agent.

If runtime stop fails, the response is a retryable 503 with stable code
`reviewer_session_stop_failed`; the Agent remains safely archived and a retry
can converge the stop. Tests prove an ownership conflict invokes no runtime
stop. Terminal creation uses the same lifecycle lock, so it cannot race a new
PTY between archive and stop.

## Required proof matrix

| Required behavior | Authoritative proof | State |
|---|---|---|
| One managed identity transaction | FleetDB service, Redis, Postgres, transport, capability, and API suites converge the Role and Agent together. | Green |
| Exact replay | Repeated active and archived commands return the same deterministic Agent and versioned preset without duplicates. | Green |
| Concurrent convergence | Redis and Postgres concurrent command suites converge on one shared Role and one Agent. | Green |
| Preset and ownership drift | FleetDB and Loom tests reject unmanaged rows, mismatched preset ownership, and invalid persisted definitions. | Green |
| Purpose-scoped authority | Composition exposes one managed-reviewer command wrapper; PR Review receives no generic Agents issuer or low-level Role/Agent writer. | Green |
| Archive and reactivation | Handler tests and packaged UI prove active -> archived -> active for the same Agent while the shared Role remains. | Green |
| Session retirement | Focused tests prove managed archive precedes owned-runtime stop, stop failure is visible/retryable, and unmanaged conflict does not stop a runtime. Final packaged proof changes the old PTY from live to dead and creates a fresh terminal/session on reopen. | Green |
| Transcript attribution | Interaction reads the exact managed Agent's Codex thread. Final packaged proof displays a fresh live Codex review from the PR-7 detached worktree. | Green |
| Fail-closed UI conflict | A deliberately unmanaged PR-8 Agent causes HTTP 409, leaves Retry visible, and retains its original Role and absent managed metadata. | Green |
| Retired legacy path | Architecture tests reject `internal/app/prreviewer`, retired sequential symbols, and multiple managed-reviewer Fleet routes. | Green |
| Paired contract | Fleet capability/version checks, canonical and vendored OpenAPI, generated Go/TypeScript models, transport tests, and the contract guard pass together. | Green |

## Verification

FleetDB commit `91c99d628` passed the complete `make gate`, including build,
vet, lint, race-enabled unit and integration suites, coverage, Redis and
Postgres storage contracts, API integration, container E2E, restart/recovery,
and harness evaluation.

Loom focused verification after the session-retirement correction passed:

```text
GOCACHE=/private/tmp/phase10-08-fix-go-cache \
  go test ./internal/webui/handlers/prreview ./internal/webui/app

ok github.com/tysonthomas9/loomcli/internal/webui/handlers/prreview
ok github.com/tysonthomas9/loomcli/internal/webui/app

GOCACHE=/private/tmp/phase10-08-fix-go-cache \
  go test ./internal/archtest -run 'Phase10|Reviewer|Interaction' -count=1

ok github.com/tysonthomas9/loomcli/internal/archtest
```

The authoritative paired Loom gate passed at `daa16149c` with FleetDB
`91c99d628`:

```text
FLEET_DB_REPO=/Users/tyson/codebase/code-agents/rc-2/fleet-db-modular-monolith-phase7 \
FLEET_DB_BIN=/private/tmp/fleetdb-phase10-08-bin \
GOCACHE=/private/tmp/loom-phase10-08-final-go-cache \
make gate

=== Go quality gates PASSED ===
=== Frontend quality gates PASSED ===
=== All quality gates PASSED ===
```

The final gate covers generated contracts, the vendored FleetDB specification,
architecture and retirement inventories, race-enabled whole-repository tests,
coverage thresholds, frontend Vitest, TypeScript, and production builds.

## Packaged product proof record

The final source rebuilt and sealed the isolated app at
`/private/tmp/phase10-08-tauri-target/release/bundle/macos/Loom Agents.app`.
It contains the WebUI, bundled workflows, Loom sidecar, paired FleetDB sidecar,
and Tauri shell. Runtime metadata reported:

- Loom build `daa16149c`;
- FleetDB source `91c99d628`;
- Loom sidecar SHA-256
  `0b1ce3ab0a8a814bbf4c7e109e60f52e8d62516d3e1ce08556d7c0adbf5fa691`;
- isolated data directory `/private/tmp/phase10-08-desktop-data`; and
- final dynamic URL `http://127.0.0.1:57041`.

The proof used a deterministic localhost GitHub read stub for PRs 7 and 8 and
a proof-only Git shim that redirected only the exact bounded PR-head fetch to a
run-owned local bare repository. The Workspace still observed the canonical
GitHub repository identity. No GitHub write was made. This is deterministic
connector and checkout provisioning, not live GitHub evidence.

Codex execution was live: the packaged terminal launched the installed Codex
backend from the exact detached PR-7 worktree and reached the paid external
service. The final conversation became `idle` with three assistant messages
attributed to one Codex turn. The synthetic fixture recorded the same commit
as review base and head, so Codex correctly reported an empty review diff. The
claim proven here is terminal/session/transcript attribution, not the fixture's
review content quality.

The final close/reopen journey observed:

- live terminal `term_f0e3f4f8-2c1e-4fb1-b6c1-ac49da2d948a` and Interaction
  session `session-2322fd5c-f0f0-4052-9728-888dd2797672`;
- successful reviewer DELETE, disappearance from active Agents, and the same
  terminal projected with `pty_alive: false`;
- reactivation of the same deterministic Agent ID;
- fresh terminal `term_35498106-3910-45b2-aea4-ca60c6303d26`; and
- fresh Interaction session
  `session-bb6ee336-a924-437b-84c7-324e9074c0c3` in the same PR-7 worktree.

For the negative journey, the packaged Loom CLI created PR 8's deterministic
Agent name with unmanaged Role `proof-foreign-reviewer` and no managed
metadata. **Discuss PR** returned HTTP 409 and rendered the ownership-conflict
message plus Retry. The post-conflict Agent projection still had the same
unmanaged Role, null metadata, and enabled state.

Inspected screenshots (PNG, SHA-256):

- `/private/tmp/phase10-08-live-reviewer-transcript-final.png` — final build,
  fresh real-Codex PR-7 transcript after close/reopen,
  `9575729dc1e54fd6bff1e94ada8c4e0ebe32b32392d05047cff240c397b56414`;
- `/private/tmp/phase10-08-close-reopen-fresh-session.png` — final build,
  fresh post-close terminal and exact PR-7 checkout,
  `16a428c8320959fbf35a6c7bcf6c53776c73dcbae65021bbc09f0025b24b979f`;
- `/private/tmp/phase10-08-reviewer-conflict-final.png` — final build,
  fail-closed unmanaged PR-8 identity conflict,
  `819f4bac924f223aae900ba9e2530ff4b46c3d25e53c175d63c75912952802cf`;
  and
- `/private/tmp/phase10-08-pr-list.png` — packaged two-PR list and deterministic
  connector fixture,
  `b6c8511f72b77a49e68b1a1199e6765c19188f9327c8a1712ad2039e0e876f18`.

Only the run-owned app, dynamic runtime, localhost stub, proof repository,
proof browser namespace, and temporary data were used. No default
`loomcli-local-mode` project, persistent Desktop data, foreign browser profile,
or foreign process was stopped or reused.

## Next stack

Stack 10.9 centralizes Automation event admission behind one private owner
implementation while preserving distinct webhook, workflow, and system trust
origins. It must prove exact-byte webhook verification, running-Execution
parent authority, registered system producers, canonical defensive copying,
and retirement of duplicated event validation before stack 10.10 starts.
