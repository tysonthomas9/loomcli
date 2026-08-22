# Terminal State Lifecycle: Reconnect and Restore

Status: **proposal, revision 3** (2026-08-22). Produced from the revision-1
draft after independent vetting by two reviewers (Claude and Codex) against
the pinned source, with four rounds of structured disagreement resolved in
§8. No implementation is included.

Source baseline: `origin/v5` at `024bb68259a0610b78a994cfc7d8de6310d695cb`.
Line references are pinned to that commit and will drift after edits.

## Decision summary

| # | Decision | Status |
|---|---|---|
| D1 | Fix the attach/live ordering bug and slow-viewer loss **first**, as a transport-correctness change against the existing ring (Phase 1). It does not claim the ring is a valid snapshot. | Agreed |
| D2 | The long-term state owner is **libghostty-vt embedded in-process as WASM under wazero**, one isolated module instance per session. It keeps loom a single `CGO_ENABLED=0` binary. It must pass every Phase 0 gate before production code depends on it. | Agreed, gated |
| D3 | If WASM passes fidelity but misses a resource gate, first attribute the miss with an A/B native-versus-WASM prototype. Use the same library via **cgo** (`mitchellh/go-libghostty`) only when the excess is demonstrably caused by wazero/WASM rather than libghostty's state model, and only after cgo passes every fidelity, four-target packaging, installed-desktop, and process-failure gate; this changes the `CGO_ENABLED=0` release invariant and loses WASM trap containment. **Fidelity** failure falls back to a packaged **Node worker** (`@xterm/headless` + `@xterm/addon-serialize`); supplementary state must come from supported xterm state or a maintained serializer fork, never a parallel raw-stream parser. tmux is reserved for restart survival (D11), not for this problem. | Agreed after §8.1 |
| D4 | Initial state is sent to the browser as **VT output from the libghostty formatter plus the parser continuation suffix**, written into the existing `@xterm/xterm` after an explicit reset. libghostty's binary `GHOSTSNP` snapshot is used **only** for same-version in-process checkpoints, never on the wire or as a durable format (its v1 format carries no compatibility guarantee). | Agreed |
| D5 | One **owner goroutine per session** serializes PTY output, resize, input, focus/controller changes, attach cuts, snapshots, and synthetic output. `Sequence` increments only for server→browser events (`Output`, `Resize`, `Notice`, `Close`); input, focus, attach, and snapshot are ordered owner commands that do not consume wire sequence numbers. | Agreed |
| D6 | Canonical geometry and all browser-originated PTY traffic belong to the **most-recently-focused viewer** (the controller); on its disconnect, control passes to the most recently attached remaining viewer, whose stored dimensions are applied immediately. Non-controllers are read-only and render at canonical size. | Agreed |
| D7 | Slow viewers are **disconnected** with close code 4003 when their byte-accounted live queue exceeds 256 KiB; the browser reconnects and gets a fresh snapshot. Silent frame loss is removed. | Agreed |
| D8 | The WebSocket protocol becomes a **versioned, directional binary envelope** negotiated as subprotocol `loom-terminal.v1`; a hard cut on the direct-PTY endpoint. | Agreed |
| D9 | Server-side retention is defined by a **per-session memory budget**, not a line count: libghostty's `GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES` is set from the budget, and the browser's independent 10,000-line cap (`TERMINAL_SCROLLBACK_LINES`) is **removed** so the server is the single source of truth for what a reconnect restores. Budget: ≤ 8 MiB incremental RSS per session at 160×50 and ≤ 16 MiB at 500×200, inclusive of checkpoint and journal; encoded initial state ≤ 8 MiB; restore latency gated separately. Effective retained lines are reported as diagnostics, never promised. | **Ratified by owner 2026-08-22** |
| D10 | **Archives are cut from v1.** There is no production archive writer today. A v2 sketch is kept in §10. | Agreed |
| D11 | **Server-restart survival is out of scope** for v1. PTYs die with `loom serve` today. tmux is the documented path if it becomes a hard requirement. | Agreed |
| D12 | The auto-mode agent viewer (`agent_tmux.go`) keeps its separate raw protocol and is **untouched** in v1. | Agreed |
| D13 | Only **detach/reconnect** and **process end** are v1 lifecycle operations. Freeze-display catch-up and execution suspend/resume are future concepts, not v1 commitments. | Agreed |

Do not extend the regular-expression-based VT checkpointing in `ringbuf.go`
into a home-grown emulator under any outcome.

## 1. Problem and observed evidence

On 2026-08-22, a reconnect to a still-live Loom terminal produced a mostly
blank view containing stray `[K` text. A read-only WebSocket capture showed:

```text
frameBytes:             262173
modeCheckpointBytes:         22
screenResetBytes:              7
retainedRingBytes:        262144
bodyStartsWithLiteral[K:     true
bodyStartsWithESC[K:        false
```

The arithmetic is exact: `22 + 7 + 262144 = 262173` and matches the replay
assembly in `pty_session.go:206-213`. The process and PTY were alive. The ring
had evicted the leading ESC byte of an `ESC [ K` erase command, so the fresh
browser received the suffix `[K` as visible text. Both reviewers confirmed the
mechanism: `advanceHeadLocked` (`ringbuf.go:173-202`) repairs eviction cuts
only through DECSET/DECRST sequences matched by `privateModePattern`
(`ringbuf.go:21`); every other sequence can be cut.

This is one instance of a class. A raw byte suffix can also begin inside a
UTF-8 code point, a CSI/OSC/DCS/APC string, a cursor/colour/tab-stop/scroll-
region/charset change, a buffer transition not in the checkpoint, or a
synchronized-output region. A larger ring lowers the frequency; it does not
make an arbitrary byte suffix a valid terminal snapshot.

Two further correctness gaps exist independent of the ring:

1. **No ordering barrier at attach.** `attachNew` registers the attachment
   (`pty_session.go:192-204`) and *then* takes `ReplaySnapshot()` (`:207`).
   `drain` appends to the ring (`:149`) and *then* fans out (`:158-163`). A
   chunk that lands between registration and snapshot is delivered twice. The
   handler writes the replay and only afterwards starts the live pump
   (`ws.go:320-334`).
2. **Silent loss for slow viewers.** `attachmentState.send` is a non-blocking
   send into a 64-slot channel of 4 KiB reads (`pty_session.go:305-315`,
   `terminal_relay.go:46`); overflow drops frames with no signal. The loss is
   intentional and codified by `pty_manager_test.go:417-451`. A replay write
   failure is logged and ignored (`ws.go:322-324`), leaving a permanent gap.

## 2. Current architecture (verified)

Browser: `package.json:63-64` declares `@xterm/xterm ^6.0.0` and
`@xterm/addon-fit ^0.11.0`; `package-lock.json:3665-3678` pins 6.0.0 and
0.11.0. The terminal is constructed in `XTermRenderer.tsx:107-115` with a
10,000-line scrollback (`:22`). The renderer persists across reconnects
(`TerminalInstance.tsx:263-354`), its handle exposes no `reset`
(`XTermRenderer.tsx:15-20`), every xterm emits resize (`:164-166`), and the WS
client treats every frame as raw terminal bytes (`terminalConnection.ts:206-227`)
and a normal close as session termination (`:229-256`).

Server: `PTYManager`/`MultiPTYManager` own PTYs per `(workspace, session)`
behind the `PTYSource` seam (`source.go:20-66`) and hand the handler an
`Attachment` (`source.go:81-108`). `localAttachment.Resize` ignores `connID`
(`pty_session.go:62-64`), so geometry is last-writer-wins across viewers.
`WriteInput` relies on a `PIPE_BUF` atomicity claim (`:56-60`) that applies to
pipes, not tty devices. `PTYCommandRunner` (`source.go:72-75`) can start
sessions with no viewer attached. `defaultGracePeriod = 0`
(`pty_manager.go:48-57`) keeps detached PTYs alive while the server process
lives; it provides no restart survival.

Facts that correct the revision-1 draft:

- The live `/api/workspaces/{ws}/terminal/sessions/{session}/scrollback`
  endpoint in `openapi.yaml:1797` is **stale**: it is not registered
  (`tab_module.go:24-41`) and the frontend records its removal
  (`terminal.ts:1-7`). There is no second consumer of ring bytes.
- `sessionhistory.Store` has **no production writer**; only
  `ListSessionHistory`/`GetSessionScrollback` read it
  (`session_service.go:850-896`). `ScrollbackPath` is never set in production.
  `Add`/`Complete` are non-transactional Redis read-modify-write
  (`store.go:79-95,122-150`); the history endpoint's OpenAPI says `text/plain`
  while the handler returns JSON (`openapi.yaml:1285-1308`,
  `session_history.go:30-49`); `scrollback_path` exposes filesystem paths
  guarded by a lexical prefix check that follows symlinks
  (`session_service.go:905-941`). This is cleanup for v2, not an archive
  pipeline.
- `internal/sessions.Store` owns the canonical session directory, metadata,
  prompt, and native transcript (`store.go:21-35`); `AppendTranscript` assigns
  its transcript sequence under the transcript flock (`:145-176`). The sibling
  `internal/sessions/eventstore` owns `events.jsonl` and preserves harness-
  supplied event sequences (`eventstore.go:35-48,106-129`). Any future terminal
  artifact must be rooted in this directory after identity mapping.
- tmux is **not** gone from v5: `agent_tmux.go:1-13` attaches the web UI to
  auto-mode agent sessions created by `internal/cli/automode/automode_tmux*.go`;
  it is optional (`server_app.go:191-212`) and isolated from the direct-PTY
  path. The "read-only" agent handler in fact runs the bidirectional pump
  (`agent.go:140-145,337-344`), and every connection calls `resize-window`
  (`agent_tmux.go:324-343`). None of this makes tmux "already paid for" as a
  general state owner.
- Release shape: `.goreleaser.yml:11-22` defines `CGO_ENABLED=0` cross-builds
  for linux/darwin × amd64/arm64; the repo contains no cgo. Desktop
  (`tauri.conf.json:29-35`) bundles `loom` and `fleet-db` only — no Node.
- `agentd` does not exist in this repository; it appears only in comments
  (`source.go:14`, `pty_manager.go:53`). Remote placement is designed for via
  `PTYSource`, not built.

## 3. Required semantics

### 3.1 The state handoff invariant

One owner goroutine per session serializes PTY output, resize, input,
focus/controller changes, attach cuts, snapshots, and synthetic output.
`Sequence` increments only for server→browser events (`Output`, `Resize`,
`Notice`, `Close`). Input, focus, attach, and snapshot are ordered owner
commands that do not consume wire sequence numbers, so a correct browser never
sees a gap. Attaching a viewer is one owner command:

1. Apply all events through sequence `N` to the state owner.
2. Produce the initial state labelled `N`.
3. Register the subscriber for events `> N`.
4. Send the initial state, then a contiguous stream beginning at `N + 1`.

No gap, duplicate, or reordering across the cut. The owner loop is the fix for
gap 1 in §1; a mutex around `attachNew` alone is insufficient because `drain`
would still append before the subscriber set is chosen.

Input goes through the same loop: a mutex on the fd would prevent byte
interleaving but not establish order against resize or give one auditable
sequence. Phase 0's latency gate keeps this acceptable.

### 3.2 Public seam

`PTYSource` stays placement-agnostic. `Attachment` loses the ambiguous
`Scrollback()` and gains an atomic initial state:

```go
type Generation [16]byte // new per PTY process; emulator rebuilds keep it

type TerminalInitialState struct {
    Generation Generation
    Sequence   uint64 // last applied event sequence N
    Cols, Rows uint16 // canonical geometry at N
    RetainedLines uint32 // history lines in Data at this geometry; sizes browser scrollback (D9)
    Encoding   string // "xterm-vt/1"
    Data       []byte // VT bytes incl. parser continuation; may be empty
}

type TerminalEvent struct {
    Sequence   uint64
    Kind       EventKind // Output | Resize | Notice | Close
    Data       []byte    // Output: VT bytes; Notice: UTF-8 status; Close: reason
    Cols, Rows uint16    // Resize only
}

type Attachment interface {
    ConnID() string
    InitialState() TerminalInitialState
    Output() <-chan TerminalEvent // contiguous, strictly after InitialState.Sequence
    WriteInput(p []byte) (int, error)               // accepted only from the controller
    RequestResize(cols, rows uint16) error          // stored for every viewer; applied for the controller
    Focus() error                                   // makes this viewer the controller
    Closed() <-chan struct{}
    CloseReason() CloseReason // ExitKilled | ExitExited | ExitShutdown | SlowConsumer | Replaced | StateRebuilding
}
```

The invariant appears in the interface documentation and in a contract test
that `localAttachment` and any future remote implementation must pass.

### 3.3 Slow viewers

Each subscriber has a byte-accounted live queue capped at 256 KiB
(configurable within a bounded range; the initial-state blob is capped
separately at 8 MiB and exempt). Overflow closes that subscriber with
`CloseReason = SlowConsumer`; the handler closes the WebSocket with code
**4003** / `slow consumer; resnapshot required`. The browser reconnects
immediately with jitter and receives a new snapshot. The PTY, state owner, and
other viewers are never back-pressured.

### 3.4 Canonical geometry and input ownership

A PTY has one size and one writer policy. The **most-recently-focused viewer**
is the controller. The controller owns all browser-originated PTY traffic —
input, resize, and terminal-query replies (DA/DSR/OSC responses that every
xterm instance would otherwise answer, producing duplicate replies). Non-
controller viewers are read-only and render at canonical size; focusing one
makes it the controller. The owner tracks every viewer's last reported
dimensions; on focus hand-off or controller disconnect it sequences and
applies a resize to the new controller's stored dimensions rather than waiting
for another `ResizeObserver` callback. Backend-owned `WriteToSession` remains
an independent trusted input source serialized by the same owner.

### 3.5 Wire protocol

WebSocket subprotocol `loom-terminal.v1`. Frames are a compact binary
envelope: magic/version, `kind`, 16-byte generation, `uint64` sequence,
payload length, payload. The protocol is directional:

- Server kinds: `initial_state`, `output`, `resize`, `notice`, `close`.
- Client kinds: `input`, `resize_request`, `focus`. The owner assigns their
  ordering when it accepts them.

The first frame after attach is always `initial_state`, even when empty, so
the browser knows exactly when to `terminal.reset()` and apply the snapshot,
synchronized with xterm's asynchronous write queue. The browser pins the
generation from `initial_state`, discards pending writes from any prior
generation, and reconnects on any later frame with a different generation.
Emulator rebuild keeps the PTY generation and sequence; only creation of a new
PTY process creates a new generation and resets the sequence.

`Notice` is structured UTF-8 status for browser chrome and is never written
into xterm or libghostty. Any server-generated content intended to appear
inside the terminal (stale-session banner, talk-to-lead context banner) is
encoded as `Output`, applied to libghostty, and delivered like PTY output, so
it survives in later snapshots. A `close` frame carries the exit reason; the
WebSocket close code is the authoritative signal if the frame cannot be
delivered.

Hard cut: servers reject clients that do not offer the subprotocol and vice
versa. Replay-write failure aborts the attach; it never falls through to live
output. The auto-mode tmux endpoint keeps its raw protocol (D12).

## 4. Lifecycle model

v1 lifecycle operations are **detach/reconnect** and **process end** (D13).

| User operation | Process runs? | State owner runs? | On return |
|---|---:|---:|---|
| Detach viewing | Yes | Yes | Snapshot now, then live output |
| Process end | No | Finalized | `close` frame; ended session cannot be resumed |

```text
live_attached --detach--> live_detached --attach(snapshot)--> live_attached
       |
       +----process exit / kill / shutdown-----> ended
```

- **Detach viewing** is tab close, navigation, sleep, network loss, reconnect.
  The PTY, owner loop, and state owner keep running. Resume is a new atomic
  snapshot plus live events; the browser does not need every byte emitted
  while detached.
- **End.** When the child exits or is killed the session transitions to
  `ended` and the browser receives a `close` frame with the reason. An ended
  session cannot resurrect the OS process. Backend-level conversation resume
  (Claude/Codex) is a **new** session linked by metadata.
- Neither an emulator snapshot nor a tmux pane keeps a process alive across a
  `loom serve` restart unless the PTY owner outlives the web server (D11).

**Future work — not v1:** freeze-display with catch-up from a recorded event
window, and audited execution suspend/resume (`SIGSTOP`/`SIGCONT` on the
process group). Both need separate backlog, authorization, signal-delivery,
lease, and watchdog designs. The supervisor's existing freeze detection
(`liveness.go:30-39,137-173`) concerns suspension of the Loom supervisor
process itself, not a child PTY process group, and is not reusable here.
Archived viewing is v2 (§10).

## 5. State-owner evaluation

Criteria, in order: (1) restoration fidelity for Claude/Codex TUIs — both
buffers, cursor position/visibility/shape, SGR, mouse tracking *and* encoding,
margins, tabs, charsets, OSC 8, parser continuation, resize history; (2) a
single ordering domain with the PTY owner; (3) release fit with a
`CGO_ENABLED=0` single binary and a Tauri bundle with no external runtime;
(4) failure isolation, resource bounds, versionability; (5) whether
server-restart survival is required (it is not, for v1).

| Rank | Candidate | Fidelity | Ordering | Release fit | Verdict |
|---|---|---|---|---|---|
| 1 | **libghostty-vt, WASM under wazero, in-process** | Formatter emits modes, cursor, hyperlink (OSC 8), scrolling region, tabstops, charsets, kitty keyboard, palette; continuation API exports unfinished parser input as replayable bytes | Same goroutine as PTY owner | Pure Go, single binary, no runtime dep; wasm blob `go:embed`-ed | **Choose, gated by Phase 0** |
| 2 | libghostty-vt via cgo (`go-libghostty`) | Same | Same | Breaks `CGO_ENABLED=0`; Zig cross-compile in release; loses WASM trap containment | Resource-failure fallback (D3) |
| 3 | `@xterm/headless` + `addon-serialize` in a packaged Node worker | Parser family matches the browser; but `serialize()` at 0.14.0 omits `?25`, `?1005/1006/1015/1016`, OSC 8, DECSCUSR, title, DECSTBM, tabs, charsets — the first two regress vs `ringbuf.go:230,233` | Cross-process IPC; barrier waits for worker ack | Requires bundling a Node runtime in GoReleaser and Tauri, supervision, health checks | Fidelity-failure fallback (D3) |
| 4 | tmux pane ownership | Redraw is a valid screen, but `capture-pane -e` is not a full state export; TERM/terminfo sit between app and browser | tmux owns the PTY; Loom becomes a client | Host dependency everywhere; geometry last-writer-wins today | Only if D11 flips |
| — | Pure-Go emulators (`charmbracelet/x/vt`, `vito/midterm`, `hinshun/vt10x`) | None currently demonstrates a complete, versioned full-state serializer for modern TUIs; `charmbracelet/x` is labelled experimental, `vt10x` too limited | In-process | Ideal | Re-evaluate if one gains a complete serializer |

What would change the ranking: making restart survival mandatory (tmux first);
libghostty failing the differential corpus or budgets (D3); a Go emulator
gaining a complete serializer (pure Go first).

### 5.1 libghostty-vt facts relied upon (verified 2026-08-22, `main`)

- `terminal.h`: `ghostty_terminal_new/free`, `ghostty_terminal_vt_write`,
  `ghostty_terminal_resize`, `ghostty_terminal_reset`, `ghostty_terminal_get/set`;
  `GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES` ("maximum scrollback allocation
  in bytes … pruned at page granularity, a page is about 400KB") alongside a
  physical-line limit; caller-driven scrollback compression for idle sessions.
- **Safe cuts and continuation** (`terminal.h`): `ghostty_terminal_vt_write_until_ground`
  writes "only the shortest prefix needed to reach ground … the stateless point
  of the stream" and reports bytes consumed. `GHOSTTY_TERMINAL_OPT_CONTINUATION_MAX_BYTES`
  enables tracking of "replay-safe VT continuation bytes [that] reconstruct an
  escape sequence or UTF-8 codepoint which was unfinished at the end of the
  most recent VT write call … used automatically by terminal snapshots and may
  also be exported directly" via `ghostty_terminal_continuation_write/buf/alloc`.
  This is the mechanism that makes the `[K` class structurally impossible
  without Loom parsing the raw stream itself.
- `formatter.h`: `ghostty_formatter_terminal_new` with `emit = plain | VT |
  HTML`; `GhosttyFormatterTerminalExtra{modes, scrolling_region, tabstops,
  palette, pwd, keyboard, screen{cursor, style, hyperlink, protection,
  kitty_keyboard, charsets}}`. The formatter exposes the **active** screen;
  `style` is the cursor's current SGR rendition, not DECSCUSR cursor shape;
  window title is not an extra.
- `snapshot.h`: `ghostty_snapshot_encode` / `ghostty_snapshot_decoder_*`;
  CRC32C record stream with `CONTINUATION`, `READY`, then `HISTORY` pages
  newest→oldest. "Snapshot format version 1 is a work in progress and does not
  yet carry a binary-compatibility guarantee."
- `vt.h`: "WARNING: This is an incomplete, work-in-progress API. It is not yet
  stable and is definitely going to change." MIT licence.
- Go: `mitchellh/go-libghostty` (cgo, static link, `zig cc` cross-compile;
  "not promising any API stability yet"). WASM precedent: `seruman/hauntty`
  (Go session persistence on libghostty-vt WASM), `neurosnap/zmx`.

Consequences: pin the ghostty commit, Zig toolchain, WASM bytes, and checksum;
build the blob in CI and commit it; changing any of these invalidates in-process
checkpoints (acceptable — they are not durable). Budget re-pin work every
upgrade and keep the differential suite as the gate.

## 6. libghostty-vt embedding design

```text
PTY fd ──read──► owner goroutine (per session)
                    │ seq++ for every server→browser event
                    ├─► libghostty terminal (wazero instance, this session only)
                    ├─► subscriber queues (byte-capped, events > cut)
                    ├─► last-good GHOSTSNP checkpoint + Go-owned journal (Output/Resize after it)
attach ────────────► owner: apply→snapshot(N)→register(N+1)→send
input/resize/focus ► owner: order, apply resize to emulator, write fd (controller only)
```

- **Instance model:** compile the WASM module once at startup; instantiate one
  module/memory per session. Isolated memory bounds blast radius and avoids a
  global mutex; compiled code is shared.
- **Terminal responses and blocked input:** v1 does not install
  `GHOSTTY_TERMINAL_OPT_WRITE_PTY`; libghostty is a passive state mirror and
  the focused browser is the sole terminal-query responder. With no viewer,
  queries remain unanswered, as today. PTY control writes are non-blocking and
  byte-bounded. Partial writes and `EAGAIN` remain in an owner-managed FIFO;
  writable readiness returns to the owner, which performs the next write.
  Resize operations stay ordered behind earlier queued input, and blocked
  input never blocks output parsing, fan-out, or attach snapshots.
- **Snapshot encoder (Loom-owned):** set `GHOSTTY_TERMINAL_OPT_CONTINUATION_MAX_BYTES`
  to 64 KiB before the first PTY byte. When the primary screen is active, emit
  its formatter state (cursor, modes, margins, tabs, charsets, hyperlinks via
  the extras, with alternate-buffer selection **excluded** from generic mode
  replay) and append the exported continuation **last**. When the alternate
  screen is active, obtain a ground-state cut, encode the current terminal to
  GHOSTSNP, decode a temporary same-blob clone, switch only the clone to
  primary (write `?1049l` to the clone) and format it, then emit `?1049h` and
  the live alternate-screen formatter output, leaving alternate active; never
  mutate the live terminal to inspect an inactive screen. DECSCUSR and title
  are restored only if the pinned API exposes them; Loom does not track them
  with a second parser.
  If continuation is unavailable (tracking limit exceeded or enabled late),
  keep the attach pending while the owner processes later output with
  `vt_write_until_ground`. If ground occurs inside a PTY read, emit the
  consumed prefix as one `Output` event, cut the snapshot after that event,
  and emit the remaining bytes as a later event. Bound this wait and fail the
  attach retryably on expiry. Phase 0 fails D2 if either active-buffer case
  does not converge after the suffix.
- **Scrollback:** retention is a byte budget (D9). The owner sets
  `GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES` to the session's scrollback share
  of the budget; libghostty prunes at page granularity (~400 KB), and the
  budget accounting includes that slack, the checkpoint, and the journal.
  libghostty's caller-driven scrollback compression is invoked by the owner
  when the session is idle. `initial_state` carries the server's retained
  line count at current geometry so the browser sizes its xterm `scrollback`
  to fit the restore; the browser holds no history the server cannot
  restore. Phase 0 reports memory at 160×50 and 500×200
  (`terminal_relay.go:47-50` bounds).
- **Recovery:** keep one last-good GHOSTSNP checkpoint, its WASM checksum, and
  a bounded Go-owned journal of `Output` and `Resize` events after its
  sequence. Checkpoint at least every 1 MiB of replayable output and never
  truncate the journal until a newer checkpoint succeeds; checkpoint and
  journal memory count against the session budget. On a wazero trap,
  instantiate a fresh module from the same embedded blob, decode the
  checkpoint, replay only subsequent `Output`/`Resize` (never input), and
  atomically swap it into the owner while retaining generation and sequence.
  During rebuild, reject attaches with retryable close code **4004** /
  `state rebuilding; retry`; never send a `Notice` before `initial_state`. If
  recovery fails, keep the PTY and raw live delivery running but mark correct
  reattach unavailable rather than fabricating state. Arbitrary capped-suffix
  replay is never a recovery path.
- **No viewer from spawn:** the owner loop and emulator start with the PTY
  (`EnsureSession`), not at first attach.
- **Diagnostics:** export queue bytes, overflow count, snapshot size/time,
  checkpoint age, rebuild count, and effective retained lines.

## 7. Implementation sequence

### Phase 0 — packaging and parity proof (gate for D2)

Throwaway vertical slice, no production changes. **All eight must pass:**

1. A recorded Claude/Codex corpus including saturated scrollback, alternate
   buffers, synchronized output, wide/combining text, mouse tracking and SGR
   encoding, bracketed paste, OSC 8, cursor hide/show, DA/DSR/OSC queries, and
   every byte split.
2. For every cut, compare immediately after restore **and again after feeding
   the identical remaining suffix**; exercise ground-state cuts and
   continuation-carrying cuts.
3. Compare both buffers, retained history, cell attributes, cursor
   position/visibility (and shape/title if claimed), active modes, margins,
   tabs, charsets, hyperlinks, and dimensions — not screenshots.
4. Stress owner ordering with output, resize, focus hand-off, backend and
   browser input, a deliberately blocked PTY writer, attach cuts, and
   stale-generation frames; prove exactly one terminal-query reply with
   multiple viewers.
5. Prove overflow→4003→resnapshot and trap→checkpoint/journal rebuild
   convergence, including old xterm writes queued when a new generation
   arrives.
6. Measure peak and post-GC incremental RSS with scrollback saturated to the
   configured byte budget at 160×50 and 500×200, for 1 and 40 sessions,
   including page-granularity slack, checkpoint, journal, formatter output,
   and rebuild transients; enforce the 8 MiB initial-state cap and report
   encoded size and effective retained lines at 80/160/500 columns as
   documentation, not as pass criteria. Budget: ≤ 8 MiB per session at
   160×50, ≤ 16 MiB at 500×200.
7. ≥ 20 MiB/s single-session parsing in 4 KiB chunks; p99 ≤ 2 ms for hot
   owner events; with 40 sessions instantiated and 8 simultaneously parsing,
   aggregate throughput ≥ 80 MiB/s and each active session p99 ≤ 10 ms;
   snapshot generation plus browser application p95 ≤ 1 s.
8. Build and execute smoke tests on native linux/darwin × amd64/arm64, then
   verify an installed Tauri bundle with no external runtime and the embedded
   WASM checksum intact.

Exit: pass → Phase 2 on libghostty/WASM. Resource-only failure → attribute,
then cgo per D3. Fidelity failure → Node worker per D3. Never → extend
`ringbuf.go`.

### Phase 1 — transport correctness (ships first, independent of Phase 0)

- Owner goroutine per session; sequenced server→browser events; atomic attach
  cut (§3.1); input and resize through the owner.
- `Attachment.InitialState()` replaces `Scrollback()`; the existing ring's
  checkpoint+reset+body becomes the interim `Data`, labelled in code as not a
  valid snapshot.
- Byte-capped subscriber queues; 4003 slow-consumer close; replay-write
  failure aborts attach.
- `loom-terminal.v1` envelope and subprotocol; frontend `reset` on
  `initial_state` synchronized with the write queue; generation pinning;
  reconnect on 4003/4004.
- Geometry controller and input ownership (§3.4); `focus` and
  `resize_request` client frames.
- Synthetic banners become `Output` through the owner.
- Tests: contract test for the invariant; delete the drop-frames test
  (`pty_manager_test.go:417-451`).
- UX note: during Phase 1 the server still holds only the 256 KiB ring, so a
  reset-and-restore shows less history than the browser accumulated live.
  This is transitional; Phase 2 makes the server budget the only retention.

### Phase 2 — libghostty state owner

- Embed the pinned WASM blob; per-session instance; Loom snapshot encoder
  (§6); checkpoint + journal; rebuild on trap.
- Shadow mode feeds libghostty beside the production ring but never treats
  the ring replay as an oracle. On sampled cuts, restore formatter output
  into an instrumented xterm oracle and feed the subsequent suffix before
  comparison. Flip only after the Phase 0 corpus remains clean and an
  instrumented real-session soak reports no mismatches, traps, budget
  violations, or unavailable snapshots.
- Remove `ringBuffer` mode parsing after the flip.
- Remove `TERMINAL_SCROLLBACK_LINES` (`XTermRenderer.tsx:22`); size xterm
  `scrollback` from the `initial_state` retained-line hint plus live headroom.

### v2 (not in scope) — archives (§10), freeze/suspend (§4).

## 8. Points contested between reviewers, and their resolution

1. **Phase 0 fallback order (D3).** Codex: fall back to the Node worker,
   because it preserves the direct-PTY architecture and cgo violates the
   `CGO_ENABLED=0` constraint. Claude: `CGO_ENABLED=0` is a release setting,
   not a product requirement; if libghostty passes fidelity but fails only on
   WASM performance/memory, switching the same library to cgo is a build-chain
   change, whereas Node adds a runtime, IPC, and supervision. **Resolved:**
   the split rule in D3, with Codex's conditions — attribute the miss with a
   native-vs-WASM A/B first, re-run every gate under cgo, and record that the
   release invariant and trap containment change.
2. **Server-side scrollback size (D9).** Codex set memory budgets without a
   line count; Claude tied the count to the browser's 10,000 lines so that
   reset-and-restore is not a visible regression. **Resolved by the owner:**
   retention is a memory budget only, the browser's independent line cap is
   removed, and effective line counts are diagnostics. This also removes the
   only case in which the browser could hold history the server cannot
   restore.
3. **Parser continuation.** Codex: formatter output cannot carry unfinished
   parser input, so a cut mid-sequence could pass the immediate comparison
   and diverge when the suffix arrives. **Resolved by evidence:** libghostty
   exposes `vt_write_until_ground` and the `continuation_*` export (§5.1);
   the Loom snapshot encoder appends the continuation bytes and Phase 0
   gate 2 compares after the suffix is fed.
4. **v1 scope.** Codex: freeze/catch-up and suspend/resume are speculative
   and the cited watchdog is about the supervisor, not child process groups.
   **Resolved:** cut from v1 (D13).

## 9. Validation matrix

Primary oracle: differential convergence between a continuously attached
browser xterm and a fresh browser xterm restored from the server snapshot,
compared both immediately and after the remaining suffix is fed.

Required cases: every split position inside representative ANSI/DEC/OSC
sequences and multibyte UTF-8; >256 KiB of output including `ESC[K` across the
old ring edge; normal/alternate buffers with either active at the cut; cursor
visibility and style; colours; hyperlinks; bracketed paste; mouse tracking and
SGR encoding; scroll regions; tab stops; Claude/Codex full-screen and
synchronized-output traffic; resize immediately before, during, and after
attach; output arriving during the cut; multiple viewers with one controller,
focus hand-off, and single query reply; slow-viewer overflow then resnapshot;
emulator trap, rebuild from checkpoint+journal, attach; sessions started by
`EnsureSession` with no viewer; `loom serve` restart with the documented
outcome (session ends); generation mismatch on stale reconnect; WASM blob
re-pin invalidating checkpoints.

Tests compare buffer contents, cursor, active buffer, dimensions, and modes —
not screenshots or plaintext alone.

## 10. v2 sketch: archived terminal artifact (deferred)

Prerequisites: map terminal tabs onto `internal/sessions` identity; fix or
delete `sessionhistory.Store` (non-transactional writes, stale OpenAPI,
path-exposing `scrollback_path`); define retention, quotas, deletion, and
workspace authorization; decide input-capture policy (off by default —
keystrokes carry secrets).

Minimum artifact, written into the session directory under an opaque ID:

```text
terminal/
  metadata.json   # identity, lifecycle, geometry history, TERM, pinned versions, checksums
  events.bin      # canonical: raw output + resize, with seq and timestamps (lossless bytes)
  final.vt        # snapshot-encoder output at exit, for instant static load
  final.txt       # plain-text projection for search/accessibility
  events.cast     # derived asciicast v2 export (UTF-8 JSON; not canonical)
```

Redis stores references only. `openapi.yaml` changes go through the spec and
`make gen-go-api`.

## 11. Out of scope for v1

Server-restart survival (D11); archives and playback (D10); freeze/suspend
(D13); remote `agentd` placement; the auto-mode tmux viewer (D12); backend
conversation continuation.

## 12. Owner ratification

Reviewer consensus is complete. Before implementation, the owner must ratify
the product/release choices in D3 (cgo fallback on attributed resource
failure), D6 (non-controller viewers read-only until focused — a visible change
from today's any-viewer-can-type behaviour), and D11 (no server-restart
survival in v1). D9 was ratified on 2026-08-22 (memory budget, browser line
cap removed). This is authorization, not unresolved technical disagreement.

## Primary references

- xterm.js: https://github.com/xtermjs/xterm.js/ (`@xterm/xterm@6.0.0`,
  `@xterm/headless@6.0.0`, `@xterm/addon-serialize@0.14.0` on npm)
- libghostty-vt: https://libghostty.tip.ghostty.org/ and
  https://github.com/ghostty-org/ghostty/tree/main/include/ghostty/vt
- go-libghostty: https://github.com/mitchellh/go-libghostty
- hauntty (WASM precedent): https://github.com/seruman/hauntty
- wazero: https://wazero.io/
- asciicast v2: https://docs.asciinema.org/manual/asciicast/v2/
- tmux: https://github.com/tmux/tmux/wiki/Getting-Started
