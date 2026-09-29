---
name: loom-project-lead
description: Operate a Loom/FleetDB project backlog as an interactive project lead. Use whenever the user asks about Loom epics, ticket validity, plan review, backlog triage, planner or task-runner agents, repository assignment, daemon readiness, blocked or quarantined work, or monitoring agent execution. Inspect first, show decisive command output, obtain approval before every task/repo/agent mutation, and verify actual runtime behavior rather than trusting configured state alone.
compatibility: Requires the `loom` CLI in a configured Loom workspace with FleetDB access.
---

# Loom Project Lead

Manage Loom work from intent through planning, implementation, and monitoring while the user retains authority over mutations.

## Non-negotiable rules

1. Treat FleetDB as canonical. Use `loom data ...`, not `gh`, for issue data.
2. Ask before changing task data, repository definitions, role definitions, or agent assignments.
3. Read-only inspection does not need approval. Use it to make the proposed mutation concrete.
4. Show the output that matters: counts, IDs, dependency edges, source repos, backend health, queue reasons, active task IDs, and decisive errors. Summarize large JSON instead of dumping it.
5. Do not switch branches or clean, reset, stash, or revert worktrees. Preserve changes from users and other agents.
6. Use specific paths when committing. Never stage unrelated work with `git add .` or `git add -A`.
7. Redact credentials and omit full agent prompts from process output.

## Start every lead session read-only

Run the startup sweep:

```sh
loom data list --status=review --output json
loom data blocked --output json
loom data list --status=open --type=epic --output json
loom workspace ops diagnose --json
```

Treat `diagnose` as the readiness gate. A running daemon is not enough if repositories, worktrees, or agents are not ready.

Summarize the results and return control:

```text
What would you like to do?
1. Review plans
2. Create new tickets
3. Triage backlog
4. Check status / ask questions
5. Epic status
6. Manage repos or agents
```

Skip the menu when the user has already authorized a specific operation.

## Understand an epic before activating agents

1. Read the epic with `loom data show <epic-id> --output json`.
2. List its children and inspect each ticket's status, type, source repo, dependencies, description, design, labels, and assignee.
3. State the epic's intended end state in one paragraph.
4. Validate that the tickets form a buildable path:
   - Each ticket belongs to the repository where its changes must land.
   - Dependencies point from consumers to prerequisites.
   - Security-sensitive choices are explicit, not left to the worker.
   - Routine actions are not accidentally blocked by excessive human approval.
   - Deployment, authentication, persistence, and end-to-end acceptance have clear owners.
   - There is a final acceptance ticket spanning the deployed workflow when multiple tickets compose one product.
5. Report gaps and propose exact changes. Wait for approval before applying them.

Repository affinity is operational, not descriptive. An agent scoped to `--repos steve-code-agent` will not claim a ticket whose `source_repo` is `steve`, even when both sit under the same epic.

If `loom data update` cannot change a ticket's source repository, use a migration after approval:

1. Stop affected agents.
2. Create replacement tickets with the correct `--source-repo` and `--parent`.
3. Recreate dependency edges using replacement IDs.
4. Rewire downstream acceptance tickets.
5. Verify the new graph.
6. Close old tickets as superseded, preserving them for history.
7. Restart agents and verify queue matches.

Do not alter a ticket that already has human or agent implementation changes without first inspecting its worktree and review state.

## Review plans correctly

1. List review work with `loom data list --status=review`.
2. Show the selected ticket with `loom data show <id>`.
3. Ask: `Approve this plan, request changes, or skip?`
4. On approval, move it to open and clear `needs-revision`:

```sh
loom data update <id> --status open --remove-label needs-revision
```

5. For changes, add focused feedback and return it to open:

```sh
loom data comment <id> "FEEDBACK: ..."
loom data update <id> --status open
```

Clearing `needs-revision` is essential. A status-only approval can send the ticket back through the planning loop while implementation agents continue skipping it.

## Configure planner and implementation agents

Inspect before changing anything:

```sh
loom workspace ops diagnose --json
loom repo list --json
loom role list
loom agentdef list --json
loom backend health --json
```

Validate backend readiness, not merely installation. An installed backend with missing authentication can enter its login UI and appear to hang under a headless daemon.

For the normal plan-then-build flow:

```sh
loom agentdef add planner --role plan --auto \
  --repos <repo> --parent <epic-id> --task-filter needs_design \
  --backend <backend>

loom agentdef add runner --role task --auto \
  --repos <repo> --parent <epic-id> --task-filter has_design \
  --backend <backend>
```

Use `--task <id>` only to pin the first cycle deliberately.

Backend precedence must be verified empirically. An assignment may display `backend=codex` while a daemon still spawns `--backend claude` from the role. Check both:

```sh
loom role show plan
loom role show task
loom agentdef list --json
```

When the requested backend must govern these roles, update them after approval:

```sh
loom role set plan backend codex
loom role set task backend codex
```

Then start and repair through the desktop-owned runtime:

```sh
loom agentdef start planner
loom agentdef start runner
loom workspace ops ensure-runtime --json
loom workspace ops status --json
```

Do not launch `loom daemon`, use `nohup loom daemon`, or run a duplicate `loom epic run` for an epic already assigned through the UI/backend.

## Verify execution, not configuration

Configuration output can be stale or misleading. Verify all four layers:

1. Ticket: correct status and assignee.
2. Assignment: correct backend, desired state, live status, and active task.
3. Queue: expected tickets match agent constraints.
4. Process/session: actual backend child exists and is producing activity.

Useful commands:

```sh
loom data show <task-id> --output json
loom agentdef list --json
loom daemon queue <agent>
loom workspace ops diagnose --json
```

When checking processes, print only PID, parent PID, elapsed time, and a truncated command. Never expose the full prompt or environment.

Interpret common states carefully:

- Planner `working` plus an `in_progress` ticket and active backend child: planning is live.
- Runner `idle` with `0 tasks match` and tickets filtered as not ready: healthy and waiting for approved designs.
- `desired_state=running` with no process and repeated ownership errors: not healthy, even if `live_status` still says working.
- Session metadata at zero tokens is not proof of a stall by itself; confirm transcript/file activity and the backend child.

## Monitor the pipeline

Use a short polling pass rather than an open-ended terminal dashboard:

```sh
loom data list --status=in_progress --output json
loom data list --status=review --output json
loom data blocked --output json
loom data list --limit=10 --output json
loom agentdef list --json
loom workspace ops diagnose --json
```

Report transitions:

- `open -> in_progress`: agent claimed work.
- `in_progress -> review`: planner produced a design; human approval is next.
- `open with design -> in_progress`: implementation runner claimed approved work.
- `blocked` or `loom:quarantined`: automation stopped repeated failures; inspect the recorded comment before reopening.

After every mutation, show the command result, re-read the affected record, and ask what the user wants next.

## Diagnose stuck agents

Use the detailed decision tree in [references/agent-recovery.md](references/agent-recovery.md). Key rules:

1. Start with `loom workspace ops diagnose --json` and backend health.
2. Distinguish no eligible work from an execution stall with `loom daemon queue <agent>`.
3. Check the actual spawned backend, not only role/assignment configuration.
4. Preserve worktree changes and checkpoints.
5. Treat repeated no-progress kills and ownership-lease conflicts as separate failures.
6. Ask before resetting task status, replacing an assignment, reopening quarantine, or running recovery.

## Command reference

Read [references/commands.md](references/commands.md) when creating or triaging tickets, managing agents, or producing a status report.

## Completion checklist

- Epic intent and ticket graph are coherent.
- Every active ticket has the correct source repo.
- Planner and runner filters match the intended review gate.
- Requested backend is healthy and visible in the actual spawned command.
- Desktop runtime and daemon are running.
- Planner is actively working or has no eligible planning work.
- Runner is actively working or has no approved designs.
- Blocked/quarantined tickets are explained and changed only with approval.
- No unrelated repository files were modified or cleaned.

