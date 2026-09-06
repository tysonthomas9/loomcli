# Browser recovery offers and selected-detail scope

The browser now exposes an optional, strictly decoded recovery offer on expired resync notifications. The decoder checks the native five-field shape, canonical random handle, manifest, current workspace, future UTC expiry and immutable repository scope used for the stream URL. The offer and copied repository list are frozen. Invalid offers are omitted without dropping the ordinary resync reason or changing the accepted checkpoint.

Validation uses the actual fetch-event-source library: retired callback rejection, valid and malformed offers, workspace/repository mismatches, and a connected `connect` call that updates saved configuration without changing the active wire subscription. The latter must continue validating offers against the original stream filter. Go registry expiry serialization is canonical UTC even when the server clock uses another zone.

The browser audit also reproduced selected-detail workspace races. `useIssueDetail` now owns a memoized scope identity committed in a layout effect. Workspace changes clear visible details and reject late requests and saved fetch/edit/clear callbacks; edits also require the currently selected issue ID. A committed A→B→A gets a different identity. A speculative B render that suspends leaves the still-visible A callbacks valid. Unmount cleanup invalidates outstanding request IDs.

Two new workspace regressions failed before the fix: late A→B→A completion republished old detail, and a workspace change retained loaded detail. The final detail suite covers those cases, stale callbacks and the suspended-render case. Reintroducing render-time scope mutation makes the Suspense regression fail; the production source was restored afterward. This is hook-level and simulated-stream evidence, not a browser/service run.

## Validation

- 101 focused stream/offer tests passed (82 stream tests and 19 decoder cases).
- 33 detail-hook tests passed, including controlled late responses and Suspense.
- Go registry race tests passed with non-UTC producer serialization checked.
- Full frontend unit suite passed: 9,063 tests in 417 files. Typecheck, lint (no errors), architecture checks and production build passed; changed-file lint is clean. Existing unrelated lint and bundle-size warnings remain.
- Independent review identified and resolved the speculative-render and active-wire-filter gaps.

## Scope

Offer decoding is not recovery attempt ownership. Automatic HTTP retries still share a library-loop generation. No certificate fetch, retry suspension, cache publication or checkpoint acknowledgment is enabled. The [browser coverage contract](../design/sse-browser-recovery-coverage.md) records the graph/detail/filtered-cache gaps and publication-generation requirements that must be implemented next. The selected-detail scope fix prevents workspace leakage but does not certify detail recovery.
