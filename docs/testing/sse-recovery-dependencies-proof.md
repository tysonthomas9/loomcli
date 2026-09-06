# Native recovery dependency contract

The recovery offer, backend reader, registry and frontend preparer now require `fleet.issue-workspace.v3`. Its ninth required field is the complete directed `dependencies` collection. Each dependency has exactly five fields: issue_id, depends_on_id, type, created_at and created_by. Only Fleet's five types are accepted. Both endpoints must occur in the manifest issue collection; self-edges, duplicate typed triples, malformed metadata and missing arrays fail validation. Related reverse rows remain distinct records.

The Go bridge preserves the original native document bytes. The frontend freezes the full dependency records and declares dependency coverage explicitly. Neither layer substitutes empty data for a v2 payload. Dependency coverage means the stored collection exists in the native snapshot; graph transforms, detail relationship publication, comments/history and all-view certification remain separate work.

Validation includes strict shared Go/browser corpus cases, explicit registry v2 rejection, existing recovery ownership regressions, and package races. The producer's real PostgreSQL test demonstrates that concurrent dependency deletion cannot mix a newer graph with an older certified head. Final commands/results are recorded with the draft PR; local logs use `/private/tmp/sse-stack-review/recovery-dependencies-*`.

This is a coordinated strict producer/consumer cutover. It does not publish browser caches, acknowledge a checkpoint or establish deployed paired-browser behavior. No merge or deployment is included.

Local validation: `make check-frontend` passed all six stages, with 426 files and 9,306 tests (81.69% statement coverage). Independent review then found four malformed timestamp forms accepted by Go's permissive parser; all four failed in the negative control and now reject. The final 63-case shared corpus passed both Go race and TypeScript runs. Backend Fleet/realtime/subscription race tests passed; scoped Go lint reported zero issues.

Production frontend build passed in 2.62s. Independent review verified timestamp parity and strict version rejection after the fix; no remaining blocker was found within this manifest scope.
