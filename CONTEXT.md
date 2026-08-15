# Loom

Loom orchestrates coding agents over shared workspaces: humans and autonomous agents
create issues, plan, implement, and review through a common control plane (fleet-db) and
a local driver runtime.

## Language

### Autonomous agents

**Role**:
An agent's named identity: the prompt it speaks through, and — for scripted roles —
the machinery it runs. Instances reference a role; scout is a role.
_Avoid_: agent kind, agent type, template

**Scripted role**:
A role whose name is bound, at compile time, to autonomous machinery — a workflow,
leaf runners, trust and preflight requirements, a journal. The binding ships in code
and cannot be created at runtime; the role's prompt remains editable data.
_Avoid_: agent kind, builtin agent

**Agent instance**:
One AgentService record — a durable, addressable occurrence of a scripted role in a
workspace, created and managed by users. Many instances of one role may coexist and
share that role.
_Avoid_: agent service (when the record is meant), background agent

**Trigger kind**:
How an instance is scheduled or executed (`cron`, `event`, …) — a property of the
instance, independent of its agent kind. Renamed from the record's former bare `kind`.
_Avoid_: kind (bare — ambiguous against agent kind)

**Trigger binding**:
The record that connects a route key to an agent instance, defining when it runs.
_Avoid_: schedule (that is only the cron flavour), hook

**Route key**:
The normalized address of a trigger source, such as `cron.scout.weekly` or
`github.pull_request.opened`.

**Reconciler**:
The create-or-repair operation that makes a desired record real: get, create if missing,
diff, patch. Idempotent by definition.
_Avoid_: provisioner, ensurer

**Trusted runner**:
A runner entrypoint granted the trusted-local credential superset (provider credentials
exposed to its tasks). Trust is declared by the scripted-role catalog in code — never
derived from editable role content.

### Scout

**Scout**:
The builtin ticket-recommender agent kind: it analyzes a workspace on a schedule and
files recommended issues.

**Recommended issue**:
An issue created by scout carrying the `recommended` label, quarantined from
auto-planning until a human approves it.
_Avoid_: suggestion, draft issue

**Journal**:
An agent instance's working-memory file (scout: `history.md`) — state the agent itself
reads and writes across runs, e.g. for dedupe. Not a log.
_Avoid_: history, log

### Runs and logs

**Driver run**:
One execution of a workflow by the driver, attributed to the agent instance and trigger
binding that caused it.

**Task run**:
One task executed within a driver run by a runner (e.g. scout-analyze, scout-write).

**Log artifact**:
The captured output bytes of a task run or driver run, stored as a content artifact in
the control plane.
_Avoid_: log file, task log (when the storage is meant)

**Transcript**:
The normalized, backend-agnostic event sequence of an AI session or task run
(`TranscriptEntry`), produced by the canonical transcript parser — never re-derived
client-side.
