# Phase 0 Integration Baseline

- **Status:** Complete — base integration, refresh, and Phase 0 inventory closure are recorded
- **Original validation:** 2026-07-14
- **Original validated Loom code head:** `09f071d0af6c3493eff724f32dab656c05f10cdf`
- **Original validated FleetDB head:** `7f7104b9441e81976da6d88bb32496f92c5aab38`
- **Phase 1 source refresh:** 2026-07-15 at Loom `122d4d79cc12c6a14116429e6c9ce04483c24198` and FleetDB `8120c788ccc78477a61cfba591fe0445c580ab77`
- **Migration status:** Reviewed; MM-1 through MM-7 were approved in Phase 1

This record freezes the first executable migration step: integrate the live base branches, reconcile the cross-repository contract, prove the current application, and establish the source snapshot for the inventories completed in Phase 1. Documentation commits after the validated Loom code head do not change the measured product tree.

## Phase 1 source refresh

The behavior-neutral Phase 1 guardrail branch starts from Loom `122d4d79`, whose parents include the prior `unified-agents` head and current `origin/v5` `95e97289`. That source is 45 commits ahead and 0 behind `origin/v5`. Companion FleetDB `8120c788` is 41 commits ahead and 0 behind `origin/main`; both ancestry checks pass. The refreshed FleetDB contract and Loom vendored snapshot match at SHA-256 `81ecb22b61bdb8fe15d5828f7695b9112ebe58fc825026dc644d22dd9a8c8e11`.

The pre-guardrail source measures 165 Go packages, including 159 under `internal`. The original text scan finds 96 files containing the spelling `store.Store`; the AST guard resolves two aliased composite-Store imports and excludes five lookalikes such as `stackstore.Store` and `eventstore.Store`, producing a semantic ratchet of 93 files. Of those, 82 are outside `cli/serve`, `infra`, and `store`. The frontend has 604 production TS/TSX files, 94 top-level component directories, and a 1,569-line `App.tsx`. Package size and import fanout still pass at 25 and 18. The checked-in machine baseline records these values as characterization and coupling ratchets; package and frontend file counts remain diagnostic only.

The refresh passed focused and race-tested supervisor packages, frontend unit/build checks, the deterministic task-quarantine playground scenario, `make test-fleetdb-supervisor`, and the full repository gate. It extends the immutable evidence below. MM-1 through MM-7 were later approved by the Phase 1 review, but that approval does not itself authorize an unproved package move.

## Immutable revisions

| Repository state | Revision | Evidence |
|---|---|---|
| Loom branch before integration | `13fc88e5a1af951eaaec0868a559b788969a35a5` | Historical `unified-agents` head; 40 ahead and 25 behind the fetched `v5` |
| Loom base integrated | `673f44cfec81006014e7b853dcb1a87eea9b2383` | Fetched `origin/v5` |
| Loom merge commit | `5ac943c97ca82fa2bf8a3218287c28fe865b5f6f` | Parents are the two revisions above |
| Loom compatibility fix | `09f071d0af6c3493eff724f32dab656c05f10cdf` | Final validated code head; pushed to `origin/unified-agents` for PR #192 |
| FleetDB branch before integration | `c157127e9baafd7d6d44c83e68ed64ab8fec8194` | Historical `unified-agents` head; 35 ahead and 8 behind the fetched `main` |
| FleetDB base integrated | `2a336c0b1e228ceba849a3b0d1a71c6ec4a75ccc` | Fetched `origin/main` |
| FleetDB merge and pushed head | `7f7104b9441e81976da6d88bb32496f92c5aab38` | Parents include the two FleetDB revisions above; local and remote branch heads match |

At validation time, the Loom code head was 42 commits ahead and 0 behind `origin/v5`; the FleetDB head was 36 ahead and 0 behind `origin/main`. Both ancestry checks passed. The FleetDB branch has no open PR for its new merge commit; the historical companion PR is already merged.

## Contract reconciliation

The final FleetDB source contract and Loom vendored snapshot are byte-identical:

```text
d568a05e2f101bb5fafd65e500ea69f6bd4c70bcabe3430918701e946c12061a  fleet-db/api/openapi.yaml
d568a05e2f101bb5fafd65e500ea69f6bd4c70bcabe3430918701e946c12061a  loomcli/internal/infra/fleetdb/testdata/fleetdb-openapi.yaml
```

The integration also exposed a duplicate FleetDB OpenAPI path for `/api/v1/{workspace}/driver-runs/{run_id}/events`. The older, narrower entry was removed, the richer route retained, and `TestOpenAPIYAMLHasUniqueKeys` was added after first proving the duplicate as a failing test. Loom's contract guard passes against the corrected snapshot.

## Conflict ledger

### FleetDB

| Conflict | Semantic resolution |
|---|---|
| `internal/storage/postgres/migrate/migrate_test.go` | Preserve the unified-agent migration coverage and the base branch's migration 040 coverage. |

### Loom

| Conflict group | Files | Semantic resolution |
|---|---|---|
| Role model | `internal/domain/role.go` | Keep built-in role guards while integrating `RoleKind` and inline prompt fields. |
| Server composition | `internal/webui/app/server_modules.go` | Keep the shared connector dispatcher and compose the PR-review module, invalidator, and fallback paths from `v5`. |
| Agent API and UI | `internal/webui/handlers/agents/module.go`; `internal/webui/frontend/src/api/workspace/workspace.ts`; `internal/webui/frontend/src/hooks/agents/index.ts`; `internal/webui/frontend/src/components/CreateAgentModal/CreateAgentModal.tsx`; `internal/webui/frontend/src/components/CreateAgentModal/CreateAgentModal.test.tsx`; `internal/webui/frontend/src/components/CreateAgentModal/__tests__/CreateAgentModal.test.tsx`; `internal/webui/frontend/src/views/AgentsPage.tsx`; `internal/webui/frontend/src/views/__tests__/AgentsPage.test.tsx` | Preserve durable agent-record routes and behavior templates while adding interactive prompts. Constrain supervised onboarding/PR-review flows, exclude interactive roles from the background gallery, and keep custom interactive agents out of the file-prompt editor. |
| Sessions and PR review | `internal/webui/frontend/src/components/IssueDetailPanel/sessions/SessionDetailView.tsx`; `internal/webui/frontend/src/views/PRReviewWorkspace.tsx` | Preserve session Markdown rendering and the `v5` PR-review workspace/conversation behavior. Reviewer selection accepts worker roles and rejects interactive roles. |
| Workflow catalog | `internal/workflows/workflows.go` | Preserve self-healing/full driver resolution together with digest and subset-manifest behavior. |
| Smoke image identity | `smoke-test/smoke-test-slack-epic-runner-stack.sh` | Keep canonical image fingerprinting and environment preservation. |
| Local-mode composition | `test/local-mode/docker-compose.yml` | Keep durable artifacts and the explicit deterministic backend wiring. |

The combined runtime then exposed a latent plane-boundary defect: shared `plan` and `task` role records carry prompt files for the TypeScript prompt-agent plane, while the Go daemon deliberately rejects explicit prompt overrides on built-in roles. Commit `09f071d0a` now omits those workflow-only prompt files only when projecting shared store roles into daemon configuration. The stored role remains unchanged, custom-role prompt files remain available, and the supervisor's explicit-config rejection test remains intact.

## Validation ledger

| Target | Command or surface | Result | Duration/evidence |
|---|---|---|---|
| FleetDB full gate | `make gate` | Pass, including Postgres/API tests, full E2E, coverage, and harness evaluation | Full E2E: 110.674 s; total gate time was not captured by the target. Coverage: 80.6%; all 28 checked packages exceeded 50%. |
| FleetDB OpenAPI uniqueness | `go test ./internal/api -run TestOpenAPIYAMLHasUniqueKeys -count=1` | Pass | Confirms the corrected YAML contains no duplicate mapping keys. |
| Loom FleetDB contract guard | `go test ./internal/infra/fleetdb -run 'TestFleetDBSpecSnapshotFresh\|Test.*Contract' -count=1` | Pass | Confirms snapshot freshness and handwritten-client route coverage. |
| Loom projection regression | `go test ./internal/cli/config -run TestLoadDaemonConfigFromStoreOmitsBuiltinWorkerPromptFiles -count=1` | Red before the fix; green after it | Focused package run: 0.373 s after the fix. |
| Loom daemon/config sweep | `go test ./internal/cli/config ./internal/cli/daemon/... ./internal/cli/serve/workspacemgr -count=1` | Pass with normal host permissions | 18.1 s. The restricted-sandbox attempt failed only where Unix sockets and process groups were prohibited. |
| FleetDB supervisor path | `make test-fleetdb-supervisor` in a clean Loom environment | Pass | 17.6 s including dependency-cache population in a fresh temporary home. |
| Frontend merge surface | Production build, lint, and five focused Vitest files | Pass | 175 tests passed; lint reported 0 errors and 26 pre-existing warnings. Exact focused-test shell transcript was not retained. |
| Loom full gate | Clean-environment command below | Pass: Go, frontend, and aggregate quality gates | Approximately 188 s observed by the command runner. |
| Local-mode runtime | `LOCAL_MODE_API_URL=http://127.0.0.1:8482 make local-mode-verify` | Pass | 4.4 s; planner design/review, coder close/commit/diff, completed sessions, and transcript entries all verified. |
| Browser surface | Kanban and Create Agent modal at `http://127.0.0.1:8483/ws/LOCALMODE/kanban` | Pass | Accessibility snapshot showed both agents, the epic board, planner in Review, coder in Done, and behavior plus interactive agent choices. Local screenshot: `/tmp/loom-agent-browser/v5-merge/create-agent.png`. |
| Teardown | Named `make local-mode-down` invocation | Pass | Containers, network, and named volumes removed. |

The final Loom gate used an isolated home and removed ambient Loom runtime variables:

```sh
env -u LOOM_WORKSPACE -u LOOM_WORKSPACE_RUNTIME_DIR \
  -u LOOM_AGENT_NAME -u LOOM_AGENT_ROLE -u LOOM_AGENT_TERMINAL_ID \
  -u LOOM_SESSION_ID -u LOOM_NOTIFY_TOKEN -u LOOM_CONFIG_DIR \
  -u LOOM_DESKTOP_DATA_DIR -u LOOM_FRONTEND_DIR -u LOOM_WEBUI_URL \
  -u LOOM_LOCAL_RUNTIME HOME=/tmp/loom-v5-merge-gate-home \
  GOCACHE=/tmp/go-build-cache make gate
```

## Local-mode proof coordinates

| Dimension | Value |
|---|---|
| Depth | End to end |
| Realness | Deterministic orchestration with `localdogfood`; no paid AI service |
| Provisioning | Podman Compose |
| Polarity | Positive path |
| Target | Loom `09f071d0a` plus FleetDB `7f7104b9` |
| Compose project | `loomcli-v5-merge` |
| Ports | FleetDB `8480`, Loom API `8482`, UI `8483` |
| Built images | Loom `ecaca0fefd29`; FleetDB `11245b906eba` |
| Browser route | `/ws/LOCALMODE/kanban`, followed by the Create Agent modal |

This proves the current supervisor-backed product path. It does not prove supervisor-disabled operation.

## Re-baselined structural measurements

| Measure | Value at Loom `09f071d0a` |
|---|---:|
| Go packages | 163 |
| Internal Go packages | 159 |
| Production files referring to `store.Store` | 95 |
| Those references outside `cli/serve`, `infra`, and `store` | 85 |
| Frontend production TS/TSX files | 602 |
| Top-level component directories | 94 |
| `App.tsx` lines | 1,569 |
| Largest non-generated production package | 25 files |
| Maximum internal import fanout / configured ceiling | 18 / 18 |

Current `check:dir-size` violations are `src/utils` (29), `src/hooks/workspace` (23), `src/components/FileExplorer` (22), `src/components/AgentDetailPanel` (18), and `src/hooks/ui` (17).

## Phase 0 inventory closure and known gaps

Phase 1 closed the remaining Phase 0 inventory work with machine-checked artifacts:

- `migration-baseline.json` binds the refreshed Loom, FleetDB, and OpenAPI revisions to exact gate/ancestry/contract commands, declared environment set/unset inputs, expected skips, pass results, and evidence paths;
- `capability-graph.yaml` is approved, carries the ten owners and MM-1 through MM-7 dependencies, and default-denies undeclared module roots and edges;
- `analysis-matrix.yaml` enforces four release targets, seven tag/race profiles, and an all-files AST pass;
- `direct-writes.yaml` type-resolves and ratchets 233 adapter write rows across declared persistence receiver and package-function surfaces, while the generic Lease and ActionLedger direct-use baselines remain zero;
- `mutation-ledger.yaml` records the three reviewed Phase 2B Workflow Catalog command specifications without claiming their implementation;
- `runtime-components.yaml` names 83 lifecycle definitions (56 managed, 3 bounded command polls, 15 request-scoped, and 9 startup waits) and default-deny ratchets all 108 in-scope non-test source goroutine launch definitions with their callees and reviewed component links or bounded dispositions; and
- `performance-baseline.yaml` records four measured metrics and keeps workflow-approval latency and FleetDB round trips `not-yet-migrated` with null values and reproducible future procedures.

`test/modular-monolith/supervisor-disabled-matrix.yaml` and `make test-supervisor-disabled` now exist. The stable `deterministic-plan-coder` row is intentionally **RED**, owned by `execution-reliability-lane`, and exits nonzero without provisioning. This is a productized failure contract, not supervisor-disabled proof.

The five frontend directory-size violations still need a ratcheted baseline or remediation before `check:dir-size` joins `check:arch`. FleetDB's `design_format` contract still lacks a dedicated real-Postgres create/get/update/get integration proof. Refreshed FleetDB head `8120c788` is pushed, while branch publication/PR creation remains separate operational work. The browser screenshot remains local evidence rather than a committed artifact.

Phase 0 is complete because every required inventory is now measured or, for metrics that depend on Phase 2 behavior, explicitly recorded as not yet migrated with an owner and acceptance procedure. No capability extraction or supervisor retirement is implied.

---

[Migration overview](README.md) · Next: [Current-state evidence](01-current-state.md) · [Phase 1 evidence](06-phase-1-decisions-and-evidence.md)
