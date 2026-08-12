# Phase 9 Direct Writes and Outside-Module Package Catalog

- **Status:** Complete at Phase 9 Wave 9.41
- **Cataloged Phase 9 implementation/evidence head:** `fa810fd230b9c0e9452c25bc20b60eecd549b732`
- **Direct-write snapshot:** 90 rows / 108 call sites, unchanged since Wave 9.24 snapshot head `45dae70cbeb801a531d7b839d7208ec1ec3b7020`
- **Outside-module package snapshot:** 141 of 158 production packages
- **Canonical inventories:** [direct writes](../../../internal/archtest/testdata/direct-writes.yaml) · [production package shape](../../../internal/archtest/testdata/production-package-shape.yaml)

## Purpose and scope

This catalog answers two different architecture questions:

1. **Where may production code call a declared mutating persistence surface
   directly?**
2. **Why do the production packages outside `internal/modules/` still exist
   after Phase 9?**

The direct-write section is the human-readable view of the checked-in,
type-resolved architecture ledger. It is not a list of every business command,
SQL statement, HTTP request, or FleetDB operation. A row is one unique
`source file + receiver + mutating method + aggregate owner` combination.
Its call-site count records how many source selections or method values in that
file resolve to the same mutating surface. Read-only calls are not rows, but
the default-deny classifier still requires every recognized persistence method
to be explicitly declared read-only or mutating.

All 90 current rows have disposition `owner_adapter`; no transitional
direct-write row remains. A new row, a changed site count, a changed owner, or
a stale deleted row fails the architecture gate.

The package section uses the package-shape scanner's production semantics. A
package is a directory under `internal/` with at least one non-test Go file in
any build-tag variant. Generated Go files count because they create a compiled
import seam; `_test.go` files do not. The descriptions explain the durable
reason each package remains outside a capability root: application
orchestration, delivery, an external adapter, reusable platform mechanics, or
WebUI/HTTP composition.

## Direct persistence-write summary

| Aggregate owner | Rows | Call sites |
|---|---:|---:|
| Agents | 12 | 12 |
| Execution | 31 | 32 |
| Interaction | 20 | 20 |
| Read projection | 2 | 2 |
| Source Control | 3 | 9 |
| Workspace | 22 | 33 |
| **Total** | **90** | **108** |

A lower row count means fewer distinct source locations reach directly into
persistence. It does not mean Loom supports fewer mutations. The separate
mutation ledger remains at 107 reviewed business commands.

## Direct persistence-write calls

### Agents

12 rows representing 12 call sites.

| Source file | Mutating persistence call | Call sites |
|---|---|---:|
| `internal/app/serve/agents_fleetdb.go` | `internal/infra/fleetdb.AgentManagementTransport.AcquireAgentOwnership` | 1 |
| `internal/app/serve/agents_fleetdb.go` | `internal/infra/fleetdb.AgentManagementTransport.ApplyAgentServiceLifecycle` | 1 |
| `internal/app/serve/agents_fleetdb.go` | `internal/infra/fleetdb.AgentManagementTransport.ArchiveAgentService` | 1 |
| `internal/app/serve/agents_fleetdb.go` | `internal/infra/fleetdb.AgentManagementTransport.CreateAgentRole` | 1 |
| `internal/app/serve/agents_fleetdb.go` | `internal/infra/fleetdb.AgentManagementTransport.CreateAgentService` | 1 |
| `internal/app/serve/agents_fleetdb.go` | `internal/infra/fleetdb.AgentManagementTransport.DeleteAgentRole` | 1 |
| `internal/app/serve/agents_fleetdb.go` | `internal/infra/fleetdb.AgentManagementTransport.ReleaseAgentOwnership` | 1 |
| `internal/app/serve/agents_fleetdb.go` | `internal/infra/fleetdb.AgentManagementTransport.RenewAgentOwnership` | 1 |
| `internal/app/serve/agents_fleetdb.go` | `internal/infra/fleetdb.AgentManagementTransport.SetAgentServiceDesiredState` | 1 |
| `internal/app/serve/agents_fleetdb.go` | `internal/infra/fleetdb.AgentManagementTransport.SetAgentServiceDesiredStateOwned` | 1 |
| `internal/app/serve/agents_fleetdb.go` | `internal/infra/fleetdb.AgentManagementTransport.UpdateAgentRole` | 1 |
| `internal/app/serve/agents_fleetdb.go` | `internal/infra/fleetdb.AgentManagementTransport.UpdateAgentServiceIdentity` | 1 |

### Execution

31 rows representing 32 call sites.

| Source file | Mutating persistence call | Call sites |
|---|---|---:|
| `internal/app/serve/automation_workflow_catalog.go` | `internal/store.AtomicAwaitStore.ResolveAwaitAndResume` | 1 |
| `internal/app/serve/execution.go` | `internal/store.AtomicAwaitStore.ResolveAwaitAndResume` | 1 |
| `internal/app/serve/execution.go` | `internal/store.AwaitStore.RegisterAwaitAndCheck` | 1 |
| `internal/app/serve/execution.go` | `internal/store.DriverRunStore.Claim` | 1 |
| `internal/app/serve/execution.go` | `internal/store.DriverRunStore.Create` | 1 |
| `internal/app/serve/execution.go` | `internal/store.DriverRunStore.Finish` | 1 |
| `internal/app/serve/execution.go` | `internal/store.DriverRunStore.Heartbeat` | 1 |
| `internal/app/serve/execution.go` | `internal/store.DriverRunStore.RecoverStale` | 1 |
| `internal/app/serve/execution.go` | `internal/store.DriverRunStore.ResumeAwaiting` | 1 |
| `internal/app/serve/execution.go` | `internal/store.DriverRunStore.Suspend` | 1 |
| `internal/app/serve/execution_outbox_delivery.go` | `internal/store.OutboxStore.Create` | 1 |
| `internal/app/serve/execution_outbox_delivery.go` | `internal/store.OutboxStore.MarkResult` | 1 |
| `internal/app/serve/execution_reconciliation_queues.go` | `internal/store.AwaitEventNotificationStore.ClaimAwaitEventNotifications` | 1 |
| `internal/app/serve/execution_reconciliation_queues.go` | `internal/store.AwaitEventNotificationStore.CompleteAwaitEventNotification` | 1 |
| `internal/app/serve/execution_reconciliation_queues.go` | `internal/store.AwaitEventNotificationStore.RetryAwaitEventNotification` | 1 |
| `internal/app/serve/execution_reconciliation_queues.go` | `internal/store.DriverRunOutcomeStore.ClaimDriverRunOutcomes` | 1 |
| `internal/app/serve/execution_reconciliation_queues.go` | `internal/store.DriverRunOutcomeStore.CompleteDriverRunOutcome` | 1 |
| `internal/app/serve/execution_reconciliation_queues.go` | `internal/store.DriverRunOutcomeStore.RetryDriverRunOutcome` | 1 |
| `internal/app/serve/execution_reconciliation_queues.go` | `internal/store.TerminalDriverRunWorkRecoveryQueueStore.ClaimTerminalDriverRunWorkRecoveries` | 1 |
| `internal/app/serve/execution_reconciliation_queues.go` | `internal/store.TerminalDriverRunWorkRecoveryQueueStore.CompleteTerminalDriverRunWorkRecovery` | 1 |
| `internal/app/serve/execution_reconciliation_queues.go` | `internal/store.TerminalDriverRunWorkRecoveryQueueStore.RetryTerminalDriverRunWorkRecovery` | 1 |
| `internal/app/serve/execution_task_run_convergence.go` | `internal/store.OutboxStore.Create` | 1 |
| `internal/app/serve/execution_task_run_convergence.go` | `internal/store.TaskRunEventStore.Append` | 1 |
| `internal/app/serve/execution_task_run_convergence.go` | `internal/store.TaskRunTerminalConvergenceStore.CompleteTaskRunTerminalConvergence` | 1 |
| `internal/app/serve/execution_task_run_convergence.go` | `internal/store.TerminalDriverStepRepairStore.RepairTerminalDriverStep` | 1 |
| `internal/app/serve/execution_task_run_ports.go` | `internal/store.NodeStore.Create` | 1 |
| `internal/app/serve/execution_task_run_ports.go` | `internal/store.NodeStore.Heartbeat` | 1 |
| `internal/app/serve/execution_task_run_ports.go` | `internal/store.NodeStore.Update` | 2 |
| `internal/app/serve/execution_worker_profiles.go` | `internal/store.WorkerProfileStore.Create` | 1 |
| `internal/app/serve/execution_worker_profiles.go` | `internal/store.WorkerProfileStore.Delete` | 1 |
| `internal/app/serve/execution_worker_profiles.go` | `internal/store.WorkerProfileStore.Update` | 1 |

### Interaction

20 rows representing 20 call sites.

| Source file | Mutating persistence call | Call sites |
|---|---|---:|
| `internal/app/serve/interaction_fleetdb.go` | `internal/infra/fleetdb.InteractionMutationTransport.ClaimInteractionInbox` | 1 |
| `internal/app/serve/interaction_fleetdb.go` | `internal/infra/fleetdb.InteractionMutationTransport.CompleteInteractionInbox` | 1 |
| `internal/app/serve/interaction_fleetdb.go` | `internal/infra/fleetdb.InteractionMutationTransport.CreateInteractionTerminal` | 1 |
| `internal/app/serve/interaction_fleetdb.go` | `internal/infra/fleetdb.InteractionMutationTransport.EnqueueInteractionInbox` | 1 |
| `internal/app/serve/interaction_fleetdb.go` | `internal/infra/fleetdb.InteractionMutationTransport.FinishInteractionSession` | 1 |
| `internal/app/serve/interaction_fleetdb.go` | `internal/infra/fleetdb.InteractionMutationTransport.ForceInterruptInteractionSession` | 1 |
| `internal/app/serve/interaction_fleetdb.go` | `internal/infra/fleetdb.InteractionMutationTransport.HeartbeatInteractionSession` | 1 |
| `internal/app/serve/interaction_fleetdb.go` | `internal/infra/fleetdb.InteractionMutationTransport.InterruptInteractionSessionIfLeaseMissing` | 1 |
| `internal/app/serve/interaction_fleetdb.go` | `internal/infra/fleetdb.InteractionMutationTransport.PatchInteractionSession` | 1 |
| `internal/app/serve/interaction_fleetdb.go` | `internal/infra/fleetdb.InteractionMutationTransport.RecoverInteractionSessionStart` | 1 |
| `internal/app/serve/interaction_fleetdb.go` | `internal/infra/fleetdb.InteractionMutationTransport.StartInteractionSession` | 1 |
| `internal/app/serve/interaction_fleetdb.go` | `internal/infra/fleetdb.InteractionMutationTransport.UpdateInteractionTerminal` | 1 |
| `internal/sessions/archive.go` | `*internal/sessions.Store.AppendEnvelope` | 1 |
| `internal/sessions/archive.go` | `*internal/sessions.Store.CompactIndex` | 1 |
| `internal/sessions/archive.go` | `*internal/sessions.Store.CreateSession` | 1 |
| `internal/sessions/archive.go` | `*internal/sessions.Store.PurgeOlderThan` | 1 |
| `internal/sessions/archive.go` | `*internal/sessions.Store.ReIndex` | 1 |
| `internal/sessions/archive.go` | `*internal/sessions.Store.SaveMetadata` | 1 |
| `internal/sessions/archive.go` | `*internal/sessions.Store.SyncNativeTranscript` | 1 |
| `internal/sessions/archive.go` | `*internal/sessions.Store.SyncSubagentTranscript` | 1 |

### Read projection

2 rows representing 2 call sites.

| Source file | Mutating persistence call | Call sites |
|---|---|---:|
| `internal/usage/projection.go` | `*internal/usage.Store.Append` | 1 |
| `internal/usage/projection.go` | `*internal/usage.Store.PurgeOlderThan` | 1 |

### Source Control

3 rows representing 9 call sites.

| Source file | Mutating persistence call | Call sites |
|---|---|---:|
| `internal/infra/sourcecontrolstackstore/stackstore.go` | `*internal/infra/sourcecontrolstackstore.LocalStore.save` | 1 |
| `internal/infra/sourcecontrolstackstore/stackstore.go` | `*internal/infra/sourcecontrolstackstore.LocalStore.updateStackNodeRecord` | 2 |
| `internal/infra/sourcecontrolstackstore/stackstore.go` | `*internal/infra/sourcecontrolstackstore.LocalStore.withLock` | 6 |

### Workspace

22 rows representing 33 call sites.

| Source file | Mutating persistence call | Call sites |
|---|---|---:|
| `internal/cli/serve/workspacemgr/repository_admission_journal_store.go` | `*internal/cli/serve/workspacemgr.RepositoryAdmissionJournal.saveLocked` | 1 |
| `internal/cli/serve/workspacemgr/repository_admission_journal_store.go` | `*internal/cli/serve/workspacemgr.RepositoryAdmissionJournal.withLockedFile` | 6 |
| `internal/cli/serve/workspacemgr/repository_admission_lease.go` | `*internal/cli/serve/workspacemgr.RepositoryAdmissionJournal.acquireMaterializationLock` | 1 |
| `internal/cli/serve/workspacemgr/repository_admission_lease.go` | `*internal/cli/serve/workspacemgr.RepositoryAdmissionJournal.activateMaterializationAuthority` | 1 |
| `internal/cli/serve/workspacemgr/repository_admission_lease.go` | `*internal/cli/serve/workspacemgr.RepositoryAdmissionJournal.deactivateAllMaterializationAuthorities` | 1 |
| `internal/cli/serve/workspacemgr/repository_admission_lease.go` | `*internal/cli/serve/workspacemgr.RepositoryAdmissionJournal.deactivateMaterializationAuthority` | 4 |
| `internal/cli/serve/workspacemgr/repository_admission_lease.go` | `*internal/cli/serve/workspacemgr.RepositoryAdmissionJournal.renewMaterializationAuthority` | 1 |
| `internal/cli/serve/workspacemgr/repository_admission_lease.go` | `internal/infra/fleetdb.RepositoryAdmissionTransport.ClaimRepositoryAdmissionRecovery` | 1 |
| `internal/cli/serve/workspacemgr/repository_admission_lease.go` | `internal/infra/fleetdb.RepositoryAdmissionTransport.RenewRepositoryAdmission` | 1 |
| `internal/cli/serve/workspacemgr/repository_admission_process.go` | `*internal/cli/serve/workspacemgr.RepositoryAdmissionJournal.bind` | 1 |
| `internal/cli/serve/workspacemgr/repository_admission_process.go` | `*internal/cli/serve/workspacemgr.RepositoryAdmissionJournal.prepare` | 2 |
| `internal/cli/serve/workspacemgr/repository_admission_process.go` | `*internal/cli/serve/workspacemgr.RepositoryAdmissionJournal.remove` | 1 |
| `internal/cli/serve/workspacemgr/repository_admission_process.go` | `internal/infra/fleetdb.RepositoryAdmissionTransport.BeginRepositoryAdmission` | 1 |
| `internal/cli/serve/workspacemgr/repository_admission_process.go` | `internal/infra/fleetdb.RepositoryAdmissionTransport.CommitRepositoryAdmission` | 1 |
| `internal/cli/serve/workspacemgr/repository_admission_process.go` | `internal/infra/fleetdb.RepositoryAdmissionTransport.CreateWorkspaceWithRepositoryAdmission` | 1 |
| `internal/cli/serve/workspacemgr/repository_admission_process.go` | `internal/infra/fleetdb.RepositoryAdmissionTransport.FailRepositoryAdmission` | 1 |
| `internal/infra/workspacecatalog/catalog.go` | `internal/store.RepoStore.Create` | 1 |
| `internal/infra/workspacecatalog/catalog.go` | `internal/store.RepoStore.Delete` | 1 |
| `internal/infra/workspacecatalog/catalog.go` | `internal/store.RepoStore.Update` | 1 |
| `internal/infra/workspacecatalog/catalog.go` | `internal/store.WorkspaceStore.Create` | 1 |
| `internal/infra/workspacecatalog/catalog.go` | `internal/store.WorkspaceStore.Delete` | 1 |
| `internal/infra/workspacecatalog/catalog.go` | `internal/store.WorkspaceStore.Update` | 3 |


## Packages outside `internal/modules/`

| Architectural role | Packages |
|---|---:|
| Application workflows and composition | 11 |
| CLI and delivery surfaces | 32 |
| Driver and orchestration | 5 |
| Infrastructure adapters | 14 |
| Platform and shared mechanisms | 36 |
| WebUI and HTTP delivery | 43 |
| **Total outside modules** | **141** |

The 17 packages under `internal/modules/` are deliberately omitted from this
section. Together they form the ten capability roots and their owner-local
adapters. “Outside modules” does not mean “legacy”: it includes composition
roots, external adapters, delivery mechanisms, generated contracts, and
cross-capability platform mechanics. The final Phase 9 deletion audit found no
remaining horizontal model/repository plane or fallback package.

### Application workflows and composition

11 packages.

| Package | Purpose |
|---|---|
| `internal/app/agentprovisioning` | Coordinates durable agent provisioning, progress recovery, and runtime convergence through Agents-owned ports. |
| `internal/app/agentprovisioning/fleetdb` | Adapts the agent-provisioning workflow to FleetDB-backed progress persistence. |
| `internal/app/connectorgrants` | Coordinates connector-grant creation as an application workflow across Connectors and Automation seams. |
| `internal/app/prreviewer` | Ensures the managed role and agent identity used by the pull-request review UI. |
| `internal/app/serve` | Composition root that wires capability interfaces, owner adapters, authorities, recovery queues, and runtime providers for `loom serve`. |
| `internal/app/systemeventing` | Routes trusted internal system events into Automation through a narrow application workflow. |
| `internal/app/webhookingestion` | Validates and admits external webhook deliveries into Automation. |
| `internal/app/workflowauthoring` | Coordinates built-in, native, and global-runner workflow authoring and version materialization. |
| `internal/app/workflowbinding` | Coordinates binding lifecycle between authored workflows and Automation triggers. |
| `internal/app/workfloweventing` | Routes workflow-produced events through Automation without exposing its persistence adapter. |
| `internal/app/workitemmove` | Coordinates Work Item status moves and their cross-capability effects. |

### CLI and delivery surfaces

32 packages.

| Package | Purpose |
|---|---|
| `internal/cli` | Shared CLI dependency assembly, backend invocation, task routing, worktree resolution, locking, and tracing. |
| `internal/cli/agent` | Autonomous plan/task worker commands: claim work, build prompts, execute agents, recover, and complete runs. |
| `internal/cli/agent/lead` | Interactive lead command and session-heartbeat lifecycle. |
| `internal/cli/agentdef` | Operator command surface for managing agent definitions through the Agents capability. |
| `internal/cli/automode` | Foreground autonomous CLI loop, tmux integration, retry policy, rate limiting, and usage emission. |
| `internal/cli/backends` | Concrete Codex, Claude, Cursor, Gemini, OpenCode, and external harness process adapters. |
| `internal/cli/cleanup` | Operator commands for purging session and usage history. |
| `internal/cli/cmdstore` | CLI-side composition of narrow connector and workspace catalog persistence access. |
| `internal/cli/config` | Reads and writes local Loom configuration, repository lists, checkpoints, and Fleet settings. |
| `internal/cli/connector` | Operator command surface for connector configuration and lifecycle. |
| `internal/cli/data` | Work Item and TaskRun management CLI, including create, query, claim, update, comment, and completion operations. |
| `internal/cli/doctor` | Diagnoses local runtime, Redis, FleetDB, tmux, stale-process, and transcript health. |
| `internal/cli/driver` | Hidden and operator Driver commands for registration, execution, task runs, run context, and native bundles. |
| `internal/cli/epic` | Epic execution, reconciliation, and stack coordination commands. |
| `internal/cli/git` | User-facing Git operations, diff inspection, synchronization, conflict handling, and pull-request commands. |
| `internal/cli/hooks` | Installs, parses, and dispatches Loom repository hooks and assignment context. |
| `internal/cli/local` | Starts and supervises the machine-local Loom runtime and OS-specific background process integration. |
| `internal/cli/managementapi` | Small authenticated management-API client used by standalone CLI commands. |
| `internal/cli/monitor` | Collects and renders workspace, Work Item, agent, Git, and runtime monitoring state. |
| `internal/cli/repo` | Operator commands for workspace repository registration and mutation. |
| `internal/cli/role` | Operator commands for Agents role lifecycle. |
| `internal/cli/serve` | `loom serve` command, runtime startup, authentication, cache, background-loop, and platform policy wiring. |
| `internal/cli/serve/metricscmd` | Metrics and usage collection handlers for the serve process. |
| `internal/cli/serve/opsimpl` | Concrete backend and Git operation implementations exposed to higher-level operation interfaces. |
| `internal/cli/serve/serveadapter` | Adapts serve composition to capability interfaces, providers, task-run environments, and runtime callbacks. |
| `internal/cli/serve/worker` | Worker-node commands, profile registration, service lifecycle, and log forwarding. |
| `internal/cli/serve/workspacemgr` | Workspace creation and repository-admission materialization, fencing, journaling, recovery, and persistence adapters. |
| `internal/cli/sessionfinalize` | Finalizes source-control worktrees when an agent session completes. |
| `internal/cli/stack` | CLI commands and resolvers for Source Control stack lineage. |
| `internal/cli/trigger` | Automation trigger and binding management CLI. |
| `internal/cli/workflow` | Workflow Catalog and workflow-run management CLI. |
| `internal/cli/workspace` | Workspace initialization, status, backend checks, repository worktrees, and workspace operations. |

### Driver and orchestration

5 packages.

| Package | Purpose |
|---|---|
| `internal/driver` | Workflow/TaskRun execution engine: registration, claims, retries, await handling, outcomes, task bridges, and worktree preparation. |
| `internal/driver/daytonahost` | Adapter that places driver execution on Daytona-hosted environments. |
| `internal/driver/nativearchive` | Policy for accepting and materializing native workflow archives. |
| `internal/driver/sandbox` | Process/container launchers, isolation policy, resource limits, and serve-only egress relay. |
| `internal/epicrunner` | Lead-owned epic assignment startup and persisted assignment-context resolution. |

### Infrastructure adapters

14 packages.

| Package | Purpose |
|---|---|
| `internal/infra/automationruntime` | Automation event bridge and reconcilers for issue-journal ready/review events, awaits, cursors, and subject keys. |
| `internal/infra/connectorsproviders` | Provider-specific connector metadata and behavior for GitHub, Slack, Datadog, and defaults. |
| `internal/infra/connectorsvault` | Sealed credential vault adapter implementing Connectors secret lifecycle. |
| `internal/infra/fleetdb` | Shared low-level FleetDB transport implementations for capability-owned ports outside Work Items. |
| `internal/infra/interactionchat` | Codex and harness chat-runtime adapters for interactive conversations. |
| `internal/infra/interactionclient` | Client adapter for remote Interaction management operations. |
| `internal/infra/interactionlead` | Codex and harness interactive-lead runtimes, delivery queues, metadata, transcripts, and session lifecycle. |
| `internal/infra/localgit` | Contained local Git executor, inspector, and network/process policy. |
| `internal/infra/memstore` | In-memory persistence adapter for platform, execution, agent, artifact, connector, worker, and workspace contracts. |
| `internal/infra/sourcecontrolpublisher` | Publishes and reconciles branches/stacks to local or GitHub forges with scrubbed output. |
| `internal/infra/sourcecontrolstackstore` | Machine-local persistence adapter for Source Control stack lineage and node records. |
| `internal/infra/workflowdistribution` | Locates, validates, embeds, and digests packaged built-in workflow bundles. |
| `internal/infra/workflowdistribution/authoring` | Bridges packaged workflow distribution into the workflow-authoring application interface. |
| `internal/infra/workspacecatalog` | Persistence adapter for Workspace and repository catalog commands and queries. |

### Platform and shared mechanisms

36 packages.

| Package | Purpose |
|---|---|
| `internal/agenterr` | Normalizes backend, harness, and domain failures into stable agent outcomes and error classes. |
| `internal/agentpolicy` | Single retry, failover, block, fast-fail, and backoff-bucket policy for agent outcomes. |
| `internal/archtest` | Architecture analyzer and exact inventories for capability edges, writes, packages, mutations, runtimes, and performance. |
| `internal/atomicfile` | Writes and replaces files atomically to prevent partial durable state. |
| `internal/bootstrap` | Resolves local/server storage mode, paths, active workspace, embedded FleetDB startup, and bootstrap state. |
| `internal/circuitbreaker` | Reusable closed/open/half-open failure circuit breaker. |
| `internal/configlock` | Cross-process lock protecting local configuration mutation. |
| `internal/domain` | Platform-orchestration value types for nodes, sessions, awaits, outbox, and runtime roles shared by execution transports and composition; it does not carry the Work Items model. |
| `internal/events` | Structured local event model, JSONL persistence/replay, metrics, and trace-context propagation. |
| `internal/events/otelexport` | Exports Loom events through OpenTelemetry. |
| `internal/gitauth` | Resolves Git authentication material from approved credential sources. |
| `internal/gitbranch` | Validates and normalizes Git branch names. |
| `internal/harness` | Retry helper for invoking external agent harnesses. |
| `internal/httpclient` | Authenticated CLI-to-Loom HTTP client with auth discovery, device flow, and token caching. |
| `internal/kv` | Redis-compatible key/value client helpers, Lua scripts, and stale-record handling. |
| `internal/localsettings` | Persists machine-local UI and operator settings. |
| `internal/localworkspace` | Safe local workspace, repository, worktree, and PR-review checkout paths and Git process helpers. |
| `internal/lockfile` | Portable filesystem/process lock implementation for Unix, Windows, and WASM. |
| `internal/logrouter` | Routes and rotates process logs and watches log sources. |
| `internal/logstore` | Opens, locates, and streams persisted session/runtime logs. |
| `internal/netutil` | Free-port allocation and health-check helpers. |
| `internal/observability/tracing` | OpenTelemetry tracing configuration and propagation helpers. |
| `internal/ops` | Import-light operation interfaces and shared DTOs used by both CLI and WebUI layers. |
| `internal/platform/authority` | Authority, issuer, trust-mode, system credential, and admission primitives shared by capability seams. |
| `internal/platform/fleethttp` | Capability-neutral FleetDB HTTP connection pooling and request mechanics. |
| `internal/platform/httptransport` | Bounded JSON and generic HTTP transport helpers. |
| `internal/platform/loomapi/gen` | Generated Go wire types for the Loom management OpenAPI contract. |
| `internal/platform/repositoryremote` | Canonical repository-remote parsing and normalization. |
| `internal/platform/runtime` | Runtime host, component lifecycle, health, clocks, provider identity, workspace identity, and subprocess environment policy. |
| `internal/roleprompts` | Loads and resolves built-in role prompt templates. |
| `internal/sessions` | Durable agent-session metadata, transcript ingestion, indexing, compaction, notification, querying, and cleanup. |
| `internal/sessions/redact` | Redacts secrets and sensitive values from persisted session material. |
| `internal/sessions/transcript` | Canonical transcript event parsing, normalization, wrapper conversion, and tag stripping. |
| `internal/store` | Composition-only persistence interfaces for platform orchestration records not owned by Work Items; production use outside approved composition and owner adapters is ratcheted to zero. |
| `internal/testutil` | Shared test fixtures and environment isolation helpers; intended only for `_test.go` consumers. |
| `internal/usage` | Collects, persists, projects, queries, and purges agent token/cost usage. |

### WebUI and HTTP delivery

43 packages.

| Package | Purpose |
|---|---|
| `internal/webui` | Shared WebUI HTTP configuration, response/error handling, backend capability reporting, metrics, tracing, and safe dialing. |
| `internal/webui/agentcoord` | Coordinates interactive agent runtime start/stop and exposes its narrow WebUI interface. |
| `internal/webui/agentmodules` | Assembles agent, automation, and workspace HTTP route modules from capability dependencies. |
| `internal/webui/app` | Top-level WebUI/serve composition, workspace registration, frontend serving, and route assembly. |
| `internal/webui/apperrors` | Stable application-error vocabulary mapped by HTTP delivery code. |
| `internal/webui/coordinator` | Orders per-workspace lifecycle hooks with resource handoff, rollback, and deregistration. |
| `internal/webui/editor` | Discovers and launches configured editors with OS-specific process handling. |
| `internal/webui/filecoord` | Contained workspace file browsing, content, versions, history, Git status, and rooted access policy. |
| `internal/webui/fleet` | Fleet worker HTTP endpoints, JWT/auth, metrics, rate limiting, store registry, and task timeout enforcement. |
| `internal/webui/handlers/agents` | Agent creation, lifecycle, schedule, session, history, and canonical response handlers. |
| `internal/webui/handlers/agentsmanagement` | HTTP module exposing Agents management commands and queries. |
| `internal/webui/handlers/approvals` | HTTP module for Workflow Catalog approval and activation operations. |
| `internal/webui/handlers/connectors` | HTTP module for connector and grant lifecycle. |
| `internal/webui/handlers/driverapi` | Run-token-protected Driver operation endpoints for awaits, events, task runs, connectors, Work Items, and workflows. |
| `internal/webui/handlers/executionmanagement` | HTTP module for Execution management commands and projections. |
| `internal/webui/handlers/git` | Git diff, graph, pull-request, and repository operation handlers. |
| `internal/webui/handlers/health` | Server and runtime readiness/health endpoints. |
| `internal/webui/handlers/interactionmanagement` | HTTP module for Interaction terminal, inbox, session, and transcript management. |
| `internal/webui/handlers/issues` | Work Item list/detail/search/move/comment/dependency/event/repository/session/tab HTTP handlers. |
| `internal/webui/handlers/localsettings` | HTTP endpoints for machine-local settings. |
| `internal/webui/handlers/misc` | Cross-cutting endpoints for auth config, backends, files, logs, editors, sessions, and worker operations. |
| `internal/webui/handlers/onboarding` | First-task onboarding endpoint and route module. |
| `internal/webui/handlers/prreview` | Pull-request review listing, membership, reviewer provisioning, seeding, and streaming handlers. |
| `internal/webui/handlers/roles` | HTTP module for role queries and lifecycle. |
| `internal/webui/handlers/taskrunapi` | Lease-protected task-run artifact and Daytona operations used by task runners. |
| `internal/webui/handlers/terminal` | Interactive terminal, PTY, tab, session, WebSocket, and agent-identity handlers. |
| `internal/webui/handlers/triggerbindings` | HTTP module for Automation trigger binding lifecycle. |
| `internal/webui/handlers/webhooks` | External webhook verification, secret resolution, admission, and provider-specific handlers. |
| `internal/webui/handlers/workflows` | Workflow listing, built-in/native workflow registration, preflight, and task-workflow run handlers. |
| `internal/webui/handlers/workspace` | Workspace/repository creation, deletion, ordering, jobs, catalog, and default-selection handlers. |
| `internal/webui/hooks` | Per-workspace lifecycle hooks for Fleet stores, subscriptions, PTYs, terminals, and Work Items adapters. |
| `internal/webui/localredis` | Machine-local Redis persistence for issue tabs, session history, metadata, snapshots, and metrics. |
| `internal/webui/readprojection` | Builds UI read projections for binding runs and task-workflow runs. |
| `internal/webui/server/dto` | Canonical HTTP request/response DTOs and validation for agents, Work Items, sessions, and workspaces. |
| `internal/webui/server/handler` | Reusable HTTP parsing, authentication, error mapping, responses, and run-history helpers. |
| `internal/webui/server/middleware` | Authentication, CORS, security, recovery, rate limiting, workspace, and logging middleware. |
| `internal/webui/server/realtime` | SSE/WebSocket hub, mutation events, terminal relay, and short-lived access-token handling. |
| `internal/webui/sessioncoord` | Coordinates session history, transcript serving, diffs, execution projection, and event-store reads. |
| `internal/webui/sourcecontrolcoord` | Coordinates source-control operations behind a WebUI-facing interface. |
| `internal/webui/storeadapter` | Projects narrow workspace/repository stores into the shared `ops.WorkspaceData` WebUI read shape. |
| `internal/webui/subscription` | Multi-workspace subscriptions and Work Item mutation long-poll/event delivery. |
| `internal/webui/terminal` | PTY/tmux lifecycle, buffers, tabs, commands, source metadata, and terminal implementation. |
| `internal/webui/workspacecoord` | Coordinates workspace validation, cache, mutation jobs, and workspace-operation ports. |


## Enforcement and maintenance

The machine-readable inventories remain authoritative:

- `TestCheckedInDirectWriteInventoryStrictCounts` pins 90 rows and 108 sites.
- `TestCheckedInProductionPackageShape` pins the exact 158-package list and
  its `17 modules / 141 outside` split.
- Direct-write analysis runs over all 11 declared build profiles and uses Go
  type information, not method-name matching.
- Package descriptions in this document are explanatory; adding or deleting a
  package still requires updating and passing the exact package inventory.

When a direct persistence call or outside-module package changes, update the
canonical inventory first, regenerate or review this catalog against it, and
run the focused architecture tests before changing the Phase 9 evidence.

---

[Migration overview](README.md) · [Phase 9 package consolidation](16-phase-9-package-consolidation.md)
