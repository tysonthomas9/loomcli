# Agent Recovery Decision Tree

Use this reference when an agent is active but work is not moving.

## 1. Establish the symptom

Read current state without mutation:

```sh
loom data show <task-id> --output json
loom agentdef list --json
loom daemon queue <agent>
loom workspace ops diagnose --json
loom backend health --json
```

Build one compact assertion containing ticket status, assignee, agent live state, backend, process presence, and transcript/session activity.

## 2. Rank likely causes

Test these in order:

1. No eligible task: queue is empty because status, design, dependency, parent, filter, or source repo excludes every ticket.
2. Backend unavailable: binary is installed but authentication or account readiness is missing.
3. Configuration precedence: assignment says one backend while the role or actual spawned command uses another.
4. Stale process or checkpoint: supervisor state and OS processes disagree.
5. Daemon ownership conflict: a restarted daemon cannot acquire the logical agent lease.

Change one variable at a time and re-run the compact assertion.

## 3. Source-repo mismatch

Signal:

```text
loom daemon queue planner
... repo mismatch
```

Parent epic scope does not override repository affinity. Correct the tickets or agent scope after approval. Prefer ticket migration to cross-repo agent access when work must land in a fork and the original repository must remain pristine.

## 4. Backend authentication stall

Signal:

- `loom backend health --json` reports installed but unhealthy or unauthenticated.
- The Loom leaf process runs with near-zero CPU.
- No backend child or transcript appears.
- The watchdog eventually kills and retries the task.

Do not repeatedly restart the same unhealthy backend. Select a healthy backend after approval and verify both role configuration and actual spawned process.

## 5. Role versus assignment backend

Signal:

- `loom agentdef list --json` says `backend=codex`.
- Process command still contains `--backend claude`.

Inspect `loom role show plan` and `loom role show task`. If the roles remain on Claude, update the role backend after approval, stop stale attempts, and restart. Verification succeeds only when the actual child is the intended backend.

## 6. Quarantine after repeated no-progress kills

Loom may move a ticket to `blocked`, clear its assignee, add `loom:quarantined`, and comment with kill history after repeated retries.

Read that comment before acting. Once the root cause is corrected, ask before reopening:

```sh
loom data update <id> --status open
```

The quarantine label can remain as an audit marker unless the user explicitly asks to remove it.

## 7. Ownership lease after daemon restart

Signal:

- Old backend process is gone.
- `live_status` can still look active.
- New daemon logs repeat `HTTP 409 ... already claimed` every few seconds.
- No current planner process exists.

Do not trust stale live status. Ownership leases can outlive the process and block the same logical agent name.

Offer these options with trade-offs:

1. Wait for lease expiry. Lowest mutation risk, but delays work.
2. After approval, reset the orphaned task to `open`, replace the assignment with a new logical name, pin its first cycle to the task, and verify the new claim. This avoids touching repository files.
3. Use `loom recover` only after explaining its behavior. It can analyze tasks through a backend and clean untracked files; never run it blindly in a dirty multi-agent workspace, and never use `--force` without explicit approval.

Do not directly edit FleetDB storage or bypass ownership fencing.

## 8. Safe stop/restart sequence

After approval:

1. `loom agentdef stop <name>`.
2. Poll assignment state and OS process state.
3. If graceful yield times out, inspect daemon logs before considering `--force`.
4. Preserve `.agent.checkpoint.json`, `.agent.lock`, and user/agent worktree changes unless an approved recovery command owns their cleanup.
5. Apply the smallest configuration change.
6. `loom agentdef start <name>`.
7. `loom workspace ops ensure-runtime --json`.
8. Verify ticket claim, queue, actual backend process, and session activity.

## 9. Evidence to show the user

Show:

- Backend health verdict and message.
- Actual `--backend` value from a truncated process command.
- Ticket status and assignee.
- Queue match/filter reasons.
- Exact daemon error line.
- Whether repository files were touched.

Do not show:

- Full agent prompts in process arguments.
- Environment variables or credentials.
- Raw multi-megabyte JSON or logs.

