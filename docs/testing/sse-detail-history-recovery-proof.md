# Detail history and comment recovery

The panel previously converted a failed history GET into an empty Journey and only retried when `updated_at` changed. Its comment form could deliver an old submission into a new selection, while a subsequent detail read could erase or duplicate a confirmed local comment.

`useIssueHistory` now owns an abortable 200-event read for a committed workspace and issue. It joins the ordinary query recovery coordinator, supersedes pre-recovery work, propagates failure and preserves same-scope rows with an explicit error. A retry button requests another read. Successful empty history remains a valid empty response. Detail object changes, relevant mutations and connection epochs invalidate history even when timestamps match. Invalidations during recovery increment a revision and require another pass before completion.

App passes the intended route/pending-panel/active-panel ID separately from the last loaded detail. History therefore enrolls B while A's detail is retained during loading. Scope retirement, cancellation, malformed arrays and foreign issue IDs cannot publish. A suspended speculative render does not retire the committed owner.

Comment submission uses a committed selection and per-submission owner. Late success, error, cleanup and focus effects cannot affect another issue, another workspace or an A-B-A replacement. Returned comments must belong to the requested issue. Confirmed local comments form a separate selection-scoped overlay: pre-write reads cannot erase them, authoritative appearances deduplicate and retire them, and subsequent authoritative deletion is respected. This overlay does not claim recovery certification.

## Evidence and limits

- Full frontend Vitest: 9,189 tests across 422 files passed in 29.96 seconds (`/private/tmp/sse-stack-review/history-frontend-full.log`).
- Focused coverage: 10 history hook tests, 38 comment form tests, 3 confirmed-comment overlay tests and 93 panel tests passed. The panel/coordinator integration proves a pre-recovery response cannot satisfy intended B history while loaded detail still contains A.
- Typecheck, ESLint, architecture checks and production build passed. ESLint retains 26 existing warnings; the production build retains the chunk-size warning. Logs: `/private/tmp/sse-stack-review/history-{typecheck,lint,arch,build}.log`.
- Final independent review reported no remaining blocker in this delta.

 Independent review identified the loaded-detail/selected-history dependency gap, prompting explicit selection propagation and a component test with the real coordinator.

This is ordinary bounded history repair, not full-history completeness or a certified snapshot. Independent detail/history GETs do not share a database snapshot or commit fence. Comment reconciliation does not fence every other UI mutation callback. Native graph/detail/comment/history coverage, one cache publication generation, checkpoint acknowledgment and real paired FleetDB/browser SSE proof remain outstanding. No SSE checkpoint behavior changes here.
