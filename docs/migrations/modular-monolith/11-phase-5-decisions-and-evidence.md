# Phase 5 Agents and Interaction: Decisions and Evidence

- **Status:** Source ownership plus the FleetDB and Loom full gates are
  complete; the Interaction sole-writer scan is zero and the graph is at
  `completed_phase: 5`. Twenty local packaged Desktop real-Codex rows are
  accepted, four GitHub rows remain authorization-fenced, and the repaired
  package's failure-path regression is waiting for macOS unlock
- **Date:** 2026-08-01
- **Working base:** Loom `accb1fa60a8a546b8952368d52d5cee85bdf00ee`
- **FleetDB contract commit:** `a2e603b` (`feat: add phase 5 capability contracts`)
- **FleetDB current source head:** `539dd37`
  (`fix(claims): renew worker lease on re-claim`)
- **Loom implementation commit:** `33206b1af`
  (`refactor: extract phase 5 capability modules`)
- **Loom current implementation head:** `b080f316a`
  (`fix(agents): preserve live claims and triage routing`)
- **Last completed source-architecture phase:** 5
- **Migration:** [Modular Monolith Migration](README.md)

This record distinguishes source-level ownership closure from final
exact-head acceptance. The Agents, Interaction, Source Control, and Connectors
roots and the `AgentProvisioning` process manager are present. Production
session, terminal, lease, and inbox mutations now pass through Interaction
commands or owner-private persistence adapters, so the graph is ratcheted to
`completed_phase: 5`. The exact Loom source gate and FleetDB full gate are
green. Packaged Desktop real-backend rows 01-06 and 11-24 are accepted. Rows
07-10 require an actual authorized GitHub repository, and the repaired package
still needs the final fail-closed/deletion/restart UI regression before final
acceptance.

## Implemented Phase 5 seams

| Area | Implemented contract |
|---|---|
| Agents | Twenty default-deny commands own Agent identity, Role references, desired state, aggregate lifecycle convergence, fenced `AgentOwnershipLease` generations, and the six bounded transitional compatibility operations. Identity commands exclude terminal, Git, PR, worktree, trigger, and execution operations. |
| AgentProvisioning | A durable, secret-free intent is recorded before Role, Agent, Binding, and Grant convergence. Deterministic step command IDs and version-CAS progress make external-commit replay restart-safe. `UnusedRolePolicy` retains an independently committed, unreferenced Role rather than deleting it speculatively. |
| Interaction | Session start creates the `AgentSession` and `AgentLease` generation atomically. Patch, heartbeat, finish, terminal, claim, and completion mutations require a fenced `SessionAuthority`; interactive finish terminalizes the exact bound `TerminalSession` in the same FleetDB transaction as the session outcome and lease release. Recovery is a registered runtime component. Every process-local PTY destruction path uses one central default-deny `interaction.force-interrupt` boundary that carries the caller-observed lease ID and fencing token and verifies that exact generation together with the session and terminal stream/tab placement before teardown. A delayed prior-generation teardown therefore cannot adopt a recovered successor. Natural child exit cleans dead local state immediately and queues bounded durable repair; explicit teardown fails closed, and workspace deregistration retains its manager until convergence succeeds or the bounded retry queue is exhausted. A restart force-converges persisted prior-generation IDs before creating a successor, while generic PUT cannot erase canonical tab identity. Interactive children receive only a filtered, ephemeral session envelope and owner API URL. Session and batch execution stores remain distinct, with combination confined to an activity query. |
| Interaction delivery | Operator enqueue remains typed. Non-Interaction production callers receive only an authority-free `InboxEnqueuer`; the registered `serve-interaction-inbox-delivery` component derives a fresh system authority for the exact workspace and enqueue action and never exposes the issuer. |
| Interaction retry | Inbox completion includes the positive claimed `attempt`. A `queued` completion clears only that claim owner/expiry, preserves the attempt, and lets the next claim increment it. Same-attempt replay converges, while an older attempt or lease generation cannot clear a successor claim. |
| Workflow Catalog | Operator authoring always creates an inactive untrusted immutable version. System-managed authoring is a separate action for trusted builtins and optional atomic activation. Both bind source and bundle digests. |
| Automation and Connectors | AgentProvisioning uses system-only exact-definition `ensure-managed-binding` and `ensure-grant` commands. Divergent same-ID state conflicts rather than being adopted. |
| Source Control and Connectors | Source Control validates a contained non-symlink materialization target and asks Connectors to perform a bounded credential-brokered Git read. Credentials do not appear in the public command or result. |
| Workflow distribution | Runtime authoring callers moved to `internal/app/workflowauthoring`; build and static distribution stay under `internal/infra/workflowdistribution`. The retired `internal/workflows` path has an import tombstone and cannot return. |

The mutation ledger contains 102 reviewed command rows: one
`agentprovisioning.*`, twenty `agents.*`, four `artifacts.*`, fifteen
`automation.*`, one mutating `connectors.*`, forty `execution.*`, fifteen
`interaction.*`, one `sourcecontrol.*`, and five `workflowcatalog.*`.
`connectors.execute-git-read` is intentionally not a mutation row: it is a
bounded credential-brokered read; the materialized filesystem change belongs
to `sourcecontrol.materialize-workspace`.

## Interaction sole-writer audit

The target architecture makes Interaction the sole writer of `AgentSession`,
`TerminalSession`, `AgentLease`, and inbox state. Phase 5 also requires session
and batch-run persistence to remain distinct and requires lead and terminal
paths to stop using daemon IPC for these operations.

Current production source has the following semantic mutation counts outside
`internal/modules/interaction` and FleetDB or in-memory persistence adapters:

| Aggregate | Sites | Current production paths | Verdict |
|---|---:|---|---|
| `AgentSession` | 0 | None | Sole writer enforced |
| `AgentLease` | 0 | None | Sole writer enforced |
| Inbox | 0 | None | Sole writer enforced |
| `TerminalSession` | 0 | None | Sole writer enforced |

`TestPhase5InteractionOwnershipBlockerRatchet` scans every non-test Go source
under `internal`, excluding only infrastructure, Interaction owner code, and
the known forwarding tracing decorator. It now requires an empty result and
requires `completed_phase: 5`. The dedicated scan covers `internal/driver`,
`internal/leadcontrol`, `internal/agentinbox`, and
`internal/webui/svcimpl`, which are outside the primary direct-write analyzer
roots.

The migrated paths use three deliberately different placements:

- interactive session children use the least-privileged Interaction HTTP
  client and one-use lease proof for patch, heartbeat, finish, claim, and
  completion;
- serve composition uses typed Interaction commands and the narrow registered
  inbox enqueuer;
- batch workers retain TaskRun and Worker state under Execution and no longer
  mint shadow AgentSession or AgentLease records.

### AgentCommand is a narrower transition

`AgentCommand` is not a blanket exception for session persistence. The target
retains it only for lead, session, and interactive daemon command delivery
until Phase 6. The live WebUI lifecycle create and daemon Ack/Complete paths
fit that transition. Read-only daemon reconciliation and metrics paths do not
violate write ownership, and the command-store tracing layer is a decorator,
not a semantic owner.

`agentdef` no longer creates `AgentCommand` task-spawn records. Batch DriverRun
and TaskRun dispatch cannot use the transitional exception. The completed
11-profile direct-write refresh removes the former stale rows and all
Phase-5-expired Interaction rows.

### `agentdef` compatibility surface

`agentdef` is now an Agents identity/lifecycle command, not a shortcut across
Agents, Execution, Interaction, and Source Control:

| Retired input | Phase 5 migration |
|---|---|
| `add --backend`, `add --task-filter` | Set shared behavior policy with `loom role set ROLE backend VALUE` or `loom role set ROLE task_filter VALUE`. |
| `add --repos`, `add --parent` | Create an Execution profile with `loom worker profile add PROFILE --role ROLE --repo REPO [--parent-epic EPIC]`, then attach its canonical reference with `loom agentdef add NAME --role ROLE --profile PROFILE`. |
| `add --task`, `add --orchestrator` | Dispatch through an Execution workflow. A future one-command launch must be a named durable server workflow, not Agent create followed by TaskRun request in the CLI. |
| `stop --force` | Use the transitional runtime command `loom data agent stop NAME --force`; the Interaction owner resolves exact session and terminal coordinates. |
| `add --repo-groups`, `add --cross-repo` | No WorkerProfile contract exists yet. The CLI fails closed with an unknown flag until parity is deliberately designed. |

Canonical identity flags remain `--role`, `--profile`, `--mode`, `--auto`,
`--max-concurrency`, and `--budget-policy`. `start`, ordinary `stop`, and
`remove` call the single revision-CAS, replay-safe Agents lifecycle command so
desired state, managed bindings, binding grants, and archival converge in one
FleetDB commit. All three commands print a printable generation-bound
`--request-id` before issuing the mutation. Omission creates a fresh token for
the currently observed Agent; an explicit value must be the exact token
printed by a prior attempt, so the CLI cannot silently rebind an old operation
to a same-name replacement. The adapter reuses the exact request for one
bounded ambiguous-response retry, and a later CLI invocation with the same
token replays the original Fleet receipt even after the Agent revision
advances. Every lifecycle request also carries the immutable, server-generated
Agent generation encoded by that token. A receipt miss requires that
generation to match the current Agent atomically; a receipt hit replays only
while the receipt, supplied generation, and current live or archived Agent all
name the same generation. Consequently a delayed command captured before
destructive workspace deletion cannot mutate a same-ID replacement, even if
its timestamps are identical. Destructive workspace deletion invalidates its
receipt domain: PostgreSQL cascades the workspace-scoped receipt rows, while
Redis binds every receipt probe and commit to the current server-stamped
Workspace incarnation so even stale Agent and receipt keys cannot be adopted
after same-key recreation. Receipt replay is therefore guaranteed across
ordinary restart, not across destructive workspace deletion; the generation
and Workspace-incarnation fences still make a captured old command fail
closed. The raw Fleet delete API may omit its generation only to look up an
existing exact receipt after public reads become not-found; the CLI retains
the old generation in its printed token. Deleting an Agent that never existed
still returns not-found.

Physical supervisor deletion remains Phase 6, but direct supervisor writes to
Interaction aggregates are not deferred by that deletion schedule. Supervisor
behavior must be reimplemented through the new owner before the old process is
deleted.

## Architecture and runtime inventory

| Inventory | Phase 5 value | Evidence status |
|---|---:|---|
| Active capability roots | 8 | Agents, Artifacts, Automation, Connectors, Execution, Interaction, Source Control, Workflow Catalog |
| Mutation commands | 102 | Ledger schema, sort, required-ID completeness, and Interaction action-to-ledger parity are enforced |
| Runtime components | 90 | Adds AgentProvisioning and Interaction recovery plus separately scheduled Workspace repository-admission recovery and lease renewal, the bounded local FleetDB recovery wait, and managed-daemon workspace rotation while retiring two synthetic session-heartbeat definitions |
| Managed runtime components | 61 | Phase 5 registrations use the platform runtime host; local daemon workspace rotation is now explicitly inventoried and the retired session heartbeat loops no longer inflate the count |
| Ticker definitions | 54 | Exact AST parity passes, including the two previously unrecorded local recovery tickers |
| Goroutine-launch definitions | 105 | Exact AST parity passes after removing three retired session-heartbeat launches and recording the bounded Phase 5 ownership-analysis worker plus the guarded repository-admission materialization watchdog and renewal worker |
| Measured performance records | 6 | Historical measurements retained; the Phase 5 change updates only the structural background-component inventory |
| Composite Store files / outside composition | 78 / 66 | The exact checked-in ratchet is saturated but has no increase. Interaction chat receives operation closures; its infrastructure adapter does not receive the composite Store. |
| Legacy handler-import exceptions | 87 | Three stale allowances were removed from the exact checked-in ratchet |
| Primary direct-write rows/sites | 260 / 269 | Exact 11-profile refresh; owner-specific FleetDB transport packages expose previously latent type-resolved calls without adding persistence behavior. Includes the owner-private Agents prompt-repair primitive, three remaining `internal/driver` rows with Phase 6 expiry, and no Interaction-owned driver rows |

The runtime inventory is an all-source definition ledger, not a claim that all
build-tag variants run simultaneously. No new Phase 5 latency result is claimed
without a defined workload and measurement artifact. The registered inbox
delivery authority adapter and terminal-lifecycle force-interrupt adapter are
request-scoped, not background loops, so neither adds a runtime component.
Workspace repository admission instead contributes two managed platform-runtime
components so materialization cannot block lease safety. Recovery immediately
reconciles durable FleetDB admission and protected local-journal state after
restart, then repeats every 15 seconds with bounded execution and host-managed
backoff. A separately scheduled renewal pass immediately renews process-active
60-second Fleet leases and repeats every 15 seconds under a 10-second pass
timeout; every renewal binds the exact owner ID, owner generation, spec
fingerprint, and expected version while process-local mutations are serialized.

## Validation ledger

| Check | Result | Evidence |
|---|---|---|
| Mutation ledger load and Phase 5 contract | PASS | Focused ledger, Phase 5 graph, Interaction action parity, delivery-authority, force-interrupt authority, attempt-fencing, and live-claim-renewal tests pass with 102 rows |
| Runtime, performance, retired-workflows, and profile-boundary focused checks | PASS | Exact runtime AST parity, performance/runtime totals, retired-workflows tombstone, profile boundary, cache lifecycle, and checked-snapshot focused tests pass |
| Interaction sole-writer ratchet | PASS | The live scan observes exactly zero direct aggregate mutation sites and the test now requires zero at `completed_phase: 5` |
| Exact 11-profile direct-write refresh | PASS | `go run ./scripts/archcheck snapshot-direct-writes` completed all four release and seven tag/race profiles. The checked inventory exactly matches 260 rows / 269 sites; the additional rows are type-resolution coverage from owner-specific FleetDB transport packages, the Agents-owned prompt-repair primitive is classified at its private persistence adapter, and all three driver rows retain Phase 6 expiry |
| Loom full gate | PASS | At `b080f316a`, `make check-go` passed all 16 Go gates against the paired Fleet source and `make check-frontend` passed all six frontend gates. Architecture peaked at 1215.8 MiB below 2048 MiB; race coverage was 65.6% against 60%. Logs: `/tmp/loom-phase5-check-go-current-rerun.log`, `/tmp/loom-phase5-check-frontend-current.log`. |
| SDK and Desktop static validation | PASS | SDK tests and typecheck passed 72/72; Desktop typecheck and frontend build passed. Packaged runtime acceptance remains separate below |
| Architecture memory, disk, and structural ratchets | PASS | Exact guard: Store 78/78, outside composition 66/66, handler imports 87/87, direct writes 260, module roots 8, mutation commands 102, runtime components 90, goroutine definitions 105, profiles 11/11. The final exact-head run observed 1215.8 MiB peak tree RSS below 2048 MiB. Native-profile analysis reuses the explicit caller cache while cross-target/tag caches remain disposable. |
| Paired FleetDB contract checksum | PASS | FleetDB `api/openapi.yaml` and Loom's vendored `fleetdb-openapi.yaml` both hash to `f4a5726bd78e643d867bbabdc9b9de37ff6b9dbcdfa57f7de10ab90c2a9c4479` |
| FleetDB full container gate | PASS | The exact full gate passed with the `539dd37` same-holder renewal test present: outer race, lint, 80.8% coverage, PostgreSQL contracts, Redis restart/crash recovery, E2E, and harness evaluation all passed. Log: `/tmp/fleet-phase5-gate-current-rerun.log`; the post-edit harness lint also passed. |
| Packaged Desktop preflight | PASS | `Loom Agents.app` is rebuilt and ad-hoc resealed from Loom `b080f316a` with paired FleetDB `539dd37`; all six embedded workflows passed their packaged-builtin gate, the Loom sidecar reports `dev (b080f316a)`, packaged Node is `v24.13.1`, the JIT smoke/entitlement pass, and deep strict signature verification passes. Final UI launch is waiting for macOS unlock. |
| Remote-less local worktree handoff | PASS | `e68159bd6` admits only a same-common-directory local source. `512b97e01` additionally carries the token-free admitted repository source only to the trusted bundled local task runner, so local-branch delivery and Local Review never depend on a mutable named `origin`. Two distinct Coder branches and two Local Review runs completed through the packaged UI. |
| Packaged Desktop real-backend positive, fail-closed, and restart proof | RUNNING (20/24 POSITIVE ROWS; 4 AUTHORIZATION-FENCED) | Rows 01-06 and 11-24 have distinct real Codex transcripts and their required designs, commits, diffs, comments, task links, or normal interactive completion. Rows 23-24 exposed stale worker registration and login-shell CLI drift; Fleet `539dd37` and Loom `b080f316a` repair both plus deterministic Bug-triage handoff. Rows 07-10 still require an actual GitHub target. Fail-closed, active-delete conflict, and repaired-package restart/UI switching await unlock. |

Cross-target and tagged profiles retain a disposable absolute `GOCACHE` that
is removed after each serialized profile. The untagged native profile now
reuses an explicit caller cache already populated by the gate's vet/build/lint
stages. This preserves semantic isolation for every variant while avoiding a
second full native compilation cache. The complete 11-profile matrix and final
full gate pass without ENOSPC; exact inventory also records the local FleetDB
recovery and managed-daemon workspace-rotation tickers.

## Completion criteria

The source-ownership criteria for `completed_phase: 5` are satisfied: the four
Interaction aggregate classes have zero external semantic mutation sites,
batch and interactive persistence are separated, the delivery system authority
is registered and least-privileged, exact-attempt retry fencing is durable, and
the ledger enumerates every mutating Interaction action.

Final acceptance still requires:

1. repaired-package UI regression proving live-claim renewal, deterministic
   Bug routing, fail-closed authority, active-card deletion conflict, agent
   switching/task-panel behavior, and restart persistence; and
2. completion of GitHub rows 07-10 after `<owner/repository>` is replaced by an
   actual authorized test repository; and
3. an immutable append with commit hashes, contract checksum, commands, and
   artifact locations.

---

[Migration index](README.md) · [Target architecture](02-target-architecture.md)
· [Migration plan](03-migration-plan.md) ·
[Enforcement and gates](04-enforcement-and-gates.md)
