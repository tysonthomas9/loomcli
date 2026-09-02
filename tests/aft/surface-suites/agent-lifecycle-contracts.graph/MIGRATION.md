# Agent lifecycle contract graph equivalence

This graph replaces five legacy executions with five unique terminal leaves.
Scenarios 3 and 5 share the nested agents-page -> create-dialog -> Task Runner
template trunk. All original mechanics remain in their original order.

Independence proof: the graph-scoped fixture creates only the two workspaces and
repo metadata. Each leaf owns a distinct outcome and identity (`orphan`, missing
agent, `delete`, `life`, or `health`) and every work file it reads is produced by
the fixture or by that same leaf. The recreated `delete` agent is never read by
another leaf.

Added strict-state assertions:

- Every path starts by confirming `about:blank`.
- Scenarios 3 and 5 confirm the template is visible and the name field is
  ready at the two shared branch points.
- Each terminal repeats one minimal observable state because all five legacy
  scenarios ended in `run` or `api`: the modal error, monitor panel, agent name,
  or unchanged blank browser, as appropriate.
