# Issue-store write ownership proof

The store previously selected optimistic work by issue ID alone. An older timed-out request could remove or roll back a successor for the same ID. Foreign-workspace or foreign-repository events could enter a pending issue's buffer before scope validation and later bypass the gates when flushed. Fetch scope changes left old optimistic timers attached to the new map.

The repair gives callbacks exact entry and scope ownership, validates event scope before buffering, and separates unresolved API commands from optimistic UI timeout state. Strict ordinary recovery refuses unresolved commands in its workspace and rejects a read overlapped by a command, including a command that settles before the read does. The coordinator observes an invalidation revision so changes arriving while other participants remain pending require another issue read.

## Evidence

The new ownership suite reproduced 11 failures against the original implementation (`/private/tmp/sse-stack-review/store-write-ownership-red.log`). A twelfth regression exercises the real query coordinator with an issue read that finishes before another participant, then receives an accepted mutation and must reread before overall completion. The final suite also covers reentrant command admission during publication, stale subscription callbacks, mutable filter inputs, reentrant fetch ownership, retired timers, retry scheduling after a workspace switch and already-aborted no-op fetches.

- Full frontend Vitest: 9,208 tests across 423 files passed in 28.60 seconds (`/private/tmp/sse-stack-review/store-frontend-full.log`). This includes 19 ownership regressions and the original 112 store tests.
- Existing store fixtures that seeded map state directly now configure the same workspace scope that production uses before issuing commands; the ownership guard was not relaxed to accommodate fixtures.
- Typecheck, ESLint, architecture checks and production build passed. ESLint retains 26 existing warnings and build retains the chunk-size warning. Logs: `/private/tmp/sse-stack-review/store-{typecheck,lint,arch,build}.log`.
- Independent review completed with no remaining blocker in the ownership delta.

## Limits

These are store/coordinator tests with fixture APIs. They do not prove native snapshot publication or paired FleetDB/browser behavior. The [publication design](../design/sse-issue-store-publication.md) records every writer and the remaining final acceptance seam. Ordinary reads still use timestamp merging; unresolved commands remain uncertain even after an optimistic timeout. No recovery cursor is acknowledged here.
