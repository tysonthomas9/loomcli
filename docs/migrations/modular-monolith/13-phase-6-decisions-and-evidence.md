# Phase 6 Decisions and Evidence

- **Status:** Source, paired-service, container, supervisor-disabled, and
  packaged-binary acceptance complete; fresh packaged-Desktop UI and real-Codex
  canary pending macOS unlock
- **Date:** 2026-08-03
- **Loom implementation:** `02daec3395636748992a48551b83d3fedfe6ec1e`
- **FleetDB implementation:** `51b8a493e3fce0845ccd56da64c6c1f39383ccd0`
- **Migration:** [Modular Monolith Migration](README.md)

## Outcome

Phase 6 removes the legacy supervisor topology instead of moving it into a new
package. Canonical Agent identity, desired state, Agent lifecycle commands,
WorkerProfile placement, TaskRun execution, and Interaction session ownership
are now the only supported paths. The checked architecture graph is ratcheted
to `completed_phase: 6`; expired compatibility paths have zero allowances.

The principal physical removals are:

- `internal/cli/daemon`, including `supervisor`, daemon control, restart,
  command polling, and workspace lock code;
- `internal/rpc`, `internal/backend/agentipc`, `internal/webui/daemon`, and the
  WebUI daemon control module;
- legacy FleetDB Agent CRUD, `AgentCommand`, and `DaemonProfile` API, service,
  storage, Lua, and projection paths;
- supervisor-only defaults, daemon registry and IPC clients, legacy agent
  compatibility stores, and the old workflow path;
- task-spawn and lifecycle behavior that crossed Agents, Execution,
  Interaction, and Source Control through `agentdef`.

Historical FleetDB event names remain only as replay tombstones. Migration 079
drops the retired legacy Agent table; it does not establish a second live
aggregate.

## Replacement decisions

| Retired responsibility | Phase 6 owner and behavior |
|---|---|
| Agent identity and lifecycle | Agents owns canonical identity, desired state, generation/revision fencing, and atomic start/stop/remove. |
| Binding and grant drift | The registered Agents desired-state reconciler periodically repairs only canonical declared intent with exact authority and revision. |
| Batch execution | Execution owns TaskRun admission, heartbeat, bounded concurrency, retry, recovery, artifacts, and terminal convergence. |
| Interactive execution | Interaction owns AgentSession, TerminalSession, AgentLease, inbox, and fenced interruption. |
| Repository placement | WorkerProfile and Source Control supply explicit repository and lineage policy; WorkerProfile updates use parent-epic compare-and-swap. |
| Task arbitration | Role task filters explicitly select planning, coding, review, or read-only bug work before claim and revalidate canonical card state after claim. |
| Runtime defaults | Task-ready and task-review eventing, TS execution leaf selection, stale timing, retry limits, and worker concurrency are platform policy rather than supervisor toggles. |
| `agentdef` | The CLI uses canonical Agent and WorkerProfile management boundaries and rejects retired cross-capability flags. |

## Exact Phase 6 parity matrix

`test/modular-monolith/phase6-parity-matrix.yaml` contains seven authoritative,
ordered rows. The runner first discovers the selected top-level tests, rejects
missing or extra tests, executes `go test -json`, and requires every declared
test to emit both `run` and `pass`. A `skip` event fails the gate.

| Row | Required behavior |
|---|---|
| `architecture-retirement` | Checked inventories match and daemon, RPC, agent IPC, and WebUI daemon paths cannot return. |
| `canonical-agent-reconciliation` | Exact-authority periodic reconciliation repairs drift without changing intent and isolates failures. |
| `canonical-agentdef-boundary` | Canonical Agent lifecycle is atomic, retired flags are absent, and a missing management endpoint fails closed. |
| `runtime-defaults` | Stale timing, ready/review events, concurrency, and retry limits are explicit bounded platform defaults. |
| `heartbeats-and-recovery` | Every child is heartbeated, stale recovery is parent-fenced, fence loss cancels execution, and monotonic time governs staleness. |
| `retry-concurrency-and-epics` | Retries retain distinct evidence, exhaustion reaches Blocked, missing convergence/artifacts fail closed, and epic state is canonical. |
| `prompt-agent-arbitration` | Sixty deterministic scenarios cover exact claims, planner/coder/review/bug routing, contention, ambiguous failures, delivery, and terminal policy. |

The combined `make test-supervisor-disabled` proof passed all seven rows after
creating a fresh isolated Podman stack. Its product verifier created public
planner and coder agents before their tasks, observed both tasks in Review,
verified the planner design, both completed transcripts, the coder local-branch
reference and diff, and asserted zero automatic agent definitions, daemon
processes, or daemon control sockets. Teardown removed all containers, volumes,
and the network. Final summary: `green=1 red=0 failed=0 total=1`.

## Source and service validation

| Check | Result |
|---|---|
| FleetDB full gate | PASS at `51b8a493`: static/lint, race unit and integration suites, 80.8% aggregate coverage with all 28 package floors, PostgreSQL storage/archive/API, real-container E2E, Redis degradation/recovery, crash recovery, replay, claims, RPC/HTTP, and harness evaluation. |
| Loom full gate | PASS at `02daec339` against the exact FleetDB source and binary: all Go, architecture, frontend, race, and coverage gates passed. |
| Architecture | PASS across all 11 profiles plus all-files AST: Store `67/57`, 82 handler imports, 225 direct-write rows, 8 roots, 105 mutation commands, 71 runtime components, 80 goroutine launches, 6/6 performance records, and zero pending decisions. Peak measured tree RSS was 1230.9 MiB below the 2048 MiB guard. |
| Paired contract | PASS: FleetDB `api/openapi.yaml` and Loom `internal/infra/fleetdb/testdata/fleetdb-openapi.yaml` are byte-identical at SHA-256 `816b0b0ca5a3398238cf56152f6a040e1bc4cc3bd3c5d1e2dde3fa775dca7ef0`. |
| Failure policy | PASS: real-container crash/restart and Redis recovery run in FleetDB's gate; Phase 6 tests separately prove parent-fence cancellation, stale recovery, retry exhaustion to Blocked, and fail-closed missing artifacts or convergence. |

The final exact-head Loom gate exposed one lint issue in the new JSON evidence
renderer: diagnostic writes ignored their `io.Writer` error. Phase 6 now
propagates those failures and has a focused regression test; the full gate was
rerun successfully after the fix.

## Packaged Desktop provenance

The macOS app was rebuilt from Loom `02daec339` and FleetDB `51b8a493`, then
ad-hoc resealed for local acceptance. Deep strict verification passes, the
Loom sidecar reports `dev (02daec339)`, packaged Node retains
`com.apple.security.cs.allow-jit`, and its execution smoke test passes.

| Packaged executable | SHA-256 |
|---|---|
| Loom | `b0e70627874a299fad59c084ee4cf0e8f1bdba5e8782c21da122b5a46e7e62a4` |
| FleetDB | `16681809a2f9aa09b16679e9ff15091ad3b9480ed9a0d776a9d0756dd3de5162` |
| Desktop | `6cb6e6ff3a95a5e0198985c7fc8839014f7fba1b86faa6161ac7e5d7ea4e0194` |
| Node | `34c0af7cb2ba9eeb14e0675695e3f6da15fa5e98901e62149cf4fc1d594c8fa0` |

The fresh UI and real-Codex canary remain pending because macOS was locked and
Computer Use could not acquire the app. This is an external acceptance blocker,
not a source, package-build, signing, or deterministic-runtime failure. Phase 5's
20 accepted real-Codex rows remain historical evidence and are not relabeled as
a fresh Phase 6 package run.

## Scope disposition

The operator waived GitHub mutations for this phase. No push, PR, review,
comment, merge, or other GitHub state change is required for Phase 6 acceptance.
The Desktop dependency install reported three existing npm audit findings (one
low and two high); no unrequested audit fix changed the lockfile or dependency
graph.

---

[Migration overview](README.md) · [Migration plan](03-migration-plan.md) · [Enforcement and gates](04-enforcement-and-gates.md)
