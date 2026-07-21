# Store slices own mutations and shared UI state in the web frontend

Issue mutations, agent-panel view state, and workspace actions were each implemented ad hoc by the components that needed them, which produced three copies of status-change logic with divergent delegation semantics, two unsynchronized writers of one persistence key, and per-mount duplicates of transient state (delete-undo timers, job polling). We decided these live in store slices: issue mutations are issue-store actions (owning optimistic updates, rollback, per-mutation status, and the delegation policy — assigning an agent starts its work, on every surface including bulk); panel view state is a slice keyed by workspace+agent with a single writer (write-through for discrete state, debounced for typing); workspace actions are a slice owning the undo timer, job polling, and one invalidation strategy, with the sync-vs-async clone distinction hidden behind the action contract.

Surfaces never call mutation APIs directly. A component that needs to change an issue or workspace calls a slice action; the alternative — each component wrapping the API client with its own saving/error state — is the regime this decision retires.

## Consequences

- `useWorkspaceViewActions.updateIssueStatus` and the per-component `updateIssue` calls are absorbed and deleted as the migration completes.
- The assignee prompt hangs off the transition (any surface reporting `needs_assignee`), not the kanban drag gesture.
- Single-instance semantics (one undo timer, one job poll, one persistence writer) are structural, not conventional.
