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

**Actor fidelity**:
The tier-1 test rule that every mutation is attributable to the actor the scenario's intent names — agents mutate through the API (curl/`api:` is their real path), humans drive mounted UI controls. Suite hooks provision fixtures freely; the rule governs test bodies.
_Avoid_: realistic test, no-mocking

**Readback**:
An API read that verifies the outcome of a UI action inside the same scenario. Permitted in product-correctness suites; distinct from a standalone contract probe, which belongs in a surface suite.
_Avoid_: contract test (when a readback is meant)

**Surface suite**:
A test under `tests/aft/surface-suites/` that keeps product surface regression-guarded without claiming a user scenario — fabricated fixtures, UI-orphaned endpoints, standalone contract probes. Each carries the condition that would promote it to the product-correctness tier.
_Avoid_: smoke test, coverage test

**Issue mutation**:
Any change to an issue's fields or status. Issue mutations flow through the issue store's actions; surfaces never call the API directly.
_Avoid_: issue update, patch

**Delegation**:
Assigning an agent to an issue. Delegation starts the agent's work — assignment and starting are one gesture, on every surface including bulk.
_Avoid_: assignment (when starting is meant)

**Launch spec**:
Everything needed to start an agent's terminal process — command, environment, working directory — plus the permission to do so. Built in one place; a caller cannot obtain a spec it may not launch.
_Avoid_: launch args, spawn config
