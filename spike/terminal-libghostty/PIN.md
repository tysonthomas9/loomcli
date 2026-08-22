# Milestone 0.1 pins

- Ghostty repository: `https://github.com/ghostty-org/ghostty`
- Ghostty HEAD: `da5ddcb0857c0e4ddb32f7a089911e9038d040f3`
- Zig: `0.16.0` (`/opt/homebrew/bin/zig version`)
- Go: `go1.26.0 darwin/arm64`
- Node: `v24.11.1` (Node 22+ requirement satisfied)
- `@xterm/headless`: `6.0.0`
- Go wrapper: not used; a hand-written cgo shim was used because the pinned C API is sufficient and the go-libghostty API is unstable.

## Exact build commands

From `spike/terminal-libghostty/vendor/ghostty`:

```sh
mkdir -p .zig-cache/global .zig-cache/local build
env ZIG_GLOBAL_CACHE_DIR="$PWD/.zig-cache/global" \
    ZIG_LOCAL_CACHE_DIR="$PWD/.zig-cache/local" \
    /opt/homebrew/bin/zig build -Demit-lib-vt -Doptimize=ReleaseFast \
    -Dtarget=aarch64-macos -p "$PWD/build"
```

From `spike/terminal-libghostty`:

```sh
mkdir -p .npm-cache
NPM_CONFIG_CACHE="$PWD/.npm-cache" npm install
GOCACHE="$PWD/.go-cache" go build -o build/snapshot snapshot.go
```

The first npm attempt used the default cache and failed because the existing
`/Users/tyson/.npm` cache contains root-owned entries; the local-cache command
above is the successful reproducible command.
