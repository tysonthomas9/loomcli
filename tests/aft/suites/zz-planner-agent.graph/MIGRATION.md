# Planner agent graph equivalence

This graph replaces eleven legacy executions with eleven unique terminal
leaves. Scenarios 1-5 share their exact agents-page -> create-dialog ->
Planner-template prefix. The tree then branches invalid-name, backend, and repo
scope before the default-Planner creation edge. All original mechanics remain
in their original order.

Independence proof:

- The graph uses `fixtureScope: path`; every execution recreates and tears down
  its own `E2E-PL-${AFT_CASE_KEY}` workspace and optional case-owned empty
  workspace. Its source repository lives under the same case directory, so
  worktrees and branches cannot leak across leaves.
- `AFT_CASE_ID` remains the stable report identity; AFT owns the compact,
  uppercase, run-scoped `AFT_CASE_KEY` used for Loom resource isolation.
- Scenarios 1, 3, and 7-11 create `planner-${RUN_ID:-local}` before entering
  their leaf. Duplicate-name, rail/monitor, idle, active-task, stopped-state,
  and delete/recreate leaves therefore consume state created in their own path.
- Invalid-name, backend, and repo-scope paths branch before default creation;
  their shared prefix is their own original prefix. The repo-less scenario is a
  direct branch from `browser-ready` and does not touch the normal workspace
  Planner prerequisite.
- The fixture creates the lead companion and repo metadata per path. Every
  remaining leaf-specific agent, issue, and work file is produced before it is
  read in that same leaf. Case-derived workspace IDs avoid relying on Loom's
  metadata-only workspace deletion to remove local checkout directories.

Added strict-state assertions:

- Every path starts by confirming `about:blank`.
- The two shared Planner-template assertions are moved legacy checks for
  scenario 1 and additional branch checks for the other paths on that trunk.
- The default-Planner branch point adds one mounted-surface assertion only to
  scenarios 1, 3, and 7-11.
- Scenarios 1-6, 8, 10, and 11 add one terminal observation because their
  legacy final step was not an `expect`. Scenario 10's original final wait is
  retained before the added `notText` assertion.

Scenarios 7 and 9 move their original trailing assertions into terminal states
without adding a terminal check. Scenario 1 now shares the default-Planner
creation and exact persisted Info/API contract with scenarios 3 and 7-11;
its leaf records that already-verified definition as the scenario outcome.
