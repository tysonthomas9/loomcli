# Phase 1 Decisions and Evidence

- **Status:** Complete
- **Date:** 2026-07-15
- **Phase 0 source:** Loom `122d4d79cc12c6a14116429e6c9ce04483c24198`; FleetDB `8120c788ccc78477a61cfba591fe0445c580ab77`
- **Phase 1 implementation source:** Loom `9a5b30516f88e74d315cf54c7ed693cc93f8e5b5`
- **Phase 1 measurement source:** Loom `b64b5256ba206d482037a5dee2703fdecbb3c497`
- **Scope:** Architecture decisions, enforceable guardrails, characterization, inventory closure, and bounded execution-reliability fixes

## Milestone decision

Phase 1 is complete. It approves the target architecture and makes the pre-extraction state reproducible; it does not claim a capability extraction, the Workflow Catalog pilot, or supervisor-disabled parity. The architecture report still shows zero capability module roots, 93 composite-Store files with 82 outside composition, and 233 direct persistence-write rows. All ten capabilities in the approved graph remain `planned`.

## MM-1 through MM-7

| ID | Approved outcome |
|---|---|
| MM-1 | Adopt Workspace, Work Items, Agents, Workflow Catalog, Automation, Execution, Interaction, Source Control, Connectors, and Artifacts as the ten coarse owners, including the documented Workspace/Source Control split. |
| MM-2 | A server issues the 256-bit local operator credential and stores it in a mode-0600 runtime file. Loopback location alone grants no authority. |
| MM-3 | Advertise a capability key only when the active Redis or Postgres deployment can execute it with backend parity. Missing support fails readiness without fallback. |
| MM-4 | Use Workflow Catalog approve, unapprove, and activate as the first complete backend pilot after a behavior-neutral read seam. |
| MM-5 | Put public capability roots at `internal/modules/<capability>`; optional subpackages remain owner-private. |
| MM-6 | Remove a compatibility facade within two migration waves unless a reviewed extension records an owner. |
| MM-7 | Mutating CLIs discover an explicitly configured management endpoint, never start a host implicitly, fail closed when unavailable, authenticate locally, and migrate family by family with output and exit-code compatibility. |

The canonical decision records are `internal/archtest/testdata/capability-graph.yaml` and the `decisions` section of `internal/archtest/testdata/migration-baseline.json`. The graph is approved; the capabilities themselves remain planned.

## Checked-in evidence

| Artifact | Phase 1 evidence |
|---|---|
| `internal/archtest/testdata/capability-graph.yaml` | Approved ten-capability ownership, allowed edges, machine-enforced `completed_phase: 1` legacy/debt expiries, app/platform and external-import restrictions, and bounded durable-event metadata. |
| `internal/archtest/testdata/analysis-matrix.yaml` | Four release targets and seven tag/race profiles enforced, plus tests-inclusive non-generated all-files AST analysis. |
| `internal/archtest/testdata/migration-baseline.json` | Analyzer `1.0.0`, structural/Store/handler ratchets, all required inventories complete, zero pending architecture decisions, and a source-bound hard-gate snapshot with exact argv, environment set/unset inputs, expected skips, results, and evidence paths. |
| `internal/archtest/testdata/direct-writes.yaml` | Complete type-resolved baseline of 233 write rows under the declared app/CLI/module/HTTP adapter roots, including Store, session, stack, usage, and tab repositories plus package-level persistence helpers; method/function values are counted and undeclared persistence surfaces fail closed. Generic Lease and ActionLedger direct-use baselines are zero. |
| `internal/archtest/testdata/mutation-ledger.yaml` | Three reviewed Phase 2B specifications: Workflow Catalog approve, unapprove, and activate. These rows define future authority, atomicity, retry, and fault tests; they do not assert implementation. |
| `internal/archtest/testdata/runtime-components.yaml` | Exact in-scope non-test source ticker and goroutine-definition parity, default-deny launch dispositions with resolving component links or bounded reasons, plus file/function verification for each named non-ticker runtime definition, including owner, cadence, cancellation, readiness, retry, and lifecycle class. The all-source scan includes architecture tooling, mutually exclusive platform and `testbackend` variants, and the fake harness. |
| `internal/archtest/testdata/performance-baseline.yaml` | Six strictly validated performance/operability records: four measured and two explicitly not yet migrated. |
| `test/modular-monolith/characterization-matrix.yaml` and `test/modular-monolith/characterization/` | Five pinned behavior rows covering workflow approval, trigger admission, agent provisioning, execution recovery, and supervisor restart policy. |
| `test/modular-monolith/supervisor-disabled-matrix.yaml` and `scripts/supervisordisabled/` | Strict executable contract with argv/timeouts/teardown/assertions and one intentionally RED, owned execution row. |

The reliability lane also aligns the stale-task default in `internal/driver/stale_task_sweeper.go` and `internal/cli/serve/serve_runtime_policy.go`, bounds the recovery cutoff by both a monotonic projection and the live wall clock, covers standalone lead and serve-hosted task session heartbeats, adds the supervisor AgentSession/AgentLease heartbeat, serializes full-record heartbeats against starting-to-running and terminal lifecycle transitions, makes heartbeat barrier waits deadline-aware, and implements caller-declared startup checks in `internal/infra/fleetdb/capabilities.go`, `internal/bootstrap/openstore.go`, and `internal/cli/cmdstore/cmdstore.go`.

## Performance and operability baseline

| Record | Status | Result |
|---|---|---|
| `loom serve` startup/readiness | Measured | 10 isolated samples; startup p50 `43.349 ms`, p95 `108.486 ms`; readiness p50 `141.699 ms`, p95 `209.558 ms`. |
| Workflow-approval latency | **Not yet migrated** | Sample count `0`; p50 `null`; p95 `null`. Measure only after the authenticated Phase 2 management path exists. |
| FleetDB round trips per workflow approval | **Not yet migrated** | Round trips per command `null`. Do not infer this from legacy direct-Store call sites. |
| Production background components | Measured | 83 named lifecycle definitions, including 58 ticker sites: 56 managed, 3 command-poll, 15 request-scoped, and 9 startup-wait. A separate exact ledger covers all 108 in-scope non-test source goroutine launch definitions, their callees, and their reviewed component links or bounded dispositions. The background count is the 56 managed definitions, not simultaneous instances. |
| Full build gate | Measured historical baseline | Approximately `188 s` at Loom `09f071d0`; refresh on the current source before approving the first pilot. |
| Frontend route chunks | Measured | Clean production build `7.61 s`; 13 lazy route entries with exact raw byte sizes in the manifest. Terminal is eagerly mounted and therefore has no lazy route-entry chunk. |

## Proof commands and interpretation

Run from the Loom repository root:

```sh
GOCACHE=/tmp/go-build-cache go run ./scripts/archcheck check
GOCACHE=/tmp/go-build-cache go test ./internal/archtest -count=1
GOCACHE=/tmp/go-build-cache make test-characterization
GOCACHE=/tmp/go-build-cache go test \
  ./internal/driver ./internal/cli/serve ./internal/cli/agent/lead \
  ./internal/cli/daemon/supervisor ./internal/infra/fleetdb \
  ./internal/bootstrap ./internal/cli/cmdstore \
  -run 'Test(StaleTaskSweeperUsesMonotonicElapsedTimeAcrossWallClockJumps|StaleTaskSweeperRunOnce|ServeHostedTaskSessionHeartbeatAdvancesUntilCanceled|FinishFlueTaskSessionDrainsInFlightStaleHeartbeat|DriverStaleTaskMaxAgeMatchesSweeperDefault|StandaloneLeadSessionHeartbeatAdvancesUntilStopped|HeartbeatAgentSessionsOnce|AgentSessionFinalizationDrainsInFlightStaleHeartbeat|RequireCapabilities|OpenStoreCloud|OpenStoreWithCapabilities)' \
  -count=1
GOCACHE=/tmp/go-build-cache go run ./scripts/supervisordisabled --validate
```

`make check-go` is the repository integration gate and includes `make check-architecture`, `make test-characterization`, and the non-provisioning `make check-supervisor-disabled` schema/contract validation. The supervisor-disabled execution target has different semantics:

```sh
make test-supervisor-disabled
```

That command must currently exit nonzero and report this stable row:

| Row | State | Owner | Why it remains RED |
|---|---|---|---|
| `deterministic-plan-coder` | **RED** | `execution-reliability-lane` | The TS prompt-agent execution leaf lacks a deterministic local backend, public-API prompt-agent seeding is not ordered before task creation, and local mode still enters the workspace daemon manager. |

The row fixes `LOOM_LOCAL_MODE_PLANE=ts` and `LOOM_TASK_READY_EVENTS=1`, declares fresh Compose setup/verification/teardown, and requires zero auto-agentdefs, daemon processes, and daemon sockets; public-API plan/task agents; planner design/review; coder completion; both transcripts; and coder diff evidence. A declared-red row does not provision and can never count as proof.

## Phase 2 boundary

The next implementation phase starts with the Workflow Catalog read seam, then implements the three Phase 2B intent commands with Redis/Postgres parity, typed operator authority, CAS/atomicity, capability advertisement, management-API routing, and cross-repository contract proof. Until those slices land:

- the mutation-ledger rows remain specifications;
- `workflow_catalog.version_lifecycle.v1` is not claimed as an available deployment capability;
- workflow-approval latency and FleetDB round trips remain null; and
- no supervisor code may be deleted based on the RED matrix row.

---

[Migration overview](README.md) · [Phase 0 baseline](00-phase-0-baseline.md) · [Migration plan](03-migration-plan.md) · [Enforcement and gates](04-enforcement-and-gates.md)
