# Loom

Loom orchestrates coding agents over issue workspaces: agents mutate workspace state through the API/CLI while humans observe and steer through the web UI. This glossary records the canonical language for concepts that recur across the product, the test harnesses, and architecture reviews.

## Language

**Agent artifact**:
Anything an agent's work leaves on disk — a worktree, a log, or a session record (transcript, diff, metadata).
_Avoid_: agent output, agent files

**Layout owner**:
The single module that owns where an agent-artifact family lives on disk. Worktrees belong to `localworkspace`, logs to `webui/log`, session records to `sessions.Store`; no other code constructs those paths.
_Avoid_: path helper, path convention

**Seeding seam**:
The supported entry point through which tests create agent artifacts. Anything seeded through it is indistinguishable from runtime-produced state because it runs the product's own creation flows.
_Avoid_: fixture hack, test scaffolding

**Seed command**:
A hidden, test-gated CLI command that exercises the seeding seam from another process (test harness, container).
_Avoid_: test endpoint, debug command

**Issue mutation**:
Any change to an issue's fields or status. Issue mutations flow through the issue store's actions; surfaces never call the API directly.
_Avoid_: issue update, patch

**Delegation**:
Assigning an agent to an issue. Delegation starts the agent's work — assignment and starting are one gesture, on every surface including bulk.
_Avoid_: assignment (when starting is meant)

**Launch spec**:
Everything needed to start an agent's terminal process — command, environment, working directory — plus the permission to do so. Built in one place; a caller cannot obtain a spec it may not launch.
_Avoid_: launch args, spawn config
