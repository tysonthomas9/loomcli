# Agent flow graph equivalence

This graph replaces five legacy executions with five unique terminal leaves. The
original name, test intent, action order, routes, and mechanics are retained as
an ordered subsequence of each lowered path.

Independence proof:

- The graph uses `fixtureScope: path`; every execution receives its own
  `E2E-AF-${AFT_CASE_KEY}` workspace and a same-case source repository.
- `AFT_CASE_ID` remains the stable report identity; AFT owns the compact,
  uppercase, run-scoped `AFT_CASE_KEY` used for Loom resource isolation.
- Setup first removes only that case's incomplete workspace and rebuilds its
  fixture repository. Teardown enumerates terminal tabs and agents before
  deleting the same case-owned workspace, so a failed path cannot poison or
  delete another path's state. This also avoids relying on workspace deletion
  to remove the checkout directory, which is intentionally retained by Loom.
- Scenarios 1, 3, 4, and 5 replay the mounted UI trunk that provisions `nova`;
  the standalone epic-completion scenario does not.
- Scenarios 2 and 4 each replay the epic/task/workflow block on their own real
  prerequisite branch. That block writes `agentEpicId`, `agentTaskId`, and
  `agentRunId` inside the consuming path.
- Scenario 3 writes its own diff fixture before reading it. Scenario 5 creates
  its own `iris` agent. No path reads a sibling path's product rows or work
  files.

The Storyboard therefore shows a four-execution `nova` trunk with a nested
session-history epic branch, alongside the standalone epic-completion branch.
No leaf is forced through a product setup it does not consume.

Added strict-state assertions:

- Every path starts by confirming `about:blank`.
- The `nova` and epic branch-point states confirm their mounted surfaces.
- Scenario 3 confirms the diff marker after its original wait.
- Scenario 4 confirms the session-detail view remains mounted.
- Scenario 5 confirms the second agent remains visible.

The terminal assertions for scenarios 1 and 2 are moved legacy assertions, not
new checks. Added prerequisite mechanics precede each leaf's intact legacy
mechanics.
