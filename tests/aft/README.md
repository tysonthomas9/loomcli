# aft browser tests

Live-server E2E suites for the Loom web UI, driven by
[aft](https://github.com/tysonthomas9/aft) — deterministic YAML browser tests with a
Claude recovery agent that diagnoses (`--strict`) or heals (`--heal`) failures.

These complement the mocked Playwright suite: they run against the **real v5 stack**
(`loom serve` + embedded fleet-db + vite preview, fresh isolated workspace, auth open),
so they cover what mocks can't — above all that server-side mutations reach the UI via
SSE.

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
UI-create prefix fans out to description, priority, label, comment, lifecycle, and card
reopen journeys. Transition coverage selects six complete root-to-terminal paths; two
named golden journeys run independently, for eight fresh-browser replays total. Every
path gets a stable `AFT_CASE_ID`, suite-level cleanup, source-line provenance, and a graph
plan/evidence section in the reports. Read the package's `README.md` and manifest before
editing mechanics in a fragment.

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

### Live terminal tier

`make test-aft-terminal` proves the agents-page Logs tab's live-tmux path:
`AgentLogsTab` detects a real auto-mode tmux session, mounts
`EmbeddedTerminal`, and renders non-empty real codex output through wterm. It
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
