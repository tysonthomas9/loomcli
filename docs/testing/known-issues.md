# Known E2E test issues

> **Status:** Current · *audited 2026-07-23*

Register of defects the manual E2E plans have hit, with their regression guards.
**All four entries below are fixed** — nothing here is currently an
expected-failure. If a symptom reappears, the named guard is the first thing to
run. New expected-failures go here, and the corresponding row in
[e2e-cli.md](e2e-cli.md) / [e2e-ui.md](e2e-ui.md) must be marked at the same time.

## `loomcli-26v50.40` — fleet-db daemon PUT loses fields (FIXED)

**Status:** fixed. Fixed in the **fleet-db repository**, not this one:
`(fleet-db repo) internal/projection/handlers.go:handleDaemonUpdate` now
`Delete`s the daemon profile hash before `SetFields` when
`event.Metadata["mode"] == "upsert"`. PATCH semantics (mode=patch or unset)
preserved. D5 in [e2e-cli.md](e2e-cli.md) is a must-pass row — PUT with an empty
body actually clears the storage hash. Regression guard:
`TestProjectEvent_DaemonUpdate_{Upsert,Patch}*` in
`(fleet-db repo) internal/projection/projector_test.go`. Grepping *this* repo
for `internal/projection` finds nothing; that is expected.

**UNVERIFIED (2026-07-23).** Nothing in this repo can confirm the above. The
sibling `../fleet-db` checkout available at audit time was at 2026-05-05 and
contains neither `handleDaemonUpdate` nor `internal/projection/projector_test.go`
— so the symbol and file names here are from the original bug handoff, not from
a build anyone has re-read. Before relying on D5 as a must-pass row, re-grep a
current fleet-db checkout.

---

## `loomcli-26v50.41` — localredis dirty-flag skip ineffective for embedded CLI (FIXED)

**Status:** fixed. `Manager.load()` (`internal/webui/localredis/manager.go:667`) now seeds `lastDumpHash` with the SHA-256 of the loaded `snap.Entries` (`:703-712`), so the first post-load `Dump` short-circuits when the keyspace hasn't been mutated (the comparison is `:292-296`; the post-write update is `:337-341`; the field itself is declared at `:120-126`). C3 in [e2e-cli.md](e2e-cli.md) is a must-pass row — read-only CLI invocations leave snapshot mtime stable. Regression guard: `TestDump_SkipsAfterReload` (`internal/webui/localredis/manager_perf_test.go:66`).

---

## `loomcli-26v50.39` — embedded fleet-db waitid race (FIXED)

**Status:** fixed. C4 in `e2e-cli.md` is the regression guard. If "waitid: no child processes" reappears in stderr during embedded-mode operation, .39 has regressed.

---

## RPC socket bind permission (FIXED)

**Status:** fixed for embedded local mode. `bootstrap.StartEmbedded` (`internal/bootstrap/embedded.go:310`) passes `--rpc-enabled=false` (`:372-377`), so the embedded fleet-db used by loom does not bind `/var/run/fleet-db.sock`.

If this warning reappears during embedded-mode tests, verify the command path is using `StartEmbedded` rather than launching `fleet-db` directly. The manual preflight sidesteps the same bind by passing `--rpc-socket=/tmp/loom-fleet-db.sock` ([e2e-preflight.md](e2e-preflight.md)).

---

## Test methodology pitfalls

Moved. The runner conventions (separate stdout/stderr capture, anchored success
patterns, no CLI self-verification, process-leak detection) live once in
[e2e-preflight.md](e2e-preflight.md) §Test-runner conventions. They were
duplicated here and drifted; that section is canonical.

One rule was specific to this page and is retained: **a documented
expected-failure is not coverage.** It tracks debt; it does not assert correct
behavior. Never count an expected-failure's "pass" toward a success total. As of
this audit there are no open expected-failures, so every row in
[e2e-cli.md](e2e-cli.md) counts.

## Related

- [e2e-preflight.md](e2e-preflight.md) — runner conventions and session setup
- [e2e-cli.md](e2e-cli.md) — the CLI matrix these entries annotate
- [README.md](README.md) — testing docs index
