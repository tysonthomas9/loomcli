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
- Verified FleetDB agent lifecycle endpoints over the rebuilt compose stack:
  create, start, yield, restart, and stop.

## Fixed During This Pass

- Multi-line repo path and clone URL paste now splits into separate entries.
- Header `+ New Issue` remains accessible at the tested browser width.
- FleetDB workspace creation no longer uses sync clone fallback when async clone is unavailable.
- FleetDB agent registration now creates local worktrees, so Files/Git/Diff work without manual checkout.
- Sidebar `+ Add agent` now opens a real agent creation modal instead of the workspace modal.
- Files and agent side panel include FleetDB-configured agents even when monitor data is empty.
- Agent lifecycle endpoints in FleetDB store mode now update `state` and
  `desired_state` instead of returning `not_implemented`.
- Store-backed workspace lookup now returns explicit load errors instead of
  treating FleetDB failures as workspace misses.

## Remaining Findings

### ISSUE-001: Residual Fallback Paths Need A Deliberate Cleanup Pass

Severity: Medium

Evidence:
- `rg fallback internal cmd` still finds production compatibility paths and fallback_backends fields that were not removed in this pass.
- Follow-up filed as `loomcli-j94pc` and addressed for FleetDB workspace lookup and lifecycle placeholders. Remaining matches are non-Beads compatibility or AI backend fallback fields.

Expected: remaining fallback references are either removed or explicitly documented as non-Beads compatibility behavior.
