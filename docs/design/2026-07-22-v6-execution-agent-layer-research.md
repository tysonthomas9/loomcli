# Loom v6 Execution-Agent Layer Research

**Status:** Research synthesis and decision input; not an accepted contract<br>
**Date:** 2026-07-22<br>
**Loom base:** `b490a290b0ba66ca7965c58aeaf030d24940c99a`<br>
**Scope:** TypeScript SDK and runtime contracts for executing agents inside
Loom-managed `TaskRun`s.

**Related local contracts:**

- [`2026-07-22-v6-execution-agent-priorities-and-typescript-examples.md`](2026-07-22-v6-execution-agent-priorities-and-typescript-examples.md)
- [`native-flue-driver-integration.md`](native-flue-driver-integration.md)
- [`fleetdb-agent-platform-v2-proposal.md`](fleetdb-agent-platform-v2-proposal.md)
- [`fleetdb-agent-platform-v2-execution-topology-addendum.md`](fleetdb-agent-platform-v2-execution-topology-addendum.md)
- [`fleetdb-agent-platform-v2-phased-delivery.md`](fleetdb-agent-platform-v2-phased-delivery.md)
- [`2026-06-18-stack-aware-pr-publisher.md`](2026-06-18-stack-aware-pr-publisher.md)
- [`../product/agent-execution-prd.md`](../product/agent-execution-prd.md)
- [`../product/container-runner-mvp-spec.md`](../product/container-runner-mvp-spec.md)
- [`../product/session-artifact-contract.md`](../product/session-artifact-contract.md)
- [`../../sdk/README.md`](../../sdk/README.md)
- [`../../sdk/api-surface.v1.json`](../../sdk/api-surface.v1.json)

## Executive conclusion

**Finding:** Flue, Daytona, and Vercel Eve solve different parts of the system.

- **Flue** is an agent and workflow harness.
- **Daytona** is an isolated compute and workspace provider.
- **Eve** is a batteries-included durable agent application framework.
- **Loom** is the current orchestration, authorization, placement, and task
  lifecycle system, and should become the canonical delivery and evidence
  system.

**Recommendation:** Loom v6 should add one provider-neutral, lease-bound
execution-agent layer. It should compose a harness adapter, an environment
adapter, repository and delivery services, and a durable evidence sink. It
should not turn every CLI command, provider, or built-in workflow into an SDK
runner primitive.

Target three authority-separated TypeScript surfaces:

1. `@loom/sdk/control` for operator and application authority.
2. `@loom/sdk/driver` for one claimed `DriverRun` and orchestration.
3. `@loom/sdk/runner` for one leased and fenced `TaskRun`.

Add execution-agent behavior as an additive, lease-bound namespace under the
runner client. Provider adapters carry no independent Loom authority.

| Existing term | Model it as | Do not model it as |
|---|---|---|
| `epic-runner` | Built-in driver/workflow policy | Universal SDK primitive |
| Cron | Durable trigger binding that invokes a driver | `cronRunner` |
| Daytona | Environment provider | `daytonaRunner` authority |
| Flue or Eve | Agent-harness adapter | Loom task scheduler |
| Git checkout/diff/commit | Repository service | `gitRunner` |
| Push/PR/review | Policy-checked delivery service | Unrestricted shell escape |

## Research method and source snapshot

The main research pass inventoried Loom's current driver SDK, runner SDK,
built-in workflows, product specs, execution-topology addendum, and an
out-of-tree `loom.agent.exec` prototype. Three delegated source passes then
mapped Flue, Daytona, and Eve independently. The synthesis was cross-checked
against this v6 base and the Loom-pinned Flue checkout.

| Target | Snapshot inspected | Important caveat |
|---|---|---|
| Loom v6 | Base commit `b490a290b` cloned from the committed v5 HEAD | The source v5 worktree had unrelated uncommitted work; it is not part of this base. |
| Loom research worktree | Commit `03c56b90712eb79ec5770120a4fd90aa37faed35` | It contains a later `loom.agent.exec` prototype that is not present in this v6 base. |
| Flue used by Loom | Commit `492bf47b9f3d6c379d00471523987b8fe9511f7d`, `@flue/runtime` `1.0.0-beta.2` | Loom must not assume newer upstream APIs exist in its pinned bundle. |
| Current upstream Flue | `@flue/runtime` `1.0.0-beta.9` at research time | Pre-1.0 APIs and event contracts can move. |
| Daytona | Official `@daytona/sdk` TypeScript documentation, documented as v0.200 | Several capabilities vary by sandbox class, region, tier, or experimental status. |
| Vercel Eve | Commit `d2b57131c7817d61e9d6e4117f665aba20199bab`, package `0.27.1` | Eve is explicitly beta; use it as an architectural reference, not a compatibility target. |

The process-agent prototype came from the Loom research checkout at
`/Users/tyson/codebase/code-agents/agent-traces/loomcli`, commit
`03c56b90712eb79ec5770120a4fd90aa37faed35`, primarily
`sdk/prototypes/agent-exec/README.md` and
`sdk/prototypes/agent-exec/agent-exec.mjs`. Those sources are provenance for
the findings below, not files available in this v6 clone.

## Established Loom constraints

These are existing design or implementation constraints. They are not new
recommendations from this research.

1. **FleetDB is the durable task authority.** It owns claims, dependency
   readiness, transitions, leases, `DriverRun`s, `TaskRun`s, sessions, and
   artifact metadata. Loom is the policy and runtime-control layer over those
   records.
2. **Resource meanings stay distinct.** A `DriverRun` is one orchestration
   execution, a `TaskRun` is one finite fenced attempt, an `AgentService` is a
   persistent agent, and a session groups agent telemetry or conversation
   state. A session does not replace a `TaskRun`.
3. **Native Flue owns authoring and build semantics.** Loom registers an
   already-built immutable Flue artifact and explicitly activates a
   `DriverVersion`; it does not regenerate a Flue project.
4. **Driver code orchestrates.** Privileged coding work belongs to a real child
   `TaskRun`, not to an untracked helper process inside the driver.
5. **Runner placement and sandbox placement are independent.** The process
   owning lease heartbeats and artifact uploads can run on a host or executor
   pod while the filesystem and shell live in Daytona.
6. **Mutation is scoped and fenced.** A remote environment must not receive
   broad FleetDB authority. Cloud artifacts must be server-visible and
   finalized before completion.
7. **Unknown or untrusted driver code fails closed.** It requires an isolating
   launcher; sandbox placement records also preserve egress mode and mechanism.
8. **The public SDK v1 is frozen.** Its two authority-bearing clients are
   `@loom/sdk/driver` and `@loom/sdk/runner`; the package also exports a root
   entry and authority-free `@loom/sdk/runtime-adapters` utilities. Additions
   can be additive; removals and renames require a major version.
9. **Driver suspension already exists.** `events.await` and `workflows.await`
   suspend and deterministically re-enter driver code by call order. Eve-style
   durability must not create a second competing `DriverRun` checkpoint model.
10. **A requested `runner` is a pinned execution strategy.** It is distinct
    from the `runnerId` of the process that later claims the task attempt.

### Documentation drift to avoid

Some older documents predate later decisions:

- `workflow-driver-authoring-guide.md` and parts of the original V2 proposal
  describe Loom accepting raw TypeScript and building `.loom/workflows`.
  `native-flue-driver-integration.md` supersedes that authoring path.
- Older examples import a removed `@loom/sdk/flue` surface. The current driver
  import is `@loom/sdk/driver`.
- Older product documents sometimes use `AgentSession` as a generic run record
  and list stale terminal statuses. This note does not adopt that taxonomy.
- Older examples select `providerProfile: "flue-daytona"`. The current public
  driver request selects `runner`; provider-profile fields remain compatibility
  metadata, not the current request primitive.

## Current Loom SDK and runner baseline

| Surface | Current responsibility | Gap relevant to v6 |
|---|---|---|
| Driver SDK | Epics, agent orchestration, task claim/complete/release, child `TaskRun`s, connector calls, durable awaits, and child workflows | No reusable execution-agent abstraction; deliberately not broad operator authority |
| Runner SDK | One TaskRun's request/input, task/run reads, heartbeat, logs, artifacts, runtime credential lookup, and completion | No provider-neutral environment, process, agent invocation, repository, delivery, approval, or full execution-event contract |
| Runtime adapters | Flue event normalization, transcript collection/redaction, usage extraction, and transcript serialization | Useful authority-free utilities, not an execution context or provider contract |
| App/control SDK | Proposed in V2 | Not currently published |
| Built-in workflows | `epic-runner`, local and Daytona task runners, and a GitHub review workflow | Leaf workflows repeat execution, Git, evidence, and delivery policy |
| CLI | Human/operator UX plus local process and Git behavior | Mixed authority and presentation make it the wrong parity target |

The clearest implementation evidence is
[`daytona-task-runner.ts`](../../internal/workflows/builtin/daytona-task-runner.ts).
One workflow currently performs all of the following:

- resolves Codex, Daytona, and GitHub credentials;
- provisions and deletes a Daytona sandbox;
- adapts Daytona filesystem and process APIs to Flue;
- constructs a Flue harness through `@flue/runtime/internal`;
- clones, verifies, branches, diffs, commits, and pushes a repository;
- invokes the agent and converts Flue events to Loom transcript and usage;
- uploads patch and pull-request artifacts;
- calls GitHub directly to create or find a pull request; and
- decides cleanup and retention behavior.

That workflow is useful product evidence, but its shape should not become the
SDK contract. It combines four separable concerns: Loom task authority, agent
harness, execution environment, and repository delivery.

### Process-agent prototype finding

A later research worktree, not included in this v6 base, explored a
`loom.agent.exec` helper with these confirmed semantics:

- one Loom `AgentSession` per external agent-process invocation;
- a stable `invocationKey` and server-composed session identity;
- structured return values for spawn failure, non-zero exit, and timeout;
- programming/specification errors throw;
- bounded session-open retries followed by degraded observability rather than
  blocking useful work;
- automatic close by default and explicit deferred finalization when the leaf
  needs to stamp the semantic task outcome;
- caller-owned `argv`, with helper-owned stream parsing, redaction, transcript
  upload, and usage extraction; and
- preflight creates no session because no agent process has started.

**Finding:** Those are useful external-process adapter semantics. They are not
the complete execution-agent layer because they do not allocate an environment,
establish a repository, authorize delivery, park for approval, or recover
provider placement.

## External framework map: Flue

### Role

Flue is the closest match for an agent harness. It is not a sandbox control
plane, task scheduler, repository service, or forge-delivery system.

### Capability map

| Area | Current Flue primitive | Boundary important to Loom |
|---|---|---|
| Agent authoring | `defineAgent`, profiles, model, instructions, tools, skills, Actions, subagents, compaction, durability, cwd, sandbox | Loom should not reproduce the model-loop authoring surface. |
| Harness and sessions | Root harness, named sessions, prompt, skill, task, shell, compact, filesystem | A Flue session is not a Loom task attempt. |
| Calls and cancellation | Promise-like call handles, abort signal, local operation abort | Local cancellation does not replace Loom run cancellation or fencing. |
| Structured results | Valibot input/output schemas and model/token/cost results | Validation is not a durable external-effect ledger. |
| Tools | Typed custom tools and MCP connections | Tool input validation is not authorization or retry policy. |
| Skills | Imported `SKILL.md` and programmatic skill definitions | Skills are instruction/resource packages, not jobs. |
| Subagents | Named agent profiles and child sessions via `session.task` | Child sessions are not child `TaskRun`s or `DriverRun`s. |
| Actions | Reusable deterministic application behavior around a harness | An Action is not automatically an inspectable Loom workflow. |
| Workflows | Finite typed runs with `runId` and input/output | Arbitrary TypeScript is not checkpointed or transparently resumed. |
| Agent dispatch | Durable input accepted into one continuing agent instance | Direct agent work is a submission, not a workflow run. |
| Models/providers | Provider/model selection, registration, thinking options | No FleetDB placement, capacity, or Loom fallback policy. |
| Sandboxes | Virtual just-bash, Node local, and custom `SandboxApi` adapters | Flue begins with an available environment; it does not own remote allocation, reuse, or destruction. |
| Client SDK | Prompt/send/observe agents; invoke workflows; get/stream runs | Data-plane client, not deployment or fleet control. |
| Streaming | Durable Streams offsets, long-poll/SSE, reconnection | Batch boundaries can redeliver; consumers must tolerate at-least-once delivery. |
| Events/transcript | Typed run, turn, message, tool, task, compaction, log, and settlement events | Stable normalized events and raw provider messages have different compatibility guarantees. |
| Usage | Per-turn, response, operation, and compaction usage/cost | Overlapping rollups can double-count if Loom sums every level. |
| Persistence | Execution, submission, run, event, conversation, and attachment stores | Does not persist sandbox files, repo state, secrets, or arbitrary external effects. |
| Durability | Ordered agent submissions, leases/fenced attempts, conservative recovery | Stronger for continuing agents than finite workflows. |
| Auth/secrets | Application route middleware and provider/application environment | No general run-scoped secrets broker or connector grant system. |
| Observability | Observers, instrumentation, structured logs, OpenTelemetry adapter | Export, aggregation, sampling, retention, and redaction remain application-owned. |
| Scheduling/channels | External scheduler calls `invoke`/`dispatch`; channel packages verify inbound requests | No native scheduler; outbound clients, OAuth, and business dedupe remain application-owned. |
| Evals | Framework-agnostic; docs recommend Vitest Evals or another harness | No native Loom-like evaluation and evidence resource. |

### Durability distinctions

- Continuing agents have canonical conversation history and an ordered durable
  submission queue.
- Recovery reuses completed model output and tool results when safe. A tool call
  whose outcome is unknown is not blindly repeated.
- Durable Node agents require a persistence adapter and a single live owner per
  instance; this is not active-active execution.
- Flue workflows do not checkpoint arbitrary TypeScript. Retrying starts a new
  run.
- At the inspected pin, interrupted Node workflow runs can remain orphaned as
  active even when durable records survive.
- Conversation durability and sandbox/workspace durability are independent.

### Not core Flue

Provider blueprints for Daytona, E2B, Modal, Vercel, and others are examples of
project-owned adapters. They are not a Flue provisioning plane. Flue also does
not provide core Git/worktree/commit/push/PR operations, an epic/DAG scheduler,
Loom artifact delivery, generic connector grants, a secrets broker, or a
compensation ledger.

### Loom boundary

Flue can own model turns, provider transport, tools, skills, in-process
subagents, agent conversation durability, harness-local events, and the
filesystem/command adapter after an environment exists. Loom still owns
assignment, task/run identity, lease/fence, placement, environment lifecycle,
credentials, repository/delivery policy, durable artifacts, and final outcome.

## External provider map: Daytona

### Role

Daytona supplies isolated execution computers and toolbox APIs. It does not own
the model loop, durable conversation, task state machine, approval policy, or
Loom completion semantics.

### Capability map

| Area | Daytona primitive | Important limit or distinction |
|---|---|---|
| Sandbox lifecycle | Create/get/list/start/stop/delete, pause/resume, archive/restore, recover, resize, labels, inactivity timers, TTL | Pause is VM/Windows-specific; archive is container-specific. Auto-stop may require explicit activity refresh for long agent work. |
| Process and shell | One-shot exec with cwd/env/timeout; named shell sessions; background commands; status, input, logs, streaming | There is no universal process-handle `AbortSignal`; PTYs have explicit kill. |
| PTY and reattach | Create/connect/list/inspect/resize/kill PTYs; rediscover named process sessions; reattach by sandbox ID | Loom must persist the TaskRun/session-to-sandbox mapping. |
| Filesystem | Create/delete/list/detail/move, upload/download, streaming, find/search, replace text, permissions | No first-class atomic patch transaction or repository diff. |
| Git | Clone, status, branch, checkout, add, commit, pull, push, reset/restore, history/remotes/config | Checkout-local Git only; no PR, issue, review, or forge API. Persisting plaintext credentials is explicitly dangerous. |
| Images/templates | Base image or Dockerfile builders, env/workdir/entrypoint, build logs | Image lifecycle is provider administration, not TaskRun authority. |
| Snapshots/forks | Snapshot services, cold filesystem snapshots, VM hot-memory snapshots, copy-on-write forks | Snapshot-from-running and fork APIs were experimental at research time. |
| Volumes | Persistent FUSE/S3 mounts, shared mounts, tenant subpaths | Non-transactional and unsuitable for database/block-storage semantics. |
| Network/ingress | Block-all and allowlists, linked sandboxes, preview URLs, signed URLs, SSH | Preview is proxied HTTP ingress; organization policy can override sandbox settings. |
| Secrets/auth | API keys/JWT, resource scopes, organization secrets, HTTPS egress substitution | Provider credentials are not automatically scoped to one Loom TaskRun. |
| Resources/placement | CPU, memory, disk, GPU, sandbox class, regions, dedicated/BYOC runners | Capabilities vary by class and target; require negotiation. |
| Logs/events/metrics | Command logs/streams, build logs, CPU/memory/disk metrics, OTEL, audit logs, lifecycle webhooks | Split across SDK and platform APIs. |
| Code intelligence | Stateless code run, stateful Python interpreter, LSP, chart artifacts | Useful optional capability, not universal execution core. |
| GUI/computer use | Linux/Windows mouse, keyboard, screenshots, recordings, display/window and accessibility APIs | macOS was private alpha at research time. |
| Agent integration | MCP tools and official agent-framework integrations | Integration still leaves the task/model lifecycle to the embedding system. |

### Loom boundary

Loom should define a provider-neutral environment contract and use Daytona as
one implementation. It should not wrap the full Daytona administration API.
The leaf runner must not receive organization-wide Daytona authority; operator
or executor code should provision the environment and expose only a
capability-scoped handle tied to the TaskRun.

Daytona also suggests improvements over a literal wrapper:

- prefer `argv` plus an explicit shell mode instead of only command strings;
- distinguish client timeouts from server-side cancellation;
- require process-tree cancellation for long work;
- negotiate optional capabilities rather than exposing nullable universal APIs;
- keep sandbox, process session, interpreter context, volume, and immutable
  template lifetimes separate; and
- broker Git/LLM credentials so plaintext need not enter the sandbox.

## External framework map: Vercel Eve

### Role

Eve is the strongest reference for production agent conveniences. It owns much
more of the application stack than Loom should require from every execution
adapter.

### Capability map

| Area | Eve primitive | Boundary important to Loom |
|---|---|---|
| Authoring | Filesystem agent with instructions, tools, skills, connections, hooks, sandbox, schedules, channels, and subagents | Eve owns the agent/model loop; Loom also supports external CLIs that do not. |
| Agent loop | AI SDK tool loop inside a durable outer workflow | Not a generic external-process protocol. |
| Models | Gateway or AI SDK model, dynamic selection, reasoning/provider options, budgets, compaction, structured output | Model config belongs to the Eve definition rather than a generic runner binary. |
| Typed tools | Schemas, execution, approval policy, redacted/model-specific output, rich context | Authored tools execute in the trusted app runtime; only delegated commands run in the sandbox. |
| Built-in tools | Bash, file read/write, glob/grep, web, todo, questions, subagent, skill, connection search | Complete-file writes use stale-read checks; no generic patch primitive. |
| Shell/filesystem | Persistent `/workspace`, run/spawn/wait/kill, text/binary file APIs, network mutation | Strong substrate, but no repository/worktree/delivery abstraction. |
| Sandbox backends | Vercel Sandbox, Docker, Microsandbox, Just Bash, custom adapter | Default network policy is permissive unless tightened. |
| Computer/browser | No first-class GUI, browser, screenshot, or computer-use contract found | Requires a tool/connection or Loom environment extension. |
| Git | Shell Git; GitHub channel can prepare authenticated refs and PR context | No generic public Git SDK or push/PR delivery contract. |
| Lifecycle | Session -> turn -> durable step; conversation or finite task modes; client create/send/stream/cancel | No Loom claim, lease, queue, attempt, review, or completion state machine. |
| Checkpoint/resume | Completed steps checkpoint; crashes/redeploys resume; approvals/OAuth/subagent waits park | Interrupted steps may rerun; tool authors still own external-effect idempotency. |
| Streaming/events | Durable typed session/turn/message/action/approval/subagent/compaction/result/failure events with cursors | No durable FIFO for concurrent user messages; callers/channels serialize them. |
| Context | Always-on instructions, progressive skills, workspace discovery, per-session state, compaction, todo preservation | Cross-session coordination requires an external store. |
| Artifacts | Persistent sandbox files, attachments, structured result, eval reports | No generalized Loom artifact/diff/transcript/delivery registry. |
| Observability | OpenTelemetry, model/tool spans, usage, hooks, audit/event callbacks, Agent Runs | Content capture requires an explicit sensitive-data policy. |
| Auth/connections | Route auth, principals, MCP/OpenAPI, OAuth, approvals, per-step tokens, credential brokering | Route auth alone does not enforce session ownership; trusted tools retain app authority. |
| Composition | Isolated subagents, lightweight copies, nesting, remote agents, connections, extensions | External agent runtimes still need a shared Loom execution protocol. |
| Deployment | Vercel Workflow/Sandbox/Cron or self-hosted adapters | Web/session-centric rather than TaskRun/worker-centric. |
| Evals | Public HTTP API, datasets, deterministic/LLM assertions, event/tool/order checks, reporters and artifacts | Strong precedent for testing the same public protocol production clients use. |

### Loom lessons

Borrow Eve's durable approval, connection-broker, typed-event, sandbox-adapter,
subagent-lineage, observability, and public-protocol eval concepts. Do not make
Eve's project layout or model loop a Loom compatibility requirement.

Eve notably lacks the things that differentiate Loom's execution plane:
first-class task claims and fences, fleet placement, repository/worktree
identity, guarded Git delivery, durable patch/transcript artifacts, and atomic
task completion/dependency progression.

## Comparative responsibility map

| Concern | Canonical owner |
|---|---|
| Tasks, dependency readiness, `TaskRun`s, leases, and fences | FleetDB/Loom |
| Driver orchestration and durable waits | Loom Driver SDK |
| Agent loop and provider transport | Flue or another harness adapter |
| Compute and workspace isolation | Daytona, local, container, or another environment adapter |
| Canonical execution events and identity | Loom execution layer |
| Harness turn recovery | Harness adapter, correlated to Loom identity |
| Tool authorization and external-effect approval | Loom policy/control plane plus bounded harness tools |
| Secret access | Loom-scoped connection/credential broker |
| Checkout-local Git | Loom repository service implemented through environment capabilities |
| Push, PR, review, merge, comments | Trusted Loom delivery/action service |
| Transcript, log, usage, diff, commit, test, and cleanup evidence | Loom artifact/evidence system |
| Schedules and webhooks | Loom trigger bindings |
| Environment cleanup and retention | Loom execution profile enforced by provider adapter |

## Proposed architecture

```text
@loom/sdk/control
  drivers, versions, bindings, profiles, approvals, run operations
                         |
                         v
@loom/sdk/driver
  orchestration, TaskRun creation, children, waits, connectors
                         |
                         v
@loom/sdk/runner
  lease-bound ExecutionContext
      |-- AgentHarnessAdapter
      |     Flue | process CLI | Eve | custom
      |-- EnvironmentAdapter
      |     local | Daytona | container | custom
      |-- RepositoryService
      |-- DeliveryService
      `-- EvidenceSink
```

Runner placement, agent-loop placement, and sandbox placement should be
recorded explicitly:

- **Runner placement:** process owning the lease, heartbeat, TaskRun mutation,
  logs, and artifact upload.
- **Agent-loop placement:** where the harness or external CLI process runs.
- **Sandbox placement:** where the code-editing filesystem and shell run.

Eve and the current Daytona Flue runner place the trusted harness outside the
sandbox and direct shell/filesystem operations remotely. A Codex CLI bootstrap
inside Daytona places the agent loop inside the sandbox. Both topologies should
be representable without overloading one `runtimeProvider` field.

## Proposed SDK surfaces

### `@loom/sdk/control`

**Recommendation:** Complete the missing operator/application client for:

- publish, inspect, activate, and retire driver versions;
- create and manage manual, cron, webhook, GitHub, and internal-event bindings;
- define and resolve execution profiles;
- inspect, cancel, retry, or supersede runs;
- resolve approvals;
- manage connector grants and policies; and
- inspect fleet/environment capacity and retained resources.

Cron belongs here as a durable trigger binding. A missed tick should produce a
visible delivery state and replay identity, not disappear inside an in-process
`cronRunner`.

### `@loom/sdk/driver`

Keep the existing run-scoped orchestration authority. Add only capabilities
that are genuinely orchestration resources, such as cancellation/compensation
of child work or typed effect records. Do not add shell, environment, or broad
task-administration methods.

### `@loom/sdk/runner` execution namespace

An additive `execution` namespace should remain scoped to the client's one
TaskRun lease and fence.

| Primitive | Responsibility |
|---|---|
| `execution.open/attach` | Create or recover the TaskRun's execution envelope with an idempotency key. |
| `execution.heartbeat` | Renew liveness and provider activity while preserving fence checks. |
| `execution.cancel` | Observe/request cancellation and propagate it to agent and environment processes. |
| `execution.finalize` | Idempotently publish terminal evidence and one TaskRun outcome. |
| `execution.environment` | Access the resolved environment handle and negotiated capabilities. |
| `execution.agent` | Invoke a harness/process adapter and stream canonical events. |
| `execution.repository` | Manage the assigned checkout and produce diff/commit evidence. |
| `execution.delivery` | Request policy-checked push/PR/review operations. |
| `execution.approvals` | Request or observe an approval within the allowed effect policy. |
| `execution.children` | Start/await/cancel explicitly authorized harness sub-invocations with lineage. |
| `execution.events/evidence` | Publish canonical events, usage, transcript, logs, and artifacts. |

`execution.children` does not create child `TaskRun`s. New `TaskRun`s remain a
driver-orchestration operation; this runner surface only governs subordinate
agent invocations inside the current fenced attempt.

### Adapter SPI

Provider adapters need shared types and conformance tests but no new Loom
authority. They can live in packages such as:

```text
@loom/sdk/execution-types
@loom/adapter-flue
@loom/adapter-process
@loom/adapter-daytona
```

The exact package names are open. The authority model is not: adapters receive
an already-scoped `ExecutionContext` and cannot mint or widen permissions.

## Execution object model

```text
TaskRun attempt (lease + fence; DriverRun parent optional)
  `-- Execution
        |-- Environment lease
        |-- Repository checkout
        |-- Agent invocation 1..n
        |     `-- model/tool/process turns
        |-- Harness child-invocation lineage
        |-- Approval/effect records
        `-- Event, usage, transcript, artifact, and delivery evidence
```

An immutable server-resolved execution profile should select:

- runner placement and agent-loop placement;
- environment provider, image/template, resources, and region;
- workspace checkout strategy and expected base ref;
- harness adapter and backend/model policy;
- required and optional capabilities;
- network and egress policy;
- secret/connection references;
- repository and delivery policy;
- time, token, cost, depth, and fan-out budgets; and
- success/failure cleanup and retention policy.

Callers may select an authorized named profile. They must not be able to
self-elevate by supplying arbitrary network, secret, trust, or delivery config
inside TaskRun input.

## Environment provider contract

Required portable core:

- provision or attach and persist provider identity before model work;
- health and activity refresh;
- spawn with structured `argv`, cwd, env references, timeout, and cancellation;
- stream stdout/stderr, send input, wait, and kill the process tree;
- bounded text/binary file operations, search, and patch application;
- capability discovery;
- release, delete, or retain according to policy; and
- report cleanup and credential-scrub outcome.

Negotiated optional capabilities:

- PTY and terminal reattachment;
- background process sessions;
- preview/ingress endpoints;
- snapshots and forks;
- persistent/cache volumes;
- GUI/computer use;
- stateful interpreters and LSP; and
- provider-specific metrics.

Do not place every optional method on one nullable universal interface. A
provider should advertise capabilities, and a profile requiring an unavailable
capability should fail preflight before the agent starts.

## Agent-harness contract

First adapters should be:

1. Public Flue harness integration.
2. Generic external process/CLI integration for Codex, Claude, Cursor,
   OpenCode, Gemini, and similar tools.
3. Later remote-agent or Eve integration if a product use case requires it.

The common contract should cover:

- stable invocation identity and parent/root lineage;
- input and optional typed result schema;
- backend/model and adapter version metadata;
- time, token, cost, and tool budgets;
- timeout and process-tree cancellation;
- canonical streaming events;
- transcript, usage, and structured terminal result;
- checkpoint/resume hooks where the harness genuinely supports them; and
- explicit error classification.

Preserve the process prototype's useful error rule: process and model failures
return structured results, while invalid invocation specifications throw
programming errors.

The current Daytona runner's use of `@flue/runtime/internal` should not become a
public contract. V6 should either upgrade Flue and use a public embedding API or
define a small compatibility adapter with contract tests for the pinned build.

## Repository and delivery contract

Git is a required coding-agent capability, but checkout-local Git and
externally visible forge mutation have different authority.

### `execution.repository`

Repository operations inside the assigned environment:

- clone or worktree creation;
- fetch and exact ref/SHA verification;
- base/head identity and dirty-state capture;
- status and changed-file enumeration;
- text and binary diff/patch;
- apply patch with conflict evidence;
- branch, add, and commit;
- local reset/restore only when profile policy permits it; and
- durable output-branch/commit metadata.

### `execution.delivery`

Externally visible operations through trusted, audited code:

- guarded branch push, preferably with expected SHA or force-with-lease;
- pull-request create/update/reparent;
- review, issue comment, and merge;
- freshness preconditions;
- stable idempotency/effect IDs;
- approval and connector-grant enforcement; and
- durable delivery artifacts and provider response metadata.

The current connector SDK lacks pull-request creation, while the Daytona runner
implements it directly. The stack-aware PR publisher already assumes a cleaner
boundary: execution materializes the correct branch, then a separate publisher
reconciles PR state. V6 should converge on that split.

Raw GitHub credentials should not be copied into a sandbox or persisted in a
Git credential store. Use a short-lived capability, host-bound proxy, or trusted
delivery service. A model can choose change content; trusted policy chooses the
repository, destination, credential, and freshness constraint.

## Canonical events and durable evidence

Every execution event should carry:

- monotonic sequence or durable cursor;
- timestamp;
- workspace, TaskRun, and execution IDs;
- DriverRun, invocation, and parent IDs when those resources exist;
- attempt and fence;
- typed event category and payload;
- trace context;
- adapter/provider version;
- redaction state; and
- idempotency/effect identity where applicable.

Initial event categories:

- lifecycle and progress;
- model message/reasoning;
- tool call/result;
- shell/process;
- filesystem/repository;
- usage and budget;
- approval and checkpoint;
- child invocation;
- artifact and delivery;
- warning/error; and
- terminal result.

The execution layer should durably publish:

- transcript and canonical event stream;
- stdout/stderr and relevant log tail;
- token, cost, cache, and duration usage without double-counting harness
  rollups;
- diff, changed files, binary patch, and base/head SHAs;
- gate/test output;
- commit and output branch;
- push result and PR/review metadata;
- preflight and error details;
- runner, harness, and sandbox placement; and
- cleanup, retention, and credential-scrub outcome.

Cloud execution must reject laptop-local or sandbox-local file URIs as final
artifact references. Evidence must remain readable after the environment is
deleted.

## Approvals, effects, and durability

Borrow Eve's durable pause model at Loom authority boundaries:

- request an approval for a typed effect;
- approve, deny, or expire it;
- park without holding model compute when the adapter can checkpoint safely;
- resume from a known checkpoint;
- give every irreversible effect a deterministic ID; and
- never repeat an effect whose previous outcome is unknown.

Do not duplicate Flue's conservative mid-turn recovery. Loom owns durability at
TaskRun, child-run, approval, and external-effect boundaries. The harness owns
turn-level recovery and reports its checkpoint/effect evidence to Loom.

If v6 does not require mid-agent parking initially, gate final delivery and
other external effects after the agent returns. Exact mid-turn resume requires
new TaskRun waiting semantics, durable harness state, environment reattachment,
and careful lease/capacity release; it should not be implied by an SDK method
alone.

## Session terminology

The process-agent prototype used one `AgentSession` per agent-process
invocation. Eve calls a durable multi-turn conversation a session. Flue has
continuing agent instances plus named sessions. These are not automatically the
same Loom resource.

**Recommendation:** Do not silently change existing `AgentSession` semantics.
If Loom needs multi-turn continuity across multiple TaskRuns or invocations,
introduce an explicit parent such as `AgentThread` or `Conversation`.

```text
AgentThread / Conversation (optional durable parent or cross-run link)
  |-- TaskRun attempt 1 -> Execution -> AgentSession / Invocation 1
  `-- TaskRun attempt 2 -> Execution -> AgentSession / Invocation 2
```

An invocation can contain many model turns and tool calls while remaining one
observable agent-process or harness invocation.

## Illustrative API shape

The names below are design input, not a frozen API.

```ts
import { TaskRunClient } from "@loom/sdk/runner";
import { flueAgent } from "@loom/adapter-flue";

const run = TaskRunClient.fromEnv();

const execution = await run.execution.open({
  executionKey: "task-run-attempt",
  profile: "coding-daytona-flue-v1",
});

const repo = await execution.repository.checkout({
  remote: execution.input.repository,
  ref: execution.input.baseRef,
  expectedSha: execution.input.baseSha,
});

const result = await execution.agent.invoke({
  invocationKey: "implement",
  adapter: flueAgent(agentDefinition),
  cwd: repo.path,
  input: { prompt: execution.input.prompt },
});

const change = await repo.captureChange();
await execution.evidence.publish(change);

const delivery = await execution.delivery.publish({
  kind: "pull_request",
  change,
  expectedBaseSha: repo.baseSha,
});

await execution.finalize({ result, delivery });
```

The equivalent external CLI adapter would supply structured `argv` and an
explicit transcript mode. Neither adapter can widen the execution profile or
mutate another TaskRun.

## Recommended coherent v6 slices

### Foundation

- settle the open decisions below;
- define execution, placement, event, effect, and profile schemas;
- decide whether to upgrade Flue or support the current pin first;
- publish adapter conformance tests; and
- define additive compatibility against the frozen SDK v1 surface.

### First usable slice

- lease-bound `runner.execution` envelope;
- generic process-agent adapter;
- public Flue adapter;
- local and Daytona environment adapters;
- canonical events, transcript, usage, and artifact publication;
- repository checkout/diff/commit service;
- trusted push/PR delivery;
- preflight, cancellation, cleanup, and retention; and
- real-protocol conformance tests using the same surface production runners
  use.

### Subsequent work

- durable approval parking and resume;
- child invocations and subagent budgets within one TaskRun;
- connection/secret brokerage;
- persistent environments spanning multiple attempts;
- richer endpoint, GUI, browser, interpreter, and LSP capabilities;
- additional harness/environment adapters; and
- control SDK for full driver/binding/profile lifecycle.

## Explicit non-goals

- One SDK primitive for every Loom CLI command.
- `epicRunner`, `cronRunner`, `gitRunner`, or provider-named task authority.
- Reimplementing Flue tools, skills, model loop, compaction, or provider APIs.
- Wrapping every Daytona administrative operation.
- Making Eve API compatibility a v6 requirement.
- Giving TaskRun code operator/control-plane authority.
- Giving raw organization credentials to agent or sandbox code.
- Treating local CLI Git behavior as the cloud delivery contract.
- Treating a Flue subagent session as a Loom child TaskRun.
- Promising transparent replay of arbitrary TypeScript or unknown external
  effects.

## Open decisions

| Decision | Recommended starting point |
|---|---|
| Is Flue mandatory or one harness adapter? | Runtime-neutral execution contract with a first-party Flue adapter |
| Does v6 target pinned Flue beta.2 or upgrade first? | Upgrade first, then depend only on tested public APIs |
| Where does the agent loop run for Daytona? | Trusted runner/executor outside the sandbox for v1; record placement explicitly |
| What owns an environment? | One TaskRun attempt by default; later profiles may opt into reuse |
| How are durable conversations represented? | Preserve invocation sessions and add an explicit optional thread/conversation parent |
| Who may push or create PRs? | Trusted Loom delivery service; execution-local Git prepares the change |
| Which approvals ship first? | External effects and final delivery before arbitrary mid-turn parking |
| Should secrets ever be readable in the environment? | Prefer non-readable broker/proxy substitution; allow only explicit narrowly scoped exceptions |
| Is GUI/computer-use in the first slice? | No; negotiate it as a later optional environment capability |
| Separate execution authority client or runner namespace? | Additive `runner.execution`, preserving the three-authority SDK model |

## Primary external references

### Flue

- [Pinned Flue source](https://github.com/withastro/flue/tree/492bf47b9f3d6c379d00471523987b8fe9511f7d)
- [Current Agent API](https://flueframework.com/docs/api/agent-api/)
- [Current Workflow API](https://flueframework.com/docs/api/workflow-api/)
- [Durable execution](https://flueframework.com/docs/concepts/durable-execution/)
- [Sandbox API](https://flueframework.com/docs/api/sandbox-api/)
- [Persistence API](https://flueframework.com/docs/api/data-persistence-api/)
- [Events](https://flueframework.com/docs/api/events-reference/)
- [Streaming protocol](https://flueframework.com/docs/api/streaming-protocol/)
- [Schedules](https://flueframework.com/docs/guide/schedules/)
- [SDK overview](https://flueframework.com/docs/sdk/overview/)
- [Upstream beta.9 declaration snapshot](https://unpkg.com/@flue/runtime@1.0.0-beta.9/dist/index.d.mts)

### Daytona

- [TypeScript SDK](https://www.daytona.io/docs/en/typescript-sdk/)
- [Architecture](https://www.daytona.io/docs/en/architecture/)
- [Sandboxes](https://www.daytona.io/docs/en/sandboxes/)
- [Process and code execution](https://www.daytona.io/docs/en/process-code-execution/)
- [PTY](https://www.daytona.io/docs/en/pty/)
- [Filesystem](https://www.daytona.io/docs/en/file-system-operations/)
- [Git operations](https://www.daytona.io/docs/en/git-operations/)
- [Snapshots](https://www.daytona.io/docs/en/snapshots/)
- [Volumes](https://www.daytona.io/docs/en/volumes/)
- [Network limits](https://www.daytona.io/docs/en/network-limits/)
- [Secrets](https://www.daytona.io/docs/en/secrets/)
- [Computer use](https://www.daytona.io/docs/en/computer-use/)

### Vercel Eve

- [Repository at the inspected commit](https://github.com/vercel/eve/tree/d2b57131c7817d61e9d6e4117f665aba20199bab)
- [Project layout](https://github.com/vercel/eve/blob/d2b57131c7817d61e9d6e4117f665aba20199bab/docs/reference/project-layout.md)
- [Default harness](https://github.com/vercel/eve/blob/d2b57131c7817d61e9d6e4117f665aba20199bab/docs/concepts/default-harness.md)
- [Execution model and durability](https://github.com/vercel/eve/blob/d2b57131c7817d61e9d6e4117f665aba20199bab/docs/concepts/execution-model-and-durability.md)
- [Sessions, runs, and streaming](https://github.com/vercel/eve/blob/d2b57131c7817d61e9d6e4117f665aba20199bab/docs/concepts/sessions-runs-and-streaming.md)
- [Sandbox API](https://github.com/vercel/eve/blob/d2b57131c7817d61e9d6e4117f665aba20199bab/docs/sandbox.mdx)
- [Security model](https://github.com/vercel/eve/blob/d2b57131c7817d61e9d6e4117f665aba20199bab/docs/concepts/security-model.md)
- [Connections](https://github.com/vercel/eve/blob/d2b57131c7817d61e9d6e4117f665aba20199bab/docs/connections/overview.mdx)
- [Subagents](https://github.com/vercel/eve/blob/d2b57131c7817d61e9d6e4117f665aba20199bab/docs/subagents.mdx)
- [Evals](https://github.com/vercel/eve/blob/d2b57131c7817d61e9d6e4117f665aba20199bab/docs/evals/overview.mdx)

## Bottom line

Loom needs one provider-neutral, lease-bound execution-agent layer. Flue,
Daytona, Eve, Git, cron, and built-in workflows plug into different parts of
that layer; they are not peer runner primitives.
