# Known E2E test issues

Documented failures and their tracking tickets. Tests that hit these should NOT be reported as part of a passing run — they're either tracking debt or environment quirks.

## `loomcli-26v50.40` — fleet-db daemon PUT loses fields (FIXED)

**Status:** fixed. `internal/projection/handlers.go:handleDaemonUpdate` now `Delete`s the daemon profile hash before `SetFields` when `event.Metadata["mode"] == "upsert"`. PATCH semantics (mode=patch or unset) preserved. D5 in `e2e-cli.md` should now PASS — PUT with empty body actually clears the storage hash. Regression guard: `TestProjectEvent_DaemonUpdate_{Upsert,Patch}*` in fleet-db's `internal/projection/projector_test.go`.

---

## `loomcli-26v50.41` — localredis dirty-flag skip ineffective for embedded CLI (FIXED)

**Status:** fixed. `Manager.load()` now seeds `lastDumpHash` with the SHA-256 of the loaded `snap.Entries`, so the first post-load Dump short-circuits when the keyspace hasn't been mutated. C3 in `e2e-cli.md` should now PASS — read-only CLI invocations leave snapshot mtime stable. Regression guard: `TestDump_SkipsAfterReload` in `manager_perf_test.go`.

---

## `loomcli-26v50.39` — embedded fleet-db waitid race (FIXED)

**Status:** fixed. C4 in `e2e-cli.md` is the regression guard. If "waitid: no child processes" reappears in stderr during embedded-mode operation, .39 has regressed.

---

## RPC socket bind permission (cosmetic, low priority)

**Symptom:** fleet-db logs `rpc-listener: bind: permission denied on /var/run/fleet-db.sock` because the default RPC socket path requires root.

**Workaround:** `e2e-preflight.md` passes `--rpc-socket=` to disable RPC entirely (loom doesn't use it).

**Long-term fix:** make the embedded.go `--rpc-socket=` flag default behavior, OR repoint to a user-writable location.

---

## Test methodology pitfalls

These are not bugs — they're documented gotchas the test runner must avoid. From the code-review pass on the test plan itself:

1. **`grep -vE "level="` strips real errors.** Always capture stdout and stderr separately. Assert stderr is free of `level=ERROR` (with explicit allowlist for known startup noise).

2. **Don't trust the CLI to verify its own writes.** `loom role show` reads through the same client path as `loom role set` — a stale-cache or wrong-endpoint bug would silently round-trip. Always cross-check writes against fleet-db via curl.

3. **Anchor success patterns.** `grep -qE "^Created workspace ACME$"` not `grep -q Created` — the latter matches startup logs containing "Created embedded fleet-db".

4. **Process leak detection.** After each embedded-mode test, `pgrep fleet-db` should return zero. A non-zero count indicates a defer didn't run (likely an `os.Exit` shortcut bypassing `cmdstore.WithStore`'s deferred Close).

5. **D5 is not coverage.** A documented expected-failure tracks debt; it does not test correct behavior. Don't include D5's "pass" in success counts.
