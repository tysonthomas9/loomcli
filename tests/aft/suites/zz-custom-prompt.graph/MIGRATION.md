# Custom-prompt graph migration

This package preserves the 22 product executions from
`zz-custom-prompt.test.yaml`. Fifteen paths reuse the mounted Create Agent
dialog trunk. The two template-looking-prompt outcomes share a deeper reset and
creation trunk before branching into storage and argv proofs.

Every terminal leaf remains independently runnable. Shared graph fixtures only
bootstrap the two isolated workspaces; any agent, role, prompt file, or saved API
value consumed by a leaf is created on that leaf's root-to-terminal path. Setup
and teardown use `clean-test-workspace.sh`, which closes terminal tabs before
agents and workspaces so interactive prompt cases cannot leak PTYs into sibling
executions or later runs.

Original test names, test intents, routes, and ordered mechanics are retained.
Graph states add explicit observable contracts and the Flow view collapses the
shared transition screenshots without collapsing the 22 execution results.
