# Success-bearing query recovery

Session surfaces are now covered by [session recovery](sse-session-recovery-proof.md).
The scope and validation below describe the original core-query milestone.

## Why the old refresh promises cannot acknowledge recovery

The query registry's ordinary `refetch()` promise resolves when work settles,
including failure, cancellation or removal. Agent refresh previously returned
immediately when another request was running. Issue refresh catches errors for
its existing retry/UI behavior. Awaiting those methods would acknowledge failed
or pre-recovery requests as successful snapshots.

The shared registry currently covers blocked issues only. Issue and agent
stores, terminal/session hooks, recent activity and the workspace file browser
have separate refresh paths. A registry-only barrier cannot certify the whole
visible workspace.

## Implemented seam

Strict `refreshForRecovery` operations for issues, agents and enabled registry
entries require a request started after invocation. They reject request failures,
cancellation and scope supersession, and resolve only after successful data
commit. Pre-existing in-flight responses cannot satisfy them. Legacy methods
retain their UI/retry behavior; same-scope legacy issue refreshes may join an
active strict request without masking its failure from the strict caller.
Issue reconnect indicators clear after a successful fetch, not just reconnection.

The provider owns a query recovery coordinator per workspace/repository-filter
identity. StoreWiring enrolls issues and agents, and the provider enrolls the
blocked-query registry. Resync starts refresh of those registered surfaces;
repeated resync signals join ongoing work. A new participant joins an active
attempt. Removal withdraws its requirement, while remount creates a new identity.
Scope changes cancel the old attempt so late completions cannot acknowledge it.

Nested registration needs an additional rule: the registry can finish while a
store request is pending. A blocked query mounted in that interval must still
join overall recovery. The coordinator records each aggregate's membership
revision and rechecks it before resolving, restarting changed aggregates.
The registry revision tracks required identities, not request completion or
stable React recommits.

## Explicit limits

Coordinator completion means successful refresh of the registered query
surfaces only. It does not advance or reset Last-Event-ID and is not a server
snapshot acknowledgment. Failed strict refresh remains a failure; a later
resync or explicit refresh can try again.

Terminal/session queries, file-browser data and bounded recent activity still
need enrollment with honest success contracts before claiming complete visible
state recovery. Registry membership cannot stand in for those surfaces.
The committed projection prefix must provide a fence that query reads actually
include; a raw mutation head cannot certify projected effects. Select that fence
before recovery reads, then verify retention before resuming from it. Fixed
replay bounds, full snapshot/reset acknowledgment, storage restart and paired
browser proofs remain separate unfinished requirements.

## Recorded validation

All 411 frontend unit-test files passed (8,859 tests), including strict store
failure/supersession tests, registry lifecycle tests, coordinator dynamic
membership tests and provider repo-scope cancellation. Full TypeScript checking,
scoped ESLint and formatting checks passed. Independent review caught the nested
registry enrollment gap; the final regression reproduces its sequence with an
actual late failing query and now rejects overall recovery. Review also prompted
expected-workspace checks before either store starts a recovery request.

These are deterministic frontend tests. They do not establish real storage,
paired browser, committed projection fence or complete visible-state recovery.
