# Bounded recent-activity recovery

The subsequent [file query proof](sse-file-query-recovery-proof.md) covers
Git status and file metadata. The scope below describes the activity milestone.

Home recent activity now participates in the query recovery coordinator. A
recovery attempt reads the issue store synchronously, selects the five most
recently updated non-epic issues and requests fifteen events per issue. Every
selected history must succeed before the request commits. Ordinary read failures
retain ambient activity; strict recovery rejects the attempt.

The participant exposes the issue map generation as a recovery revision. If
issue recovery replaces that map before the barrier finishes, activity reads
again, even when React has not rendered or the selected IDs remain unchanged.
This avoids certifying history selected from an outdated issue list. Existing
issue-store writes replace maps rather than mutating them in place.

The scoped request owner cancels stale workspace/coordinator requests, fences
loaders that ignore abort, and makes ordinary refreshes join active recovery.
Successful history merges with live arrivals by event ID, preserving the
150-item buffer. Home intentionally includes all repositories; its activity
input is the unfiltered issue-store map.

## Honest history responses

The frontend history API forwards cancellation and validates the success
envelope, array shape, issue identity, timestamps and consumed event fields.
The HTTP handler emits an explicit empty array after a successful empty read.
Missing Fleet or HTTP-backend history payloads and missing backend results
fail the read rather than masquerading as empty history. Fleet pages require an
explicit boolean `has_more`; a missing flag cannot silently truncate history. Pagination must provide a usable next cursor
when it reports more history.

## Limits

This proves recovery of a bounded selection, not complete workspace history or
an atomic snapshot. The current Fleet newest-tail adapter still scans the full
issue history to select its tail; the returned limit does not bound server
scanning. Multi-page cycle detection is also still pending; the service timeout
bounds a failed traversal. A committed projection fence, explicit retained cursor reset protocol,
remaining file-related queries, real database-server restart and paired browser
proofs remain outstanding. No SSE checkpoint reset is introduced here.

## Validation

- Full frontend suite: 412 files and 8,916 tests passed. A subsequent additional
  coordinator-replacement regression passed in the final 29-test activity run.
- History API: 20 focused tests passed; frontend typecheck and scoped ESLint
  passed.
- Full race-enabled tests passed for the HTTP and Fleet backends, issue service
  and issue handlers. Fault cases cover malformed/missing initial payloads, missing later
  pages, nil backend results, and canonical successful empty arrays.
- Scoped Go lint reported zero issues; frontend formatting passed. Independent
  review vetted activity ownership/dependencies and the Fleet/service/handler
  contract; identified response-shape issues were repaired.

These are deterministic frontend tests, Go tests using isolated local HTTP
fixtures, and static checks. They do not provide real storage restart or paired
application/browser recovery evidence.
