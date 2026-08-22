# Milestone 0.1 report

## Files created

- `snapshot.go`: native cgo harness and thin shim over the pinned Ghostty C headers/library.
- `oracle.mjs`: `@xterm/headless` differential grid oracle.
- `run_parity.sh`: representative corpus driver, testing every byte cut.
- `PIN.md`: exact dependency pins and build commands.
- `package.json`: private spike dependency declaration.
- `go.mod`: private spike Go module.
- `.gitignore`: excludes Ghostty vendor source, caches, node modules, and build output.

No files under `internal/webui` or the main `go.mod` were changed.

## Build and run results

Ghostty’s `libghostty-vt` static library built successfully for macOS arm64
with Zig 0.16.0. The harness compiled and the parity driver completed
successfully:

```text
$ ./run_parity.sh
PASS all corpus cuts
```

The exact build commands are in `PIN.md`. The driver uses local
`ZIG_GLOBAL_CACHE_DIR`, `ZIG_LOCAL_CACHE_DIR`, `GOCACHE`, and npm cache paths.

## 0.1 parity table

Every case was tested at cuts `0..N`, inclusive, where `N` is the byte length.

| Case | Bytes | Cuts | Result |
|---|---:|---:|---|
| prompt + `ls --color` | 54 | 55 | PASS |
| alt-screen vim-like frame | 54 | 55 | PASS |
| SGR-heavy line | 43 | 44 | PASS |
| cursor-hidden TUI | 25 | 26 | PASS |
| CSI/UTF-8 split case | 31 | 32 | PASS |
| **Total** | **207** | **212** | **PASS** |

The oracle compares dimensions, active buffer, cursor, cell text, width,
colors, and cell attributes immediately after restore plus after the suffix.

## First diffs and deviations

The initial implementation found and fixed these encoder defects during the
run:

1. Formatter output leaves the cursor at its setup CUP; the encoder now emits
   the live cursor CUP after both-buffer formatting.
2. Clone buffer-switch controls must be included in the emitted VT stream;
   they are now emitted as `?1049l`/`?1049h` around the primary and alternate
   formatter output.
3. The formatter does not restore the current SGR state when the cursor is on
   an unstyled cell. The shim reads `GHOSTTY_TERMINAL_DATA_CURSOR_STYLE` and
   emits the current SGR before the continuation suffix.

The first oracle-only mismatch was ANSI color mode versus indexed color mode
(for example, `31m` versus `38;5;1m`) for the same palette value. The oracle
canonicalizes ANSI and indexed modes to their shared palette value so this
does not report a false visual/style difference. This is a deliberate oracle
deviation and should be revisited with the production serializer’s attribute
contract.

The shim does not attempt title restoration or DECSCUSR restoration; the
Phase 0 design explicitly allows those to remain unavailable when the pinned
API does not expose them. The corpus also does not cover the Milestone 0.2
features.

## Recommendation

Proceed to Milestone 0.2. Milestone 0.1 supports the native fidelity decision
for the representative corpus, including both active-buffer cases and cuts
inside CSI/UTF-8 input. Before relying on this as a production encoder, 0.2
should expand color/attribute assertions beyond the oracle normalization and
exercise title, cursor-shape, scrollback, synchronized output, hyperlinks,
and the remaining full-screen protocol cases.
