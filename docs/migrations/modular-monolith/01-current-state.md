# Current-State Evidence

- **Status:** Historical Phase 1 through Phase 5 evidence remains immutable;
  Phase 6 is complete at `completed_phase: 6`, including fresh signed
  packaged-Desktop UI and real-Codex planner/coder acceptance
- **Snapshot:** 2026-07-15, pre-guardrail Loom code head `122d4d79` after refreshing `origin/v5`
- **Migration:** [Modular Monolith Migration](README.md)

## What the measurements say

| Measure | Snapshot value | Interpretation |
|---|---:|---|
| Go packages from `go list ./...` | 165 total; 159 under `internal/...` | Loom is already highly packaged |
| Largest non-generated production package / configured internal fanout ceiling | 25 files / 18 imports | Existing size and fanout gates pass at their ceilings |
| Production files using the composite Store interface | 93 semantic uses; the old text scan reports 96 | The broad dependency is semantic, not a large-package problem |
| Composite Store uses outside `cli/serve`, `infra`, and `store` | 82 semantic uses; the old text scan reports 86 | Composite persistence reaches capability code |
| Direct persistence-write rows from declared adapter roots | 233 type-resolved rows | Existing writes are owned and ratcheted, not yet migrated |
| Architecture analysis profiles | 11 of 11 enforced plus all-files AST checks | Build tags and release targets cannot silently hide forbidden edges |
| Frontend production TS/TSX files | 604 | The frontend needs ownership boundaries |
| Frontend top-level component directories | 94 | Component folders do not currently provide those boundaries |
| `internal/webui/frontend/src/App.tsx` | 1,569 lines | The application shell directly composes many capabilities |

Package size and raw fanout are therefore supporting maintainability signals, not migration success criteria. Both can improve while mutation ownership remains global.

## Broad dependency centers

`store.Store` exposes approximately thirty repository/capability accessors from `internal/store/store.go`. It is passed through handlers, CLI packages, workflows, trigger code, driver execution, and lead control. New capability code that still receives this interface has not been modularized.

`internal/domain/platform.go` co-locates records with independent lifecycles: drivers and versions, agent services, trigger bindings, driver runs/steps, and task runs. A global domain package makes ownership invisible even when imports point downward.

The current `internal/webui/service.AgentService` is not a future module seam. It combines identity CRUD, desired-state operations, terminal tokens, logs, diffs, Git pull/push/reset, PR creation, and worktree operations. “Agent-scoped” is not equivalent to one responsibility.

Several HTTP and CLI paths independently implement business workflows:

- prompt-agent provisioning writes Role, AgentService, TriggerBinding, and grants through `createPromptAgent`, `createPromptAgentBinding`, and `provisionPromptAgentGrants`;
- workflow registration/approval is orchestrated by HTTP functions such as `approveWorkflowVersion`, `createWorkflowVersion`, and `workflowVersionAction`, and separately by CLI functions such as `runWorkflowBuild` and the CLI `workflowVersionAction`;
- trigger validation/defaulting and run-now behavior appear in `createBinding`, `runBinding`, `routerBindingFlags.validateForCreate`, and `runBindingsCreate`;
- persistence derives trigger routing data in `TriggerBindingCreate.WithDerivedRoute` and `DefaultBindingID`.

This spreads one use case across transport, services, domain records, persistence, and fleet-db adapters.

## Existing enforcement is horizontal

The `.golangci.yml` `depguard.rules` entries, including `handler-layer-isolation`, `sdk-leaf`, and the technical-layer rules below them, establish a useful horizontal DAG and special restrictions for handlers and `cli/data`. They do not establish capability ownership for the large middle tier: `domain`, `store`, `infra`, `driver`, `trigger`, `workflows`, `connector`, `leadcontrol`, `sessions`, and related packages.

The pre-guardrail handler rule targeted singular `**/handler/**`, while production code lives under `internal/webui/handlers/**`. The Phase 1 bootstrap corrects the plural path, keeps zero-baseline imports in depguard, and checks the existing backend, Store, daemon, and `webui/store` imports against a per-file machine baseline. Remediation must remove those exceptions and direct adapters toward capability public APIs, not permanently bless `internal/webui/service`, which this migration retires as a business-logic bucket.

The frontend ESLint graph in `internal/webui/frontend/eslint.config.js` enforces horizontal folders (`views`, `components`, `hooks`, `stores`, `api`, `types`). Same-layer imports remain broad, type-only imports cross every edge, and tests are outside the graph. `WorkspaceViewContext` spans issue, agent, terminal, navigation, filtering, and review behavior.

## The prior plane model is useful but insufficient

A pre-integration exploratory scan grouped the tree into kernel, platform, state, execution, orchestration, HTTP transport, and composition levels. It reported four upward tree edges and two tree-level cycles. That historical result was never an ownership model or checked-in enforcement source. Phase 1 replaced it as the authority source with the approved capability graph, 11 enforced build profiles, all-files AST checks, and default-deny validation for new module roots and edges. The four legacy cleanup candidates still exist:

| Edge | Likely mechanical correction |
|---|---|
| `bootstrap → webui/localredis` | Move the generic embedded-Redis helper below transport |
| `runtimepreflight → cli/backends` | Lift the backend registry out of CLI composition |
| `stackstore → bootstrap` | Extract path resolution from bootstrap |
| `infra/memstore → trigger` | Stop persistence importing trigger-engine implementation |

The result does not prove modularity. Global domain, Store, orchestration, service, and transport layers can satisfy a perfect downward import graph while retaining ambiguous aggregate ownership and unsafe partial writes. Horizontal layering remains a rule inside each capability; it is not the top-level migration unit.

## Transaction and authority leaks

| Current operation | Risk exposed by the current structure |
|---|---|
| Create prompt agent | Sequential Role → AgentService → Binding → grant writes use best-effort compensation and can leave orphans |
| Enable/pause or change role | Agent desired state and attached binding configuration update separately |
| Delete binding/agent | Binding/grant ordering can leave authority drift after partial failure |
| Approve workflow | Driver metadata is read/replaced without an operator check or explicit revision/CAS |
| Trigger dispatch | Idempotency and hop depth still span layers; `internal.issue.*` binding create/update now requires workflow-actor exclusion and legacy unsafe bindings fail closed at dispatch |
| Claim/run/finish | Issue state, TaskRun, locks, and fencing cross record families |

Transport-only authorization cannot fix these paths because CLI and internal callers reach the same capabilities. The migration must establish typed authority at every module command boundary and atomic fleet-db commands or recoverable process managers for cross-record writes.

The current Phase 4 hardening closes three concrete failure modes without
claiming the wider migration is complete. A healthy DriverRun now performs
exact-parent-owner-fenced stale-child TaskRun recovery and drains that loop
before parent finalization. First-class Repo lifecycle commands commit the Repo,
audit event, and workspace admission mapping atomically and reject updates or
deletes that would orphan Issue or TaskRun placement, including deletion of a
sole implicit fallback while repo-less work is ready. Internal issue-event
bindings must exclude workflow-origin actors, so a workflow cannot retrigger
itself through its own Issue mutation.

Two fleet-db mechanisms also defeat a simplistic “one table equals one module” split. `ActionLedger` records run-side-effect actions across task status/comments, PR creation/merge, and TaskRun start (`fleet-db/internal/models/platform.go:770-865`). The generic Lease record covers `agent_service`, `driver_run`, `task_run`, `terminal`, and `artifact_upload` (`fleet-db/internal/models/control_plane.go:435-492`). The target must assign ActionLedger coordination and each Lease instance to one owner without inventing a global shared business module.

## Runtime ownership is also distributed

The Phase 1 runtime inventory replaced the old scalar loop estimates. That historical snapshot named 83 lifecycle component definitions across `cmd`, `internal`, and `sdk`: 58 exact `time.NewTicker` AST sites plus 25 additional boot or long-lived components. A separate default-deny AST ledger ratcheted all 108 in-scope non-test source goroutine launch definitions. Phase 3 named 85 components, 56 ticker sites, 58 managed components, and 107 goroutine launch definitions. The final Phase 4 tree names 86 components, 53 ticker sites, 58 managed components, four foreground command-poll components, and 103 goroutine launch definitions. Each launch still records its callee and either resolves to lifecycle-component IDs or carries an explicit bounded, request, command, or helper disposition with a reason. These are source-definition ratchets, not counts of simultaneously running product loops.

Phase 1 also removed the stale-task policy mismatch: `serve` now sources the same twenty-minute default as `StaleTaskSweeper`. The sweeper uses the earlier of a monotonic projection and the current wall clock: forward jumps cannot mass-age persisted heartbeats, while a backward jump conservatively protects fresh post-jump heartbeats and recovery resumes as the new clock advances. Standalone lead sessions, serve-hosted task sessions, and supervisor-owned AgentSession/AgentLease records have explicit heartbeat coverage; terminal finalization drains in-flight session heartbeats before writing completion.

The target is one lifecycle framework coordinating isolated capability reconcilers—not one giant loop. Supervisor code should be characterized, replaced in its target owners, and deleted rather than moved into a polished legacy module.

## Reproducible snapshot commands

These commands reproduce the pre-guardrail measurements at `122d4d79`:

```sh
GOCACHE=/tmp/go-build-cache go list ./... | wc -l
GOCACHE=/tmp/go-build-cache go list ./internal/... | wc -l
scripts/check-package-size.sh 25
GOCACHE=/tmp/go-build-cache scripts/check-import-fanout.sh 18
GOCACHE=/tmp/go-build-cache make check-architecture

# Historical exact-spelling diagnostic; the architecture guard is the semantic ratchet.
rg -l 'store\.Store' internal -g '*.go' -g '!*_test.go' | wc -l
rg -l 'store\.Store' internal -g '*.go' -g '!*_test.go' \
  | rg -v '^internal/(cli/serve|infra|store)/' | wc -l

find internal/webui/frontend/src -type f \
  \( -name '*.ts' -o -name '*.tsx' \) \
  ! -name '*.test.ts' ! -name '*.test.tsx' \
  ! -name '*.spec.ts' ! -name '*.spec.tsx' | wc -l
find internal/webui/frontend/src/components \
  -mindepth 1 -maxdepth 1 -type d | wc -l
wc -l internal/webui/frontend/src/App.tsx

git rev-list --left-right --count HEAD...origin/v5
git rev-parse HEAD origin/v5
git merge-base --is-ancestor origin/v5 HEAD
shasum -a 256 internal/infra/fleetdb/testdata/fleetdb-openapi.yaml
```

From the companion fleet-db checkout:

```sh
git rev-list --left-right --count HEAD...origin/main
git rev-parse HEAD origin/main
git merge-base --is-ancestor origin/main HEAD
shasum -a 256 api/openapi.yaml
```

The Phase 1 architecture tooling generated and continues to validate capability edges, Store consumers, direct adapter writes, runtime components, performance records, and authority/transaction specifications. The frozen pre-extraction values remain zero capability module roots, 93 composite-Store files, 82 outside composition, and 233 direct-write rows. The Phase 3 pre-commit base-plus-diff snapshot has two active module roots, 88 composite-Store files, 77 outside composition, and 226 direct-write rows across 249 call sites. The immutable Phase 4 validation check has four active roots, 82 composite-Store files, 71 outside composition, 90 legacy handler-import exceptions, and 243 primary direct-write rows across 265 call sites. The current exact-head reliability-hardening architecture check retains the same four roots, Store `82/71`, and 90 handler exceptions while ratcheting 251 primary direct-write rows across 273 sites, 60 command namespaces, 86 runtime components, and 103 goroutine definitions across all 11 profiles plus the all-files AST pass. Phase 4 separately froze ten mixed `internal/driver` rows across 11 sites. Phase 5 activates eight capability roots, records 102 mutation commands and 90 runtime components, including 61 managed components after adding separately scheduled Workspace repository-admission restart recovery and lease renewal, and ratchets the dedicated semantic scan to zero direct Interaction aggregate mutations outside owner adapters. Its completed structural refresh produces Store `78/66` and 87 handler exceptions. The remaining composition-edge Store adapters are explicit transitional bridges; owner modules receive narrow operations instead of the aggregate. The completed 11-profile write refresh records 260 primary rows across 269 sites after classifying the Agents-owned prompt-repair primitive at its private persistence adapter and moving FleetDB transport interfaces into owner-specific packages, which exposes previously latent type-resolved calls without adding persistence behavior. The three remaining non-Interaction `internal/driver` rows are included directly in the primary inventory with their Phase 6 expiry preserved. Retiring the synthetic batch session paths also removes two runtime-component definitions, one ticker, and three goroutine launches, leaving 54 tickers; recording the bounded Phase 5 ownership-analysis worker plus the guarded repository-admission materialization watchdog and renewal worker keeps the exact all-source launch ledger at 105. The historical table above is therefore not presented as current source state. Immutable Phase 2 and Phase 3 snapshots preserve their provenance; the retained Phase 4 pre-commit snapshot remains explicitly provisional and `phase4-execution-validation-53cbe2577` remains immutable evidence for its exact source. The appended `phase4-reliability-validation-67c45972f` record binds its named packaged Desktop, Podman, raw-browser, focused-source, FleetDB-gate, paired-contract, exact-head architecture, and aggregate Loom gate proof.

Phase 6 is the current source state. It retains eight active capability roots
while reducing Store use to `67/57`, handler exceptions to 82, direct-write
rows to 225, runtime components to 71, and goroutine launches to 80. The
complete mutation ledger has 105 commands. All 11 profiles, the all-files AST
pass, the seven-row no-skip parity matrix, both full gates, and the fresh
supervisor-disabled stack pass at Loom `02daec339` with FleetDB `51b8a493`.
The paired OpenAPI checksum is `816b0b0c…7ef0`. The exact signed package also
completed UI-created real-Codex task `PHASE5-REPAIRED-20260801-23` with a
persisted plan, readable planner and coder transcripts, exit-0 local-branch
delivery, an exact one-file diff, and Review convergence. See the
[Phase 6 evidence](13-phase-6-decisions-and-evidence.md).

---

[All migrations](../README.md) · [Migration overview](README.md) · Next: [Target architecture](02-target-architecture.md) · [Phase 6 evidence](13-phase-6-decisions-and-evidence.md)
