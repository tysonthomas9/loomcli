# Fleet-DB Query Parity

**Status:** Acceptance note for `loomcli-wpltp.2.3`
**Date:** 2026-05-01
**Gate:** G1, `make test-fleetdb-cli`

## Covered Query Surface

The fleet-db-only CLI gate now asserts deterministic query results for:

- `list --limit`
- `list --type`
- `list --assignee`
- `list --status`
- `ready --limit`
- `ready --priority`
- `blocked`
- `search`
- `count`
- `count --by-type`
- `count --by-status`
- `stats`

The gate runs every CLI fixture against an isolated fleet-db/miniredis process
and strips `PATH`, so accidental `bd` dependencies fail.

## Supported Fleet-DB Filters

These are accepted parity filters for fleet-db default mode:

| Backend option | CLI coverage | Notes |
|---|---|---|
| `ListOpts.Status` | `05_list_filters.json` | Maps to `list -status` |
| `ListOpts.IssueType` | `05_list_filters.json` | Maps to `list -type` |
| `ListOpts.Assignee` | `05_list_filters.json` | Maps to `list -assignee` |
| `ListOpts.Labels` | Covered by label fixtures; broader metadata ticket owns more | Server-side all-label semantics |
| `ListOpts.ParentID` | `03_create_with_parent.json` | Used for parent/child query paths |
| `ListOpts.Limit` | `05_list_filters.json` | Server-side limit |
| `ListOpts.SourceRepos` | Not yet in CLI fixture | Workspace/repo scoping ticket owns full coverage |
| `ListOpts.UpdatedAfter/UpdatedBefore` | Not yet in CLI fixture | Supported by fleet backend query builder |
| `ReadyOpts.Priority` | `09_ready.json` | Deterministic one-result assertion |
| `ReadyOpts.Assignee/Type/ParentID/Limit` | Partially covered | Remaining combinations are lower-risk variants |
| `BlockedOpts.ParentID/Assignee/Priority/Type/Limit` | `10_blocked.json` covers base blocked query | Specific filter combinations can be added as regressions |
| `SearchIssues(query, limit)` | `14_search.json` | Uses fleet-db search/list query |
| `CountOpts.Status/IssueType/Assignee/Labels/SourceRepos` | `15_count.json` covers total and group flags | Count returns total only through `IssueBackend.Count` |

## Intentional Non-Parity Before Deletion

The following `backend.ListOpts` fields are still marked unsupported for
fleet-db in `backend/types.go` and guarded by
`internal/backend/fleet/params.go`. They may remain before beads deletion only
because the fleet backend fails closed with `ErrFilterNotSupported` instead of
returning silently unfiltered data.

| Field group | Fields | Decision |
|---|---|---|
| Priority direct/range | `Priority`, `PriorityMin`, `PriorityMax` | Ready supports priority. General list priority filters remain non-parity until fleet-db adds server-side support or the UI stops exposing them. |
| Any-label and ID set | `LabelsAny`, `IDs` | Non-parity. Must not be exposed in fleet-db default UI without implementation. |
| Text contains variants | `Query`, `TitleContains`, `DescriptionContains`, `NotesContains` | Use `SearchIssues`/CLI `search` as the supported search path. Field-specific contains filters are non-parity. |
| Created/closed date ranges | `CreatedAfter`, `CreatedBefore`, `ClosedAfter`, `ClosedBefore` | Non-parity. Updated date ranges are the only accepted date filters today. |
| Empty/null checks | `EmptyDescription`, `NoAssignee`, `NoLabels` | Non-parity. |
| Beads-only/special state | `Pinned`, `Ephemeral`, `IncludeTemplates`, `MolType` | Non-parity. Remove from active fleet-db UX or implement explicitly. |
| Exclusions | `ExcludeStatus`, `ExcludeTypes` | Non-parity. |
| Scheduling filters | `Deferred`, `DeferAfter`, `DeferBefore`, `DueAfter`, `DueBefore`, `Overdue` | Owned by metadata/defer parity follow-up. |
| Performance hint | `AllowStale` | Non-parity; fleet-db default should not expose it as user behavior. |

Before `loomcli-wpltp.9` makes fleet-db default, any active caller that can set
one of these fields must either stop exposing the filter, translate it to a
supported fleet-db query, or keep the fail-closed error visible to the user.
