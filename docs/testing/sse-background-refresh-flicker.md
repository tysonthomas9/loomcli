# Background refresh flicker regression — 2026-09-09

Creating a task and running planning → approval → implementation removed the entire Kanban board during successful background collection refreshes. The issue store set `isLoading` on every fetch; `IssueViewGuard` replaced the mounted board with skeletons. The idle-board test did not exercise this path.

The store now records whether the active query scope has committed a snapshot, including an empty snapshot. Same-scope refreshes preserve the board. Scope retirement/reset clears this marker; scope replacement atomically clears issues and sets loading. Explicit fenced recovery continues to block rendering. The marker is assigned before notifying subscribers so reentrant scope retirement wins. Authoritative refresh scheduling, mutation reconciliation, and error handling remain intact.

## Validation

- Four regression cases cover populated/empty background refreshes, repository scope change, and reset/recovery. The two background cases fail against the previous store and pass with the fix.
- 158 focused tests passed across issueStore, issueStoreWriteOwnership, and IssueViewGuard. TypeScript/Vite build, scoped ESLint, Prettier, and git diff checks passed. Build emits existing chunk/circular-import warnings.
- Independent sub-agent reviewed implementation and regression tests: no blocking findings.
- Browser path: `New Issue` → automatic planning → open Review task → `Approve` → automatic implementation → `Completed` → task `Runs` tab. No API writes or mocked browser responses. Read-only session API confirmed both phases completed with exit 0, and implementation changed one file (+5 lines) with transcript/diff.
- Before: LOCALMODE-7, four sampled empty-board intervals (20, 22, 18, 16 ms), eight whole-board removals.
- After: LOCALMODE-8, zero loading-skeleton samples and zero removals of existing baseline cards. DOM observation ran from before creation through completion; task card movements are allowed. Existing completed ungrouped lane is normally hidden after completion, so the test opened `Completed` to inspect it.
- Agent-browser upgraded to 0.37.1 and recorded at 60 fps. Fixed recording: `/private/tmp/sse-flow-fixed-0909/create-plan-implement.mp4`; baseline: `/private/tmp/sse-flow-0909/create-plan-implement.mp4`. Video files remain local. Encoded fps does not imply every frame is distinct; idle frames may be repeated and long idle gaps compressed.
- An automation click initially opened the adjacent rejection form; it was canceled without submission. A fresh-reference Approve click was verified to move the task to Open. Completion waits that assumed a permanently visible Done card timed out because the completed lane is hidden; final UI and sessions establish completion.

Checked-in evidence: [results](evidence/pg-sse-flicker-0909/result.json), [sessions](evidence/pg-sse-flicker-0909/sessions-final.json), [Runs screenshot](evidence/pg-sse-flicker-0909/completed-runs.png), and red/green test logs in the same directory.

The existing `loomcli-pg-browser-manual-0909` stack remains running at http://localhost:8583/ws/LOCALMODE/kanban. It uses real PostgreSQL/Redis/Loom/FleetDB with deterministic localdogfood agents, not paid AI inference. Only the frontend was rebuilt; users must refresh existing tabs. This fix does not claim completion of the wider SSE recovery architecture.
