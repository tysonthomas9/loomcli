# Phase 6 Decisions and Evidence

- **Status:** Complete: source, paired-service, container,
  supervisor-disabled, signed packaged-Desktop UI, and real-Codex acceptance
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

## Fresh packaged Desktop real-Codex acceptance

Computer Use launched the exact signed Phase 6 package above. Its packaged Loom
sidecars ran directly from that app bundle and served the UI on
`127.0.0.1:58782`; no development server or browser-only harness substituted
for the product. Through the Desktop UI, the proof selected workspace
`PHASE5-REPAIRED-20260801`, repository `phase5-repaired-proof-repo`, and created
these enabled Codex agents:

- planner `agt-phase6-final-codex-planner-20260-114c5850`;
- coder `agt-phase6-final-codex-coder-2026080-65a2fe0a`.

The UI then created task `PHASE5-REPAIRED-20260801-23`, **Phase 6 signed
Desktop Codex canary**. The first dispatch correctly selected the planner while
the coder skipped `has_design=false`. The planner completed with exit 0 in
3m28s, persisted a repository-grounded design, left the worktree unchanged, and
moved the task to Review. After the task was moved back to Open through the UI,
the planner skipped `hasDesign=true` and the coder was the sole active worker.
It completed with exit 0 in 1m33s and returned the task to Review, unassigned.
Both real-Codex transcripts remained readable in the task side panel.

The coder delivered local branch `loom/PHASE5-REPAIRED-20260801-23` at
`c629ddf52270`. The UI diff and an independent post-run `git show` both contain
exactly one new file, `phase6-final-desktop-canary.md`, with these two lines:

```markdown
# Phase 6 Desktop Canary
Verified through the signed Phase 6 packaged Desktop on 2026-08-03.
```

The coder's transcript records exact-byte, UTF-8/LF, scope, and protected-file
checks, `npm test` at 2/2 passing, a clean worktree, and no push or PR action.
The detached run worktree readback was clean, the branch ref resolved to the
same commit and diff, and a workspace-wide search found no built-in-runner fake
completion sentinel. The captured Desktop diff screenshot at
`/tmp/phase6-final-desktop-codex-diff.jpeg` has SHA-256
`d5a60d38d08ff15e12ca11b1338200e56e792effd0bacf265cda949469a53eaf`.
This is fresh Phase 6 package evidence; it does not relabel Phase 5's historical
20-row matrix.

## Post-completion repository scoping correction

Repository display names are workspace-scoped. The same checkout may be
cloned or attached independently in any number of workspaces; explicit
workspace routes remain authoritative. A legacy repo-only lookup succeeds only
when there is exactly one owner and otherwise fails closed with HTTP 409 and
`REPO_AMBIGUOUS`. FleetDB enforces the contract in Redis, PostgreSQL,
repository admission, replay/projection, and disaster-recovery paths. The
PostgreSQL admission alias primary key is now `(workspace, repository_name)`.

Loom maps definitive admission collisions to HTTP 409 instead of a generic
500, removes unbound local recovery receipts after a definitive conflict, and
canonicalizes mixed-case workspace routes before constructing FleetDB keys.
The packaged service log now retains a 50 MiB active file and two bounded
backups; startup compacts an older oversized active file. Missing active
transcripts back off from three seconds to a 30-second ceiling and reset after
success.

The paired source tests passed Redis and PostgreSQL storage contracts,
resolution middleware, replay/projector recovery, the complete FleetDB
container integration target, and Loom's full Go/frontend gate against the
exact companion FleetDB binary. Packaged UI acceptance then proved all of the
following on 2026-08-03:

- pre-existing `Loom-P6` and `Loom-P61` both expose `loomcli`;
- UI Add Repo completed independently in `SHARED-REPO-PROOF-A` and
  `SHARED-REPO-PROOF-B`, with both workspaces exposing `loomcli`;
- a second same-origin checkout in one workspace was assigned the distinct
  local display name `loomcli-2` instead of returning HTTP 500;
- `SHARED-REPO-PROOF-C` was created from the prefilled
  `octocat/Hello-World` remote and finalized with detected
  `default_branch=master` and `current_branch=master`; Loom no longer assumes
  `main` before clone discovery;
- the rebuilt sidecar SHA-256 was
  `792db915a875910b57b89207c0439b640c93d0924e91c558876890ba35b80609`,
  paired FleetDB was
  `5d2f1d0c54627d241d30d6b50a7717abe2aa2921a7cfe86ae42005897cad50f7`,
  and the UI screenshot at `/tmp/phase6-shared-repository-proof.png` was
  `dd3619207764f2836019c19d43ee4ba769116437c5255db55935941c1f9dfcf7`;
- after restart the active service log was 114 KiB with one 50 MiB backup,
  replacing the earlier multi-gigabyte active log.

## Scope disposition

The operator waived GitHub mutations for this phase. No push, PR, review,
comment, merge, or other GitHub state change is required for Phase 6 acceptance.
The Desktop dependency install reported three existing npm audit findings (one
low and two high); no unrequested audit fix changed the lockfile or dependency
graph.

---

[Migration overview](README.md) · [Migration plan](03-migration-plan.md) · [Enforcement and gates](04-enforcement-and-gates.md)
