# Issue creation graph equivalence

This graph replaces four legacy executions with four terminal leaves. Every
leaf keeps its original name, test intent, route setup, and ordered mechanics.

The board-entry transition is a real shared trunk. The blank-title path forks
before first-session hydration because that wait was not part of its legacy
mechanics. The basic-create path retains that wait, while the full-field and
duplicate paths share the ordinary mounted-form branch.

Added strict-state assertions:

- Every path starts by confirming `about:blank`.
- Board readiness also confirms the toolbar is visible.
- Basic creation repeats the title-scoped one-card observation after its
  durable event readback.
- Full-field creation adds a title-scoped Backlog count after its original
  reload wait.

The duplicate path moves its existing final count and error assertions into the
terminal state.
