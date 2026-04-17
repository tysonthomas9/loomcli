# Part 1 — detach PTY lifetime from WebSocket lifetime

## Problem

Page refresh and network blips kill the user's shell. `handlers/terminal/ws.go:runTerminalRelay`
calls `p.manager.Detach(connID)` the instant `WSToPTY` returns, which closes the tmux session's
PTY and kills the child.

## Non-goals

- Surviving pod restart (requires Part 2's Firecracker snapshots or accepting the loss).
- Multi-pod routing (edge/control-plane's job).
- Preserving running processes across `loom serve` restarts.

## Nine design knobs (agreed defaults)

| # | Knob | Default | Reason |
|---|---|---|---|
| 1 | Reconnect identity | `(workspace, session)` tuple | already unique per workspace; no new client state |
| 2 | Grace period after WS close | 60 s | client's reconnect ceiling is 30 s; 2× covers slow networks |
| 3 | Idle reap (no output, no WS) | 30 min | catches abandoned sessions without fighting laptop-sleep |
| 4 | Scrollback ring | 256 KB / session | ~2000 lines; 20 sessions × 256 KB ≈ 5 MB RAM |
| 5 | Replay semantics | server emits `\x1b[2J\x1b[H` + ring on every attach | wterm doesn't clear its grid on reconnect; server reset avoids duplication |
| 6 | Session cap | detached count; default 40 (was 20) | absorbs grace-period overlap |
| 7 | Explicit kill | `DELETE /terminal/session?session=…` (or existing `?force=true`) | tab close should kill, not wait for reaper |
| 8 | Drain-while-detached | PTY reader keeps running into the ring | prevents kernel PTY buffer from filling and blocking the child |
| 9 | Pod restart | PTYs die | persistent agents (Part 2) handle this via VM snapshot |

## Option A — Minimal change

Mutate `TerminalManager` in place. No new abstractions. Smallest diff, highest future cost.

### Changes

- Replace `sessions map[string]*TerminalSession` (keyed by `connID`) with two maps:
  - `liveSessions map[string]*LiveSession` keyed by `internalName` (shared across WS reattaches)
  - `wsConns map[string]string` (`connID → internalName`) for resize dispatch
- New `LiveSession` struct owning the PTY fd + fan-out channels per attached WS.
- New `drain.go` goroutine per `LiveSession` pumping PTY → ring + WS set.
- `Detach(connID)` calls `ScheduleKill(60s)` when last WS detaches. Does *not* close PTY.
- `SessionCount()` returns `len(liveSessions)` (counts detached).
- Bump `defaultMaxTerminalSessions = 40`.

### Pros
- Smallest diff (~120 lines excluding tests).
- Reuses `ScheduleKill` (already correct in `lifecycle.go`), `ScrollbackBuffer`, `PtyToWS`, `WSToPTY` as-is.
- Zero new interfaces.

### Cons
- Part 2 forces `ws.go` surgery again — the handler is hard-wired to `*TerminalManager`. An `AgentdClient` cannot slot in without reshaping the handler.
- Two-map invariant in `TerminalManager` increases lock-ordering complexity.

## Option B — Clean architecture with `TerminalHost` / `TerminalConn`

Full interface seam. Introduce `TerminalHost`, `TerminalConn`, `SessionRegistry`, `LocalPTYHost`,
`ByteRingBuffer`. `ws.go` talks only to interfaces.

### Changes

New files in `internal/webui/terminal/`:
- `host.go` — `TerminalHost`, `TerminalConn`, `SessionKey`
- `local_host.go` — `LocalPTYHost` wrapping `*TerminalManager` via adapter; `localConn`; `sessionRegistry`; `sessionRecord`
- `bytering.go` — 256 KB byte ring

Modified:
- `realtime/terminal_relay.go` — `WSToPTY` takes `ConnResizer` instead of `(Resizer, connID)`
- `handlers/terminal/ws.go` — `runTerminalRelay` rewritten against `TerminalHost`
- `handlers/terminal/module.go` — inject `NewLocalPTYHost(manager)` into handler params

### Pros
- Cleanest seam. `ws.go` never needs to change in Part 2 — `AgentdClient` just implements `TerminalHost`.
- Very mockable for handler tests (no tmux in `ws_test.go`).
- Byte-capped ring (not line-capped) is the right primitive for replay.

### Cons
- ~350 lines of interface + adapter code that carries little Part 1-only behaviour.
- Reviewers must understand a richer seam before the feature that justifies it (Part 2) exists.
- `SessionRegistry` partially duplicates `TerminalManager`'s existing `sessions` + `pendingKills` + `killingSet` tracking.

## Option C — Pragmatic balance (recommended)

One value type (`SessionKey`), one interface (`PTYSource`), a two-context split in `ws.go`, and a
byte-capped scrollback. `TerminalManager` stays intact and satisfies `PTYSource` directly.

### Changes

New file:
- `internal/webui/terminal/key.go` — `SessionKey{WorkspaceID, Name}` value type

Modified:
- `internal/webui/terminal/manager.go` — add `PTYSource` interface (8 methods, all existing) with trivial `*Key` adapters; `*TerminalManager` satisfies it
- `internal/webui/terminal/scrollback.go` — switch from line-cap to byte-cap (256 KB)
- `internal/webui/terminal/lifecycle.go` — add `lastOutputAt` atomic timestamp; idle-reap check inside `ScheduleKill`'s deferred goroutine
- `internal/webui/handlers/terminal/ws.go` — `runTerminalRelay` rewritten with `ptyCtx` / `wsCtx` split, replay, `ScheduleKill(60s)` on detach
- `internal/webui/handlers/terminal/terminalWSParams` — `manager *TerminalManager` → `manager PTYSource`

Scrollback replay on every attach:

```go
if snap := scrollback.Bytes(); len(snap) > 0 {
    _ = conn.Write(ctx, websocket.MessageBinary,
        append([]byte("\x1b[2J\x1b[H"), snap...))
}
```

### The key mechanical move: two contexts

```go
// ptyCtx lives as long as the PTY reader needs to drain output.
// It is NOT tied to the WebSocket connection lifetime.
ptyCtx, ptyCancel := context.WithCancel(context.Background())
defer ptyCancel()

// wsCtx is cancelled when the WebSocket drops.
wsCtx, wsCancel := context.WithCancel(reqCtx)
defer wsCancel()

go PtyToWS(ptyCtx, …)    // PTY → ring + (if WS) → WS frames
WSToPTY(wsCtx, …)        // blocks until WS drops
// WS dropped. PTY reader still running.
manager.Detach(connID)
manager.ScheduleKill(workspace, session, 60*time.Second)
```

### Resulting `ws.go:runTerminalRelay` (~40 lines)

```go
func runTerminalRelay(
    reqCtx context.Context,
    conn *websocket.Conn,
    p *terminalWSParams,
    session, workspace string,
) (websocket.StatusCode, string) {
    key := webuterminal.SessionKey{WorkspaceID: workspace, Name: session}

    isNewSession := session == "talk-to-lead" && !p.manager.SessionExists(workspace, session)

    termSession, err := p.manager.Attach(workspace, session, attachCommandForSession(session), 80, 24)
    if err != nil {
        if errors.Is(err, webuterminal.ErrSessionBeingKilled) {
            return websocket.StatusCode(wsCloseSessionKilled), "session is being killed"
        }
        return websocket.StatusInternalError, err.Error()
    }
    connID := termSession.ConnID

    if workspace != "" {
        p.manager.SetSessionOwner(session, workspace)
    }
    if isNewSession && p.loomServerURL != "" {
        injectTerminalContextBanner(termSession, p.loomServerURL, workspaceConfigFn(reqCtx, p, workspace))
    }
    realtime.BroadcastSessionIssueEvent(p.tabMetaStore, p.hub, workspace, session)

    // Replay scrollback on every attach.
    scrollback := p.manager.GetScrollbackBuffer(workspace, session)
    if snap := scrollback.Bytes(); len(snap) > 0 {
        _ = conn.Write(reqCtx, websocket.MessageBinary,
            append([]byte("\x1b[2J\x1b[H"), snap...))
    }

    ptyCtx, ptyCancel := context.WithCancel(context.Background())
    defer ptyCancel()
    wsCtx, wsCancel := context.WithCancel(reqCtx)
    defer wsCancel()

    watchSessionKill(wsCtx, wsCancel, conn, termSession)
    monitor := &terminalMonitor{mgr: p.manager}

    crashCh := make(chan realtime.CrashInfo, 1)
    go func() {
        crashCh <- realtime.PtyToWS(ptyCtx, ptyCancel, conn, termSession.PTY,
            termSession.Name, monitor, scrollback)
    }()

    realtime.WSToPTY(wsCtx, conn, termSession.PTY, p.manager, connID)

    if err := p.manager.Detach(connID); err != nil {
        slog.Error("detach failed", "conn_id", connID, "err", err)
    }
    p.manager.ScheduleKill(workspace, session, 60*time.Second)
    return (<-crashCh).WSClose()
}
```

### Pros

- `ws.go` talks only to `PTYSource`. Part 2's `AgentdClient` implements the same interface;
  handler is untouched.
- Uses existing machinery: `ScheduleKill` is already correct, `ScrollbackBuffer` already exists
  and the `ScrollbackAppender` hook in `PtyToWS` is already wired.
- Single new value type + single new interface + context split. No registry, no fan-out
  channels, no new goroutine pools.

### Cons

- Requires `ScrollbackBuffer` internal change (line-cap → byte-cap).
- `lastOutputAt` atomic + idle-reap check inside `ScheduleKill` is a small extension of
  existing code, not wholly new abstraction.

## Trade-off matrix

| | Minimal (A) | Pragmatic (C) | Clean (B) |
|---|---|---|---|
| Part 1 diff size | ~120 lines | ~150 lines | ~350 lines |
| New types | 1 (`LiveSession`) | 2 (`SessionKey`, `PTYSource`) | 5 (`TerminalHost`, `TerminalConn`, `SessionKey`, `LocalPTYHost`, `sessionRegistry`) + `ByteRingBuffer` |
| `ws.go` churn in Part 1 | low | medium (rewritten, but simpler) | medium (full refactor) |
| `ws.go` churn in Part 2 | high — full rewrite to add AgentdClient | zero — new `PTYSource` impl plugs in | zero — new `TerminalHost` impl plugs in |
| Test surface change | medium (two-map invariants) | small (idle-reap, replay) | large (3 new mockable types) |
| Reviewer load | low | low | high |

## Recommendation: **Option C (Pragmatic)**

- Option A loses Part 2 readiness and still requires a new `LiveSession` type, fan-out channels,
  and a drain goroutine. The claimed savings evaporate when Part 2 lands.
- Option B adds types that carry no Part 1 behaviour. `SessionRegistry`, `TerminalConn`,
  `TerminalHost` only pay off when there's a second implementation. Introducing them today
  forces reviewers to evaluate a seam before the feature justifying it exists.
- Option C introduces precisely the two things that are useful right now (`SessionKey` for
  typed identity; `PTYSource` for Part 2 swap-in) and pushes everything else into mutations of
  code paths that already exist.

### Tests for "is this the right middle"

| Question | Answer under C |
|---|---|
| Can Part 2 add `AgentdClient` without touching `ws.go`? | Yes — implement `PTYSource` and pass it to `terminalWSParams`. |
| Does every new type carry Part 1 behaviour? | Yes — `SessionKey` is the reconnect key; `PTYSource` is the handler's only backend view. |
| Could a simpler solution work? | Only by giving up Part 2 readiness (Option A). |

## Part 2 delta under Option C

**Stable (written once now):**
- `ws.go` body.
- `SessionKey` identity.
- `ScrollbackBuffer` + byte-cap.
- Two-context split.

**Changes in Part 2:**
- New `internal/webui/terminal/agentd_client.go` — implements `PTYSource` over gRPC-over-vsock.
  Wraps the bidirectional stream as `io.ReadWriter` exposed through a `*TerminalSession`-shaped
  facade. `ScheduleKill` talks to the microVM's kill endpoint.
- `terminalWSParams.manager` field type changes from `*TerminalManager` to `PTYSource` — one-line
  change (already done in Part 1 as part of Option C).
- Factory in the server-app init path that selects `LocalPTYHost` vs `AgentdClient` per agent
  kind (read from Redis).

No gRPC, vsock, or Firecracker import touches `ws.go`, `manager.go`, or `lifecycle.go`.

## Implementation plan under Option C

1. Add `SessionKey` and `PTYSource` interface; make `*TerminalManager` satisfy it.
2. Convert `ScrollbackBuffer` to byte-capped ring; expose `Bytes()` for replay.
3. Add `lastOutputAt` atomic on `TerminalSession` (set by `PtyToWS`).
4. Extend `ScheduleKill`'s deferred goroutine with idle-reap check (`time.Since(lastOutputAt) > 30*time.Minute` when `!hasActiveConnections`).
5. Rewrite `runTerminalRelay` with two-context split, replay emission, `ScheduleKill(60s)` on detach.
6. Bump `defaultMaxTerminalSessions = 40`.
7. Wire `DELETE /terminal/session?session=…` (or surface existing `?force=true` path via the UI on tab close).
8. Tests:
   - Unit: `ScrollbackBuffer` byte-cap; `ScheduleKill` + idle-reap; `SessionCount` includes detached.
   - Integration: page-refresh preserves session (WS close → WS reconnect within 5s → replay
     contains prior output); grace-period expiry kills; explicit DELETE kills immediately.

Estimated: ~2 days including tests and a dev-env smoke via `agent-browser`.
