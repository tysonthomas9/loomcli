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

Original graph IDs and existing mechanic order are retained. The CUS-D5 pilot extends its path
with the agent-detail route and a grouped terminal-state assertion, and its human-facing name now
describes controlled-runtime connection plus literal prompt delivery. Graph states add explicit
observable contracts and the Flow view collapses shared transition screenshots without collapsing
the 22 independent execution results.

The controlled-Codex bootstrap support lives in the shared deterministic stub, so it can affect
other Codex terminal scenarios even though the graph edit is limited to CUS-D5. This pilot validates
only CUS-D5; dependent suites remain a rollout gate before applying the pattern corpus-wide.
