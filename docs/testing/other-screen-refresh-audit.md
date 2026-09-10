# Other-screen refresh audit — 2026-09-09

The Kanban fix did not remove all refresh flicker. This audit found and fixed two further cases, validated through agent-browser 0.37.1 with 60 fps recordings against the running PostgreSQL/Redis/Loom/FleetDB local-mode stack. Agents used deterministic localdogfood, not paid inference.

| Surface | Finding | Evidence/status |
| --- | --- | --- |
| Monitor bottlenecks | Every background blocked-query fetch replaced existing content with Loading; even a loaded empty result flashed. | Confirmed in browser: five Loading characterData transitions. Fixed: preserve data when non-null; final actual /blocked response completed with zero flashes. Populated-list continuity also covered by mounted-node test. |
| Issue-panel journey | Background history fetch inserted a Loading issue history row above existing events, moving content. | Confirmed insertion after comment mutation. Fixed for populated history: final UI-saved comment triggered two completed /events requests with zero loading insertions. Empty history initial/retry loading remains visible. |
| Issue collection views | Share the store's initial-vs-background loading state. | Prior Kanban lifecycle proof applies to shared state; no separate List/Table/Graph browser mutation proof in this audit. |
| Full issue detail route | No loading insertion observed in the attempted status-change probe. | Inconclusive: the attempted select did not commit a status change, so this is not a valid refresh pass. |
| Skills tree | Catalog invalidation changes loaded→loading, disables scoped tree, and replaces it with Loading skills; expansion reset is possible. | Source-traced candidate, not browser-triggered/fixed. Need create/edit skill through UI while another expanded skills tree is visible. |
| Agent Diff | New commit signal clears files, patches, viewed and expanded state before refetch; loading replaces diff. | Source-traced candidate, not browser-triggered/fixed. Need keep diff open while a deterministic coder produces another commit. |
| Terminal, Usage, Observability, Settings | Primary guards retain existing content when data/tabs/config exist. | Source review only; not exhaustive browser clearance. |
| Ordinary file trees/history | Directory refresh generally retains state; populated file history retains content. Empty history/loading and file-editor loading text may still move layout. | Source review only. |

## Scope safety and tests

ProjectHealthPanel now shows its loading replacement only while blockedIssues is null. useBlockedIssues resets data on query-key changes so repository/filter switches cannot expose the previous scope's rows. Same-key cache remains available during background refresh. IssueDetailPanel only inserts the history loading row when events are empty; owner changes already clear events before paint.

Regression tests assert identical mounted DOM nodes through background refresh for populated and empty bottlenecks, and null data while a new repository scope is pending. Tests fail on prior behavior. 150 focused tests pass across ProjectHealthPanel, IssueDetailPanel and useBlockedIssues. Frontend build/typecheck, scoped ESLint and diff checks pass. Existing build chunk/circular-import warnings remain. Independent reviewer caught the cross-scope fallback risk; final revision reviewed with no blockers.

## Browser evidence

Test actions used product UI only: create LOCALMODE-9/10, comment, approve. Read-only browser Performance entries establish actual endpoint refreshes; no mocked responses or API writes were used. Final history comment visibly changed COMMENTS (0) to COMMENTS (1). Final Monitor trigger was approval of LOCALMODE-10. Comments alone did not trigger Monitor's blocked projection and were not accepted as proof.

Checked-in JSON and red/green test outputs: [evidence](evidence/screen-refresh-flicker-0909). Local recordings: `/private/tmp/sse-screen-audit-0909/history-proven.mp4` and `monitor-proven.mp4`. Recorded with --fps 60; static frames may repeat. An earlier multi-tab screenshot stalled the audit browser, so final accepted proofs use separate named browsers. Failed/uncommitted selector attempts and empty network captures were excluded. The earlier history text excerpt is truncated; the code path and prior lifecycle trace also record the loading insertion.

The user stack remains running at http://localhost:8583. Refresh existing tabs to load the rebuilt frontend. This audit does not claim every screen is flicker-free; Skills/agent Diff and the additional matrix gaps remain follow-up work.

Recording limitation: final Monitor recorder reported only one distinct captured frame despite a 60 fps encoded file; do not use that file as motion proof. Monitor verification rests on the agent-browser DOM observer and completed /blocked request. History recording reported 347 distinct frames. Screenshot capture stalled again after recording; owned audit browsers were stopped without touching the application stack.
