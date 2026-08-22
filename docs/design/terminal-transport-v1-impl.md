# Terminal transport v1 — Phase 1 implementation spec

Implements Phase 1 of `terminal-state-lifecycle.md` (D1, D5, D6, D7, D8; the
interim `InitialState` of §7 Phase 1). Emulator-agnostic: the existing
`ringBuffer` remains the interim state source. No libghostty, no archives.

Baseline: `origin/v5` + docs branch (`098e0419a`). Branch: `feat/terminal-transport-v1`.

## Goals

1. Attach is one atomic owner operation: initial state labelled `N`, then a
   contiguous live stream from `N+1`. No duplicate, gap, or reorder.
2. Slow viewers are disconnected (close code 4003) instead of silently losing
   frames.
3. One geometry/input controller per session (most-recently-focused viewer).
4. Versioned binary WebSocket protocol `loom-terminal.v1` on the direct-PTY
   endpoint `GET /api/workspaces/{ws}/terminal/ws`. Hard cut.
5. Browser resets xterm exactly when `initial_state` arrives; pins generation.

## Non-goals

No changes to the auto-mode tmux endpoint (`/agents/{name}/terminal/ws`,
`agent.go`, `agent_tmux.go`, `realtime.PtyToWS`) — it keeps the raw protocol.
No change to `ringbuf.go` replay content. No OpenAPI changes (WS is not in the
spec). No libghostty.

## Wire protocol `loom-terminal.v1`

Negotiated as a WebSocket subprotocol. Server rejects (HTTP 426 before
upgrade, or close 1002 after) clients that do not offer it; the client
refuses to proceed if the server does not select it. All frames are binary
WebSocket messages. All integers big-endian.

```text
Header (28 bytes):
  magic      u16   0x4C54  ("LT")
  version    u8    1
  kind       u8    see below
  generation [16]byte
  sequence   u64   server frames: event sequence; initial_state: N; client frames: 0
Payload: kind-specific, rest of message.

Server → client kinds
  0x01 initial_state  payload: cols u16, rows u16, retained_lines u32,
                               encoding_len u8, encoding bytes ("xterm-vt/1"),
                               data bytes (may be empty)
  0x02 output         payload: raw VT bytes
  0x03 resize         payload: cols u16, rows u16   (canonical geometry changed)
  0x04 notice         payload: UTF-8 JSON {"code":string,"message":string}
  0x05 close          payload: UTF-8 reason (informational; WS close code is authoritative)

Client → server kinds
  0x81 input          payload: raw bytes for the PTY
  0x82 resize_request payload: cols u16, rows u16
  0x83 focus          payload: empty
```

Rules:
- The first server frame after upgrade is always `initial_state` (even with
  empty data). Its `sequence` is `N` = the last applied event sequence; every
  following frame has sequence `N+1, N+2, …` contiguously.
- `generation` is 16 random bytes chosen when the PTY process is spawned. All
  frames of a session carry it. The client pins it from `initial_state` and
  reconnects if any later frame differs.
- Client frames must carry the pinned generation; the server ignores client
  frames with a mismatched generation (logs at debug).
- `sequence` increments only for server→client `output`, `resize`, `notice`,
  `close`. Input, focus, attach, and snapshot do not consume sequence numbers.
- `resize_request` from a non-controller is stored but not applied. `focus`
  makes the sender the controller and, if its stored dimensions differ from
  canonical, emits a `resize` event and resizes the PTY.
- `input` from a non-controller is dropped (debug log). Backend-owned
  `WriteToSession` is an independent trusted source.

WebSocket close codes (server-initiated):
- 4001 backend exited (existing), 4002 session killed (existing),
- **4003** `slow consumer; resnapshot required` — client reconnects
  immediately (jittered ≤ 250 ms), expecting a fresh `initial_state`.
- **4004** `state rebuilding; retry` — reserved for Phase 2; client treats
  like 4003 with backoff.
- 1000 normal — only when the session ended benignly (as today).
- 1002 protocol error — malformed frame, wrong magic/version, or missing
  subprotocol after upgrade.

## Go: `internal/webui/terminal`

### Types (source.go)

```go
type Generation [16]byte

type EventKind uint8
const (
    EventOutput EventKind = 1
    EventResize EventKind = 2
    EventNotice EventKind = 3
    EventClose  EventKind = 4
)

type TerminalEvent struct {
    Sequence   uint64
    Kind       EventKind
    Data       []byte  // Output: VT bytes; Notice: JSON; Close: reason
    Cols, Rows uint16  // Resize only
}

type TerminalInitialState struct {
    Generation    Generation
    Sequence      uint64 // N
    Cols, Rows    uint16
    RetainedLines uint32 // 0 in Phase 1 (ring has no line count)
    Encoding      string // "xterm-vt/1"
    Data          []byte
}

type CloseReason string
const (
    CloseExited        CloseReason = "exited"
    CloseKilled        CloseReason = "killed"
    CloseShutdown      CloseReason = "shutdown"
    CloseSlowConsumer  CloseReason = "slow_consumer"
    CloseReplaced      CloseReason = "replaced"
    CloseStateRebuild  CloseReason = "state_rebuilding" // Phase 2
)

type Attachment interface {
    ConnID() string
    InitialState() TerminalInitialState
    Output() <-chan TerminalEvent       // contiguous, strictly after InitialState.Sequence; closed on detach/close
    WriteInput(p []byte) (int, error)   // accepted only while this attachment is the controller
    RequestResize(cols, rows uint16) error
    Focus() error
    CloseReason() CloseReason           // valid after Output() is closed
}
```

Keep the existing `ExitReason*` string constants as aliases of the matching
`CloseReason` values so the service layer compiles; `Scrollback()` is removed.
Keep `realtime.Resizer`/`AttachmentExitReader` compiling via small adapters
in the handler package if needed, or update `realtime` minimally.

### Owner loop (pty_session.go, new file owner.go if needed for LOC limits)

Per `ptySession`:

- `reader` goroutine: `pty.Read` into 4 KiB buffers → sends `outputChunk` to
  the owner's command channel (blocking send — the reader is the only thing
  that may block on the owner).
- `owner` goroutine: single `select` loop over the command channel and
  `done`. Commands: `outputChunk`, `injectOutput` (server-generated
  terminal-visible bytes), `attach`, `detach`, `input`, `resizeRequest`,
  `focus`, `close(reason)`. Each command is applied fully before the next.
- State: `seq uint64`, `generation`, `cols,rows` (canonical), `controller
  connID`, `subs map[connID]*subscriber` (each with `dims`, `attachedAt`,
  queue), `ring *ringBuffer`.
- `outputChunk`/`injectOutput`: `seq++`, `ring.Append`, fan out
  `TerminalEvent{Output}` to every subscriber.
- `attach(connID, cols, rows)`: build `InitialState{Sequence: seq, Data:
  checkpoint+screenResetSeq+body from ring}`, register subscriber (dims
  stored; if no controller yet, becomes controller and PTY is sized to its
  dims with a sequenced `Resize` event), reply on a result channel. The
  initial state and the subscriber registration happen in the same command
  — nothing can interleave.
- `detach(connID)`: remove subscriber, close its channel with
  `CloseReplaced`/benign; if it was the controller, hand control to the most
  recently attached remaining subscriber and apply its dims (sequenced
  `Resize`).
- `resizeRequest(connID, cols, rows)`: store dims; if controller and dims ≠
  canonical → enqueue `pty.Setsize` on the writer FIFO, set canonical,
  `seq++`, fan out `Resize`.
- `focus(connID)`: set controller; apply its dims as above if they differ.
- `input(connID, bytes)`: if controller → enqueue on writer FIFO; else drop.
- `writer` goroutine: consumes an owner-ordered bounded FIFO (256 KiB) of
  `{input bytes | setsize}` items and performs the blocking `pty.Write` /
  `pty.Setsize`. Overflow drops the input item and emits a `Notice`
  `{"code":"input_dropped"}`. This keeps a blocked PTY writer from stalling
  output, fan-out, or attach.
- `close(reason)`: close all subscribers with reason, close PTY, kill child
  (existing semantics), stop goroutines.

Subscriber queue: a channel plus a byte counter; fan-out is non-blocking; if
`queuedBytes + len(event.Data) > 256 KiB` the owner closes that subscriber
with `CloseSlowConsumer` (channel closed, reason set) and removes it. Bytes
are decremented when the handler dequeues (use a small wrapper so the
handler's receive path updates the counter; simplest: the subscriber's
`Output()` returns a channel fed by a per-subscriber pump goroutine that
owns the byte accounting — choose the simplest correct design and document
it).

`PTYSource.AttachSession(key, cols, rows, launch)` keeps its signature;
`Detach`, `Kill`, etc. unchanged. `PTYCommandRunner.WriteToSession` routes
through the owner as trusted input (bypasses controller check).

### Tests (Go)

- Contract test `TestAttachmentContract` in `internal/webui/terminal`: with a
  fake PTY (pipe), attach while output is streaming; assert the initial state
  plus live events reconstruct the exact byte stream with no duplicate or
  gap (compare against bytes written to the fake PTY), for 1000 randomized
  attach timings. Run under `-race`.
- Slow consumer: subscriber that never reads → closed with
  `CloseSlowConsumer`, other subscribers unaffected, PTY reader never blocks.
- Controller: two attachments; only the focused one's resize applies;
  non-controller input dropped; focus hand-off on detach applies the new
  controller's dims.
- Delete the drop-frames test (`pty_manager_test.go:417-451` region) and fix
  any test relying on `Scrollback()`.

## Go: handler + protocol codec

- New package `internal/webui/terminal/proto` (pure encode/decode of the
  frame header and payloads, with table-driven tests including malformed
  input). No websocket dependency.
- `ws.go`: negotiate subprotocol (`websocket.AcceptOptions.Subprotocols`),
  reject if not selected. Replace `att.Scrollback()` write with
  `initial_state` frame; on write error → close 1011 and detach (never fall
  through). Pump: `TerminalEvent` → frames; on `Output()` closed map
  `CloseReason` → WS close code (exited 4001, killed 4002, shutdown 1001,
  slow_consumer 4003, state_rebuilding 4004, replaced 1000). Client read
  loop: decode frames; `input` → `att.WriteInput`; `resize_request` →
  `att.RequestResize` (bounds-check ≤ 500×200); `focus` → `att.Focus()`;
  malformed → close 1002.
- `maybeEmitStaleRestartBanner` and `injectTerminalContextBanner` become
  owner `injectOutput` (terminal-visible, sequenced) — expose via a small
  optional interface on the manager (`OutputInjector`), not on `Attachment`.
  Note: `injectTerminalContextBanner` today writes the banner as PTY
  *input*; verify what the banner is and preserve its user-visible effect.
  If it is meant to be typed into the shell, keep it as trusted input via
  `WriteToSession`; if it is display-only, make it `injectOutput`.
- `realtime.WSToPTY`/`AttachmentToWS` remain for the tmux path; add new
  functions (or a new file) for the v1 relay rather than changing their
  behaviour.

## Frontend (`internal/webui/frontend`)

- New `src/components/TerminalView/instances/terminalProtocol.ts`: TS codec
  mirroring `proto` (encode client frames, decode server frames), unit tests
  with the same vectors as the Go tests (copy the hex vectors).
- `terminalConnection.ts`: `new WebSocket(url, ["loom-terminal.v1"])`;
  verify `ws.protocol === "loom-terminal.v1"` on open else close 1002 and
  surface `error`. On `initial_state`: call `onInitialState({cols, rows})`,
  then `await renderer.reset()`, then `write(data)`. **`terminal.reset()`
  does NOT clear xterm's internal write queue** (verified on
  `@xterm/xterm@6.0.0`: `CoreTerminal.reset()` never touches `_writeBuffer`),
  so bytes from the previous generation still queued in xterm would be parsed
  *after* a synchronous reset — exactly on the 4003 path where a backlog
  exists by construction. Therefore the renderer's `reset()` must be
  asynchronous: `new Promise(r => terminal.write("", () => { terminal.reset();
  r(); }))` — the empty write's callback fires only after everything queued
  before it has been parsed, so the reset discards it. While the reset is
  pending, the connection must hold every frame received after
  `initial_state` (in order) and flush them only after the snapshot `write`.
  Pin generation; mismatch → reconnect. `output` → write. Track
  `expectedSequence`: a gap or duplicate in server sequence numbers is
  treated like 4003 (immediate resnapshot reconnect) and logged.
  Client input sent before `initial_state` must not be dropped: either queue
  it until the generation is pinned or keep the UI state `connecting` until
  `initial_state` (and queue). Flush order after `initial_state`: pending
  `focus` first (so this viewer is the controller), then `resize_request`,
  then queued input. A throwing `new WebSocket` must transition to
  `disconnected` with backoff, never hang in `connecting`. A failed token
  fetch is NOT fatal: the server supports token-less attach when terminal
  auth is disabled (`ws.go` `auth == nil`), so keep connecting without a
  token as before.
  `resize` → renderer `setSize(cols, rows)` (non-controller view). `notice`
  → callback. Close 4003/4004 → `disconnected` + immediate jittered reconnect
  (4004 with backoff). Replace `encodeResize` string protocol with
  `resize_request` frames; send `focus` when the xterm gains focus.
- `XTermRenderer.tsx`: add `reset(): Promise<void>` (drain-then-reset, see above) and `setSize(cols, rows)` to the handle.
- Keep `TERMINAL_SCROLLBACK_LINES` for now (Phase 2 removes it).
- Update `terminalConnection.test.ts` / `TerminalInstance.test.tsx`.

## Slices and acceptance

| Slice | Scope | Acceptance |
|---|---|---|
| A | `internal/webui/terminal` owner loop, types, queues, controller, tests | `go build ./...`, `go test -race ./internal/webui/...`, `make lint`, `make check-loc` |
| B | `proto` package, `ws.go` relay, close codes, banners | same + `make gate` Go side |
| C | frontend codec, connection, renderer, tests | `npm run lint`, `npm run test:unit`, `npm run check:arch`, `npm run build` |

Each slice must leave the tree building and all tests green (temporary
adapters are fine between A and B). Respect `scripts/check-loc.sh` (≤1000
lines per source file, ≤2000 per test file): split files rather than exceed.
No raw `exec` (`make check-no-raw-exec`).
