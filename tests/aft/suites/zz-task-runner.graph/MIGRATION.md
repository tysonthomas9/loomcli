# Task Runner graph equivalence

This graph replaces eleven legacy executions with eleven unique terminal
leaves. Scenarios 1-5 share the nested agents-page -> create-dialog -> Task
Runner-template tree. All original mechanics remain in their original order.

Independence proof: the graph uses `fixtureScope: path`, so every execution
creates its own `E2E-TR-${AFT_CASE_KEY}` workspace, two case-owned source
repositories and bare remote, `worker-a`, and lead companion before its leaf.
This removes contamination from delegation, git/worktree mutation, and
idle-work scenarios without depending on workspace deletion to erase retained
checkout directories. Every leaf-specific agent, issue, log, worktree, and AFT
work file is created by the fixture or by that same leaf before it is read.
`AFT_CASE_ID` remains the stable report identity; AFT owns the compact,
uppercase, run-scoped `AFT_CASE_KEY` used for Loom resource isolation.

Added strict-state assertions:

- Every path starts by confirming `about:blank`.
- Scenarios 1-5 confirm the template and name field at the two shared branch
  points.
- Scenarios 1-8 add one terminal observation because their legacy final step
  was not an `expect`. Scenario 6 retains its original final board wait before
  the added Ready-column count.

Scenarios 9-11 move their original trailing waits/assertions into terminal
states without adding a terminal check.
