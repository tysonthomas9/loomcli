# FleetDB UI Dogfood Report

Target: http://localhost:8091
Backend: clean FleetDB compose stack on ports 8090/8091/8092
Session: loom-fleetdb-dogfood-3

## Validation Covered

- Created a workspace from a clean FleetDB-backed UI using local repos.
- Created issues in a multi-repo workspace.
- Created a FleetDB agent from the sidebar using `+ Add agent`.
- Verified the server created a local git worktree for that FleetDB agent.
- Verified Files view can select the new agent and read `README.md`.
- Verified Git status for the new agent reports the FleetDB-created worktree.
- Verified agent side panel Git, Diff, and Files tabs render FleetDB-backed data.
- Verified committed agent diff renders `README.md` with `+ui edit`.

## Fixed During This Pass

- Multi-line repo path and clone URL paste now splits into separate entries.
- Header `+ New Issue` remains accessible at the tested browser width.
- FleetDB workspace creation no longer uses sync clone fallback when async clone is unavailable.
- FleetDB agent registration now creates local worktrees, so Files/Git/Diff work without manual checkout.
- Sidebar `+ Add agent` now opens a real agent creation modal instead of the workspace modal.
- Files and agent side panel include FleetDB-configured agents even when monitor data is empty.

## Remaining Findings

### ISSUE-001: Agent Lifecycle Controls Are Still Not Implemented In FleetDB Store Mode

Severity: Medium

Evidence:
- `POST /api/workspaces/{ws}/agents/{name}/start|stop|restart|yield` still returns `not_implemented`.
- Follow-up filed as `loomcli-lqf7e`.

Expected: FleetDB/local-supervisor mode has a clear start/stop/restart/yield contract, or the UI hides controls that cannot work.

### ISSUE-002: Residual Fallback Paths Need A Deliberate Cleanup Pass

Severity: Medium

Evidence:
- `rg fallback internal cmd` still finds production compatibility paths and fallback_backends fields that were not removed in this pass.
- Follow-up filed as `loomcli-j94pc`.

Expected: remaining fallback references are either removed or explicitly documented as non-Beads compatibility behavior.
