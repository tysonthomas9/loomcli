# Terminal State Lifecycle: Reconnect, Pause, and Archive

Status: design proposal for independent vetting; no implementation is included.

Source baseline: `origin/v5` at `024bb68259a0610b78a994cfc7d8de6310d695cb`.
Line references below are pinned to that commit and will drift after edits.

## Decision summary

Loom already uses the stable `@xterm/xterm` package as its browser terminal.
The current reconnect implementation does not use a terminal emulator on the
server: it retains an arbitrary 256 KiB suffix of raw PTY bytes and manually
checkpoints a subset of DEC private modes. That cannot reconstruct arbitrary
terminal state reliably.

Two credible long-term approaches are:

1. **Recommended: xterm state owner.** Keep `@xterm/xterm` in the browser and
   add one packaged Node worker per Loom runtime using `@xterm/headless` plus
   `@xterm/addon-serialize`. Every PTY output and resize event passes through
   the worker in order. Reconnect receives a serialized emulator snapshot at
   an atomic stream boundary, followed by live output after that boundary.
2. **Alternative: tmux state owner.** Put each process in a tmux pane and let
   tmux own the live grid and process lifetime. A new browser attaches to the
   pane and tmux redraws current state. Recording and portable archives remain
   separate work.

Do not extend Loom's current regular-expression-based VT checkpointing into a
home-grown emulator. If packaging and supervising Node is unacceptable,
choose tmux rather than expanding the Go replay ring.

The recommendation is conditional: choose the xterm state owner when exact
browser/server emulator parity and portable terminal artifacts are primary.
Choose tmux when surviving Loom server restarts with the same OS process is the
primary requirement and a host-level tmux dependency is acceptable.

## Problem and observed evidence

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

The arithmetic is exact: `22 + 7 + 262144 = 262173`. The process and PTY were
still alive. The ring had evicted the leading ESC byte from an `ESC [ K`
erase command, so the fresh browser received the suffix `[K` as visible text.

This is one concrete boundary failure, not the whole defect class. A raw suffix
can also begin inside:

- a UTF-8 code point;
- CSI, OSC, DCS, or APC control strings;
- a normal cursor, color, tab-stop, scrolling-region, or character-set change;
- a normal/alternate buffer transition not represented by the checkpoint;
- a synchronized-output region.

A larger ring only makes failures less frequent. It does not make an arbitrary
byte suffix a valid terminal snapshot.

## Current architecture

The browser side is already on the right foundation:

- `internal/webui/frontend/package.json:63-64` depends on
  `@xterm/addon-fit` and `@xterm/xterm`.
- `internal/webui/frontend/src/components/TerminalView/instances/XTermRenderer.tsx:1-3`
  imports those packages.
- `XTermRenderer.tsx:107-115` constructs the terminal and loads the fit addon.

The server-side replay path is the weak point:

- `internal/webui/terminal/ringbuf.go:9-12` caps retained output at 256 KiB.
- `ringbuf.go:63-91` evicts raw bytes without respecting VT or UTF-8
  boundaries.
- `ringbuf.go:94-167` recognizes only a selected list of DEC private modes.
- `ringbuf.go:217-240` returns that mode checkpoint plus the retained suffix.
- `internal/webui/terminal/pty_session.go:15-18` defines the clear-and-home
  sequence prepended on replay.
- `pty_session.go:133-163` appends every PTY chunk to the ring and fans a
  best-effort copy to attachments.
- `pty_session.go:176-215` registers an attachment, then obtains its replay
  snapshot in a separate operation.
- `internal/webui/handlers/terminal/ws.go:320-335` writes replay first and then
  starts pumping live output.

There are two additional correctness gaps:

1. Snapshot and live registration do not have a single ordering barrier. Output
   can be duplicated or ordered incorrectly around attachment setup.
2. `pty_session.go:31-35` and the non-blocking attachment send allow a slow
   viewer to lose live frames. The ring is described as ground truth, but that
   ground truth is itself not a valid state snapshot.

The existing abstraction is useful but underspecified:

```go
type Attachment interface {
    Output() <-chan []byte
    Scrollback() []byte
    // ...
}
```

`PTYSource` in `internal/webui/terminal/source.go:10-66` already hides local PTY
versus future remote `agentd` placement from the WebSocket handler. The design
should deepen this seam instead of teaching the handler about emulators,
workers, tmux, or artifact storage.

## Required semantics

### The state handoff invariant

Every output or resize event receives a monotonically increasing sequence
number. Attaching a viewer must be one logical operation:

1. Apply all events through sequence `N` to the authoritative state owner.
2. Produce a snapshot labeled `N`.
3. Subscribe the attachment to events starting at `N + 1`.
4. Send the snapshot.
5. Send the queued and then live events from `N + 1` onward.

There must be no gap, duplicate, or reordering across the snapshot/live cut.
This contract matters more than the specific package used.

The public seam should express the result rather than expose “scrollback”:

```go
type TerminalInitialState struct {
    Sequence uint64
    Encoding string // versioned, for example "xterm-vt/1"
    Data     []byte
}

type Attachment interface {
    InitialState() TerminalInitialState
    Output() <-chan TerminalEvent // events begin strictly after InitialState.Sequence
    WriteInput([]byte) (int, error)
    Resize(connID string, cols, rows uint16) error
    // existing identity and exit methods
}
```

The exact Go types may change, but the invariant must appear in the interface
documentation and contract tests. `Scrollback()` should not remain as an
ambiguous synonym.

### Slow viewers

A slow browser must never silently lose terminal frames. A bounded queue is
still appropriate, but overflow should close that viewer with a resumable
reason. The browser reconnects and obtains a new snapshot. The PTY, state
owner, recorder, and other viewers continue without backpressure.

### One canonical geometry

A PTY has one row/column size even with multiple viewers. Loom must select and
document one policy, such as an explicit controlling viewer or the most recent
focused viewer. Snapshot metadata and resize events must use that canonical
geometry. “Every viewer resizes the PTY” is not a deterministic policy.

## Lifecycle model

“Pause” currently conflates three distinct operations. The product and API
should name them separately.

| User operation | Process runs? | State owner runs? | On return |
|---|---:|---:|---|
| Detach viewing | Yes | Yes | Snapshot now, then live output |
| Freeze display | Yes | Yes | Jump to now or replay missed events |
| Suspend execution | No | Yes | Continue the same process after explicit resume |
| View archive | No process | No live state required | Static final state or timed playback |

### Detach viewing

This is the normal tab-close, navigation, sleep, network-loss, and WebSocket
reconnect behavior. Closing the viewer does not stop the PTY. The state owner
and recorder continue consuming output. Resume means a new atomic snapshot plus
live events; the browser does not need every byte emitted while detached.

The existing server already intends this behavior: `ws.go:288-290` and
`ws.go:342-344` detach the WebSocket while leaving the PTY alive for a grace
period. The new design makes restoration exact.

### Freeze display

This is a viewer feature, not process control. The browser records sequence `N`
and stops painting while the process continues. Resume offers:

- **Jump to now** (default): discard the viewer's missed frames and request a
  fresh snapshot.
- **Catch up**: read recorded events after `N`, with a progress limit and a
  fallback to jump-to-now when the backlog is too large.

### Suspend execution

This must be an explicit advanced command named **Suspend execution**, not
“pause terminal.” On local POSIX systems it can signal the process group with
`SIGSTOP` and later `SIGCONT`; remote placements need a backend-specific
equivalent. xterm and tmux do not make application suspension safe: leases,
network peers, subprocesses outside the process group, and external timeouts
may expire. Authorization and audit events are required.

### End and archive

When the child exits or is killed, Loom finalizes an immutable artifact and
transitions the session to `ended_archived`. Loading it never spawns a PTY.

```text
live_attached --detach--> live_detached --attach(snapshot)--> live_attached
       |                       |
       +----suspend----------> live_suspended --resume------> live_detached
       |
       +----process exit-----> ended_archived --open-------> read_only_view
```

An archived terminal can be viewed or replayed. It cannot resurrect the exited
OS process. A backend such as Codex or Claude may support starting a **new**
process with application-level conversation context; that is a separate
session linked by metadata, not terminal process resume.

Likewise, an emulator snapshot preserves pixels, cursor, modes, and scrollback;
it does not keep a process alive across a Loom server restart. Same-process
restart survival requires the PTY owner to outlive the web server, for example
`agentd`, a dedicated local runtime daemon, or tmux.

## Approach 1: xterm state owner (recommended)

### Stable packages

- Browser rendering: existing `@xterm/xterm`.
- Server-side terminal emulation: `@xterm/headless`.
- State serialization: `@xterm/addon-serialize`.
- Portable timed export: asciicast v2.

The official xterm.js README explicitly describes `@xterm/headless` plus the
serialize addon as a way to track process state and restore it on reconnect.
The serialize addon emits VT sequences, so the existing browser xterm consumes
the snapshot through the same write path as ordinary PTY output.

Pin `@xterm/xterm`, `@xterm/headless`, and `@xterm/addon-serialize` to versions
listed as mutually compatible by the selected xterm release. Do not infer addon
compatibility from similar version numbers. Package selection is approved only
after the proof-of-concept parity suite passes on the locked versions.

### Placement

The local PTY owner is Go, while `@xterm/headless` is Node. Package one
supervised Node terminal-state worker with each Loom runtime; do not launch one
Node process per terminal. The worker multiplexes sessions over a narrow local
IPC protocol.

```text
PTY owner (Go) assigns sequence and timestamp
             |
             +--> terminal-state worker (@xterm/headless)
             |       +--> serialized snapshot at barrier N
             |
             +--> append-only event recorder
             |
             +--> attachment queues (events N+1...)
```

The state worker must live beside the PTY owner:

- Desktop/local: packaged with the Loom local runtime, not a developer checkout
  or globally installed npm package.
- Remote: inside or beside `agentd`; do not stream all PTY state back to the
  desktop merely to take a snapshot.

`PTYSource` remains placement-agnostic. The WebSocket handler receives only the
atomic `InitialState` and ordered `Output` stream.

### Failure recovery

The recorder is owned by the PTY side of the boundary so a state-worker crash
does not kill or stop recording the process. Supervision restarts the worker
and rehydrates it from the most recent checkpoint plus subsequent output and
resize events. Attach attempts wait or return a retryable “state rebuilding”
status until rehydration completes.

Arbitrary capped suffix replay is not an allowed recovery shortcut. Recovery
must begin from a complete emulator snapshot or from the beginning of a valid
event history.

### Benefits

- Browser and server use the same parser family and VT behavior.
- Snapshots represent terminal state rather than guesses about byte history.
- Static archives load directly into the existing xterm renderer.
- The same sequenced event model supports detach, frozen viewers, timed
  playback, seeking, and remote PTY placement.

### Costs and risks

- Loom must package, supervise, version, and health-check a Node worker.
- Worker IPC and snapshot barriers are new production infrastructure.
- xterm serialization compatibility must be tested during every package
  upgrade.
- Process lifetime still needs a durable PTY owner if Loom web-server restart
  survival is required.

## Approach 2: tmux state owner

Run every Loom terminal process inside a named tmux pane. tmux owns the PTY,
grid, history, and live process. Loom's WebSocket connects through a fresh tmux
client; tmux produces a valid redraw instead of Loom replaying a raw suffix.

### Benefits

- tmux is a mature terminal multiplexer designed for detach and reattach.
- The process naturally survives WebSocket and Loom web-server restarts while
  the tmux server remains alive.
- Multi-viewer attachment and canonical pane geometry already have defined
  behavior.
- It avoids a Node sidecar and a custom state-worker IPC protocol.

### Costs and risks

- tmux becomes a required, packaged runtime dependency across macOS, Linux,
  containers, and remote `agentd` images.
- tmux terminal behavior sits between the application and browser xterm; parity
  depends on correct `TERM`, terminfo, and tmux version/configuration.
- tmux `capture-pane` is useful for text export but is not by itself a complete
  portable emulator snapshot with cursor, modes, images, and event timing.
- Durable recording, archived playback, retention, and secret handling still
  need the artifact design below.
- Reintroducing tmux reverses the current `origin/v5` direct-PTY ownership model
  and requires migration for existing local and remote sessions.

tmux is the stronger choice if “resume the identical process after Loom
restarts” outweighs exact xterm serialization and portable archives. It is not
a reason to keep the current Go byte-ring replay.

## Archived terminal artifact

Live snapshots and historical recordings solve different problems. Store a
versioned bundle rather than overloading `ScrollbackPath` with another format:

```text
terminal-artifact/
  metadata.json       # identity, lifecycle, dimensions, TERM, versions
  final.vt            # serialized final xterm state for instant static load
  events.cast         # interoperable asciicast v2 timed output/resize export
  final.txt           # searchable and accessible text projection
  checkpoints/        # optional periodic VT snapshots for bounded seek time
```

`metadata.json` should include session and workspace IDs, backend and placement,
start/end timestamps, exit code/reason, initial and final dimensions, `TERM`,
artifact schema version, xterm package versions, checksums, and retention class.

Asciicast v2 is newline-delimited JSON with output, input, marker, and resize
events. Loom should record output and resize by default. Input capture must be
off by default because keystrokes can contain passwords, tokens, and private
prompts; enabling it requires an explicit policy and UI disclosure.

Asciicast output is UTF-8 JSON text. The implementation must define and test
how arbitrary PTY bytes are decoded. If lossless byte recovery is required,
retain a versioned binary event log as the canonical recovery stream and treat
`events.cast` as the interoperable export. Do not silently corrupt or discard
invalid byte sequences.

Evolve `internal/webui/sessionhistory/store.go:24-35` from
`ScrollbackPath string` toward a versioned `TerminalArtifactRef`. Preserve the
old field during migration so existing records remain readable. The current
store has no TTL (`store.go:169-175`), so the proposal must not ship until
artifact retention, size quotas, deletion, and access authorization are
defined. Redis should store metadata/references, not large artifact bodies.

For an ended session, the frontend loads `final.vt` into a fresh read-only
`@xterm/xterm`. Playback loads the nearest checkpoint before the requested time
and applies later events. `final.txt` supports search, copy, accessibility, and
environments where a terminal renderer is unavailable.

## Implementation sequence

### Phase 0: package and parity proof

Build a throwaway vertical slice before changing production replay:

1. Start one packaged terminal-state worker.
2. Feed identical PTY bytes and resize events to browser xterm and headless
   xterm.
3. Serialize the headless instance and load it into a new browser xterm.
4. Compare normal/alternate buffers, cursor, modes, dimensions, and visible
   text.
5. Exercise the installed desktop bundle and one remote/container runtime, not
   only a source checkout.

Decision gate: if packaging, supervision, or parity is unacceptable, stop and
write the tmux migration design. Do not fall back to incrementally expanding
the Go mode regex.

### Phase 1: deepen the attachment contract

- Introduce sequenced output and resize events.
- Replace `Scrollback()` semantics with atomic `InitialState()` semantics.
- Make attach create the snapshot/subscription cut in one owner operation.
- Disconnect and resnapshot slow viewers instead of silently dropping frames.
- Keep the WebSocket handler ignorant of the chosen state owner.

### Phase 2: xterm state worker

- Add a session-multiplexed local IPC protocol with apply, resize, snapshot,
  close, health, and rehydrate operations.
- Package locked xterm dependencies in desktop and remote runtime artifacts.
- Add worker supervision and recovery from checkpoint plus event log.
- Remove `ringBuffer` mode parsing only after the new path passes shadow-mode
  comparison and reconnect soak tests.

### Phase 3: terminal artifacts

- Add `TerminalArtifactStore` with create/finalize/open/delete operations.
- Add `TerminalArtifactRef` to session history with backward-compatible reads.
- Write output/resize events and periodic checkpoints while the session lives.
- Finalize `final.vt`, `events.cast`, and `final.txt` on every exit path.
- Add read-only final-state and playback UI.

### Phase 4: explicit lifecycle controls

- Expose detach/freeze as viewer controls.
- Add audited suspend/resume only for placements that can guarantee process
  group control.
- Decide whether local PTY ownership moves out of the Loom web-server process
  when same-process restart survival is required.

## Validation matrix

The primary oracle is differential: a continuously attached browser xterm and
a fresh browser xterm restored from the server snapshot must converge on the
same terminal state.

Required cases:

- every split position inside representative ANSI/DEC/OSC sequences;
- every split position inside multibyte UTF-8;
- more than 256 KiB of output, including an `ESC[K` across the old ring edge;
- normal and alternate buffers, cursor visibility/style, colors, hyperlinks,
  bracketed paste, mouse modes, scroll regions, and tab stops;
- Codex/Claude full-screen and synchronized-output traffic;
- resize immediately before, during, and after attach;
- output arriving during the snapshot/live barrier;
- multiple viewers with one canonical geometry;
- slow-viewer overflow followed by resnapshot;
- state-worker crash, rehydrate, and attach;
- Loom web-server restart with the documented process-lifetime outcome;
- ended-session load when no PTY or worker is live;
- checkpoint seek plus event replay;
- artifact schema and xterm package upgrade compatibility;
- retention deletion, workspace authorization, and input-redaction policy.

Tests must compare buffer contents, cursor, active buffer, dimensions, and
relevant modes—not only screenshots or final plaintext.

## Questions the vetting agent must answer

Return each conclusion as **supported**, **overstated**, or **unsupported**, with
source/test evidence:

1. Does the observed frame arithmetic and current replay source support the
   stated root cause and broader defect class?
2. Do `@xterm/headless` and `@xterm/addon-serialize` restore all terminal state
   Loom needs on the exact locked package versions? Identify exclusions.
3. Can one Node worker be packaged and supervised consistently in desktop,
   local serve, containers, and remote `agentd`? What is the failure policy?
4. Is the sequence-`N` snapshot/live invariant implementable through the
   current `PTYSource` seam without leaking placement details into handlers?
5. Should the recorder live with the PTY owner, the state worker, or both?
6. Is xterm state ownership preferable to tmux for Loom's actual process
   persistence requirement? State the requirement assumed.
7. Are detach viewing, freeze display, suspend execution, archive viewing, and
   backend conversation continuation correctly separated?
8. Are artifact formats, byte encoding, retention, authorization, and secret
   risks adequately handled?
9. Which concurrency, crash, resize, upgrade, and migration cases are missing?
10. Final recommendation: **accept**, **rework**, or **reject**, with the
    smallest proof needed before implementation.

The vetting agent should inspect the pinned checkout rather than trusting line
references or package claims in this document.

## Primary references

- xterm.js project README, including maintained addons and Node/headless
  reconnect use case: https://github.com/xtermjs/xterm.js/
- asciicast v2 specification: https://docs.asciinema.org/manual/asciicast/v2/
- tmux getting started and advanced-use documentation:
  https://github.com/tmux/tmux/wiki/Getting-Started and
  https://github.com/tmux/tmux/wiki/Advanced-Use
