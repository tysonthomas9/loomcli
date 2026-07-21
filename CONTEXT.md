# Loom

Loom is an AI-agent orchestration platform: Workspaces own Roles, Agents, and
issues; Worker agents claim and complete tasks under daemon supervision. Core
objects (Workspace, Role, Agent, Lead, Worker, fleet-db, flue) are defined in
[docs/loom-glossary.md](docs/loom-glossary.md), which remains the vocabulary of
record for them. This file holds terms resolved during design sessions since.

## Language

### Change custody

**Placement**:
Where a task run executes: the host's local worktree, or a remote sandbox
(e.g. a Daytona sandbox).
_Avoid_: environment, location

**Host**:
The Loom-side plane that requests a task run and owns its local placement —
the daemon's execution leaf or the serve driver plane.
_Avoid_: driver side, Go side

**Runner**:
The program that executes a task run's backend work against a placement.
_Avoid_: leaf (that's the host's execution end), executor

**Change**:
The repository edits a task run produces, however represented (working-tree
edits, commits, a patch, a pushed branch, a pull request).
_Avoid_: diff, patch (those are representations of a Change, not the concept)

**Change Custody**:
Ownership of a task run's Change from isolation through capture to Delivery.
Exactly one party — Host or Runner — holds custody for a given run; split or
ambiguous custody is a defect, not a mode.
_Avoid_: patch-back handling, git handling

**Custody Default**:
The Runner holds Change Custody unless the Delivery Plan explicitly assigns
it to the Host. The Runner side is Loom's extensibility surface, so delivery
capability accrues there; custody is always declared in the plan, never
inferred from runner behavior or naming.
_Avoid_: placement ownership, runner mode, entrypoint switching

**Delivery Plan**:
The Host's up-front statement, carried with the run request, of how a run's
Change is expected to land (applied to the host worktree, a stacked branch, a
pull request, none) and who holds custody.
_Avoid_: delivery config, run options

**Change Outcome**:
The custody holder's typed account of the Change a run produced or delivered:
a patch against a stated base, a remote delivery, or no change. Reconciled
against the Delivery Plan; a mismatch is an error, never a silent no-op.
_Avoid_: runner result (that's the whole result envelope; the outcome is one
part of it), patch presence

**Delivery**:
Landing a captured Change where the Delivery Plan says it belongs.
_Avoid_: finalize, patch-back (one possible delivery, not the concept)

### Agent observability

**Eval Candidate**:
A terminal task session with a published transcript that the current judge
version has not yet evaluated, inside the eval lookback window and sampling
cohort. The unit the eval agent selects each tick.
_Avoid_: evaluable session, session with transcript (having produced a
transcript is not the test — having published one is)

### Task-plane session model

**Agent Invocation**:
One agent invocation attempt within a task-run Attempt (failed spawns
included); the unit an AgentSession records, exactly one session per
invocation — and the only unit: deterministic work spawns no agent and
yields no session, leaving TaskRun/driver-run records only. An OS process
is one implementation of an invocation (process leaves); one in-process
harness prompt call is another (daytona harness, LOOMCLI-105).
_Avoid_: agent run (a run can hold many invocations), execution, agent
process invocation (too narrow — a harness prompt call invokes an agent
with no dedicated process), deterministic exec session / exec session (a
non-concept — sessions are agent invocations by construction; LOOMCLI-106)

**Invocation Key**:
The leaf-chosen stable slug naming an Agent Invocation within a task-run
Attempt ("judge", "worker-2"). Re-supplying it re-identifies the same
invocation; it names, it never sequences — ordering comes from start time.
_Avoid_: session name, agent id, sequence number

**Attempt**:
One claim of a TaskRun. Ordinals are dense per claim with fencing order as
the truth; terminal recovery paths (stale fail, quarantine, park) claim
nothing and so mint no Attempt.
_Avoid_: retry, scheduler_attempt (a retry-loop projection of Attempt, not
its definition)

**Parent Session**:
The session from whose execution a session was spawned — a workflow session
spawning a leaf, or an agent spawning a subagent. Empty when the spawner has
no session of its own. Run grouping is the TaskRun's job, never the parent
link's.
_Avoid_: workflow session link, subagent tree (both are instances, not the
concept)
