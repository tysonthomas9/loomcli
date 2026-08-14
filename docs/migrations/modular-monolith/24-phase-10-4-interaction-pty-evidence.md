# Phase 10.4 Interaction and PTY Evidence

- **Status:** Complete; owner cutover, legacy deletion, full gate, and packaged Desktop proof pass
- **Date:** 2026-08-13
- **Parent decision:**
  [Phase 10 consolidated deep-module goal](20-phase-10-consolidated-deep-module-goal.md)
- **Stack:** 10.4 of 10.12
- **Loom branch:** `modular-monolith-phase10-04-interaction-pty`
- **Loom parent:** `763ae161e2e98d4941d091241879623b015be197`
- **Loom implementation commit:** `c7007e6a6d62ba9ec4504ee2baa1b23ec10ecd28`
- **FleetDB companion:** `aec752566821fa029714cef62d4421cbf8238f3d`

## Outcome

Interaction now owns terminal tabs, terminal-session lifecycle, attach planning,
replay, resize, scrollback reset, and session-history projection. The WebUI is a
delivery adapter over Interaction commands and queries; it no longer owns
terminal or session coordination.

The operating-system PTY mechanism is an earned private adapter at
`internal/infra/pty`. Redis-backed interaction history and tab projection are
private infrastructure at `internal/infra/localredis`. Neither adapter is a
public product port. Bootstrap constructs the private runtime and gives
delivery only the Interaction capabilities it consumes.

Execution retains task-run session projection because task-run convergence is
an Execution concern. Interaction consumes that projection without acquiring
Execution mutation authority. Runtime identifiers, launch capabilities,
leases, fence values, and private PTY handles are not serialized into the HTTP
contract.

The following legacy packages are deleted without forwarding packages or dual
paths:

- `internal/webui/terminal`;
- `internal/webui/sessioncoord`; and
- `internal/webui/localredis`.

Canonical surviving terminal routes and their Go and TypeScript representations
come from OpenAPI. Stale tmux routes were removed. Agent and terminal names are
validated, including dotted agent names, and tmux lookup uses exact identity
matching.

## Requirement-to-proof matrix

| Required proof | Result | Representative coverage |
|---|---|---|
| Owner boundary | Interaction owns terminal and tab lifecycle; WebUI is delivery-only and private PTY/Redis details cannot cross the owner API. | Interaction constructor, HTTP adapter, reachability, and retired-root architecture tests |
| Attach and fencing | One `PlanTerminalAttach` capability validates identity and returns an owner-approved replay/attach plan without exposing leases, fences, launch handles, or PTY internals. | Interaction terminal tests and generated-contract tests |
| Replay and reconnect | Reconnect emits a complete bounded timeline, resets renderer scrollback, and does not duplicate or grow prior output. | PTY replay tests, terminal connection tests, and packaged Desktop replay journey |
| Resize | Structured resize messages update the PTY and are reflected after reconnect. Renderer and viewport readiness are stable. | PTY resize tests, `TerminalInstance.test.tsx`, and packaged Desktop resize journey |
| Tab lifecycle | Creating a terminal is idempotent across metadata refresh races; one click produces one tab, and close removes only the selected live tab. | `TerminalView.test.tsx` metadata-sync race regression and packaged Desktop create/close journey |
| Session history | Interaction owns archive queries while Execution retains its task-run projection. Terminal history survives navigation and process exit. | session archive query tests, interaction history tests, and final API projection |
| Transport contract | OpenAPI-generated Go and TypeScript types describe the surviving terminal routes; private runtime values are absent. | generated API staleness, terminal contract tests, and frontend production build |
| Legacy deletion | The three WebUI-owned terminal/session packages and obsolete tmux routes cannot return. | architecture retired-root and route guards |

## Architecture and shrink evidence

The exact production topology after Stack 10.4 is:

- 155 total production packages;
- 18 packages under `internal/modules`;
- 137 packages outside `internal/modules`;
- 38 one-file packages; and
- 57 one-or-two-file packages.

The total matches Stack 10.3 because three legacy packages were deleted and
three explicit owner or adapter seams replaced them. This is an ownership
deepening transaction rather than package-count growth.

The authoritative architecture transaction reports:

```text
Architecture guardrails passed
  composite Store files: 13/13
  outside composition: 0/0
  legacy handler imports: 0/0
  direct persistence-write rows: 84
  capability module roots: 10
  reviewed mutation commands: 107
  named runtime components: 71
  in-scope non-test goroutine launch definitions: 78
  performance records: 6 (6 measured, 0 explicitly deferred)
  pending architecture decisions: 0
  build profiles enforced: 11/11 (all-files AST checks active)
rsswatch peak_tree_rss_mib=1268.0 limit_mib=2048
```

The direct-write inventory increased from 82 to 84 only because the strict
scanner now includes the relocated `internal/infra/localredis` adapter. Those
two rows describe existing Redis writes; Stack 10.4 adds no new persistence
behavior or bypass.

## Repository gate

The full Loom gate ran with Go and frontend parallelism bounded to two and with
both `FLEET_DB_REPO` and `FLEET_DB_BIN` pinned to the exact companion above.
After generated files, import grouping, websocket deprecation annotations, and
frontend formatting were corrected, the clean transaction completed with:

```text
=== Go quality gates PASSED ===
=== Frontend quality gates PASSED ===
=== All quality gates PASSED ===
```

The standalone architecture transaction also passed all 11 build profiles and
the all-files AST checks within the 2048 MiB memory limit. Focused Go tests for
the generated API, PTY adapter, Interaction module, and terminal handlers pass.
The three terminal frontend suites pass 100 tests, and the repository-wide
frontend run passes 8,750 tests with one intentional skip.

## Packaged Desktop proof

The exact packaged application was rebuilt and launched from:

```text
desktop/src-tauri/target/release/bundle/macos/Loom Agents.app
```

Its bundled Loom binary has SHA-256:

```text
2064b1454b8cb13b6fbc69dacfdd5f47a6887040e2e1b75f7b288e16f1ed1d8e
```

The UI created workspace `PHASE10-4-PROOF-20260813` and terminal
`lead-shell-1`. The terminal printed its initial geometry marker at 48 rows by
88 columns, resized in fullscreen to 57 rows by 120 columns, then survived two
board-to-terminal navigation cycles. Both markers replayed cleanly without
partial escape fragments, duplicated scrollback, or cumulative output growth.

One click on **New terminal** created exactly one `lead-shell-2`, proving the
metadata-refresh race is idempotent in the product. Closing that tab through
the UI removed only `lead-shell-2`; the backend projection contained exactly
the remaining `lead-shell-1`. Exiting the final shell and closing the packaged
application completed cleanly. Server logs showed successful HTTP and WebSocket
status codes, the expected delete, and no protocol error.

The replay and resize screenshot is
`/private/tmp/phase10-4-exact-terminal-replay-resize.jpeg`, SHA-256:

```text
9ae0e30b5f27aa02fa5b593c8bc25534d7e02541f976c3915ca60cf96e8e4539
```

The single-tab result after close is
`/private/tmp/phase10-4-exact-terminal-close.jpeg`, SHA-256:

```text
f990a8330c676be261750b2979b2a68c0c4a63b8186d042e8c4fd4649c509748
```

## Provisioning observation

The first packaged launch used the existing default Desktop data snapshot. Its
885,505 entries could not finish FleetDB compaction and capability negotiation
within the health deadline; the probe received EOF after compaction was
canceled. The launch failed visibly and the original data remained untouched.

The exact same packaged executable was then launched with isolated Desktop data
and configuration roots under
`/private/tmp/phase10-4-desktop-proof-20260813`. That controlled root produced
the successful product journey above. This isolates proof state without
claiming that Stack 10.4 resolves large-snapshot startup performance.

No live Codex backend, GitHub mutation, or paid provider was used for this
Stack 10.4 proof.

## Delivery boundary

Stack 10.4 changes Loom only; FleetDB remains pinned as the exact companion and
requires no contract change for this stack. The implementation and this
evidence record form one reviewable Loom PR. Stack 10.5 may begin only from the
resulting Stack 10.4 evidence commit.
