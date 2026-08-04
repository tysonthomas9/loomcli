# Distributed Control Plane Data Model Review

> **Status:** Superseded — 2026-06-03 by
> `docs/design/fleetdb-agent-platform-v2-proposal.md`, and since then by
> shipped code. *audited 2026-07-23*
>
> **Read this file for the reasoning, not for the field names.** About two
> thirds of the proposed *global* models exist today under different names
> with different fields (see "Shipped As" below). Every proposed *local*
> model (CheckoutBinding, WorktreeBinding, LocalNodeConfig, SecretBinding),
> every proposed *observed* model (CheckoutStatus, RuntimeStatus,
> DiffSummary), and every Campaign model was never built — zero non-test
> occurrences in `internal/`. The unbuilt local/observed half is still the
> only written record of that design, which is why this file survives
> despite being wrong about names.

**Date:** 2026-04-30
**Related:** `docs/design/distributed-control-plane.md`,
`docs/design/2026-07-23-control-plane-as-built.md`,
`docs/design/fleetdb-agent-platform-v2-proposal.md`, `loomcli-26v50`,
`loomcli-37h1h`

## Shipped As

Land on real code in one hop. Verified 2026-07-23.

| Proposed here | Shipped as | Notes |
|---|---|---|
| `WorkerProfile` | `domain.WorkerProfile`, `internal/domain/platform.go:104` | Keyed by `ProfileID`. Gains `RuntimePolicy`, `Capabilities`, `Enabled`. Loses `desired_state`, `repo_groups`, `cross_repo`, `fallback_backends`, `parent_scope`, `schedule_ref`, `task_filter`, `placement`; `backend_override` shipped as `Backend`. |
| `AgentService` | `domain.AgentService`, `internal/domain/platform.go:147` | Kind enum (`platform.go:123-136`) is larger than proposed. Fields: `ServiceID`, `TriggerRefs`, `PlacementPolicy`, `LeaseID`, `StateRef`, `BudgetPolicy`. |
| `TaskRun` | `domain.TaskRun`, `internal/domain/platform.go:498` | `TaskRunID`; `Runner`/`RunnerKind`/`RunnerRef`/`ProviderProfile` instead of `runtime_provider`. No `branch`/`base_commit`/`final_commit`/`attempt`/`deadline_at`. |
| `Lease` (one polymorphic record) | Three purpose-built mechanisms | `domain.AgentLease` (`internal/domain/control_plane.go:167`), `domain.AgentOwnershipLease` (`control_plane.go:182`), and task-run fencing inline on `TaskRun.LeaseID`/`FencingToken` (`platform.go:513-514`). There is no generic `resource_type` lease. |
| `Node` | `domain.Node`, `internal/domain/control_plane.go:39` | Workspace-scoped. `OwnerActor`, not `owner_type`/`owner_id`. |
| `NodeHeartbeat` (a record) | An *operation*, not a record | `NodeStore.Heartbeat` (`internal/store/control_plane_store.go:37`) advances `Node.LastHeartbeat`/`Node.ExpiresAt`. `ToolInventory` and `Capacity` are fields on `Node` itself. Callers: `internal/driver/executor.go:467`, `internal/driver/task_worker.go:217`. |
| `Artifact` | `domain.Artifact`, `internal/domain/control_plane.go:134` | Store contract at `internal/store/control_plane_store.go:225`. |
| `TerminalSession` | `domain.TerminalSession`, `internal/domain/control_plane.go:112` | Store contract at `internal/store/control_plane_store.go:146`. |
| `Campaign`, `CampaignRun`, `CampaignStep` | — | Never built. The only trace is the enum value `AgentServiceKindCampaignOrchestrator` (`internal/domain/platform.go:136`). |
| `CheckoutBinding`, `WorktreeBinding`, `LocalNodeConfig`, `SecretBinding` | — | Never built. The local layer is still `bootstrap.StateCache` (`internal/bootstrap/statecache.go:33-52`) plus `internal/localworkspace`. |
| `CheckoutStatus`, `RuntimeStatus`, `DiffSummary` | — | Never built. Observed run state rides on `domain.TaskRun.Status`/`LastHeartbeat`/`ErrorClass` and `domain.AgentSession` (`internal/domain/control_plane.go:81`). |

Store contracts for the shipped half live in
`internal/store/control_plane_store.go` (`NodeStore:37`,
`AgentSessionStore:95`, `TerminalSessionStore:146`, `ArtifactStore:225`,
`AgentLeaseStore:258`, `AgentOwnershipLeaseStore:284`) and
`internal/store/platform_store.go` (`TaskRunStore:762`). The code that
drives them is `internal/driver` — a package this document never names.
See `docs/design/2026-07-23-control-plane-as-built.md`.

## Purpose

This document compares the data models that exist today with the models
needed for a task-driven, distributed control plane that still supports
long-lived agents for cron, on-call, and feature orchestration work.

Use this as the review checklist before implementation. It names:

- what exists now
- what should stay
- what should be split or renamed
- what is missing
- which fields are global, local, or observed

## Current Data Models

### Fleet-Db Domain Models

These live under `internal/domain` and are the best starting point for
the global model.

| Current type | Role today | Assessment |
|---|---|---|
| `domain.Workspace` | Fleet-db workspace metadata: key, name, description, state. | Good global shape. Keep machine-local paths out. |
| `domain.Repo` | Fleet-db repo metadata: remote URL, branch, groups, source repo ID. | Good global shape. Keep local checkout path out. |
| `domain.Role` | Fleet-db role policy: prompt/model/backend/tools/filters/budget. | Good global policy object. Needs clearer use across task runs and long-lived services. |
| `domain.Agent` | Long-lived assignment of role to repos with a small `State`. | Overloaded. It mixes worker profile, desired state, and assignment. Needs split. |
| `domain.DaemonProfile` | Per-workspace daemon settings. | Mixed global and local. Restart/concurrency policy is global; PID/log/socket paths are local/node-specific. |

### Local State Cache

`internal/bootstrap.StateCache` is the correct direction for
machine-local data.

Current shape:

```text
StateCache
  Version
  LastWorkspace
  Workspaces[workspace_key]
    Path
    Repos[repo_name] -> local path
    Agents[agent_name].Worktree -> local path
```

Assessment:

- Good: clearly says it is not canonical config.
- Good: has workspace, repo, and agent local path maps.
- Missing: no run/worktree binding distinction.
- Missing: no checkout health metadata.
- Missing: no local node identity or tool inventory.
- Underused: fleet-db commands set `LastWorkspace`, but repo and agent
  path bindings are still mostly resolved through runtime config.

### YAML Models

These were the main distributed-design blocker in April 2026. Most of the
block has since been removed — the rows below are annotated with their
2026-07-23 state.

| Current type | Problem as written 2026-04-30 | 2026-07-23 |
|---|---|---|
| `config.LoomConfig` | Stores global workspace list and local paths in `~/.loom/config.yaml`. | **Wrong now.** `internal/cli/config/config.go:22-23`: "LoomConfig is a FleetDB-backed workspace view used by older command code while the internal DTOs are collapsed onto domain types." `LoadConfig` (`config.go:120-134`) opens the fleet-db store; local paths are overlaid from `bootstrap.LoadStateCache` (`config.go:180`). Nothing reads or writes `~/.loom/config.yaml` outside tests. |
| `config.WorkspaceConfig` | Combines workspace identity, local path, repos, backend, lifecycle state. | Still combines them, and still carries `yaml:` struct tags (`internal/cli/config/config.go:46-56`), but it is now populated from fleet-db, not from a YAML file. |
| `config.RepoConfig` | Combines repo identity with local path. | Same: still combined, still `yaml:`-tagged (`config.go:57-65`), no longer YAML-sourced. |
| ~~`config.ProjectFile`~~ | ~~Project-local `loom.yaml` still drives daemon runtime.~~ | **No such type.** `grep -rn 'ProjectFile' internal --include='*.go'` returns nothing. Daemon config now loads from fleet-db via `config.LoadDaemonConfig` (`internal/cli/config/project.go:165-190`). |
| `config.DaemonSettings` | Mixes runtime policy, local paths, backend config, Redis/fleet settings. | Still mixed, at `internal/cli/config/project.go:19-28`, and still `yaml:`-tagged — but sourced from fleet-db, not `loom.yaml`. |
| `config.AgentEntry` | YAML agent assignment that still drives daemon/agent execution. | Type still exists (`internal/cli/config/project.go:96`). UNVERIFIED whether it still drives execution; do not restate either way without checking its callers. |

These should not be the runtime source of truth in fleet-db mode. As of
2026-07-23 they no longer are: the last non-test YAML runtime readers are
gone, though `gopkg.in/yaml.v3` is still a direct dependency (`go.mod:39`)
and the struct tags remain.

### Fleet Worker Coordination

`internal/webui/fleet` contains a Redis-backed worker/claim subsystem.
It is useful prior art, but it is not the desired distributed runtime
model.

Current shape:

```text
Worker
  WorkerID
  Repos
  RegisteredAt

ClaimResponse
  TaskID
  Success
  Payload
```

Assessment:

- Good: recognizes worker registration and task claims.
- Missing: node identity, user/service identity, labels, capabilities,
  capacity, drain state, runtime provider, tool inventory.
- Missing: lease token/fencing token as first-class data.
- Missing: heartbeat timestamps as durable observed state.
- Missing: active task lease renewal tied to worker heartbeat.
- Current claim path is issue-claim oriented, not general task-run
  orchestration.

### Daemon Runtime Status

`DaemonAgentStatus` and `DaemonState` capture local supervisor facts.

Current shape:

```text
DaemonState
  PID
  StartedAt
  Agents[]

DaemonAgentStatus
  Worktree
  Role
  Repo
  PID
  Status
  TaskID
  EpicID
  RestartCount
  LastStart
  LastExit
  WorktreePath
  BackoffUntil
```

Assessment:

- Useful as observed runtime data.
- Too local/process-specific to be global canonical state.
- No TTL/staleness model.
- No node identity.
- No lease/fencing token.
- No clean link to `TaskRun` or `AgentService`.

### Session Models

`internal/entity.Session` and `internal/sessions.Session` capture agent
session telemetry.

Assessment:

- Useful telemetry: backend, model, token usage, diff stats, exit code.
- Not a `TaskRun`: no lease, runtime provider, node ID, artifact set,
  retry lifecycle, or ownership semantics.
- Should become part of run artifacts or run telemetry.

### V2 Entity Models

`internal/entity.Workspace`, `Repo`, and `Agent` are not aligned with the
fleet-db boundary yet.

Examples:

- `entity.Workspace.Path` is core data, but path is local.
- `entity.Repo.Path` is core data, but path is local.
- `entity.Agent` is an autonomous entity with issue-like status and
  agent state, but it does not map cleanly to worker profile, service,
  runtime, or task run.

These may be useful later, but they should be reconciled with the
global/local/observed split before becoming authoritative.

## Proposed Model Overview

The proposed model has three layers:

```text
Global:   intent, metadata, leases, runs, audit
Local:    checkout paths, worktrees, PTYs, processes, secrets
Observed: node-reported runtime facts with timestamps/TTL
```

## Proposed Global Models

### Workspace

Canonical project container.

```text
Workspace
  key
  name
  description
  lifecycle_state
  error_message
  default_branch
  created_at
  updated_at
```

Existing fit: `domain.Workspace` is close.

Do not add:

- local path
- user-specific default
- process/runtime details

### Repo

Canonical source repository in a workspace.

```text
Repo
  workspace_key
  name
  remote_url
  default_branch
  remote_name
  groups
  source_repo_id
  created_at
  updated_at
```

Existing fit: `domain.Repo` is close.

Do not add:

- checkout path
- current local branch
- dirty state

Those belong to `CheckoutBinding` or observed checkout status.

### Role

Policy for how work should be performed.

```text
Role
  workspace_key
  name
  description
  prompt_ref
  model
  backend
  task_filter
  path_patterns
  skills
  max_priority
  max_concurrency
  max_budget_usd
  read_only
  allowed_tools
  denied_tools
```

Existing fit: `domain.Role` is close.

Open decision: prompt storage should probably be a reference or
artifact, not a local-only prompt file path in distributed mode.

### WorkerProfile

> Shipped, differently: `domain.WorkerProfile`,
> `internal/domain/platform.go:104-121`. It did **not** replace `agentdef` —
> `loom agentdef` is still a public command; `loom worker profile`
> (`internal/cli/serve/worker/profile_cmd.go`) was added alongside it. The
> shipped fields are `WorkspaceKey, ProfileID, Name, Role, Backend,
> RuntimePolicy, Repos, MaxPriority, MaxParallel, ParentEpic, Labels,
> Capabilities, Enabled, Metadata` — no `desired_state`, `repo_groups`,
> `cross_repo`, `fallback_backends`, `parent_scope`, `schedule_ref`,
> `task_filter`, or `placement`. `backend_override` shipped as `Backend`.

Named queue/filter/profile for repeatable task execution. This replaces
the public `agentdef` concept and is the non-runtime part of the current
`domain.Agent`.

```text
WorkerProfile
  workspace_key
  name
  role_name
  backend_override
  fallback_backends
  repos
  repo_groups
  cross_repo
  parent_scope
  task_filter
  desired_state
  max_parallel
  placement
  schedule_ref optional
  created_at
  updated_at
```

Ownership:

- Global.
- User/team intent.
- Does not prove anything is running.

Current source:

- Split out from `domain.Agent`.
- YAML `AgentEntry` should migrate here or to `AgentService`,
  depending on whether it is finite-task or long-lived.

### AgentService

> Shipped, differently: `domain.AgentService`,
> `internal/domain/platform.go:147-168`. The kind enum
> (`platform.go:123-136`) is larger than the one below: `lead`, `support`,
> `triage`, `on_call`, `scheduled`, `maintenance`, `orchestrator`,
> `always_on`, `cron`, `event`, `campaign_orchestrator`. Fields differ too:
> `ServiceID`, `TriggerRefs`, `PlacementPolicy`, `LeaseID`, `StateRef`,
> `BudgetPolicy`.

Long-lived desired process for cron, on-call, event responders, and
campaign orchestrators.

```text
AgentService
  workspace_key
  name
  kind: always_on | cron | event | on_call | campaign_orchestrator | maintenance
  role_name
  profile_name optional
  desired_state: running | stopped | paused
  schedule_id optional
  event_sources
  placement
  max_instances
  restart_policy
  permissions
  budget
  created_at
  updated_at
```

Key distinction:

- `AgentService` owns a service lease.
- Work it performs should still create activities, task runs, or
  campaign steps for auditability.

Examples:

```text
oncall-triage
  kind=on_call
  desired_state=running
  required_capabilities=pagerduty,slack,codex

repo-cleanup
  kind=cron
  schedule="0 */6 * * *"
  concurrency_policy=Forbid

feature-orchestrator
  kind=campaign_orchestrator
  max_instances=1
```

### Campaign

> **Never built.** No `Campaign`, `CampaignRun`, or `CampaignStep` type
> exists in `internal/`. The only trace is the enum value
> `AgentServiceKindCampaignOrchestrator` (`internal/domain/platform.go:136`)
> and its two switch-statement uses. Applies to the `CampaignRun` and
> `CampaignStep` sections below as well.

Bounded orchestration goal, such as "complete this feature end-to-end
for six hours".

```text
Campaign
  id
  workspace_key
  title
  goal
  scope_type: epic | issue | repo | custom
  scope_ref
  success_criteria
  duration
  budget
  max_parallel
  runtime_policy
  status
  created_by
  created_at
  updated_at
```

Purpose:

- Captures intent.
- Lets a long-lived orchestrator plan and coordinate many task runs.
- Provides a bounded lifecycle for "work on this feature for N hours".

### CampaignRun

One execution attempt of a campaign orchestrator.

```text
CampaignRun
  id
  campaign_id
  orchestrator_service
  node_id
  runtime_provider
  status
  lease_id
  started_at
  deadline_at
  ended_at
  summary
```

### CampaignStep

Auditable step inside a campaign run.

```text
CampaignStep
  id
  campaign_run_id
  kind: plan | implement | test | review | merge | report | create_issue
  title
  status
  task_run_id optional
  artifact_ids
  result_summary
  started_at
  ended_at
```

### TaskRun

> Shipped: `domain.TaskRun`, `internal/domain/platform.go:498`, with the
> store contract at `internal/store/platform_store.go:762-773`. It is no
> longer "the main missing primitive". Field shape differs: `TaskRunID`,
> and `Runner`/`RunnerKind`/`RunnerRef`/`ProviderProfile` in place of
> `runtime_provider`; no `branch`, `base_commit`, `final_commit`,
> `attempt`, or `deadline_at`. Fencing rides inline on
> `LeaseID`/`FencingToken` (`platform.go:513-514`).

Finite execution attempt for a task or campaign step.

```text
TaskRun
  id
  workspace_key
  task_id
  campaign_run_id optional
  campaign_step_id optional
  role_name
  profile_name optional
  runtime_provider: local | e2b | kubernetes
  node_id
  status: queued | leased | starting | running | completing | completed | failed | expired | cancelled
  lease_id
  attempt
  branch
  base_commit
  final_commit
  started_at
  ended_at
  deadline_at
  error_class
  error_message
```

This is the main missing primitive.

How it differs from `Session`:

- owns execution lifecycle
- has runtime provider
- has node identity
- has lease/fencing semantics
- links to artifacts and retry policy

Session telemetry can attach to `TaskRun`.

### Lease

> Shipped as three mechanisms, not one polymorphic record. There is no
> generic `resource_type` lease. `domain.AgentLease`
> (`internal/domain/control_plane.go:167`, keyed by `SessionID`),
> `domain.AgentOwnershipLease` (`control_plane.go:182`, keyed by
> `AgentID`), each with `Token` + `FencingToken int64` +
> `Status(active|released|expired)`; and task-run fencing inline on
> `domain.TaskRun.LeaseID`/`FencingToken`. Store contracts:
> `internal/store/control_plane_store.go:258,284`.

Time-bounded ownership for task runs, long-lived services, campaigns,
terminals, or other exclusive resources.

```text
Lease
  id
  resource_type: task_run | agent_service | campaign_run | terminal
  resource_id
  holder_node_id
  holder_worker_id
  token_hash
  version
  acquired_at
  expires_at
  released_at
  status: active | expired | released | superseded
```

Required invariants:

- acquire is atomic
- renew requires current token
- complete requires current token
- release requires current token
- stale token cannot mutate resource

### Node

Registered machine, sandbox, or runtime host.

```text
Node
  id
  owner_type: user | service
  owner_id
  display_name
  runtime_provider: local | e2b | kubernetes | bare_metal
  labels
  capabilities
  max_parallel_runs
  drain_state
  version
  registered_at
  last_heartbeat_at
```

For E2B, a sandbox can be represented as a node for the duration of a
run, or as runtime metadata under an E2B-specific provider. The core
scheduler should not depend on E2B-specific fields.

### NodeHeartbeat

> **Not a record.** Heartbeat shipped as an *operation* on `NodeStore`
> (`internal/store/control_plane_store.go:37`) that advances
> `Node.LastHeartbeat` / `Node.ExpiresAt`
> (`internal/domain/control_plane.go:50-51`). `ToolInventory` and
> `Capacity` are fields on `Node` itself, not on a separate observed
> record. Callers: `internal/driver/executor.go:467`,
> `internal/driver/task_worker.go:217`.

Observed, expiring node status.

```text
NodeHeartbeat
  node_id
  observed_at
  capacity_available
  running_run_ids
  active_service_ids
  tool_inventory
  checkout_summaries
  health
```

This can be stored as a latest-state projection plus event history.

### Artifact

Durable output of a run or campaign step.

```text
Artifact
  id
  workspace_key
  run_id optional
  campaign_run_id optional
  kind: patch | commit | transcript | log | test_result | diff_summary | screenshot | report
  uri
  content_hash
  metadata
  created_at
```

Artifacts let ephemeral runtimes such as E2B shut down without losing
reviewable output.

### TerminalSession

Global metadata for a local PTY.

```text
TerminalSession
  id
  workspace_key
  node_id
  run_id optional
  checkout_binding_id optional
  title
  status: opening | open | closed | failed
  access_mode: owner_write | shared_read | shared_write
  attached_users
  created_at
  last_activity_at
```

The PTY process and file descriptor are local to the node.

## Proposed Local Models

> **None of these were built.** `CheckoutBinding`, `WorktreeBinding`,
> `LocalNodeConfig`, and `SecretBinding` have zero occurrences in
> `internal/`. The local layer is still `bootstrap.StateCache`
> (`internal/bootstrap/statecache.go:33-52`) plus resolution helpers in
> `internal/localworkspace`. In particular the `LocalNodeConfig` split
> never happened: `domain.DaemonProfile` still mixes local `PIDFile`,
> `LogDir`, `EventsDir` into a fleet-db record
> (`internal/domain/daemon_profile.go:13-26`), and its docstring now argues
> that is intentional. This section is the only written record of the
> proposed split — keep it.

### CheckoutBinding

Machine-local binding between global repo identity and local path.

```text
CheckoutBinding
  node_id
  workspace_key
  repo_name
  local_path
  clone_url_used
  created_at
  updated_at
```

Storage:

- local state cache for local mode
- node-local database for daemon mode
- reported as observed summary to fleet-db

### WorktreeBinding

Machine-local binding for a profile, service, task run, or campaign run
worktree.

```text
WorktreeBinding
  node_id
  workspace_key
  repo_name
  owner_type: profile | service | task_run | campaign_run
  owner_id
  local_path
  created_at
  updated_at
```

This is the missing distinction in current `StateCache.Agents`.

### LocalNodeConfig

Local machine settings that should not be fleet-db-global.

```text
LocalNodeConfig
  node_id
  loom_dir
  pid_file
  log_dir
  socket_dir
  default_runtime_provider
  secret_provider
  tool_paths
```

This absorbs the local parts of `DaemonProfile` and `DaemonSettings`.

### SecretBinding

Local or scoped secret reference.

```text
SecretBinding
  node_id
  secret_ref
  provider
  scope
```

Global records should reference `secret_ref`, not secret values.

## Proposed Observed Models

> **None of these were built.** No `CheckoutStatus`, `RuntimeStatus`, or
> `DiffSummary` type exists. (The one `RuntimeStatus` in the tree is an
> unrelated string field on a driver outbox payload,
> `internal/driver/outbox_dispatcher.go:25`.) Observed run state is carried
> on `domain.TaskRun.Status`/`LastHeartbeat`/`ErrorClass`
> (`internal/domain/platform.go:511,533,535`) and `domain.AgentSession`
> (`internal/domain/control_plane.go:81`).

### CheckoutStatus

```text
CheckoutStatus
  node_id
  workspace_key
  repo_name
  observed_at
  branch
  commit
  dirty
  changed_files_count
  missing
  error_message
```

### RuntimeStatus

```text
RuntimeStatus
  resource_type: task_run | agent_service | campaign_run
  resource_id
  node_id
  observed_at
  state
  pid optional
  current_activity
  last_output_at
  error_class
  error_message
```

### DiffSummary

```text
DiffSummary
  run_id
  node_id
  observed_at
  files_changed
  lines_added
  lines_removed
  patch_artifact_id optional
```

## Current vs Proposed Mapping

| Current model | Proposed destination |
|---|---|
| `domain.Workspace` | Keep as `Workspace`, possibly add lifecycle polish only. |
| `domain.Repo` | Keep as `Repo`. |
| `domain.Role` | Keep as `Role`; review prompt/file reference semantics. |
| `domain.Agent` | Split into `WorkerProfile` and maybe `AgentService`; remove runtime state from profile. |
| `domain.DaemonProfile.RestartPolicy` | Global policy for services/runs/profiles. |
| `domain.DaemonProfile.PIDFile/LogDir/EventsDir` | Move to `LocalNodeConfig`. |
| `StateCache.LastWorkspace` | Keep as local UX preference. |
| `StateCache.Workspaces[*].Path` | Keep/evolve as local workspace checkout binding. |
| `StateCache.Workspaces[*].Repos` | Evolve into `CheckoutBinding`. |
| `StateCache.Workspaces[*].Agents` | Split into `WorktreeBinding` for profile/service/run. |
| `config.LoomConfig` | Retire as runtime source; migrate useful state to fleet-db or local cache. |
| `config.WorkspaceConfig` | Split into `Workspace`, `CheckoutBinding`, and local checkout status. |
| `config.RepoConfig` | Split into `Repo` and `CheckoutBinding`. |
| `config.AgentEntry` | Migrate to `WorkerProfile` or `AgentService`. |
| `config.DaemonSettings` | Split into global policies and `LocalNodeConfig`. |
| `webui/fleet.Worker` | Replace/evolve into `Node` plus `NodeHeartbeat`. |
| Redis task claim key | Replace/evolve into `Lease` with fencing token. |
| `DaemonAgentStatus` | Convert to `RuntimeStatus` projection. |
| `entity.Session` | Attach as telemetry/artifacts for `TaskRun` and `CampaignStep`. |
| terminal tab/session metadata | Map to `TerminalSession` plus local PTY state. |

## Execution Model Comparison

### Finite Task Run

Use for normal implementation, planning, testing, review, and E2E runs.

```text
Task -> TaskRun -> Lease -> RuntimeProvider -> Artifact -> Completion
```

Benefits:

- auditable
- retryable
- safe for E2B
- easy to parallelize
- clear success/failure boundary

### Long-Lived Agent Service

Use for background and forever-running work.

```text
AgentService -> ServiceLease -> RuntimeStatus -> Activities/TaskRuns
```

Good for:

- cron
- on-call
- webhook/event responders
- maintenance loops
- queue consumers

Important rule:

> A long-lived service owns a service lease, but meaningful work should
> still create activities, campaign steps, or task runs.

### Campaign Orchestrator

Use for "complete this feature end-to-end for a duration".

```text
Campaign -> CampaignRun -> Orchestrator Service Lease
         -> CampaignSteps -> TaskRuns -> Artifacts
```

The orchestrator is long-lived for the campaign duration, but the
implementation/test/review work underneath remains task-run based.

This gives:

- strategy and adaptation
- bounded duration
- budget controls
- resumability
- parallel task execution
- reviewable artifacts

## E2B Mapping

> **E2B was never implemented.** The only occurrence in the Go tree is the
> unused enum value `RuntimeProviderE2B`
> (`internal/domain/control_plane.go:25`). The ephemeral remote provider
> that shipped is Daytona (`internal/driver/bundled_runner.go:16-20`,
> `internal/workflows/builtin/daytona-task-runner.ts`). The mapping below
> is still correct as *provider-agnostic* guidance — read "E2B" as "the
> remote sandbox provider".

E2B should be a runtime provider, not the control plane.

| Concept | E2B mapping |
|---|---|
| Node | Ephemeral sandbox node or provider-specific runtime instance. |
| RuntimeProvider | `e2b`. *(Shipped value is `local`; there is no remote provider enum value in use.)* |
| TaskRun | One sandbox run or one run inside a warm sandbox. |
| Local checkout | Sandbox filesystem path. |
| TerminalSession | E2B PTY attached to a run. |
| Artifact | Uploaded patch/log/transcript/test result before sandbox expiry. |
| Lease | Still owned by fleet-db/control plane, not E2B. |

E2B-specific fields should live in runtime metadata:

```text
runtime_metadata
  sandbox_id
  template
  timeout
  region
```

Do not make `sandbox_id` the identity of the task or worker.

## API Surface Implications

### Workspace APIs

Use stable workspace keys in context.

Bad:

```text
/api/workspaces/{name-or-key}/...
context workspace = raw path segment
```

Better:

```text
resolve {ws} once -> canonical workspace key
context workspace = canonical key
```

### Checkout APIs

> **Never built.** None of these routes are registered anywhere in
> `internal/webui`, and there is no CLI equivalent — checkouts have no
> surface at all. See "Proposed Local Models" above.

Checkout routes should make locality explicit:

```text
GET /api/workspaces/{ws}/checkouts
POST /api/workspaces/{ws}/checkouts/{repo}/bind
GET /api/workspaces/{ws}/nodes/{node}/checkouts
```

### Run APIs

> **Shipped in a different shape.** Task-run operations are a single
> op-dispatch route, not one route per verb:
> `POST /api/workspaces/{ws}/task-run/{op}`
> (`internal/webui/handlers/taskrunapi/module.go:145`), with ops `get`,
> `task-get`, `heartbeat`, `log-append`, `complete`, `runtime-credential`,
> `artifact-declare`, `artifact-get`, `artifact-list`, `artifact-finalize`
> (`module.go:106-117`), plus
> `PUT /api/workspaces/{ws}/task-run/artifacts/{artifactId}/content`
> (`module.go:148`). Auth is a per-task-run **lease-token bearer**, not a
> fencing token in the request body (`module.go:151-159`,
> `internal/webui/server/middleware/auth_routes.go`). The client contract
> is `@loom/sdk/runner` — see `sdk/README.md` and
> `docs/product/loom-typescript-sdk-spec.md`.
>
> `GET /api/workspaces/{ws}/runs/{runId}`, `/events`, and `/stream` do
> exist (`internal/webui/handlers/workflows/module.go:41-43`) but belong to
> the *workflow driver run*, a different concept from a task run.

```text
POST /api/workspaces/{ws}/tasks/{task}/runs
POST /api/workspaces/{ws}/runs/{run}/claim
POST /api/workspaces/{ws}/runs/{run}/heartbeat
POST /api/workspaces/{ws}/runs/{run}/complete
GET  /api/workspaces/{ws}/runs/{run}
GET  /api/workspaces/{ws}/runs/{run}/artifacts
```

All lease-protected mutations require the fencing token.

### Long-Lived Service APIs

> **No HTTP surface.** Agent services are managed by CLI only:
> `loom worker service` (`internal/cli/serve/worker/service_cmd.go`).

```text
POST /api/workspaces/{ws}/services
PATCH /api/workspaces/{ws}/services/{name}/desired-state
POST /api/workspaces/{ws}/services/{name}/claim
POST /api/workspaces/{ws}/services/{name}/heartbeat
GET  /api/workspaces/{ws}/services/{name}/runtime
```

### Campaign APIs

> **Never built** — no routes, no CLI, no domain types. See the Campaign
> note above.

```text
POST /api/workspaces/{ws}/campaigns
POST /api/workspaces/{ws}/campaigns/{id}/runs
GET  /api/workspaces/{ws}/campaigns/{id}/status
POST /api/workspaces/{ws}/campaigns/{id}/stop
GET  /api/workspaces/{ws}/campaigns/{id}/steps
```

## Migration Order

Step status audited 2026-07-23.

| Step | Status |
|---|---|
| 1. Rename the concepts | Partially. `worker profile` and `agent service` both exist as domain types and CLI verbs; `agentdef` was not retired. "Worker" is still overloaded four ways — see `docs/loom-glossary.md`. |
| 2. Split `domain.Agent` | Done for the new types (`WorkerProfile`, `AgentService`), not for the migration — `agentdef` CRUD still exists alongside. `AgentRuntime`/`RuntimeStatus` never appeared. |
| 3. Add Node and Lease | Done, differently — see the Node/Lease/NodeHeartbeat notes above. |
| 4. Add TaskRun | Done (`internal/domain/platform.go:498`, `internal/store/platform_store.go:762`). |
| 5. Add Checkout/Worktree Bindings | Not done. `StateCache` was not evolved. |
| 6. Add Campaign Models | Not done. |

### Step 1: Rename the Concepts in Code

Before deep implementation, stop using overloaded words.

Recommended names:

| Current word | Proposed meaning |
|---|---|
| agentdef | worker profile or agent service definition |
| agent state | desired state if global, runtime state if observed |
| worker | process or executor loop |
| node | machine/sandbox identity |
| session | telemetry attached to a run |
| daemon profile | global policy only; local daemon config separately |

### Step 2: Split `domain.Agent`

Create separate model(s):

```text
WorkerProfile
AgentService
AgentRuntime or RuntimeStatus
```

Migrate existing `agentdef` CRUD to the profile/service model.

### Step 3: Add Node and Lease

Add the minimum distributed primitives:

```text
Node
NodeHeartbeat
Lease
```

Do this before rewriting daemon behavior. Otherwise daemon rewrite will
invent local ownership semantics again.

### Step 4: Add TaskRun

Promote finite execution into a first-class model:

```text
TaskRun
Artifact
RuntimeStatus
```

Sessions can become telemetry attached to runs.

### Step 5: Add Checkout/Worktree Bindings

Evolve `StateCache`:

```text
CheckoutBinding
WorktreeBinding
LocalNodeConfig
```

Then make `loom task/plan/agent` resolve through fleet-db profile +
local binding instead of YAML.

### Step 6: Add Campaign Models

Once TaskRun and AgentService exist:

```text
Campaign
CampaignRun
CampaignStep
```

Campaign orchestration should not be built as a separate one-off system.

## Open Decisions For Review

1. Should the public name be `agent`, `worker`, or `service` for
   long-lived processes?
2. Should `WorkerProfile` and `AgentService` be separate tables or one
   table with `kind`?
3. Should local checkout bindings stay only in `state.json`, or should
   nodes report a latest checkout-binding projection to fleet-db?
4. Should E2B sandboxes register as first-class `Node` rows or stay as
   runtime metadata under `TaskRun`?
5. Should campaign orchestrators always be long-lived services, or can
   they be finite task runs with a longer timeout?
6. Which daemon settings are truly global policy vs local node config?
7. What is the minimum lease store implementation: fleet-db native
   event-sourced entity, Redis projection, or both?
8. How much terminal/file access should be available for E2B runs after
   completion?
9. Do we need per-user workspace ordering/defaults in fleet-db, or is
   local `state.json` enough for now?
10. How should prompt files become portable: inline prompt, artifact,
    repo-relative path, or managed prompt reference?

## Review Checklist

For each proposed model, verify:

- global fields are machine-agnostic
- local paths do not enter fleet-db canonical records
- observed fields have timestamps or TTL behavior
- mutations name the authority: user, node, worker, or lease holder
- every execution path has a run/activity record
- every exclusive execution path has a fencing token
- E2B-specific details stay behind runtime metadata
- local mode uses the same control flow as distributed mode
- YAML migration has an explicit destination for every retained field

## Related

- `docs/design/2026-07-23-control-plane-as-built.md` — where the shipped
  models are actually driven from (`internal/driver`) and the real HTTP
  surface.
- `docs/design/distributed-control-plane.md` — the conceptual companion to
  this file; still current for the global/local/observed split, lease
  semantics, and push-vs-pull.
- `docs/design/fleetdb-agent-platform-v2-proposal.md` — the 2026-06-03
  correction that supersedes this document.
- `sdk/README.md` and `docs/product/loom-typescript-sdk-spec.md` — the
  client contract for the task-run API that replaced the Run APIs proposed
  here.

## Summary

Current loom has useful global metadata models for workspace, repo, and
role. It has a promising local state cache. It also has YAML and
local daemon models that conflict with distributed execution.

The proposed model splits the overloaded pieces:

```text
domain.Agent      -> WorkerProfile + AgentService + RuntimeStatus
config.AgentEntry -> WorkerProfile/AgentService
Session           -> TaskRun telemetry/artifacts
Worker            -> Node + NodeHeartbeat
Redis claim       -> Lease with fencing token
Workspace path    -> CheckoutBinding
Daemon paths      -> LocalNodeConfig
```

This split supports both finite task-driven execution and long-lived
agents for cron, on-call, and campaign orchestration without making the
system unsafe for multi-user distributed operation.
