# aft browser tests

Live-server E2E suites for the Loom web UI, driven by
[aft](https://github.com/tysonthomas9/aft) — deterministic YAML browser tests with a
Claude recovery agent that diagnoses (`--strict`) or heals (`--heal`) failures.

These complement the mocked Playwright suite: they run against the **real v5 stack**
(`loom serve` + embedded fleet-db + vite preview, fresh isolated workspace, auth open),
so they cover what mocks can't — above all that server-side mutations reach the UI via
SSE.

Poll-loop comments use these budget classes: `ui-click-retry` (3s) for browser clicks
that can race route or popover mounting; `db-persist` (10-20s) for direct fleet-db/API
write propagation and asynchronous action-created rows; `worktree-materialization`
(10s) for local agent worktree creation after a row exists; `monitor-cache` (16-20s)
for `/api/monitor/status` cache and join propagation; `context-delivery` (45s) for
epic assignment delivery state crossing monitor/UI boundaries; and `terminal-launch`
(40-120s) for PTY spawn plus terminal tab/session launch metadata.

## Run

```bash
make test-aft                      # deterministic, no model calls (what CI runs)
make test-aft-real                 # opt-in real codex epic-runner tier
make test-aft-terminal             # opt-in live-tmux Logs tab tier (real codex)
make test-aft-strict               # failures get an agent diagnosis + suggested fix
make test-aft-heal                 # local dev: agent may complete a broken step's intent
tests/aft/run-aft.sh --record ...  # or call the harness directly with any aft flags
```

Extra aft flags go through `AFT_ARGS`, e.g.
`make test-aft AFT_ARGS="--screenshots --record-all"`.

The harness starts `scripts/start-e2e-server.sh` (loom API on `E2E_PORT`, default 8090;
vite preview on `E2E_FRONTEND_PORT`, default 3100, proxying `/api`), waits for readiness,
and passes both `tests/aft/suites/` and `tests/aft/surface-suites/` to **one aft run
invocation** before tearing the stack down. Setting `AFT_SUITES` explicitly replaces that
default with exactly one file or directory, which keeps the real-* tiers isolated. The
primary workspace is `e2e-ws` with id **`E2E-WS`** — exported to suites/hooks as `AFT_WS`.

Requirements: `go`, `node` >= 20 (24 for agent-browser), `agent-browser`, a **fleet-db**
checkout (default `../fleet-db`; a sibling `../fleet-db-main` — e.g. a git worktree of
origin/main — is preferred when present because the epic-runner needs the driver-runs
domain), an aft checkout (default `../testing-app`, override `AFT_DIR`), a **flue**
checkout at `../flue` (pinned commit in `internal/workflows/FLUE_COMMIT`, built with
pnpm) for the agent-flow suite, and `claude` unless `--no-agent`.

## Two suite tiers

The directory is the tier; there is no YAML flag that changes a test's meaning.

- `tests/aft/suites/` is the product-correctness tier. Every test follows actor fidelity:
  each mutation in the test body must be attributable to the persona named by `intent:`.
  An agent or API-client actor should mutate through the API because Loom's model is
  “agents mutate via API, humans observe.” A human actor must use an existing mounted UI
  control. Every intent names the actor explicitly.
- Suite-level `setup:` and `teardown:` are fixture provisioning and cleanup, so they may use
  the API freely. A test-body API **readback** may verify the result of a human UI action;
  standalone API contract blocks do not belong in this tier.
- Use `api:` for single-request API interactions so reports describe the operation clearly and
  assertions stay structural. Keep `run:` for polls, pipelines, and other shell orchestration;
  the aft loader requires intents on every `run:`, `api:`, and `wait:` step that uses `fn:`.
- `tests/aft/surface-suites/` is the surface-wiring tier for intentionally UI-orphaned API
  contracts, fabricated or hollow fixtures, and compatibility surfaces that cannot yet form
  a faithful scenario. Every surface test explains why it is here and what change would
  promote it.

The default run executes both directories together so ordering, reporting, and coverage
describe one deterministic suite run.

## Graph scenario packages

Branching product flows may use a directory whose `flow.graph.yaml` manifest owns the
complete topology. `states.yaml` contains observable UI contracts, while explicitly
imported files under `transitions/` contain ordinary AFT mechanics. AFT discovers only
the manifest—not its fragments—and validates the complete DAG before opening a browser.

`tests/aft/suites/issue-detail.graph/` is the first product-correctness pilot. Its shared
UI-create prefix fans out to description save/cancel, type, priority/owner, label,
comment, lifecycle, title, dependency, and card-reopen journeys. Transition coverage
selects twelve complete root-to-terminal paths; two named golden journeys run
independently, for fourteen fresh-browser replays total. Every path gets a stable
`AFT_CASE_ID`, suite-level cleanup, source-line provenance, and a graph plan/evidence
section in the reports. Read the package's `README.md` and manifest before editing
mechanics in a fragment.

## Real codex tier

`make test-aft-real` runs the opt-in real-codex tier: the server keeps every
other agent CLI stubbed (`e2e/stubs-real-codex/`), but lets the epic-runner
resolve the operator's real `codex` CLI from `PATH`. It requires `codex` on `PATH` and a logged-in `~/.codex/auth.json`
(`codex login`). The target passes `--no-agent`; only the server-side codex run is
real.

This does not spend marginal API dollars for a ChatGPT-account codex login, but it
does consume that account's codex rate-limit window. Do not loop it casually.
CI never runs this tier because CI has no operator `~/.codex`, the run is
nondeterministic, and it touches a real account limit.

Accidental triggering is blocked three ways: `AFT_REAL_CODEX=1` must be set, the
separate `make test-aft-real` target sets it, and real scenarios live in
`tests/aft/real-suites/` instead of the default `tests/aft/suites/` directory.
In real mode the harness also unsets `OPENAI_API_KEY`, defaults `AFT_TIMEOUT` to
`600000`, and fails fast if `codex` or `~/.codex/auth.json` is missing.

### Other real backends: claude, opencode, cursor

The same host epic-runner tier exists for every other first-class agent backend,
gated behind `AFT_REAL_BACKEND=<codex|claude|opencode|cursor>` (`AFT_REAL_CODEX=1`
is the back-compat alias for `codex`). Each tier lets exactly ONE real CLI resolve
on the server's PATH; every other agent CLI is stubbed via a per-tier stub set
(`e2e/stubs-real-<backend>/`, symlinks into `e2e/stubs/`). Suites live in
`tests/aft/real-suites-<backend>/` and force the workspace default backend via
`PATCH /api/workspaces/E2E-WS/config/backend` as their first step, then assert the
recorded session's `backend`, `files_changed >= 1`, a real transcript + diff, and
HELLO.md physically on disk — same real-vs-stub discriminators as the codex tier.

| target | real CLI | preflight | cost class |
|---|---|---|---|
| `make test-aft-real` | `codex` | `~/.codex/auth.json` | ChatGPT-account rate window (`OPENAI_API_KEY` unset) |
| `make test-aft-real-claude` | `claude` | `${CLAUDE_CONFIG_DIR:-~/.claude}/.credentials.json` | Claude subscription rate window (`ANTHROPIC_API_KEY` unset) |
| `make test-aft-real-opencode` | `opencode` | binary only — provider auth lives in opencode's own config | whatever provider opencode selects |
| `make test-aft-real-cursor` | `cursor-agent` | `cursor-agent status` logged in | Cursor account usage (`CURSOR_API_KEY` unset) |

`make test-aft-real-all` runs all four sequentially, each on its own fresh stack.
None of these run in CI (no operator credentials there), and none should be looped
casually — every run consumes the respective account's rate/usage window.

### Live interactive tier

`make test-aft-live-interactive` (add `LIVE_BACKEND=claude` for the harness-wrapper
runtime) drives a **Custom-prompt interactive agent** with a real backend, so the
chain prompt → `loom lead` argv → live model → instructed artifact is proven end to
end instead of stopping at argv. Suites live in `tests/aft/live-interactive-suites/`.

The acceptance criterion lives *in the prompt*: the agent is told to write a marker
file whose exact contents contain this run's id. No stub writes it, no built-in
prompt produces it, and a stale prompt yields the wrong id — one artifact, three
discriminators. The test then asserts the bytes, asserts nothing else changed
relative to a **captured baseline** (the workspace root is not clean — the stack
writes response JSON after its seed commit), and closes the terminal tab fatally.

Live-tier flags are owned by `run-aft.sh`, not aft, and are consumed before the
stack boots:

| flag | effect |
|---|---|
| `--live` | opt in; requires `--no-agent`, refuses `--strict`/`--heal`, and requires `AFT_SUITES` to point at a `live-*` path |
| `--real-backend <b>` | flag form of `AFT_REAL_BACKEND` |
| `--max-real-cases <n>` | refuses to start when the selected suites hold more cases than the cap |
| `--with-daemon` | start and own a real daemon; required for `live-worker*` suites and rejected elsewhere |

Two properties this tier depends on, both deliberate: interactive agents get **no
worktree**, so the artifact lands in the isolated workspace root; and readiness is
asserted from **rendered terminal output**, never from `pty_alive`, which is true for
attachable metadata before any child has spawned. There is no transcript assertion
because an interactive lead records no task session — the evidence is the artifact,
the terminal, and the orchestration session id.

**The workspace root is not a security boundary.** Codex leads launch with
`--dangerously-bypass-approvals-and-sandbox`; changing the process cwd confines
nothing. The model can write anywhere the operator can, and LI-1's negative-scope
assertion only inspects the test workspace. Treat the whole host account as exposed
until this tier runs inside a real sandbox with only the workspace writable.

Cleanup is **harness-owned, not suite-owned**, because a suite cannot be trusted with
it: aft skips every remaining step once one fails (so a test's own final "close the
tab" step is skipped on exactly the runs that leak), and treats suite-teardown failure
as report-only. So `run-aft.sh` runs `scripts/live-sweep.sh` after aft exits, pass or
fail: it deletes every terminal tab, then checks the **process table** against a
pre-run PID baseline — because `DeleteTab` removes metadata first and only logs a
PTY-kill failure, "no tabs" alone is not proof the child died. A sweep that cannot
prove cleanliness fails the run even when every test passed.

Every run then appends an accounting line to `reports/live-ledger.log` (backend,
resolved binary + version, case count, exit code, wall time, surviving tabs,
process-sweep verdict, and daemon-cleanup verdict).

Known gap: there is **no working spend ceiling for a claude live agent**.
`--max-budget-usd` is documented by `claude --help` as "only works with `--print`",
and the lead path deliberately omits `-p`. Until an external watchdog exists, prefer
`LIVE_BACKEND=codex` and keep fixtures small.

### Live supervised-worker tier

`make test-aft-live-workers` (optionally `LIVE_BACKEND=claude`) runs exactly two
cases from `tests/aft/live-worker-suites/`: LW-1 creates a Task Runner through
`CreateAgentModal` and delegates a designed implementation task through the issue
assignee UI; LW-2 creates a Planner, creates a designless task, and starts the
Planner from its agent header. Both workers run under a foreground `loom daemon`
that `run-aft.sh` owns.

The different start surfaces are intentional. The assignee picker is a Task Runner
surface and explicitly excludes `plan` roles, so forcing Planner through it would
contradict the product contract. Phase 2 adds the previously missing browser
lifecycle seam: an idle background agent's header exposes **Start agent**, backed by
the same lifecycle endpoint and queued daemon command as assignment-driven starts.
Modal-created background agents are persisted as `desired_state: stopped`, so the
30-second config reconciler cannot race that explicit action. When the start command
arrives for a newly created agent, the daemon suppresses only that agent during its
synchronous config reload, then starts it once with the requested task/session scope.

The daemon now legitimately starts with zero agents. That is a product seam, not a
test bypass: the empty supervisor, FleetDB config reconciler, and queued-command
poller already supported agents added later, while the old early exit made a fresh
workspace impossible to operate and caused the local daemon supervisor to restart
until an agent happened to exist. The live suite proves the startup banner says
`Agents: 0`, then proves each UI lifecycle command reached that same daemon through
its per-agent start/stop log records.

The worker fixture uses the primary isolated E2E workspace and adds a tiny committed
Makefile plus a throwaway **local bare origin** under `$AFT_WORK_DIR`. A Task Runner
following its normal commit/stack-publish prompt therefore has a complete local
delivery path but cannot mutate a hosted repository. Only the selected backend is
real; all other AI CLIs and `gh` remain in the stub farm. Claude's noninteractive
worker sessions default to `LOOM_MAX_BUDGET_USD=5.00`; override with
`AFT_LIVE_MAX_BUDGET_USD` only deliberately.

LW-1 requires all of: task closure, byte-exact content in
`worktrees/<repo>/<agent>/`, selected-backend session identity, `files_changed >= 1`,
healthy evidence, a structured native `tool_use` entry, diff content containing the
unique marker, and the same patch rendered in the browser's Runs → Diff view. LW-2
requires a review-state design with a run-unique token, concrete architecture/file/
error/acceptance/test sections, healthy selected-backend session evidence, a
structured native tool event, the design rendered in the browser, and the session
visible in Runs. It deliberately does **not** use `files_changed == 0` as its
real-backend discriminator.

Cleanup order is strict: successful cases stop their workers through the product
lifecycle endpoint and wait for daemon confirmation; after AFT returns, the harness
stops the owned daemon and requires its clean exit marker; only then does the global
process-baseline sweep prove no selected-backend process survived. The EXIT trap
also stops the daemon on interruption before tearing down the server.

### Live terminal tier

`make test-aft-terminal` proves the agents-page Logs tab's live-tmux path:
`AgentLogsTab` detects a real auto-mode tmux session, mounts
`EmbeddedTerminal`, and renders non-empty real codex output through xterm. It
requires the normal real-codex preflight (a real `codex` on `PATH` and a logged-in
`~/.codex/auth.json`) plus `tmux` on `PATH`.

This tier is opt-in by running the target; there is no extra environment flag.
Its suites live in `tests/aft/real-terminal-suites/`, so `make test-aft-real`
does not include them. Claude remains stubbed and the spawned agent is codex-only,
so it spends zero Claude tokens. The run does auto-spawn a real agent and consumes
the operator's codex account rate-limit window, so do not loop it casually. CI
cannot run this tier because it has neither operator Codex credentials nor a
supported interactive tmux environment.

### Podman cloud tier

`make test-aft-podman` runs the real-codex scenario against `loom-serve`,
fleet-db, and Redis in separate Podman containers. It proves that Loom selects
ModeCloud and projects session artifacts across the container/Redis boundary.
Runner-filesystem isolation is architectural: Codex writes to the `loom-work`
named volume mounted at `/work`, while the host credential and frontend mounts
(`~/.codex` and the built frontend) are read-only. The suite verifies that
architecture structurally by inspecting the serve container and requiring the
`/work` mount to have type `volume`, never `bind`. It also checks that `HELLO.md`
does not appear under host `tmp/` as a cheap guard against a future host-bind
regression; that absence check is not, by itself, proof of filesystem isolation.

This manual tier requires a running Podman machine, a real `codex` CLI with a
logged-in `~/.codex/auth.json`, and the normal local AFT checkout. The first run
builds the four stack images and can take several minutes; later runs reuse them
unless `AFT_PODMAN_REBUILD=1` is set. On Apple silicon, the capped `loom-serve`
container is the isolation boundary; per-task nested-container sandboxing is not
available through the macOS Podman machine. CI cannot run this tier because it
has neither the Podman machine nor operator Codex credentials, and the real run
consumes an account rate-limit window.

aft runs suite files alphabetically. `zz-agent-flow` creates an agent definition and
run artifacts, but it now runs in its own workspace, **`E2E-WS-AGENT`**, so agent
artifacts no longer leak into the shared **`E2E-WS`** workspace used by empty-state
suites. New suites that create persistent agents should use their own workspace too.

Every run writes `tests/aft/reports/report.html` — a self-contained run browser (suite
navigation, run history + trend, step screenshots, video playback with a step timeline,
agent verdicts).

Product bugs and stack-improvement work surfaced by these runs are tracked in
[`FINDINGS.md`](FINDINGS.md).

## Coverage

Coverage is measured, not declared. Each run regenerates a **census** of the web UI's
surface straight from the frontend source (`scripts/gen-census.py`: routes from
`router.tsx`, `data-testid`s + `testId=` props, API endpoints from `wsUrl()` calls and
`/api` literals across `src` — tests, mocks, and the generated OpenAPI contract
excluded, so only UI-reachable endpoints count), and aft records a **trace**
of what each test actually touched (browser URLs, the page's own network requests, API
calls from `run:` steps, executed-step testids/selectors). The join — covered and
uncovered routes/endpoints/testids, with the tests that touched each — prints after the
run summary and renders at the top of the HTML report. The census is a **combined metric
across both tiers**: a surface test marks a route, endpoint, or testid covered exactly as a
product-correctness test does. Tier meaning lives only in the directories and comments,
not in census arithmetic. A new route or endpoint added to the app shows up as uncovered
automatically; no list to maintain.

## CI

`.github/workflows/aft.yml` runs `make test-aft` (plus `--record --screenshots --junit`)
on every PR. Reports upload as the `aft-reports` artifact and a per-test table lands in
the step summary. `workflow_dispatch` with `mode: strict` adds agent diagnoses (needs
`ANTHROPIC_API_KEY`). Required secret: `AFT_CHECKOUT_TOKEN` with read access to the
private `tysonthomas9/aft` **and** `tysonthomas9/fleet-db` repos.

## Suites

Product-correctness (`tests/aft/suites/`):

- `smoke`, `sse-resilience`, and `issue-lifecycle` — open-auth boot, live board delivery,
  reconnect catch-up, and agent-driven lifecycle transitions.
- `issue-create-ui`, `issue-detail`, `issue-detail.graph`, `comments`, and `markdown-safety`
  — human creation, the split graph pilot for complete detail-panel UI journeys,
  field/comment editing, API readbacks, activity ordering, and safe rendering.
- `dependencies-graph`, `filters`, `views`, `table-bulk`, and `pages` — dependency/graph
  behavior, URL filtering, route/view switching, bulk actions, and page contracts.
- `monitor`, `review-queue`, and `pr-workspace-degraded` — monitor/empty states, the honest
  review-queue empty scenario, and browser-observable connector degradation.
- `workspace-mgmt` and `workspaces` — clone validation, Local/Empty modal creation, repo
  management, rename, switching, and deep links.
- `settings-design-format` — the real settings toggle plus Daytona credential/runtime UI.
- `zz-agent-flow` — modal-created agents, product-seeded logs/worktrees, stub epic execution,
  completed-task session navigation, transcripts, and agent diff UI.

Surface wiring (`tests/aft/surface-suites/`):

- `workspace-order` — intentionally UI-unreachable reorder API persistence.
- `review-actions` — approve/request-changes wiring over hollow review fixtures.
- `design-format-legacy` — legacy inline-HTML auto-detect and sanitization.
- `api-contracts` — standalone health/config/readiness contract probes.
- `pr-contracts` — PR reviewer endpoint degraded-mode status and error-code contracts.

Suites that create issues declare `teardown: scripts/close-open-issues.sh`, so every
suite starts against an empty board. Cross-step state goes through `$AFT_WORK_DIR`.

## v5 anchors worth knowing

The default board is **epic-grouped swim lanes** (`swim-lane-board`); issues without an
epic land in the "Ungrouped" lane (`swim-lane-lane-epic-__ungrouped__`). Columns are
`section[data-status=backlog|ready|blocked|in_progress|review|done]` — the "Open" column
is `data-status=ready` and holds status-`open` issues; cards are `article` elements
(`aria-label^="Issue: ..."`, no testid). There is **no rendered connection indicator** in
v5 — assert SSE by observing board content. `<title>` is "Loom"; there is no h1 on the
board. Kanban drag uses dnd-kit PointerSensor with a 5px activation distance.
