# Phase 0 — libghostty-vt state-owner feasibility (throwaway spike)

Implements Phase 0 of `terminal-state-lifecycle.md` (§7). This is a **throwaway
vertical slice** to decide D2: does libghostty-vt, restored into a browser
`@xterm/xterm`, faithfully reconstruct terminal state for Claude/Codex TUIs, and
can it be packaged as WASM under wazero inside loom's `CGO_ENABLED=0` single
binary? Nothing here ships to production. Live in `spike/terminal-libghostty/`
(a top-level throwaway dir) — do NOT wire into `internal/webui`.

Branch: `feat/terminal-phase0-libghostty`.

## Why this exists

Phase 1 fixed the transport but the interim state source is still the 256 KiB
regex-checkpoint ring. Phase 2 replaces it with a real emulator snapshot. Before
committing to that, prove the emulator choice on the two open risks:
- **Fidelity** (gate for D2): the restored screen matches a continuously-fed
  terminal, byte-for-byte on the cell grid, incl. after the live suffix.
- **Packaging/perf** (gate for D3): WASM/wazero meets memory + throughput budgets
  and builds for all four release targets with no external runtime.

Fidelity is library-model-dependent, not binding-dependent, so it can be proven
via the **native/cgo** path first; the WASM question is separable and comes after.

## Milestones (do 0.1 first; stop and report before 0.2)

### Milestone 0.1 — fidelity feasibility (native)

Deliverables under `spike/terminal-libghostty/`:
1. A pinned libghostty-vt build. Pin the ghostty commit (record it in
   `PIN.md` with the exact SHA + Zig version). Build the vt library with the
   installed Zig 0.16.0 (`zig version` must print 0.16.0). Prefer
   `mitchellh/go-libghostty` (cgo) for the harness if it builds cleanly against
   the pinned lib; otherwise a thin hand-written cgo shim over the pinned
   `include/ghostty/vt/*.h` is acceptable. Record the exact build commands in
   `PIN.md`.
2. A Go harness `snapshot.go` that, given a byte stream + a sequence of resize
   ops: feeds them to a libghostty terminal (`ghostty_terminal_vt_write`,
   `_resize`), enables continuation tracking
   (`GHOSTTY_TERMINAL_OPT_CONTINUATION_MAX_BYTES`), and produces the **Loom
   snapshot encoder** output = formatter VT (all extras: modes, cursor,
   scrolling_region, tabstops, charsets, hyperlink; both buffers per §6) +
   exported continuation bytes.
3. A Node oracle `oracle.mjs` using `@xterm/headless` (install it in the spike
   dir's own package.json — do NOT touch `internal/webui/frontend`): builds
   two headless terminals, (A) fed the full byte stream continuously, (B) fed
   the snapshot then the remaining suffix after the cut; serializes both grids
   (cells + attributes + cursor + active buffer + modes + dimensions) and
   diffs them.
4. A driver `run_parity.sh` (or a Go test) that, for a small corpus, takes a cut
   at a random point, produces the snapshot via `snapshot.go`, and asserts A==B
   via `oracle.mjs`. Corpus for 0.1 (small, representative): a plain prompt +
   `ls --color`; an alt-screen app frame (vim-like: `\e[?1049h` … `\e[?1049l`);
   an SGR-heavy line; a cursor-hidden (`\e[?25l`) TUI; a cut placed **mid-CSI**
   and **mid-UTF-8** to exercise continuation.

Milestone 0.1 passes iff: builds on this machine; and for every corpus case at
every tested cut, grid A == grid B **both immediately and after the suffix**.
Report a table of case × cut → pass/fail with the first diff on any failure.

### Milestone 0.2 (DO NOT START until I approve 0.1)
Full corpus (saturated scrollback, mouse tracking+SGR encoding, bracketed
paste, OSC 8, synchronized output, wide/combining, DA/DSR queries, every byte
split), + the WASM/wazero build and the memory/throughput budgets (gates 6-8),
+ four-target build check. This is where D3 gets decided.

## Hard rules
- Throwaway only: everything under `spike/terminal-libghostty/`. No imports from
  or into `internal/webui`. Do not modify `go.mod`/`go.sum` of the main module
  for cgo experiments — use a separate `go.mod` inside the spike dir.
- Record every external pin (ghostty SHA, Zig version, go-libghostty version,
  @xterm/headless version) in `PIN.md`.
- If the build cannot be made to work on this machine within reasonable effort,
  STOP and report exactly where it failed (command + error) — do not fake a pass.
- The oracle must compare **grid state**, not screenshots or plain text.

## Report format
Files created; exact build/run commands with pass/fail; the 0.1 parity table;
any deviations; and a clear recommendation: does 0.1 support proceeding to 0.2,
and are there early fidelity gaps (e.g. DECSCUSR, title) to note for §6.
