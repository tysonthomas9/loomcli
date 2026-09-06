# Selected detail ordinary recovery

This change enrolls App's selected issue detail in the existing query recovery coordinator. A recovery pass starts a fresh abortable GET; pre-existing reads cannot satisfy it. Errors, malformed or foreign-ID responses, cancellation and late responses fail the pass instead of returning empty success. An explicit HTTP 404 clears the missing record and preserves failure; transient errors leave the route open with its error state.

Each committed workspace and selected issue owns its request. Switching selection registers the replacement before unregistering the previous participant. Ordinary invalidations increment a revision, so an invalidation arriving during recovery requires another read before the barrier completes. Old callbacks, workspace A-B-A responses and abandoned Suspense renders cannot retire or write the current selection.

App treats changed list rows as invalidations for the intended route or panel selection, rather than copying fields based on timestamps. This prevents clock-ahead stale rows from overwriting full detail results and avoids undefined-assignee patch loops. Same-ID workspace changes refetch using the new committed callback. Panel reopen, scope retirement and unmount cancel delayed detail cleanup.

## Boundaries

This is an ordinary GET recovery participant, not a native snapshot certificate. It does not consume recovery handles, acknowledge a cursor, suspend SSE retries or prove graph/detail/history coverage. Detail history and comments have separate ownership; their full recovery contracts remain outstanding. The publication generation for all issue-store writers is also outstanding.

## Validation

- Full frontend Vitest: 9,167 tests across 420 files passed (28.68 seconds), recorded in `/private/tmp/sse-stack-review/detail-frontend-full.log`.
- Focused hook/API: 169 tests passed; App: 128 tests passed. Tests cover pre-recovery supersession, 404/503 failure, selection handoff, revision reread, malformed ID, late ignored abort, pending-B/old-A list changes, same-ID workspace change and close/reopen cleanup.
- Typecheck, ESLint, architecture checks and production build passed. ESLint retains 26 existing warnings and the build retains its chunk-size warning. Logs: `/private/tmp/sse-stack-review/detail-{typecheck,lint,arch,build}.log`.
- The App view-change regression failed before removing the unwanted view-triggered clear, then passed after the fix.
- Independent review identified the last-loaded-A/pending-B invalidation race; invalidation now derives its authority from the selected route/panel ID. Final review reported no remaining high-impact finding in this delta.

This is package and component test evidence, not a real paired FleetDB/browser SSE proof. Hosted CI status is separate.
