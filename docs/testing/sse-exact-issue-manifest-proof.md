# Strict issue recovery manifest v2

Loom now accepts only `fleet.issue-workspace.v2` for native issue recovery documents and recovery offers. The same eight response fields remain. Fleet v2 supplies `through` from the repeatable-read snapshot's own validated applied prefix and requires its source highwater to equal that prefix. V1 described a previously captured lower bound and must not be interpreted as this stronger boundary.

The Fleet HTTP client, captured-source recovery registry, browser offer decoder, and native snapshot preparer reject v1. Unknown native issue fields and the original validated document bytes remain preserved. The shared Go/TypeScript corpus includes an otherwise valid v1 document that both runtimes must reject; offer tests also reject v1. This is a strict cutoff, with no version normalization or fallback.

## Limits

The manifest describes the producer contract; parsing it cannot independently establish database writer provenance. Exactness requires all writers to use guarded projection or command paths. The companion FleetDB implementation adds a database writer/provenance gate: only newly established protocol lanes qualify, while existing lanes with unproven history refuse exact reads. That database gate requires its own real PostgreSQL validation; Loom cannot establish it by inspecting JSON. Source-highwater equality alone cannot detect historical writes that changed issue state without appending source. Privileged database changes remain outside the guarded-writer contract.

A cursor is not a durable source incarnation, an authorization scope, or proof that separately captured requests belong to one browser recovery attempt. Existing captured-source and offer checks remain necessary. Ready membership is evaluated at the producer transaction's time and can change as defer times expire without any new source event. Time-based refresh remains necessary.

This change enables no browser cache replacement, replay checkpoint acknowledgment, or additional query coverage. Graph, selected detail, comments, and history are outside this native workspace issue manifest. Publication remains disconnected pending its own ownership and coverage proofs.

## Validation

Deterministic frontend tests and local HTTP fixtures exercise v2 success and v1 rejection, raw document preservation, malformed payload handling, offers and SSE resync behavior. They do not constitute a running paired Loom/FleetDB browser proof or hosted CI evidence. Exact storage behavior is tested in the companion FleetDB change.

- Focused frontend recovery/offer/SSE suite: 194 tests passed across five files. Full frontend suite: 9,210 tests across 423 files passed in 27.68 seconds; production frontend build passed in 2.61 seconds.
- TypeScript `tsc --noEmit`, ESLint on changed frontend files and frontend architecture checks: passed. Independent cross-repository review found no contract mismatch.
- `go test -race -p 1 ./internal/backend/fleet ./internal/webui/server/realtime ./internal/webui/subscription -run 'Recovery|Resync'`: passed. Local HTTP listeners required sandbox socket permission; the initial restricted run could not bind its fixture.
- Scoped Go lint for Fleet client, realtime and subscription packages: passed with zero issues (existing unknown `norawexec` directive warning).
- Logs: `/private/tmp/loom-exact-manifest-{frontend,go,tsc,eslint,golint}.log`.
