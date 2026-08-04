# Module Map for External Docs and Developer Onboarding

> **Status:** Stale snapshot · *audited 2026-08-03* — research findings, input
> for writing docs rather than the docs themselves. The survey below is a
> point-in-time analysis that has not been re-run; several of its recommendations
> have since shipped (see §10.3).

**Date:** 2026-07-23
**Branch surveyed:** `feat/codex-reviewer-resume` @ `b490a290b`
**Method:** primary sources only (Go source, `go.mod`, `Makefile`, `.golangci.yml`,
`.goreleaser.yml`, CLI `--help` from a freshly built binary, HTTP route
registrations, `api/openapi.yaml`, `sdk/*.d.ts` + `sdk/api-surface.v1.json`,
package doc comments, and the repo's own dated docs). Every claim cites a file
(and line where it matters). Anything not confirmed in code is marked
**UNVERIFIED**.

---

## Executive summary

Loom is a Go CLI (`loom`) plus a React web UI that runs many AI coding agents in
parallel against a shared, fleet-db-backed issue tracker. There is exactly one
Go module (`github.com/tysonthomas9/loomcli`, `go.mod:1`), one binary entry point
(`cmd/loom/main.go`), ~154 Go packages under internal/, and one in-repo SPA at
`internal/webui/frontend/`.

Seven findings shape how the docs should be written:

1. **The product is bigger than the README says.** The README documents ~20
   commands; the built binary exposes **37 visible top-level commands** (plus
   `help`/`completion`, plus 2 hidden: `hooks`, `log-router`) — including
   `workflow`, `driver`, `trigger`, `connector`, `stack`, `epic`, `worker`,
   `local`, `install-service`, and `doctor`. Whole subsystems (workflow drivers,
   stacked PRs, triggers/webhooks, connector egress vault) have no user-facing
   docs at all.
2. **The README has verified factual errors** — the default backend is `codex`,
   not `claude` (`internal/cli/backend.go:29`, `:90`), and `loom serve` no longer
   serves the UI unless `--frontend-dir` is passed
   (`internal/webui/app/frontend.go:11-16`).
3. **The architecture is documented only in the linter.** `.golangci.yml:77-186`
   encodes the real layering (`sdk → infra → web → cli`) with per-package
   membership and enforced deny rules. The comment at `.golangci.yml:79` points
   at a plan file *outside the repo*. There is no `docs/arch/` note for it.
4. **The glossary is thinner than the docs that cite it.** `docs/agents/domain.md:12-14`
   describes `docs/loom-glossary.md` as containing a "request lifecycle, object
   model, the four planes". The glossary (63 lines) contains none of those — only
   core objects, role kind, prompt selection, and overloaded names.
5. **Two referenced files do not exist:** `docs/testing-terminology.md` (cited by
   `AGENTS.md:20`, `AGENTS.md:35`, `docs/agents/domain.md:18`,
   `internal/cli/agent/prompts/pr-review-checkout.md:39`) and
   `docs/testing/fleetdb-acceptance-gates.md` (cited by `docs/testing/README.md:32`,
   `docs/testing/go-backend-tests.md:35`). `RUNTIME-AND-DEPLOYMENT.md` is cited by
   `deploy/podman-stack/README.md:5` and is also absent.
6. **The repo does not build or test standalone.** Real E2E and container work
   needs two sibling checkouts, `../fleet-db` and `../flue`
   (`scripts/start-e2e-server.sh:86-95`, `deploy/podman-stack/build.sh:29,42`).
   Neither is mentioned in `README.md` or `AGENTS.md`.
7. **Every product spec is marked Draft** — 16 of 16 files in `docs/product/`.
   Only two docs in the repo claim to describe shipped behavior:
   `docs/observability/tracing-contract.md` ("source-of-truth") and
   `docs/design/workflow-driver-authoring-guide.md` ("current platform contract"),
   and the latter's code samples are stale (see §5).

---

## 1. What the product actually is

### Repo's own one-liner

> "Agent management CLI for parallel AI coding workflows backed by fleet-db.
> Run multiple AI agents in parallel across git worktrees — each agent works
> independently on its own branch, picking tasks from a shared issue tracker,
> then integrating back through a structured push/pull/sync workflow."
> — `README.md:3-5`

The binary's own help is narrower and slightly stale: `"Agent management CLI for
parallel Claude Code workflows"` (`internal/cli/root.go:37`).

### The value proposition, in the repo's vocabulary

Per `docs/loom-glossary.md`:

| Term | Meaning in this repo | Source |
|---|---|---|
| **loom** | The AI-agent orchestration platform backed by fleet-db. Not the video product, not Java Project Loom. | `docs/loom-glossary.md:3-5` |
| **Workspace** | fleet-db-scoped project container; owns repos, roles, agent definitions, issues, daemon profiles, local runtime state. | `docs/loom-glossary.md:9-11` |
| **Role** | Reusable agent configuration: prompt selection, backend, model, task filter, tool policy, `kind`. | `docs/loom-glossary.md:12-13` |
| **Agent** | A named assignment that uses a role. | `docs/loom-glossary.md:13-14` |
| **Lead** | The default *interactive* role and terminal agent; not the only possible one. | `docs/loom-glossary.md:15-18` |
| **Worker** | Autonomous role/agent that claims and completes tasks under daemon supervision. | `docs/loom-glossary.md:19-20` |
| **role.kind** | `interactive` \| `worker`. When unset, legacy naming applies: `lead`/`orchestrator` → interactive, everything else → worker. | `docs/loom-glossary.md:24-35` |
| **fleet / fleet-db** | The control-plane data service that stores Loom state. **A separate service and separate repo.** | `docs/loom-glossary.md:55` |
| **flue** | External agent-harness framework used to build and run TypeScript agents/workflows. **A separate repo.** | `docs/loom-glossary.md:56` |
| **aether** | The UI/design-system terminology used by the web UI. | `docs/loom-glossary.md:57` |
| **codex / claude** | AI backends / agent CLIs. | `docs/loom-glossary.md:58-59` |
| **daytona / atlas** | Deployment or provider concepts in design docs / runtime config. | `docs/loom-glossary.md:60-61` |

Terms **missing from the glossary** that are load-bearing in the code and would
mislead a reader: *cortex* (a UI codename — `docs/epics/cortex-ui-v6-workspace-redesign.md`,
`docs/design/cortex-v7/`), *driver* / *DriverVersion* / *DriverRun*
(`internal/domain/platform.go:64-102`), *stack* (stacked PRs —
`internal/cli/stack/stack_cmd.go:28`), *connector* (sealed-credential egress —
`internal/cli/connector/connector_cmd.go:35`), *trigger binding*
(`internal/trigger/pattern.go:1-22`), *TaskRun*, *epic runner*, and *backend*
(which means two entirely different things — see §3.2).

### The core loop the product sells

From the CLI's own help text:

1. `loom plan <agent>` — planning agent picks the top-priority task, researches,
   writes a design into the task's `--design` field, sets status `review`
   (`loom plan --help`, source `internal/cli/agent/plan.go:36`).
2. `loom lead` — interactive terminal agent reviews/approves plans, files
   tickets, triages the backlog (`internal/cli/agent/lead/lead.go:45`).
3. `loom task <agent>` — implementation agent follows the approved design,
   implements, tests, commits, pushes, closes (`internal/cli/agent/task.go:28`).
4. `loom push` / `loom pull` / `loom sync` / `loom pr` — integrate worktree
   branches with AI conflict resolution (`internal/cli/git/`).
5. `loom daemon` — supervises many of these with auto-restart
   (`internal/cli/daemon/daemon_cmd.go:71`).
6. `loom serve` + web UI — kanban/table/graph/monitor/terminal views over the
   same state (`internal/cli/serve/serve.go:101`).

`--auto` turns 1 and 3 into continuous loops with `--max-tasks`, `--interval`,
and `--idle-timeout` exit conditions (`README.md:168-239`, flags confirmed on
`loom plan --help`).

---

## 2. Top-level architecture

### Repository shape

| Path | What it is | Language | Notes |
|---|---|---|---|
| `cmd/loom/` | The one and only binary entry point. 57 lines. | Go | `cmd/loom/main.go:13-37` blank-imports every CLI sub-package so their `init()` calls `cli.RegisterCommand`. |
| `internal/` | ~154 Go packages, ~200k non-test lines. | Go | See §3. |
| `internal/webui/frontend/` | The React 18 + Vite SPA (`@loom/web-ui`). 597 non-test `.ts`/`.tsx` files. | TypeScript | **Not embedded** in the Go binary — served from disk or by an external host. |
| `sdk/` | `@loom/sdk` — the public JS/TS SDK for workflow drivers and task runners. | JS + `.d.ts` | Not yet published to npm (`sdk/CHANGELOG.md:6-9`). |
| `api/` | `openapi.yaml` (3.1, 6760 lines) + `oapi-codegen.yaml`. | YAML | Source of truth for both Go types (`internal/backend/api/gen/types.gen.go`) and TS types (`internal/webui/frontend/src/types/generated/openapi.ts`). |
| `desktop/` | Tauri 2 macOS shell ("Loom Agents"). Thin controller only. | TS + Rust | `desktop/README.md:5-12`; bundles `loom` **and** `fleet-db` as sidecars (`desktop/src-tauri/tauri.conf.json:33`). |
| `services/auth/` | Standalone BetterAuth-based auth service (Hono + drizzle). | TypeScript | `services/auth/package.json`; consumed via `loom serve --auth-url`. |
| `npm/` | npm shim package `loomcli` that downloads a release binary. | JS | `npm/package.json:2`, `npm/install.js`. Does **not** ship the SDK. |
| `deploy/`, `test/`, `e2e/`, `scripts/`, `smoke-test/` | Deployment references and test harnesses. | Compose/sh/Go | See §7, §8. |
| `docs/` | 61 markdown files across `design/`, `product/`, `arch/`, `testing/`, `agents/`, `observability/`, `epics/`. | Markdown | See §9. |

### The layer model (verified from the linter, not from prose)

`.golangci.yml:77-79` states the dependency direction outright:
`sdk → infra → web → cli`. Membership and deny rules are enforced by `depguard`:

| Layer | Packages (from `.golangci.yml`) | Rule |
|---|---|---|
| **sdk (leaf)** | `internal/types`, `internal/entity`, `internal/backend/*.go` (root only), `internal/notify`, `internal/agenterr`, `internal/authmode`, `internal/workspaceerrors`, `internal/events/*.go` (root only), `internal/ops`, `internal/httpclient` | `.golangci.yml:80-92`; may not import any infra package (`:93-137`) |
| **infra** | `internal/rpc`, `internal/sessions`, `internal/lockfile`, `internal/circuitbreaker`, `internal/kv`, `internal/configlock`, `internal/usage`, `internal/logrouter`, `internal/atomicfile`, `internal/gitbranch`, `internal/workspace`, `internal/debug`, `internal/testutil`, `internal/backend/{mapping,beads,fleet,agentipc,api}`, `internal/events/otelexport` | `.golangci.yml:138-159`; must not import `internal/webui` or `internal/cli` (`:160-164`) |
| **web** | `internal/webui/**` | `.golangci.yml:165-168`; must not import `internal/cli` (`:169-171`) |
| **cli** | `internal/cli/**` | top of the stack |
| **special: `internal/cli/data`** | sdk-only command package | `.golangci.yml:177-186` — may not import `cli` root, `cli` sub-packages, `webui`, or any infra package. It talks to a loom server over HTTP via the generated OpenAPI client only. This is why `cmd/loom/main.go:43-50` registers it explicitly instead of via `init()`. |
| **special: handlers** | `**/handler/**` | `.golangci.yml:62-76` — handlers must not import `internal/webui/daemon`, `internal/backend`, `internal/notify`, `internal/webui/store`, or `internal/store`; they must go through `internal/webui/service`. |

Additional enforced structural gates (all in `make check-go`, `Makefile:469-497`):
per-file LOC ≤1000/2500 (`scripts/check-loc.sh`), files-per-package ≤25
(`scripts/check-package-size.sh`), import fanout ≤18
(`scripts/check-import-fanout.sh`), no raw `exec.Command` in unit tests
(`scripts/check-no-raw-exec.sh`), no `log.Printf`
(`scripts/check-no-log-printf.sh`), no new beads references
(`scripts/check-no-beads-prod.sh`), control-plane path invariants
(`scripts/check-control-plane-paths.sh`).

### The two legal control-plane paths

`README.md:26-32` and `docs/product/local-mode-product-spec.md:22-33` both state
the same invariant, and `scripts/check-control-plane-paths.sh` enforces it in CI:

```
local mode:  loomcli -> HTTP client -> fleet-db subprocess -> RedisStorage -> miniredis or external Redis
cloud mode:  loomcli -> HTTP client -> fleet-db service    -> Redis/Postgres
```

`internal/infra/memstore` (7,043 non-test lines) is a **test-only** store double.
It is explicitly not a local runtime, fallback, cache, or embedded Redis
(`README.md:317-318`, `docs/product/local-mode-product-spec.md:32-33`).

---

## 3. Module map of `internal/`

Non-test line counts, largest first, are the honest weighting: `internal/cli`
(58,955 / 262 files) and `internal/webui` (45,874 / 258 files) are together ~72%
of the Go code. Everything else is comparatively small.

### 3.1 Control plane / data access

| Package | Purpose | Entry file | User-facing? |
|---|---|---|---|
| `internal/domain` | Canonical control-plane domain types: Driver/DriverVersion/DriverRun, Role, Agent, DaemonProfile, Connector, Await, ControlPlane. | `internal/domain/platform.go:64-102`, `control_plane.go` | internal |
| `internal/entity` | Canonical "V2" domain types (Issue, Agent, Comment, Dependency, Diff). Leaf package, zero internal imports. | `internal/entity/doc.go:1-10` | internal |
| `internal/types` | Pre-V2 issue types, enums, ID generation, federation, issue hashing. Being migrated away from in favour of `entity`. | `internal/types/issue.go`, `enums.go` | internal |
| `internal/backend` (root) | **`IssueBackend`** — the pluggable *issue-tracking data access* interface (Get/List/Ready/Blocked/Stats/Search/…). Nothing to do with AI backends. | `internal/backend/issuebackend.go:18` | internal |
| `internal/backend/fleet` | `IssueBackend` implementation over the fleet-db HTTP API. | `internal/backend/fleet/` | internal |
| `internal/backend/api` + `api/gen` | `IssueBackend` implementation over a remote **loom server** (`--server`), using types generated from `api/openapi.yaml`. | `internal/backend/api/gen/types.gen.go` (generated) | internal |
| `internal/backend/agentipc` | `IssueBackend` that proxies Claim/Update/Close to the daemon's agent IPC unix socket; everything else returns `KindNotImplemented`. | `internal/backend/agentipc` (package doc) | internal |
| `internal/backend/mapping` | Wire-type → entity-type mapping layer. | `internal/backend/mapping/` | internal |
| `internal/store` + `internal/store/storetest` | `store.Store` interface + a store-agnostic conformance suite run against both memstore and the fleet-db client. | `internal/store/storetest` (package doc) | internal |
| `internal/infra/fleetdb` | The real `store.Store`: HTTP client against fleet-db. 4,731 lines. | `internal/infra/fleetdb/` | internal |
| `internal/infra/memstore` | **Test-only** in-process store double. 7,043 lines. | `internal/infra/memstore/` | internal (tests) |
| `internal/fleethttp` | Shared low-level HTTP plumbing between the two fleet-db callers (`backend/fleet` and `infra/fleetdb`). | `internal/fleethttp` (package doc) | internal |
| `internal/httpclient` | Auth-aware HTTP client (sdk layer). | `internal/httpclient/` | internal |
| `internal/rpc` | A **separate, older** line-JSON RPC protocol over a Unix socket (~45 ops: `ping`, `status`, CRUD, `dep_*`, `label_*`, `get_mutations`, `wait_for_mutations`, `get_graph_data`, …). **No server implementation exists in this repo** — every importer is a client (~20 files in `internal/webui`, plus `internal/cli/serve/metricscmd`). It is the client side of an external/legacy issue daemon and is *not* the agent-supervisor daemon's protocol. `socket_path.go:31` hashes the workspace path into `/tmp/loom-{hash}/` to dodge the 104-byte macOS `sun_path` limit. | `internal/rpc/protocol.go:6-60`, `client.go:44` | internal |
| `internal/kv` | Redis client wrapper, Lua scripts, stale detection, reconciler. | `internal/kv/client.go` | internal |
| `internal/bootstrap` | Local-mode bootstrap: mode selection, embedded fleet-db spawn, store opening, path resolution. | `internal/bootstrap/embedded.go`, `mode.go`, `openstore.go`, `paths.go` | internal |
| `internal/ops` | Cross-layer operation *interfaces* (backend/file/git/workspace ops). Leaf package; both `cli` and `webui` import it, neither owns the types. | `internal/ops/doc.go:1-5` | internal |

### 3.2 Agent execution / AI backends

> **Naming trap to call out prominently in the dev guide:** `internal/backend` is
> the *issue* backend. `internal/cli/backends` is the *AI CLI* backend. Both are
> called "backend" throughout the codebase and the CLI (`--backend` refers to the
> latter; `loom backend list` lists the latter).

| Package | Purpose | Entry file | User-facing? |
|---|---|---|---|
| `internal/cli` (root, `backend.go`) | The `Backend` interface (`Name`, `InvokeInteractive`, `InvokeNonInteractive`) + registry + resolution. | `internal/cli/backend.go:19-23`, registry `:26-30` | user-facing via `--backend` |
| `internal/cli/backends` | The concrete AI backends: claude, codex, cursor, gemini, opencode, echo (test), and **external plugins**. 3,425 lines. | `backend_claude.go:200`, `backend_codex.go:234`, `backend_cursor.go:167`, `backend_gemini.go:144`, `backend_opencode.go:143`, `backend_external.go:174` | user-facing |
| `internal/backendnames` | Two string constants (`claude`, `codex`). 6 lines — trivial. | `internal/backendnames/names.go` | internal |
| `internal/cli/backendcheck` | Single seam to the harness CLI discovery primitive; a separate package purely to keep `internal/cli`'s import fanout under the gate. | `internal/cli/backendcheck` (package doc) | internal |
| `internal/harness` | **One production file.** Wires backend invocations to the external `olesho/harness-wrapper` supervisor (`go.mod:17`, not vendored, no `replace`); `RunWithRetry` (`retry.go:86`) adds loom's retry policy on top and defers non-transient classes to `agentpolicy`. All PTY/screen-emulation/status-classification/per-harness-profile logic lives *outside this repo*. | `internal/harness/retry.go:1-9,26-63,86,121-137` | internal |
| `internal/cli/automode` | The `--auto` loop: claim → prompt → invoke → count → sleep, with max-tasks/idle-timeout/signal exits. 2,339 lines. | `internal/cli/automode/` | user-facing behaviour |
| `internal/cli/agent` | `loom plan`, `loom task`, `loom agent`, `loom recover`, `loom list`, `loom claim`, `loom complete` + the 8 embedded prompt templates. 3,541 lines. | `internal/cli/agent/plan.go:36`, `task.go:28`, `agent_cmd.go:28`, `prompts.go:22` | user-facing |
| `internal/cli/agent/lead` | `loom lead` — the interactive terminal-agent runtime. | `internal/cli/agent/lead/lead.go:45` | user-facing |
| `internal/sessions` | Agent session records, transcripts, session index/store. 3,728 lines / 30 files. | `internal/sessions/` | internal (surfaced in UI) |
| `internal/leadcontrol` | Lead conversation control + resume across restarts. 3,112 lines. | `internal/leadcontrol/` | internal |
| `internal/epicrunner` | **Not** the epic-runner workflow. Shared lead-assignment primitives: lead role classification, the lead/epic bind lock, the assignment context. ~330 lines. | `internal/epicrunner/start.go:1-4`, `IsLeadRole` at `:71-79` | internal |
| `internal/agentpolicy` | Single source of truth for "what do we DO about this error" across the three decision layers (in-invocation retry, auto-loop, daemon supervisor). | `internal/agentpolicy` (package doc) | internal |
| `internal/agenterr` | Error classification + `Outcome` taxonomy consumed by `agentpolicy`. | `internal/agenterr/classify.go` | internal |
| `internal/agentinbox` | Agent message envelope. 67 lines — trivial. | `internal/agentinbox/message.go` | internal |
| `internal/runtimepreflight` | Fail-closed checks *before* a runner is queued (backend CLI present, auth present) so runs cannot fake-complete. | `internal/runtimepreflight` (package doc) | internal |
| `internal/runtimectx` | Process-wide root context holder so infra helpers can reach it without a cli↔infra cycle. 32 lines. | `internal/runtimectx` (package doc) | internal |
| `internal/usage` | Token/cost collection into `usage.jsonl`, plus purge. | `internal/usage/collector.go` | user-facing via `loom usage` |
| `internal/lockfile` | Cross-platform agent lock files + PID management. | `internal/lockfile/lock.go` | internal |
| `internal/configlock` | Config-file locking primitive. 50 lines — trivial. | `internal/configlock/configlock.go` | internal |
| `internal/atomicfile` | Atomic file write helper. 42 lines — trivial. | `internal/atomicfile/atomicfile.go` | internal |
| `internal/circuitbreaker` | Circuit-breaker state machine for unreliable downstreams. 250 lines. | `internal/circuitbreaker/` (package doc) | internal |

**Backend resolution is five separate ladders, not one.** A doc that says
"flag > env > profile > default" is describing something that is assembled
across two processes:

| Context | Ladder | Source |
|---|---|---|
| CLI process-local | `--backend` → `LOOM_BACKEND` → `codex` | `internal/cli/backend.go:82-91` (doc comment at `:78-81` explicitly excludes the profile) |
| Task runner | agent row `Backend` → `DaemonProfile.AgentBackend` → `codex` | `internal/driver/task_bridge.go:751-772` |
| Preflight / workspace default | `DaemonProfile.AgentBackend` → `codex` | `internal/runtimepreflight/preflight.go:31,55-67` |
| Web terminal launch | `agent.Backend` → `role.Backend` → `DaemonProfile.AgentBackend` → none | `internal/webui/handlers/terminal/agent_session.go:361-377` |
| Daemon per-agent failover | entry backend → `FallbackBackends[i-1]` | `internal/cli/daemon/supervisor/backend.go:88+` |

The daemon profile reaches a child process as argv/env at spawn time —
`--backend <name>` (`internal/cli/daemon/supervisor/spawn.go:93-95,110-112`) or
`LOOM_BACKEND` (`agent_session.go:437-439`).

Also worth flagging for the dev guide: the AI-backend registry is process-global
mutable state, and its test seams (`TestingResetBackendState`, `TestingBackends`,
`TestingActiveBackend`, `TestingBackendMu`) live in production code at
`internal/cli/backend.go:151-196`.

### 3.3 Daemon and local runtime

| Package | Purpose | Entry file | User-facing? |
|---|---|---|---|
| `internal/cli/daemon` | The supervisor: profiles, agent lifecycle, restart backoff, IPC, logs, queue preview. Largest CLI sub-package at 10,885 lines. | `internal/cli/daemon/daemon_cmd.go:71`, `profile_cmd.go:24`, `supervisor/` | user-facing |
| `internal/cli/daemonregistry` | cwd-independent daemon liveness detection from the fleet-db Node registry, alongside lock-file and state-file detection. | `internal/cli/daemonregistry` (package doc) | internal |
| `internal/cli/local` | `loom local` — the desktop local runtime: LaunchAgent install, foreground service, supervised `loom serve` + `loom daemon` subprocesses, drain/resume. | `internal/cli/local/local_cmd.go:33`, `runtime.go`, `daemon.go:29`, `launchagent.go` | user-facing (desktop) |
| `internal/localsettings` | Desktop-local runtime settings (`local-settings.json`, `0600`). | `internal/localsettings` (package doc) | internal |
| `internal/localworkspace` | Machine-local workspace filesystem helpers. 591 lines, one file. | `internal/localworkspace/` | internal |
| `internal/workspace` | Workspace ID normalization + tracing attrs. 106 lines. | `internal/workspace/id.go` | internal |
| `internal/workspaceerrors` | Workspace error sentinel types. 63 lines — trivial. | `internal/workspaceerrors/errors.go` | internal |
| `internal/cli/workspace` | `loom workspace` (both the v1 `create/list/remove` worktree flow and the v2 fleet-db `add/use/set/show/status` flow) + `loom workspace ops` + `loom init` + `loom status`. | `workspace_cmd.go:34`, `workspacev2_cmd.go:35`, `ops_cmd.go:33`, `init.go:28` | user-facing |
| `internal/cli/cmdstore` | Per-command store/context plumbing (`WithStore`, `SetRootContext`). | `internal/cli/cmdstore/` | internal |
| `internal/cli/doctor` | `loom doctor` health checks with `--json` and `--fix`. | `internal/cli/doctor/doctor.go:65` | user-facing |
| `internal/cli/cleanup` | `loom cleanup` (sessions/usage/events retention) and `loom sessions clean`. | `internal/cli/cleanup/cleanup_cmd.go:24` | user-facing |
| `internal/cli/envfilter` | Env allow/deny filtering for spawned agents. 135 lines. | `internal/cli/envfilter/` | internal |
| `internal/cli/sessionfinalize` | Session finalization hook. 74 lines — trivial. | `internal/cli/sessionfinalize/` | internal |
| `internal/cli/hooks` | **Hidden** command tree: Claude Code lifecycle hook handlers (`session-start`, `user-prompt-submit`, `stop`, `pre-task`, `post-task`, `yield-guard`, `session-end`) + `install`/`uninstall`/`status`. | `internal/cli/hooks/hooks_cmd.go:19-109` (all `Hidden: true` except install/uninstall/status) | mixed |

### 3.4 Git / integration

| Package | Purpose | Entry file | User-facing? |
|---|---|---|---|
| `internal/cli/git` | `loom push`, `pull`, `sync`, `pr`, `reset` with AI conflict resolution. 3,832 lines. | `push.go:16`, `pull.go:16`, `sync.go:17`, `pr.go:17`, `reset.go:22` | user-facing |
| `internal/gitbranch` | Shared branch-ref inspection and recovery helpers. | `internal/gitbranch` (package doc) | internal |
| `internal/cli/stack` | `loom stack` — stacked-PR lineage: init/add/move/set-base/remove/restack/validate/publish/status/show/list. | `internal/cli/stack/stack_cmd.go:28` | user-facing |
| `internal/stackstore` | Persists stack lineage. Currently a loomcli-side `LocalStore` backed by `~/.loom/stacks.json` with configlock + atomic writes. | `internal/stackstore` (package doc) | internal |
| `internal/stacklineage` | Stack ordering/branch/type primitives. | `internal/stacklineage/order.go` | internal |
| `internal/stackpublish` | Publishing stacked PRs to a forge (GitHub implementation + generic `forge.go`). | `internal/stackpublish/forge.go`, `forge_github.go` | internal |

### 3.5 Workflow drivers / platform extensibility

| Package | Purpose | Entry file | User-facing? |
|---|---|---|---|
| `internal/driver` | The workflow-driver execution engine: claim/lease/heartbeat, run tokens, bundle digest verification, sandbox placement policy, task bridge/worker, await, composition, outbox dispatcher, stale sweeper. 10,625 lines. | `internal/driver/executor.go:66`, `register.go:34`, `run.go`, `await_op.go`, `composition.go` | internal (surfaced via `loom driver`/`loom workflow`) |
| `internal/driver/sandbox` | The SB1–SB4 isolation seam: `SandboxLauncher`, process launcher, container launcher (podman-first/docker-fallback), egress modes, trust placement policy. | `sandbox/launcher.go:77-79`, `container.go:145`, `egress.go:53`, `policy.go:75-93` | operator-facing via env |
| `internal/driver/runtypes` | Neutral leaf holding `RunRequest`/`RunResult` to break the driver↔sandbox cycle. 35 lines. | `internal/driver/runtypes/runtypes.go:5-9` | internal |
| `internal/workflows` | Built-in workflow registry + Flue bundle build + digest + self-heal registration. | `internal/workflows/workflows.go:79-82`, `digest.go:10-30` | internal |
| `internal/workflows/builtin/` | The TypeScript sources: `epic-runner.ts`, `github-review-agent.ts`, plus runner files `local-task-runner.ts`, `daytona-task-runner.ts`, `openshell-task-runner.ts` (deprecated stub), `github-review-task-runner.ts`. All `//go:embed`-ed. | `internal/workflows/workflows.go:29-45` | authorable |
| `internal/trigger` | Trigger routing primitives: glob route keys, subject-key templates, cron source, internal-event loopback with hop cap, issue-journal bridge, await matcher, delivery sweeper. | `internal/trigger/pattern.go:1-22` | config-facing |
| `internal/connector` | Sealed-credential connector vault + deny-by-default egress grants + single dispatch choke point + audit journal. | `internal/connector/dispatch.go:14-29`, `registry_default.go:30-38` | user-facing via `loom connector` |
| `internal/cli/driver` | `loom driver register|run` plus **14 hidden** driver-op commands used by workflow runtimes (`exec-task`, `claim-ready`, `complete-task`, `deliver-lead-assignment`, …). | `internal/cli/driver/driver_cmd.go:41`, hidden set at `agent_cmd.go:66-98`, `task_cmd.go:75-107`, `epic_cmd.go:36-44`, `exec_cmd.go:62-70` | mixed |
| `internal/cli/workflow` | `loom workflow clone|build|approve|unapprove|activate|run|list|versions|readyz|digest`. | `internal/cli/workflow/workflow_cmd.go:50` | user-facing |
| `internal/cli/trigger` | `loom trigger bindings|events|deliveries`. | `internal/cli/trigger/trigger_cmd.go:25` | user-facing |
| `internal/cli/connector` | `loom connector create|list|rotate|grant|audit`. | `internal/cli/connector/connector_cmd.go:35` | user-facing |
| `internal/cli/epic` | `loom epic run` — drain an epic via the epic-runner workflow. | `internal/cli/epic/run.go:52` | user-facing |

### 3.6 Web server / UI

`internal/webui` is 258 non-test files / 45,874 lines with **149 non-test
`mux.HandleFunc` registrations** across two muxes. Composition root is
`internal/webui/app`.

| Package | Purpose | Entry file |
|---|---|---|
| `internal/webui/app` | Composition root: `Server` struct, `NewServer`, `StartServer`, route registration, module assembly, middleware chain. | `app/server.go:41` (struct), `app/server_app.go:27` (`NewServer`), `app/server.go:182` (`StartServer`), `app/routes.go:14` (`registerRoutes`) |
| `internal/webui/appinfra`, `appstores`, `handlermux`, `modbuilder` | Facade packages that exist purely to keep `app`'s import fanout under the `check-import-fanout.sh` gate. | `appinfra/appinfra.go:1-4`, `appstores/appstores.go:1-3`, `handlermux/handlermux.go:1-4`, `modbuilder/modbuilder.go:1-3` |
| `internal/webui/handlers/*` | 17 route-module packages: `agents`, `agentcontrol`, `approvals`, `driverapi`, `git`, `health`, `issues`, `localsettings`, `misc`, `onboarding`, `prreview`, `taskrunapi`, `terminal`, `webhooks`, `workflows`, `workspace`, `connectors` (test-only). | e.g. `handlers/issues/module.go:39-62` |
| `internal/webui/service` + `svcimpl` | Service-layer interfaces + implementations (issue, agent, workspace, file, diff, session, terminal). Handlers may only reach data through here (`.golangci.yml:62-76`). | `service/issue_impl.go:50`, `svcimpl/session_service.go:32` |
| `internal/webui/server/{dto,handler,middleware,realtime}` | DTOs/validation; shared HTTP plumbing; auth/CORS/rate-limit/security-headers/workspace middleware; SSE hub + terminal auth + WS relay. | `server/middleware/auth.go:113`, `server/realtime/hub.go:62`, `server/realtime/handler.go:68` |
| `internal/webui/terminal` | PTY manager, multi-PTY manager, ring-buffer scrollback, tmux *attach* manager. | `terminal/pty_manager.go`, `terminal/multi_pty_manager.go`, `terminal/agent_tmux.go:1-13` |
| `internal/webui/daemon` | Issue-daemon connection pool, discovery, circuit breaker. | `internal/webui/daemon/` |
| `internal/webui/fleet` | Fleet worker API: register/claim/done/heartbeat, fleet JWT + signing key, rate limit, metrics. | `internal/webui/fleet/module.go:48-68` |
| `internal/webui/coordinator` + `hooks` | Per-workspace subsystem lifecycle registry with ordered register / reverse deregister and rollback. | `coordinator/coordinator.go` |
| `internal/webui/{tabmeta,issuetabs,sessionhistory}` | Redis-backed persistence for terminal tab metadata, per-issue tab state (24h TTL), per-issue session history (no TTL). | package docs in each |
| `internal/webui/localredis` | In-process miniredis + JSON snapshot at `~/.loom/terminal-state/snapshot.json` when no external Redis. 808 lines. | `localredis/manager.go:1-6` |
| `internal/webui/storeadapter` | fleet-db `store.Store` adapters, incl. workspace-path self-healing. | `storeadapter/selfheal.go` |
| `internal/webui/log`, `editor`, `fileaccess`, `service/pathsec` | Log SSE streaming; "open in editor" per-OS launchers; file-access policy; shared path security validators. | `log/streamer.go:122`, `editor/launch_darwin.go`, `service/pathsec` |
| `internal/cli/serve` | The `loom serve` command + its sub-trees (`worker`, `install-service`, `log-router`) and adapters. 8,244 lines. | `internal/cli/serve/serve.go:101` (cmd), `:135` (`runServe`), `:242-244` (handoff to `webuiapp.StartServer`) |
| `internal/cli/monitor` | `loom monitor` / `loom status` terminal dashboard. | `internal/cli/monitor/monitor.go:26` |

**Route groups** (registration sites; full table in the source):

| Group | Prefix | Registered at | Audience |
|---|---|---|---|
| Health / bootstrap config | `/health`, `/api/health`, `/api/config`, `/api/metrics` | `app/routes.go:42-47` | public |
| Monitor dashboards | `/api/monitor/*` | `app/routes.go:96-117` | user |
| Workspace CRUD | `/api/workspaces*` | `app/routes.go:142-169` | user |
| Workspace-scoped modules | `/api/workspaces/{ws}/…` (nested mux) | `app/routes.go:172-199` | user |
| Issues / comments / deps / tabs / sessions | under `{ws}` | `handlers/issues/module.go:39-62`, `tab_module.go:32-34`, `session_module.go:51-65` | user |
| Agents + agent control | under `{ws}` | `handlers/agents/module.go:24-34`, `handlers/agentcontrol/module.go:23-27` | user |
| SSE mutation stream | `{ws}/events`, `{ws}/events/token` | `subscription/module.go:52,54` | user |
| Terminals + tabs/state | `{ws}/terminal/*`, `{ws}/agents/{name}/terminal/*` | `handlers/terminal/module.go:73-90`, `tab_module.go:27-41` | user |
| Git + diffs + file browser | `{ws}/git/*`, `{ws}/agents/{name}/git/*`, `{ws}/files*` | `handlers/git/module.go:31-47`, `handlers/misc/module.go:35-50` | user |
| Pull requests | `{ws}/pull-requests*` | `handlers/prreview/module.go:97-104` | user |
| Workflows | `{ws}/workflows/*`, `{ws}/runs/*` | `handlers/workflows/module.go:34-38` | user |
| **Driver-op API** | `{ws}/driver/*` | `handlers/driverapi/module.go:191-198` | **internal** — workflow bundles, run-scoped token |
| **Task-run API** | `{ws}/task-run/*` | `handlers/taskrunapi/module.go:145-148` | **internal** — lease token |
| Webhooks / triggers | `{ws}/webhooks/{name}`, `{ws}/trigger-*` | `handlers/webhooks/module.go:45-49` | ingress + user reads |
| Fleet worker API | `{ws}/fleet/*` | `internal/webui/fleet/module.go:48-68` | **internal** |
| Remote worker API | `/api/internal/workers/*` | `app/server_workspace.go:124-137` — only registered when `LOOM_WORKER_TOKEN` is set | **internal** |
| SPA / static | `/` | `app/frontend.go:15` — only when `--frontend-dir` is set | public |

**Middleware order** (`app/server.go:257-271`): otelhttp → Recover → RequestLog →
Prometheus → RateLimit → SecurityHeaders → **Auth** → CORS → route capture →
tracing → mux.

**Realtime.** The product SSE stream is `GET /api/workspaces/{ws}/events`
(`subscription/module.go:52`, handler `server/realtime/handler.go:68`) with
`Last-Event-ID` catch-up (`handler.go:135,182-197`), heartbeats
(`handler.go:223-227`), and `event: mutation` frames (`handler.go:246`).
`docs/design/generic-sse-envelope.md` is **accurate** on the envelope
(`entity_type`/`entity_id`/`action` at `server/realtime/hub.go:44-46`, legacy
`type`/`issue_id` retained at `:43,47`) but **incomplete**: four other
`text/event-stream` endpoints exist that it does not mention — log streaming
(`log/streamer.go:122`), driver epic watch (`handlers/driverapi/watch.go:66`),
workflow run stream (`handlers/workflows/module.go:239`), and PR reviewer stream
(`handlers/prreview/stream.go:67`).

**Terminals.** Three mechanisms coexist: (a) PTY-backed web terminals via
`creack/pty` and a WebSocket relay with an in-band `\x1b[RESIZE:cols;rows]`
control sequence (`handlers/terminal/ws.go:88-93`,
`server/realtime/terminal_relay.go:55-57`); (b) tmux *attach-only* for CLI
auto-mode agent sessions — the code explicitly never creates tmux sessions
(`terminal/agent_tmux.go:1-13,50-52`), and degrades to log streaming when tmux is
absent (`app/server.go:78`); (c) xterm.js in the browser
(`internal/webui/frontend/package.json`, `components/TerminalView/instances/XTermRenderer.tsx`).
`docs/arch/terminal-system.md` documents the frontend hierarchy.

**Auth.** Two modes only, `open` and `oidc`, decided purely by whether
`--auth-url`/`LOOM_AUTH_URL` is set (`internal/authmode/authmode.go:4,7`;
selection at `handlers/misc/auth_config.go:128-142`). JWKS is fetched from
`<auth-url>/api/auth/jwks` (`app/server_app_helpers.go:16`) through an
SSRF-guarded dialer (`internal/webui/safe_dial.go`), cached
(`server/middleware/jwks.go`), and enforced by
`server/middleware/auth.go:113` — RS256 only, by pointer identity
(`auth.go:97`), expiration required, 5s leeway, bearer header only, 8 KiB token
cap. Public and self-authenticating routes are listed in
`server/middleware/auth_routes.go:12-92`.

### 3.7 Observability, events, transport

| Package | Purpose | Entry file |
|---|---|---|
| `internal/events` | JSONL event bus with rotation, listeners, replay, metrics query. | `internal/events/emitter.go:16`, `event.go:29` |
| `internal/notify` | In-process workspace-scoped pub/sub bus — the internal event backbone decoupling producers from SSE hub / audit / metrics. Leaf package, stdlib only, no persistence. | `internal/notify/doc.go:1-9` |
| `internal/observability/tracing` | Canonical OTel TracerProvider/propagator/OTLP init for `loom-cli`, `loom-serve`, `loom-daemon`, `loom-agent`. | `internal/observability/tracing` (package doc); CLI wiring at `internal/cli/root.go:221-253` |
| `internal/logrouter` | Log routing, rotation, and file watching (backs the hidden `loom log-router`). | `internal/logrouter/router.go` |
| `internal/debug` | Debug/verbose/quiet mode flags. 58 lines — trivial. | `internal/debug/debug.go` |
| `internal/netutil` | Free-port allocation and `/healthz` polling. 89 lines. | `internal/netutil/freeport.go` |
| `internal/testutil` | Shared test helpers; must only be imported from `_test.go`. | `internal/testutil` (package doc) |

Tracing is off unless `LOOM_TRACE=1` or `OTEL_EXPORTER_OTLP_ENDPOINT` is set
(`internal/cli/root.go:222`); short-lived `loom-cli`/`loom-agent` runs export
synchronously so spans survive `os.Exit` (`root.go:229-231`). The service-name
mapping is at `root.go:258-271`. Contract:
`docs/observability/tracing-contract.md` (the one doc in the repo marked
"source-of-truth", mirrored in the fleet-db repo).

---

## 4. The CLI surface

Registration is decentralized: each sub-package's `init()` calls
`cli.RegisterCommand` (`internal/cli/root.go:280-287`), and `cmd/loom/main.go:13-37`
blank-imports them all. `internal/cli/data` is the exception — it cannot import
`internal/cli` (`.golangci.yml:177-186`), so `cmd/loom/main.go:43-50` registers
its commands explicitly.

**Global flags** (`internal/cli/root.go:103-108`): `--backend`, `--log-format`,
`--log-output`, `--server`, `--workspace`, `-v/--version`. `--server` and
`--workspace` are mirrored into `LOOM_SERVER_URL` / `LOOM_WORKSPACE` in
`PersistentPreRunE` (`root.go:119-128`) so flag and env resolve identically.

### 4.1 Day-one user commands

| Command | What a user does with it | Source |
|---|---|---|
| `loom init` | Guided setup: prerequisites, fleet-db, workspace validation. | `internal/cli/workspace/init.go:28` |
| `loom workspace add\|use\|set\|show\|status\|list` | fleet-db workspace lifecycle. | `internal/cli/workspace/workspacev2_cmd.go:35-80` |
| `loom repo add\|list\|show\|remove` | Repos inside a workspace. | `internal/cli/repo/repo_cmd.go:33-65` |
| `loom role add\|set\|unset\|show\|list\|remove` | Role definitions (prompt, backend, model, kind, filters). | `internal/cli/role/role_cmd.go:37-95` |
| `loom agentdef add\|list\|show\|remove\|start\|stop` | Named agent assignments in fleet-db. | `internal/cli/agentdef/agentdef_cmd.go:50-95` |
| `loom plan` / `loom task` / `loom agent` | Run worker agents (single-shot or `--auto`). | `internal/cli/agent/{plan,task,agent_cmd}.go` |
| `loom lead` | Run the interactive terminal agent (`--prompt`, `--message`). | `internal/cli/agent/lead/lead.go:45` |
| `loom daemon [profile\|status\|logs\|start\|stop\|restart\|queue\|config]` | Supervise many agents. | `internal/cli/daemon/daemon_cmd.go:71-131` |
| `loom serve` | Start the API/SSE/WS server (and optionally the SPA). | `internal/cli/serve/serve.go:101` |
| `loom monitor` (alias `status`, `mon`) | Live terminal dashboard. | `internal/cli/monitor/monitor.go:26` |
| `loom list` (alias `ls`) | Worktrees and their status. | `internal/cli/agent/list.go:16` |
| `loom push\|pull\|sync\|pr\|reset` | Git integration with AI conflict resolution. | `internal/cli/git/` |
| `loom recover` | Clear stale locks, analyze/reset an orphaned task, clean untracked files. | `internal/cli/agent/recover.go:26` |
| `loom doctor` | Health report (`--json`, `--fix`). | `internal/cli/doctor/doctor.go:65` |
| `loom usage` | Token/cost summaries from `usage.jsonl`. | `internal/cli/cleanup/usage_cmd.go:31` |
| `loom cleanup` / `loom sessions clean` | Retention purge (default 30d). | `internal/cli/cleanup/cleanup_cmd.go:24`, `sessions_cmd.go:18` |
| `loom backend list\|health\|info` | Which AI backends are installed and healthy. | `internal/cli/backends/backend_cmd.go:21-207` |
| `loom data …` | Backend-aware issue commands for agents and scripts (`ready`, `show`, `claim`, `close`, `create`, `update`, `comment`, `list`, `blocked`, `agents`, `monitor`, `agent start\|stop\|restart\|yield`). | `internal/cli/data/root.go:21` |

### 4.2 Advanced / power-user commands

| Command | Purpose | Source |
|---|---|---|
| `loom stack …` | Stacked-PR lineage and publishing. | `internal/cli/stack/stack_cmd.go:28` |
| `loom workflow …` | Author/build/approve/activate/run Flue workflows. | `internal/cli/workflow/workflow_cmd.go:50` |
| `loom driver register\|run` | Register a built Flue driver artifact; queue a run. | `internal/cli/driver/driver_cmd.go:41-63` |
| `loom trigger bindings\|events\|deliveries` | Trigger binding CRUD + audit trail. | `internal/cli/trigger/trigger_cmd.go:25` |
| `loom connector create\|list\|rotate\|grant\|audit` | Sealed-credential connectors and deny-by-default egress grants. | `internal/cli/connector/connector_cmd.go:35` |
| `loom epic run` | Drain an epic via the epic-runner workflow. | `internal/cli/epic/run.go:52` |
| `loom worker` (+ `worker profile`, `worker service`) | Run a remote agent worker that registers with a control plane. | `internal/cli/serve/worker/worker_cmd.go:36`, `profile_cmd.go:38`, `service_cmd.go:45` |
| `loom local …` | macOS desktop local runtime service. | `internal/cli/local/local_cmd.go:33` |
| `loom install-service` | Generate/install a platform-native service definition for `loom serve`. | `internal/cli/serve/install/install_service_cmd.go:35` |
| `loom workspace ops status\|diagnose\|ensure-runtime` | Workspace runtime diagnostics. | `internal/cli/workspace/ops_cmd.go:33-54` |
| `loom claim` / `loom complete` | Lock-file signalling used by agents inside auto mode. | `internal/cli/agent/claim.go:17`, `complete.go:14` |

### 4.3 Hidden / internal commands

26 commands carry `Hidden: true` and should be documented (if at all) only in
the developer guide:

| Group | Commands | Source |
|---|---|---|
| Driver ops (used by workflow runtimes over CLI transport) | `epic-get`, `epic-snapshot`, `list-agents`, `agent-orchestration-session`, `update-agent-parent`, `deliver-lead-assignment`, `deliver-agent-message`, `exec-task`, `work-task-run`, `claim-ready`, `active-task-runs`, `complete-task`, `release-task`, `recover-stale-tasks` | `internal/cli/driver/{epic,agent,task,exec}_cmd.go` |
| Claude Code hook handlers | `hooks claude-code`, `session-start`, `user-prompt-submit`, `stop`, `pre-task`, `post-task`, `yield-guard`, `session-end`, `dispatch` | `internal/cli/hooks/hooks_cmd.go:21-109`, `hooks_dispatch_cmd.go:27` |
| Other | `daemon seed-transcript`, `log-router` | `internal/cli/daemon/seed_transcript_cmd.go:34`, `internal/cli/serve/logroutercmd/logrouter_cmd.go:28` |

`loom hooks install|uninstall|status` are **not** hidden and are user-facing.

---

## 5. Extension points

A third party can plug in at six places. Only two of them are documented anywhere.

### 5.1 AI backend plugins — **undocumented**

Any executable on `PATH` named `loom-backend-<name>` is auto-discovered and
registered as a backend (`internal/cli/backends/backend_external.go:130-179`).
The pattern is explicitly modelled on `git-credential-*` / `kubectl-*`
(`backend_external.go:24-27`). Built-ins win on name collision
(`backend_external.go:162-166`); broken plugins are non-fatal
(`backend_external.go:131-133`).

Plugin contract, all verified in `backend_external.go`:

| Invocation | Purpose |
|---|---|
| `<bin> invoke --interactive` | Interactive session; prompt arrives on **stdin** (never argv, to keep it out of process listings — `:39-44`), then stdin falls through to the terminal. |
| `<bin> invoke --non-interactive` | Headless run through the harness wrapper (`:54-62`). |
| `<bin> meta --json` | Display name/version metadata (`:90`). |
| `<bin> health --json` | `{Installed, Healthy, Message}` (`:112-128`). 5s timeout (`:22`). |

Env handed to the plugin (`backend_codex.go:116-124`): filtered parent env +
`LOOM_WORKTREE_PATH`, `LOOM_AGENT_NAME`, session vars, with the loom executable's
directory prepended to `PATH`. Working examples live in `test/playground/loom-backend-playground*`
and `test/local-mode/loom-backend-localdogfood`; the only prose is
`test/playground/README.md`.

### 5.2 Prompt overrides — **undocumented**

`renderPrompt` looks for `./loom-prompts/<name>.md` in the working directory
before falling back to the embedded template, and falls back gracefully if the
override fails to execute (`internal/cli/agent/prompts.go:101-120`). Overridable
names are the eight files in `internal/cli/agent/prompts/`: `lead.md`,
`planning.md`, `task.md`, `fleet_planning.md`, `fleet_task.md`,
`conflict_resolution.md`, `pr-review.md`, `pr-review-checkout.md`. Template
variables are listed at `internal/cli/agent/prompts.go:26-43` and (partially) in
`loom agent --help`. `role.prompt_file` also accepts `builtin:<id>` selectors
(`docs/loom-glossary.md:46-51`).

### 5.3 The JS/TS SDK — documented in `sdk/README.md`, stale elsewhere

`@loom/sdk` (`sdk/package.json:2`) exports exactly four subpaths
(`sdk/package.json:6-25`): `.`, `./driver`, `./runner`, `./runtime-adapters`.

- **`./driver`** — the workflow-facing HTTP client.
  `createLoomDriverClient(options?)` / `createLoomClient` (`sdk/driver.d.ts:480-481`),
  `LoomDriverClient.fromEnv()` (`:392`). Namespaces (`:400-440`): `epics`
  (`get`/`snapshot`/`watch` — `watch` is an SSE `AsyncGenerator` with
  `Last-Event-ID` resume), `agents`, `tasks`, `taskRuns`, `connectors`, `events`
  (`await`/`list`), `workflows` (`start`/`await`), plus result helpers
  `completed()`/`failed()`/`needsReview()` (`:442-450`) and flat aliases for older
  workflows (`:451-477`). Errors are `DriverApiError{code, retryable, status, details}`
  over a frozen 25-code union (`:29-59`, `:382-389`).
- **`./runner`** — the task-run harness client: `TaskRunClient` with
  `logs.append`, `artifacts.{declare,get,list}`, `runtimeCredentials.get`,
  `heartbeat`, `completeRun` (`sdk/runner.d.ts:243-283`).
- **`./runtime-adapters`** — Flue-event → transcript/usage/log conversion plus
  redaction (`sdk/runtime-adapters.d.ts:35-46`).

The wire contract is frozen in `sdk/api-surface.v1.json`
(`"contract": "loom-driver-sdk/v1"`, `:2-3`): auth modes (`:5-20`), error
envelope + codes + `neverRetryable: ["token_expired"]` (`:21-52`), result
statuses (`:53`), watch SSE (`:55-63`), await rules (`:64-72`), **20 op paths**
(`:73-116`), and the client namespace/connector-method map (`:117-147`). It is
enforced from both sides — `sdk/contract.test.mjs` and
`internal/webui/handlers/driverapi/contract_test.go`.

**Not published to npm.** `sdk/CHANGELOG.md:6-9` and `sdk/README.md:221-234` both
say publishing is deferred; until then the documented path is to vendor
`driver.js` (single file, no local imports) — `sdk/README.md:28-30`. There is no
`npm publish` step in `.github/workflows/`.

Runnable examples: `sdk/examples/epic-runner-watch.mjs`,
`sdk/examples/task-fan-out.mjs`.

### 5.4 Workflow drivers (Flue bundles)

A **Driver** is a registered TypeScript program; a **DriverVersion** is one
immutable built Flue bundle; a **DriverRun** is one execution of a pinned version
(`internal/domain/platform.go:64-102`). Authoring path:

`loom workflow clone <name>` → edit TS → `loom workflow build` →
`loom workflow approve` → `loom workflow activate` → `loom workflow run`
(`internal/cli/workflow/workflow_cmd.go:56-119`), or `POST
/api/workspaces/{ws}/workflows/{name}/versions` for HTTP submission
(`internal/webui/handlers/workflows/module.go:34`).

Built-in workflows — exactly two (`internal/workflows/workflows.go:79-82`):
`epic-runner` and `github-review-agent`, each bundling sibling task-runner files
(`workflows.go:95-107`). `openshell-task-runner` is a deprecated fail-closed stub
filtered out of derived manifests (`workflows.go:402-404,428-430`).

Building a bundle needs the **flue** toolchain: `flue build --target node`
(`workflows.go:732-752`), resolved from `LOOM_REAL_FLUE_CMD_JSON` /
`LOOM_REAL_FLUE_CMD` / `PATH` (`workflows.go:754-773`), with `@flue/runtime`
resolved from `LOOM_FLUE_RUNTIME_ROOT` / `FLUE_RUNTIME_ROOT` / `FLUE_REPO`
(`workflows.go:689-710`) and `@loom/sdk` from `LOOM_SDK_ROOT` or `./sdk`
(`workflows.go:674-687`). The flue commit is pinned in
`internal/workflows/FLUE_COMMIT` and enforced by
`scripts/rebuild-builtin-bundle.sh:23-37`. Generated bundles are gitignored
(`.gitignore:12`, matching `AGENTS.md:37-42`).

**Trust and sandboxing** (all verified against `AGENTS.md:85-154`):

| Claim | Verdict | Evidence |
|---|---|---|
| `trust_level` is `trusted`/`untrusted`, missing = untrusted | ✅ | `internal/domain/platform.go:44-62,73` |
| Executor refuses an untrusted bundle outside an isolating launcher, `errorClass=sandbox_required`, nothing spawned, enforced regardless of `LOOM_DRIVER_SANDBOX` | ✅ | `driver/sandbox/policy.go:27,75-93`; call site `driver/executor.go:755-757` |
| No self-elevation; untrusted re-registration *demotes* | ✅ | `driver/register.go:574-589` |
| Built-in epic-runner stamps trusted | ✅ | `internal/workflows/workflows.go:177` |
| HTTP workflow submissions stamp untrusted | ✅ | `workflows.go:329-334,390` |
| "Operator/CLI registration … stamp trusted" | ⚠️ **partly wrong** — the Go API default is trusted (`driver/register.go:538-543`) but the **CLI defaults to untrusted** unless `--trusted` (`internal/cli/driver/driver_cmd.go:164-172`, help at `:105-106`) | |
| Elevation via `PATCH .../drivers/{id}` with `{"trust_level":"trusted"}` | ⚠️ **UNVERIFIED here** — no such route in `internal/webui`; the client side exists (`internal/store/platform_store.go:41-43`, `internal/infra/fleetdb/platform.go:822`). The route belongs to fleet-db. | |
| "step-9 backfill stamps pre-existing driver rows trusted exactly once" | ❌ **UNVERIFIED** — only referenced in comments (`driver/sandbox/policy.go:8-9`, `domain/platform.go:57-59`); no backfill code in loomcli. | |
| `LOOM_DRIVER_SANDBOX=container`, podman-first/docker-fallback, default `process` | ✅ | `driver/sandbox/container.go:67,105-123,159-170`, default at `:107-109` |
| Sandbox image/runtime/binary/caps env vars and defaults | ✅ | `container.go:70-80,95-98,308-310,324-326` |
| Egress modes `all\|serve-only\|none\|delegated`; empty resolves trusted→`all`, untrusted→`serve-only` | ✅ | `driver/sandbox/egress.go:53,58-73,98-112` |
| `serve-only` = `--network=none` + unix-socket relay, `LOOM_DRIVER_API_URL` rewritten to `http://127.0.0.1:8484` | ✅ | `egress.go:92,154-200,195,241,266-332,411-441` |
| Placement record carries `egress_mode` + `egress_mechanism` | ✅ | `container.go:243-244`, `domain/platform.go:551-552` |
| Test paths `go test ./internal/driver -run TestContainerLauncherPodmanIntegration` / `TestContainerEgressPodmanIntegration` / `TestSandboxEgressForwarderHostNode` | ⚠️ **stale paths** — those tests live in `internal/driver/sandbox/` (`sandbox/container_test.go:489`, `sandbox/egress_test.go:331,417`), so `./internal/driver` will not match them. | |
| `go test ./internal/driver -run TestStep9`, `scripts/test-step9-sandbox.sh` | ✅ | `internal/driver/step9_e2e_test.go:155,232,439,523`; `scripts/test-step9-sandbox.sh:115-134` |
| SELinux policy-module guidance | **UNVERIFIED as code** — deployment lore; `egress.go:28-32` carries matching comments but nothing enforces it in-repo. | |

`internal/driver/env.go` is worth calling out in the dev guide: it is the
allow-list/deny-list that decides what parent env reaches a driver runtime
(`env.go:3-40` allowlist, `:40-75` sensitive exacts/prefixes/fragments), with one
deliberate widening for the local task runner
(`env.go:78-99,110-124` — ANTHROPIC/CLAUDE_CODE_OAUTH/OPENAI/CODEX/GEMINI/GOOGLE/CURSOR/GITHUB_TOKEN/GH_TOKEN).

### 5.5 Triggers and webhooks — partially extensible

- **Trigger bindings are user data**: created/updated via
  `loom trigger bindings create|update` (`internal/cli/trigger/trigger_cmd.go:60-81`)
  with glob route keys (`internal/trigger/pattern.go:12-21`), subject-key
  templates (`subject_key.go:9-14`), cron sources
  (`cron.go:22-30,52-60`), and internal-event loopback with a hop cap
  (`internal_source.go:52-70`).
- **Webhook adapters are not user-extensible**: `defaultRegistry()` is a Go map
  containing exactly one adapter, `github`
  (`internal/webui/handlers/webhooks/adapter.go:75-79`). Route:
  `POST /api/workspaces/{ws}/webhooks/{name}` (`webhooks/module.go:45`).
- The issue-journal bridge (`internal/trigger/issue_journal_bridge.go:3-44`)
  carries a self-trigger warning that belongs in user docs: bindings on
  `internal.issue.created` **must** set `exclude_actor_kinds`.

`docs/design/2026-06-07-trigger-workflow-proposal.md` is **largely implemented
and then some** — the proposal's "deferred: schedule runner" is now
`internal/trigger/cron.go`, and its "missing: UI/API for managing trigger
bindings" is now `loom trigger`.

### 5.6 Connectors — configurable, not extensible

Three providers only, registered in Go: `github`, `slack`, `datadog`
(`internal/connector/registry_default.go:30-38`; source kinds closed-switched at
`internal/domain/connector.go:35-46`). Adding a fourth requires a Go change. What
*is* configurable: connector credentials (sealed, stdin-only), deny-by-default
egress grants (binding × action × resource), rotation, and the audit journal —
all via `loom connector` (`internal/cli/connector/connector_cmd.go:35-467`). Base
URLs are overridable per provider via
`LOOM_CONNECTOR_{GITHUB,SLACK,DATADOG}_BASE_URL`
(`registry_default.go:16-23`). All calls funnel through one choke point that
emits exactly one audit record for granted *and* refused calls
(`internal/connector/dispatch.go:14-29`).

`docs/design/2026-06-07-slack-agent-service-proposal.md` is **still a proposal**:
the `AgentService` storage model exists, but there is no Socket Mode anywhere in
`internal/**.go`, no slack-agent driver, and no Slack ActionLedger types. What
shipped instead is the connector egress path (`slack.chat.post`,
`slack.conversations.read` — `sdk/api-surface.v1.json:136-139`).

---

## 6. Configuration and data

### 6.1 There is no config file

**There is no `loom.yaml` in production code.** `.gitignore:41` still ignores it
and `yaml:` struct tags survive on `LoomConfig` / `DaemonSettings`
(`internal/cli/config/config.go:21-57`, `project.go:20-28`), but there are zero
`yaml.Unmarshal` / `yaml.NewDecoder` calls in non-test Go. The only yaml call is
`yaml.Marshal` for *display* at `internal/cli/daemon/daemon_cmd.go:569`. The code
says so directly at `internal/cli/backend.go:80-81`: settings "live in FleetDB
daemon profiles and are applied by the daemon, not read from local YAML during
CLI startup." `LoadDaemonConfig` (`internal/cli/config/project.go:165,193-240`)
overlays the fleet-db `DaemonProfile` onto built-in defaults.

Consequence for the user docs: **configuration means fleet-db objects plus
environment variables**, edited through the noun-verb CLI (`loom workspace`,
`loom repo`, `loom role`, `loom agentdef`, `loom daemon profile`), not a file.

### 6.2 Where state lives

`LoomDir()` resolves in this order (`internal/bootstrap/paths.go:39-51`):
`LOOM_CONFIG_DIR` → a per-process temp dir when running under `go test`
("tests must NEVER touch the real `~/.loom`", `paths.go:29-33`) → `$HOME/.loom`.

| Path under `~/.loom/` | Contents | Written by |
|---|---|---|
| `state.json` | Regenerable cache: `last_workspace`, per-workspace path/repos/agent worktrees. Explicitly "never load-bearing for correctness". | `internal/bootstrap/statecache.go:19-52,108,118,133` |
| `workspaces/<name>/` | Per-workspace runtime dir. | `internal/bootstrap/paths.go:68-74` |
| `fleet-db/embedded.lock` | Exclusive flock around the embedded fleet-db. | `internal/bootstrap/embedded.go:81` |
| `fleet-db/runtime.json` | `{pid,url,redis_addr,redis_external,redis_db,redis_tls,redis_config_hash,snapshot_path,started_at}` — enables reuse instead of respawn. | `internal/bootstrap/embedded.go:129,149-171` |
| `fleet-db/redis-snapshot.json` (+ `.bak`) | **The local-mode backup artifact.** | `internal/webui/localredis/manager.go:244,269` — see §6.3 |
| `terminal-state/snapshot.json` (+ `.bak`) | Terminal tab labels/pinning/ordering/notes/per-issue layouts. | `internal/cli/serve/daemonwire/localredis.go:20` |
| `local-settings.json` (`0600`, dir `0700`) | Desktop-local settings: external Redis config, agent runtime default, sealed runtime credentials. | `internal/localsettings/settings.go:24,120,170-172` |
| `runtime-credentials.key`, `connector-vault-key` | Sealing keys. | `settings.go:25,361`; `internal/connector/vault.go:30` |
| `stacks.json`, `stack-locks/<id>/` | Stack lineage + per-stack locks. | `internal/stackstore/stackstore.go:85`; `internal/cli/epic/epic_reconcile.go:86` |
| `tokens/<sha256(serverURL)>.json` (dir `0700`) | Cached device-flow JWTs per server. | `internal/httpclient/token_cache.go:24-38,57` |
| `usage.jsonl` | Token/cost records read by `loom usage`. | `internal/usage/store.go:27` |
| `events/events-*.jsonl` | Event bus JSONL (override with `LOOM_EVENTS_DIR`). | `internal/cli/agent_event_bus.go:41-50` |
| `logs/<workspaceID>/agents/<agent>.log` | Agent archive logs. | `internal/cli/agent/archive_log.go:13` |
| `issue-bridge-cursor.json` | Issue-journal bridge cursor. | `internal/cli/serve/serve_loops.go:327` |
| `daemon.lock`, `loom.sock`, `bin/fleet-db` | Daemon lock, RPC socket, optional fleet-db binary drop. | `internal/cli/daemon_runtime.go:44`; `internal/rpc/socket_path.go:57`; `internal/bootstrap/embedded.go:569` |

Separately, a **per-project** `.loom/` holds `daemon.pid`, `daemon.lock`,
`daemon-agents.json`, `logs/`, `events/`
(`internal/cli/config/project.go:197-199,604`), and worktrees are computed
structurally, not configured:
agent `<ws>/worktrees/<repo>/<agent>`, task-run
`<ws>/.loom/task-worktrees/<repo>/<taskRunID>`, PR-review
`<ws>/.loom/pr-worktrees/<repo>/pr-<n>` — each validated against path escape
(`internal/localworkspace/localworkspace.go:39-41,61,64-66,87,90-92`).

Sessions live under `<workspaceRuntimeDir>/sessions/` with
`index.jsonl`, then per-session `metadata.json`, `prompt.txt`,
`agent_transcript.jsonl`, `events.jsonl`
(`internal/sessions/store.go:24-52`, `query.go:21,150,224`). Session IDs are
`YYYYMMDD-HHMMSS-<agent>-<taskshort>-<8hex>` (`internal/sessions/id.go:14-36`).

### 6.3 Storage modes — README table verified

| README claim (`README.md:310-322`) | Verdict | Evidence |
|---|---|---|
| Local is default; trigger is `LOOM_FLEET_DB_URL` unset | ✅ | `internal/bootstrap/mode.go:54-59` — `DetectMode()` is "the only place that distinction is made" (`:51-53`) |
| Local spawns an embedded fleet-db subprocess per CLI invocation | ✅ with nuance — a **healthy existing runtime is reused**, not respawned | `openstore.go:122-160`, `embedded.go:183-212` |
| Backed by in-process miniredis with a JSON snapshot at `~/.loom/fleet-db/redis-snapshot.json` | ✅ | `embedded.go:339,350-355` |
| "The miniredis snapshot is the source of truth for backups — copy that file" | ✅ **but incomplete** — when external Redis is configured, *no snapshot is written at all* | `embedded.go:258-263,346,356-358` |
| Cloud mode: `LOOM_FLEET_DB_URL=<https://…>`, loom is purely a client | ✅ | `mode.go:34-36`, `openstore.go:112-120` |
| Cloud "requires `LOOM_FLEET_DB_API_KEY`, or `LOOM_FLEET_DB_ACTOR` in dev mode" | ⚠️ not enforced client-side; both are optional, `fleetdb.New` only errors on empty `BaseURL` | `openstore.go:95-96`, `internal/infra/fleetdb/client.go:106-108` |
| `internal/infra/memstore` is test-only | ✅ **mechanically enforced** — a build-guard test AST-parses the repo and fails on any non-test import | `internal/infra/memstore/import_guard_test.go:15-51` |
| `~/.loom/state.json` is a regenerable cache | ✅ verbatim match with the code comment | `internal/bootstrap/statecache.go:19-32` |

### 6.4 The embedded fleet-db, precisely

`internal/bootstrap/embedded.go:310` `StartEmbedded` is the whole story:

- Binary discovery order (`embedded.go:526-580`): `FLEET_DB_BIN` → `fleet-db` on
  `PATH` → sibling of the loom executable → `<LoomDir>/bin/fleet-db`. **Nothing
  downloads it** — `npm/install.js:63` and `scripts/install.sh:12` fetch only
  `loom`.
- Spawn argv (`embedded.go:373-378`): `--redis-durability-profile=managed
  --auth-dev-mode --authz-enabled=false --rpc-enabled=false`.
- Child env (`embedded.go:379-400`): `FLEET_SERVER_ADDR`, `FLEET_REDIS_ADDR`,
  pool defaults (`FLEET_REDIS_POOL_SIZE=100`, `FLEET_REDIS_MIN_IDLE_CONNS=10`),
  optional `LOOM_TRACE_PARENT`, and `FLEET_REDIS_{DB,TLS_ENABLED,PASSWORD}` when
  external Redis is configured. Note the `FLEET_` prefix — these are *not*
  `LOOM_*` vars.
- Detached process group so Ctrl-C on loom doesn't double-signal
  (`embedded.go:408`); health gate `WaitForHealthz` with a 30 s ceiling
  (`:268-273,435`).

### 6.5 Auth surfaces — there are four, and they are unrelated

| Surface | Credential | Where enforced |
|---|---|---|
| **loom web/API server** | `open` or `oidc` (RS256 JWT via JWKS from `<auth-url>/api/auth/jwks`) | `internal/authmode/authmode.go:4,7`; `internal/webui/server/middleware/auth.go:113`; CLI side discovers mode via `GET /api/config` and caches device-flow JWTs in `~/.loom/tokens/` (`internal/httpclient/client.go:30,55-64`) |
| **fleet-db** | `LOOM_FLEET_DB_API_KEY` → sent as **both** `X-API-Key` and `X-Fleet-API-Key`; `LOOM_FLEET_DB_ACTOR` → `X-Actor` | `internal/fleethttp/fleethttp.go:36-48`; actor fallback chain `LOOM_FLEET_DB_ACTOR` → `LOOM_AGENT_NAME` → `$USER` (`internal/bootstrap/openstore.go:176-185`). The embedded fleet-db runs `--auth-dev-mode --authz-enabled=false` so `X-Actor` alone suffices (`embedded.go:370-371`) |
| **driver-op API** | Run-scoped HS256 bearer (`LOOM_RUN_TOKEN`) **or** the legacy "header quad" `X-Loom-Driver-{Run-Id,Node-Id,Lease-Id,Fencing-Token}` | `internal/webui/handlers/driverapi/module.go:49-52`, `token_auth.go:19-26`; minted at `internal/driver/run.go:253-273`, injected at `internal/driver/executor.go:804-829` |
| **fleet worker registration** | `X-Fleet-API-Key`, gated by `LOOM_FLEET_API_KEY` / `--fleet-api-key` | `internal/webui/fleet/handlers.go:70-74`, `internal/cli/serve/serve.go:168` |

### 6.6 Environment variables

The README documents **5** env vars (`README.md:349-355`). The Go code reads
roughly **120**, and nothing under `docs/` documents them. Grouped inventory
(defaults cited where code sets one; "—" = no default):

**Control plane / bootstrap / auth**

| Var | Default | Read at |
|---|---|---|
| `LOOM_CONFIG_DIR` | `$HOME/.loom` (temp dir under `go test`) | `internal/bootstrap/paths.go:40` |
| `LOOM_FLEET_DB_URL` | unset ⇒ local mode | `internal/bootstrap/mode.go:23,55`; `openstore.go:53,113` |
| `LOOM_FLEET_DB_API_KEY` | — | `internal/bootstrap/openstore.go:18,95` |
| `LOOM_FLEET_DB_ACTOR` | → `LOOM_AGENT_NAME` → `$USER` | `openstore.go:25,176-185` |
| `LOOM_WORKSPACE` | — (`ErrNoActiveWorkspace`) | `internal/bootstrap/mode.go:19,77` |
| `LOOM_WORKSPACE_ID` / `LOOM_WORKSPACE_RUNTIME_DIR` | `"default"` for tmux naming / — | `internal/workspace/id.go:24`; `internal/cli/worktree_resolve.go:577` |
| `LOOM_SERVER_URL` | — | `internal/cli/issue_backend_resolve.go:55,177` |
| `LOOM_ISSUE_BACKEND` | `fleetdb` (validated against `fleetdb\|fleet\|api`) | `internal/cli/fleet_mode.go:20,30,63`; `issue_backend_resolve.go:24,34-55` |
| `LOOM_FLEET_URL`, `LOOM_FLEET_API_KEY`, `LOOM_FLEET_ACTOR`, `LOOM_FLEET_JWT_KEY`, `LOOM_FLEET_MODE` | `""` / `""` / → `LOOM_AGENT_NAME` / — / `"true"` enables | `internal/cli/config/config_fleet.go:23-37`; `internal/cli/serve/serve.go:41,167,168` |
| `LOOM_FLEETDB_REDIS_URL`, `_REDIS_PASSWORD`, `_WORKSPACE`, `_AUTO_START` | `""` / `""` / `""` / `false` | `internal/cli/config/config_fleetdb.go:27-40` |
| `LOOM_FLEET_DB_REDIS_ADDR\|_PASSWORD\|_DB\|_TLS` | — | `internal/localsettings/settings.go:33-36` |
| `LOOM_AUTH_URL`, `LOOM_AUTH_ISSUER`, `LOOM_AUTH_AUDIENCE` | `""` / `--auth-url` / `"loom"` | `internal/cli/serve/serve.go:175-177` |
| `LOOM_CONNECTOR_VAULT_KEY`, `LOOM_CONNECTOR_{GITHUB,SLACK,DATADOG}_BASE_URL`, `LOOM_CONNECTOR_UPSTREAM_TIMEOUT` | key file / provider prod URLs / — | `internal/connector/vault.go:28`; `registry_default.go:16-23`; `internal/webui/app/server_connectors.go:19` |

> Note: `FLEET_DB_BIN`, `FLEET_SERVER_ADDR`, `FLEET_REDIS_*` are **`FLEET_`**-prefixed,
> not `LOOM_` (`internal/bootstrap/embedded.go:29,37-38,380-381`).

**AI backend / agent runtime**

| Var | Default | Read at |
|---|---|---|
| `LOOM_BACKEND` | **`codex`** | `internal/cli/backend.go:88-91`, `internal/backendnames/names.go:5` |
| `LOOM_AGENT_EFFORT` → `LOOM_CLAUDE_EFFORT` | `""` | `internal/cli/backends/backend_effort.go:9,12` |
| `LOOM_OPENCODE_MODEL`, `LOOM_MAX_BUDGET_USD`, `LOOM_DEBUG_STREAM`, `LOOM_NO_EXTERNAL_BACKENDS`, `LOOM_LEAD_CONTROLLED` | `""` / — / off / off / — | `backend_opencode.go:147`, `backend_claude.go:52,204`, `backend_external.go:182`, `harness_lead_runtime.go:17,25` |
| `LOOM_TRANSCRIPT_MODE`, `LOOM_EVENTSTORE_WRITE`, `LOOM_SERVE_FROM_EVENTSTORE`, `LOOM_REDACT_TRANSCRIPTS` | off / `false` / off / off | `backends/transcript_flags.go:25-44`; `internal/webui/svcimpl/eventstore_serving.go:19`; `internal/sessions/native_transcript.go:18` |
| `LOOM_COST_PER_MTOK_INPUT` / `_OUTPUT` | `DefaultPricing[backend]`, fallback claude | `internal/usage/usage.go:73,78` |
| `LOOM_AGENT_NAME`, `LOOM_AGENT_ROLE`, `LOOM_ROLE*`, `LOOM_AGENT_PATH_PATTERNS`, `LOOM_AGENT_REPO`, `LOOM_SOURCE_REPOS`, `LOOM_ASSIGNED_TASK_ID`, `LOOM_WORKTREE_PATH`, `LOOM_READ_ONLY`, `LOOM_ALLOWED_TOOLS`, `LOOM_DENIED_TOOLS`, `LOOM_YIELD_FILE` | — (injected at spawn) | `internal/cli/task_router.go:188-223`; `internal/cli/daemon/supervisor/spawn.go:40-44,118-140`; `internal/cli/agent/prompts.go:420-523` |
| `LOOM_SESSION_ID`, `LOOM_ORCHESTRATOR_SESSION_ID`, `LOOM_AGENT_TERMINAL_ID`, `LOOM_AGENT_LEASE_ID`, `LOOM_AGENT_LEASE_TOKEN` | — | `internal/cli/agent/lead/lead.go:31-33`; `internal/cli/issue_backend_resolve.go:106-107` |
| `LOOM_FIXED_POLLING`, `LOOM_RESUME_TTL`, `LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS`, `LOOM_TASK_QUARANTINE_THRESHOLD`, `LOOM_DAEMON_SOCKET`, `LOOM_DAEMON_LEAF[_RUNNER]` | off / `30m` / profile then `900` / default (≤0 disables) / — / `"ts"` | `automode_poller.go:18`; `agent/daemon_resume.go:14`; `supervisor/restart.go:385-394`; `supervisor/quarantine.go:123-129`; `issue_backend_resolve.go:101`; `agent/tsruntime/tsruntime.go:33,53` |
| `LOOM_DEFAULT_BRANCH` | `main` | `internal/cli/worktree.go:128,140` |

**Driver / sandbox / task-run** — see also the sandbox table in §5.4.

| Var | Default | Read at |
|---|---|---|
| `LOOM_DRIVER_EXECUTOR` | enabled (`0/false/off/no` disables) | `internal/cli/serve/serve.go:42,449-455` |
| `LOOM_DRIVER_TASK_WORKER_CONCURRENCY` / `_TASK_RUN_MAX_ATTEMPTS` / `_STALE_TASK_MAX_AGE` | `2` (clamp 1–32) / `2` (clamp 1–10) / — | `serve.go:43-45,458,462` |
| `LOOM_DRIVER_API_URL` / `LOOM_DRIVER_API_TOKEN` | loopback `http://<bind>:<port>` / `""` | `serve.go:415,431`; SDK `sdk/driver.js:66-67` |
| `LOOM_RUN_TOKEN`, `LOOM_RUN_TOKEN_SIGNING_KEY`, `LOOM_RUN_TOKEN_TTL` | minted per run / ephemeral per-process / `24h` | `internal/driver/run.go:201-222,328,349`; injected `executor.go:826-829` |
| `LOOM_DRIVER_LEGACY_AUTH_ENV` | **ON** (only `0/false/off/no` disables) | `internal/driver/executor.go:682,692-704` |
| `LOOM_DRIVER_SANDBOX` | `process` | `internal/driver/sandbox/container.go:67,106-109` |
| `LOOM_DRIVER_SANDBOX_{BINARY,IMAGE,RUNTIME,MEMORY,CPUS,PIDS_LIMIT}` | podman→docker / `docker.io/library/node:22-slim` / engine default / `1g` / `1.0` / `256` | `container.go:70-80,95-98` |
| `LOOM_DRIVER_SANDBOX_EGRESS` | trusted→`all`, untrusted→`serve-only` | `internal/driver/sandbox/egress.go:53,98-112` |
| `LOOM_SANDBOX_RELAY_SOCKET` / `_PORT` | container port `8484` | `egress.go:87-92` |
| `LOOM_AWAIT_MAX_TIMEOUT_MS` / `_MAX_PER_RUN` / `_TOTAL_SUSPEND_CAP_MS` | 14 d / `100` / 30 d | `internal/driver/await_op.go:33-54` |
| `LOOM_COMPOSITION_MAX_DEPTH` | `4` | `internal/driver/composition.go:53,57` |
| `LOOM_DRIVER_{NODE_ID,LEASE_ID,RUN_ID,WORKSPACE,FENCING_TOKEN,STEP_ID,RUNNER_ID}` | — | `internal/cli/driver/run_context.go:41-94` |
| `LOOM_TASK_RUN_{ID,LEASE_TOKEN,API_URL}`, `LOOM_RUNNER_LEASE_TOKEN`, `LOOM_PARENT_SESSION_ID` | — | `internal/cli/driver/exec_cmd.go:139-197`; SDK `sdk/runner.js:4-17` |
| `LOOM_WORKER_{CONTROL_PLANE,WORKSPACE,AGENT,BACKEND,TOKEN}` | — | `internal/cli/serve/worker/worker_cmd.go:69-72,121`; `LOOM_WORKER_TOKEN` also gates `/api/internal/workers/*` at `internal/webui/app/server_workspace.go:125` |
| `LOOM_SDK_ROOT`, `LOOM_FLUE_RUNTIME_ROOT`, `LOOM_REAL_FLUE_CMD[_JSON]`, `LOOM_FLUE_AGENT_MODEL` | — | `internal/workflows/workflows.go:675,691,755,765`; `internal/driver/env.go:21` |

**serve / webui / observability**

| Var | Default | Read at |
|---|---|---|
| `LOOM_SERVER_PORT` / `LOOM_BIND_ADDR` | `8080` / `127.0.0.1` | `internal/cli/serve/serve.go:148-157` |
| `LOOM_CORS_ORIGIN`, `LOOM_FRONTEND_URL`, `LOOM_FRONTEND_DIR` | `""` | `serve.go:88,160,162` |
| `LOOM_REDIS_ADDR` / `LOOM_REDIS_PASSWORD` | `""` | `serve.go:165-166` |
| `LOOM_SENTRY_DSN`, `LOOM_DISABLE_H2C` | `""` / `"1"` disables h2c | `serve.go:170`; `internal/webui/app/server.go:273` |
| `LOOM_WEBUI_URL`, `LOOM_NOTIFY_TOKEN`, `LOOM_WEBUI_GITHUB_TOKEN` | `http://127.0.0.1:8080` / `<runtimeDir>/notify.token` / — | `internal/cli/backends/backend_session_env.go:128-149`; `internal/webui/handlers/prreview/module.go:22` |
| `LOOM_TRIGGER_SWEEP_INTERVAL` / `_BATCH`, `LOOM_AWAIT_SWEEP_INTERVAL` / `_BATCH`, `LOOM_TRIGGER_CRON_INTERVAL`, `LOOM_TRIGGER_HOP_DEPTH_CAP` | `15s` (cap 3600) / `50` (cap 500) / `30s` / `50` / `30s` / `4` | `internal/cli/serve/serve_loops.go:141-191`; `serve.go:46`; `internal/trigger/internal_source.go:49-53` |
| `LOOM_ISSUE_BRIDGE_INTERVAL` / `_DISABLED` / `_STATE_PATH` / `_REPLAY` | `2s` / off / `<LoomDir>/issue-bridge-cursor.json` / off | `serve.go:47-49`; `serve_loops.go:314-327`; `internal/trigger/issue_journal_bridge.go:78` |
| `LOOM_STALE_CHECK_INTERVAL` / `_THRESHOLD` / `_LEADER_TTL` | `15s` / `5m` / `30s` | `internal/kv/stale.go:34-51` |
| `LOOM_EVENTS_DIR` | `<LoomDir>/events` | `internal/cli/agent_event_bus.go:41-50` |
| `LOOM_TRACE`, `LOOM_ENV`, `LOOM_TRACE_PARENT`, `LOOM_DEBUG`, `LOOM_DEBUG_RPC` | off / `""` / — / off / off | `internal/cli/root.go:222,235`; `internal/observability/tracing/tracing.go:42`; `internal/debug/debug.go:9`; `internal/rpc/client.go:23` |

**Desktop / local runtime**

| Var | Default | Read at |
|---|---|---|
| `LOOM_LOCAL_RUNTIME` | `""`; values `desktop` \| `headless` | `internal/cli/serve/serve.go:54-57,252` |
| `LOOM_DESKTOP_DATA_DIR` | macOS `~/Library/Application Support/Loom/data`, else `~/.loom/desktop` | `serve.go:55,255`; `internal/cli/local/runtime.go:116-128` |
| `LOOM_PR_GIT_PASSWORD` | — (set then scrubbed) | `internal/stackpublish/origin_checkout.go:45`, `scrub.go:19-20` |

The Tauri desktop shell itself reads **no** `LOOM_*` vars; it only injects
`window.__LOOM_NEEDS_RELOCATION__` (`desktop/src-tauri/src/lib.rs:49`).

**Test-only gates** (do not document externally): `LOOM_RUN_EMBEDDED_SMOKE`,
`LOOM_INTEGRATION_TESTS`, `LOOM_FAKE_CLAUDE_TUI`, `LOOM_SANDBOX_PODMAN_TEST`,
`LOOM_HOST_BRIDGE_HELPER`, `LOOM_REAL_FLUE_TEST`, `LOOM_LIVE_DAYTONA[_PR]`,
`LOOM_LIVE_BACKEND`, `LOOM_RUN_DAYTONA_E2E[_PR_E2E]`, `LOOM_DAYTONA_E2E_MODEL`,
`LOOM_STACK_E2E[_REPO]`, `LOOM_E2E_GITHUB_REPO`, `LOOM_BIN`, `LOOM_BASE_URL`,
`LOOM_PLAYGROUND_REQUIRE_SERVE`, `LOOM_TEST_{HELPER_MODE,ROOT_PID,CHILD_PID_FILE}`,
`LOOM_STEP9_SANDBOX`.

**Verified README env-table errors:**

1. `README.md:351` says `LOOM_BACKEND` defaults to `claude`. It defaults to
   **`codex`** (`internal/cli/backend.go:29,91`).
2. `README.md:261` says resolution is "flag > env > workspace daemon profile >
   default claude". `ResolveBackendName` (`internal/cli/backend.go:82-91`) has
   **no daemon-profile step** — the profile is materialised into `--backend`
   argv / `LOOM_BACKEND` env by the *spawner*
   (`internal/cli/daemon/supervisor/spawn.go:93-112`,
   `internal/webui/handlers/terminal/agent_session.go:377,385-387,437-439`), so
   the chain is assembled across two processes.
3. `README.md:353` documents `LOOM_WORKTREES_DIR` (default `./worktrees`). **No
   non-test Go code reads it.** Worktree paths are computed structurally
   (`internal/localworkspace/localworkspace.go:39-41`); the only production
   reference left is a stale flag help string at
   `internal/cli/workspace/init.go:54`.
4. `README.md:246-249` lists three backends. Five are registered — `claude`,
   `codex`, `cursor`, `gemini`, `opencode` (`internal/cli/backends/backend_{claude,codex,cursor,gemini,opencode}.go`),
   confirmed by running `loom backend list`.

### 6.7 Env firewall for spawned runtimes

`internal/driver/env.go` is the allow/deny list that decides what parent env
reaches a driver runtime, and belongs in the security section of the dev guide:

- **Allowlist (exact)**: `PATH HOME PWD OLDPWD TMPDIR TMP TEMP TERM USER LOGNAME
  SHELL TZ LANG` plus `LOOM_CONFIG_DIR`, `LOOM_FLUE_AGENT_MODEL`,
  `LOOM_HOST_BRIDGE_HELPER`, `LOOM_DRIVER_TASK_RUNNER_CMD{,_JSON}`
  (`env.go:5-27`), prefix `LC_` (`:29-31`).
- **Denylist**: every fleet-db/lease/worker credential (`env.go:33-56`), prefixes
  `AWS_ AZURE_ GCP_ GOOGLE_ FLEET_ GIT_CONFIG_` (`:58-65`), fragments
  `SECRET TOKEN PASSWORD PRIVATE_KEY ACCESS_KEY API_KEY` (`:67-74`).
- **One deliberate widening**: `localTaskRunnerBaseEnv` (`env.go:82-100,111-123`)
  re-admits provider credentials (ANTHROPIC / CLAUDE_CODE_OAUTH / OPENAI / CODEX /
  GEMINI / GOOGLE / CURSOR / GITHUB_TOKEN / GH_TOKEN) **only** for the local task
  runner. Remote/Daytona keeps the strict filter.

---

## 7. Runtime topology

Four supported shapes. All four use fleet-db as the control plane; they differ in
who starts what.

### A. Bare CLI, local mode (zero install beyond `loom` + `fleet-db`)

```
loom <cmd>  ──►  embedded fleet-db subprocess  ──►  in-process miniredis
                       (reused if healthy)              └─► ~/.loom/fleet-db/redis-snapshot.json
```

Every `loom <cmd>` boots or reuses a fleet-db subprocess against the same
on-disk snapshot (`internal/bootstrap/embedded.go:347-349,183-212`). The user
needs: the `loom` binary and a `fleet-db` binary reachable via `FLEET_DB_BIN`,
`PATH`, the loom binary's directory, or `~/.loom/bin/`
(`internal/bootstrap/embedded.go:526-580`). Nothing downloads `fleet-db`.

### B. `loom serve` + external frontend

```
browser ──► static frontend (nginx / CDN / Vite preview)
              └─ /api/* proxied ──► loom serve (:8080) ──► fleet-db ──► Redis/miniredis
```

`loom serve` is described by its own help as "a pure JSON API / SSE / WebSocket
server. The frontend is served externally." It serves the SPA **only** when
`--frontend-dir` / `LOOM_FRONTEND_DIR` points at a built `dist/`
(`internal/webui/app/frontend.go:11-16`); otherwise it logs "api-only mode —
frontend served externally" (`app/server_app.go:99-103`) and non-`/api` paths
404. `deploy/README.md` documents two shapes: same-origin (nginx serves the SPA
and proxies `/api/*`, `deploy/docker-compose.yml`) and cross-origin (frontend on
a CDN with `VITE_API_BASE_URL` baked in, server run with `--frontend-url`).

Optional add-ons on the serve process: `--fleet-mode` for multi-node task
coordination (`README.md:76-89`), `--redis-addr` for shared terminal state,
`--auth-url` for JWT auth, `LOOM_DRIVER_EXECUTOR` + task workers for workflow
execution (`internal/cli/serve/serve.go:449-462`).

### C. `loom daemon` supervision

```
loom daemon ──► supervisor ──► N × (loom plan|task|agent <worktree> --auto --daemon-mode)
      │                              each in its own git worktree, own pgroup
      ├─ daemon.sock       (control: agent_stop/start/restart/list/yield)
      └─ agent-ipc.sock    (agent → daemon: claim/update/complete/heartbeat)
```

The supervisor re-execs the **loom binary itself** (`internal/cli/daemon/supervisor/spawn.go:83,88-115`)
with `Setpgid: true` (`:38`) and injects the agent's identity, role constraints,
and `LOOM_TRACE_PARENT` (`:40-73`). Both sockets sit next to the PID file;
`agent-ipc.sock` is `chmod 0600` with a 5 s read deadline
(`internal/cli/daemon/daemon_ipc.go:71-75,100`). A pre-spawn gate parks an agent
in `AgentStateBackendUnavailable` — restart budget preserved, no backoff — when
its backend binary is missing, and auto-recovers when it reappears
(`supervisor/backend.go:31,47-82`). In fleet mode, agent supervision is skipped
entirely (`supervisor/supervisor.go:183-188`).

Three independent daemon-liveness sources exist and a doc should say so:
the cwd-scoped `.loom/daemon.lock`
(`internal/cli/daemon/daemon_cmd.go:407-431`), the workspace-scoped
`<LoomDir>/workspaces/<ws>/daemon.lock` added because a second daemon started
from a different cwd is otherwise undetectable
(`internal/cli/daemon/workspace_lock.go:17-24`), and the fleet-db Node registry
(`internal/cli/daemonregistry/daemonregistry.go:1-9`, heartbeat 30 s / TTL 2 min
at `supervisor/supervisor.go:216-217`).

### D. Desktop app (macOS)

```
Loom Agents.app (Tauri)
   └─ sidecar: loom local service       ← per-user LaunchAgent
         ├─ loom serve  (detached child, --bind --port --fleet-mode)
         │     └─ embedded fleet-db sidecar (FLEET_DB_BIN injected)
         └─ loom daemon (supervised child, restarted on failure)
```

`desktop/README.md:5-12` states the shell is "intentionally a thin controller"
and the bundled `loom` sidecar owns the runtime.
`desktop/src-tauri/tauri.conf.json:33` bundles **two** external binaries,
`binaries/loom` and `binaries/fleet-db`, plus `resources/webui/` as the frontend.
`loom local` spawns serve at `internal/cli/local/local_cmd.go:240-244` and
supervises the daemon at `internal/cli/local/daemon.go:29-33`; log paths are
`<dataDir>/logs/{loom-serve,loom-local-service,loom-daemon}.log`
(`internal/cli/local/runtime.go:146,207,211`). Data dir resolution:
`LOOM_DESKTOP_DATA_DIR` → `LOOM_CONFIG_DIR` → macOS app-support dir
(`internal/cli/local/runtime.go:106-128`). Install/verify/troubleshoot steps are
in `docs/product/desktop-installation-runbook.md` (marked Draft).

### E. Container / distributed (partially shipped)

- **Container sandbox for workflow drivers** is real:
  `LOOM_DRIVER_SANDBOX=container` with podman-first/docker-fallback, read-only
  rootfs, `no-new-privileges`, mandatory memory/cpu/pids caps, and four egress
  modes (`internal/driver/sandbox/container.go:145-170,298-331`,
  `egress.go:58-73`).
- **Remote agent workers** are real: `loom worker --control-plane <url>` registers
  with a serve instance, uses HTTP-backed lock/event/log interfaces, and
  deregisters on shutdown (`internal/cli/serve/worker/worker_cmd.go:36`); the
  server side only exists when `LOOM_WORKER_TOKEN` is set
  (`internal/webui/app/server_workspace.go:124-137`).
- **The codified distributed reference** is `deploy/podman-stack/`
  (serve + fleet-db + redis + workers + stub upstream on a podman machine —
  `deploy/podman-stack/README.md:1-20`). It requires host-built `flue`
  (`build.sh:42-49`) and enforces a flue commit pin (`build.sh:57-67`).
- `docs/product/container-runner-mvp-spec.md` (a general container *agent* runner,
  as opposed to the driver sandbox) is marked **Draft** and should be treated as
  a plan, not a description — **UNVERIFIED as shipped**.

### Process/socket inventory (useful for a troubleshooting page)

| Socket / port | Owner | Purpose |
|---|---|---|
| `:8080` (default) | `loom serve` | API + SSE + WS |
| `:3000` | Vite dev server (`make dev`) | dev-only SPA with `/api` proxy (`internal/webui/frontend/vite.config.ts:177-198`) |
| ephemeral loopback | embedded fleet-db | `FLEET_SERVER_ADDR` (`internal/bootstrap/embedded.go:380`) |
| ephemeral loopback | embedded miniredis | `FLEET_REDIS_ADDR` (`embedded.go:381`) |
| `<pidDir>/daemon.sock` | `loom daemon` | control ops (`internal/cli/daemon/daemon_control.go:417-420`) |
| `<pidDir>/agent-ipc.sock` | `loom daemon` | agent → daemon mutations, `0600` (`internal/cli/daemon/daemon_ipc.go:461-464`) |
| `~/.loom/loom.sock` (hashed short path on macOS) | **client-only in this repo** | `internal/rpc/socket_path.go:31,57` — see the note in §3.1 |
| in-container `127.0.0.1:8484` + host unix socket | sandbox egress relay | `internal/driver/sandbox/egress.go:87-92,154-200` |
| loopback `127.0.0.1:0` | codex app-server | `internal/leadcontrol/codex_runtime.go:296` |

## 8. Build, test, run

### Day-one Makefile targets

| Target | What it does | Source |
|---|---|---|
| `make build` | Go binary only, no frontend. Stamps `internal/cli.Build` from git. | `Makefile:40-42` |
| `make build-frontend` | `npm install && npm run build` in `internal/webui/frontend`. | `Makefile:457-459` |
| `make build-all` | Both. | `Makefile:462` |
| `make dev` | `scripts/dev.sh` — air-reloaded Go API on :8080 + Vite on :3000. Requires `air` and Node ≥20 (`dev-check`, `Makefile:572-576`). | `Makefile:578-579` |
| `make test` | `TEST_COVER=1 ./scripts/test.sh`. | `Makefile:45-47` |
| `make check` / `make gate` | The unified gate: `check-go` (13 steps) and `check-frontend` (6 steps) in parallel. | `Makefile:517-541` |
| `make check-go` | gofmt → vet → build → golangci-lint + control-plane path guard → LOC → package size → import fanout → exec.Command guard → log.Printf guard → beads guard → generated-API staleness → `go test -p 1 -race` → coverage ≥60. | `Makefile:469-499` |
| `make check-frontend` | prettier → tsc → eslint → architectural checks → generated-code staleness → vitest coverage ≥60. | `Makefile:501-515` |
| `make gate-e2e` / `gate-e2e-full` | gate + real Playwright smoke; `-full` adds `go test -tags container ./e2e/`. | `Makefile:544-554` |
| `make hooks` | Install the pre-push gate hook. | `Makefile:556-561` |
| `make gen-go-api` | Regenerate `internal/backend/api/gen/types.gen.go` from `api/openapi.yaml` (via a 3.1→3.0 preprocessor, oapi-codegen v2.6.0). | `Makefile:318-326` |
| `make local-mode-up` / `-codex-up` / `-claude-up` / `-daytona-up` / `-verify` / `-down` / `-logs` | Podman/Docker local-mode stack from `test/local-mode/docker-compose*.yml`. Auto-selects podman compose → podman-compose → docker compose. | `Makefile:22-38,162-235` |
| `make help` | Prints the annotated target list. | `Makefile:594-651` |

### Frontend scripts (`internal/webui/frontend/package.json`)

`dev`, `build`, `typecheck`, `lint`, `format`, `test:unit`, `test:coverage`,
`test:e2e[:ui|:headed|:debug]`, `test:visual[:update]`, `test:e2e:integration`,
`test:e2e:api`, `generate:types` (openapi-typescript from `api/openapi.yaml`), and
the architectural gates `check:loc`, `check:no-raw-fetch`,
`check:no-hardcoded-urls`, `check:boundaries`, `check:dir-size`,
`check:generated`.

### Testing vocabulary

`AGENTS.md:19-35` mandates a "terminology handshake" using four axes —
**depth / realness / provisioning / polarity** — and names the trap words
`local`, `live`, `real`, `verify`, `gate`. The three evidence classes are
defined there: *deterministic* (orchestration only), *real* (real local
backend), *live* (reaches a real external/paid service).

**The canonical map for this vocabulary, `docs/testing-terminology.md`, does not
exist.** It is cited from `AGENTS.md:20`, `AGENTS.md:35`,
`docs/agents/domain.md:18` and `:42`, and
`internal/cli/agent/prompts/pr-review-checkout.md:39`. Writing it is the single
highest-leverage doc task in the repo.

What *does* exist: `docs/testing/README.md` (layer table + commands),
`test-infrastructure.md`, `test-patterns.md`, `go-backend-tests.md`,
`frontend-tests.md`, `known-issues.md`, `coverage-gaps.md`, the manual E2E plans
(`e2e-preflight.md`, `e2e-cli.md`, `e2e-ui.md`, `local-mode-podman-e2e.md`), and
the runtime-testing runbook `.agent-skills/loom-pr-test/SKILL.md`.

### Test harness directories

| Path | Purpose |
|---|---|
| `test/local-mode/` | The main full-stack dogfood stack (compose + `verify-local-mode.sh` + a `loom-backend-localdogfood` mock). |
| `test/playground/` | Daemon-lifecycle **failure-mode** harness: mock backends that crash/hang/run-slow, driving real pgroups, orphan PIDs, watchdog timing (`test/playground/README.md:1-16`). |
| `test/fleetdb/` | fleet-db-only UI regression + empty-CLI stacks. |
| `test/distributed/` | Distributed smoke compose. |
| `e2e/` | Alpine container with loom + Chromium + Playwright + agent-browser (`e2e/README.md`). |
| `deploy/podman-stack/` | Local podman stack emulating the **distributed** topology; the first codified serve+fleet-db deployment (`deploy/podman-stack/README.md:1-20`). |
| `smoke-test/` | One script: Slack epic-runner stack smoke test. |

### CI

`.github/workflows/`: `ci.yml` (go-quality-gate, frontend-quality-gate,
coverage-go, coverage-frontend, frontend-standalone, test-macos-go),
`e2e.yml`, `playwright.yml`, `nightly.yml`, `release.yml` (GoReleaser),
`desktop-release.yml`. Go version comes from `go.mod` in every job
(`ci.yml:27,78,162`, `release.yml:26`).

Release artifacts (`.goreleaser.yml`): linux + darwin, amd64 + arm64, plus a
separate `loomcli-frontend_<version>.tar.gz` static bundle. **No Windows build**,
despite Windows source files existing (`internal/lockfile/lock_windows.go`,
`internal/webui/editor/launch_windows.go`, `internal/cli/signal_dir_windows.go`).

---

## 9. Documentation gaps

### 9.1 Files that are referenced but do not exist

| Missing file | Cited from | Impact |
|---|---|---|
| `docs/testing-terminology.md` | `AGENTS.md:20`, `AGENTS.md:35`, `docs/agents/domain.md:18`, `docs/agents/domain.md:42`, `internal/cli/agent/prompts/pr-review-checkout.md:39` | **Highest impact.** `AGENTS.md` makes a "terminology handshake" mandatory before running anything slow or irreversible, and points at a file that isn't there. Every agent and every new dev hits this. |
| `docs/testing/fleetdb-acceptance-gates.md` | `docs/testing/README.md:32`, `docs/testing/go-backend-tests.md:35` | The named acceptance gates for backend/CLI, browser, SSE, workspace, supervisor, embedded-local, remote-distributed, and deletion lint are undiscoverable. |
| `RUNTIME-AND-DEPLOYMENT.md` | `deploy/podman-stack/README.md:5` (referred to for the "T3/T4 shape") | The distributed topology tiers T3/T4 are named but never defined anywhere in the repo. |
| `/Users/tyson/.claude/plans/purrfect-weaving-stream.md` | `.golangci.yml:79` — "See … for layer definitions" | The authoritative definition of the `sdk → infra → web → cli` layering lives in a personal file outside the repo. |
| `CONTRIBUTING.md`, root `CHANGELOG.md` | — | Absent. Only `sdk/CHANGELOG.md` exists. |
| `internal/webui/frontend/README.md` | — | A 597-file React app with no README. |

### 9.2 Documented claims that are wrong or stale

| Claim | Where | Reality |
|---|---|---|
| Default backend is `claude` | `README.md:246`, `README.md:261`, `README.md:351` | It is `codex` — `internal/cli/backend.go:29,91` |
| Backend resolution includes the workspace daemon profile | `README.md:261` | `ResolveBackendName` has no such step (`internal/cli/backend.go:82-91`); the profile is injected at spawn time by the supervisor / terminal launcher |
| Three backends (`claude`, `codex`, `opencode`) | `README.md:244-249` | Five registered: + `cursor`, `gemini` |
| `LOOM_WORKTREES_DIR` controls the worktrees directory | `README.md:353` | Dead — read only by tests; paths are structural (`internal/localworkspace/localworkspace.go:39-41`) |
| "`loom serve` starts a web UI… Open http://localhost:8080" | `README.md:56-60`, `README.md:160` | `loom serve` is API-only unless `--frontend-dir` is set (`internal/webui/app/frontend.go:11-16`); `Makefile:582` states "post-Phase-5 there is no Loom-served UI" |
| Web UI has 5 views (Kanban/Table/Graph/Monitor/Settings) | `README.md:91-96` | 13 routes exist: `kanban`, `list`, `table`, `graph`, `monitor`, `observability`, `terminal`, `agents`, `prs`, `settings`, `workspace`, `files`, plus issue/agent detail (`internal/webui/frontend/src/router.tsx:88-166`) |
| Root help: "LOOM_BACKEND … (default: codex)" alongside "parallel Claude Code workflows" | `internal/cli/root.go:37,83` | Internally inconsistent with `README.md:3` ("parallel AI coding workflows") — the env default here is right, the README's is wrong |
| CI uses Go 1.24; coverage threshold 25%/40%; release includes Windows | `docs/testing/test-infrastructure.md:35,38,49` | Go version comes from `go.mod` = 1.25.6 (`.github/workflows/ci.yml:27`); thresholds are 60 (`Makefile:497`) / 70 default (`scripts/check-coverage.sh:9`); `.goreleaser.yml` builds linux+darwin only |
| Glossary contains "request lifecycle, object model, the four planes" | `docs/agents/domain.md:12-14` | `docs/loom-glossary.md` (63 lines) has none of these; only "control-plane" appears, once, at `:55` |
| `docs/design/generic-sse-envelope.md`: one product SSE stream per workspace | that doc | Envelope claims all verified; but four more SSE endpoints exist that it doesn't mention (see §3.6) |
| Workflow authoring guide code samples: `import … from '@loom/sdk/flue'`, bare `export async function run(ctx)` | `docs/design/workflow-driver-authoring-guide.md:134,136,188,190` | That SDK subpath does not exist (`sdk/package.json:6-25`); Flue now requires `export default defineWorkflow({...})` (`internal/workflows/builtin/epic-runner.ts:3-20`). Also stale in `docs/design/fleetdb-agent-platform-v2-proposal.md:714,972` |
| Workflow guide: `providerProfile`/`supportedProviders` on exec-task | `workflow-driver-authoring-guide.md:203-205` | Not in the wire contract — `sdk/api-surface.v1.json:82-99` and `sdk/driver.d.ts:83-112` use `runner` |
| Workflow guide: "helper methods … carry credentials through environment variables" (CLI subprocess transport) | `workflow-driver-authoring-guide.md:167-170` | `sdk/driver.js` has no `child_process`; it is HTTP-only against `LOOM_DRIVER_API_URL` with a run-token bearer (`sdk/driver.js:65-75`) |
| `AGENTS.md`: "Operator/CLI registration … stamp trusted" | `AGENTS.md:93` | The **CLI defaults to untrusted** unless `--trusted` (`internal/cli/driver/driver_cmd.go:105-106,164-172`) |
| `AGENTS.md`: podman/egress test commands under `./internal/driver` | `AGENTS.md:105-106,150-153` | Those tests live in `internal/driver/sandbox/` (`sandbox/container_test.go:489`, `sandbox/egress_test.go:331,417`); the documented `go test ./internal/driver -run …` matches nothing |
| Stack publisher: "two-phase reorder" | `docs/design/2026-06-18-stack-aware-pr-publisher.md:80-84,100-102` | Five phases shipped (`internal/stackpublish/reconciler.go:273,282,294,303,377`); the package's own doc says "four-phase" (`forge.go:1-8`). Intent is faithful; the count is stale in two places |
| `deploy/podman-stack/build.sh:62` reads the flue pin from `internal/workflows/builtin-dist/FLUE_COMMIT` | that file | That path is gitignored (`.gitignore:12`); the live pin is `internal/workflows/FLUE_COMMIT` |

### 9.3 Whole subsystems with zero user-facing documentation

Ranked by size of the undocumented surface:

1. **Workflow drivers** (`loom workflow`, `loom driver`, `internal/driver` 10.6k
   lines, the whole sandbox/trust/egress model). The only doc is the
   authoring guide, whose samples don't compile against the current SDK.
2. **Stacked PRs** (`loom stack`, 11 subcommands, `internal/stackpublish` +
   `stacklineage` + `stackstore`). Only a design doc marked "Proposed".
3. **Triggers and webhooks** (`loom trigger`, `POST /webhooks/{name}`, cron,
   internal-event loopback, the issue-journal bridge's self-trigger hazard).
4. **Connectors** (`loom connector`, sealed credentials, deny-by-default egress
   grants, the audit journal). Security-relevant and entirely undocumented for
   users.
5. **The `loom worker` remote-worker mode** and `LOOM_WORKER_TOKEN` gating.
6. **`loom local` / the desktop runtime** — only the Draft installation runbook.
7. **`loom epic run`** and the epic-runner model.
8. **The `loom hooks` Claude Code integration** — `install`/`uninstall`/`status`
   are user-facing but undocumented.

### 9.4 Undocumented extension points

- **AI backend plugins** (`loom-backend-<name>` on `PATH`, with the
  `invoke --interactive|--non-interactive` / `meta --json` / `health --json`
  contract) — `internal/cli/backends/backend_external.go:24-179`. The only prose
  is `test/playground/README.md`, a test doc.
- **Prompt overrides** at `./loom-prompts/<name>.md` —
  `internal/cli/agent/prompts.go:101-120`. **Zero** documentation anywhere;
  `grep -rn 'loom-prompts' --include='*.md'` returns nothing.
- **`builtin:<id>` prompt selectors** — one sentence in
  `docs/loom-glossary.md:46-51`, no list of available ids.

### 9.5 Onboarding blockers for a new developer

1. **Sibling repos are undeclared.** `../fleet-db` is needed to build the
   fleet-db binary (`scripts/start-e2e-server.sh:86-95`,
   `scripts/test-runner-pr-e2e.sh:25`, `Makefile:442-444`) and `../flue` for
   workflow bundles (`deploy/podman-stack/build.sh:29,42-49`). Only
   `docs/product/desktop-installation-runbook.md:28` and
   `docs/observability/tracing.md:25` mention them, and neither is a place a new
   dev would look. `README.md` and `AGENTS.md` say nothing.
2. **No architecture document.** The layering exists only in `.golangci.yml`, and
   the definitions it cites are outside the repo. `docs/arch/` has exactly two
   files, both frontend feature notes.
3. **Two things named "backend".** `internal/backend` = issue backend;
   `internal/cli/backends` = AI backend. Both have env vars
   (`LOOM_ISSUE_BACKEND` vs `LOOM_BACKEND`), both have resolution ladders, both
   appear in `DaemonProfile` (`IssueBackend` and `AgentBackend`,
   `internal/domain/daemon_profile.go:20-21`). The only place this is said out
   loud is a comment at `internal/cli/fleet_mode.go:18-20`.
4. **Package docs were patchy at survey time — since remediated.** At the
   surveyed commit `b490a290b`, 82 of 158 Go packages carried a package doc
   comment (52%), and the ones that didn't included `internal/driver`,
   `internal/sessions`, `internal/webui` root, and `internal/cli` root. A
   backfill has since closed this: 153 of the 154 packages under `internal/`
   now carry one, the sole exception being
   `internal/webui/handlers/connectors`.
5. **`docs/product/` is 16 Draft files with no "as-shipped" marker.** A reader
   cannot tell `local-mode-product-spec.md` (largely shipped) from
   `container-runner-mvp-spec.md` (not shipped) without reading the code.
6. **Env-var sprawl with no reference.** ~120 `LOOM_*` vars, 5 documented.
7. **No documentation of what `loom` does to the machine.** Notably: the daemon
   re-execs the loom binary and sets process groups
   (`internal/cli/daemon/supervisor/spawn.go:38,83`); the codex backend is
   invoked with `--dangerously-bypass-approvals-and-sandbox`
   (`internal/cli/backends/backend_codex.go:39-50,100-113`) and the claude lead
   runtime with `--dangerously-skip-permissions`
   (`internal/cli/backends/harness_lead_runtime.go:105`). These are legitimate
   design choices for a supervised agent runner, but they are exactly what a
   security-conscious user needs told up front, and no doc mentions them.

### 9.6 Which existing docs are trustworthy

| Doc | Trust | Why |
|---|---|---|
| `docs/loom-glossary.md` | **High** for what it covers | Verified against `domain.ResolveRoleKind` and `epicrunner.IsLeadRole`. Incomplete, not wrong. |
| `docs/observability/tracing-contract.md` | **High** | The only doc self-labelled "source-of-truth"; matches `internal/cli/root.go:221-271` and the `LOOM_TRACE_PARENT` propagation at `spawn.go:70-73`. |
| `docs/design/generic-sse-envelope.md` | **High on the envelope, incomplete on scope** | Every field claim verified in `server/realtime/`. |
| `docs/agents/issue-tracker.md` | **High** | Cites exact source lines and even warns about stale installed binaries (`:33-37`). Updated 2026-07-22. |
| `docs/arch/terminal-system.md`, `docs/arch/issue-detail-view.md` | **Medium-high** | Recent (2026-07-20), component-level, verified against the frontend tree. |
| `docs/design/2026-06-07-trigger-workflow-proposal.md` | **Understated** | Largely implemented *and exceeded* — cron and binding CRUD listed as "deferred/missing" both now exist. |
| `docs/design/2026-07-22-lead-conversation-resume.md` | **Accurate as a decision log, not as a description** | Every code-observation citation resolves (±5 lines); every *design* element (`Agent.CurrentSessionID`, conversation-home locator, resume outcome record, claude resume argv branch) is **not in the tree** — `grep -rn CurrentSessionID internal/` returns zero hits. The shipped piece is the codex resume path (commit `aec26e468`). |
| `docs/api.md` | **Stale** | Last touched 2026-05-28; 124 commits have landed in `internal/webui` since. Prefer `api/openapi.yaml` (2026-07-14, staleness-gated by `make check-go-api-staleness`). |
| `docs/testing/test-infrastructure.md` | **Stale** | See §9.2. |
| `docs/design/workflow-driver-authoring-guide.md` | **Concept accurate, samples broken** | See §9.2 and §5.4. |
| `docs/design/2026-06-07-slack-agent-service-proposal.md` | **Unimplemented proposal** | No Socket Mode anywhere in `internal/**.go`; the shipped shape is connector egress instead. |
| `docs/product/*` (all 16) | **Draft; mixed** | `local-mode-product-spec.md` invariants are enforced in CI; `container-runner-mvp-spec.md` is unshipped. Treat individually. |

---

## 10. Recommended documentation structure

Two deliverables. Neither should be written from the existing docs — every
section below names the primary sources to write it from.

### 10.1 External user documentation

| § | Section | Write it from |
|---|---|---|
| 1 | **What Loom is** — the parallel-agent model, worktrees, the shared issue tracker | `README.md:3-5`, `docs/loom-glossary.md`, `docs/product/orchestrator-worker-model.md` (validate against code) |
| 2 | **Vocabulary** — workspace, repo, role, role kind, agent, lead, worker, task/epic, backend (both senses), driver/workflow, stack, connector, trigger | `docs/loom-glossary.md` + the missing-terms list in §1 of this document |
| 3 | **Install** — binary, npm shim, from source; what you also need (`fleet-db` binary, at least one AI CLI) | `scripts/install.sh`, `npm/install.js`, `.goreleaser.yml`, `internal/bootstrap/embedded.go:526-580` |
| 4 | **Quickstart** — `loom init` → `workspace add` → `repo add` → `role set` → `agentdef add` → `plan`/`lead`/`task` | `loom init --help`, `README.md:34-49`, `internal/cli/workspace/init.go:28` |
| 5 | **Core concepts: the task lifecycle** — open → in_progress → review → open → closed; design field; ready/blocked | `internal/cli/root.go:60-62`, `internal/cli/agent/{plan,task}.go`, `docs/product/agent-lifecycle-state-machine.md` (verify) |
| 6 | **Running agents** — `plan`, `task`, `agent`, `lead`; `--auto` and its exit conditions; task filters; `--parent` epic scoping | `loom {plan,task,agent,lead} --help`, `internal/cli/automode/` |
| 7 | **Choosing an AI backend** — the five built-ins, `loom backend list/health/info`, resolution order (**corrected**), effort/model knobs | `internal/cli/backend.go:82-91`, `internal/cli/backends/backend_cmd.go`, `internal/cli/backends/backend_effort.go` |
| 8 | **Supervising with the daemon** — profiles, restart policy, backend-unavailable parking, quarantine, `daemon queue`, logs | `internal/cli/daemon/`, `internal/domain/daemon_profile.go:13-25`, `supervisor/{backend,restart,quarantine}.go` |
| 9 | **The web UI** — `loom serve`, the 13 views, terminals, SSE live updates, auth modes | `internal/webui/frontend/src/router.tsx:88-166`, `internal/cli/serve/serve.go:101-180`, `docs/arch/terminal-system.md` |
| 10 | **Git integration** — push/pull/sync/pr/reset, AI conflict resolution, worktree layout | `internal/cli/git/`, `internal/localworkspace/localworkspace.go:39-92` |
| 11 | **Stacked pull requests** — stacks, units, lineage, restack, publish | `internal/cli/stack/stack_cmd.go`, `internal/stacklineage/types.go`, `internal/stackpublish/reconciler.go` (**new doc — nothing exists**) |
| 12 | **Epics and the epic runner** | `internal/cli/epic/run.go`, `internal/workflows/builtin/epic-runner.ts:39-52`, `internal/epicrunner/start.go` |
| 13 | **Workflows** — clone/build/approve/activate/run, what a driver is, trust levels, sandbox modes | `internal/cli/workflow/workflow_cmd.go`, `internal/driver/sandbox/`, `sdk/README.md` (**rewrite of the authoring guide**) |
| 14 | **Triggers and webhooks** — bindings, route keys, cron, GitHub webhooks, the self-trigger hazard | `internal/trigger/{pattern,cron,internal_source,issue_journal_bridge}.go`, `internal/webui/handlers/webhooks/` (**new doc**) |
| 15 | **Connectors** — sealed credentials, egress grants, rotation, audit | `internal/cli/connector/connector_cmd.go`, `internal/connector/dispatch.go:14-29` (**new doc**) |
| 16 | **Deployment topologies** — local, serve+frontend, daemon, desktop, distributed | §7 of this document; `deploy/README.md`, `deploy/podman-stack/README.md` |
| 17 | **Configuration reference** — fleet-db objects + the full env-var table | §6 of this document (**there is no existing env reference**) |
| 18 | **Security** — auth modes, the four auth surfaces, sandbox/egress, connector vault, what loom passes to AI CLIs | `docs/security.md` (expand), `internal/webui/server/middleware/auth.go`, `internal/driver/env.go`, §9.5 item 7 |
| 19 | **Data, backup, retention** — what lives where, how to back up local mode, `loom cleanup` | §6.2, `internal/webui/localredis/manager.go:2-11`, `internal/cli/cleanup/` |
| 20 | **Troubleshooting** — `loom doctor`, `loom recover`, three daemon-liveness sources, common failures | `internal/cli/doctor/`, `internal/cli/agent/recover.go`, `docs/product/failure-modes-recovery-ux.md` (verify) |
| 21 | **Extending Loom** — backend plugins, prompt overrides, SDK workflows | `internal/cli/backends/backend_external.go`, `internal/cli/agent/prompts.go:101-120`, `sdk/` (**all three currently undocumented**) |
| 22 | **CLI reference** — generated | Cobra help text; consider `cobra-cli`-style generation so it cannot drift |
| 23 | **HTTP API reference** — generated | `api/openapi.yaml` (already gated by `make check-go-api-staleness`); retire the hand-written `docs/api.md` |

### 10.2 Developer onboarding guide

| § | Section | Write it from |
|---|---|---|
| 1 | **Read this first** — glossary, the two-things-called-backend trap, testing terminology | `docs/loom-glossary.md`, `AGENTS.md`, §3.2 and §9.5 of this document |
| 2 | **Prerequisites and sibling repos** — Go 1.25.6, Node ≥20, `air`, podman/docker, tmux (optional), `../fleet-db`, `../flue` | `go.mod:3`, `Makefile:572-576`, `scripts/start-e2e-server.sh:86-95`, `deploy/podman-stack/build.sh:29,42` (**currently undocumented**) |
| 3 | **First build and run** — `make build`, `make dev`, `make help` | `Makefile:40-42,457-462,578-579,594-651` |
| 4 | **Repository tour** — the table in §2 of this document | this document |
| 5 | **The layer model and how it's enforced** — `sdk → infra → web → cli`, handler isolation, `cli/data` isolation, LOC/package-size/fanout gates | `.golangci.yml:62-186`, `scripts/check-*.sh` (**the single most important missing dev doc**) |
| 6 | **Control-plane invariants** — the two legal paths, memstore is test-only, `check-control-plane-paths.sh` | `internal/bootstrap/mode.go:26-59`, `internal/infra/memstore/import_guard_test.go:15-51` |
| 7 | **How a command is wired** — `init()` registration, `PersistentPreRunE`, `Deps`, `cmdstore` | `cmd/loom/main.go`, `internal/cli/root.go:102-151,273-293`, `internal/cli/deps.go` |
| 8 | **How an agent actually runs** — resolution ladders, prompt rendering, harness-wrapper PTY, session artifacts, error classification | `internal/cli/backends/backend_wrapper.go:63-102`, `internal/harness/retry.go`, `internal/sessions/`, `internal/agenterr` + `internal/agentpolicy` |
| 9 | **The daemon** — supervisor structure, spawn argv/env, the two sockets, restart/quarantine, three liveness sources | `internal/cli/daemon/`, `supervisor/spawn.go:25-140`, `daemon_ipc.go`, `daemonregistry/` |
| 10 | **The web server** — composition root, module registration, middleware order, service-layer rule, SSE hub, terminals | `internal/webui/app/{server,server_app,routes}.go`, `.golangci.yml:62-76`, `server/realtime/` |
| 11 | **The frontend** — Vite/React layout, generated OpenAPI types, the five `check:*` architectural gates, testing pyramid | `internal/webui/frontend/{package.json,vite.config.ts,src/router.tsx}`, `scripts/*.mjs` (**needs a frontend README**) |
| 12 | **The driver/workflow platform** — Driver/Version/Run, executor lifecycle, trust + sandbox + egress, run tokens, the SDK contract | `internal/driver/`, `internal/driver/sandbox/`, `sdk/api-surface.v1.json`, `internal/workflows/` |
| 13 | **Storage** — `store.Store`, fleetdb client, memstore double, conformance suites, entity vs types migration | `internal/store/store.go:20-53`, `internal/infra/`, `internal/store/storetest/`, `internal/entity/doc.go` |
| 14 | **Observability** — tracing contract, service names, event bus, notify bus, Prometheus | `docs/observability/tracing-contract.md`, `internal/cli/root.go:217-271`, `internal/events/`, `internal/notify/doc.go` |
| 15 | **Testing** — the four-axis vocabulary, the evidence classes, unit/integration/e2e/visual, the harness directories, the runbook | `AGENTS.md:19-35` (**and the missing `docs/testing-terminology.md`**), `docs/testing/`, `test/*/README.md`, `.agent-skills/loom-pr-test/SKILL.md` |
| 16 | **Quality gates** — what `make check` runs and why each gate exists | `Makefile:469-541`, `scripts/check-*.sh`, `.pre-commit-config.yaml`, `.githooks/` |
| 17 | **Making a change** — branch, gate, hooks, the `make gate` clean-env caveat | `AGENTS.md:44-61`, `Makefile:556-568` |
| 18 | **Release** — GoReleaser, the separate frontend tarball, npm shim, desktop DMG | `.goreleaser.yml`, `.github/workflows/{release,desktop-release}.yml`, `npm/`, `desktop/scripts/release-macos.sh` |
| 19 | **Docs conventions** — dated `docs/design/`, `docs/arch/`, `docs/product/`; no `CONTEXT.md`/`docs/adr/`; how to mark shipped vs proposed | `docs/agents/domain.md:1-31` (fix its glossary over-claim first) |
| 20 | **Known rough edges** — the stale-doc list in §9.2, unimplemented design docs, the `internal/rpc` server-less client, `openshell-task-runner` deprecation | §9 of this document |

### 10.3 Suggested first five doc PRs, in order

1. ~~Write `docs/testing-terminology.md` — five files already link to it.~~
   **Shipped** — `docs/testing-terminology.md`.
2. ~~Add `docs/arch/layering.md` from `.golangci.yml:62-186` so the architecture
   stops living in a linter and a personal plan file.~~ **Shipped differently**
   — landed as the generated `docs/reference/architecture.md`, which derives the
   layers from the depguard rules and the real import graph instead of copying
   them by hand. The personal plan file is still cited at `.golangci.yml:79`.
3. ~~Fix the verified README errors (§9.2 rows 1–6) and add the sibling-repo
   prerequisite.~~ **Shipped** — the prerequisite table is in `AGENTS.md`.
4. ~~Add `docs/reference/environment.md` from §6.6.~~ **Shipped** as
   `docs/reference/env-vars.md`, generated by `scripts/loomdoc` rather than
   written by hand.
5. Rewrite `docs/design/workflow-driver-authoring-guide.md` against the current
   SDK, or mark it superseded by `sdk/README.md`. **Still open.**
