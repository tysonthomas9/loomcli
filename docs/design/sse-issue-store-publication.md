# Issue-store publication ownership

The existing ordinary recovery read uses the same timestamp merge as normal list fetches. That merge is not a certified snapshot replacement: it can retain recent local creations, preserve clock-ahead rows, and omit relationship information absent from the native manifest. Do not acknowledge a recovery cursor from its success.

## Writer inventory

All production issue-map writes currently originate in `stores/issueStore.ts`; the audit found no production external `issueStore.setState` caller. The exported Zustand `StoreApi` nevertheless permits bypass, so this is an inventory observation, not an enforced interface guarantee.

| Writer | Current ownership requirement | Remaining certified-publication requirement |
| --- | --- | --- |
| Ordinary list/graph fetch | Active request controller and committed fetch scope | Capture a publication generation; late reads cannot restore pre-publication rows. Replace through a strict prepared-data path, without timestamp merge. |
| SSE mutation | Validate workspace and repository before local application or optimistic buffering; retire subscription callbacks | Events after the snapshot boundary require ordered replay under the accepted generation. |
| Optimistic status write | Exact command entry and current scope owner | Keep pending work separate from certified base state, or defer snapshot acceptance until every relevant command resolves. |
| Command success/failure | Match the initiating entry; an old request cannot remove or roll back its successor | Server completion alone does not prove projection visibility; incorporate committed effects into the acceptance boundary. |
| Rollback timeout | Match the exact entry and scope; retire on scope/reset | Timeout reverts UI state only. It does not settle the HTTP command or make recovery safe. |
| Projection debounce and fetch retry | Cancel on scope retirement; retain bounded refresh scheduling | A retired timer cannot start a writer that supersedes the accepted generation. |
| Reset and scope changes | Retire visible entries, requests, timers and callback owners | A-B-A must receive a new identity, including dormant query state. |

## Command uncertainty and ordinary recovery

An unresolved status request is tracked independently from `pendingIds` and optimistic entries. A UI timeout can remove an optimistic row while the request remains in flight. Resetting or leaving a workspace does not cancel the server-side operation; returning to that workspace cannot treat it as resolved.

Ordinary recovery refuses to start with an unresolved command for its workspace. It also checks a command-admission revision before publishing a response, so a command that starts and finishes during a read cannot be hidden by an empty pending set at completion. Scope, accepted mutations and command transitions change the recovery revision exposed to the query coordinator. If another participant is still pending, a newly invalidated issue participant must reread before the overall barrier completes.

These guards do not prove read-your-writes at the projection layer. They prevent a known false-success case and expose uncertainty until a later read can run. Definitive snapshot acceptance still needs FleetDB's committed-effect fence and a complete view manifest.

## Required final publication seam

A prepared recovery snapshot must be built off-store and carry immutable source, workspace, boundary and coverage evidence. A single synchronous acceptance function must check the exact attempt and scope, require an unchanged publication generation, reject unresolved or unaccounted commands, and replace every covered base cache together. It must fence all ordinary fetches, mutation responses, rollback callbacks, debounce/retry timers and retained consumers before publishing. Optimistic overlays must either be included in the acceptance rules or keep acceptance pending.

The current issue manifest covers native issue/ready/blocked/deferred data only. Graph relationships, selected detail relationships, comments/history, independently filtered blocked queries and other action-dependent families need explicit coverage. Native decoding or a successful ordinary refresh alone cannot authorize checkpoint acknowledgment.
