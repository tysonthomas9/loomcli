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

## Execution and Streaming Context

Shared language for Loom's domains. Task execution context first, streaming second.

## Execution Language

**Repository Set**:
The exact collection of repository names selected as the preferred working context for a Task. It determines what appears in a TaskRun Root, but is not an authorization or filesystem-isolation boundary; repository groups are expanded to exact names before being stored.
_Avoid_: Workspace, repository scope, repo allowlist

**Repository Access Policy**:
The independent policy that governs which repositories an AI backend may read or modify. It is not expressed by Repository Set membership.
_Avoid_: Repository Set access mode, working-context permissions

**Primary Repository**:
The optional single repository a Task principally belongs to for ownership, filtering, and linking.
_Avoid_: Only repository, execution scope

**Selected Repository**:
A repository included in a Task's working context, whether or not the Task has a Primary Repository.
_Avoid_: Additional Repository, secondary workspace, dependency allowlist

**Task Repository Set**:
The Repository Set formed by a Task's optional Primary Repository and its Selected Repositories. A TaskRun begins with this set; a repository acquired by an active run is also added to the Task for future runs.
_Avoid_: Worker profile repos, agent affinity

**Repository Acquisition**:
The Loom-mediated act of adding an exact registered repository to both an active TaskRun and its Task because the AI backend discovers that it needs that repository.
_Avoid_: TaskRun repository override, implicit workspace access

**TaskRun Root**:
The directory in which an AI backend starts for a TaskRun. It belongs exclusively to that TaskRun, contains one repository checkout for each member of the Task Repository Set, and is reused only by scheduler retries of the same run.
_Avoid_: Workspace, global workspace, cross-repository worktree

**TaskRun Root Manifest**:
The durable inventory of the repository checkouts belonging to one TaskRun Root together with that root's lifecycle and retention identity.
_Avoid_: Worktree counter, directory scan

**TaskRun Root Manager**:
The node-local owner of TaskRun Root provisioning, physical inventory, retention, reconciliation, and cleanup. It realizes the lifecycle FleetDB records for a TaskRun without making FleetDB responsible for node-local paths.
_Avoid_: Git garbage collector, Task scheduler, FleetDB filesystem manager

**Task Change Set**:
An immutable version of the repository-keyed backend commits produced by an implementation or correction TaskRun and confirmed on shared remotes. Review and correction work always name the exact Task Change Set they consume.
_Avoid_: Shared working directory, patch-only handoff, uncommitted review state

**Task Branch**:
The stable branch a Task owns in each repository it changes. AI backends append Task commits to these branches, which later TaskRuns consume through Task Change Sets.
_Avoid_: Shared workspace branch, TaskRun branch

**Valid TaskRun Completion**:
A completion claim made only when every checkout is clean and every changed repository has a backend-authored commit confirmed on its Task Branch remote. A clean no-change result is valid; unfinished changes require Backend Continuation, and an unrecoverable push failure fails the execution.
_Avoid_: Loom auto-commit, successful dirty exit, best-effort push

**Backend Continuation**:
Another turn in the same native AI backend session and TaskRun Root, used when completion validation finds unfinished work such as uncommitted changes.
_Avoid_: Cold scheduler retry, replacement backend session

**Git Push Proxy**:
The Loom-owned operation that pushes and verifies backend-authored commits without exposing remote credentials to the AI backend process.
_Avoid_: Loom-authored commit, backend credential access

**Review TaskRun**:
A TaskRun that evaluates an exact Task Change Set from its own TaskRun Root. Whether it may modify code is determined by Repository Access Policy rather than by repository selection.
_Avoid_: Shared implementation checkout, mutable review target

**Correction TaskRun**:
A TaskRun that starts from a reviewed Task Change Set, appends backend-authored commits to the Task Branches, and produces a new Task Change Set version.
_Avoid_: Reviewer patch, mutated change set

## Streaming Language

**SSE Frame**:
The unit of transmission on a Server-Sent Events stream, terminated by a blank line. Events, retry directives, and comments are all frames.
_Avoid_: Message, packet, raw SSE write

**Resumable Event**:
An event frame that carries an event ID, advancing the client's Last-Event-ID. A stream is only actually resumable when the server honors that ID on reconnect; that honoring rule is the stream's cursor contract. An ID the server never consults does not make its stream resumable.
_Avoid_: ID'd event, mutation frame

**Non-resumable Event**:
An event frame without an event ID. It never moves the client's reconnect cursor, whether it announces stream state or carries a domain payload.
_Avoid_: Control frame, control event

**Retry Directive**:
A frame that only sets the client's reconnection delay. It dispatches no event and carries no data.
_Avoid_: Retry event

**Heartbeat Comment**:
A comment frame sent periodically to keep an idle connection alive. Its cadence belongs to the individual stream, and clients ignore its text.
_Avoid_: Keepalive event, shared heartbeat interval
