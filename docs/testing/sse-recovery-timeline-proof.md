# Native recovery timeline v6 proof

## Scope

This slice changes the strict shared recovery manifest to `fleet.issue-workspace.v6`. Unselected recovery keeps `history: null`. Selected history retains the complete native event records and adds a required `timeline` array, in the same order and with the same count. Older manifests are rejected; there is no compatibility fallback.

Fleet owns formatting through its existing `service.FormatTimeline` implementation and ordinary history record-ID codec. Loom validates the timeline shape and its correspondence to native records, then maps it into a private, frozen `PreparedIssueRecoveryTimeline`. This slice does not publish prepared data into mounted caches, acknowledge recovery, or replace the accepted SSE checkpoint.

The selected window remains the newest 200 visible source events, with explicit `present` and `has_older`. This is not complete archived history or proof that matching an event's entity ID captures every indirect issue effect. The enclosing source-bound `through` is a checkpoint; timeline `c1` IDs are ordinary history record identities, and Loom does not decode their opaque payloads.

## Contract and parity

Each timeline row has exactly nine fields: `id`, `event_id`, `timestamp`, `actor`, `action`, `category`, `summary`, `changes`, and `metadata`. The raw `event_id`, timestamp, actor, action, and metadata must match the corresponding native event. Timeline IDs must be unique canonical `c1` envelopes. Categories use the eight existing Fleet values. Changes contain exact string `field`, `before`, and `after` members, with unique fields ordered by UTF-8 bytes, including a legitimate empty field name. Empty collections remain explicit in native wire data.

The Go and browser validators share `internal/backend/fleet/testdata/issue_recovery_corpus.json`, including malformed fields, duplicate IDs, count mismatches, native-record mismatches, and UTF-8 ordering cases. The 167-case corpus is a deterministic contract fixture, not evidence of a running Fleet storage service.

`issue_recovery_timeline.json` in that directory was generated from Fleet's actual formatter and recovery HTTP DTO. Its 46 rows cover all 43 current actions plus three issue-update JSON edge cases. The paired `issue_recovery_timeline_expected.json` records the ordinary Loom Event JSON shape. A Go test invokes the existing private `eventDataToTypesEvent` mapper and compares its serialized output to that fixture; the browser adapter consumes the same expected output. No second summary or change formatter was introduced in Loom.

The initial producer fixture incorrectly used entity type `issue` for every action. Loom rejected it. The fixture generator now uses Fleet's canonical action-to-entity mapping and validates every native event before recording it; client validation was not weakened.

Selected Fleet recovery also checks `event.read`, in addition to `issue.read`, before reading and again after encoding the response. This review found no fallback from a selected request to an unselected reader.

## Validation

Evidence class: deterministic native-contract, HTTP fixture, and UI adapter tests. The actual Fleet formatter generated the golden input, but these tests do not establish paired live services or browser behavior.

- Shared Go corpus: 167 cases passed with the race detector.
- Ordinary Go serving-mapper parity: 46 golden rows passed with the race detector.
- Final focused Go race run: Fleet 1.601s; realtime 1.269s; subscription 1.378s; web UI service 1.349s. The earlier full affected-package run passed all other tests; its invalid-golden failure was corrected and rerun.
- Scoped Go lint, including the service mapper test: zero issues. `git diff --check` passed.
- `make check-frontend`: passed all six gates (format, typecheck, ESLint, architecture, generated-code staleness, unit tests with coverage). All 9,483 tests across 432 files passed; test duration 34.26s. Overall coverage: statements 81.81%, branches 74.26%, functions 80.43%, lines 83.87%.
- `npm run build`: passed after dependency installation completed; Vite built in 2.80s. It emitted the existing advisory for chunks larger than 500 kB.

Go logs: `/private/tmp/loom-recovery-timeline-final-go.log` and `/private/tmp/loom-recovery-timeline-lint.log`. Frontend logs: `/private/tmp/loom-recovery-timeline-frontend-gate.log` and `/private/tmp/loom-recovery-timeline-frontend-build.log`.

An initial build attempt overlapped the gate's dependency installation and failed because `tsc` was temporarily unavailable. The sequential rerun passed after the gate finished; that setup failure is not classified as a source regression.

The [real paired browser run](sse-timeline-browser-proof.md) verified ordinary history/comment rendering and reproduced stale board state while the strict feed returned 503 on local-mode Redis. Successful SSE recovery remains unverified; no deployment or cache-publication guarantee is asserted.
