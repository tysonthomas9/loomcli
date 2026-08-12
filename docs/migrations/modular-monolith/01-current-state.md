# Current-State Evidence

- **Status:** Proposed migration input
- **Snapshot:** 2026-07-14, validated Loom code head `09f071d0a` after integrating `origin/v5`
- **Migration:** [Modular Monolith Migration](README.md)

## What the measurements say

| Measure | Snapshot value | Interpretation |
|---|---:|---|
| Go packages from `go list ./...` | 163 total; 159 under `internal/...` | Loom is already highly packaged |
| Largest non-generated production package / configured internal fanout ceiling | 25 files / 18 imports | Existing size and fanout gates pass at their ceilings |
| Production files referring to `store.Store` | 95 | The broad dependency is semantic, not a large-package problem |
| `store.Store` references outside `cli/serve`, `infra`, and `store` | 85 | Composite persistence reaches capability code |
| Frontend production TS/TSX files | 602 | The frontend needs ownership boundaries |
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

One handler rule currently targets singular `**/handler/**`, while production code lives under `internal/webui/handlers/**`. Correcting that pattern without a legacy baseline would expose existing violations. Its eventual remediation must direct adapters toward capability public APIs, not permanently bless `internal/webui/service`, which this migration retires as a business-logic bucket.

The frontend ESLint graph in `internal/webui/frontend/eslint.config.js` enforces horizontal folders (`views`, `components`, `hooks`, `stores`, `api`, `types`). Same-layer imports remain broad, type-only imports cross every edge, and tests are outside the graph. `WorkspaceViewContext` spans issue, agent, terminal, navigation, filtering, and review behavior.

## The prior plane model is useful but insufficient

A pre-integration exploratory scan grouped the tree into kernel, platform, state, execution, orchestration, HTTP transport, and composition levels. It reported four upward tree edges and two tree-level cycles. That historical result was never an ownership model or checked-in enforcement source; the post-integration graph inventory remains pending. The four cleanup candidates still exist:

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
| Trigger dispatch | Idempotency, hop depth, and actor filtering live in different places; actor filtering is not enforced |
| Claim/run/finish | Issue state, TaskRun, locks, and fencing cross record families |

Transport-only authorization cannot fix these paths because CLI and internal callers reach the same capabilities. The migration must establish typed authority at every module command boundary and atomic fleet-db commands or recoverable process managers for cross-record writes.

Two fleet-db mechanisms also defeat a simplistic “one table equals one module” split. `ActionLedger` records run-side-effect actions across task status/comments, PR creation/merge, and TaskRun start (`fleet-db/internal/models/platform.go:770-865`). The generic Lease record covers `agent_service`, `driver_run`, `task_run`, `terminal`, and `artifact_upload` (`fleet-db/internal/models/control_plane.go:435-492`). The target must assign ActionLedger coordination and each Lease instance to one owner without inventing a global shared business module.

## Runtime ownership is also distributed

At this snapshot, `serve_loops.go` has six explicit store-backed launchers. The default-on driver-run executor and two default task workers bring the stable `loom serve` automation subset to nine loops, excluding web-server and infrastructure loops. `EnsureBuiltinRolePrompts` and `EnsurePromptAgentIdentityRecords` are boot-only repairs. Daemon and per-agent loops are configuration-dependent, so the old scalar “fourteen long-lived loops” is not retained as a baseline. A named component inventory must define the counting rule before setting a runtime threshold. Retry intervals and policy can be owned by packages or overridden in `serve`; the stale-task package default is twenty minutes while `serve_loops.go` supplies a five-minute fallback.

The target is one lifecycle framework coordinating isolated capability reconcilers—not one giant loop. Supervisor code should be characterized, replaced in its target owners, and deleted rather than moved into a polished legacy module.

## Reproducible snapshot commands

These commands reproduce the recorded measurements at `09f071d0a`:

```sh
GOCACHE=/tmp/go-build-cache go list ./... | wc -l
GOCACHE=/tmp/go-build-cache go list ./internal/... | wc -l
scripts/check-package-size.sh 25
GOCACHE=/tmp/go-build-cache scripts/check-import-fanout.sh 18

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

The eventual architecture tooling should generate capability edges, Store consumers, direct adapter writes, and authority coverage automatically rather than relying on hand-run inventories.

---

[All migrations](../README.md) · [Migration overview](README.md) · Next: [Target architecture](02-target-architecture.md)
