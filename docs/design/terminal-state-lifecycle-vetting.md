# Vetting notes: terminal-state-lifecycle.md @ b6f4b2926 (Claude, 2026-08-22)

Verdict: rework (narrowly). Diagnosis and attach invariant are correct. Recommendation is
under-argued: (1) never evaluates an in-process Go VT emulator; (2) claims
@xterm/addon-serialize restores state it verifiably does not.

## Verified against source
- ringbuf.go:173-202 advanceHeadLocked only repairs eviction cuts through DECSET/DECRST
  (privateModePattern). ESC[K straddling the ring edge is not repaired -> "[K" suffix. Arithmetic
  22+7+262144=262173 matches attachNew (pty_session.go:206-213).
- pty_session.go:192-207 registers the attachment BEFORE ReplaySnapshot(); a chunk arriving in
  between is in both snapshot and channel -> duplicate. Confirmed ordering hole.
- pty_session.go:305-315 non-blocking send, 64 x 4KiB (TerminalReadBufSize=4096) then silent drop.
- pty_manager.go:55 defaultGracePeriod=0: local sessions live as long as `loom serve`.
- @xterm/xterm locked 6.0.0; @xterm/headless@6.0.0 and @xterm/addon-serialize@0.14.0 published.

## Q2: addon-serialize 0.14.0 (read SerializeAddon.ts) restores
buffers (normal+alt), SGR attrs/colours, cursor POSITION, and modes ?1 ?66 ?2004 4 ?6 ?45 ?1004
?7l and mouse tracking ?9/1000/1002/1003 ONLY. It does NOT restore:
- cursor visibility ?25 (current ring checkpoint DOES, ringbuf.go:230) -> regression for Claude/Codex TUIs
- mouse encoding ?1005/1006/1015/1016 (current ring DOES, ringbuf.go:233)
- OSC 8 hyperlinks, DECSCUSR cursor style, window title, DECSTBM, tab stops, charsets,
  extended underline.
terminal.modes does not expose cursor visibility, so that must be tracked from the raw stream.

## Q3: Node worker packaging — unsupported as written
desktop/src-tauri/tauri.conf.json:32 externalBin = [loom, fleet-db]; no Node runtime. loom is a
single Go binary with no runtime Node dependency. `agentd` does not exist in this repo (comment
references only: source.go:14, pty_manager.go:53). No failure policy for absent/unhealthy worker;
no memory budget (ring sized 10MB/40 sessions).

## Q4: supported. Fix = take snapshot + register subscriber under the same lock drain holds
between Append and fan-out. Go-only; emulator-agnostic. Browser must terminal.reset() before
applying snapshot (XTermRenderer.tsx never resets on reconnect).

## Q6: overstated — tmux is still a live dependency: internal/webui/terminal/agent_tmux.go
(web UI attaches to auto-mode agent sessions), internal/cli/automode/automode_tmux*.go creates
them, loom doctor checks the binary. Host-level tmux cost is already partly paid.

## Q8: api/openapi.yaml is source of truth (CLAUDE.md); scrollback_path already exposed
(types.gen.go:2655) plus endpoints openapi.yaml:1285 (history scrollback) and :1797 (live
/terminal/sessions/{session}/scrollback) — a second consumer of ring bytes the doc omits.

## Q9 missing
- Third option never evaluated: in-process Go VT emulator (charmbracelet/x/vt, vito/midterm,
  hinshun/vt10x). Not "home-grown"; removes the Node sidecar cost. Trade-off = weaker parser
  parity with browser xterm. Must be argued, not skipped.
- PTYCommandRunner.EnsureSession starts sessions with no viewer; state owner must consume from spawn.
- MultiPTYManager per-workspace keying.
- maybeEmitStaleRestartBanner writes to WS directly, bypassing the event stream.
- Worker IPC should be parent-child stdio pipes, not a socket.
- Snapshot barrier must wait for worker ack of seq N; live fan-out stays Go-only.

## Q10: rework. Smallest proof:
1. Ship Phase 1 (atomic cut + disconnect-on-overflow + browser reset()) against the existing ring.
2. ~1-day Phase 0 bake-off with THREE contenders (xterm headless+serialize, a Go emulator,
   tmux capture-pane -e) fed the same recorded Claude/Codex session, compared cell-by-cell vs a
   continuously attached browser xterm; include cursor-visibility and SGR-mouse cases.
3. Then pick the state owner.

## Addendum: libghostty-vt as a fourth state-owner candidate (researched 2026-08-22)

What it is: the VT parser + terminal-state core of Ghostty, exposed as a zero-dependency C
library (no libc required). No renderer. MIT.

Relevant API (include/ghostty/vt/*.h, main):
- terminal.h: ghostty_terminal_new/free, ghostty_terminal_vt_write, ghostty_terminal_resize,
  ghostty_terminal_reset, ghostty_terminal_get/set (modes, colors, size).
- formatter.h: ghostty_formatter_terminal_new + format; emit = plain text | VT | HTML.
  GhosttyFormatterTerminalExtra can emit: modes (CSI h/l differing from default), scrolling
  region (DECSTBM/DECSLRM), tabstops, palette (OSC 4), pwd (OSC 7), keyboard modes; screen
  extras: cursor (CUP), style (SGR), hyperlink (OSC 8), protection (DECSCA), kitty keyboard,
  charsets. => covers EVERY gap identified in @xterm/addon-serialize 0.14.0.
- snapshot.h: ghostty_snapshot_encode / decoder_* — binary, CRC32C record stream, carries
  unfinished VT/UTF-8 parser CONTINUATION, READY marker (renderable prefix) then scrollback
  HISTORY pages newest->oldest for incremental restore. Exactly the worker-rehydrate use case.

Embedding in Go (loom releases with CGO_ENABLED=0, .goreleaser.yml:16; zero cgo today):
- (a) cgo: github.com/mitchellh/go-libghostty (MIT; wraps terminal, vt_write, resize,
  formatter, snapshot, render state). Static link by default via pkg-config; cross-compile
  with `zig cc -target ...` for x86_64/aarch64 linux+macos (+windows-gnu). Requires Zig 0.16+
  to build the lib; no prebuilt libs vendored in the module.
- (b) WASM: compile libghostty-vt to wasm32 and run under wazero (pure Go). Precedent:
  seruman/hauntty (Go session persistence using libghostty-vt WASM), browstty,
  obsidian-ghostty-terminal. Keeps CGO_ENABLED=0 and the single-static-binary release.

Ecosystem precedent for this exact use: neurosnap/zmx ("session persistence for terminal
processes, using libghostty-vt for terminal state restore"), hauntty, headless-terminal,
termscope.

Stability caveats (verbatim): vt.h "WARNING: This is an incomplete, work-in-progress API. It
is not yet stable and is definitely going to change." snapshot.h: "Snapshot format version 1
is a work in progress and does not yet carry a binary-compatibility guarantee."
go-libghostty: "I'm not promising any API stability yet."

Fit assessment:
+ In-process (no Node sidecar, no IPC, no supervision); state owner dies with the PTY owner,
  which is the right failure domain.
+ Formatter VT output covers the full state set, loads into browser @xterm/xterm via the
  normal write path.
+ Snapshot format designed for persist/restore with parser continuation — the ring-edge
  "[K" class of bug is structurally impossible.
+ Parser parity: Ghostty's VT parser is arguably more conformant than xterm.js's; the
  differential oracle (browser xterm vs restored xterm) still applies.
- Build chain: either cgo+zig cross-compile (breaks CGO_ENABLED=0 release) or wasm+wazero
  (new runtime dep, perf to be measured, needs the wasm artifact built+vendored).
- API/format instability: pin a commit, vendor headers/artifact, own the re-pin cost; do
  NOT persist the binary snapshot format as a long-term archive — archive the formatter VT
  output + asciicast instead; use binary snapshots only for in-process checkpoints.
- Browser/server parser mismatch (Ghostty vs xterm.js) is the price vs @xterm/headless.
