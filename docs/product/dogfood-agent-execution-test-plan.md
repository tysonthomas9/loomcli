# Dogfood Agent Execution Test Plan

> **Status:** Draft
> **Date:** 2026-05-04
> **Related:** `docs/product/agent-execution-prd.md`,
> `docs/product/local-mode-product-spec.md`,
> `docs/product/container-runner-mvp-spec.md`

## Purpose

Define end-to-end scenarios that prove agent execution is visible and
debuggable from the product.

The dogfood suite should catch regressions where tasks update but agents,
sessions, logs, or artifacts are missing from the UI.

## Test Environments

| Environment | Purpose |
|---|---|
| Local mode | First shippable slice; no containers required. |
| Podman regression stack | Distributed/container runner MVP validation. |
| Browser UI | Confirms real product surfaces update. |

## Required Seed Data

Create controlled tasks:

- planning task: open, high priority, no design
- coding task: open, high priority, approved design
- optional failure task: approved design with known missing gate/remote

Use unique titles and IDs for deterministic selection.

## Scenario 1: Local Planner Visibility

1. Start local Loom server.
2. Start planner from UI or CLI.
3. Verify agent card appears.
4. Verify planner claims the no-design task.
5. Verify task Sessions tab shows running session.
6. Wait for design update.
7. Verify task timeline includes design update.
8. Verify session finalizes as completed.

Expected:

- named agent is visible
- task shows claimed agent
- design field is populated
- session remains after run exit

## Scenario 2: Local Coder Visibility

1. Seed task with approved design.
2. Start coder.
3. Verify agent card appears.
4. Verify coder claims the designed task.
5. Verify session appears.
6. Verify file change appears in diff/files UI.
7. Verify test/gate result is recorded.
8. Verify commit/push result is recorded.
9. Verify task reaches expected final state.

Expected:

- changed file artifact exists
- session transcript/log exists
- task timeline includes claim, file change, test/gate, commit

## Scenario 3: Direct CLI Visibility

1. Run `loom plan` directly from shell with a named agent.
2. Open UI.
3. Verify direct run appears like a daemon-launched run.
4. Verify task Sessions tab is populated.

Expected:

- no invisible CLI run
- warning appears only if server publication is unavailable

## Scenario 4: Container Planner

1. Start Podman stack.
2. Launch planner container through supported runner command.
3. Verify container metadata appears in UI.
4. Verify task claim and session appear.
5. Stop container after completion.
6. Verify session/logs remain available.

Expected:

- `podman run --rm` does not lose artifacts
- run includes container ID/name/image

## Scenario 5: Container Coder

1. Seed approved-design task.
2. Launch coder container.
3. Verify agent/run card appears before model invocation.
4. Verify task claim.
5. Verify file change/diff artifact.
6. Verify commit and push result.
7. Verify final status.

Expected:

- UI updates without manual DB or filesystem repair
- logs and transcript persist after container exit

## Scenario 6: Preflight Failure

1. Start runner with missing backend auth or missing tool.
2. Verify preflight fails before model invocation.
3. Verify task note/timeline records failure when task context exists.
4. Verify recovery action is shown.

Expected:

- no hidden model run
- error class is stable
- recovery action is actionable

## Scenario 7: Stale Runner

1. Start container runner.
2. Kill container during execution.
3. Wait past heartbeat threshold.
4. Verify run is stale.
5. Release claim or mark failed.

Expected:

- stale state is visible
- task does not look actively worked forever

## UI Assertions

Each scenario should verify:

- agent sidebar/card content
- task card claim indicator
- issue detail sessions tab
- issue timeline
- logs/transcript viewer
- diff/files view when applicable
- final status and error class

## API Assertions

Each scenario should verify:

- `/api/monitor/agents`
- `/api/monitor/status`
- `/api/workspaces/{ws}/issues/{id}`
- `/api/workspaces/{ws}/issues/{id}/sessions`
- run/artifact APIs once added

## Exit Criteria

- Planner and coder dogfood paths pass locally.
- Container planner and coder paths pass in Podman.
- Sessions and artifacts survive runner exit.
- No manual worktree/session repair is required.
- Failure cases produce visible recovery UX.
